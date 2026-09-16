package agentdb

// connection_credentials.go — the one place a connection credential is stored
// (design/2026-09-11-project-connections.md, addendum "Connect Google button",
// decisions A1, A9, T17).
//
// The original connections design kept every credential out of the database.
// The addendum amends that for exactly one case: a Google refresh token
// obtained by an operator pressing Connect Google. It is stored here ONLY as
// AES-256-GCM ciphertext sealed by go/connections.Sealer under a key that lives
// in agentd's environment, so a database backup on its own is useless. This
// package never sees the key and never decrypts anything: it stores and
// returns opaque bytes.
//
// Connect and disconnect are configuration decisions and go through the config
// log like every other one — but the log is the one table in this database that
// is copied everywhere (the changelog, `config.changed` events, MCP history
// tools). So the payload is built from connectionCredentialEvent, an explicit
// METADATA struct, and never from the row: a column added to the row later
// cannot leak into the log by being forgotten. This is a deliberate departure
// from §15.2's "payload is the full row", and it is why these events cannot be
// reverted (config_revert.go) — the thing a revert would have to put back is
// not in the log.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrConnectionCredentialNotFound is "no stored credential for (project,
	// account)" — including one stored under another project, on purpose (P5).
	ErrConnectionCredentialNotFound = errors.New("connection credential not found")
	// ErrConnectionCredentialInvalid wraps every validation failure on a put.
	ErrConnectionCredentialInvalid = errors.New("invalid connection credential")
)

// ConnectionCredential is one sealed account credential (migration 052). One
// row per (project, account): every google_account connection naming the same
// account shares it (A1), and a reconnect replaces it.
//
// Nonce, Ciphertext and KeyID are `json:"-"` so that encoding a row by accident
// — in a handler, a log line, a debug dump — publishes no sealed material. The
// key id is not secret, but nothing outside agentd has a use for it, and the
// fewer places it appears the fewer places a reader has to reason about.
type ConnectionCredential struct {
	Project      string   `json:"project" gorm:"primaryKey;type:text"`
	Account      string   `json:"account" gorm:"primaryKey;type:text"`
	Provider     string   `json:"provider" gorm:"type:text;not null"`
	AccountEmail string   `json:"account_email" gorm:"type:text;not null"`
	Scopes       []string `json:"scopes" gorm:"serializer:json;type:jsonb;not null"`
	KeyID        string   `json:"-" gorm:"type:text;not null"`
	Nonce        []byte   `json:"-" gorm:"type:bytea;not null"`
	Ciphertext   []byte   `json:"-" gorm:"type:bytea;not null"`
	ConnectedBy  string   `json:"connected_by" gorm:"type:text;not null"`
	ConnectedAt  int64    `json:"connected_at" gorm:"not null"` // unix ms
}

func (ConnectionCredential) TableName() string { return "connection_credentials" }

// connectionCredentialEvent is the config-event payload for both actions. The
// fields are listed one by one on purpose; see the file comment. The account
// email is here because ConfigWrite has no human identity, and "who connected
// which Google account" is the whole point of the record (A8, A9).
type connectionCredentialEvent struct {
	Account        string   `json:"account"`
	Provider       string   `json:"provider"`
	AccountEmail   string   `json:"account_email"`
	Scopes         []string `json:"scopes"`
	ConnectedBy    string   `json:"connected_by"`
	ConnectedAt    int64    `json:"connected_at"`
	DisconnectedBy string   `json:"disconnected_by,omitempty"`
}

func connectionCredentialEventFor(c *ConnectionCredential) connectionCredentialEvent {
	return connectionCredentialEvent{
		Account:      c.Account,
		Provider:     c.Provider,
		AccountEmail: c.AccountEmail,
		Scopes:       nonNilScopes(c.Scopes),
		ConnectedBy:  c.ConnectedBy,
		ConnectedAt:  c.ConnectedAt,
	}
}

// The rationales are fixed sentences with no email in them (A9): the email is
// already in the payload, and a rationale is rendered in more places than a
// payload is.
const (
	connectionConnectRationale    = "Google account connected from the console"
	connectionDisconnectRationale = "Google account disconnected from the console"
)

// nonNilScopes keeps the stored and logged scope list a JSON array. The column
// is NOT NULL DEFAULT '[]'; a nil slice would serialise as the JSON value null,
// which is legal jsonb and would make "no scopes" have two spellings.
func nonNilScopes(scopes []string) []string {
	if scopes == nil {
		return []string{}
	}
	return scopes
}

