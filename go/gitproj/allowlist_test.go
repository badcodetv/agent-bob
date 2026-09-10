package gitproj

import (
	"errors"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// TestIsEnvRef is the whole secret rule at its narrowest: the ONLY shape a
// credential-bearing field may be published in is a whole-value ${VAR}
// reference. Everything else — literal, partial, padded, empty — is not that
// shape. The rule is stated positively on purpose (design doc DI1): "reject
// literals" is a denylist and a denylist missed `Bearer ${TOKEN}` once already.
func TestIsEnvRef(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
		why   string
	}{
		{"whole reference", "${SLACK_WEBHOOK_URL}", true,
			"the one accepted shape: agentd resolves it from its own environment at send time"},
		{"whole reference, leading underscore", "${_PRIVATE}", true,
			"a legal shell identifier; matches the resolver's own pattern"},
		{"whole reference with digits", "${TOKEN_2}", true, "a legal identifier"},

		{"literal slack webhook", "https://hooks.slack.com/services/T00000/B00000/xxxxxxxxxxxxxxxx", false,
			"THE case this ticket exists for: a Slack incoming-webhook URL is a bearer token in its entirety"},
		{"literal token", "xoxb-1234-5678-abcdefg", false, "an ordinary pasted secret"},
		{"partial interpolation, bearer", "Bearer ${TOKEN}", false,
			"DI1: cmd/agentd passes this through as a LITERAL, and it is the shape most likely to carry a real token"},
		{"partial interpolation, url path", "https://hooks.slack.com/services/${SECRET}", false,
			"same shape, embedded in a URL"},
		{"two references", "${A}${B}", false, "not a whole-value reference; nothing resolves it"},
		{"leading whitespace", " ${TOKEN}", false,
			"resolveHeaders trims before matching but MCPServerConfig.Validate does not, so whether this " +
				"resolves depends on which reader picks it up. A value that is safe only on some paths is refused."},
		{"trailing whitespace", "${TOKEN} ", false, "same reasoning as leading whitespace"},
		{"newline inside", "${TOKEN}\n", false, "not the shape"},
		{"bare dollar", "$TOKEN", false, "the resolvers only understand the ${VAR} form"},
		{"empty braces", "${}", false, "not a legal identifier"},
		{"invalid identifier", "${MY-TOKEN}", false, "a dash is not legal in a shell identifier; would never resolve"},
		{"empty string", "", false,
			"NOT a reference — but CheckRenderable treats an unset value as 'not configured' and allows it; " +
				"see TestCheckRenderableEmptyIsUnset"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsEnvRef(tc.value); got != tc.want {
				t.Fatalf("IsEnvRef(%q) = %v, want %v — %s", tc.value, got, tc.want, tc.why)
			}
		})
	}
}

// TestCheckRenderableAttentionChannel drives the rule through the real
// ProjectSettings table rather than a fixture, so the test fails if somebody
// downgrades AttentionChannel's decision. The channel's stored shape is
// {"kind":"webhook","url":"…","headers":{…}} — the leaf rule must reach `url`
// and every header VALUE while leaving `kind` and header NAMES alone.
func TestCheckRenderableAttentionChannel(t *testing.T) {
	cases := []struct {
		name      string
		channel   any
		wantErr   bool
		wantField string // substring expected in the error's Field
		why       string
	}{
		{
			name:    "unset",
			channel: agentdb.JSONMap{},
			why:     "a project with no attention channel must still render",
		},
		{
			name:    "env ref url, env ref header",
			channel: agentdb.JSONMap{"kind": "webhook", "url": "${SLACK_WEBHOOK}", "headers": map[string]any{"Authorization": "${SLACK_TOKEN}"}},
			why:     "the shape the design wants operators to use",
		},
		{
			name:      "literal url",
			channel:   agentdb.JSONMap{"kind": "webhook", "url": "https://hooks.slack.com/services/T0/B0/xxxx"},
			wantErr:   true,
			wantField: "AttentionChannel.url",
			why:       "the known blocker in §D: the store still accepts a literal webhook URL, and it is a bearer token",
		},
		{
			name:      "partially interpolated url",
			channel:   agentdb.JSONMap{"kind": "webhook", "url": "https://hooks.slack.com/${SECRET}"},
			wantErr:   true,
			wantField: "AttentionChannel.url",
			why:       "DI1: not a safe reference, and the shape most likely to carry a real token",
		},
		{
			name:      "literal header value",
			channel:   agentdb.JSONMap{"kind": "webhook", "url": "${W}", "headers": map[string]any{"Authorization": "Bearer sk-live-abc"}},
			wantErr:   true,
			wantField: "AttentionChannel.headers.Authorization",
			why:       "the error must name the exact header so an operator can fix it without guessing",
		},
		{
			name:      "partially interpolated header value",
			channel:   agentdb.JSONMap{"kind": "webhook", "url": "${W}", "headers": map[string]any{"Authorization": "Bearer ${TOKEN}"}},
			wantErr:   true,
			wantField: "AttentionChannel.headers.Authorization",
			why:       "cmd/agentd sends this as a literal today; it must never be published",
		},
		{
			name:    "empty url string",
			channel: agentdb.JSONMap{"kind": "webhook", "url": ""},
			why:     "an empty value carries no credential and means 'not set'",
		},
		{
			name:      "headers is not an object",
			channel:   agentdb.JSONMap{"kind": "webhook", "url": "${W}", "headers": "Authorization: Bearer sk-live-abc"},
			wantErr:   true,
			wantField: "AttentionChannel.headers",
			why:       "a blob we cannot walk is a blob we cannot say is safe — this exact shape would otherwise hide a token from the leaf check",
		},
		{
			name:      "url is a number",
			channel:   agentdb.JSONMap{"kind": "webhook", "url": 42},
			wantErr:   true,
			wantField: "AttentionChannel.url",
			why:       "a non-string is not a ${VAR} reference",
		},
		{
			name:    "kind and header names render untouched",
			channel: agentdb.JSONMap{"kind": "webhook", "headers": map[string]any{"X-Some-Literal-Name": "${V}"}},
			why:     "the leaf rule must not overreach: the discriminator and header names are not credentials",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckRenderable(StructProjectSettings, map[string]any{"AttentionChannel": tc.channel})
			assertRefusal(t, err, tc.wantErr, tc.wantField, tc.why)
		})
	}
}

