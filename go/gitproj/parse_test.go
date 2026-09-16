package gitproj

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// workerFile is what Render is expected to produce for a worker: a
// frontmatter block of the worker's non-prompt fields, then the system
// prompt as the body. Tests build old/new pairs from it so a "change" is
// only ever the thing the test names.
func workerFile(t *testing.T, values map[string]interface{}, body string) []byte {
	t.Helper()
	out, err := RenderFrontmatter(values, body)
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}
	return out
}

func fullWorkerValues() map[string]interface{} {
	return map[string]interface{}{
		"name":        "copywriter",
		"description": "writes the words",
		"enabled":     true,
		"image":       "example",
		"briefing":    []string{"kind=lesson", "kind=rolling-summary,worker=copywriter"},
		"mcp_config":  map[string]interface{}{"servers": map[string]interface{}{"core": "builtin"}},
	}
}

// TestParseAgainstOnlyEnabledChanged is the DI2 regression test, and the
// reason this ticket exists.
//
// `PUT /agent/workers/{name}` wipes any field the caller omits. An importer
// that turned a worker file into a whole Worker struct would therefore strip
// the worker's briefing — and its mcp_config, description and image — every
// time a human toggled `enabled` in git. Fields must name `enabled` and
// nothing else.
func TestParseAgainstOnlyEnabledChanged(t *testing.T) {
	body := "You write short, plain sentences.\n"
	old := workerFile(t, fullWorkerValues(), body)

	next := fullWorkerValues()
	next["enabled"] = false
	edited := workerFile(t, next, body)

	ch, err := ParseAgainst("bob", "bob/workers/copywriter.md", old, edited)
	if err != nil {
		t.Fatalf("ParseAgainst: %v", err)
	}

	if ch.Kind != KindWorker || ch.Name != "copywriter" {
		t.Fatalf("got kind=%q name=%q, want worker/copywriter", ch.Kind, ch.Name)
	}
	want := map[string]interface{}{"enabled": false}
	if !reflect.DeepEqual(ch.Fields, want) {
		t.Fatalf("Fields = %#v, want exactly %#v", ch.Fields, want)
	}
	// Named explicitly, because these are the fields DI2 was found wiping.
	for _, forbidden := range []string{"briefing", "mcp_config", "description", "image", "name"} {
		if _, ok := ch.Fields[forbidden]; ok {
			t.Fatalf("Fields claims %q changed when it did not: %#v", forbidden, ch.Fields)
		}
	}
	if ch.BodyChanged || ch.BodyOnly {
		t.Fatalf("body did not change; got BodyChanged=%v BodyOnly=%v", ch.BodyChanged, ch.BodyOnly)
	}
}

func TestParseAgainstBodyAndFields(t *testing.T) {
	oldValues := fullWorkerValues()
	oldBody := "Original prompt.\n"

	cases := []struct {
		name        string
		values      func(map[string]interface{})
		body        string
		wantFields  map[string]interface{}
		wantBody    bool
		wantOnly    bool
		wantChanged bool
	}{
		{
			name:        "body only",
			body:        "Rewritten prompt, sharper.\n",
			wantFields:  nil,
			wantBody:    true,
			wantOnly:    true,
			wantChanged: true,
		},
		{
			name:        "nothing changed",
			body:        oldBody,
			wantFields:  nil,
			wantBody:    false,
			wantOnly:    false,
			wantChanged: false,
		},
		{
			name:        "field only",
			values:      func(v map[string]interface{}) { v["description"] = "writes the words, well" },
			body:        oldBody,
			wantFields:  map[string]interface{}{"description": "writes the words, well"},
			wantBody:    false,
			wantOnly:    false,
			wantChanged: true,
		},
		{
			name:        "body and field together",
			values:      func(v map[string]interface{}) { v["enabled"] = false },
			body:        "Rewritten prompt, sharper.\n",
			wantFields:  map[string]interface{}{"enabled": false},
			wantBody:    true,
			wantOnly:    false,
			wantChanged: true,
		},
		{
			name:        "list changed",
			values:      func(v map[string]interface{}) { v["briefing"] = []string{"kind=lesson"} },
			body:        oldBody,
			wantFields:  map[string]interface{}{"briefing": []string{"kind=lesson"}},
			wantBody:    false,
			wantOnly:    false,
			wantChanged: true,
		},
		{
			name: "nested map changed",
			values: func(v map[string]interface{}) {
				v["mcp_config"] = map[string]interface{}{"servers": map[string]interface{}{"core": "off"}}
			},
			body:        oldBody,
			wantFields:  map[string]interface{}{"mcp_config": map[string]interface{}{"servers": map[string]interface{}{"core": "off"}}},
			wantBody:    false,
			wantOnly:    false,
			wantChanged: true,
		},
		{
			name:        "field line deleted clears it",
			values:      func(v map[string]interface{}) { delete(v, "description") },
			body:        oldBody,
			wantFields:  map[string]interface{}{"description": nil},
			wantBody:    false,
			wantOnly:    false,
			wantChanged: true,
		},
	}

	old := workerFile(t, oldValues, oldBody)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := fullWorkerValues()
			if tc.values != nil {
				tc.values(values)
			}
			next := workerFile(t, values, tc.body)

			ch, err := ParseAgainst("bob", "bob/workers/copywriter.md", old, next)
			if err != nil {
				t.Fatalf("ParseAgainst: %v", err)
			}
			if !reflect.DeepEqual(ch.Fields, tc.wantFields) {
				t.Fatalf("Fields = %#v, want %#v", ch.Fields, tc.wantFields)
			}
			if ch.BodyChanged != tc.wantBody {
				t.Fatalf("BodyChanged = %v, want %v", ch.BodyChanged, tc.wantBody)
			}
			if ch.BodyOnly != tc.wantOnly {
				t.Fatalf("BodyOnly = %v, want %v", ch.BodyOnly, tc.wantOnly)
			}
			if ch.HasChange() != tc.wantChanged {
				t.Fatalf("HasChange() = %v, want %v", ch.HasChange(), tc.wantChanged)
			}
			if tc.wantBody && ch.Body != tc.body {
				t.Fatalf("Body = %q, want %q", ch.Body, tc.body)
			}
		})
	}
}

