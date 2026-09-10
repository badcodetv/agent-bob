package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// ---------------------------------------------------------------------------
// Fakes.
//
// fakeDatasets is a miniature store rather than a canned-answer stub, and
// deliberately so: the whole security story on these routes is that the project
// reaching the store is the credential's, and a stub that ignores its first
// argument would pass every tenancy test in this file while the handler leaked
// every project's datasets. This one enforces (project, name) exactly as
// agentdb does — including answering ErrDatasetNotFound, never an empty slice,
// for a name that exists only in another project.
// ---------------------------------------------------------------------------

type fakeDatasets struct {
	rows []*agentdb.Dataset // every project's rows, as the real table holds them

	// err, when set, is returned by every method — the store-failure legs.
	err error

	// What crossed the seam. Asserted, never assumed.
	gotProject  string
	gotName     string
	gotSelector string
	gotLimit    int
	gotVersion  int
	calls       []string
}

func (f *fakeDatasets) note(call, project, name string) {
	f.calls = append(f.calls, call)
	f.gotProject, f.gotName = project, name
}

func (f *fakeDatasets) inProject(project, name string) []*agentdb.Dataset {
	var out []*agentdb.Dataset
	for _, d := range f.rows {
		if d.Project == project && (name == "" || d.Name == name) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

func (f *fakeDatasets) ListDatasets(_ context.Context, project, selector string, limit int) ([]*agentdb.Dataset, error) {
	f.note("ListDatasets", project, "")
	f.gotSelector, f.gotLimit = selector, limit
	if f.err != nil {
		return nil, f.err
	}
	// One row per name, the highest version — the store's own reduction.
	seen := map[string]bool{}
	var out []*agentdb.Dataset
	for _, d := range f.inProject(project, "") {
		if seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeDatasets) CurrentDataset(_ context.Context, project, name string) (*agentdb.Dataset, error) {
	f.note("CurrentDataset", project, name)
	if f.err != nil {
		return nil, f.err
	}
	rows := f.inProject(project, name)
	if len(rows) == 0 {
		return nil, agentdb.ErrDatasetNotFound
	}
	return rows[0], nil
}

func (f *fakeDatasets) GetDatasetVersion(_ context.Context, project, name string, version int) (*agentdb.Dataset, error) {
	f.note("GetDatasetVersion", project, name)
	f.gotVersion = version
	if f.err != nil {
		return nil, f.err
	}
	for _, d := range f.inProject(project, name) {
		if d.Version == version {
			return d, nil
		}
	}
	return nil, agentdb.ErrDatasetNotFound
}

func (f *fakeDatasets) ListDatasetVersions(_ context.Context, project, name string, limit int) ([]*agentdb.Dataset, error) {
	f.note("ListDatasetVersions", project, name)
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	rows := f.inProject(project, name)
	if len(rows) == 0 {
		return nil, agentdb.ErrDatasetNotFound
	}
	return rows, nil
}

// fakeDatasetBlobs is the byte plane. A key it does not hold is an error, which
// is what every real BlobStore does for a missing blob.
type fakeDatasetBlobs struct {
	data map[string]string
	got  string
}

func (f *fakeDatasetBlobs) Read(_ context.Context, key string) (io.ReadCloser, error) {
	f.got = key
	body, ok := f.data[key]
	if !ok {
		return nil, fmt.Errorf("blob %q not found", key)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// dataset builds a row. Every field is non-zero so a handler that drops one is
// caught by the shape assertions rather than passing on a zero that matches.
func dataset(project, name string, version int) *agentdb.Dataset {
	return &agentdb.Dataset{
		ID:               fmt.Sprintf("ds-%s-%d", name, version),
		Project:          project,
		Name:             name,
		Version:          version,
		Labels:           agentdb.LabelSet{"hypothesis": "1a2b3c4d", "metric": "basket"},
		BlobPath:         agentdb.DatasetBlobPrefix + name + "-" + fmt.Sprint(version),
		SizeBytes:        40213,
		RowCount:         512,
		SHA256:           "abc123",
		ContentType:      "text/csv",
		CreatedByWorker:  "researcher",
		CreatedBySession: "sess-1",
		CreatedAt:        1789000000123,
	}
}

// identityWithDatasetScope is the principal a ?token= download arrives with,
// as agentd's middleware builds it: the token's project, and a "<project>/<name>"
// pin. Nothing in this package verifies the token — httpapi holds no JWT code —
// so this is exactly the seam under test.
func identityWithDatasetScope(customer, scope string) IdentityFunc {
	return func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "dataset-token:" + customer, Customer: customer, DatasetScope: scope}, nil
	}
}

func newDatasetHandlers(t *testing.T, store DatasetStore, blobs DatasetBlobReader, id IdentityFunc) *Handlers {
	t.Helper()
	return newHandlers(t, Config{
		Runner:       stubRunner{},
		Store:        stubStore{},
		Identity:     id,
		Datasets:     store,
		DatasetBlobs: blobs,
	})
}

// ---------------------------------------------------------------------------
// The metadata routes.
// ---------------------------------------------------------------------------

// selector and limit reach the store unchanged (the store clamps the limit —
// this package must not develop a second opinion about it), the project comes
// from the credential and never from the query, and the envelope is
// {"datasets":[…]} with an empty result rendering as [] rather than null.
func TestDatasetList_EnvelopeAndPlumbing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		path         string
		wantSelector string
		wantLimit    int
	}{
		{"no filters", "/agent/datasets", "", 0},
		{"selector and limit", "/agent/datasets?selector=hypothesis%3D1a2b3c4d&limit=25", "hypothesis=1a2b3c4d", 25},
		{"a project in the query is ignored, not honoured", "/agent/datasets?project=other", "", 0},
		{"a junk limit degrades to the store's default", "/agent/datasets?limit=-3", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 1)}}
			h := newDatasetHandlers(t, store, nil, identityFor("acme"))
			rec := do(h, http.MethodGet, tc.path, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
			}
			if store.gotProject != "acme" || store.gotSelector != tc.wantSelector || store.gotLimit != tc.wantLimit {
				t.Fatalf("store saw (project=%q selector=%q limit=%d), want (acme, %q, %d)",
					store.gotProject, store.gotSelector, store.gotLimit, tc.wantSelector, tc.wantLimit)
			}
			var got struct {
				Datasets []datasetResp `json:"datasets"`
			}
			decodeInto(t, rec, &got)
			if len(got.Datasets) != 1 || got.Datasets[0].Name != "basket" {
				t.Fatalf("datasets = %+v", got.Datasets)
			}
		})
	}

	t.Run("empty is [] and not null", func(t *testing.T) {
		h := newDatasetHandlers(t, &fakeDatasets{}, nil, identityFor("acme"))
		rec := do(h, http.MethodGet, "/agent/datasets", "")
		if body := strings.TrimSpace(rec.Body.String()); body != `{"datasets":[]}` {
			t.Fatalf("body = %s, want {\"datasets\":[]}", body)
		}
	})
}

