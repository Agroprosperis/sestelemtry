package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEmailTaken is returned when another user already has the email
// (compared case-insensitively).
var ErrEmailTaken = errors.New("storage: email already registered")

// InitAuthSchema creates the user, session and role-grant tables
// (idempotent). The API is their only reader and writer, so it owns the
// bootstrap, like the tariffs and edge schemas. Mirrored by
// migrations/017_auth.sql.
func InitAuthSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id               bigserial PRIMARY KEY,
			email            text NOT NULL,
			name             text NOT NULL DEFAULT '',
			password_hash    text NOT NULL DEFAULT '',
			disabled         boolean NOT NULL DEFAULT false,
			auth_provider    text NOT NULL DEFAULT 'local',
			external_subject text NOT NULL DEFAULT '',
			created_at       timestamptz NOT NULL DEFAULT now(),
			updated_at       timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower ON users (lower(email))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS users_external_subject
			ON users (auth_provider, external_subject) WHERE external_subject <> ''`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash   bytea PRIMARY KEY,
			user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
			created_at   timestamptz NOT NULL DEFAULT now(),
			last_seen_at timestamptz NOT NULL DEFAULT now(),
			expires_at   timestamptz NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_user ON sessions (user_id)`,
		`CREATE TABLE IF NOT EXISTS role_grants (
			user_id         bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
			role            text NOT NULL,
			organization_id text,
			created_at      timestamptz NOT NULL DEFAULT now(),
			CONSTRAINT role_grants_unique UNIQUE NULLS NOT DISTINCT (user_id, role, organization_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec auth schema: %w", err)
		}
	}
	return nil
}

// UserRow is one account. PasswordHash is empty for accounts that sign
// in through an external provider.
type UserRow struct {
	ID              int64
	Email           string
	Name            string
	PasswordHash    string
	Disabled        bool
	AuthProvider    string
	ExternalSubject string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// RoleGrantRow is one role assignment. An empty OrganizationID (NULL in
// the table) grants the role on every organization.
type RoleGrantRow struct {
	UserID         int64
	Role           string
	OrganizationID string
}

const userColumns = `id, email, name, password_hash, disabled, auth_provider, external_subject, created_at, updated_at`

func scanUser(row pgx.Row) (UserRow, error) {
	var u UserRow
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Disabled,
		&u.AuthProvider, &u.ExternalSubject, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// CountUsers returns the number of accounts, disabled ones included.
func CountUsers(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count users: %w", err)
	}
	return n, nil
}

// InsertUser creates an account together with its grants in one
// transaction, so a user never exists half-configured.
func InsertUser(ctx context.Context, pool *pgxpool.Pool, u UserRow, grants []RoleGrantRow) (int64, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	provider := u.AuthProvider
	if provider == "" {
		provider = "local"
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("storage: begin insert user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, disabled, auth_provider, external_subject)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, u.Email, u.Name, u.PasswordHash, u.Disabled, provider, u.ExternalSubject).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrEmailTaken
		}
		return 0, fmt.Errorf("storage: insert user: %w", err)
	}
	if err := insertGrants(ctx, tx, id, grants); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("storage: commit insert user: %w", err)
	}
	return id, nil
}

// GetUserByEmail looks an account up case-insensitively.
func GetUserByEmail(ctx context.Context, pool *pgxpool.Pool, email string) (UserRow, bool, error) {
	if pool == nil {
		return UserRow{}, false, fmt.Errorf("storage: nil pool")
	}
	u, err := scanUser(pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, email))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRow{}, false, nil
		}
		return UserRow{}, false, fmt.Errorf("storage: query user by email: %w", err)
	}
	return u, true, nil
}

// GetUser returns one account by id.
func GetUser(ctx context.Context, pool *pgxpool.Pool, id int64) (UserRow, bool, error) {
	if pool == nil {
		return UserRow{}, false, fmt.Errorf("storage: nil pool")
	}
	u, err := scanUser(pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRow{}, false, nil
		}
		return UserRow{}, false, fmt.Errorf("storage: query user: %w", err)
	}
	return u, true, nil
}

// ListUsers returns every account ordered by email.
func ListUsers(ctx context.Context, pool *pgxpool.Pool) ([]UserRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY lower(email)`)
	if err != nil {
		return nil, fmt.Errorf("storage: query users: %w", err)
	}
	defer rows.Close()
	out := make([]UserRow, 0, 16)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate users: %w", err)
	}
	return out, nil
}

// UserUpdate lists the fields to change; nil leaves a field as it is.
type UserUpdate struct {
	Name         *string
	Disabled     *bool
	PasswordHash *string
	// Grants, when set, replaces every grant of the user.
	Grants *[]RoleGrantRow
	// RevokeSessions signs the user out everywhere in the same
	// transaction, except for the session whose hash is KeepSession.
	RevokeSessions bool
	KeepSession    []byte
}

