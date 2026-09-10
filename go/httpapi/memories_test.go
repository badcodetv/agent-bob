package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/badcodetv/agent-bob/agentdb"
)

// fakeMemories records the query it was handed and returns a canned page, so the
// handler's parameter plumbing can be asserted without a database.
type fakeMemories struct {
	got  agentdb.MemorySearchQuery
	out  []*agentdb.MemorySearchResult
	err  error
	call int

	// The single-record legs (T18). gotProject/gotID/gotSelector record what
	// crossed the seam — the project is the whole tenancy story on both routes,
	// so it is asserted, never assumed.
	one         *agentdb.Memory
	oneErr      error
	gotProject  string
	gotID       string
	gotSelector string

	// The append leg (O7). gotCreate is the row as it crossed the seam —
	// provenance and project are asserted there, because that is the only place
	// they can still be wrong; created is what the store hands back, kept
	// deliberately DIFFERENT from gotCreate so a handler echoing the caller's
	// struct instead of the stored row is caught.
	gotCreate    *agentdb.Memory
	gotEmbedding []float32
	created      *agentdb.Memory
	createErr    error
}

func (f *fakeMemories) CreateMemory(_ context.Context, m *agentdb.Memory, embedding []float32) (*agentdb.Memory, bool, error) {
	f.call++
	f.gotCreate, f.gotEmbedding = m, embedding
	if f.createErr != nil {
		return nil, false, f.createErr
	}
	if f.created != nil {
		return f.created, embedding != nil, nil
	}
	return m, embedding != nil, nil
}

func (f *fakeMemories) SearchMemories(_ context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error) {
	f.call++
	f.got = *q
	return f.out, f.err
}

func (f *fakeMemories) GetMemory(_ context.Context, project, id string) (*agentdb.Memory, error) {
	f.call++
	f.gotProject, f.gotID = project, id
	return f.one, f.oneErr
}

func (f *fakeMemories) NewestMemory(_ context.Context, project, selector string) (*agentdb.Memory, error) {
	f.call++
	f.gotProject, f.gotSelector = project, selector
	return f.one, f.oneErr
}

// bigMemory is a body comfortably past the 500-byte snippet cut
// (agentdb/memories.go:35), because a test that reads back 200 bytes untruncated
// proves nothing at all about truncation.
func bigMemory() *agentdb.Memory {
	return &agentdb.Memory{
		ID:               "mem-1",
		Project:          "acme",
		Labels:           agentdb.LabelSet{"name": "hypothesis-a", "kind": "state"},
		Content:          strings.Repeat("the AI bubble bursts and liquidity floods in. ", 40), // 1840 bytes
		CreatedByWorker:  "reviewer-a",
		CreatedBySession: "sess-1",
		CreatedAt:        1789000000123,
	}
}

func newMemoryHandlers(t *testing.T, store MemoryStore, id IdentityFunc) *Handlers {
	t.Helper()
	return newHandlers(t, Config{
		Runner:   stubRunner{},
		Store:    stubStore{},
		Identity: id,
		Memories: store,
	})
}

// Every parameter in the §7.6 contract reaches the store — and nothing else
// does. The project comes from the token, never from the query string (P5).
func TestListMemories_QueryPlumbing(t *testing.T) {
	tests := []struct {
		name string
		path string
		want agentdb.MemorySearchQuery
	}{
		{
			name: "no filters is the recency question",
			path: "/agent/memories",
			want: agentdb.MemorySearchQuery{Project: "acme"},
		},
		{
			name: "selector, query and limit",
			path: "/agent/memories?selector=kind%3Drolling-summary%2Cworker%3Demail-answerer&query=refund+policy&limit=25",
			want: agentdb.MemorySearchQuery{
				Project:       "acme",
				LabelSelector: "kind=rolling-summary,worker=email-answerer",
				Query:         "refund policy",
				Limit:         25,
			},
		},
		{
			name: "a project in the query is ignored, not honoured",
			path: "/agent/memories?project=other",
			want: agentdb.MemorySearchQuery{Project: "acme"},
		},
		{
			name: "a junk limit degrades to the store's default, not an error",
			path: "/agent/memories?limit=-3",
			want: agentdb.MemorySearchQuery{Project: "acme"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeMemories{}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			if rec := do(h, http.MethodGet, tc.path, ""); rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if store.got.Project != tc.want.Project ||
				store.got.LabelSelector != tc.want.LabelSelector ||
				store.got.Query != tc.want.Query ||
				store.got.Limit != tc.want.Limit {
				t.Fatalf("query:\n got %+v\nwant %+v", store.got, tc.want)
			}
			if store.got.QueryEmbedding != nil {
				t.Fatalf("no embedder is wired, so the semantic leg must be off: %v", store.got.QueryEmbedding)
			}
		})
	}
}

// The embedder is consulted only when there is text to embed, and a nil return
// (a degraded provider) is passed through as "no semantic leg" rather than an
// error — §7.6.5: the result shape never changes.
func TestListMemories_Embedder(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		vec      []float32
		wantCall string
		wantVec  bool
	}{
		{name: "text is embedded", path: "/agent/memories?query=refunds", vec: []float32{0.5},
			wantCall: "refunds", wantVec: true},
		{name: "no text, no embedding call", path: "/agent/memories?selector=kind%3Dnote", vec: []float32{0.5}},
		{name: "a degraded provider costs the leg, not the answer",
			path: "/agent/memories?query=refunds", vec: nil, wantCall: "refunds"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			store := &fakeMemories{}
			h := newHandlers(t, Config{
				Runner: stubRunner{}, Store: stubStore{}, Identity: identityFor("acme"),
				Memories: store,
				MemoryEmbedder: func(_ context.Context, text string) []float32 {
					seen = text
					return tc.vec
				},
			})
			if rec := do(h, http.MethodGet, tc.path, ""); rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if seen != tc.wantCall {
				t.Fatalf("embedder called with %q, want %q", seen, tc.wantCall)
			}
			if got := store.got.QueryEmbedding != nil; got != tc.wantVec {
				t.Fatalf("embedding present=%v, want %v", got, tc.wantVec)
			}
		})
	}
}

