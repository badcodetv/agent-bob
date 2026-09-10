package gitproj

import (
	"strings"
	"testing"
)

func TestRenderFrontmatterDeterministic(t *testing.T) {
	values := map[string]interface{}{
		"zeta":  1,
		"alpha": "x",
		"beta": map[string]interface{}{
			"z": 1,
			"a": 2,
		},
		"flag": true,
		"list": []string{"b", "a"},
	}

	first, err := RenderFrontmatter(values, "body text\n")
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := RenderFrontmatter(values, "body text\n")
		if err != nil {
			t.Fatalf("RenderFrontmatter (iter %d): %v", i, err)
		}
		if string(again) != string(first) {
			t.Fatalf("non-deterministic output on iteration %d:\n--- first ---\n%s\n--- again ---\n%s", i, first, again)
		}
	}
}

func TestRenderFrontmatterKeyOrder(t *testing.T) {
	values := map[string]interface{}{"zeta": "z", "alpha": "a", "mid": "m"}
	out, err := RenderFrontmatter(values, "hi\n")
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}
	text := string(out)
	ai := strings.Index(text, "alpha:")
	mi := strings.Index(text, "mid:")
	zi := strings.Index(text, "zeta:")
	if !(ai < mi && mi < zi) {
		t.Fatalf("keys not in alphabetical order:\n%s", text)
	}
}

func TestRenderFrontmatterFormat(t *testing.T) {
	out, err := RenderFrontmatter(map[string]interface{}{"name": "copywriter"}, "system prompt body\n")
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}
	want := "---\nname: copywriter\n---\n\nsystem prompt body\n"
	if string(out) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

func TestRenderFrontmatterEmptyValues(t *testing.T) {
	out, err := RenderFrontmatter(map[string]interface{}{}, "just a body\n")
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}
	want := "---\n{}\n---\n\njust a body\n"
	if string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderFrontmatterUnsupportedType(t *testing.T) {
	_, err := RenderFrontmatter(map[string]interface{}{"bad": 3.14}, "")
	if err == nil {
		t.Fatalf("want error for unsupported type float64, got nil")
	}
}

func TestParseFrontmatterRoundTrip(t *testing.T) {
	values := map[string]interface{}{
		"name":   "copywriter",
		"labels": []string{"team-marketing", "role-writer"},
		"mcp_config": map[string]interface{}{
			"transport": "stdio",
			"enabled":   true,
		},
		"revision": 4,
	}
	body := "You write marketing copy.\n\nBe punchy.\n"

	rendered, err := RenderFrontmatter(values, body)
	if err != nil {
		t.Fatalf("RenderFrontmatter: %v", err)
	}

	gotValues, gotBody, err := ParseFrontmatter(rendered)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if gotBody != body {
		t.Fatalf("body mismatch: got %q want %q", gotBody, body)
	}
	if gotValues["name"] != "copywriter" {
		t.Fatalf("name mismatch: %v", gotValues["name"])
	}
	if gotValues["revision"] != 4 {
		t.Fatalf("revision mismatch: %v (%T)", gotValues["revision"], gotValues["revision"])
	}
	labels, ok := gotValues["labels"].([]string)
	if !ok || len(labels) != 2 || labels[0] != "team-marketing" || labels[1] != "role-writer" {
		t.Fatalf("labels mismatch: %#v", gotValues["labels"])
	}
	nested, ok := gotValues["mcp_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("mcp_config not a map: %#v", gotValues["mcp_config"])
	}
	if nested["transport"] != "stdio" || nested["enabled"] != true {
		t.Fatalf("nested mismatch: %#v", nested)
	}

	// Re-rendering what we parsed must reproduce the same bytes: this is
	// the property the importer's "no commit if it re-renders identical"
	// loop-termination argument (§C of the design doc) depends on.
	rerendered, err := RenderFrontmatter(gotValues, gotBody)
	if err != nil {
		t.Fatalf("RenderFrontmatter (re-render): %v", err)
	}
	if string(rerendered) != string(rendered) {
		t.Fatalf("re-render mismatch:\n--- original ---\n%s\n--- re-rendered ---\n%s", rendered, rerendered)
	}
}

func TestParseFrontmatterBodyOnly(t *testing.T) {
	values, body, err := ParseFrontmatter([]byte("just markdown, no frontmatter\n"))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if values != nil {
		t.Fatalf("want nil values for body-only file, got %#v", values)
	}
	if body != "just markdown, no frontmatter\n" {
		t.Fatalf("body mismatch: %q", body)
	}
}

func TestParseFrontmatterMalformed(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"unterminated", "---\nname: copywriter\nno closing fence\n"},
		{"invalid yaml", "---\nname: [unterminated\n---\n\nbody\n"},
		{"not a mapping", "---\n- a\n- b\n---\n\nbody\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseFrontmatter([]byte(tc.data))
			if err == nil {
				t.Fatalf("want error, got nil")
			}
		})
	}
}

func TestParseFrontmatterHandEdited(t *testing.T) {
	// Different indentation and quoting than RenderFrontmatter would
	// produce — the importer (§C) must be able to read files a human
	// edited by hand, not just files this package wrote.
	data := "---\nname:    \"copywriter\"\nrevision: 7\n---\nno blank line before body\n"
	values, body, err := ParseFrontmatter([]byte(data))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if values["name"] != "copywriter" || values["revision"] != 7 {
		t.Fatalf("values mismatch: %#v", values)
	}
	if body != "no blank line before body\n" {
		t.Fatalf("body mismatch: %q", body)
	}
}
