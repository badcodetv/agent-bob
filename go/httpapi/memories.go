package httpapi

// The memory read route (spec 03-memory §7.6, design 15-operator-console B2).
//
//	GET /agent/memories
//	  query: ?selector=<k8s label selector>&query=<free text>&limit=<n>
//	         &since=<bound>&until=<bound>&latest_per=<label key>
//	         &include_retracted=1
//	         A bound is RFC3339, unix milliseconds, or a relative age ("7d").
//	  auth : the ordinary session JWT; the project comes from the Customer claim,
//	         never from the query (P5) — same posture as GET /agent/config-events.
//	  200  : {"memories": [MemorySearchResult, …]}
//	  400  : a malformed selector, reported with the parser's own message; or an
//	         include_retracted that is not exactly `1`
//	  403  : include_retracted asked for by a session-scoped (embed) credential
//	  501  : no store wired, or a store that is not Postgres
//
// include_retracted (O11 of design/2026-08-20-agent-wolf.md) is the audit view.
// `retracts` is an ordinary label, so anything holding the core MCP tools can
// withdraw any row in its project — including one appended through the POST
// below as the application's own authoritative state — and with the filter
// always on, that erasure is indistinguishable from the row never having
// existed. With the flag, withdrawn rows come back carrying EVERY retraction
// against them (`retracted_by`), each with its own provenance, so a reader can
// tell "the application took this back" from "something in a container erased
// it". It is available to project API keys and console JWTs only.
//
// This is the same §7.6 relevance contract the memory_search MCP tool calls, and
// deliberately the same one: selector filter, free text fused by RRF, recency as
// a tiebreak, limit defaulting to 20 and capped at 100 IN THE STORE. There are no
// weights, no modes and no extra knobs here — a second, subtly different search
// would be a second contract to keep true.
//
// Beside it, the two FULL-CONTENT reads (T18 of
// design/2026-08-06-embeddable-agent-bob.md):
//
//	GET /agent/memories/{id}
//	GET /agent/memories/current?name=<n>
//	  200  : one memory in full — content, not a snippet
//	  400  : `current` with no name, or a name that is not a legal label value
//	  404  : no such memory in this project, WHATEVER the reason
//
// They exist because the search result above carries a `snippet` cut at 500
// bytes (agentdb/memories.go:35,259-262) and has no Content field at all, so
// until now the only way to read a memory whole was from inside a container
// through the memory_get / memory_current MCP tools. An embedding application
// rendering project state from memory — the reason this plan exists — cannot
// live on 500 bytes.
//
// They are the SAME two store calls those tools make (Store.GetMemory and
// Store.NewestMemory, agentdb/memories.go:152,190), deliberately: a second query
// path would be a second set of scoping rules to keep true, and the scoping is
// the whole security story here.
//
// And, since O7 of design/2026-08-20-agent-wolf.md, the ONE write:
//
//	POST /agent/memories
//	  body : {"labels": {...}, "content": "...", "embed": true}
//	  auth : a project API key or a console JWT — NOT a session token (which the
//	         host's middleware rejects 401 before this handler runs) and NOT an
//	         embed token (403 here, on Identity.SessionScope).
//	  201  : the stored row, in memoryRecordResp shape
//	  400  : a body carrying provenance, bad labels, blank or oversized content
//	  403  : no project in the credential, or a session-scoped one
//	  501  : no store wired, a store that is not Postgres, or an embedding this
//	         database has no column to hold
//
// This file used to say memories were written by workers through their tools and
// that there was therefore no POST counterpart. That was true, and it was also
// the reason an application embedding Bob could hold no state of its own: the
// only write surface was memory_create on the core MCP server, authenticated by
// a session token an embedder does not hold.
//
// What the new route adds is not just a write — it is the TRUST ANCHOR. The
// server stamps provenance EMPTY, from the credential's class, and refuses a
// body that tries to supply it. Everything written from inside a container
// carries a worker name or a session id, so a reader can tell "the application
// wrote this" from "something in a container wrote this" and decline to take
// authoritative state from the second. A prompt-injected page read during deep
// research can append a memory; it cannot append one that looks like the
// application's.
//
// (Empty provenance is necessary, not sufficient: agentdb.ApplyTopology also
// writes provenance-free seeds and is reachable by any API-class credential. A
// reader that depends on this must also pin the label vocabulary it trusts.)
//
// Still append-only, and still no PUT and no DELETE anywhere on this file's
// routes: "changing" a memory is appending a newer one (§7.1).
//
// Postgres-only, like the store itself (jsonb selectors + tsvector). On the
// SQLite fallback the store returns ErrMemoryRequiresPostgres and these routes
// answer 501 — the same "not configured on this host" posture as
// POST /agent/project-token, rather than a 500 that reads like a bug.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// MemoryStore is the slice of agentdb.Store these routes need. It is now the
// SAME set the MCP tools take (cmd/agentd/mcp_memory.go:46-52) — create, get,
// search, newest — and note what is still not in it: no update and no delete, so
// the append-only invariant survives the seam (§7.1). *agentdb.Store satisfies
// it.
//
// GetMemory, NewestMemory and CreateMemory joined SearchMemories here rather
// than arriving as further Config fields: one field cannot be wired to two
// different stores by accident, and a host that has a memory store at all has
// these.
type MemoryStore interface {
	SearchMemories(ctx context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error)
	// CreateMemory appends one row. The second return says whether it landed
	// WITH an embedding, read back from the database rather than inferred from
	// the argument; this route does not report it (the response is the stored
	// row in memoryRecordResp shape, which has no such field), but the seam
	// keeps the store's signature rather than narrowing it, so *agentdb.Store
	// and the MCP tools' memoryStore stay one contract.
	CreateMemory(ctx context.Context, m *agentdb.Memory, embedding []float32) (*agentdb.Memory, bool, error)
	// GetMemory takes the project as an argument, not as a filter the caller
	// applies afterwards: a memory of another project is simply not found.
	GetMemory(ctx context.Context, project, id string) (*agentdb.Memory, error)
	// NewestMemory answers the newest match for a selector, in full. The `name=`
	// KV convention (§7.1) is what /agent/memories/current asks it.
	NewestMemory(ctx context.Context, project, selector string) (*agentdb.Memory, error)
}

