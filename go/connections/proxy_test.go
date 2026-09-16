package connections

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The fake session token the container would send, and the real credential
// the proxy must swap in. Neither may ever appear where the other belongs.
const (
	testSessionJWT = "session-jwt-abc"
	testExpiredJWT = "session-jwt-expired"
	testRealPAT    = "github_pat_REAL"
)

// seenRequest is what the fake upstream recorded about one request.
type seenRequest struct {
	Method   string
	Host     string
	Path     string
	RawPath  string
	RawQuery string
	Header   http.Header
	Body     string
}

// recordingUpstream is an httptest MCP upstream that records every request
// and answers with whatever handler the test installs (200 "ok" by default).
type recordingUpstream struct {
	srv *httptest.Server

	mu      sync.Mutex
	seen    []seenRequest
	handler http.HandlerFunc
}

func newRecordingUpstream(t *testing.T) *recordingUpstream {
	t.Helper()
	u := &recordingUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.seen = append(u.seen, seenRequest{
			Method: r.Method, Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath,
			RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body),
		})
		h := u.handler
		u.mu.Unlock()
		if h == nil {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		}
		h(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *recordingUpstream) setHandler(h http.HandlerFunc) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.handler = h
}

func (u *recordingUpstream) requests() []seenRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seenRequest(nil), u.seen...)
}

// revokedToken is a TokenSource whose upstream refused the refresh token.
type revokedToken struct{}

func (revokedToken) Token(context.Context) (string, error) { return "", ErrCredentialRevoked }

// proxyHarness wires NewProxy to a recording upstream, a fake authenticator
// and a mutable grant store.
type proxyHarness struct {
	up    *recordingUpstream
	reg   *Registry
	proxy http.Handler

	mu        sync.Mutex
	callers   map[string]Caller // session token → caller
	grants    map[string][]string
	gone      map[string]bool
	grantsErr error
	logs      []string
}

// newProxyHarness builds project "wolf" with:
//   - github: bearer, upstream = the recording server's /mcp/v1 (no trailing
//     slash, so an empty {rest} proves the URL is used exactly)
//   - gmail:  bearer, but its env var is unset → unavailable
//   - docs:   google_oauth whose token source reports ErrCredentialRevoked
//
// and a worker "researcher" holding ["github", "gmail", "docs"].
func newProxyHarness(t *testing.T) *proxyHarness {
	t.Helper()
	h := &proxyHarness{
		up: newRecordingUpstream(t),
		callers: map[string]Caller{
			testSessionJWT: {Project: "wolf", SessionID: "sess-1", Worker: "researcher"},
		},
		grants: map[string][]string{"researcher": {"github", "gmail", "docs"}},
		gone:   map[string]bool{},
	}
	env := map[string]string{
		"WOLF_GITHUB_PAT":      testRealPAT,
		"GOOGLE_CLIENT_ID":     "cid",
		"GOOGLE_CLIENT_SECRET": "csecret",
		"KAI_GOOGLE_REFRESH":   "refresh-secret",
	}
	reg, err := NewRegistry(map[string]map[string]Spec{
		"wolf": {
			"github": {
				Description: "repos",
				URL:         h.up.srv.URL + "/mcp/v1",
				Auth:        Auth{Type: AuthBearer, TokenEnv: "WOLF_GITHUB_PAT"},
			},
			"gmail": {
				Description: "inbox",
				URL:         h.up.srv.URL + "/gmail",
				Auth:        Auth{Type: AuthBearer, TokenEnv: "GMAIL_PAT_UNSET"},
			},
			"docs": {
				Description: "docs",
				URL:         h.up.srv.URL + "/docs",
				Auth: Auth{Type: AuthGoogleOAuth, ClientIDEnv: "GOOGLE_CLIENT_ID",
					ClientSecretEnv: "GOOGLE_CLIENT_SECRET", RefreshTokenEnv: "KAI_GOOGLE_REFRESH"},
			},
		},
	}, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	docs, _ := reg.Get("wolf", "docs")
	docs.Token = revokedToken{}
	h.wire(reg)
	return h
}

// wire installs reg and builds the proxy over the harness's fake
// authenticator and grant store.
func (h *proxyHarness) wire(reg *Registry) {
	h.reg = reg
	h.proxy = NewProxy(ProxyConfig{
		Registry: reg,
		Authenticate: func(r *http.Request) (Caller, error) {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if tok == testExpiredJWT {
				return Caller{}, fmt.Errorf("session sess-9 is archived: %w", ErrSessionExpired)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			c, ok := h.callers[tok]
			if !ok {
				return Caller{}, errors.New("bad signature")
			}
			return c, nil
		},
		Grants: func(_ context.Context, project, worker string) ([]string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if project != "wolf" {
				return nil, fmt.Errorf("unexpected project %q", project)
			}
			if h.grantsErr != nil {
				return nil, h.grantsErr
			}
			if h.gone[worker] {
				return []string{}, ErrWorkerGone
			}
			return h.grants[worker], nil
		},
		Logf: func(format string, args ...any) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.logs = append(h.logs, fmt.Sprintf(format, args...))
		},
	})
}

func (h *proxyHarness) setCaller(tok string, c Caller) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callers[tok] = c
}

