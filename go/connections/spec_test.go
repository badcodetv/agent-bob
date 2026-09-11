package connections

import (
	"fmt"
	"strings"
	"testing"
)

func validBearer() Spec {
	return Spec{
		Description: "badcodetv repos",
		URL:         "https://api.githubcopilot.com/mcp/",
		Auth:        Auth{Type: AuthBearer, TokenEnv: "WOLF_GITHUB_PAT"},
	}
}

func validGoogle() Spec {
	return Spec{
		Description: "Kai's inbox",
		URL:         "https://gmailmcp.googleapis.com/mcp/v1",
		Auth: Auth{
			Type:            AuthGoogleOAuth,
			ClientIDEnv:     "GOOGLE_CLIENT_ID",
			ClientSecretEnv: "GOOGLE_CLIENT_SECRET",
			RefreshTokenEnv: "KAI_GOOGLE_REFRESH",
		},
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		conn    string
		wantErr bool
	}{
		{"ok", "github", false},
		{"ok with dash", "google-docs", false},
		{"reserved core", "core", true},
		{"reserved ui", "ui", true},
		{"bad shape", "-github", true},
		{"empty", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.conn)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateName(%q): want error, got nil", tc.conn)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateName(%q): want nil, got %v", tc.conn, err)
			}
		})
	}
}

func TestSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    Spec
		wantErr bool
	}{
		{"valid bearer", validBearer(), false},
		{"valid google", validGoogle(), false},
		{"missing url", func() Spec { s := validBearer(); s.URL = ""; return s }(), true},
		{"relative url", func() Spec { s := validBearer(); s.URL = "/mcp/"; return s }(), true},
		{"http non-local refused", func() Spec { s := validBearer(); s.URL = "http://example.com/mcp/"; return s }(), true},
		{"http localhost ok", func() Spec { s := validBearer(); s.URL = "http://localhost:9999/mcp/"; return s }(), false},
		{"http 127.0.0.1 ok", func() Spec { s := validBearer(); s.URL = "http://127.0.0.1:9999/mcp/"; return s }(), false},
		{"bearer missing token_env", func() Spec { s := validBearer(); s.Auth.TokenEnv = ""; return s }(), true},
		{"bearer with google fields", func() Spec {
			s := validBearer()
			s.Auth.ClientIDEnv = "X"
			return s
		}(), true},
		{"google missing client_id_env", func() Spec { s := validGoogle(); s.Auth.ClientIDEnv = ""; return s }(), true},
		{"google missing client_secret_env", func() Spec { s := validGoogle(); s.Auth.ClientSecretEnv = ""; return s }(), true},
		{"google missing refresh_token_env", func() Spec { s := validGoogle(); s.Auth.RefreshTokenEnv = ""; return s }(), true},
		{"google with token_env", func() Spec { s := validGoogle(); s.Auth.TokenEnv = "X"; return s }(), true},
		{"bad env var name", func() Spec { s := validBearer(); s.Auth.TokenEnv = "not-a-var"; return s }(), true},
		{"bad env var leading digit", func() Spec { s := validBearer(); s.Auth.TokenEnv = "1TOKEN"; return s }(), true},
		{"unknown auth type", func() Spec { s := validBearer(); s.Auth.Type = "basic"; return s }(), true},
		{"empty auth type", func() Spec { s := validBearer(); s.Auth.Type = ""; return s }(), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate(): want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate(): want nil, got %v", err)
			}
		})
	}
}

func TestNewRegistry_ValidationFailsBoot(t *testing.T) {
	specs := map[string]map[string]Spec{
		"wolf": {"core": validBearer()}, // reserved name
	}
	if _, err := NewRegistry(specs, func(string) string { return "x" }, nil); err == nil {
		t.Fatalf("NewRegistry with a reserved name: want error, got nil")
	}

	specs = map[string]map[string]Spec{
		"wolf": {"github": func() Spec { s := validBearer(); s.URL = ""; return s }()},
	}
	if _, err := NewRegistry(specs, func(string) string { return "x" }, nil); err == nil {
		t.Fatalf("NewRegistry with an invalid spec: want error, got nil")
	}
}

func TestNewRegistry_MissingEnvVarUnavailable(t *testing.T) {
	var logCalls int
	var lastMsg string
	logf := func(format string, args ...any) {
		logCalls++
		lastMsg = fmt.Sprintf(format, args...)
	}

	specs := map[string]map[string]Spec{
		"wolf": {"github": validBearer()},
	}
	reg, err := NewRegistry(specs, func(string) string { return "" }, logf)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if logCalls != 1 {
		t.Fatalf("logf calls: want 1, got %d (last: %q)", logCalls, lastMsg)
	}
	if !strings.Contains(lastMsg, "WOLF_GITHUB_PAT") {
		t.Fatalf("log message should name the env var: %q", lastMsg)
	}

	conn, ok := reg.Get("wolf", "github")
	if !ok {
		t.Fatalf("Get: connection not found")
	}
	if conn.Token != nil {
		t.Fatalf("unavailable connection must have a nil Token")
	}
	if conn.Unavailable == "" {
		t.Fatalf("want a non-empty Unavailable reason")
	}
	if !strings.Contains(conn.Unavailable, "WOLF_GITHUB_PAT") {
		t.Fatalf("Unavailable should name the env var: %q", conn.Unavailable)
	}

	infos := reg.List("wolf")
	if len(infos) != 1 || infos[0].Available {
		t.Fatalf("List: want one unavailable entry, got %+v", infos)
	}
}