// The concrete store must always satisfy the seam.
var _ MemoryStore = (*agentdb.Store)(nil)

// MemoryEmbedder supplies the embedding for the semantic leg. It is optional and
// returns nil freely: a nil embedder, or a nil return from a provider that
// failed, costs this one query its semantic leg and nothing else (§7.6.5 — the
// result shape never changes). The host wires it from whatever embedding
// provider it already built, which is why the seam is a func rather than the
// provider type: httpapi stays free of the extension packages.
//
// CreateMemory reuses it on the WRITE path, and there the "returns nil freely"
// clause bites differently: memories are never re-embedded (§7.1), so a provider
// outage during an append writes a row that is permanently invisible to semantic
// search, and this seam cannot say so — it has no error to return. The MCP tool
// fails the create instead (cmd/agentd/mcp_memory.go:313-319, the D2 asymmetry).
// Closing that gap needs a second, error-returning seam and a host wiring for
// it; until then the degradation is silent, and label and keyword search still
// find the row.
type MemoryEmbedder func(ctx context.Context, text string) []float32

// memoryReadable is the gate every memory route shares: a store must be wired,
// and the credential must name a project. Both answers are about the request's
// world rather than about any particular memory, so neither can leak one — which
// is why they run before the id or name is even looked at.
//
// It writes the error response and returns ok=false; the caller just returns.
func (h *Handlers) memoryReadable(w http.ResponseWriter, id Identity) bool {
	if h.cfg.Memories == nil {
		http.Error(w, "the memory store is not configured on this host", http.StatusNotImplemented)
		return false
	}
	if id.Customer == "" {
		// 403 and not 404: no memory is being hidden — the question cannot be
		// asked at all, because memories are namespaced by project and this
		// credential names none.
		http.Error(w, "no project in token", http.StatusForbidden)
		return false
	}
	return true
}

