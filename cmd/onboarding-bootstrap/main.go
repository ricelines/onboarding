package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ricelines/chat/onboarding/internal/bootstrap"
)

func main() {
	cfg, err := bootstrap.FromEnv()
	if err != nil {
		log.Fatalf("load bootstrap config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := bootstrap.NewRunner(cfg).Run(ctx); err != nil {
		log.Fatalf("bootstrap onboarding: %v", err)
	}
}
