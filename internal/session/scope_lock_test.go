package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
)

func TestLockedSessionScopePersistenceAndBranch(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(Config{Enabled: true, Path: filepath.Join(t.TempDir(), "sessions.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	policy := modelpolicy.Policy{}.With("private", []string{"debug:allowed"}).With("parent", []string{"debug:*"})
	policy.LockSession = true
	saved := &Session{ID: NewID(), Agent: "private", CWD: "/invented/project", Provider: "debug", ProviderKey: "debug", Model: "allowed", ModelPolicy: policy}
	if err := store.Create(ctx, saved); err != nil {
		t.Fatal(err)
	}
	msg := NewMessage(saved.ID, core.UserText("invented fixture"), -1)
	if err := store.AddMessage(ctx, saved.ID, msg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Session)
	}{
		{"agent", func(s *Session) { s.Agent = "coding" }}, {"workspace", func(s *Session) { s.CWD = "/other" }}, {"worktree", func(s *Session) { s.WorktreeDir = "/other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *saved
			tc.mutate(&next)
			if err := store.Update(ctx, &next); err == nil {
				t.Fatal("scope retarget accepted")
			}
		})
	}
	next := *saved
	next.ModelPolicy = modelpolicy.Policy{}
	next.Name = "rename permitted"
	if err := store.Update(ctx, &next); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, saved.ID)
	if err != nil || !got.ModelPolicy.LockSession || len(got.ModelPolicy.Rules) != 2 {
		t.Fatalf("sticky policy lost: %+v %v", got, err)
	}
	if err := store.AssignSessionProject(ctx, saved.ID, "other", saved.CWD, ""); err == nil {
		t.Fatal("project retarget accepted")
	}
	if _, err := store.BindSessionWorkspace(ctx, saved.ID, SessionWorkspaceBinding{ProjectID: "other", CWD: "/other"}); err == nil {
		t.Fatal("workspace rebind accepted")
	}
	if _, err := store.SwitchSessionWorkspace(ctx, saved.ID, SessionWorkspaceBinding{ProjectID: "other", CWD: "/other"}); err == nil {
		t.Fatal("workspace switch accepted")
	}
	branch, err := store.CreateBranch(ctx, saved.ID, CreateBranchOptions{AnchorMessageID: msg.ID})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Get(ctx, branch.Session.ID)
	if err != nil || !child.ModelPolicy.LockSession || len(child.ModelPolicy.Rules) != 2 || child.Agent != saved.Agent || child.CWD != saved.CWD {
		t.Fatalf("branch scope: %+v %v", child, err)
	}
	child.Agent = "coding"
	if err := store.Update(ctx, child); err == nil {
		t.Fatal("branch retarget accepted")
	}
}