// TestParseAgainstFormattingIsNotAChange pins the tolerance the design doc's
// §C requires: the importer reads files hand-edited by humans, and a person
// who reorders keys, quotes a value, reindents a nested map or drops the
// blank line after the fence has changed nothing. Reporting those as changes
// would append meaningless entries to the config log and make the
// import→render loop commit for a no-op.
func TestParseAgainstFormattingIsNotAChange(t *testing.T) {
	old := []byte("---\n" +
		"enabled: true\n" +
		"name: copywriter\n" +
		"port: 8080\n" +
		"tags:\n" +
		"  - a\n" +
		"  - b\n" +
		"nested:\n" +
		"  alpha: 1\n" +
		"  zeta: 2\n" +
		"---\n\n" +
		"Prompt body.\n")

	cases := []struct {
		name string
		next string
	}{
		{
			name: "keys reordered",
			next: "---\nname: copywriter\nport: 8080\ntags:\n  - a\n  - b\nnested:\n  zeta: 2\n  alpha: 1\nenabled: true\n---\n\nPrompt body.\n",
		},
		{
			name: "values quoted",
			next: "---\nenabled: \"true\"\nname: \"copywriter\"\nport: \"8080\"\ntags:\n  - \"a\"\n  - \"b\"\nnested:\n  alpha: 1\n  zeta: 2\n---\n\nPrompt body.\n",
		},
		{
			name: "reindented and flow style",
			next: "---\nenabled: true\nname: copywriter\nport: 8080\ntags: [a, b]\nnested:\n    alpha: 1\n    zeta: 2\n---\n\nPrompt body.\n",
		},
		{
			name: "no blank line after fence",
			next: "---\nenabled: true\nname: copywriter\nport: 8080\ntags:\n  - a\n  - b\nnested:\n  alpha: 1\n  zeta: 2\n---\nPrompt body.\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch, err := ParseAgainst("bob", "bob/workers/copywriter.md", old, []byte(tc.next))
			if err != nil {
				t.Fatalf("ParseAgainst: %v", err)
			}
			if len(ch.Fields) != 0 {
				t.Fatalf("formatting-only edit reported field changes: %#v", ch.Fields)
			}
			if ch.BodyChanged {
				t.Fatalf("formatting-only edit reported a body change")
			}
			if ch.HasChange() {
				t.Fatalf("formatting-only edit reported HasChange() = true")
			}
		})
	}
}

