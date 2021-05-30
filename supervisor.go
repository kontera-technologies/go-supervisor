package supervisor

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultMaxSpawns               = 1
	defaultMaxSpawnAttempts        = 10
	defaultMaxSpawnBackOff         = 2*time.Minute
	defaultMaxRespawnBackOff       = 2*time.Minute
	defaultMaxInterruptAttempts    = 5
	defaultMaxTerminateAttempts    = 5
	defaultNotifyEventTimeout      = time.Millisecond
	defaultParserBufferSize        = 4096
	defaultIdleTimeout             = 10 * time.Second
	defaultTerminationGraceTimeout = time.Second
	defaultEventTimeFormat = time.RFC3339Nano
)

var EnsureClosedTimeout = time.Second

type Event struct {
	Id      string
	Code    string
	Message string
	Time    time.Time
	TimeFormat string
}

func (ev Event) String() string {
	if len(ev.Message) == 0 {
		return fmt.Sprintf("[%30s][%s] %s", ev.Time.Format(ev.TimeFormat), ev.Id, ev.Code)
	}
	return fmt.Sprintf("[%s][%30s] %s - %s", ev.Time.Format(ev.TimeFormat), ev.Id, ev.Code, ev.Message)
}

const (
	ready uint32 = 1 << iota
	running
	respawning
	stopped
	errored
)

func phaseString(s uint32) string {
	str := "unknown"
	switch s {
	case ready:
		str = "ready"
	case running:
		str = "running"
	case respawning:
		str = "respawning"
	case stopped:
		str = "stopped"
	case errored:
		str = "errored"
	}
	return fmt.Sprintf("%s(%d)", str, s)
}

type ProduceFn func() (*interface{}, error)

type Process struct {
	cmd             *exec.Cmd
	pid             int64
	spawnCount      int64
	stopC           chan bool
	ensureAllClosed func()

	phase   uint32
	phaseMu sync.Mutex

	lastError        atomic.Value
	lastProcessState atomic.Value

	opts *ProcessOptions

	eventTimer      *time.Timer
	eventNotifierMu sync.Mutex

	doneNotifier chan bool
	rand         *rand.Rand
	stopSleep    chan bool
}

func (p *Process) Input() chan<- []byte {
	return p.opts.In
}

