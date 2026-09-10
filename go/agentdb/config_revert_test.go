package agentdb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// T22 — RevertEvent (design §D).
//
// The property under test throughout is that a revert is a FORWARD write: the
// log gets LONGER, the reverted record is still there, and the compensating
// change is an ordinary logged mutation with a reason.
// ---------------------------------------------------------------------------

const revertProject = "acme"

func revertStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	return newConfigLogTestStore(t), context.Background()
}

// newestFor returns the newest config event for one entity.
func newestFor(t *testing.T, s *Store, ctx context.Context, entity string) *ConfigEvent {
	t.Helper()
	evs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: revertProject, Entity: entity})
	if err != nil {
		t.Fatalf("list %s: %v", entity, err)
	}
	if len(evs) == 0 {
		t.Fatalf("no config events for %s", entity)
	}
	return evs[0]
}

func countEvents(t *testing.T, s *Store, ctx context.Context) int {
	t.Helper()
	evs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: revertProject})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return len(evs)
}

// A prompt rewrite reverted: the worker's prompt goes back, the bad record
// stays, and the log is one longer.
func TestRevertEvent_PromptWriteGoesBackForward(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "email-answerer")
	w.SystemPrompt = "Answer the question and stop."
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := s.SetWorkerPrompt(ctx, revertProject, "email-answerer",
		"Answer at length, with three examples.", ConfigWrite{Rationale: "more detail"}); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	bad := newestFor(t, s, ctx, "worker:email-answerer")
	before := countEvents(t, s, ctx)

	written, err := s.RevertEvent(ctx, revertProject, bad.ID, ConfigWrite{Rationale: "it got worse"})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}

	got, err := s.GetWorker(ctx, revertProject, "email-answerer")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if got.SystemPrompt != "Answer the question and stop." {
		t.Errorf("prompt was not put back: %q", got.SystemPrompt)
	}

	// Forward, never backward: the log grew and the reverted record survives.
	if after := countEvents(t, s, ctx); after != before+1 {
		t.Errorf("log length: want %d, got %d — a revert must APPEND", before+1, after)
	}
	if _, err := s.GetConfigEvent(ctx, revertProject, bad.ID); err != nil {
		t.Errorf("the reverted record must still exist: %v", err)
	}
	if written.Rationale != "it got worse" {
		t.Errorf("rationale: %q", written.Rationale)
	}
	if written.Seq <= bad.Seq {
		t.Errorf("the compensating record must come after the one it reverts: %d vs %d", written.Seq, bad.Seq)
	}
}

// A create with no predecessor reverts to a delete.
func TestRevertEvent_CreateRevertsToDelete(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "spare-hand")
	w.SystemPrompt = "Do the odd jobs."
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "hire"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	created := newestFor(t, s, ctx, "worker:spare-hand")

	if _, err := s.RevertEvent(ctx, revertProject, created.ID, ConfigWrite{Rationale: "hired in error"}); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if _, err := s.GetWorker(ctx, revertProject, "spare-hand"); !errors.Is(err, ErrWorkerNotFound) {
		t.Errorf("the worker should be gone: %v", err)
	}
}

