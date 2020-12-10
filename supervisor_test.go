package supervisor_test

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fortytw2/leaktest"

	su "github.com/kontera-technologies/go-supervisor/v2"
)

func TestMain(m *testing.M) {
	su.EnsureClosedTimeout = time.Millisecond * 10
	os.Exit(m.Run())
}

func safeStop(t *time.Timer) {
	if !t.Stop() {
		<-t.C
	}
}

type testCommon interface {
	Helper()
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

func runFor(t *testing.T, from, to int, f func(t *testing.T, i int)) {
	t.Helper()
	for i := from; i < to; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Helper()
			f(t, i)
		})
	}
}

func fatalIfErr(t testCommon, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertExpectedEqualsActual(t *testing.T, expected, actual interface{}) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("\n\tExpected: %q\n\tActual:   %q", expected, actual)
	}
}

func testDir(t testCommon) string {
	testDir, err := filepath.Abs("testdata")
	fatalIfErr(t, err)
	return testDir
}

func funcName() string {
	pc, _, _, ok := runtime.Caller(1)
	if !ok {
		return "?"
	}

	fn := runtime.FuncForPC(pc)
	return strings.TrimPrefix(fn.Name(), "github.com/kontera-technologies/go-supervisor/v2_test.")
}

// logProcessEvents is a helper function that registers an event notifier that
// will pass all events to the logger.
func logProcessEvents(t testCommon, p *su.Process) (teardown func()) {
	t.Helper()
	closeC := make(chan interface{})
	notifier := p.EventNotifier()
	go func() {
		for stop := false; !stop; {
			select {
			case x := <-notifier:
				log.Printf("%+v", x)
				// t.Logf("%+v", x)
			case <-closeC:
				stop = true
			}
		}
	}()
	return func() {
		close(closeC)
	}
}

func makeErrorParser(fromR io.Reader, parserSize int) su.ProduceFn {
	p := su.MakeLineParser(fromR, parserSize)
	return func() (*interface{}, error) {
		raw, err := p()
		if raw != nil {
			var res interface{}
			res = errors.New((*raw).(string))
			return &res, nil
		}
		return nil, err
	}
}

// ensureProcessKilled logs a fatal error if the process isn't dead, and kills the process.
func ensureProcessKilled(t testCommon, pid int) {
	t.Helper()
	signalErr := syscall.Kill(pid, syscall.Signal(0))
	if signalErr != syscall.Errno(3) {
		t.Errorf("child process (%d) is still running, killing it.", pid)
		fatalIfErr(t, syscall.Kill(pid, syscall.SIGKILL))
	}
}

func TestJsonParser(t *testing.T) {
	p := su.NewProcess(su.ProcessOptions{
		Id:           funcName(),
		Name:         "./endless_jsons.sh",
		Dir:          testDir(t),
		OutputParser: su.MakeJsonLineParser,
		ErrorParser:  makeErrorParser,
		MaxSpawns:    1,
		Out:          make(chan *interface{}, 5),
	})

	expected := map[string]interface{}{
		"foo": "bar",
		"quo": []interface{}{"quz", float64(1), false},
	}

	fatalIfErr(t, p.Start())
	defer p.Stop()

	time.AfterFunc(time.Millisecond * 30, func() {
		fatalIfErr(t, p.Stop())
	})

	runFor(t, 0, 3, func(t *testing.T, i int) {
		select {
		case v := <-p.Stdout():
			assertExpectedEqualsActual(t, expected, *v)
		case <-time.After(time.Millisecond * 30):
			t.Error("Expected output.")
		}
	})

	select {
	case v := <-p.Stdout():
		t.Errorf("Unexpected output - %#v", *v)
	case <-time.After(time.Millisecond * 20):
	}
}

func TestBadJsonOutput(t *testing.T) {
	out := bytes.NewReader([]byte(`{"a":"b"}
2019/08/21
13:43:24
invalid character '}'
{"c":"d"}{"c":"d"}
{"c":"d"}`))
	tmp := su.MakeJsonLineParser(out, 4096)
	p := func() *interface{} {
		a,_ := tmp()
		return a
	}

	assertExpectedEqualsActual(t, map[string]interface{}{"a": "b"}, *p())
	assertExpectedEqualsActual(t, float64(2019), *p())
	assertExpectedEqualsActual(t, (*interface{})(nil), p())
	assertExpectedEqualsActual(t, float64(13), *p())
	assertExpectedEqualsActual(t, (*interface{})(nil), p())
	assertExpectedEqualsActual(t, (*interface{})(nil), p())
	assertExpectedEqualsActual(t, map[string]interface{}{"c": "d"}, *p())
	assertExpectedEqualsActual(t, map[string]interface{}{"c": "d"}, *p())
	assertExpectedEqualsActual(t, map[string]interface{}{"c": "d"}, *p())
	assertExpectedEqualsActual(t, (*interface{})(nil), p())
}