// EmptyInput empties all messages from the Input channel.
func (p *Process) EmptyInput() {
	for {
		select {
		case _, ok := <-p.opts.In:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func (p *Process) Stdout() <-chan *interface{} {
	return p.opts.Out
}

func (p *Process) Stderr() <-chan *interface{} {
	return p.opts.Err
}

func (p *Process) LastProcessState() *os.ProcessState {
	v := p.lastProcessState.Load()
	if v == nil {
		return nil
	}
	return v.(*os.ProcessState)
}

func (p *Process) LastError() error {
	v := p.lastError.Load()
	if v == nil {
		return nil
	}
	if x, ok := v.(error); ok {
		return x
	}
	return nil
}

func (p *Process) Pid() int {
	return int(atomic.LoadInt64(&p.pid))
}

func (p *Process) Start() (err error) {
	p.phaseMu.Lock()
	defer p.phaseMu.Unlock()
	if p.phase != ready && p.phase != respawning {
		return fmt.Errorf(`process phase is "%s" and not "ready" or "respawning"`, phaseString(p.phase))
	}

	for attempt := 0; p.opts.MaxSpawnAttempts == -1 || attempt < p.opts.MaxSpawnAttempts; attempt++ {
		err = p.unprotectedStart()
		if err == nil {
			p.phase = running
			return
		}
		if !p.sleep(p.CalcBackOff(attempt, time.Second, p.opts.MaxSpawnBackOff)) {
			break
		}
	}

	p.phase = errored
	p.notifyDone()
	return
}

func (p *Process) unprotectedStart() error {
	p.cmd = newCommand(p.opts)

	inPipe, err := p.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to fetch stdin pipe: %s", err)
	}

	outPipe, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to fetch stdout pipe: %s", err)
	}

	errPipe, err := p.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to fetch stderr pipe: %s", err)
	}

	if p.opts.OutputParser == nil {
		return errors.New("missing output streamer")
	}

	if p.opts.ErrorParser == nil {
		return errors.New("missing error streamer")
	}

	if err = p.cmd.Start(); err != nil {
		return err
	}

	atomic.AddInt64(&p.spawnCount, 1)
	atomic.StoreInt64(&p.pid, int64(p.cmd.Process.Pid))

	p.stopC = make(chan bool)
	heartbeat, isMonitorClosed, isInClosed, isOutClosed, isErrClosed := make(chan bool), make(chan bool), make(chan bool), make(chan bool), make(chan bool)

	go chanToWriter(p.opts.In, inPipe, p.notifyEvent, isInClosed, p.stopC, heartbeat)
	go readerToChan(p.opts.OutputParser(outPipe, p.opts.ParserBufferSize), p.opts.Out, isOutClosed, p.stopC, heartbeat)
	go readerToChan(p.opts.ErrorParser(errPipe, p.opts.ParserBufferSize), p.opts.Err, isErrClosed, p.stopC, nil)

	go monitorHeartBeat(p.opts.IdleTimeout, heartbeat, isMonitorClosed, p.stopC, p.Stop, p.notifyEvent)

	var ensureOnce sync.Once
	p.ensureAllClosed = func() {
		ensureOnce.Do(func() {
			select {
			case <-p.stopC:
			default:
				log.Printf("[%s] ensureAllClosed was called before stopC channel was closed.", p.opts.Id)
			}
			if p.opts.Debug { log.Printf("[%s] Starting to ensure all pipes have closed.", p.opts.Id) }
			if cErr := ensureClosed("stdin", isInClosed, inPipe.Close); cErr != nil {
				log.Printf("[%s] Possible memory leak, stdin go-routine not closed. Error: %s", p.opts.Id, cErr)
			}
			if cErr := ensureClosed("stdout", isOutClosed, outPipe.Close); cErr != nil {
				log.Printf("[%s] Possible memory leak, stdout go-routine not closed. Error: %s", p.opts.Id, cErr)
			}
			if cErr := ensureClosed("stderr", isErrClosed, errPipe.Close); cErr != nil {
				log.Printf("[%s] Possible memory leak, stderr go-routine not closed. Error: %s", p.opts.Id, cErr)
			}
			if cErr := ensureClosed("heartbeat monitor", isMonitorClosed, nil); cErr != nil {
				log.Printf("[%s] Possible memory leak, monitoring go-routine not closed. Error: %s", p.opts.Id, cErr)
			}
		})
	}

	go p.waitAndNotify()

	p.notifyEvent("ProcessStart", fmt.Sprintf("pid: %d", p.Pid()))
	return nil
}

func chanToWriter(in <-chan []byte, out io.Writer, notifyEvent func(string, ...interface{}), closeWhenDone, stopC, heartbeat chan bool) {
	defer close(closeWhenDone)
	for {
		select {
		case <-stopC:
			return
		case raw, chanOpen := <-in:
			if !chanOpen {
				notifyEvent("Error", "Input channel closed unexpectedly.")
				return
			}

			_, err := out.Write(raw)
			if err != nil {
				notifyEvent("WriteError", err.Error())
				return
			}
			heartbeat <- true
		}
	}
}

func readerToChan(producer ProduceFn, out chan<- *interface{}, closeWhenDone, stopC, heartbeat chan bool) {
	defer close(closeWhenDone)

	cleanPipe := func() {
		for {
			if res, err := producer(); res != nil {
				out <- res
			} else if err != nil {
				return
			}
		}
	}

	for {
		if res, err := producer(); res != nil {
			select {
			case out <- res:
				select {
				case heartbeat <- true:
				default:
				}
			case <-stopC:
				cleanPipe()
				return
			}
		} else if err != nil {
			return
		}

		select {
		case <-stopC:
			cleanPipe()
			return
		default:
		}
	}
}

