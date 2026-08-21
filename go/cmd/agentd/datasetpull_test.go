package main

// datasetpull_test.go — O6a. Every test function name begins with
// TestPullWorkspaceFile so the Validation filter (`-run 'TestPullWorkspaceFile'`)
// and this file agree. No Docker: the exec seam is a fake, per the injected
// sessionExec shape datasetpull.go documents.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/agentkittest"
	"github.com/binocarlos/badcode-agent-orange/execenv"
	"github.com/binocarlos/badcode-agent-orange/extension"
)

// fakeSession is a scripted, Docker-free stand-in for a running session's
// exec surface. It dispatches on cmd[0] ("test", "wc", "cat") and records
// every argv it was asked to run, so tests can assert both what came back
// and what was actually invoked.
type fakeSession struct {
	testFExit int
	testFErr  error

	wcOut  []byte
	wcExit int
	wcErr  error

	catOut  []byte
	catExit int
	catErr  error

	calls [][]string
}

func (f *fakeSession) exec(_ context.Context, cmd []string, _ execenv.ExecOptions) (*execenv.ExecResult, error) {
	cp := append([]string(nil), cmd...)
	f.calls = append(f.calls, cp)
	if len(cmd) == 0 {
		return nil, fmt.Errorf("fakeSession: empty argv")
	}
	switch cmd[0] {
	case "test":
		if f.testFErr != nil {
			return nil, f.testFErr
		}
		return &execenv.ExecResult{ExitCode: f.testFExit}, nil
	case "wc":
		if f.wcErr != nil {
			return nil, f.wcErr
		}
		return &execenv.ExecResult{ExitCode: f.wcExit, Stdout: f.wcOut}, nil
	case "cat":
		if f.catErr != nil {
			return nil, f.catErr
		}
		return &execenv.ExecResult{ExitCode: f.catExit, Stdout: f.catOut}, nil
	default:
		return nil, fmt.Errorf("fakeSession: unexpected command %v", cmd)
	}
}

// happyFakeSession returns a fakeSession scripted for a clean pull of
// content, with wc -c reporting the true size.
func happyFakeSession(content []byte) *fakeSession {
	return &fakeSession{
		testFExit: 0,
		wcOut:     []byte(fmt.Sprintf("%d /workspace/x\n", len(content))),
		wcExit:    0,
		catOut:    content,
		catExit:   0,
	}
}

// failDeleteBlobs wraps a BlobStore so every Delete fails, for the
// delete-on-failure test.
type failDeleteBlobs struct {
	extension.BlobStore
}

func (failDeleteBlobs) Delete(context.Context, string) error {
	return fmt.Errorf("delete always fails (test)")
}

// --- path rejection --------------------------------------------------------

func TestPullWorkspaceFile_PathRejection(t *testing.T) {
	cases := []struct {
		name string
		rel  string
	}{
		{"empty string", ""},
		{"dot", "."},
		{"dotdot", ".."},
		{"climb through workspace", "a/../../etc/passwd"},
		{"climb into workspace-evil sibling", "../workspace-evil/x"},
		{"absolute path", "/workspace/prices.csv"},
		{"absolute path outside workspace", "/etc/passwd"},
		{"NUL byte", "prices\x00.csv"},
		{"newline", "prices\n.csv"},
		{"dot-git segment", ".git/config"},
		{"dot-git segment nested", "sub/.git/HEAD"},
		{"workspace-evil prefix collision", "../workspace-evil/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSession{}
			blobs := agentkittest.NewMemBlobs()
			_, err := pullWorkspaceFile(context.Background(), f.exec, tc.rel, "text/csv", 1<<20, blobs)
			if err == nil {
				t.Fatalf("pullWorkspaceFile(%q): want error, got nil", tc.rel)
			}
			if len(f.calls) != 0 {
				t.Fatalf("pullWorkspaceFile(%q): rejected path still exec'd: %v", tc.rel, f.calls)
			}
		})
	}
}

