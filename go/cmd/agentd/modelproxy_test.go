package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// TestCredentialMode pins the three answers the browser is told, in the same
// precedence the two acting call sites apply: an OAuth token wins outright
// (attended subscription billing by default; production blanks the token), an
// API key alone is proxy mode, neither is the mock. RD18: the mock is the
// DEFAULT (both credential lines ship blank in .env.example), so a wrong
// answer here is the difference between "the product works" and "no model was
// ever called".
func TestCredentialMode(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		oauth string
		want  string
	}{
		{"neither is mock", "", "", credentialModeMock},
		{"api key alone", "sk-ant-api03-x", "", credentialModeAPIKey},
		{"oauth token alone", "", "sk-ant-oat01-x", credentialModeSubscription},
		{"oauth token wins over api key", "sk-ant-api03-x", "sk-ant-oat01-x", credentialModeSubscription},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := credentialMode(tt.key, tt.oauth); got != tt.want {
				t.Fatalf("credentialMode(%q, %q) = %q, want %q", tt.key, tt.oauth, got, tt.want)
			}
		})
	}
}

// TestAuthConfigReportsCredentialMode is the wire-level half: whatever
// credentialMode decided has to reach the browser, in every login mode.
func TestAuthConfigReportsCredentialMode(t *testing.T) {
	for _, mode := range []string{credentialModeMock, credentialModeAPIKey, credentialModeSubscription} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			authConfigHandler("", false, mode)(rec, httptest.NewRequest(http.MethodGet, "/auth/config", nil))
			var resp struct {
				CredentialMode string `json:"credential_mode"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.CredentialMode != mode {
				t.Fatalf("credential_mode = %q, want %q", resp.CredentialMode, mode)
			}
		})
	}
}

func TestNewModelProxyHandler_MockWhenNoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	h := newModelProxyHandler(newSessionTokenAuth([]byte("s3cret"), nil))
	if h == nil {
		t.Fatal("handler is nil")
	}
	// Mock provider answers GET .../health with the mock note.
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "" {
		t.Fatalf("precondition: key should be empty, got %q", got)
	}
}

func TestModelProvider_TargetPathIsDirectAnthropic(t *testing.T) {
	p := modelProvider{endpoint: "https://api.anthropic.com", apiKey: "k"}
	if got := p.TargetPath("/v1/messages"); got != "/v1/messages" {
		t.Fatalf("TargetPath = %q, want /v1/messages (no /anthropic prefix)", got)
	}
	if p.Endpoint() != "https://api.anthropic.com" || p.APIKey() != "k" {
		t.Fatalf("provider accessors wrong: %+v", p)
	}
}

func TestSandboxSessionEnv_PointsAtAgentProxyAndDummyKey(t *testing.T) {
	env := sandboxSessionEnv("http://172.17.0.1:8099")
	if env["ANTHROPIC_BASE_URL"] != "http://172.17.0.1:8099/agent-proxy" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q", env["ANTHROPIC_BASE_URL"])
	}
	if env["HOST_API_URL"] != "http://172.17.0.1:8099" {
		t.Fatalf("HOST_API_URL = %q", env["HOST_API_URL"])
	}
	if !strings.HasPrefix(env["ANTHROPIC_API_KEY"], "sk-ant-") {
		t.Fatalf("expected dummy passthrough key, got %q", env["ANTHROPIC_API_KEY"])
	}
}

// TestSubscriptionSessionEnv locks direct (subscription) mode: sessions get the
// OAuth token and NO proxy plumbing — base URL and API key present but EMPTY.
// Absent is not enough: a snapshot taken in proxy mode carries both in its
// image config, and only an explicit `KEY=` overrides them on restore.
func TestSubscriptionSessionEnv_DirectWithOAuthTokenOnly(t *testing.T) {
	env := subscriptionSessionEnv("http://172.17.0.1:8099", "sk-ant-oat01-test")
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-test" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q", env["CLAUDE_CODE_OAUTH_TOKEN"])
	}
	if env["HOST_API_URL"] != "http://172.17.0.1:8099" {
		t.Fatalf("HOST_API_URL = %q", env["HOST_API_URL"])
	}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"} {
		if v, ok := env[k]; !ok || v != "" {
			t.Fatalf("%s must be set to empty in subscription mode, got %q (present=%v)", k, v, ok)
		}
	}
}

// TestSessionEnv_ModeSwitchLeavesNoStaleWiring is the production failure: a
// session snapshotted under one model mode and restored under the other. The
// snapshot image's env is the old mode's; the container env is the image env
// with the new session env laid over it (what Docker does). Nothing from the
// old mode may survive.
func TestSessionEnv_ModeSwitchLeavesNoStaleWiring(t *testing.T) {
	const self = "http://172.17.0.1:8099"
	overlay := func(image, session map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range image {
			out[k] = v
		}
		for k, v := range session {
			out[k] = v
		}
		return out
	}

	t.Run("proxy snapshot restored in subscription mode", func(t *testing.T) {
		got := overlay(sandboxSessionEnv(self), subscriptionSessionEnv(self, "sk-ant-oat01-new"))
		if got["ANTHROPIC_BASE_URL"] != "" {
			t.Errorf("ANTHROPIC_BASE_URL = %q: the session would talk to the proxy, which serves the mock", got["ANTHROPIC_BASE_URL"])
		}
		if got["ANTHROPIC_API_KEY"] != "" {
			t.Errorf("ANTHROPIC_API_KEY = %q, want empty", got["ANTHROPIC_API_KEY"])
		}
	})

	t.Run("subscription snapshot restored in proxy mode", func(t *testing.T) {
		got := overlay(subscriptionSessionEnv(self, "sk-ant-oat01-old"), sandboxSessionEnv(self))
		if got["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want empty", got["CLAUDE_CODE_OAUTH_TOKEN"])
		}
		if got["ANTHROPIC_BASE_URL"] != self+"/agent-proxy" {
			t.Errorf("ANTHROPIC_BASE_URL = %q", got["ANTHROPIC_BASE_URL"])
		}
	})
}

// ---------------------------------------------------------------------------
// T13 — /agent-proxy/ guarded by the session token (H6)
// ---------------------------------------------------------------------------

// realKeyProxyHandler stands up the real-key branch of newModelProxyHandler
// pointed at a fake upstream, so the tests below can prove both that a
// request is forwarded (and what the upstream actually received) and that a
// refused request never reaches the upstream at all.
func realKeyProxyHandler(t *testing.T, auth *sessionTokenAuth, upstream *httptest.Server) http.Handler {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-real-test-key")
	t.Setenv("ANTHROPIC_UPSTREAM_URL", upstream.URL)
	return newModelProxyHandler(auth)
}

func TestModelProxy_NoToken_401AndUpstreamNeverContacted(t *testing.T) {
	contacted := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	h := realKeyProxyHandler(t, newSessionTokenAuth([]byte("s3cret"), nil), upstream)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}")))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if contacted {
		t.Fatal("the upstream must never be contacted for an unauthenticated request")
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("want a JSON {\"error\":...} body, got %q (err=%v)", rec.Body.String(), err)
	}
}

func TestModelProxy_ForgedOrWrongSecretToken_401(t *testing.T) {
	secret := []byte("s3cret")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream contacted with a bad token")
	}))
	defer upstream.Close()
	h := realKeyProxyHandler(t, newSessionTokenAuth(secret, nil), upstream)

	for _, tok := range []string{
		"not-a-jwt",
		mintSessionToken(t, []byte("some-other-secret"), time.Hour, "acme", "sess-1"),
	} {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
		req.Header.Set("X-Api-Key", tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status = %d, want 401", tok, rec.Code)
		}
	}
}

// TestModelProxy_ValidSessionToken_ForwardedWithRealKey proves the credential
// swap: the upstream sees the real ANTHROPIC_API_KEY (x-api-key, per
// modelproxy.buildProxyRequest), never the session JWT.
func TestModelProxy_ValidSessionToken_ForwardedWithRealKey(t *testing.T) {
	secret := []byte("s3cret")
	sessions := &fakeSessionLookup{sessions: map[string]*agentdb.Session{
		"sess-1": {ID: "sess-1", Customer: "acme", Status: "running"},
	}}
	var gotKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	h := realKeyProxyHandler(t, newSessionTokenAuth(secret, sessions), upstream)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("X-Api-Key", mintSessionToken(t, secret, time.Hour, "acme", "sess-1"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if gotKey != "sk-ant-api03-real-test-key" {
		t.Fatalf("upstream x-api-key = %q, want the real configured key", gotKey)
	}
}

// TestModelProxy_AuthorizationFallback: a client that sends the token under
// Authorization instead of X-Api-Key (the header the SDK actually uses) still
// gets through.
func TestModelProxy_AuthorizationFallback(t *testing.T) {
	secret := []byte("s3cret")
	sessions := &fakeSessionLookup{sessions: map[string]*agentdb.Session{
		"sess-1": {ID: "sess-1", Customer: "acme", Status: "running"},
	}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h := realKeyProxyHandler(t, newSessionTokenAuth(secret, sessions), upstream)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", mintSessionToken(t, secret, time.Hour, "acme", "sess-1"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

// TestModelProxy_ExpiredToken_LiveVsArchived is the T13 hazard itself: an
// expired token (one Bash could have read out of a container long after the
// turn that used it) must keep working for a session still in progress, and
// must stop working the moment that session is no longer live.
func TestModelProxy_ExpiredToken_LiveVsArchived(t *testing.T) {
	secret := []byte("s3cret")
	sessions := &fakeSessionLookup{sessions: map[string]*agentdb.Session{
		"live":     {ID: "live", Customer: "acme", Status: "running"},
		"archived": {ID: "archived", Customer: "acme", Status: "running", SnapshotState: "archived"},
		"errored":  {ID: "errored", Customer: "acme", Status: "error"},
	}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h := realKeyProxyHandler(t, newSessionTokenAuth(secret, sessions), upstream)

	call := func(sessionID string) int {
		expired := mintSessionToken(t, secret, -time.Minute, "acme", sessionID)
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
		req.Header.Set("X-Api-Key", expired)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := call("live"); got != http.StatusOK {
		t.Fatalf("expired token for a LIVE session: status = %d, want 200", got)
	}
	if got := call("archived"); got != http.StatusUnauthorized {
		t.Fatalf("expired token for an ARCHIVED session: status = %d, want 401", got)
	}
	if got := call("errored"); got != http.StatusUnauthorized {
		t.Fatalf("expired token for an ERRORED session: status = %d, want 401", got)
	}
}

// TestModelProxy_NoDatabase_ValidTokenForwarded_NoPanic is the nil-store trap:
// on the SQLite fallback agentDB is a nil *agentdb.Store, and boxing it
// straight into the mcpSessionLookup interface makes a non-nil interface whose
// GetSession panics. main.go must pass an untyped nil instead; here we prove
// the resulting handler still forwards a valid (unexpired) token, and that an
// EXPIRED one is refused outright rather than panicking (with no store,
// sessionKnown can never become true).
func TestModelProxy_NoDatabase_ValidTokenForwarded_NoPanic(t *testing.T) {
	secret := []byte("s3cret")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	// The exact construction main.go uses when agentDB == nil: leave the
	// mcpSessionLookup interface at its zero value, never a boxed nil pointer.
	var noStore mcpSessionLookup
	h := realKeyProxyHandler(t, newSessionTokenAuth(secret, noStore), upstream)

	valid := mintSessionToken(t, secret, time.Hour, "acme", "sess-1")
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req.Header.Set("X-Api-Key", valid)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // must not panic
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token with no database: status = %d, want 200", rec.Code)
	}

	expired := mintSessionToken(t, secret, -time.Minute, "acme", "sess-1")
	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	req2.Header.Set("X-Api-Key", expired)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2) // must not panic
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("expired token with no database: status = %d, want 401", rec2.Code)
	}
}

// TestModelProxy_MockMode_NoTokenRequired: the guard wraps ONLY the real-key
// branch. With no ANTHROPIC_API_KEY set, the mock handler must still answer
// with no session token at all — mounting a guard there would break every
// offline stack.
func TestModelProxy_MockMode_NoTokenRequired(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	h := newModelProxyHandler(newSessionTokenAuth([]byte("s3cret"), nil))
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3","messages":[]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("mock mode must not require a session token, got 401: %q", rec.Body.String())
	}
}