func TestNewRegistry_AvailableBearer(t *testing.T) {
	specs := map[string]map[string]Spec{
		"wolf": {"github": validBearer()},
	}
	env := map[string]string{"WOLF_GITHUB_PAT": "secret-token"}
	reg, err := NewRegistry(specs, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	conn, ok := reg.Get("wolf", "github")
	if !ok {
		t.Fatalf("Get: not found")
	}
	if conn.Token == nil {
		t.Fatalf("want a non-nil Token")
	}
	tok, err := conn.Token.Token(nil) //nolint:staticcheck // test-only nil context, Token never inspects it here
	if err != nil {
		t.Fatalf("Token(): %v", err)
	}
	if tok != "secret-token" {
		t.Fatalf("Token(): want %q, got %q", "secret-token", tok)
	}
	if conn.Unavailable != "" {
		t.Fatalf("available connection must have empty Unavailable, got %q", conn.Unavailable)
	}
}

func TestNewRegistry_GoogleOAuthAvailableWhenEnvSet(t *testing.T) {
	// GoogleRefresh (token.go, T3) never makes a network call until Token()
	// is first invoked, so a registry build with real-looking env values
	// resolves to an available connection without needing a fake token
	// endpoint here — token_test.go exercises the exchange itself.
	specs := map[string]map[string]Spec{
		"wolf": {"gmail": validGoogle()},
	}
	env := map[string]string{
		"GOOGLE_CLIENT_ID":     "id",
		"GOOGLE_CLIENT_SECRET": "secret",
		"KAI_GOOGLE_REFRESH":   "refresh",
	}
	reg, err := NewRegistry(specs, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	conn, ok := reg.Get("wolf", "gmail")
	if !ok {
		t.Fatalf("Get: not found")
	}
	if conn.Unavailable != "" {
		t.Fatalf("want available once google_oauth env vars are all set, got Unavailable=%q", conn.Unavailable)
	}
	if conn.Token == nil {
		t.Fatalf("want a non-nil Token source")
	}
}

func TestRegistryListAndNamesSorted(t *testing.T) {
	specs := map[string]map[string]Spec{
		"wolf": {
			"github": validBearer(),
			"docs":   validGoogle(),
		},
	}
	env := map[string]string{"WOLF_GITHUB_PAT": "x"}
	reg, err := NewRegistry(specs, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	names := reg.Names("wolf")
	if len(names) != 2 || names[0] != "docs" || names[1] != "github" {
		t.Fatalf("Names: want [docs github], got %v", names)
	}
	infos := reg.List("wolf")
	if len(infos) != 2 || infos[0].Name != "docs" || infos[1].Name != "github" {
		t.Fatalf("List: want sorted [docs github], got %+v", infos)
	}
}

func TestRegistryNilSafe(t *testing.T) {
	var r *Registry
	if got := r.List("wolf"); got != nil {
		t.Fatalf("nil registry List: want nil, got %v", got)
	}
	if got := r.Names("wolf"); got != nil {
		t.Fatalf("nil registry Names: want nil, got %v", got)
	}
	if _, ok := r.Get("wolf", "github"); ok {
		t.Fatalf("nil registry Get: want not-found")
	}
}

func TestRegistryUnknownProjectOrName(t *testing.T) {
	specs := map[string]map[string]Spec{"wolf": {"github": validBearer()}}
	env := map[string]string{"WOLF_GITHUB_PAT": "x"}
	reg, err := NewRegistry(specs, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if got := reg.List("nope"); got != nil {
		t.Fatalf("List(unknown project): want nil, got %v", got)
	}
	if got := reg.Names("nope"); got != nil {
		t.Fatalf("Names(unknown project): want nil, got %v", got)
	}
	if _, ok := reg.Get("wolf", "nope"); ok {
		t.Fatalf("Get(unknown name): want not-found")
	}
	if _, ok := reg.Get("nope", "github"); ok {
		t.Fatalf("Get(unknown project): want not-found")
	}
}

// staticTokenIsTokenSource is a compile-time check that the exported
// constructor really implements TokenSource.
var _ TokenSource = StaticToken("x")

func TestStaticTokenErr(t *testing.T) {
	ts := StaticToken("abc")
	tok, err := ts.Token(nil) //nolint:staticcheck
	if err != nil || tok != "abc" {
		t.Fatalf("StaticToken.Token(): want (abc, nil), got (%q, %v)", tok, err)
	}
}
