package main

// datasetreaper_test.go covers what the ticket calls out as untested in an
// earlier revision: not just "the version reaper ran", but the orphan sweep
// itself — the prefix re-assertion, the disabled-interval no-op, context
// cancellation, and that one failed delete does not abort the rest of the
// pass.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// fakeDatasetReapStore is a scriptable stand-in for *agentdb.Store's two
// reaper methods.
type fakeDatasetReapStore struct {
	mu sync.Mutex

	reapCalls    int
	reapKeep     int
	reapReturn   int
	reapErr      error
	orphanReturn []string
	orphanErr    error
}

func (f *fakeDatasetReapStore) ReapDatasetVersions(ctx context.Context, keepPerName int, blobs agentdb.DatasetBlobDeleter) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reapCalls++
	f.reapKeep = keepPerName
	return f.reapReturn, f.reapErr
}

func (f *fakeDatasetReapStore) ListOrphanBlobPaths(ctx context.Context, blobs agentdb.DatasetBlobLister, minAge time.Duration) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.orphanReturn))
	copy(out, f.orphanReturn)
	return out, f.orphanErr
}

// fakeDatasetReapBlobs is a scriptable stand-in for the process-wide
// extension.BlobStore, narrowed to Delete + List.
type fakeDatasetReapBlobs struct {
	mu sync.Mutex

	deleted     []string
	failOn      map[string]bool // keys that error when deleted
	deleteCalls int
}

func (f *fakeDatasetReapBlobs) Delete(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.failOn[key] {
		return errDatasetReapTestDelete
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeDatasetReapBlobs) List(ctx context.Context, prefix string) ([]string, error) {
	return nil, nil // never used directly by the reaper; the store owns listing
}

var errDatasetReapTestDelete = &fakeDeleteError{}

type fakeDeleteError struct{}

func (*fakeDeleteError) Error() string { return "fake delete failure" }

// fakeClock lets a test advance datasetReaper.now without a real sleep.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1700000000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestDatasetReapSweep_CallsReapAndLogsOneSummaryLine: the version reaper is
// invoked with the configured keep count, and one summary line is logged
// naming both counts, whatever they are — the pinned log shape from the
// ticket text.
func TestDatasetReapSweep_CallsReapAndLogsOneSummaryLine(t *testing.T) {
	store := &fakeDatasetReapStore{reapReturn: 3}
	blobs := &fakeDatasetReapBlobs{}
	var lines []string
	r := newDatasetReaper(store, blobs, 30, func(f string, a ...any) {
		lines = append(lines, fmt.Sprintf(f, a...))
	})
	r.Sweep(context.Background())

	if store.reapCalls != 1 {
		t.Fatalf("ReapDatasetVersions calls = %d, want 1", store.reapCalls)
	}
	if store.reapKeep != 30 {
		t.Fatalf("ReapDatasetVersions keepPerName = %d, want 30", store.reapKeep)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "reaped 3 versions") && strings.Contains(l, "swept 0 orphan blobs") && strings.Contains(l, "0 delete failures") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no summary line matched the pinned shape; got %v", lines)
	}
}

// TestDatasetReapSweep_OrphanNeedsTwoPassesAtLeastMinAgeApart: a path
// reported orphaned on one pass is NOT deleted; reported again before minAge
// has elapsed, still not deleted; reported again at/after minAge, deleted.
func TestDatasetReapSweep_OrphanNeedsTwoPassesAtLeastMinAgeApart(t *testing.T) {
	const path = agentdb.DatasetBlobPrefix + "abc"
	store := &fakeDatasetReapStore{orphanReturn: []string{path}}
	blobs := &fakeDatasetReapBlobs{}
	clock := newFakeClock()
	r := newDatasetReaper(store, blobs, 30, func(string, ...any) {})
	r.now = clock.now

	r.Sweep(context.Background()) // pass 1: first sighting
	if len(blobs.deleted) != 0 {
		t.Fatalf("deleted on first sighting: %v", blobs.deleted)
	}

	clock.advance(30 * time.Minute)
	r.Sweep(context.Background()) // pass 2: too soon
	if len(blobs.deleted) != 0 {
		t.Fatalf("deleted before minAge elapsed: %v", blobs.deleted)
	}

	clock.advance(31 * time.Minute) // now 61m since first sighting
	r.Sweep(context.Background())   // pass 3: minAge has elapsed
	if len(blobs.deleted) != 1 || blobs.deleted[0] != path {
		t.Fatalf("deleted = %v, want exactly [%s]", blobs.deleted, path)
	}
}