func TestPullWorkspaceFile_PathAcceptance(t *testing.T) {
	// A well-formed relative path is NOT rejected by hardening — it reaches
	// the exec stage (and succeeds end to end), proving the checks above
	// reject their targets specifically and not everything.
	content := []byte("timestamp,value\n2026-08-19T00:00:00Z,1\n")
	f := happyFakeSession(content)
	blobs := agentkittest.NewMemBlobs()
	pf, err := pullWorkspaceFile(context.Background(), f.exec, "prices.csv", "text/csv", 1<<20, blobs)
	if err != nil {
		t.Fatalf("pullWorkspaceFile: unexpected error: %v", err)
	}
	if pf.SizeBytes != int64(len(content)) {
		t.Fatalf("SizeBytes = %d, want %d", pf.SizeBytes, len(content))
	}
}

// --- argv, never a shell ----------------------------------------------------

func TestPullWorkspaceFile_ArgvNotShell(t *testing.T) {
	content := []byte("timestamp,value\n2026-08-19T00:00:00Z,1\n")
	f := happyFakeSession(content)
	blobs := agentkittest.NewMemBlobs()
	if _, err := pullWorkspaceFile(context.Background(), f.exec, "prices.csv", "text/csv", 1<<20, blobs); err != nil {
		t.Fatalf("pullWorkspaceFile: unexpected error: %v", err)
	}
	if len(f.calls) == 0 {
		t.Fatalf("expected at least one exec call")
	}
	// Every call is a direct binary invocation, never a shell wrapping a
	// string command line: the command name itself (cmd[0]) is never "sh" or
	// "bash". Checked on cmd[0] rather than "no element anywhere in the
	// argv", because the ticket's own pinned argv for the size probe is
	// []string{"wc", "-c", abs} — "-c" is wc's legitimate byte-count flag
	// there, not a shell flag, so flagging any occurrence of "-c" would
	// reject the very shape the ticket mandates.
	for _, call := range f.calls {
		if len(call) == 0 {
			continue
		}
		if call[0] == "sh" || call[0] == "bash" {
			t.Fatalf("exec call used a shell: %v", call)
		}
	}
}

// --- exit-code handling ------------------------------------------------------

func TestPullWorkspaceFile_ExitCodeHandling(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		f := &fakeSession{testFExit: 1}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "missing.csv", "text/csv", 1<<20, blobs)
		if err == nil {
			t.Fatal("want error for missing path")
		}
		if len(f.calls) != 1 {
			t.Fatalf("want exactly the test -f probe, got %v", f.calls)
		}
	})

	t.Run("directory path", func(t *testing.T) {
		// `test -f` on a directory also exits non-zero; there is no separate
		// code path, but the ticket names it as its own case, so it is
		// exercised as its own case.
		f := &fakeSession{testFExit: 1}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "adir", "text/csv", 1<<20, blobs)
		if err == nil {
			t.Fatal("want error for directory path")
		}
	})

	t.Run("cat exits non-zero after successful probe", func(t *testing.T) {
		f := &fakeSession{
			testFExit: 0,
			wcOut:     []byte("10 /workspace/x\n"),
			wcExit:    0,
			catExit:   1,
			catOut:    nil,
		}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "x", "text/csv", 1<<20, blobs)
		if err == nil {
			t.Fatal("want error when cat exits non-zero")
		}
		keys, listErr := blobs.List(context.Background(), agentdb.DatasetBlobPrefix)
		if listErr != nil {
			t.Fatalf("List: %v", listErr)
		}
		if len(keys) != 0 {
			t.Fatalf("a blob was written despite cat failing: %v", keys)
		}
	})

	t.Run("wc exits non-zero", func(t *testing.T) {
		f := &fakeSession{testFExit: 0, wcExit: 1}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "x", "text/csv", 1<<20, blobs)
		if err == nil {
			t.Fatal("want error when wc -c exits non-zero")
		}
	})

	t.Run("wc prints a non-integer first field", func(t *testing.T) {
		f := &fakeSession{testFExit: 0, wcOut: []byte("not-a-number /workspace/x\n"), wcExit: 0}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "x", "text/csv", 1<<20, blobs)
		if err == nil {
			t.Fatal("want error when wc -c output is not an integer")
		}
	})
}

