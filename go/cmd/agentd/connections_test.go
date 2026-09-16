package main

// connections_test.go — T12: agentd's wiring of project connections
// (design/2026-09-11-project-connections.md). go/connections/proxy_test.go
// already pins every row of the proxy's status table against fakes; what is
// proved HERE is the adapter agentd supplies to it — that the caller comes
// from the real session-token verifier under the live-only rule, that the
// worker is read from the session ROW and never from anything the container
// sent, that a disabled or missing worker is "gone" rather than a store
// failure — plus the boot-time pieces main.go leans on: the route is absent
// without a database, the dual-map warning, and the logged resolver.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
)

// connectTestWorkers is the worker read seam the handler takes, backed by a
// map. err, when set, is returned for every read (the 503 row).
type connectTestWorkers struct {
	workers map[string]*agentdb.Worker // key: project + "/" + name
	err     error
	reads   []string
}

func (f *connectTestWorkers) GetWorker(_ context.Context, project, name string) (*agentdb.Worker, error) {
	f.reads = append(f.reads, project+"/"+name)
	if f.err != nil {
		return nil, f.err
	}
	w, ok := f.workers[project+"/"+name]
	if !ok {
		return nil, agentdb.ErrWorkerNotFound
	}
	return w, nil
}

// logRecorder collects formatted log lines; safe for the proxy's deferred
// per-request line.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (l *logRecorder) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logRecorder) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// connectRig is one project ("acme") with one connection ("github") pointed
// at a fake upstream that records what it was sent.
type connectRig struct {
	secret   []byte
	sessions *fakeSessionLookup
	workers  *connectTestWorkers
	handler  http.Handler
	upstream *httptest.Server

	mu        sync.Mutex
	contacted int
	gotAuth   string
}

