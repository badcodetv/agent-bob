package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
	"github.com/badcodetv/agent-bob/extension"
	"github.com/badcodetv/agent-bob/extension/devclaims"
	"github.com/badcodetv/agent-bob/httpapi"
)

// googleconnect_test.go — T23 of design/2026-09-11-project-connections.md: the
// four Connect Google routes against a fake Google (token, tokeninfo, revoke),
// a fake credential store and a real Registry + project map.

const (
	gcTestCode    = "auth-code-4f1e9a"
	gcTestRefresh = "refresh-token-77c2d1"
	gcTestAccess  = "access-token-9b0e3f"
	gcTestIDToken = "id-token-5a5a5a"
	gcTestEmail   = "Emperor.ENC@gmail.com"
	gcTestClient  = "client-id.apps.googleusercontent.com"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

// fakeGoogle is one httptest server playing Google's token, tokeninfo and
// revoke endpoints. Every knob is read under mu so a test can change it
// between the start and the callback.
type fakeGoogle struct {
	srv *httptest.Server

	mu            sync.Mutex
	noRefresh     bool
	scope         string
	aud           string
	revokeStatus  int
	tokenStatus   int
	tokenRequests []url.Values
	revoked       []string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	g := &fakeGoogle{
		scope:        googleConnectScopes,
		aud:          gcTestClient,
		revokeStatus: http.StatusOK,
		tokenStatus:  http.StatusOK,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.tokenRequests = append(g.tokenRequests, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		if g.tokenStatus != http.StatusOK {
			w.WriteHeader(g.tokenStatus)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		body := map[string]any{
			"access_token": gcTestAccess,
			"token_type":   "Bearer",
			"expires_in":   3599,
			"scope":        g.scope,
			"id_token":     gcTestIDToken,
		}
		if !g.noRefresh {
			body["refresh_token"] = gcTestRefresh
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /tokeninfo", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.URL.Query().Get("id_token") != gcTestIDToken {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"aud": g.aud, "email": gcTestEmail, "email_verified": "true",
		})
	})
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.revoked = append(g.revoked, r.PostForm.Get("token"))
		w.WriteHeader(g.revokeStatus)
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) set(f func(g *fakeGoogle)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f(g)
}

func (g *fakeGoogle) tokenCalls() []url.Values {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]url.Values(nil), g.tokenRequests...)
}

func (g *fakeGoogle) revokedTokens() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.revoked...)
}

// fakeConnectStore is an in-memory googleConnectStore.
type fakeConnectStore struct {
	mu          sync.Mutex
	rows        map[acctKey]*agentdb.ConnectionCredential
	puts        int
	disconnects []string
	putErr      error
}

func newFakeConnectStore() *fakeConnectStore {
	return &fakeConnectStore{rows: map[acctKey]*agentdb.ConnectionCredential{}}
}

func (f *fakeConnectStore) GetConnectionCredential(_ context.Context, project, account string) (*agentdb.ConnectionCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[acctKey{project, account}]
	if !ok {
		return nil, agentdb.ErrConnectionCredentialNotFound
	}
	cp := *row
	return &cp, nil
}

func (f *fakeConnectStore) ListConnectionCredentials(_ context.Context, project string) ([]*agentdb.ConnectionCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*agentdb.ConnectionCredential
	for k, row := range f.rows {
		if k.project == project {
			cp := *row
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account < out[j].Account })
	return out, nil
}

func (f *fakeConnectStore) PutConnectionCredential(_ context.Context, c *agentdb.ConnectionCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	cp := *c
	f.rows[acctKey{c.Project, c.Account}] = &cp
	f.puts++
	return nil
}

func (f *fakeConnectStore) DeleteConnectionCredential(_ context.Context, project, account, by string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.rows[acctKey{project, account}]; !ok {
		return agentdb.ErrConnectionCredentialNotFound
	}
	delete(f.rows, acctKey{project, account})
	f.disconnects = append(f.disconnects, by)
	return nil
}

func (f *fakeConnectStore) row(project, account string) *agentdb.ConnectionCredential {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[acctKey{project, account}]
}

// fakeInvalidator records Invalidate calls.
type fakeInvalidator struct {
	mu    sync.Mutex
	calls []acctKey
}

func (f *fakeInvalidator) Invalidate(project, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, acctKey{project, account})
}

func (f *fakeInvalidator) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// ── rig ───────────────────────────────────────────────────────────────────────

const gcTestProjectMap = `{
  "users": {
    "kai@badcode.dev": ["*"],
    "richard@example.com": ["enc"],
    "viewer@example.com": ["enc"]
  },
  "projects": {
    "enc": {"operators": ["richard@example.com"]}
  }
}`