// TestCheckRenderableMCPConfig covers the other leaf-checked blob. Its shape is
// server name → {command, args, env, url, headers}, so the selectors have to
// step over the server name with a wildcard first.
func TestCheckRenderableMCPConfig(t *testing.T) {
	cases := []struct {
		name      string
		cfg       any
		wantErr   bool
		wantField string
		why       string
	}{
		{
			name: "stdio server with env refs",
			cfg: agentdb.JSONMap{"gmail": map[string]any{
				"command": "/usr/bin/mcp-gmail",
				"args":    []any{"--stdio"},
				"env":     map[string]any{"GMAIL_TOKEN": "${GMAIL_TOKEN}"},
			}},
			why: "command and args render as-is; env is checked",
		},
		{
			name: "literal env value",
			cfg: agentdb.JSONMap{"gmail": map[string]any{
				"command": "/usr/bin/mcp-gmail",
				"env":     map[string]any{"GMAIL_TOKEN": "ya29.a0Afreal"},
			}},
			wantErr:   true,
			wantField: "MCPConfig.gmail.env.GMAIL_TOKEN",
			why: "MCPServerConfig.Validate accepts this today — it only refuses values CONTAINING '${' — " +
				"so the store is not the backstop and this check is",
		},
		{
			name:      "literal http url",
			cfg:       agentdb.JSONMap{"zapier": map[string]any{"url": "https://mcp.zapier.com/api/mcp/s/NjM4Zm-secret/mcp"}},
			wantErr:   true,
			wantField: "MCPConfig.zapier.url",
			why:       "hosted MCP endpoints routinely put the credential in the URL path — the same 'the URL is the token' shape as a Slack webhook",
		},
		{
			name:    "env ref http url",
			cfg:     agentdb.JSONMap{"zapier": map[string]any{"url": "${ZAPIER_MCP_URL}"}},
			why:     "the fix an operator applies: move it into an environment variable",
		},
		{
			name:      "literal header value",
			cfg:       agentdb.JSONMap{"api": map[string]any{"url": "${API_URL}", "headers": map[string]any{"Authorization": "Bearer sk-live"}}},
			wantErr:   true,
			wantField: "MCPConfig.api.headers.Authorization",
			why:       "same case as the attention channel's headers",
		},
		{
			name: "several servers, one bad",
			cfg: agentdb.JSONMap{
				"good": map[string]any{"command": "/bin/ok", "env": map[string]any{"A": "${A}"}},
				"bad":  map[string]any{"command": "/bin/no", "env": map[string]any{"B": "literal"}},
			},
			wantErr:   true,
			wantField: "MCPConfig.bad.env.B",
			why:       "one bad server refuses the whole render; §D is refusal, never partial output",
		},
		{
			name:    "empty config",
			cfg:     agentdb.JSONMap{},
			why:     "no servers configured is ordinary",
		},
		{
			name:      "server entry is not an object",
			cfg:       agentdb.JSONMap{"weird": "https://host/secret"},
			wantErr:   true,
			wantField: "MCPConfig.weird",
			why:       "unwalkable shape is refused rather than skipped",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckRenderable(StructWorker, map[string]any{"MCPConfig": tc.cfg})
			assertRefusal(t, err, tc.wantErr, tc.wantField, tc.why)
		})
	}
}

