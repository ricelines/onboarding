package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReserveInitialUserAgentIsIdempotent(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	first, created, err := db.ReserveInitialUserAgent(context.Background(), "@alice:example.com", "onboarding-default", "default", "alice-bot", "secret")
	if err != nil {
		t.Fatalf("ReserveInitialUserAgent() error = %v", err)
	}
	if !created {
		t.Fatal("first reserve should create a record")
	}

	second, created, err := db.ReserveInitialUserAgent(context.Background(), "@alice:example.com", "onboarding-default", "default", "ignored", "ignored")
	if err != nil {
		t.Fatalf("second ReserveInitialUserAgent() error = %v", err)
	}
	if created {
		t.Fatal("second reserve should reuse the existing record")
	}
	if second.BotUsername != first.BotUsername || second.BotPassword != first.BotPassword {
		t.Fatalf("second reserve changed reserved credentials: %#v", second)
	}
}
