package main

// mcp_datasets.go — the dataset MCP tools (design/2026-08-20-agent-wolf.md,
// O6b), registered onto the core MCP server in main.go.
//
// The whole surface is three tools:
//
//	dataset_list(selector?, limit?)                   → metadata, never bytes
//	dataset_get(name, version?)                       → a scoped download URL
//	dataset_put(name, path, if_version, …)            → a new version
//
// # Why bytes never travel through a tool result
//
// A dataset is a shared numeric time series — a price history, a macro series —
// and the whole reason the atom exists is that such a thing must never cross the
// model's context window. So the two directions use two different transports and
// neither is the tool result:
//
//	write │ dataset_put names a file under /workspace; agentd pulls it out of the
//	      │ running container with Exec+cat (datasetpull.go) and stores the blob
//	read  │ dataset_get returns a short-lived, single-dataset download URL; the
//	      │ agent curls it to a file inside its own container
//
// ⚠️ The bytes stay out of context; the CREDENTIAL does not. dataset_get's
// download_url carries a bearer token, is returned as a tool result, and
// therefore enters the model context, the persisted transcript, the SSE stream,
// and every subscriber of worker.finished. It is bounded by a 300s TTL and a
// single-dataset scope, and that bound is the entire mitigation. The tool
// description says so to the model as well as here.
//
// # Compare-and-swap is mandatory
//
// Every write carries if_version, which must equal the current version (0 means
// "must not exist"). Without it two sessions that both read v5 and both write
// would silently lose a day of data. The check is the store's — a unique index
// on (project, name, version) — never a read-then-write here.
//
// # Provenance comes from the token, never from an argument
//
// created_by_worker and created_by_session are taken from mcpCaller, which the
// MCP layer resolved from the session token before any handler ran. There is
// deliberately no argument that lets a caller name itself, and a caller whose
// session row could not be read (Identified == false) is refused rather than
// written with empty provenance. That is what makes the embedding application's
// trust rule — "empty provenance means the SERVER wrote it" — enforceable
// rather than aspirational: nothing inside a container can produce a row that
// looks server-written.
//
// # No config event
//
// A dataset is data, not configuration, so these tools write no config event —
// the same call memory_create makes. Note for anyone auditing that decision:
// TestMutationsAreLogged classifies store methods by NOUN through
// looksLikeConfigMutation over configEntityNouns
// (go/agentdb/config_events_test.go:562-592), and `Dataset` is not among those
// nouns, so that sweep does not cover the dataset store methods at all.
// Verified 2026-08-21, not assumed; this ticket's Validation re-runs that test
// so a later change to the noun list surfaces here rather than silently.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	agentkit "github.com/binocarlos/badcode-agent-orange"
	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/execenv"
	"github.com/binocarlos/badcode-agent-orange/extension"
)

// datasetCatalog is the narrow slice of *agentdb.Store the tools need. Note
// what is NOT in it: no update and no delete. Versions are immutable and only
// the reaper (O8) ever removes one, so the append-only posture survives the
// seam.
type datasetCatalog interface {
	CreateDatasetVersion(ctx context.Context, d *agentdb.Dataset, ifVersion int) (*agentdb.Dataset, error)
	CurrentDataset(ctx context.Context, project, name string) (*agentdb.Dataset, error)
	GetDatasetVersion(ctx context.Context, project, name string, version int) (*agentdb.Dataset, error)
	ListDatasets(ctx context.Context, project, selector string, limit int) ([]*agentdb.Dataset, error)
}

// sessionExecer is the engine seam dataset_put needs: run argv inside the
// CALLING session's container. agentkit.Runner satisfies it (asserted below).
// Narrow on purpose, exactly like sessionSnapshotter (mcp_images.go) and
// sessionLocator (mcp_skills.go) — these tools may exec into a session and do
// nothing else with the Runner, and a test can fake one honestly.
type sessionExecer interface {
	ExecInSession(ctx context.Context, ref agentkit.SessionRef, cmd []string,
		opts execenv.ExecOptions) (*execenv.ExecResult, error)
}

