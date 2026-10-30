package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/k8snetworkplumbingwg/linuxptp-daemon/pkg/ipc"
)

type step struct {
	Message ipc.Message `json:"message"`
}

func main() {
	socket := flag.String("socket", "/var/run/ptp/events.sock", "Path to cloud-event-proxy Unix socket")
	messages := flag.String("messages", "", "JSON array of {message} steps")
	flag.Parse()

	if *messages == "" {
		log.Fatal("--messages is required")
	}

	var steps []step
	if err := json.Unmarshal([]byte(*messages), &steps); err != nil {
		log.Fatalf("invalid --messages JSON: %v", err)
	}
	if len(steps) == 0 {
		log.Fatal("--messages must contain at least one step")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cache := ipc.NewCache(64)

	// First message pre-populates the cache before the Link connects
	cache.Send(steps[0].Message)
	log.Printf("cached initial message: type=%s profile=%s", steps[0].Message.Type, steps[0].Message.Profile)

	link := ipc.NewLink(*socket, cache)
	go link.Run(ctx)
	log.Printf("link started, dialing %s", *socket)

	// Each subsequent message is sent on SIGUSR1
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)

	for i, s := range steps[1:] {
		select {
		case <-usr1:
		case <-ctx.Done():
			return
		}
		cache.Send(s.Message)
		log.Printf("sent message %d: type=%s profile=%s", i+1, s.Message.Type, s.Message.Profile)
	}

	<-ctx.Done()
}