func newConnectRig(t *testing.T) *connectRig {
	t.Helper()
	rig := &connectRig{
		secret: []byte("s3cret"),
		sessions: &fakeSessionLookup{sessions: map[string]*agentdb.Session{
			"live":     {ID: "live", Customer: "acme", Worker: "researcher", Status: "running"},
			"archived": {ID: "archived", Customer: "acme", Worker: "researcher", Status: "running", SnapshotState: "archived"},
			"chat":     {ID: "chat", Customer: "acme", Status: "running"},
			"idle":     {ID: "idle", Customer: "acme", Worker: "idler", Status: "running"},
			"ghost":    {ID: "ghost", Customer: "acme", Worker: "deleted-one", Status: "running"},
		}},
		workers: &connectTestWorkers{workers: map[string]*agentdb.Worker{
			"acme/researcher": {Project: "acme", Name: "researcher", Enabled: true, Connections: agentdb.ConnectionList{"github"}},
			"acme/idler":      {Project: "acme", Name: "idler", Enabled: false, Connections: agentdb.ConnectionList{"github"}},
			"acme/bystander":  {Project: "acme", Name: "bystander", Enabled: true, Connections: agentdb.ConnectionList{}},
		}},
	}
	rig.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.mu.Lock()
		rig.contacted++
		rig.gotAuth = r.Header.Get("Authorization")
		rig.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(rig.upstream.Close)

	reg, err := connections.NewRegistry(map[string]map[string]connections.Spec{
		"acme": {"github": {
			Description: "repos",
			URL:         rig.upstream.URL + "/mcp/",
			Auth:        connections.Auth{Type: connections.AuthBearer, TokenEnv: "ACME_GITHUB_PAT"},
		}},
	}, func(k string) string {
		if k == "ACME_GITHUB_PAT" {
			return "github_pat_REAL"
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	rig.handler = newConnectHandler(reg, newSessionTokenAuth(rig.secret, rig.sessions), rig.workers, quietf)
	return rig
}

// call posts one MCP request to /connect/github/ with token in Authorization
// (bare, as the sandbox sends ${SESSION_TOKEN}) plus any extra headers.
func (rig *connectRig) call(t *testing.T, token string, extra map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/connect/github/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	rig.handler.ServeHTTP(rec, req)
	return rec
}

func (rig *connectRig) upstreamCalls() int {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	return rig.contacted
}

// TestConnectHandler is the T12 acceptance table for the adapter.
func TestConnectHandler(t *testing.T) {
	cases := []struct {
		name      string
		sessionID string
		ttl       time.Duration
		noToken   bool
		badSecret bool
		extra     map[string]string
		storeErr  error
		wantCode  int
		wantError string // substring of the JSON error body; "" for a forwarded call
		forwarded bool
	}{
		{name: "valid token, worker holds the connection → forwarded",
			sessionID: "live", ttl: time.Hour, wantCode: 200, forwarded: true},
		{name: "expired token, live session → forwarded",
			sessionID: "live", ttl: -time.Minute, wantCode: 200, forwarded: true},
		{name: "expired token, archived session → 401 expired",
			sessionID: "archived", ttl: -time.Minute, wantCode: 401, wantError: "session token expired"},
		{name: "unexpired token, archived session → still honoured (only expiry is live-gated)",
			sessionID: "archived", ttl: time.Hour, wantCode: 200, forwarded: true},
		{name: "no token → 401 rejected",
			noToken: true, wantCode: 401, wantError: "session token rejected"},
		{name: "wrong-secret token → 401 rejected",
			sessionID: "live", ttl: time.Hour, badSecret: true, wantCode: 401, wantError: "session token rejected"},
		{name: "unknown session → 401 rejected",
			sessionID: "nope", ttl: time.Hour, wantCode: 401, wantError: "session token rejected"},
		{name: "session with no worker → 403",
			sessionID: "chat", ttl: time.Hour, wantCode: 403, wantError: "not running as a worker"},
		{name: "a header naming a worker is ignored: the chat session is still workerless",
			sessionID: "chat", ttl: time.Hour,
			extra:    map[string]string{"X-Worker": "researcher", "X-Agentkit-Worker": "researcher", "X-Session-Id": "live"},
			wantCode: 403, wantError: "not running as a worker"},
		{name: "disabled worker → 403 gone",
			sessionID: "idle", ttl: time.Hour, wantCode: 403, wantError: "worker idler is disabled or gone"},
		{name: "deleted worker → 403 gone",
			sessionID: "ghost", ttl: time.Hour, wantCode: 403, wantError: "worker deleted-one is disabled or gone"},
		{name: "store error reading the worker → 503",
			sessionID: "live", ttl: time.Hour, storeErr: errors.New("connection refused"),
			wantCode: 503, wantError: "could not read worker researcher's connections"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newConnectRig(t)
			rig.workers.err = tc.storeErr
			token := ""
			if !tc.noToken {
				secret := rig.secret
				if tc.badSecret {
					secret = []byte("some-other-secret")
				}
				token = mintSessionToken(t, secret, tc.ttl, "acme", tc.sessionID)
			}
			rec := rig.call(t, token, tc.extra)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body)
			}
			if tc.wantError != "" && !strings.Contains(rec.Body.String(), tc.wantError) {
				t.Fatalf("body = %s, want it to contain %q", rec.Body, tc.wantError)
			}
			if got := rig.upstreamCalls(); (got > 0) != tc.forwarded {
				t.Fatalf("upstream contacted %d time(s), forwarded want %v", got, tc.forwarded)
			}
			if tc.forwarded {
				rig.mu.Lock()
				gotAuth := rig.gotAuth
				rig.mu.Unlock()
				if gotAuth != "Bearer github_pat_REAL" {
					t.Fatalf("upstream Authorization = %q, want the real credential", gotAuth)
				}
			}
		})
	}
}

// TestConnectHandlerReadsTheWorkerFromTheSessionRow pins Decision 4's "the
// worker is Session.Worker from the session row — never a header": a session
// running as a worker with NO grant stays refused even when the request
// claims, in every header a container might try, to be a worker that holds
// the connection; and the grant lookup is for the row's worker only.
func TestConnectHandlerReadsTheWorkerFromTheSessionRow(t *testing.T) {
	rig := newConnectRig(t)
	rig.sessions.sessions["bystander-session"] = &agentdb.Session{
		ID: "bystander-session", Customer: "acme", Worker: "bystander", Persona: "researcher", Status: "running",
	}
	token := mintSessionToken(t, rig.secret, time.Hour, "acme", "bystander-session")
	rec := rig.call(t, token, map[string]string{
		"X-Worker":          "researcher",
		"X-Agentkit-Worker": "researcher",
		"X-Persona":         "researcher",
	})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "worker bystander does not hold connection github") {
		t.Fatalf("status = %d body = %s; want 403 naming the ROW's worker (bystander), not the persona or a header", rec.Code, rec.Body)
	}
	if len(rig.workers.reads) != 1 || rig.workers.reads[0] != "acme/bystander" {
		t.Fatalf("worker reads = %v, want exactly [acme/bystander]", rig.workers.reads)
	}
	if rig.upstreamCalls() != 0 {
		t.Fatal("upstream must not be contacted for a worker that does not hold the connection")
	}
}

