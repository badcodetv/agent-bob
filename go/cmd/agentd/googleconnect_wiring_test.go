package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
)

// googleconnect_wiring_test.go — T24: the one thing main.go does with
// Connect Google. wireGoogleConnect is exercised directly rather than
// through main() (which is not callable from a test): a fresh Registry, a
// fresh pair of muxes, and either a nil store (no DATABASE_URL) or a fake
// one (agentDB present) — the same two worlds main.go's `agentDB != nil`
// branches everywhere else.

func wiringRegistry(t *testing.T) *connections.Registry {
	t.Helper()
	reg, err := connections.NewRegistry(map[string]map[string]connections.Spec{
		"enc": {
			"gmail": {Description: "ENC's Gmail", URL: "https://gmailmcp.googleapis.com/mcp/v1", Auth: connections.Auth{Type: connections.AuthGoogleAccount, Account: "google"}},
		},
	}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func wiringSettings(t *testing.T) *projectSettingsHolder {
	t.Helper()
	settings, err := parseProjectSettings([]byte(gcTestProjectMap))
	if err != nil {
		t.Fatal(err)
	}
	holder := &projectSettingsHolder{}
	holder.ptr.Store(settings)
	return holder
}

// TestWireGoogleConnect_NoStore: the sqlite fallback (agentDB nil). The
// routes must be absent — mounting them on a nil store would panic the
// first request the T13 nil-store trap this package keeps naming — and the
// Registry must report the account unavailable with the DATABASE_URL reason,
// never silently "connected: false" with no explanation.
func TestWireGoogleConnect_NoStore(t *testing.T) {
	reg := wiringRegistry(t)
	apiMux, root := http.NewServeMux(), http.NewServeMux()
	cfg := testConnectConfig(t, "https://bob.example.test")

	// The typed-nil trap itself: a nil *agentdb.Store, exactly as main.go's
	// agentDB is on the sqlite fallback, passed as the interface parameter.
	var nilStore *agentdb.Store
	wireGoogleConnect(apiMux, root, nilStore, reg, cfg, wiringSettings(t), "", []byte("jwt-secret"), func(string, ...any) {})

	rec := httptest.NewRecorder()
	apiMux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agent/connections", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /agent/connections with no store: status %d, want 404 (route must not be mounted)", rec.Code)
	}

	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, googleConnectCallbackPath+"?state=x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET callback with no store: status %d, want 404 (route must not be mounted)", rec.Code)
	}

	ok, reason := reg.Availability("enc", "gmail")
	if ok || reason != "Connect Google needs DATABASE_URL" {
		t.Fatalf("Availability = %v, %q; want unavailable with the no-database reason", ok, reason)
	}
}

// TestWireGoogleConnect_WithStore: agentDB present and Connect Google
// enabled. The routes must be mounted — a real connect start reaches the
// handler and answers 200, not 404 — and the Registry must have a working
// AccountSource so a declared account starts out "not connected" rather
// than "unavailable: needs DATABASE_URL".
func TestWireGoogleConnect_WithStore(t *testing.T) {
	reg := wiringRegistry(t)
	apiMux, root := http.NewServeMux(), http.NewServeMux()
	cfg := testConnectConfig(t, "https://bob.example.test")
	store := newFakeConnectStore()

	wireGoogleConnect(apiMux, root, store, reg, cfg, wiringSettings(t), "", []byte("jwt-secret"), func(string, ...any) {})

	req := httptest.NewRequest(http.MethodPost, "/agent/connections/google/connect", nil)
	req = req.WithContext(contextWithPrincipal(req.Context(), gcListedOperator))
	rec := httptest.NewRecorder()
	apiMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST connect with a store: status %d body %s, want 200 (route must be mounted)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, googleConnectCallbackPath+"?state=not-a-real-state", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET callback with a store: status %d, want 400 (state_invalid page; route must be mounted)", rec.Code)
	}

	ok, reason := reg.Availability("enc", "gmail")
	if ok || reason == "Connect Google needs DATABASE_URL" || reason == "" {
		t.Fatalf("Availability = %v, %q; want an AccountSource installed (unavailable: not connected, not the no-database reason)", ok, reason)
	}
}

// TestWireGoogleConnect_StoreButDisabled: agentDB present but Connect
// Google's own config is disabled (e.g. no AGENTKIT_CONNECTIONS_KEY). The
// handoff note is explicit: mount the routes anyway (they answer 503 with
// the reason) — a project operator opening Settings should see why, not a
// 404 that looks like the button was never built.
func TestWireGoogleConnect_StoreButDisabled(t *testing.T) {
	reg := wiringRegistry(t)
	apiMux, root := http.NewServeMux(), http.NewServeMux()
	cfg, err := loadGoogleConnectConfig(connectEnv(nil), "https://bob.example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.enabled() {
		t.Fatal("test setup: cfg should be disabled with no env")
	}
	store := newFakeConnectStore()

	wireGoogleConnect(apiMux, root, store, reg, cfg, wiringSettings(t), "", []byte("jwt-secret"), func(string, ...any) {})

	req := httptest.NewRequest(http.MethodPost, "/agent/connections/google/connect", nil)
	req = req.WithContext(contextWithPrincipal(req.Context(), gcListedOperator))
	rec := httptest.NewRecorder()
	apiMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST connect, cfg disabled: status %d body %s, want 503 (route mounted, feature disabled)", rec.Code, rec.Body.String())
	}

	ok, reason := reg.Availability("enc", "gmail")
	if ok || reason != cfg.disabledReason {
		t.Fatalf("Availability = %v, %q; want unavailable with cfg's own disabled reason %q", ok, reason, cfg.disabledReason)
	}
}

