package supervisor

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type (
	Event struct {
		Code    int
		Message string
		Time    time.Time
	}

	Process struct {
		// communication
		Stdout chan *[]byte
		Stderr chan *[]byte
		Stdin  chan *[]byte

		// internal usage
		closeHandlers func() bool
		isdone        int32
		stopping      int32
		killed        int32
		stopped       int32

		command string
		options *Options

		// safe variables
		mu               sync.Mutex
		cmd              *exec.Cmd
		pid              int
		needToNotifyDone bool
		needToSendEvents bool
		doneChannel      chan bool
		eventsChannel    chan *Event
		lastError        error
	}

	Options struct {
		Args                    []string // argumets to pass
		SpawnAttempts           int      // attempts before giving up
		AttemptsBeforeTerminate int      // on Stop() terminate process after X interrupt attempts
		Debug                   bool     // print events to stdout
		Dir                     string   // run dir
		Id                      string   // will be added to every log print
		MaxSpawns               int      // Max spawn limit
		StdoutIdleTime          int      // stop worker if we didn't recived stdout message in X seconds
		StderrIdleTime          int      // stop worker if we didn't recived stderr message in X seconds
		Env                     []string // see os.Cmd Env attribute
		InheritEnv              bool     // take parent process environment variables

		DelayBetweenSpawns func(currentSleep int) (sleep int) // in seconds
	}
)

// public

func Supervise(command string, opt ...Options) (p *Process, err error) {
	options := &Options{}
	if len(opt) > 0 {
		options = &opt[0]
	}

	if options.Args == nil {
		options.Args = make([]string, 0)
	}

	if options.AttemptsBeforeTerminate == 0 {
		options.AttemptsBeforeTerminate = 10
	}

	if options.DelayBetweenSpawns == nil {
		options.DelayBetweenSpawns = func(currentSleep int) (sleepTime int) {
			if currentSleep > 500 {
				sleepTime = 1
			} else {
				sleepTime = currentSleep * 2
			}
			return sleepTime
		}
	}

	if options.Id == "" {
		options.Id = "ID"
	}

	if options.SpawnAttempts == 0 {
		options.SpawnAttempts = 10
	}

	if options.MaxSpawns == 0 {
		options.MaxSpawns = 1
	}

	p = &Process{
		command: command,
		options: options,
		Stdout:  make(chan *[]byte),
		Stderr:  make(chan *[]byte),
		Stdin:   make(chan *[]byte),
	}

	if err := p.start(); err != nil {
		return p, err
	}
	go p.watch()
	return p, nil
}

func (p *Process) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastError
}

func (p *Process) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

func (p *Process) NotifyEvents(c chan *Event) (channel chan *Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.needToSendEvents = true
	p.eventsChannel = c
	return c
}

func (p *Process) NotifyDone(c chan bool) (channel chan bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.needToNotifyDone = true
	p.doneChannel = c
	return c
}

func (p *Process) Running() bool {
	if p.cmd == nil {
		return false
	} else if p.isKilled() {
		return false
	} else if p.cmd.ProcessState != nil {
		return !p.cmd.ProcessState.Exited()
	} else {
		return true
	}
}

func (p *Process) Stop() {
	if p.isDone(true) {
		p.isStopping(true)
		defer p.isStopping(false)
		done := make(chan bool)
		p.stop()

		go func() {
			if p.needToNotifyDone {
				p.doneChannel <- true
			}
			<-time.After(time.Second)
			p.closeChannels()
			done <- true
		}()

		<-done
	}
}

func (p *Process) IsDone() bool {
	return p.isDone() && !p.isStopping()
}

// private
func (p *Process) closeChannels() {
	//close(p.Stdin)
	close(p.Stderr)
	close(p.Stdout)
	if p.needToSendEvents {
		close(p.eventsChannel)
	}
	if p.needToNotifyDone {
		close(p.doneChannel)
	}
}

