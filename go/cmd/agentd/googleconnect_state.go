package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/badcodetv/agent-bob/connections"
	"golang.org/x/oauth2"
)

// googleconnect_state.go — the pieces of Connect Google that hold no HTTP
// handler: the boot config, the signed OAuth state, the in-memory pending
// connects, PKCE and the authorize URL
// (design/2026-09-11-project-connections.md, "Addendum 2026-09-16", A3, A6, A7).
// Nothing here logs; nothing here puts a secret, verifier, nonce or state into
// an error.

// googleConnectScopes is everything a person is asked to approve, in the form
// Google's `scope` parameter takes. It lives only here, never in the project
// map, so a map edit cannot widen the request (A3). openid + email are there
// so the ID token names the connected account without another API call.
const googleConnectScopes = "openid email https://www.googleapis.com/auth/drive https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.compose https://www.googleapis.com/auth/documents https://www.googleapis.com/auth/spreadsheets"

// requiredProductScopes are the scopes the callback insists were granted:
// googleConnectScopes minus the identity pair, derived so there is one list.
var requiredProductScopes = func() []string {
	var out []string
	for _, s := range strings.Fields(googleConnectScopes) {
		if s != "openid" && s != "email" {
			out = append(out, s)
		}
	}
	return out
}()

// googleConnectTTL bounds both the signed state and the pending entry (A6).
const googleConnectTTL = 10 * time.Minute

// googleConnectCallbackPath is where Google sends the browser back. It is
// under /auth/ because nginx already proxies that prefix to agentd and the
// callback must be unauthenticated.
const googleConnectCallbackPath = "/auth/connections/google/callback"

// googleConnectConfig is Connect Google's boot configuration. Enabled when
// disabledReason is "". The four endpoint fields are unexported and set only
// by loadGoogleConnectConfig (to Google's) or by test code — never from the
// environment or the project map, like connections.Auth's tokenURL.
type googleConnectConfig struct {
	clientID, clientSecret, publicBase string
	sealer                             *connections.Sealer
	disabledReason                     string

	authURL, tokenURL, tokeninfoURL, revokeURL string
}

func (c googleConnectConfig) enabled() bool { return c.disabledReason == "" }

// redirectURI is the callback URL registered on the OAuth client.
func (c googleConnectConfig) redirectURI() string {
	return strings.TrimRight(c.publicBase, "/") + googleConnectCallbackPath
}

// loadGoogleConnectConfig reads Connect Google's environment. A missing
// variable disables the feature with a reason naming it; a key that is set
// but malformed, or that is the JWT secret, is an error so agentd refuses to
// boot — both are an operator mistake that would otherwise surface later as
// every stored connection failing to open. jwtSecret is the raw
// AGENTKIT_JWT_SECRET (empty in dev-open mode). Errors never carry a value.
func loadGoogleConnectConfig(getenv func(string) string, publicBase string, jwtSecret []byte) (googleConnectConfig, error) {
	cfg := googleConnectConfig{
		publicBase:   publicBase,
		authURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		tokenURL:     "https://oauth2.googleapis.com/token",
		tokeninfoURL: "https://oauth2.googleapis.com/tokeninfo",
		revokeURL:    "https://oauth2.googleapis.com/revoke",
	}

	rawKey := getenv("AGENTKIT_CONNECTIONS_KEY")
	if rawKey == "" {
		cfg.disabledReason = "Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set"
		return cfg, nil
	}
	key, err := connections.ParseKey(rawKey)
	if err != nil {
		return googleConnectConfig{}, err
	}
	// Either spelling of "the same secret twice": the env strings match, or
	// the decoded key is byte-for-byte the JWT secret.
	if len(jwtSecret) > 0 && (hmac.Equal([]byte(rawKey), jwtSecret) || hmac.Equal(key, jwtSecret)) {
		return googleConnectConfig{}, errors.New("AGENTKIT_CONNECTIONS_KEY must not be the same secret as AGENTKIT_JWT_SECRET: generate a separate one with `openssl rand -base64 32`")
	}
	if cfg.sealer, err = connections.NewSealer(key); err != nil {
		return googleConnectConfig{}, err
	}

	cfg.clientID = getenv("GOOGLE_CLIENT_ID")
	cfg.clientSecret = getenv("GOOGLE_CLIENT_SECRET")
	switch {
	case cfg.clientID == "":
		cfg.disabledReason = "Connect Google is off: GOOGLE_CLIENT_ID is not set"
	case cfg.clientSecret == "":
		cfg.disabledReason = "Connect Google is off: GOOGLE_CLIENT_SECRET is not set"
	}
	return cfg, nil
}

// authorizeURL is where the console sends the browser: Google's consent page
// asking for googleConnectScopes, offline access and a fresh consent so a
// refresh token comes back (A7), with PKCE S256 (A6.3).
func authorizeURL(cfg googleConnectConfig, state, challenge string) string {
	q := url.Values{
		"response_type":          {"code"},
		"client_id":              {cfg.clientID},
		"redirect_uri":           {cfg.redirectURI()},
		"scope":                  {googleConnectScopes},
		"state":                  {state},
		"access_type":            {"offline"},
		"prompt":                 {"consent"},
		"include_granted_scopes": {"false"},
		"code_challenge":         {challenge},
		"code_challenge_method":  {"S256"},
	}
	return cfg.authURL + "?" + q.Encode()
}

