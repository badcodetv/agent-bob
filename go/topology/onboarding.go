package topology

// onboarding.go — the sixteenth built-in, and the only one that is not a
// coordination shape. It exists so that creating a project can go through the
// same ApplyTopology transaction as everything else
// (design/2026-09-08-memory-coordinated-organisation.md §A): one worker, the
// interviewer, whose conversation produces a CHARTER — a goal, a measure and
// a first set of labelling rules. It deliberately produces no roster; the
// architect the charter creates does that (Decision A1).
//
// No questions, on purpose. The project's name and goal are already collected
// by the create-project form, and the interview's entire job is to ask the
// rest — a topology question here would be the same question twice.
//
// No subscription and no schedule either. The interview is a CHAT, created
// with persona:"interviewer" rather than worker:"interviewer" (Decision A7),
// so nothing wakes this worker as a job and it needs no trigger. The worker
// row exists to hold the prompt that the chat resolves.

import (
	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/orgprompts"
)

// OnboardingWorker is the worker name onboarding creates. The session that
// runs the interview names it as its persona, and POST /agent/charter/apply
// disables it once the charter lands (Decision A8), so the name is shared
// between three packages and lives here rather than as a string literal in
// each.
const OnboardingWorker = "interviewer"

func init() {
	Register(&Topology{
		Name:    "onboarding",
		Version: "v1",
		Description: "The first conversation in a new project. One worker, an interviewer, " +
			"which asks what the project is for and how you would know it is working, " +
			"then writes that down as a charter for you to approve. It designs no team — " +
			"approving the charter creates an architect, and the architect designs the team.",
		Questions: nil,
		Render:    renderOnboarding,
	})
}

// renderOnboarding is the pure renderer for onboarding@v1. It takes no
// answers; the signature is the registry's.
func renderOnboarding(Answers) (*Bundle, error) {
	return &Bundle{
		Workers: []agentdb.Worker{{
			Name: OnboardingWorker,
			Description: "Interviews whoever created this project and deposits a charter " +
				"— goal, measure and labelling rules — for a human to approve.",
			SystemPrompt: orgprompts.Interviewer(),
			MaxInstances: agentdb.DefaultMaxInstances,
			Enabled:      true,
		}},
		Subscriptions: nil,
		Schedules:     nil,
	}, nil
}
