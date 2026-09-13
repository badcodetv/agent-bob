// Login endpoints for the standalone stack: Google Sign-In and a fixed
// password login for tests. Both verify an identity, look the email up in the
// hard-coded email → projects map, and mint one project-scoped HS256 JWT per
// allowed project ("project" is the existing customer claim/column — a pure
// namespacing concept, no project table). apiAuthMiddleware verifies the minted
// tokens on its bearer-token path; nothing downstream changes.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/badcodetv/agent-bob/extension"
	"github.com/badcodetv/agent-bob/extension/devclaims"
	"github.com/golang-jwt/jwt/v5"
)

// projectMap maps a lowercased email address to the project IDs (kebab-case
// strings, e.g. "apples-oranges") that user may enter.
type projectMap map[string][]string

// projectWildcard in a user's project list grants every project in the map,
// plus a login token that can mint tokens for brand-new project IDs (dev
// convenience — a project "exists" once a session carries its name).
const projectWildcard = "*"

// validProjectID gates project IDs mintable via the wildcard: kebab-case,
// like "apples-oranges". Keeps arbitrary strings out of the customer column.
var validProjectID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// projectConfig is the per-project half of the object form: ops config for a
// project that a third-party application integrates with. Both fields are
// optional — a project with neither is just a namespace, exactly as before.
type projectConfig struct {
	// APIKeyEnv names the environment variable holding this project's API key.
	// The key value itself is never written in the map; only the variable name
	// is, so the map stays safe to commit and mount. Empty ⇒ no key (see T2).
	APIKeyEnv string `json:"api_key_env"`
	// AllowedOrigins lists the origins permitted to frame this project's embed
	// page. It drives Content-Security-Policy: frame-ancestors — not CORS; no
	// browser ever makes a cross-origin request to agentd by design.
	AllowedOrigins []string `json:"allowed_origins"`
	// GitHubTokenEnv names the environment variable holding this project's
	// git-projection push token (design/2026-09-09-git-projection.md, G7).
	// Same shape as APIKeyEnv: the map names the variable, never the secret.
	//
	// Overlap with agentdb.ProjectSettings.GitTokenEnv: that column is the
	// one the console/API write and the renderer actually reads — it is the
	// live, per-project source of truth and it is NOT importable (§D). This
	// field is the boot-time convenience for the same shape of value, useful
	// for a deployment that provisions projects from its project map rather
	// than through the console. Decision: when both are set, GitTokenEnv
	// (the database column) wins, because it is the one a human can change
	// at runtime without a redeploy and the one the git-projection renderer
	// is documented to read; this field is consulted only as a fallback for
	// a project whose settings row has never set GitTokenEnv. Whichever
	// component resolves the effective token-env name is responsible for
	// applying that precedence — this file only validates and stores the
	// map's own value.
	GitHubTokenEnv string `json:"github_token_env"`
}

// projectSettings is the whole parsed map file: who may log in, and per-project
// ops config. The flat legacy form parses into this with an empty projects half.
type projectSettings struct {
	users    projectMap
	projects map[string]projectConfig
}

// envVarName is a plausible environment variable name — the shell's own rule.
// Catching "WOLF-API-KEY" at boot beats discovering at runtime that a project
// silently has no key.
var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseProjectSettings decodes either form of the project map.
//
//	legacy: {"kai@badcode.dev": ["wolf", "demo"]}
//	object: {"users": {...}, "projects": {"wolf": {"api_key_env": …}}}
//
// The two are told apart by the *shape of the values*, not by key names: an
// email address is a perfectly legal JSON key and there is nothing structural
// stopping someone being called "users@…", so keys prove nothing. Legacy values
// are arrays; object-form values are objects. A file mixing both is an error
// rather than a guess.
func parseProjectSettings(raw []byte) (*projectSettings, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("project map: %w", err)
	}
	if len(probe) == 0 {
		return nil, fmt.Errorf("project map: empty")
	}
	var arrays, objects int
	for _, v := range probe {
		switch firstJSONToken(v) {
		case '[':
			arrays++
		case '{':
			objects++
		}
	}
	switch {
	case arrays > 0 && objects > 0:
		return nil, fmt.Errorf("project map: mixes the flat form (email → [projects]) with the object form ({\"users\": …, \"projects\": …}); use one or the other")
	case objects > 0:
		return parseProjectSettingsObjectForm(probe)
	default:
		users, err := parseUsers(raw)
		if err != nil {
			return nil, err
		}
		return &projectSettings{users: users, projects: map[string]projectConfig{}}, nil
	}
}

