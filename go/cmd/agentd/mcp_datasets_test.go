package main

// mcp_datasets_test.go — O6b. Every test function name begins with
// TestDatasetTools so the Validation filter (`-run 'TestDatasetTools'`) and this
// file agree; -run is an unanchored substring and a case named anything else is
// a case that silently never runs at the gate.
//
// No Docker and no database: the store, the exec seam, the blob store and the
// pull pipeline are all injected.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	agentkit "github.com/badcodetv/agent-bob"
	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/agentkittest"
	"github.com/badcodetv/agent-bob/execenv"
	"github.com/badcodetv/agent-bob/extension"
)

// ── Fakes ───────────────────────────────────────────────────────────────────

// fakeDatasetCatalog records what the tools asked for — the project argument is
// the whole tenancy story, so the tests need to see it.
type fakeDatasetCatalog struct {
	// current / versions answer the reads. A nil entry means not found.
	current  map[string]*agentdb.Dataset // project|name
	versions map[string]*agentdb.Dataset // project|name|version
	rows     []*agentdb.Dataset          // what ListDatasets returns

	listCalls   []datasetListCall
	created     []*agentdb.Dataset
	ifVersions  []int
	createErr   error
	currentErr  error
	listErr     error
	nextVersion int
}

type datasetListCall struct {
	project  string
	selector string
	limit    int
}

func newFakeDatasetCatalog() *fakeDatasetCatalog {
	return &fakeDatasetCatalog{
		current:     map[string]*agentdb.Dataset{},
		versions:    map[string]*agentdb.Dataset{},
		nextVersion: 1,
	}
}

func (f *fakeDatasetCatalog) CreateDatasetVersion(_ context.Context, d *agentdb.Dataset, ifVersion int) (*agentdb.Dataset, error) {
	f.created = append(f.created, d)
	f.ifVersions = append(f.ifVersions, ifVersion)
	if f.createErr != nil {
		return nil, f.createErr
	}
	// Echo a row the way the store does: defaults filled on a COPY, with the
	// version and id the database assigned rather than anything the caller sent
	// (O2's note — a caller that echoes its own input reads zeros).
	stored := *d
	stored.ID = fmt.Sprintf("ds-%d", len(f.created))
	stored.Version = f.nextVersion
	stored.CreatedAt = 1789000000123
	return &stored, nil
}

func (f *fakeDatasetCatalog) CurrentDataset(_ context.Context, project, name string) (*agentdb.Dataset, error) {
	if f.currentErr != nil {
		return nil, f.currentErr
	}
	if d, ok := f.current[project+"|"+name]; ok && d != nil {
		return d, nil
	}
	return nil, agentdb.ErrDatasetNotFound
}

func (f *fakeDatasetCatalog) GetDatasetVersion(_ context.Context, project, name string, version int) (*agentdb.Dataset, error) {
	if d, ok := f.versions[fmt.Sprintf("%s|%s|%d", project, name, version)]; ok && d != nil {
		return d, nil
	}
	return nil, agentdb.ErrDatasetNotFound
}

func (f *fakeDatasetCatalog) ListDatasets(_ context.Context, project, selector string, limit int) ([]*agentdb.Dataset, error) {
	f.listCalls = append(f.listCalls, datasetListCall{project: project, selector: selector, limit: limit})
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.rows) > limit {
		return f.rows[:limit], nil
	}
	return f.rows, nil
}

// fakeExecer is the sessionExecer seam: it records which session was exec'd
// into, because a session must only ever be able to read its OWN workspace.
type fakeExecer struct {
	refs    []agentkit.SessionRef
	session *fakeSession // reused from datasetpull_test.go
	err     error
}

func (f *fakeExecer) ExecInSession(ctx context.Context, ref agentkit.SessionRef, cmd []string, opts execenv.ExecOptions) (*execenv.ExecResult, error) {
	f.refs = append(f.refs, ref)
	if f.err != nil {
		return nil, f.err
	}
	return f.session.exec(ctx, cmd, opts)
}

// recordingBlobs wraps the in-memory blob store and records every Delete, so a
// test can assert the cleanup was ATTEMPTED rather than infer it from absence.
type recordingBlobs struct {
	*agentkittest.MemBlobs
	mu      sync.Mutex
	deleted []string
	failAll bool
}

func newRecordingBlobs() *recordingBlobs {
	return &recordingBlobs{MemBlobs: agentkittest.NewMemBlobs()}
}

func (b *recordingBlobs) Delete(ctx context.Context, key string) error {
	b.mu.Lock()
	b.deleted = append(b.deleted, key)
	b.mu.Unlock()
	if b.failAll {
		return errors.New("delete always fails (test)")
	}
	return b.MemBlobs.Delete(ctx, key)
}

// stored lists what is actually in the blob store, for the assertions about
// what a failed write left behind.
func (b *recordingBlobs) stored(t *testing.T) []string {
	t.Helper()
	keys, err := b.List(context.Background(), agentdb.DatasetBlobPrefix)
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}
	return keys
}