// TestConnectHandlerRereadsGrantsEachRequest: revoking a grant between two
// requests of the same session flips 200 → 403 without a restart.
func TestConnectHandlerRereadsGrantsEachRequest(t *testing.T) {
	rig := newConnectRig(t)
	token := mintSessionToken(t, rig.secret, time.Hour, "acme", "live")
	if rec := rig.call(t, token, nil); rec.Code != http.StatusOK {
		t.Fatalf("first call: %d %s", rec.Code, rec.Body)
	}
	rig.workers.workers["acme/researcher"].Connections = agentdb.ConnectionList{}
	if rec := rig.call(t, token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("after revoke: %d %s, want 403", rec.Code, rec.Body)
	}
}

// TestMountConnectRoute: /connect/ is mounted only when there is a worker
// store (Postgres). On the SQLite fallback there are no workers to hold a
// grant, and the route must be absent (404) rather than a handler that
// 503s — or panics on a nil *agentdb.Store boxed into the interface.
func TestMountConnectRoute(t *testing.T) {
	reg, err := connections.NewRegistry(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := newSessionTokenAuth([]byte("s3cret"), nil)

	t.Run("no database → route absent", func(t *testing.T) {
		mux := http.NewServeMux()
		var noStore connectWorkerLookup
		if mountConnectRoute(mux, reg, auth, noStore, quietf) {
			t.Fatal("mountConnectRoute reported mounting with no worker store")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/connect/github/", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (route absent)", rec.Code)
		}
	})

	t.Run("typed-nil store → route absent, no panic", func(t *testing.T) {
		mux := http.NewServeMux()
		var nilStore *agentdb.Store
		if mountConnectRoute(mux, reg, auth, nilStore, quietf) {
			t.Fatal("a nil *agentdb.Store must count as no database")
		}
	})

	t.Run("database → route mounted", func(t *testing.T) {
		mux := http.NewServeMux()
		if !mountConnectRoute(mux, reg, auth, &connectTestWorkers{}, quietf) {
			t.Fatal("mountConnectRoute did not mount with a worker store")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/connect/github/", nil))
		// Mounted, and the proxy's own first refusal (no token) answers.
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 from the mounted proxy", rec.Code)
		}
	})
}

// TestWarnIfBothProjectMaps: an inline AGENTKIT_PROJECT_MAP silently wins
// over AGENTKIT_PROJECT_MAP_FILE (loadProjectSettings), so a connections
// block added to the file would do nothing. agentd says so, loudly.
func TestWarnIfBothProjectMaps(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantWarn bool
	}{
		{name: "neither", env: map[string]string{}},
		{name: "inline only", env: map[string]string{"AGENTKIT_PROJECT_MAP": `{"a@b.c":["x"]}`}},
		{name: "file only", env: map[string]string{"AGENTKIT_PROJECT_MAP_FILE": "/etc/agent-bob/project-map.json"}},
		{name: "both", env: map[string]string{
			"AGENTKIT_PROJECT_MAP":      `{"a@b.c":["x"]}`,
			"AGENTKIT_PROJECT_MAP_FILE": "/etc/agent-bob/project-map.json",
		}, wantWarn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logRecorder{}
			got := warnIfBothProjectMaps(func(k string) string { return tc.env[k] }, logs.logf)
			if got != tc.wantWarn {
				t.Fatalf("warned = %v, want %v", got, tc.wantWarn)
			}
			out := logs.joined()
			if tc.wantWarn {
				for _, want := range []string{"WARNING", "AGENTKIT_PROJECT_MAP", "/etc/agent-bob/project-map.json", "ignored"} {
					if !strings.Contains(out, want) {
						t.Errorf("warning %q does not mention %q", out, want)
					}
				}
				if strings.Contains(out, `a@b.c`) {
					t.Errorf("warning must not echo the inline map's content: %q", out)
				}
			} else if out != "" {
				t.Errorf("unexpected log output: %q", out)
			}
		})
	}
}

