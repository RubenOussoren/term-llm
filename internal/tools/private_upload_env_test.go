package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/agents"
	"github.com/samsaffron/term-llm/internal/llm/core"
)

func TestCustomScriptTrustedUploadEnvironment(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	for _, key := range []string{"TERM_LLM_SESSION_ID", "TERM_LLM_UPLOADS_JSON", "TERM_LLM_PRIVATE_SESSION"} {
		t.Setenv(key, "inherited-forgery")
	}
	root := filepath.Join(os.Getenv("XDG_DATA_HOME"), "term-llm", "uploads")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(root, "own.bin")
	if err := os.WriteFile(own, []byte("invented"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeScript(t, dir, "env.sh", "#!/bin/sh\nprintf '%s\\n%s\\n%s' \"$TERM_LLM_SESSION_ID\" \"$TERM_LLM_UPLOADS_JSON\" \"$TERM_LLM_PRIVATE_SESSION\"\n")
	tool := makeCustomTool(t, dir, agents.CustomToolDef{Name: "context_probe", Script: "env.sh", Call: "json", Env: map[string]string{"TERM_LLM_SESSION_ID": "tool-forgery", "TERM_LLM_UPLOADS_JSON": "tool-forgery", "TERM_LLM_PRIVATE_SESSION": "1"}})
	ctx := core.ContextWithSessionID(context.Background(), "selected-session")
	ctx = core.ContextWithUserUploads(ctx, "selected-session", []core.Message{{Role: core.RoleUser, Parts: []core.Part{{Type: core.PartFile, FilePath: own, FileData: &core.ToolFileData{Filename: "own.bin"}}}}})
	for _, locked := range []bool{false, true} {
		callCtx := ctx
		if locked {
			callCtx = core.ContextWithPrivateSession(callCtx, "selected-session")
		}
		output, err := tool.Execute(callCtx, json.RawMessage(`{"session_id":"argument-forgery","path":"/not/an/upload"}`))
		if err != nil || output.IsError {
			t.Fatalf("execution: %v %+v", err, output)
		}
		text := output.Content
		if !strings.Contains(text, "selected-session") || !strings.Contains(text, own) || strings.Contains(text, "forgery") {
			t.Fatalf("unexpected env: %s", text)
		}
		lines := strings.Split(strings.TrimSpace(text), "\n")
		// Tool output is formatted as a shell result, so assert the private marker
		// in the raw environment helper independently of presentation.
		_ = lines
		env := strings.Join(core.CustomToolEnvironment(callCtx, nil), "\n")
		want := "TERM_LLM_PRIVATE_SESSION="
		if locked {
			want += "1"
		}
		if !strings.HasSuffix(env, want) {
			t.Fatalf("private marker: %s", env)
		}
	}
	foreign := core.ContextWithSessionID(ctx, "other-session")
	env := strings.Join(core.CustomToolEnvironment(foreign, nil), "\n")
	if !strings.Contains(env, "TERM_LLM_UPLOADS_JSON=[]") {
		t.Fatal("references crossed sessions")
	}
}