// TestDatasetReapSweep_ForgetsAPathThatStopsBeingOrphaned: a path seen once,
// then absent from the next orphan list (something referenced it again)
// must never be deleted even after minAge, because the two-pass memory is
// cleared, not just aged.
func TestDatasetReapSweep_ForgetsAPathThatStopsBeingOrphaned(t *testing.T) {
	const path = agentdb.DatasetBlobPrefix + "abc"
	store := &fakeDatasetReapStore{orphanReturn: []string{path}}
	blobs := &fakeDatasetReapBlobs{}
	clock := newFakeClock()
	r := newDatasetReaper(store, blobs, 30, func(string, ...any) {})
	r.now = clock.now

	r.Sweep(context.Background()) // first sighting

	store.orphanReturn = nil // the row got written; no longer orphaned
	clock.advance(2 * time.Hour)
	r.Sweep(context.Background())

	store.orphanReturn = []string{path} // orphaned again, freshly
	clock.advance(2 * time.Hour)
	r.Sweep(context.Background()) // this is a FIRST sighting again
	if len(blobs.deleted) != 0 {
		t.Fatalf("deleted a path whose orphan memory should have been cleared: %v", blobs.deleted)
	}
}

// TestDatasetReapSweep_NeverDeletesAPathOutsideTheDatasetPrefix: the
// belt-and-braces re-assertion. Even when the fake store insists a path is
// orphaned and old enough, a path outside agentdb.DatasetBlobPrefix (e.g. one
// under the artifacts namespace) must never reach Delete — agentd shares one
// global BlobStore with artifacts and snapshots.
func TestDatasetReapSweep_NeverDeletesAPathOutsideTheDatasetPrefix(t *testing.T) {
	const foreign = "_artifacts/bytes/should-never-be-touched"
	store := &fakeDatasetReapStore{orphanReturn: []string{foreign}}
	blobs := &fakeDatasetReapBlobs{}
	clock := newFakeClock()
	r := newDatasetReaper(store, blobs, 30, func(string, ...any) {})
	r.now = clock.now

	r.Sweep(context.Background())
	clock.advance(2 * time.Hour)
	r.Sweep(context.Background())
	clock.advance(2 * time.Hour)
	r.Sweep(context.Background())

	if len(blobs.deleted) != 0 {
		t.Fatalf("deleted a path outside DatasetBlobPrefix: %v", blobs.deleted)
	}
	if blobs.deleteCalls != 0 {
		t.Fatalf("Delete was called at all for a foreign-prefix path: %d calls", blobs.deleteCalls)
	}
}

// TestDatasetReapSweep_DeleteErrorDoesNotAbortThePass: two orphans past
// minAge, one whose delete fails — the other still gets deleted, the version
// reap still ran, and the failure is counted rather than fatal.
func TestDatasetReapSweep_DeleteErrorDoesNotAbortThePass(t *testing.T) {
	bad := agentdb.DatasetBlobPrefix + "bad"
	good := agentdb.DatasetBlobPrefix + "good"
	store := &fakeDatasetReapStore{reapReturn: 1, orphanReturn: []string{bad, good}}
	blobs := &fakeDatasetReapBlobs{failOn: map[string]bool{bad: true}}
	clock := newFakeClock()
	var failuresLogged int
	r := newDatasetReaper(store, blobs, 10, func(f string, a ...any) {
		line := fmt.Sprintf(f, a...)
		if strings.Contains(line, "1 delete failures") {
			failuresLogged++
		}
	})
	r.now = clock.now

	r.Sweep(context.Background()) // first sighting of both
	clock.advance(2 * time.Hour)
	r.Sweep(context.Background()) // both old enough; bad fails, good succeeds

	if len(blobs.deleted) != 1 || blobs.deleted[0] != good {
		t.Fatalf("deleted = %v, want exactly [%s]", blobs.deleted, good)
	}
	if store.reapCalls != 2 {
		t.Fatalf("version reap must still run every pass regardless of orphan delete failures: %d calls", store.reapCalls)
	}
	if failuresLogged == 0 {
		t.Fatalf("no summary line reported the 1 delete failure")
	}
}

// TestDatasetReapRun_ZeroIntervalStartsNoTicker: interval <= 0 means Run
// returns immediately without ever calling Sweep — the loop is simply never
// started, matching AGENTKIT_DATASET_REAP_INTERVAL=off.
func TestDatasetReapRun_ZeroIntervalStartsNoTicker(t *testing.T) {
	store := &fakeDatasetReapStore{}
	blobs := &fakeDatasetReapBlobs{}
	r := newDatasetReaper(store, blobs, 30, func(string, ...any) {})

	done := make(chan struct{})
	go func() {
		r.Run(context.Background(), 0)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run(ctx, 0) did not return promptly — it must not start a ticker")
	}
	if store.reapCalls != 0 {
		t.Fatalf("Sweep ran with interval <= 0: %d calls", store.reapCalls)
	}
}

// TestDatasetReapRun_ReturnsOnContextCancel: a running loop stops as soon as
// its context is cancelled, rather than leaking a goroutine forever.
func TestDatasetReapRun_ReturnsOnContextCancel(t *testing.T) {
	store := &fakeDatasetReapStore{}
	blobs := &fakeDatasetReapBlobs{}
	r := newDatasetReaper(store, blobs, 30, func(string, ...any) {})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, time.Hour) // long enough that only cancellation stops it
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
