// Package policydb persists locked session policy intersections atomically.
package policydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/samsaffron/term-llm/internal/modelpolicy"
)

// Update serializes policy intersection with the metadata write.
// A deferred read transaction is insufficient: two writers could both read the
// broad policy before either persists a restriction. BEGIN IMMEDIATE takes the
// SQLite writer reservation before reading, including across store instances.
// The query's final arguments must be policy, id, and five identity guard values.
func Update(ctx context.Context, db *sql.DB, query string, args []any, id string, incoming modelpolicy.Policy, available bool) (sql.Result, error) {
	if !available {
		return db.ExecContext(ctx, query, args...)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
	var raw sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT model_policy FROM sessions WHERE id = ?", id).Scan(&raw); err != nil {
		return nil, err
	}
	var saved modelpolicy.Policy
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &saved); err != nil {
			return nil, fmt.Errorf("decode persisted model policy: %w", err)
		}
	}
	if saved.LockSession {
		// Rules are an ordered intersection, not a replacement allowlist. Keep the
		// durable lock even when a stale caller supplies an empty/unlocked policy.
		// Update's final arguments are policy, id, and five identity guard values.
		args[len(args)-7] = JSON(saved.Append(incoming))
	}
	result, err := conn.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	return result, nil
}

// JSON encodes a session policy, preserving a lock even without model rules.
func JSON(policy modelpolicy.Policy) any {
	if !policy.Restricted() && !policy.LockSession {
		return nil
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return nil
	}
	return string(encoded)
}