var _ sessionExecer = (agentkit.Runner)(nil)

const (
	// datasetListDefaultLimit / datasetListMaxLimit bound dataset_list. The
	// store clamps too (clampDatasetListLimit), but silently — and a silent
	// truncation is worse than none, so the cap is applied here where it can be
	// STATED in the result.
	datasetListDefaultLimit = 20
	datasetListMaxLimit     = 100

	// datasetDefaultContentType is what a dataset is unless the caller says
	// otherwise. It matters beyond labelling: row_count is only counted for
	// text/csv (datasetpull.go), and row_count is what the shrink guard below
	// defends.
	datasetDefaultContentType = "text/csv"

	// defaultDatasetMaxBytes is the per-write cap when AGENTKIT_DATASET_MAX_BYTES
	// is unset. Generous — a decade of daily observations is a few hundred KB —
	// but bounded, because the exec that pulls the file buffers the whole of it
	// in agentd's memory (go/execenv/docker/client.go:238-240).
	defaultDatasetMaxBytes = 64 << 20 // 64 MiB

	// datasetMaxBytesVar names the knob in exactly one place; the boot error
	// quotes it so an operator can find what they typed wrong.
	datasetMaxBytesVar = "AGENTKIT_DATASET_MAX_BYTES"
)

// parseDatasetMaxBytes resolves AGENTKIT_DATASET_MAX_BYTES: a plain integer
// COUNT OF BYTES, not a "64MiB"-style size string — the unit is in the variable
// name and a suffix grammar would be one more thing to get subtly wrong.
//
// Unset or blank is the 64 MiB default. Anything that is not a positive integer
// is a boot error naming the variable, because an operator who set a cap and got
// the default silently would find out only when a container OOMed agentd.
func parseDatasetMaxBytes(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultDatasetMaxBytes, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a whole number of bytes", datasetMaxBytesVar, raw)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: %q must be a positive number of bytes (omit the variable for the %d byte default)",
			datasetMaxBytesVar, raw, int64(defaultDatasetMaxBytes))
	}
	return n, nil
}

type datasetTools struct {
	store    datasetCatalog
	sessions sessionExecer
	blobs    extension.BlobStore
	// secret signs download tokens — the API-class secret (AGENTKIT_JWT_SECRET),
	// which is what apiAuthMiddleware's ?token= leg verifies with (O5).
	secret []byte
	// selfURL is AGENTKIT_SELF_URL: how a session container, nested in DinD,
	// reaches agentd. Read ONCE at boot and passed in — never os.Getenv from
	// inside a handler. It is deliberately NOT AGENTKIT_PUBLIC_BASE_URL (see
	// permalink.go): the public base is a browser-facing address and is
	// unreachable from inside a nested container.
	selfURL string
	// maxBytes is the per-write cap handed to the pull pipeline.
	maxBytes int64
	// pull is the workspace pull pipeline (O6a), injected so tests exercise the
	// tools without a container. Production binds it to pullWorkspaceFile.
	pull func(ctx context.Context, exec sessionExec, relPath, contentType string,
		maxBytes int64, blobs extension.BlobStore) (*pulledFile, error)
}

func newDatasetTools(store datasetCatalog, sessions sessionExecer, blobs extension.BlobStore,
	secret []byte, selfURL string, maxBytes int64) *datasetTools {
	return &datasetTools{
		store:    store,
		sessions: sessions,
		blobs:    blobs,
		secret:   secret,
		selfURL:  strings.TrimRight(strings.TrimSpace(selfURL), "/"),
		maxBytes: maxBytes,
		pull:     pullWorkspaceFile,
	}
}

