package agentdb

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// Retraction (§7.1): how an append-only store takes something back.
//
// A memory labelled `retracts=<id>` withdraws the memory with that id from every
// SELECTION path — briefings and search — without deleting a row. Before this,
// `retracts` was a label that nothing anywhere consulted: a project could write
// the withdrawal, see it stored, and go on being briefed by the wrong fact for
// ever. These tests exist so that cannot silently return.
//
// Live-Postgres only, like every memory test: the filter is one correlated
// NOT EXISTS in SQL, so a green `go test ./...` with no database proves nothing
// about it. Run with:
//
//	AGENTKIT_TEST_POSTGRES_URL=postgres://... go test ./agentdb/ -run Retract
// ---------------------------------------------------------------------------

// TestRetractionHidesAMemoryFromEverySelectionPath is the whole contract in one
// test: the wrong fact stops reaching briefings (NewestMemory), stops reaching
// search by recency and stops reaching search by relevance — while its row, and
// the retraction that withdrew it, both remain readable.
func TestRetractionHidesAMemoryFromEverySelectionPath(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	wrong := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "The gallery opens at 10am on Sundays.",
		Labels: map[string]string{"kind": "fact", "name": "opening-hours"},
	}, unitVector(1))

	// Before: it is the current value of its name, and it is findable.
	if got, err := s.NewestMemory(ctx, p, "kind=fact,name=opening-hours"); err != nil || got.ID != wrong.ID {
		t.Fatalf("precondition: newest = %v, %v; want the memory we just wrote", got, err)
	}

	retraction := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Wrong: Sunday opening was never confirmed. Source was a draft flyer.",
		Labels: map[string]string{"kind": "retraction", RetractionLabel: wrong.ID},
	}, unitVector(2))

	// 1. Briefings. This is the one that matters most — it is the only memory
	//    read core itself performs, once per selector, at job composition.
	if _, err := s.NewestMemory(ctx, p, "kind=fact,name=opening-hours"); !errors.Is(err, ErrMemoryNotFound) {
		t.Errorf("a retracted memory is still the current value of its selector: err = %v, want ErrMemoryNotFound", err)
	}

	// 2. Search by recency (no query text).
	res, err := s.SearchMemories(ctx, &MemorySearchQuery{Project: p, LabelSelector: "kind=fact"})
	if err != nil {
		t.Fatalf("SearchMemories (recency): %v", err)
	}
	for _, r := range res {
		if r.ID == wrong.ID {
			t.Errorf("a retracted memory came back from the recency leg")
		}
	}

	// 3. Search by relevance — a retracted memory must not be able to return by
	//    scoring well, which is why the filter lives in the hard WHERE and not
	//    in a post-filter over the results.
	res, err = s.SearchMemories(ctx, &MemorySearchQuery{
		Project: p, Query: "gallery opening hours Sundays", QueryEmbedding: unitVector(1),
	})
	if err != nil {
		t.Fatalf("SearchMemories (hybrid): %v", err)
	}
	for _, r := range res {
		if r.ID == wrong.ID {
			t.Errorf("a retracted memory came back from the hybrid leg with score %v", r.Score)
		}
	}

	// 4. Nothing was deleted. The row is still there, still attributed, and the
	//    retraction explaining it is still selectable — that is the point of
	//    doing this with a label instead of a DELETE.
	if got, err := s.GetMemory(ctx, p, wrong.ID); err != nil || got.Content == "" {
		t.Errorf("the retracted row must remain readable by id: %v, %v", got, err)
	}
	if got, err := s.GetMemory(ctx, p, retraction.ID); err != nil || got.Labels[RetractionLabel] != wrong.ID {
		t.Errorf("the retraction itself must remain readable and carry its target: %v, %v", got, err)
	}
}

// TestRetractionUncoversTheMemoryBeneathIt is the reason retraction beats
// "write a correction beside it": once the wrong value is withdrawn, the
// selector resolves to the previous good value rather than to nothing. A project
// that mis-learned something returns to what it knew before.
func TestRetractionUncoversTheMemoryBeneathIt(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	sel := "kind=playbook,name=posting-cadence"
	good := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Post three times a week.",
		Labels: map[string]string{"kind": "playbook", "name": "posting-cadence"},
	}, unitVector(3))
	bad := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Post forty times a day.",
		Labels: map[string]string{"kind": "playbook", "name": "posting-cadence"},
	}, unitVector(3))

	if got, err := s.NewestMemory(ctx, p, sel); err != nil || got.ID != bad.ID {
		t.Fatalf("precondition: newest must be the bad advice: %v, %v", got, err)
	}

	mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Retracting the 40/day cadence: it came from a misread benchmark.",
		Labels: map[string]string{"kind": "retraction", RetractionLabel: bad.ID},
	}, unitVector(4))

	got, err := s.NewestMemory(ctx, p, sel)
	if err != nil {
		t.Fatalf("NewestMemory after retraction: %v", err)
	}
	if got.ID != good.ID {
		t.Errorf("selector resolved to %q, want the good value that preceded the retracted one (%q)", got.ID, good.ID)
	}
}

