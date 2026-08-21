package agentdb

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The memory store is append-only BY CONSTRUCTION: the guarantee is that no
// mutating method exists to call. This test is the guard rail — it fails the
// moment someone adds one, which is the only way the invariant can be broken.
func TestMemoriesStoreIsAppendOnly(t *testing.T) {
	typ := reflect.TypeOf(&Store{})

	mutators := []string{"update", "delete", "set", "patch", "remove", "upsert", "purge", "prune"}
	var found []string
	haveCreate, haveGet, haveSearch, haveNewest := false, false, false, false
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "memor") { // Memory / Memories
			continue
		}
		switch name {
		case "CreateMemory":
			haveCreate = true
		case "GetMemory":
			haveGet = true
		case "SearchMemories":
			haveSearch = true
		case "NewestMemory":
			haveNewest = true
		}
		for _, m := range mutators {
			if strings.Contains(lower, m) {
				found = append(found, name)
			}
		}
	}
	if len(found) > 0 {
		t.Fatalf("memories are immutable (§7.1): no mutating store method may exist, found %v", found)
	}
	if !haveCreate || !haveGet || !haveSearch || !haveNewest {
		t.Fatalf("expected CreateMemory/GetMemory/SearchMemories/NewestMemory to exist, got create=%v get=%v search=%v newest=%v",
			haveCreate, haveGet, haveSearch, haveNewest)
	}
}

// TestMemorySqlite pins the degradation decision (§7, docs/15-standalone-stack.md):
// **memory requires Postgres**, and the sqlite dev store says so out loud.
//
// The alternative — a keyword-only sqlite implementation — was rejected: the
// memory system's whole promise is that what a worker wrote down is there
// later, and a store that quietly drops jsonb selectors, tsvector ranking and
// the semantic leg would keep answering searches with plausible, incomplete
// results. A store that silently forgets is worse than no store, so all three
// entry points fail with ErrMemoryRequiresPostgres on any non-Postgres dialect.
//
// (This is the outer boundary only. *Within* Postgres, pgvector is optional:
// migration 022 adds the vector column when the extension is available and
// search drops the semantic CTE when it is not — that degradation is silent by
// design, because keyword+recency still returns real rows in the same shape.)
func TestMemorySqlite(t *testing.T) {
	ctx := context.Background()
	sqliteStore := newTestStore(t)

	// A sqlite store that HAS a memories table is the interesting case: the
	// refusal must come from the dialect, not from a missing table, or a
	// half-working store would appear the moment someone ran AutoMigrate.
	migrated := newTestStore(t)
	if err := migrated.DB().AutoMigrate(&Memory{}); err != nil {
		t.Fatalf("automigrate Memory on sqlite: %v", err)
	}

	// (*Store)(nil) is the "no store wired" case — still an error, not a panic.
	var nilStore *Store

	tests := []struct {
		name string
		call func(s *Store) error
	}{
		{"create", func(s *Store) error {
			_, _, err := s.CreateMemory(ctx, &Memory{
				Project: "p", Content: "the refund window is 30 days",
				Labels: LabelSet{"kind": "fact"},
			}, nil)
			return err
		}},
		{"create with embedding", func(s *Store) error {
			_, _, err := s.CreateMemory(ctx, &Memory{Project: "p", Content: "x"}, make([]float32, MemoryEmbeddingDim))
			return err
		}},
		{"get", func(s *Store) error {
			_, err := s.GetMemory(ctx, "p", "some-id")
			return err
		}},
		{"search, bare selector", func(s *Store) error {
			_, err := s.SearchMemories(ctx, &MemorySearchQuery{Project: "p", LabelSelector: "kind=fact"})
			return err
		}},
		{"search, query text", func(s *Store) error {
			_, err := s.SearchMemories(ctx, &MemorySearchQuery{Project: "p", Query: "refund window"})
			return err
		}},
		{"search, nil query", func(s *Store) error {
			_, err := s.SearchMemories(ctx, nil)
			return err
		}},
		// The briefing / memory_current read path (C4, D3) is Postgres-only for
		// the same reason: a briefing that silently degrades to "no memory" would
		// make every worker look freshly amnesiac with nothing in the logs.
		{"newest", func(s *Store) error {
			_, err := s.NewestMemory(ctx, "p", "kind=rolling-summary,worker=w")
			return err
		}},
	}

	for _, tc := range tests {
		for _, store := range []struct {
			label string
			s     *Store
		}{
			{"sqlite", sqliteStore},
			{"sqlite with a memories table", migrated},
			{"nil store", nilStore},
		} {
			t.Run(tc.name+"/"+store.label, func(t *testing.T) {
				err := tc.call(store.s)
				if !errors.Is(err, ErrMemoryRequiresPostgres) {
					t.Fatalf("want ErrMemoryRequiresPostgres, got %v", err)
				}
				// The message has to tell an operator what to do about it:
				// it names Postgres and why (jsonb/tsvector/pgvector).
				for _, want := range []string{"Postgres", "jsonb", "tsvector", "pgvector"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error message must mention %q, got %q", want, err.Error())
					}
				}
			})
		}
	}

	// Nothing was written along the way: the refusal happens before any SQL, so
	// a caller cannot end up with rows it can never read back.
	var rows int64
	if err := migrated.DB().Raw("SELECT COUNT(*) FROM memories").Scan(&rows).Error; err != nil {
		t.Fatalf("count sqlite memories: %v", err)
	}
	if rows != 0 {
		t.Fatalf("sqlite must accept no memory writes at all, found %d rows", rows)
	}
}

