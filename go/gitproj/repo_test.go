package gitproj

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every test here runs against a local bare repository in t.TempDir(). There is
// no network, ever.

func requireGitBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available; skipping the git driver suite")
	}
}

// runGitCmd runs git directly, for arranging fixtures and for reading back what
// the driver actually wrote. It never goes through Repo.
func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	fixed := []string{
		"-c", "user.name=Fixture",
		"-c", "user.email=fixture@example.test",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "safe.directory=" + dir,
	}
	cmd := exec.Command("git", append(fixed, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func newBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, dir, "-c", "init.defaultBranch=main", "init", "--bare")
	return dir
}

func newClone(t *testing.T, remote, name string) *Repo {
	t.Helper()
	r, err := Clone(context.Background(), filepath.Join(t.TempDir(), name), remote, "main")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	return r
}

// writeAndCommit is the render path in miniature: write a tree, commit it.
func writeAndCommit(t *testing.T, r *Repo, files map[string][]byte, subject string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := r.WriteTree(ctx, files, "orange"); err != nil {
		t.Fatalf("write tree: %v", err)
	}
	sha, err := r.Commit(ctx, subject, "", map[string]string{"Orange-Seq": "1"})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return sha
}

func TestRepoCloneIsIdempotent(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	remote := newBareRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")

	first, err := Clone(ctx, dir, remote, "main")
	if err != nil {
		t.Fatalf("first clone: %v", err)
	}
	sha := writeAndCommit(t, first, map[string][]byte{"orange/settings.md": []byte("one\n")}, "first")

	second, err := Clone(ctx, dir, remote, "main")
	if err != nil {
		t.Fatalf("re-clone over an existing clone: %v", err)
	}
	got, err := second.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != sha {
		t.Fatalf("existing clone was not reused: head %s, want %s", got, sha)
	}
	if second.Branch() != "main" || second.Remote() != remote {
		t.Fatalf("reused clone lost its identity: branch %q remote %q", second.Branch(), second.Remote())
	}

	opened, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened.Branch() != "main" || opened.Remote() != remote {
		t.Fatalf("Open read the wrong identity: branch %q remote %q", opened.Branch(), opened.Remote())
	}
}

func TestRepoCloneReportsCorruptionInsteadOfDeleting(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	remote := newBareRemote(t)

	junk := filepath.Join(t.TempDir(), "junk")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(junk, "important.txt")
	if err := os.WriteFile(keep, []byte("do not delete me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Clone(ctx, junk, remote, "main"); !errors.Is(err, ErrCorruptClone) {
		t.Fatalf("want ErrCorruptClone, got %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a corrupt clone must be reported, not deleted: %v", err)
	}

	good := filepath.Join(t.TempDir(), "clone")
	if _, err := Clone(ctx, good, remote, "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := Clone(ctx, good, "https://example.invalid/somewhere-else.git", "main"); !errors.Is(err, ErrRemoteMismatch) {
		t.Fatalf("want ErrRemoteMismatch, got %v", err)
	}
}

func TestRepoWriteTreeDeletesDroppedFiles(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	r := newClone(t, newBareRemote(t), "clone")

	writeAndCommit(t, r, map[string][]byte{
		"orange/workers/architect.md":  []byte("architect\n"),
		"orange/workers/copywriter.md": []byte("copywriter\n"),
		"orange/settings.md":           []byte("settings\n"),
	}, "two workers")

	// copywriter is gone from the render: the file must go with it.
	changed, err := r.WriteTree(ctx, map[string][]byte{
		"orange/workers/architect.md": []byte("architect\n"),
		"orange/settings.md":          []byte("settings\n"),
	}, "orange")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("dropping a file must report changed=true")
	}
	if _, err := os.Stat(filepath.Join(r.Path(), "orange/workers/copywriter.md")); !os.IsNotExist(err) {
		t.Fatalf("dropped file still on disk: %v", err)
	}
	if _, err := r.Commit(ctx, "one worker", "", map[string]string{"Orange-Seq": "2"}); err != nil {
		t.Fatal(err)
	}
	tracked := runGitCmd(t, r.Path(), "ls-files")
	if strings.Contains(tracked, "copywriter") {
		t.Fatalf("dropped file still tracked:\n%s", tracked)
	}

	// An untracked leftover inside the subfolder is swept too.
	stray := filepath.Join(r.Path(), "orange/workers/stray.md")
	if err := os.WriteFile(stray, []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.WriteTree(ctx, map[string][]byte{
		"orange/workers/architect.md": []byte("architect\n"),
		"orange/settings.md":          []byte("settings\n"),
	}, "orange"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("untracked leftover survived: %v", err)
	}
}

func TestRepoWriteTreeReportsNoChangeWhenIdentical(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	r := newClone(t, newBareRemote(t), "clone")

	files := map[string][]byte{
		"orange/settings.md":          []byte("settings\n"),
		"orange/workers/architect.md": []byte("architect\n"),
	}
	sha := writeAndCommit(t, r, files, "initial")

	// This is the property that makes the import → render loop terminate.
	changed, err := r.WriteTree(ctx, files, "orange")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("re-rendering identical content must report changed=false")
	}
	again, err := r.Commit(ctx, "should be a no-op", "", map[string]string{"Orange-Seq": "2"})
	if err != nil {
		t.Fatalf("committing with nothing staged must be a no-op, not an error: %v", err)
	}
	if again != sha {
		t.Fatalf("no-op commit returned %s, want the unchanged head %s", again, sha)
	}
	if n := strings.Count(runGitCmd(t, r.Path(), "log", "--oneline"), "\n"); n != 1 {
		t.Fatalf("expected exactly one commit, log has %d", n)
	}

	// One byte of difference is a change.
	files["orange/settings.md"] = []byte("settings changed\n")
	changed, err = r.WriteTree(ctx, files, "orange")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed content must report changed=true")
	}
}

