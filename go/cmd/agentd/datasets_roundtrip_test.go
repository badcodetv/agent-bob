//go:build integration

package main

// datasets_roundtrip_test.go — O9 of design/2026-08-20-agent-wolf.md.
//
// The ONE test in the tree that drives the dataset atom against a REAL
// container and a REAL Postgres. Everything beneath it is unit-tested with
// fakes: O5's routes against a fake store, O6a's pull against a stub exec,
// O6b's tools against both. That leaves four failure modes that no fake can
// reproduce, and this file exists for exactly those four:
//
//	1. a download_url built on the wrong base (AGENTKIT_PUBLIC_BASE_URL rather
//	   than AGENTKIT_SELF_URL) is unreachable from inside a container;
//	2. a shell-quoting bug in the Exec+cat pull mangles bytes (there is no
//	   shell — the pull is argv — and this proves it end to end);
//	3. a mis-scoped ?token= either fails to authenticate the agent's curl or
//	   authenticates it for the wrong dataset;
//	4. a byte-mangling read anywhere between /workspace, the blob store, the
//	   HTTP response and the file curl writes back inside the container.
//
// It is behind `//go:build integration` and it is NOT in go/systemtest: the
// three dataset tools live in `package main`, which Go cannot import, and the
// systemtest rig is a library rig over agentkittest.MemStore with no
// agentdb.Store, no Postgres, no httpapi mux — and a TestMain that shells out
// to `docker build ../../sandbox` and os.Exit(1)s rather than skipping.
//
// Run it:
//
//	AGENTKIT_TEST_POSTGRES_URL=postgres://... \
//	  go test -tags integration ./cmd/agentd/... -run TestDatasetRoundTripLive -count=1 -v -timeout 600s
//
// ⚠️ Without -tags integration this file is not compiled, -run matches nothing
// and the command exits 0. That vacuous pass is the thing this ticket exists to
// prevent, so the Validation says so and so does this comment.
//
// ⚠️ A download_url carries a bearer credential. Nothing in this file logs one:
// every failure message that has to name a URL runs it through redactURL first.
//
// PREREQUISITES, and the three ways this test can SKIP. The ticket names two —
// AGENTKIT_TEST_POSTGRES_URL unset, and Docker unreachable. There is a third it
// does not, and it is stated here because a silent dependency is worse than a
// documented one: the container image. The default (curlimages/curl:latest) is
// PULLED FROM DOCKER HUB if it is not already in the local Docker's image
// store, so on a machine with Docker and Postgres but no registry egress this
// test skips rather than runs. On such a machine, pre-pull the image once, or
// point AGENTKIT_TEST_DATASET_IMAGE at any locally present image carrying
// curl, cat, wc and test (the Wolf/core installation images all do). Every one
// of the three skips names what was missing; none of them passes vacuously.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/google/uuid"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	agentkit "github.com/binocarlos/badcode-agent-orange"
	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/execenv"
	"github.com/binocarlos/badcode-agent-orange/extension"
	"github.com/binocarlos/badcode-agent-orange/extension/filesblob"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
)

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const (
	// roundTripImageVar overrides the container image. The default below is a
	// tiny public image that carries curl plus busybox's cat/wc/test — the four
	// binaries the pull pipeline and the agent's download actually use. Any
	// image with those four works; the Wolf/core installation images do too.
	roundTripImageVar = "AGENTKIT_TEST_DATASET_IMAGE"
	// defaultRoundTripImage is pulled if it is not already present locally.
	defaultRoundTripImage = "curlimages/curl:latest"

	// roundTripLabel marks every container this file creates, so a human (or a
	// later run) can find and remove a leak with
	//	docker ps -a --filter label=agentkit.dataset-roundtrip=true
	roundTripLabel      = "agentkit.dataset-roundtrip"
	roundTripLabelValue = "true"

	// pingPath is the reachability pre-flight's route. It is mounted on the
	// OUTER mux, outside apiAuthMiddleware, exactly as agentd mounts /health —
	// so a 401 here would mean the mux is wrong rather than the credential.
	pingPath = "/__ping"
)

// ---------------------------------------------------------------------------
// TestDatasetRoundTripLive
// ---------------------------------------------------------------------------