func TestMemoriesFormatVector(t *testing.T) {
	if got := FormatVector([]float32{0, 1, -0.5}); got != "[0,1,-0.5]" {
		t.Fatalf("FormatVector = %q", got)
	}
	if got := FormatVector(nil); got != "[]" {
		t.Fatalf("FormatVector(nil) = %q", got)
	}
}

func TestMemoriesTableName(t *testing.T) {
	if name := (Memory{}).TableName(); name != "memories" {
		t.Fatalf("table name: %q", name)
	}
}

// The two ceilings are different numbers for different reasons, and a future
// edit that collapsed them would silently change what `embed:false` buys.
func TestMemorySizeCeilingsAreDistinct(t *testing.T) {
	if MaxEmbeddedMemoryBytes >= MaxMemoryBytes {
		t.Fatalf("the embedding ceiling (%d) must sit below the storage ceiling (%d): the whole point of embed:false is storing what cannot be embedded",
			MaxEmbeddedMemoryBytes, MaxMemoryBytes)
	}
}

// A result that was not retracted must encode EXACTLY as it did before O11
// added the field. `omitempty` is the whole reason: every existing consumer of
// GET /agent/memories — the console's memory browser included — decodes this
// shape, and a `"retracted_by": null` appearing on every row of every ordinary
// search would be a wire change for a facility almost no caller asked for.
func TestMemorySearchResultOmitsRetractedByWhenAbsent(t *testing.T) {
	plain, err := json.Marshal(&MemorySearchResult{
		ID: "mem-1", Labels: LabelSet{"kind": "fact"}, Snippet: "s",
		CreatedByWorker: "w", CreatedBySession: "sess-1", CreatedAt: 1789000000123,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"id":"mem-1","labels":{"kind":"fact"},"snippet":"s","score":0,` +
		`"created_by_worker":"w","created_by_session":"sess-1","created_at":1789000000123}`
	if string(plain) != want {
		t.Fatalf("an unretracted result changed shape:\n got %s\nwant %s", plain, want)
	}

	// And when it IS retracted, every key of every retraction is on the wire —
	// a reader that cannot see the retractor's provenance cannot tell an
	// application's own withdrawal from an attacker's.
	withRetraction, err := json.Marshal(&MemorySearchResult{
		ID: "mem-1", Labels: LabelSet{}, CreatedAt: 1,
		RetractedBy: []MemoryRetraction{{
			MemoryID: "mem-2", CreatedByWorker: "researcher",
			CreatedBySession: "sess-9", CreatedAt: 1789000000456,
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const wantRetraction = `"retracted_by":[{"memory_id":"mem-2","created_by_worker":"researcher",` +
		`"created_by_session":"sess-9","created_at":1789000000456}]`
	if !strings.Contains(string(withRetraction), wantRetraction) {
		t.Fatalf("retraction shape:\n got %s\nwant it to contain %s", withRetraction, wantRetraction)
	}
}
