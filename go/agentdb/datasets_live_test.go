package agentdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Dataset store — everything that touches the table (O2).
//
// These are live-Postgres tests because the write path is jsonb + a unique
// index under concurrency, and sqlite can prove nothing about either. They skip
// without AGENTKIT_TEST_POSTGRES_URL (openLivePG, live_pg_test.go) — a skip
// here means the CAS criterion is UNPROVEN, not that it passed.

// newLiveDatasetProject returns a per-run unique project id and registers
// cleanup of every dataset row written under it. Datasets hang off no FK, so
// nothing cascades and the sweep has to be explicit.
func newLiveDatasetProject(t *testing.T, s *Store) string {
	t.Helper()
	project := "wolf-" + uuid.New().String()
	t.Cleanup(func() {
		_ = s.DB().Exec("DELETE FROM datasets WHERE project = ?", project).Error
	})
	return project
}

func liveDataset(project, name string) *Dataset {
	return &Dataset{
		Project:  project,
		Name:     name,
		BlobPath: "_datasets/bytes/" + uuid.New().String(),
		SHA256:   fmt.Sprintf("%064x", 1),
	}
}

// TestDatasetLivePG_CreateFirstVersionAndReadItBack: ifVersion 0 on a name with
// no row succeeds at version 1, the store fills exactly the three fields the
// caller left zero, and both read paths return what was written.
func TestDatasetLivePG_CreateFirstVersionAndReadItBack(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)

	before := time.Now().UnixMilli()
	in := liveDataset(project, "drone-suppliers-basket")
	in.Labels = LabelSet{"hypothesis": "1a2b3c4d", "metric": "drone-suppliers-basket"}
	in.SizeBytes = 40213
	in.RowCount = 512
	in.CreatedByWorker = "researcher"
	in.CreatedBySession = "hyp-1a2b3c4d"

	got, err := s.CreateDatasetVersion(ctx, in, 0)
	if err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("versions start at 1, got %d", got.Version)
	}
	if !strings.HasPrefix(got.ID, "ds-") {
		t.Fatalf("generated id must be \"ds-\" + uuid4, got %q", got.ID)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(got.ID, "ds-")); err != nil {
		t.Fatalf("generated id must carry a uuid4: %q (%v)", got.ID, err)
	}
	if got.ContentType != "text/csv" {
		t.Fatalf("default content type must be text/csv, got %q", got.ContentType)
	}
	if got.CreatedAt < before || got.CreatedAt > time.Now().UnixMilli() {
		t.Fatalf("created_at %d is not a plausible unix-MILLISECONDS stamp (window %d..%d) — "+
			"seconds would be ~1000x too small", got.CreatedAt, before, time.Now().UnixMilli())
	}
	if got.SizeBytes != 40213 || got.RowCount != 512 {
		t.Fatalf("size/row_count not stored verbatim: %d/%d", got.SizeBytes, got.RowCount)
	}
	if got.CreatedByWorker != "researcher" || got.CreatedBySession != "hyp-1a2b3c4d" {
		t.Fatalf("provenance lost: %+v", got)
	}
	if got.Labels["hypothesis"] != "1a2b3c4d" || got.Labels["metric"] != "drone-suppliers-basket" {
		t.Fatalf("labels lost: %+v", got.Labels)
	}

	// Both read paths agree, and the labels really are jsonb.
	cur, err := s.CurrentDataset(ctx, project, "drone-suppliers-basket")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.ID != got.ID || cur.Version != 1 {
		t.Fatalf("current = %+v, want the row just written", cur)
	}
	pinned, err := s.GetDatasetVersion(ctx, project, "drone-suppliers-basket", 1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if pinned.ID != got.ID {
		t.Fatalf("get v1 = %+v, want the row just written", pinned)
	}
	var hyp string
	if err := s.DB().Raw("SELECT labels->>'hypothesis' FROM datasets WHERE id = ?", got.ID).Scan(&hyp).Error; err != nil {
		t.Fatalf("jsonb query: %v", err)
	}
	if hyp != "1a2b3c4d" {
		t.Fatalf("labels are not stored as jsonb: got %q", hyp)
	}

	// A version that was never written is absent, not an error of another kind.
	if _, err := s.GetDatasetVersion(ctx, project, "drone-suppliers-basket", 2); !errors.Is(err, ErrDatasetNotFound) {
		t.Fatalf("unwritten version: want ErrDatasetNotFound, got %v", err)
	}
	// An unlabelled write stores {} rather than NULL (the column is NOT NULL).
	plain, err := s.CreateDatasetVersion(ctx, liveDataset(project, "unlabelled"), 0)
	if err != nil {
		t.Fatalf("create unlabelled: %v", err)
	}
	if plain.Labels == nil || len(plain.Labels) != 0 {
		t.Fatalf("absent labels must read back as an empty set, got %#v", plain.Labels)
	}
}