func (b *recordingBlobs) deletes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.deleted...)
}

const (
	testSelfURL       = "http://172.17.0.1:8099"
	testPublicBaseURL = "https://console.example.com"
)

var testDatasetSecret = []byte("dataset-tool-test-secret")

// identifiedCaller is what the MCP layer produces for a real session: a project
// from the token's customer claim, a session id from its sid claim, and
// Identified because the session ROW was read.
func identifiedCaller() mcpCaller {
	return mcpCaller{Project: "wolf", SessionID: "sess-1", Worker: "researcher", Identified: true}
}

func testDatasetTools(store datasetCatalog, exec sessionExecer, blobs extension.BlobStore) *datasetTools {
	return newDatasetTools(store, exec, blobs, testDatasetSecret, testSelfURL, 1<<20)
}

// seedDataset is one stored row, in the shape httpapi's tests use so the two
// pinned bodies can be compared field for field.
func seedDataset(project, name string, version int) *agentdb.Dataset {
	return &agentdb.Dataset{
		ID:               fmt.Sprintf("ds-%s-%d", name, version),
		Project:          project,
		Name:             name,
		Version:          version,
		Labels:           agentdb.LabelSet{"hypothesis": "1a2b3c4d", "metric": name},
		BlobPath:         agentdb.DatasetBlobPrefix + "deadbeef",
		SizeBytes:        40213,
		RowCount:         512,
		SHA256:           "abc123",
		ContentType:      "text/csv",
		CreatedByWorker:  "researcher",
		CreatedBySession: "sess-1",
		CreatedAt:        1789000000123,
	}
}

// ── Surface ─────────────────────────────────────────────────────────────────

// TestDatasetToolsSurface pins the whole surface: three tools, and nothing that
// deletes or mutates a version in place.
func TestDatasetToolsSurface(t *testing.T) {
	tools := testDatasetTools(newFakeDatasetCatalog(), &fakeExecer{}, newRecordingBlobs()).tools()
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.InputSchema == nil || tool.Handler == nil {
			t.Fatalf("tool %q is incompletely declared", tool.Name)
		}
	}
	want := []string{"dataset_list", "dataset_get", "dataset_put"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want exactly %v", names, want)
	}
}

// TestDatasetToolsRegisteredOnlyInThePostgresBlock reads main.go, because the
// criterion is about WHERE the registration line sits: inside the
// `if agentDB != nil` block that builds the core MCP server, so the dataset
// tools are absent off Postgres exactly like the rest of the core surface.
// Registering them anywhere else would compile and would mount tools whose
// store is nil.
func TestDatasetToolsRegisteredOnlyInThePostgresBlock(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	s := string(src)
	start := strings.Index(s, "mcpSrv := newMCPServer(")
	end := strings.Index(s, "root.Handle(coreMCPPath)")
	if end < 0 {
		end = strings.Index(s, "log.Printf(\"[agentd] core mcp:")
	}
	if start < 0 || end < 0 || end < start {
		t.Fatalf("could not locate the core MCP registration block in main.go")
	}
	block := s[start:end]
	if !strings.Contains(block, "mcpSrv.register(newDatasetTools(") {
		t.Fatalf("the dataset tools are not registered inside the core MCP block:\n%s", block)
	}
	if strings.Count(s, "newDatasetTools(") != 1 {
		t.Fatalf("newDatasetTools is wired %d times in main.go, want exactly one line", strings.Count(s, "newDatasetTools("))
	}
}

// TestDatasetToolsAppearInTheBootLogToolList: the boot line logs
// mcpSrv.toolNames(), so registering the three is what makes them appear. This
// asserts the registry actually accepts all three under their pinned names.
func TestDatasetToolsAppearInTheBootLogToolList(t *testing.T) {
	srv := newMCPServer(coreMCPServerName, func(*http.Request) (mcpCaller, error) { return mcpCaller{}, nil })
	srv.register(testDatasetTools(newFakeDatasetCatalog(), &fakeExecer{}, newRecordingBlobs()).tools()...)
	got := strings.Join(sortedStrings(srv.toolNames()), ",")
	if got != "dataset_get,dataset_list,dataset_put" {
		t.Fatalf("tools= would log %q", got)
	}
}

// ── dataset_list ────────────────────────────────────────────────────────────

// o5PinnedMetadataBody is the literal body go/httpapi/datasets_test.go's
// TestDatasetCurrent_IsTheBareObjectInThePinnedShape asserts on, copied here
// VERBATIM. The design pins ten field names as byte-identical between that HTTP
// response and dataset_list's entries; O5 could pin only its own half, because
// this ticket did not exist yet. This is the other half. If the two ever
// disagree, this test fails rather than an integrator's parser three tickets
// later.
const o5PinnedMetadataBody = `{"id":"ds-basket-7","name":"basket","version":7,` +
	`"labels":{"hypothesis":"1a2b3c4d","metric":"basket"},` +
	`"size_bytes":40213,"row_count":512,"sha256":"abc123","content_type":"text/csv",` +
	`"created_by_worker":"researcher","created_by_session":"sess-1","created_at":1789000000123}`