// TestParseAgainstDropsGitFields is the DI3 rule: the five fields that say
// which repo, which branch, which subfolder, which push credential and which
// webhook secret are rendered for a human to read and are never importable.
// Anyone with commit access could otherwise redirect a project's projection at
// a repo they control — or, with the webhook secret, decide which key their own
// forged deliveries are verified against.
func TestParseAgainstDropsGitFields(t *testing.T) {
	oldValues := map[string]interface{}{
		"base_image":             "example",
		"git_remote":             "https://github.com/badcode/wolf",
		"git_branch":             "main",
		"git_subfolder":          "bob",
		"git_token_env":          "WOLF_GITHUB_TOKEN",
		"git_webhook_secret_env": "WOLF_WEBHOOK_SECRET",
	}
	old := workerFile(t, oldValues, "Project prompt.\n")

	nextValues := map[string]interface{}{
		"base_image":             "other",
		"git_remote":             "https://github.com/attacker/exfil",
		"git_branch":             "steal",
		"git_subfolder":          "bob",
		"git_token_env":          "ATTACKER_TOKEN",
		"git_webhook_secret_env": "ATTACKER_WEBHOOK_SECRET",
	}
	next := workerFile(t, nextValues, "Project prompt.\n")

	ch, err := ParseAgainst("bob", "bob/settings.md", old, next)
	if err != nil {
		t.Fatalf("ParseAgainst: %v", err)
	}
	if ch.Kind != KindSettings || ch.Name != "" {
		t.Fatalf("got kind=%q name=%q, want settings with an empty name", ch.Kind, ch.Name)
	}

	want := map[string]interface{}{"base_image": "other"}
	if !reflect.DeepEqual(ch.Fields, want) {
		t.Fatalf("Fields = %#v, want exactly %#v (the git_* fields must never be importable)", ch.Fields, want)
	}

	// This file carries only the five git_* keys, not "connections" (added in
	// project-connections T11 — worker-only), so the expectation here is the
	// git-only subset rather than every not-importable key there is.
	wantDropped := []string{"git_branch", "git_remote", "git_subfolder", "git_token_env", "git_webhook_secret_env"}
	got := append([]string(nil), ch.DroppedFields...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, wantDropped) {
		t.Fatalf("DroppedFields = %v, want %v", got, wantDropped)
	}
}

// TestParseAgainstDropsConnections is TestParseAgainstDropsGitFields's
// counterpart for project-connections T11: a worker file's `connections` key
// renders for a human to read but must never come back in through the import
// door, because a commit that could rewrite it would grant a worker "*" —
// every connection the project has — without going through CanGrant.
func TestParseAgainstDropsConnections(t *testing.T) {
	oldValues := map[string]interface{}{
		"name":        "architect",
		"connections": []string{"github"},
	}
	old := workerFile(t, oldValues, "You are the architect.\n")

	nextValues := map[string]interface{}{
		"name":        "architect",
		"connections": []string{"*"},
	}
	next := workerFile(t, nextValues, "You are the architect.\n")

	ch, err := ParseAgainst("bob", "bob/workers/architect.md", old, next)
	if err != nil {
		t.Fatalf("ParseAgainst: %v", err)
	}
	if ch.Kind != KindWorker || ch.Name != "architect" {
		t.Fatalf("got kind=%q name=%q, want a worker named architect", ch.Kind, ch.Name)
	}
	if _, present := ch.Fields["connections"]; present {
		t.Fatalf("connections came back through the import door: %#v", ch.Fields)
	}
	if len(ch.Fields) != 0 {
		t.Fatalf("only connections changed; want no importable fields, got %#v", ch.Fields)
	}
	if !reflect.DeepEqual(ch.DroppedFields, []string{"connections"}) {
		t.Fatalf("DroppedFields = %v, want [connections]", ch.DroppedFields)
	}
}

func TestNotImportableFields(t *testing.T) {
	want := []string{"connections", "git_branch", "git_remote", "git_subfolder", "git_token_env", "git_webhook_secret_env"}
	if got := NotImportableFields(); !reflect.DeepEqual(got, want) {
		t.Fatalf("NotImportableFields() = %v, want %v", got, want)
	}
}

// TestParseCreate covers the no-previous-version door: every non-empty field
// the file carries is reported, because on a create there is no stored row
// whose omitted fields could be wiped.
func TestParseCreate(t *testing.T) {
	next := workerFile(t, map[string]interface{}{
		"name":        "editor",
		"enabled":     true,
		"description": "",
	}, "Edit ruthlessly.\n")

	ch, err := Parse("bob", "bob/workers/editor.md", next)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]interface{}{"name": "editor", "enabled": true}
	if !reflect.DeepEqual(ch.Fields, want) {
		t.Fatalf("Fields = %#v, want %#v (an empty field is nothing to set)", ch.Fields, want)
	}
	if !ch.BodyChanged {
		t.Fatalf("BodyChanged = %v, want true", ch.BodyChanged)
	}
	if ch.BodyOnly {
		t.Fatalf("BodyOnly = true, but frontmatter fields changed too: %#v", ch.Fields)
	}
	if ch.Body != "Edit ruthlessly.\n" {
		t.Fatalf("Body = %q", ch.Body)
	}
}

