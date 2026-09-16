package connections

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"sync"

	"github.com/badcodetv/agent-bob/agentdb"
)

// Wildcard is the grant that means "every connection the project has"
// (agentdb.Worker.Connections, connection_list's `held`).
const Wildcard = "*"

// ReservedNames are connection names that would collide with an MCP server
// agentd already mounts into every session: "core" is the core MCP server
// (go/cmd/agentd/mcpserver.go), "ui" is the sandbox's in-image server, which a
// session server of the same name would silently replace
// (sandbox/src/harness/claude-agent-sdk.ts).
var ReservedNames = []string{"core", "ui"}

// AuthType names how a connection authenticates to its upstream.
type AuthType string

const (
	// AuthBearer is a static token sent as "Authorization: Bearer <token>".
	AuthBearer AuthType = "bearer"
	// AuthGoogleOAuth exchanges a long-lived refresh token for a short-lived
	// access token, cached until shortly before expiry.
	AuthGoogleOAuth AuthType = "google_oauth"
	// AuthGoogleAccount uses a Google refresh token an operator obtained by
	// pressing Connect Google in the console, stored sealed in Postgres and
	// looked up at use time through the Registry's AccountSource
	// (account.go). Every connection naming the same Account shares that one
	// credential. It names no env vars: the OAuth client is always agentd's
	// login client (GOOGLE_CLIENT_ID/GOOGLE_CLIENT_SECRET), the only client
	// the redirect URI is registered on.
	AuthGoogleAccount AuthType = "google_account"
)

// DefaultGoogleAccount is the account a google_account connection uses when
// its auth recipe names none.
const DefaultGoogleAccount = "google"

// Auth is an auth recipe: it names environment variables, never a secret
// value itself. The project map is data an operator hand-edits and git may
// one day render config from (NotImportable for connections specifically,
// design decision 3) — no field here may carry a credential directly.
type Auth struct {
	Type            AuthType `json:"type"`
	TokenEnv        string   `json:"token_env,omitempty"`         // bearer
	ClientIDEnv     string   `json:"client_id_env,omitempty"`     // google_oauth
	ClientSecretEnv string   `json:"client_secret_env,omitempty"` // google_oauth
	RefreshTokenEnv string   `json:"refresh_token_env,omitempty"` // google_oauth
	// Account names the stored Google credential a google_account connection
	// uses; "" means DefaultGoogleAccount. It is a name, not a secret.
	Account string `json:"account,omitempty"` // google_account
	// tokenURL overrides Google's token endpoint. Deliberately unexported and
	// therefore never unmarshalled from the project map: an edit there must
	// not be able to redirect the client secret and refresh token to an
	// attacker-controlled endpoint. Set only by the unexported test
	// constructor in token_test.go.
	tokenURL string
}

// Spec is one connection's configuration, as named in a project's
// `connections` map.
type Spec struct {
	Description string `json:"description"`
	URL         string `json:"url"`
	Auth        Auth   `json:"auth"`
}

// envNamePattern is the shape of an environment variable name the project map
// may reference.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateName is the identity rule for a connection name: the same shape a
// worker grant must match (agentdb.ValidateConnectionName), plus not one of
// ReservedNames.
func ValidateName(name string) error {
	if err := agentdb.ValidateConnectionName(name); err != nil {
		return err
	}
	for _, r := range ReservedNames {
		if name == r {
			return fmt.Errorf("connection name %q is reserved", name)
		}
	}
	return nil
}

