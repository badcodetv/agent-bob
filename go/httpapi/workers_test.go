package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// fakeWorkerStore is an in-memory WorkersStore keyed by project+name. It mirrors
// the real store's contract closely enough to prove the handlers' behaviour:
// the project argument is honoured verbatim (so a leak shows up as a cross-
// project hit) and the same sentinel errors come back.
type fakeWorkerStore struct {
	rows map[string]*agentdb.Worker
	err  error // when set, every method returns it
	// The config-log actor the handler passed down, and how many writes it made.
	lastWrite agentdb.ConfigWrite
	writes    int
}

func newFakeWorkerStore(workers ...*agentdb.Worker) *fakeWorkerStore {
	f := &fakeWorkerStore{rows: map[string]*agentdb.Worker{}}
	for _, w := range workers {
		f.rows[w.Project+"/"+w.Name] = w
	}
	return f
}

func (f *fakeWorkerStore) ListWorkers(_ context.Context, project string) ([]*agentdb.Worker, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := []*agentdb.Worker{}
	for _, w := range f.rows {
		if w.Project == project {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeWorkerStore) GetWorker(_ context.Context, project, name string) (*agentdb.Worker, error) {
	if f.err != nil {
		return nil, f.err
	}
	w, ok := f.rows[project+"/"+name]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", agentdb.ErrWorkerNotFound, project, name)
	}
	return w, nil
}

func (f *fakeWorkerStore) UpsertWorker(_ context.Context, w *agentdb.Worker, cw agentdb.ConfigWrite) (*agentdb.Worker, error) {
	f.lastWrite = cw
	f.writes++
	if f.err != nil {
		return nil, f.err
	}
	if w.Name == "" {
		return nil, fmt.Errorf("%w: name is required", agentdb.ErrWorkerInvalid)
	}
	f.rows[w.Project+"/"+w.Name] = w
	return w, nil
}

func (f *fakeWorkerStore) DeleteWorker(_ context.Context, project, name string, cw agentdb.ConfigWrite) error {
	f.lastWrite = cw
	f.writes++
	if f.err != nil {
		return f.err
	}
	key := project + "/" + name
	if _, ok := f.rows[key]; !ok {
		return fmt.Errorf("%w: %s/%s", agentdb.ErrWorkerNotFound, project, name)
	}
	delete(f.rows, key)
	return nil
}

func workerHandlers(t *testing.T, store WorkersStore, identity IdentityFunc) *Handlers {
	t.Helper()
	if identity == nil {
		identity = okIdentity
	}
	return newHandlers(t, Config{
		Runner:   stubRunner{},
		Store:    stubStore{},
		Identity: identity,
		Workers:  store,
	})
}

func workerReq(method, path, name, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if name != "" {
		r.SetPathValue("name", name)
	}
	return r
}

func TestWorkersHTTP_ListAndGet(t *testing.T) {
	answerer := agentdb.NewWorker("acme", "email-answerer")
	answerer.Description = "answers inbound email"
	answerer.Briefing = agentdb.SelectorList{"kind=house-style"}
	archivist := agentdb.NewWorker("acme", "archivist")
	h := workerHandlers(t, newFakeWorkerStore(answerer, archivist), nil)

	rec := httptest.NewRecorder()
	h.ListWorkers(rec, workerReq("GET", "/agent/workers", "", ""))
	if rec.Code != 200 {
		t.Fatalf("list status %d body=%s", rec.Code, rec.Body)
	}
	var listed struct {
		Workers []*agentdb.Worker `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v (%s)", err, rec.Body)
	}
	if len(listed.Workers) != 2 || listed.Workers[0].Name != "archivist" {
		t.Fatalf("list body: %s", rec.Body)
	}

	rec = httptest.NewRecorder()
	h.GetWorker(rec, workerReq("GET", "/agent/workers/email-answerer", "email-answerer", ""))
	if rec.Code != 200 {
		t.Fatalf("get status %d body=%s", rec.Code, rec.Body)
	}
	var got agentdb.Worker
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode get: %v (%s)", err, rec.Body)
	}
	if got.Name != "email-answerer" || got.MaxInstances != 1 || !got.Enabled {
		t.Fatalf("get body: %+v", got)
	}
	if len(got.Briefing) != 1 || got.Briefing[0] != "kind=house-style" {
		t.Fatalf("briefing not serialised: %+v", got.Briefing)
	}

	rec = httptest.NewRecorder()
	h.GetWorker(rec, workerReq("GET", "/agent/workers/nobody", "nobody", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing worker: want 404, got %d (%s)", rec.Code, rec.Body)
	}
}

func TestWorkersHTTP_PutDefaultsAndEcho(t *testing.T) {
	tests := []struct {
		name             string
		body             string
		wantMaxInstances int
		wantEnabled      bool
		wantFrozen       bool
		wantBriefing     agentdb.SelectorList
	}{
		{
			name:             "omitted fields take spec defaults",
			body:             `{"description":"answers email"}`,
			wantMaxInstances: 1,
			wantEnabled:      true,
			wantFrozen:       false,
			wantBriefing:     nil,
		},
		{
			// The human path CAN freeze — this route is JWT-guarded, which is
			// the whole boundary of docs/product/10-topology-library.md §3.
			name:             "explicit frozen true freezes",
			body:             `{"frozen":true}`,
			wantMaxInstances: 1,
			wantEnabled:      true,
			wantFrozen:       true,
			wantBriefing:     nil,
		},
		{
			name:             "explicit values win",
			body:             `{"max_instances":4,"enabled":false,"briefing":["kind=house-style","topic=pricing"]}`,
			wantMaxInstances: 4,
			wantEnabled:      false,
			wantBriefing:     agentdb.SelectorList{"kind=house-style", "topic=pricing"},
		},
		{
			name:             "explicit enabled false is not swallowed",
			body:             `{"enabled":false}`,
			wantMaxInstances: 1,
			wantEnabled:      false,
			wantBriefing:     nil,
		},
		{
			name:             "empty briefing list is preserved",
			body:             `{"briefing":[]}`,
			wantMaxInstances: 1,
			wantEnabled:      true,
			wantBriefing:     agentdb.SelectorList{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeWorkerStore()
			h := workerHandlers(t, store, nil)
			rec := httptest.NewRecorder()
			h.PutWorker(rec, workerReq("PUT", "/agent/workers/email-answerer", "email-answerer", tc.body))
			if rec.Code != 200 {
				t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
			}
			// The response is the stored row read back (§9), not the request.
			var echoed agentdb.Worker
			if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil {
				t.Fatalf("decode echo: %v (%s)", err, rec.Body)
			}
			stored := store.rows["acme/email-answerer"]
			if stored == nil {
				t.Fatalf("nothing stored: %#v", store.rows)
			}
			if stored.Project != "acme" {
				t.Fatalf("project must come from the token, got %q", stored.Project)
			}
			if stored.MaxInstances != tc.wantMaxInstances || echoed.MaxInstances != tc.wantMaxInstances {
				t.Fatalf("max_instances: want %d, stored %d, echoed %d",
					tc.wantMaxInstances, stored.MaxInstances, echoed.MaxInstances)
			}
			if stored.Enabled != tc.wantEnabled || echoed.Enabled != tc.wantEnabled {
				t.Fatalf("enabled: want %v, stored %v, echoed %v",
					tc.wantEnabled, stored.Enabled, echoed.Enabled)
			}
			if stored.Frozen != tc.wantFrozen || echoed.Frozen != tc.wantFrozen {
				t.Fatalf("frozen: want %v, stored %v, echoed %v",
					tc.wantFrozen, stored.Frozen, echoed.Frozen)
			}
			if len(stored.Briefing) != len(tc.wantBriefing) {
				t.Fatalf("briefing: want %#v, got %#v", tc.wantBriefing, stored.Briefing)
			}
			if (tc.wantBriefing == nil) != (stored.Briefing == nil) {
				t.Fatalf("briefing nil-ness: want nil=%v, got %#v", tc.wantBriefing == nil, stored.Briefing)
			}
		})
	}
}

// Freeze and unfreeze are BOTH ordinary PUTs on the human path (F1): the same
// route that toggles `enabled` toggles `frozen`, and the false direction must
// not be swallowed — an unfreeze that silently stayed frozen would lock the
// worker away from the very humans the flag reserves it for.
func TestWorkersHTTP_FreezeAndUnfreezeRoundTrip(t *testing.T) {
	store := newFakeWorkerStore()
	h := workerHandlers(t, store, nil)

	put := func(body string) agentdb.Worker {
		t.Helper()
		rec := httptest.NewRecorder()
		h.PutWorker(rec, workerReq("PUT", "/agent/workers/quality-scorer", "quality-scorer", body))
		if rec.Code != 200 {
			t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
		}
		var echoed agentdb.Worker
		if err := json.Unmarshal(rec.Body.Bytes(), &echoed); err != nil {
			t.Fatalf("decode echo: %v (%s)", err, rec.Body)
		}
		return echoed
	}

	if got := put(`{"description":"scores email","frozen":true}`); !got.Frozen {
		t.Fatalf("freeze via PUT did not stick: %+v", got)
	}
	if store.rows["acme/quality-scorer"].Frozen != true {
		t.Fatalf("freeze not stored")
	}
	if got := put(`{"description":"scores email","frozen":false}`); got.Frozen {
		t.Fatalf("unfreeze via PUT did not stick: %+v", got)
	}
	if store.rows["acme/quality-scorer"].Frozen != false {
		t.Fatalf("unfreeze not stored")
	}
	// DI11 inverted this. It read "an omitted field means false, per this
	// route's replace semantics" — which pinned the RULE, not a safety
	// property. The safety property is the one above: an explicit unfreeze
	// must land. An OMITTED frozen now keeps the stored value, because a save
	// that says nothing about freezing must not quietly thaw a worker a human
	// froze.
	put(`{"description":"scores email","frozen":true}`)
	if got := put(`{"description":"scores email"}`); !got.Frozen {
		t.Fatalf("an omitted frozen must KEEP the stored true (DI11), got %+v", got)
	}
	if !store.rows["acme/quality-scorer"].Frozen {
		t.Fatalf("the store must still hold frozen=true after a PUT that omitted it")
	}
}

func TestWorkersHTTP_PutRejectsBadInput(t *testing.T) {
	h := workerHandlers(t, newFakeWorkerStore(), nil)

	rec := httptest.NewRecorder()
	h.PutWorker(rec, workerReq("PUT", "/agent/workers/x", "x", `{not json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed json: want 400, got %d", rec.Code)
	}

	// A validation failure from the store surfaces as 400, never 500.
	store := newFakeWorkerStore()
	store.err = fmt.Errorf("%w: name %q is not kebab-case", agentdb.ErrWorkerInvalid, "Bad Name")
	h = workerHandlers(t, store, nil)
	rec = httptest.NewRecorder()
	h.PutWorker(rec, workerReq("PUT", "/agent/workers/Bad%20Name", "Bad Name", `{}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid worker: want 400, got %d (%s)", rec.Code, rec.Body)
	}

	// Anything else is a 500.
	store = newFakeWorkerStore()
	store.err = errors.New("database on fire")
	h = workerHandlers(t, store, nil)
	rec = httptest.NewRecorder()
	h.PutWorker(rec, workerReq("PUT", "/agent/workers/x", "x", `{}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("store failure: want 500, got %d (%s)", rec.Code, rec.Body)
	}
}

func TestWorkersHTTP_Delete(t *testing.T) {
	store := newFakeWorkerStore(agentdb.NewWorker("acme", "archivist"))
	h := workerHandlers(t, store, nil)

	rec := httptest.NewRecorder()
	h.DeleteWorker(rec, workerReq("DELETE", "/agent/workers/archivist", "archivist", ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (%s)", rec.Code, rec.Body)
	}
	if len(store.rows) != 0 {
		t.Fatalf("row not deleted: %#v", store.rows)
	}

	rec = httptest.NewRecorder()
	h.DeleteWorker(rec, workerReq("DELETE", "/agent/workers/archivist", "archivist", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: want 404, got %d", rec.Code)
	}
}

// The project comes from the token and nowhere else: a caller scoped to "other"
// must not read, overwrite, or delete acme's worker, and must not be able to
// smuggle a project in the request body.
func TestWorkersHTTP_ProjectIsolation(t *testing.T) {
	acmeWorker := agentdb.NewWorker("acme", "email-answerer")
	acmeWorker.SystemPrompt = "acme prompt"
	store := newFakeWorkerStore(acmeWorker)

	otherIdentity := func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "eve@other.com", Customer: "other"}, nil
	}
	h := workerHandlers(t, store, otherIdentity)

	rec := httptest.NewRecorder()
	h.ListWorkers(rec, workerReq("GET", "/agent/workers", "", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"workers":[]`) {
		t.Fatalf("list leaked across projects: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.GetWorker(rec, workerReq("GET", "/agent/workers/email-answerer", "email-answerer", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project get: want 404, got %d (%s)", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	h.DeleteWorker(rec, workerReq("DELETE", "/agent/workers/email-answerer", "email-answerer", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project delete: want 404, got %d", rec.Code)
	}
	if store.rows["acme/email-answerer"] == nil {
		t.Fatalf("acme's worker was deleted by a caller from another project")
	}

	// A body-supplied project is ignored — the token wins.
	rec = httptest.NewRecorder()
	h.PutWorker(rec, workerReq("PUT", "/agent/workers/email-answerer", "email-answerer",
		`{"project":"acme","system_prompt":"hijacked"}`))
	if rec.Code != 200 {
		t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
	}
	if store.rows["acme/email-answerer"].SystemPrompt != "acme prompt" {
		t.Fatalf("cross-project write leaked: %+v", store.rows["acme/email-answerer"])
	}
	if store.rows["other/email-answerer"] == nil {
		t.Fatalf("write landed outside the caller's project: %#v", store.rows)
	}
}

func TestWorkersHTTP_Unauthorized(t *testing.T) {
	h := workerHandlers(t, newFakeWorkerStore(), func(*http.Request) (Identity, error) {
		return Identity{}, errors.New("no token")
	})
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		req     *http.Request
	}{
		{"list", h.ListWorkers, workerReq("GET", "/agent/workers", "", "")},
		{"get", h.GetWorker, workerReq("GET", "/agent/workers/w", "w", "")},
		{"put", h.PutWorker, workerReq("PUT", "/agent/workers/w", "w", `{}`)},
		{"delete", h.DeleteWorker, workerReq("DELETE", "/agent/workers/w", "w", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, tc.req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
		})
	}
}

// With no worker store wired the routes are honestly unimplemented rather than
// silently returning an empty catalogue.
func TestWorkersHTTP_NotConfigured(t *testing.T) {
	h := newHandlers(t, Config{Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity})
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		req     *http.Request
	}{
		{"list", h.ListWorkers, workerReq("GET", "/agent/workers", "", "")},
		{"get", h.GetWorker, workerReq("GET", "/agent/workers/w", "w", "")},
		{"put", h.PutWorker, workerReq("PUT", "/agent/workers/w", "w", `{}`)},
		{"delete", h.DeleteWorker, workerReq("DELETE", "/agent/workers/w", "w", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, tc.req)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("want 501, got %d", rec.Code)
			}
		})
	}
}

// New() fills Workers from AgentDB so a host that already passes its agentdb
// store gets the worker routes for free — but a nil *agentdb.Store must not
// become a non-nil interface, which would turn the honest 501 into a panic.
func TestWorkersHTTP_StoreDefaultsFromAgentDB(t *testing.T) {
	t.Run("auto-filled from AgentDB", func(t *testing.T) {
		h := newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity,
			AgentDB: &agentdb.Store{},
		})
		if h.cfg.Workers == nil {
			t.Fatal("Workers should default to the AgentDB store")
		}
	})

	t.Run("explicit store wins over AgentDB", func(t *testing.T) {
		fake := newFakeWorkerStore(agentdb.NewWorker("acme", "archivist"))
		h := newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity,
			AgentDB: &agentdb.Store{}, Workers: fake,
		})
		rec := httptest.NewRecorder()
		h.ListWorkers(rec, workerReq("GET", "/agent/workers", "", ""))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "archivist") {
			t.Fatalf("explicit store not used: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("nil AgentDB leaves the routes unimplemented", func(t *testing.T) {
		var nilDB *agentdb.Store
		h := newHandlers(t, Config{
			Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity,
			AgentDB: nilDB,
		})
		if h.cfg.Workers != nil {
			t.Fatal("a nil *agentdb.Store must not become a non-nil WorkersStore")
		}
		rec := httptest.NewRecorder()
		h.ListWorkers(rec, workerReq("GET", "/agent/workers", "", ""))
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("want 501, got %d", rec.Code)
		}
	})
}

// The routes must be reachable through Mux() with their path wildcards bound,
// and *agentdb.Store must satisfy the WorkersStore seam.
func TestWorkersHTTP_MuxRouting(t *testing.T) {
	var _ WorkersStore = (*agentdb.Store)(nil)

	store := newFakeWorkerStore(agentdb.NewWorker("acme", "archivist"))
	h := workerHandlers(t, store, nil)
	mux := h.Mux()

	for _, tc := range []struct {
		method, path string
		body         string
		wantCode     int
	}{
		{"GET", "/agent/workers", "", 200},
		{"GET", "/agent/workers/archivist", "", 200},
		{"PUT", "/agent/workers/new-worker", `{"description":"d"}`, 200},
		{"DELETE", "/agent/workers/archivist", "", http.StatusNoContent},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var req *http.Request
			if tc.body == "" {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			} else {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("want %d, got %d (%s)", tc.wantCode, rec.Code, rec.Body)
			}
		})
	}
	if store.rows["acme/new-worker"] == nil {
		t.Fatalf("PUT through the mux did not reach the store: %#v", store.rows)
	}

	// A host carrying an Endpoints value from before the worker routes existed
	// leaves those four fields empty; registering an empty pattern would panic,
	// so they are guarded like Snapshot/Archive.
	noWorkerRoutes := DefaultEndpoints
	noWorkerRoutes.ListWorkers = ""
	noWorkerRoutes.GetWorker = ""
	noWorkerRoutes.PutWorker = ""
	noWorkerRoutes.DeleteWorker = ""
	older := newHandlers(t, Config{
		Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity, Workers: store,
		Endpoints: noWorkerRoutes,
	})
	m := older.Mux() // must not panic
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "/agent/workers", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmounted worker route: want 404, got %d", rec.Code)
	}
}

// T27 (from DI2, found live during T1): a PUT that omits a field must not
// erase it.
//
// The handler used to build a fresh agentdb.NewWorker and assign Description,
// SystemPrompt, MCPConfig, Image and Briefing from the body unconditionally, so
// installing a new prompt with {"system_prompt": …} wrote every other one of
// those five as its zero value. It did that silently: 200, and a read-back echo
// that looked right because it echoed what had just been stored. That is how
// the architect lost its briefing.
//
// Absent (or an explicit JSON null) now keeps the stored value; an explicit
// "", {} or [] still clears it, which is why these five could not simply be
// merged. Asserted against the STORE, never the echo — the echo is what made
// the original defect invisible.
func TestWorkersHTTP_PutKeepsOmittedFields(t *testing.T) {
	existing := func() *agentdb.Worker {
		w := agentdb.NewWorker("acme", "architect")
		w.Description = "designs the roster"
		w.SystemPrompt = "You are the architect."
		w.Image = "bob/architect:v3"
		w.MCPConfig = agentdb.JSONMap{"servers": "core"}
		w.Briefing = agentdb.SelectorList{"name=label-registry", "kind=charter"}
		return w
	}

	// One field at a time, so a failure names the field rather than the row.
	keeps := []struct {
		name string
		body string
	}{
		{"omitting briefing keeps it", `{"system_prompt":"You are the architect, revised."}`},
		{"null briefing keeps it", `{"briefing":null}`},
		{"omitting system_prompt keeps it", `{"description":"designs the roster, revised"}`},
		{"omitting mcp_config keeps it", `{"description":"designs the roster, revised"}`},
		{"omitting image keeps it", `{"description":"designs the roster, revised"}`},
		{"omitting description keeps it", `{"image":"bob/architect:v4"}`},
	}
	for _, tc := range keeps {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeWorkerStore(existing())
			h := workerHandlers(t, store, nil)
			rec := httptest.NewRecorder()
			h.PutWorker(rec, workerReq("PUT", "/agent/workers/architect", "architect", tc.body))
			if rec.Code != 200 {
				t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
			}
			stored := store.rows["acme/architect"]
			if stored == nil {
				t.Fatalf("nothing stored: %#v", store.rows)
			}
			want := existing()
			if !strings.Contains(tc.body, `"briefing"`) || strings.Contains(tc.body, `"briefing":null`) {
				if len(stored.Briefing) != len(want.Briefing) {
					t.Errorf("briefing: want %#v, got %#v", want.Briefing, stored.Briefing)
				}
			}
			if !strings.Contains(tc.body, `"system_prompt"`) && stored.SystemPrompt != want.SystemPrompt {
				t.Errorf("system_prompt: want %q, got %q", want.SystemPrompt, stored.SystemPrompt)
			}
			if !strings.Contains(tc.body, `"mcp_config"`) && len(stored.MCPConfig) != len(want.MCPConfig) {
				t.Errorf("mcp_config: want %#v, got %#v", want.MCPConfig, stored.MCPConfig)
			}
			if !strings.Contains(tc.body, `"image"`) && stored.Image != want.Image {
				t.Errorf("image: want %q, got %q", want.Image, stored.Image)
			}
			if !strings.Contains(tc.body, `"description"`) && stored.Description != want.Description {
				t.Errorf("description: want %q, got %q", want.Description, stored.Description)
			}
		})
	}

	// Clearing must stay possible, or Briefing becomes append-only over HTTP.
	clears := []struct {
		name  string
		body  string
		check func(*testing.T, *agentdb.Worker)
	}{
		{
			name: "explicit empty briefing clears it",
			body: `{"briefing":[]}`,
			check: func(t *testing.T, w *agentdb.Worker) {
				if len(w.Briefing) != 0 {
					t.Errorf("briefing: want cleared, got %#v", w.Briefing)
				}
			},
		},
		{
			name: "explicit empty system_prompt clears it",
			body: `{"system_prompt":""}`,
			check: func(t *testing.T, w *agentdb.Worker) {
				if w.SystemPrompt != "" {
					t.Errorf("system_prompt: want cleared, got %q", w.SystemPrompt)
				}
			},
		},
		{
			name: "explicit empty mcp_config clears it",
			body: `{"mcp_config":{}}`,
			check: func(t *testing.T, w *agentdb.Worker) {
				if len(w.MCPConfig) != 0 {
					t.Errorf("mcp_config: want cleared, got %#v", w.MCPConfig)
				}
			},
		},
		{
			name: "explicit empty image clears it",
			body: `{"image":""}`,
			check: func(t *testing.T, w *agentdb.Worker) {
				if w.Image != "" {
					t.Errorf("image: want cleared, got %q", w.Image)
				}
			},
		},
	}
	for _, tc := range clears {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeWorkerStore(existing())
			h := workerHandlers(t, store, nil)
			rec := httptest.NewRecorder()
			h.PutWorker(rec, workerReq("PUT", "/agent/workers/architect", "architect", tc.body))
			if rec.Code != 200 {
				t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
			}
			stored := store.rows["acme/architect"]
			if stored == nil {
				t.Fatalf("nothing stored: %#v", store.rows)
			}
			tc.check(t, stored)
		})
	}
}

// The other half of the rule: on CREATE there is no stored value, so an
// omitted field is its zero value and nothing about the existing behaviour
// changes. TestWorkersHTTP_PutDefaultsAndEcho covers the defaults; this pins
// that keep-on-absent did not accidentally teach the handler to look up a row
// that is not there.
func TestWorkersHTTP_PutCreateIsUnchangedByKeepOnAbsent(t *testing.T) {
	store := newFakeWorkerStore()
	h := workerHandlers(t, store, nil)
	rec := httptest.NewRecorder()
	h.PutWorker(rec, workerReq("PUT", "/agent/workers/newcomer", "newcomer", `{"description":"brand new"}`))
	if rec.Code != 200 {
		t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
	}
	stored := store.rows["acme/newcomer"]
	if stored == nil {
		t.Fatalf("nothing stored: %#v", store.rows)
	}
	if stored.Description != "brand new" {
		t.Errorf("description = %q", stored.Description)
	}
	if stored.SystemPrompt != "" || stored.Image != "" || stored.Briefing != nil || stored.MCPConfig != nil {
		t.Errorf("a created worker must carry zero values for what the body omitted: %+v", stored)
	}
}

// DI11: the three control fields keep on absent too, so the route has ONE rule.
//
// T27 made description, system_prompt, mcp_config, image and briefing keep
// their stored value when a PUT omits them, and deliberately left
// max_instances, enabled and frozen replacing to their defaults. That split
// meant a caller saving only a prompt silently thawed a frozen worker,
// re-enabled a disabled one and reset its concurrency to 1 — the three fields
// a human uses to CONTROL a worker, each failing in the unsafe direction. Every
// other writer already keeps: the agents' worker_update is a read-modify-write
// and the git importer is a field merge. This route was the odd one out.
func TestWorkersHTTP_PutKeepsOmittedControlFields(t *testing.T) {
	existing := func() *agentdb.Worker {
		w := agentdb.NewWorker("acme", "scorer")
		w.SystemPrompt = "You score answers."
		w.MaxInstances = 4
		w.Enabled = false
		w.Frozen = true
		return w
	}

	t.Run("omitted control fields keep the stored values", func(t *testing.T) {
		store := newFakeWorkerStore(existing())
		h := workerHandlers(t, store, nil)
		rec := httptest.NewRecorder()
		h.PutWorker(rec, workerReq("PUT", "/agent/workers/scorer", "scorer",
			`{"system_prompt":"You score answers, revised."}`))
		if rec.Code != 200 {
			t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
		}
		// Read from the STORE, never the echo — the echo is what hid DI2.
		got := store.rows["acme/scorer"]
		if !got.Frozen {
			t.Error("frozen: a prompt-only PUT thawed a frozen worker")
		}
		if got.Enabled {
			t.Error("enabled: a prompt-only PUT re-enabled a disabled worker")
		}
		if got.MaxInstances != 4 {
			t.Errorf("max_instances: want 4 kept, got %d", got.MaxInstances)
		}
		if got.SystemPrompt != "You score answers, revised." {
			t.Errorf("the field that WAS sent must still land, got %q", got.SystemPrompt)
		}
	})

	t.Run("explicit values still win, including false and a lower count", func(t *testing.T) {
		store := newFakeWorkerStore(existing())
		h := workerHandlers(t, store, nil)
		rec := httptest.NewRecorder()
		h.PutWorker(rec, workerReq("PUT", "/agent/workers/scorer", "scorer",
			`{"enabled":true,"frozen":false,"max_instances":2}`))
		if rec.Code != 200 {
			t.Fatalf("put status %d body=%s", rec.Code, rec.Body)
		}
		got := store.rows["acme/scorer"]
		if got.Frozen || !got.Enabled || got.MaxInstances != 2 {
			t.Errorf("explicit control values were not applied: %+v", got)
		}
		if got.SystemPrompt != "You score answers." {
			t.Errorf("an omitted prompt must still be kept (T27), got %q", got.SystemPrompt)
		}
	})

	t.Run("a store read that fails is refused, never written over", func(t *testing.T) {
		// Falling through to NewWorker's defaults here would be the same wipe,
		// arriving only when the database hiccups. So the read failing must
		// mean nothing is written at all.
		store := newFakeWorkerStore(existing())
		store.err = errors.New("connection reset")
		h := workerHandlers(t, store, nil)
		rec := httptest.NewRecorder()
		h.PutWorker(rec, workerReq("PUT", "/agent/workers/scorer", "scorer",
			`{"system_prompt":"You score answers, revised."}`))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("want 500 on a failed read, got %d body=%s", rec.Code, rec.Body)
		}
		if store.writes != 0 {
			t.Fatalf("a PUT whose read failed must not write; writes=%d", store.writes)
		}
	})
}
