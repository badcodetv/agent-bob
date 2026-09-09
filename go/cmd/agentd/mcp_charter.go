package main

// mcp_charter.go — one tool, `charter_validate`, for the onboarding
// interviewer (design/2026-09-08-memory-coordinated-organisation.md, Decision
// A5).
//
// It exists because a schema described to a model only in prose is a
// documented failure: Agent Wolf's equivalent produced thirteen errors on its
// first real attempt. The fix that worked there was a validator the model
// calls BEFORE depositing, sharing the gate's own code — so this tool runs
// charter.Parse, charter.Validate and charter.Resolve, exactly what
// POST /agent/charter/apply runs, and the two can never drift into two
// accounts of the same charter.
//
// Two things it deliberately does NOT do.
//
// It does not write. The deposit goes through memory_create, so the charter
// carries container provenance like any other memory; a validator that could
// also deposit would let a model skip its own gate.
//
// It does not return the resolved bundle, only a summary of it. The bundle
// contains orgprompts.Architect() — the entire architect prompt, every tool
// name, every reconciliation rule — and the interviewer calls this repeatedly
// while a charter converges. Returning it would put that text into the
// interview's context on every call, for no reason: the interviewer's job ends
// at the charter, and what the architect is told is not its business.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/binocarlos/badcode-agent-orange/charter"
)

// charterTools has no store: validation is pure. It is registered inside
// main.go's `if agentDB != nil` guard anyway, with every other core tool —
// a project that has no product layer has nothing to apply a charter to, and
// a tool that appeared only on some stacks would be worse than one that never
// appears.
type charterTools struct{}

func newCharterTools() *charterTools { return &charterTools{} }

const charterValidateDescription = `Check a project charter before you deposit it. ` +
	`Pass the EXACT content you are about to write with memory_create — the summary line ` +
	`and the JSON object, character for character — as a string, or pass the JSON object ` +
	`on its own if you have not composed the deposit yet.

Returns {"valid": true} plus a summary of what approving the charter would do, or ` +
	`{"valid": false} with one entry per problem, each naming the field to fix. Every ` +
	`problem is reported at once, so you can fix them in one pass rather than one per call.

This tool writes NOTHING. Call it as often as you like. Depositing is a separate act ` +
	`(memory_create), and approving is a human's.`

func (c *charterTools) tools() []*mcpTool {
	return []*mcpTool{
		{
			Name:        "charter_validate",
			Description: charterValidateDescription,
			InputSchema: objectSchema(map[string]any{
				"charter": map[string]any{
					"description": "The charter: either the whole deposit as a string (summary line, " +
						"then the JSON object), or the JSON object on its own.",
				},
			}, []string{"charter"}),
			Handler: c.validate,
		},
	}
}

// charterValidateArgs takes `charter` as raw JSON because the argument is
// deliberately either a string or an object — the interviewer holds a string
// once it has composed a deposit, and an object before that, and being told
// "wrong type" at the moment it is trying to check its work is the least
// useful possible answer.
type charterValidateArgs struct {
	Charter json.RawMessage `json:"charter"`
}

// charterValidateResult is the whole response shape. `Summary` describes the
// effects; it never carries prompt text — see the file header.
type charterValidateResult struct {
	Valid   bool             `json:"valid"`
	Errors  []charter.Issue  `json:"errors,omitempty"`
	Summary *charterEffects  `json:"summary,omitempty"`
	Charter *charterEchoedIn `json:"charter,omitempty"`
}

// charterEffects is what approving this charter would do, in the terms a
// person reading the console panel would recognise.
type charterEffects struct {
	ArchitectName     string   `json:"architect_name"`
	ArchitectCron     string   `json:"architect_cron"`
	ScheduleEnabled   bool     `json:"schedule_enabled"`
	SubscriptionEvent string   `json:"subscription_event"`
	MemorySeedLabels  []string `json:"memory_seed_labels"`
	SettingsFields    []string `json:"settings_fields"`
	WorkerCount       int      `json:"worker_count"`
}

