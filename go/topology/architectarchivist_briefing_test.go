package topology

import (
	"context"
	"strings"
	"testing"

	agentkit "github.com/badcodetv/agent-bob"
	"github.com/badcodetv/agent-bob/agentdb"
)

// T4 — the dead default briefing.
//
// RollingSummarySelector (agentkit.RollingSummarySelector, compose.go) is the
// default briefing section every worker gets: `kind=rolling-summary,
// worker=<name>`. The shipped archivist policy told the model to write
// `kind=summary, name=<thread-slug>` instead — a different `kind` and no
// `worker=` label at all — so the default section matched nothing, in every
// project, forever. Fixing the selector alone would not have helped: the
// policy still would not have produced a matching label. This file pins the
// other side: the archivist's prompt must actually instruct writing under the
// selector the default briefing reads.

// fakeBriefingSource is a minimal agentkit.BriefingMemorySource backed by a
// map, mirroring the one in compose_briefing_test.go (unexported there, so
// duplicated rather than imported).
type fakeBriefingSource struct {
	bySelector map[string]*agentdb.Memory
}

func (f *fakeBriefingSource) NewestMemory(_ context.Context, project, selector string) (*agentdb.Memory, error) {
	if project == "" {
		return nil, agentdb.ErrMemoryNotFound
	}
	if m, ok := f.bySelector[selector]; ok {
		return m, nil
	}
	return nil, agentdb.ErrMemoryNotFound
}

// TestArchivistPromptInstructsRollingSummary is the test that would have found
// nothing before this ticket: the archivist's rendered system prompt must tell
// it to write a memory under the exact selector the default briefing reads for
// the worker whose conversation just finished. Before this ticket, nothing in
// the prompt named `rolling-summary` or `worker=` at all — the policy only
// ever produced `kind=summary, name=<slug>`.
func TestArchivistPromptInstructsRollingSummary(t *testing.T) {
	b, err := architectArchivistV1(t).Instantiate(Answers{
		ArchitectQuestionGoal: "run marketing for an art collective",
	})
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	_, archivist := b.Workers[0], b.Workers[1]

	prompt := archivist.SystemPrompt
	if !strings.Contains(prompt, "rolling-summary") {
		t.Fatalf("archivist prompt does not mention rolling-summary at all:\n%s", prompt)
	}
	if !strings.Contains(prompt, "worker=") {
		t.Fatalf("archivist prompt mentions rolling-summary but never tells it to label with worker=:\n%s", prompt)
	}
	// It must also be able to say WHICH worker: the worker.finished envelope
	// names it in the "From worker: " line of the rendered event
	// (agentkit.renderFirstMessage, compose.go), so the prompt must point the
	// archivist at that line rather than at the thread slug it invents itself.
	if !strings.Contains(prompt, "From worker") {
		t.Fatalf("archivist prompt tells it to label with worker= but never says where to find the subject worker's name:\n%s", prompt)
	}
}

// TestArchivistRollingSummaryReachesSubjectWorkersBriefing is the end-to-end
// acceptance case: a memory shaped exactly as the (fixed) archivist prompt
// instructs — kind=rolling-summary, worker=<the subject worker> — must be
// exactly what BuildBriefingSections' default section picks up for that
// worker. This is the wiring the prompt fix depends on; compose.go's side of
// it was never broken, but pinning it here means a future change to either
// side that breaks the agreement fails a test in this package too.
func TestArchivistRollingSummaryReachesSubjectWorkersBriefing(t *testing.T) {
	b, err := architectArchivistV1(t).Instantiate(Answers{
		ArchitectQuestionGoal:          "run marketing for an art collective",
		ArchitectQuestionArchitectName: "architect",
	})
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	architect := b.Workers[0]
	architect.Project = "acme"

	const summary = "Reconciled the roster with last week's brief; no changes needed."
	src := &fakeBriefingSource{bySelector: map[string]*agentdb.Memory{
		agentkit.RollingSummarySelector(architect.Name): {ID: "mem-1", Content: summary},
	}}

	sections := agentkit.BuildBriefingSections(context.Background(), src, "acme", &architect, nil)
	if len(sections) != 1 {
		t.Fatalf("sections = %d, want exactly 1 (the default rolling-summary section)", len(sections))
	}
	if sections[0].Heading != agentkit.DefaultBriefingHeading {
		t.Errorf("heading = %q, want the default heading %q", sections[0].Heading, agentkit.DefaultBriefingHeading)
	}
	if sections[0].Content != summary {
		t.Errorf("content = %q, want %q", sections[0].Content, summary)
	}

	// The historical bug, made concrete: a memory shaped like the OLD policy's
	// output (kind=summary, name=<slug>, no worker= label) still matches
	// nothing under the default selector. This is not a claim that compose.go
	// changed — it never needed to — but it is the failure this ticket closes
	// on the archivist side.
	oldShaped := &fakeBriefingSource{bySelector: map[string]*agentdb.Memory{
		"kind=summary,name=architect-thread": {ID: "mem-2", Content: summary},
	}}
	if got := agentkit.BuildBriefingSections(context.Background(), oldShaped, "acme", &architect, nil); len(got) != 0 {
		t.Errorf("old-shaped summary unexpectedly matched the default selector: %v", got)
	}
}
