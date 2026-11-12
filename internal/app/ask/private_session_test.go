package ask

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/llm/mockprovider"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	"github.com/samsaffron/term-llm/internal/session"
)

func TestDirectAskRejectsLockedSessionsBeforeProviderSetup(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		resume, lockedAgent, debug, raw, retarget bool
	}{
		{name: "new", lockedAgent: true},
		{name: "debug", lockedAgent: true, debug: true},
		{name: "debugraw", lockedAgent: true, raw: true},
		{name: "locked-resume", resume: true},
		{name: "resume-debug", resume: true, debug: true},
		{name: "resume-debugraw", resume: true, raw: true},
		{name: "retarget", resume: true, retarget: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Chdir(t.TempDir())
			agentDir := t.TempDir()
			body := "name: private\nprovider: mock\nmodel: mock-model\nallowed_models: [mock:mock-model]\n"
			if tc.lockedAgent {
				body += "lock_session: true\n"
			}
			if err := os.WriteFile(filepath.Join(agentDir, "agent.yaml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			db := filepath.Join(t.TempDir(), "sessions.db")
			cfg := &config.Config{DefaultProvider: "mock", Providers: map[string]config.ProviderConfig{"mock": {Model: "mock-model"}}}
			cfg.Sessions.Enabled = true
			cfg.Sessions.Path = db
			opts := Options{Agent: agentDir, Debug: tc.debug, DebugRaw: tc.raw, Text: true, Porcelain: true}
			if tc.resume {
				store, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: db})
				if err != nil {
					t.Fatal(err)
				}
				policy := modelpolicy.Policy{LockSession: true}.With(agentDir, []string{"mock:mock-model"})
				sess := &session.Session{ID: session.NewID(), Agent: agentDir, Provider: "mock", ProviderKey: "mock", Model: "mock-model", ModelPolicy: policy}
				if err := store.Create(context.Background(), sess); err != nil {
					t.Fatal(err)
				}
				if err := store.AddMessage(context.Background(), sess.ID, session.NewMessage(sess.ID, core.UserText("must not replay"), -1)); err != nil {
					t.Fatal(err)
				}
				store.Close()
				opts.Resume, opts.Changed.Resume = sess.ID, true
				if tc.retarget {
					opts.Provider = "other:forbidden"
				}
			}
			provider := mockprovider.NewMockProvider("mock").AddTextResponse("must not run")
			deps := testDependencies(io.Discard, io.Discard)
			deps.LoadConfig = func() (*config.Config, error) { return cfg, nil }
			deps.SessionStore.DBPath = db
			created := false
			deps.NewProvider = func(*config.Config, bool) (core.Provider, error) { created = true; return provider, nil }
			err := Run(context.Background(), []string{"question"}, opts, deps)
			if err == nil || !strings.Contains(err.Error(), "locked sessions are not supported by direct ask") {
				t.Fatalf("error=%v", err)
			}
			if created || len(provider.RecordedRequests()) != 0 {
				t.Fatal("locked ask reached provider setup/dispatch")
			}
		})
	}
}