func (h *proxyHarness) setGrants(worker string, g []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.grants[worker] = g
}

func (h *proxyHarness) logLines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

// do sends one request straight into the handler (no mux: the mux's own path
// cleaning must not be what protects the upstream).
func (h *proxyHarness) do(method, target, token string, hdr http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(`{"jsonrpc":"2.0"}`))
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	h.proxy.ServeHTTP(rec, req)
	return rec
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("error response Content-Type = %q, want application/json", ct)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q is not {\"error\": ...}: %v", rec.Body.String(), err)
	}
	return body.Error
}

// TestProxy_StatusTable produces every row of the normative status table
// (design/2026-09-11-project-connections.md, "Proxy behaviour") with a
// dedicated case, and proves every refusal happens without contacting the
// upstream.
func TestProxy_StatusTable(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(h *proxyHarness)
		target    string
		token     string
		upstream  http.HandlerFunc
		wantCode  int
		wantError string // substring of the JSON error; "" → not an error response
		contacted bool   // whether the upstream should have been reached
	}{
		{
			name: "no session token", target: "/connect/github/",
			wantCode: 401, wantError: "session token rejected",
		},
		{
			name: "invalid session token", target: "/connect/github/", token: "forged",
			wantCode: 401, wantError: "session token rejected",
		},
		{
			name: "expired token for a session that is not live", target: "/connect/github/", token: testExpiredJWT,
			wantCode: 401, wantError: "session token expired",
		},
		{
			name: "session has no worker", target: "/connect/github/", token: "chat-jwt",
			setup: func(h *proxyHarness) {
				h.setCaller("chat-jwt", Caller{Project: "wolf", SessionID: "sess-2"})
			},
			wantCode:  403,
			wantError: "this session is not running as a worker; connections are granted to workers",
		},
		{
			name: "worker deleted or disabled", target: "/connect/github/", token: testSessionJWT,
			setup: func(h *proxyHarness) {
				h.mu.Lock()
				h.gone["researcher"] = true
				h.mu.Unlock()
			},
			wantCode: 403, wantError: "worker researcher is disabled or gone, and holds no connections",
		},
		{
			name: "worker does not hold the connection", target: "/connect/github/", token: testSessionJWT,
			setup:     func(h *proxyHarness) { h.setGrants("researcher", []string{"gmail"}) },
			wantCode:  403,
			wantError: "worker researcher does not hold connection github (connection_list shows what exists)",
		},
		{
			name: "grant lookup fails", target: "/connect/github/", token: testSessionJWT,
			setup: func(h *proxyHarness) {
				h.mu.Lock()
				h.grantsErr = errors.New("connection refused")
				h.mu.Unlock()
			},
			wantCode: 503, wantError: "could not read worker researcher's connections; retry",
		},
		{
			name: "dot-dot segments", target: "/connect/github/../../x", token: testSessionJWT,
			wantCode: 400, wantError: "invalid path",
		},
		{
			name: "single dot segment", target: "/connect/github/./x", token: testSessionJWT,
			wantCode: 400, wantError: "invalid path",
		},
		{
			name: "percent-encoded dot-dot", target: "/connect/github/%2e%2e/x", token: testSessionJWT,
			wantCode: 400, wantError: "invalid path",
		},
		{
			name: "encoded slash", target: "/connect/github/a%2Fb", token: testSessionJWT,
			wantCode: 400, wantError: "invalid path",
		},
		{
			name: "encoded slash, lower case", target: "/connect/github/a%2fb", token: testSessionJWT,
			wantCode: 400, wantError: "invalid path",
		},
		{
			name: "connection not in this project", target: "/connect/nope/", token: testSessionJWT,
			setup:    func(h *proxyHarness) { h.setGrants("researcher", []string{"*"}) },
			wantCode: 404, wantError: "no connection nope in this project",
		},
		{
			name: "connection unavailable", target: "/connect/gmail/", token: testSessionJWT,
			wantCode: 503, wantError: "GMAIL_PAT_UNSET",
		},
		{
			name: "token source refused (credential revoked)", target: "/connect/docs/", token: testSessionJWT,
			wantCode:  502,
			wantError: "Google refused the refresh token for docs — get a new one: docs/22-connections.md §Google",
		},
		{
			name: "upstream answers 401", target: "/connect/github/", token: testSessionJWT,
			upstream: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="github"`)
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantCode: 502, wantError: "upstream rejected the credential for github", contacted: true,
		},
		{
			name: "upstream answers 403", target: "/connect/github/", token: testSessionJWT,
			upstream: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
				w.WriteHeader(http.StatusForbidden)
			},
			wantCode: 502, wantError: "upstream rejected the credential for github", contacted: true,
		},
		{
			name: "otherwise: upstream's status streamed as-is", target: "/connect/github/", token: testSessionJWT,
			upstream: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTeapot)
				_, _ = io.WriteString(w, "short and stout")
			},
			wantCode: http.StatusTeapot, contacted: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newProxyHarness(t)
			if tc.setup != nil {
				tc.setup(h)
			}
			if tc.upstream != nil {
				h.up.setHandler(tc.upstream)
			}
			rec := h.do(http.MethodPost, tc.target, tc.token, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantError != "" {
				if got := errorBody(t, rec); !strings.Contains(got, tc.wantError) {
					t.Fatalf("error = %q, want it to contain %q", got, tc.wantError)
				}
			} else if got := rec.Body.String(); got != "short and stout" {
				t.Fatalf("body = %q, want the upstream's body verbatim", got)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != "" {
				t.Fatalf("WWW-Authenticate leaked to the client: %q", got)
			}
			if n := len(h.up.requests()); (n > 0) != tc.contacted {
				t.Fatalf("upstream contacted %d time(s), want contacted=%v", n, tc.contacted)
			}
		})
	}
}

// TestProxy_CredentialSwap: the upstream receives the real credential and
// none of the session's own credentials; only allowlisted headers cross.
func TestProxy_CredentialSwap(t *testing.T) {
	h := newProxyHarness(t)
	rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, http.Header{
		"Cookie":               {"sid=console-session"},
		"X-Api-Key":            {"wolf-api-key"},
		"X-Session-Id":         {"sess-1"},
		"X-Forwarded-For":      {"10.0.0.1"},
		"User-Agent":           {"claude-agent-sdk"},
		"Accept":               {"application/json, text/event-stream"},
		"Content-Type":         {"application/json"},
		"Mcp-Session-Id":       {"mcp-123"},
		"Mcp-Protocol-Version": {"2025-06-18"},
		"Last-Event-Id":        {"ev-7"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	reqs := h.up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(reqs))
	}
	got := reqs[0]
	if a := got.Header.Values("Authorization"); len(a) != 1 || a[0] != "Bearer "+testRealPAT {
		t.Fatalf("upstream Authorization = %q, want exactly [Bearer <real>]", a)
	}
	for k, vs := range got.Header {
		for _, v := range vs {
			if strings.Contains(v, testSessionJWT) {
				t.Fatalf("session token reached the upstream in %s: %q", k, v)
			}
		}
	}
	for _, dropped := range []string{"Cookie", "X-Api-Key", "X-Session-Id", "X-Forwarded-For", "User-Agent"} {
		if v := got.Header.Get(dropped); v != "" {
			t.Errorf("upstream received %s: %q (not on the allowlist)", dropped, v)
		}
	}
	for k, want := range map[string]string{
		"Accept":               "application/json, text/event-stream",
		"Content-Type":         "application/json",
		"Mcp-Session-Id":       "mcp-123",
		"Mcp-Protocol-Version": "2025-06-18",
		"Last-Event-Id":        "ev-7",
	} {
		if v := got.Header.Get(k); v != want {
			t.Errorf("upstream %s = %q, want %q", k, v, want)
		}
	}
	if got.Body != `{"jsonrpc":"2.0"}` {
		t.Errorf("upstream body = %q, want the request body verbatim", got.Body)
	}
	for _, line := range h.logLines() {
		if strings.Contains(line, testRealPAT) || strings.Contains(line, testSessionJWT) {
			t.Fatalf("a log line carries a credential: %q", line)
		}
	}
}

// TestProxy_UpstreamSeesItsOwnHost: the outbound Host is the upstream's,
// never the inbound (agentd's) one.
func TestProxy_UpstreamSeesItsOwnHost(t *testing.T) {
	h := newProxyHarness(t)
	req := httptest.NewRequest(http.MethodPost, "http://agentd:8099/connect/github/", nil)
	req.Header.Set("Authorization", testSessionJWT)
	h.proxy.ServeHTTP(httptest.NewRecorder(), req)
	reqs := h.up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(reqs))
	}
	want := strings.TrimPrefix(h.up.srv.URL, "http://")
	if reqs[0].Host != want {
		t.Fatalf("upstream Host = %q, want %q", reqs[0].Host, want)
	}
}

// TestProxy_PathAndQuery: an empty {rest} is the spec URL exactly (no
// trailing slash added); a non-empty rest is joined on; the query string is
// kept; encoded characters other than "/" survive.
func TestProxy_PathAndQuery(t *testing.T) {
	tests := []struct {
		target    string
		wantPath  string
		wantQuery string
	}{
		{"/connect/github/", "/mcp/v1", ""},
		{"/connect/github", "/mcp/v1", ""},
		{"/connect/github/?cursor=abc", "/mcp/v1", "cursor=abc"},
		{"/connect/github/tools/list?a=1&b=two", "/mcp/v1/tools/list", "a=1&b=two"},
		{"/connect/github/tools/", "/mcp/v1/tools/", ""},
		{"/connect/github/a%20b", "/mcp/v1/a b", ""},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			h := newProxyHarness(t)
			rec := h.do(http.MethodPost, tc.target, testSessionJWT, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
			}
			reqs := h.up.requests()
			if len(reqs) != 1 {
				t.Fatalf("upstream saw %d requests, want 1", len(reqs))
			}
			if reqs[0].Path != tc.wantPath || reqs[0].RawQuery != tc.wantQuery {
				t.Fatalf("upstream got path %q query %q, want %q %q", reqs[0].Path, reqs[0].RawQuery, tc.wantPath, tc.wantQuery)
			}
		})
	}
}

// TestProxy_MethodsPassThrough: Streamable HTTP uses POST, GET and DELETE.
func TestProxy_MethodsPassThrough(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			h := newProxyHarness(t)
			rec := h.do(method, "/connect/github/", testSessionJWT, http.Header{"Mcp-Session-Id": {"mcp-1"}})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			reqs := h.up.requests()
			if len(reqs) != 1 || reqs[0].Method != method {
				t.Fatalf("upstream saw %+v, want one %s", reqs, method)
			}
		})
	}
}

// TestProxy_ResponseHeaders: Mcp-Session-Id comes back to the client;
// Set-Cookie and WWW-Authenticate never do.
func TestProxy_ResponseHeaders(t *testing.T) {
	h := newProxyHarness(t)
	h.up.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Mcp-Session-Id", "mcp-new")
		w.Header().Set("Set-Cookie", "upstream=1")
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":{}}`)
	})
	rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Mcp-Session-Id"); got != "mcp-new" {
		t.Errorf("Mcp-Session-Id = %q, want mcp-new", got)
	}
	for _, k := range []string{"Set-Cookie", "WWW-Authenticate"} {
		if got := rec.Header().Get(k); got != "" {
			t.Errorf("%s passed to the client: %q", k, got)
		}
	}
	if got := rec.Body.String(); got != `{"result":{}}` {
		t.Errorf("body = %q", got)
	}
}

