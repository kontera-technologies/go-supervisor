package supervisor

import (
	"io"
	"os"
	"syscall"
	"time"
)

type ProcessOptions struct {
	// If Name contains no path separators, Command uses LookPath to
	// resolve Name to a complete path if possible. Otherwise it uses Name
	// directly as Path.
	Name string

	// The returned Cmd's Args field is constructed from the command name
	// followed by the elements of arg, so arg should not include the
	// command name itself. For example, Command("echo", "hello").
	// Args[0] is always name, not the possibly resolved Path.
	Args []string

	// Env specifies the environment of the process.
	// Each entry is of the form "key=value".
	// If Env is nil, the new process uses the current process's
	// environment.
	// If Env contains duplicate environment keys, only the last
	// value in the slice for each duplicate key is used.
	Env []string

	// When InheritEnv is true, os.Environ() will be prepended to Env.
	InheritEnv bool

	// Dir specifies the working directory of the command.
	// If Dir is the empty string, Run runs the command in the
	// calling process's current directory.
	Dir string

	// ExtraFiles specifies additional open files to be inherited by the
	// new process. It does not include standard input, standard output, or
	// standard error. If non-nil, entry i becomes file descriptor 3+i.
	//
	// ExtraFiles is not supported on Windows.
	ExtraFiles []*os.File

	// SysProcAttr holds optional, operating system-specific attributes.
	// Run passes it to os.StartProcess as the os.ProcAttr's Sys field.
	SysProcAttr *syscall.SysProcAttr

	In  chan []byte
	Out chan *interface{}
	Err chan *interface{}

	EventNotifier chan Event

	Id string

	// Debug - when this flag is set to true, events will be logged to the default go logger.
	Debug bool

	OutputParser func(fromR io.Reader, bufferSize int) ProduceFn
	ErrorParser func(fromR io.Reader, bufferSize int) ProduceFn

	// MaxSpawns is the maximum number of times that a process can be spawned
	// Set to -1, for an unlimited amount of times.
	// Will use defaultMaxSpawns when set to 0.
	MaxSpawns int

	// MaxSpawnAttempts is the maximum number of spawns attempts for a process.
	// Set to -1, for an unlimited amount of attempts.
	// Will use defaultMaxSpawnAttempts when set to 0.
	MaxSpawnAttempts int

	// MaxSpawnBackOff is the maximum duration that we will wait between spawn attempts.
	// Will use defaultMaxSpawnBackOff when set to 0.
	MaxSpawnBackOff time.Duration

	// MaxRespawnBackOff is the maximum duration that we will wait between respawn attempts.
	// Will use defaultMaxRespawnBackOff when set to 0.
	MaxRespawnBackOff time.Duration

	// MaxInterruptAttempts is the maximum number of times that we will try to interrupt the process when closed, before
	// terminating and/or killing it.
	// Set to -1, to never send the interrupt signal.
	// Will use defaultMaxInterruptAttempts when set to 0.
	MaxInterruptAttempts int

	// MaxTerminateAttempts is the maximum number of times that we will try to terminate the process when closed, before
	// killing it.
	// Set to -1, to never send the terminate signal.
	// Will use defaultMaxTerminateAttempts when set to 0.
	MaxTerminateAttempts int

	// NotifyEventTimeout is the amount of time that the process will BLOCK while trying to send an event.
	NotifyEventTimeout time.Duration

	// ParserBufferSize is the size of the buffer to be used by the OutputParser and ErrorParser.
	// Will use defaultParserBufferSize when set to 0.
	ParserBufferSize int

	// IdleTimeout is the duration that the process can remain idle (no output) before we terminate the process.
	// Set to -1, for an unlimited idle timeout (not recommended)
	// Will use defaultIdleTimeout when set to 0.
	IdleTimeout  time.Duration

	// TerminationGraceTimeout is the duration of time that the supervisor will wait after sending interrupt/terminate
	// signals, before checking if the process is still alive.
	// Will use defaultTerminationGraceTimeout when set to 0.
	TerminationGraceTimeout time.Duration

	// EventTimeFormat is the time format used when events are marshaled to string.
	// Will use defaultEventTimeFormat when set to "".
	EventTimeFormat string
}

// init initializes the opts structure with default and required options.
func initProcessOptions(opts ProcessOptions) *ProcessOptions {
	if opts.SysProcAttr == nil {
		opts.SysProcAttr = &syscall.SysProcAttr{}
	} else {
		opts.SysProcAttr = deepCloneSysProcAttr(*opts.SysProcAttr)
	}

	// Start a new process group for the spawned process.
	opts.SysProcAttr.Setpgid = true

	if opts.InheritEnv {
		opts.Env = append(os.Environ(), opts.Env...)
	}

	if opts.MaxSpawns == 0 {
		opts.MaxSpawns = defaultMaxSpawns
	}
	if opts.MaxSpawnAttempts == 0 {
		opts.MaxSpawnAttempts = defaultMaxSpawnAttempts
	}
	if opts.MaxSpawnBackOff == 0 {
		opts.MaxSpawnBackOff = defaultMaxSpawnBackOff
	}
	if opts.MaxRespawnBackOff == 0 {
		opts.MaxRespawnBackOff = defaultMaxRespawnBackOff
	}
	if opts.MaxInterruptAttempts == 0 {
		opts.MaxInterruptAttempts = defaultMaxInterruptAttempts
	}
	if opts.MaxTerminateAttempts == 0 {
		opts.MaxTerminateAttempts = defaultMaxTerminateAttempts
	}
	if opts.NotifyEventTimeout == 0 {
		opts.NotifyEventTimeout = defaultNotifyEventTimeout
	}
	if opts.ParserBufferSize == 0 {
		opts.ParserBufferSize = defaultParserBufferSize
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = defaultIdleTimeout
	}
	if opts.TerminationGraceTimeout == 0 {
		opts.TerminationGraceTimeout = defaultTerminationGraceTimeout
	}
	if opts.EventTimeFormat == "" {
		opts.EventTimeFormat = defaultEventTimeFormat
	}
	if opts.In == nil {
		opts.In = make(chan []byte)
	}
	if opts.Out == nil {
		opts.Out = make(chan *interface{})
	}
	if opts.Err == nil {
		opts.Err = make(chan *interface{})
	}

	return &opts
}

// deepCloneSysProcAttr is a helper function that deep-copies the syscall.SysProcAttr struct and returns a reference to the
// new struct.
func deepCloneSysProcAttr(x syscall.SysProcAttr) *syscall.SysProcAttr {
	if x.Credential != nil {
		y := *x.Credential
		x.Credential = &y
	}
	return &x
}
