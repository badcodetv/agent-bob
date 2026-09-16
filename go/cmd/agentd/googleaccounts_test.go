package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
)

// fakeCredentialStore is an in-memory connectionCredentialStore for
// googleaccounts_test.go. Safe for concurrent use so the concurrency test can
// hammer it.
type fakeCredentialStore struct {
	mu    sync.Mutex
	rows  map[acctKey]*agentdb.ConnectionCredential
	calls int32
	err   error // if set, GetConnectionCredential always returns this
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{rows: map[acctKey]*agentdb.ConnectionCredential{}}
}

func (f *fakeCredentialStore) GetConnectionCredential(ctx context.Context, project, account string) (*agentdb.ConnectionCredential, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	row, ok := f.rows[acctKey{project, account}]
	if !ok {
		return nil, agentdb.ErrConnectionCredentialNotFound
	}
	cp := *row
	return &cp, nil
}

func (f *fakeCredentialStore) put(sealer *connections.Sealer, project, account, refreshToken, email string, connectedAt int64) {
	nonce, ciphertext, err := sealer.Seal([]byte(refreshToken), connections.AccountAAD(project, account))
	if err != nil {
		panic(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[acctKey{project, account}] = &agentdb.ConnectionCredential{
		Project:      project,
		Account:      account,
		Provider:     "google",
		AccountEmail: email,
		KeyID:        sealer.KeyID(),
		Nonce:        nonce,
		Ciphertext:   ciphertext,
		ConnectedBy:  "richard@example.com",
		ConnectedAt:  connectedAt,
	}
}

func (f *fakeCredentialStore) delete(project, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, acctKey{project, account})
}

func (f *fakeCredentialStore) tamper(project, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[acctKey{project, account}]
	if row == nil {
		return
	}
	row.Ciphertext = append([]byte(nil), row.Ciphertext...)
	row.Ciphertext[0] ^= 0xFF
}

// tokenServer is a minimal RFC 6749 token endpoint: it hands out an access
// token that names the refresh token it was exchanged for (so a test can tell
// which refresh token was actually used), unless configured to refuse.
func newTokenServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		refresh := r.PostForm.Get("refresh_token")
		if refresh == "revoke-me" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-for-" + refresh,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestSealer(t *testing.T) *connections.Sealer {
	t.Helper()
	key, err := connections.ParseKey(testConnectionsKey)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	sealer, err := connections.NewSealer(key)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return sealer
}

func TestGoogleAccounts_NotConnected(t *testing.T) {
	store := newFakeCredentialStore()
	srv, calls := newTokenServer(t)
	cfg := googleConnectConfig{clientID: "client-id", clientSecret: "client-secret", sealer: newTestSealer(t), tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	_, err := ga.Token(context.Background(), "enc", "google")
	if !errors.Is(err, connections.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
	st := ga.Status("enc", "google")
	if st.Connected {
		t.Fatalf("want not connected, got %+v", st)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("no token request should have been made, got %d", *calls)
	}
}

func TestGoogleAccounts_ConnectedCachesTokenSource(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, calls := newTokenServer(t)
	store.put(sealer, "enc", "google", "refresh-1", "richard@example.com", 1000)

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	tok, err := ga.Token(context.Background(), "enc", "google")
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if tok != "access-for-refresh-1" {
		t.Fatalf("want access-for-refresh-1, got %q", tok)
	}
	if _, err := ga.Token(context.Background(), "enc", "google"); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("second call must not make a new token request: got %d calls", *calls)
	}

	st := ga.Status("enc", "google")
	if !st.Connected || st.Email != "richard@example.com" || st.Unavailable != "" {
		t.Fatalf("Status: got %+v", st)
	}
}

func TestGoogleAccounts_ReconnectExchangesNewRefreshToken(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, calls := newTokenServer(t)
	store.put(sealer, "enc", "google", "refresh-1", "richard@example.com", 1000)

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	if _, err := ga.Token(context.Background(), "enc", "google"); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("want 1 call after first Token, got %d", got)
	}

	// Reconnect: new connected_at, new refresh token. No Invalidate call —
	// Token must notice the change on its own by re-reading the row.
	store.put(sealer, "enc", "google", "refresh-2", "richard@example.com", 2000)

	tok, err := ga.Token(context.Background(), "enc", "google")
	if err != nil {
		t.Fatalf("Token after reconnect: %v", err)
	}
	if tok != "access-for-refresh-2" {
		t.Fatalf("want the new refresh token to be exchanged, got %q", tok)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("want 2 calls after reconnect, got %d", got)
	}
}