func TestRepoWriteTreeRefusesPathsOutsideSubfolder(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	r := newClone(t, newBareRemote(t), "clone")

	for _, bad := range []string{
		"../.github/workflows/evil.yml",
		"orange/../../escape.md",
		"/etc/passwd",
		".github/workflows/evil.yml",
		"orange/.git/config",
	} {
		if _, err := r.WriteTree(ctx, map[string][]byte{bad: []byte("x")}, "orange"); !errors.Is(err, ErrPathOutsideSubfolder) {
			t.Fatalf("path %q: want ErrPathOutsideSubfolder, got %v", bad, err)
		}
	}
}

func TestRepoCommitTrailersAndFixedAuthorRoundTrip(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	r := newClone(t, newBareRemote(t), "clone")

	if _, err := r.WriteTree(ctx, map[string][]byte{"orange/workers/copywriter.md": []byte("prompt\n")}, "orange"); err != nil {
		t.Fatal(err)
	}
	// The body is model-written text, and it tries to forge a trailer.
	body := "the tone was wrong for a technical audience\n\nOrange-Seq: 999999"
	sha, err := r.Commit(ctx, "worker_prompt_write: copywriter", body, map[string]string{
		"Orange-Project":      "wolf",
		"Orange-Seq":          "1247",
		"Orange-Event":        "7f3c",
		"Orange-Action":       "worker_prompt_write",
		"Orange-Actor-Worker": "architect",
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if sha == "" {
		t.Fatal("commit returned no sha")
	}

	author := strings.TrimSpace(runGitCmd(t, r.Path(), "log", "-1", "--pretty=%an|%ae|%cn|%ce"))
	want := AuthorName + "|" + AuthorEmail + "|" + AuthorName + "|" + AuthorEmail
	if author != want {
		t.Fatalf("author/committer identity is %q, want the fixed %q", author, want)
	}

	subject := strings.TrimSpace(runGitCmd(t, r.Path(), "log", "-1", "--pretty=%s"))
	if subject != "worker_prompt_write: copywriter" {
		t.Fatalf("subject round-trip: %q", subject)
	}
	full := runGitCmd(t, r.Path(), "log", "-1", "--pretty=%B")
	if !strings.Contains(full, "the tone was wrong for a technical audience") {
		t.Fatalf("rationale did not survive:\n%s", full)
	}

	// git itself must read our trailers, and only ours.
	trailers := runGitCmd(t, r.Path(), "log", "-1", "--pretty=%(trailers:only,unfold)")
	for _, line := range []string{
		"Orange-Action: worker_prompt_write",
		"Orange-Actor-Worker: architect",
		"Orange-Event: 7f3c",
		"Orange-Project: wolf",
		"Orange-Seq: 1247",
	} {
		if !strings.Contains(trailers, line) {
			t.Fatalf("missing trailer %q in:\n%s", line, trailers)
		}
	}
	if strings.Contains(trailers, "999999") {
		t.Fatalf("a trailer forged in the body was parsed as a real trailer:\n%s", trailers)
	}

	// Trailer keys are sorted, so the same event always yields the same message.
	block := full[strings.Index(full, "Orange-Action"):]
	if got := strings.Index(block, "Orange-Project"); got < strings.Index(block, "Orange-Event") {
		t.Fatalf("trailer block is not in sorted key order:\n%s", block)
	}
}

func TestRepoCommitRejectsForgedTrailerArguments(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	r := newClone(t, newBareRemote(t), "clone")
	if _, err := r.WriteTree(ctx, map[string][]byte{"orange/settings.md": []byte("x\n")}, "orange"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, "subject", "", map[string]string{"Orange-Actor-Worker": "architect\nOrange-Seq: 9"}); err == nil {
		t.Fatal("a newline in a trailer value must be refused")
	}
	if _, err := r.Commit(ctx, "line one\nline two", "", nil); err == nil {
		t.Fatal("a multi-line subject must be refused")
	}
	if _, err := r.Commit(ctx, "subject", "", map[string]string{"bad key": "x"}); err == nil {
		t.Fatal("an invalid trailer key must be refused")
	}
}

func TestRepoPushFastForwardOnly(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	remote := newBareRemote(t)

	a := newClone(t, remote, "a")
	writeAndCommit(t, a, map[string][]byte{"orange/settings.md": []byte("one\n")}, "first")
	if err := a.Push(ctx); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if err := a.Push(ctx); err != nil {
		t.Fatalf("pushing an already-pushed head must be a no-op: %v", err)
	}

	// A second writer (a human, or a second clone) moves the remote on.
	b := newClone(t, remote, "b")
	writeAndCommit(t, b, map[string][]byte{
		"orange/settings.md": []byte("one\n"),
		"orange/humans.md":   []byte("edited by a human\n"),
	}, "human edit")
	if err := b.Push(ctx); err != nil {
		t.Fatalf("second writer push: %v", err)
	}

	// a now diverges.
	writeAndCommit(t, a, map[string][]byte{
		"orange/settings.md": []byte("two\n"),
	}, "second")

	// Without a fetch, a's local view still says fast-forward; the remote is the
	// one that refuses, and that refusal must map to the same error.
	if err := a.Push(ctx); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("remote rejection: want ErrNotFastForward, got %v", err)
	}
	// After a fetch, the local check catches it before any network write.
	if err := a.Fetch(ctx); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := a.Push(ctx); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("local ff check: want ErrNotFastForward, got %v", err)
	}

	// Nothing was forced: the remote still holds b's commit.
	bHead, err := b.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remoteHead := strings.TrimSpace(runGitCmd(t, remote, "rev-parse", "refs/heads/main"))
	if remoteHead != bHead {
		t.Fatalf("remote head is %s, want the other writer's %s — something overwrote it", remoteHead, bHead)
	}

	// And the ancestry helpers agree with the refusal.
	aRemote, err := a.RemoteSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	aHead, err := a.HeadSHA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if aRemote != bHead {
		t.Fatalf("RemoteSHA is %s, want %s", aRemote, bHead)
	}
	ok, err := a.IsAncestor(ctx, aRemote, aHead)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("IsAncestor says the diverged remote is an ancestor")
	}
}

