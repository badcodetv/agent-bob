package charter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/orgprompts"
)

// goodCharter is the reference input: every required field filled, both
// optional architect fields left to their defaults.
func goodCharter() *Charter {
	return &Charter{
		Goal:       "Send one newsletter a week to the shop's mailing list.",
		Measure:    "Four newsletters have gone out in a month and the list is bigger than 430.",
		LabelRules: "kind=draft — a newsletter written but not sent. name=<issue-slug>.",
		Rationale:  "Ellen's problem is repeat visits, not new customers.",
	}
}

// TestResolveProducesExactlyTheArchitect pins the shape of the bundle: one
// worker and no roster. The count is the assertion that matters — Decision A1
// is that the interview creates the architect and NOTHING else, and a second
// worker sneaking in here would put roster design in two places.
func TestResolveProducesExactlyTheArchitect(t *testing.T) {
	b, err := Resolve(goodCharter())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(b.Workers) != 1 {
		t.Fatalf("workers: want exactly 1 (the architect), got %d", len(b.Workers))
	}
	w := b.Workers[0]
	if w.Name != DefaultArchitect {
		t.Errorf("worker name: want %q, got %q", DefaultArchitect, w.Name)
	}
	if w.SystemPrompt != orgprompts.Architect() {
		t.Error("the architect's prompt is not orgprompts.Architect() verbatim")
	}
	if !w.Enabled {
		t.Error("the architect must be enabled — it is triggered by an event, not by being on")
	}
	if w.Frozen {
		t.Error("the architect must not be frozen (Decision C4: nothing is frozen by default)")
	}
	if len(w.Briefing) != 1 || w.Briefing[0] != RegistrySelector {
		t.Errorf("architect briefing: want [%s], got %v", RegistrySelector, w.Briefing)
	}
	// Project, IDs and timestamps belong to apply, not to a pure resolve.
	if w.Project != "" || w.CreatedAt != 0 || w.UpdatedAt != 0 {
		t.Errorf("worker carries apply-time fields: project=%q created=%d updated=%d", w.Project, w.CreatedAt, w.UpdatedAt)
	}
}

// The schedule ships ON, and that is the product: approving a charter starts a
// loop that changes the project on a clock, by itself. It was disabled through
// the build (C5) so half-finished code could not run; T25 turned it on. The
// assertion is kept sharp because flipping it back would make onboarding
// silently produce an architect that never runs.
func TestResolveSchedulesTheArchitectAndTurnsItOn(t *testing.T) {
	b, err := Resolve(goodCharter())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(b.Schedules) != 1 {
		t.Fatalf("schedules: want 1, got %d", len(b.Schedules))
	}
	s := b.Schedules[0]
	if !s.Enabled {
		t.Error("the architect's schedule must render ENABLED — an approved charter that " +
			"produces a switched-off architect does nothing until someone finds the schedule editor")
	}
	if s.Worker != DefaultArchitect {
		t.Errorf("schedule worker: want %q, got %q", DefaultArchitect, s.Worker)
	}
	if s.Cron != DefaultArchitectCron {
		t.Errorf("schedule cron: want the default %q, got %q", DefaultArchitectCron, s.Cron)
	}
	if strings.TrimSpace(s.Input) == "" {
		t.Error("a schedule with no input delivers an empty first message")
	}
	if s.TargetSession != "" {
		t.Errorf("schedule must start a fresh job, not continue a session; target_session=%q", s.TargetSession)
	}
}

// "Run the architect now" is an EVENT, not a chat, because briefings are
// built only on the dispatch path — so the subscription is what makes the
// button work at all.
func TestResolveSubscribesTheArchitectToItsRunEvent(t *testing.T) {
	b, err := Resolve(goodCharter())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(b.Subscriptions) != 1 {
		t.Fatalf("subscriptions: want 1, got %d", len(b.Subscriptions))
	}
	sub := b.Subscriptions[0]
	if sub.EventType != EventArchitectRun {
		t.Errorf("subscription event type: want %q, got %q", EventArchitectRun, sub.EventType)
	}
	if sub.Worker != DefaultArchitect {
		t.Errorf("subscription worker: want %q, got %q", DefaultArchitect, sub.Worker)
	}
	if !sub.Enabled {
		t.Error("the architect.run subscription must be enabled, or the button does nothing")
	}
	if sub.Project != "" || sub.ID != "" {
		t.Errorf("subscription carries apply-time fields: project=%q id=%q", sub.Project, sub.ID)
	}
}