// TestDatasetRoundTripLive is the ticket's single test function. Its phases run
// in one t.Run-free body on purpose: each depends on the container, the store
// and the versions the previous one left behind, and a subtest that skipped
// halfway would leave the assertions after it silently unexecuted.
func TestDatasetRoundTripLive(t *testing.T) {
	// ── Two guards, because the two prerequisites are independently absent ──
	//
	// Deliberately NOT testing.Short(): that is a flag check, not a probe, and
	// it does not skip when Docker is simply not installed.
	pgURL := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if pgURL == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL is not set — this test needs a real Postgres (see the plan's § \"The throwaway Postgres\")")
	}
	docker, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Skipf("Docker is not reachable (client: %v) — this test needs a real container", err)
	}
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = docker.Ping(pingCtx)
	cancelPing()
	if err != nil {
		t.Skipf("Docker is not reachable (ping: %v) — this test needs a real container", err)
	}
	defer docker.Close() //nolint:errcheck

	ctx := context.Background()

	// ── The store: real Postgres, a project of this run's own ────────────────
	store, err := agentdb.Open(pgURL)
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	project := "o9-" + uuid.NewString()
	t.Cleanup(func() {
		// Rows first-registered means rows last-deleted: the container and the
		// blobs go before this. A teardown failure is an error, never silence.
		if err := store.DB().Exec("DELETE FROM datasets WHERE project = ?", project).Error; err != nil {
			t.Errorf("teardown: delete dataset rows for project %q: %v", project, err)
		}
		var left int64
		if err := store.DB().Raw("SELECT COUNT(*) FROM datasets WHERE project = ?", project).Scan(&left).Error; err != nil {
			t.Errorf("teardown: count dataset rows for project %q: %v", project, err)
		} else if left != 0 {
			t.Errorf("teardown: %d dataset rows survived for project %q", left, project)
		}
	})

	// ── The blob plane: a real filesystem BlobStore, the same one agentd uses
	// by default (backends.go's fs branch), rooted in this test's temp dir ────
	blobs := filesblob.NewBlobStore(t.TempDir())
	t.Cleanup(func() { assertBlobsRemoved(t, blobs) })

	// ── The listener: O5's four routes behind the REAL apiAuthMiddleware ─────
	//
	// The middleware is the point. The ?token= leg is the one place in the tree
	// where a credential arrives in a URL, and a test that called the bare
	// handler would prove nothing about the route an agent actually curls.
	secret := []byte("o9-roundtrip-secret-" + uuid.NewString())
	api, err := httpapi.New(httpapi.Config{
		Runner:       &stubRunner{},        // required by New; unused by these routes
		Store:        newFakeRouterStore(), // ditto
		Identity:     identityFromRequest,  // the SAME reader main() wires
		Datasets:     store,                // the real store
		DatasetBlobs: blobs,                // the real byte plane
	})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	spy := &downloadSpy{}
	root := http.NewServeMux()
	root.HandleFunc("GET "+pingPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})
	// No project API keys are configured and the secret is non-empty, so
	// dev-open mode is OFF: an unauthenticated request is a 401, which is what
	// makes the ?token= leg's success meaningful.
	root.Handle("/", spy.wrap(apiAuthMiddleware(secret, &projectKeyIndex{}, api.Mux())))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	selfURL := srv.URL // http://127.0.0.1:<port> — reachable from --network host

	// ── The container ───────────────────────────────────────────────────────
	image := os.Getenv(roundTripImageVar)
	if strings.TrimSpace(image) == "" {
		image = defaultRoundTripImage
	}
	box := startRoundTripContainer(t, docker, image)

	// The tools' exec seam, bound to that container. It asserts the session id
	// it was called with, because dataset_put takes the workspace it reads from
	// the TOKEN's session and never from an argument — a regression there would
	// let one session read another's files.
	const sessionID = "sess-o9-roundtrip"
	execer := &containerExecer{t: t, box: box, wantSession: sessionID}

	tools := newDatasetTools(store, execer, blobs, secret, selfURL, defaultDatasetMaxBytes)
	caller := mcpCaller{Project: project, SessionID: sessionID, Worker: "o9-researcher", Identified: true}

	// ── 1. THE REACHABILITY PRE-FLIGHT, before anything else ────────────────
	//
	// An unreachable listener otherwise surfaces as a baffling byte-comparison
	// failure nine lines later.
	pingURL := selfURL + pingPath
	res, err := box.exec(ctx, []string{"curl", "-fsS", "-o", "/dev/null", "-w", "%{http_code}", pingURL})
	if err != nil {
		t.Fatalf("pre-flight: could not exec curl in the container for %s: %v", pingURL, err)
	}
	if code := strings.TrimSpace(string(res.Stdout)); res.ExitCode != 0 || code != "200" {
		t.Fatalf("pre-flight: the container cannot reach the test listener at %s — curl exited %d with http_code %q, stderr %q."+
			" Nothing below this line can pass; the container's network namespace does not see the listener",
			pingURL, res.ExitCode, code, strings.TrimSpace(string(res.Stderr)))
	}

	// ── 2. BYTE FIDELITY ────────────────────────────────────────────────────
	//
	// NUL, a non-ASCII run, an invalid UTF-8 byte, and both LF and CRLF. Any
	// layer that re-encoded, trimmed or line-split would change these bytes.
	payload := []byte("alpha\r\nbeta\n\x00\xffgamma £ é 中\r\n\ndelta")
	box.writeFile(t, "binary.bin", payload)

	binaryName := "o9-binary"
	written := putDataset(t, ctx, tools, caller, map[string]any{
		"name": binaryName, "path": "binary.bin", "if_version": 0,
		"content_type": "application/octet-stream",
		"labels":       map[string]string{"kind": "o9-roundtrip"},
	})
	wantSum := sha256.Sum256(payload)
	if written.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("dataset_put sha256 = %s, want %s — the Exec+cat pull did not preserve the bytes",
			written.SHA256, hex.EncodeToString(wantSum[:]))
	}
	if written.SizeBytes != int64(len(payload)) {
		t.Fatalf("dataset_put size_bytes = %d, want %d", written.SizeBytes, len(payload))
	}
	if written.Version != 1 {
		t.Fatalf("dataset_put version = %d, want 1", written.Version)
	}

	handle := getDataset(t, ctx, tools, caller, map[string]any{"name": binaryName})
	if handle.SHA256 != written.SHA256 {
		t.Fatalf("dataset_get sha256 = %s, want %s", handle.SHA256, written.SHA256)
	}
	// THE VERBATIM RULE. The URL is built on AGENTKIT_SELF_URL (the listener),
	// never on the public base URL, and the curl below is handed the exact
	// string the tool returned — no host, scheme, port or query is rewritten.
	// assertURLUnrewritten proves it from the SERVER's side afterwards.
	if !strings.HasPrefix(handle.DownloadURL, selfURL+"/agent/datasets/") {
		t.Fatalf("download_url is not built on selfURL (%s): got %s", selfURL, redactURL(handle.DownloadURL))
	}

	// The agent's curl: verbatim URL, no -H of any kind. -f so a non-200 is a
	// non-zero exit rather than an HTML error page written to the file.
	res, err = box.exec(ctx, []string{"curl", "-fsS", "-o", "/workspace/downloaded.bin", handle.DownloadURL})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("in-container download of %s failed: err=%v exit=%d stderr=%q",
			redactURL(handle.DownloadURL), err, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	got := box.readFile(t, "/workspace/downloaded.bin")
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded bytes differ from what was written:\n got %q (%d bytes)\nwant %q (%d bytes)",
			got, len(got), payload, len(payload))
	}
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != handle.SHA256 {
		t.Fatalf("downloaded sha256 = %s, want the tool result's %s", hex.EncodeToString(gotSum[:]), handle.SHA256)
	}
	spy.assertUnauthenticated(t)
	spy.assertURLUnrewritten(t, handle.DownloadURL)

	// ── 3. THE CANONICAL CSV ────────────────────────────────────────────────
	//
	// header `timestamp,value`, RFC3339 UTC, ascending, LF, no trailing blank
	// line. row_count is max(0, N-1) and must equal the data rows written.
	const seriesName = "o9-series"
	csv, dataRows := canonicalCSV(10)
	box.writeFile(t, "series.csv", csv)
	series := putDataset(t, ctx, tools, caller, map[string]any{
		"name": seriesName, "path": "series.csv", "if_version": 0,
		"content_type": "text/csv",
	})
	if series.RowCount != dataRows {
		t.Fatalf("row_count = %d, want %d (the data rows written, header excluded)", series.RowCount, dataRows)
	}
	if series.Version != 1 {
		t.Fatalf("series version = %d, want 1", series.Version)
	}

	// ── 4. CAS: a stale if_version is refused and changes NOTHING ───────────
	_, err = tools.put(ctx, caller, mustArgs(t, map[string]any{
		"name": seriesName, "path": "series.csv", "if_version": 0, "content_type": "text/csv",
	}))
	if err == nil {
		t.Fatalf("a stale if_version=0 against version 1 was accepted; CAS is not enforced")
	}
	if !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("stale-CAS error does not name the current version: %v", err)
	}
	after := getDataset(t, ctx, tools, caller, map[string]any{"name": seriesName})
	if after.Version != series.Version || after.SizeBytes != series.SizeBytes || after.SHA256 != series.SHA256 {
		t.Fatalf("a refused write changed the dataset: version %d→%d, size %d→%d, sha %s→%s",
			series.Version, after.Version, series.SizeBytes, after.SizeBytes, series.SHA256, after.SHA256)
	}

	// ── 5. THE SHRINK GUARD ─────────────────────────────────────────────────
	shrunk, shrunkRows := canonicalCSV(4) // 4 < 10/2 → refused without allow_shrink
	box.writeFile(t, "shrunk.csv", shrunk)
	_, err = tools.put(ctx, caller, mustArgs(t, map[string]any{
		"name": seriesName, "path": "shrunk.csv", "if_version": 1, "content_type": "text/csv",
	}))
	if err == nil {
		t.Fatalf("a %d-row replacement of a %d-row dataset was accepted without allow_shrink", shrunkRows, dataRows)
	}
	for _, want := range []string{fmt.Sprintf("%d rows", dataRows), fmt.Sprintf("to %d", shrunkRows)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("shrink refusal does not name both counts (missing %q): %v", want, err)
		}
	}
	allowed := putDataset(t, ctx, tools, caller, map[string]any{
		"name": seriesName, "path": "shrunk.csv", "if_version": 1,
		"content_type": "text/csv", "allow_shrink": true,
	})
	if allowed.Version != 2 || allowed.RowCount != shrunkRows {
		t.Fatalf("allow_shrink write = version %d row_count %d, want version 2 row_count %d",
			allowed.Version, allowed.RowCount, shrunkRows)
	}

	// ── 6. A TOKEN FOR A IS NOT A TOKEN FOR B — 404, never 403 ──────────────
	//
	// O5's non-oracle rule: a token holder probing names must not be able to
	// tell "not yours" from "not there".
	seriesHandle := getDataset(t, ctx, tools, caller, map[string]any{"name": seriesName})

	// The minted URL pins ?version=2, and o9-binary only ever reached version 1
	// — so swapping the name alone would be answered 404 by the VERSION lookup
	// and the scope check would never be consulted. (That is not hypothetical:
	// it is how the first cut of this test passed with httpapi's DatasetScope
	// check deleted outright.) Drop the version so the request asks for the
	// other dataset's CURRENT version, which exists, and the only thing left
	// that can produce a 404 is the token's (project, name) pin.
	currentURL := dropVersionParam(t, seriesHandle.DownloadURL)

	// Control, first: version-less, the token still fetches its OWN dataset.
	// Without this a 404 below could come from dropping the parameter rather
	// than from the pin, and the probe would be vacuous in the other direction.
	if code, _ := getStatus(t, currentURL); code != http.StatusOK {
		t.Fatalf("the version-less form of %q's own download URL returned %d, want 200 — the cross-dataset probe below would be vacuous", seriesName, code)
	}

	crossURL := swapDatasetName(t, currentURL, seriesName, binaryName)
	code, body := getStatus(t, crossURL)
	if code != http.StatusNotFound {
		t.Fatalf("a token minted for %q downloaded %q: status = %d, want 404", seriesName, binaryName, code)
	}
	if bytes.Contains(body, payload) {
		t.Fatalf("a cross-dataset token returned %q's bytes", binaryName)
	}
	spy.assertUnauthenticated(t)

	// ── 7. Cleanup is asserted, not hoped for ───────────────────────────────
	//
	// The container is destroyed by its own t.Cleanup (registered in
	// startRoundTripContainer and verified there by inspect); the blobs are
	// removed and re-listed by assertBlobsRemoved; the rows are deleted and
	// re-counted above. Three successful writes means exactly three blobs — a
	// refused write's blob is rolled back by dataset_put, and a fourth here
	// would be a leak that O3's orphan sweep would later have to find.
	keys, err := blobs.List(ctx, agentdb.DatasetBlobPrefix)
	if err != nil {
		t.Fatalf("list dataset blobs: %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("blob store holds %d dataset blobs, want 3 (one per successful write; refused writes must roll theirs back): %v",
			len(keys), keys)
	}
}