// memoryIncludeRetracted reads ?include_retracted, which is the audit view's
// switch (O11 of design/2026-08-20-agent-wolf.md).
//
// EXACTLY `1` turns it on and absence turns it off. Every other spelling — `0`,
// `true`, `yes`, the empty string, and `?include_retracted` with no `=` at all —
// is a 400 naming the parameter, and that strictness is the point rather than
// pedantry: the caller of this parameter is auditing whether its own state was
// erased, and a value that quietly degraded to "filtered" would hand it an
// erasure dressed as an absence. `strconv.ParseBool` is deliberately not used
// for the same reason — it would accept `0` and answer false.
//
// It writes the error response and returns ok=false; the caller just returns.
func memoryIncludeRetracted(w http.ResponseWriter, q url.Values) (bool, bool) {
	// Has(), not Get(): a present-but-empty parameter is a caller that thinks it
	// said something, and Get() cannot tell it from absent.
	if !q.Has("include_retracted") {
		return false, true
	}
	if q.Get("include_retracted") == "1" {
		return true, true
	}
	http.Error(w, `include_retracted: the only accepted value is 1 — omit the parameter for the default, `+
		`which hides retracted memories`, http.StatusBadRequest)
	return false, false
}

// ListMemories serves GET /agent/memories — the memory browser's read path.
func (h *Handlers) ListMemories(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.memoryReadable(w, id) {
		return
	}
	q := r.URL.Query()
	// include_retracted is settled BEFORE anything else about the query, because
	// both of its answers are refusals and neither depends on the rest of it.
	includeRetracted, ok := memoryIncludeRetracted(w, q)
	if !ok {
		return
	}
	if includeRetracted && id.SessionScope != "" {
		// An embed token: API-class (same secret, empty `sid`), but minted for a
		// browser inside a third-party page, so it reaches exactly the places a
		// container's output reaches. The audit view exists to catch an actor
		// with that reach erasing the application's own state; handing the view
		// to that actor tells it precisely which erasure was noticed.
		//
		// The ORDINARY read is untouched — this restricts the parameter, not the
		// route. A session token, meanwhile, never gets here at all: different
		// signing key, and agentd's middleware rejects a non-empty `sid` with 401
		// before routing (cmd/agentd/auth.go, doc 22 RD30).
		http.Error(w, "this credential is scoped to a single session and may not read retracted memories", http.StatusForbidden)
		return
	}
	text := q.Get("query")
	// The embedding is computed only when there is text to embed: an empty query
	// is a recency question, not a relevance one.
	var vec []float32
	if text != "" && h.cfg.MemoryEmbedder != nil {
		vec = h.cfg.MemoryEmbedder(r.Context(), text)
	}
	// since/until accept the same forms the MCP tools accept, parsed by the same
	// function (agentdb.ParseMSTime) so the two surfaces cannot drift. An
	// unparseable bound is the caller's error, reported with the parser's words.
	var since, until int64
	for _, bound := range []struct {
		param string
		dst   *int64
	}{{"since", &since}, {"until", &until}} {
		raw := strings.TrimSpace(q.Get(bound.param))
		if raw == "" {
			continue
		}
		ms, err := agentdb.ParseMSTime(raw, time.Now())
		if err != nil {
			http.Error(w, bound.param+": "+err.Error(), http.StatusBadRequest)
			return
		}
		*bound.dst = ms
	}

	rows, err := h.cfg.Memories.SearchMemories(r.Context(), &agentdb.MemorySearchQuery{
		Project:        id.Customer, // from the claim, always — never a parameter
		LabelSelector:  q.Get("selector"),
		Query:          text,
		QueryEmbedding: vec,
		Limit:          queryInt(r, "limit", 0),
		Since:          since,
		Until:          until,
		// The store validates the key and refuses an inverted range; this route
		// deliberately does not duplicate either check, because the catch-all
		// below already reports a store error as 400 with its own message.
		LatestPer:        strings.TrimSpace(q.Get("latest_per")),
		IncludeRetracted: includeRetracted,
	})
	if err != nil {
		if errors.Is(err, agentdb.ErrMemoryRequiresPostgres) {
			http.Error(w, err.Error(), http.StatusNotImplemented)
			return
		}
		// Everything else this call can fail with is the caller's selector, and
		// the parser's own message is the most useful thing to hand back.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if rows == nil {
		rows = []*agentdb.MemorySearchResult{}
	}
	writeJSON(w, map[string]any{"memories": rows})
}