// UpdateUser applies upd in one transaction. The bool is false when no
// user has the id.
func UpdateUser(ctx context.Context, pool *pgxpool.Pool, id int64, upd UserUpdate) (bool, error) {
	if pool == nil {
		return false, fmt.Errorf("storage: nil pool")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("storage: begin update user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE users SET
			name          = COALESCE($2, name),
			disabled      = COALESCE($3, disabled),
			password_hash = COALESCE($4, password_hash),
			updated_at    = now()
		WHERE id = $1
	`, id, upd.Name, upd.Disabled, upd.PasswordHash)
	if err != nil {
		return false, fmt.Errorf("storage: update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if upd.Grants != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM role_grants WHERE user_id = $1`, id); err != nil {
			return false, fmt.Errorf("storage: clear grants: %w", err)
		}
		if err := insertGrants(ctx, tx, id, *upd.Grants); err != nil {
			return false, err
		}
	}
	if upd.RevokeSessions {
		if _, err := tx.Exec(ctx, `
			DELETE FROM sessions
			WHERE user_id = $1 AND ($2::bytea IS NULL OR token_hash <> $2)
		`, id, upd.KeepSession); err != nil {
			return false, fmt.Errorf("storage: revoke sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("storage: commit update user: %w", err)
	}
	return true, nil
}

func insertGrants(ctx context.Context, tx pgx.Tx, userID int64, grants []RoleGrantRow) error {
	for _, g := range grants {
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_grants (user_id, role, organization_id)
			VALUES ($1, $2, NULLIF($3, ''))
			ON CONFLICT DO NOTHING
		`, userID, g.Role, g.OrganizationID); err != nil {
			return fmt.Errorf("storage: insert grant: %w", err)
		}
	}
	return nil
}

// ListRoleGrants returns the grants of one user.
func ListRoleGrants(ctx context.Context, pool *pgxpool.Pool, userID int64) ([]RoleGrantRow, error) {
	return queryGrants(ctx, pool, `WHERE user_id = $1`, userID)
}

// ListAllRoleGrants returns the grants of every user.
func ListAllRoleGrants(ctx context.Context, pool *pgxpool.Pool) ([]RoleGrantRow, error) {
	return queryGrants(ctx, pool, ``)
}

func queryGrants(ctx context.Context, pool *pgxpool.Pool, where string, args ...any) ([]RoleGrantRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT user_id, role, COALESCE(organization_id, '')
		FROM role_grants `+where+`
		ORDER BY user_id, role, organization_id NULLS FIRST
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: query grants: %w", err)
	}
	defer rows.Close()
	out := make([]RoleGrantRow, 0, 8)
	for rows.Next() {
		var g RoleGrantRow
		if err := rows.Scan(&g.UserID, &g.Role, &g.OrganizationID); err != nil {
			return nil, fmt.Errorf("storage: scan grant: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate grants: %w", err)
	}
	return out, nil
}

// SessionRow is the server side of a session cookie.
type SessionRow struct {
	UserID     int64
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// InsertSession stores a new session under the hash of its cookie
// secret.
func InsertSession(ctx context.Context, pool *pgxpool.Pool, tokenHash []byte, userID int64, expiresAt time.Time) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
	`, tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("storage: insert session: %w", err)
	}
	return nil
}

// GetSession looks a session up by the hash of its cookie secret.
func GetSession(ctx context.Context, pool *pgxpool.Pool, tokenHash []byte) (SessionRow, bool, error) {
	if pool == nil {
		return SessionRow{}, false, fmt.Errorf("storage: nil pool")
	}
	var s SessionRow
	err := pool.QueryRow(ctx, `
		SELECT user_id, expires_at, last_seen_at FROM sessions WHERE token_hash = $1
	`, tokenHash).Scan(&s.UserID, &s.ExpiresAt, &s.LastSeenAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionRow{}, false, nil
		}
		return SessionRow{}, false, fmt.Errorf("storage: query session: %w", err)
	}
	return s, true, nil
}

// TouchSession records activity and moves the expiry forward.
func TouchSession(ctx context.Context, pool *pgxpool.Pool, tokenHash []byte, lastSeen, expiresAt time.Time) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE sessions SET last_seen_at = $2, expires_at = $3 WHERE token_hash = $1
	`, tokenHash, lastSeen, expiresAt); err != nil {
		return fmt.Errorf("storage: touch session: %w", err)
	}
	return nil
}

// DeleteSession ends one session.
func DeleteSession(ctx context.Context, pool *pgxpool.Pool, tokenHash []byte) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("storage: delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions drops sessions whose expiry is before now.
func DeleteExpiredSessions(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1`, now); err != nil {
		return fmt.Errorf("storage: delete expired sessions: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