// TestDatasetLivePG_VersionsAreMonotonicAndGapless: the row written at
// ifVersion == n carries version n+1, for every n in a chain.
func TestDatasetLivePG_VersionsAreMonotonicAndGapless(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)
	const name = "cpi-yoy"

	for n := 0; n < 5; n++ {
		got, err := s.CreateDatasetVersion(ctx, liveDataset(project, name), n)
		if err != nil {
			t.Fatalf("create at ifVersion %d: %v", n, err)
		}
		if got.Version != n+1 {
			t.Fatalf("ifVersion %d must produce version %d, got %d", n, n+1, got.Version)
		}
		cur, err := s.CurrentDataset(ctx, project, name)
		if err != nil || cur.Version != n+1 {
			t.Fatalf("current after write %d: %+v err=%v", n, cur, err)
		}
	}

	// No gaps: every version from 1..5 is readable, and 6 is not.
	for v := 1; v <= 5; v++ {
		if _, err := s.GetDatasetVersion(ctx, project, name, v); err != nil {
			t.Fatalf("version %d must exist: %v", v, err)
		}
	}
	if _, err := s.GetDatasetVersion(ctx, project, name, 6); !errors.Is(err, ErrDatasetNotFound) {
		t.Fatalf("version 6 must not exist, got %v", err)
	}

	// Earlier versions are immutable: v1's blob path is still v1's.
	v1, _ := s.GetDatasetVersion(ctx, project, name, 1)
	v5, _ := s.GetDatasetVersion(ctx, project, name, 5)
	if v1.BlobPath == v5.BlobPath {
		t.Fatalf("each version keeps its own blob path")
	}
}

// TestDatasetLivePG_MismatchedIfVersionConflicts: every wrong ifVersion is a
// conflict carrying the version the caller SHOULD have passed, and 0 means the
// name does not exist in this project.
func TestDatasetLivePG_MismatchedIfVersionConflicts(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)
	const name = "unemployment-rate"

	// A name with no row: any ifVersion but 0 conflicts with Current == 0.
	_, err := s.CreateDatasetVersion(ctx, liveDataset(project, name), 3)
	var conflict ErrDatasetVersionConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("want a version conflict, got %v", err)
	}
	if conflict.Current != 0 {
		t.Fatalf("an absent name reports Current 0, got %d", conflict.Current)
	}

	if _, err := s.CreateDatasetVersion(ctx, liveDataset(project, name), 0); err != nil {
		t.Fatalf("create v1: %v", err)
	}
	if _, err := s.CreateDatasetVersion(ctx, liveDataset(project, name), 1); err != nil {
		t.Fatalf("create v2: %v", err)
	}

	// ifVersion 0 on an existing name is a conflict, not a fresh version 1.
	_, err = s.CreateDatasetVersion(ctx, liveDataset(project, name), 0)
	if !errors.As(err, &conflict) || conflict.Current != 2 {
		t.Fatalf("ifVersion 0 on an existing name: want conflict Current 2, got %v", err)
	}
	// A stale ifVersion, and one from the future.
	for _, ifVersion := range []int{1, 7} {
		_, err = s.CreateDatasetVersion(ctx, liveDataset(project, name), ifVersion)
		if !errors.As(err, &conflict) || conflict.Current != 2 {
			t.Fatalf("ifVersion %d: want conflict Current 2, got %v", ifVersion, err)
		}
	}

	// A rejected write leaves nothing behind.
	var n int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM datasets WHERE project = ? AND name = ?", project, name).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("conflicting writes must not insert: %d rows, want 2", n)
	}
}