// ---------------------------------------------------------------------------
// Tool call helpers
// ---------------------------------------------------------------------------

func mustArgs(t *testing.T, args map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal tool arguments: %v", err)
	}
	return raw
}

func putDataset(t *testing.T, ctx context.Context, tools *datasetTools, caller mcpCaller, args map[string]any) datasetWritten {
	t.Helper()
	out, err := tools.put(ctx, caller, mustArgs(t, args))
	if err != nil {
		t.Fatalf("dataset_put(%v): %v", args["name"], err)
	}
	w, ok := out.(datasetWritten)
	if !ok {
		t.Fatalf("dataset_put returned %T, want datasetWritten", out)
	}
	return w
}

func getDataset(t *testing.T, ctx context.Context, tools *datasetTools, caller mcpCaller, args map[string]any) datasetHandle {
	t.Helper()
	out, err := tools.get(ctx, caller, mustArgs(t, args))
	if err != nil {
		t.Fatalf("dataset_get(%v): %v", args["name"], err)
	}
	h, ok := out.(datasetHandle)
	if !ok {
		t.Fatalf("dataset_get returned %T, want datasetHandle", out)
	}
	return h
}

// canonicalCSV builds § "The canonical dataset CSV": header `timestamp,value`,
// RFC3339 timestamps in UTC, ascending, LF endings, no trailing blank line. It
// returns the bytes and the number of DATA rows, which is what row_count must
// equal.
func canonicalCSV(rows int) ([]byte, int) {
	var b strings.Builder
	b.WriteString("timestamp,value\n")
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&b, "%s,%.2f\n", day.AddDate(0, 0, i).Format(time.RFC3339), 100+float64(i))
	}
	return []byte(b.String()), rows
}

