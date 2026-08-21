package httpapi

// Dataset reads over HTTP (O5 of design/2026-08-20-agent-wolf.md).
//
//	GET /agent/datasets?selector=&limit=          → {"datasets":[…]}
//	GET /agent/datasets/{name}                    → the current version, bare
//	GET /agent/datasets/{name}/versions?limit=    → {"versions":[…]}, newest first
//	GET /agent/datasets/{name}/download?version=&token=
//	                                              → the raw bytes
//
// A dataset is a project-scoped, named, versioned, labelled blob (agentdb's
// datasets.go): every write creates a new immutable version and "current" is the
// highest version of (project, name). These four routes are the READ half — the
// write half is the dataset_put MCP tool, because the bytes are pulled out of a
// running container's workspace and never cross an HTTP request body.
//
// Tenancy, as everywhere else on this surface: the project is the credential's
// Customer claim and there is NO project parameter on any of the four routes.
// A name that belongs to another project is indistinguishable from one that does
// not exist — see datasetNotFound.
//
// Three things are deliberately shaped the way they are:
//
//  1. **Two store seams with different defaulting rules.** Config.Datasets is
//     metadata and is auto-filled from AgentDB like every other product-layer
//     store; Config.DatasetBlobs is the BYTE plane and is not, because this
//     package must not import extension (see MemoryStore's header) and agentdb
//     cannot reach a blob store at all. The host wires it in cmd/agentd from the
//     one process-wide BlobStore. A nil Datasets is 501 everywhere; a nil
//     DatasetBlobs is 501 on the download route ALONE — metadata still answers.
//
//  2. **The response body is datasetResp, not agentdb.Dataset.** That struct
//     carries json tags for `blob_path` and `project`: the first is an internal
//     storage key (handing it out invites a caller to build its own blob
//     addresses) and the second is already the caller's own credential. Same
//     reasoning as memoryRecordResp.
//
//  3. **The download route accepts a scoped ?token= credential** that no other
//     route in the tree accepts, verified by agentd's middleware, never here —
//     httpapi holds no JWT code. What arrives is Identity.DatasetScope, a
//     "<project>/<name>" pin this file compares against the request. A query
//     parameter rather than a fragment or a header because the consumer is an
//     agent's `curl` inside a container: curl cannot send a fragment, and a
//     header would make the model compose one.
//
// Postgres-only, like the store (jsonb labels + the unique index CAS depends
// on). On the SQLite fallback every store call returns
// agentdb.ErrDatasetRequiresPostgres and these routes answer 501 — the same
// "not configured on this host" posture the memory routes take.

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
)

// DatasetStore is the slice of agentdb.Store the four read routes need. Note
// what is NOT in it: no create, no delete. Datasets are written from inside a
// session (the dataset_put tool pulls the bytes out of the workspace) and
// removed only by the reaper, so neither belongs on an HTTP seam.
type DatasetStore interface {
	// ListDatasets answers at most one row per name — always the highest
	// version — filtered by a Kubernetes label selector. The store clamps the
	// limit; this package passes it through untouched.
	ListDatasets(ctx context.Context, project, selector string, limit int) ([]*agentdb.Dataset, error)
	// CurrentDataset is the highest version of (project, name).
	CurrentDataset(ctx context.Context, project, name string) (*agentdb.Dataset, error)
	// GetDatasetVersion is one pinned version. Versions start at 1.
	GetDatasetVersion(ctx context.Context, project, name string, version int) (*agentdb.Dataset, error)
	// ListDatasetVersions is the version history, newest first. A name with no
	// rows in this project is ErrDatasetNotFound rather than an empty slice, so
	// the route can answer 404 instead of "exists, but empty".
	ListDatasetVersions(ctx context.Context, project, name string, limit int) ([]*agentdb.Dataset, error)
}

// The concrete store must always satisfy the seam.
var _ DatasetStore = (*agentdb.Store)(nil)

// DatasetBlobReader is the byte plane: one method, the only one this package
// needs, declared here rather than taken as extension.BlobStore because httpapi
// must not import extension. extension.BlobStore satisfies it structurally, and
// cmd/agentd passes the process-wide store in.
//
// It is deliberately read-only. Nothing in this package may write, delete or
// enumerate blobs: an enumeration would reach `_artifacts/bytes/` and every
// session snapshot in the deployment, since agentd runs ONE global BlobStore
// (agentdb.DatasetBlobPrefix's comment says why that matters).
type DatasetBlobReader interface {
	Read(ctx context.Context, key string) (io.ReadCloser, error)
}

// datasetNotFound is the ONE answer for absent, malformed, foreign-project, and
// "that version does not exist" alike, byte for byte. A dataset name is chosen
// by whoever wrote it — `1a2b3c4d-drone-suppliers-basket` — and is guessable in
// a way a uuid is not, so any distinguishable answer here is a project-membership
// oracle. Same rule and same posture as memoryNotFound and resolveSessionByName.
const datasetNotFound = "dataset not found"