// firstJSONToken returns the first non-whitespace byte of a raw JSON value.
func firstJSONToken(v json.RawMessage) byte {
	for _, b := range v {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return b
		}
	}
	return 0
}

func parseProjectSettingsObjectForm(probe map[string]json.RawMessage) (*projectSettings, error) {
	for k := range probe {
		if k != "users" && k != "projects" {
			return nil, fmt.Errorf("project map: unknown top-level key %q (the object form takes only \"users\" and \"projects\")", k)
		}
	}
	out := &projectSettings{users: projectMap{}, projects: map[string]projectConfig{}}
	if rawUsers, ok := probe["users"]; ok {
		users, err := parseUsers(rawUsers)
		if err != nil {
			return nil, err
		}
		out.users = users
	}
	if rawProjects, ok := probe["projects"]; ok {
		var projects map[string]projectConfig
		if err := json.Unmarshal(rawProjects, &projects); err != nil {
			return nil, fmt.Errorf("project map: projects: %w", err)
		}
		for id, cfg := range projects {
			if !validProjectID.MatchString(id) || len(id) > 64 {
				return nil, fmt.Errorf("project map: project %q is not a valid project id (want kebab-case, e.g. apples-oranges)", id)
			}
			if cfg.APIKeyEnv != "" && !envVarName.MatchString(cfg.APIKeyEnv) {
				return nil, fmt.Errorf("project map: project %q: api_key_env %q is not a valid environment variable name", id, cfg.APIKeyEnv)
			}
			if cfg.GitHubTokenEnv != "" && !envVarName.MatchString(cfg.GitHubTokenEnv) {
				return nil, fmt.Errorf("project map: project %q: github_token_env %q is not a valid environment variable name", id, cfg.GitHubTokenEnv)
			}
			for _, origin := range cfg.AllowedOrigins {
				if err := validateOrigin(origin); err != nil {
					return nil, fmt.Errorf("project map: project %q: allowed_origins: %w", id, err)
				}
			}
			out.projects[id] = cfg
		}
	}
	// An object form that grants nobody a login and configures no project is a
	// mistake worth failing on, the same way an empty flat map is.
	if len(out.users) == 0 && len(out.projects) == 0 {
		return nil, fmt.Errorf("project map: empty (neither users nor projects)")
	}
	return out, nil
}

// parseUsers decodes the email → project-IDs map, lowercasing emails and
// rejecting entries that could silently grant nothing or everything. It is the
// whole of the legacy form and the "users" half of the object form.
func parseUsers(raw []byte) (projectMap, error) {
	var in map[string][]string
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("project map: %w", err)
	}
	out := make(projectMap, len(in))
	for email, projects := range in {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			return nil, fmt.Errorf("project map: empty email key")
		}
		if len(projects) == 0 {
			return nil, fmt.Errorf("project map: %s has no projects", email)
		}
		for _, p := range projects {
			if strings.TrimSpace(p) == "" {
				return nil, fmt.Errorf("project map: %s has an empty project id", email)
			}
		}
		out[email] = projects
	}
	return out, nil
}

// validateOrigin accepts scheme://host[:port] and nothing else. Origins land in
// a CSP frame-ancestors list, where a path is meaningless and a wildcard would
// let anyone frame the project — so both are refused rather than trimmed.
// Plain http is allowed only for loopback, which is where a dev server lives.
func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("%q is not a URL: %w", origin, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%q must be an absolute origin, e.g. https://wolf.badcode.dev", origin)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%q must be scheme://host[:port] with no path, query or fragment", origin)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" {
			return fmt.Errorf("%q must use https (plain http is allowed only for localhost)", origin)
		}
	default:
		return fmt.Errorf("%q has scheme %q; want https", origin, u.Scheme)
	}
	return nil
}