// ---------------------------------------------------------------------------
// The listener's spy
// ---------------------------------------------------------------------------

// downloadSpy records every request that reached the download route, so two
// criteria can be asserted from the SERVER's side rather than from the client's
// intentions: that the agent's curl carried no X-API-Key and no Authorization,
// and that the URL it used was the minted one, unrewritten.
type downloadSpy struct {
	mu   sync.Mutex
	seen []recordedRequest
}

type recordedRequest struct {
	host, requestURI string
	headers          http.Header
}

func (s *downloadSpy) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			s.mu.Lock()
			s.seen = append(s.seen, recordedRequest{
				host: r.Host, requestURI: r.URL.RequestURI(), headers: r.Header.Clone(),
			})
			s.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

// assertUnauthenticated is the criterion "the download carries no X-API-Key and
// no Authorization header": the ?token= leg is the only path in the tree that
// authenticates without either, and if the request had carried one the middleware
// would have taken a different branch entirely.
func (s *downloadSpy) assertUnauthenticated(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seen) == 0 {
		t.Fatalf("no request reached the download route — the assertion below would be vacuous")
	}
	for i, r := range s.seen {
		if v := r.headers.Get(apiKeyHeader); v != "" {
			t.Fatalf("download request %d carried %s — the ?token= leg was not what authenticated it", i, apiKeyHeader)
		}
		if r.headers.Get("Authorization") != "" {
			t.Fatalf("download request %d carried an Authorization header — the ?token= leg was not what authenticated it", i)
		}
	}
}

