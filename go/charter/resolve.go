package charter

// resolve.go — the charter's one effect: turning what the interview agreed
// into the rows that make it real. Everything a charter causes is here, so
// "what does approving this actually do?" has one answer readable in one
// screen, and so `charter_validate` (MCP) and POST /agent/charter/apply can
// share it exactly rather than drifting into two accounts of the same act
// (Decision A5).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/orgprompts"
	"github.com/binocarlos/badcode-agent-orange/topology"
)

// RegistrySelector is the label selector that reads the project's label
// registry back: the project-wide briefing carries it, so every job in the
// project is handed the current rulebook with no per-worker configuration
// (Decision B1). One constant, because the same string has to appear in the
// settings patch, in the architect's own briefing and in the docs.
const RegistrySelector = "name=label-registry"

// ArchitectRunInput is what a firing of the architect's schedule delivers as
// the job's first message. A schedule says what the worker is told, not only
// when it runs; the standing instruction is "do the loop in your prompt",
// because the loop is the prompt.
const ArchitectRunInput = "Scheduled review: run your architect loop from step 0, then finish."

// Resolve turns a validated charter into the bundle that applying it writes.
// It is pure — no clock, no I/O, no randomness — and leaves Project, IDs and
// timestamps zero for ApplyTopology to stamp, exactly like a built-in
// topology's renderer.
//
// It produces, and deliberately nothing else:
//
//   - ONE worker, the architect, carrying orgprompts.Architect().
//   - ONE schedule at the charter's cadence, DISABLED (Decision C5): a
//     freshly-approved charter must not start a daily self-revising loop
//     before a human has watched it run once.
//   - ONE subscription on architect.run, which is how "Run the architect now"
//     reaches it. It has to be an event and not a chat: BuildBriefingSections
//     runs only inside ComposeJob on the dispatch path, so opening a chat
//     with the architect would silently deprive it of the label registry.
//   - A settings patch carrying the project background prose and the
//     project-wide briefing.
//   - TWO memory seeds: the goal, and the label registry.
//
// No roster. Deciding which workers should exist is the architect's job
// (Decision A1), and it does that on its first run through the bootstrap
// branch of its prompt.
//
// Resolve does not re-validate. Callers run Validate first and refuse on
// issues; the only error returned here is the one case where a charter that
// passed validation still cannot produce rows.
func Resolve(c *Charter) (*topology.Bundle, error) {
	if c == nil {
		return nil, fmt.Errorf("charter: resolve: charter is nil")
	}

	name := strings.TrimSpace(c.ArchitectName)
	if name == "" {
		name = DefaultArchitect
	}
	cron := strings.TrimSpace(c.ArchitectCron)
	if cron == "" {
		cron = DefaultArchitectCron
	}
	// Defensive, not decorative: Resolve is reachable from the MCP tool with
	// a charter a model just wrote, and a name that fails here would other-
	// wise surface as a store error halfway through the apply transaction.
	if err := agentdb.ValidateWorkerName(name); err != nil {
		return nil, fmt.Errorf("charter: resolve: architect_name: %w", err)
	}
	if _, err := agentdb.ParseCron(cron); err != nil {
		return nil, fmt.Errorf("charter: resolve: architect_cron: %w", err)
	}

	return &topology.Bundle{
		Workers: []agentdb.Worker{{
			Name: name,
			Description: "Decides what work this project needs doing, who does it, and how " +
				"they hand things to each other. Reviews the organisation against the " +
				"evidence and revises it.",
			SystemPrompt: orgprompts.Architect(),
			MaxInstances: agentdb.DefaultMaxInstances,
			Enabled:      true,
			// The architect's own briefing is the registry. The project-wide
			// briefing below already delivers it to every job, this one
			// included; naming it here too means the architect keeps the
			// rulebook even if the project-wide list is later cleared.
			Briefing: agentdb.SelectorList{RegistrySelector},
		}},
		Subscriptions: []agentdb.Subscription{{
			EventType: EventArchitectRun,
			Worker:    name,
			Enabled:   true,
		}},
		Schedules: []agentdb.Schedule{{
			Worker: name,
			Cron:   cron,
			Input:  ArchitectRunInput,
			// Disabled: see Decision C5 above. T25 is the ticket that turns
			// it on, and the console's toggle is how an operator does.
			Enabled: false,
		}},
		SettingsPatch: &agentdb.ProjectSettings{
			SystemPrompt: projectBackground(c),
			Briefing:     agentdb.SelectorList{RegistrySelector},
		},
		MemorySeeds: []agentdb.Memory{
			{
				Content: goalSeed(c),
				Labels:  agentdb.LabelSet{"kind": MemoryKindGoal, "name": MemoryKindGoal},
			},
			{
				Content: registrySeed(c),
				Labels:  agentdb.LabelSet{"kind": MemoryKindRegistry, "name": "label-registry"},
			},
		},
	}, nil
}