func (p *Process) start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isDone() {
		return nil
	}

	var err error

	p.cmd = exec.Command(p.command, p.options.Args...)
	env := make([]string, 0)

	if p.options.InheritEnv {
		env = os.Environ()
	}

	if p.options.Env != nil {
		p.cmd.Env = append(env, p.options.Env...)
	}

	if p.options.Dir != "" {
		p.cmd.Dir = p.options.Dir
	}

	stdout, stderr, stdin, err := p.openPipes()
	if err != nil {
		return err
	}

	p.isStopped(false)
	p.isKilled(false)

	closeIn := p.handleIn(stdin, p.Stdin)
	closeOut := p.handleOut("stdout", stdout, p.Stdout, p.options.StdoutIdleTime)
	closeErr := p.handleOut("stderr", stderr, p.Stderr, p.options.StderrIdleTime)

	p.closeHandlers = func() bool {
		for k, v := range map[string]chan bool{
			"stdin":  closeIn,
			"stdout": closeOut,
			"stderr": closeErr,
		} {
			p.event(5, "closing %s handler...", k)
			select {
			case v <- true:
				<-v
			case <-time.After(time.Second):
				p.event(6, "%s is still open... memory leak...", k)
			}
		}
		return false
	}

	p.event(8, "starting instance...")
	err = p.cmd.Start()
	if err != nil {
		return err
	}

	p.pid = p.cmd.Process.Pid

	p.event(22, "instance ready...")

	return nil
}

// run in its own goroutine
func (p *Process) watch() {
	attempt := 1
	currentSleep := 1
	numSpawns := 1
	for {
		start := time.Now()
		p.lastError = p.cmd.Wait()
		time.Sleep(time.Second)
		if p.isDone() {
			break
		}

		if p.lastError == nil {
			p.event(12, "instance exited with exit code 0")
		} else {
			p.event(7, "instance crashed: %q", p.lastError.Error())
		}

		if numSpawns >= p.options.MaxSpawns {
			p.event(13, "reached max spawns...")
			p.Stop() // cleanup
			break
		} else {
			numSpawns += 1
		}

		if (time.Now().Sub(start).Seconds()) > 60 {
			attempt = 1
			currentSleep = 1
		} else {
			attempt += 1
			currentSleep = p.options.DelayBetweenSpawns(currentSleep)
		}
		if attempt > p.options.SpawnAttempts {
			p.event(9, "giving up, instance failed to start...")
			p.Stop() // shutting down instance and send done notification...
			break
		}

		p.event(10, "going to sleep for %d seconds...", currentSleep)
		p.stop() // cleanup
		p.event(29, "entering sleep stage...")

		milliseconds := currentSleep * 1000
		waited := 0
		for waited < milliseconds {
			time.Sleep(10 * time.Millisecond)
			waited += 10
			if p.isDone() {
				break
			}
		}

		p.start()
	}
	p.event(11, "watch daemon is off...")
}

func (p *Process) Restart() {
	p.stop()
}

func (p *Process) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isStopped() {
		return
	}

	defer p.isStopped(true)

	p.event(20, "going to kill process..")

	attempts := 0

	for p.Running() && p.cmd != nil && p.cmd.Process != nil {
		attempts++
		if attempts < p.options.AttemptsBeforeTerminate {
			p.event(3, "sending interrupt to process - attempt %d", attempts)
			p.cmd.Process.Signal(os.Interrupt)
			time.Sleep(time.Second)
		} else {
			p.event(4, "refuse to quit, kill it (pid %d)...", p.cmd.Process.Pid)
			p.cmd.Process.Kill()
			p.cmd.Process.Signal(os.Kill)
			p.isKilled(true)
			time.Sleep(time.Second)
			break
		}
	}

	p.event(98, "closing handlers...")
	p.closeHandlers()
}

