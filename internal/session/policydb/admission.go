package policydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/samsaffron/term-llm/internal/config"
	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	"github.com/samsaffron/term-llm/internal/sqliteutil"
)

// AdmissionInput identifies the exact provider request and host-owned scope.
type AdmissionInput struct {
	Request                     core.Request
	Policy                      modelpolicy.Policy
	Config                      *config.Config
	Agent, CWD, Provider, Model string
}

// Store is the optional native capability required for private admission.
// Implementations must validate and persist against the latest durable policy
// under one writer reservation, returning only the committed intersection.
type Store interface {
	AdmitPrivateSession(context.Context, AdmissionInput) (modelpolicy.Policy, error)
}

// Admit validates identity and model before persisting any new restrictions.
// Denial rolls back, so an incompatible caller cannot poison the saved policy.
func Admit(ctx context.Context, db *sql.DB, in AdmissionInput, available bool) (modelpolicy.Policy, error) {
	if !available {
		return modelpolicy.Policy{}, fmt.Errorf("atomic private session admission is unavailable")
	}
	var effective modelpolicy.Policy
	err := sqliteutil.WithImmediateMigrationTx(ctx, db, func(tx sqliteutil.Executor) error {
		var raw sql.NullString
		var agent, cwd, worktree string
		if err := tx.QueryRow(`SELECT model_policy, COALESCE(agent, ''), COALESCE(cwd, ''), COALESCE(worktree_dir, '') FROM sessions WHERE id = ?`, in.Request.SessionID).Scan(&raw, &agent, &cwd, &worktree); err != nil {
			return fmt.Errorf("load private session admission scope: %w", err)
		}
		var saved modelpolicy.Policy
		if raw.Valid {
			if err := json.Unmarshal([]byte(raw.String), &saved); err != nil {
				return fmt.Errorf("decode private session policy: %w", err)
			}
		}
		if !saved.LockSession && !in.Policy.LockSession {
			effective = in.Policy
			return nil
		}
		if !saved.LockSession || !in.Policy.LockSession {
			return fmt.Errorf("locked session requires a locked runtime")
		}
		if in.Request.Debug || in.Request.DebugRaw {
			return fmt.Errorf("locked sessions do not support provider debug logging")
		}
		if agent != in.Agent {
			return fmt.Errorf("locked session cannot change agent")
		}
		if worktree != "" {
			cwd = worktree
		}
		if in.CWD == "" && cwd != "" {
			return fmt.Errorf("locked session workspace is unavailable")
		}
		if in.CWD != "" && filepath.Clean(in.CWD) != filepath.Clean(cwd) {
			return fmt.Errorf("locked session cannot change workspace")
		}
		effective = saved.Append(in.Policy)
		model := in.Model
		if in.Request.Model != "" {
			model = in.Request.Model
		}
		if err := effective.CheckWithConfig(in.Config, in.Agent, in.Provider, model); err != nil {
			return err
		}
		if in.Request.Resume != nil {
			continuation := in.Request.Resume.Request
			if continuation.SessionID != in.Request.SessionID {
				return fmt.Errorf("locked continuation cannot change session")
			}
			if err := effective.CheckWithConfig(in.Config, in.Agent, in.Provider, continuation.Model); err != nil {
				return err
			}
		}
		if len(effective.Rules) == len(saved.Rules) {
			return nil
		}
		_, err := tx.Exec(`UPDATE sessions SET model_policy = ? WHERE id = ?`, JSON(effective), in.Request.SessionID)
		return err
	})
	if err != nil {
		return modelpolicy.Policy{}, err
	}
	return effective, nil
}