// TestDatasetToolsListShapeIsPinned asserts the entry shape as a LITERAL body —
// the envelope key, every field name, and created_at in unix milliseconds —
// and then checks that same set of names against O5's HTTP response.
func TestDatasetToolsListShapeIsPinned(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.rows = []*agentdb.Dataset{seedDataset("wolf", "basket", 7)}
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()

	res, err := invokeToolRaw(t, tools, "dataset_list", identifiedCaller(), map[string]any{})
	if err != nil {
		t.Fatalf("dataset_list: %v", err)
	}
	var envelope struct {
		Datasets json.RawMessage `json:"datasets"`
	}
	if err := json.Unmarshal(res, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := `[{"name":"basket","version":7,"size_bytes":40213,"row_count":512,` +
		`"sha256":"abc123","content_type":"text/csv",` +
		`"labels":{"hypothesis":"1a2b3c4d","metric":"basket"},` +
		`"created_at":1789000000123,"created_by_worker":"researcher","created_by_session":"sess-1"}]`
	if string(envelope.Datasets) != want {
		t.Fatalf("datasets:\n got %s\nwant %s", envelope.Datasets, want)
	}

	// blob_path is an internal storage key and project is already the caller's
	// own credential; neither may ever be serialised.
	for _, forbidden := range []string{"blob_path", `"project"`, "_datasets/bytes/", `"id"`} {
		if strings.Contains(string(res), forbidden) {
			t.Fatalf("dataset_list leaked %s: %s", forbidden, res)
		}
	}
}

// TestDatasetToolsListFieldNamesMatchTheHTTPRoute is the cross-surface half of
// the pin above: the ten shared names, taken from each side's own literal.
func TestDatasetToolsListFieldNamesMatchTheHTTPRoute(t *testing.T) {
	var httpFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(o5PinnedMetadataBody), &httpFields); err != nil {
		t.Fatalf("unmarshal O5's pinned body: %v", err)
	}
	blob, err := json.Marshal(entryOf(seedDataset("wolf", "basket", 7)))
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var toolFields map[string]json.RawMessage
	if err := json.Unmarshal(blob, &toolFields); err != nil {
		t.Fatalf("unmarshal entry: %v", err)
	}

	// `id` is O5's alone — a version id is a storage detail a model has no use
	// for. Every other field name must appear on both sides.
	delete(httpFields, "id")
	if len(httpFields) != 10 || len(toolFields) != 10 {
		t.Fatalf("shared field count: http=%d tool=%d, want 10 and 10", len(httpFields), len(toolFields))
	}
	for name, httpVal := range httpFields {
		toolVal, ok := toolFields[name]
		if !ok {
			t.Fatalf("field %q is in the HTTP response but not in dataset_list", name)
		}
		if string(httpVal) != string(toolVal) {
			t.Fatalf("field %q: http %s, tool %s — same name, different value for the same row",
				name, httpVal, toolVal)
		}
	}
}

// TestDatasetToolsListLimitDefaultAndCap: default 20, maximum 100, and the cap
// is STATED rather than applied silently — a model that asked for 500 and got
// 100 would otherwise conclude the project holds 100 datasets.
func TestDatasetToolsListLimitDefaultAndCap(t *testing.T) {
	cases := []struct {
		name      string
		args      map[string]any
		wantLimit int
		wantNote  bool
	}{
		{"absent is 20", map[string]any{}, 20, false},
		{"zero is 20", map[string]any{"limit": 0}, 20, false},
		{"negative is 20", map[string]any{"limit": -5}, 20, false},
		{"in range passes through", map[string]any{"limit": 7}, 7, false},
		{"at the maximum", map[string]any{"limit": 100}, 100, false},
		{"over the maximum is capped and said so", map[string]any{"limit": 500}, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeDatasetCatalog()
			tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()
			out, err := invokeTool(t, tools, "dataset_list", identifiedCaller(), tc.args)
			if err != nil {
				t.Fatalf("dataset_list: %v", err)
			}
			if got := store.listCalls[0].limit; got != tc.wantLimit {
				t.Fatalf("store limit = %d, want %d", got, tc.wantLimit)
			}
			note, hasNote := out["note"].(string)
			if hasNote != tc.wantNote {
				t.Fatalf("note present = %v (%q), want %v", hasNote, note, tc.wantNote)
			}
			if tc.wantNote && !strings.Contains(note, "100") {
				t.Fatalf("the cap note must state the cap, got %q", note)
			}
		})
	}
}