// Validate checks a Spec's shape: the URL is absolute and https (http is
// allowed only for localhost/127.0.0.1, so a local fake upstream can be
// tested against), the auth fields present match Type exactly (no fields for
// the other type), every env var name it references is a legal environment
// variable name, and Type is one of the known values.
func (s Spec) Validate() error {
	if s.URL == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(s.URL)
	if err != nil || !u.IsAbs() {
		return fmt.Errorf("url %q must be an absolute URL", s.URL)
	}
	switch u.Scheme {
	case "https":
		// ok
	case "http":
		host := u.Hostname()
		if host != "localhost" && host != "127.0.0.1" {
			return fmt.Errorf("url %q: http is only allowed for localhost/127.0.0.1 (local fakes); use https", s.URL)
		}
	default:
		return fmt.Errorf("url %q: scheme must be https (or http for localhost/127.0.0.1)", s.URL)
	}

	if s.Auth.Account != "" && s.Auth.Type != AuthGoogleAccount {
		return fmt.Errorf(`auth.account is only for auth.type %q, not %q`, AuthGoogleAccount, s.Auth.Type)
	}

	switch s.Auth.Type {
	case AuthBearer:
		if s.Auth.ClientIDEnv != "" || s.Auth.ClientSecretEnv != "" || s.Auth.RefreshTokenEnv != "" {
			return errors.New(`auth.type "bearer" must not set client_id_env, client_secret_env or refresh_token_env`)
		}
		if s.Auth.TokenEnv == "" {
			return errors.New(`auth.type "bearer" requires token_env`)
		}
		if err := validateEnvName("token_env", s.Auth.TokenEnv); err != nil {
			return err
		}
	case AuthGoogleOAuth:
		if s.Auth.TokenEnv != "" {
			return errors.New(`auth.type "google_oauth" must not set token_env`)
		}
		if s.Auth.ClientIDEnv == "" || s.Auth.ClientSecretEnv == "" || s.Auth.RefreshTokenEnv == "" {
			return errors.New(`auth.type "google_oauth" requires client_id_env, client_secret_env and refresh_token_env`)
		}
		for _, pair := range [][2]string{
			{"client_id_env", s.Auth.ClientIDEnv},
			{"client_secret_env", s.Auth.ClientSecretEnv},
			{"refresh_token_env", s.Auth.RefreshTokenEnv},
		} {
			if err := validateEnvName(pair[0], pair[1]); err != nil {
				return err
			}
		}
	case AuthGoogleAccount:
		if s.Auth.TokenEnv != "" || s.Auth.ClientIDEnv != "" || s.Auth.ClientSecretEnv != "" || s.Auth.RefreshTokenEnv != "" {
			return errors.New(`auth.type "google_account" must not set token_env, client_id_env, client_secret_env or refresh_token_env (it always uses GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET, and the token comes from Connect Google)`)
		}
		if err := agentdb.ValidateConnectionName(s.Auth.accountName()); err != nil {
			return fmt.Errorf("auth.account: %w", err)
		}
	case "":
		return errors.New("auth.type is required")
	default:
		return fmt.Errorf("auth.type %q is not a known auth type (want %q, %q or %q)", s.Auth.Type, AuthBearer, AuthGoogleOAuth, AuthGoogleAccount)
	}
	return nil
}

// accountName is Account with the default applied.
func (a Auth) accountName() string {
	if a.Account == "" {
		return DefaultGoogleAccount
	}
	return a.Account
}

func validateEnvName(field, value string) error {
	if !envNamePattern.MatchString(value) {
		return fmt.Errorf("%s %q is not a legal environment variable name", field, value)
	}
	return nil
}

// Info is what a caller (connection_list, worker_create/_update) is told
// about a connection: enough to decide whether to grant it, never enough to
// reach it.
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	// Unavailable says why, naming the env var(s) — names are not secrets.
	Unavailable string `json:"unavailable,omitempty"`
}

// Connection is one project's resolved connection: its spec, its parsed
// upstream URL, and — when available — a TokenSource able to produce the
// real credential. Token is nil exactly when Unavailable is non-empty, and
// always nil for google_account, whose credential is reached through the
// Registry's AccountSource (Registry.Availability is the one check).
type Connection struct {
	Project, Name string
	Spec          Spec
	URL           *url.URL
	Token         TokenSource
	Unavailable   string
}

// Registry is every project's resolved connections, built once at boot from
// the project map and the environment. A google_account connection's
// availability and token are not resolved at boot but looked up at use
// through the AccountSource installed by SetAccounts (account.go).
type Registry struct {
	byProject map[string]map[string]*Connection

	accountsMu     sync.RWMutex
	accounts       AccountSource
	disabledReason string
}