// newPKCEVerifier returns a fresh RFC 7636 code verifier (32 random octets).
func newPKCEVerifier() string { return oauth2.GenerateVerifier() }

// pkceChallenge is the S256 code challenge for verifier.
func pkceChallenge(verifier string) string { return oauth2.S256ChallengeFromVerifier(verifier) }

// newConnectNonce returns 32 random bytes, base64url without padding: safe as
// a cookie value and inside the state.
func newConnectNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("connect google: generating nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ── Signed state (A6) ─────────────────────────────────────────────────────────

// connectState is the OAuth state's payload. Exp is unix seconds. V is
// stamped by signState.
type connectState struct {
	V       int    `json:"v"`
	Project string `json:"p"`
	Account string `json:"a"`
	Email   string `json:"u"`
	Nonce   string `json:"n"`
	Exp     int64  `json:"exp"`
}

const connectStateVersion = 1

var (
	// errStateInvalid: malformed, unknown version, or the signature does not
	// verify. Such a state cannot be trusted to name a project.
	errStateInvalid = errors.New("connect google: the state is invalid")
	// errStateExpired: a genuine state past its expiry.
	errStateExpired = errors.New("connect google: the state has expired")
)

// stateMAC is HMAC-SHA256(key, payload) over the base64url payload text.
func stateMAC(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// signState returns base64url(json) + "." + base64url(HMAC-SHA256(key, that)).
// key is the Sealer's StateKey, never the encryption key or the JWT secret.
func signState(key []byte, st connectState) (string, error) {
	st.V = connectStateVersion
	body, err := json.Marshal(st)
	if err != nil {
		return "", fmt.Errorf("connect google: encoding state: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + base64.RawURLEncoding.EncodeToString(stateMAC(key, payload)), nil
}

// verifyState checks the signature first and only then looks inside, so an
// unsigned payload never decides anything — not even which error is returned.
// It returns errStateInvalid or errStateExpired, never text from the input.
// With errStateExpired the verified payload is returned too, so the callback
// can redirect to that project's Settings with reason "expired".
func verifyState(key []byte, raw string, now time.Time) (connectState, error) {
	payload, sig, ok := strings.Cut(raw, ".")
	if !ok || payload == "" || strings.Contains(sig, ".") {
		return connectState{}, errStateInvalid
	}
	gotMAC, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(gotMAC, stateMAC(key, payload)) {
		return connectState{}, errStateInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return connectState{}, errStateInvalid
	}
	var st connectState
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		return connectState{}, errStateInvalid
	}
	if st.V != connectStateVersion || st.Project == "" || st.Account == "" || st.Email == "" || st.Nonce == "" || st.Exp == 0 {
		return connectState{}, errStateInvalid
	}
	if now.Unix() >= st.Exp {
		// The signature verified, so the payload may name where to send the
		// person back to with "expired" (T23); it must not be used for more.
		return st, errStateExpired
	}
	return st, nil
}

// ── Pending connects (A6.2) ───────────────────────────────────────────────────

// pendingConnectsCap bounds memory held for connects nobody finished.
const pendingConnectsCap = 256

var errPendingConnectsFull = errors.New("connect google: too many connects in progress; try again in a few minutes")

// pendingConnect is what the callback needs and the state does not carry:
// the PKCE verifier stays server-side.
type pendingConnect struct {
	project, account, email, verifier string
	exp                               time.Time
}

// pendingConnects makes the state single-use: the callback takes the entry
// before exchanging the code, so a replay finds nothing. In memory, so an
// agentd restart mid-flow costs the person one more click; that assumes one
// agentd process, which is how Bob runs.
type pendingConnects struct {
	mu  sync.Mutex
	m   map[string]pendingConnect
	now func() time.Time
}

func newPendingConnects() *pendingConnects {
	return &pendingConnects{m: map[string]pendingConnect{}, now: time.Now}
}

// put records a pending connect under its nonce. Expired entries are reaped
// first; past the cap it refuses rather than evicting a live connect.
func (p *pendingConnects) put(nonce string, pc pendingConnect) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for n, e := range p.m {
		if !now.Before(e.exp) {
			delete(p.m, n)
		}
	}
	if len(p.m) >= pendingConnectsCap {
		return errPendingConnectsFull
	}
	p.m[nonce] = pc
	return nil
}

// take removes and returns the entry for nonce. An expired entry is removed
// and reported as missing.
func (p *pendingConnects) take(nonce string) (pendingConnect, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, ok := p.m[nonce]
	if !ok {
		return pendingConnect{}, false
	}
	delete(p.m, nonce)
	if !p.now().Before(pc.exp) {
		return pendingConnect{}, false
	}
	return pc, true
}

func (p *pendingConnects) len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.m)
}