// runs in its own goroutine
func (p *Process) handleIn(in io.WriteCloser, channel chan *[]byte) chan bool {
	p.event(0, "opening stdin handler...")
	c := make(chan bool)

	go func() {
		defer p.event(0, "stdin handler is now closed...")
		for {
			select {
			case message := <-channel:
				if _, err := in.Write(append(*message, '\n')); err != nil {
					select {
					case <-c:
						c <- true
						return
					}
				}
			case <-c:
				c <- true
				return
			}
		}
	}()

	return c
}

func (p *Process) getHeartbeater(name string, seconds int) chan bool {
	c := make(chan bool, 1000)

	go func() {
		for {
			t := time.NewTimer(time.Second * time.Duration(seconds))

			select {
			case msg := <-c:
				if !msg {
					return
				}
			case <-t.C:
				p.event(15, "%s - reached timeout, restarting instance...", name)
				p.stop()
				return
			}

			t.Stop()
		}
	}()

	return c
}

// runs in its own goroutine
func (p *Process) handleOut(name string, out *bufio.Reader, channel chan *[]byte, heartbeat int) chan bool {
	p.event(0, "opening %v handler...", name)

	c := make(chan bool)

	go func() {
		defer p.event(0, "%v handler is now closed...", name)
		var heartbeatChannel chan bool
		shouldHeartbeat := heartbeat > 0

		if shouldHeartbeat {
			heartbeatChannel = p.getHeartbeater(name, heartbeat)
		}
		beat := func(k bool) {
			if shouldHeartbeat {
				heartbeatChannel <- k
			}
		}

		defer func() {
			err := recover()

			if p != nil {
				defer beat(false)
				if err != nil {
					p.event(90, "%s handler: %s , recovering...", name, err)
					if !p.isDone() {
						select {
						case <-c:
							c <- true
							return
						}
					}
				}
			}
		}()

		for {
			select {
			case <-c:
				c <- true
				return
			default:
				line, err := out.ReadBytes('\n')
				beat(true)

				if err != nil {
					p.event(1, "can't read from %s: %s", name, err)
					select {
					case <-c:
						c <- true
						return
					}
				}

				select {
				case channel <- &line:
				case <-c:
					c <- true
					return
				}
			}
		}

	}()

	return c
}

func (p *Process) event(code int, message string, format ...interface{}) {
	msg := &Event{
		Message: fmt.Sprintf(("[%s] " + message), append([]interface{}{p.options.Id}, format...)...),
		Time:    time.Now(),
		Code:    code,
	}

	if p.options.Debug {
		log.Printf("%s", msg.Message)
	}

	if p.needToSendEvents && !p.isDone() {
		p.eventsChannel <- msg
	}
}

func (p *Process) openPipes() (stdout, stderr *bufio.Reader, stdin io.WriteCloser, err error) {
	stdin, err = p.cmd.StdinPipe()

	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stdin pipe: %s", err)
	}

	out, err := p.cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stdout pipe: %s", err)
	}
	stdout = bufio.NewReader(out)

	er, err := p.cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stderr pipe: %s", err)
	}
	stderr = bufio.NewReader(er)

	return stdout, stderr, stdin, nil
}

func (p *Process) isKilled(killed ...bool) bool {
	return isSomething(&p.killed, killed)
}

func (p *Process) isDone(done ...bool) bool {
	return isSomething(&p.isdone, done)
}

func (p *Process) isStopped(stop ...bool) bool {
	return isSomething(&p.stopped, stop)
}

func (p *Process) isStopping(stopping ...bool) bool {
	return isSomething(&p.stopping, stopping)
}

func isSomething(n *int32, o []bool) bool {
	if len(o) > 0 {
		if o[0] {
			return atomic.CompareAndSwapInt32(n, 0, 1)
		} else {
			return atomic.CompareAndSwapInt32(n, 1, 0)
		}
	} else {
		return atomic.LoadInt32(n) == 1
	}

}