// TestCheckRenderableEmptyIsUnset documents the deliberate decision about empty
// values, which the ticket asked to be settled explicitly: an absent or empty
// value passes, because refusing it would make every project without an
// attention channel unrenderable, and an empty string cannot leak anything.
// The renderer is expected to OMIT the key rather than write `url: ""`.
func TestCheckRenderableEmptyIsUnset(t *testing.T) {
	for name, value := range map[string]any{
		"nil value":         nil,
		"empty jsonmap":     agentdb.JSONMap{},
		"empty string leaf": agentdb.JSONMap{"url": ""},
		"nil leaf":          agentdb.JSONMap{"url": nil},
	} {
		t.Run(name, func(t *testing.T) {
			if err := CheckRenderable(StructProjectSettings, map[string]any{"AttentionChannel": value}); err != nil {
				t.Fatalf("an unset value must render as 'not configured', got: %v", err)
			}
		})
	}
}

// TestCheckRenderableUnknownAndNever covers the two refusals that are not about
// value shape: a field the allowlist has never heard of, and a field it has
// decided against.
func TestCheckRenderableUnknownAndNever(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		err := CheckRenderable(StructWorker, map[string]any{"SlackBotToken": "xoxb-real"})
		assertRefusal(t, err, true, "SlackBotToken",
			"a field with no allowlist entry must refuse, which is the same protection the guard test gives at build time")
		if !strings.Contains(err.Error(), "allowlist.go") {
			t.Errorf("the refusal must tell the next engineer where to record the decision, got: %v", err)
		}
	})

	t.Run("never field", func(t *testing.T) {
		err := CheckRenderable(StructSkill, map[string]any{"OwnerEmail": "kai@example.com"})
		assertRefusal(t, err, true, "OwnerEmail",
			"Never means never: a renderer that passes it anyway has a bug, and silently dropping it would hide that")
	})

	t.Run("unknown struct", func(t *testing.T) {
		// Subscription itself is now allowlist-covered (G19), so this uses a
		// name no table will ever claim to exercise the "no table at all" path.
		err := CheckRenderable("NotARenderedStruct", map[string]any{"ID": "s1"})
		if err == nil {
			t.Fatal("a struct with no allowlist table must refuse, not default to publishing")
		}
		if !errors.Is(err, ErrUnrenderable) {
			t.Errorf("refusals must wrap ErrUnrenderable so callers can match on it, got %T", err)
		}
	})
}

// TestCheckRenderableAcceptsPlainFields is the other half: the rule must not be
// so strict that ordinary configuration stops rendering.
func TestCheckRenderableAcceptsPlainFields(t *testing.T) {
	err := CheckRenderable(StructProjectSettings, map[string]any{
		"BaseImage":         "bob/example:v3",
		"SystemPrompt":      "You are the wolf project.",
		"MaxConcurrentJobs": 4,
		"Briefing":          agentdb.SelectorList{"kind=lesson"},
		"GitRemote":         "https://github.com/binocarlos/wolf",
		"GitTokenEnv":       "WOLF_GITHUB_TOKEN",
	})
	if err != nil {
		t.Fatalf("ordinary settings must render: %v", err)
	}
}

// TestGitTokenEnvHoldsANameNotASecret pins the distinction the whole
// GitTokenEnv decision rests on, because it is the one entry that looks like it
// should have been RenderEnvRefOnly and is deliberately not.
func TestGitTokenEnvHoldsANameNotASecret(t *testing.T) {
	r, ok := RuleFor(StructProjectSettings, "GitTokenEnv")
	if !ok {
		t.Fatal("GitTokenEnv has no allowlist entry")
	}
	if r.Decision != Render {
		t.Fatalf("GitTokenEnv should be Render, got %q. It stores the NAME of an environment variable "+
			"(the api_key_env pattern), never its value — requiring it to be ${VAR} would ask an operator "+
			"to name a variable that names a variable.", r.Decision)
	}
	if !r.NotImportable {
		t.Fatal("GitTokenEnv must be marked NotImportable: a commit that could rewrite it would redirect " +
			"which credential agentd pushes with")
	}
	// The value it holds is a bare identifier, and that must be renderable.
	if err := CheckRenderable(StructProjectSettings, map[string]any{"GitTokenEnv": "WOLF_GITHUB_TOKEN"}); err != nil {
		t.Fatalf("a variable name must render: %v", err)
	}
}

