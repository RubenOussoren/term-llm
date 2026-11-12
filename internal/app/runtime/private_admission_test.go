package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	"github.com/samsaffron/term-llm/internal/session"
)

// Embedding only Store deliberately hides native optional capabilities.
type nonAtomicPrivateStore struct{ session.Store }

func TestPrivateAdmissionRequiresNativeAtomicStore(t *testing.T) {
	store, saved, cfg := lockedSessionFixture(t)
	ctx := core.ContextWithSessionID(context.Background(), saved.ID)
	unsupported := &nonAtomicPrivateStore{Store: store}
	admitted, _, err := AdmitPrivateSession(ctx, unsupported, core.Request{SessionID: saved.ID}, saved.ModelPolicy, cfg, saved.Agent, saved.CWD, "debug", "allowed")
	if err == nil || !strings.Contains(err.Error(), "atomic native") || core.PrivateSessionFromContext(admitted) {
		t.Fatalf("unsupported store admitted private request: %v", err)
	}
	// The native logging decorator is explicitly unwrapped; its best-effort
	// Update semantics must never replace the strict admission transaction.
	logging := session.NewLoggingStore(store, func(string, ...any) {})
	admitted, effective, err := AdmitPrivateSession(ctx, logging, core.Request{SessionID: saved.ID}, saved.ModelPolicy, cfg, saved.Agent, saved.CWD, "debug", "allowed")
	if err != nil || !effective.LockSession || !core.PrivateSessionFromContext(admitted) {
		t.Fatalf("native decorated admission: %+v %v", effective, err)
	}
}

func TestPrivateAdmissionDenialDoesNotPersistCallerRules(t *testing.T) {
	store, saved, cfg := lockedSessionFixture(t)
	for _, tc := range []struct {
		name       string
		request    core.Request
		agent, cwd string
	}{
		{"debug", core.Request{SessionID: saved.ID, Debug: true}, saved.Agent, saved.CWD},
		{"debugraw", core.Request{SessionID: saved.ID, DebugRaw: true}, saved.Agent, saved.CWD},
		{"agent", core.Request{SessionID: saved.ID}, "other", saved.CWD},
		{"workspace", core.Request{SessionID: saved.ID}, saved.Agent, t.TempDir()},
		{"continuation-model", core.Request{SessionID: saved.ID, Resume: &core.Continuation{Request: core.Request{SessionID: saved.ID, Model: "denied"}}}, saved.Agent, saved.CWD},
		{"continuation-session", core.Request{SessionID: saved.ID, Resume: &core.Continuation{Request: core.Request{SessionID: "foreign", Model: "allowed"}}}, saved.Agent, saved.CWD},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming := saved.ModelPolicy.With("caller", []string{"debug:*"})
			ctx := core.ContextWithSessionID(context.Background(), saved.ID)
			admitted, _, err := AdmitPrivateSession(ctx, store, tc.request, incoming, cfg, tc.agent, tc.cwd, "debug", "allowed")
			if err == nil || core.PrivateSessionFromContext(admitted) {
				t.Fatal("invalid atomic admission accepted")
			}
			latest, getErr := store.Get(ctx, saved.ID)
			if getErr != nil || len(latest.ModelPolicy.Rules) != len(saved.ModelPolicy.Rules) {
				t.Fatalf("denial persisted caller restriction: %+v %v", latest, getErr)
			}
		})
	}
	// Ordinary unlocked adapters retain their non-private legacy behavior.
	unlocked := *saved
	unlocked.ID = session.NewID()
	unlocked.ModelPolicy = modelpolicy.Policy{}
	if err := store.Create(context.Background(), &unlocked); err != nil {
		t.Fatal(err)
	}
	ctx := core.ContextWithSessionID(context.Background(), unlocked.ID)
	admitted, effective, err := AdmitPrivateSession(ctx, &nonAtomicPrivateStore{Store: store}, core.Request{SessionID: unlocked.ID}, modelpolicy.Policy{}, cfg, unlocked.Agent, unlocked.CWD, "debug", "allowed")
	if err != nil || effective.LockSession || core.PrivateSessionFromContext(admitted) {
		t.Fatalf("ordinary adapter behavior changed: %v", err)
	}
}