// --- the size cap -------------------------------------------------------------

func TestPullWorkspaceFile_SizeCap(t *testing.T) {
	t.Run("refused before content is read", func(t *testing.T) {
		f := &fakeSession{
			testFExit: 0,
			wcOut:     []byte("1000 /workspace/x\n"),
			wcExit:    0,
			// catOut intentionally unset — if cat is invoked at all the test
			// below (zero cat calls) fails, proving the refusal happens
			// before content is pulled.
		}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "big.csv", "text/csv", 100, blobs)
		if err == nil {
			t.Fatal("want error: probed size over cap")
		}
		if !strings.Contains(err.Error(), "1000") || !strings.Contains(err.Error(), "100") {
			t.Fatalf("refusal does not name both sizes: %v", err)
		}
		for _, call := range f.calls {
			if call[0] == "cat" {
				t.Fatalf("cat was invoked despite the probed size already exceeding the cap: %v", f.calls)
			}
		}
	})

	t.Run("TOCTOU: file grew between probe and read, blob rolled back", func(t *testing.T) {
		grown := bytes.Repeat([]byte("x"), 200)
		f := &fakeSession{
			testFExit: 0,
			wcOut:     []byte("50 /workspace/x\n"), // probe under the cap
			wcExit:    0,
			catOut:    grown, // but the actual read is over it
			catExit:   0,
		}
		blobs := agentkittest.NewMemBlobs()
		_, err := pullWorkspaceFile(context.Background(), f.exec, "x", "text/csv", 100, blobs)
		if err == nil {
			t.Fatal("want error: actual bytes pulled exceed the cap")
		}
		if !strings.Contains(err.Error(), "200") || !strings.Contains(err.Error(), "100") {
			t.Fatalf("refusal does not name both sizes: %v", err)
		}
		keys, listErr := blobs.List(context.Background(), agentdb.DatasetBlobPrefix)
		if listErr != nil {
			t.Fatalf("List: %v", listErr)
		}
		if len(keys) != 0 {
			t.Fatalf("blob was not rolled back after the post-write cap failure: %v", keys)
		}
	})
}

// --- delete-on-failure --------------------------------------------------------

func TestPullWorkspaceFile_DeleteOnFailure(t *testing.T) {
	grown := bytes.Repeat([]byte("y"), 200)
	f := &fakeSession{
		testFExit: 0,
		wcOut:     []byte("50 /workspace/x\n"),
		wcExit:    0,
		catOut:    grown,
		catExit:   0,
	}
	blobs := failDeleteBlobs{BlobStore: agentkittest.NewMemBlobs()}
	_, err := pullWorkspaceFile(context.Background(), f.exec, "x", "text/csv", 100, blobs)
	if err == nil {
		t.Fatal("want error: actual bytes pulled exceed the cap")
	}
	// The original cap-violation error must surface unchanged, not a delete
	// error, even though Delete always fails on this store.
	if !strings.Contains(err.Error(), "200") || !strings.Contains(err.Error(), "100") {
		t.Fatalf("original error was replaced by the delete failure: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "delete") {
		t.Fatalf("the delete failure leaked into the returned error: %v", err)
	}
}

// --- byte fidelity -------------------------------------------------------------

func TestPullWorkspaceFile_ByteFidelity(t *testing.T) {
	content := []byte{0x00, 'h', 'é', 'l', 'l', 'o', 0xff, 0xfe, '\n', 'w', 'o', 'r', 'l', 'd'}
	f := happyFakeSession(content)
	blobs := agentkittest.NewMemBlobs()
	pf, err := pullWorkspaceFile(context.Background(), f.exec, "binary.bin", "application/octet-stream", 1<<20, blobs)
	if err != nil {
		t.Fatalf("pullWorkspaceFile: unexpected error: %v", err)
	}

	rc, err := blobs.Read(context.Background(), pf.BlobPath)
	if err != nil {
		t.Fatalf("Read stored blob: %v", err)
	}
	defer rc.Close()
	stored := new(bytes.Buffer)
	if _, err := stored.ReadFrom(rc); err != nil {
		t.Fatalf("read stored blob body: %v", err)
	}
	if !bytes.Equal(stored.Bytes(), content) {
		t.Fatalf("stored bytes differ from input:\n got  %v\n want %v", stored.Bytes(), content)
	}

	wantSum := sha256.Sum256(content)
	if pf.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Fatalf("SHA256 = %s, want %s", pf.SHA256, hex.EncodeToString(wantSum[:]))
	}
	if pf.SizeBytes != int64(len(content)) {
		t.Fatalf("SizeBytes = %d, want %d", pf.SizeBytes, len(content))
	}
	// Non-csv content type: RowCount must stay 0 regardless of embedded newlines.
	if pf.RowCount != 0 {
		t.Fatalf("RowCount = %d, want 0 for a non-csv content type", pf.RowCount)
	}
}

