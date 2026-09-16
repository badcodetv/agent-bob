package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/badcodetv/agent-bob/modelproxy"
)

// dummyPassthroughKey satisfies Claude Code's startup check; it is NEVER sent
// upstream — newModelProxyHandler injects the real key (from agentd's own env).
const dummyPassthroughKey = "sk-ant-api03-proxy-passthrough-key-00000000000000000000000000000000000000000000000000000000AA"

// modelProvider configures the real upstream for the agentd /agent-proxy route.
// It implements modelproxy.PathRewriter so the upstream path is the direct
// Anthropic path (/v1/messages), not the Azure-style /anthropic/v1/messages.
type modelProvider struct {
	endpoint string
	apiKey   string
}

func (p modelProvider) Endpoint() string                     { return p.endpoint }
func (p modelProvider) APIKey() string                       { return p.apiKey }
func (p modelProvider) RewriteModel(name string) string      { return name }
func (p modelProvider) TargetPath(inboundPath string) string { return inboundPath }

// newModelProxyHandler chooses the model path at startup. With
// CLAUDE_CODE_OAUTH_TOKEN set (subscription mode — the token outranks the API
// key), sessions bypass the proxy entirely: main points them straight at
// api.anthropic.com, so the proxy serves the mock rather than sitting mounted
// with a real key nothing should reach. Otherwise: real Anthropic when
// ANTHROPIC_API_KEY is set in agentd's env, mock SSE when neither is.
//
// The mock is scriptable (see modelproxy/script.go): with
// AGENTKIT_MOCK_MODEL_SCRIPT (inline JSON) or AGENTKIT_MOCK_MODEL_SCRIPT_FILE
// (a path) set, the mock serves scripted turns — including `tool_use` blocks,
// which the canned stream can never produce. That is what makes an offline test
// able to drive a worker into calling an MCP tool. Neither set → the canned
// stream, unchanged.
//
// auth guards ONLY the real-key branch (H6, docs/19-embedding.md): before
// 2026-09, that branch mounted a real Anthropic key behind no auth at all —
// anything on the docker network that could reach agentd's port could spend
// Kai's API budget with no session, no rate limit and no attribution. Mock
// and subscription mode stay open: neither mounts a billing key (subscription
// mode does not even mount THIS handler for sessions — see the log line
// below), so there is nothing there for a guard to protect.
func newModelProxyHandler(auth *sessionTokenAuth) http.Handler {
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
		// Not mock mode — sessions are on the real model, direct. This only says
		// the unused /agent-proxy/ route is inert (no billing key mounted on it).
		log.Printf("[agentd] subscription mode → /agent-proxy/ is unused by sessions and mounts no API key")
		return modelproxy.MockHandler()
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		table, err := loadMockModelScript()
		if err != nil {
			// A misconfigured script must not boot: a silently-ignored one turns
			// into a mysterious test failure a long way from the typo.
			log.Fatalf("[agentd] %v", err)
		}
		if table == nil {
			log.Printf("[agentd] ANTHROPIC_API_KEY unset → MOCK model proxy (set it for a real agent)")
			return modelproxy.MockHandler()
		}
		log.Printf("[agentd] ANTHROPIC_API_KEY unset → SCRIPTED mock model proxy (%d rule(s))", len(table.Rules))
		return modelproxy.ScriptedMockHandler(table)
	}
	endpoint := envOr("ANTHROPIC_UPSTREAM_URL", "https://api.anthropic.com")
	log.Printf("[agentd] real model proxy → %s (session-token guarded)", endpoint)
	return requireSessionToken(auth, modelproxy.Handler(modelProvider{endpoint: endpoint, apiKey: key}))
}

