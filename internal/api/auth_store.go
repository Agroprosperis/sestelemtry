package api

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/storage"
)

// userUpdate lists the account fields to change; nil leaves a field
// as it is.
type userUpdate struct {
	Name               *string
	Disabled           *bool
	PasswordHash       *string
	MustChangePassword *bool
	Grants             *[]auth.Grant
	// RevokeSessions signs the user out everywhere except the session
	// whose token hash is KeepSession.
	RevokeSessions bool
	KeepSession    []byte
}

// authStore is the persistence the auth layer needs. *AuthStore
// implements it on top of internal/storage.
type authStore interface {
	CountUsers(ctx context.Context) (int, error)
	CreateUser(ctx context.Context, u storage.UserRow, grants []auth.Grant) (int64, error)
	UserByEmail(ctx context.Context, email string) (storage.UserRow, bool, error)
	UserByID(ctx context.Context, id int64) (storage.UserRow, bool, error)
	ListUsers(ctx context.Context) ([]storage.UserRow, error)
	UserGrants(ctx context.Context, userID int64) ([]auth.Grant, error)
	AllGrants(ctx context.Context) (map[int64][]auth.Grant, error)
	UpdateUser(ctx context.Context, id int64, upd userUpdate) (bool, error)
	CreateSession(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error
	Session(ctx context.Context, tokenHash []byte) (storage.SessionRow, bool, error)
	TouchSession(ctx context.Context, tokenHash []byte, lastSeen, expiresAt time.Time) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

// AuthStore adapts the storage package's account tables to the auth
// layer.
type AuthStore struct {
	pool *pgxpool.Pool
}

func NewAuthStore(pool *pgxpool.Pool) *AuthStore {
	return &AuthStore{pool: pool}
}

func (s *AuthStore) CountUsers(ctx context.Context) (int, error) {
	return storage.CountUsers(ctx, s.pool)
}

func (s *AuthStore) CreateUser(ctx context.Context, u storage.UserRow, grants []auth.Grant) (int64, error) {
	return storage.InsertUser(ctx, s.pool, u, grantRows(grants))
}

func (s *AuthStore) UserByEmail(ctx context.Context, email string) (storage.UserRow, bool, error) {
	return storage.GetUserByEmail(ctx, s.pool, email)
}

func (s *AuthStore) UserByID(ctx context.Context, id int64) (storage.UserRow, bool, error) {
	return storage.GetUser(ctx, s.pool, id)
}

func (s *AuthStore) ListUsers(ctx context.Context) ([]storage.UserRow, error) {
	return storage.ListUsers(ctx, s.pool)
}

func (s *AuthStore) UserGrants(ctx context.Context, userID int64) ([]auth.Grant, error) {
	rows, err := storage.ListRoleGrants(ctx, s.pool, userID)
	if err != nil {
		return nil, err
	}
	out := make([]auth.Grant, 0, len(rows))
	for _, r := range rows {
		out = append(out, auth.Grant{Role: auth.Role(r.Role), OrganizationID: r.OrganizationID})
	}
	return out, nil
}

func (s *AuthStore) AllGrants(ctx context.Context) (map[int64][]auth.Grant, error) {
	rows, err := storage.ListAllRoleGrants(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]auth.Grant)
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], auth.Grant{Role: auth.Role(r.Role), OrganizationID: r.OrganizationID})
	}
	return out, nil
}

func (s *AuthStore) UpdateUser(ctx context.Context, id int64, upd userUpdate) (bool, error) {
	su := storage.UserUpdate{
		Name:               upd.Name,
		Disabled:           upd.Disabled,
		PasswordHash:       upd.PasswordHash,
		MustChangePassword: upd.MustChangePassword,
		RevokeSessions:     upd.RevokeSessions,
		KeepSession:        upd.KeepSession,
	}
	if upd.Grants != nil {
		rows := grantRows(*upd.Grants)
		su.Grants = &rows
	}
	return storage.UpdateUser(ctx, s.pool, id, su)
}

func (s *AuthStore) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error {
	return storage.InsertSession(ctx, s.pool, tokenHash, userID, expiresAt)
}

func (s *AuthStore) Session(ctx context.Context, tokenHash []byte) (storage.SessionRow, bool, error) {
	return storage.GetSession(ctx, s.pool, tokenHash)
}

func (s *AuthStore) TouchSession(ctx context.Context, tokenHash []byte, lastSeen, expiresAt time.Time) error {
	return storage.TouchSession(ctx, s.pool, tokenHash, lastSeen, expiresAt)
}

func (s *AuthStore) DeleteSession(ctx context.Context, tokenHash []byte) error {
	return storage.DeleteSession(ctx, s.pool, tokenHash)
}

func (s *AuthStore) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return storage.DeleteExpiredSessions(ctx, s.pool, now)
}

func grantRows(grants []auth.Grant) []storage.RoleGrantRow {
	out := make([]storage.RoleGrantRow, 0, len(grants))
	for _, g := range grants {
		out = append(out, storage.RoleGrantRow{Role: string(g.Role), OrganizationID: g.OrganizationID})
	}
	return out
}