// A full page says so too: a result of exactly `limit` rows may not be the whole
// story, and silence there reads as "that is all of them".
func TestDatasetToolsListFullPageIsAnnounced(t *testing.T) {
	store := newFakeDatasetCatalog()
	for i := 0; i < 3; i++ {
		store.rows = append(store.rows, seedDataset("wolf", fmt.Sprintf("m%d", i), 1))
	}
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()
	out, err := invokeTool(t, tools, "dataset_list", identifiedCaller(), map[string]any{"limit": 3})
	if err != nil {
		t.Fatalf("dataset_list: %v", err)
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "there may be more") {
		t.Fatalf("a full page must be announced, note = %q", note)
	}
}

// The project is the caller's, never an argument, and the selector reaches the
// store unchanged — the parser is the store's and its message is the model's.
func TestDatasetToolsListScopeAndSelectorPassThrough(t *testing.T) {
	store := newFakeDatasetCatalog()
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()
	if _, err := invokeTool(t, tools, "dataset_list", identifiedCaller(),
		map[string]any{"selector": "hypothesis=1a2b3c4d, exists metric"}); err != nil {
		t.Fatalf("dataset_list: %v", err)
	}
	call := store.listCalls[0]
	if call.project != "wolf" || call.selector != "hypothesis=1a2b3c4d, exists metric" {
		t.Fatalf("store call = %+v, want the caller's project and the selector verbatim", call)
	}

	// There is no project parameter to smuggle one in with.
	if _, err := invokeTool(t, tools, "dataset_list", identifiedCaller(),
		map[string]any{"project": "someone-else"}); err == nil {
		t.Fatal("dataset_list accepted a project argument")
	}

	store.listErr = errors.New("agentdb: dataset selector: unexpected token")
	if _, err := invokeTool(t, tools, "dataset_list", identifiedCaller(),
		map[string]any{"selector": "="}); err == nil || !strings.Contains(err.Error(), "unexpected token") {
		t.Fatalf("the parser's own message must reach the model, got %v", err)
	}
}

// ── dataset_get ─────────────────────────────────────────────────────────────

// TestDatasetToolsGetDownloadURLPrefix — the most likely wrong turn in this
// ticket. The URL must be built on AGENTKIT_SELF_URL (how a container nested in
// DinD reaches agentd) and never on AGENTKIT_PUBLIC_BASE_URL, which is a
// browser-facing address unreachable from in there.
func TestDatasetToolsGetDownloadURLPrefix(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.current["wolf|basket"] = seedDataset("wolf", "basket", 7)
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()

	out, err := invokeTool(t, tools, "dataset_get", identifiedCaller(), map[string]any{"name": "basket"})
	if err != nil {
		t.Fatalf("dataset_get: %v", err)
	}
	rawURL, _ := out["download_url"].(string)
	wantPrefix := testSelfURL + "/agent/datasets/basket/download?version=7&token="
	if !strings.HasPrefix(rawURL, wantPrefix) {
		t.Fatalf("download_url = %q, want the prefix %q", rawURL, wantPrefix)
	}
	if strings.Contains(rawURL, testPublicBaseURL) {
		t.Fatalf("download_url was built on the PUBLIC base url: %q", rawURL)
	}
	// No bytes anywhere in the result — that is the whole point of the atom.
	if _, ok := out["content"]; ok {
		t.Fatalf("dataset_get returned content: %v", out)
	}

	// The pinned field set, exactly: the seven metadata fields plus the
	// credential and its expiry. Nothing more (no blob_path, no project, no id)
	// and nothing less.
	var keys []string
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "content_type,download_url,expires_at,labels,name,row_count,sha256,size_bytes,version"
	if strings.Join(keys, ",") != want {
		t.Fatalf("dataset_get fields = %v, want exactly %s", keys, want)
	}
}

// TestDatasetToolsGetURLMatchesTheMiddlewarePath ties this tool to O5's
// hardcoded literal path. agentd's ?token= leg matches a path spelled out in
// cmd/agentd (middleware runs before the mux and cannot see httpapi.Endpoints),
// so if the two spellings drift the agent's curl 401s from inside a container
// and nothing says why.
func TestDatasetToolsGetURLMatchesTheMiddlewarePath(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.current["wolf|drone.suppliers_basket-1"] = seedDataset("wolf", "drone.suppliers_basket-1", 3)
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()

	out, err := invokeTool(t, tools, "dataset_get", identifiedCaller(),
		map[string]any{"name": "drone.suppliers_basket-1"})
	if err != nil {
		t.Fatalf("dataset_get: %v", err)
	}
	rawURL, _ := out["download_url"].(string)
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("the minted URL does not parse: %v", err)
	}
	if !datasetDownloadPath(req) {
		t.Fatalf("apiAuthMiddleware's ?token= leg would not fire for %q", req.URL.Path)
	}
	if req.URL.Query().Get(datasetTokenParam) == "" {
		t.Fatalf("the URL carries no %s parameter: %q", datasetTokenParam, rawURL)
	}
}