// ---------------------------------------------------------------------------
// Result shapes
//
// The ten field NAMES below are byte-identical to httpapi's datasetResp
// (go/httpapi/datasets.go, O5), which is what GET /agent/datasets returns.
// Six tickets read one or the other and nothing reconciles them at runtime, so
// a rename here is a silent integration break there. Both sides are pinned by a
// literal whole-body assertion in their own package's tests.
// ---------------------------------------------------------------------------

// datasetEntry is one dataset in dataset_list: the current version's metadata
// and nothing else. No bytes, and no blob_path — that is an internal storage
// key, and no project, which is already the caller's own credential.
type datasetEntry struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	SizeBytes   int64  `json:"size_bytes"`
	RowCount    int    `json:"row_count"`
	SHA256      string `json:"sha256"`
	ContentType string `json:"content_type"`
	// Labels renders as {} rather than null when there are none: a model should
	// not have to reason about the difference.
	Labels map[string]string `json:"labels"`
	// CreatedAt is unix MILLISECONDS — the datasets table's unit, the same one
	// memories use, and deliberately unlike dataset_get's expires_at, which is
	// unix SECONDS. The two units do not unify; the house rule is to encode the
	// unit wherever one is carried.
	CreatedAt        int64  `json:"created_at"`
	CreatedByWorker  string `json:"created_by_worker"`
	CreatedBySession string `json:"created_by_session"`
}

func entryOf(d *agentdb.Dataset) datasetEntry {
	return datasetEntry{
		Name:             d.Name,
		Version:          d.Version,
		SizeBytes:        d.SizeBytes,
		RowCount:         d.RowCount,
		SHA256:           d.SHA256,
		ContentType:      d.ContentType,
		Labels:           labelMap(d.Labels),
		CreatedAt:        d.CreatedAt,
		CreatedByWorker:  d.CreatedByWorker,
		CreatedBySession: d.CreatedBySession,
	}
}

// datasetHandle is dataset_get's result: the same metadata plus the credential
// that fetches the bytes.
type datasetHandle struct {
	Name        string            `json:"name"`
	Version     int               `json:"version"`
	SizeBytes   int64             `json:"size_bytes"`
	RowCount    int               `json:"row_count"`
	SHA256      string            `json:"sha256"`
	ContentType string            `json:"content_type"`
	Labels      map[string]string `json:"labels"`
	DownloadURL string            `json:"download_url"`
	// ExpiresAt is unix SECONDS, matching its sibling embedTokenResponse.ExpiresAt
	// (embedtoken.go:63-67) and deliberately unlike created_at's milliseconds.
	ExpiresAt int64 `json:"expires_at"`
}

// datasetWritten is dataset_put's result: the row as the database now holds it,
// read back rather than echoed from the caller's arguments (docs/18 §6).
type datasetWritten struct {
	Name      string `json:"name"`
	Version   int    `json:"version"`
	SizeBytes int64  `json:"size_bytes"`
	RowCount  int    `json:"row_count"`
	SHA256    string `json:"sha256"`
}

// ---------------------------------------------------------------------------
// Tool descriptions
//
// Prompt, not documentation: read on every job, and the only thing standing
// between a model and a misuse of the atom.
// ---------------------------------------------------------------------------

const datasetListDescription = `List this project's datasets — one entry per dataset, at its CURRENT version.

A dataset is a named, versioned file this project shares across sessions: a ` +
	`price history, a macro series, anything numeric that would be absurd to ` +
	`retype into a memory. This returns METADATA ONLY — name, version, size, ` +
	`row_count, sha256, labels, provenance. Never the contents. To read one, call ` +
	`dataset_get and download it to a file.

The optional selector uses the same Kubernetes-style grammar as memory_search, ` +
	`comma-ANDed: "hypothesis=1a2b3c4d", "metric in (cpi, ppi)", "exists metric", ` +
	`"!deprecated". No OR and no nesting — run two searches instead.

limit defaults to 20 and is capped at 100; when the cap applies the result says ` +
	`so rather than truncating silently.`