// TestProxy_StreamsSSEWithoutBuffering: the first SSE event reaches the
// client while the upstream is still holding the response open.
func TestProxy_StreamsSSEWithoutBuffering(t *testing.T) {
	h := newProxyHarness(t)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	h.up.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "event: message\ndata: {\"id\":1}\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "event: message\ndata: {\"id\":2}\n\n")
	})
	front := httptest.NewServer(h.proxy)
	defer front.Close()

	req, _ := http.NewRequest(http.MethodGet, front.URL+"/connect/github/", nil)
	req.Header.Set("Authorization", testSessionJWT)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if line != "event: message\n" {
			t.Fatalf("first line = %q, want the first event", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first SSE event did not arrive while the upstream was still open (buffered?)")
	}
	close(release)
}

// TestProxy_GrantReReadEachRequest: revoking a grant between two requests on
// the same session flips 200 → 403.
func TestProxy_GrantReReadEachRequest(t *testing.T) {
	h := newProxyHarness(t)
	if rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil); rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", rec.Code)
	}
	h.setGrants("researcher", []string{"gmail"})
	rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("after revoke status = %d, want 403", rec.Code)
	}
	if n := len(h.up.requests()); n != 1 {
		t.Fatalf("upstream saw %d requests, want 1 (the revoked one must not cross)", n)
	}
}