// TestRetractionIsProjectLocal pins P5 at the SQL level. The NOT EXISTS clause
// correlates on the row's own project rather than binding one, so a retraction
// written in one project must not be able to withdraw a memory in another — the
// failure mode a shared `retracts` id would otherwise create.
func TestRetractionIsProjectLocal(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	victim := newLiveProject(t, s)
	attacker := newLiveProject(t, s)

	m := mustCreateMemory(t, s, &Memory{
		Project: victim, Content: "Budget is £2,000/month.",
		Labels: map[string]string{"kind": "fact", "name": "budget"},
	}, unitVector(5))

	// A memory in a DIFFERENT project naming the victim's id.
	mustCreateMemory(t, s, &Memory{
		Project: attacker, Content: "Retract the neighbour's budget.",
		Labels: map[string]string{"kind": "retraction", RetractionLabel: m.ID},
	}, unitVector(6))

	got, err := s.NewestMemory(ctx, victim, "kind=fact,name=budget")
	if err != nil {
		t.Fatalf("a cross-project retraction withdrew a memory it does not own: %v", err)
	}
	if got.ID != m.ID {
		t.Errorf("newest = %q, want %q", got.ID, m.ID)
	}
}

// TestRetractionOfAnUnknownIdIsInert covers the ordinary mistake: a typo'd or
// already-gone id must withdraw nothing and fail nothing. Retraction is a filter
// over rows that exist, not an assertion that one does.
func TestRetractionOfAnUnknownIdIsInert(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	keep := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Keep me.", Labels: map[string]string{"kind": "fact"},
	}, unitVector(7))
	mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Retracts nothing that exists.",
		Labels: map[string]string{"kind": "retraction", RetractionLabel: "no-such-memory-id"},
	}, unitVector(8))

	got, err := s.NewestMemory(ctx, p, "kind=fact")
	if err != nil || got.ID != keep.ID {
		t.Errorf("an inert retraction disturbed the store: %v, %v", got, err)
	}
}

// ---------------------------------------------------------------------------
// include_retracted (O11 of design/2026-08-20-agent-wolf.md): the audit view.
//
// Everything above is about a retraction being HONOURED. These are about it
// being VISIBLE. `retracts` is an ordinary label, so anything holding the core
// MCP tools can withdraw any row in its project — including one an embedding
// application wrote as its own authoritative state. With the filter always on,
// that erasure is indistinguishable from the row never having existed, and no
// reader can raise the alarm.
//
// IncludeRetracted lifts the filter for a caller that has already been checked
// as trusted at the HTTP boundary, and hands back every retraction of every row
// it returns, each with its own provenance — which is the fact a reader needs:
// a retraction written from inside a container carries a worker or a session,
// and one written by the application itself carries neither.
// ---------------------------------------------------------------------------

// retractedByIDs is the ids of the retracting memories, in the order returned.
func retractedByIDs(r *MemorySearchResult) []string {
	out := make([]string, len(r.RetractedBy))
	for i, x := range r.RetractedBy {
		out[i] = x.MemoryID
	}
	return out
}