const datasetGetDescription = `Get a short-lived download URL for one dataset — then curl it TO A FILE.

Returns the dataset's metadata plus a download_url. Omit version for the ` +
	`current one; pass a version to pin an exact past one.

Use it like this, and nothing else:

    curl -sS -o prices.csv "<download_url>"

Then read the file with your tools (python, awk, duckdb). Do NOT print the ` +
	`file's contents into the conversation: these files are thousands of rows ` +
	`long, and pasting one wastes the context you need to think in.

Do NOT echo the download_url back either. It CARRIES A BEARER CREDENTIAL: ` +
	`anything that can read this conversation — the transcript, the event ` +
	`stream, a subscriber — can fetch the file with it. It is scoped to this one ` +
	`dataset and expires in a few minutes (expires_at, unix seconds), which is ` +
	`the entire mitigation. Fetch it, use it, do not repeat it.`

const datasetPutDescription = `Write a new version of a dataset from a file in your workspace.

Give the path of a file you have already written under /workspace (relative, ` +
	`e.g. "prices.csv" — not an absolute path, and it may not escape /workspace). ` +
	`agentd reads the file out of this container itself; the contents never pass ` +
	`through this conversation.

if_version is COMPARE-AND-SWAP and it is not optional. Pass the version you ` +
	`based your work on — the version dataset_get or dataset_list just showed ` +
	`you — or 0 to mean "this dataset must not exist yet". If someone else wrote ` +
	`in the meantime you get an error naming the real current version: re-read ` +
	`that version, merge your work into it, and try again. This is what stops ` +
	`two jobs silently overwriting each other's day of data.

Every write creates a NEW version; nothing is ever overwritten in place.

For a refetchable series (prices, a macro series) write the WHOLE series each ` +
	`time — dividends re-adjust price history and statistical agencies restate ` +
	`macro data months later, so an appended file diverges from its source and ` +
	`nothing detects it.

The canonical format for a series is CSV with the header exactly ` +
	"`timestamp,value`" + `, RFC3339 timestamps in UTC, ascending, one metric per ` +
	`dataset. Readers depend on that header being exact.

A replacement with fewer than half as many rows as the current version is ` +
	`REFUSED, because that is what a half-finished fetch looks like. If the ` +
	`series really did shrink, say so with allow_shrink: true.`

// tools returns the three dataset tools, in the order the model sees them:
// look, read, write.
func (d *datasetTools) tools() []*mcpTool {
	return []*mcpTool{
		{
			Name:        "dataset_list",
			Description: datasetListDescription,
			InputSchema: objectSchema(map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "Kubernetes-style label selector, comma-ANDed. Optional; omit for every dataset in the project.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "How many datasets to return. Default 20, maximum 100.",
				},
			}, nil),
			Handler: d.list,
		},
		{
			Name:        "dataset_get",
			Description: datasetGetDescription,
			InputSchema: objectSchema(map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The dataset name, as returned by dataset_list.",
				},
				"version": map[string]any{
					"type":        "integer",
					"description": "Optional. Omit (or 0) for the current version; otherwise the exact version to pin.",
				},
			}, []string{"name"}),
			Handler: d.get,
		},
		{
			Name:        "dataset_put",
			Description: datasetPutDescription,
			InputSchema: objectSchema(map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Stable identity, kebab-case: [A-Za-z0-9] with '-', '_' or '.', at most 63 characters.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Path of the file to upload, RELATIVE to /workspace, e.g. \"prices.csv\". Absolute paths are refused.",
				},
				"if_version": map[string]any{
					"type":        "integer",
					"description": "Compare-and-swap: the version you based this on, or 0 meaning the dataset must not exist yet. Required.",
				},
				"labels": map[string]any{
					"type":                 "object",
					"description":          "Flat string→string labels: what this series is and what it belongs to. Identifiers only: [A-Za-z0-9] with '-', '_', '.', ≤63 chars, ≤32 labels.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"content_type": map[string]any{
					"type":        "string",
					"description": "Optional; defaults to text/csv. row_count is only counted for text/csv.",
				},
				"allow_shrink": map[string]any{
					"type":        "boolean",
					"description": "Set true only when the series genuinely has fewer than half the rows of the current version.",
				},
			}, []string{"name", "path", "if_version"}),
			Handler: d.put,
		},
	}
}

