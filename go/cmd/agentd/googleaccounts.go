package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
)

// googleaccounts.go — the store-backed connections.AccountSource behind
// Connect Google (design/2026-09-11-project-connections.md, "Addendum
// 2026-09-16: Connect Google button", A11, T22).
//
// The Registry is built once at boot and never sees the database or the
// encryption key (A11, account.go). This is the piece agentd installs with
// Registry.SetAccounts: it reads the sealed row through a narrow store
// interface, opens it with the Sealer, and turns the plaintext refresh token
// into an access token via connections.GoogleRefresh — all inside this file.
// The decrypted refresh token never leaves the oauth2 token source it is
// wrapped in: it is not logged, not returned, and not put in an error.

// connectionCredentialStore is the one store method googleAccounts needs.
// Narrow on purpose: like the T13 nil-store trap elsewhere in this package, a
// nil *agentdb.Store boxed into a non-nil interface would make every "is
// there a store" check lie, so T24 must construct googleAccounts only when
// agentDB != nil and never store a nil concrete pointer here.
type connectionCredentialStore interface {
	GetConnectionCredential(ctx context.Context, project, account string) (*agentdb.ConnectionCredential, error)
}

// googleAccountsStatusTTL bounds how long a cached Status answer is trusted
// before the next call re-reads the store. Status is on Registry.Availability's
// path (List, Servers, the proxy), so it must not do I/O on every call.
const googleAccountsStatusTTL = 30 * time.Second

// googleAccountsStatusTimeout bounds the background read Status makes when
// its cache is stale.
const googleAccountsStatusTimeout = 2 * time.Second

// acctKey is the (project, account) pair every cache in this file is keyed by.
type acctKey struct{ project, account string }

type googleAccountsStatusEntry struct {
	status  connections.AccountStatus
	expires time.Time
}

type googleAccountsTokenEntry struct {
	connectedAt int64
	src         connections.TokenSource
}

// googleAccounts is the AccountSource connections.Registry.SetAccounts
// installs when Connect Google is enabled. Constructed once at boot; safe for
// concurrent use.
type googleAccounts struct {
	store                            connectionCredentialStore
	sealer                           *connections.Sealer
	clientID, clientSecret, tokenURL string
	logf                             func(string, ...any)
	now                              func() time.Time

	mu        sync.Mutex
	status    map[acctKey]googleAccountsStatusEntry
	tokens    map[acctKey]googleAccountsTokenEntry
	errLogged map[acctKey]bool
}

// newGoogleAccounts builds a googleAccounts over store using cfg's sealer and
// Google OAuth client. cfg must be enabled (a non-nil sealer alone is not
// enough — T21's handoff note: gate on cfg.enabled()).
func newGoogleAccounts(store connectionCredentialStore, cfg googleConnectConfig, logf func(string, ...any)) *googleAccounts {
	return &googleAccounts{
		store:        store,
		sealer:       cfg.sealer,
		clientID:     cfg.clientID,
		clientSecret: cfg.clientSecret,
		tokenURL:     cfg.tokenURL,
		logf:         logf,
		now:          time.Now,
		status:       map[acctKey]googleAccountsStatusEntry{},
		tokens:       map[acctKey]googleAccountsTokenEntry{},
		errLogged:    map[acctKey]bool{},
	}
}

// Invalidate drops any cached status and token source for (project, account),
// so the very next call re-reads the store. T23 calls this right after a
// connect or disconnect commits.
func (g *googleAccounts) Invalidate(project, account string) {
	key := acctKey{project, account}
	g.mu.Lock()
	delete(g.status, key)
	delete(g.tokens, key)
	delete(g.errLogged, key)
	g.mu.Unlock()
}