func BenchmarkBadJsonOutput(b *testing.B) {

}

func TestProcess_Start(t *testing.T) {
	p := su.NewProcess(su.ProcessOptions{
		Id:           funcName(),
		Name:         "./greet_with_error.sh",
		Args:         []string{"Hello"},
		Dir:          testDir(t),
		OutputParser: su.MakeLineParser,
		ErrorParser:  makeErrorParser,
		MaxSpawns:    1,
		Out:          make(chan *interface{}, 1),
		Err:          make(chan *interface{}, 1),
	})

	fatalIfErr(t, p.Start())
	defer p.Stop()

	x := []byte("world\n")
	select {
	case p.Input() <- x:
	case <-time.After(time.Millisecond):
		t.Error("Input wasn't consumed in 1 millisecond")
	}

	select {
	case out := <-p.Stdout():
		assertExpectedEqualsActual(t, "Hello world", *out)
	case <-time.After(time.Millisecond * 200):
		t.Error("No output in 200ms")
	}

	select {
	case v := <-p.Stderr():
		assertExpectedEqualsActual(t, "Bye world", (*v).(error).Error())
	case <-time.After(time.Millisecond * 200):
		t.Error("No error in 200ms")
	}
}

func TestMakeLineParser(t *testing.T) {
	cmd := exec.Command("./endless.sh")
	cmd.Dir = testDir(t)
	out, _ := cmd.StdoutPipe()
	_ = cmd.Start()
	c := make(chan *interface{})
	go func() {
		x := su.MakeLineParser(out, 0)
		for a,_ := x(); a != nil; a,_ = x() {
			c <- a
		}
		close(c)
	}()
	time.AfterFunc(time.Second, func() {
		_ = cmd.Process.Kill()
	})

	runFor(t, 0, 10, func(t *testing.T, i int) {
		select {
		case x := <-c:
			if x == nil {
				t.Error("unexpected nil")
				return
			}
			assertExpectedEqualsActual(t, "foo", *x)
		case <-time.After(time.Millisecond * 20):
			t.Error("Expected output before 20ms pass.")
		}
	})
}

func TestProcess_Signal(t *testing.T) {
	p := su.NewProcess(su.ProcessOptions{
		Id:           funcName(),
		Name:         "./endless.sh",
		Dir:          testDir(t),
		Out:          make(chan *interface{}, 10),
		OutputParser: su.MakeLineParser,
		ErrorParser:  makeErrorParser,
	})

	fatalIfErr(t, p.Start())
	defer p.Stop()
	pid := p.Pid()

	c := make(chan bool)
	time.AfterFunc(time.Millisecond * 70, func() {
		fatalIfErr(t, syscall.Kill(-p.Pid(), syscall.SIGINT))
		c <- true
	})

	runFor(t, 0, 5, func(t *testing.T, i int) {
		select {
		case out := <-p.Stdout():
			if *out != "foo" {
				t.Errorf(`Expected: "foo", received: "%s"`, *out)
			}
		case err := <-p.Stderr():
			t.Error("Unexpected error:", err)
		case <-time.After(time.Millisecond * 30):
			t.Error("Expected output in channel")
		}
	})

	<-c
	time.Sleep(time.Millisecond * 10)
	ensureProcessKilled(t, pid)
}

func TestProcess_Close(t *testing.T) {
	p := su.NewProcess(su.ProcessOptions{
		Id:            funcName(),
		Name:          "./trap.sh",
		Args:          []string{"endless.sh"},
		Dir:           testDir(t),
		OutputParser:  su.MakeLineParser,
		ErrorParser:   makeErrorParser,
		EventNotifier: make(chan su.Event, 10),
		MaxInterruptAttempts: 1,
		MaxTerminateAttempts: 2,
		TerminationGraceTimeout: time.Millisecond,
	})

	procClosedC := make(chan error)
	fatalIfErr(t, p.Start())
	time.AfterFunc(time.Millisecond*20, func() {
		procClosedC <- p.Stop()
	})

	var err error
	var childPid int

	select {
	case v := <-p.Stderr():
		childPid, err = strconv.Atoi((*v).(error).Error())
		if err != nil {
			t.Fatal("Expected child process id in error channel. Instead received:", (*v).(error).Error())
		}
	case <-time.After(time.Millisecond * 10):
		t.Fatal("Expected child process id in error channel in 100 milliseconds")
	}

	t.Run("<-procClosedC", func(t *testing.T) {
		fatalIfErr(t, <-procClosedC)
	})

	t.Run("trapped signals", func(t *testing.T) {
		errs := map[string]string{
			"InterruptError": "interrupt signal failed - 1 attempts",
			"TerminateError": "terminate signal failed - 2 attempts",
		}

		for i := 0; i < 10 && len(errs) > 0; i++ {
			select {
			case ev := <-p.EventNotifier():
				if !strings.HasSuffix(ev.Code, "Error") {
					continue
				}
				assertExpectedEqualsActual(t, errs[ev.Code], ev.Message)
				delete(errs, ev.Code)
			default:
			}
		}
		for code,err := range errs {
			t.Errorf(`expected a %s event - "%s"`, code, err)
		}
	})

	time.Sleep(time.Millisecond * 15)
	ensureProcessKilled(t, childPid)
}