// memoryNotFound is the one answer for absent, malformed and "belongs to another
// project". A memory id is a uuid and hard to guess, but `name=` values are
// chosen by whoever wrote them and are not — so both routes answer with this
// single string, and a caller cannot use either one as an existence oracle for a
// project it is not in. Same rule, same words as resolveSessionByName's 404
// (sessions_byname.go:124-129).
const memoryNotFound = "memory not found"

// memoryRecordResp is one memory in full. It mirrors the MCP tools' memoryRecord
// (cmd/agentd/mcp_memory.go:73-81) key for key so the two surfaces describe a
// memory identically, minus `session_url`: building a permalink needs the
// externally-reachable base URL, which the tool layer has and this package does
// not. `created_by_session` is here, so a client that has a base URL can build
// the same link itself.
//
// It is a distinct type rather than agentdb.Memory because that struct carries
// `project`, which is already the caller's own token claim and is noise in a
// per-project response.
type memoryRecordResp struct {
	ID               string            `json:"id"`
	Labels           map[string]string `json:"labels"`
	Content          string            `json:"content"`
	CreatedByWorker  string            `json:"created_by_worker"`
	CreatedBySession string            `json:"created_by_session"`
	CreatedAt        int64             `json:"created_at"`
}

func memoryRecordOf(mem *agentdb.Memory) memoryRecordResp {
	// Labels are copied into a plain map so an absent label set renders as {}
	// rather than null — a client should not have to branch on that.
	labels := make(map[string]string, len(mem.Labels))
	for k, v := range mem.Labels {
		labels[k] = v
	}
	return memoryRecordResp{
		ID:               mem.ID,
		Labels:           labels,
		Content:          mem.Content,
		CreatedByWorker:  mem.CreatedByWorker,
		CreatedBySession: mem.CreatedBySession,
		CreatedAt:        mem.CreatedAt,
	}
}

// GetMemory serves GET /agent/memories/{id} — one memory, whole.
func (h *Handlers) GetMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.memoryReadable(w, id) {
		return
	}
	memID := strings.TrimSpace(r.PathValue("id"))
	if memID == "" {
		// Belt and braces: the wildcard should never match an empty segment, and
		// an empty id reaching the store is an argument error (a 500) rather than
		// the 404 it plainly is.
		http.Error(w, memoryNotFound, http.StatusNotFound)
		return
	}
	// The project is the store call's first argument — tenancy decided in the
	// query, never by filtering a row that was already read. Same posture as the
	// memory_get tool (cmd/agentd/mcp_memory.go:357-365).
	mem, err := h.cfg.Memories.GetMemory(r.Context(), id.Customer, memID)
	if err != nil {
		writeMemoryReadError(w, err)
		return
	}
	writeJSON(w, memoryRecordOf(mem))
}