// Both seeds, with the exact labels the rest of the design selects on. A
// typo in one of these is the "goes green and does nothing" failure: the
// architect's memory_current("project-goal") returns {found:false} and it
// designs a roster for a project whose purpose it cannot read.
func TestResolveSeedsTheGoalAndTheRegistry(t *testing.T) {
	c := goodCharter()
	b, err := Resolve(c)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(b.MemorySeeds) != 2 {
		t.Fatalf("memory seeds: want 2, got %d", len(b.MemorySeeds))
	}

	goal, registry := b.MemorySeeds[0], b.MemorySeeds[1]

	wantGoalLabels := agentdb.LabelSet{"kind": MemoryKindGoal, "name": MemoryKindGoal}
	if !reflect.DeepEqual(goal.Labels, wantGoalLabels) {
		t.Errorf("goal seed labels: want %v, got %v", wantGoalLabels, goal.Labels)
	}
	if !strings.Contains(goal.Content, c.Goal) {
		t.Error("goal seed does not contain the charter's goal")
	}
	if !strings.Contains(goal.Content, c.Measure) {
		t.Error("goal seed does not contain the measure — the architect judges itself against it")
	}

	wantRegistryLabels := agentdb.LabelSet{"kind": MemoryKindRegistry, "name": "label-registry"}
	if !reflect.DeepEqual(registry.Labels, wantRegistryLabels) {
		t.Errorf("registry seed labels: want %v, got %v", wantRegistryLabels, registry.Labels)
	}
	// The seed is frame + project rules: dropping either half is a silent
	// failure. Without the frame, nobody is told how retraction or the
	// automatic briefing section work; without the rules, the charter's
	// whole third question was for nothing.
	if !strings.Contains(registry.Content, "retracts=<memory-id>") {
		t.Error("registry seed is missing the shipped frame (no retraction mechanics)")
	}
	if !strings.Contains(registry.Content, c.LabelRules) {
		t.Error("registry seed is missing the charter's own label rules")
	}
}

// The settings patch carries BOTH halves of §B: the prose every worker reads
// unconditionally, and the project-wide briefing that hands every job the
// registry with no per-worker configuration.
func TestResolvePatchesSettingsWithBackgroundAndBriefing(t *testing.T) {
	c := goodCharter()
	c.ProjectBackground = "An independent bookshop in Bristol."
	b, err := Resolve(c)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if b.SettingsPatch == nil {
		t.Fatal("settings patch: want one, got nil")
	}
	p := b.SettingsPatch
	for _, want := range []string{c.ProjectBackground, c.Goal, c.Measure} {
		if !strings.Contains(p.SystemPrompt, want) {
			t.Errorf("project background is missing %q", want)
		}
	}
	if len(p.Briefing) != 1 || p.Briefing[0] != RegistrySelector {
		t.Errorf("project briefing: want [%s], got %v", RegistrySelector, p.Briefing)
	}
	// Zero-means-keep is the overlay's convention, so a patch that filled in
	// numerics would silently overwrite an operator's caps.
	if p.MaxConcurrentJobs != 0 || p.DailyTokensHard != 0 || p.BriefingMaxBytes != 0 {
		t.Errorf("settings patch sets numerics it should leave alone: %+v", p)
	}
	if p.Project != "" {
		t.Errorf("settings patch carries a project: %q", p.Project)
	}
}

// Optional fields honour the charter when given, and only then.
func TestResolveHonoursTheChartersArchitectNameAndCron(t *testing.T) {
	c := goodCharter()
	c.ArchitectName = "chief-of-staff"
	c.ArchitectCron = "0 9 * * 1"

	b, err := Resolve(c)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := b.Workers[0].Name; got != "chief-of-staff" {
		t.Errorf("worker name: want chief-of-staff, got %q", got)
	}
	if got := b.Schedules[0].Worker; got != "chief-of-staff" {
		t.Errorf("schedule worker: want chief-of-staff, got %q", got)
	}
	if got := b.Subscriptions[0].Worker; got != "chief-of-staff" {
		t.Errorf("subscription worker: want chief-of-staff, got %q", got)
	}
	if got := b.Schedules[0].Cron; got != "0 9 * * 1" {
		t.Errorf("cron: want 0 9 * * 1, got %q", got)
	}
}

// Resolve is reachable from the MCP tool with whatever a model just wrote, so
// a name or cron that never went through Validate must fail loudly here
// rather than half-way through the apply transaction.
func TestResolveRefusesAnUnusableNameOrCron(t *testing.T) {
	for name, mutate := range map[string]func(*Charter){
		"bad worker name": func(c *Charter) { c.ArchitectName = "Not A Name" },
		"bad cron":        func(c *Charter) { c.ArchitectCron = "@daily" },
	} {
		t.Run(name, func(t *testing.T) {
			c := goodCharter()
			mutate(c)
			if _, err := Resolve(c); err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}

	if _, err := Resolve(nil); err == nil {
		t.Error("Resolve(nil): want an error, got nil")
	}
}

// Purity, the same property topology.TestRegisteredRenderersAreDeterministic
// pins over the built-ins: same charter in, same bundle out, structurally and
// as bytes. Apply, preview and charter_validate all call this, and a preview
// that disagreed with what apply then wrote would be worse than no preview.
func TestResolveIsDeterministic(t *testing.T) {
	c := goodCharter()
	first, err := Resolve(c)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	again, err := Resolve(c)
	if err != nil {
		t.Fatalf("resolve (repeat): %v", err)
	}

	if !reflect.DeepEqual(first, again) {
		t.Fatalf("bundles differ structurally:\nfirst %#v\nagain %#v", first, again)
	}
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	bb, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("marshal again: %v", err)
	}
	if string(a) != string(bb) {
		t.Fatalf("bundles differ as bytes:\n%s\nvs\n%s", a, bb)
	}
}