// ---------------------------------------------------------------------------
// dataset_list
// ---------------------------------------------------------------------------

type datasetListArgs struct {
	Selector string `json:"selector"`
	Limit    int    `json:"limit"`
}

func (d *datasetTools) list(ctx context.Context, caller mcpCaller, raw json.RawMessage) (any, error) {
	var args datasetListArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	limit, capped := clampDatasetToolLimit(args.Limit)

	rows, err := d.store.ListDatasets(ctx, caller.Project, strings.TrimSpace(args.Selector), limit)
	if err != nil {
		return nil, err
	}
	out := make([]datasetEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, entryOf(r))
	}
	result := map[string]any{"datasets": out, "count": len(out)}
	// State the cap rather than truncating silently — a model that asked for 500
	// and got 100 must be told, or it will conclude the project holds 100.
	switch {
	case capped:
		result["limit"] = limit
		result["note"] = fmt.Sprintf(
			"limit was capped at %d (the maximum). Narrow the search with a selector to see the rest.", datasetListMaxLimit)
	case len(out) == limit:
		result["limit"] = limit
		result["note"] = fmt.Sprintf(
			"exactly %d datasets were returned, which is the limit — there may be more. Raise limit (maximum %d) or narrow the selector.",
			limit, datasetListMaxLimit)
	}
	return result, nil
}

// clampDatasetToolLimit applies the tool's documented bounds and reports
// whether the caller's own number was reduced. Absent, zero or negative is the
// default; over the maximum is the maximum, and capped is true so the caller
// can say so.
func clampDatasetToolLimit(limit int) (effective int, capped bool) {
	if limit <= 0 {
		return datasetListDefaultLimit, false
	}
	if limit > datasetListMaxLimit {
		return datasetListMaxLimit, true
	}
	return limit, false
}

// ---------------------------------------------------------------------------
// dataset_get
// ---------------------------------------------------------------------------

type datasetGetArgs struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
}

func (d *datasetTools) get(ctx context.Context, caller mcpCaller, raw json.RawMessage) (any, error) {
	var args datasetGetArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if err := agentdb.ValidateDatasetName(name); err != nil {
		return nil, err
	}
	if args.Version < 0 {
		return nil, fmt.Errorf("version must be 1 or greater (or omitted for the current version), got %d", args.Version)
	}

	// The project is an argument of the store call, not a filter applied after:
	// another project's dataset is simply not found, with no existence leak.
	var (
		row *agentdb.Dataset
		err error
	)
	if args.Version == 0 {
		row, err = d.store.CurrentDataset(ctx, caller.Project, name)
	} else {
		row, err = d.store.GetDatasetVersion(ctx, caller.Project, name, args.Version)
	}
	if err != nil {
		if errors.Is(err, agentdb.ErrDatasetNotFound) {
			if args.Version == 0 {
				return nil, fmt.Errorf("no dataset named %q in this project — call dataset_list to see what there is", name)
			}
			return nil, fmt.Errorf("dataset %q has no version %d — call dataset_get without a version for the current one", name, args.Version)
		}
		return nil, err
	}

	// The token pins (project, NAME) and carries no version, so a URL minted
	// while a name is at v3 still resolves after a tick takes it to v7 — the
	// version is in the query string, which the token does not constrain.
	token, exp, err := mintDatasetToken(d.secret, caller.Project, row.Name, 0)
	if err != nil {
		return nil, fmt.Errorf("could not mint a download credential for %q: %w", name, err)
	}

	return datasetHandle{
		Name:        row.Name,
		Version:     row.Version,
		SizeBytes:   row.SizeBytes,
		RowCount:    row.RowCount,
		SHA256:      row.SHA256,
		ContentType: row.ContentType,
		Labels:      labelMap(row.Labels),
		DownloadURL: d.downloadURL(row.Name, row.Version, token),
		ExpiresAt:   exp,
	}, nil
}

