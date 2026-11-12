package serve

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/llm/mockprovider"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	runpkg "github.com/samsaffron/term-llm/internal/run"
	"github.com/samsaffron/term-llm/internal/session"
)

func TestLockedRunnerPersistsEffectivePolicyAndRejectsReplayRetarget(t *testing.T) {
	cfg, agent := modelAllowlistFixture(t, "debug:allowed")
	path := filepath.Join(agent, "agent.yaml")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteString("lock_session: true\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	store := reviewSessionStore(t)
	runner := &cmdRunner{baseCfg: cfg, defaults: cmdRunnerOptions{Store: store}}
	env, err := runner.prepare(context.Background(), runpkg.Request{AgentName: agent, Cwd: t.TempDir(), Platform: runpkg.PlatformJob, Persist: true, ModelPolicy: modelpolicy.Policy{}.With("parent", []string{"debug:*"})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	saved, err := store.Get(context.Background(), env.req.SessionID)
	if err != nil || !saved.ModelPolicy.LockSession || len(saved.ModelPolicy.Rules) != 2 {
		t.Fatalf("effective policy not durable: %+v %v", saved, err)
	}
	anchor := session.NewMessage(saved.ID, core.UserText("invented sensitive fixture"), -1)
	if err := store.AddMessage(context.Background(), saved.ID, anchor); err != nil {
		t.Fatal(err)
	}
	branch, err := store.CreateBranch(context.Background(), saved.ID, session.CreateBranchOptions{AnchorMessageID: anchor.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{saved.ID, branch.Session.ID} {
		for _, tc := range []struct{ name, agent, provider, cwd string }{
			{"provider", agent, "other:denied", saved.CWD}, {"agent", "coding", "debug:allowed", saved.CWD}, {"workspace", agent, "debug:allowed", t.TempDir()},
		} {
			t.Run(tc.name+id, func(t *testing.T) {
				wrong := mockprovider.NewMockProvider("wrong").AddTextResponse("must not run")
				_, err := runner.Run(context.Background(), runpkg.Request{SessionID: id, Resume: true, AgentName: tc.agent, Cwd: tc.cwd, Provider: tc.provider, ProviderInstance: wrong, Messages: []core.Message{core.UserText("invented follow-up")}}, nil)
				if err == nil {
					t.Fatal("locked replay retarget accepted")
				}
				if len(wrong.RecordedRequests()) != 0 {
					t.Fatal("history reached wrong provider")
				}
			})
		}
	}
	// Removing the agent flag or widening its current policy must not erase the
	// session's captured own rule, including on branches.
	if err := os.WriteFile(path, []byte("name: reviewer\nprovider: other\nmodel: denied\nallowed_models: [other:*]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := runner.resolveRunModel(context.Background(), runpkg.Request{SessionID: saved.ID, Resume: true, Provider: "other:denied"}); err == nil {
		t.Fatal("loosened definition escaped saved policy")
	}
}

func TestLockedCompactionRejectsWrongProviderAndKeepsPolicy(t *testing.T) {
	provider := mockprovider.NewMockProvider("mock").AddTextResponse("invented compacted brief")
	rt, _ := newServeCompactionTestRuntime(t, provider)
	policy := modelpolicy.Policy{}.With("private", []string{"mock:mock-model"})
	policy.LockSession = true
	rt.modelPolicy, rt.sessionMeta.ModelPolicy = policy, policy
	if err := rt.store.Update(context.Background(), rt.sessionMeta); err != nil {
		t.Fatal(err)
	}
	rt.providerKey = "wrong"
	if _, err := rt.compactSession(context.Background(), rt.sessionMeta.ID); err == nil {
		t.Fatal("wrong-provider compaction admitted")
	}
	if len(provider.RecordedRequests()) != 0 {
		t.Fatal("history reached provider before compaction policy check")
	}
	rt.providerKey = "mock"
	if _, err := rt.compactSession(context.Background(), rt.sessionMeta.ID); err != nil {
		t.Fatal(err)
	}
	requests := provider.RecordedRequests()
	if len(requests) == 0 || requests[0].Model != "mock-model" {
		t.Fatalf("compaction used wrong model: %+v", requests)
	}
	saved, err := rt.store.Get(context.Background(), rt.sessionMeta.ID)
	if err != nil || !saved.ModelPolicy.LockSession || len(saved.ModelPolicy.Rules) != 1 {
		t.Fatalf("compaction lost policy: %+v %v", saved, err)
	}
}