// assertURLUnrewritten proves the container's curl used download_url verbatim:
// the host and the full request URI the server saw must equal the minted URL's,
// query string and parameter order included.
func (s *downloadSpy) assertURLUnrewritten(t *testing.T, minted string) {
	t.Helper()
	u, err := url.Parse(minted)
	if err != nil {
		t.Fatalf("parse download_url: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.seen {
		if r.host == u.Host && r.requestURI == u.RequestURI() {
			return
		}
	}
	t.Fatalf("no request arrived with the minted download_url's host (%s) and request URI — the curl rewrote it", u.Host)
}

// redactURL renders a download URL for a failure message with its credential
// removed. Nothing in this file prints a token.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable url>"
	}
	q := u.Query()
	if q.Get(datasetTokenParam) != "" {
		q.Set(datasetTokenParam, "<redacted>")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// swapDatasetName rewrites the dataset NAME in a minted download URL, leaving
// the token untouched — the "a token for A must not fetch B" probe. Give it a
// URL that has been through dropVersionParam: a version pin that the other
// dataset does not have would be answered 404 by the version lookup, hiding
// whether the scope check ran at all.
func swapDatasetName(t *testing.T, minted, from, to string) string {
	t.Helper()
	want := "/agent/datasets/" + from + "/download"
	if !strings.Contains(minted, want) {
		t.Fatalf("minted URL does not contain %q: %s", want, redactURL(minted))
	}
	if u, err := url.Parse(minted); err == nil && u.Query().Get("version") != "" {
		t.Fatalf("swapDatasetName was given a version-pinned URL (%s); a version the target lacks would produce the 404 on its own", redactURL(minted))
	}
	return strings.Replace(minted, want, "/agent/datasets/"+to+"/download", 1)
}

// dropVersionParam removes ?version= from a minted download URL, leaving the
// token and every other parameter untouched. Absent, the route resolves the
// dataset's current version (httpapi/datasets.go:292-296).
func dropVersionParam(t *testing.T, minted string) string {
	t.Helper()
	u, err := url.Parse(minted)
	if err != nil {
		t.Fatalf("parse minted URL %s: %v", redactURL(minted), err)
	}
	q := u.Query()
	if q.Get("version") == "" {
		t.Fatalf("minted URL carries no ?version= to drop: %s", redactURL(minted))
	}
	q.Del("version")
	u.RawQuery = q.Encode()
	return u.String()
}

// getStatus issues a bare GET — no X-API-Key, no Authorization — and returns
// the status and the body.
func getStatus(t *testing.T, raw string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", redactURL(raw), err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", redactURL(raw), err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", redactURL(raw), err)
	}
	return resp.StatusCode, body
}

