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

	// Output:
	// Recived event: ProcessStart
	// Recived STDOUT message: STDOUT MESSAGE
	// Recived STDERR message: STDERR MESSAGE
	// Recived event: ProcessDone - exit status 0
	// Recived event: StoppingHeartbeatMonitoring - Stop signal received.
	// Recived event: Sleep - Sleeping for 1s before respwaning instance.
	// Recived event: ProcessRespawn - Trying to respawn instance.
	// Recived event: ProcessStart
	// Recived STDOUT message: STDOUT MESSAGE
	// Recived STDERR message: STDERR MESSAGE
	// Recived event: ProcessDone - exit status 0
	// Recived event: StoppingHeartbeatMonitoring - Stop signal received.
	// Recived event: Sleep - Sleeping for 1s before respwaning instance.
	// Recived event: ProcessRespawn - Trying to respawn instance.
	// Recived event: ProcessStart
	// Recived STDOUT message: STDOUT MESSAGE
	// Recived STDERR message: STDERR MESSAGE
	// Recived event: ProcessDone - exit status 0
	// Recived event: StoppingHeartbeatMonitoring - Stop signal received.
	// Recived event: Sleep - Sleeping for 1s before respwaning instance.
	// Recived event: ProcessRespawn - Trying to respawn instance.
	// Recived event: ProcessStart
	// Recived STDOUT message: STDOUT MESSAGE
	// Recived STDERR message: STDERR MESSAGE
	// Recived event: ProcessDone - exit status 0
	// Recived event: StoppingHeartbeatMonitoring - Stop signal received.
	// Recived event: RespawnError - Max number of respawns reached.
	// Closing loop we are done...
}