// --- row counting ----------------------------------------------------------

func TestPullWorkspaceFile_RowCounting(t *testing.T) {
	cases := []struct {
		name        string
		content     []byte
		contentType string
		want        int
	}{
		{"trailing newline", []byte("h\na\nb\n"), "text/csv", 2},
		{"no trailing newline", []byte("h\na\nb"), "text/csv", 2},
		{"header only", []byte("h\n"), "text/csv", 0},
		{"empty file", []byte(""), "text/csv", 0},
		{"CRLF endings", []byte("h\r\na\r\nb\r\n"), "text/csv", 2},
		{"text/plain is never counted", []byte("h\na\nb\n"), "text/plain", 0},
		{"content-type parameters are ignored", []byte("h\na\nb\n"), "text/csv; charset=utf-8", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := happyFakeSession(tc.content)
			blobs := agentkittest.NewMemBlobs()
			pf, err := pullWorkspaceFile(context.Background(), f.exec, "x.csv", tc.contentType, 1<<20, blobs)
			if err != nil {
				t.Fatalf("pullWorkspaceFile: unexpected error: %v", err)
			}
			if pf.RowCount != tc.want {
				t.Fatalf("RowCount = %d, want %d", pf.RowCount, tc.want)
			}
			if pf.RowCount < 0 {
				t.Fatalf("RowCount must never be negative, got %d", pf.RowCount)
			}
		})
	}
}

// --- blob-key uniqueness ------------------------------------------------------

func TestPullWorkspaceFile_BlobKeyUniqueness(t *testing.T) {
	content := []byte("timestamp,value\n2026-08-19T00:00:00Z,1\n")
	blobs := agentkittest.NewMemBlobs()

	f1 := happyFakeSession(content)
	pf1, err := pullWorkspaceFile(context.Background(), f1.exec, "prices.csv", "text/csv", 1<<20, blobs)
	if err != nil {
		t.Fatalf("first pull: unexpected error: %v", err)
	}

	f2 := happyFakeSession(content)
	pf2, err := pullWorkspaceFile(context.Background(), f2.exec, "prices.csv", "text/csv", 1<<20, blobs)
	if err != nil {
		t.Fatalf("second pull: unexpected error: %v", err)
	}

	if pf1.BlobPath == pf2.BlobPath {
		t.Fatalf("two identical calls produced the same blob key: %s", pf1.BlobPath)
	}
	if !strings.HasPrefix(pf1.BlobPath, agentdb.DatasetBlobPrefix) {
		t.Fatalf("BlobPath %q does not carry the dataset blob prefix", pf1.BlobPath)
	}
	if !strings.HasPrefix(pf2.BlobPath, agentdb.DatasetBlobPrefix) {
		t.Fatalf("BlobPath %q does not carry the dataset blob prefix", pf2.BlobPath)
	}
}