// TestBuildConnectionRegistry: one boot line per project naming each
// connection and its availability — never an env value.
func TestBuildConnectionRegistry(t *testing.T) {
	specs := map[string]map[string]connections.Spec{
		"wolf": {
			"github": {URL: "https://api.githubcopilot.com/mcp/", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "WOLF_GITHUB_PAT"}},
			"docs":   {URL: "https://docsmcp.googleapis.com/mcp/v1", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "WOLF_DOCS_TOKEN"}},
		},
		"acme": {
			"github": {URL: "https://api.githubcopilot.com/mcp/", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "ACME_GITHUB_PAT"}},
		},
	}
	env := map[string]string{"WOLF_GITHUB_PAT": "github_pat_SECRETVALUE", "ACME_GITHUB_PAT": "github_pat_OTHERSECRET"}
	logs := &logRecorder{}
	reg, err := buildConnectionRegistry(specs, func(k string) string { return env[k] }, logs.logf)
	if err != nil {
		t.Fatalf("buildConnectionRegistry: %v", err)
	}
	if got := reg.Names("wolf"); strings.Join(got, ",") != "docs,github" {
		t.Fatalf("wolf names = %v", got)
	}
	out := logs.joined()
	for _, want := range []string{
		`project "acme": github=available`,
		`project "wolf": docs=UNAVAILABLE (env var WOLF_DOCS_TOKEN is not set), github=available`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("boot log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SECRETVALUE") || strings.Contains(out, "OTHERSECRET") {
		t.Fatalf("boot log leaked a credential value:\n%s", out)
	}
	if strings.Index(out, `connections: project "acme":`) > strings.Index(out, `connections: project "wolf":`) {
		t.Errorf("projects should be logged in sorted order:\n%s", out)
	}

	t.Run("no map → empty registry, one line saying so", func(t *testing.T) {
		logs := &logRecorder{}
		reg, err := buildConnectionRegistry(nil, func(string) string { return "" }, logs.logf)
		if err != nil || reg == nil {
			t.Fatalf("reg=%v err=%v; want an empty non-nil registry", reg, err)
		}
		if reg.Names("wolf") != nil {
			t.Fatal("empty registry should have no names")
		}
		if !strings.Contains(logs.joined(), "no project has connections") {
			t.Errorf("log = %q", logs.joined())
		}
	})

	t.Run("invalid spec fails the boot", func(t *testing.T) {
		_, err := buildConnectionRegistry(map[string]map[string]connections.Spec{
			"wolf": {"core": {URL: "https://x.example/", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "X"}}},
		}, func(string) string { return "v" }, quietf)
		if err == nil {
			t.Fatal("a reserved connection name must fail the boot")
		}
	})
}

// TestConnectionServersFor: the closure main.go hands the dispatcher and the
// session-context provider resolves grants to /connect/ entries, and logs
// the grants it had to skip (so a worker quietly missing a tool is
// explainable from agentd's log) — naming env vars, never values.
func TestConnectionServersFor(t *testing.T) {
	if connectionServersFor(nil, "http://self", quietf) != nil {
		t.Fatal("a nil registry must yield a nil resolver (no connections layer)")
	}
	reg, err := connections.NewRegistry(map[string]map[string]connections.Spec{
		"wolf": {
			"github": {URL: "https://api.githubcopilot.com/mcp/", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "WOLF_GITHUB_PAT"}},
			"gmail":  {URL: "https://gmailmcp.googleapis.com/mcp/v1", Auth: connections.Auth{Type: connections.AuthBearer, TokenEnv: "WOLF_GMAIL_TOKEN"}},
		},
	}, func(k string) string {
		if k == "WOLF_GITHUB_PAT" {
			return "github_pat_SECRET"
		}
		return ""
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		grants   []string
		wantKeys string
		wantLogs []string
	}{
		{name: "no grants", grants: nil, wantKeys: ""},
		{name: "one available grant", grants: []string{"github"}, wantKeys: "github"},
		{name: "wildcard skips the unavailable one and says why", grants: []string{"*"}, wantKeys: "github",
			wantLogs: []string{`project "wolf"`, `gmail`, `WOLF_GMAIL_TOKEN`}},
		{name: "a stale grant naming no connection is logged", grants: []string{"github", "notion"}, wantKeys: "github",
			wantLogs: []string{`notion`, `no such connection`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logRecorder{}
			resolve := connectionServersFor(reg, "http://172.17.0.1:8099/", logs.logf)
			got := resolve("wolf", tc.grants)
			keys := make([]string, 0, len(got))
			for k := range got {
				keys = append(keys, k)
			}
			if strings.Join(sortedStrings(keys), ",") != tc.wantKeys {
				t.Fatalf("servers = %v, want keys %q", got, tc.wantKeys)
			}
			if e, ok := got["github"]; ok {
				if e.URL != "http://172.17.0.1:8099/connect/github/" || e.Headers["Authorization"] != "${SESSION_TOKEN}" {
					t.Fatalf("github entry = %+v", e)
				}
			}
			out := logs.joined()
			for _, want := range tc.wantLogs {
				if !strings.Contains(out, want) {
					t.Errorf("log %q missing %q", out, want)
				}
			}
			if len(tc.wantLogs) == 0 && out != "" {
				t.Errorf("unexpected log: %q", out)
			}
			if strings.Contains(out, "SECRET") {
				t.Fatalf("log leaked a value: %q", out)
			}
		})
	}
}