// requireSessionToken wraps next so it is reachable only with a session token
// that verifyToken accepts under requireLive=true (H6): a valid, unexpired
// token is always honoured; an expired one is honoured only for a session
// that is still live (sessionIsLive) — the same live-only rule T12's
// `/connect/` uses, sharing sessionTokenAuth.verifyToken so the two cannot
// drift apart.
//
// The credential arrives in X-Api-Key: that is the header the Claude Agent
// SDK sends its configured ANTHROPIC_API_KEY under, and the Runner puts the
// per-session JWT there (runner.go sessionEnv, unless
// Policy.DisableModelAPIKeyOverride is set) precisely so this guard has
// something to check without the sandbox changing at all. Authorization is
// checked too, as a fallback for a client that sends it there instead — never
// the other way around, since X-Api-Key is what the SDK actually sends.
//
// The upstream is never contacted when this refuses: the wrapped handler's
// http.Handler is only reached after this returns.
func requireSessionToken(auth *sessionTokenAuth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r.Header.Get("X-Api-Key"))
		if raw == "" {
			raw = bearerToken(r.Header.Get("Authorization"))
		}
		if _, err := auth.verifyToken(r.Context(), raw, true); err != nil {
			writeProxyUnauthorized(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeProxyUnauthorized writes the 401 JSON body for a request
// requireSessionToken refused. The message is whatever verifyToken decided
// ("session token rejected" / "session token expired" / etc — see
// mcpserver.go's status table); this never leaks which case it was beyond
// that message, and never contacts the upstream.
func writeProxyUnauthorized(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// The credential modes reported to the browser by GET /auth/config (RD18).
// agentd has always logged which model path it booted with; nothing told the
// UI, so a stack running the offline mock — which is what a user following the
// README verbatim gets, both credential lines shipping blank — writes plausible
// canned output into Desk, Events and Jobs with no marker anywhere.
const (
	credentialModeMock         = "mock"
	credentialModeAPIKey       = "api-key"
	credentialModeSubscription = "subscription"
)

// credentialMode names the model credential agentd booted with. It is the ONE
// place that precedence is expressed for reporting, and it deliberately mirrors
// the two places that act on it: newModelProxyHandler (token set → mock proxy,
// bypassed; else key → real proxy, else mock) and main's subscriptionMode
// (`oauthToken != ""` — the token outranks the key, so blanking the token is
// what flips a deployment to unattended API billing). Mirrored rather than
// shared because those two decide different things; a test pins the three
// answers so the mirror cannot drift silently.
// The comparisons are raw (not trimmed) on purpose: both of those callers test
// the raw value, and a badge that disagreed with the proxy would be worse than
// no badge at all.
func credentialMode(apiKey, oauthToken string) string {
	switch {
	case oauthToken != "":
		return credentialModeSubscription
	case apiKey != "":
		return credentialModeAPIKey
	default:
		return credentialModeMock
	}
}

// mockScriptEnv / mockScriptFileEnv name the two ways a stack supplies a mock
// model script. Inline wins when both are set (a `.env` override beating a
// baked-in file is the least surprising precedence).
const (
	mockScriptEnv     = "AGENTKIT_MOCK_MODEL_SCRIPT"
	mockScriptFileEnv = "AGENTKIT_MOCK_MODEL_SCRIPT_FILE"
)

// loadMockModelScript reads the configured script table, or (nil, nil) when
// neither environment variable is set.
func loadMockModelScript() (*modelproxy.ScriptTable, error) {
	if inline := strings.TrimSpace(os.Getenv(mockScriptEnv)); inline != "" {
		t, err := modelproxy.ParseScriptTable(inline)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", mockScriptEnv, err)
		}
		return t, nil
	}
	path := strings.TrimSpace(os.Getenv(mockScriptFileEnv))
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", mockScriptFileEnv, err)
	}
	t, err := modelproxy.ParseScriptTable(string(b))
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", mockScriptFileEnv, path, err)
	}
	if t == nil {
		return nil, fmt.Errorf("%s (%s): file is empty", mockScriptFileEnv, path)
	}
	return t, nil
}

// Every model variable either mode sets is written by BOTH modes — as an empty
// string by the mode that does not use it — because a key left out is not a key
// left unset. `docker commit` copies the container's env into the snapshot
// image's config, so a session archived under one mode and restored under the
// other inherits the old mode's wiring for any key the new env omits. That was a
// real production failure: sessions snapshotted in API-key mode, restored after
// the box moved to subscription mode, kept ANTHROPIC_BASE_URL=…/agent-proxy and
// talked to the proxy — which in subscription mode serves the mock. An explicit
// `KEY=` overrides the image value, and both the sandbox (`if
// (config.ANTHROPIC_BASE_URL)`) and the in-image CLI treat empty as unset.

// sandboxSessionEnv is injected into every session container. It points the
// in-sandbox model SDK at agentd's own /agent-proxy route (reachable from inside
// DinD at selfURL) and supplies a dummy key so the CLI boots.
func sandboxSessionEnv(selfURL string) map[string]string {
	return map[string]string{
		"ANTHROPIC_BASE_URL":      selfURL + "/agent-proxy",
		"HOST_API_URL":            selfURL,
		"ANTHROPIC_API_KEY":       dummyPassthroughKey,
		"CLAUDE_CODE_OAUTH_TOKEN": "", // blank one a subscription-mode snapshot baked in
	}
}

// subscriptionSessionEnv is the session env for subscription mode: the in-image
// `claude` CLI authenticates to api.anthropic.com directly with the Claude Code
// OAuth token (from `claude setup-token`). ANTHROPIC_BASE_URL is blank (the
// sandbox skips its model proxy plumbing) and so is ANTHROPIC_API_KEY (the CLI
// must fall through to the OAuth token; the Runner's JWT override is disabled
// too, via Policy.DisableModelAPIKeyOverride).
func subscriptionSessionEnv(selfURL, oauthToken string) map[string]string {
	return map[string]string{
		"HOST_API_URL":            selfURL,
		"CLAUDE_CODE_OAUTH_TOKEN": oauthToken,
		"ANTHROPIC_BASE_URL":      "",
		"ANTHROPIC_API_KEY":       "",
	}
}