// TestProxy_Redirects: a redirect off the connection's host becomes a 502
// and the other host is never contacted; a redirect on the same host is
// handed back unchanged.
func TestProxy_Redirects(t *testing.T) {
	t.Run("off-host", func(t *testing.T) {
		h := newProxyHarness(t)
		other := newRecordingUpstream(t)
		h.up.setHandler(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.srv.URL+"/steal", http.StatusFound)
		})
		rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", rec.Code)
		}
		if got := errorBody(t, rec); !strings.Contains(got, "upstream redirected off-host") {
			t.Fatalf("error = %q", got)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Fatalf("Location passed to the client: %q", loc)
		}
		if n := len(other.requests()); n != 0 {
			t.Fatalf("the other host was contacted %d time(s)", n)
		}
	})
	t.Run("same host", func(t *testing.T) {
		h := newProxyHarness(t)
		h.up.setHandler(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/mcp/v2", http.StatusFound)
		})
		rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302 passed back", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/mcp/v2" {
			t.Fatalf("Location = %q, want it unchanged", loc)
		}
		if n := len(h.up.requests()); n != 1 {
			t.Fatalf("upstream saw %d requests, want 1 (the proxy must not follow)", n)
		}
	})
	t.Run("same host, absolute", func(t *testing.T) {
		h := newProxyHarness(t)
		abs := h.up.srv.URL + "/mcp/v2"
		h.up.setHandler(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, abs, http.StatusTemporaryRedirect)
		})
		rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil)
		if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != abs {
			t.Fatalf("status = %d Location = %q, want 307 %q", rec.Code, rec.Header().Get("Location"), abs)
		}
	})
}