// The response is exactly {"memories":[…]}, carrying labels and provenance —
// §7.3 says provenance is part of the answer, not an extra.
func TestListMemories_ResponseShape(t *testing.T) {
	store := &fakeMemories{out: []*agentdb.MemorySearchResult{{
		ID:               "mem-1",
		Labels:           agentdb.LabelSet{"kind": "rolling-summary", "worker": "email-answerer"},
		Snippet:          "customers ask about refunds first",
		Score:            0.0163,
		CreatedByWorker:  "email-answerer",
		CreatedBySession: "sess-1",
		CreatedAt:        1789000000123,
	}}}
	h := newMemoryHandlers(t, store, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/memories", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Memories []map[string]any `json:"memories"`
	}
	decodeInto(t, rec, &body)
	if len(body.Memories) != 1 {
		t.Fatalf("want 1 record, got %d", len(body.Memories))
	}
	first := body.Memories[0]
	for _, k := range []string{"id", "labels", "snippet", "score", "created_by_worker", "created_by_session", "created_at"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("record is missing %q (the pinned MemorySearchResult shape): %+v", first, k)
		}
	}
	if first["created_at"].(float64) != 1789000000123 {
		t.Fatalf("created_at must be the raw unix MILLISECONDS: %v", first["created_at"])
	}

	empty := &fakeMemories{}
	h = newMemoryHandlers(t, empty, identityFor("acme"))
	rec = do(h, http.MethodGet, "/agent/memories", "")
	if got := rec.Body.String(); got != "{\"memories\":[]}\n" {
		t.Fatalf("an empty result must be [], not null: %s", got)
	}
}

// Errors keep their posture: a bad selector is the caller's fault and carries the
// parser's own words; a non-Postgres store is a deployment fact, not a bad
// request, so it answers 501 like POST /agent/project-token does.
func TestListMemories_Errors(t *testing.T) {
	t.Run("400 with the parser's message on a bad selector", func(t *testing.T) {
		store := &fakeMemories{err: errors.New("agentdb: memory search selector: unexpected token \"~\"")}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodGet, "/agent/memories?selector=k~v", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if got := rec.Body.String(); got == "" || !strings.Contains(got, "unexpected token") {
			t.Fatalf("the parser's own message must survive: %q", got)
		}
	})

	t.Run("501 on a non-Postgres store", func(t *testing.T) {
		store := &fakeMemories{err: agentdb.ErrMemoryRequiresPostgres}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/memories", ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
	})
}

// Auth posture: 401 without an identity, 403 for a token carrying no project,
// 501 when the host wired no store, and no method other than GET.
func TestListMemories_AuthAndAvailability(t *testing.T) {
	t.Run("401 without identity", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, func(*http.Request) (Identity, error) {
			return Identity{}, http.ErrNoCookie
		})
		if rec := do(h, http.MethodGet, "/agent/memories", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d", rec.Code)
		}
		if store.call != 0 {
			t.Fatal("an unauthenticated request must not reach the store")
		}
	})

	t.Run("403 with no project claim", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor(""))
		if rec := do(h, http.MethodGet, "/agent/memories", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d", rec.Code)
		}
		if store.call != 0 {
			t.Fatal("a projectless token must not reach the store")
		}
	})

	t.Run("501 with no store", func(t *testing.T) {
		h := newMemoryHandlers(t, nil, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/memories", ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("mutating methods are not routed", func(t *testing.T) {
		// Memories are append-only (§7.1). POST is the ONE write — the append
		// route below — and there is still no way to change or remove a row.
		h := newMemoryHandlers(t, &fakeMemories{}, identityFor("acme"))
		for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
			if rec := do(h, m, "/agent/memories", `{}`); rec.Code == http.StatusOK {
				t.Fatalf("%s must not be served", m)
			}
		}
	})
}

// The route is where the UI expects it, and it is mounted by Mux().
func TestListMemories_Endpoint(t *testing.T) {
	if DefaultEndpoints.ListMemories != "GET /agent/memories" {
		t.Fatalf("route moved: %q", DefaultEndpoints.ListMemories)
	}
}