func TestGoogleAccounts_InvalidateMakesDisconnectImmediate(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, _ := newTokenServer(t)
	store.put(sealer, "enc", "google", "refresh-1", "richard@example.com", 1000)

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	// Warm the cache.
	if st := ga.Status("enc", "google"); !st.Connected {
		t.Fatalf("want connected before disconnect, got %+v", st)
	}

	store.delete("enc", "google")
	ga.Invalidate("enc", "google")

	st := ga.Status("enc", "google")
	if st.Connected {
		t.Fatalf("want not connected immediately after Invalidate, got %+v", st)
	}
	if _, err := ga.Token(context.Background(), "enc", "google"); !errors.Is(err, connections.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected after disconnect, got %v", err)
	}
}

func TestGoogleAccounts_WrongKeyIDNoDecryptAttempt(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, calls := newTokenServer(t)
	store.put(sealer, "enc", "google", "refresh-1", "richard@example.com", 1000)

	// Simulate a key rotation: the row was sealed under `sealer`, but the
	// googleAccounts we build here holds a different one (same length,
	// different bytes), so KeyID differs and Open must never be tried.
	otherKeyB64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes, base64
	otherKey, err := connections.ParseKey(otherKeyB64)
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	otherSealer, err := connections.NewSealer(otherKey)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: otherSealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	st := ga.Status("enc", "google")
	if st.Unavailable == "" || !strings.Contains(st.Unavailable, "encryption key changed") {
		t.Fatalf("want the key-changed reason, got %+v", st)
	}

	_, err = ga.Token(context.Background(), "enc", "google")
	if !errors.Is(err, connections.ErrKeyChanged) {
		t.Fatalf("want ErrKeyChanged, got %v", err)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("a wrong key must never reach the token endpoint, got %d calls", *calls)
	}
}

func TestGoogleAccounts_TamperedCiphertextErrorNamesNeitherTokenNorBytes(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, _ := newTokenServer(t)
	store.put(sealer, "enc", "google", "the-super-secret-refresh-token", "richard@example.com", 1000)
	store.tamper("enc", "google")

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	_, err := ga.Token(context.Background(), "enc", "google")
	if err == nil {
		t.Fatalf("want an error for tampered ciphertext, got nil")
	}
	if strings.Contains(err.Error(), "the-super-secret-refresh-token") {
		t.Fatalf("error must not name the token: %q", err.Error())
	}
	if errors.Is(err, connections.ErrNotConnected) || errors.Is(err, connections.ErrKeyChanged) {
		t.Fatalf("a tamper is neither ErrNotConnected nor ErrKeyChanged: %v", err)
	}
}

func TestGoogleAccounts_InvalidGrantIsCredentialRevoked(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, _ := newTokenServer(t)
	store.put(sealer, "enc", "google", "revoke-me", "richard@example.com", 1000)

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	_, err := ga.Token(context.Background(), "enc", "google")
	if !errors.Is(err, connections.ErrCredentialRevoked) {
		t.Fatalf("want ErrCredentialRevoked, got %v", err)
	}
}

func TestGoogleAccounts_ConcurrentTokenCallsShareOneRequest(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	store.put(sealer, "enc", "google", "refresh-1", "richard@example.com", 1000)

	var calls int32
	release := make(chan struct{})
	var firstSeen sync.WaitGroup
	firstSeen.Add(1)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		once.Do(firstSeen.Done)
		<-release
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "token_type": "Bearer", "expires_in": 3600})
	}))
	defer srv.Close()

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	ga := newGoogleAccounts(store, cfg, t.Logf)

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = ga.Token(context.Background(), "enc", "google")
		}(i)
	}
	firstSeen.Wait()
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("20 concurrent Token calls: want exactly 1 upstream request, got %d", got)
	}
}

func TestGoogleAccounts_StatusSurvivesTransientStoreError(t *testing.T) {
	store := newFakeCredentialStore()
	sealer := newTestSealer(t)
	srv, _ := newTokenServer(t)
	store.put(sealer, "enc", "google", "refresh-1", "richard@example.com", 1000)

	cfg := googleConnectConfig{clientID: "id", clientSecret: "secret", sealer: sealer, tokenURL: srv.URL}
	var logCount int32
	ga := newGoogleAccounts(store, cfg, func(string, ...any) { atomic.AddInt32(&logCount, 1) })
	ga.now = func() time.Time { return time.Unix(0, 0) }

	if st := ga.Status("enc", "google"); !st.Connected {
		t.Fatalf("want connected before the store breaks, got %+v", st)
	}

	// Force the cache to look stale, then make the store fail.
	ga.now = func() time.Time { return time.Unix(1000, 0) }
	store.mu.Lock()
	store.err = errors.New("boom")
	store.mu.Unlock()

	for i := 0; i < 3; i++ {
		st := ga.Status("enc", "google")
		if !st.Connected {
			t.Fatalf("call %d: a store error must keep the last known status, got %+v", i, st)
		}
		ga.now = func() time.Time { return time.Unix(1000, 0) } // stay stale so every call re-reads
	}
	if got := atomic.LoadInt32(&logCount); got != 1 {
		t.Fatalf("a repeated store error must log once, got %d log calls", got)
	}
}