func findResult(res []*MemorySearchResult, id string) *MemorySearchResult {
	for _, r := range res {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// TestRetractionLivePG_IncludeRetractedRevealsWithdrawnRows is the flag's whole
// point, asserted on every leg the hard filter governs: with it, the withdrawn
// row comes back — carrying who withdrew it — from the recency leg and from the
// relevance legs alike; without it, nothing changes at all.
func TestRetractionLivePG_IncludeRetractedRevealsWithdrawnRows(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	wrong := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "The gallery opens at 10am on Sundays.",
		Labels: map[string]string{"kind": "fact", "name": "opening-hours"},
	}, unitVector(1))
	retraction := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Retracting the Sunday hours.",
		Labels: map[string]string{"kind": "fact", RetractionLabel: wrong.ID},
		// Provenance from inside a container: exactly what a trusted reader
		// looks at to decide the withdrawal is not the application's own word.
		CreatedByWorker: "researcher", CreatedBySession: "sess-9",
	}, unitVector(2))

	// 1. Default: unchanged. The withdrawn row is gone from the recency leg and
	//    NOTHING carries a retracted_by.
	res, err := s.SearchMemories(ctx, &MemorySearchQuery{Project: p, LabelSelector: "kind=fact"})
	if err != nil {
		t.Fatalf("SearchMemories (default): %v", err)
	}
	if findResult(res, wrong.ID) != nil {
		t.Fatalf("the default must still hide a retracted memory: %v", resultIDs(res))
	}
	for _, r := range res {
		if r.RetractedBy != nil {
			t.Errorf("no result may carry retractions when the flag is absent: %+v", r.RetractedBy)
		}
	}

	// 2. Recency leg, flag on.
	res, err = s.SearchMemories(ctx, &MemorySearchQuery{
		Project: p, LabelSelector: "kind=fact", IncludeRetracted: true,
	})
	if err != nil {
		t.Fatalf("SearchMemories (include_retracted): %v", err)
	}
	got := findResult(res, wrong.ID)
	if got == nil {
		t.Fatalf("include_retracted must return the withdrawn row: %v", resultIDs(res))
	}
	if want := []string{retraction.ID}; !reflect.DeepEqual(retractedByIDs(got), want) {
		t.Fatalf("retracted_by = %v, want %v", retractedByIDs(got), want)
	}
	r0 := got.RetractedBy[0]
	if r0.CreatedByWorker != "researcher" || r0.CreatedBySession != "sess-9" {
		t.Errorf("the retraction's OWN provenance is the whole point: %+v", r0)
	}
	if r0.CreatedAt != retraction.CreatedAt {
		t.Errorf("created_at = %d, want the retraction's own %d (unix milliseconds)", r0.CreatedAt, retraction.CreatedAt)
	}
	// A row nobody withdrew omits the field entirely, even with the flag on.
	if other := findResult(res, retraction.ID); other == nil || other.RetractedBy != nil {
		t.Errorf("an unretracted row must carry no retractions: %+v", other)
	}

	// 3. The relevance legs, flag on — the filter is part of the HARD clause,
	//    so lifting it must lift it for keyword+vector fusion too.
	res, err = s.SearchMemories(ctx, &MemorySearchQuery{
		Project: p, Query: "gallery opening hours Sundays", QueryEmbedding: unitVector(1),
		IncludeRetracted: true,
	})
	if err != nil {
		t.Fatalf("SearchMemories (hybrid, include_retracted): %v", err)
	}
	if got := findResult(res, wrong.ID); got == nil || len(got.RetractedBy) != 1 {
		t.Fatalf("the hybrid leg must return the withdrawn row with its retraction: %+v", got)
	}
}

// TestRetractionLivePG_IncludeRetractedReturnsEveryRetraction pins owner
// decision B5. Returning only the NEWEST retraction is a resurrection attack:
// an attacker appends its own untrusted retraction on top of the application's
// legitimate one, the reader discards the untrusted retractor and concludes the
// row was never withdrawn — and state that was properly taken back comes back.
func TestRetractionLivePG_IncludeRetractedReturnsEveryRetraction(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	// Explicit, distinct timestamps: CreateMemory stamps time.Now().UnixMilli()
	// when CreatedAt is zero, so two retractions appended back-to-back land in
	// the same millisecond and "newest first" degrades to a random-UUID
	// tiebreak. The ordering this test is about must be decided by the field
	// the criterion names.
	base := int64(1_700_000_000_000)

	target := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Hypothesis A is live.",
		Labels:    map[string]string{"kind": "state", "name": "hyp-a"},
		CreatedAt: base,
	}, nil)

	// The application's own withdrawal: EMPTY provenance.
	mine := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Withdrawn by the application.",
		Labels:    map[string]string{"kind": "retraction", RetractionLabel: target.ID},
		CreatedAt: base + 1000,
	}, nil)
	// And an attacker's, appended afterwards from inside a container.
	theirs := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Ignore that; the hypothesis is fine.",
		Labels:          map[string]string{"kind": "retraction", RetractionLabel: target.ID},
		CreatedByWorker: "researcher", CreatedBySession: "sess-evil",
		CreatedAt: base + 2000,
	}, nil)

	res, err := s.SearchMemories(ctx, &MemorySearchQuery{
		Project: p, LabelSelector: "kind=state", IncludeRetracted: true,
	})
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	got := findResult(res, target.ID)
	if got == nil {
		t.Fatalf("the withdrawn row is missing: %v", resultIDs(res))
	}
	// Newest first, and BOTH present — the trusted reader asks "does ANY
	// retraction of this row have empty provenance?", which it cannot ask of a
	// single newest one.
	if want := []string{theirs.ID, mine.ID}; !reflect.DeepEqual(retractedByIDs(got), want) {
		t.Fatalf("retracted_by = %v, want every retraction newest-first %v", retractedByIDs(got), want)
	}
	if got.RetractedBy[0].CreatedBySession != "sess-evil" {
		t.Errorf("the newest retraction lost its provenance: %+v", got.RetractedBy[0])
	}
	if got.RetractedBy[1].CreatedByWorker != "" || got.RetractedBy[1].CreatedBySession != "" {
		t.Errorf("the application's own retraction must still read as provenance-free: %+v", got.RetractedBy[1])
	}
}