// TestProxy_TransportErrorIs502: an unreachable upstream is a 502, never a
// hang or a panic.
func TestProxy_TransportErrorIs502(t *testing.T) {
	h := newProxyHarness(t)
	h.up.srv.Close()
	rec := h.do(http.MethodPost, "/connect/github/", testSessionJWT, nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	errorBody(t, rec)
}

// TestProxy_LogsOneLinePerRequest: one line per request naming project,
// session, worker, connection, method and status — never a header value.
func TestProxy_LogsOneLinePerRequest(t *testing.T) {
	h := newProxyHarness(t)
	h.do(http.MethodPost, "/connect/github/", testSessionJWT, http.Header{"Mcp-Session-Id": {"mcp-secretish"}})
	h.do(http.MethodDelete, "/connect/github/", "forged", nil)
	lines := h.logLines()
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %q", len(lines), lines)
	}
	for _, want := range []string{"wolf", "sess-1", "researcher", "github", "POST", "200"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line %q does not mention %q", lines[0], want)
		}
	}
	if !strings.Contains(lines[1], "DELETE") || !strings.Contains(lines[1], "401") {
		t.Errorf("refusal log line %q should carry method and status", lines[1])
	}
	for _, line := range lines {
		for _, secret := range []string{testRealPAT, testSessionJWT, "forged", "mcp-secretish"} {
			if strings.Contains(line, secret) {
				t.Fatalf("log line %q carries header value %q", line, secret)
			}
		}
	}
}