// parseProjectMap decodes either form and returns just the user→projects half —
// what the login handlers need. Its signature is unchanged from before the
// object form existed.
func parseProjectMap(raw []byte) (projectMap, error) {
	s, err := parseProjectSettings(raw)
	if err != nil {
		return nil, err
	}
	return s.users, nil
}

// loadProjectSettings reads the map from AGENTKIT_PROJECT_MAP (inline JSON,
// wins) or AGENTKIT_PROJECT_MAP_FILE (path to a mounted JSON file).
func loadProjectSettings(getenv func(string) string) (*projectSettings, error) {
	if inline := getenv("AGENTKIT_PROJECT_MAP"); inline != "" {
		return parseProjectSettings([]byte(inline))
	}
	if path := getenv("AGENTKIT_PROJECT_MAP_FILE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("project map file: %w", err)
		}
		return parseProjectSettings(raw)
	}
	return nil, fmt.Errorf("no project map: set AGENTKIT_PROJECT_MAP or AGENTKIT_PROJECT_MAP_FILE")
}

// loadProjectSettingsOptional is loadProjectSettings but tolerant of the map
// being absent entirely: it returns (nil, nil) when neither env var is set. The
// zero-config demo has no map at all, and it must still boot.
func loadProjectSettingsOptional(getenv func(string) string) (*projectSettings, error) {
	if getenv("AGENTKIT_PROJECT_MAP") == "" && getenv("AGENTKIT_PROJECT_MAP_FILE") == "" {
		return nil, nil
	}
	return loadProjectSettings(getenv)
}

// loadProjectMap is loadProjectSettings' user-half shorthand.
func loadProjectMap(getenv func(string) string) (projectMap, error) {
	s, err := loadProjectSettings(getenv)
	if err != nil {
		return nil, err
	}
	return s.users, nil
}