// TestRetractionLivePG_IncludeRetractedParticipatesInLatestPer is the paired
// contract W5's tamper detection is written against. IncludeRetracted belongs
// to the HARD filter, which is also what feeds the latest_per pre-reduction —
// so with the flag a retracted row can WIN its name's slot, and without it the
// older row beneath it wins instead. Two competent readings of "include
// retracted rows" answer this request differently; this test picks one.
func TestRetractionLivePG_IncludeRetractedParticipatesInLatestPer(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	// Explicit, distinct timestamps: which of the two rows is "latest" for the
	// name is the whole contract here, so it must be pinned by data and not by
	// how long two back-to-back inserts happen to take.
	base := int64(1_700_000_000_000)

	older := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Hypothesis A: proposed.",
		Labels:    map[string]string{"kind": "state", "name": "hyp-a"},
		CreatedAt: base,
	}, nil)
	newer := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Hypothesis A: live.",
		Labels:    map[string]string{"kind": "state", "name": "hyp-a"},
		CreatedAt: base + 1000,
	}, nil)
	mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Withdrawing the live status.",
		Labels:          map[string]string{"kind": "retraction", RetractionLabel: newer.ID},
		CreatedByWorker: "researcher", CreatedBySession: "sess-evil",
		CreatedAt: base + 2000,
	}, nil)

	with, err := s.SearchMemories(ctx, &MemorySearchQuery{
		Project: p, LabelSelector: "kind=state", LatestPer: "name", IncludeRetracted: true,
	})
	if err != nil {
		t.Fatalf("SearchMemories (latest_per, include_retracted): %v", err)
	}
	if want := []string{newer.ID}; !reflect.DeepEqual(resultIDs(with), want) {
		t.Fatalf("latest_per with the flag = %v, want the retracted row %v", resultIDs(with), want)
	}
	if len(with[0].RetractedBy) != 1 {
		t.Fatalf("the winning row must say it was withdrawn: %+v", with[0])
	}

	without, err := s.SearchMemories(ctx, &MemorySearchQuery{
		Project: p, LabelSelector: "kind=state", LatestPer: "name",
	})
	if err != nil {
		t.Fatalf("SearchMemories (latest_per): %v", err)
	}
	if want := []string{older.ID}; !reflect.DeepEqual(resultIDs(without), want) {
		t.Fatalf("latest_per without the flag = %v, want the row beneath it %v", resultIDs(without), want)
	}
	if without[0].RetractedBy != nil {
		t.Errorf("no retractions may ride along when the flag is absent: %+v", without[0].RetractedBy)
	}
}

// TestRetractionLivePG_IncludeRetractedLeavesTheSingleRowReadsAlone. The flag is
// a read-side AUDIT facility on search, not a new default: briefings compose
// from NewestMemory, and if retraction stopped applying there a withdrawn fact
// would be back in front of a model. GetMemory was already deliberately
// unfiltered and gains nothing.
func TestRetractionLivePG_IncludeRetractedLeavesTheSingleRowReadsAlone(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	wrong := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "The gallery opens at 10am on Sundays.",
		Labels: map[string]string{"kind": "fact", "name": "opening-hours"},
	}, nil)
	mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Wrong: never confirmed.",
		Labels: map[string]string{"kind": "retraction", RetractionLabel: wrong.ID},
	}, nil)

	if _, err := s.NewestMemory(ctx, p, "kind=fact,name=opening-hours"); !errors.Is(err, ErrMemoryNotFound) {
		t.Errorf("NewestMemory must keep honouring retraction: err = %v, want ErrMemoryNotFound", err)
	}
	got, err := s.GetMemory(ctx, p, wrong.ID)
	if err != nil || got.ID != wrong.ID {
		t.Fatalf("GetMemory must keep returning the withdrawn row: %+v, %v", got, err)
	}
	if got.Content != "The gallery opens at 10am on Sundays." {
		t.Errorf("GetMemory content changed: %q", got.Content)
	}
}