func validateConnectionCredential(c *ConnectionCredential) error {
	if c == nil {
		return fmt.Errorf("%w: a credential is required", ErrConnectionCredentialInvalid)
	}
	if strings.TrimSpace(c.Project) == "" {
		return fmt.Errorf("%w: project is required (P5: the namespace is never inferred)", ErrConnectionCredentialInvalid)
	}
	// The account follows the connection-name rule (A1), so the same string can
	// key a map entry, a config-event entity and a URL path segment.
	if err := ValidateConnectionName(c.Account); err != nil {
		return fmt.Errorf("%w: account: %v", ErrConnectionCredentialInvalid, err)
	}
	required := []struct{ field, value string }{
		{"provider", c.Provider},
		{"account_email", c.AccountEmail},
		{"key_id", c.KeyID},
		{"connected_by", c.ConnectedBy},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return fmt.Errorf("%w: %s is required", ErrConnectionCredentialInvalid, r.field)
		}
	}
	// Lengths only: a message naming the bytes would put sealed material in an
	// error string, and error strings get logged.
	if len(c.Nonce) == 0 {
		return fmt.Errorf("%w: nonce is required", ErrConnectionCredentialInvalid)
	}
	if len(c.Ciphertext) == 0 {
		return fmt.Errorf("%w: ciphertext is required", ErrConnectionCredentialInvalid)
	}
	if c.ConnectedAt <= 0 {
		return fmt.Errorf("%w: connected_at must be a unix-millisecond time", ErrConnectionCredentialInvalid)
	}
	return nil
}

// PutConnectionCredential inserts or replaces (reconnect) the credential for
// (project, account), writing `connection_connect`.
//
// It takes no ConfigWrite: the only caller is the console's OAuth callback, a
// human act whose identity is c.ConnectedBy, and the rationale is fixed. A
// reconnect is logged as a second connect rather than as an update — to a
// reader of the changelog, pressing the button again IS connecting again.
func (s *Store) PutConnectionCredential(ctx context.Context, c *ConnectionCredential) error {
	if err := validateConnectionCredential(c); err != nil {
		return err
	}
	row := *c
	row.Scopes = nonNilScopes(c.Scopes)
	if _, err := s.WithConfigEvent(ctx, ConfigChange{
		Project: row.Project,
		Action:  ActionConnectionConnect,
		Payload: connectionCredentialEventFor(&row),
		Write:   ConfigWrite{Rationale: connectionConnectRationale},
	}, func(tx *gorm.DB) error {
		// An upsert rather than read-then-write: two callbacks for the same
		// account racing must end with one row, not a primary-key error for
		// whichever lost. Last writer wins, and both connects are logged.
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "project"}, {Name: "account"}},
			UpdateAll: true,
		}).Create(&row).Error
	}); err != nil {
		return fmt.Errorf("failed to store connection credential: %w", err)
	}
	return nil
}

// DeleteConnectionCredential removes the credential for (project, account),
// writing `connection_disconnect` with the row's metadata as it last stood plus
// who disconnected it. by is the human's email (A9).
func (s *Store) DeleteConnectionCredential(ctx context.Context, project, account, by string) error {
	if strings.TrimSpace(by) == "" {
		return fmt.Errorf("%w: disconnected_by is required", ErrConnectionCredentialInvalid)
	}
	existing, err := s.GetConnectionCredential(ctx, project, account)
	if err != nil {
		return err
	}
	payload := connectionCredentialEventFor(existing)
	payload.DisconnectedBy = by

	vanished := false
	if _, err := s.WithConfigEvent(ctx, ConfigChange{
		Project: project,
		Action:  ActionConnectionDisconnect,
		Payload: payload,
		Write:   ConfigWrite{Rationale: connectionDisconnectRationale},
	}, func(tx *gorm.DB) error {
		res := tx.Where("project = ? AND account = ?", project, account).Delete(&ConnectionCredential{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// Lost a race with a concurrent disconnect: roll back rather than
			// log a disconnect this call did not perform.
			vanished = true
			return fmt.Errorf("%w: %s/%s", ErrConnectionCredentialNotFound, project, account)
		}
		return nil
	}); err != nil {
		if vanished || errors.Is(err, ErrConnectionCredentialNotFound) {
			return fmt.Errorf("%w: %s/%s", ErrConnectionCredentialNotFound, project, account)
		}
		return fmt.Errorf("failed to delete connection credential: %w", err)
	}
	return nil
}

// GetConnectionCredential returns the stored credential, sealed bytes included —
// the caller that decrypts (agentd's account source) needs them.
func (s *Store) GetConnectionCredential(ctx context.Context, project, account string) (*ConnectionCredential, error) {
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("agentdb: GetConnectionCredential requires a project (P5)")
	}
	var c ConnectionCredential
	err := s.gdb.WithContext(ctx).Where("project = ? AND account = ?", project, account).First(&c).Error
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrConnectionCredentialNotFound, project, account)
		}
		return nil, fmt.Errorf("failed to get connection credential: %w", err)
	}
	c.Scopes = nonNilScopes(c.Scopes)
	return &c, nil
}

// ListConnectionCredentials returns a project's credentials ordered by account.
// The sealed bytes come back too: the console's list only needs metadata, but
// one read shape is simpler than two, and the HTTP layer builds its own
// response rather than encoding rows.
func (s *Store) ListConnectionCredentials(ctx context.Context, project string) ([]*ConnectionCredential, error) {
	if strings.TrimSpace(project) == "" {
		return nil, fmt.Errorf("agentdb: ListConnectionCredentials requires a project (P5)")
	}
	var out []*ConnectionCredential
	if err := s.gdb.WithContext(ctx).Where("project = ?", project).Order("account ASC").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("failed to list connection credentials: %w", err)
	}
	for _, c := range out {
		c.Scopes = nonNilScopes(c.Scopes)
	}
	if out == nil {
		out = []*ConnectionCredential{}
	}
	return out, nil
}