// ---------------------------------------------------------------------------
// Blob teardown
// ---------------------------------------------------------------------------

// assertBlobsRemoved deletes every dataset blob this test wrote and proves the
// prefix is empty afterwards. A failure is loud: an unremoved blob is exactly
// the orphan O3's sweep exists to find, and a test that leaks one is testing
// the sweep by accident.
func assertBlobsRemoved(t *testing.T, blobs extension.BlobStore) {
	t.Helper()
	ctx := context.Background()
	keys, err := blobs.List(ctx, agentdb.DatasetBlobPrefix)
	if err != nil {
		t.Errorf("teardown: list dataset blobs: %v", err)
		return
	}
	for _, k := range keys {
		if err := blobs.Delete(ctx, k); err != nil {
			t.Errorf("teardown: delete blob %q: %v", k, err)
		}
	}
	left, err := blobs.List(ctx, agentdb.DatasetBlobPrefix)
	if err != nil {
		t.Errorf("teardown: re-list dataset blobs: %v", err)
		return
	}
	if len(left) != 0 {
		t.Errorf("teardown: %d dataset blobs survived: %v", len(left), left)
	}
}

// ---------------------------------------------------------------------------
// The container
//
// Copied from go/systemtest/testenv_test.go's real-Docker environment, not
// imported: that file is a _test.go in another package. What is copied is the
// shape — host Docker socket, --network host, labels for cleanup, exec via
// stdcopy — and what is dropped is everything about sessions, health probes and
// the ExecutionEnvironment interface, none of which this test needs. The
// production execenv/docker.Socket adapter is not usable here for the reason
// testenv_test.go states: it addresses containers by DNS name on a shared
// network, which a `go test` binary on the host cannot resolve, and its
// Provision insists on a healthy in-image agent this image does not run.
// ---------------------------------------------------------------------------