// newAccountProxyHarness is project "wolf" with google_account connections
// mail and drive (both on the default account) and calendar (account
// "archive"), all pointing at the recording upstream, a fake AccountSource
// installed, and worker "researcher" holding "*".
func newAccountProxyHarness(t *testing.T) (*proxyHarness, *fakeAccounts) {
	t.Helper()
	h := &proxyHarness{
		up: newRecordingUpstream(t),
		callers: map[string]Caller{
			testSessionJWT: {Project: "wolf", SessionID: "sess-1", Worker: "researcher"},
		},
		grants: map[string][]string{"researcher": {"*"}},
		gone:   map[string]bool{},
	}
	reg, err := NewRegistry(map[string]map[string]Spec{
		"wolf": {
			"mail":     {Description: "inbox", URL: h.up.srv.URL + "/gmail", Auth: Auth{Type: AuthGoogleAccount}},
			"drive":    {Description: "files", URL: h.up.srv.URL + "/drive", Auth: Auth{Type: AuthGoogleAccount, Account: "google"}},
			"calendar": {Description: "cal", URL: h.up.srv.URL + "/cal", Auth: Auth{Type: AuthGoogleAccount, Account: "archive"}},
		},
	}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	src := newFakeAccounts()
	reg.SetAccounts(src, "")
	h.wire(reg)
	return h, src
}

const testGoogleAccess = "ya29.fake-access"

// TestProxy_GoogleAccountRows produces the addendum's google_account rows of
// the status table, none of which contacts the upstream.
func TestProxy_GoogleAccountRows(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(src *fakeAccounts)
		wantCode  int
		wantError string
	}{
		{
			name:      "account not connected",
			wantCode:  503,
			wantError: "mail uses the Google account google, which is not connected — a project operator presses Connect Google in Settings",
		},
		{
			name: "status connected but the row is gone by Token time",
			setup: func(src *fakeAccounts) {
				src.setStatus("wolf", "google", AccountStatus{Connected: true})
				src.setTokenErr("wolf", "google", ErrNotConnected)
			},
			wantCode:  503,
			wantError: "mail uses the Google account google, which is not connected — a project operator presses Connect Google in Settings",
		},
		{
			name: "stored credential sealed with a different key (status)",
			setup: func(src *fakeAccounts) {
				src.setStatus("wolf", "google", AccountStatus{Connected: true, Unavailable: KeyChangedReason("google")})
			},
			wantCode:  503,
			wantError: "the stored Google connection for google can no longer be read (the encryption key changed) — connect Google again",
		},
		{
			name: "stored credential sealed with a different key (token)",
			setup: func(src *fakeAccounts) {
				src.connect("wolf", "google", "", testGoogleAccess)
				src.setTokenErr("wolf", "google", fmt.Errorf("open: %w", ErrKeyChanged))
			},
			wantCode:  503,
			wantError: "the stored Google connection for google can no longer be read (the encryption key changed) — connect Google again",
		},
		{
			name: "Google refuses the refresh token",
			setup: func(src *fakeAccounts) {
				src.connect("wolf", "google", "", testGoogleAccess)
				src.setTokenErr("wolf", "google", ErrCredentialRevoked)
			},
			wantCode:  502,
			wantError: "Google refused the stored token for google — connect Google again in Settings",
		},
		{
			name: "any other token failure",
			setup: func(src *fakeAccounts) {
				src.connect("wolf", "google", "", testGoogleAccess)
				src.setTokenErr("wolf", "google", errors.New("dial tcp: refused"))
			},
			wantCode:  502,
			wantError: "could not obtain a credential for mail; retry",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, src := newAccountProxyHarness(t)
			if tc.setup != nil {
				tc.setup(src)
			}
			rec := h.do(http.MethodPost, "/connect/mail/", testSessionJWT, nil)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if got := errorBody(t, rec); got != tc.wantError {
				t.Fatalf("error = %q, want %q", got, tc.wantError)
			}
			if n := len(h.up.requests()); n != 0 {
				t.Fatalf("upstream contacted %d time(s), want 0", n)
			}
		})
	}
}