// A delete reverts to a recreate — and a restored subscription keeps its
// ORIGINAL id, or it is not the same edge in the org chart.
func TestRevertEvent_DeleteRestoresTheRowWithItsID(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "answerer")
	w.SystemPrompt = "Answer."
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("worker: %v", err)
	}
	sub, err := s.CreateSubscription(ctx, &Subscription{
		Project: revertProject, EventType: "email.received", Worker: "answerer", Enabled: true,
	}, ConfigWrite{Rationale: "wire it"})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	originalID := sub.ID

	if err := s.DeleteSubscription(ctx, revertProject, originalID, ConfigWrite{Rationale: "too noisy"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	deleted := newestFor(t, s, ctx, "subscription:"+originalID)

	if _, err := s.RevertEvent(ctx, revertProject, deleted.ID, ConfigWrite{Rationale: "we need it after all"}); err != nil {
		t.Fatalf("revert: %v", err)
	}

	restored, err := s.GetSubscription(ctx, revertProject, originalID)
	if err != nil {
		t.Fatalf("the subscription was not restored: %v", err)
	}
	if restored.ID != originalID {
		t.Errorf("restored under a new id %q — the chart would draw a different edge", restored.ID)
	}
	if restored.EventType != "email.received" || restored.Worker != "answerer" {
		t.Errorf("restored row differs: %+v", restored)
	}
}

// Schedules take the same path, and the disabled/enabled flag has to survive.
func TestRevertEvent_ScheduleUpdate(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "sweeper")
	w.SystemPrompt = "Sweep."
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("worker: %v", err)
	}
	sch, err := s.CreateSchedule(ctx, &Schedule{
		Project: revertProject, Worker: "sweeper", Cron: "0 9 * * *", Input: "sweep", Enabled: false,
	}, ConfigWrite{Rationale: "nightly"})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	next := *sch
	next.Cron = "*/5 * * * *"
	next.Enabled = true
	if _, err := s.UpdateSchedule(ctx, &next, ConfigWrite{Rationale: "faster"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	bad := newestFor(t, s, ctx, "schedule:"+sch.ID)

	if _, err := s.RevertEvent(ctx, revertProject, bad.ID, ConfigWrite{Rationale: "too often"}); err != nil {
		t.Fatalf("revert: %v", err)
	}
	got, err := s.GetSchedule(ctx, revertProject, sch.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if got.Cron != "0 9 * * *" {
		t.Errorf("cron not put back: %q", got.Cron)
	}
	if got.Enabled {
		t.Error("the schedule was enabled by the change and must come back disabled")
	}
}

// Decision D2, and the assertion the whole design turns on: payloads are whole
// rows, so reverting a record that something else has since written over would
// silently erase that later change too.
func TestRevertEvent_RefusesANonNewestTarget(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "answerer")
	w.SystemPrompt = "v1"
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := s.SetWorkerPrompt(ctx, revertProject, "answerer", "v2", ConfigWrite{Rationale: "two"}); err != nil {
		t.Fatalf("v2: %v", err)
	}
	target := newestFor(t, s, ctx, "worker:answerer")
	if _, _, err := s.SetWorkerPrompt(ctx, revertProject, "answerer", "v3", ConfigWrite{Rationale: "three"}); err != nil {
		t.Fatalf("v3: %v", err)
	}

	before := countEvents(t, s, ctx)
	_, err := s.RevertEvent(ctx, revertProject, target.ID, ConfigWrite{Rationale: "back to v1"})
	if !errors.Is(err, ErrRevertRefused) {
		t.Fatalf("want ErrRevertRefused, got %v", err)
	}
	// The refusal has to NAME what is in the way, or the reader is stuck.
	if !strings.Contains(err.Error(), "worker_prompt_write") {
		t.Errorf("the refusal must name the intervening change: %v", err)
	}
	if !strings.Contains(err.Error(), "newest") {
		t.Errorf("the refusal must say why: %v", err)
	}
	if after := countEvents(t, s, ctx); after != before {
		t.Errorf("a refused revert must write NOTHING: %d → %d", before, after)
	}
	got, _ := s.GetWorker(ctx, revertProject, "answerer")
	if got.SystemPrompt != "v3" {
		t.Errorf("a refused revert must not touch the row: %q", got.SystemPrompt)
	}
}

// Reverting a revert is refused by exactly the same rule, and that is the
// documented answer: the way back is to revert the REVERT, which is newest.
func TestRevertEvent_RevertingTheSameEventTwiceIsRefused(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "answerer")
	w.SystemPrompt = "v1"
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := s.SetWorkerPrompt(ctx, revertProject, "answerer", "v2", ConfigWrite{Rationale: "two"}); err != nil {
		t.Fatalf("v2: %v", err)
	}
	target := newestFor(t, s, ctx, "worker:answerer")

	first, err := s.RevertEvent(ctx, revertProject, target.ID, ConfigWrite{Rationale: "back"})
	if err != nil {
		t.Fatalf("first revert: %v", err)
	}
	if _, err := s.RevertEvent(ctx, revertProject, target.ID, ConfigWrite{Rationale: "again"}); !errors.Is(err, ErrRevertRefused) {
		t.Fatalf("a second revert of the same record must be refused, got %v", err)
	}

	// And the way back works: revert the revert.
	if _, err := s.RevertEvent(ctx, revertProject, first.ID, ConfigWrite{Rationale: "actually keep v2"}); err != nil {
		t.Fatalf("revert of the revert: %v", err)
	}
	got, _ := s.GetWorker(ctx, revertProject, "answerer")
	if got.SystemPrompt != "v2" {
		t.Errorf("reverting the revert should restore v2, got %q", got.SystemPrompt)
	}
}

// A topology apply records a DECISION, not a row.
func TestRevertEvent_RefusesTopologyApply(t *testing.T) {
	s, ctx := revertStore(t)

	if _, err := s.ApplyTopology(ctx, TopologyApplication{
		Project:  revertProject,
		Topology: "solo@v1",
		Workers: []*Worker{
			{Name: "solo", Description: "the one", SystemPrompt: "work", MaxInstances: DefaultMaxInstances, Enabled: true},
		},
	}, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	applied := newestFor(t, s, ctx, "topology:solo@v1")

	_, err := s.RevertEvent(ctx, revertProject, applied.ID, ConfigWrite{Rationale: "undo it"})
	if !errors.Is(err, ErrRevertRefused) {
		t.Fatalf("want ErrRevertRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "decision") {
		t.Errorf("the refusal must explain what a topology_apply is: %v", err)
	}
}

// Another project's record is not found — the same answer as a record that
// does not exist, so an id cannot be probed.
func TestRevertEvent_AnotherProjectsEventIsNotFound(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker("other", "theirs")
	w.SystemPrompt = "not ours"
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	theirs, err := s.ListConfigEvents(ctx, ConfigEventQuery{Project: "other"})
	if err != nil || len(theirs) == 0 {
		t.Fatalf("seed events: %v", err)
	}

	if _, err := s.RevertEvent(ctx, revertProject, theirs[0].ID, ConfigWrite{Rationale: "nope"}); !errors.Is(err, ErrConfigEventNotFound) {
		t.Fatalf("want ErrConfigEventNotFound, got %v", err)
	}
}

// An empty rationale is filled in rather than left blank: a revert is the one
// change whose reason is always knowable.
func TestRevertEvent_DefaultsTheRationaleToWhatItReverted(t *testing.T) {
	s, ctx := revertStore(t)

	w := NewWorker(revertProject, "answerer")
	w.SystemPrompt = "v1"
	if _, err := s.UpsertWorker(ctx, w, ConfigWrite{Rationale: "seed"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := s.SetWorkerPrompt(ctx, revertProject, "answerer", "v2", ConfigWrite{Rationale: "two"}); err != nil {
		t.Fatalf("v2: %v", err)
	}
	target := newestFor(t, s, ctx, "worker:answerer")

	written, err := s.RevertEvent(ctx, revertProject, target.ID, ConfigWrite{})
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !strings.Contains(written.Rationale, target.ID) {
		t.Errorf("the default rationale must name what it reverted: %q", written.Rationale)
	}
}