// CurrentMemory serves GET /agent/memories/current?name=<n> — the newest memory
// labelled name=<n>, in full. It is the HTTP twin of the memory_current tool
// (§7.3), and answers the same question an embedding app asks on every page
// render: "what is the current state of this thing".
//
// The literal `current` segment wins over {id} in Go's ServeMux precedence, so
// this route is reachable however a memory id happens to be spelled.
func (h *Handlers) CurrentMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.memoryReadable(w, id) {
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		// Not defaulted to "everything": a bare `name=` selector would match
		// every row with no name label and hand back an arbitrary memory.
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	// The name is interpolated into selector text, so it must be a legal label
	// value first — which is also what stops a comma, '=', '!' or parenthesis
	// smuggling a second term into the query (agentdb/labels.go:28-34). The tool
	// applies exactly this guard for exactly this reason (mcp_memory.go:384-389).
	if err := agentdb.ValidateLabelValue(name); err != nil {
		http.Error(w, "name: "+err.Error(), http.StatusBadRequest)
		return
	}
	mem, err := h.cfg.Memories.NewestMemory(r.Context(), id.Customer, "name="+name)
	if err != nil {
		writeMemoryReadError(w, err)
		return
	}
	writeJSON(w, memoryRecordOf(mem))
}

// writeMemoryReadError maps a single-memory store failure onto a status.
//
// The default is 500, NOT 404: a database that is refusing connections is not a
// memory that is missing, and answering "not found" would send an operator
// hunting for a row that is sitting right there. The 404 is reserved for the one
// error that genuinely means it.
func writeMemoryReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentdb.ErrMemoryNotFound):
		http.Error(w, memoryNotFound, http.StatusNotFound)
	case errors.Is(err, agentdb.ErrMemoryRequiresPostgres):
		http.Error(w, err.Error(), http.StatusNotImplemented)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ---------------------------------------------------------------------------
// The append route (O7 of design/2026-08-20-agent-wolf.md).
// ---------------------------------------------------------------------------

// createMemoryBody is POST /agent/memories' request.
//
// There is no `project` field: the project is the credential's, exactly as it is
// on every read here (P5). A `project` key in the body is ignored rather than
// refused, which is the same answer ?project= already gets on the read route.
//
// The two provenance keys ARE fields, and that is the point: they exist only so
// that a body carrying them can be REFUSED. Decoding them as json.RawMessage
// makes "the key is present" the test — including `"created_by_worker": ""` and
// `"created_by_session": null`, both of which a caller could otherwise send
// believing it had said something. Silently dropping them would leave that
// caller believing it had attributed the memory, while every reader downstream
// read the memory as the application's own word.
type createMemoryBody struct {
	Labels  map[string]string `json:"labels"`
	Content string            `json:"content"`
	// Embed is a POINTER so "absent" and "false" are distinguishable — absent
	// means true, the same default memory_create has.
	Embed *bool `json:"embed"`

	CreatedByWorker  json.RawMessage `json:"created_by_worker"`
	CreatedBySession json.RawMessage `json:"created_by_session"`
}

