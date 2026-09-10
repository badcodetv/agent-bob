package agentdb

// memories_provenance_live_test.go — Decision B3 / ticket T3
// (design/2026-09-08-memory-coordinated-organisation.md): CreatedByWorker as a
// queryable HARD filter on SearchMemories.
//
// The "self" sentinel and the identity refusal are an MCP-tool-layer concern
// (go/cmd/agentd/mcp_memory.go, mcp_memory_test.go) — by the time a query
// reaches the store it is always a real worker name or empty, never "self".
// What belongs here is the store contract itself: the filter is exact-match on
// created_by_worker, it is a HARD filter (both paths, ANDed with everything
// else), and — the one bug this design explicitly calls out — it composes
// with LatestPer by narrowing the set LatestPer reduces over, not by filtering
// its output.
//
// Run with AGENTKIT_TEST_POSTGRES_URL set; without it these skip.

import (
	"context"
	"testing"
	"time"
)

func TestMemorySearchCreatedByWorker(t *testing.T) {
	s := openLivePG(t)
	project := newLiveProject(t, s)
	ctx := context.Background()

	alice := mustCreateMemory(t, s, &Memory{
		Project: project, Content: "alice wrote this",
		Labels: LabelSet{"kind": "note"}, CreatedByWorker: "alice-worker",
	}, unitVector(3))
	bob := mustCreateMemory(t, s, &Memory{
		Project: project, Content: "bob wrote this",
		Labels: LabelSet{"kind": "note"}, CreatedByWorker: "bob-worker",
	}, unitVector(3))

	paths := []struct {
		name  string
		query string
	}{
		{"recency path", ""},
		{"hybrid path", "wrote this"},
	}

	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			res, err := s.SearchMemories(ctx, &MemorySearchQuery{
				Project: project, Query: path.query, CreatedByWorker: "alice-worker",
			})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(res) != 1 || res[0].ID != alice.ID {
				t.Fatalf("want only alice's memory, got %v", resultIDs(res))
			}
			if hasID(res, bob.ID) {
				t.Error("bob's memory must not be returned when filtering by alice's name")
			}
		})
	}

	t.Run("empty means unfiltered — every existing caller is unaffected", func(t *testing.T) {
		res, err := s.SearchMemories(ctx, &MemorySearchQuery{Project: project, LabelSelector: "kind=note"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(res) != 2 {
			t.Fatalf("want both rows with no created_by_worker filter, got %v", resultIDs(res))
		}
	})

	t.Run("a name that wrote nothing here returns nothing", func(t *testing.T) {
		res, err := s.SearchMemories(ctx, &MemorySearchQuery{Project: project, CreatedByWorker: "nobody"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(res) != 0 {
			t.Fatalf("want no rows, got %v", resultIDs(res))
		}
	})
}

// TestMemorySearchCreatedByWorkerComposesWithLatestPer is the acceptance
// criterion that most directly tests placement: the filter must sit in the
// `filtered` CTE, upstream of the LatestPer reduction, so LatestPer picks the
// newest row WITHIN the filtered set — never the newest row overall with a
// wrong-author row then discarded after the fact. A filter applied AFTER
// LatestPer would let another worker's newer row win the DISTINCT ON slot and
// silently exclude the name entirely, rather than falling back to the
// filtered caller's own older row.
func TestMemorySearchCreatedByWorkerComposesWithLatestPer(t *testing.T) {
	s := openLivePG(t)
	project := newLiveProject(t, s)
	ctx := context.Background()

	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	min := int64(60 * 1000)

	// Same name="alpha" slot, written by two different workers, alice's the
	// OLDER of the two. If the filter ran after LatestPer, bob's newer row
	// would already have won the slot and alice's would already be gone —
	// filtering by alice afterwards would then find nothing for "alpha".
	aliceAlpha := atMillisByWorker(t, s, project, "alpha status from alice", LabelSet{"kind": "status", "name": "alpha"}, base, "alice-worker", unitVector(4))
	_ = atMillisByWorker(t, s, project, "alpha status from bob", LabelSet{"kind": "status", "name": "alpha"}, base+min, "bob-worker", unitVector(4))
	aliceBeta := atMillisByWorker(t, s, project, "beta status from alice", LabelSet{"kind": "status", "name": "beta"}, base, "alice-worker", unitVector(4))

	res, err := s.SearchMemories(ctx, &MemorySearchQuery{
		Project: project, LabelSelector: "kind=status", LatestPer: "name",
		CreatedByWorker: "alice-worker",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want alice's newest row per name (2 names), got %d: %v", len(res), resultIDs(res))
	}
	if !hasID(res, aliceAlpha.ID) {
		t.Error("alice's OWN alpha row must win her slot — the filter must run before the reduction, not after")
	}
	if !hasID(res, aliceBeta.ID) {
		t.Error("alice's only beta row is missing")
	}
	for _, r := range res {
		if r.CreatedByWorker != "alice-worker" {
			t.Fatalf("a row from %q leaked through the created_by_worker + latest_per composition", r.CreatedByWorker)
		}
	}
}

// atMillisByWorker is atMillis (memories_query_live_test.go) plus an explicit
// author, because the LatestPer composition test needs both an explicit
// created_at ordering AND provenance on the same rows.
func atMillisByWorker(t *testing.T, s *Store, project, content string, labels LabelSet, ms int64, worker string, emb []float32) *Memory {
	t.Helper()
	return mustCreateMemory(t, s, &Memory{
		Project:         project,
		Labels:          labels,
		Content:         content,
		CreatedAt:       ms,
		CreatedByWorker: worker,
	}, emb)
}
