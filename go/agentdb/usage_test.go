package agentdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The live twin of GetProjectUsageSince (ticket A4,
// design/2026-09-11-onboarding-work-plan.md §1.4). Skipped unless
// AGENTKIT_TEST_POSTGRES_URL is set:
//
//	AGENTKIT_TEST_POSTGRES_URL=postgres://... go test ./agentdb/ -run TestLivePG_GetProjectUsageSince

// insertQueryEventsAt writes one agent_query_events row with an explicit
// created_at (unix seconds), bypassing UpsertQueryEvents' autoCreateTime so a
// test can place a row on either side of a `since` boundary — exactly what
// CountProjectTokensSince's own live test (tokenbudget_live_test.go) does for
// the same reason.
func insertQueryEventsAt(t *testing.T, s *Store, sessionID, queryID, eventsJSON string, createdAt int64) {
	t.Helper()
	if err := s.DB().WithContext(context.Background()).Exec(`
		INSERT INTO agent_query_events (id, session_id, query_id, events, search_text, created_at)
		VALUES (?, ?, ?, ?::jsonb, '', ?)`,
		uuid.New().String(), sessionID, queryID, eventsJSON, createdAt,
	).Error; err != nil {
		t.Fatalf("insert query event: %v", err)
	}
}

// TestLivePG_GetProjectUsageSince_SumsTokensCostAndQueries: two captured-shape
// query rows for a project sum to the right input/output/cost, and the query
// count is the number of ROWS, not the number of stored envelopes —
// usageEnvelopes expands each row into several, so a naive COUNT(*) over that
// join would over-count.
func TestLivePG_GetProjectUsageSince_SumsTokensCostAndQueries(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := "proj-" + uuid.New().String()
	sess := newLiveSession(t, s, project, "u@x.com")

	now := time.Now().Unix()
	// capturedQueryEventsRow(uncached, cacheCreation, cacheRead, out) — each row
	// also carries the real `totalCostUsd` field (0.0004) from the captured shape.
	insertQueryEventsAt(t, s, sess.ID, "q1", capturedQueryEventsRow(100, 400, 0, 30), now)
	insertQueryEventsAt(t, s, sess.ID, "q2", capturedQueryEventsRow(7, 0, 1500, 3), now)

	usage, err := s.GetProjectUsageSince(ctx, project, now-60)
	if err != nil {
		t.Fatalf("GetProjectUsageSince: %v", err)
	}
	// Input: (100+400+0) + (7+0+1500) = 2007. Output: 30+3 = 33.
	if usage.InputTokens != 2007 || usage.OutputTokens != 33 {
		t.Fatalf("tokens: got %d/%d, want 2007/33", usage.InputTokens, usage.OutputTokens)
	}
	if usage.Queries != 2 {
		t.Fatalf("queries: got %d, want 2 (one per stored row, not one per envelope)", usage.Queries)
	}
	// Two rows at 0.0004 each.
	if usage.CostUSD < 0.00079 || usage.CostUSD > 0.00081 {
		t.Fatalf("cost: got %v, want ~0.0008 (two rows of totalCostUsd=0.0004)", usage.CostUSD)
	}
}

// TestLivePG_GetProjectUsageSince_ExcludesRowsBeforeSince is the boundary the
// route's "today" window depends on: a row stamped before `since` must not be
// counted, and one at-or-after it must be.
func TestLivePG_GetProjectUsageSince_ExcludesRowsBeforeSince(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	project := "proj-" + uuid.New().String()
	sess := newLiveSession(t, s, project, "u@x.com")

	since := time.Now().Unix()
	insertQueryEventsAt(t, s, sess.ID, "before", capturedQueryEventsRow(10, 0, 0, 5), since-3600)
	insertQueryEventsAt(t, s, sess.ID, "at-boundary", capturedQueryEventsRow(20, 0, 0, 6), since)

	usage, err := s.GetProjectUsageSince(ctx, project, since)
	if err != nil {
		t.Fatalf("GetProjectUsageSince: %v", err)
	}
	if usage.Queries != 1 {
		t.Fatalf("queries: got %d, want 1 (the row before `since` must be excluded)", usage.Queries)
	}
	if usage.InputTokens != 20 || usage.OutputTokens != 6 {
		t.Fatalf("tokens: got %d/%d, want 20/6 (only the at-boundary row)", usage.InputTokens, usage.OutputTokens)
	}
}

// TestLivePG_GetProjectUsageSince_ScopedToProject: another project's spend must
// never leak into this one's totals — the same isolation
// CountProjectTokensSince already guarantees for the budget gate.
func TestLivePG_GetProjectUsageSince_ScopedToProject(t *testing.T) {
	s := openLivePG(t)
	ctx := context.Background()
	mine := "proj-" + uuid.New().String()
	other := "proj-" + uuid.New().String()
	mySess := newLiveSession(t, s, mine, "u@x.com")
	otherSess := newLiveSession(t, s, other, "u@x.com")

	since := time.Now().Unix() - 60
	insertQueryEventsAt(t, s, mySess.ID, "mine", capturedQueryEventsRow(1, 0, 0, 1), since+1)
	insertQueryEventsAt(t, s, otherSess.ID, "other", capturedQueryEventsRow(999, 0, 0, 999), since+1)

	usage, err := s.GetProjectUsageSince(ctx, mine, since)
	if err != nil {
		t.Fatalf("GetProjectUsageSince: %v", err)
	}
	if usage.Queries != 1 || usage.InputTokens != 1 || usage.OutputTokens != 1 {
		t.Fatalf("cross-project leak: got %+v, want exactly this project's one row", usage)
	}
}

// TestLivePG_GetProjectUsageSince_UnspentIsZero: a project with no query events
// sums to zero via COALESCE, not an error — the ordinary case for a brand new
// project's usage panel.
func TestLivePG_GetProjectUsageSince_UnspentIsZero(t *testing.T) {
	s := openLivePG(t)
	usage, err := s.GetProjectUsageSince(context.Background(), "proj-"+uuid.New().String(), 0)
	if err != nil {
		t.Fatalf("GetProjectUsageSince: %v", err)
	}
	if usage != (Usage{}) {
		t.Fatalf("unspent project: got %+v, want the zero value", usage)
	}
}

// TestGetProjectUsageSince_RequiresProject: the same guard CountProjectTokensSince
// has, so a caller cannot accidentally sum every project's spend with "".
func TestGetProjectUsageSince_RequiresProject(t *testing.T) {
	if _, err := (&Store{}).GetProjectUsageSince(context.Background(), "", 0); err == nil {
		t.Fatalf("want an error for an empty project")
	}
}