// TestDatasetToolsGetTokenScopeIsTheNameNotTheVersion: the token pins
// (project, name), so a URL minted while a name is at v3 still verifies after a
// tick takes it to v7. Pinning a version — or a dataset id, which IS
// per-version — would break that.
func TestDatasetToolsGetTokenScopeIsTheNameNotTheVersion(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.current["wolf|basket"] = seedDataset("wolf", "basket", 3)
	store.versions["wolf|basket|7"] = seedDataset("wolf", "basket", 7)
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()

	atV3, err := invokeTool(t, tools, "dataset_get", identifiedCaller(), map[string]any{"name": "basket"})
	if err != nil {
		t.Fatalf("dataset_get: %v", err)
	}
	tok := tokenOf(t, atV3["download_url"].(string))
	project, name, err := verifyDatasetToken(testDatasetSecret, tok)
	if err != nil {
		t.Fatalf("the minted token does not verify: %v", err)
	}
	if project != "wolf" || name != "basket" {
		t.Fatalf("token pins (%q, %q), want (wolf, basket)", project, name)
	}
	if strings.Contains(tok, "version") {
		t.Fatalf("the token appears to carry a version: %q", tok)
	}

	// The same credential, used against a LATER version of the same name.
	atV7, err := invokeTool(t, tools, "dataset_get", identifiedCaller(),
		map[string]any{"name": "basket", "version": 7})
	if err != nil {
		t.Fatalf("dataset_get(version=7): %v", err)
	}
	p2, n2, err := verifyDatasetToken(testDatasetSecret, tokenOf(t, atV7["download_url"].(string)))
	if err != nil || p2 != project || n2 != name {
		t.Fatalf("a v7 URL pins (%q,%q,%v), want the same pair as v3", p2, n2, err)
	}
	if !strings.Contains(atV7["download_url"].(string), "version=7") {
		t.Fatalf("the pinned version is not in the URL: %v", atV7["download_url"])
	}
	if atV7["version"] != float64(7) {
		t.Fatalf("version = %v, want 7", atV7["version"])
	}
}

// expires_at is unix SECONDS, matching embedTokenResponse.ExpiresAt and
// deliberately unlike created_at's milliseconds. A millisecond value here would
// read as the year 58708 and expire nothing.
func TestDatasetToolsGetExpiresAtIsUnixSeconds(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.current["wolf|basket"] = seedDataset("wolf", "basket", 1)
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()

	out, err := invokeTool(t, tools, "dataset_get", identifiedCaller(), map[string]any{"name": "basket"})
	if err != nil {
		t.Fatalf("dataset_get: %v", err)
	}
	exp := int64(out["expires_at"].(float64))
	now := time.Now().Unix()
	if exp < now+int64(datasetTokenMinTTL.Seconds()) || exp > now+int64(datasetTokenMaxTTL.Seconds()) {
		t.Fatalf("expires_at = %d; now = %d — not a unix-SECONDS expiry inside the token's TTL bounds", exp, now)
	}
	// created_at is milliseconds and expires_at is seconds: three orders of
	// magnitude apart, which is exactly the confusion the units test guards.
	if exp > 1e11 {
		t.Fatalf("expires_at = %d looks like milliseconds", exp)
	}
}

// A name that does not exist, a version that does not exist and a nonsense
// version each get their own answer, and none of them is a stack trace.
func TestDatasetToolsGetNotFoundAndBadVersion(t *testing.T) {
	store := newFakeDatasetCatalog()
	tools := testDatasetTools(store, &fakeExecer{}, newRecordingBlobs()).tools()

	if _, err := invokeTool(t, tools, "dataset_get", identifiedCaller(),
		map[string]any{"name": "missing"}); err == nil || !strings.Contains(err.Error(), "no dataset named") {
		t.Fatalf("err = %v, want a not-found naming dataset_list", err)
	}
	store.current["wolf|basket"] = seedDataset("wolf", "basket", 3)
	if _, err := invokeTool(t, tools, "dataset_get", identifiedCaller(),
		map[string]any{"name": "basket", "version": 99}); err == nil || !strings.Contains(err.Error(), "no version 99") {
		t.Fatalf("err = %v, want a per-version not-found", err)
	}
	if _, err := invokeTool(t, tools, "dataset_get", identifiedCaller(),
		map[string]any{"name": "basket", "version": -1}); err == nil {
		t.Fatal("a negative version was accepted")
	}
	if _, err := invokeTool(t, tools, "dataset_get", identifiedCaller(),
		map[string]any{"name": "not a legal name"}); err == nil {
		t.Fatal("an illegal dataset name was accepted")
	}
}

