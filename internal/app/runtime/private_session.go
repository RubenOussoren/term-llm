package runtime

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/samsaffron/term-llm/internal/config"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
	runpkg "github.com/samsaffron/term-llm/internal/run"
	"github.com/samsaffron/term-llm/internal/session"
	"github.com/samsaffron/term-llm/internal/session/policydb"
)

func LockedModelPolicy(policy modelpolicy.Policy) modelpolicy.Policy {
	if policy.LockSession {
		return policy
	}
	return modelpolicy.Policy{}
}

// AdmitPrivateSession runs before history projection or provider dispatch. Only
// durable, identity-checked locked sessions receive the host-only marker.
func AdmitPrivateSession(ctx context.Context, store session.Store, req core.Request, policy modelpolicy.Policy, cfg *config.Config, agent, cwd, provider, model string) (context.Context, modelpolicy.Policy, error) {
	if store == nil || req.SessionID == "" {
		if policy.LockSession {
			return ctx, policy, fmt.Errorf("locked session requires durable session storage")
		}
		return ctx, policy, nil
	}
	saved, lookupErr := store.Get(ctx, req.SessionID)
	// The initial read only discovers unsupported-store locks. Native stores
	// always re-read and validate under their SQLite writer reservation, even if
	// this snapshot is unlocked. No admission decision uses its stale policy.
	nativeStore := store
	for {
		logging, ok := nativeStore.(*session.LoggingStore)
		if !ok {
			break
		}
		nativeStore = logging.Store
	}
	atomic, supported := nativeStore.(policydb.Store)
	if !supported {
		if lookupErr != nil || saved == nil {
			return ctx, policy, fmt.Errorf("session scope unavailable for atomic admission")
		}
		if saved.ModelPolicy.LockSession || policy.LockSession {
			return ctx, policy, fmt.Errorf("locked session requires atomic native admission storage")
		}
		return ctx, policy, nil
	}
	effective, err := atomic.AdmitPrivateSession(ctx, policydb.AdmissionInput{Request: req, Policy: policy, Config: cfg, Agent: agent, CWD: cwd, Provider: provider, Model: model})
	if err != nil {
		return ctx, policy, err
	}
	if effective.LockSession {
		ctx = core.ContextWithPrivateSession(ctx, req.SessionID)
	}
	return ctx, effective, nil
}

// CheckSessionScope rejects retargeting a locked transcript before history is loaded.
// Empty request fields mean reuse the saved identity, never choose a new one.
func CheckSessionScope(s *session.Session, agent, cwd string) error {
	if s == nil || !s.ModelPolicy.LockSession {
		return nil
	}
	if agent != "" && agent != s.Agent {
		return fmt.Errorf("locked session cannot change agent")
	}
	saved := s.CWD
	if s.WorktreeDir != "" {
		saved = s.WorktreeDir
	}
	if cwd != "" && filepath.Clean(cwd) != filepath.Clean(saved) {
		return fmt.Errorf("locked session cannot change workspace")
	}
	return nil
}

// CheckLockedRun closes the engine checkpoint's inner model/session
// override before provider state import or any history replay.
func CheckLockedRun(policy modelpolicy.Policy, cfg *config.Config, req runpkg.Request, selected ModelSelection, debug bool) error {
	if !policy.LockSession {
		return nil
	}
	if debug || req.Debug || req.DebugRaw {
		return fmt.Errorf("locked sessions do not support provider debug logging")
	}
	if req.Progressive != nil {
		return fmt.Errorf("locked sessions do not support progressive runs")
	}
	if req.Continuation == nil {
		return nil
	}
	saved := req.Continuation.Request
	if saved.SessionID != req.SessionID {
		return fmt.Errorf("locked continuation cannot change session")
	}
	return policy.CheckWithConfig(cfg, req.AgentName, selected.Provider, saved.Model)
}

func PersistedRunPolicy(inherited, effective modelpolicy.Policy) modelpolicy.Policy {
	if effective.LockSession {
		return effective
	}
	return inherited
}

// PrivateSessionExecutionContext transfers admission into a separately owned
// execution context without replacing its cancellation, deadline or other values.
func PrivateSessionExecutionContext(ctx, admitted context.Context, sessionID string) context.Context {
	ctx = core.ContextWithSessionID(ctx, sessionID)
	if core.PrivateSessionFromContext(admitted) && core.SessionIDFromContext(admitted) == sessionID {
		ctx = core.ContextWithPrivateSession(ctx, sessionID)
	}
	return ctx
}