// downloadURL builds the URL the agent curls.
//
// It is built on AGENTKIT_SELF_URL and never on AGENTKIT_PUBLIC_BASE_URL: the
// public base is the browser-facing address (permalink.go documents the split)
// and is unreachable from inside a session container nested in DinD.
//
// The path is spelled out literally rather than derived from
// httpapi.DefaultEndpoints, and that is deliberate but load-bearing: agentd's
// ?token= middleware leg matches a HARDCODED literal path too
// (datasetDownloadPath, auth.go), because middleware runs before the mux and
// cannot see httpapi.Endpoints. Both spellings must agree or the agent's curl
// 401s from inside a container with no useful explanation, so
// TestDatasetToolsGetURLMatchesTheMiddlewarePath feeds this URL to that
// function and asserts it is accepted.
//
// The query is assembled by hand rather than with url.Values.Encode(), which
// sorts keys alphabetically and would emit token before version — the shape is
// pinned as `?version=<v>&token=<t>` in the design, and a URL an operator reads
// in a log should look the way the document says.
func (d *datasetTools) downloadURL(name string, version int, token string) string {
	return d.selfURL + "/agent/datasets/" + url.PathEscape(name) + "/download" +
		"?version=" + strconv.Itoa(version) +
		"&" + datasetTokenParam + "=" + url.QueryEscape(token)
}

// ---------------------------------------------------------------------------
// dataset_put — the one that writes
// ---------------------------------------------------------------------------

type datasetPutArgs struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	IfVersion   int               `json:"if_version"`
	Labels      map[string]string `json:"labels"`
	ContentType string            `json:"content_type"`
	AllowShrink bool              `json:"allow_shrink"`
}

