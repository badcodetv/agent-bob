package charter

import (
	"strings"
	"testing"
)

// wellFormed is a deposit shaped exactly like the interviewer is asked to
// produce: a human-readable summary line, then one JSON object.
const wellFormed = `Onboarding interview complete; charter proposed.
{
  "goal": "grow monthly recurring revenue",
  "measure": "MRR, tracked weekly",
  "label_rules": "kind=summary for weekly digests; kind=lesson for retros",
  "rationale": "the founder wants a single north-star metric"
}`

// TestParseWellFormed covers the first acceptance criterion: a well-formed
// deposit parses, returning both the summary line and the charter.
func TestParseWellFormed(t *testing.T) {
	c, summary, err := Parse(wellFormed)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	if summary != "Onboarding interview complete; charter proposed." {
		t.Fatalf("summary: got %q", summary)
	}
	if c == nil {
		t.Fatal("charter: got nil")
	}
	if c.Goal != "grow monthly recurring revenue" {
		t.Fatalf("goal: got %q", c.Goal)
	}
	if c.Measure != "MRR, tracked weekly" {
		t.Fatalf("measure: got %q", c.Measure)
	}
	if c.LabelRules != "kind=summary for weekly digests; kind=lesson for retros" {
		t.Fatalf("label_rules: got %q", c.LabelRules)
	}
	if c.Rationale != "the founder wants a single north-star metric" {
		t.Fatalf("rationale: got %q", c.Rationale)
	}
	// Optional fields were omitted, so they must come back zero-valued —
	// Resolve (T8) is what applies the "architect"/"0 9 * * *" defaults, not
	// Parse.
	if c.ArchitectName != "" || c.ArchitectCron != "" || c.ProjectBackground != "" {
		t.Fatalf("optional fields should be empty when omitted, got %+v", c)
	}
}

// TestParseUnknownField covers the second acceptance criterion: an unknown
// JSON key errors, and the error names its path — so a model depositing a
// malformed charter can fix it without guessing which field was wrong.
func TestParseUnknownField(t *testing.T) {
	content := `Charter proposed.
{
  "goal": "g",
  "measure": "m",
  "label_rules": "l",
  "rationale": "r",
  "roster": ["support", "sales"]
}`
	_, _, err := Parse(content)
	if err == nil {
		t.Fatal("Parse: want error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "roster") {
		t.Fatalf("error should name the offending path %q, got: %v", "roster", err)
	}
}

// TestParseMissingSummaryLine covers the third acceptance criterion: a
// deposit whose line 1 is blank (the JSON starts immediately) errors, rather
// than silently treating the opening brace as the summary.
func TestParseMissingSummaryLine(t *testing.T) {
	content := `
{
  "goal": "g",
  "measure": "m",
  "label_rules": "l",
  "rationale": "r"
}`
	_, _, err := Parse(content)
	if err == nil {
		t.Fatal("Parse: want error for missing summary line, got nil")
	}
	if !strings.Contains(err.Error(), "summary") {
		t.Fatalf("error should mention the missing summary line, got: %v", err)
	}
}

// TestParseEmptyBody covers the fourth acceptance criterion: an empty
// deposit errors outright.
func TestParseEmptyBody(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"totally empty", ""},
		{"whitespace only", "   \n\t  "},
		{"summary with nothing after it", "Charter proposed.\n"},
		{"summary with only blank lines after it", "Charter proposed.\n\n  \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Parse(tt.content)
			if err == nil {
				t.Fatalf("Parse(%q): want error, got nil", tt.content)
			}
		})
	}
}

// TestParseMalformedJSON is not one of the five named acceptance criteria,
// but Parse must not panic or silently succeed on broken JSON — this pins
// that the JSON decode error surfaces as a Parse error.
func TestParseMalformedJSON(t *testing.T) {
	content := "Charter proposed.\n{not json"
	_, _, err := Parse(content)
	if err == nil {
		t.Fatal("Parse: want error for malformed JSON, got nil")
	}
}

// TestValidateAllRequiredFieldsMissing pins that Validate reports every
// blank required field in one pass — the "all issues at once" contract that
// lets a model fix a charter in one round instead of one-error-per-round.
func TestValidateAllRequiredFieldsMissing(t *testing.T) {
	c := &Charter{}
	issues := Validate(c)
	wantPaths := []string{"goal", "measure", "label_rules", "rationale"}
	if len(issues) != len(wantPaths) {
		t.Fatalf("issues: want %d, got %d: %+v", len(wantPaths), len(issues), issues)
	}
	got := map[string]bool{}
	for _, iss := range issues {
		got[iss.Path] = true
		if iss.Message == "" {
			t.Fatalf("issue for %q has no message", iss.Path)
		}
	}
	for _, p := range wantPaths {
		if !got[p] {
			t.Fatalf("missing issue for path %q, got %+v", p, issues)
		}
	}
}

// TestValidateWhitespaceOnlyCountsAsMissing pins that trimming happens
// before the empty check — a field of pure whitespace is not a real value.
func TestValidateWhitespaceOnlyCountsAsMissing(t *testing.T) {
	c := &Charter{
		Goal:       "  \t ",
		Measure:    "m",
		LabelRules: "l",
		Rationale:  "r",
	}
	issues := Validate(c)
	if len(issues) != 1 || issues[0].Path != "goal" {
		t.Fatalf("want exactly one issue on %q, got %+v", "goal", issues)
	}
}

// TestValidateThreeRulesBrokenAtOnce is the fifth acceptance criterion
// verbatim: a charter breaking three rules at once returns exactly three
// issues, each with an addressable Path.
func TestValidateThreeRulesBrokenAtOnce(t *testing.T) {
	c := &Charter{
		Goal:          "",                  // broken: required
		Measure:       "m",                 // fine
		LabelRules:    "l",                 // fine
		Rationale:     "r",                 // fine
		ArchitectName: "Not Kebab Case!",   // broken: fails ValidateWorkerName
		ArchitectCron: "not a cron string", // broken: fails ParseCron
	}
	issues := Validate(c)
	if len(issues) != 3 {
		t.Fatalf("want exactly 3 issues, got %d: %+v", len(issues), issues)
	}
	wantPaths := map[string]bool{"goal": false, "architect_name": false, "architect_cron": false}
	for _, iss := range issues {
		if _, ok := wantPaths[iss.Path]; !ok {
			t.Fatalf("unexpected issue path %q: %+v", iss.Path, iss)
		}
		wantPaths[iss.Path] = true
		if iss.Message == "" {
			t.Fatalf("issue for %q has no message", iss.Path)
		}
	}
	for p, seen := range wantPaths {
		if !seen {
			t.Fatalf("missing expected issue for path %q, got %+v", p, issues)
		}
	}
}

// TestValidateOptionalFieldsOKWhenValid pins the non-error side of the
// architect_name/architect_cron rules: a valid value (or an empty, omitted
// one) reports nothing.
func TestValidateOptionalFieldsOKWhenValid(t *testing.T) {
	tests := []struct {
		name string
		c    *Charter
	}{
		{
			name: "optional fields omitted entirely",
			c: &Charter{
				Goal: "g", Measure: "m", LabelRules: "l", Rationale: "r",
			},
		},
		{
			name: "optional fields present and valid",
			c: &Charter{
				Goal: "g", Measure: "m", LabelRules: "l", Rationale: "r",
				ArchitectName: "architect", ArchitectCron: "0 9 * * *",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if issues := Validate(tt.c); len(issues) != 0 {
				t.Fatalf("want no issues, got %+v", issues)
			}
		})
	}
}
