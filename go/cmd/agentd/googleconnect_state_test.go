package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/connections"
)

// testConnectionsKey is a valid AGENTKIT_CONNECTIONS_KEY: base64 of 32 bytes.
var testConnectionsKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

func connectEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestGoogleConnectConfig_Load(t *testing.T) {
	full := map[string]string{
		"AGENTKIT_CONNECTIONS_KEY": testConnectionsKey,
		"GOOGLE_CLIENT_ID":         "client-id.apps.googleusercontent.com",
		"GOOGLE_CLIENT_SECRET":     "client-secret-value",
	}
	without := func(k string) map[string]string {
		m := map[string]string{}
		for kk, v := range full {
			if kk != k {
				m[kk] = v
			}
		}
		return m
	}
	with := func(k, v string) map[string]string {
		m := without(k)
		m[k] = v
		return m
	}

	tests := []struct {
		name         string
		env          map[string]string
		jwtSecret    []byte
		wantErr      bool
		wantDisabled string // "" means enabled
	}{
		{name: "everything set", env: full, jwtSecret: []byte("jwt-secret")},
		{name: "dev-open (no JWT secret) still loads", env: full},
		{
			name:         "key unset",
			env:          without("AGENTKIT_CONNECTIONS_KEY"),
			wantDisabled: "Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set",
		},
		{name: "key not base64", env: with("AGENTKIT_CONNECTIONS_KEY", "not base64!!"), wantErr: true},
		{name: "key wrong length", env: with("AGENTKIT_CONNECTIONS_KEY", base64.StdEncoding.EncodeToString(make([]byte, 16))), wantErr: true},
		{name: "key string equal to JWT secret", env: full, jwtSecret: []byte(testConnectionsKey), wantErr: true},
		{name: "key bytes equal to JWT secret", env: full, jwtSecret: bytes.Repeat([]byte{7}, 32), wantErr: true},
		{
			name:    "malformed key fails even when client vars are missing",
			env:     map[string]string{"AGENTKIT_CONNECTIONS_KEY": "short"},
			wantErr: true,
		},
		{
			name:         "client id unset",
			env:          without("GOOGLE_CLIENT_ID"),
			wantDisabled: "Connect Google is off: GOOGLE_CLIENT_ID is not set",
		},
		{
			name:         "client secret unset",
			env:          without("GOOGLE_CLIENT_SECRET"),
			wantDisabled: "Connect Google is off: GOOGLE_CLIENT_SECRET is not set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadGoogleConnectConfig(connectEnv(tt.env), "http://localhost:8080", tt.jwtSecret)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got config (disabled=%q)", cfg.disabledReason)
				}
				// No part of either secret may reach the error text: boot logs print it.
				for _, secret := range []string{tt.env["AGENTKIT_CONNECTIONS_KEY"], string(tt.jwtSecret)} {
					if len(secret) >= 4 && strings.Contains(err.Error(), secret) {
						t.Fatalf("error %q contains a secret value", err)
					}
				}
				if !strings.Contains(err.Error(), "AGENTKIT_CONNECTIONS_KEY") {
					t.Fatalf("error %q does not name the variable", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.disabledReason != tt.wantDisabled {
				t.Fatalf("disabledReason = %q, want %q", cfg.disabledReason, tt.wantDisabled)
			}
			if cfg.enabled() != (tt.wantDisabled == "") {
				t.Fatalf("enabled() = %v with reason %q", cfg.enabled(), cfg.disabledReason)
			}
			if tt.wantDisabled == "" {
				if cfg.sealer == nil || cfg.clientID != full["GOOGLE_CLIENT_ID"] || cfg.clientSecret != full["GOOGLE_CLIENT_SECRET"] {
					t.Fatalf("enabled config is incomplete: sealer=%v clientID=%q", cfg.sealer != nil, cfg.clientID)
				}
			}
			// The Google endpoints are Google's; only test code may override them.
			if cfg.authURL != "https://accounts.google.com/o/oauth2/v2/auth" ||
				cfg.tokenURL != "https://oauth2.googleapis.com/token" ||
				cfg.tokeninfoURL != "https://oauth2.googleapis.com/tokeninfo" ||
				cfg.revokeURL != "https://oauth2.googleapis.com/revoke" {
				t.Fatalf("endpoints = %q %q %q %q", cfg.authURL, cfg.tokenURL, cfg.tokeninfoURL, cfg.revokeURL)
			}
		})
	}
}