func (d *datasetTools) put(ctx context.Context, caller mcpCaller, raw json.RawMessage) (any, error) {
	var args datasetPutArgs
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(args.Name)
	if err := agentdb.ValidateDatasetName(name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Path) == "" {
		return nil, errors.New("path is required: the workspace file to upload, relative to /workspace")
	}
	if args.IfVersion < 0 {
		return nil, fmt.Errorf("if_version must be 0 (the dataset must not exist) or the current version, got %d", args.IfVersion)
	}
	// Validated here as well as in the store so the model gets the specific
	// complaint rather than a wrapped database error.
	if err := agentdb.ValidateLabels(args.Labels); err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	contentType := strings.TrimSpace(args.ContentType)
	if contentType == "" {
		contentType = datasetDefaultContentType
	}

	// THE INVARIANT. Provenance is the caller's, and a caller we cannot identify
	// gets no write at all — never a write with empty provenance. An empty
	// created_by_session is how the embedding application recognises state IT
	// wrote (the trust anchor: "empty provenance ⇒ server-written"), so a row
	// written from inside a container must never be able to look like one.
	// RD4's reasoning, mcpserver.go:105-111, applied to data rather than to the
	// config log.
	if !caller.Identified {
		return nil, fmt.Errorf(
			"nothing was written: the calling session %q could not be identified, so this dataset's provenance is unknown."+
				" A dataset written from a session must carry that session — an unattributed write is reserved for the server itself",
			caller.SessionID)
	}
	if d.sessions == nil {
		return nil, errors.New("dataset_put is not available on this deployment: no session runtime is wired")
	}

	// Pull the file out of the calling session's container. caller.SessionID
	// comes from the token, so a session can only ever read its OWN workspace.
	exec := func(ctx context.Context, cmd []string, opts execenv.ExecOptions) (*execenv.ExecResult, error) {
		return d.sessions.ExecInSession(ctx, agentkit.SessionRef{SessionID: caller.SessionID}, cmd, opts)
	}
	pulled, err := d.pull(ctx, exec, args.Path, contentType, d.maxBytes, d.blobs)
	if err != nil {
		return nil, err
	}

	// From here on the blob exists, so every failure path deletes it. A delete
	// failure is logged and never fatal — O3's orphan sweep is the backstop.
	current, err := d.store.CurrentDataset(ctx, caller.Project, name)
	if err != nil && !errors.Is(err, agentdb.ErrDatasetNotFound) {
		deleteBlobBestEffort(ctx, d.blobs, pulled.BlobPath)
		return nil, err
	}
	if errors.Is(err, agentdb.ErrDatasetNotFound) {
		current = nil
	}

	if refusal := shrinkRefusal(name, current, pulled.RowCount, args.AllowShrink); refusal != nil {
		deleteBlobBestEffort(ctx, d.blobs, pulled.BlobPath)
		return nil, refusal
	}

	// The store's compare-and-swap is the authority, not the read above: two
	// sessions can pass that read and only one can pass the unique index.
	stored, err := d.store.CreateDatasetVersion(ctx, &agentdb.Dataset{
		Project:          caller.Project, // in code, always — never an argument
		Name:             name,
		Labels:           agentdb.LabelSet(args.Labels),
		BlobPath:         pulled.BlobPath,
		SizeBytes:        pulled.SizeBytes,
		RowCount:         pulled.RowCount,
		SHA256:           pulled.SHA256,
		ContentType:      contentType,
		CreatedByWorker:  caller.Worker,    // provenance, from the token
		CreatedBySession: caller.SessionID, // provenance, from the token
	}, args.IfVersion)
	if err != nil {
		deleteBlobBestEffort(ctx, d.blobs, pulled.BlobPath)
		var conflict agentdb.ErrDatasetVersionConflict
		if errors.As(err, &conflict) {
			return nil, versionConflictError(name, args.IfVersion, conflict.Current)
		}
		return nil, err
	}

	// Echo the row as the database holds it, not the arguments we sent: the
	// version and the id are the store's to assign (docs/18 §6).
	return datasetWritten{
		Name:      stored.Name,
		Version:   stored.Version,
		SizeBytes: stored.SizeBytes,
		RowCount:  stored.RowCount,
		SHA256:    stored.SHA256,
	}, nil
}

// shrinkRefusal implements the shrink guard: a replacement with fewer than half
// the current version's rows is refused unless the caller says the shrink is
// real.
//
// It applies ONLY when a current version exists and its row_count is above
// zero. A current version of 0 rows has nothing to shrink from — every count is
// at least half of nothing — and a first write has no predecessor at all.
//
// The comparison is 2*new < current rather than a float ratio: exactly half is
// allowed, and integer arithmetic has no rounding to argue about.
func shrinkRefusal(name string, current *agentdb.Dataset, newRows int, allowShrink bool) error {
	if allowShrink || current == nil || current.RowCount <= 0 {
		return nil
	}
	if 2*newRows >= current.RowCount {
		return nil
	}
	return fmt.Errorf(
		"nothing was written: %q would go from %d rows to %d, which is less than half."+
			" That is what a half-finished fetch looks like. Check the source, or pass allow_shrink: true if the series really did shrink",
		name, current.RowCount, newRows)
}

// versionConflictError names the version the caller should have passed, so the
// model can re-read and retry rather than guess.
func versionConflictError(name string, ifVersion, current int) error {
	if current == 0 {
		return fmt.Errorf(
			"nothing was written: %q does not exist, but if_version was %d. Pass if_version: 0 to create it",
			name, ifVersion)
	}
	return fmt.Errorf(
		"nothing was written: %q is at version %d, not %d — someone else wrote while you were working."+
			" Re-read it with dataset_get(name: %q), merge your work into that version, and retry with if_version: %d",
		name, current, ifVersion, name, current)
}