// NewRegistry validates every spec (a validation failure is a boot error) and
// resolves each spec's env vars into a TokenSource. A missing or empty env
// var does NOT fail the boot: the connection is recorded Unavailable and logf
// is called exactly once, naming the project, connection and variable —
// never a value. getenv defaults to os.Getenv; logf defaults to a no-op.
func NewRegistry(specs map[string]map[string]Spec, getenv func(string) string, logf func(string, ...any)) (*Registry, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	reg := &Registry{byProject: make(map[string]map[string]*Connection, len(specs))}
	for project, byName := range specs {
		projConns := make(map[string]*Connection, len(byName))
		for name, spec := range byName {
			if err := ValidateName(name); err != nil {
				return nil, fmt.Errorf("project %q: connection %q: %w", project, name, err)
			}
			if err := spec.Validate(); err != nil {
				return nil, fmt.Errorf("project %q: connection %q: %w", project, name, err)
			}
			u, err := url.Parse(spec.URL)
			if err != nil {
				return nil, fmt.Errorf("project %q: connection %q: url: %w", project, name, err)
			}
			if spec.Auth.Type == AuthGoogleAccount {
				spec.Auth.Account = spec.Auth.accountName()
			}
			conn := &Connection{Project: project, Name: name, Spec: spec, URL: u}
			resolveToken(conn, getenv)
			if conn.Unavailable != "" {
				logf("connections: project %q connection %q unavailable: %s", project, name, conn.Unavailable)
			}
			projConns[name] = conn
		}
		reg.byProject[project] = projConns
	}
	return reg, nil
}

// resolveToken fills in conn.Token, or conn.Unavailable when the env vars a
// spec names are not set (or the auth type is not yet buildable — see
// googleTokenSource in token.go).
func resolveToken(conn *Connection, getenv func(string) string) {
	auth := conn.Spec.Auth
	switch auth.Type {
	case AuthBearer:
		tok := getenv(auth.TokenEnv)
		if tok == "" {
			conn.Unavailable = fmt.Sprintf("env var %s is not set", auth.TokenEnv)
			return
		}
		conn.Token = StaticToken(tok)
	case AuthGoogleOAuth:
		for _, envVar := range []string{auth.ClientIDEnv, auth.ClientSecretEnv, auth.RefreshTokenEnv} {
			if getenv(envVar) == "" {
				conn.Unavailable = fmt.Sprintf("env var %s is not set", envVar)
				return
			}
		}
		ts, reason := googleTokenSource(getenv(auth.ClientIDEnv), getenv(auth.ClientSecretEnv), getenv(auth.RefreshTokenEnv), auth.tokenURL)
		if reason != "" {
			conn.Unavailable = reason
			return
		}
		conn.Token = ts
	case AuthGoogleAccount:
		// Nothing to resolve at boot: Registry.Availability and the proxy ask
		// the AccountSource at use time.
	}
}

// Get looks up one project's connection by name. A nil Registry is safe and
// reports not-found.
func (r *Registry) Get(project, name string) (*Connection, bool) {
	if r == nil {
		return nil, false
	}
	byName, ok := r.byProject[project]
	if !ok {
		return nil, false
	}
	c, ok := byName[name]
	return c, ok
}

// List returns every connection a project has, sorted by name. A nil
// Registry, or a project with none, returns nil.
func (r *Registry) List(project string) []Info {
	if r == nil {
		return nil
	}
	byName := r.byProject[project]
	if len(byName) == 0 {
		return nil
	}
	infos := make([]Info, 0, len(byName))
	for name, c := range byName {
		ok, reason := r.Availability(project, name)
		infos = append(infos, Info{
			Name:        name,
			Description: c.Spec.Description,
			Available:   ok,
			Unavailable: reason,
		})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

// Names returns every connection name a project has, sorted. A nil Registry,
// or a project with none, returns nil.
func (r *Registry) Names(project string) []string {
	if r == nil {
		return nil
	}
	byName := r.byProject[project]
	if len(byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