type gcRig struct {
	t        *testing.T
	google   *fakeGoogle
	store    *fakeConnectStore
	inval    *fakeInvalidator
	settings *projectSettingsHolder
	cfg      googleConnectConfig
	deps     googleConnectDeps
	logs     *logRecorder
	apiMux   *http.ServeMux
	root     *http.ServeMux
	now      time.Time
}

func newGCRig(t *testing.T, mutate ...func(*googleConnectDeps)) *gcRig {
	t.Helper()
	google := newFakeGoogle(t)
	cfg := testConnectConfig(t, "https://bob.example.test")
	cfg.tokenURL = google.srv.URL + "/token"
	cfg.tokeninfoURL = google.srv.URL + "/tokeninfo"
	cfg.revokeURL = google.srv.URL + "/revoke"

	reg, err := connections.NewRegistry(map[string]map[string]connections.Spec{
		"enc": {
			"gmail":  {Description: "ENC's Gmail", URL: "https://gmailmcp.googleapis.com/mcp/v1", Auth: connections.Auth{Type: connections.AuthGoogleAccount, Account: "google"}},
			"drive":  {Description: "ENC's Drive", URL: "https://drivemcp.googleapis.com/mcp/v1", Auth: connections.Auth{Type: connections.AuthGoogleAccount}},
			"github": {Description: "GitHub", URL: "https://api.githubcopilot.com/mcp/", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "ENC_GITHUB_TOKEN"}},
		},
	}, func(k string) string {
		if k == "ENC_GITHUB_TOKEN" {
			return "ghp-static-secret"
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := parseProjectSettings([]byte(gcTestProjectMap))
	if err != nil {
		t.Fatal(err)
	}
	holder := &projectSettingsHolder{}
	holder.ptr.Store(settings)

	store := newFakeConnectStore()
	accounts := newGoogleAccounts(store, cfg, func(string, ...any) {})
	reg.SetAccounts(accounts, "")

	r := &gcRig{
		t: t, google: google, store: store, inval: &fakeInvalidator{},
		settings: holder, cfg: cfg, logs: &logRecorder{},
		now: time.Unix(1_800_000_000, 0),
	}
	r.deps = googleConnectDeps{
		cfg:           cfg,
		registry:      reg,
		store:         store,
		accounts:      r.inval,
		stillOperator: connectOperatorRecheck(holder, ""),
		jwtSecretSet:  true,
		logf:          r.logs.logf,
		now:           func() time.Time { return r.now },
		httpClient:    google.srv.Client(),
	}
	for _, m := range mutate {
		m(&r.deps)
	}
	r.apiMux = http.NewServeMux()
	r.root = http.NewServeMux()
	registerGoogleConnect(r.apiMux, r.root, r.deps)
	return r
}

var (
	gcWildcardOperator = principal{email: "kai@badcode.dev", customer: "enc", operator: true}
	gcListedOperator   = principal{email: "richard@example.com", customer: "enc", operator: true}
	gcViewer           = principal{email: "viewer@example.com", customer: "enc"}
)

// api serves an authenticated request as p, skipping the middleware (the
// principal is exactly what apiAuthMiddleware would have set).
func (r *gcRig) api(p principal, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(contextWithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	r.apiMux.ServeHTTP(rec, req)
	return rec
}

// start presses Connect Google and returns the state and the cookie value.
func (r *gcRig) start(p principal, account string) (state, cookie string) {
	r.t.Helper()
	rec := r.api(p, http.MethodPost, "/agent/connections/"+account+"/connect")
	if rec.Code != http.StatusOK {
		r.t.Fatalf("connect: status %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AuthorizeURL string `json:"authorize_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		r.t.Fatalf("connect body: %v (%s)", err, rec.Body.String())
	}
	u, err := url.Parse(body.AuthorizeURL)
	if err != nil {
		r.t.Fatal(err)
	}
	state = u.Query().Get("state")
	for _, c := range rec.Result().Cookies() {
		if c.Name == googleConnectCookie {
			cookie = c.Value
		}
	}
	if state == "" || cookie == "" {
		r.t.Fatalf("connect: state %q cookie %q", state, cookie)
	}
	return state, cookie
}

// callback is Google sending the browser back. cookie "" sends no cookie.
func (r *gcRig) callback(query url.Values, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, googleConnectCallbackPath+"?"+query.Encode(), nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: googleConnectCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	r.root.ServeHTTP(rec, req)
	return rec
}

func okCallback(state string) url.Values {
	return url.Values{"state": {state}, "code": {gcTestCode}, "scope": {googleConnectScopes}}
}

// redirectResult parses a callback 303 into (path, query).
func redirectResult(t *testing.T, rec *httptest.ResponseRecorder) (string, url.Values) {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status %d, want 303; body %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host != "https://bob.example.test" {
		t.Fatalf("redirect %q is not on the public base URL", loc)
	}
	return loc.Path, loc.Query()
}

func wantReason(t *testing.T, rec *httptest.ResponseRecorder, reason string) {
	t.Helper()
	path, q := redirectResult(t, rec)
	if path != "/p/enc/settings" {
		t.Fatalf("redirect path %q", path)
	}
	if q.Get("connect") != "google" || q.Get("result") != "error" || q.Get("reason") != reason {
		t.Fatalf("redirect query %v, want result=error reason=%s", q, reason)
	}
}

// assertNoSecrets checks a body/log text for every secret value the flow
// handles.
func (r *gcRig) assertNoSecrets(where, text string, extra ...string) {
	r.t.Helper()
	for _, s := range append([]string{gcTestCode, gcTestRefresh, gcTestAccess, gcTestIDToken, r.cfg.clientSecret}, extra...) {
		if s != "" && strings.Contains(text, s) {
			r.t.Fatalf("%s contains a secret value %q:\n%s", where, s, text)
		}
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestGoogleConnect_HappyPath(t *testing.T) {
	r := newGCRig(t)
	state, cookie := r.start(gcListedOperator, "google")

	rec := r.callback(okCallback(state), cookie)
	path, q := redirectResult(t, rec)
	if path != "/p/enc/settings" || q.Get("connect") != "google" || q.Get("result") != "connected" || q.Has("reason") {
		t.Fatalf("redirect %s?%s", path, q.Encode())
	}

	row := r.store.row("enc", "google")
	if row == nil {
		t.Fatal("no row stored")
	}
	plain, err := r.cfg.sealer.Open(row.Nonce, row.Ciphertext, connections.AccountAAD("enc", "google"))
	if err != nil || string(plain) != gcTestRefresh {
		t.Fatalf("stored ciphertext opens to %q, %v", plain, err)
	}
	if row.AccountEmail != strings.ToLower(gcTestEmail) || row.ConnectedBy != "richard@example.com" ||
		row.Provider != "google" || row.KeyID != r.cfg.sealer.KeyID() || row.ConnectedAt != r.now.UnixMilli() {
		t.Fatalf("row metadata %+v", row)
	}
	if strings.Join(row.Scopes, " ") != strings.Join(requiredProductScopes, " ") {
		t.Fatalf("scopes %v", row.Scopes)
	}
	if r.inval.count() != 1 {
		t.Fatalf("Invalidate called %d times, want 1", r.inval.count())
	}

	// The exchange carried the code, the PKCE verifier and the redirect URI.
	calls := r.google.tokenCalls()
	if len(calls) != 1 {
		t.Fatalf("token requests %d", len(calls))
	}
	if calls[0].Get("code") != gcTestCode || calls[0].Get("code_verifier") == "" ||
		calls[0].Get("redirect_uri") != "https://bob.example.test"+googleConnectCallbackPath {
		t.Fatalf("token request %v", calls[0])
	}

	// The cookie is cleared, and the callback is never a referrer.
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == googleConnectCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("bob_connect cookie not cleared")
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Referrer-Policy %q", rec.Header().Get("Referrer-Policy"))
	}

	r.assertNoSecrets("redirect", rec.Header().Get("Location")+rec.Body.String(), state, cookie)
	logs := r.logs.joined()
	r.assertNoSecrets("logs", logs, state, cookie, string(row.Ciphertext))
	if !strings.Contains(logs, "result=connected") || !strings.Contains(logs, "richard@example.com") {
		t.Fatalf("logs lack the outcome line:\n%s", logs)
	}
}

func TestGoogleConnect_StartCookieAndURL(t *testing.T) {
	r := newGCRig(t)
	rec := r.api(gcWildcardOperator, http.MethodPost, "/agent/connections/google/connect")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var c *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == googleConnectCookie {
			c = ck
		}
	}
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/auth/connections/" || c.MaxAge != 600 || !c.Secure {
		t.Fatalf("cookie %+v", c)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	u, _ := url.Parse(body["authorize_url"])
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "https://bob.example.test"+googleConnectCallbackPath {
		t.Fatalf("authorize url %s", u)
	}
	// The state's nonce is the cookie.
	st, err := verifyState(r.cfg.sealer.StateKey(), q.Get("state"), r.now)
	if err != nil || st.Nonce != c.Value || st.Project != "enc" || st.Account != "google" || st.Email != "kai@badcode.dev" {
		t.Fatalf("state %+v %v", st, err)
	}
	if !strings.Contains(r.logs.joined(), "start") {
		t.Fatalf("no start log line: %s", r.logs.joined())
	}
	r.assertNoSecrets("logs", r.logs.joined(), q.Get("state"), c.Value)

	// Over http, the cookie is not Secure (localhost stack).
	local := newGCRig(t, func(d *googleConnectDeps) { d.cfg.publicBase = "http://localhost:8080" })
	rec = local.api(gcWildcardOperator, http.MethodPost, "/agent/connections/google/connect")
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == googleConnectCookie && ck.Secure {
			t.Fatal("cookie Secure over http")
		}
	}
}

func TestGoogleConnect_Authority(t *testing.T) {
	tests := []struct {
		name       string
		p          principal
		noSecret   bool
		wantStatus int
		wantInBody string
	}{
		{name: "wildcard operator JWT", p: gcWildcardOperator, wantStatus: http.StatusOK},
		{name: "operators-listed JWT", p: gcListedOperator, wantStatus: http.StatusOK},
		{name: "non-operator JWT", p: gcViewer, wantStatus: http.StatusForbidden, wantInBody: "operator"},
		{name: "API key", p: principal{email: apiKeyEmail("enc"), customer: "enc", apiKey: true, operator: true}, wantStatus: http.StatusForbidden},
		{name: "embed-scoped", p: principal{email: "kai@badcode.dev", customer: "enc", embedSession: "sess-1", operator: true}, wantStatus: http.StatusForbidden},
		{name: "dataset-scoped", p: principal{email: datasetTokenEmail("enc"), customer: "enc", datasetScope: "enc/prices", operator: true}, wantStatus: http.StatusForbidden},
		{name: "wildcard login token (no project)", p: principal{email: "kai@badcode.dev", customer: projectWildcard, operator: true}, wantStatus: http.StatusForbidden},
		{name: "dev-open", p: principal{email: "demo@example.com", customer: "enc", operator: true}, noSecret: true, wantStatus: http.StatusForbidden, wantInBody: "AGENTKIT_JWT_SECRET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newGCRig(t, func(d *googleConnectDeps) { d.jwtSecretSet = !tt.noSecret })
			for _, method := range []string{http.MethodPost, http.MethodDelete} {
				target := "/agent/connections/google/connect"
				if method == http.MethodDelete {
					target = "/agent/connections/google"
					// Something to disconnect, so an allowed caller gets 200.
					r.store.rows[acctKey{"enc", "google"}] = sealedRow(t, r.cfg, "enc", "google", gcTestRefresh, 1)
				}
				rec := r.api(tt.p, method, target)
				if rec.Code != tt.wantStatus {
					t.Fatalf("%s: status %d, want %d (%s)", method, rec.Code, tt.wantStatus, rec.Body.String())
				}
				if tt.wantStatus != http.StatusOK {
					var body map[string]string
					if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
						t.Fatalf("%s: error body %q", method, rec.Body.String())
					}
					if !strings.Contains(body["error"], tt.wantInBody) {
						t.Fatalf("%s: error %q lacks %q", method, body["error"], tt.wantInBody)
					}
				}
			}
		})
	}
}

// TestGoogleConnect_AuthorityThroughMiddleware: a real console JWT for an
// operators-listed, non-wildcard login reaches the route as an operator; the
// same login without the claim does not.
func TestGoogleConnect_AuthorityThroughMiddleware(t *testing.T) {
	r := newGCRig(t)
	secret := []byte("test-jwt-secret")
	h := apiAuthMiddleware(secret, (*projectKeyIndex)(nil), r.apiMux)
	issuer := devclaims.New(secret)
	for _, tc := range []struct {
		operator bool
		want     int
	}{{true, http.StatusOK}, {false, http.StatusForbidden}} {
		tok, err := issuer.IssueOperator(context.Background(), extension.ContextScope{UserEmail: "richard@example.com", Customer: "enc"}, "", tc.operator)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/agent/connections/google/connect", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("operator=%v: status %d want %d (%s)", tc.operator, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestGoogleConnect_StartRefusals(t *testing.T) {
	t.Run("account not declared", func(t *testing.T) {
		r := newGCRig(t)
		for _, target := range []string{"/agent/connections/work/connect", "/agent/connections/github/connect"} {
			if rec := r.api(gcWildcardOperator, http.MethodPost, target); rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status %d (%s)", target, rec.Code, rec.Body.String())
			}
		}
	})
	t.Run("disabled config", func(t *testing.T) {
		r := newGCRig(t, func(d *googleConnectDeps) {
			d.cfg.disabledReason = "Connect Google is off: GOOGLE_CLIENT_SECRET is not set"
		})
		rec := r.api(gcWildcardOperator, http.MethodPost, "/agent/connections/google/connect")
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "GOOGLE_CLIENT_SECRET") {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("pending connects full", func(t *testing.T) {
		r := newGCRig(t)
		for i := 0; i < pendingConnectsCap; i++ {
			if rec := r.api(gcWildcardOperator, http.MethodPost, "/agent/connections/google/connect"); rec.Code != http.StatusOK {
				t.Fatalf("connect %d: %d", i, rec.Code)
			}
		}
		if rec := r.api(gcWildcardOperator, http.MethodPost, "/agent/connections/google/connect"); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("past cap: status %d", rec.Code)
		}
	})
}

func TestGoogleConnect_CallbackRefusals(t *testing.T) {
	t.Run("bad signature is an HTML 400", func(t *testing.T) {
		r := newGCRig(t)
		state, cookie := r.start(gcListedOperator, "google")
		payload, sig, _ := strings.Cut(state, ".")
		forged := payload + "." + strings.Repeat("A", len(sig))
		rec := r.callback(okCallback(forged), cookie)
		if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Body.String(), `href="https://bob.example.test"`) {
			t.Fatalf("page lacks the public base link: %s", rec.Body.String())
		}
		if rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal("HTML page must not leak the callback URL as a referrer")
		}
		r.assertNoSecrets("page", rec.Body.String(), state, forged, cookie)
		if r.store.row("enc", "google") != nil {
			t.Fatal("stored on a forged state")
		}
		// No state at all is the same page.
		if rec := r.callback(url.Values{"code": {gcTestCode}}, cookie); rec.Code != http.StatusBadRequest {
			t.Fatalf("no state: %d", rec.Code)
		}
	})

	t.Run("expired", func(t *testing.T) {
		r := newGCRig(t)
		state, cookie := r.start(gcListedOperator, "google")
		r.now = r.now.Add(googleConnectTTL + time.Second)
		wantReason(t, r.callback(okCallback(state), cookie), "expired")
		if len(r.google.tokenCalls()) != 0 {
			t.Fatal("exchanged an expired state")
		}
	})

	t.Run("no cookie / other cookie does not consume the pending entry", func(t *testing.T) {
		r := newGCRig(t)
		state, cookie := r.start(gcListedOperator, "google")
		wantReason(t, r.callback(okCallback(state), ""), "other_browser")
		wantReason(t, r.callback(okCallback(state), "someone-elses-nonce"), "other_browser")
		if len(r.google.tokenCalls()) != 0 || r.store.row("enc", "google") != nil {
			t.Fatal("other_browser reached the exchange")
		}
		// The right browser still completes.
		path, q := redirectResult(t, r.callback(okCallback(state), cookie))
		if path != "/p/enc/settings" || q.Get("result") != "connected" {
			t.Fatalf("right browser after a wrong one: %s %v", path, q)
		}
		r.assertNoSecrets("logs", r.logs.joined(), state, cookie, "someone-elses-nonce")
	})

	t.Run("replayed callback", func(t *testing.T) {
		r := newGCRig(t)
		state, cookie := r.start(gcListedOperator, "google")
		redirectResult(t, r.callback(okCallback(state), cookie))
		wantReason(t, r.callback(okCallback(state), cookie), "expired")
		if len(r.google.tokenCalls()) != 1 || r.store.puts != 1 {
			t.Fatalf("replay exchanged again: %d token calls, %d puts", len(r.google.tokenCalls()), r.store.puts)
		}
	})

	t.Run("access_denied is cancelled", func(t *testing.T) {
		r := newGCRig(t)
		state, cookie := r.start(gcListedOperator, "google")
		wantReason(t, r.callback(url.Values{"state": {state}, "error": {"access_denied"}}, cookie), "cancelled")
		if len(r.google.tokenCalls()) != 0 {
			t.Fatal("exchanged after cancel")
		}
	})

	t.Run("no refresh token", func(t *testing.T) {
		r := newGCRig(t)
		r.google.set(func(g *fakeGoogle) { g.noRefresh = true })
		state, cookie := r.start(gcListedOperator, "google")
		wantReason(t, r.callback(okCallback(state), cookie), "no_refresh_token")
		if r.store.row("enc", "google") != nil || r.inval.count() != 0 {
			t.Fatal("stored without a refresh token")
		}
	})

	t.Run("missing gmail.compose", func(t *testing.T) {
		r := newGCRig(t)
		r.google.set(func(g *fakeGoogle) {
			g.scope = strings.Replace(googleConnectScopes, "https://www.googleapis.com/auth/gmail.compose", "", 1)
		})
		state, cookie := r.start(gcListedOperator, "google")
		rec := r.callback(okCallback(state), cookie)
		wantReason(t, rec, "missing_scopes")
		_, q := redirectResult(t, rec)
		if q.Get("missing") != "gmail.compose" {
			t.Fatalf("missing = %q, want gmail.compose", q.Get("missing"))
		}
		if r.store.row("enc", "google") != nil {
			t.Fatal("stored with a missing scope")
		}
	})

	t.Run("aud mismatch", func(t *testing.T) {
		r := newGCRig(t)
		r.google.set(func(g *fakeGoogle) { g.aud = "someone-else.apps.googleusercontent.com" })
		state, cookie := r.start(gcListedOperator, "google")
		wantReason(t, r.callback(okCallback(state), cookie), "exchange_failed")
		if r.store.row("enc", "google") != nil {
			t.Fatal("stored with a foreign audience")
		}
		r.assertNoSecrets("logs", r.logs.joined(), state, cookie)
	})

	t.Run("token endpoint refuses", func(t *testing.T) {
		r := newGCRig(t)
		r.google.set(func(g *fakeGoogle) { g.tokenStatus = http.StatusBadRequest })
		state, cookie := r.start(gcListedOperator, "google")
		wantReason(t, r.callback(okCallback(state), cookie), "exchange_failed")
		r.assertNoSecrets("logs", r.logs.joined(), state, cookie)
	})

	t.Run("operator removed from the map mid-flow", func(t *testing.T) {
		r := newGCRig(t)
		state, cookie := r.start(gcListedOperator, "google")
		without, err := parseProjectSettings([]byte(`{"users": {"richard@example.com": ["enc"]}, "projects": {"enc": {}}}`))
		if err != nil {
			t.Fatal(err)
		}
		r.settings.ptr.Store(without)
		wantReason(t, r.callback(okCallback(state), cookie), "not_allowed")
		if r.store.row("enc", "google") != nil {
			t.Fatal("stored for a removed operator")
		}
	})

	t.Run("store failure", func(t *testing.T) {
		r := newGCRig(t)
		r.store.putErr = errors.New("postgres is down")
		state, cookie := r.start(gcListedOperator, "google")
		wantReason(t, r.callback(okCallback(state), cookie), "store_failed")
		if r.inval.count() != 0 {
			t.Fatal("invalidated after a failed store")
		}
	})
}

