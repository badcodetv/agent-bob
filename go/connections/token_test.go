package connections

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
)

// tokenResponse is the RFC 6749 §5.1 token endpoint success body.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func TestGoogleRefresh_ExchangesOnFirstCall(t *testing.T) {
	var calls int32
	var mu sync.Mutex
	var form map[string][]string
	var gotClientID, gotClientSecret string
	var sawBasicAuth bool
	// The form is captured under a mutex and read back only after Token()
	// returns below (never concurrently with the handler): the HTTP round
	// trip already makes that ordering true in practice, but only the mutex
	// gives the race detector a happens-before edge it can see, so this
	// deliberately avoids a bare shared variable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		mu.Lock()
		form = map[string][]string(r.PostForm)
		// oauth2's AuthStyleAutoDetect tries the client credentials in the
		// Authorization header first (and only falls back to body params on
		// a non-2xx response, which this handler never returns), so assert
		// whichever place they actually arrive in rather than pin the style.
		if id, secret, ok := r.BasicAuth(); ok {
			sawBasicAuth = true
			gotClientID, gotClientSecret = id, secret
		} else {
			gotClientID = form["client_id"][0]
			gotClientSecret = form["client_secret"][0]
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "access-1", TokenType: "Bearer", ExpiresIn: 3600})
	}))
	defer srv.Close()

	ts := GoogleRefresh("client-id", "client-secret", "the-refresh-token", srv.URL)
	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "access-1" {
		t.Fatalf("Token: want access-1, got %q", tok)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls: want 1, got %d", calls)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := form["grant_type"]; len(got) != 1 || got[0] != "refresh_token" {
		t.Fatalf("grant_type: got %v", got)
	}
	if got := form["refresh_token"]; len(got) != 1 || got[0] != "the-refresh-token" {
		t.Fatalf("refresh_token: got %v", got)
	}
	if gotClientID != "client-id" {
		t.Fatalf("client_id: got %q (basic auth: %v)", gotClientID, sawBasicAuth)
	}
	if gotClientSecret != "client-secret" {
		t.Fatalf("client_secret: got %q (basic auth: %v)", gotClientSecret, sawBasicAuth)
	}
}

func TestGoogleRefresh_CachedUntilExpiry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "access-1", TokenType: "Bearer", ExpiresIn: 3600})
	}))
	defer srv.Close()

	ts := GoogleRefresh("id", "secret", "refresh", srv.URL)
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("a second call before expiry must not refresh: want 1 call, got %d", calls)
	}
}

func TestGoogleRefresh_RefreshesAfterExpiry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		tok := "access-1"
		if n > 1 {
			tok = "access-2"
		}
		// expires_in 1s: the oauth2 library treats a token as expired
		// slightly before its stated lifetime (a small "early expiry"
		// margin), so a token minted with a 1s lifetime is already
		// treated as expired essentially immediately — good enough to
		// force a genuine second exchange without a long sleep.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: tok, TokenType: "Bearer", ExpiresIn: 1})
	}))
	defer srv.Close()

	ts := GoogleRefresh("id", "secret", "refresh", srv.URL)
	first, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if first != "access-1" {
		t.Fatalf("first Token: want access-1, got %q", first)
	}
	time.Sleep(1500 * time.Millisecond)
	second, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if second != "access-2" {
		t.Fatalf("second Token after expiry: want access-2, got %q", second)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls after expiry: want 2, got %d", calls)
	}
}

func TestGoogleRefresh_ConcurrentCallersShareOneRequest(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	var firstRequestSeen sync.WaitGroup
	firstRequestSeen.Add(1)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		once.Do(firstRequestSeen.Done)
		<-release // hold every concurrent caller here until they've all arrived
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "access-1", TokenType: "Bearer", ExpiresIn: 3600})
	}))
	defer srv.Close()

	ts := GoogleRefresh("id", "secret", "refresh", srv.URL)

	const n = 20
	var wg sync.WaitGroup
	results := make([]string, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = ts.Token(context.Background())
		}(i)
	}
	// Give every goroutine a chance to call Token() and pile up behind the
	// (single) in-flight refresh's mutex before letting the server respond.
	firstRequestSeen.Wait()
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if results[i] != "access-1" {
			t.Fatalf("caller %d: want access-1, got %q", i, results[i])
		}
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("20 concurrent callers: want exactly 1 upstream request, got %d", calls)
	}
}

func TestGoogleRefresh_InvalidGrantIsCredentialRevoked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "Token has been expired or revoked.",
		})
	}))
	defer srv.Close()

	ts := GoogleRefresh("id", "secret", "the-super-secret-refresh-token", srv.URL)
	_, err := ts.Token(context.Background())
	if err == nil {
		t.Fatalf("want an error, got nil")
	}
	if !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("want errors.Is(err, ErrCredentialRevoked), got %v", err)
	}
	if strings.Contains(err.Error(), "the-super-secret-refresh-token") {
		t.Fatalf("error must never contain the refresh token: %q", err.Error())
	}
}

func TestGoogleRefresh_OtherUpstreamErrorNeverLeaksRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "server_error"})
	}))
	defer srv.Close()

	ts := GoogleRefresh("id", "secret", "another-super-secret-refresh-token", srv.URL)
	_, err := ts.Token(context.Background())
	if err == nil {
		t.Fatalf("want an error, got nil")
	}
	if errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("a plain server_error must not be reported as ErrCredentialRevoked")
	}
	if strings.Contains(err.Error(), "another-super-secret-refresh-token") {
		t.Fatalf("error must never contain the refresh token: %q", err.Error())
	}
}
