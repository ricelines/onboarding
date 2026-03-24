package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ricelines/chat/onboarding/internal/config"
	managerclient "github.com/ricelines/chat/onboarding/internal/manager"
	"github.com/ricelines/chat/onboarding/internal/matrix"
	"github.com/ricelines/chat/onboarding/internal/mcpserver"
	"github.com/ricelines/chat/onboarding/internal/provisioner"
	"github.com/ricelines/chat/onboarding/internal/store"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close store: %v", closeErr)
		}
	}()

	svc := provisioner.NewService(
		db,
		matrix.NewClient(cfg.MatrixHomeserverURL, cfg.RegistrationToken),
		managerclient.NewClient(cfg.ManagerURL),
		cfg,
	)
	svc.SetLogger(log.Printf)
	server := mcpserver.NewServer(svc)

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("onboarding provisioner listening on %s", cfg.ListenAddr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("listen: %v", err)
	}
}