func TestProcess_RespawnOnFailedExit(t *testing.T) {
	p := su.NewProcess(su.ProcessOptions{
		Id:                funcName(),
		Name:              "./error.sh",
		Dir:               testDir(t),
		OutputParser:      su.MakeLineParser,
		ErrorParser:       su.MakeLineParser,
		Err:               make(chan *interface{}, 3),
		MaxSpawns:         3,
		MaxRespawnBackOff: time.Millisecond,
	})

	fatalIfErr(t, p.Start())
	defer p.Stop()

	runFor(t, 0, 3, func(t *testing.T, i int) {
		select {
		case out := <-p.Stdout():
			t.Errorf("Unexpected output: %#v", out)
		case v := <-p.Stderr():
			assertExpectedEqualsActual(t, "Bye world", *v)
		case <-time.After(time.Millisecond * 3000):
			t.Error("Expected error within 3000ms")
			return
		}
	})

	select {
	case out := <-p.Stdout():
		t.Errorf("Unexpected output: %#v", out)
	case v := <-p.Stderr():
		t.Errorf("Unexpected error: %#v", *v)
	case <-time.After(time.Millisecond * 500):
	}
}

func TestProcess_NoRespawnOnSuccessExit(t *testing.T) {
	runtime.Caller(0)
	p := su.NewProcess(su.ProcessOptions{
		Id:           funcName(),
		Name:         "./echo.sh",
		Dir:          testDir(t),
		OutputParser: su.MakeLineParser,
		ErrorParser:  makeErrorParser,
	})

	fatalIfErr(t, p.Start())
	defer p.Stop()

	select {
	case out := <-p.Stdout():
		assertExpectedEqualsActual(t, "Hello world", *out)
	case <-time.After(time.Millisecond * 150):
		t.Error("No output in 150 milliseconds")
	}

	select {
	case out := <-p.Stdout():
		t.Errorf("Unexpected output: %s", *out)
	case <-time.After(time.Millisecond * 10):
	}
}

func TestCalcBackOff(t *testing.T) {
	p1 := su.NewProcess(su.ProcessOptions{Id: funcName() + "-1"})
	p2 := su.NewProcess(su.ProcessOptions{Id: funcName() + "-2"})

	for i := 0; i < 5; i++ {
		a, b := p1.CalcBackOff(i, time.Second, time.Minute), p2.CalcBackOff(i, time.Second, time.Minute)
		if a == b {
			t.Errorf("2 identical results for CalcBackOff(%d, time.Minute): %v", i, a)
		}
	}
}

func TestProcess_Restart(t *testing.T) {
	defer leaktest.Check(t)()
	timer := time.NewTimer(0)
	safeStop(timer)

	// initialGoroutines := runtime.NumGoroutine()
	p := su.NewProcess(su.ProcessOptions{
		Id:                funcName(),
		Name:              "./endless.sh",
		Dir:               testDir(t),
		OutputParser:      su.MakeLineParser,
		ErrorParser:       makeErrorParser,
		Out:               make(chan *interface{}, 5),
		IdleTimeout:       time.Millisecond * 30,
		MaxSpawns:         2,
		MaxRespawnBackOff: time.Microsecond * 100,
		TerminationGraceTimeout: time.Millisecond,
	})

	fatalIfErr(t, p.Start())
	defer p.Stop()

	numGoroutines := -1

	runFor(t, 0, 3, func(t *testing.T, i int) {
		timer.Reset(time.Millisecond * 20)
		if numGoroutines == -1 {
			numGoroutines = runtime.NumGoroutine()
		}
		select {
		case out := <-p.Stdout():
			if *out != "foo" {
				t.Errorf(`Expected: "foo", received: "%s"`, *out)
			}
		case err := <-p.Stderr():
			t.Error("Unexpected error:", err)
		case <-timer.C:
			t.Error("Expected output in channel")
			return
		}
		safeStop(timer)
	})

	fatalIfErr(t, p.Restart())

	t.Run("SIGINT received", func(t *testing.T) {
		if state := p.LastProcessState(); state != nil {
			raw := state.Sys()
			waitStatus, ok := raw.(syscall.WaitStatus)
			if !ok {
				t.Fatalf("Process.LastError().Sys() should be of type syscall.WaitStatus, %q received", raw)
			} else if waitStatus.Signal() != syscall.SIGINT {
				t.Errorf("Expected %#v, %#v signal received", syscall.SIGINT.String(), waitStatus.Signal().String())
			}
		}
	})

	runFor(t, 3, 6, func(t *testing.T, i int) {
		timer.Reset(time.Millisecond * 20)
		select {
		case out := <-p.Stdout():
			if *out != "foo" {
				t.Errorf(`Expected: "foo", received: "%s"`, *out)
			}
		case err := <-p.Stderr():
			t.Error("Unexpected error:", err)
		case <-timer.C:
			t.Error("Expected output in channel within 120ms")
			return
		}
		safeStop(timer)
	})

	_ = p.Restart()

	t.Run("MaxSpawns reached", func(t *testing.T) {
		timer.Reset(time.Millisecond * 24)
		select {
		case out := <-p.Stdout():
			t.Error("Unexpected output:", *out)
		case err := <-p.Stderr():
			t.Error("Unexpected error:", err)
		case <-timer.C:
			return
		}
		safeStop(timer)
	})
	time.Sleep(time.Second)
}

