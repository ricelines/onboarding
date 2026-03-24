package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ricelines/chat/onboarding/internal/managerforwarders"
)

func main() {
	var (
		managerContainer string
		forwarderImage   string
		namePrefix       string
		pollInterval     time.Duration
		addHostGateway   bool
	)

	flag.StringVar(&managerContainer, "manager-container", "", "amber-manager container name")
	flag.StringVar(&forwarderImage, "forwarder-image", "ghcr.io/ricelines/onboarding:v0.1", "image that contains /app/onboarding-tcp-forwarder")
	flag.StringVar(&namePrefix, "name-prefix", "onboarding-manager-forwarder", "docker container name prefix for forwarders")
	flag.DurationVar(&pollInterval, "poll-interval", 200*time.Millisecond, "poll interval")
	flag.BoolVar(&addHostGateway, "add-host-gateway", runtime.GOOS == "linux", "add host.docker.internal:host-gateway to forwarder containers")
	flag.Parse()

	if managerContainer == "" {
		fmt.Fprintln(os.Stderr, "error: --manager-container is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	monitor, err := managerforwarders.Start(ctx, managerforwarders.Config{
		ManagerContainerName: managerContainer,
		ForwarderImage:       forwarderImage,
		ForwarderNamePrefix:  namePrefix,
		PollInterval:         pollInterval,
		AddHostGateway:       addHostGateway,
		Logger: func(format string, args ...any) {
			log.Printf(format, args...)
		},
	})
	if err != nil {
		log.Fatalf("start manager forwarders: %v", err)
	}

	<-ctx.Done()
	if err := monitor.Close(); err != nil && ctx.Err() == nil {
		log.Fatalf("close manager forwarders: %v", err)
	}
}