// TestDatasetToolsGetDescriptionCarriesTheCredentialWarning. The description is
// the only thing standing between a model and pasting a bearer credential into
// a transcript that fans out to every subscriber of worker.finished.
func TestDatasetToolsGetDescriptionCarriesTheCredentialWarning(t *testing.T) {
	for _, want := range []string{
		"curl",             // fetch it
		"-o prices.csv",    // TO A FILE
		"Do NOT print the", // not the contents
		"Do NOT echo the download_url",
		"BEARER CREDENTIAL",
		"expires",
	} {
		if !strings.Contains(datasetGetDescription, want) {
			t.Fatalf("dataset_get's description does not say %q:\n%s", want, datasetGetDescription)
		}
	}
}

// ── dataset_put ─────────────────────────────────────────────────────────────

// putTools wires a dataset_put that pulls `content` out of a fake container.
func putTools(t *testing.T, store *fakeDatasetCatalog, content []byte) (*datasetTools, *recordingBlobs, *fakeExecer) {
	t.Helper()
	blobs := newRecordingBlobs()
	exec := &fakeExecer{session: happyFakeSession(content)}
	return testDatasetTools(store, exec, blobs), blobs, exec
}

// TestDatasetToolsPutWritesAVersionWithCallerProvenance: the happy path, and
// the invariant that makes the whole trust model hold.
func TestDatasetToolsPutWritesAVersionWithCallerProvenance(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.nextVersion = 4
	tools, _, exec := putTools(t, store, []byte("timestamp,value\n2026-08-19T00:00:00Z,141.22\n"))

	out, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(), map[string]any{
		"name": "basket", "path": "prices.csv", "if_version": 0,
		"labels": map[string]string{"hypothesis": "1a2b3c4d"},
	})
	if err != nil {
		t.Fatalf("dataset_put: %v", err)
	}

	if len(store.created) != 1 {
		t.Fatalf("created %d rows, want 1", len(store.created))
	}
	got := store.created[0]
	if got.Project != "wolf" || got.CreatedBySession != "sess-1" || got.CreatedByWorker != "researcher" {
		t.Fatalf("provenance = %+v, want the CALLER's project/session/worker", got)
	}
	if got.ContentType != "text/csv" {
		t.Fatalf("content_type = %q, want the text/csv default", got.ContentType)
	}
	if got.RowCount != 1 || got.SizeBytes == 0 || got.SHA256 == "" {
		t.Fatalf("counted metadata = %+v, want it from the pulled bytes", got)
	}
	if store.ifVersions[0] != 0 {
		t.Fatalf("if_version reached the store as %d, want 0", store.ifVersions[0])
	}
	// The exec went into the CALLING session and no other.
	if len(exec.refs) == 0 || exec.refs[0].SessionID != "sess-1" {
		t.Fatalf("exec refs = %+v, want the caller's own session", exec.refs)
	}
	// The row is echoed back as the DATABASE holds it, not as we sent it.
	if out["version"] != float64(4) {
		t.Fatalf("version = %v, want the store's 4 — the read-back, not the argument", out["version"])
	}
	if out["name"] != "basket" || out["row_count"] != float64(1) {
		t.Fatalf("echo = %v", out)
	}
	// No bytes in the result.
	if _, leaked := out["content"]; leaked {
		t.Fatalf("dataset_put echoed content: %v", out)
	}
}

// TestDatasetToolsPutRefusesAnUnidentifiedCaller — the RD4 invariant applied to
// data. A row written from inside a container must never be able to carry empty
// provenance, because empty provenance is how the embedding application
// recognises what IT wrote.
func TestDatasetToolsPutRefusesAnUnidentifiedCaller(t *testing.T) {
	store := newFakeDatasetCatalog()
	tools, blobs, exec := putTools(t, store, []byte("timestamp,value\n"))

	_, err := invokeTool(t, tools.tools(), "dataset_put",
		mcpCaller{Project: "wolf", SessionID: "sess-1", Identified: false},
		map[string]any{"name": "basket", "path": "prices.csv", "if_version": 0})
	if err == nil {
		t.Fatal("an unidentified caller was allowed to write")
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %q, want it to say nothing was written", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("a row was created anyway: %+v", store.created)
	}
	if len(exec.refs) != 0 {
		t.Fatalf("the refusal still exec'd into the container: %+v", exec.refs)
	}
	if len(blobs.deletes()) != 0 || len(blobs.stored(t)) != 0 {
		t.Fatalf("the refusal still touched the blob store")
	}
}

// There is no argument that lets a caller name itself. decodeArgs rejects
// unknown fields, so an attempt is an error rather than a silent no-op — a
// model that believed it had set provenance would be worse than one told it
// cannot.
func TestDatasetToolsPutHasNoProvenanceArgument(t *testing.T) {
	store := newFakeDatasetCatalog()
	tools, _, _ := putTools(t, store, []byte("timestamp,value\n"))
	for _, field := range []string{"created_by_worker", "created_by_session", "project"} {
		args := map[string]any{"name": "basket", "path": "prices.csv", "if_version": 0, field: "spoofed"}
		if _, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(), args); err == nil {
			t.Fatalf("dataset_put accepted a %q argument", field)
		}
	}
	if len(store.created) != 0 {
		t.Fatalf("a spoofed write got through: %+v", store.created)
	}
}

