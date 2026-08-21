package main

// datasetpull.go — O6a: the workspace pull pipeline. One internal, testable
// function that gets a file out of a running session container safely:
// validate the path, probe, cap, pull, hash, count, store the blob. There is
// no MCP surface here and no CAS — O6b orchestrates both on top of this.
//
// Pattern copied from onArtifactRegistered (go/runner.go:2733-2839): resolve
// the running instance, then Exec argv against it. NOT copied is that path's
// error handling — onArtifactRegistered is best-effort artifact capture and
// only checks `err != nil`, ignoring ExecResult.ExitCode entirely
// (go/runner.go:2824-2828). `cat` on a missing file returns err == nil,
// ExitCode == 1 and empty stdout, so copying that verbatim here would
// silently store a 0-row dataset instead of failing. Every exec below checks
// both.
//
// The exec seam is injected (sessionExec) rather than this function taking a
// Runner or an (ExecutionEnvironment, InstanceID) pair — that is what makes
// the whole pipeline unit-testable with no Docker. O6b binds it to the new
// Runner.ExecInSession.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/execenv"
	"github.com/binocarlos/badcode-agent-orange/extension"
)

// sessionExec is the injected exec seam: run argv (never a shell) against an
// already-resolved session instance. Bound by O6b to Runner.ExecInSession;
// bound by tests to a stub that never touches Docker.
type sessionExec func(ctx context.Context, cmd []string, opts execenv.ExecOptions) (*execenv.ExecResult, error)

// pulledFile is what pullWorkspaceFile produces: the metadata a caller needs
// to record a dataset version. The bytes themselves are never returned — they
// are already durable in the blob store at BlobPath.
type pulledFile struct {
	BlobPath  string // under agentdb.DatasetBlobPrefix
	SizeBytes int64
	RowCount  int
	SHA256    string
}

