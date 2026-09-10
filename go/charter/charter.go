// Package charter defines the artifact an onboarding interview deposits: a
// project's goal, how success is measured, its first label vocabulary, and
// who runs the architect. It is deliberately NOT a roster — deciding what
// workers should exist is the architect's job (design
// 2026-09-08-memory-coordinated-organisation.md, Decision A1), not the
// interview's.
//
// Parse and Validate exist because the interviewer depositing this charter is
// a language model, and a schema described to a model only in prose is a
// documented failure mode: Agent Wolf's equivalent produced thirteen schema
// errors on its first real attempt (R269, design doc's Decision A5). A
// validator that names every problem at once, each at an addressable Path,
// lets the model fix a charter in one round instead of one error at a time.
//
// This package does not touch prompt text or topology — that is Resolve
// (ticket T8), which lives in resolve.go and imports go/orgprompts and
// go/topology. Parse and Validate need neither.
package charter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
)

// Charter is what the interview deposits. It describes the project's purpose
// and its first labelling rules, and names the architect. It does NOT
// describe a roster — the architect builds that.
type Charter struct {
	Goal    string `json:"goal"`    // required
	Measure string `json:"measure"` // required — how you would know it works

	// LabelRules is the first version of the label registry: the vocabulary
	// and, per label, when it should be written. Free prose, deliberately —
	// core enforces no vocabulary (docs/product/03-memory.md:37-40).
	LabelRules string `json:"label_rules"` // required

	ArchitectName string `json:"architect_name,omitempty"` // default "architect"
	ArchitectCron string `json:"architect_cron,omitempty"` // default "0 9 * * *"

	ProjectBackground string `json:"project_background,omitempty"`
	Rationale         string `json:"rationale"` // required
}

// Issue is one addressable validation failure. Path names the JSON field so
// a model (or a console) can point at exactly what to fix, rather than
// re-reading the whole schema.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Memory kinds and event/worker names the rest of the onboarding-and-architect
// design (T8 onward) reuses. Collected here, next to the schema they
// describe, rather than redefined per-ticket.
const (
	MemoryKindCharter  = "org-charter"  // labels {kind, name: <session id>}
	MemoryKindGoal     = "project-goal" // labels {kind, name: "project-goal"}
	MemoryKindRegistry = "registry"     // labels {kind, name: "label-registry"}

	EventArchitectRun = "architect.run"

	DefaultArchitect     = "architect"
	DefaultArchitectCron = "0 9 * * *"
)

// unknownFieldRe pulls the field name out of encoding/json's
// DisallowUnknownFields error, which has the fixed shape
// `json: unknown field "foo"`. Charter is a flat struct (no nested objects),
// so the field name IS the path — there is no need for a general JSON-pointer
// walker here.
var unknownFieldRe = regexp.MustCompile(`unknown field "([^"]+)"`)

// Parse reads one deposited charter. The format the interviewer is asked to
// produce (design doc, Decision A6: it re-deposits the complete charter every
// revision, never a partial update) is: line 1 is a human-readable summary of
// what changed and why, then the rest of the content is exactly one JSON
// object. Unknown JSON keys are rejected rather than silently ignored, so a
// model that invents a field (e.g. tries to smuggle a roster into the
// charter) is told immediately instead of having the field vanish.
func Parse(content string) (c *Charter, summary string, err error) {
	if strings.TrimSpace(content) == "" {
		return nil, "", fmt.Errorf("charter deposit is empty: want a summary line followed by a JSON object")
	}

	firstLine, rest, hasRest := strings.Cut(content, "\n")
	summary = strings.TrimSpace(firstLine)
	if summary == "" {
		return nil, "", fmt.Errorf("charter deposit is missing its line 1 summary: " +
			"want a human-readable summary line before the JSON object")
	}
	if !hasRest || strings.TrimSpace(rest) == "" {
		return nil, "", fmt.Errorf("charter deposit has a summary line (%q) but no JSON body after it", summary)
	}

	dec := json.NewDecoder(strings.NewReader(rest))
	dec.DisallowUnknownFields()
	var parsed Charter
	if decErr := dec.Decode(&parsed); decErr != nil {
		if m := unknownFieldRe.FindStringSubmatch(decErr.Error()); m != nil {
			return nil, "", fmt.Errorf("charter deposit: unknown field %q at path %q — "+
				"the charter schema does not have this key", m[1], m[1])
		}
		return nil, "", fmt.Errorf("charter deposit: invalid JSON body: %w", decErr)
	}

	return &parsed, summary, nil
}

// Validate checks a parsed charter against every rule at once and returns
// every violation, never just the first — see the package doc for why that
// matters. A nil or empty return means the charter is fit to resolve
// (ticket T8) and apply.
func Validate(c *Charter) []Issue {
	var issues []Issue

	requireNonBlank := func(path, value string) {
		if strings.TrimSpace(value) == "" {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("%s is required", path)})
		}
	}
	requireNonBlank("goal", c.Goal)
	requireNonBlank("measure", c.Measure)
	requireNonBlank("label_rules", c.LabelRules)
	requireNonBlank("rationale", c.Rationale)

	if c.ArchitectName != "" {
		if err := agentdb.ValidateWorkerName(c.ArchitectName); err != nil {
			issues = append(issues, Issue{Path: "architect_name", Message: err.Error()})
		}
	}
	if c.ArchitectCron != "" {
		if _, err := agentdb.ParseCron(c.ArchitectCron); err != nil {
			issues = append(issues, Issue{Path: "architect_cron", Message: err.Error()})
		}
	}

	return issues
}