// TestDatasetToolsPutVersionConflictNamesTheCurrentVersionAndDeletesTheBlob.
// The CAS is the store's — two writers can pass any read we do here and only
// one can pass the unique index — so the conflict arrives AFTER the blob has
// been written, and the blob must not be left behind.
func TestDatasetToolsPutVersionConflictNamesTheCurrentVersionAndDeletesTheBlob(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.createErr = agentdb.ErrDatasetVersionConflict{Current: 5}
	tools, blobs, _ := putTools(t, store, []byte("timestamp,value\n1,2\n"))

	_, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(),
		map[string]any{"name": "basket", "path": "prices.csv", "if_version": 3})
	if err == nil {
		t.Fatal("the conflict was swallowed")
	}
	if !strings.Contains(err.Error(), "version 5") || !strings.Contains(err.Error(), "if_version: 5") {
		t.Fatalf("err = %q, want it to name the current version so the model can retry", err)
	}
	// The blob written moments ago is gone — attempted, and recorded.
	deleted := blobs.deletes()
	if len(deleted) != 1 || !strings.HasPrefix(deleted[0], agentdb.DatasetBlobPrefix) {
		t.Fatalf("deletes = %v, want the one dataset blob just written", deleted)
	}
	if left := blobs.stored(t); len(left) != 0 {
		t.Fatalf("the orphaned blob is still stored: %v", left)
	}
}

// A delete that fails is logged, never fatal: the original error is what the
// model needs, and O3's orphan sweep is the backstop.
func TestDatasetToolsPutConflictSurvivesAFailedBlobDelete(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.createErr = agentdb.ErrDatasetVersionConflict{Current: 2}
	tools, blobs, _ := putTools(t, store, []byte("timestamp,value\n1,2\n"))
	blobs.failAll = true

	_, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(),
		map[string]any{"name": "basket", "path": "prices.csv", "if_version": 1})
	if err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("err = %v, want the unchanged conflict error", err)
	}
	if len(blobs.deletes()) != 1 {
		t.Fatalf("the delete was not attempted: %v", blobs.deletes())
	}
}

// A first write to a name that does not exist, with if_version != 0, is the
// other half of the CAS vocabulary and gets its own sentence.
func TestDatasetToolsPutConflictOnAMissingDatasetSaysHowToCreateIt(t *testing.T) {
	store := newFakeDatasetCatalog()
	store.createErr = agentdb.ErrDatasetVersionConflict{Current: 0}
	tools, _, _ := putTools(t, store, []byte("timestamp,value\n1,2\n"))

	_, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(),
		map[string]any{"name": "basket", "path": "prices.csv", "if_version": 4})
	if err == nil || !strings.Contains(err.Error(), "if_version: 0") {
		t.Fatalf("err = %v, want the create instruction", err)
	}
}

// TestDatasetToolsPutShrinkGuard — all three shapes.
func TestDatasetToolsPutShrinkGuard(t *testing.T) {
	// 4 data rows: "h\na\nb\nc\nd\n" → row_count 4.
	small := []byte("timestamp,value\n1,1\n2,2\n3,3\n4,4\n")
	cases := []struct {
		name        string
		currentRows int
		hasCurrent  bool
		allowShrink bool
		wantRefused bool
	}{
		{"a big shrink is refused", 512, true, false, true},
		{"exactly half is allowed", 8, true, false, false},
		{"allow_shrink lets it through", 512, true, true, false},
		{"a current version of 0 rows has nothing to shrink from", 0, true, false, false},
		{"no current version at all", 0, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeDatasetCatalog()
			if tc.hasCurrent {
				cur := seedDataset("wolf", "basket", 2)
				cur.RowCount = tc.currentRows
				store.current["wolf|basket"] = cur
			}
			tools, blobs, _ := putTools(t, store, small)
			args := map[string]any{"name": "basket", "path": "prices.csv", "if_version": 2}
			if tc.allowShrink {
				args["allow_shrink"] = true
			}
			_, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(), args)
			if tc.wantRefused {
				if err == nil {
					t.Fatal("the shrink was allowed")
				}
				// The refusal names BOTH counts, or a model cannot judge it.
				if !strings.Contains(err.Error(), "512 rows") || !strings.Contains(err.Error(), "to 4") {
					t.Fatalf("err = %q, want both row counts named", err)
				}
				if !strings.Contains(err.Error(), "allow_shrink") {
					t.Fatalf("err = %q, want the remedy named", err)
				}
				if len(store.created) != 0 {
					t.Fatalf("the refusal still wrote a row: %+v", store.created)
				}
				if len(blobs.deletes()) != 1 {
					t.Fatalf("the refused write left its blob behind: %v", blobs.deletes())
				}
				return
			}
			if err != nil {
				t.Fatalf("dataset_put: %v", err)
			}
			if len(store.created) != 1 {
				t.Fatalf("created %d rows, want 1", len(store.created))
			}
		})
	}
}

