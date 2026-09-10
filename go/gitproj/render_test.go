package gitproj

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
)

func TestDumpTree(t *testing.T) {
	if os.Getenv("DUMP") == "" {
		t.Skip("set DUMP=1")
	}
	tree, err := RenderTree(goldenState(), "orange")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Printf("===== %s =====\n%s<<<END\n", p, tree[p])
	}
}

// goldenState is one fully-populated project: every entity kind, several
// workers, nested jsonb blobs, label sets and selector lists.
func goldenState() ProjectState {
	return ProjectState{
		Settings: &agentdb.ProjectSettings{
			Project:      "wolf",
			BaseImage:    "ghcr.io/badcode/wolf:3",
			SystemPrompt: "You are the Wolf project.\n\nMeasure everything.",
			MCPConfig: agentdb.JSONMap{
				"slack": map[string]any{
					"command": "/usr/local/bin/slack-mcp",
					"args":    []any{"--stdio", "--verbose"},
					"env":     map[string]any{"SLACK_TOKEN": "${WOLF_SLACK_TOKEN}"},
				},
				"prices": map[string]any{
					"url":     "${WOLF_PRICES_MCP_URL}",
					"headers": map[string]any{"Authorization": "${WOLF_PRICES_KEY}"},
				},
			},
			AttentionChannel: agentdb.JSONMap{
				"kind":    "webhook",
				"url":     "${WOLF_ATTENTION_WEBHOOK}",
				"headers": map[string]any{"Authorization": "${WOLF_ATTENTION_TOKEN}"},
			},
			MaxConcurrentJobs:   4,
			DailyTokensSoft:     1000000,
			DailyTokensHard:     0,
			BriefingMaxBytes:    2048,
			SnapshotTTLDays:     30,
			Briefing:            agentdb.SelectorList{"kind=charter", "kind=label-registry"},
			GitRemote:           "https://github.com/badcode/wolf-org",
			GitBranch:           "",
			GitSubfolder:        "",
			GitTokenEnv:         "WOLF_GITHUB_TOKEN",
			GitWebhookSecretEnv: "WOLF_WEBHOOK_SECRET",
			UpdatedAt:           1757000000,
		},
		Workers: []*agentdb.Worker{
			{
				Project:      "wolf",
				Name:         "architect",
				Description:  "Designs the roster.",
				SystemPrompt: "You are the architect.\n",
				MCPConfig:    agentdb.JSONMap{"core": map[string]any{"command": "core-mcp"}},
				Image:        "wolf-base:7",
				Briefing:     agentdb.SelectorList{"kind=charter"},
				MaxInstances: 1,
				Enabled:      true,
				Frozen:       false,
				CreatedAt:    1756000000,
				UpdatedAt:    1757000000,
			},
			{
				Project:      "wolf",
				Name:         "scorekeeper",
				SystemPrompt: "Score the hypotheses.",
				MaxInstances: 2,
				Enabled:      false,
				Frozen:       true,
			},
		},
		Skills: []*agentdb.Skill{
			{
				ID: "sk-1", Name: "csv-notes", Customer: "wolf", OwnerEmail: "kai@example.com",
				Description: "How to read the series.", Visibility: "organizational",
				Revision: 1, Markdown: "# old revision\n",
			},
			{
				ID: "sk-2", Name: "csv-notes", Customer: "wolf", OwnerEmail: "kai@example.com",
				Description: "How to read the series.", Visibility: "organizational",
				Revision: 2, Markdown: "# csv-notes\n\nRead column 3 first.\n",
				InstallSh:        "apk add --no-cache jq\n",
				Labels:           agentdb.LabelSet{"topic": "data", "audience": "all"},
				RequiresBuild:    true,
				CreatedByWorker:  "architect",
				CreatedBySession: "sess-9",
			},
		},
		Subscriptions: []*agentdb.Subscription{
			{
				ID: "sub-1", Project: "wolf", EventType: "architect.run",
				Filter: agentdb.JSONMap{"worker": "architect"}, Worker: "architect",
				MaxFiringsPerHour: 0, Enabled: true, CreatedAt: 1756000000,
			},
		},
		Schedules: []*agentdb.Schedule{
			{
				ID: "sched-1", Project: "wolf", Worker: "architect", Cron: "0 7 * * *",
				Input: "Review the roster.", Enabled: true,
				ProvisionFailures: 3, LastProvisionError: "port pool exhausted",
				LastEvaluated: "2026-09-09T07:00", CreatedAt: 1756000000,
			},
		},
		Images: []*agentdb.CustomImage{
			{
				ID: "img-1", Name: "wolf-base", Customer: "wolf", OwnerEmail: "kai@example.com",
				Description: "Base for wolf workers.", Visibility: "organizational",
				Version: 6, ContentHash: "deadbeef", RegistryHandle: `{"ref":"x"}`,
				CreatedAt: 1756000000,
			},
			{
				ID: "img-2", Name: "wolf-base", Customer: "wolf", OwnerEmail: "kai@example.com",
				Description: "Base for wolf workers.", Visibility: "organizational",
				Version: 7, RequiresBuild: true, BaseInstallation: "core",
				Focus: "Trading research.", Labels: agentdb.LabelSet{"kind": "base"},
				CreatedByWorker: "architect", CreatedBySession: "sess-9",
				CreatedAt: 1757000000, ExpiresAt: 1780000000,
			},
		},
		Documents: []*agentdb.Memory{
			{
				ID: "mem-1", Project: "wolf", Content: "older board",
				Labels:    agentdb.LabelSet{"name": "message-board", "kind": "board"},
				CreatedAt: 1756000000,
			},
			{
				ID: "mem-2", Project: "wolf", Content: "The board says: hold.\n",
				Labels:    agentdb.LabelSet{"name": "message-board", "kind": "board"},
				CreatedAt: 1757000000,
			},
		},
	}
}

