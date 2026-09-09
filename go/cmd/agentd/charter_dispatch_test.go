package main

// charter_dispatch_test.go — T13: emitting `architect.run` produces a
// dispatched architect job whose composed prompt contains the label registry.
//
// This is the end-to-end proof of two decisions that are only true together.
//
// C5 says the daily schedule ships disabled and "Run the architect now" is an
// EVENT, not a chat. The reason is mechanical: BuildBriefingSections runs only
// inside ComposeJob on the dispatch path, so opening a chat with the architect
// would silently deprive it of the rulebook — no error, no empty section, just
// an architect designing an organisation without knowing the label vocabulary.
//
// B1 says the registry reaches every job through a PROJECT-WIDE briefing, set
// once on project settings rather than per worker.
//
// Neither is observable from the other side of a unit test. What is observable
// is the composed prompt of the job the event actually produced, so that is
// what this asserts — and it builds the worker, the subscription and the
// settings from charter.Resolve rather than by hand, because a test that
// hand-wrote them would keep passing after Resolve stopped producing them.

import (
	"context"
	"strings"
	"testing"

	agentkit "github.com/binocarlos/badcode-agent-orange"
	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/charter"
	"github.com/binocarlos/badcode-agent-orange/orgprompts"
)

const registryContentForDispatch = "kind=draft — a newsletter written but not sent."

// seedFromCharter puts a resolved charter into the fake store the way
// ApplyTopology would: the worker, the subscription, and the settings patch
// overlaid onto the project's settings.
func seedFromCharter(t *testing.T, store *fakeRouterStore, project string, withProjectBriefing, withWorkerBriefing bool) *charter.Charter {
	t.Helper()
	c := &charter.Charter{
		Goal:       "Send one newsletter a week.",
		Measure:    "Four have gone out in a month.",
		LabelRules: registryContentForDispatch,
		Rationale:  "Repeat visits are the problem.",
	}
	bundle, err := charter.Resolve(c)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for i := range bundle.Workers {
		w := bundle.Workers[i]
		w.Project = project
		if !withWorkerBriefing {
			w.Briefing = nil
		}
		store.addWorker(&w)
	}
	for i := range bundle.Subscriptions {
		s := bundle.Subscriptions[i]
		s.Project = project
		store.addSubscription(&s)
	}

	settings := agentdb.DefaultProjectSettings(project)
	if withProjectBriefing {
		merged, _ := agentdb.TopologySettingsOverlay(settings, bundle.SettingsPatch)
		settings = merged
	}
	store.settings[project] = settings
	return c
}

// architectRunJob posts architect.run, ticks once, and returns the single job
// that came out — failing if the count is anything but one.
func architectRunJob(t *testing.T, store *fakeRouterStore, memories agentkit.BriefingMemorySource) startJobInput {
	t.Helper()
	starter := &fakeJobStarter{store: store}
	rt, _ := newTestRouter(store, starter, func(c *dispatcherConfig) {
		c.CoreMCP = coreMCPServers("http://172.17.0.1:8099")
		c.Memories = memories
	})
	postEvent(t, store, "acme", charter.EventArchitectRun,
		"Run the architect now (pressed in the console).", agentdb.EventEnvelope{})
	if err := rt.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(starter.jobs) != 1 {
		t.Fatalf("architect.run produced %d jobs, want exactly 1 — the subscription "+
			"charter.Resolve creates is what makes the console's button work at all", len(starter.jobs))
	}
	return starter.jobs[0]
}

// The whole point: the event reaches the architect, and the job it starts
// carries the label registry.
func TestArchitectRunReachesABriefedJob(t *testing.T) {
	store := newFakeRouterStore()
	seedFromCharter(t, store, "acme", true, true)

	job := architectRunJob(t, store, fakeBriefingSource{
		charter.RegistrySelector: registryContentForDispatch,
	}).Job

	if !strings.Contains(job.SystemPrompt, registryContentForDispatch) {
		t.Errorf("the label registry did not reach the composed prompt:\n%s", job.SystemPrompt)
	}
	// The architect's own prompt must be in there too — a briefing without the
	// prompt would be a job that reads the rulebook and does nothing with it.
	if !strings.Contains(job.SystemPrompt, "STEP 0 — BOOTSTRAP") {
		t.Error("the architect's own prompt is not in the composed prompt")
	}
	if !strings.Contains(job.SystemPrompt, orgprompts.Architect()) {
		t.Error("the architect's prompt was not carried verbatim")
	}
	// And the core tools, or every instruction in that prompt names something
	// it cannot call.
	if _, ok := job.MCPServers[coreMCPServerName]; !ok {
		t.Errorf("the architect's job is not told about the core tool server: %v", job.MCPServers)
	}
}

// The negative case, which is what makes the positive one mean something: with
// no briefing selecting it, the same event produces the same job WITHOUT the
// registry. If this passed too, the assertion above would be proving that the
// registry text appears somewhere in a prompt, not that a briefing delivered
// it.
//
// BOTH briefings have to go, because charter.Resolve deliberately sets the
// registry selector twice — once project-wide (B1) and once on the architect
// itself, so that clearing the project list later cannot silently strip the
// architect of the rulebook. That redundancy is the right design and it is
// what makes this the only shape the negative case can take.
func TestArchitectRunWithNoBriefingAtAllHasNoRegistry(t *testing.T) {
	store := newFakeRouterStore()
	seedFromCharter(t, store, "acme", false, false)

	// The memory exists and is readable; only the project-wide briefing that
	// would have selected it is absent.
	job := architectRunJob(t, store, fakeBriefingSource{
		charter.RegistrySelector: registryContentForDispatch,
	}).Job

	if strings.Contains(job.SystemPrompt, registryContentForDispatch) {
		t.Errorf("the registry reached the prompt with no briefing selecting it, so the "+
			"positive case proves nothing about how it got there:\n%s", job.SystemPrompt)
	}
	if !strings.Contains(job.SystemPrompt, "STEP 0 — BOOTSTRAP") {
		t.Error("the job did not compose at all; the negative case is vacuous")
	}
}

// The architect's own briefing is a second, independent route to the same
// note (charter.Resolve sets both). Drop the project-wide list and the worker
// still gets the rulebook — which is why Resolve sets both.
func TestArchitectKeepsTheRegistryThroughItsOwnBriefing(t *testing.T) {
	store := newFakeRouterStore()
	seedFromCharter(t, store, "acme", false, true)

	worker, err := store.GetWorker(context.Background(), "acme", charter.DefaultArchitect)
	if err != nil {
		t.Fatalf("get architect: %v", err)
	}
	if len(worker.Briefing) != 1 || worker.Briefing[0] != charter.RegistrySelector {
		t.Fatalf("the architect's own briefing is %v, want [%s]", worker.Briefing, charter.RegistrySelector)
	}

	job := architectRunJob(t, store, fakeBriefingSource{
		charter.RegistrySelector: registryContentForDispatch,
	}).Job
	if !strings.Contains(job.SystemPrompt, registryContentForDispatch) {
		t.Errorf("with the project briefing gone, the architect's own briefing did not "+
			"deliver the registry:\n%s", job.SystemPrompt)
	}
}