// TestProxy_GoogleAccountNoSource: a Registry with no AccountSource answers
// 503 with agentd's disabled reason.
func TestProxy_GoogleAccountNoSource(t *testing.T) {
	h, _ := newAccountProxyHarness(t)
	h.reg.SetAccounts(nil, "Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set")
	rec := h.do(http.MethodPost, "/connect/mail/", testSessionJWT, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := errorBody(t, rec); got != "Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set" {
		t.Fatalf("error = %q", got)
	}
}

// TestProxy_GoogleAccountConnectAndDisconnect: connected → the upstream gets
// the source's access token; disconnecting between two requests turns 200
// into 503 with no Registry rebuild.
func TestProxy_GoogleAccountConnectAndDisconnect(t *testing.T) {
	h, src := newAccountProxyHarness(t)
	src.connect("wolf", "google", "enc@example.com", testGoogleAccess)

	rec := h.do(http.MethodPost, "/connect/mail/", testSessionJWT, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("connected: status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	reqs := h.up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(reqs))
	}
	if a := reqs[0].Header.Values("Authorization"); len(a) != 1 || a[0] != "Bearer "+testGoogleAccess {
		t.Fatalf("upstream Authorization = %q, want exactly the source's access token", a)
	}
	if reqs[0].Path != "/gmail" {
		t.Fatalf("upstream path = %q, want /gmail", reqs[0].Path)
	}

	src.disconnect("wolf", "google")
	rec = h.do(http.MethodPost, "/connect/mail/", testSessionJWT, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("after disconnect: status = %d, want 503", rec.Code)
	}
	if n := len(h.up.requests()); n != 1 {
		t.Fatalf("upstream saw %d requests, want 1 (the disconnected one must not cross)", n)
	}
	for _, line := range h.logLines() {
		if strings.Contains(line, testGoogleAccess) {
			t.Fatalf("a log line carries the access token: %q", line)
		}
	}
}

// TestProxy_GoogleAccountSharesOneTokenPath: two connections naming the same
// account look their token up under that one account; a third on another
// account under its own.
func TestProxy_GoogleAccountSharesOneTokenPath(t *testing.T) {
	h, src := newAccountProxyHarness(t)
	src.connect("wolf", "google", "enc@example.com", testGoogleAccess)
	src.connect("wolf", "archive", "old@example.com", "ya29.archive")

	for _, target := range []string{"/connect/mail/", "/connect/drive/", "/connect/mail/x", "/connect/calendar/"} {
		if rec := h.do(http.MethodPost, target, testSessionJWT, nil); rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (body %q)", target, rec.Code, rec.Body.String())
		}
	}
	calls := src.callCounts()
	if calls["wolf/google"] != 3 || calls["wolf/archive"] != 1 || len(calls) != 2 {
		t.Fatalf("Token calls = %v, want wolf/google:3 wolf/archive:1", calls)
	}
	reqs := h.up.requests()
	if got := reqs[3].Header.Get("Authorization"); got != "Bearer ya29.archive" {
		t.Fatalf("calendar Authorization = %q, want the archive account's token", got)
	}
}
