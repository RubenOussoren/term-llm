package session

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/samsaffron/term-llm/internal/modelpolicy"
)

func TestLockedSessionPolicyPersistsNarrowingAgainstStaleWriters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(Config{Enabled: true, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	other, err := NewSQLiteStore(Config{Enabled: true, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	broad := modelpolicy.Policy{LockSession: true}.With("private", []string{"debug:A", "debug:B"})
	sess := &Session{ID: NewID(), Agent: "private", CWD: "/fixture", ModelPolicy: broad}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	stale := *sess
	narrow := *sess
	narrow.ModelPolicy = broad.With("private", []string{"debug:A"})
	if err := store.Update(ctx, &narrow); err != nil {
		t.Fatal(err)
	}
	// Simulate a later widened agent and stale runtime snapshots on independent
	// SQLite connections. Neither may replace the durable intersection.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next := stale
			writer := store
			if i%2 == 0 {
				writer = other
				next.ModelPolicy = modelpolicy.Policy{}
			}
			errs <- writer.Update(ctx, &next)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	saved, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed := saved.ModelPolicy.Append(broad)
	if !resumed.LockSession || !resumed.Allows("debug", "A") || resumed.Allows("debug", "B") {
		t.Fatalf("narrowing lost: %+v", resumed)
	}
	if len(saved.ModelPolicy.Rules) != 2 {
		t.Fatalf("duplicate or lost rules: %+v", saved.ModelPolicy)
	}
}

func TestUnlockedSessionPolicyUpdateRemainsReplaceable(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess := &Session{ID: NewID(), ModelPolicy: modelpolicy.Policy{}.With("old", []string{"debug:A"})}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	sess.ModelPolicy = modelpolicy.Policy{}.With("new", []string{"debug:B"})
	if err := store.Update(ctx, sess); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ModelPolicy.LockSession || !saved.ModelPolicy.Allows("debug", "B") || saved.ModelPolicy.Allows("debug", "A") {
		t.Fatalf("unlocked replacement changed: %+v", saved.ModelPolicy)
	}
}
