package engine

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/llm/mockprovider"
)

type uploadProbeTool struct{ got string }

func (*uploadProbeTool) Preview(json.RawMessage) string { return "" }

func (p *uploadProbeTool) Spec() core.ToolSpec {
	return core.ToolSpec{Name: "upload_probe", Description: "fixture", Schema: map[string]any{"type": "object"}}
}
func (p *uploadProbeTool) Execute(ctx context.Context, _ json.RawMessage) (core.ToolOutput, error) {
	p.got = core.UserUploadsJSONFromContext(ctx)
	return core.TextOutput("fixture"), nil
}
func TestEngineUploadContextExcludesApprovalPrefix(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	root := filepath.Join(os.Getenv("XDG_DATA_HOME"), "term-llm", "uploads")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	part := func(name string) core.Part {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("invented"), 0600); err != nil {
			t.Fatal(err)
		}
		return core.Part{Type: core.PartFile, FilePath: path, FileData: &core.ToolFileData{Filename: name}}
	}
	own, foreign := part("own.bin"), part("foreign.bin")
	for _, inline := range []bool{false, true} {
		t.Run(map[bool]string{false: "api", true: "inline"}[inline], func(t *testing.T) {
			provider := mockprovider.NewMockProvider("mock").AddTurn(mockprovider.MockTurn{InlineTools: inline, ToolCalls: []core.ToolCall{{ID: "call", Name: "upload_probe", Arguments: json.RawMessage(`{}`)}}}).AddTextResponse("done")
			probe := &uploadProbeTool{}
			e := NewEngine(provider, NewToolRegistry())
			e.RegisterTool(probe)
			stream, err := e.Stream(context.Background(), core.Request{SessionID: "own", Messages: []core.Message{{Role: core.RoleUser, Parts: []core.Part{own}}, {Role: core.RoleAssistant, Parts: []core.Part{foreign}}}, ApprovalTranscriptPrefix: []core.Message{{Role: core.RoleUser, Parts: []core.Part{foreign}}}, Tools: []core.ToolSpec{probe.Spec()}, MaxTurns: 3})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			for {
				_, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if !strings.Contains(probe.got, own.FilePath) || strings.Contains(probe.got, foreign.FilePath) {
				t.Fatalf("tool context: %s", probe.got)
			}
		})
	}
}
