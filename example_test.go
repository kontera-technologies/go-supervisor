package supervisor_test

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/kontera-technologies/go-supervisor/v2"
)

func Example() {
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
		MaxSpawnBackOff:      time.Second,
		MaxRespawnBackOff:    time.Second,
	})

	exit := make(chan bool)

	go func() {
		for {
			select {
			case msg := <-p.Stdout():
				fmt.Printf("Received STDOUT message: %s\n", *msg)
			case msg := <-p.Stderr():
				fmt.Printf("Received STDERR message: %s\n", *msg)
			case event := <-events:
				if event.Message != "" {
					fmt.Printf("Received event: %s - %s\n", event.Code, event.Message)
				} else {
					fmt.Printf("Received event: %s\n", event.Code)
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

	// Output:
	// Received event: ProcessStart
	// Received STDOUT message: STDOUT MESSAGE
	// Received STDERR message: STDERR MESSAGE
	// Received event: ProcessDone - exit status 0
	// Received event: StoppingHeartbeatMonitoring - Stop signal received.
	// Received event: Sleep - Sleeping for 1s before respwaning instance.
	// Received event: ProcessRespawn - Trying to respawn instance.
	// Received event: ProcessStart
	// Received STDOUT message: STDOUT MESSAGE
	// Received STDERR message: STDERR MESSAGE
	// Received event: ProcessDone - exit status 0
	// Received event: StoppingHeartbeatMonitoring - Stop signal received.
	// Received event: Sleep - Sleeping for 1s before respwaning instance.
	// Received event: ProcessRespawn - Trying to respawn instance.
	// Received event: ProcessStart
	// Received STDOUT message: STDOUT MESSAGE
	// Received STDERR message: STDERR MESSAGE
	// Received event: ProcessDone - exit status 0
	// Received event: StoppingHeartbeatMonitoring - Stop signal received.
	// Received event: Sleep - Sleeping for 1s before respwaning instance.
	// Received event: ProcessRespawn - Trying to respawn instance.
	// Received event: ProcessStart
	// Received STDOUT message: STDOUT MESSAGE
	// Received STDERR message: STDERR MESSAGE
	// Received event: ProcessDone - exit status 0
	// Received event: StoppingHeartbeatMonitoring - Stop signal received.
	// Received event: RespawnError - Max number of respawns reached.
	// Closing loop we are done...
}