// charterEchoedIn is the short read-back: what the tool understood, so a model
// that passed a string can see its summary line was found and its JSON parsed
// into the fields it meant. Content, not prompt text.
type charterEchoedIn struct {
	Summary string `json:"summary,omitempty"`
	Goal    string `json:"goal"`
	Measure string `json:"measure"`
}

func (c *charterTools) validate(_ context.Context, _ mcpCaller, raw json.RawMessage) (any, error) {
	var args charterValidateArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	if len(args.Charter) == 0 {
		return nil, fmt.Errorf("charter: required — pass the deposit as a string, or the JSON object")
	}

	parsed, summaryLine, err := parseCharterArg(args.Charter)
	if err != nil {
		// A parse failure is a RESULT, not a tool error: the model is asking
		// "is this right?", and "no, and here is why" is the answer to that
		// question. An error would read as "the tool is broken".
		return charterValidateResult{
			Valid:  false,
			Errors: []charter.Issue{{Path: "charter", Message: err.Error()}},
		}, nil
	}

	if issues := charter.Validate(parsed); len(issues) > 0 {
		return charterValidateResult{Valid: false, Errors: issues}, nil
	}

	// Resolve is run for its refusals, not its output: it re-checks the
	// architect's name and cron, and a charter that validates but cannot
	// resolve must not be reported as valid — the interviewer would deposit
	// it and the human would press Approve on something that fails.
	bundle, err := charter.Resolve(parsed)
	if err != nil {
		return charterValidateResult{
			Valid:  false,
			Errors: []charter.Issue{{Path: "charter", Message: err.Error()}},
		}, nil
	}

	effects := &charterEffects{
		SubscriptionEvent: charter.EventArchitectRun,
		WorkerCount:       len(bundle.Workers),
	}
	if len(bundle.Workers) > 0 {
		effects.ArchitectName = bundle.Workers[0].Name
	}
	if len(bundle.Schedules) > 0 {
		effects.ArchitectCron = bundle.Schedules[0].Cron
		effects.ScheduleEnabled = bundle.Schedules[0].Enabled
	}
	if len(bundle.Subscriptions) > 0 {
		effects.SubscriptionEvent = bundle.Subscriptions[0].EventType
	}
	for _, seed := range bundle.MemorySeeds {
		effects.MemorySeedLabels = append(effects.MemorySeedLabels, formatLabels(seed.Labels))
	}
	if p := bundle.SettingsPatch; p != nil {
		if strings.TrimSpace(p.SystemPrompt) != "" {
			effects.SettingsFields = append(effects.SettingsFields, "system_prompt")
		}
		if len(p.Briefing) > 0 {
			effects.SettingsFields = append(effects.SettingsFields, "briefing")
		}
	}

	return charterValidateResult{
		Valid:   true,
		Summary: effects,
		Charter: &charterEchoedIn{
			Summary: summaryLine,
			Goal:    parsed.Goal,
			Measure: parsed.Measure,
		},
	}, nil
}

// parseCharterArg accepts the two shapes the tool documents. A JSON string is
// the whole deposit and goes through charter.Parse, which is what enforces the
// summary line; anything else is treated as the bare object, and its summary
// line comes back empty because there is not one yet.
func parseCharterArg(raw json.RawMessage) (*charter.Charter, string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, "", fmt.Errorf("charter: could not read the string: %w", err)
		}
		return charter.Parse(s)
	}

	// The bare object still goes through Parse, so there is exactly one
	// decoder and one unknown-field rule. A synthetic summary line is
	// prepended and thrown away: without it Parse would report the object's
	// first line as the summary and try to decode the rest, which is a
	// confusing error for a caller who did nothing wrong.
	c, _, err := charter.Parse("(no summary line — the object was passed on its own)\n" + trimmed)
	if err != nil {
		return nil, "", err
	}
	return c, "", nil
}

// formatLabels renders a label set as a stable "k=v,k=v" string, sorted by
// key, so the summary is deterministic and reads the way a selector does.
func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ",")
}