// CreateMemory serves POST /agent/memories — the one write on this surface.
//
// Every check that can be answered without the database is answered before it:
// an oversized or malformed append costs a round trip to nothing, and comes back
// with the sentence that fixes it.
func (h *Handlers) CreateMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	// Store-wired and project-claimed, the same two gates the reads share.
	if !h.memoryReadable(w, id) {
		return
	}
	// The one gate the reads do NOT share. A session-scoped credential is an
	// embed token: API-class (same secret, empty `sid`), confined only by a
	// scope claim that ownsSession checks on session-by-id routes and nowhere
	// else. It is minted for a browser inside a third-party page, so it reaches
	// exactly the places a container's output reaches — and a credential that
	// can reach a container must never mint state the application will later
	// read back as its own.
	//
	// A SESSION token cannot get this far at all: it is signed with a different
	// key, and the host's middleware rejects any token carrying a non-empty
	// `sid` with 401 before routing (cmd/agentd/auth.go, doc 22 RD30).
	if id.SessionScope != "" {
		http.Error(w, "this credential is scoped to a single session and may not append memories", http.StatusForbidden)
		return
	}

	var body createMemoryBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	// Provenance first, before anything that could make the request look
	// accepted-then-adjusted.
	for _, field := range []struct {
		name string
		raw  json.RawMessage
	}{{"created_by_worker", body.CreatedByWorker}, {"created_by_session", body.CreatedBySession}} {
		if field.raw != nil {
			http.Error(w, field.name+" is stamped by the server from your credential and cannot be set by the caller: "+
				"a memory appended through this route always has EMPTY provenance, which is exactly what marks it as the "+
				"application's own word rather than something written from inside a container. Remove the field.",
				http.StatusBadRequest)
			return
		}
	}
	if strings.TrimSpace(body.Content) == "" {
		http.Error(w, "content is required and must not be blank", http.StatusBadRequest)
		return
	}
	// The hard ceiling first, so a caller 2MB over is not told to retry with
	// `embed: false` and then refused again for a reason that was true all
	// along. Both ceilings are the STORE's (agentdb/memories.go:235-256) and are
	// checked here only so the answer is ours, before the round trip.
	if len(body.Content) > agentdb.MaxMemoryBytes {
		http.Error(w, fmt.Sprintf(
			"this memory is %d bytes, over the %d-byte ceiling for any memory — store the document as an artifact and keep a memory that points at it",
			len(body.Content), agentdb.MaxMemoryBytes), http.StatusBadRequest)
		return
	}
	wantEmbed := body.Embed == nil || *body.Embed
	if wantEmbed && len(body.Content) > agentdb.MaxEmbeddedMemoryBytes {
		http.Error(w, fmt.Sprintf(
			`this memory is %d bytes, over the %d-byte limit for meaning-based indexing — pass "embed": false to store it whole `+
				`(it stays searchable by label and by keyword), or split it`,
			len(body.Content), agentdb.MaxEmbeddedMemoryBytes), http.StatusBadRequest)
		return
	}
	// Validated here as well as in the store so the caller gets the validator's
	// specific complaint rather than a wrapped database error — and so nothing
	// reaches the embedding provider that the INSERT was going to reject anyway.
	// Same reason the memory_create tool double-checks (mcp_memory.go:294-297).
	if err := agentdb.ValidateLabels(agentdb.LabelSet(body.Labels)); err != nil {
		http.Error(w, "labels: "+err.Error(), http.StatusBadRequest)
		return
	}

	var vec []float32
	if wantEmbed && h.cfg.MemoryEmbedder != nil {
		vec = h.cfg.MemoryEmbedder(r.Context(), body.Content)
	}

	stored, _, err := h.cfg.Memories.CreateMemory(r.Context(), &agentdb.Memory{
		Project: id.Customer, // from the claim, always — never from the body
		Labels:  agentdb.LabelSet(body.Labels),
		Content: body.Content,
		// Named rather than left to the zero value, because they are the
		// invariant: this route's memories are the ones with NO author inside
		// the system, and a future edit that starts filling them in should have
		// to delete these two lines to do it.
		CreatedByWorker:  "",
		CreatedBySession: "",
	}, vec)
	if err != nil {
		writeMemoryWriteError(w, err)
		return
	}
	// §9 read-back: what the database holds, not the caller's struct — the same
	// rule memory_create follows, and the reason the response can be trusted as
	// the row a later GET will return.
	//
	// No config event is written, here or in the store: a memory is data, not
	// configuration (§15.4). If every append appeared in the changelog, one
	// hypothesis ticking daily would bury the project's actual configuration
	// history within a week.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(memoryRecordOf(stored))
}

// writeMemoryWriteError maps an append failure onto a status. It is the read
// path's twin (writeMemoryReadError) with one extra case and no 404: nothing is
// being looked up, so nothing can be missing.
//
// ErrMemoryEmbeddingUnstorable is 501 rather than 500: the request was
// well-formed and the store is healthy — this database simply has no
// content_embedding column to put the vector in (pgvector was unavailable when
// migration 022 ran), so the functionality required to fulfil the request is not
// supported here. The store's own message survives, and it names the way
// through: append it without an embedding.
func writeMemoryWriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentdb.ErrMemoryRequiresPostgres),
		errors.Is(err, agentdb.ErrMemoryEmbeddingUnstorable):
		http.Error(w, err.Error(), http.StatusNotImplemented)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