// monitorHeartBeat monitors the heartbeat channel and stops the process if idleTimeout time is passed without a
// positive heartbeat, or if a negative heartbeat is passed.
//
// isMonitorClosed will be closed when this function exists.
//
// When stopC closes, this function will exit immediately.
func monitorHeartBeat(idleTimeout time.Duration, heartbeat, isMonitorClosed, stopC chan bool, stop func() error, notifyEvent func(string, ...interface{})) {
	defer close(isMonitorClosed)
	t := time.NewTimer(idleTimeout)
	defer t.Stop()

	for alive := true; alive; {
		select {
		case <-stopC:
			notifyEvent("StoppingHeartbeatMonitoring", "Stop signal received.")
			return

		case alive = <-heartbeat:
			if alive {
				if !t.Stop() {
					<-t.C
				}
				t.Reset(idleTimeout)
			} else {
				notifyEvent("NegativeHeartbeat", "Stopping process.")
			}

		case <-t.C:
			alive = false
			notifyEvent("MissingHeartbeat", "Stopping process.")
		}
	}

	if err := stop(); err != nil {
		notifyEvent("StopError", err.Error())
	}
}

func (p *Process) waitAndNotify() {
	state, waitErr := p.cmd.Process.Wait()

	p.phaseMu.Lock()
	automaticUnlock := true
	defer func() {
		if automaticUnlock {
			p.phaseMu.Unlock()
		}
	}()

	p.lastProcessState.Store(state)

	if p.phase == stopped {
		return
	} else if p.phase != running && p.phase != respawning {
		p.notifyEvent("RespawnError", fmt.Sprintf(`process phase is "%s" and not "running" or "respawning"`, phaseString(p.phase)))
	}

	p.phase = stopped

	if waitErr != nil {
		p.notifyEvent("WaitError", fmt.Sprintf("os.Process.Wait returned an error - %s", waitErr.Error()))
		p.phase = errored
		return
	}

	if state.Success() {
		p.notifyEvent("ProcessDone", state.String())
	} else {
		p.notifyEvent("ProcessCrashed", state.String())
		p.lastError.Store(errors.New(state.String()))
	}

	// Cleanup resources
	select {
	case <-p.stopC:
	default:
		close(p.stopC)
	}
	p.ensureAllClosed()

	if !p.canRespawn() {
		p.notifyEvent("RespawnError", "Max number of respawns reached.")
		p.notifyDone()
		return
	}

	sleepFor := p.CalcBackOff(int(atomic.LoadInt64(&p.spawnCount))-1, time.Second, p.opts.MaxRespawnBackOff)
	p.notifyEvent("Sleep", fmt.Sprintf("Sleeping for %s before respwaning instance.", sleepFor.String()))
	if !p.sleep(sleepFor) {
		return
	}

	p.phase = respawning
	p.notifyEvent("ProcessRespawn", "Trying to respawn instance.")

	automaticUnlock = false
	p.phaseMu.Unlock()
	err := p.Start()

	if err != nil {
		p.notifyEvent("RespawnError", err.Error())
	}
}

func (p *Process) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	select {
	case <-t.C:
		return true
	case <-p.stopSleep:
		t.Stop()
		return false
	}
}

func (p *Process) canRespawn() bool {
	return p.opts.MaxSpawns == -1 || atomic.LoadInt64(&p.spawnCount) < int64(p.opts.MaxSpawns)
}

