# go-spawn

small library for spawning processes in `Go`, still a work in progress...

## Daemon example
`example.bash` print stuff to stdout and stderr and quit after 5 seconds...
```bash
#!/bin/bash
echo "STDOUT MESSAGE"
echo "STDERR MESSAGE" 1>&2
sleep 5
```

`daemon-exapmle.go` daemonize the bash program...
```go
package main

import (
	"github.com/kontera-technologies/go-spawn/spawner"
	"log"
)

func main() {
	p, err := spawner.Watch("example.bash", spawner.Options{
		Args:                    []string{"1"},
		Debug:                   true, // print events to stdout
		SpawnAttempts:           4,    // attempts before giving up
		AttemptsBeforeTerminate: 10,   // on Stop() terminate process after X interrupt attempts
		DelayBetweenSpawns: func(currentSleep int) (sleepTime int) { // in seconds
			if currentSleep > 500 {
				return 1
			} else {
				return 2 * currentSleep
			}
		},
	})

	if err != nil {
		log.Printf("failed to start process: %s", err)
		return
	}

	done := make(chan bool)
	pDone := p.NotifyDone(make(chan bool)) // process is done...

	events := p.NotifyEvents(make(chan *spawner.Event))

	// print events
	go func() {
		for event := range events {
			log.Printf("[%s][%d] %s", event.Time, event.Code, event.Message)
		}
		log.Printf("events loop is done...")
	}()

	// read stuff
	go func() {
	Loop:
		for {
			select {
			case msg := <-p.Stdout:
				log.Printf("Recived STDOUT message %s", msg)
			case msg := <-p.Stderr:
				log.Printf("Recived STDERR message %s", msg)
			case <-pDone: // process quit
				log.Printf("Closing loop we are done....")
				done <- true
				break Loop
			}
		}
	}()

	<-done
}
```