type roundTripBox struct {
	docker      *dockerclient.Client
	containerID string
}

func startRoundTripContainer(t *testing.T, docker *dockerclient.Client, image string) *roundTripBox {
	t.Helper()
	ctx := context.Background()

	if !imagePresent(ctx, docker, image) {
		rc, err := docker.ImagePull(ctx, image, dockertypes.ImagePullOptions{})
		if err != nil {
			t.Skipf("image %q is not present locally and could not be pulled (%v) — set %s to an image carrying curl, cat, wc and test",
				image, err, roundTripImageVar)
		}
		_, _ = io.Copy(io.Discard, rc) // the pull is not complete until the stream is drained
		_ = rc.Close()
	}

	resp, err := docker.ContainerCreate(ctx,
		&dockercontainer.Config{
			Image: image,
			// argv, no shell. An explicit entrypoint override because an
			// installation image may carry one; house rule 4 keeps CMD out of
			// installation Dockerfiles but not out of every possible image.
			Entrypoint: []string{"sleep"},
			Cmd:        []string{"900"},
			// root, so /workspace is writable whatever the image's default user
			// is (curlimages/curl runs as uid 100).
			User: "0:0",
			Labels: map[string]string{
				roundTripLabel:          roundTripLabelValue,
				"agentkit.test":         "TestDatasetRoundTripLive",
				"agentkit.test-started": time.Now().UTC().Format(time.RFC3339),
			},
		},
		// --network host: the container shares the test process's network
		// namespace, so it reaches httptest's 127.0.0.1 listener. This is the
		// straightforward configuration the ticket names; a DinD container
		// addressed at its bridge gateway would do as well.
		&dockercontainer.HostConfig{NetworkMode: "host", AutoRemove: false},
		(*dockernetwork.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "")
	if err != nil {
		t.Fatalf("create container from %q: %v", image, err)
	}
	box := &roundTripBox{docker: docker, containerID: resp.ID}

	// Registered BEFORE Start, so a failed start still tears the container down.
	t.Cleanup(func() { box.destroy(t) })

	if err := docker.ContainerStart(ctx, resp.ID, dockertypes.ContainerStartOptions{}); err != nil {
		t.Fatalf("start container %s: %v", resp.ID[:12], err)
	}

	// /workspace is the pull pipeline's root and this image has no such
	// directory; CopyToContainer refuses a destination that does not exist.
	res, err := box.exec(ctx, []string{"mkdir", "-p", "/workspace"})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("mkdir /workspace: err=%v exit=%d stderr=%q", err, res.ExitCode, res.Stderr)
	}
	// Prove the four binaries the pipeline needs are actually there, rather
	// than discovering their absence as a mangled byte comparison later.
	for _, bin := range []string{"curl", "cat", "wc", "test"} {
		if r, err := box.exec(ctx, []string{"/usr/bin/env", "which", bin}); err != nil || r.ExitCode != 0 {
			t.Skipf("image %q does not provide %q — set %s to an image carrying curl, cat, wc and test",
				image, bin, roundTripImageVar)
		}
	}
	return box
}

func imagePresent(ctx context.Context, docker *dockerclient.Client, image string) bool {
	if _, _, err := docker.ImageInspectWithRaw(ctx, image); err != nil {
		return false
	}
	return true
}