func mustRender(t *testing.T, st ProjectState, subfolder string) map[string][]byte {
	t.Helper()
	tree, err := RenderTree(st, subfolder)
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	return tree
}

func fileOf(t *testing.T, tree map[string][]byte, path string) string {
	t.Helper()
	b, ok := tree[path]
	if !ok {
		t.Fatalf("no file rendered at %q; got %v", path, sortedPaths(tree))
	}
	return string(b)
}

func sortedPaths(tree map[string][]byte) []string {
	out := make([]string, 0, len(tree))
	for p := range tree {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Determinism. This is the property everything else rests on, so it is tested
// first and hardest: the same state rendered 100 times must produce identical
// bytes, and rendering the same project with its slices in a different order
// must too.
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderTreeIsDeterministic(t *testing.T) {
	first := mustRender(t, goldenState(), "orange")

	for i := 0; i < 100; i++ {
		got := mustRender(t, goldenState(), "orange")
		if len(got) != len(first) {
			t.Fatalf("iteration %d: rendered %d files, first render had %d", i, len(got), len(first))
		}
		for path, want := range first {
			have, ok := got[path]
			if !ok {
				t.Fatalf("iteration %d: %q missing", i, path)
			}
			if string(have) != string(want) {
				t.Fatalf("iteration %d: %q differs from the first render.\n--- first ---\n%s\n--- now ---\n%s",
					i, path, want, have)
			}
		}
	}
}

func TestRenderTreeIgnoresInputOrder(t *testing.T) {
	a := goldenState()
	b := goldenState()
	// Reverse every slice: the caller's collection order must not reach the
	// output.
	reverseWorkers(b.Workers)
	reverseSkills(b.Skills)
	reverseImages(b.Images)
	reverseDocs(b.Documents)

	ta := mustRender(t, a, "orange")
	tb := mustRender(t, b, "orange")

	if strings.Join(sortedPaths(ta), ",") != strings.Join(sortedPaths(tb), ",") {
		t.Fatalf("path sets differ:\n%v\n%v", sortedPaths(ta), sortedPaths(tb))
	}
	for p, want := range ta {
		if string(tb[p]) != string(want) {
			t.Errorf("%s differs when the input slices are reversed:\n--- a ---\n%s\n--- b ---\n%s", p, want, tb[p])
		}
	}
}

func reverseWorkers(s []*agentdb.Worker) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
func reverseSkills(s []*agentdb.Skill) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
func reverseImages(s []*agentdb.CustomImage) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
func reverseDocs(s []*agentdb.Memory) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Golden files. These are the exact bytes a fully-populated project publishes;
// they are inline rather than in testdata/ because this ticket owns two files.
// A diff here is a change to what the world sees, and should be read as one.
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderTreeGoldenProject(t *testing.T) {
	tree := mustRender(t, goldenState(), "orange")

	wantPaths := []string{
		"orange/README.md",
		"orange/images/wolf-base.md",
		"orange/memory/message-board.md",
		"orange/schedules/sched-1.md",
		"orange/settings.md",
		"orange/skills/csv-notes.md",
		"orange/subscriptions/sub-1.md",
		"orange/workers/architect.md",
		"orange/workers/scorekeeper.md",
	}
	if got := sortedPaths(tree); strings.Join(got, "\n") != strings.Join(wantPaths, "\n") {
		t.Fatalf("rendered path set:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantPaths, "\n"))
	}

	for path, want := range goldenFiles {
		if got := fileOf(t, tree, path); got != want {
			t.Errorf("%s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
		}
	}
}

var goldenFiles = map[string]string{
	"orange/settings.md":             goldenSettings,
	"orange/workers/architect.md":    goldenWorkerArchitect,
	"orange/workers/scorekeeper.md":  goldenWorkerScorekeeper,
	"orange/skills/csv-notes.md":     goldenSkill,
	"orange/subscriptions/sub-1.md":  goldenSubscription,
	"orange/schedules/sched-1.md":    goldenSchedule,
	"orange/images/wolf-base.md":     goldenImage,
	"orange/memory/message-board.md": goldenMemory,
}

// The project prompt is the body; every renderable setting is frontmatter, in
// sorted key order. git_branch shows DI3's read-time default (the column is
// empty) and git_subfolder shows the subfolder actually in use.
const goldenSettings = `---
attention_channel:
  headers:
    Authorization: ${WOLF_ATTENTION_TOKEN}
  kind: webhook
  url: ${WOLF_ATTENTION_WEBHOOK}
base_image: ghcr.io/badcode/wolf:3
briefing:
  - kind=charter
  - kind=label-registry
briefing_max_bytes: 2048
daily_tokens_hard: 0
daily_tokens_soft: 1000000
git_branch: main
git_remote: https://github.com/badcode/wolf-org
git_subfolder: orange
git_token_env: WOLF_GITHUB_TOKEN
git_webhook_secret_env: WOLF_WEBHOOK_SECRET
max_concurrent_jobs: 4
mcp_config:
  prices:
    headers:
      Authorization: ${WOLF_PRICES_KEY}
    url: ${WOLF_PRICES_MCP_URL}
  slack:
    args:
      - --stdio
      - --verbose
    command: /usr/local/bin/slack-mcp
    env:
      SLACK_TOKEN: ${WOLF_SLACK_TOKEN}
snapshot_ttl_days: 30
---

You are the Wolf project.

Measure everything.
`

const goldenWorkerArchitect = `---
briefing:
  - kind=charter
description: Designs the roster.
enabled: true
frozen: false
image: wolf-base:7
max_instances: 1
mcp_config:
  core:
    command: core-mcp
name: architect
---

You are the architect.
`

// A worker with nothing optional set: the empty keys are absent entirely, and
// the two false-by-default flags are still written, because false is a
// configuration and absence would read as "take the default".
const goldenWorkerScorekeeper = `---
enabled: false
frozen: true
max_instances: 2
name: scorekeeper
---

Score the hypotheses.
`

const goldenSkill = `---
created_by_session: sess-9
created_by_worker: architect
description: How to read the series.
install_sh: |
  apk add --no-cache jq
labels:
  audience: all
  topic: data
name: csv-notes
requires_build: true
revision: 2
visibility: organizational
---

# csv-notes

Read column 3 first.
`

const goldenSubscription = `---
enabled: true
event_type: architect.run
filter:
  worker: architect
id: sub-1
max_firings_per_hour: 0
worker: architect
---

`

// Note what is NOT here: provision_failures, last_provision_error and
// last_evaluated. They are runtime state — last_evaluated is rewritten by every
// scheduler tick — and rendering them would commit once a minute forever.
const goldenSchedule = `---
cron: 0 7 * * *
enabled: true
id: sched-1
input: Review the roster.
worker: architect
---

`

const goldenImage = `---
base_installation: core
created_by_session: sess-9
created_by_worker: architect
description: Base for wolf workers.
focus: Trading research.
labels:
  kind: base
name: wolf-base
requires_build: true
version: 7
visibility: organizational
---

`

const goldenMemory = `---
labels:
  kind: board
  name: message-board
---

The board says: hold.
`

// ─────────────────────────────────────────────────────────────────────────────
// §D — refusal, not redaction, and never a partial tree.
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderTreeRefusesLiteralAttentionURL(t *testing.T) {
	const secret = "https://hooks.slack.com/services/T00/B00/XXXXnotarealtoken"

	st := goldenState()
	st.Settings.AttentionChannel = agentdb.JSONMap{"kind": "webhook", "url": secret}

	tree, err := RenderTree(st, "orange")
	if err == nil {
		t.Fatalf("a literal Slack webhook URL rendered without complaint; that URL is a bearer token")
	}
	if tree != nil {
		t.Fatalf("a refusal returned %d files; §D requires all-or-nothing, a half-published project is the failure this rule exists to stop", len(tree))
	}
	if !errors.Is(err, ErrUnrenderable) {
		t.Fatalf("error does not wrap ErrUnrenderable: %v", err)
	}
	var ue *UnrenderableError
	if !errors.As(err, &ue) {
		t.Fatalf("error is not an *UnrenderableError: %v", err)
	}
	if ue.Struct != StructProjectSettings || ue.Field != "AttentionChannel.url" {
		t.Errorf("refusal names %s.%s; want ProjectSettings.AttentionChannel.url", ue.Struct, ue.Field)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "hooks.slack.com") {
		t.Errorf("the refusal quotes the secret it refused to publish: %v", err)
	}
}

func TestRenderTreeRefusesLiteralMCPCredential(t *testing.T) {
	cases := map[string]agentdb.JSONMap{
		"literal env value": {
			"slack": map[string]any{"command": "x", "env": map[string]any{"SLACK_TOKEN": "xoxb-1-real"}},
		},
		"partial interpolation in a header": {
			"prices": map[string]any{"headers": map[string]any{"Authorization": "Bearer ${TOKEN}"}},
		},
		"literal hosted url": {
			"zapier": map[string]any{"url": "https://mcp.zapier.com/api/mcp/s/deadbeef/mcp"},
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			st := goldenState()
			st.Settings.MCPConfig = cfg
			tree, err := RenderTree(st, "orange")
			if err == nil || tree != nil {
				t.Fatalf("rendered %d files instead of refusing (err=%v)", len(tree), err)
			}
			if !errors.Is(err, ErrUnrenderable) {
				t.Fatalf("want ErrUnrenderable, got %v", err)
			}
		})
	}
}

func TestRenderTreeRefusesLiteralOnAWorkerToo(t *testing.T) {
	st := goldenState()
	st.Workers[1].MCPConfig = agentdb.JSONMap{
		"x": map[string]any{"env": map[string]any{"K": "literal-secret"}},
	}
	tree, err := RenderTree(st, "orange")
	if err == nil || tree != nil {
		t.Fatalf("rendered %d files instead of refusing (err=%v)", len(tree), err)
	}
	var ue *UnrenderableError
	if !errors.As(err, &ue) || ue.Struct != StructWorker {
		t.Fatalf("want a Worker refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "literal-secret") {
		t.Errorf("refusal quotes the value: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DI3 — the subfolder. An empty one reaching path construction would render at
// the repository root, over the project's own files and next to .github/.
// ─────────────────────────────────────────────────────────────────────────────

func TestResolveSubfolder(t *testing.T) {
	tests := []struct {
		name                       string
		explicit, settings, defalt string
		want                       string
		wantErr                    string
	}{
		{name: "explicit wins", explicit: "conf", settings: "orange", defalt: "orange", want: "conf"},
		{name: "settings when no explicit", settings: "conf", defalt: "orange", want: "conf"},
		{name: "default when neither", defalt: "orange", want: "orange"},
		{name: "empty after defaulting is refused", wantErr: "empty after defaulting"},
		{name: "whitespace is not a segment", explicit: " ", defalt: "orange", wantErr: "invalid git subfolder"},
		{name: "traversal is refused", explicit: "../..", defalt: "orange", wantErr: "invalid git subfolder"},
		{name: "nested path is refused", explicit: "a/b", defalt: "orange", wantErr: "invalid git subfolder"},
		{name: "dotfile is refused", explicit: ".github", defalt: "orange", wantErr: "invalid git subfolder"},
		{name: "uppercase is refused", explicit: "Orange", defalt: "orange", wantErr: "invalid git subfolder"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSubfolder(tc.explicit, tc.settings, tc.defalt)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("got %q, want an error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderTreeSubfolderDefaultsAndRefusals(t *testing.T) {
	// Empty argument and empty column: the default applies and nothing lands
	// at the repository root.
	st := goldenState()
	st.Settings.GitSubfolder = ""
	tree := mustRender(t, st, "")
	for _, p := range sortedPaths(tree) {
		if !strings.HasPrefix(p, agentdb.DefaultGitSubfolder+"/") {
			t.Fatalf("%q is not under the default subfolder", p)
		}
	}

	// The settings column is used when the argument is empty.
	st2 := goldenState()
	st2.Settings.GitSubfolder = "conf"
	tree2 := mustRender(t, st2, "")
	if _, ok := tree2["conf/settings.md"]; !ok {
		t.Fatalf("settings did not render under the configured subfolder: %v", sortedPaths(tree2))
	}

	// A subfolder that is not a single safe segment is refused, with no tree.
	for _, bad := range []string{"../../.github", "a/b", ".github", "Orange", " "} {
		tree3, err := RenderTree(goldenState(), bad)
		if err == nil || tree3 != nil {
			t.Errorf("subfolder %q rendered %d files instead of being refused", bad, len(tree3))
		}
	}
}

func TestRenderTreeAppliesGitBranchDefault(t *testing.T) {
	st := goldenState()
	st.Settings.GitBranch = ""
	st.Settings.GitSubfolder = ""
	got := fileOf(t, mustRender(t, st, "conf"), "conf/settings.md")
	if !strings.Contains(got, "git_branch: "+agentdb.DefaultGitBranch+"\n") {
		t.Errorf("DI3: the empty git_branch column must render as the effective default:\n%s", got)
	}
	// The effective subfolder is the one actually in use, not the empty column.
	if !strings.Contains(got, "git_subfolder: conf\n") {
		t.Errorf("git_subfolder should render the subfolder actually in use:\n%s", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Round-trip with Parse: the loop-termination property, proven rather than
// assumed. Rendering a state and parsing each file against itself must report
// no change — otherwise import → render → import would never settle.
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderTreeRoundTripsThroughParse(t *testing.T) {
	tree := mustRender(t, goldenState(), "orange")

	for _, path := range sortedPaths(tree) {
		content := tree[path]

		// 🔴 README.md is the one rendered file ParsePath does not recognise:
		// it is not one of the entity shapes, so ParsePath errors on it. That
		// is a real gap for G11 (the importer), which must skip the generated
		// README explicitly rather than quarantining a whole push because of
		// it. Reported; not fixable from this ticket's two files.
		if strings.HasSuffix(path, "/README.md") {
			if _, _, err := ParsePath("orange", path); err == nil {
				t.Errorf("ParsePath now accepts %s — update this test and the importer note", path)
			}
			continue
		}

		ch, err := ParseAgainst("orange", path, content, content)
		if err != nil {
			t.Fatalf("%s: rendered file does not parse: %v", path, err)
		}
		if ch.HasChange() {
			t.Errorf("%s: parsing a rendered file against itself reports a change (fields=%v bodyChanged=%v); the import→render loop would never terminate",
				path, ch.Fields, ch.BodyChanged)
		}
		if len(ch.DroppedFields) > 0 && path != "orange/settings.md" {
			t.Errorf("%s: only settings.md may carry not-importable keys, got %v", path, ch.DroppedFields)
		}
	}
}

// TestRenderTreeAndParseAgreeOnOneChangedField is the round-trip that has
// teeth. Parsing a file against ITSELF cannot report a change whatever the
// renderer does, so it only proves the paths and the format are readable. This
// one renders two states that differ in exactly one field and asserts Parse
// sees exactly that field and nothing else: the renderer's key names, the
// parser's key names and DI2's "only what changed" all have to agree, end to
// end, or it fails.
func TestRenderTreeAndParseAgreeOnOneChangedField(t *testing.T) {
	before := mustRender(t, goldenState(), "orange")

	changed := goldenState()
	changed.Workers[0].Enabled = false
	after := mustRender(t, changed, "orange")

	const path = "orange/workers/architect.md"
	ch, err := ParseAgainst("orange", path, before[path], after[path])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ch.BodyChanged {
		t.Errorf("the prompt did not change but the body was reported as changed")
	}
	if len(ch.Fields) != 1 {
		t.Fatalf("one field changed, Parse reported %v", ch.Fields)
	}
	if v, ok := ch.Fields["enabled"]; !ok || v != false {
		t.Fatalf("Parse reported %v, want enabled=false", ch.Fields)
	}
	// The DI2 defence, seen from the renderer's side: a worker's briefing must
	// not appear merely because the file also carries it.
	for _, unchanged := range []string{"briefing", "mcp_config", "description", "image"} {
		if _, present := ch.Fields[unchanged]; present {
			t.Errorf("%q is unchanged but was reported; an importer overlaying this map would rewrite it", unchanged)
		}
	}
}

// TestRenderTreeAndParseAgreeOnAPromptEdit is the same proof for the body: a
// prompt-only change must reach the importer as BodyOnly, which is what sends
// it down the worker_prompt_write path rather than a whole-object write.
func TestRenderTreeAndParseAgreeOnAPromptEdit(t *testing.T) {
	before := mustRender(t, goldenState(), "orange")

	changed := goldenState()
	changed.Workers[0].SystemPrompt = "You are the architect. Be brief.\n"
	after := mustRender(t, changed, "orange")

	const path = "orange/workers/architect.md"
	ch, err := ParseAgainst("orange", path, before[path], after[path])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ch.BodyOnly {
		t.Fatalf("a prompt-only edit must parse as BodyOnly; got fields=%v bodyChanged=%v", ch.Fields, ch.BodyChanged)
	}
	if ch.Body != "You are the architect. Be brief.\n" {
		t.Errorf("body is %q", ch.Body)
	}
}

func TestRenderTreeRoundTripSurvivesReformatting(t *testing.T) {
	// A human reformats a file — reorders keys, requotes a scalar, changes the
	// indent — without changing anything. The importer must see no change.
	tree := mustRender(t, goldenState(), "orange")
	rendered := fileOf(t, tree, "orange/workers/scorekeeper.md")

	reformatted := "---\n" +
		"enabled: \"false\"\n" +
		"frozen: true\n" +
		"max_instances: 2\n" +
		"name: scorekeeper\n" +
		"---\n\n" +
		"Score the hypotheses.\n"

	ch, err := ParseAgainst("orange", "orange/workers/scorekeeper.md", []byte(rendered), []byte(reformatted))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ch.HasChange() {
		t.Errorf("a pure reformat reported a change: fields=%v bodyChanged=%v", ch.Fields, ch.BodyChanged)
	}
}

func TestRenderTreeCreateParseSeesTheWholeFile(t *testing.T) {
	// The bootstrap case (G15): no previous version, so every field the file
	// carries is reported — and the not-importable git fields never are.
	tree := mustRender(t, goldenState(), "orange")
	ch, err := Parse("orange", "orange/settings.md", tree["orange/settings.md"])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range NotImportableFields() {
		if _, present := ch.Fields[k]; present {
			t.Errorf("%s came back through the import door; it must be dropped", k)
		}
	}
	if len(ch.DroppedFields) != len(NotImportableFields()) {
		t.Errorf("settings.md should render all %d not-importable fields (got %v)", len(NotImportableFields()), ch.DroppedFields)
	}
	if !ch.BodyChanged || !strings.HasPrefix(ch.Body, "You are the Wolf project.") {
		t.Errorf("the project prompt must be the body, got %q", ch.Body)
	}
	for _, want := range []string{"base_image", "mcp_config", "attention_channel", "briefing"} {
		if _, ok := ch.Fields[want]; !ok {
			t.Errorf("field %q missing from the parsed create: %v", want, ch.Fields)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Layout, bodies, and omission of unset keys.
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderTreeBodyIsTheProse(t *testing.T) {
	tree := mustRender(t, goldenState(), "orange")

	worker := fileOf(t, tree, "orange/workers/architect.md")
	if strings.Contains(worker, "system_prompt:") {
		t.Errorf("a worker's prompt must be the body, not a frontmatter key:\n%s", worker)
	}
	if !strings.HasSuffix(worker, "You are the architect.\n") {
		t.Errorf("worker body is not the system prompt:\n%s", worker)
	}

	settings := fileOf(t, tree, "orange/settings.md")
	if strings.Contains(settings, "system_prompt:") {
		t.Errorf("the project prompt must be the body:\n%s", settings)
	}

	skill := fileOf(t, tree, "orange/skills/csv-notes.md")
	if strings.Contains(skill, "markdown:") {
		t.Errorf("a skill's document must be the body:\n%s", skill)
	}
	if !strings.HasSuffix(skill, "Read column 3 first.\n") {
		t.Errorf("skill body is not its markdown:\n%s", skill)
	}

	memory := fileOf(t, tree, "orange/memory/message-board.md")
	if !strings.HasSuffix(memory, "The board says: hold.\n") {
		t.Errorf("a memory document's body must be its content:\n%s", memory)
	}
	if !strings.Contains(memory, "name: message-board") {
		t.Errorf("a memory document's labels must be its frontmatter:\n%s", memory)
	}
}

func TestRenderTreeBodyGetsExactlyOneTrailingNewline(t *testing.T) {
	st := ProjectState{Workers: []*agentdb.Worker{
		{Name: "a", SystemPrompt: "no newline", Enabled: true},
		{Name: "b", SystemPrompt: "one newline\n", Enabled: true},
	}}
	tree := mustRender(t, st, "orange")
	for name, want := range map[string]string{"a": "no newline\n", "b": "one newline\n"} {
		_, body, err := ParseFrontmatter([]byte(fileOf(t, tree, "orange/workers/"+name+".md")))
		if err != nil {
			t.Fatalf("worker %s: %v", name, err)
		}
		if body != want {
			t.Errorf("worker %s body is %q, want %q", name, body, want)
		}
	}
}

func TestRenderTreeOmitsUnsetKeysAndKeepsMeaningfulZeros(t *testing.T) {
	st := ProjectState{Workers: []*agentdb.Worker{{
		Project: "wolf", Name: "bare", Enabled: false, MaxInstances: 0,
	}}}
	got := fileOf(t, mustRender(t, st, "orange"), "orange/workers/bare.md")

	for _, absent := range []string{"description:", "image:", "briefing:", "mcp_config:", "system_prompt:"} {
		if strings.Contains(got, absent) {
			t.Errorf("unset optional key %q was written as an empty value:\n%s", absent, got)
		}
	}
	// A false flag and a zero number are configuration, not absence: omitting
	// them would publish a file that reads as "take the default".
	for _, present := range []string{"enabled: false", "frozen: false", "max_instances: 0", "name: bare"} {
		if !strings.Contains(got, present) {
			t.Errorf("expected %q in:\n%s", present, got)
		}
	}
}

func TestRenderTreeNeverRendersNeverFields(t *testing.T) {
	tree := mustRender(t, goldenState(), "orange")
	// Timestamps churn on every write and would put a diff in every commit;
	// project/customer is identity; owner_email is personal data.
	banned := []string{"updated_at", "created_at", "project:", "customer", "owner_email", "kai@example.com",
		"content_hash", "registry_handle", "blob_prefix", "promoted_by", "manifest",
		"last_evaluated", "provision_failures", "last_provision_error"}
	for _, path := range sortedPaths(tree) {
		body := string(tree[path])
		for _, b := range banned {
			if strings.Contains(body, b) {
				t.Errorf("%s publishes %q, which no allowlist entry permits:\n%s", path, b, body)
			}
		}
	}
}

func TestRenderTreeReadme(t *testing.T) {
	got := fileOf(t, mustRender(t, goldenState(), "orange"), "orange/README.md")
	for _, want := range []string{"written by Agent Orange", "applied back into the system", "ignored on import"} {
		if !strings.Contains(got, want) {
			t.Errorf("README does not say %q:\n%s", want, got)
		}
	}
	for _, f := range NotImportableFields() {
		if !strings.Contains(got, "`"+f+"`") {
			t.Errorf("README does not name the ignored field %s:\n%s", f, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Newest-per-name reduction, and the refusals around it.
// ─────────────────────────────────────────────────────────────────────────────

func TestRenderTreeRendersNewestPerName(t *testing.T) {
	tree := mustRender(t, goldenState(), "orange")

	skill := fileOf(t, tree, "orange/skills/csv-notes.md")
	if !strings.Contains(skill, "revision: 2") || strings.Contains(skill, "old revision") {
		t.Errorf("the newest skill revision must win:\n%s", skill)
	}
	image := fileOf(t, tree, "orange/images/wolf-base.md")
	if !strings.Contains(image, "version: 7") {
		t.Errorf("the newest image version must win:\n%s", image)
	}
	memory := fileOf(t, tree, "orange/memory/message-board.md")
	if strings.Contains(memory, "older board") {
		t.Errorf("the newest named memory must win:\n%s", memory)
	}
}

func TestRenderTreeRefusesMemoryWithoutNameLabel(t *testing.T) {
	st := ProjectState{Documents: []*agentdb.Memory{
		{ID: "mem-x", Content: "a lesson", Labels: agentdb.LabelSet{"kind": "lesson"}},
	}}
	tree, err := RenderTree(st, "orange")
	if err == nil || tree != nil {
		t.Fatalf("rendered %d files for a memory with no name= label (err=%v)", len(tree), err)
	}
	if !strings.Contains(err.Error(), "mem-x") || !strings.Contains(err.Error(), "name=") {
		t.Errorf("the error should name the memory and the missing label: %v", err)
	}
}

func TestRenderTreeRefusesUnsafeNames(t *testing.T) {
	cases := map[string]ProjectState{
		"worker":   {Workers: []*agentdb.Worker{{Name: "../../.github/workflows/x", Enabled: true}}},
		"skill":    {Skills: []*agentdb.Skill{{ID: "s", Name: "Not A Name", Revision: 1}}},
		"memory":   {Documents: []*agentdb.Memory{{ID: "m", Labels: agentdb.LabelSet{"name": "../escape"}}}},
		"schedule": {Schedules: []*agentdb.Schedule{{ID: "..", Worker: "w", Cron: "* * * * *"}}},
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			tree, err := RenderTree(st, "orange")
			if err == nil || tree != nil {
				t.Fatalf("rendered %d files for an unsafe %s name (err=%v)", len(tree), name, err)
			}
		})
	}
}

func TestRenderTreeRefusesDuplicatePaths(t *testing.T) {
	st := ProjectState{Workers: []*agentdb.Worker{
		{Name: "twin", SystemPrompt: "one", Enabled: true},
		{Name: "twin", SystemPrompt: "two", Enabled: true},
	}}
	tree, err := RenderTree(st, "orange")
	if err == nil || tree != nil {
		t.Fatalf("two workers with one name rendered %d files (err=%v)", len(tree), err)
	}
	if !strings.Contains(err.Error(), "orange/workers/twin.md") {
		t.Errorf("the error should name the contested path: %v", err)
	}
}

func TestRenderTreeNilSettingsRendersNoSettingsFile(t *testing.T) {
	tree := mustRender(t, ProjectState{Workers: []*agentdb.Worker{{Name: "a", Enabled: true}}}, "orange")
	if _, ok := tree["orange/settings.md"]; ok {
		t.Errorf("a project with no settings row must not render settings.md")
	}
	if _, ok := tree["orange/README.md"]; !ok {
		t.Errorf("the README always renders")
	}
}

func TestRenderTreeRefusesNilRows(t *testing.T) {
	if _, err := RenderTree(ProjectState{Workers: []*agentdb.Worker{nil}}, "orange"); err == nil {
		t.Errorf("a nil worker must be an error, not a skipped row")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Value conversion: the narrow set of types the frontmatter writer accepts, and
// the two things it cannot express.
// ─────────────────────────────────────────────────────────────────────────────

func TestFrontmatterValueConversions(t *testing.T) {
	got, err := frontmatterValue(agentdb.SelectorList{"a=b", "c=d"})
	if err != nil {
		t.Fatalf("SelectorList: %v", err)
	}
	if _, ok := got.([]string); !ok {
		t.Errorf("a named []string type must convert to []string, got %T", got)
	}

	got, err = frontmatterValue(agentdb.LabelSet{"k": "v"})
	if err != nil {
		t.Fatalf("LabelSet: %v", err)
	}
	m, ok := got.(map[string]interface{})
	if !ok || m["k"] != "v" {
		t.Errorf("a named map[string]string must convert to map[string]interface{}, got %#v", got)
	}

	// jsonb numbers arrive as float64.
	got, err = frontmatterValue(agentdb.JSONMap{"n": float64(3)})
	if err != nil {
		t.Fatalf("integral float: %v", err)
	}
	if m := got.(map[string]interface{}); m["n"] != 3 {
		t.Errorf("an integral float64 should render as an int, got %#v", m["n"])
	}

	// int64 is the type of DailyTokensSoft; the frontmatter writer takes int.
	if _, err := frontmatterValue(int64(7)); err != nil {
		t.Errorf("int64: %v", err)
	}

	for name, v := range map[string]any{
		"non-integer number":  agentdb.JSONMap{"n": 1.5},
		"null inside a blob":  agentdb.JSONMap{"n": nil},
		"list of non-strings": agentdb.JSONMap{"n": []any{1, 2}},
	} {
		if _, err := frontmatterValue(v); err == nil {
			t.Errorf("%s should be refused, not guessed at", name)
		}
	}
}