// test_timings_compressed_data can be used to test the performance of this library.
func test_timings_compressed_data(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	f, err := os.Open("testdata/ipsum.zlib")
	fatalIfErr(t, err)
	content, err := ioutil.ReadAll(f)
	fatalIfErr(t, err)

	producer := su.NewProcess(su.ProcessOptions{
		Id:               funcName(),
		Name:             "./zlib.sh",
		Dir:              testDir(t),
		OutputParser:     su.MakeLineParser,
		ErrorParser:      su.MakeLineParser,
		MaxSpawnAttempts: 1,
		ParserBufferSize: 170000,
	})

	fatalIfErr(t, producer.Start())

	stop := make(chan bool)
	pDone := make(chan bool)

	prodInNum := int64(0)
	prodOutNum := int64(0)

	go func() {
		for {
			select {
			case <-stop:
				log.Println("prodInNum", prodInNum)
				return
			case <-time.After(time.Microsecond):
				producer.Input() <- content
				prodInNum++
			}
		}
	}()

	go func() {
		for {
			select {
			case <-stop:
				log.Println("prodOutNum", prodOutNum)
				return
			case <-producer.Stdout():
				prodOutNum++
			}
		}
	}()

	go func() {
		<-stop
		_ = producer.Stop()
		close(pDone)
	}()

	time.AfterFunc(time.Second*10, func() {
		close(stop)
	})

	<-pDone

	log.Println(prodInNum, prodOutNum)
}

// test_timings can be used to test the performance of this library.
func test_timings(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	f, err := os.Open("testdata/ipsum.txt")
	fatalIfErr(t, err)

	ipsum, err := ioutil.ReadAll(f)
	fatalIfErr(t, err)
	ipsum = append(ipsum, '\n')

	producer := su.NewProcess(su.ProcessOptions{
		Id:               funcName(),
		Name:             "./producer.sh",
		Dir:              testDir(t),
		OutputParser:     su.MakeBytesParser,
		ErrorParser:      su.MakeLineParser,
		ParserBufferSize: 170000,
	})
	incrementer := su.NewProcess(su.ProcessOptions{
		Id:               funcName(),
		Name:             "./incrementer.sh",
		Dir:              testDir(t),
		OutputParser:     su.MakeBytesParser,
		ErrorParser:      su.MakeLineParser,
		ParserBufferSize: 170000,
	})

	fatalIfErr(t, producer.Start())
	fatalIfErr(t, incrementer.Start())

	stop := make(chan bool)
	pDone := make(chan bool)
	iDone := make(chan bool)

	prodInNum := int64(0)
	prodOutNum := int64(0)
	incOutNum := int64(0)

	go func() {
		for {
			select {
			case <-stop:
				log.Println("prodInNum", prodInNum)
				return
			case <-time.After(time.Microsecond * 50):
				producer.Input() <- ipsum
				prodInNum++
			}
		}
	}()

	go func() {
		for {
			select {
			case <-stop:
				log.Println("prodOutNum", prodOutNum)
				return
			case msg := <-producer.Stdout():
				incrementer.Input() <- (*msg).([]byte)
				prodOutNum++
			}
		}
	}()

	go func() {
		for {
			select {
			case <-stop:
				log.Println("incOutNum", incOutNum)
				return
			case <-incrementer.Stdout():
				incOutNum++
			}
		}
	}()

	go func() {
		<-stop
		_ = producer.Stop()
		close(pDone)
	}()
	go func() {
		<-stop
		_ = incrementer.Stop()
		close(iDone)
	}()

	time.AfterFunc(time.Second*10, func() {
		close(stop)
	})

	<-iDone
	<-pDone

	log.Println(prodInNum, prodOutNum, incOutNum)
}