// The two-way mapping ListMemories already does: the store's Postgres sentinel
// is 501 ("this deployment cannot answer"), and anything else the call can fail
// with is the caller's selector, reported with the parser's own message.
func TestDatasetList_SelectorErrorIs400AndPostgresIs501(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"a malformed selector is the caller's fault", errors.New("agentdb: dataset selector: unexpected ')'"), http.StatusBadRequest},
		{"the sqlite fallback cannot answer", agentdb.ErrDatasetRequiresPostgres, http.StatusNotImplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDatasetHandlers(t, &fakeDatasets{err: tc.err}, nil, identityFor("acme"))
			rec := do(h, http.MethodGet, "/agent/datasets?selector=junk", "")
			if rec.Code != tc.want {
				t.Fatalf("status=%d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.err.Error()) {
				t.Fatalf("body %q does not carry the store's own message %q", rec.Body, tc.err)
			}
		})
	}
}

// The single-name route answers a BARE object, with exactly the pinned fields in
// the pinned order and created_at in unix MILLISECONDS. Asserted as a literal
// body rather than field by field: six tickets and one MCP tool are written
// against this shape, and a renamed or reordered key is the kind of drift that
// is invisible until an integrator's parser fails.
func TestDatasetCurrent_IsTheBareObjectInThePinnedShape(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{
		dataset("acme", "basket", 1), dataset("acme", "basket", 7),
	}}
	h := newDatasetHandlers(t, store, nil, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/datasets/basket", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	want := `{"id":"ds-basket-7","name":"basket","version":7,` +
		`"labels":{"hypothesis":"1a2b3c4d","metric":"basket"},` +
		`"size_bytes":40213,"row_count":512,"sha256":"abc123","content_type":"text/csv",` +
		`"created_by_worker":"researcher","created_by_session":"sess-1","created_at":1789000000123}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body:\n got %s\nwant %s", got, want)
	}
	if store.calls[len(store.calls)-1] != "CurrentDataset" {
		t.Fatalf("calls = %v, want the current-version read", store.calls)
	}
}

// An unlabelled dataset renders labels as {} rather than null: a client should
// not have to branch on that.
func TestDatasetCurrent_UnlabelledRendersEmptyObject(t *testing.T) {
	row := dataset("acme", "basket", 1)
	row.Labels = nil
	h := newDatasetHandlers(t, &fakeDatasets{rows: []*agentdb.Dataset{row}}, nil, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/datasets/basket", "")
	if !strings.Contains(rec.Body.String(), `"labels":{}`) {
		t.Fatalf("body = %s, want labels:{}", rec.Body)
	}
}

// blob_path is an internal storage key and project is already the caller's own
// credential. Neither may appear in ANY metadata response — which is the reason
// datasetResp exists instead of serialising agentdb.Dataset, and the reason this
// test sweeps all three routes rather than the one that was easiest to write.
func TestDatasetMetadata_NeverLeaksBlobPathOrProject(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 1)}}
	h := newDatasetHandlers(t, store, nil, identityFor("acme"))
	for _, path := range []string{
		"/agent/datasets",
		"/agent/datasets/basket",
		"/agent/datasets/basket/versions",
	} {
		rec := do(h, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", path, rec.Code, rec.Body)
		}
		body := rec.Body.String()
		for _, leak := range []string{"blob_path", agentdb.DatasetBlobPrefix, `"project"`, "acme"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s: response leaks %q: %s", path, leak, body)
			}
		}
	}
}

// The versions envelope is {"versions":[…]} newest first, and a name with no
// rows is 404 — the store answers ErrDatasetNotFound rather than an empty slice
// precisely so this route can say "not found" instead of "exists, but empty".
func TestDatasetVersions_EnvelopeIsNewestFirst(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{
		dataset("acme", "basket", 1), dataset("acme", "basket", 3), dataset("acme", "basket", 2),
	}}
	h := newDatasetHandlers(t, store, nil, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/datasets/basket/versions?limit=50", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var got struct {
		Versions []datasetResp `json:"versions"`
	}
	decodeInto(t, rec, &got)
	if len(got.Versions) != 3 || got.Versions[0].Version != 3 || got.Versions[2].Version != 1 {
		t.Fatalf("versions = %+v, want 3,2,1", got.Versions)
	}
	if store.gotLimit != 50 {
		t.Fatalf("limit reached the store as %d, want 50 (the store clamps, not this package)", store.gotLimit)
	}

	empty := newDatasetHandlers(t, &fakeDatasets{}, nil, identityFor("acme"))
	if rec := do(empty, http.MethodGet, "/agent/datasets/nothing/versions", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for a name with no rows", rec.Code)
	}
}

// 404 parity. Unknown, malformed, foreign, and every unusable ?version= answer
// with a BYTE-IDENTICAL body: the route is not an existence oracle, and a
// dataset name is caller-chosen and guessable in a way a uuid is not.
func TestDatasetRoutes_NotFoundParityIsByteIdentical(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{
		dataset("acme", "basket", 7),
		dataset("other", "secret-basket", 1), // another project's, and invisible
	}}
	h := newDatasetHandlers(t, store, &fakeDatasetBlobs{}, identityFor("acme"))

	var bodies []string
	for _, tc := range []struct{ name, path string }{
		{"unknown name", "/agent/datasets/absent"},
		{"unknown name, versions", "/agent/datasets/absent/versions"},
		{"unknown name, download", "/agent/datasets/absent/download"},
		{"malformed name", "/agent/datasets/not%20a%20name"},
		{"malformed name, download", "/agent/datasets/not%20a%20name/download"},
		{"another project's dataset", "/agent/datasets/secret-basket"},
		{"another project's dataset, download", "/agent/datasets/secret-basket/download"},
		{"non-numeric version", "/agent/datasets/basket/download?version=latest"},
		{"version zero", "/agent/datasets/basket/download?version=0"},
		{"negative version", "/agent/datasets/basket/download?version=-2"},
		{"a version that does not exist", "/agent/datasets/basket/download?version=99"},
	} {
		rec := do(h, http.MethodGet, tc.path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404 (body %s)", tc.name, rec.Code, rec.Body)
		}
		bodies = append(bodies, rec.Body.String())
		if bodies[0] != bodies[len(bodies)-1] {
			t.Fatalf("%s: body %q differs from %q — these must be indistinguishable",
				tc.name, bodies[len(bodies)-1], bodies[0])
		}
	}
	if !strings.Contains(bodies[0], datasetNotFound) {
		t.Fatalf("404 body = %q, want the shared %q", bodies[0], datasetNotFound)
	}
}

// Tenancy, end to end and from the attacker's seat: a credential for `other`
// asks for `acme`'s dataset on every one of the four routes, by name, and gets
// nothing back on any of them — not the bytes, not the metadata, not even the
// knowledge that the name exists.
func TestDatasetRoutes_CrossProjectReadsAreRefused(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 7)}}
	blobs := &fakeDatasetBlobs{data: map[string]string{
		agentdb.DatasetBlobPrefix + "basket-7": "timestamp,value\n2026-08-20T00:00:00Z,143.90\n",
	}}
	h := newDatasetHandlers(t, store, blobs, identityFor("other"))

	if rec := do(h, http.MethodGet, "/agent/datasets", ""); rec.Code != http.StatusOK ||
		strings.TrimSpace(rec.Body.String()) != `{"datasets":[]}` {
		t.Fatalf("list from another project: status=%d body=%s — it must not enumerate acme's datasets",
			rec.Code, rec.Body)
	}
	for _, path := range []string{
		"/agent/datasets/basket",
		"/agent/datasets/basket/versions",
		"/agent/datasets/basket/download",
		"/agent/datasets/basket/download?version=7",
	} {
		rec := do(h, http.MethodGet, path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s from another project: status=%d body=%s, want 404", path, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "143.90") {
			t.Fatalf("%s served another project's bytes", path)
		}
	}
	if store.gotProject != "other" {
		t.Fatalf("the store was asked about project %q — the project must come from the credential", store.gotProject)
	}
}

// A store that is not wired is 501 on all four; a credential naming no project
// is 403 on all four. Neither answer is about a dataset, so neither can leak one.
func TestDatasetRoutes_NoStoreIs501AndNoProjectIs403(t *testing.T) {
	paths := []string{
		"/agent/datasets",
		"/agent/datasets/basket",
		"/agent/datasets/basket/versions",
		"/agent/datasets/basket/download",
	}
	noStore := newDatasetHandlers(t, nil, &fakeDatasetBlobs{}, identityFor("acme"))
	noProject := newDatasetHandlers(t, &fakeDatasets{}, &fakeDatasetBlobs{},
		func(*http.Request) (Identity, error) { return Identity{UserEmail: "u@x"}, nil })
	for _, path := range paths {
		if rec := do(noStore, http.MethodGet, path, ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s with no store: status=%d, want 501", path, rec.Code)
		}
		if rec := do(noProject, http.MethodGet, path, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s with no project: status=%d, want 403", path, rec.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// The download route.
// ---------------------------------------------------------------------------

func TestDatasetDownload_ServesBytesWithTheRightHeaders(t *testing.T) {
	const body = "timestamp,value\n2026-08-19T00:00:00Z,141.22\n2026-08-20T00:00:00Z,143.90\n"
	store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 7)}}
	blobs := &fakeDatasetBlobs{data: map[string]string{agentdb.DatasetBlobPrefix + "basket-7": body}}
	h := newDatasetHandlers(t, store, blobs, identityFor("acme"))

	rec := do(h, http.MethodGet, "/agent/datasets/basket/download", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if rec.Body.String() != body {
		t.Fatalf("body = %q, want the blob verbatim", rec.Body)
	}
	if blobs.got != agentdb.DatasetBlobPrefix+"basket-7" {
		t.Fatalf("read blob key %q — the key must come from the row", blobs.got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("Content-Type = %q, want the row's text/csv", ct)
	}
	if v := rec.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", v)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") ||
		!strings.Contains(cd, "basket") {
		t.Fatalf("Content-Disposition = %q, want attachment named after the dataset", cd)
	}
	// size_bytes is metadata written by a different call than the bytes; a
	// stale one would truncate the response, so the handler must not set a
	// length from it. (The transport's own framing is not under test here —
	// this pins the handler.)
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("Content-Length = %q, want none — it would come from stale metadata", cl)
	}
}

// A row with no content type is served as octet-stream. Never let the browser
// guess at agent-produced bytes on this origin.
func TestDatasetDownload_EmptyContentTypeIsOctetStream(t *testing.T) {
	row := dataset("acme", "basket", 1)
	row.ContentType = ""
	blobs := &fakeDatasetBlobs{data: map[string]string{row.BlobPath: "x"}}
	h := newDatasetHandlers(t, &fakeDatasets{rows: []*agentdb.Dataset{row}}, blobs, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/datasets/basket/download", "")
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
	}
}

// ?version= absent is the current version; present and valid is that version.
func TestDatasetDownload_VersionSelection(t *testing.T) {
	rows := []*agentdb.Dataset{dataset("acme", "basket", 3), dataset("acme", "basket", 7)}
	data := map[string]string{}
	for _, r := range rows {
		data[r.BlobPath] = "v" + fmt.Sprint(r.Version)
	}
	for _, tc := range []struct {
		name, path, wantBody, wantCall string
		wantVersion                    int
	}{
		{"absent means current", "/agent/datasets/basket/download", "v7", "CurrentDataset", 0},
		{"pinned version", "/agent/datasets/basket/download?version=3", "v3", "GetDatasetVersion", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDatasets{rows: rows}
			h := newDatasetHandlers(t, store, &fakeDatasetBlobs{data: data}, identityFor("acme"))
			rec := do(h, http.MethodGet, tc.path, "")
			if rec.Code != http.StatusOK || rec.Body.String() != tc.wantBody {
				t.Fatalf("status=%d body=%q, want 200/%q", rec.Code, rec.Body, tc.wantBody)
			}
			if store.calls[len(store.calls)-1] != tc.wantCall || store.gotVersion != tc.wantVersion {
				t.Fatalf("store calls=%v version=%d, want %s/%d",
					store.calls, store.gotVersion, tc.wantCall, tc.wantVersion)
			}
		})
	}
}

// The row is right there and the bytes are not: 410, not 404. The distinction
// matters to an operator — "this existed and no longer does" is where the
// reaper or an orphan sweep is the answer.
func TestDatasetDownload_MissingBlobIs410(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 7)}}
	h := newDatasetHandlers(t, store, &fakeDatasetBlobs{}, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/datasets/basket/download", "")
	if rec.Code != http.StatusGone {
		t.Fatalf("status=%d, want 410 (body %s)", rec.Code, rec.Body)
	}
}

// The asymmetry is the point: a host with no blob reader can still answer
// metadata, and says 501 on the one route it cannot honour.
func TestDatasetDownload_NilBlobReaderIs501OnThatRouteAlone(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 7)}}
	h := newDatasetHandlers(t, store, nil, identityFor("acme"))
	if rec := do(h, http.MethodGet, "/agent/datasets/basket/download", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("download with no blob reader: status=%d, want 501", rec.Code)
	}
	for _, path := range []string{"/agent/datasets", "/agent/datasets/basket", "/agent/datasets/basket/versions"} {
		if rec := do(h, http.MethodGet, path, ""); rec.Code != http.StatusOK {
			t.Fatalf("%s with no blob reader: status=%d, want 200 — metadata does not need bytes", path, rec.Code)
		}
	}
}

// A dataset token pins ONE name. Used against another name it is 404 and not
// 403 — the same non-oracle rule, because otherwise a token holder could walk
// the project's dataset names by watching which refusal came back.
//
// It carries no version, deliberately: a URL minted while a name sat at v3 must
// still fetch v7 of the same name afterwards.
func TestDatasetDownload_ScopedTokenIsPinnedToOneName(t *testing.T) {
	rows := []*agentdb.Dataset{dataset("acme", "basket", 3), dataset("acme", "basket", 7), dataset("acme", "other", 1)}
	data := map[string]string{}
	for _, r := range rows {
		data[r.BlobPath] = "v" + fmt.Sprint(r.Version)
	}
	newH := func(scope string) *Handlers {
		return newDatasetHandlers(t, &fakeDatasets{rows: rows}, &fakeDatasetBlobs{data: data},
			identityWithDatasetScope("acme", scope))
	}
	if rec := do(newH("acme/basket"), http.MethodGet, "/agent/datasets/basket/download", ""); rec.Code != http.StatusOK {
		t.Fatalf("its own dataset: status=%d body=%s, want 200", rec.Code, rec.Body)
	}
	// No version in the pin: the same token reaches an older version by name.
	if rec := do(newH("acme/basket"), http.MethodGet, "/agent/datasets/basket/download?version=3", ""); rec.Code != http.StatusOK ||
		rec.Body.String() != "v3" {
		t.Fatalf("pinned version through a name-scoped token: status=%d body=%q", rec.Code, rec.Body)
	}
	for _, tc := range []struct{ name, scope, path string }{
		{"another dataset in the same project", "acme/basket", "/agent/datasets/other/download"},
		{"the same name in another project", "elsewhere/basket", "/agent/datasets/basket/download"},
	} {
		rec := do(newH(tc.scope), http.MethodGet, tc.path, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", tc.name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), datasetNotFound) {
			t.Fatalf("%s: body %q, want the shared 404 sentence", tc.name, rec.Body)
		}
	}
}

// A dataset-scoped credential must not become a general project credential by
// walking onto a metadata route. It is not a credential the middleware issues
// for those paths at all (that is the auth ticket's lock) — this pins the other
// half: the metadata routes read no dataset scope and grant nothing extra.
func TestDatasetRoutes_ScopedCredentialCannotEnumerate(t *testing.T) {
	store := &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 7), dataset("acme", "other", 1)}}
	h := newDatasetHandlers(t, store, &fakeDatasetBlobs{}, identityWithDatasetScope("acme", "acme/basket"))
	rec := do(h, http.MethodGet, "/agent/datasets/other/download", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for a name outside the pin", rec.Code)
	}
	if len(store.calls) != 0 {
		t.Fatalf("the store was consulted (%v) before the pin was checked — the refusal must not depend on the row",
			store.calls)
	}
}

// The four patterns are pinned here as literals because five later tickets and
// one MCP tool build URLs against them — and because a host that blanks one
// must UNMOUNT that route rather than panic on an empty ServeMux pattern, which
// is what the guarded registration map in Mux() is for.
func TestDatasetRoutes_DefaultPatternsAndUnmounting(t *testing.T) {
	for field, want := range map[string]string{
		DefaultEndpoints.ListDatasets:    "GET /agent/datasets",
		DefaultEndpoints.GetDataset:      "GET /agent/datasets/{name}",
		DefaultEndpoints.DatasetVersions: "GET /agent/datasets/{name}/versions",
		DefaultEndpoints.DownloadDataset: "GET /agent/datasets/{name}/download",
	} {
		if field != want {
			t.Fatalf("pattern %q, want %q", field, want)
		}
	}

	ep := DefaultEndpoints
	ep.DownloadDataset = "" // a host that does not want to serve bytes
	h := newHandlers(t, Config{
		Runner:       stubRunner{},
		Store:        stubStore{},
		Identity:     identityFor("acme"),
		Endpoints:    ep,
		Datasets:     &fakeDatasets{rows: []*agentdb.Dataset{dataset("acme", "basket", 1)}},
		DatasetBlobs: &fakeDatasetBlobs{},
	})
	if rec := do(h, http.MethodGet, "/agent/datasets/basket/download", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a blanked pattern should be unmounted: status=%d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/agent/datasets/basket", ""); rec.Code != http.StatusOK {
		t.Fatalf("the other three stay mounted: status=%d", rec.Code)
	}
}