// pullWorkspaceFile validates relPath, probes that it exists and fits under
// maxBytes, pulls its exact bytes out of the running session via argv execs,
// hashes them, counts rows for text/csv, and stores the result as a fresh
// dataset blob.
//
// Symlinks are explicitly NOT checked. They cannot be decided from a path
// string, and the `test -f` probe below — which follows symlinks and reports
// on the target — is the only defence a file's identity gets.
func pullWorkspaceFile(ctx context.Context, exec sessionExec, relPath, contentType string, maxBytes int64, blobs extension.BlobStore) (*pulledFile, error) {
	abs, err := validateWorkspaceRelPath(relPath)
	if err != nil {
		return nil, fmt.Errorf("pull-workspace-file: %w", err)
	}

	// 1. Probe existence. `test -f` follows symlinks; a directory, a missing
	// path or anything that isn't a regular file exits non-zero.
	res, err := exec(ctx, []string{"test", "-f", abs}, execenv.ExecOptions{})
	if err != nil {
		return nil, fmt.Errorf("pull-workspace-file: probe %q: %w", relPath, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("pull-workspace-file: %q does not exist or is not a regular file", relPath)
	}

	// 2. Probe size BEFORE reading content, so an oversized file never gets
	// pulled into memory in the first place — execAndCollect buffers the
	// whole of stdout unbounded (go/execenv/docker/client.go:238-240), and
	// agentd shares DinD's network namespace and serves every session, so an
	// unbounded pull is a memory DoS from inside a container.
	res, err = exec(ctx, []string{"wc", "-c", abs}, execenv.ExecOptions{})
	if err != nil {
		return nil, fmt.Errorf("pull-workspace-file: size probe %q: %w", relPath, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("pull-workspace-file: size probe %q exited %d", relPath, res.ExitCode)
	}
	probedSize, err := parseWcSize(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("pull-workspace-file: size probe %q: %w", relPath, err)
	}
	if probedSize > maxBytes {
		return nil, fmt.Errorf("pull-workspace-file: %q is %d bytes, over the %d byte cap", relPath, probedSize, maxBytes)
	}

	// 3. Pull the exact bytes. stdcopy.StdCopy is binary-safe, so nothing
	// here may re-encode, trim or line-split the payload — it is hashed and
	// stored exactly as returned.
	res, err = exec(ctx, []string{"cat", abs}, execenv.ExecOptions{})
	if err != nil {
		return nil, fmt.Errorf("pull-workspace-file: read %q: %w", relPath, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("pull-workspace-file: read %q exited %d", relPath, res.ExitCode)
	}
	content := res.Stdout

	// 4. Store the blob under a fresh key. Per write attempt, never derived
	// from the dataset name or a version number — two concurrent pulls of the
	// same dataset must not collide, and the reaper/orphan sweep both
	// enumerate on this prefix over a BlobStore shared with artifacts and
	// snapshots.
	blobKey := agentdb.DatasetBlobPrefix + uuid.NewString()
	if err := blobs.Write(ctx, blobKey, bytes.NewReader(content)); err != nil {
		return nil, fmt.Errorf("pull-workspace-file: store blob: %w", err)
	}

	// 5. Re-check the cap against the bytes actually pulled — the file can
	// grow between the wc -c probe and the cat above (TOCTOU). This check
	// runs after the write, so a failure here rolls the blob back.
	if int64(len(content)) > maxBytes {
		deleteBlobBestEffort(ctx, blobs, blobKey)
		return nil, fmt.Errorf("pull-workspace-file: %q grew to %d bytes mid-pull, over the %d byte cap", relPath, len(content), maxBytes)
	}

	sum := sha256.Sum256(content)

	rowCount := 0
	if isCSVContentType(contentType) {
		rowCount = countCSVRows(content)
	}

	return &pulledFile{
		BlobPath:  blobKey,
		SizeBytes: int64(len(content)),
		RowCount:  rowCount,
		SHA256:    hex.EncodeToString(sum[:]),
	}, nil
}

// deleteBlobBestEffort deletes a blob this function just wrote after a later
// failure. The delete failing is logged, never fatal — the original error is
// what the caller needs to see, and O3's orphan sweep is the backstop for any
// blob a failed delete leaves behind.
func deleteBlobBestEffort(ctx context.Context, blobs extension.BlobStore, key string) {
	if err := blobs.Delete(ctx, key); err != nil {
		log.Printf("[agentd] pull-workspace-file: best-effort delete of orphaned blob %q failed: %v", key, err)
	}
}

// validateWorkspaceRelPath validates relPath against the enumerated
// rejection list and returns the absolute path under /workspace it resolves
// to. It runs before any exec.
//
// Rejected: the empty string; "."; ".."; any path that Clean-resolves outside
// /workspace/ (e.g. "a/../../etc/passwd", "../workspace-evil/x") or to
// /workspace itself; any relPath beginning "/" (absolute paths are rejected
// outright — "relative to /workspace" is the tool contract, so
// "/workspace/prices.csv" is a rejection, not a synonym, and is checked on
// the raw string because Cleaning "/workspace" + an absolute path can still
// land under the /workspace/ prefix); a NUL byte or newline anywhere in the
// string; a path segment equal to ".git".
func validateWorkspaceRelPath(relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.ContainsAny(relPath, "\x00\n") {
		return "", fmt.Errorf("path %q contains a NUL byte or a newline", relPath)
	}
	if strings.HasPrefix(relPath, "/") {
		return "", fmt.Errorf("path %q is absolute; paths are relative to /workspace", relPath)
	}

	abs := path.Clean("/workspace/" + relPath)
	if !strings.HasPrefix(abs, "/workspace/") {
		return "", fmt.Errorf("path %q escapes /workspace", relPath)
	}

	for _, seg := range strings.Split(abs, "/") {
		if seg == ".git" {
			return "", fmt.Errorf("path %q contains a .git segment", relPath)
		}
	}

	return abs, nil
}

// parseWcSize parses `wc -c`'s output ("<n> <path>\n") into the byte count.
// A non-integer first field is an error, never treated as zero.
func parseWcSize(out []byte) (int64, error) {
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty wc -c output %q", out)
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("wc -c output %q: not an integer: %w", out, err)
	}
	return n, nil
}

// isCSVContentType reports whether contentType is text/csv, ignoring any
// parameters ("text/csv; charset=utf-8" counts).
func isCSVContentType(contentType string) bool {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	return strings.TrimSpace(contentType) == "text/csv"
}

// countCSVRows computes RowCount for text/csv content: max(0, N-1), where N
// counts a \n-terminated run plus a final unterminated run when the last
// byte is not \n. So "h\na\nb" and "h\na\nb\n" both give 2, "h\n" gives 0,
// and empty bytes give 0 — never negative. CRLF line endings still count:
// the \r stays in the bytes and is not treated specially.
func countCSVRows(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := bytes.Count(content, []byte("\n"))
	if content[len(content)-1] != '\n' {
		n++
	}
	if n == 0 {
		return 0
	}
	return n - 1
}