// TestDatasetLivePG_ConcurrentWritersExactlyOneWins is the ticket's central
// criterion, and it is the reason the whole store is Postgres-only. Real
// goroutines, one real database, no mocks: the next version number and the CAS
// check are decided inside one transaction, and the unique index on
// (project, name, version) is the backstop when two transactions read the same
// high-water mark before either commits.
func TestDatasetLivePG_ConcurrentWritersExactlyOneWins(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)

	// Round 1 races at ifVersion 0 (nothing exists); round 2 races at
	// ifVersion 1 against the row round 1 wrote. Both are the same race, but
	// the second one has an existing row to read a high-water mark from.
	for round, ifVersion := range []int{0, 1} {
		name := "race-basket"
		const writers = 16

		var wg sync.WaitGroup
		errs := make([]error, writers)
		ids := make([]string, writers)
		start := make(chan struct{})
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				got, err := s.CreateDatasetVersion(ctx, liveDataset(project, name), ifVersion)
				errs[i] = err
				if got != nil {
					ids[i] = got.ID
				}
			}(i)
		}
		close(start)
		wg.Wait()

		winners := 0
		for i, err := range errs {
			if err == nil {
				winners++
				continue
			}
			var conflict ErrDatasetVersionConflict
			if !errors.As(err, &conflict) {
				t.Fatalf("round %d writer %d: a loser's error must be an ErrDatasetVersionConflict "+
					"(never a raw driver error and never a bare \"duplicate key\" string), got %v", round, i, err)
			}
			if conflict.Current != ifVersion+1 {
				t.Fatalf("round %d writer %d: the loser must be told the version it should have passed: "+
					"Current = %d, want %d", round, i, conflict.Current, ifVersion+1)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: exactly one writer must succeed, got %d", round, winners)
		}

		var n int64
		if err := s.DB().Raw(
			"SELECT COUNT(*) FROM datasets WHERE project = ? AND name = ? AND version = ?",
			project, name, ifVersion+1).Scan(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Fatalf("round %d: exactly one row must exist at version %d, got %d", round, ifVersion+1, n)
		}
	}
}

// TestDatasetLivePG_ProjectIsBoundFirst: a name that exists only in another
// project is NOT FOUND, the same answer as absent — neither read is an
// existence oracle across the tenancy boundary (the posture memoryNotFound
// states for memories).
func TestDatasetLivePG_ProjectIsBoundFirst(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	owner := newLiveDatasetProject(t, s)
	other := newLiveDatasetProject(t, s)
	const name = "shared-name"

	if _, err := s.CreateDatasetVersion(ctx, liveDataset(owner, name), 0); err != nil {
		t.Fatalf("create in owner: %v", err)
	}

	if _, err := s.CurrentDataset(ctx, other, name); !errors.Is(err, ErrDatasetNotFound) {
		t.Fatalf("cross-project current: want ErrDatasetNotFound, got %v", err)
	}
	if _, err := s.GetDatasetVersion(ctx, other, name, 1); !errors.Is(err, ErrDatasetNotFound) {
		t.Fatalf("cross-project get: want ErrDatasetNotFound, got %v", err)
	}
	// Absent in the owner's own project reads exactly the same.
	if _, err := s.CurrentDataset(ctx, owner, "never-written"); !errors.Is(err, ErrDatasetNotFound) {
		t.Fatalf("absent name: want ErrDatasetNotFound, got %v", err)
	}

	// Versions are per (project, name): the other project's first write is its
	// own version 1, not version 2.
	got, err := s.CreateDatasetVersion(ctx, liveDataset(other, name), 0)
	if err != nil {
		t.Fatalf("create in other project: %v", err)
	}
	if got.Version != 1 {
		t.Fatalf("version numbering must be per (project, name), got %d", got.Version)
	}
	cur, err := s.CurrentDataset(ctx, owner, name)
	if err != nil || cur.Version != 1 || cur.Project != owner {
		t.Fatalf("the owner's row must be untouched: %+v err=%v", cur, err)
	}
}

// TestDatasetLivePG_CallerSuppliedFieldsAreNeverOverwritten: the store fills
// ID, CreatedAt and ContentType when, and ONLY when, the caller left them zero.
// row_count in particular is stored exactly as supplied — the store never
// parses CSV, agentd counts it (O6a).
func TestDatasetLivePG_CallerSuppliedFieldsAreNeverOverwritten(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)

	in := liveDataset(project, "explicit-everything")
	in.ID = "ds-" + uuid.New().String()
	in.CreatedAt = 1789000000123
	in.ContentType = "application/json"
	in.RowCount = 0
	in.SizeBytes = 0

	got, err := s.CreateDatasetVersion(ctx, in, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID != in.ID {
		t.Fatalf("caller id overwritten: %q", got.ID)
	}
	if got.CreatedAt != 1789000000123 {
		t.Fatalf("caller created_at overwritten: %d", got.CreatedAt)
	}
	if got.ContentType != "application/json" {
		t.Fatalf("caller content type overwritten: %q", got.ContentType)
	}
	// Zero is a legal row count (a header-only CSV) and must survive as zero.
	if got.RowCount != 0 || got.SizeBytes != 0 {
		t.Fatalf("zero size/row_count must be stored verbatim, got %d/%d", got.SizeBytes, got.RowCount)
	}

	// A caller-supplied row count is stored exactly, never recomputed.
	next := liveDataset(project, "explicit-everything")
	next.RowCount = 999999
	next.SizeBytes = 1 << 40
	got, err = s.CreateDatasetVersion(ctx, next, 1)
	if err != nil {
		t.Fatalf("create v2: %v", err)
	}
	if got.RowCount != 999999 || got.SizeBytes != 1<<40 {
		t.Fatalf("row_count/size_bytes not stored verbatim: %d/%d", got.RowCount, got.SizeBytes)
	}
}

