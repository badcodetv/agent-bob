package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestCredentialMode pins the three answers the browser is told, in the same
// precedence the two acting call sites apply: an OAuth token wins outright
// (attended subscription billing by default; production blanks the token), an
// API key alone is proxy mode, neither is the mock. RD18: the mock is the
// DEFAULT (both credential lines ship blank in .env.example), so a wrong
// answer here is the difference between "the product works" and "no model was
// ever called".
func TestCredentialMode(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		oauth string
		want  string
	}{
		{"neither is mock", "", "", credentialModeMock},
		{"api key alone", "sk-ant-api03-x", "", credentialModeAPIKey},
		{"oauth token alone", "", "sk-ant-oat01-x", credentialModeSubscription},
		{"oauth token wins over api key", "sk-ant-api03-x", "sk-ant-oat01-x", credentialModeSubscription},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := credentialMode(tt.key, tt.oauth); got != tt.want {
				t.Fatalf("credentialMode(%q, %q) = %q, want %q", tt.key, tt.oauth, got, tt.want)
			}
		})
	}
}

// TestAuthConfigReportsCredentialMode is the wire-level half: whatever
// credentialMode decided has to reach the browser, in every login mode.
func TestAuthConfigReportsCredentialMode(t *testing.T) {
	for _, mode := range []string{credentialModeMock, credentialModeAPIKey, credentialModeSubscription} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			authConfigHandler("", false, mode)(rec, httptest.NewRequest(http.MethodGet, "/auth/config", nil))
			var resp struct {
				CredentialMode string `json:"credential_mode"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.CredentialMode != mode {
				t.Fatalf("credential_mode = %q, want %q", resp.CredentialMode, mode)
			}
		})
	}
}

func TestNewModelProxyHandler_MockWhenNoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	h := newModelProxyHandler()
	if h == nil {
		t.Fatal("handler is nil")
	}
	// Mock provider answers GET .../health with the mock note.
	if got := os.Getenv("ANTHROPIC_API_KEY"); got != "" {
		t.Fatalf("precondition: key should be empty, got %q", got)
	}
}

func TestModelProvider_TargetPathIsDirectAnthropic(t *testing.T) {
	p := modelProvider{endpoint: "https://api.anthropic.com", apiKey: "k"}
	if got := p.TargetPath("/v1/messages"); got != "/v1/messages" {
		t.Fatalf("TargetPath = %q, want /v1/messages (no /anthropic prefix)", got)
	}
	if p.Endpoint() != "https://api.anthropic.com" || p.APIKey() != "k" {
		t.Fatalf("provider accessors wrong: %+v", p)
	}
}

func TestSandboxSessionEnv_PointsAtAgentProxyAndDummyKey(t *testing.T) {
	env := sandboxSessionEnv("http://172.17.0.1:8099")
	if env["ANTHROPIC_BASE_URL"] != "http://172.17.0.1:8099/agent-proxy" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q", env["ANTHROPIC_BASE_URL"])
	}
	if env["HOST_API_URL"] != "http://172.17.0.1:8099" {
		t.Fatalf("HOST_API_URL = %q", env["HOST_API_URL"])
	}
	if !strings.HasPrefix(env["ANTHROPIC_API_KEY"], "sk-ant-") {
		t.Fatalf("expected dummy passthrough key, got %q", env["ANTHROPIC_API_KEY"])
	}
}

// TestSubscriptionSessionEnv locks direct (subscription) mode: sessions get the
// OAuth token and NO proxy plumbing — base URL and API key present but EMPTY.
// Absent is not enough: a snapshot taken in proxy mode carries both in its
// image config, and only an explicit `KEY=` overrides them on restore.
func TestSubscriptionSessionEnv_DirectWithOAuthTokenOnly(t *testing.T) {
	env := subscriptionSessionEnv("http://172.17.0.1:8099", "sk-ant-oat01-test")
	if env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-test" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q", env["CLAUDE_CODE_OAUTH_TOKEN"])
	}
	if env["HOST_API_URL"] != "http://172.17.0.1:8099" {
		t.Fatalf("HOST_API_URL = %q", env["HOST_API_URL"])
	}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"} {
		if v, ok := env[k]; !ok || v != "" {
			t.Fatalf("%s must be set to empty in subscription mode, got %q (present=%v)", k, v, ok)
		}
	}
}

// TestSessionEnv_ModeSwitchLeavesNoStaleWiring is the production failure: a
// session snapshotted under one model mode and restored under the other. The
// snapshot image's env is the old mode's; the container env is the image env
// with the new session env laid over it (what Docker does). Nothing from the
// old mode may survive.
func TestSessionEnv_ModeSwitchLeavesNoStaleWiring(t *testing.T) {
	const self = "http://172.17.0.1:8099"
	overlay := func(image, session map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range image {
			out[k] = v
		}
		for k, v := range session {
			out[k] = v
		}
		return out
	}

	t.Run("proxy snapshot restored in subscription mode", func(t *testing.T) {
		got := overlay(sandboxSessionEnv(self), subscriptionSessionEnv(self, "sk-ant-oat01-new"))
		if got["ANTHROPIC_BASE_URL"] != "" {
			t.Errorf("ANTHROPIC_BASE_URL = %q: the session would talk to the proxy, which serves the mock", got["ANTHROPIC_BASE_URL"])
		}
		if got["ANTHROPIC_API_KEY"] != "" {
			t.Errorf("ANTHROPIC_API_KEY = %q, want empty", got["ANTHROPIC_API_KEY"])
		}
	})

	t.Run("subscription snapshot restored in proxy mode", func(t *testing.T) {
		got := overlay(subscriptionSessionEnv(self, "sk-ant-oat01-old"), sandboxSessionEnv(self))
		if got["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
			t.Errorf("CLAUDE_CODE_OAUTH_TOKEN = %q, want empty", got["CLAUDE_CODE_OAUTH_TOKEN"])
		}
		if got["ANTHROPIC_BASE_URL"] != self+"/agent-proxy" {
			t.Errorf("ANTHROPIC_BASE_URL = %q", got["ANTHROPIC_BASE_URL"])
		}
	})
}
