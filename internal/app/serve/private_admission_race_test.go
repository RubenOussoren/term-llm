package serve

import (
	"context"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/llm/mockprovider"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	runpkg "github.com/samsaffron/term-llm/internal/run"
	"github.com/samsaffron/term-llm/internal/session"
)

// Admission's initial Get returns an intentionally stale snapshot, without
// holding any SQLite connection or writer reservation while the test pauses.
type pausedAdmissionStore struct {
	*session.SQLiteStore
	armed        atomic.Bool
	read, resume chan struct{}
}

func (s *pausedAdmissionStore) Get(ctx context.Context, id string) (*session.Session, error) {
	saved, err := s.SQLiteStore.Get(ctx, id)
	if s.armed.CompareAndSwap(true, false) {
		close(s.read)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return saved, err
}

func TestPrivateAdmissionUsesLatestPolicyAtProviderBoundary(t *testing.T) {
	for _, kind := range []string{"turn", "compaction"} {
		for _, selected := range []string{"allowed", "other"} {
			t.Run(kind+"/"+selected, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cfg, agent := modelAllowlistFixture(t, "debug:allowed, debug:other")
				path := filepath.Join(t.TempDir(), "sessions.db")
				first, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: path})
				if err != nil {
					t.Fatal(err)
				}
				defer first.Close()
				second, err := session.NewSQLiteStore(session.Config{Enabled: true, Path: path})
				if err != nil {
					t.Fatal(err)
				}
				defer second.Close()
				store := &pausedAdmissionStore{SQLiteStore: first, read: make(chan struct{}), resume: make(chan struct{})}
				broad := modelpolicy.Policy{LockSession: true}.With("private", []string{"debug:allowed", "debug:other"})
				saved := &session.Session{ID: session.NewID(), Agent: agent, CWD: t.TempDir(), Provider: "debug", ProviderKey: "debug", Model: selected, ModelPolicy: broad}
				if err := first.Create(ctx, saved); err != nil {
					t.Fatal(err)
				}
				for _, msg := range []core.Message{core.UserText("fixture question"), core.AssistantText("fixture answer")} {
					if err := first.AddMessage(ctx, saved.ID, session.NewMessage(saved.ID, msg, -1)); err != nil {
						t.Fatal(err)
					}
				}
				provider := mockprovider.NewMockProvider("mock").AddTextResponse("fixture response")
				runner := &cmdRunner{baseCfg: cfg, defaults: cmdRunnerOptions{Store: store, Tools: "spawn_agent", ToolsSet: true}}
				env, err := runner.prepare(ctx, runpkg.Request{SessionID: saved.ID, Resume: true, AgentName: agent, Cwd: saved.CWD, Provider: "debug:" + selected, ProviderInstance: provider, ModelPolicy: broad.With("runtime-B", []string{"debug:" + selected})}, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer env.Close()
				store.armed.Store(true)
				done := make(chan error, 1)
				go func() {
					if kind == "compaction" {
						_, err := env.runtime.compactSession(ctx, saved.ID)
						done <- err
						return
					}
					_, err := env.runtime.RunWithEvents(ctx, true, false, []core.Message{core.UserText("follow-up")}, env.llmReq, nil)
					done <- err
				}()
				select {
				case <-store.read:
				case err := <-done:
					t.Fatalf("request returned before admission checkpoint: %v", err)
				}
				narrowed := *saved
				narrowed.ModelPolicy = broad.With("concurrent-A", []string{"debug:allowed"})
				err = second.Update(ctx, &narrowed)
				close(store.resume)
				if err != nil {
					t.Fatal(err)
				}
				err = <-done
				if selected == "other" {
					if err == nil {
						t.Fatal("stale B-only admission was accepted after A-only commit")
					}
					if len(provider.RecordedRequests()) != 0 {
						t.Fatal("denied admission dispatched a provider request")
					}
					latest, getErr := first.Get(ctx, saved.ID)
					if getErr != nil || !latest.ModelPolicy.Allows("debug", "allowed") || len(latest.ModelPolicy.Rules) != 2 {
						t.Fatalf("denial poisoned policy: %+v %v", latest, getErr)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(provider.RecordedRequests()) == 0 {
					t.Fatal("allowed admission did not exercise provider boundary")
				}
				latest, getErr := first.Get(ctx, saved.ID)
				if getErr != nil || !reflect.DeepEqual(env.runtime.modelPolicy, latest.ModelPolicy) || len(latest.ModelPolicy.Rules) != 4 {
					t.Fatalf("runtime lost committed intersection: runtime=%+v saved=%+v err=%v", env.runtime.modelPolicy, latest, getErr)
				}
				if spawn := env.runtime.spawnRunner; spawn == nil || !reflect.DeepEqual(spawn.modelPolicyState().Policy, latest.ModelPolicy) {
					t.Fatal("child policy source did not inherit the committed admission intersection")
				}

			})
		}
	}
}