// PROJECT ISOLATION and the live relevance contract, against the real store: two
// projects append their own memories and neither route can see the other's,
// whatever the query says.
func TestListMemories_LivePG(t *testing.T) {
	url := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL not set — skipping live Postgres test")
	}
	store, err := agentdb.Open(url)
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	// Release the pool when this test ends. Registered here so it runs LAST
	// (t.Cleanup is LIFO) — data cleanups registered below still need it open.
	t.Cleanup(func() { _ = store.Close() })
	mine, theirs := "memread-mine", "memread-theirs"
	t.Cleanup(func() {
		for _, p := range []string{mine, theirs} {
			_ = store.DB().Exec("DELETE FROM memories WHERE project = ?", p).Error
		}
	})

	seed := func(project, content string, labels agentdb.LabelSet) {
		t.Helper()
		if _, _, err := store.CreateMemory(context.Background(), &agentdb.Memory{
			Project: project, Labels: labels, Content: content,
			CreatedByWorker: "email-answerer", CreatedBySession: "sess-1",
		}, nil); err != nil {
			t.Fatalf("seed %s: %v", project, err)
		}
	}
	seed(mine, "customers ask about refunds before anything else", agentdb.LabelSet{"kind": "rolling-summary", "worker": "email-answerer"})
	seed(mine, "the office is closed on Fridays", agentdb.LabelSet{"kind": "note"})
	seed(theirs, "their secret refunds policy", agentdb.LabelSet{"kind": "rolling-summary"})

	read := func(project, query string) []*agentdb.MemorySearchResult {
		t.Helper()
		h := newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{},
			Identity: identityFor(project), AgentDB: store,
		})
		rec := do(h, http.MethodGet, "/agent/memories"+query, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("read %s%s: status=%d body=%s", project, query, rec.Code, rec.Body)
		}
		var body struct {
			Memories []*agentdb.MemorySearchResult `json:"memories"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Memories
	}

	got := read(mine, "")
	if len(got) != 2 {
		t.Fatalf("a project must see exactly its own memories, got %d: %+v", len(got), got)
	}
	// No query text ⇒ newest first (§7.6.2).
	if got[0].Snippet != "the office is closed on Fridays" {
		t.Fatalf("no query text must answer newest-first: %+v", got)
	}
	if got[0].CreatedByWorker != "email-answerer" || got[0].CreatedBySession != "sess-1" {
		t.Fatalf("provenance must ride on the row: %+v", got[0])
	}

	// The selector filters within the project…
	if got := read(mine, "?selector=kind%3Dnote"); len(got) != 1 || got[0].Labels["kind"] != "note" {
		t.Fatalf("selector filter is wrong: %+v", got)
	}
	// …and cannot be used to reach across it, nor can a project= parameter.
	if got := read(mine, "?query=refunds&project="+theirs); len(got) != 1 ||
		got[0].Snippet != "customers ask about refunds before anything else" {
		t.Fatalf("a project= query must never cross the boundary, got %+v", got)
	}
	if got := read(theirs, ""); len(got) != 1 || got[0].Snippet != "their secret refunds policy" {
		t.Fatalf("the other project's own memories are wrong: %+v", got)
	}

	// A malformed selector is the caller's fault, reported with the parser's words.
	h := newHandlers(t, Config{
		Runner: stubRunner{}, Store: stubStore{},
		Identity: identityFor(mine), AgentDB: store,
	})
	if rec := do(h, http.MethodGet, "/agent/memories?selector=kind%20in%20(a", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("a bad selector must be 400: status=%d body=%s", rec.Code, rec.Body)
	}
}

// ---------------------------------------------------------------------------
// T18 — the full-content read routes
// ---------------------------------------------------------------------------

// THE reason these routes exist: GET /agent/memories hands back a 500-byte
// snippet, so an embedding app rendering state from memory needs a read that
// does not stop mid-sentence.
func TestGetMemory_ReturnsFullContentUntruncated(t *testing.T) {
	mem := bigMemory()
	if len(mem.Content) <= 500 {
		t.Fatalf("this test is only meaningful past the snippet cut, got %d bytes", len(mem.Content))
	}
	store := &fakeMemories{one: mem}
	h := newMemoryHandlers(t, store, identityFor("acme"))

	rec := do(h, http.MethodGet, "/agent/memories/mem-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	decodeInto(t, rec, &body)
	if body["content"] != mem.Content {
		t.Fatalf("content must come back whole: got %d bytes, want %d", len(body["content"].(string)), len(mem.Content))
	}
	if _, ok := body["snippet"]; ok {
		t.Fatal("this route answers with content; a snippet key would invite a client to read the short one")
	}
	// Provenance rides on the record here exactly as it does on a search hit
	// (§7.3): the caller must be able to say which worker wrote this, and when.
	for _, k := range []string{"id", "labels", "content", "created_by_worker", "created_by_session", "created_at"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("record is missing %q: %+v", k, body)
		}
	}
	if body["created_at"].(float64) != 1789000000123 {
		t.Fatalf("created_at must be the raw unix MILLISECONDS: %v", body["created_at"])
	}
	// The project is the token's, never the path's — the store call is where
	// tenancy is decided, so it is the thing asserted.
	if store.gotProject != "acme" || store.gotID != "mem-1" {
		t.Fatalf("store call: project=%q id=%q", store.gotProject, store.gotID)
	}
}

// A memory belonging to another project is ErrMemoryNotFound from the store
// (agentdb/memories.go:152-172), and the route must not dress that up as
// anything an attacker can tell apart from a typo — mirroring the MCP tool's
// posture at cmd/agentd/mcp_memory.go:357-365.
func TestGetMemory_CrossProjectIsIndistinguishableFromAbsent(t *testing.T) {
	body := func(id string) (int, string) {
		t.Helper()
		// One store, one error: the point is that "exists elsewhere" and "never
		// existed" reach the handler identically and must leave identically.
		store := &fakeMemories{oneErr: agentdb.ErrMemoryNotFound}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodGet, "/agent/memories/"+id, "")
		return rec.Code, rec.Body.String()
	}
	foreignCode, foreignBody := body("mem-owned-by-theirs")
	absentCode, absentBody := body("mem-never-existed")
	if foreignCode != http.StatusNotFound || absentCode != http.StatusNotFound {
		t.Fatalf("both must be 404, got %d and %d", foreignCode, absentCode)
	}
	if foreignBody != absentBody {
		t.Fatalf("the two answers must be byte-identical:\n %q\n %q", foreignBody, absentBody)
	}
}

// memory_current's semantics, over HTTP: the newest memory labelled name=<n>.
func TestCurrentMemory(t *testing.T) {
	t.Run("the selector is exactly name=<n>", func(t *testing.T) {
		store := &fakeMemories{one: bigMemory()}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodGet, "/agent/memories/current?name=hypothesis-a", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if store.gotProject != "acme" || store.gotSelector != "name=hypothesis-a" {
			t.Fatalf("store call: project=%q selector=%q", store.gotProject, store.gotSelector)
		}
		var body map[string]any
		decodeInto(t, rec, &body)
		if body["content"] != bigMemory().Content {
			t.Fatal("current must answer with the whole body, like memory_current does")
		}
	})

	t.Run("nothing written under that name is a 404, not an error", func(t *testing.T) {
		store := &fakeMemories{oneErr: agentdb.ErrMemoryNotFound}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/memories/current?name=nothing-yet", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("a missing name is the caller's mistake", func(t *testing.T) {
		store := &fakeMemories{one: bigMemory()}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/memories/current", ""); rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if store.call != 0 {
			t.Fatal("an empty name must not reach the store: name= matches every unlabelled row")
		}
	})

	// The name is interpolated into selector text, so it is validated as a label
	// value first — the same guard memory_current applies (mcp_memory.go:384-389).
	// Without it a comma or '!' smuggles a second term into the query.
	t.Run("a name that is not a legal label value cannot smuggle a selector", func(t *testing.T) {
		for _, bad := range []string{"a,kind!=secret", "a=b", "hypothesis a", "a)"} {
			store := &fakeMemories{one: bigMemory()}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			rec := do(h, http.MethodGet, "/agent/memories/current?name="+url.QueryEscape(bad), "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("name %q: status=%d body=%s", bad, rec.Code, rec.Body)
			}
			if store.call != 0 {
				t.Fatalf("name %q reached the store as selector %q", bad, store.gotSelector)
			}
		}
	})

	// Route precedence, not decoration: `current` is a literal segment and must
	// win over the {id} wildcard, or the by-id handler answers this request with
	// a 404 for a memory called "current".
	t.Run("current is not swallowed by the by-id wildcard", func(t *testing.T) {
		store := &fakeMemories{one: bigMemory()}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/memories/current?name=hypothesis-a", ""); rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if store.gotID != "" {
			t.Fatalf("the by-id handler ran instead, with id=%q", store.gotID)
		}
	})
}

// Both routes carry the same auth/availability posture as GET /agent/memories,
// and neither grows a write counterpart: memories are append-only (§7.1).
func TestMemoryReadRoutes_AuthAndAvailability(t *testing.T) {
	paths := []string{"/agent/memories/mem-1", "/agent/memories/current?name=hypothesis-a"}

	for _, path := range paths {
		t.Run("401 without identity "+path, func(t *testing.T) {
			store := &fakeMemories{one: bigMemory()}
			h := newMemoryHandlers(t, store, func(*http.Request) (Identity, error) {
				return Identity{}, http.ErrNoCookie
			})
			if rec := do(h, http.MethodGet, path, ""); rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d", rec.Code)
			}
			if store.call != 0 {
				t.Fatal("an unauthenticated request must not reach the store")
			}
		})

		t.Run("403 with no project claim "+path, func(t *testing.T) {
			store := &fakeMemories{one: bigMemory()}
			h := newMemoryHandlers(t, store, identityFor(""))
			if rec := do(h, http.MethodGet, path, ""); rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d", rec.Code)
			}
			if store.call != 0 {
				t.Fatal("a projectless token has no namespace to read in")
			}
		})

		t.Run("501 with no store "+path, func(t *testing.T) {
			h := newMemoryHandlers(t, nil, identityFor("acme"))
			if rec := do(h, http.MethodGet, path, ""); rec.Code != http.StatusNotImplemented {
				t.Fatalf("status=%d", rec.Code)
			}
		})

		t.Run("501 on a non-Postgres store "+path, func(t *testing.T) {
			store := &fakeMemories{oneErr: agentdb.ErrMemoryRequiresPostgres}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			if rec := do(h, http.MethodGet, path, ""); rec.Code != http.StatusNotImplemented {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})

		t.Run("500 on a store outage "+path, func(t *testing.T) {
			// A database that is down is not a memory that is missing: answering
			// 404 sends an operator hunting for a row that is sitting right there.
			store := &fakeMemories{oneErr: errors.New("agentdb: get memory: connection refused")}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			if rec := do(h, http.MethodGet, path, ""); rec.Code != http.StatusInternalServerError {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
		})

		t.Run("no write methods "+path, func(t *testing.T) {
			h := newMemoryHandlers(t, &fakeMemories{one: bigMemory()}, identityFor("acme"))
			for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
				if rec := do(h, m, path, `{}`); rec.Code == http.StatusOK {
					t.Fatalf("%s must not be served", m)
				}
			}
		})
	}
}

// The routes are where docs/19-embedding.md will say they are, and Mux mounts
// them.
func TestMemoryReadRoutes_Endpoints(t *testing.T) {
	if DefaultEndpoints.GetMemory != "GET /agent/memories/{id}" {
		t.Fatalf("route moved: %q", DefaultEndpoints.GetMemory)
	}
	if DefaultEndpoints.CurrentMemory != "GET /agent/memories/current" {
		t.Fatalf("route moved: %q", DefaultEndpoints.CurrentMemory)
	}
}

// The proof that matters, against the real store: the SAME memory read through
// the search route is cut at 500 bytes and through the new routes is not.
func TestMemoryReadRoutes_LivePG(t *testing.T) {
	url := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL not set — skipping live Postgres test")
	}
	store, err := agentdb.Open(url)
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	// Release the pool when this test ends. Registered here so it runs LAST
	// (t.Cleanup is LIFO) — data cleanups registered below still need it open.
	t.Cleanup(func() { _ = store.Close() })
	mine, theirs := "memfull-mine", "memfull-theirs"
	t.Cleanup(func() {
		for _, p := range []string{mine, theirs} {
			_ = store.DB().Exec("DELETE FROM memories WHERE project = ?", p).Error
		}
	})

	ctx := context.Background()
	long := strings.Repeat("the AI bubble bursts and liquidity floods in. ", 40)
	seed := func(project, content string, labels agentdb.LabelSet) *agentdb.Memory {
		t.Helper()
		// The second return is "did the store actually persist an embedding"
		// (readiness S3/RD3). These fixtures pass a nil vector deliberately —
		// these tests are about full-content reads, not search — so it is
		// always false here and nothing turns on it.
		m, _, err := store.CreateMemory(ctx, &agentdb.Memory{
			Project: project, Labels: labels, Content: content,
			CreatedByWorker: "reviewer-a", CreatedBySession: "sess-1",
		}, nil)
		if err != nil {
			t.Fatalf("seed %s: %v", project, err)
		}
		return m
	}
	stale := seed(mine, "an older reading of the hypothesis", agentdb.LabelSet{"name": "hypothesis-a"})
	current := seed(mine, long, agentdb.LabelSet{"name": "hypothesis-a"})
	foreign := seed(theirs, "their private hypothesis", agentdb.LabelSet{"name": "hypothesis-a"})

	h := func(project string) *Handlers {
		return newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{},
			Identity: identityFor(project), AgentDB: store,
		})
	}

	// 1. The search route still truncates — this is the problem being solved, so
	//    it is asserted rather than assumed.
	rec := do(h(mine), http.MethodGet, "/agent/memories?selector=name%3Dhypothesis-a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status=%d body=%s", rec.Code, rec.Body)
	}
	var search struct {
		Memories []*agentdb.MemorySearchResult `json:"memories"`
	}
	decodeInto(t, rec, &search)
	if len(search.Memories) != 2 || len(search.Memories[0].Snippet) != 500 {
		t.Fatalf("expected the newest of two hits cut to 500 bytes, got %d hits, first %d bytes",
			len(search.Memories), len(search.Memories[0].Snippet))
	}

	// 2. …and the by-id route does not.
	rec = do(h(mine), http.MethodGet, "/agent/memories/"+current.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("by id: status=%d body=%s", rec.Code, rec.Body)
	}
	var got struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	decodeInto(t, rec, &got)
	if got.Content != long {
		t.Fatalf("by id returned %d bytes, want the whole %d", len(got.Content), len(long))
	}

	// 3. current takes the newest of the two under that name, in full.
	rec = do(h(mine), http.MethodGet, "/agent/memories/current?name=hypothesis-a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current: status=%d body=%s", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &got)
	if got.ID != current.ID || got.Content != long {
		t.Fatalf("current must be the newest match in full: id=%s (want %s, older is %s), %d bytes",
			got.ID, current.ID, stale.ID, len(got.Content))
	}

	// 4. Tenancy: the other project's memory is not reachable by id, and its own
	//    name= reading is its own. Both projects used the SAME name, which is the
	//    case that would break a route scoping on the label instead of the row.
	if rec := do(h(mine), http.MethodGet, "/agent/memories/"+foreign.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a foreign id must be 404: status=%d body=%s", rec.Code, rec.Body)
	}
	rec = do(h(theirs), http.MethodGet, "/agent/memories/current?name=hypothesis-a", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("theirs: status=%d body=%s", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &got)
	if got.ID != foreign.ID {
		t.Fatalf("each project reads its own name=hypothesis-a, got %s", got.ID)
	}

	// 5. A name nobody has written under is absent, not an error.
	if rec := do(h(mine), http.MethodGet, "/agent/memories/current?name=never-written", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

// TestListMemories_NarrowingParams pins the three query parameters added
// 2026-08-10 (design plan T5): they reach the store, they accept the same forms
// the MCP tools accept, and a malformed bound is the caller's error.
func TestListMemories_NarrowingParams(t *testing.T) {
	t.Run("all three reach the store", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		before := time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
		rr := do(h, http.MethodGet, "/agent/memories?since=7d&until=2026-08-10T00:00:00Z&latest_per=name", "")
		after := time.Now().Add(-7 * 24 * time.Hour).UnixMilli()
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
		}
		if store.got.Since < before || store.got.Since > after {
			t.Errorf("since = %d, want within [%d, %d]", store.got.Since, before, after)
		}
		if want := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC).UnixMilli(); store.got.Until != want {
			t.Errorf("until = %d, want %d", store.got.Until, want)
		}
		if store.got.LatestPer != "name" {
			t.Errorf("latest_per = %q, want %q", store.got.LatestPer, "name")
		}
	})

	t.Run("a malformed bound is 400 with the parser's message", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rr := do(h, http.MethodGet, "/agent/memories?since=last%20Tuesday", "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if !strings.Contains(rr.Body.String(), "relative age") {
			t.Errorf("body does not carry the parser's wording: %s", rr.Body.String())
		}
	})

	t.Run("absent params behave exactly as before", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rr := do(h, http.MethodGet, "/agent/memories?selector=kind%3Dfact", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		if store.got.Since != 0 || store.got.Until != 0 || store.got.LatestPer != "" {
			t.Fatalf("unset params must stay zero: %+v", store.got)
		}
	})
}

// ---------------------------------------------------------------------------
// The append route (O7) — POST /agent/memories.
//
// This is the ONE write on the memory surface, and the reason it exists is that
// an application embedding Orange had no way to hold state at all: the only
// writer was memory_create on the core MCP server, authenticated by a session
// token an embedder does not hold.
//
// What the tests below are really pinning is the TRUST ANCHOR. Provenance is
// stamped by the server, empty, from the caller's credential class — and a
// reader can therefore tell "written by the application" from "written from
// inside a container" and refuse to take state from the second.
// ---------------------------------------------------------------------------

func appendBody(t *testing.T, content string, labels map[string]string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"content": content}
	if labels != nil {
		body["labels"] = labels
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(raw)
}

// The route is where docs/19-embedding.md will say it is, and Mux mounts it.
func TestMemoryAppendRoute_Endpoint(t *testing.T) {
	if DefaultEndpoints.CreateMemory != "POST /agent/memories" {
		t.Fatalf("route moved: %q", DefaultEndpoints.CreateMemory)
	}
}

// The happy path, and the invariant the whole trust model rests on: what reaches
// the store carries the project from the credential and EMPTY provenance, and
// what comes back is the row the store returned, not the caller's struct.
func TestMemoryAppendRoute_StampsEmptyProvenance(t *testing.T) {
	stored := &agentdb.Memory{
		ID:      "mem-stored",
		Project: "acme",
		Labels:  agentdb.LabelSet{"kind": "hypothesis", "name": "hyp-1a2b3c4d", "status": "live"},
		Content: "the petrodollar thesis, as the application recorded it",
		// The store stamps these; the handler never invents them.
		CreatedAt: 1789000000123,
	}
	store := &fakeMemories{created: stored}
	h := newMemoryHandlers(t, store, identityFor("acme"))

	rec := do(h, http.MethodPost, "/agent/memories",
		appendBody(t, "the petrodollar thesis", map[string]string{
			"kind": "hypothesis", "name": "hyp-1a2b3c4d", "status": "live", "owner": "kai-at-badcode.dev",
		}, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if store.gotCreate == nil {
		t.Fatal("the append never reached the store")
	}
	if store.gotCreate.Project != "acme" {
		t.Fatalf("project = %q, want acme — it comes from the credential", store.gotCreate.Project)
	}
	if store.gotCreate.CreatedByWorker != "" || store.gotCreate.CreatedBySession != "" {
		t.Fatalf("provenance must be stamped EMPTY, got worker=%q session=%q",
			store.gotCreate.CreatedByWorker, store.gotCreate.CreatedBySession)
	}
	if store.gotCreate.Content != "the petrodollar thesis" {
		t.Fatalf("content = %q", store.gotCreate.Content)
	}
	if store.gotCreate.Labels["owner"] != "kai-at-badcode.dev" {
		t.Fatalf("labels = %v", store.gotCreate.Labels)
	}

	var got memoryRecordResp
	decodeInto(t, rec, &got)
	if got.ID != "mem-stored" || got.CreatedAt != 1789000000123 {
		t.Fatalf("the response must be the STORED row read back, got %+v", got)
	}
	if got.CreatedByWorker != "" || got.CreatedBySession != "" {
		t.Fatalf("the response must show empty provenance, got %+v", got)
	}
	if got.Content != stored.Content {
		t.Fatalf("content = %q, want the stored row's %q", got.Content, stored.Content)
	}
	// The whole memoryRecordResp shape, key for key — a client reading this
	// route and the two read routes must not have to branch.
	var keys map[string]any
	decodeInto(t, rec, &keys)
	for _, k := range []string{"id", "labels", "content", "created_by_worker", "created_by_session", "created_at"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("response is missing %q (the memoryRecordResp shape): %v", k, keys)
		}
	}
}

// Provenance in the body is REFUSED, not ignored. Silently dropping it would
// leave the caller believing it had attributed the memory to something — and
// the reader that trusts empty provenance would then be trusting a lie the
// caller thought it had told.
func TestMemoryAppendRoute_RejectsProvenanceInTheBody(t *testing.T) {
	for _, body := range []string{
		`{"content":"x","created_by_worker":"researcher"}`,
		`{"content":"x","created_by_session":"sess-1"}`,
		`{"content":"x","created_by_worker":""}`,
		`{"content":"x","created_by_session":null}`,
		`{"content":"x","created_by_worker":"a","created_by_session":"b"}`,
	} {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodPost, "/agent/memories", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status=%d, want 400 (rejected, never ignored)", body, rec.Code)
		}
		if store.call != 0 {
			t.Fatalf("body %s reached the store", body)
		}
		if !strings.Contains(rec.Body.String(), "created_by_") {
			t.Fatalf("the 400 must name the field: %q", rec.Body.String())
		}
	}
}

// The project is the credential's, and a project in the body is not a way to
// write into someone else's namespace (P5).
func TestMemoryAppendRoute_ProjectComesFromTheCredential(t *testing.T) {
	store := &fakeMemories{}
	h := newMemoryHandlers(t, store, identityFor("acme"))
	rec := do(h, http.MethodPost, "/agent/memories", `{"content":"x","project":"someone-else"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if store.gotCreate.Project != "acme" {
		t.Fatalf("project = %q, want acme", store.gotCreate.Project)
	}
}

// Auth: an API key and a console JWT both write; an embed token does not. An
// embed token is API-class — same secret, empty `sid` — and confined only by a
// scope claim that is checked on session-by-id routes and nowhere else. It
// reaches a browser inside a third-party page, so it must never mint the state
// the board is rendered from.
//
// A SESSION token is not testable here and deliberately so: it is signed with a
// different key and rejected 401 by agentd's middleware before any handler runs
// (cmd/agentd/auth.go, pinned by TestSessionTokenIsRejectedByProjectRoutes,
// which now lists this route).
func TestMemoryAppendRoute_AuthAndAvailability(t *testing.T) {
	const body = `{"content":"x"}`

	t.Run("401 without identity", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, func(*http.Request) (Identity, error) {
			return Identity{}, http.ErrNoCookie
		})
		if rec := do(h, http.MethodPost, "/agent/memories", body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d", rec.Code)
		}
		if store.call != 0 {
			t.Fatal("an unauthenticated request must not reach the store")
		}
	})

	t.Run("403 with no project claim", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor(""))
		if rec := do(h, http.MethodPost, "/agent/memories", body); rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d", rec.Code)
		}
		if store.call != 0 {
			t.Fatal("a projectless token has no namespace to write in")
		}
	})

	t.Run("403 for an embed token", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, func(*http.Request) (Identity, error) {
			return Identity{UserEmail: "api-key:acme", Customer: "acme", SessionScope: "s-hyp-a"}, nil
		})
		rec := do(h, http.MethodPost, "/agent/memories", body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body)
		}
		if store.call != 0 {
			t.Fatal("a session-scoped credential must not reach the store")
		}
	})

	t.Run("an unscoped credential does write", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodPost, "/agent/memories", body); rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("501 with no store", func(t *testing.T) {
		h := newMemoryHandlers(t, nil, identityFor("acme"))
		if rec := do(h, http.MethodPost, "/agent/memories", body); rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d", rec.Code)
		}
	})

	t.Run("501 on a non-Postgres store", func(t *testing.T) {
		store := &fakeMemories{createErr: agentdb.ErrMemoryRequiresPostgres}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodPost, "/agent/memories", body)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("500 on a store outage", func(t *testing.T) {
		store := &fakeMemories{createErr: errors.New("agentdb: create memory: connection refused")}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodPost, "/agent/memories", body); rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
	})
}