// TestWireGoogleConnect_MalformedKeyFailsBoot: main.go must() on
// loadGoogleConnectConfig, so a key that is set but wrong is a boot failure,
// not a quietly disabled feature — and the error never quotes the value.
func TestWireGoogleConnect_MalformedKeyFailsBoot(t *testing.T) {
	for name, key := range map[string]string{
		"not base64":   "definitely not base64!!",
		"wrong length": "c2hvcnQ=",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadGoogleConnectConfig(connectEnv(map[string]string{
				"AGENTKIT_CONNECTIONS_KEY": key,
				"GOOGLE_CLIENT_ID":         "client-id.apps.googleusercontent.com",
				"GOOGLE_CLIENT_SECRET":     "client-secret-value",
			}), "https://bob.example.test", []byte("jwt-secret"))
			if err == nil {
				t.Fatal("malformed AGENTKIT_CONNECTIONS_KEY: want a boot error, got nil")
			}
			if strings.Contains(err.Error(), key) {
				t.Fatalf("boot error quotes the key value: %v", err)
			}
		})
	}
	// The key equal to the JWT secret is the other boot failure.
	if _, err := loadGoogleConnectConfig(connectEnv(map[string]string{
		"AGENTKIT_CONNECTIONS_KEY": testConnectionsKey,
	}), "https://bob.example.test", []byte(testConnectionsKey)); err == nil {
		t.Fatal("AGENTKIT_CONNECTIONS_KEY == AGENTKIT_JWT_SECRET: want a boot error, got nil")
	}
}

// TestWireGoogleConnect_BootLine: exactly one line, naming the redirect URI
// when enabled, and never a secret.
func TestWireGoogleConnect_BootLine(t *testing.T) {
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	cfg := testConnectConfig(t, "http://localhost:8080")
	wireGoogleConnect(http.NewServeMux(), http.NewServeMux(), newFakeConnectStore(), wiringRegistry(t), cfg, wiringSettings(t), "", []byte("jwt-secret"), logf)
	if len(lines) != 1 || lines[0] != "[agentd] connect google: enabled (redirect http://localhost:8080/auth/connections/google/callback)" {
		t.Fatalf("boot lines = %q", lines)
	}
	for _, secret := range []string{testConnectionsKey, "client-secret-value", "jwt-secret"} {
		if strings.Contains(lines[0], secret) {
			t.Fatalf("boot line leaks a secret: %q", lines[0])
		}
	}

	lines = nil
	var nilStore *agentdb.Store
	wireGoogleConnect(http.NewServeMux(), http.NewServeMux(), nilStore, wiringRegistry(t), cfg, wiringSettings(t), "", []byte("jwt-secret"), logf)
	if len(lines) != 1 || lines[0] != "[agentd] connect google: DISABLED (Connect Google needs DATABASE_URL)" {
		t.Fatalf("boot lines without a store = %q", lines)
	}
}

// TestProjectMapExample_ParsesWithGoogleAccount: the documented example map
// (local-config/project-map.example.json) still parses — operators and all —
// and builds a Registry whose ENC connections are google_account on the
// default account.
func TestProjectMapExample_ParsesWithGoogleAccount(t *testing.T) {
	raw, err := os.ReadFile("../../../local-config/project-map.example.json")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := parseProjectSettings(raw)
	if err != nil {
		t.Fatalf("example project map does not parse: %v", err)
	}
	reg, err := buildConnectionRegistry(connectionSpecsOf(settings), func(string) string { return "" }, func(string, ...any) {})
	if err != nil {
		t.Fatalf("example project map does not build a Registry: %v", err)
	}
	for _, name := range []string{"gmail", "drive"} {
		conn, ok := reg.Get("enc", name)
		if !ok {
			t.Fatalf("example map: enc has no %q connection", name)
		}
		if conn.Spec.Auth.Type != connections.AuthGoogleAccount || conn.Spec.Auth.Account != connections.DefaultGoogleAccount {
			t.Fatalf("enc/%s auth = %+v, want google_account on %q", name, conn.Spec.Auth, connections.DefaultGoogleAccount)
		}
	}
	holder := &projectSettingsHolder{}
	holder.ptr.Store(settings)
	if !connectOperatorRecheck(holder, "")("enc", "richard@example.com") {
		t.Fatal("example map: richard@example.com should be an operator of enc")
	}
}