// TestDatasetLivePG_NoConfigEventIsWritten: a dataset is data, not
// configuration. §15.3's log records decisions someone made about how the
// project is wired; a metric refresh is neither.
func TestDatasetLivePG_NoConfigEventIsWritten(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)

	var before int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM config_events WHERE project = ?", project).Scan(&before).Error; err != nil {
		t.Fatalf("count before: %v", err)
	}
	if _, err := s.CreateDatasetVersion(ctx, liveDataset(project, "no-config-event"), 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	var after int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM config_events WHERE project = ?", project).Scan(&after).Error; err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("a dataset write must log no config event: %d -> %d", before, after)
	}
}

// TestDatasetLivePG_UniqueIndexIsTheBackstop provokes the race the goroutine
// test can only hope for. Two transactions reading the same high-water mark
// before either commits is rare in wall-clock terms and impossible to schedule
// from Go, so this test builds the state directly: an uncommitted insert at
// version 1 is invisible to the store's MAX(version) read, so the store passes
// its own CAS check and then blocks on the unique index until the seeding
// transaction commits — at which point its insert fails with 23505.
//
// That failure must surface as an ErrDatasetVersionConflict carrying the
// RE-READ current version (1), never as a raw driver error and never as a bare
// "duplicate key" string. The re-read cannot happen inside the aborted
// transaction, which is the whole reason the store unwinds first and asks
// again on a fresh connection.
func TestDatasetLivePG_UniqueIndexIsTheBackstop(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := newLiveDatasetProject(t, s)
	const name = "backstop"

	tx := s.DB().Begin()
	if tx.Error != nil {
		t.Fatalf("begin: %v", tx.Error)
	}
	committed := make(chan struct{})
	if err := tx.Exec(
		`INSERT INTO datasets (id, project, name, version, labels, blob_path, size_bytes, row_count,
		                       sha256, content_type, created_by_worker, created_by_session, created_at)
		 VALUES (?, ?, ?, 1, '{}'::jsonb, '_datasets/bytes/seed', 0, 0, 'seed', 'text/csv', '', '', 1)`,
		"ds-"+uuid.New().String(), project, name).Error; err != nil {
		tx.Rollback()
		t.Fatalf("seed insert: %v", err)
	}

	type result struct {
		ds  *Dataset
		err error
	}
	done := make(chan result, 1)
	go func() {
		ds, err := s.CreateDatasetVersion(ctx, liveDataset(project, name), 0)
		done <- result{ds, err}
	}()

	// Give the writer time to read the (still 0) high-water mark, pass its CAS
	// check and block on the index, then let the seeding row become visible.
	select {
	case r := <-done:
		tx.Rollback()
		t.Fatalf("the writer must block on the unique index until the seeding row commits; "+
			"it returned early with %+v err=%v", r.ds, r.err)
	case <-time.After(500 * time.Millisecond):
	}
	close(committed)
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit seed: %v", err)
	}

	r := <-done
	select {
	case <-committed:
	default:
		t.Fatalf("the writer returned before the seeding commit — the backstop was not exercised")
	}
	var conflict ErrDatasetVersionConflict
	if !errors.As(r.err, &conflict) {
		t.Fatalf("a unique-index violation must surface as ErrDatasetVersionConflict, got %v", r.err)
	}
	if conflict.Current != 1 {
		t.Fatalf("the conflict must carry the RE-READ current version 1, got %d", conflict.Current)
	}
	if strings.Contains(r.err.Error(), "duplicate key") || strings.Contains(r.err.Error(), "23505") {
		t.Fatalf("the driver's message must not reach the caller: %q", r.err)
	}
	if r.ds != nil {
		t.Fatalf("a losing write returns no dataset, got %+v", r.ds)
	}

	// One row at version 1, and it is the seeded one.
	var n int64
	if err := s.DB().Raw("SELECT COUNT(*) FROM datasets WHERE project = ? AND name = ?", project, name).Scan(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("exactly one row must exist, got %d", n)
	}
}