func TestGoogleConnect_Reconnect(t *testing.T) {
	r := newGCRig(t)
	state, cookie := r.start(gcListedOperator, "google")
	redirectResult(t, r.callback(okCallback(state), cookie))
	first := r.store.row("enc", "google")

	r.now = r.now.Add(time.Hour)
	state, cookie = r.start(gcWildcardOperator, "google")
	redirectResult(t, r.callback(okCallback(state), cookie))
	second := r.store.row("enc", "google")
	if r.store.puts != 2 || second.ConnectedAt == first.ConnectedAt || second.ConnectedBy != "kai@badcode.dev" {
		t.Fatalf("reconnect did not replace: puts %d, %+v", r.store.puts, second)
	}
	if string(second.Nonce) == string(first.Nonce) {
		t.Fatal("reconnect reused the seal nonce")
	}
}

func TestGoogleConnect_Disconnect(t *testing.T) {
	for _, tc := range []struct {
		name        string
		revoke      int
		wantRevoked bool
	}{
		{"revoke ok", http.StatusOK, true},
		{"revoke fails", http.StatusInternalServerError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newGCRig(t)
			r.google.set(func(g *fakeGoogle) { g.revokeStatus = tc.revoke })
			r.store.rows[acctKey{"enc", "google"}] = sealedRow(t, r.cfg, "enc", "google", gcTestRefresh, 5)

			rec := r.api(gcListedOperator, http.MethodDelete, "/agent/connections/google")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d %s", rec.Code, rec.Body.String())
			}
			var body map[string]bool
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["revoked"] != tc.wantRevoked {
				t.Fatalf("body %s, want revoked=%v", rec.Body.String(), tc.wantRevoked)
			}
			if got := r.google.revokedTokens(); len(got) != 1 || got[0] != gcTestRefresh {
				t.Fatalf("revoke called with %v", got)
			}
			if r.store.row("enc", "google") != nil {
				t.Fatal("row not deleted")
			}
			if len(r.store.disconnects) != 1 || r.store.disconnects[0] != "richard@example.com" {
				t.Fatalf("disconnected_by %v", r.store.disconnects)
			}
			if r.inval.count() != 1 {
				t.Fatalf("Invalidate %d", r.inval.count())
			}
			logs := r.logs.joined()
			r.assertNoSecrets("logs", logs)
			if !strings.Contains(logs, "disconnect") {
				t.Fatalf("no disconnect line: %s", logs)
			}

			// Second disconnect: nothing there.
			if rec := r.api(gcListedOperator, http.MethodDelete, "/agent/connections/google"); rec.Code != http.StatusNotFound {
				t.Fatalf("second disconnect: %d", rec.Code)
			}
		})
	}

	t.Run("sealed with another key still deletes, revoked false", func(t *testing.T) {
		r := newGCRig(t)
		row := sealedRow(t, r.cfg, "enc", "google", gcTestRefresh, 5)
		row.KeyID = "0000000000000000"
		r.store.rows[acctKey{"enc", "google"}] = row
		rec := r.api(gcListedOperator, http.MethodDelete, "/agent/connections/google")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revoked":false`) {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		if len(r.google.revokedTokens()) != 0 || r.store.row("enc", "google") != nil {
			t.Fatal("wrong-key disconnect revoked or kept the row")
		}
	})
}