// datasetResp is one dataset version on the wire.
//
// `blob_path` and `project` are absent, and that is the reason this type exists
// rather than serialising agentdb.Dataset — see this file's header. The ten
// fields below `id` are byte-identical in name to the dataset_list MCP tool's
// output, so the HTTP surface and the tool surface describe a dataset the same
// way.
type datasetResp struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Version          int               `json:"version"`
	Labels           map[string]string `json:"labels"`
	SizeBytes        int64             `json:"size_bytes"`
	RowCount         int               `json:"row_count"`
	SHA256           string            `json:"sha256"`
	ContentType      string            `json:"content_type"`
	CreatedByWorker  string            `json:"created_by_worker"`
	CreatedBySession string            `json:"created_by_session"`
	// CreatedAt is unix MILLISECONDS — the datasets table's unit, and the same
	// one memories use. The agent_* tables are seconds; the two do not unify.
	CreatedAt int64 `json:"created_at"`
}

func datasetOf(d *agentdb.Dataset) datasetResp {
	// Labels are copied into a plain map so an absent label set renders as {}
	// rather than null — a client should not have to branch on that.
	labels := make(map[string]string, len(d.Labels))
	for k, v := range d.Labels {
		labels[k] = v
	}
	return datasetResp{
		ID:               d.ID,
		Name:             d.Name,
		Version:          d.Version,
		Labels:           labels,
		SizeBytes:        d.SizeBytes,
		RowCount:         d.RowCount,
		SHA256:           d.SHA256,
		ContentType:      d.ContentType,
		CreatedByWorker:  d.CreatedByWorker,
		CreatedBySession: d.CreatedBySession,
		CreatedAt:        d.CreatedAt,
	}
}

func datasetsOf(rows []*agentdb.Dataset) []datasetResp {
	out := make([]datasetResp, 0, len(rows))
	for _, d := range rows {
		if d == nil {
			continue
		}
		out = append(out, datasetOf(d))
	}
	return out
}

// datasetReadable is the gate all four routes share: a store must be wired, and
// the credential must name a project. Both answers are about the request's
// world rather than about any particular dataset, so neither can leak one —
// which is why they run before the name is even looked at. memoryReadable's
// reasoning exactly.
//
// It writes the error response and returns ok=false; the caller just returns.
func (h *Handlers) datasetReadable(w http.ResponseWriter, id Identity) bool {
	if h.cfg.Datasets == nil {
		http.Error(w, "the dataset store is not configured on this host", http.StatusNotImplemented)
		return false
	}
	if id.Customer == "" {
		// 403 and not 404: no dataset is being hidden — the question cannot be
		// asked at all, because datasets are namespaced by project and this
		// credential names none.
		http.Error(w, "no project in token", http.StatusForbidden)
		return false
	}
	return true
}

// datasetName reads and validates {name}. A name that could not legally exist
// is 404 rather than 400: 404 parity is the whole point (see datasetNotFound),
// and a caller able to tell "illegal" from "absent" from "someone else's" can
// walk the difference.
func datasetName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := strings.TrimSpace(r.PathValue("name"))
	if err := agentdb.ValidateDatasetName(name); err != nil {
		http.Error(w, datasetNotFound, http.StatusNotFound)
		return "", false
	}
	return name, true
}

// writeDatasetError maps a store failure onto a status.
//
// The default is 500, NOT 404 — a database refusing connections is not a
// dataset that is missing, and answering "not found" would send an operator
// hunting for a row that is sitting right there. writeMemoryReadError's rule.
func writeDatasetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentdb.ErrDatasetNotFound):
		http.Error(w, datasetNotFound, http.StatusNotFound)
	case errors.Is(err, agentdb.ErrDatasetRequiresPostgres):
		http.Error(w, err.Error(), http.StatusNotImplemented)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ListDatasets serves GET /agent/datasets?selector=&limit=.
