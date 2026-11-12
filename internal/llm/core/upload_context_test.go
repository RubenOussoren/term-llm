package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestSessionPreservesInheritedIdentityWhenOmitted(t *testing.T) {
	ctx := ContextWithSessionID(context.Background(), "trusted-parent")
	inherited, id := ContextWithRequestSession(ctx, "")
	if id != "trusted-parent" || SessionIDFromContext(inherited) != id {
		t.Fatal("omitted request erased trusted agent lifecycle identity")
	}
	explicit, id := ContextWithRequestSession(ctx, "explicit-request")
	if id != "explicit-request" || SessionIDFromContext(explicit) != id {
		t.Fatal("explicit request did not bind its own identity")
	}
}

func uploadFixture(t *testing.T, name string) Part {
	t.Helper()
	root := filepath.Join(os.Getenv("XDG_DATA_HOME"), "term-llm", "uploads")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("invented fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return Part{Type: PartFile, FilePath: path, FileData: &ToolFileData{Filename: name, MediaType: "application/octet-stream", SizeBytes: 999, Base64: "NEVER_IN_ENV"}}
}
func TestUserUploadContextAdmission(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	own := uploadFixture(t, "own.bin")
	sibling := uploadFixture(t, "other.bin")
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := uploadFixture(t, "link.bin")
	if err := os.Remove(link.FilePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(own.FilePath, link.FilePath); err != nil {
		t.Fatal(err)
	}
	messages := []Message{
		{Role: RoleUser, Parts: []Part{own, own, {Type: PartFile, FilePath: sibling.FilePath}, {Type: PartText, Text: sibling.FilePath}, {Type: PartFile, FilePath: outside, FileData: own.FileData}, link}},
		{Role: RoleAssistant, Parts: []Part{sibling}}, {Role: RoleTool, Parts: []Part{sibling}},
	}
	ctx := ContextWithSessionID(context.Background(), "own-session")
	ctx = ContextWithUserUploads(ctx, "own-session", messages)
	raw := UserUploadsJSONFromContext(ctx)
	var refs []UploadReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Path != own.FilePath || refs[0].SizeBytes != 16 || len(refs[0].ID) != 32 {
		t.Fatalf("unexpected references: %+v", refs)
	}
	if strings.Contains(raw, "NEVER_IN_ENV") {
		t.Fatal("file bytes in metadata")
	}
	if got := UserUploadsJSONFromContext(ContextWithSessionID(ctx, "foreign")); got != "[]" {
		t.Fatal("foreign session received references")
	}
	if got := UserUploadsJSONFromContext(ContextWithUserUploads(ctx, "", messages)); got != "[]" {
		t.Fatal("unbound references admitted")
	}
}
func TestUserUploadContextLimits(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var parts []Part
	for i := 0; i < 40; i++ {
		parts = append(parts, uploadFixture(t, fmt.Sprintf("%d.bin", i)))
	}
	ctx := ContextWithSessionID(context.Background(), "s")
	raw := UserUploadsJSONFromContext(ContextWithUserUploads(ctx, "s", []Message{{Role: RoleUser, Parts: parts}}))
	var refs []UploadReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs) != 32 || len(raw) > 64<<10 {
		t.Fatalf("limits: count=%d bytes=%d", len(refs), len(raw))
	}
	parts[0].FileData.Filename = strings.Repeat("x", 64<<10)
	raw = UserUploadsJSONFromContext(ContextWithUserUploads(ctx, "s", []Message{{Role: RoleUser, Parts: parts[:1]}}))
	if raw != "[]" {
		t.Fatal("oversized metadata admitted")
	}
}