// Stop tries to stop the process.
// Entering this function will change the phase from "running" to "stopping" (any other initial phase will cause an error
// to be returned).
//
// This function will call notifyDone when it is done.
//
// If it fails to stop the process, the phase will change to errored and an error will be returned.
// Otherwise, the phase changes to stopped.
func (p *Process) Stop() error {
	select {
	case <-p.stopSleep:
	default:
		close(p.stopSleep)
	}
	p.phaseMu.Lock()
	defer p.phaseMu.Unlock()
	defer p.notifyDone()
	err := p.unprotectedStop()
	if err != nil {
		p.phase = errored
		return err
	}
	p.phase = stopped
	return nil
}

func (p *Process) unprotectedStop() (err error) {
	p.notifyEvent("ProcessStop")

	select {
	case <-p.stopC:
	default:
		close(p.stopC)
	}
	defer p.ensureAllClosed()

	if !p.IsAlive() {
		return nil
	}

	attempt := 0
	for ; attempt < p.opts.MaxInterruptAttempts; attempt++ {
		p.notifyEvent("Interrupt", fmt.Sprintf("sending intterupt signal to %d - attempt #%d", -p.Pid(), attempt+1))
		err = p.interrupt()
		if err == nil {
			return nil
		}
	}
	if p.opts.MaxInterruptAttempts > 0 {
		p.notifyEvent("InterruptError", fmt.Sprintf("interrupt signal failed - %d attempts", attempt))
	}

	err = nil
	for attempt = 0; attempt < p.opts.MaxTerminateAttempts; attempt++ {
		p.notifyEvent("Terminate", fmt.Sprintf("sending terminate signal to %d - attempt #%d", -p.Pid(), attempt+1))
		err = p.terminate()
		if err == nil {
			return nil
		}
	}
	if p.opts.MaxTerminateAttempts > 0 {
		p.notifyEvent("TerminateError", fmt.Sprintf("terminate signal failed - %d attempts", attempt))
	}

	p.notifyEvent("Killing", fmt.Sprintf("sending kill signal to %d", p.Pid()))
	err = syscall.Kill(-p.Pid(), syscall.SIGKILL)

	if err != nil {
		p.notifyEvent("KillError", err.Error())
		return err
	}

	return nil
}

// Restart tries to stop and start the process.
// Entering this function will change the phase from running to respawning (any other initial phase will cause an error
// to be returned).
//
// If it fails to stop the process the phase will change to errored and notifyDone will be called.
// If there are no more allowed respawns the phase will change to stopped and notifyDone will be called.
//
// This function calls Process.Start to start the process which will change the phase to "running" (or "errored" if it
// fails)
// If Start fails, notifyDone will be called.
func (p *Process) Restart() error {
	p.phaseMu.Lock()
	defer p.phaseMu.Unlock()
	if p.phase != running {
		return fmt.Errorf(`process phase is "%s" and not "running"`, phaseString(p.phase))
	}
	p.phase = respawning
	err := p.unprotectedStop()

	if err != nil {
		p.phase = errored
		p.notifyDone()
		return err
	}

	if !p.canRespawn() {
		p.phase = stopped
		p.notifyDone()
		return errors.New("max number of respawns reached")
	}

	return nil
}

func (p *Process) IsAlive() bool {
	err := syscall.Kill(-p.Pid(), syscall.Signal(0))
	if errno, ok := err.(syscall.Errno); ok {
		return errno != syscall.ESRCH
	}
	return true
}

func (p *Process) IsDone() bool {
	select {
	case <-p.doneNotifier:
		return true
	default:
		return false
	}
}

func (p *Process) DoneNotifier() <-chan bool {
	return p.doneNotifier
}

// notifyDone closes the DoneNotifier channel (if it isn't already closed).
func (p *Process) notifyDone() {
	select {
	case <-p.doneNotifier:
	default:
		close(p.doneNotifier)
	}
}