// Status implements connections.AccountSource. It answers from cache when the
// cache is fresh; otherwise it makes one bounded store read. A store error
// keeps whatever status was last known (rather than flipping a healthy
// connection to unavailable because of a transient database hiccup) and logs
// once per failure streak, not once per call.
func (g *googleAccounts) Status(project, account string) connections.AccountStatus {
	key := acctKey{project, account}

	g.mu.Lock()
	if e, ok := g.status[key]; ok && g.now().Before(e.expires) {
		g.mu.Unlock()
		return e.status
	}
	g.mu.Unlock()

	st, err := g.fetchStatus(project, account)

	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		if !g.errLogged[key] {
			g.logf("[connections] google account status for %s/%s: %v", project, account, err)
			g.errLogged[key] = true
		}
		if e, ok := g.status[key]; ok {
			e.expires = g.now().Add(googleAccountsStatusTTL)
			g.status[key] = e
			return e.status
		}
		st = connections.AccountStatus{Unavailable: "connections: could not read the stored Google connection (see agentd logs)"}
		g.status[key] = googleAccountsStatusEntry{status: st, expires: g.now().Add(googleAccountsStatusTTL)}
		return st
	}
	delete(g.errLogged, key)
	g.status[key] = googleAccountsStatusEntry{status: st, expires: g.now().Add(googleAccountsStatusTTL)}
	return st
}

// fetchStatus is the uncached store read behind Status. A missing row is not
// a store error: it is "not connected", the ordinary case.
func (g *googleAccounts) fetchStatus(project, account string) (connections.AccountStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), googleAccountsStatusTimeout)
	defer cancel()
	cred, err := g.store.GetConnectionCredential(ctx, project, account)
	if err != nil {
		if errors.Is(err, agentdb.ErrConnectionCredentialNotFound) {
			return connections.AccountStatus{Connected: false}, nil
		}
		return connections.AccountStatus{}, err
	}
	if cred.KeyID != g.sealer.KeyID() {
		// Connected in the sense that a human did press Connect Google and a
		// row exists; just not usable with today's key. No decrypt attempt.
		return connections.AccountStatus{Connected: true, Email: cred.AccountEmail, Unavailable: connections.KeyChangedReason(account)}, nil
	}
	return connections.AccountStatus{Connected: true, Email: cred.AccountEmail}, nil
}

// Token implements connections.AccountSource. It always re-reads the store
// (cheap, and the only way to notice a reconnect or disconnect without
// waiting on Invalidate), then reuses a cached TokenSource for the row's
// connected_at so repeated calls do not re-exchange the refresh token.
func (g *googleAccounts) Token(ctx context.Context, project, account string) (string, error) {
	cred, err := g.store.GetConnectionCredential(ctx, project, account)
	if err != nil {
		if errors.Is(err, agentdb.ErrConnectionCredentialNotFound) {
			return "", connections.ErrNotConnected
		}
		return "", fmt.Errorf("connections: reading stored Google credential: %w", err)
	}
	if cred.KeyID != g.sealer.KeyID() {
		return "", connections.ErrKeyChanged
	}
	src, err := g.tokenSource(project, account, cred)
	if err != nil {
		return "", err
	}
	return src.Token(ctx)
}

// tokenSource returns the cached TokenSource for cred's connected_at, opening
// the sealed refresh token and building a fresh one only when there is none
// cached yet or the cached one is for an older connect. Building is local
// (Sealer.Open, then constructing the oauth2 source — no network call; the
// exchange happens in src.Token) and done while holding the lock, so
// concurrent callers for the same (project, account, connected_at) always
// land on the one cached source and therefore share its one refresh — see
// connections.GoogleRefresh's own doc comment for why that then serialises
// down to a single token request.
func (g *googleAccounts) tokenSource(project, account string, cred *agentdb.ConnectionCredential) (connections.TokenSource, error) {
	key := acctKey{project, account}

	g.mu.Lock()
	defer g.mu.Unlock()

	if e, ok := g.tokens[key]; ok && e.connectedAt == cred.ConnectedAt {
		return e.src, nil
	}

	plaintext, err := g.sealer.Open(cred.Nonce, cred.Ciphertext, connections.AccountAAD(project, account))
	if err != nil {
		return nil, err
	}
	src := connections.GoogleRefresh(g.clientID, g.clientSecret, string(plaintext), g.tokenURL)
	g.tokens[key] = googleAccountsTokenEntry{connectedAt: cred.ConnectedAt, src: src}
	return src, nil
}

var _ connections.AccountSource = (*googleAccounts)(nil)