// Everything the caller can get wrong, answered before any database round-trip.
func TestMemoryAppendRoute_Validation(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		want     int
		contains string
		reaches  bool
	}{
		{name: "malformed JSON", body: `{`, want: http.StatusBadRequest, contains: "JSON"},
		{name: "no content", body: `{"labels":{"kind":"hypothesis"}}`, want: http.StatusBadRequest, contains: "content is required"},
		{name: "blank content", body: `{"content":"   "}`, want: http.StatusBadRequest, contains: "content is required"},
		{
			name:     "a label value that is not a label",
			body:     `{"content":"x","labels":{"kind":"a hypothesis, really"}}`,
			want:     http.StatusBadRequest,
			contains: "invalid",
		},
		{
			name:     "a label key that is not a label",
			body:     `{"content":"x","labels":{"not a key":"v"}}`,
			want:     http.StatusBadRequest,
			contains: "invalid",
		},
		{name: "labels are optional", body: `{"content":"x"}`, want: http.StatusCreated, reaches: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeMemories{}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			rec := do(h, http.MethodPost, "/agent/memories", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body)
			}
			if tc.contains != "" && !strings.Contains(rec.Body.String(), tc.contains) {
				t.Fatalf("body %q does not carry %q", rec.Body.String(), tc.contains)
			}
			if (store.call != 0) != tc.reaches {
				t.Fatalf("store calls = %d, wanted reaches=%v", store.call, tc.reaches)
			}
		})
	}
}