func TestGoogleConnect_ListConnections(t *testing.T) {
	r := newGCRig(t)
	stored := sealedRow(t, r.cfg, "enc", "google", gcTestRefresh, 1_700_000_000_000)
	r.store.rows[acctKey{"enc", "google"}] = stored

	rec := r.api(gcViewer, http.MethodGet, "/agent/connections")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	for _, secret := range []string{
		gcTestRefresh, gcTestAccess, "ghp-static-secret", stored.KeyID,
		string(stored.Ciphertext), string(stored.Nonce),
		b64(stored.Ciphertext), b64(stored.Nonce), "ciphertext", "nonce", "key_id",
	} {
		if strings.Contains(raw, secret) {
			t.Fatalf("list body contains %q:\n%s", secret, raw)
		}
	}

	var got struct {
		Connections []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Account     string `json:"account"`
			Available   bool   `json:"available"`
			Unavailable string `json:"unavailable"`
		} `json:"connections"`
		Accounts []struct {
			Account      string   `json:"account"`
			Provider     string   `json:"provider"`
			Connected    bool     `json:"connected"`
			AccountEmail string   `json:"account_email"`
			ConnectedBy  string   `json:"connected_by"`
			ConnectedAt  int64    `json:"connected_at"`
			Unavailable  string   `json:"unavailable"`
			Connections  []string `json:"connections"`
		} `json:"accounts"`
		CanConnect            bool   `json:"can_connect"`
		ConnectDisabledReason string `json:"connect_disabled_reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Connections) != 3 || got.Connections[0].Name != "drive" || got.Connections[0].Account != "google" ||
		!got.Connections[0].Available || got.Connections[1].Name != "github" || got.Connections[1].Account != "" {
		t.Fatalf("connections %+v", got.Connections)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("accounts %+v", got.Accounts)
	}
	a := got.Accounts[0]
	if a.Account != "google" || a.Provider != "google" || !a.Connected || a.AccountEmail != stored.AccountEmail ||
		a.ConnectedBy != stored.ConnectedBy || a.ConnectedAt != stored.ConnectedAt || a.Unavailable != "" ||
		strings.Join(a.Connections, ",") != "drive,gmail" {
		t.Fatalf("account %+v", a)
	}
	// A viewer cannot connect, and is told why.
	if got.CanConnect || !strings.Contains(got.ConnectDisabledReason, "operator") {
		t.Fatalf("viewer can_connect=%v reason=%q", got.CanConnect, got.ConnectDisabledReason)
	}
	// The raw body leaves out optional fields rather than sending "".
	if !strings.Contains(raw, `"can_connect":false`) || strings.Contains(raw, `"account":""`) {
		t.Fatalf("raw body shape: %s", raw)
	}

	t.Run("operator can connect", func(t *testing.T) {
		rec := r.api(gcListedOperator, http.MethodGet, "/agent/connections")
		if !strings.Contains(rec.Body.String(), `"can_connect":true`) || strings.Contains(rec.Body.String(), "connect_disabled_reason") {
			t.Fatalf("operator body %s", rec.Body.String())
		}
	})
	t.Run("disabled config says why", func(t *testing.T) {
		r := newGCRig(t, func(d *googleConnectDeps) {
			d.cfg.disabledReason = "Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set"
		})
		rec := r.api(gcWildcardOperator, http.MethodGet, "/agent/connections")
		if !strings.Contains(rec.Body.String(), `"can_connect":false`) || !strings.Contains(rec.Body.String(), "AGENTKIT_CONNECTIONS_KEY") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})
	t.Run("wrong key is unavailable", func(t *testing.T) {
		r := newGCRig(t)
		row := sealedRow(t, r.cfg, "enc", "google", gcTestRefresh, 1)
		row.KeyID = "0000000000000000"
		r.store.rows[acctKey{"enc", "google"}] = row
		rec := r.api(gcWildcardOperator, http.MethodGet, "/agent/connections")
		if !strings.Contains(rec.Body.String(), "encryption key changed") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})
	t.Run("embed and dataset scopes are refused", func(t *testing.T) {
		for _, p := range []principal{
			{email: "x", customer: "enc", embedSession: "s1"},
			{email: "x", customer: "enc", datasetScope: "enc/d"},
			{email: "x"},
		} {
			if rec := r.api(p, http.MethodGet, "/agent/connections"); rec.Code != http.StatusForbidden {
				t.Fatalf("%+v: status %d", p, rec.Code)
			}
		}
	})
	t.Run("a project with no connections is an empty list, not null", func(t *testing.T) {
		rec := r.api(principal{email: "kai@badcode.dev", customer: "other", operator: true}, http.MethodGet, "/agent/connections")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"connections":[]`) || !strings.Contains(rec.Body.String(), `"accounts":[]`) {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
	})
}

