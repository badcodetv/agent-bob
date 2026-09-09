package agentdb

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Compare-and-swap on the `name=` convention (G13).
//
// The bug being fixed is not that memories are immutable — that is the design.
// It is that "the current value of x" had no write side: two workers that both
// read the message board, both rewrite it and both append were BOTH told they
// succeeded, and the loser never learned its work had been buried.
//
// Almost all of this is Postgres-only, and deliberately so: the guard is an
// advisory lock plus a WHERE clause on the INSERT, so a green `go test ./...`
// with no database proves nothing whatsoever about it. Run the real thing with:
//
//	AGENTKIT_TEST_POSTGRES_URL=postgres://... go test ./agentdb/ -run IfCurrent
//
// The offline tests at the bottom cover only what is provable without a
// database: argument rejection, the lock key, and the error's own words.
// ---------------------------------------------------------------------------

// TestIfCurrentLandsWhenTheNamedMemoryIsStillCurrent: the happy path. A caller
// that read the current value and writes it back with if_current gets exactly
// the behaviour of an ordinary append.
func TestIfCurrentLandsWhenTheNamedMemoryIsStillCurrent(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	first := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "Board: nothing in flight.",
		Labels: map[string]string{"kind": "document", MemoryNameLabel: "message-board"},
	}, nil)

	second, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{
		Project: p, Content: "Board: the newsletter is in review.",
		Labels: map[string]string{"kind": "document", MemoryNameLabel: "message-board"},
	}, nil, first.ID)
	if err != nil {
		t.Fatalf("compare-and-swap against the current memory must succeed: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("the write must be a NEW memory — compare-and-swap is not mutation")
	}
	// The read-back contract of CreateMemory holds here too: what came back is
	// what the database holds, not the caller's struct.
	if second.Content != "Board: the newsletter is in review." || second.Labels[MemoryNameLabel] != "message-board" {
		t.Fatalf("stored row = %+v", second)
	}
	// And the old value is untouched and still readable: nothing was overwritten.
	if got, err := s.GetMemory(ctx, p, first.ID); err != nil || got.Content != "Board: nothing in flight." {
		t.Fatalf("the previous version must remain intact: %v, %v", got, err)
	}
	if newest, err := s.NewestMemory(ctx, p, MemoryNameLabel+"=message-board"); err != nil || newest.ID != second.ID {
		t.Fatalf("newest = %v, %v; want the row we just wrote", newest, err)
	}
}

// TestIfCurrentRefusesAStaleID is the loser's side, sequentially: someone else
// wrote in between, so the write does not happen and the error names the winner.
func TestIfCurrentRefusesAStaleID(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	labels := map[string]string{MemoryNameLabel: "message-board"}
	stale := mustCreateMemory(t, s, &Memory{Project: p, Content: "v1", Labels: labels}, nil)
	winner := mustCreateMemory(t, s, &Memory{Project: p, Content: "v2 — somebody else", Labels: labels}, nil)

	_, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{
		Project: p, Content: "v2 — mine, based on v1", Labels: labels,
	}, nil, stale.ID)

	var conflict ErrMemoryNotCurrent
	if !errors.As(err, &conflict) {
		t.Fatalf("want ErrMemoryNotCurrent, got %v", err)
	}
	if conflict.Current != winner.ID {
		t.Fatalf("conflict names %q as current, want the winner %q", conflict.Current, winner.ID)
	}
	if conflict.IfCurrent != stale.ID || conflict.Name != "message-board" {
		t.Fatalf("conflict = %+v; it must carry back what the caller passed", conflict)
	}

	// NOTHING was written. This is the claim the error makes, and an error that
	// lied about it would be worse than no compare-and-swap at all.
	var n int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM memories WHERE project = ?", p).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("%d memories in the project, want the 2 that existed before the refused write", n)
	}
	// And the winner is still the winner.
	if newest, err := s.NewestMemory(ctx, p, MemoryNameLabel+"=message-board"); err != nil || newest.ID != winner.ID {
		t.Fatalf("newest = %v, %v; the refused write must not have changed it", newest, err)
	}
}