// exec runs argv inside the container and collects the streams. There is no
// shell anywhere in this file, which is the point: the pull pipeline hands
// attacker-supplied paths to exec as argv, and a test that went through `sh -c`
// would be exercising a transport the product does not use.
func (b *roundTripBox) exec(ctx context.Context, cmd []string) (*execenv.ExecResult, error) {
	create, err := b.docker.ContainerExecCreate(ctx, b.containerID, dockertypes.ExecConfig{
		AttachStdout: true, AttachStderr: true, Cmd: cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create %v: %w", cmd, err)
	}
	attach, err := b.docker.ContainerExecAttach(ctx, create.ID, dockertypes.ExecStartCheck{})
	if err != nil {
		return nil, fmt.Errorf("exec attach %v: %w", cmd, err)
	}
	defer attach.Close()

	var stdout, stderr bytes.Buffer
	// stdcopy is binary-safe: it preserves NULs and invalid UTF-8, which is
	// what makes `cat` a legitimate transport for dataset bytes.
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attach.Reader); err != nil && err != io.EOF {
		return nil, fmt.Errorf("exec read %v: %w", cmd, err)
	}
	inspect, err := b.docker.ContainerExecInspect(ctx, create.ID)
	if err != nil {
		return nil, fmt.Errorf("exec inspect %v: %w", cmd, err)
	}
	return &execenv.ExecResult{ExitCode: inspect.ExitCode, Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
}

// writeFile puts bytes at /workspace/<name> through the Docker copy API — a
// tar stream, not a shell heredoc, so the payload's NULs and CRLFs reach the
// container exactly as written. A shell would be the very bug this test hunts.
func (b *roundTripBox) writeFile(t *testing.T, name string, content []byte) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), ModTime: time.Now(),
	}); err != nil {
		t.Fatalf("tar header for %q: %v", name, err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write %q: %v", name, err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := b.docker.CopyToContainer(context.Background(), b.containerID, "/workspace", &buf,
		dockertypes.CopyToContainerOptions{}); err != nil {
		t.Fatalf("copy %q into the container: %v", name, err)
	}
}

// readFile cats a file back out. Used only to read what curl wrote INSIDE the
// container, which is the byte-fidelity criterion's other end.
func (b *roundTripBox) readFile(t *testing.T, abs string) []byte {
	t.Helper()
	res, err := b.exec(context.Background(), []string{"cat", abs})
	if err != nil {
		t.Fatalf("cat %s: %v", abs, err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("cat %s exited %d: %s", abs, res.ExitCode, res.Stderr)
	}
	return res.Stdout
}

// destroy removes the container and PROVES it is gone. A leaked container holds
// a host port, and the port pool is the hard ceiling on concurrent sessions —
// so a teardown failure is reported, never swallowed.
func (b *roundTripBox) destroy(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	timeout := 3
	_ = b.docker.ContainerStop(ctx, b.containerID, dockercontainer.StopOptions{Timeout: &timeout})
	if err := b.docker.ContainerRemove(ctx, b.containerID, dockertypes.ContainerRemoveOptions{Force: true}); err != nil {
		t.Errorf("teardown: remove container %s (label %s=%s) failed: %v",
			b.containerID, roundTripLabel, roundTripLabelValue, err)
		return
	}
	if _, err := b.docker.ContainerInspect(ctx, b.containerID); err == nil {
		t.Errorf("teardown: container %s still exists after remove (label %s=%s)",
			b.containerID, roundTripLabel, roundTripLabelValue)
	} else if !dockerclient.IsErrNotFound(err) {
		t.Errorf("teardown: could not confirm container %s is gone: %v", b.containerID, err)
	}
}

// ---------------------------------------------------------------------------
// The tools' exec seam, bound to the real container
// ---------------------------------------------------------------------------

// containerExecer is sessionExecer over one real container. It asserts the
// session id it is asked for, because dataset_put resolves the workspace from
// the TOKEN's session and never from a tool argument — a regression there would
// let one session read another's files, and a seam that ignored the ref would
// hide it.
type containerExecer struct {
	t           *testing.T
	box         *roundTripBox
	wantSession string
}

var _ sessionExecer = (*containerExecer)(nil)

func (c *containerExecer) ExecInSession(ctx context.Context, ref agentkit.SessionRef, cmd []string,
	_ execenv.ExecOptions) (*execenv.ExecResult, error) {
	c.t.Helper()
	if ref.SessionID != c.wantSession {
		c.t.Fatalf("dataset_put execed into session %q, want the calling session %q", ref.SessionID, c.wantSession)
	}
	return c.box.exec(ctx, cmd)
}