// TestRenderableFieldsExcludesNever checks the helper G4 will iterate.
func TestRenderableFieldsExcludesNever(t *testing.T) {
	got := RenderableFields(StructSkill)
	for _, f := range got {
		if r, _ := RuleFor(StructSkill, f); r.Decision == Never {
			t.Errorf("RenderableFields returned %q, which is Never", f)
		}
	}
	for _, banned := range []string{"OwnerEmail", "PromotedBy", "Manifest", "BlobPrefix", "Customer"} {
		for _, f := range got {
			if f == banned {
				t.Errorf("RenderableFields(Skill) includes %q, which must never be published", banned)
			}
		}
	}
	if len(got) == 0 {
		t.Fatal("RenderableFields(Skill) is empty; nothing would render at all")
	}
}

// TestCheckRenderableSubscriptionScheduleImage exercises the three tables G19
// added, the same way TestCheckRenderableAcceptsPlainFields does for
// ProjectSettings: ordinary configuration renders, and the runtime-state /
// identity fields refuse.
func TestCheckRenderableSubscriptionScheduleImage(t *testing.T) {
	t.Run("subscription renders", func(t *testing.T) {
		err := CheckRenderable(StructSubscription, map[string]any{
			"ID":                "sub-1",
			"EventType":         "memory.appended",
			"Worker":            "email-answerer",
			"MaxFiringsPerHour": 10,
			"Enabled":           true,
		})
		if err != nil {
			t.Fatalf("ordinary subscription fields must render: %v", err)
		}
	})

	t.Run("subscription project is never", func(t *testing.T) {
		err := CheckRenderable(StructSubscription, map[string]any{"Project": "wolf"})
		assertRefusal(t, err, true, "Project", "identity is the clone, not a field")
	})

	t.Run("schedule renders", func(t *testing.T) {
		err := CheckRenderable(StructSchedule, map[string]any{
			"ID":      "sch-1",
			"Worker":  "architect",
			"Cron":    "0 9 * * *",
			"Input":   "run the daily loop",
			"Enabled": true,
		})
		if err != nil {
			t.Fatalf("ordinary schedule fields must render: %v", err)
		}
	})

	t.Run("schedule runtime state is never", func(t *testing.T) {
		for _, f := range []string{"ProvisionFailures", "LastProvisionError", "LastEvaluated"} {
			err := CheckRenderable(StructSchedule, map[string]any{f: "x"})
			assertRefusal(t, err, true, f, "runtime state must never render — it would commit on every tick")
		}
	})

	t.Run("image renders", func(t *testing.T) {
		err := CheckRenderable(StructCustomImage, map[string]any{
			"Name":          "wolf-base",
			"Visibility":    "organizational",
			"Version":       3,
			"RequiresBuild": true,
		})
		if err != nil {
			t.Fatalf("ordinary image fields must render: %v", err)
		}
	})

	t.Run("image internals are never", func(t *testing.T) {
		for _, f := range []string{"OwnerEmail", "RegistryHandle", "SkillSet", "ContentHash", "BaseImageID", "ReapedAt", "ExpiresAt", "LastResumedAt", "Customer", "ID"} {
			err := CheckRenderable(StructCustomImage, map[string]any{f: "x"})
			assertRefusal(t, err, true, f, "internal storage, personal data or reaper bookkeeping must never render")
		}
	})
}

// assertRefusal keeps the table tests honest about *which* leaf refused: an
// error that says "something in the attention channel is wrong" leaves an
// operator hunting, and a test that only checks err != nil would not notice.
func assertRefusal(t *testing.T, err error, wantErr bool, wantField, why string) {
	t.Helper()
	if !wantErr {
		if err != nil {
			t.Fatalf("expected this to render (%s), got refusal: %v", why, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected a refusal (%s), got nil — this value would have been published", why)
	}
	if !errors.Is(err, ErrUnrenderable) {
		t.Fatalf("refusal must wrap ErrUnrenderable, got %T: %v", err, err)
	}
	var ue *UnrenderableError
	if !errors.As(err, &ue) {
		t.Fatalf("refusal must be an *UnrenderableError, got %T", err)
	}
	if wantField != "" && ue.Field != wantField {
		t.Fatalf("refusal named field %q, want %q — the message has to point at the exact leaf (%s)", ue.Field, wantField, why)
	}
	// The refusal must never quote the value it refused: this error reaches
	// logs and the console, and echoing the secret there would be the leak the
	// check exists to prevent.
	for _, secret := range []string{"xoxb-", "sk-live", "ya29.", "NjM4Zm-secret", "T0/B0/xxxx"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the refusal message quotes the offending value (%q appears in %q); "+
				"UnrenderableError must name the path only", secret, err.Error())
		}
	}
}