// TestIfCurrentConcurrentWritersExactlyOneWins is the test the whole ticket
// exists for. Two goroutines read the SAME current memory and both write with
// if_current set to it. Exactly one may land; the other must be told who won.
//
// A read-then-insert passes every sequential test above and fails this one:
// under Read Committed neither statement sees the other's uncommitted row, so
// both comparisons succeed and both writes land.
func TestIfCurrentConcurrentWritersExactlyOneWins(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	labels := map[string]string{MemoryNameLabel: "message-board"}
	base := mustCreateMemory(t, s, &Memory{Project: p, Content: "v1", Labels: labels}, nil)

	type outcome struct {
		stored *Memory
		err    error
	}
	results := make([]outcome, 2)
	// Both goroutines are released at once so the two transactions genuinely
	// overlap; without the barrier the race is usually won by whoever started.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			stored, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{
				Project: p, Content: "v2 by writer " + string(rune('A'+i)), Labels: labels,
			}, nil, base.ID)
			results[i] = outcome{stored: stored, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	var winners, losers []outcome
	for _, r := range results {
		if r.err == nil {
			winners = append(winners, r)
		} else {
			losers = append(losers, r)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("%d writers won, want exactly 1 — both wrote over the same current memory: %+v", len(winners), results)
	}
	if len(losers) != 1 {
		t.Fatalf("%d writers lost, want exactly 1: %+v", len(losers), results)
	}

	var conflict ErrMemoryNotCurrent
	if !errors.As(losers[0].err, &conflict) {
		t.Fatalf("the loser must get ErrMemoryNotCurrent, got %v", losers[0].err)
	}
	// The whole point: the loser can recover, which means being told WHICH
	// memory to re-read. Naming the id it already passed would be useless.
	if conflict.Current != winners[0].stored.ID {
		t.Fatalf("loser was told %q is current, but the winner wrote %q", conflict.Current, winners[0].stored.ID)
	}

	// Two rows: the base, plus exactly one of the two writes.
	var n int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM memories WHERE project = ?", p).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("%d memories in the project, want 2 (the base plus one winner)", n)
	}
}

// TestIfCurrentIgnoresRetractedMemories: a withdrawn memory is not the current
// value of anything (notRetractedSQL), and compare-and-swap must agree with
// NewestMemory about that — in both directions.
func TestIfCurrentIgnoresRetractedMemories(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	labels := map[string]string{MemoryNameLabel: "opening-hours"}
	older := mustCreateMemory(t, s, &Memory{Project: p, Content: "10am", Labels: labels}, nil)
	wrong := mustCreateMemory(t, s, &Memory{Project: p, Content: "11am — wrong", Labels: labels}, nil)
	mustCreateMemory(t, s, &Memory{
		Project: p, Content: "The 11am claim came from a draft flyer.",
		Labels: map[string]string{RetractionLabel: wrong.ID},
	}, nil)

	// A retracted row cannot win the comparison, even though it is the newest
	// row by timestamp — otherwise a withdrawal would freeze the name for ever.
	_, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{Project: p, Content: "12pm", Labels: labels}, nil, wrong.ID)
	var conflict ErrMemoryNotCurrent
	if !errors.As(err, &conflict) {
		t.Fatalf("a retracted memory must not be current, got %v", err)
	}
	if conflict.Current != older.ID {
		t.Fatalf("conflict names %q; with the newest row retracted, current is %q", conflict.Current, older.ID)
	}

	// And the row the reader WOULD get is the row that swaps successfully.
	if _, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{Project: p, Content: "12pm", Labels: labels}, nil, older.ID); err != nil {
		t.Fatalf("compare-and-swap against the current (unretracted) memory: %v", err)
	}
}

// TestIfCurrentIsProjectScoped: the comparison never reaches across projects.
// P5 is not a filter anyone may forget here either — a memory id from another
// project must lose, not win.
func TestIfCurrentIsProjectScoped(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	mine := newLiveProject(t, s)
	theirs := newLiveProject(t, s)

	labels := map[string]string{MemoryNameLabel: "message-board"}
	elsewhere := mustCreateMemory(t, s, &Memory{Project: theirs, Content: "their board", Labels: labels}, nil)
	ours := mustCreateMemory(t, s, &Memory{Project: mine, Content: "our board", Labels: labels}, nil)

	_, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{Project: mine, Content: "v2", Labels: labels}, nil, elsewhere.ID)
	var conflict ErrMemoryNotCurrent
	if !errors.As(err, &conflict) {
		t.Fatalf("an id from another project must not satisfy the comparison, got %v", err)
	}
	if conflict.Current != ours.ID {
		t.Fatalf("conflict names %q, want this project's current %q", conflict.Current, ours.ID)
	}
}

// TestIfCurrentWhenNothingCarriesTheName: the caller named an id as current for
// a name nothing holds. It loses, and the error says so with an empty Current
// rather than inventing one — a distinct enough situation that the tool layer
// gives it its own sentence.
func TestIfCurrentWhenNothingCarriesTheName(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	other := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "unrelated", Labels: map[string]string{"kind": "lesson"},
	}, nil)

	_, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{
		Project: p, Content: "v1", Labels: map[string]string{MemoryNameLabel: "message-board"},
	}, nil, other.ID)

	var conflict ErrMemoryNotCurrent
	if !errors.As(err, &conflict) {
		t.Fatalf("want ErrMemoryNotCurrent, got %v", err)
	}
	if conflict.Current != "" {
		t.Fatalf("nothing carries the name, so Current must be empty, got %q", conflict.Current)
	}
}

