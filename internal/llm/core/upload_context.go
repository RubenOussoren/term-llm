package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/samsaffron/term-llm/internal/appdata"
)

// ContextWithRequestSession binds an explicit request identity, while legacy
// callers which omit it retain their trusted context identity (e.g. agent tools).
func ContextWithRequestSession(ctx context.Context, sessionID string) (context.Context, string) {
	if sessionID == "" {
		return ctx, SessionIDFromContext(ctx)
	}
	return ContextWithSessionID(ctx, sessionID), sessionID
}

// UploadReference is metadata only. Hosts must reopen beneath their fixed upload
// root and validate the descriptor before reading; this is not a file capability.
type UploadReference struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	SizeBytes int64  `json:"size_bytes"`
}

type uploadContextKey struct{}
type uploadContext struct{ sessionID, metadata string }

// ContextWithUserUploads binds a bounded snapshot of structured user file parts
// to the active session. Approval-only prefixes and assistant/tool output must
// never be supplied here. Missing files, symlinks and bare FilePath mentions do
// not constitute uploads. No file content is read.
func ContextWithUserUploads(ctx context.Context, sessionID string, messages []Message) context.Context {
	refs := make([]UploadReference, 0)
	dataDir, err := appdata.GetDataDir()
	if err == nil && sessionID != "" {
		root := filepath.Join(dataDir, "uploads")
		seen := map[string]bool{}
		for _, msg := range messages {
			if msg.Role != RoleUser {
				continue
			}
			for _, part := range msg.Parts {
				if len(refs) == 32 {
					break
				}
				if part.Type != PartFile || part.FileData == nil || !filepath.IsAbs(part.FilePath) {
					continue
				}
				if len(part.FileData.Filename)+len(part.FileData.MediaType)+len(part.FilePath) > 64<<10 {
					continue
				}
				path := filepath.Clean(part.FilePath)
				rel, err := filepath.Rel(root, path)
				if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || seen[path] {
					continue
				}
				// Reject every symlink component, including the upload root itself.
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil || resolved != path {
					continue
				}
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				sum := sha256.Sum256([]byte(path))
				ref := UploadReference{ID: hex.EncodeToString(sum[:16]), Path: path, Filename: part.FileData.Filename, MediaType: part.FileData.MediaType, SizeBytes: info.Size()}
				candidate := append(refs, ref)
				encoded, err := json.Marshal(candidate)
				if err != nil || len(encoded) > 64<<10 {
					continue
				}
				refs = candidate
				seen[path] = true
			}
		}
	}
	encoded, _ := json.Marshal(refs)
	return context.WithValue(ctx, uploadContextKey{}, uploadContext{sessionID, string(encoded)})
}

// UserUploadsJSONFromContext returns [] unless the trusted snapshot belongs to
// this exact active session. It never consults tool arguments or process env.
func UserUploadsJSONFromContext(ctx context.Context) string {
	v, ok := ctx.Value(uploadContextKey{}).(uploadContext)
	if !ok || v.sessionID == "" || v.sessionID != SessionIDFromContext(ctx) {
		return "[]"
	}
	return v.metadata
}

// PrivateSession context is installed only after durable host scope admission.
type privateSessionKey struct{}

func ContextWithPrivateSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, privateSessionKey{}, sessionID)
}
func PrivateSessionFromContext(ctx context.Context) bool {
	id, _ := ctx.Value(privateSessionKey{}).(string)
	return id != "" && id == SessionIDFromContext(ctx)
}

// Reserved host context is never inherited or chosen by a tool definition.
func CustomToolEnvironment(ctx context.Context, env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM_LLM_SESSION_ID", "TERM_LLM_UPLOADS_JSON", "TERM_LLM_PRIVATE_SESSION":
			continue
		}
		out = append(out, entry)
	}
	private := ""
	if PrivateSessionFromContext(ctx) {
		private = "1"
	}
	return append(out, "TERM_LLM_SESSION_ID="+SessionIDFromContext(ctx), "TERM_LLM_UPLOADS_JSON="+UserUploadsJSONFromContext(ctx), "TERM_LLM_PRIVATE_SESSION="+private)
}