func (h *Handlers) ListDatasets(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.datasetReadable(w, id) {
		return
	}
	rows, err := h.cfg.Datasets.ListDatasets(r.Context(),
		id.Customer, // from the claim, always — never a parameter
		r.URL.Query().Get("selector"),
		queryInt(r, "limit", 0), // 0 → the store's default; the store clamps
	)
	if err != nil {
		if errors.Is(err, agentdb.ErrDatasetRequiresPostgres) {
			http.Error(w, err.Error(), http.StatusNotImplemented)
			return
		}
		// Everything else this call can fail with is the caller's selector, and
		// the parser's own message is the most useful thing to hand back — the
		// same two-way mapping ListMemories does.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"datasets": datasetsOf(rows)})
}

// GetDataset serves GET /agent/datasets/{name} — the current version's
// metadata, as a BARE object (no envelope).
func (h *Handlers) GetDataset(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.datasetReadable(w, id) {
		return
	}
	name, ok := datasetName(w, r)
	if !ok {
		return
	}
	// The project is the store call's first argument — tenancy decided in the
	// query, never by filtering a row that was already read.
	ds, err := h.cfg.Datasets.CurrentDataset(r.Context(), id.Customer, name)
	if err != nil {
		writeDatasetError(w, err)
		return
	}
	writeJSON(w, datasetOf(ds))
}

// ListDatasetVersions serves GET /agent/datasets/{name}/versions?limit= —
// the version history, newest first.
func (h *Handlers) ListDatasetVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.datasetReadable(w, id) {
		return
	}
	name, ok := datasetName(w, r)
	if !ok {
		return
	}
	rows, err := h.cfg.Datasets.ListDatasetVersions(r.Context(), id.Customer, name, queryInt(r, "limit", 0))
	if err != nil {
		writeDatasetError(w, err)
		return
	}
	writeJSON(w, map[string]any{"versions": datasetsOf(rows)})
}

// datasetVersionParam reads ?version=. Absent means "the current version";
// present means exactly that version.
//
// Non-numeric and below 1 are 404 and not 400, for the same reason a malformed
// name is: this route answers one sentence to every question it will not answer,
// and `?version=0` is a question about a version that cannot exist.
func datasetVersionParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("version"))
	if raw == "" {
		return 0, true // 0 = current
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		http.Error(w, datasetNotFound, http.StatusNotFound)
		return 0, false
	}
	return v, true
}

// DownloadDataset serves GET /agent/datasets/{name}/download?version=&token= —
// the only place dataset bytes leave this package.
//
// Auth is whatever identify() produced: a project API key, a console JWT, or —
// uniquely on this route — a scoped dataset token the host's middleware
// verified out of ?token= and handed over as Identity.DatasetScope.
func (h *Handlers) DownloadDataset(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.datasetReadable(w, id) {
		return
	}
	if h.cfg.DatasetBlobs == nil {
		// Metadata still answers on this host; only the bytes are unreachable.
		http.Error(w, "the dataset blob store is not configured on this host", http.StatusNotImplemented)
		return
	}
	name, ok := datasetName(w, r)
	if !ok {
		return
	}
	// A dataset token pins ONE (project, name) pair. Presented against another
	// name it is 404 and not 403 — the non-oracle rule again: a token holder
	// probing names must not be able to tell "not yours" from "not there".
	//
	// The pin carries no version, deliberately (devclaims.DatasetScope): a URL
	// minted while a name is at v3 still resolves to that name's requested
	// version after a tick moved it to v4.
	if id.DatasetScope != "" && id.DatasetScope != id.Customer+"/"+name {
		http.Error(w, datasetNotFound, http.StatusNotFound)
		return
	}
	version, ok := datasetVersionParam(w, r)
	if !ok {
		return
	}
	var (
		ds  *agentdb.Dataset
		err error
	)
	if version == 0 {
		ds, err = h.cfg.Datasets.CurrentDataset(r.Context(), id.Customer, name)
	} else {
		ds, err = h.cfg.Datasets.GetDatasetVersion(r.Context(), id.Customer, name, version)
	}
	if err != nil {
		writeDatasetError(w, err)
		return
	}
	rc, err := h.cfg.DatasetBlobs.Read(r.Context(), ds.BlobPath)
	if rc != nil {
		defer rc.Close() //nolint:errcheck // read-only reader
	}
	if err != nil || rc == nil {
		// The row is right there and the bytes are not: 410, exactly as the
		// artifact route answers for an extracted artifact whose blob is gone.
		// Not 404 — the caller's question was well formed and the answer is
		// "this existed and no longer does", which is a different fact and one
		// an operator needs to see.
		http.Error(w, "dataset bytes are no longer available", http.StatusGone)
		return
	}

	ct := ds.ContentType
	if ct == "" {
		// Never let the browser guess. Dataset bytes are agent-produced content
		// served from the API's own origin.
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// attachment, not inline, and for a security reason rather than a UX one: a
	// dataset can hold anything an agent wrote, HTML included, and rendering it
	// inline on this origin would be scripting with the console's session in
	// reach. serveArtifactBytes's reasoning, byte for byte.
	if cd := mime.FormatMediaType("attachment", map[string]string{"filename": ds.Name}); cd != "" {
		w.Header().Set("Content-Disposition", cd)
	} else {
		w.Header().Set("Content-Disposition", "attachment")
	}
	// No Content-Length: size_bytes is metadata written by a DIFFERENT call than
	// the bytes (the pull writes the blob, the store writes the row), and a
	// stale value truncates the response or breaks the connection. Chunked
	// transfer costs nothing at dataset scale.
	if _, err := io.Copy(w, rc); err != nil {
		// The status line is long gone; there is nothing to say to the client
		// that it will not already have noticed as a short read.
		return
	}
}