// EventNotifier returns the eventNotifier channel (and creates one if none exists).
//
// It is protected by Process.eventNotifierMu.
func (p *Process) EventNotifier() chan Event {
	p.eventNotifierMu.Lock()
	defer p.eventNotifierMu.Unlock()

	if p.opts.EventNotifier == nil {
		p.opts.EventNotifier = make(chan Event)
	}

	return p.opts.EventNotifier
}

// notifyEvent creates and passes an event struct from an event code string and an optional event message.
// fmt.Sprint will be called on the message slice.
//
// It is protected by Process.eventNotifierMu.
func (p *Process) notifyEvent(code string, message ...interface{}) {
	// Create the event before calling Lock.
	ev := Event{
		Id:      p.opts.Id,
		Code:    code,
		Message: fmt.Sprint(message...),
		Time:    time.Now(),
		TimeFormat: p.opts.EventTimeFormat,
	}

	// Log the event before calling Lock.
	if p.opts.Debug {
		fmt.Println(ev)
	}

	p.eventNotifierMu.Lock()
	defer p.eventNotifierMu.Unlock()

	if notifier := p.opts.EventNotifier; notifier != nil {
		if p.eventTimer == nil {
			p.eventTimer = time.NewTimer(p.opts.NotifyEventTimeout)
		} else {
			p.eventTimer.Reset(p.opts.NotifyEventTimeout)
		}

		select {
		case notifier <- ev:
			if !p.eventTimer.Stop() {
				<-p.eventTimer.C
			}
		case <-p.eventTimer.C:
			log.Printf("Failed to sent %#v. EventNotifier is set, but isn't accepting any events.", ev)
		}
	}
}

func (p *Process) interrupt() (err error) {
	err = syscall.Kill(-p.Pid(), syscall.SIGINT)
	if err != nil {
		return
	}

	time.Sleep(p.opts.TerminationGraceTimeout) // Sleep for a second to allow the process to end.
	if p.IsAlive() {
		err = errors.New("interrupt signal failed")
	}
	return
}

func (p *Process) terminate() (err error) {
	err = syscall.Kill(-p.Pid(), syscall.SIGTERM)
	if err != nil {
		return
	}

	time.Sleep(p.opts.TerminationGraceTimeout) // Sleep for a second to allow the process to end.
	if p.IsAlive() {
		err = errors.New("terminate signal failed")
	}
	return
}

func (p *Process) CalcBackOff(attempt int, step time.Duration, maxBackOff time.Duration) time.Duration {
	randBuffer := (step / 1000) * time.Duration(p.rand.Intn(1000))
	backOff := randBuffer + step*time.Duration(math.Exp2(float64(attempt)))
	if backOff > maxBackOff {
		return maxBackOff
	}
	return backOff
}

func NewProcess(opts ProcessOptions) *Process {
	return &Process{
		phase:        ready,
		opts:         initProcessOptions(opts),
		doneNotifier: make(chan bool),
		stopSleep:    make(chan bool),
		rand:         rand.New(rand.NewSource(time.Now().UTC().UnixNano())),
	}
}

// newCommand creates a new exec.Cmd struct.
func newCommand(opts *ProcessOptions) *exec.Cmd {
	cmd := exec.Command(opts.Name, opts.Args...)
	cmd.Env = opts.Env
	cmd.Dir = opts.Dir
	cmd.ExtraFiles = opts.ExtraFiles
	cmd.SysProcAttr = opts.SysProcAttr
	return cmd
}

// todo: test if panics on double-close
func ensureClosed(name string, isStopped chan bool, forceClose func() error) error {
	t := time.NewTimer(EnsureClosedTimeout)
	defer t.Stop()

	select {
	case <-isStopped:
		return nil
	case <-t.C:
		if forceClose == nil {
			return fmt.Errorf("stopped waiting for %s after %s", name, EnsureClosedTimeout)
		}
		if err := forceClose(); err != nil {
			return fmt.Errorf("%s - %s", name, err.Error())
		}

		return ensureClosed(name, isStopped, nil)
	}
}
