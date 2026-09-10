package main

// datasetreaper.go — the dataset version reaper and orphan blob sweep, on one
// ticker inside agentd (O8, design/2026-08-20-agent-wolf.md § O8).
//
// "Beside the snapshot reaper" (an earlier revision's phrasing) points at the
// wrong place: the snapshot TTL reaper is a Runner loop
// (go/snapshot_reaper.go) driven by agentkit.Policy.SnapshotReapInterval,
// which agentd merely SETS (gc.go / main.go). Datasets are product-layer and
// Postgres-only — there is no Policy field for them and no reason to invent
// one — so this loop lives entirely in cmd/agentd and is started from
// main.go only when agentDB != nil, exactly like the router, the scheduler
// and the attention sweeper beside it. go/agentkit.go and go/runner.go are
// NOT touched by this file.
//
// # The orphan sweep's two-pass safety
//
// agentdb.Store.ListOrphanBlobPaths's minAge parameter is deliberately inert
// — see its doc comment in go/agentdb/datasets.go. A blob key carries no
// timestamp, so "how old is this orphan" is not answerable from inside
// agentdb at all; the store can only ever say which paths are UNREFERENCED
// right now. Deleting on the strength of one pass would eat a `dataset_put`
// that has written its bytes to the blob store but not yet committed its row
// — the exact race O3's design calls out.
//
// The safety therefore lives here, at the caller: a path must be reported as
// orphaned on two separate sweeps at least datasetOrphanMinAge apart before
// this reaper will delete it. A path that stops being reported (because a
// row referencing it was written between two sweeps) is forgotten, not
// deleted — there is no "orphaned once" debt that outlives the write that
// cleared it.
//
// # The belt-and-braces prefix re-assertion
//
// agentd runs ONE global BlobStore, shared with `_artifacts/bytes/` and with
// session snapshot bytes (backends.go's newBlobs → Global("")). Every path
// this reaper is about to delete — from BOTH the version reap and the orphan
// sweep — is re-checked against agentdb.DatasetBlobPrefix immediately before
// the Delete call, exactly as agentdb.Store already does on its own side of
// the seam. This is deliberately redundant: a bug or an empty prefix on
// either side alone would otherwise be free to enumerate and delete every
// artifact and snapshot blob in the deployment, not just this table's own.

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// datasetOrphanMinAge is the minimum time a blob key must have appeared
// orphaned across two sweeps before datasetReaper will delete it.
const datasetOrphanMinAge = time.Hour

// datasetReapStore is the narrow slice of *agentdb.Store the reaper needs.
// A real *agentdb.Store satisfies it directly; tests use a fake.
type datasetReapStore interface {
	ReapDatasetVersions(ctx context.Context, keepPerName int, blobs agentdb.DatasetBlobDeleter) (int, error)
	ListOrphanBlobPaths(ctx context.Context, blobs agentdb.DatasetBlobLister, minAge time.Duration) ([]string, error)
}

// datasetReapBlobs is the narrow slice of the process-wide extension.BlobStore
// this reaper touches: Delete (both halves of the sweep) and List (the
// orphan sweep, via ListOrphanBlobPaths). extension.BlobStore satisfies this
// structurally, so agentd hands its one global store in directly — the same
// pattern agentdb.DatasetBlobDeleter / DatasetBlobLister already use, and for
// the same reason: extension imports agentdb, so this interface cannot live
// in extension without a cycle, and duplicating it here is cheaper than an
// adapter.
type datasetReapBlobs interface {
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// datasetReaper owns the reap+sweep pass and the orphan-sighting state the
// two-pass safety needs. The zero value is not usable — construct with
// newDatasetReaper.
type datasetReaper struct {
	store        datasetReapStore
	blobs        datasetReapBlobs
	keepVersions int
	minAge       time.Duration
	logf         func(format string, args ...any)
	now          func() time.Time // overridden in tests; defaults to time.Now

	mu          sync.Mutex
	firstSeenAt map[string]time.Time // orphan blob path → when it was FIRST seen orphaned
}

// newDatasetReaper constructs a reaper. logf defaults to log.Printf when nil.
func newDatasetReaper(store datasetReapStore, blobs datasetReapBlobs, keepVersions int, logf func(string, ...any)) *datasetReaper {
	if logf == nil {
		logf = log.Printf
	}
	return &datasetReaper{
		store:        store,
		blobs:        blobs,
		keepVersions: keepVersions,
		minAge:       datasetOrphanMinAge,
		logf:         logf,
		now:          time.Now,
		firstSeenAt:  map[string]time.Time{},
	}
}

// Run ticks Sweep every interval until ctx is cancelled. interval <= 0 means
// "never" — no ticker is started and Run returns immediately, exactly like
// the reap/sweep it would otherwise drive being permanently disabled.
func (r *datasetReaper) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sweep(ctx)
		}
	}
}

// Sweep runs one pass: the version reaper, then the orphan blob sweep, on the
// SAME tick — the ticket's requirement that the two never run independently.
// Every pass logs exactly one summary line, whatever it found.
func (r *datasetReaper) Sweep(ctx context.Context) {
	reaped, err := r.store.ReapDatasetVersions(ctx, r.keepVersions, r.blobs)
	if err != nil {
		r.logf("[agentd] datasets: version reap: %v", err)
	}

	var orphans []string
	orphans, err = r.store.ListOrphanBlobPaths(ctx, r.blobs, r.minAge)
	if err != nil {
		r.logf("[agentd] datasets: orphan list: %v", err)
		orphans = nil
	}

	swept, failures := r.sweepOrphans(ctx, orphans)

	r.logf("[agentd] datasets: reaped %d versions, swept %d orphan blobs (%d delete failures)", reaped, swept, failures)
}

// sweepOrphans applies the two-pass age safety and the belt-and-braces prefix
// re-assertion, deletes what has cleared both, and returns how many were
// swept and how many delete attempts failed. A delete failure is logged and
// does not stop the rest of the pass — the path is left in firstSeenAt so the
// next sweep retries it. A path no longer reported orphaned (something now
// references it) is forgotten rather than eventually deleted on stale
// evidence.
func (r *datasetReaper) sweepOrphans(ctx context.Context, orphans []string) (swept, failures int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	stillOrphan := make(map[string]bool, len(orphans))
	for _, p := range orphans {
		stillOrphan[p] = true
		firstSeen, seenBefore := r.firstSeenAt[p]
		if !seenBefore {
			// First sighting: record it and wait for the next pass. Never
			// delete on a single pass, however old the path might actually be.
			r.firstSeenAt[p] = now
			continue
		}
		if now.Sub(firstSeen) < r.minAge {
			continue
		}
		// Belt and braces, re-asserted immediately before every Delete: see
		// the file header. A path that fails this check is skipped forever —
		// it is not one of ours to have listed, let alone delete — and is
		// dropped from firstSeenAt so it stops being tracked.
		if !strings.HasPrefix(p, agentdb.DatasetBlobPrefix) {
			delete(r.firstSeenAt, p)
			continue
		}
		if err := r.blobs.Delete(ctx, p); err != nil {
			r.logf("[agentd] datasets: delete orphan blob %q: %v", p, err)
			failures++
			continue
		}
		delete(r.firstSeenAt, p)
		swept++
	}
	for p := range r.firstSeenAt {
		if !stillOrphan[p] {
			delete(r.firstSeenAt, p)
		}
	}
	return swept, failures
}
