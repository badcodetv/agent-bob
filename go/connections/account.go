package connections

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
)

// account.go — google_account connections (design/2026-09-11-project-connections.md,
// "Addendum 2026-09-16: Connect Google button", A1 and A11).
//
// The Registry is still built once at boot, but a google_account connection's
// credential is a refresh token an operator stored by pressing Connect Google,
// and that can happen (or be undone) while agentd runs. So such a connection
// is resolved at use: Availability asks the AccountSource for the account's
// status, and the proxy asks it for an access token. agentd supplies the
// source; this package never sees the database or the encryption key.

// ErrNotConnected is what AccountSource.Token returns when no credential is
// stored for the account.
var ErrNotConnected = errors.New("connections: the account is not connected")

// ErrKeyChanged is what AccountSource.Token returns (wrapped or bare) when a
// credential is stored but was sealed under a different encryption key than
// the one agentd holds now, so it can never be opened again.
var ErrKeyChanged = errors.New("connections: the stored credential was sealed with a different key")

// AccountStatus is an account's state as the AccountSource last saw it.
type AccountStatus struct {
	Connected bool
	Email     string
	// Unavailable is "" when the account is usable; otherwise why not (for a
	// stored credential sealed with another key, KeyChangedReason).
	Unavailable string
}

// AccountSource is how a google_account connection reaches its credential at
// use time.
type AccountSource interface {
	// Status is cached by the implementation; no I/O on the hot path.
	Status(project, account string) AccountStatus
	// Token returns an access token, or ErrNotConnected, ErrKeyChanged or
	// ErrCredentialRevoked (possibly wrapped) when there is none to give.
	Token(ctx context.Context, project, account string) (string, error)
}

// AccountInfo is one account a project declares and the connections using it.
type AccountInfo struct {
	Account     string
	Connections []string
}

// KeyChangedReason is the unavailable reason (and proxy body) for an account
// whose stored credential was sealed with a different key. Exported so the
// AccountSource agentd installs reports the same sentence the proxy does.
func KeyChangedReason(account string) string {
	return fmt.Sprintf("the stored Google connection for %s can no longer be read (the encryption key changed) — connect Google again", account)
}

// notConnectedReason names both the connection and the account, and what a
// person does about it.
func notConnectedReason(name, account string) string {
	return fmt.Sprintf("%s uses the Google account %s, which is not connected — a project operator presses Connect Google in Settings", name, account)
}

// revokedAccountReason is the proxy body when Google refuses the stored
// refresh token.
func revokedAccountReason(account string) string {
	return fmt.Sprintf("Google refused the stored token for %s — connect Google again in Settings", account)
}

// noAccountSourceReason is used when agentd installed no source and gave no
// reason of its own.
const noAccountSourceReason = "Connect Google is not configured on this agentd"

// SetAccounts installs the source once at boot, before serving. With a nil
// source, every google_account connection is unavailable with
// disabledReason (set by agentd, e.g. "Connect Google is off:
// AGENTKIT_CONNECTIONS_KEY is not set"). A nil Registry ignores the call.
func (r *Registry) SetAccounts(src AccountSource, disabledReason string) {
	if r == nil {
		return
	}
	r.accountsMu.Lock()
	defer r.accountsMu.Unlock()
	r.accounts = src
	r.disabledReason = disabledReason
}

// accountSource returns the installed source, or nil and the reason there is
// none.
func (r *Registry) accountSource() (AccountSource, string) {
	r.accountsMu.RLock()
	defer r.accountsMu.RUnlock()
	if r.accounts != nil {
		return r.accounts, ""
	}
	if r.disabledReason != "" {
		return nil, r.disabledReason
	}
	return nil, noAccountSourceReason
}

// Availability is the ONE availability check: static Unavailable first (an
// env var missing), then, for google_account, the AccountSource's Status.
// List, Servers and the proxy all use it. reason is "" exactly when ok.
func (r *Registry) Availability(project, name string) (ok bool, reason string) {
	conn, found := r.Get(project, name)
	if !found {
		return false, fmt.Sprintf("no connection %s in this project", name)
	}
	if conn.Unavailable != "" {
		return false, conn.Unavailable
	}
	if conn.Spec.Auth.Type != AuthGoogleAccount {
		if conn.Token == nil {
			return false, fmt.Sprintf("connection %s has no credential", name)
		}
		return true, ""
	}
	src, why := r.accountSource()
	if src == nil {
		return false, why
	}
	account := conn.Spec.Auth.Account
	st := src.Status(project, account)
	switch {
	case !st.Connected:
		return false, notConnectedReason(name, account)
	case st.Unavailable != "":
		return false, st.Unavailable
	}
	return true, ""
}

// Accounts lists the distinct google_account accounts a project declares,
// sorted, with the connection names that use each (also sorted). A nil
// Registry, or a project declaring none, returns nil.
func (r *Registry) Accounts(project string) []AccountInfo {
	if r == nil {
		return nil
	}
	byAccount := map[string][]string{}
	for name, c := range r.byProject[project] {
		if c.Spec.Auth.Type != AuthGoogleAccount {
			continue
		}
		byAccount[c.Spec.Auth.Account] = append(byAccount[c.Spec.Auth.Account], name)
	}
	if len(byAccount) == 0 {
		return nil
	}
	out := make([]AccountInfo, 0, len(byAccount))
	for account, names := range byAccount {
		sort.Strings(names)
		out = append(out, AccountInfo{Account: account, Connections: names})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out
}

// accountToken is the proxy's token lookup for a google_account connection:
// the access token, or the status and body to refuse with.
func (r *Registry) accountToken(ctx context.Context, conn *Connection) (token string, status int, msg string, err error) {
	src, why := r.accountSource()
	if src == nil {
		return "", http.StatusServiceUnavailable, why, nil
	}
	account := conn.Spec.Auth.Account
	tok, err := src.Token(ctx, conn.Project, account)
	switch {
	case err == nil:
		return tok, 0, "", nil
	case errors.Is(err, ErrNotConnected):
		return "", http.StatusServiceUnavailable, notConnectedReason(conn.Name, account), nil
	case errors.Is(err, ErrKeyChanged):
		return "", http.StatusServiceUnavailable, KeyChangedReason(account), nil
	case errors.Is(err, ErrCredentialRevoked):
		return "", http.StatusBadGateway, revokedAccountReason(account), nil
	}
	return "", http.StatusBadGateway, fmt.Sprintf("could not obtain a credential for %s; retry", conn.Name), err
}