// The two size ceilings, and the remedy each one names — the same two
// memory_create applies, because the ceiling belongs to the store rather than to
// any one caller (agentdb/memories.go:235-256).
func TestMemoryAppendRoute_SizeCeilings(t *testing.T) {
	big := strings.Repeat("a", agentdb.MaxEmbeddedMemoryBytes+1)
	huge := strings.Repeat("a", agentdb.MaxMemoryBytes+1)

	t.Run("over 24KB with the default embed is 400 naming the remedy", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodPost, "/agent/memories", appendBody(t, big, nil, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"embed": false`) {
			t.Fatalf("the 400 must name the remedy: %q", rec.Body.String())
		}
		if store.call != 0 {
			t.Fatal("an oversized write must cost nothing")
		}
	})

	t.Run("over 24KB with embed false is stored whole", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		rec := do(h, http.MethodPost, "/agent/memories", appendBody(t, big, nil, map[string]any{"embed": false}))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if len(store.gotCreate.Content) != len(big) {
			t.Fatalf("content was truncated: %d bytes", len(store.gotCreate.Content))
		}
	})

	t.Run("over 1MB is 400 whatever embed says", func(t *testing.T) {
		for _, extra := range []map[string]any{nil, {"embed": false}} {
			store := &fakeMemories{}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			rec := do(h, http.MethodPost, "/agent/memories", appendBody(t, huge, nil, extra))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("embed=%v: status=%d", extra, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "artifact") {
				t.Fatalf("the 400 must name the remedy: %q", rec.Body.String())
			}
			if store.call != 0 {
				t.Fatal("an oversized write must cost nothing")
			}
		}
	})
}

// `embed` decides whether the host's embedder is asked at all, and its vector is
// what reaches the store. A host with no embedder wired writes the row anyway —
// keyword and label search still find it (§7.6.5).
func TestMemoryAppendRoute_Embedding(t *testing.T) {
	vec := make([]float32, agentdb.MemoryEmbeddingDim)
	vec[0] = 0.5

	newH := func(t *testing.T, store MemoryStore, calls *[]string) *Handlers {
		return newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{}, Identity: identityFor("acme"), Memories: store,
			MemoryEmbedder: func(_ context.Context, text string) []float32 {
				*calls = append(*calls, text)
				return vec
			},
		})
	}

	t.Run("the default asks the embedder and stores the vector", func(t *testing.T) {
		store := &fakeMemories{}
		var calls []string
		h := newH(t, store, &calls)
		if rec := do(h, http.MethodPost, "/agent/memories", `{"content":"refund policy"}`); rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if len(calls) != 1 || calls[0] != "refund policy" {
			t.Fatalf("embedder calls = %v, want one with the content", calls)
		}
		if len(store.gotEmbedding) != agentdb.MemoryEmbeddingDim {
			t.Fatalf("the vector did not reach the store: %d dims", len(store.gotEmbedding))
		}
	})

	t.Run("embed false never asks", func(t *testing.T) {
		store := &fakeMemories{}
		var calls []string
		h := newH(t, store, &calls)
		if rec := do(h, http.MethodPost, "/agent/memories", `{"content":"x","embed":false}`); rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if len(calls) != 0 {
			t.Fatalf("embedder was asked anyway: %v", calls)
		}
		if store.gotEmbedding != nil {
			t.Fatal("embed:false must store no vector")
		}
	})

	t.Run("no embedder wired still writes", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme")) // no MemoryEmbedder
		if rec := do(h, http.MethodPost, "/agent/memories", `{"content":"x"}`); rec.Code != http.StatusCreated {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if store.gotEmbedding != nil {
			t.Fatal("a host with no embedder must store no vector")
		}
	})

	// A database with no pgvector column cannot store the vector, and agentdb
	// refuses rather than writing a row that could never be embedded later.
	// That is a deployment fact, not a bad request — 501, with the store's own
	// message, which names `embed: false` as the way through.
	t.Run("501 when the vector cannot be stored", func(t *testing.T) {
		store := &fakeMemories{createErr: agentdb.ErrMemoryEmbeddingUnstorable}
		var calls []string
		h := newH(t, store, &calls)
		rec := do(h, http.MethodPost, "/agent/memories", `{"content":"x"}`)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "pgvector") {
			t.Fatalf("the store's own message must survive: %q", rec.Body.String())
		}
	})
}

// Against the real store: the row lands, it reads back through the two read
// routes with empty provenance, another project cannot see it, and — the point
// of the last assertion — the append writes NO config event. A memory is data,
// not configuration; if it appeared in the changelog every hypothesis tick would
// bury the project's actual configuration history.
func TestMemoryAppendRoute_LivePG(t *testing.T) {
	dsn := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL not set — skipping live Postgres test")
	}
	store, err := agentdb.Open(dsn)
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	// Release the pool when this test ends. Registered here so it runs LAST
	// (t.Cleanup is LIFO) — data cleanups registered below still need it open.
	t.Cleanup(func() { _ = store.Close() })
	mine, theirs := "memappend-mine", "memappend-theirs"
	t.Cleanup(func() {
		for _, p := range []string{mine, theirs} {
			_ = store.DB().Exec("DELETE FROM memories WHERE project = ?", p).Error
			_ = store.DB().Exec("DELETE FROM config_events WHERE project = ?", p).Error
		}
	})

	h := func(project string) *Handlers {
		return newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{},
			Identity: identityFor(project), AgentDB: store,
		})
	}

	countConfigEvents := func(project string) int64 {
		var n int64
		if err := store.DB().Raw("SELECT count(*) FROM config_events WHERE project = ?", project).Scan(&n).Error; err != nil {
			t.Fatalf("count config events: %v", err)
		}
		return n
	}
	before := countConfigEvents(mine)

	rec := do(h(mine), http.MethodPost, "/agent/memories",
		`{"labels":{"kind":"hypothesis","name":"hyp-1a2b3c4d","status":"live"},`+
			`"content":"the petrodollar is ending because of drone warfare","embed":false}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("append: status=%d body=%s", rec.Code, rec.Body)
	}
	var got memoryRecordResp
	decodeInto(t, rec, &got)
	if got.ID == "" || got.CreatedAt == 0 {
		t.Fatalf("the stored row must come back with its id and timestamp: %+v", got)
	}
	if got.CreatedByWorker != "" || got.CreatedBySession != "" {
		t.Fatalf("provenance must be empty: %+v", got)
	}
	// Milliseconds, like every other memory timestamp — a seconds value here
	// would sort a decade behind everything the workers write.
	if got.CreatedAt < 1_600_000_000_000 {
		t.Fatalf("created_at = %d, want unix MILLISECONDS", got.CreatedAt)
	}

	// It reads back whole, and `current` finds it under its name.
	rec = do(h(mine), http.MethodGet, "/agent/memories/"+got.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read back: status=%d body=%s", rec.Code, rec.Body)
	}
	var back memoryRecordResp
	decodeInto(t, rec, &back)
	if back.Content != "the petrodollar is ending because of drone warfare" || back.Labels["status"] != "live" {
		t.Fatalf("read back = %+v", back)
	}
	rec = do(h(mine), http.MethodGet, "/agent/memories/current?name=hyp-1a2b3c4d", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current: status=%d body=%s", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &back)
	if back.ID != got.ID {
		t.Fatalf("current = %s, want the row just appended %s", back.ID, got.ID)
	}

	// Tenancy: the other project sees nothing of it, by id or by name.
	if rec := do(h(theirs), http.MethodGet, "/agent/memories/"+got.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("another project read it: status=%d body=%s", rec.Code, rec.Body)
	}

	// No config event. Data, not configuration.
	if after := countConfigEvents(mine); after != before {
		t.Fatalf("the append wrote %d config event(s) — a memory is data, not configuration", after-before)
	}
}

// ---------------------------------------------------------------------------
// include_retracted (O11 of design/2026-08-20-agent-wolf.md) — the audit view.
//
// `retracts` is an ordinary label, so anything holding the core MCP tools can
// withdraw any row in its project — including one appended over HTTP as the
// application's own authoritative state. With the filter always on, that
// erasure is indistinguishable from the row never having existed. This
// parameter lets a caller that ALREADY holds project authority see what was
// withdrawn, and by whom, and decide for itself whether to honour it.
//
// Which is why the parameter is refused for the one API-class credential that
// is handed to a browser inside somebody else's page.
// ---------------------------------------------------------------------------

// The flag reaches the store when it is exactly `1`, and its absence is false —
// the pre-O11 behaviour, unchanged, for every caller that never heard of it.
func TestListMemories_IncludeRetractedReachesTheStore(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "absent means false", path: "/agent/memories", want: false},
		{name: "absent alongside other params", path: "/agent/memories?selector=kind%3Dstate&latest_per=name", want: false},
		{name: "1 turns it on", path: "/agent/memories?include_retracted=1", want: true},
		{name: "1 alongside the narrowing params", path: "/agent/memories?latest_per=name&include_retracted=1", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeMemories{}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			if rec := do(h, http.MethodGet, tc.path, ""); rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if store.got.IncludeRetracted != tc.want {
				t.Fatalf("IncludeRetracted = %v, want %v (query %+v)", store.got.IncludeRetracted, tc.want, store.got)
			}
		})
	}
}

