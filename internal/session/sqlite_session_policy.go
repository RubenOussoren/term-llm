package session

import (
	"context"

	"github.com/samsaffron/term-llm/internal/modelpolicy"
	"github.com/samsaffron/term-llm/internal/session/policydb"
)

// AdmitPrivateSession validates and persists private admission atomically.
func (s *SQLiteStore) AdmitPrivateSession(ctx context.Context, in policydb.AdmissionInput) (modelpolicy.Policy, error) {
	return policydb.Admit(ctx, s.db, in, s.hasModelPolicy)
}