func TestRepoChangedPathsIsATreeDiff(t *testing.T) {
	requireGitBinary(t)
	ctx := context.Background()
	r := newClone(t, newBareRemote(t), "clone")

	base := writeAndCommit(t, r, map[string][]byte{
		"orange/settings.md":           []byte("settings\n"),
		"orange/workers/architect.md":  []byte("architect\n"),
		"orange/workers/copywriter.md": []byte("copywriter\n"),
	}, "base")

	if _, err := r.WriteTree(ctx, map[string][]byte{
		"orange/settings.md":          []byte("settings changed\n"), // modify
		"orange/workers/architect.md": []byte("architect\n"),        // untouched
		"orange/memory/goal.md":       []byte("the goal\n"),         // add
		// copywriter.md dropped                                        // delete
	}, "orange"); err != nil {
		t.Fatal(err)
	}
	head, err := r.Commit(ctx, "second", "", map[string]string{"Orange-Seq": "2"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.ChangedPaths(ctx, base, head)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"orange/memory/goal.md",
		"orange/settings.md",
		"orange/workers/copywriter.md",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ChangedPaths = %v, want %v", got, want)
	}

	// An empty from-SHA lists the whole tree (the bootstrap case).
	all, err := r.ChangedPaths(ctx, "", head)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || !strings.Contains(strings.Join(all, ","), "orange/memory/goal.md") {
		t.Fatalf("full tree listing = %v", all)
	}

	// A rename must read as a delete plus an add, never as a rename.
	if _, err := r.WriteTree(ctx, map[string][]byte{
		"orange/settings.md":        []byte("settings changed\n"),
		"orange/workers/planner.md": []byte("architect\n"), // same bytes, new path
		"orange/memory/goal.md":     []byte("the goal\n"),
	}, "orange"); err != nil {
		t.Fatal(err)
	}
	renamed, err := r.Commit(ctx, "third", "", map[string]string{"Orange-Seq": "3"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = r.ChangedPaths(ctx, head, renamed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "orange/workers/architect.md,orange/workers/planner.md" {
		t.Fatalf("rename should surface as delete+add, got %v", got)
	}
}