// Anything other than `1` is the caller's error, named. `0`, `true` and the
// empty string are the three a caller is most likely to send believing it had
// said something — and the one thing none of them may do is quietly mean false,
// because a client that thinks it asked for the audit view and got the filtered
// one will read an erasure as an absence.
func TestListMemories_IncludeRetractedRejectsEveryOtherValue(t *testing.T) {
	for _, raw := range []string{"0", "true", "false", "", "yes", "1,1", "01"} {
		t.Run("value "+strconv.Quote(raw), func(t *testing.T) {
			store := &fakeMemories{}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			rec := do(h, http.MethodGet, "/agent/memories?include_retracted="+url.QueryEscape(raw), "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), "include_retracted") {
				t.Errorf("the message must name the parameter: %q", rec.Body.String())
			}
			if store.call != 0 {
				t.Error("a malformed parameter must not reach the store")
			}
		})
	}

	// `?include_retracted` with no `=` is present-but-empty, and is refused for
	// the same reason as `=`: r.URL.Query().Get cannot tell it from absent.
	store := &fakeMemories{}
	h := newMemoryHandlers(t, store, identityFor("acme"))
	if rec := do(h, http.MethodGet, "/agent/memories?include_retracted", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bare flag: status=%d body=%s, want 400", rec.Code, rec.Body)
	}
}