func TestParseCreateEmptyBodyIsNotAPromptWrite(t *testing.T) {
	next := workerFile(t, map[string]interface{}{"name": "editor"}, "")
	ch, err := Parse("bob", "bob/workers/editor.md", next)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ch.BodyChanged || ch.BodyOnly {
		t.Fatalf("empty body on create reported as a body change")
	}
}

// TestParseDeletion pins that a deletion comes from the caller saying the
// file is gone, never from an empty file: an empty prompt is a legal, if
// unwise, prompt.
func TestParseDeletion(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		ch, err := ParseDelete("bob", "bob/workers/copywriter.md")
		if err != nil {
			t.Fatalf("ParseDelete: %v", err)
		}
		if !ch.Deleted || ch.Kind != KindWorker || ch.Name != "copywriter" {
			t.Fatalf("got %#v, want a deletion of worker copywriter", ch)
		}
		if len(ch.Fields) != 0 || ch.BodyChanged {
			t.Fatalf("a deletion must carry no fields or body: %#v", ch)
		}
	})

	t.Run("nil content", func(t *testing.T) {
		ch, err := ParseAgainst("bob", "bob/workers/copywriter.md", []byte("---\nname: copywriter\n---\n\nhi\n"), nil)
		if err != nil {
			t.Fatalf("ParseAgainst: %v", err)
		}
		if !ch.Deleted {
			t.Fatalf("nil new content must be a deletion: %#v", ch)
		}
	})

	t.Run("empty file is not a deletion", func(t *testing.T) {
		old := []byte("---\nname: copywriter\n---\n\nthe old prompt\n")
		ch, err := ParseAgainst("bob", "bob/workers/copywriter.md", old, []byte(""))
		if err != nil {
			t.Fatalf("ParseAgainst: %v", err)
		}
		if ch.Deleted {
			t.Fatalf("an empty file must not be read as a deletion")
		}
		if !ch.BodyChanged || ch.Body != "" {
			t.Fatalf("emptying a file is a body change to the empty prompt: %#v", ch)
		}
	})
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name      string
		subfolder string
		path      string
		old       []byte
		next      []byte
		wantIn    []string
	}{
		{
			name:   "malformed frontmatter names the file",
			path:   "bob/workers/copywriter.md",
			next:   []byte("---\nname: copywriter\n  bad: [unclosed\n---\n\nbody\n"),
			wantIn: []string{"bob/workers/copywriter.md", "frontmatter"},
		},
		{
			name:   "unterminated frontmatter names the file",
			path:   "bob/workers/copywriter.md",
			next:   []byte("---\nname: copywriter\n\nbody with no closing fence\n"),
			wantIn: []string{"bob/workers/copywriter.md", "unterminated"},
		},
		{
			name:   "malformed previous version is not treated as a create",
			path:   "bob/workers/copywriter.md",
			old:    []byte("---\n\tname: [unclosed\n---\n\nbody\n"),
			next:   []byte("---\nname: copywriter\n---\n\nbody\n"),
			wantIn: []string{"bob/workers/copywriter.md", "previous version"},
		},
		{
			name:   "path traversal is refused, not guessed at",
			path:   "bob/workers/../../.github/workflows/evil.md",
			next:   []byte("---\n---\n\nbody\n"),
			wantIn: []string{".."},
		},
		{
			name:   "path outside the subfolder is refused",
			path:   ".github/workflows/evil.md",
			next:   []byte("---\n---\n\nbody\n"),
			wantIn: []string{"not under subfolder"},
		},
		{
			name:   "unknown directory is refused",
			path:   "bob/secrets/token.md",
			next:   []byte("---\n---\n\nbody\n"),
			wantIn: []string{"unknown directory"},
		},
		{
			name:   "uppercase name is refused",
			path:   "bob/workers/CopyWriter.md",
			next:   []byte("---\n---\n\nbody\n"),
			wantIn: []string{"invalid name"},
		},
		{
			name:   "frontmatter name disagreeing with the filename is refused",
			path:   "bob/workers/copywriter.md",
			next:   []byte("---\nname: architect\n---\n\nbody\n"),
			wantIn: []string{"bob/workers/copywriter.md", "does not match the filename"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := tc.subfolder
			if sub == "" {
				sub = "bob"
			}
			_, err := ParseAgainst(sub, tc.path, tc.old, tc.next)
			if err == nil {
				t.Fatalf("want an error, got nil")
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// TestParseSubfolderDefault pins DI3's read-time default: an empty subfolder
// means `bob`, never the repository root.
func TestParseSubfolderDefault(t *testing.T) {
	ch, err := ParseAgainst("", "bob/workers/copywriter.md", nil, []byte("---\nname: copywriter\n---\n\nbody\n"))
	if err != nil {
		t.Fatalf("ParseAgainst: %v", err)
	}
	if ch.Kind != KindWorker || ch.Name != "copywriter" {
		t.Fatalf("got %#v", ch)
	}
	if _, err := ParseAgainst("", "workers/copywriter.md", nil, []byte("---\n---\n\nbody\n")); err == nil {
		t.Fatalf("a root-level path must not be accepted when the subfolder defaults to %q", DefaultSubfolder)
	}
}

func TestParsePathKinds(t *testing.T) {
	cases := []struct {
		path string
		kind Kind
		name string
	}{
		{"bob/settings.md", KindSettings, ""},
		{"bob/workers/copywriter.md", KindWorker, "copywriter"},
		{"bob/skills/brand-voice.md", KindSkill, "brand-voice"},
		{"bob/subscriptions/sub-1.md", KindSubscription, "sub-1"},
		{"bob/schedules/daily.md", KindSchedule, "daily"},
		{"bob/images/example.md", KindImage, "example"},
		{"bob/memory/message-board.md", KindMemory, "message-board"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			ch, err := Parse("bob", tc.path, []byte("---\n---\n\n"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if ch.Kind != tc.kind || ch.Name != tc.name {
				t.Fatalf("got kind=%q name=%q, want kind=%q name=%q", ch.Kind, ch.Name, tc.kind, tc.name)
			}
		})
	}
}

func TestSameValue(t *testing.T) {
	cases := []struct {
		name string
		a, b interface{}
		want bool
	}{
		{"identical strings", "x", "x", true},
		{"different strings", "x", "y", false},
		{"bool vs quoted bool", true, "true", true},
		{"int vs quoted int", 8080, "8080", true},
		{"nil vs empty string", nil, "", true},
		{"nil vs empty map", nil, map[string]interface{}{}, true},
		{"nil vs empty list", nil, []string{}, true},
		{"nil vs a value", nil, "x", false},
		{"list order matters", []string{"a", "b"}, []string{"b", "a"}, false},
		{"list across representations", []string{"a", "b"}, []interface{}{"a", "b"}, true},
		{"list length", []string{"a"}, []string{"a", "b"}, false},
		{"nested map key order", map[string]interface{}{"a": 1, "z": 2}, map[string]interface{}{"z": 2, "a": 1}, true},
		{"nested map value", map[string]interface{}{"a": 1}, map[string]interface{}{"a": 2}, false},
		{"nested map extra key", map[string]interface{}{"a": 1}, map[string]interface{}{"a": 1, "b": 2}, false},
		{"map vs scalar", map[string]interface{}{"a": 1}, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameValue(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameValue(%#v, %#v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			if got := sameValue(tc.b, tc.a); got != tc.want {
				t.Fatalf("sameValue is not symmetric: sameValue(%#v, %#v) = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

// TestParseAgainstIsPure guards the property the whole design leans on: the
// same inputs give the same answer, and neither input is mutated. The
// importer parses every changed path before applying any of them (§C,
// parse-all-then-apply), so a Parse that mutated its arguments would corrupt
// a later comparison in the same push.
func TestParseAgainstIsPure(t *testing.T) {
	old := workerFile(t, fullWorkerValues(), "Original.\n")
	values := fullWorkerValues()
	values["enabled"] = false
	next := workerFile(t, values, "Original.\n")

	oldCopy := append([]byte(nil), old...)
	nextCopy := append([]byte(nil), next...)

	first, err := ParseAgainst("bob", "bob/workers/copywriter.md", old, next)
	if err != nil {
		t.Fatalf("ParseAgainst: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := ParseAgainst("bob", "bob/workers/copywriter.md", old, next)
		if err != nil {
			t.Fatalf("ParseAgainst (iter %d): %v", i, err)
		}
		if !reflect.DeepEqual(again, first) {
			t.Fatalf("non-deterministic Change on iteration %d:\n%#v\n%#v", i, first, again)
		}
	}
	if string(old) != string(oldCopy) || string(next) != string(nextCopy) {
		t.Fatalf("ParseAgainst mutated its input")
	}
}