// projectBackground is the prose every worker in the project carries at the
// top of its prompt. The goal and the measure go in it — not only in the
// project-goal memory — because a worker reads its prompt unconditionally and
// only reads a memory if something hands it one or its prompt tells it to.
func projectBackground(c *Charter) string {
	b := &strings.Builder{}
	if bg := strings.TrimSpace(c.ProjectBackground); bg != "" {
		b.WriteString(bg)
		b.WriteString("\n\n")
	}
	b.WriteString("WHAT THIS PROJECT IS FOR\n")
	b.WriteString(strings.TrimSpace(c.Goal))
	b.WriteString("\n\nHOW WE WOULD KNOW IT IS WORKING\n")
	b.WriteString(strings.TrimSpace(c.Measure))
	b.WriteString("\n")
	return b.String()
}

// goalSeed is the same goal and measure as a readable note, so a worker can
// fetch it deliberately with memory_current("project-goal") — which is what
// the architect's bootstrap branch does before it designs anything.
func goalSeed(c *Charter) string {
	b := &strings.Builder{}
	b.WriteString("THE GOAL\n")
	b.WriteString(strings.TrimSpace(c.Goal))
	b.WriteString("\n\nTHE MEASURE — how we would know, in a month, whether this is working\n")
	b.WriteString(strings.TrimSpace(c.Measure))
	if r := strings.TrimSpace(c.Rationale); r != "" {
		b.WriteString("\n\nWHY THIS, AS AGREED IN THE ONBOARDING INTERVIEW\n")
		b.WriteString(r)
	}
	b.WriteString("\n")
	return b.String()
}

// registrySeed is the shipped frame — how name=, retracts= and the automatic
// rolling-summary section work, plus the starting vocabulary — followed by
// this project's own rules. The frame is not the charter's to write: those
// three mechanics are engine behaviour, and a project that omitted them would
// have workers writing under labels nothing delivers.
func registrySeed(c *Charter) string {
	return strings.TrimRight(orgprompts.LabelRegistry(), "\n") + "\n\n" + strings.TrimSpace(c.LabelRules) + "\n"
}

// Effects is what approving a charter would do, in the terms a person reading
// the console panel would recognise. It is a summary of the bundle and never
// the bundle itself: the bundle carries orgprompts.Architect(), the whole
// architect prompt, and both consumers here — the interviewer's
// charter_validate tool and the console's charter panel — would be showing it
// to something that has no use for it and pays by the token to receive it.
type Effects struct {
	ArchitectName     string   `json:"architect_name"`
	ArchitectCron     string   `json:"architect_cron"`
	ScheduleEnabled   bool     `json:"schedule_enabled"`
	SubscriptionEvent string   `json:"subscription_event"`
	MemorySeedLabels  []string `json:"memory_seed_labels"`
	SettingsFields    []string `json:"settings_fields"`
	WorkerCount       int      `json:"worker_count"`
}

// Summarise describes a resolved bundle. One implementation, because the MCP
// validator and the apply route must describe the same charter identically —
// Decision A5's rule, applied to the description as well as to the verdict.
func Summarise(b *topology.Bundle) *Effects {
	if b == nil {
		return nil
	}
	e := &Effects{
		SubscriptionEvent: EventArchitectRun,
		WorkerCount:       len(b.Workers),
	}
	if len(b.Workers) > 0 {
		e.ArchitectName = b.Workers[0].Name
	}
	if len(b.Schedules) > 0 {
		e.ArchitectCron = b.Schedules[0].Cron
		e.ScheduleEnabled = b.Schedules[0].Enabled
	}
	if len(b.Subscriptions) > 0 {
		e.SubscriptionEvent = b.Subscriptions[0].EventType
	}
	for _, seed := range b.MemorySeeds {
		e.MemorySeedLabels = append(e.MemorySeedLabels, formatLabels(seed.Labels))
	}
	if p := b.SettingsPatch; p != nil {
		if strings.TrimSpace(p.SystemPrompt) != "" {
			e.SettingsFields = append(e.SettingsFields, "system_prompt")
		}
		if len(p.Briefing) > 0 {
			e.SettingsFields = append(e.SettingsFields, "briefing")
		}
	}
	return e
}

// formatLabels renders a label set as a stable "k=v,k=v" string, sorted by
// key, so a summary is deterministic and reads the way a selector does.
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