// allProjects returns the deduplicated union of every concrete project in the
// map — what wildcard entries and the fixed test login are granted.
func (pm projectMap) allProjects() []string {
	seen := map[string]bool{}
	var out []string
	for _, projects := range pm {
		for _, p := range projects {
			if p != projectWildcard && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// resolve returns the effective projects for an email plus whether the entry
// is a wildcard grant. ok=false when the email isn't in the map.
func (pm projectMap) resolve(email string) (projects []string, wildcard, ok bool) {
	projects, ok = pm[email]
	if !ok {
		return nil, false, false
	}
	for _, p := range projects {
		if p == projectWildcard {
			return pm.allProjects(), true, true
		}
	}
	return projects, false, true
}

// userDirectory is what the login handlers need from the user→projects half of
// the map: resolve one email, or list every project a wildcard grants. A plain
// projectMap value satisfies it directly (used by tests and by any caller that
// really does hold a frozen snapshot); *projectSettingsHolder satisfies it by
// reading through to whatever the map currently holds, which is what lets a
// reload reach a running login handler without re-registering it (A6).
type userDirectory interface {
	resolve(email string) (projects []string, wildcard, ok bool)
	allProjects() []string
}

// storedProjectsDirectory widens a WILDCARD grant with the projects that exist
// in the database. The project map only names projects someone wrote into it;
// a wildcard login creates projects by minting a token for a new id, and those
// never reach the map — so after signing in again, the picker listed none of
// them. A wildcard holder can already mint a token for any project id, so
// listing the names grants nothing new. A non-wildcard account is untouched.
//
// Best-effort: a failed or slow read returns the map's projects alone, because
// a login that fails over a convenience list is worse than a shorter list.
type storedProjectsDirectory struct {
	userDirectory
	list func(ctx context.Context, limit int) ([]string, error)
}

// storedProjectsLimit caps how many database projects a login lists (and so
// mints tokens for). Most recently active first.
const storedProjectsLimit = 50

func (d storedProjectsDirectory) stored() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names, err := d.list(ctx, storedProjectsLimit)
	if err != nil {
		log.Printf("[agentd] login: could not list stored projects, using the project map alone: %v", err)
		return nil
	}
	out := names[:0:0]
	for _, n := range names {
		if validProjectID.MatchString(n) && len(n) <= 64 {
			out = append(out, n)
		}
	}
	return out
}

func mergeProjectNames(first, second []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{first, second} {
		for _, p := range list {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

func (d storedProjectsDirectory) resolve(email string) ([]string, bool, bool) {
	projects, wildcard, ok := d.userDirectory.resolve(email)
	if ok && wildcard {
		projects = mergeProjectNames(projects, d.stored())
	}
	return projects, wildcard, ok
}

func (d storedProjectsDirectory) allProjects() []string {
	return mergeProjectNames(d.userDirectory.allProjects(), d.stored())
}

// projectSettingsHolder is the live, reloadable project map. Every reader that
// must see a reload without a process restart — the login handlers'
// resolve/allProjects, the API-key index construction, and the git-token-env
// fallback — reads through Get() (or, for the user half, through the holder
// itself as a userDirectory) rather than closing over a *projectSettings
// captured once at boot (A6).
//
// Only a file has anything to re-read: an inline AGENTKIT_PROJECT_MAP has no
// backing file to watch, so path is left empty for it and reload/watch become
// no-ops — inline behaviour is unchanged.
type projectSettingsHolder struct {
	ptr  atomic.Pointer[projectSettings]
	path string
}

// newProjectSettingsHolder loads the map exactly as loadProjectSettingsOptional
// does (nil, nil when neither env var is set) and records the file path to
// watch, if any. Inline JSON wins over a file per loadProjectSettings'
// existing precedence, so path is set only when AGENTKIT_PROJECT_MAP is empty.
func newProjectSettingsHolder(getenv func(string) string) (*projectSettingsHolder, error) {
	s, err := loadProjectSettingsOptional(getenv)
	if err != nil {
		return nil, err
	}
	h := &projectSettingsHolder{}
	if s != nil {
		h.ptr.Store(s)
	}
	if getenv("AGENTKIT_PROJECT_MAP") == "" {
		h.path = strings.TrimSpace(getenv("AGENTKIT_PROJECT_MAP_FILE"))
	}
	return h, nil
}

// Get returns the current settings, or nil when no map is configured at all.
func (h *projectSettingsHolder) Get() *projectSettings {
	if h == nil {
		return nil
	}
	return h.ptr.Load()
}

// users is the current user→projects half, used by the userDirectory methods
// below. A nil map is a legal, safe receiver for both projectMap methods.
func (h *projectSettingsHolder) users() projectMap {
	if s := h.Get(); s != nil {
		return s.users
	}
	return nil
}

func (h *projectSettingsHolder) resolve(email string) (projects []string, wildcard, ok bool) {
	return h.users().resolve(email)
}

func (h *projectSettingsHolder) allProjects() []string {
	return h.users().allProjects()
}

// reload re-reads and re-parses the file, replacing the held settings on
// success and reporting whether it did. A read failure or a parse failure
// leaves the previous map in place and only logs it — a reload must never take
// a working deployment offline, unlike the fatal boot-time error the same
// parse failure would produce in loadProjectSettings. ok=false whenever there
// was nothing to reload (no file configured) or the reload failed.
func (h *projectSettingsHolder) reload(logf func(string, ...any)) (ok bool) {
	if h == nil || h.path == "" {
		return false
	}
	raw, err := os.ReadFile(h.path)
	if err != nil {
		logf("[agentd] project map reload: %s: %v — keeping the previous map", h.path, err)
		return false
	}
	parsed, err := parseProjectSettings(raw)
	if err != nil {
		logf("[agentd] project map reload: %s: %v — keeping the previous map", h.path, err)
		return false
	}
	h.ptr.Store(parsed)
	logf("[agentd] project map reloaded from %s: %d mapped account(s), %d configured project(s)",
		h.path, len(parsed.users), len(parsed.projects))
	return true
}

// watch reloads the map on SIGHUP and every interval (interval<=0 disables the
// timer; SIGHUP still reloads). onReload, if non-nil, runs after every
// reload that actually replaced the map — main.go uses it to recompute the
// API-key index from the same file, since api_key_env lives in the same
// "projects" section (A6). Returns immediately, doing nothing, when there is
// no file to watch (no map, or an inline map). Runs until ctx is done.
func (h *projectSettingsHolder) watch(ctx context.Context, interval time.Duration, onReload func(), logf func(string, ...any)) {
	if h == nil || h.path == "" {
		return
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP)
	defer signal.Stop(sig)
	var tick <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			logf("[agentd] project map: SIGHUP received, reloading %s", h.path)
			if h.reload(logf) && onReload != nil {
				onReload()
			}
		case <-tick:
			if h.reload(logf) && onReload != nil {
				onReload()
			}
		}
	}
}

// googleVerifier validates Google ID tokens via the tokeninfo endpoint —
// zero-dependency server-side verification (Google's TLS cert authenticates
// the response; no local JWKS handling needed at login-only volumes).
type googleVerifier struct {
	clientID     string
	tokeninfoURL string // default https://oauth2.googleapis.com/tokeninfo; injectable for tests
	hc           *http.Client
}

// Verify checks the credential (a Google ID token from Google Identity
// Services) and returns the verified, lowercased email address.
func (v *googleVerifier) Verify(r *http.Request, credential string) (string, error) {
	endpoint := v.tokeninfoURL
	if endpoint == "" {
		endpoint = "https://oauth2.googleapis.com/tokeninfo"
	}
	hc := v.hc
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		endpoint+"?id_token="+url.QueryEscape(credential), nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tokeninfo: status %d", resp.StatusCode)
	}
	var info struct {
		Aud           string `json:"aud"`
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("tokeninfo: decode: %w", err)
	}
	if info.Aud != v.clientID {
		return "", fmt.Errorf("tokeninfo: audience mismatch")
	}
	if info.EmailVerified != "true" || info.Email == "" {
		return "", fmt.Errorf("tokeninfo: email not verified")
	}
	return strings.ToLower(info.Email), nil
}

// projectToken pairs a project ID with a JWT scoped to it (customer=<id>).
type projectToken struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

// loginResponse is the shape both login endpoints return. Wildcard grants
// additionally carry a login token that POST /auth/project-token exchanges
// for tokens to brand-new project IDs.
type loginResponse struct {
	Email      string         `json:"email"`
	Projects   []projectToken `json:"projects"`
	Wildcard   bool           `json:"wildcard,omitempty"`
	LoginToken string         `json:"login_token,omitempty"`
}

// mintProjectTokens issues one project-scoped JWT per project ID. operator
// stamps the onboarding-plan §1.1 `operator:true` claim on every token minted:
// true only for a wildcard login's tokens (writeLoginResponse) and the
// wildcard project-token exchange (authProjectTokenHandler) — never for a
// non-wildcard Google account.
func mintProjectTokens(r *http.Request, issuer *devclaims.Issuer, email string, projects []string, operator bool) ([]projectToken, error) {
	out := make([]projectToken, 0, len(projects))
	for _, p := range projects {
		tok, err := issuer.IssueOperator(r.Context(), extension.ContextScope{
			UserEmail: email,
			Customer:  p,
			Job:       "web",
		}, "", operator)
		if err != nil {
			return nil, err
		}
		out = append(out, projectToken{ID: p, Token: tok})
	}
	return out, nil
}

func writeLoginResponse(w http.ResponseWriter, r *http.Request, issuer *devclaims.Issuer, email string, projects []string, wildcard bool) {
	// The operator claim tracks wildcard-ness exactly (§1.1): a wildcard grant
	// (including the test login, which is always a wildcard) is the operator;
	// a plain per-project Google account is not.
	tokens, err := mintProjectTokens(r, issuer, email, projects, wildcard)
	if err != nil {
		http.Error(w, "token generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp := loginResponse{Email: email, Projects: tokens, Wildcard: wildcard}
	if wildcard {
		// The login token is a project token whose customer is the wildcard
		// sentinel — /auth/project-token accepts it, and it matches no real
		// session rows if someone tries to use it directly as a bearer token.
		lt, err := issuer.Issue(r.Context(), extension.ContextScope{
			UserEmail: email,
			Customer:  projectWildcard,
			Job:       "login",
		}, "")
		if err != nil {
			http.Error(w, "token generation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		resp.LoginToken = lt
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// authGoogleHandler serves POST /auth/google {credential} → 401 bad credential,
// 403 email not in the project map, else {email, projects:[{id, token}]}.
func authGoogleHandler(v *googleVerifier, pm userDirectory, issuer *devclaims.Issuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Credential string `json:"credential"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Credential == "" {
			http.Error(w, "missing credential", http.StatusBadRequest)
			return
		}
		email, err := v.Verify(r, body.Credential)
		if err != nil {
			http.Error(w, "invalid credential", http.StatusUnauthorized)
			return
		}
		projects, wildcard, ok := pm.resolve(email)
		if !ok {
			http.Error(w, "no projects for this account", http.StatusForbidden)
			return
		}
		writeLoginResponse(w, r, issuer, email, projects, wildcard)
	}
}

// verifyResponse is everything POST /auth/verify-google returns. Two fields, on
// purpose: Agent Bob verifies an identity for an embedding application and
// stops there. It mints no token, grants no project and creates no user row —
// the embedding app owns its own allowlist and its own sessions (see the design
// doc's rejected alternative "Wolf runs its own Google OAuth").
type verifyResponse struct {
	Email string `json:"email"`
	// EmailVerified is always true on a 200: googleVerifier.Verify refuses an
	// unverified address outright (see the email_verified check in Verify), so
	// the only other answer this route gives is 401. The field is here so the
	// caller reads the fact rather than having to know that rule.
	EmailVerified bool `json:"email_verified"`
}

// authVerifyGoogleHandler serves POST /auth/verify-google {credential} →
// {email, email_verified}. It is the identity seam for an application that
// embeds Agent Bob: the app's backend hands over the Google ID token its
// user signed in with, Bob says whose it is, and the app decides — from its
// own allowlist — whether that person may do anything. Bob deliberately does
// not become an identity provider.
//
// API-key auth only (see authenticatedByAPIKey): this is a token-verification
// oracle, and it answers only to a project's backend.
func authVerifyGoogleHandler(v *googleVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticatedByAPIKey(r) {
			http.Error(w, "project api key required", http.StatusForbidden)
			return
		}
		var body struct {
			Credential string `json:"credential"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Credential == "" {
			http.Error(w, "missing credential", http.StatusBadRequest)
			return
		}
		email, err := v.Verify(r, body.Credential)
		if err != nil {
			// One status for every rejection — bad signature, wrong audience,
			// unverified address. Distinguishing them would tell a caller
			// holding a stolen token which part of it Google disliked.
			http.Error(w, "invalid credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(verifyResponse{Email: email, EmailVerified: true})
	}
}

// authenticatedByAPIKey reports whether the request in hand authenticated with a
// project API key rather than a bearer JWT.
//
// It reads the header rather than the principal because apiAuthMiddleware tries
// X-API-Key first and refuses a bad one outright — it never falls through to the
// bearer path or to dev-open (auth.go:65-78). So a request that reached a
// handler carrying that header carried a *valid* key; nothing here re-checks the
// value, and a principal minted by any other path has no header to show.
func authenticatedByAPIKey(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get(apiKeyHeader)) != ""
}

// registerVerifyGoogle mounts POST /auth/verify-google — and mounts nothing at
// all when GOOGLE_CLIENT_ID is unset, so a deployment without Google login
// answers 404 rather than exposing a verifier with no audience to check tokens
// against.
//
// The mux it takes is the AUTHENTICATED one, which is the difference between
// this route and /auth/google: the login routes sit on the root mux, outside
// apiAuthMiddleware, because a browser has no credential yet when it logs in.
// This route's caller is a project backend that does have one, and must present
// it (main.go registers root.Handle("/", apiAuthMiddleware(…, apiMux)) last, so
// anything on apiMux is authenticated).
func registerVerifyGoogle(mux *http.ServeMux, googleClientID string) {
	if googleClientID == "" {
		return
	}
	mux.Handle("POST /auth/verify-google", authVerifyGoogleHandler(&googleVerifier{clientID: googleClientID}))
}

// authPasswordHandler serves POST /auth/password {email, password} against the
// fixed AGENTKIT_TEST_LOGIN pair ("email:password"). TEST/DEV ONLY — it exists
// so browser e2e can exercise the full login → project → session flow without
// Google. The account is granted every project in the map.
func authPasswordHandler(testEmail, testPassword string, pm userDirectory, issuer *devclaims.Issuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "missing credentials", http.StatusBadRequest)
			return
		}
		emailOK := subtle.ConstantTimeCompare([]byte(strings.ToLower(body.Email)), []byte(testEmail)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(body.Password), []byte(testPassword)) == 1
		if !emailOK || !passOK {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		// The test account is an implicit wildcard: every project, plus the
		// ability to mint new ones (it exists for e2e and local dev).
		writeLoginResponse(w, r, issuer, testEmail, pm.allProjects(), true)
	}
}

// authProjectTokenHandler serves POST /auth/project-token {token, project} —
// the wildcard-login exchange: verifies the login token (HS256, customer="*")
// and mints a project-scoped JWT for any well-formed project ID, including
// ones no session carries yet. This is how a new project is "created": pick a
// name, get a token, start a session in it.
func authProjectTokenHandler(secret []byte, issuer *devclaims.Issuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token   string `json:"token"`
			Project string `json:"project"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}
		if !validProjectID.MatchString(body.Project) || len(body.Project) > 64 {
			http.Error(w, "invalid project id (want kebab-case, e.g. apples-oranges)", http.StatusBadRequest)
			return
		}
		claims := jwt.MapClaims{}
		tok, err := jwt.ParseWithClaims(body.Token, claims, func(*jwt.Token) (any, error) {
			return secret, nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !tok.Valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		email, _ := claims["email"].(string)
		if customer, _ := claims["customer"].(string); customer != projectWildcard || email == "" {
			http.Error(w, "not a wildcard login token", http.StatusForbidden)
			return
		}
		// Reaching here already proved customer == projectWildcard (checked
		// above), so this mint is always on behalf of an operator (§1.1).
		minted, err := mintProjectTokens(r, issuer, email, []string{body.Project}, true)
		if err != nil {
			http.Error(w, "token generation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(minted[0])
	}
}

// authConfigHandler serves GET /auth/config — the runtime config channel the
// web UI reads to decide which login UI to render (no build-time Vite env).
// credMode is the model credential agentd booted with (mock | api-key |
// subscription, from credentialMode in modelproxy.go). It rides this payload
// because it is the same kind of fact as the login modes — runtime truth the
// browser cannot infer — and because the mock is the DEFAULT: a stack whose
// credential lines are blank produces plausible canned output everywhere, and
// the UI has to be able to say so (RD18).
func authConfigHandler(googleClientID string, passwordLogin bool, credMode string) http.HandlerFunc {
	modes := []string{}
	if googleClientID != "" {
		modes = append(modes, "google")
	}
	if passwordLogin {
		modes = append(modes, "password")
	}
	if len(modes) == 0 {
		modes = append(modes, "dev")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"modes":            modes,
			"google_client_id": googleClientID,
			"credential_mode":  credMode,
		})
	}
}

// parseTestLogin splits AGENTKIT_TEST_LOGIN ("email:password") — the password
// may itself contain colons; only the first splits.
func parseTestLogin(v string) (email, password string, err error) {
	email, password, found := strings.Cut(v, ":")
	email = strings.ToLower(strings.TrimSpace(email))
	if !found || email == "" || password == "" {
		return "", "", fmt.Errorf("AGENTKIT_TEST_LOGIN must be \"email:password\"")
	}
	return email, password, nil
}