func TestGoogleConnect_OperatorRecheck(t *testing.T) {
	settings, err := parseProjectSettings([]byte(gcTestProjectMap))
	if err != nil {
		t.Fatal(err)
	}
	holder := &projectSettingsHolder{}
	holder.ptr.Store(settings)
	check := connectOperatorRecheck(holder, "test@example.com")
	for _, tc := range []struct {
		project, email string
		want           bool
	}{
		{"enc", "kai@badcode.dev", true},     // wildcard users entry
		{"enc", "richard@example.com", true}, // operators list
		{"other", "richard@example.com", false},
		{"enc", "viewer@example.com", false},
		{"enc", "test@example.com", true}, // AGENTKIT_TEST_LOGIN is an implicit wildcard
		{"enc", "stranger@example.com", false},
	} {
		if got := check(tc.project, tc.email); got != tc.want {
			t.Errorf("stillOperator(%q, %q) = %v, want %v", tc.project, tc.email, got, tc.want)
		}
	}
	if connectOperatorRecheck(nil, "")("enc", "kai@badcode.dev") {
		t.Error("no map, no test login: nobody is an operator")
	}
}

// TestGoogleConnect_RegistersBesideTheRealAPIMux: http.ServeMux panics on a
// conflicting pattern, so mounting on httpapi's own mux (what T24 does) must
// not collide with any route it already has, and the callback must win over
// root's "/" catch-all.
func TestGoogleConnect_RegistersBesideTheRealAPIMux(t *testing.T) {
	api, err := httpapi.New(httpapi.Config{Runner: &stubRunner{}, Store: newFakeRouterStore(), Identity: identityFromRequest})
	if err != nil {
		t.Fatal(err)
	}
	r := newGCRig(t)
	apiMux := api.Mux()
	root := http.NewServeMux()
	registerGoogleConnect(apiMux, root, r.deps)
	root.Handle("/", apiAuthMiddleware([]byte("s"), (*projectKeyIndex)(nil), apiMux))

	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, googleConnectCallbackPath+"?state=x", nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Connect Google") {
		t.Fatalf("callback through root: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agent/connections", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("list without a credential: %d", rec.Code)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func sealedRow(t *testing.T, cfg googleConnectConfig, project, account, refresh string, connectedAt int64) *agentdb.ConnectionCredential {
	t.Helper()
	nonce, ct, err := cfg.sealer.Seal([]byte(refresh), connections.AccountAAD(project, account))
	if err != nil {
		t.Fatal(err)
	}
	return &agentdb.ConnectionCredential{
		Project: project, Account: account, Provider: "google",
		AccountEmail: "emperor.enc@gmail.com", Scopes: requiredProductScopes,
		KeyID: cfg.sealer.KeyID(), Nonce: nonce, Ciphertext: ct,
		ConnectedBy: "richard@example.com", ConnectedAt: connectedAt,
	}
}

func b64(b []byte) string {
	return strings.TrimRight(jsonBase64(b), "=")
}

// jsonBase64 is how encoding/json would spell a []byte.
func jsonBase64(b []byte) string {
	out, _ := json.Marshal(b)
	return strings.Trim(string(out), `"`)
}