// An embed token is API-class — same secret, empty `sid` — and DOES reach this
// handler (hazard H1 of docs/19-embedding.md). It is minted for a browser
// inside a third-party page, which is the same blast radius as a container's
// output, so it may read memories and may not read the audit view of them.
//
// A real SESSION token cannot get this far at all: it is signed with a
// different key and agentd's middleware rejects any non-empty `sid` with 401
// before routing, pinned by TestSessionTokenIsRejectedByProjectRoutes
// (cmd/agentd/sessionsecret_test.go), whose route list now carries this one.
func TestListMemories_IncludeRetractedIsRefusedForASessionScopedCredential(t *testing.T) {
	embedToken := func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "api-key:acme", Customer: "acme", SessionScope: "s-hyp-a"}, nil
	}

	t.Run("403 with the flag", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, embedToken)
		rec := do(h, http.MethodGet, "/agent/memories?include_retracted=1", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body)
		}
		if store.call != 0 {
			t.Fatal("the refusal must land before the query is built")
		}
	})

	t.Run("the ordinary read is untouched", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, embedToken)
		if rec := do(h, http.MethodGet, "/agent/memories?selector=kind%3Dstate", ""); rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s, want 200 — O11 restricts the flag, not the route", rec.Code, rec.Body)
		}
		if store.got.IncludeRetracted {
			t.Error("the flag was not asked for")
		}
	})

	t.Run("an unscoped credential may ask", func(t *testing.T) {
		store := &fakeMemories{}
		h := newMemoryHandlers(t, store, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/memories?include_retracted=1", ""); rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
		}
		if !store.got.IncludeRetracted {
			t.Error("a project credential must reach the audit view")
		}
	})
}

