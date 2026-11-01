package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InitAIChatSchema creates the assistant's usage log (idempotent).
// Mirrored by migrations/021_ai_chat.sql.
//
// One row per answered (or failed) turn: who asked, about which
// organization, which tools ran and what it cost in tokens. The
// conversation itself is never stored. user_id deliberately has no
// foreign key, so the log outlives a deleted account.
func InitAIChatSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ai_chat_usage (
			id              bigserial PRIMARY KEY,
			user_id         bigint NOT NULL,
			organization_id text NOT NULL,
			created_at      timestamptz NOT NULL DEFAULT now(),
			model           text NOT NULL,
			tools           text[] NOT NULL DEFAULT '{}',
			input_tokens    integer NOT NULL DEFAULT 0,
			output_tokens   integer NOT NULL DEFAULT 0,
			status          text NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS ai_chat_usage_user_created ON ai_chat_usage (user_id, created_at)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec ai chat schema: %w", err)
		}
	}
	return nil
}

// AIChatUsageRow is one assistant turn in the usage log.
type AIChatUsageRow struct {
	UserID         int64
	OrganizationID string
	Model          string
	Tools          []string
	InputTokens    int
	OutputTokens   int
	// Status is "ok", "error" or "cancelled".
	Status string
}

// InsertAIChatUsage appends one turn to the usage log.
func InsertAIChatUsage(ctx context.Context, pool *pgxpool.Pool, row AIChatUsageRow) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	tools := row.Tools
	if tools == nil {
		tools = []string{}
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO ai_chat_usage (user_id, organization_id, model, tools, input_tokens, output_tokens, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, row.UserID, row.OrganizationID, row.Model, tools, row.InputTokens, row.OutputTokens, row.Status)
	if err != nil {
		return fmt.Errorf("storage: insert ai chat usage: %w", err)
	}
	return nil
}

// CountAIChatTurnsSince counts a user's turns logged at or after since,
// whatever their status: a failed turn still spent the daily quota.
func CountAIChatTurnsSince(ctx context.Context, pool *pgxpool.Pool, userID int64, since time.Time) (int, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM ai_chat_usage WHERE user_id = $1 AND created_at >= $2
	`, userID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("storage: count ai chat turns: %w", err)
	}
	return n, nil
}
