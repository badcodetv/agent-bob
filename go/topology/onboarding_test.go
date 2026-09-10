package topology

import (
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/orgprompts"
)

func onboardingV1(t *testing.T) *Topology {
	t.Helper()
	top, ok := Get("onboarding", "v1")
	if !ok {
		t.Fatal("onboarding@v1 not registered")
	}
	return top
}

// The whole bundle: one worker and nothing else. The emptiness is the
// contract, not an omission — a subscription or a schedule here would give
// the interviewer a second way to be woken, as a job with no human in the
// conversation, which is not what an interview is.
func TestOnboardingRendersOneWorkerAndNoWiring(t *testing.T) {
	b, err := onboardingV1(t).Instantiate(Answers{})
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

	if len(b.Workers) != 1 {
		t.Fatalf("workers: want 1, got %d", len(b.Workers))
	}
	w := b.Workers[0]
	if w.Name != OnboardingWorker {
		t.Errorf("worker name: want %q, got %q", OnboardingWorker, w.Name)
	}
	if !w.Enabled {
		t.Error("the interviewer must render enabled — the interview starts immediately")
	}
	if w.Frozen {
		t.Error("the interviewer must not render frozen")
	}
	if w.MaxInstances != agentdb.DefaultMaxInstances {
		t.Errorf("max_instances: want %d, got %d", agentdb.DefaultMaxInstances, w.MaxInstances)
	}
	if w.Description == "" {
		t.Error("worker description must not be empty")
	}
	if len(w.Briefing) != 0 {
		t.Errorf("briefing: want none (a chat never receives one), got %v", w.Briefing)
	}

	if len(b.Subscriptions) != 0 {
		t.Errorf("subscriptions: want 0, got %d", len(b.Subscriptions))
	}
	if len(b.Schedules) != 0 {
		t.Errorf("schedules: want 0, got %d", len(b.Schedules))
	}
	if len(b.MemorySeeds) != 0 {
		t.Errorf("memory seeds: want 0 — the charter seeds memory, not this, got %d", len(b.MemorySeeds))
	}
	if b.SettingsPatch != nil {
		t.Errorf("settings patch: want none, got %+v", b.SettingsPatch)
	}
}

// The prompt is shipped prose, not a template: byte-identical to the embedded
// file. A renderer that reformatted, trimmed or interpolated it would put a
// second copy of the interview's rules in play, which is exactly the
// "three places the memory schema lives" problem this design exists to shrink.
func TestOnboardingPromptIsTheEmbeddedInterviewerVerbatim(t *testing.T) {
	b, err := onboardingV1(t).Instantiate(Answers{})
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	if got, want := b.Workers[0].SystemPrompt, orgprompts.Interviewer(); got != want {
		t.Errorf("system prompt is not orgprompts.Interviewer() verbatim:\ngot  %d bytes\nwant %d bytes", len(got), len(want))
	}
}

// No questions, and Instantiate must therefore accept a nil Answers map as
// readily as an empty one — the console posts no answers for this topology.
func TestOnboardingTakesNoQuestions(t *testing.T) {
	top := onboardingV1(t)
	if len(top.Questions) != 0 {
		t.Errorf("questions: want 0, got %d", len(top.Questions))
	}
	if _, err := top.Instantiate(nil); err != nil {
		t.Errorf("instantiate with nil answers: %v", err)
	}
}

// It has to be in the catalogue List() serves, or the console cannot offer it
// and POST /agent/topologies/apply cannot resolve it.
func TestOnboardingIsInTheCatalogue(t *testing.T) {
	for _, top := range List() {
		if top.Ref() == "onboarding@v1" {
			return
		}
	}
	t.Fatal("onboarding@v1 absent from List()")
}