// The two full-content reads are unchanged: they answer one memory, whole, and
// GetMemory was already deliberately unfiltered. The flag is a search-side
// facility, so on these routes it is inert — not an error, and not a new field.
func TestListMemories_IncludeRetractedIsInertOnTheSingleRecordRoutes(t *testing.T) {
	for _, path := range []string{
		"/agent/memories/mem-1?include_retracted=1",
		"/agent/memories/current?name=hypothesis-a&include_retracted=1",
	} {
		t.Run(path, func(t *testing.T) {
			store := &fakeMemories{one: bigMemory()}
			h := newMemoryHandlers(t, store, identityFor("acme"))
			rec := do(h, http.MethodGet, path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			var body map[string]any
			decodeInto(t, rec, &body)
			if _, ok := body["retracted_by"]; ok {
				t.Fatalf("a single-record read must not gain retracted_by: %+v", body)
			}
			if body["content"] != bigMemory().Content {
				t.Fatalf("the record changed: %+v", body)
			}
		})
	}
}

// The whole contract against the real store: the withdrawn row comes back with
// its retractor's provenance, it wins its name's slot in the latest_per
// reduction, and the same request without the flag answers the row beneath it
// with no retracted_by anywhere. This pair is what W5's tamper detection reads.
func TestListMemories_IncludeRetractedLivePG(t *testing.T) {
	dsn := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL not set — skipping live Postgres test")
	}
	store, err := agentdb.Open(dsn)
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	// Release the pool when this test ends. Registered here so it runs LAST
	// (t.Cleanup is LIFO) — data cleanups registered below still need it open.
	t.Cleanup(func() { _ = store.Close() })
	// A project name nothing else can collide with: a fixed one leaves rows
	// behind when a run is killed mid-test, and the latest_per assertions below
	// would then fail for a reason that has nothing to do with the code.
	project := "memaudit-" + uuid.New().String()
	t.Cleanup(func() { _ = store.DB().Exec("DELETE FROM memories WHERE project = ?", project).Error })

	// Explicit, distinct timestamps: CreateMemory stamps time.Now().UnixMilli()
	// when CreatedAt is zero, so rows seeded back-to-back can share a
	// millisecond and which one is "latest" for the name — the whole contract
	// here — would be decided by insert latency instead of by the data.
	base := int64(1_700_000_000_000)
	seed := func(content string, labels agentdb.LabelSet, worker, session string, createdAt int64) *agentdb.Memory {
		t.Helper()
		m, _, err := store.CreateMemory(context.Background(), &agentdb.Memory{
			Project: project, Labels: labels, Content: content,
			CreatedByWorker: worker, CreatedBySession: session,
			CreatedAt: createdAt,
		}, nil)
		if err != nil {
			t.Fatalf("seed %q: %v", content, err)
		}
		return m
	}
	older := seed("hypothesis A: proposed", agentdb.LabelSet{"kind": "state", "name": "hyp-a"}, "", "", base)
	newer := seed("hypothesis A: live", agentdb.LabelSet{"kind": "state", "name": "hyp-a"}, "", "", base+1000)
	retraction := seed("ignore hypothesis A",
		agentdb.LabelSet{"kind": "retraction", agentdb.RetractionLabel: newer.ID}, "researcher", "sess-evil", base+2000)

	read := func(query string) []map[string]any {
		t.Helper()
		h := newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{},
			Identity: identityFor(project), AgentDB: store,
		})
		rec := do(h, http.MethodGet, "/agent/memories"+query, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("read %s: status=%d body=%s", query, rec.Code, rec.Body)
		}
		var body struct {
			Memories []map[string]any `json:"memories"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return body.Memories
	}

	// With the flag: the retracted row wins the name's slot, and says who took
	// it away — a session, therefore not the application's own word.
	got := read("?selector=kind%3Dstate&latest_per=name&include_retracted=1")
	if len(got) != 1 || got[0]["id"] != newer.ID {
		t.Fatalf("latest_per with the flag = %+v, want the retracted row %s", got, newer.ID)
	}
	raw, ok := got[0]["retracted_by"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("retracted_by = %+v, want one retraction", got[0]["retracted_by"])
	}
	first, _ := raw[0].(map[string]any)
	if first["memory_id"] != retraction.ID {
		t.Errorf("memory_id = %v, want the retracting memory's own id %s", first["memory_id"], retraction.ID)
	}
	if first["created_by_worker"] != "researcher" || first["created_by_session"] != "sess-evil" {
		t.Errorf("the retractor's provenance is the whole answer: %+v", first)
	}
	if first["created_at"].(float64) != float64(retraction.CreatedAt) {
		t.Errorf("created_at = %v, want the retraction's own %d (unix milliseconds)", first["created_at"], retraction.CreatedAt)
	}

	// Without it: the row beneath, and not one retracted_by key anywhere.
	got = read("?selector=kind%3Dstate&latest_per=name")
	if len(got) != 1 || got[0]["id"] != older.ID {
		t.Fatalf("latest_per without the flag = %+v, want the row beneath it %s", got, older.ID)
	}
	for _, row := range read("?selector=kind%3Dstate") {
		if _, ok := row["retracted_by"]; ok {
			t.Errorf("an ordinary search must be byte-identical to the pre-O11 shape: %+v", row)
		}
		if row["id"] == newer.ID {
			t.Errorf("the default must still hide the retracted row: %+v", row)
		}
	}
}