// TestIfCurrentNeedsANameLabel: compare-and-swap is defined against the `name=`
// convention and has no meaning without it. A caller that asks to swap without
// naming what it is swapping is refused — because the only other option is to
// append unconditionally, which is exactly the race it was guarding against.
func TestIfCurrentNeedsANameLabel(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	other := mustCreateMemory(t, s, &Memory{
		Project: p, Content: "unrelated", Labels: map[string]string{"kind": "lesson"},
	}, nil)

	_, _, err := s.CreateMemoryIfCurrent(ctx, &Memory{
		Project: p, Content: "no name here", Labels: map[string]string{"kind": "lesson"},
	}, nil, other.ID)
	if err == nil {
		t.Fatal("want an error: if_current without a name label must not fall back to an append")
	}
	var conflict ErrMemoryNotCurrent
	if errors.As(err, &conflict) {
		t.Fatalf("a missing name label is a caller error, not a lost race: %v", err)
	}
	if !strings.Contains(err.Error(), MemoryNameLabel) {
		t.Fatalf("the message must name the missing label: %v", err)
	}

	var n int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM memories WHERE project = ?", p).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d memories, want 1 — the refused write must not have landed", n)
	}
}

// TestPlainCreateMemoryIsUnchangedByIfCurrent guards the promise made to every
// existing caller: CreateMemory takes no lock, runs no transaction and appends
// unconditionally, whatever names are in play.
func TestPlainCreateMemoryIsUnchangedByIfCurrent(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	p := newLiveProject(t, s)

	labels := map[string]string{MemoryNameLabel: "message-board"}
	mustCreateMemory(t, s, &Memory{Project: p, Content: "v1", Labels: labels}, nil)
	second := mustCreateMemory(t, s, &Memory{Project: p, Content: "v2", Labels: labels}, nil)

	if newest, err := s.NewestMemory(ctx, p, MemoryNameLabel+"=message-board"); err != nil || newest.ID != second.ID {
		t.Fatalf("an unconditional append must always land: %v, %v", newest, err)
	}
}

// ---------------------------------------------------------------------------
// Offline: what needs no database.
// ---------------------------------------------------------------------------

// TestIfCurrentRejectsAnEmptyID: the refusal happens before any database
// round-trip, so it is provable on the sqlite test store — and it exists
// because the alternative is a SILENT unconditional append, which is precisely
// the race the caller was trying to avoid, performed in its name.
func TestIfCurrentRejectsAnEmptyID(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.CreateMemoryIfCurrent(context.Background(), &Memory{
		Project: "p", Content: "x", Labels: map[string]string{MemoryNameLabel: "board"},
	}, nil, "   ")
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, ErrMemoryRequiresPostgres) {
		t.Fatal("the argument must be rejected before the dialect check, so the message names the real problem")
	}
	if !strings.Contains(err.Error(), "if_current") {
		t.Fatalf("the message must name the argument at fault: %v", err)
	}
}

// TestMemoryNameLockKeyIsNamespacedAndStable pins the advisory-lock key's shape.
// Advisory keys share ONE namespace across the whole database, so two things
// that must not block each other must not hash to the same number: the prefix,
// the project and the name all have to be part of the input. A key that stopped
// depending on one of them would still pass every test above — and would either
// serialise the whole database on one lock, or stop excluding anything.
func TestMemoryNameLockKeyIsNamespacedAndStable(t *testing.T) {
	base := memoryNameLockKey("proj", "board")
	if base < 0 {
		t.Fatalf("key must be positive (63-bit masked), got %d", base)
	}
	if base == memoryNameLockKey("proj", "other") {
		t.Fatal("two names in one project must not share a key")
	}
	if base == memoryNameLockKey("other", "board") {
		t.Fatal("the same name in two projects must not share a key")
	}
	if base != memoryNameLockKey("proj", "board") {
		t.Fatal("the key must be deterministic")
	}
	// The separator matters: without it ("proj","board") and ("pro","jboard")
	// would be the same lock.
	if memoryNameLockKey("proj", "board") == memoryNameLockKey("pro", "jboard") {
		t.Fatal("project and name must be separated in the hash input")
	}
	// And it must not collide with the migration lock, which is the one other
	// advisory key this package takes.
	if base == migrationLockKey {
		t.Fatal("a memory name must never collide with the migration lock")
	}
}

// TestErrMemoryNotCurrentSaysNothingWasWritten: the error is read by a caller
// deciding what to do next, so it has to carry the winner and has to be
// unambiguous that the write did not happen.
func TestErrMemoryNotCurrentSaysNothingWasWritten(t *testing.T) {
	e := ErrMemoryNotCurrent{Name: "board", IfCurrent: "mem-1", Current: "mem-2"}
	msg := e.Error()
	for _, want := range []string{"mem-1", "mem-2", "board", "nothing was written"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q must mention %q", msg, want)
		}
	}
	empty := ErrMemoryNotCurrent{Name: "board", IfCurrent: "mem-1"}.Error()
	if !strings.Contains(empty, "no unretracted memory") {
		t.Fatalf("with no current memory the error must say so: %q", empty)
	}
	// errors.As over a VALUE receiver, which is how every caller matches it.
	var got ErrMemoryNotCurrent
	if !errors.As(error(e), &got) || got.Current != "mem-2" {
		t.Fatalf("errors.As must recover the conflict: %+v", got)
	}
}
