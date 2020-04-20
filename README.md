# go-supervisor (V2)
[![Build Status](https://travis-ci.com/kontera-technologies/go-supervisor.svg?branch=master.v2)](https://travis-ci.com/kontera-technologies/go-supervisor)
[![codecov](https://codecov.io/gh/kontera-technologies/go-supervisor/branch/master.v2/graph/badge.svg)](https://codecov.io/gh/kontera-technologies/go-supervisor)

Small library for supervising child processes in `Go`, it exposes `Stdout`,`Stderr` and `Stdin` in the "Go way" using channels...

## Example
`echo.sh` print stuff to stdout and stderr and quit after 5 seconds...
```bash
#!/usr/bin/env bash

echo "STDOUT MESSAGE"
sleep 0.1
echo "STDERR MESSAGE" 1>&2
sleep 0.1
```

`supervisor-exapmle.go` spawn and supervise the bash program...
```go
package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/kontera-technologies/go-supervisor/v2"
)

func main() {
	testDir, _ := filepath.Abs("testdata")
	events := make(chan supervisor.Event)
	p := supervisor.NewProcess(supervisor.ProcessOptions{
		Name:                 "./example.sh",
		Dir:                  testDir,
		Id:                   "example",
		EventNotifier:        events,
		OutputParser:         supervisor.MakeBytesParser,
		ErrorParser:          supervisor.MakeBytesParser,
		MaxSpawns:            4,
		MaxSpawnAttempts:     2,
		MaxInterruptAttempts: 3,
		MaxTerminateAttempts: 5,
		IdleTimeout:          10 * time.Second,
	})

	exit := make(chan bool)

	go func() {
		for {
			select {
			case msg := <-p.Stdout():
				fmt.Printf("Recived STDOUT message: %s\n", *msg)
			case msg := <-p.Stderr():
				fmt.Printf("Recived STDERR message: %s\n", *msg)
			case event := <-events:
				switch event.Code {
				case "ProcessStart":
					fmt.Printf("Recived event: %s\n", event.Code)
				default:
					fmt.Printf("Recived event: %s - %s\n", event.Code, event.Message)
				}
			case <-p.DoneNotifier():
				fmt.Println("Closing loop we are done...")
				close(exit)
				return
			}
		}
	}()

	if err := p.Start(); err != nil {
		panic(fmt.Sprintf("failed to start process: %s", err))
	}

	<-exit
}
```

running the program should produce this output
```
Recived event: ProcessStart
Recived STDOUT message: STDOUT MESSAGE
Recived STDERR message: STDERR MESSAGE
Recived event: ProcessDone - exit status 0
Recived event: StoppingHeartbeatMonitoring - Stop signal received.
Recived event: Sleep - Sleeping for 1s before respwaning instance.
Recived event: ProcessRespawn - Trying to respawn instance.
Recived event: ProcessStart
Recived STDOUT message: STDOUT MESSAGE
Recived STDERR message: STDERR MESSAGE
Recived event: ProcessDone - exit status 0
Recived event: StoppingHeartbeatMonitoring - Stop signal received.
Recived event: Sleep - Sleeping for 1s before respwaning instance.
Recived event: ProcessRespawn - Trying to respawn instance.
Recived event: ProcessStart
Recived STDOUT message: STDOUT MESSAGE
Recived STDERR message: STDERR MESSAGE
Recived event: ProcessDone - exit status 0
Recived event: StoppingHeartbeatMonitoring - Stop signal received.
Recived event: Sleep - Sleeping for 1s before respwaning instance.
Recived event: ProcessRespawn - Trying to respawn instance.
Recived event: ProcessStart
Recived STDOUT message: STDOUT MESSAGE
Recived STDERR message: STDERR MESSAGE
Recived event: ProcessDone - exit status 0
Recived event: StoppingHeartbeatMonitoring - Stop signal received.
Recived event: RespawnError - Max number of respawns reached.
Closing loop we are done...
```
