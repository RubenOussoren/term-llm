package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/llm/mockprovider"
	runpkg "github.com/samsaffron/term-llm/internal/run"
)

func TestLockedRunnerResumePersistsNarrowedAgentPolicy(t *testing.T) {
	ctx := context.Background()
	cfg, agent := modelAllowlistFixture(t, "debug:allowed, debug:other")
	writeAgent := func(allowed string) {
		t.Helper()
		body := fmt.Sprintf("name: reviewer\nprovider: debug\nmodel: allowed\nallowed_models: [%s]\nlock_session: true\n", allowed)
		if err := os.WriteFile(filepath.Join(agent, "agent.yaml"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeAgent("debug:allowed, debug:other")
	store := reviewSessionStore(t)
	runner := &cmdRunner{baseCfg: cfg, defaults: cmdRunnerOptions{Store: store}}
	env, err := runner.prepare(ctx, runpkg.Request{AgentName: agent, Cwd: t.TempDir(), Platform: runpkg.PlatformJob, Persist: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, cwd := env.req.SessionID, env.req.Cwd
	env.Close()
	stale, err := store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	writeAgent("debug:allowed")
	_, err = runner.Run(ctx, runpkg.Request{SessionID: id, Resume: true, AgentName: agent, Cwd: cwd, Platform: runpkg.PlatformJob, Persist: true, ProviderInstance: mockprovider.NewMockProvider("mock").AddTextResponse("admitted narrow resume"), Messages: []core.Message{core.UserText("resume")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A stale runtime update and a widened definition must not erase the
	// restriction captured by the intervening narrowed-agent resume.
	if err := store.Update(ctx, stale); err != nil {
		t.Fatal(err)
	}
	writeAgent("debug:allowed, debug:other")
	provider := mockprovider.NewMockProvider("wrong").AddTextResponse("must not replay")
	_, err = runner.Run(ctx, runpkg.Request{SessionID: id, Resume: true, AgentName: agent, Cwd: cwd, Provider: "debug:other", ProviderInstance: provider, Messages: []core.Message{core.UserText("follow-up")}}, nil)
	if err == nil {
		t.Fatal("widened agent escaped persisted narrowed policy")
	}
	if len(provider.RecordedRequests()) != 0 {
		t.Fatal("denied resume reached provider")
	}
	saved, err := store.Get(ctx, id)
	if err != nil || saved.ModelPolicy.Allows("debug", "other") {
		t.Fatalf("narrowed policy not durable: %+v %v", saved, err)
	}
}