running the program should produce this output
```
2015/02/02 22:48:43 starting instance...
2015/02/02 22:48:43 Recived STDERR message &STDERR MESSAGE
2015/02/02 22:48:43 Recived STDOUT message &STDOUT MESSAGE
2015/02/02 22:48:48 can't read from stderr: EOF
2015/02/02 22:48:48 [2015-02-02 22:48:48.091377454 +0200 IST][1] can't read from stderr: EOF
2015/02/02 22:48:48 can't read from stdout: EOF
2015/02/02 22:48:48 instance crashed...
2015/02/02 22:48:48 [2015-02-02 22:48:48.091476207 +0200 IST][1] can't read from stdout: EOF
2015/02/02 22:48:48 [2015-02-02 22:48:48.091524301 +0200 IST][7] instance crashed...
2015/02/02 22:48:48 stderr goroutine is done...
2015/02/02 22:48:48 stdout goroutine is done...
2015/02/02 22:48:48 [2015-02-02 22:48:48.091568633 +0200 IST][5] stderr goroutine is done...
2015/02/02 22:48:48 [2015-02-02 22:48:48.091574327 +0200 IST][5] stdout goroutine is done...
2015/02/02 22:48:48 stdin goroutine is done...
2015/02/02 22:48:48 going to sleep for 2
2015/02/02 22:48:48 [2015-02-02 22:48:48.091630534 +0200 IST][5] stdin goroutine is done...
2015/02/02 22:48:48 [2015-02-02 22:48:48.09163443 +0200 IST][10] going to sleep for 2
2015/02/02 22:48:50 starting instance...
2015/02/02 22:48:50 [2015-02-02 22:48:50.593858017 +0200 IST][8] starting instance...
2015/02/02 22:48:50 Recived STDOUT message &STDOUT MESSAGE
2015/02/02 22:48:50 Recived STDERR message &STDERR MESSAGE
2015/02/02 22:48:55 can't read from stderr: EOF
2015/02/02 22:48:55 can't read from stdout: EOF
2015/02/02 22:48:55 [2015-02-02 22:48:55.602593155 +0200 IST][1] can't read from stderr: EOF
2015/02/02 22:48:55 [2015-02-02 22:48:55.602628786 +0200 IST][1] can't read from stdout: EOF
2015/02/02 22:48:55 instance crashed...
2015/02/02 22:48:55 stderr goroutine is done...
2015/02/02 22:48:55 [2015-02-02 22:48:55.602656129 +0200 IST][7] instance crashed...
2015/02/02 22:48:55 [2015-02-02 22:48:55.602707346 +0200 IST][5] stderr goroutine is done...
2015/02/02 22:48:55 stdout goroutine is done...
2015/02/02 22:48:55 stdin goroutine is done...
2015/02/02 22:48:55 [2015-02-02 22:48:55.602764125 +0200 IST][5] stdout goroutine is done...
2015/02/02 22:48:55 [2015-02-02 22:48:55.602798814 +0200 IST][5] stdin goroutine is done...
2015/02/02 22:48:55 going to sleep for 4
2015/02/02 22:48:55 [2015-02-02 22:48:55.602853793 +0200 IST][10] going to sleep for 4
2015/02/02 22:49:00 starting instance...
2015/02/02 22:49:00 [2015-02-02 22:49:00.596979277 +0200 IST][8] starting instance...
2015/02/02 22:49:00 Recived STDOUT message &STDOUT MESSAGE
2015/02/02 22:49:00 Recived STDERR message &STDERR MESSAGE
2015/02/02 22:49:05 can't read from stdout: EOF
2015/02/02 22:49:05 [2015-02-02 22:49:05.603937469 +0200 IST][1] can't read from stdout: EOF
2015/02/02 22:49:05 instance crashed...
2015/02/02 22:49:05 stdout goroutine is done...
2015/02/02 22:49:05 can't read from stderr: EOF
2015/02/02 22:49:05 [2015-02-02 22:49:05.603998218 +0200 IST][7] instance crashed...
2015/02/02 22:49:05 [2015-02-02 22:49:05.604012808 +0200 IST][5] stdout goroutine is done...
2015/02/02 22:49:05 [2015-02-02 22:49:05.604019519 +0200 IST][1] can't read from stderr: EOF
2015/02/02 22:49:05 stdin goroutine is done...
2015/02/02 22:49:05 [2015-02-02 22:49:05.604069949 +0200 IST][5] stdin goroutine is done...
2015/02/02 22:49:05 stderr goroutine is done...
2015/02/02 22:49:05 going to sleep for 8
2015/02/02 22:49:05 [2015-02-02 22:49:05.604095573 +0200 IST][5] stderr goroutine is done...
2015/02/02 22:49:05 [2015-02-02 22:49:05.604099336 +0200 IST][10] going to sleep for 8
2015/02/02 22:49:15 starting instance...
2015/02/02 22:49:15 [2015-02-02 22:49:15.50346931 +0200 IST][8] starting instance...
2015/02/02 22:49:15 Recived STDOUT message &STDOUT MESSAGE
2015/02/02 22:49:15 Recived STDERR message &STDERR MESSAGE
2015/02/02 22:49:20 can't read from stdout: EOF
2015/02/02 22:49:20 [2015-02-02 22:49:20.511186818 +0200 IST][1] can't read from stdout: EOF
2015/02/02 22:49:20 giving up, instance failed to start...
2015/02/02 22:49:20 watch daemon is off...
2015/02/02 22:49:20 can't read from stderr: EOF
2015/02/02 22:49:20 [2015-02-02 22:49:20.511267744 +0200 IST][9] giving up, instance failed to start...
2015/02/02 22:49:20 [2015-02-02 22:49:20.511280048 +0200 IST][11] watch daemon is off...
2015/02/02 22:49:20 [2015-02-02 22:49:20.511286326 +0200 IST][1] can't read from stderr: EOF
2015/02/02 22:49:20 Closing loop we are done....
2015/02/02 22:49:20 events loop is done...
```