// The pull pipeline's own rejections reach the model unchanged, and nothing is
// written when it refuses. The path hardening itself is O6a's; this is the seam.
func TestDatasetToolsPutSurfacesPullFailures(t *testing.T) {
	store := newFakeDatasetCatalog()
	tools, _, _ := putTools(t, store, []byte("timestamp,value\n"))
	if _, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(),
		map[string]any{"name": "basket", "path": "../../etc/passwd", "if_version": 0}); err == nil ||
		!strings.Contains(err.Error(), "escapes /workspace") {
		t.Fatalf("err = %v, want the pull pipeline's own rejection", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("a rejected path still wrote a row: %+v", store.created)
	}
}

// TestDatasetToolsPutPassesTheConfiguredMaxBytes: the cap is agentd's, read once
// at boot, and is what the pull pipeline enforces. A tool that read the
// environment itself — or passed its own number — would make the knob a lie.
func TestDatasetToolsPutPassesTheConfiguredMaxBytes(t *testing.T) {
	store := newFakeDatasetCatalog()
	blobs := newRecordingBlobs()
	tools := newDatasetTools(store, &fakeExecer{session: happyFakeSession([]byte("x"))},
		blobs, testDatasetSecret, testSelfURL, 4242)
	var sawMax int64
	var sawContentType, sawPath string
	tools.pull = func(_ context.Context, _ sessionExec, relPath, contentType string,
		maxBytes int64, _ extension.BlobStore) (*pulledFile, error) {
		sawMax, sawContentType, sawPath = maxBytes, contentType, relPath
		return &pulledFile{BlobPath: agentdb.DatasetBlobPrefix + "k", SizeBytes: 1, RowCount: 0, SHA256: "d"}, nil
	}
	if _, err := invokeTool(t, tools.tools(), "dataset_put", identifiedCaller(), map[string]any{
		"name": "basket", "path": "sub/prices.tsv", "if_version": 0, "content_type": "text/tab-separated-values",
	}); err != nil {
		t.Fatalf("dataset_put: %v", err)
	}
	if sawMax != 4242 {
		t.Fatalf("maxBytes = %d, want the configured 4242", sawMax)
	}
	if sawContentType != "text/tab-separated-values" || sawPath != "sub/prices.tsv" {
		t.Fatalf("pull got (%q, %q)", sawPath, sawContentType)
	}
	if store.created[0].ContentType != "text/tab-separated-values" {
		t.Fatalf("content_type = %q, want the caller's", store.created[0].ContentType)
	}
}

// ── the boot knob ───────────────────────────────────────────────────────────

// TestDatasetToolsMaxBytesVariable: default 64MiB, plain byte counts, and a
// nonsense value is a boot error NAMING the variable. An operator who set a cap
// and silently got the default would find out when a container OOMed agentd.
func TestDatasetToolsMaxBytesVariable(t *testing.T) {
	cases := []struct {
		raw     string
		want    int64
		wantErr bool
	}{
		{"", 64 << 20, false},
		{"   ", 64 << 20, false},
		{"1048576", 1 << 20, false},
		{"64", 64, false},
		{"0", 0, true},
		{"-1", 0, true},
		{"64MiB", 0, true},
		{"1_000", 0, true},
		{"1.5", 0, true},
	}
	for _, tc := range cases {
		got, err := parseDatasetMaxBytes(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseDatasetMaxBytes(%q) = %d, want an error", tc.raw, got)
			}
			if !strings.Contains(err.Error(), datasetMaxBytesVar) {
				t.Fatalf("err = %q, want it to name %s", err, datasetMaxBytesVar)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseDatasetMaxBytes(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("parseDatasetMaxBytes(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

// invokeToolRaw is invokeTool's sibling for tests that need the exact JSON
// bytes rather than a decoded map — a pinned body is about byte order as much
// as about values.
func invokeToolRaw(t *testing.T, tools []*mcpTool, name string, caller mcpCaller, args any) ([]byte, error) {
	t.Helper()
	var tool *mcpTool
	for _, tt := range tools {
		if tt.Name == name {
			tool = tt
		}
	}
	if tool == nil {
		t.Fatalf("no tool %q", name)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	res, err := tool.Handler(context.Background(), caller, raw)
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return blob, nil
}

// tokenOf pulls the ?token= value out of a download URL.
func tokenOf(t *testing.T, rawURL string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	tok := req.URL.Query().Get(datasetTokenParam)
	if tok == "" {
		t.Fatalf("no token in %q", rawURL)
	}
	return tok
}