// The endpoints must not be settable from the environment, whatever the
// variable is called.
func TestGoogleConnectConfig_EndpointsIgnoreEnv(t *testing.T) {
	env := func(k string) string {
		switch k {
		case "AGENTKIT_CONNECTIONS_KEY":
			return testConnectionsKey
		case "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET":
			return "x"
		}
		return "https://evil.example/" + k
	}
	cfg, err := loadGoogleConnectConfig(env, "http://localhost:8080", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{cfg.authURL, cfg.tokenURL, cfg.tokeninfoURL, cfg.revokeURL} {
		if strings.Contains(u, "evil") {
			t.Fatalf("endpoint %q came from the environment", u)
		}
	}
}

// Pinned: this is what a person is asked to approve (addendum A3, with
// gmail.readonly added on Kai's answer of 2026-09-16).
func TestGoogleConnectScopes_Pinned(t *testing.T) {
	const want = "openid email https://www.googleapis.com/auth/drive https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/gmail.compose https://www.googleapis.com/auth/documents https://www.googleapis.com/auth/spreadsheets"
	if googleConnectScopes != want {
		t.Fatalf("googleConnectScopes = %q\nwant                 %q", googleConnectScopes, want)
	}
	wantRequired := []string{
		"https://www.googleapis.com/auth/drive",
		"https://www.googleapis.com/auth/gmail.readonly",
		"https://www.googleapis.com/auth/gmail.compose",
		"https://www.googleapis.com/auth/documents",
		"https://www.googleapis.com/auth/spreadsheets",
	}
	if fmt.Sprint(requiredProductScopes) != fmt.Sprint(wantRequired) {
		t.Fatalf("requiredProductScopes = %v, want %v", requiredProductScopes, wantRequired)
	}
}

func testConnectConfig(t *testing.T, publicBase string) googleConnectConfig {
	t.Helper()
	cfg, err := loadGoogleConnectConfig(connectEnv(map[string]string{
		"AGENTKIT_CONNECTIONS_KEY": testConnectionsKey,
		"GOOGLE_CLIENT_ID":         "client-id.apps.googleusercontent.com",
		"GOOGLE_CLIENT_SECRET":     "client-secret-value",
	}), publicBase, []byte("jwt-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestGoogleConnectState_RoundTrip(t *testing.T) {
	key := testConnectConfig(t, "http://localhost:8080").sealer.StateKey()
	now := time.Unix(1_800_000_000, 0)
	in := connectState{Project: "enc", Account: "google", Email: "richard@example.com", Nonce: "nonce-1", Exp: now.Add(googleConnectTTL).Unix()}

	raw, err := signState(key, in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(raw, ".") != 1 {
		t.Fatalf("state %q is not payload.signature", raw)
	}
	got, err := verifyState(key, raw, now)
	if err != nil {
		t.Fatalf("verifyState: %v", err)
	}
	in.V = 1
	if got != in {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
	// The payload is the addendum's wire form: v, p, a, u, n, exp.
	payload, _ := base64.RawURLEncoding.DecodeString(strings.SplitN(raw, ".", 2)[0])
	for _, field := range []string{`"v":1`, `"p":"enc"`, `"a":"google"`, `"u":"richard@example.com"`, `"n":"nonce-1"`, `"exp":`} {
		if !bytes.Contains(payload, []byte(field)) {
			t.Fatalf("payload %s lacks %s", payload, field)
		}
	}
}

func TestGoogleConnectState_Verify(t *testing.T) {
	key := testConnectConfig(t, "http://localhost:8080").sealer.StateKey()
	otherKey := connectionsSealer(t, bytes.Repeat([]byte{9}, 32)).StateKey()
	now := time.Unix(1_800_000_000, 0)
	good := connectState{Project: "enc", Account: "google", Email: "richard@example.com", Nonce: "nonce-1", Exp: now.Add(googleConnectTTL).Unix()}
	sign := func(k []byte, st connectState) string {
		raw, err := signState(k, st)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := sign(key, good)
	payload, sig, _ := strings.Cut(valid, ".")
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	expired := good
	expired.Exp = now.Add(-time.Second).Unix()
	wrongVersion := func() string {
		// Hand-sign a v:2 payload with the right key: a valid signature on a
		// payload this code does not understand is still invalid.
		p := base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"p":"enc","a":"google","u":"r@e.com","n":"n","exp":9999999999}`))
		return p + "." + base64.RawURLEncoding.EncodeToString(stateMAC(key, p))
	}()

	tests := []struct {
		name string
		raw  string
		want error
	}{
		{name: "valid", raw: valid},
		{name: "expired", raw: sign(key, expired), want: errStateExpired},
		{name: "exactly at expiry is expired", raw: sign(key, connectState{Project: "enc", Account: "google", Email: "a@b.c", Nonce: "n", Exp: now.Unix()}), want: errStateExpired},
		{name: "empty", raw: "", want: errStateInvalid},
		{name: "no dot", raw: payload + sig, want: errStateInvalid},
		{name: "two dots", raw: valid + ".x", want: errStateInvalid},
		{name: "flipped byte in payload", raw: flip(payload, 5) + "." + sig, want: errStateInvalid},
		{name: "flipped byte in signature", raw: payload + "." + flip(sig, 5), want: errStateInvalid},
		{name: "truncated signature", raw: payload + "." + sig[:len(sig)-2], want: errStateInvalid},
		{name: "signature not base64", raw: payload + ".!!!", want: errStateInvalid},
		{name: "signed with another key", raw: sign(otherKey, good), want: errStateInvalid},
		{name: "expired AND signed with another key is invalid, not expired", raw: sign(otherKey, expired), want: errStateInvalid},
		{name: "unknown version", raw: wrongVersion, want: errStateInvalid},
		{name: "valid signature over non-JSON", raw: "bm90anNvbg." + base64.RawURLEncoding.EncodeToString(stateMAC(key, "bm90anNvbg")), want: errStateInvalid},
		{name: "missing nonce", raw: sign(key, connectState{Project: "enc", Account: "google", Email: "a@b.c", Exp: good.Exp}), want: errStateInvalid},
		{name: "missing project", raw: sign(key, connectState{Account: "google", Email: "a@b.c", Nonce: "n", Exp: good.Exp}), want: errStateInvalid},
	}
	// A genuine expired state still names its project (T23 redirects there).
	if st, err := verifyState(key, sign(key, expired), now); !errors.Is(err, errStateExpired) || st.Project != "enc" || st.Account != "google" {
		t.Fatalf("expired state = %+v, %v; want the verified payload with errStateExpired", st, err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifyState(key, tt.raw, now)
			if !errors.Is(err, tt.want) {
				t.Fatalf("verifyState err = %v, want %v", err, tt.want)
			}
			if err != nil && tt.raw != "" && strings.Contains(err.Error(), tt.raw) {
				t.Fatalf("error text quotes the state")
			}
		})
	}
}

func connectionsSealer(t *testing.T, key []byte) *connections.Sealer {
	t.Helper()
	s, err := connections.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestGoogleConnectNonce_RandomAndURLSafe(t *testing.T) {
	a, err := newConnectNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newConnectNonce()
	if a == b || len(a) < 43 {
		t.Fatalf("nonces %q %q: want distinct, >= 32 bytes of entropy", a, b)
	}
	if strings.ContainsAny(a, "+/=;, ") {
		t.Fatalf("nonce %q is not safe in a cookie and a URL", a)
	}
}

func TestGoogleConnectPending(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	p := newPendingConnects()
	p.now = func() time.Time { return clock }
	entry := func(project string, exp time.Time) pendingConnect {
		return pendingConnect{project: project, account: "google", email: "r@example.com", verifier: "v", exp: exp}
	}

	t.Run("take twice, second misses", func(t *testing.T) {
		if err := p.put("n1", entry("enc", now.Add(googleConnectTTL))); err != nil {
			t.Fatal(err)
		}
		got, ok := p.take("n1")
		if !ok || got.project != "enc" || got.verifier != "v" {
			t.Fatalf("first take = %+v, %v", got, ok)
		}
		if _, ok := p.take("n1"); ok {
			t.Fatal("second take found the entry: a callback could be replayed")
		}
	})

	t.Run("expired entry is not taken", func(t *testing.T) {
		if err := p.put("n2", entry("enc", now.Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
		clock = now.Add(2 * time.Minute)
		defer func() { clock = now }()
		if _, ok := p.take("n2"); ok {
			t.Fatal("took an expired pending connect")
		}
	})

	t.Run("unknown nonce misses", func(t *testing.T) {
		if _, ok := p.take("nope"); ok {
			t.Fatal("took an entry that was never put")
		}
	})
}

func TestGoogleConnectPending_Cap(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	clock := now
	p := newPendingConnects()
	p.now = func() time.Time { return clock }

	// Fill to the cap: half short-lived, half long-lived.
	for i := 0; i < pendingConnectsCap; i++ {
		exp := now.Add(googleConnectTTL)
		if i%2 == 0 {
			exp = now.Add(time.Minute)
		}
		if err := p.put(fmt.Sprintf("n%d", i), pendingConnect{project: "enc", exp: exp}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	if err := p.put("over", pendingConnect{project: "enc", exp: now.Add(googleConnectTTL)}); !errors.Is(err, errPendingConnectsFull) {
		t.Fatalf("put past the cap = %v, want errPendingConnectsFull", err)
	}

	// Once the short-lived half expires, inserting reaps them first.
	clock = now.Add(2 * time.Minute)
	if err := p.put("after-reap", pendingConnect{project: "enc", exp: clock.Add(googleConnectTTL)}); err != nil {
		t.Fatalf("put after expiry: %v", err)
	}
	if got := p.len(); got != pendingConnectsCap/2+1 {
		t.Fatalf("len after reap = %d, want %d", got, pendingConnectsCap/2+1)
	}
	if _, ok := p.take("n1"); !ok {
		t.Fatal("a live entry was evicted by the reap")
	}
	if _, ok := p.take("after-reap"); !ok {
		t.Fatal("the new entry is missing")
	}
}

func TestGoogleConnectPending_Concurrent(t *testing.T) {
	p := newPendingConnects()
	exp := time.Now().Add(googleConnectTTL)
	for i := 0; i < 100; i++ {
		if err := p.put(fmt.Sprintf("n%d", i), pendingConnect{exp: exp}); err != nil {
			t.Fatal(err)
		}
	}
	// 100 goroutines race for the same 100 nonces twice over; each nonce must
	// be taken exactly once.
	results := make(chan bool, 200)
	for i := 0; i < 200; i++ {
		go func(i int) {
			_, ok := p.take(fmt.Sprintf("n%d", i%100))
			results <- ok
		}(i)
	}
	taken := 0
	for i := 0; i < 200; i++ {
		if <-results {
			taken++
		}
	}
	if taken != 100 {
		t.Fatalf("taken = %d, want exactly 100", taken)
	}
}

func TestGoogleConnectPKCE(t *testing.T) {
	// RFC 7636 appendix B.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := pkceChallenge(verifier); got != challenge {
		t.Fatalf("pkceChallenge = %q, want %q", got, challenge)
	}
	a, b := newPKCEVerifier(), newPKCEVerifier()
	if a == b || len(a) < 43 || len(a) > 128 {
		t.Fatalf("verifiers %q %q: want distinct, 43..128 chars (RFC 7636 §4.1)", a, b)
	}
}

func TestGoogleConnectAuthorizeURL(t *testing.T) {
	for _, tt := range []struct {
		publicBase, wantRedirect string
	}{
		{"http://localhost:8080", "http://localhost:8080/auth/connections/google/callback"},
		{"https://bob.box.badcode.tv", "https://bob.box.badcode.tv/auth/connections/google/callback"},
		{"https://bob.box.badcode.tv/", "https://bob.box.badcode.tv/auth/connections/google/callback"},
	} {
		t.Run(tt.publicBase, func(t *testing.T) {
			cfg := testConnectConfig(t, tt.publicBase)
			if got := cfg.redirectURI(); got != tt.wantRedirect {
				t.Fatalf("redirectURI = %q, want %q", got, tt.wantRedirect)
			}
			raw := authorizeURL(cfg, "the.state", "the-challenge")
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if u.Scheme+"://"+u.Host+u.Path != "https://accounts.google.com/o/oauth2/v2/auth" {
				t.Fatalf("authorize endpoint = %q", raw)
			}
			q := u.Query()
			want := map[string]string{
				"response_type":          "code",
				"client_id":              "client-id.apps.googleusercontent.com",
				"redirect_uri":           tt.wantRedirect,
				"scope":                  googleConnectScopes,
				"state":                  "the.state",
				"access_type":            "offline",
				"prompt":                 "consent",
				"include_granted_scopes": "false",
				"code_challenge":         "the-challenge",
				"code_challenge_method":  "S256",
			}
			for k, v := range want {
				if got := q.Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if len(q) != len(want) {
				t.Errorf("unexpected parameters: %v", q)
			}
			for _, s := range append([]string{"openid", "email"}, requiredProductScopes...) {
				if !strings.Contains(" "+q.Get("scope")+" ", " "+s+" ") {
					t.Errorf("scope lacks %s", s)
				}
			}
			// The client secret never goes to the browser.
			if strings.Contains(raw, "client-secret-value") {
				t.Fatal("authorize URL carries the client secret")
			}
		})
	}
}
