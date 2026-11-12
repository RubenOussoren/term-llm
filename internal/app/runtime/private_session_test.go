package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samsaffron/term-llm/internal/agents"
	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	runpkg "github.com/samsaffron/term-llm/internal/run"
	"github.com/samsaffron/term-llm/internal/session"
)

func lockedSessionFixture(t *testing.T) (*session.SQLiteStore, *session.Session, *config.Config) {
	t.Helper()
	store, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte("name: private\nprovider: debug\nmodel: allowed\nallowed_models: [debug:allowed]\nlock_session: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy := modelpolicy.Policy{}.With("private", []string{"debug:allowed"}).With("ancestor", []string{"debug:*"})
	policy.LockSession = true
	saved := &session.Session{ID: session.NewID(), Agent: dir, CWD: t.TempDir(), Provider: "debug", ProviderKey: "debug", Model: "allowed", ModelPolicy: policy}
	if err := store.Create(context.Background(), saved); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{DefaultProvider: "debug", Providers: map[string]config.ProviderConfig{"debug": {Model: "allowed", FastProvider: "wrong", FastModel: "denied"}, "wrong": {Model: "denied"}}}
	return store, saved, cfg
}
func TestLockedSessionModelAndScopeAdmission(t *testing.T) {
	store, saved, cfg := lockedSessionFixture(t)
	for _, tc := range []struct{ name, agent, cwd, provider, model string }{
		{"resume", saved.Agent, saved.CWD, "debug", "allowed"},
		{"agent", "coding", saved.CWD, "debug", "allowed"},
		{"workspace", saved.Agent, t.TempDir(), "debug", "allowed"},
		{"provider", saved.Agent, saved.CWD, "wrong", "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, err := AdmitPrivateSession(context.Background(), store, core.Request{SessionID: saved.ID}, saved.ModelPolicy, cfg, tc.agent, tc.cwd, tc.provider, tc.model)
			if tc.name != "resume" {
				if err == nil {
					t.Fatal("retarget admitted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !core.PrivateSessionFromContext(core.ContextWithSessionID(ctx, saved.ID)) {
				t.Fatal("private marker absent after admission")
			}
			if core.PrivateSessionFromContext(core.ContextWithSessionID(ctx, "foreign")) {
				t.Fatal("private marker crossed sessions")
			}
		})
	}
	// Saved own rules survive a loosened current definition and a new provider.
	if err := saved.ModelPolicy.CheckWithConfig(cfg, "private", "wrong", "denied"); err == nil {
		t.Fatal("sticky model rule lost")
	}
	agent, inherited, resume, err := ResolveRunAgent(context.Background(), runpkg.Request{SessionID: saved.ID, Resume: true}, cfg, store)
	if err != nil || agent == nil || !inherited.LockSession || resume.Provider != "debug" {
		t.Fatalf("resolve: %+v %+v %v", agent, inherited, err)
	}
	if _, _, _, err := ResolveRunAgent(context.Background(), runpkg.Request{SessionID: saved.ID, AgentName: "coding", Resume: true}, cfg, store); err == nil {
		t.Fatal("agent retarget reached agent loader")
	}
	aux, err := AuxProviderForPolicy(cfg, inherited, ModelSelection{Provider: "debug", Model: "allowed"}, "debug")
	if err != nil || aux == nil {
		t.Fatalf("policy-safe auxiliary fallback: %v", err)
	}
	if got := aux.Name(); got != "debug:allowed" {
		t.Fatalf("wrong auxiliary provider: %q", got)
	}
}
func TestLockedEffectivePolicyAndDefaultAgents(t *testing.T) {
	inherited := modelpolicy.Policy{}.With("parent", []string{"debug:*"})
	own := &agents.Agent{Name: "private", Provider: "debug", Model: "allowed", LockSession: true, AllowedModels: []string{"debug:allowed"}}
	cfg := &config.Config{DefaultProvider: "debug", Providers: map[string]config.ProviderConfig{"debug": {Model: "allowed"}, "wrong": {Model: "denied"}}}
	_, policy, err := SelectRunModel(cfg, ModelSelectionInput{Agent: own, Inherited: inherited})
	if err != nil || !policy.LockSession || len(LockedModelPolicy(policy).Rules) != 2 {
		t.Fatalf("effective policy: %+v %v", policy, err)
	}
	own.LockSession = false
	_, policy, err = SelectRunModel(cfg, ModelSelectionInput{Agent: own, Inherited: inherited})
	if err != nil || policy.LockSession || LockedModelPolicy(policy).Restricted() {
		t.Fatalf("default agent behavior changed: %+v %v", policy, err)
	}
}
