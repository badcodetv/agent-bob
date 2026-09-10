package gitproj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Repo is the ONLY place in this codebase that shells out to git.
//
// It deliberately drives the `git` binary through os/exec rather than linking a
// Go git library: the design (design/2026-09-09-git-projection.md §B) records
// that decision, because the semantics we depend on — trailers, fast-forward
// refusal, tree diffs — are the porcelain's semantics and we want exactly them.
//
// Concurrency: one writer per clone. Every method that touches the working tree
// or the index takes the repo mutex. Push is the single exception, and only for
// the network leg: it validates fast-forward safety under the lock, releases it,
// and then runs `git push` unlocked, so a slow or hanging remote can never block
// the render path (design §F).
type Repo struct {
	mu sync.Mutex

	repoPath string
	remote   string
	branch   string
}

// The commit author is FIXED, on purpose. The author field is the part humans
// trust by habit, so a projection commit must never look like it was written by
// a person. Who actually caused the change lives in the trailers, derived from
// the config event.
const (
	AuthorName  = "Agent Bob"
	AuthorEmail = "bob@agentbob.local"
)

var (
	// ErrNotFastForward means the remote branch is not an ancestor of local
	// HEAD. The caller surfaces it; it never resolves it. Every automatic
	// resolution (force, force-with-lease, rebase, merge -X) silently destroys
	// somebody's work — design §C.
	ErrNotFastForward = errors.New("gitproj: remote branch is not an ancestor of local HEAD")

	// ErrCorruptClone means something exists at the clone path that is not a
	// usable git work tree. It is REPORTED, never silently deleted: repair is a
	// deliberate operator/worker act (design §B "Repair").
	ErrCorruptClone = errors.New("gitproj: existing clone is not a usable git work tree")

	// ErrRemoteMismatch means a valid clone exists but points somewhere else.
	ErrRemoteMismatch = errors.New("gitproj: existing clone points at a different remote")

	// ErrNoCommits means the repository has no commit yet (unborn HEAD).
	ErrNoCommits = errors.New("gitproj: repository has no commits")

	// ErrPathOutsideSubfolder means a caller handed WriteTree a path that is not
	// inside the subfolder it is allowed to write. Path containment is a
	// security invariant, not tidiness: a name that escapes could write
	// .github/workflows/*.yml into the project's own repo — design §D.
	ErrPathOutsideSubfolder = errors.New("gitproj: path escapes its subfolder")
)

// Path is the clone's working-tree root.
func (r *Repo) Path() string { return r.repoPath }

// Remote is the URL of the `origin` remote.
func (r *Repo) Remote() string { return r.remote }

// Branch is the branch this projection renders onto.
func (r *Repo) Branch() string { return r.branch }

// Clone ensures a working clone of remote/branch exists at repoPath, and is
// idempotent:
//
//   - a valid clone already there, pointing at the same remote, is reused;
//   - a missing (or empty) directory is cloned into;
//   - anything else is reported as ErrCorruptClone or ErrRemoteMismatch — never
//     deleted. Deleting a clone is the caller's decision.
func Clone(ctx context.Context, repoPath, remote, branch string) (*Repo, error) {
	if repoPath == "" || remote == "" || branch == "" {
		return nil, errors.New("gitproj: clone needs a path, a remote and a branch")
	}
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("gitproj: resolve clone path: %w", err)
	}
	r := &Repo{repoPath: abs, remote: remote, branch: branch}

	occupied, err := dirHasEntries(abs)
	if err != nil {
		return nil, err
	}
	if occupied {
		if err := r.checkWorkTree(ctx); err != nil {
			return nil, err
		}
		have, _, err := r.git(ctx, nil, "remote", "get-url", "origin")
		if err != nil {
			return nil, fmt.Errorf("%w: no origin remote at %s", ErrCorruptClone, abs)
		}
		if strings.TrimSpace(have) != remote {
			return nil, fmt.Errorf("%w: %s has origin %q, want %q", ErrRemoteMismatch, abs, strings.TrimSpace(have), remote)
		}
		if err := r.ensureBranch(ctx); err != nil {
			return nil, err
		}
		return r, nil
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("gitproj: create clone path: %w", err)
	}
	// Clone without --branch: the branch may not exist on the remote yet (a
	// fresh projection), and cloning an empty repository must still succeed.
	if _, stderr, err := r.gitIn(ctx, filepath.Dir(abs), nil, "clone", "--origin", "origin", "--", remote, abs); err != nil {
		return nil, fmt.Errorf("gitproj: clone %s: %w: %s", remote, err, stderr)
	}
	if err := r.ensureBranch(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// Open attaches to an existing clone, reading its remote and branch from git
// itself. It does not create anything.
func Open(ctx context.Context, repoPath string) (*Repo, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("gitproj: resolve clone path: %w", err)
	}
	r := &Repo{repoPath: abs}
	if err := r.checkWorkTree(ctx); err != nil {
		return nil, err
	}
	remote, _, err := r.git(ctx, nil, "remote", "get-url", "origin")
	if err != nil {
		return nil, fmt.Errorf("%w: no origin remote at %s", ErrCorruptClone, abs)
	}
	branch, _, err := r.git(ctx, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("%w: HEAD is not on a branch at %s", ErrCorruptClone, abs)
	}
	r.remote = strings.TrimSpace(remote)
	r.branch = strings.TrimSpace(branch)
	return r, nil
}

// checkWorkTree reports whether repoPath is itself the root of a usable git work
// tree. It compares the toplevel deliberately: a plain "am I inside a work tree"
// check would happily attach to a PARENT repository if the clone directory were
// missing or wrecked, and we would then commit the projection into somebody
// else's repo.
func (r *Repo) checkWorkTree(ctx context.Context) error {
	out, stderr, err := r.git(ctx, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%w: %s: %s", ErrCorruptClone, r.repoPath, strings.TrimSpace(stderr))
	}
	top, err := filepath.EvalSymlinks(strings.TrimSpace(out))
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCorruptClone, r.repoPath, err)
	}
	self, err := filepath.EvalSymlinks(r.repoPath)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCorruptClone, r.repoPath, err)
	}
	if top != self {
		return fmt.Errorf("%w: %s is inside the work tree rooted at %s, not its own clone", ErrCorruptClone, r.repoPath, top)
	}
	return nil
}

// ensureBranch puts the clone on r.branch, whether or not the branch exists
// locally, on the remote, or at all (an empty remote leaves HEAD unborn).
func (r *Repo) ensureBranch(ctx context.Context) error {
	if cur, _, err := r.git(ctx, nil, "symbolic-ref", "--short", "HEAD"); err == nil && strings.TrimSpace(cur) == r.branch {
		return nil
	}
	// Local branch already there?
	if _, _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+r.branch); err == nil {
		if _, stderr, err := r.git(ctx, nil, "checkout", r.branch); err != nil {
			return fmt.Errorf("gitproj: checkout %s: %w: %s", r.branch, err, stderr)
		}
		return nil
	}
	// On the remote?
	if _, _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+r.branch); err == nil {
		if _, stderr, err := r.git(ctx, nil, "checkout", "-b", r.branch, "origin/"+r.branch); err != nil {
			return fmt.Errorf("gitproj: checkout tracking %s: %w: %s", r.branch, err, stderr)
		}
		return nil
	}
	// Nowhere yet. With an unborn HEAD (empty remote) just point HEAD at it;
	// otherwise branch off whatever we cloned, so a projection into a subfolder
	// of a real repo keeps the rest of that repo's content.
	if _, _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		if _, stderr, err := r.git(ctx, nil, "symbolic-ref", "HEAD", "refs/heads/"+r.branch); err != nil {
			return fmt.Errorf("gitproj: point HEAD at %s: %w: %s", r.branch, err, stderr)
		}
		return nil
	}
	if _, stderr, err := r.git(ctx, nil, "checkout", "-b", r.branch); err != nil {
		return fmt.Errorf("gitproj: create branch %s: %w: %s", r.branch, err, stderr)
	}
	return nil
}

// WriteTree writes the rendered file set into subfolder and reports whether the
// result actually differs from HEAD.
//
// files is keyed by repo-relative slash path (each key must live inside
// subfolder). Any file under subfolder that is NOT in the map is deleted, so the
// subfolder ends up byte-identical to what Render produced.
//
// changed == false means "nothing to commit". That is what makes the
// import → render loop terminate (design §C): a re-render of an imported tree
// produces no commit.
func (r *Repo) WriteTree(ctx context.Context, files map[string][]byte, subfolder string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub, err := cleanSubfolder(subfolder)
	if err != nil {
		return false, err
	}

	want := make(map[string][]byte, len(files))
	for p, b := range files {
		clean, err := containedPath(p, sub)
		if err != nil {
			return false, err
		}
		if _, dup := want[clean]; dup {
			return false, fmt.Errorf("gitproj: two rendered paths normalise to %q", clean)
		}
		want[clean] = b
	}

	tracked, err := r.listFiles(ctx, sub, false)
	if err != nil {
		return false, err
	}
	untracked, err := r.listFiles(ctx, sub, true)
	if err != nil {
		return false, err
	}

	// Delete what the render no longer contains. Only tracked deletions need
	// staging; an untracked leftover simply goes away.
	var stage []string
	for _, p := range tracked {
		if _, keep := want[p]; !keep {
			if err := os.Remove(filepath.Join(r.repoPath, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
				return false, fmt.Errorf("gitproj: remove %s: %w", p, err)
			}
			stage = append(stage, p)
		}
	}
	for _, p := range untracked {
		if _, keep := want[p]; !keep {
			if err := os.Remove(filepath.Join(r.repoPath, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
				return false, fmt.Errorf("gitproj: remove %s: %w", p, err)
			}
		}
	}

	for p, b := range want {
		full := filepath.Join(r.repoPath, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return false, fmt.Errorf("gitproj: create %s: %w", filepath.Dir(p), err)
		}
		if old, err := os.ReadFile(full); err == nil && bytes.Equal(old, b) {
			stage = append(stage, p) // unchanged on disk, but may be unstaged
			continue
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			return false, fmt.Errorf("gitproj: write %s: %w", p, err)
		}
		stage = append(stage, p)
	}

	pruneEmptyDirs(r.repoPath, sub)

	sort.Strings(stage)
	// -f: the host repo's .gitignore must not be able to silence the
	// projection. -A: stages the deletions as well as the writes.
	for _, batch := range chunk(stage, 100) {
		args := append([]string{"add", "-f", "-A", "--"}, batch...)
		if _, stderr, err := r.git(ctx, nil, args...); err != nil {
			return false, fmt.Errorf("gitproj: stage: %w: %s", err, stderr)
		}
	}
	return r.staged(ctx, sub)
}

// staged reports whether the index differs from HEAD within sub.
func (r *Repo) staged(ctx context.Context, sub string) (bool, error) {
	if _, _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		// Unborn HEAD: anything in the index is a change.
		entries, err := r.listFiles(ctx, sub, false)
		if err != nil {
			return false, err
		}
		return len(entries) > 0, nil
	}
	args := []string{"diff", "--cached", "--quiet", "HEAD"}
	if sub != "" {
		args = append(args, "--", sub)
	}
	_, stderr, err := r.git(ctx, nil, args...)
	if err == nil {
		return false, nil
	}
	if gitExitCode(err) == 1 {
		return true, nil
	}
	return false, fmt.Errorf("gitproj: diff index: %w: %s", err, stderr)
}

// listFiles returns repo-relative slash paths under sub. others selects
// untracked-but-not-ignored files instead of tracked ones.
func (r *Repo) listFiles(ctx context.Context, sub string, others bool) ([]string, error) {
	args := []string{"ls-files", "-z"}
	if others {
		args = append(args, "--others", "--exclude-standard")
	} else {
		args = append(args, "--cached")
	}
	if sub != "" {
		args = append(args, "--", sub)
	}
	out, stderr, err := r.git(ctx, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitproj: list files: %w: %s", err, stderr)
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

var trailerKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)

// Commit commits the staged tree with the fixed Agent Bob identity, the given
// subject and body, and trailers appended as "Key: value" lines in their own
// trailer paragraph (keys sorted, so the message is deterministic).
//
// Committing when nothing is staged is a no-op that returns the current HEAD.
//
// The trailer block is always its own final paragraph, which is what stops a
// model-written body from forging trailers: a "Bob-Seq: 5" line inside the
// rationale ends up in an earlier paragraph and git will not read it as a
// trailer. That guarantee holds only while trailers is non-empty.
func (r *Repo) Commit(ctx context.Context, subject, body string, trailers map[string]string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", errors.New("gitproj: commit needs a subject")
	}
	if strings.ContainsAny(subject, "\n\r") {
		return "", errors.New("gitproj: commit subject must be a single line")
	}
	keys := make([]string, 0, len(trailers))
	for k, v := range trailers {
		if !trailerKeyRe.MatchString(k) {
			return "", fmt.Errorf("gitproj: invalid trailer key %q", k)
		}
		if strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("gitproj: trailer %q value must be a single line", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	r.mu.Lock()
	defer r.mu.Unlock()

	dirty, err := r.staged(ctx, "")
	if err != nil {
		return "", err
	}
	if !dirty {
		return r.headSHA(ctx)
	}

	var msg strings.Builder
	msg.WriteString(subject)
	msg.WriteString("\n")
	if strings.TrimSpace(body) != "" {
		msg.WriteString("\n")
		msg.WriteString(strings.TrimRight(body, "\n"))
		msg.WriteString("\n")
	}
	if len(keys) > 0 {
		msg.WriteString("\n")
		for _, k := range keys {
			msg.WriteString(k)
			msg.WriteString(": ")
			msg.WriteString(trailers[k])
			msg.WriteString("\n")
		}
	}

	if _, stderr, err := r.git(ctx, []byte(msg.String()), "commit", "--no-verify", "--cleanup=whitespace", "-F", "-"); err != nil {
		return "", fmt.Errorf("gitproj: commit: %w: %s", err, stderr)
	}
	return r.headSHA(ctx)
}

// Fetch updates the remote-tracking ref for our branch. Network work; per design
// §F the push loop is allowed to hold the lock for this, but never for the push.
func (r *Repo) Fetch(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, stderr, err := r.git(ctx, nil, "fetch", "--no-tags", "origin", r.branch)
	if err != nil {
		// A branch that does not exist on the remote yet is not a failure.
		if strings.Contains(stderr, "couldn't find remote ref") {
			return nil
		}
		return fmt.Errorf("gitproj: fetch: %w: %s", err, stderr)
	}
	return nil
}

// HeadSHA is the local branch tip.
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headSHA(ctx)
}

func (r *Repo) headSHA(ctx context.Context) (string, error) {
	out, _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", ErrNoCommits
	}
	return strings.TrimSpace(out), nil
}

// RemoteSHA is our last-fetched view of the remote branch tip — the local
// remote-tracking ref, not a network call. Returns "" when the branch does not
// exist on the remote. Call Fetch first if you need it fresh.
func (r *Repo) RemoteSHA(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.remoteSHA(ctx)
}

func (r *Repo) remoteSHA(ctx context.Context) (string, error) {
	out, _, err := r.git(ctx, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+r.branch)
	if err != nil {
		if gitExitCode(err) == 1 {
			return "", nil
		}
		return "", fmt.Errorf("gitproj: read origin/%s: %w", r.branch, err)
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether commit a is an ancestor of commit b.
func (r *Repo) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isAncestor(ctx, a, b)
}

func (r *Repo) isAncestor(ctx context.Context, a, b string) (bool, error) {
	_, stderr, err := r.git(ctx, nil, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if gitExitCode(err) == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitproj: merge-base: %w: %s", err, stderr)
}

// Push pushes the local branch, FAST-FORWARD ONLY.
//
// It never forces, never uses --force-with-lease, never rebases and never
// merges. If the remote is not an ancestor of local HEAD it returns
// ErrNotFastForward and does nothing else — resolving that automatically would
// destroy somebody's work, so it is the caller's (and ultimately a human's)
// problem. Design §C.
//
// The fast-forward check runs under the lock; the network leg does not.
func (r *Repo) Push(ctx context.Context) error {
	r.mu.Lock()
	head, err := r.headSHA(ctx)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	remoteHead, err := r.remoteSHA(ctx)
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if remoteHead != "" && remoteHead != head {
		ok, err := r.isAncestor(ctx, remoteHead, head)
		if err != nil {
			r.mu.Unlock()
			return err
		}
		if !ok {
			r.mu.Unlock()
			return fmt.Errorf("%w: origin/%s is %s, local is %s", ErrNotFastForward, r.branch, short(remoteHead), short(head))
		}
	}
	r.mu.Unlock()

	// Push the exact commit we vetted, so a concurrent local commit cannot turn
	// a checked push into an unchecked one.
	_, stderr, err := r.git(ctx, nil, "push", "origin", head+":refs/heads/"+r.branch)
	if err != nil {
		if isNonFastForward(stderr) {
			return fmt.Errorf("%w: remote rejected the push: %s", ErrNotFastForward, strings.TrimSpace(stderr))
		}
		return fmt.Errorf("gitproj: push: %w: %s", err, stderr)
	}
	return nil
}

func isNonFastForward(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "non-fast-forward") ||
		strings.Contains(s, "fetch first") ||
		strings.Contains(s, "! [rejected]")
}

// ChangedPaths returns the repo-relative paths that differ between two commits.
//
// This is a TREE diff, not a history walk: the importer must never read git
// history semantically, because git has no total order to read (design §C, and
// the first of the three structural kills in the doc's Context).
//
// Renames are deliberately not detected — a rename is a delete plus an add, and
// that is exactly how the importer must treat it. When fromSHA is empty the
// result is every path at toSHA (the bootstrap case).
func (r *Repo) ChangedPaths(ctx context.Context, fromSHA, toSHA string) ([]string, error) {
	if toSHA == "" {
		return nil, errors.New("gitproj: ChangedPaths needs a target commit")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if fromSHA == "" {
		out, stderr, err := r.git(ctx, nil, "ls-tree", "-r", "-z", "--name-only", toSHA)
		if err != nil {
			return nil, fmt.Errorf("gitproj: ls-tree %s: %w: %s", short(toSHA), err, stderr)
		}
		return splitNUL(out), nil
	}

	out, stderr, err := r.git(ctx, nil, "diff", "--name-status", "--no-renames", "-z", fromSHA, toSHA)
	if err != nil {
		return nil, fmt.Errorf("gitproj: diff %s..%s: %w: %s", short(fromSHA), short(toSHA), err, stderr)
	}
	fields := splitNUL(out)
	var paths []string
	// With -z and --no-renames the stream is status, path, status, path, …
	for i := 0; i+1 < len(fields); i += 2 {
		paths = append(paths, fields[i+1])
	}
	sort.Strings(paths)
	return paths, nil
}

// --- plumbing -------------------------------------------------------------

func (r *Repo) git(ctx context.Context, stdin []byte, args ...string) (string, string, error) {
	return r.gitIn(ctx, r.repoPath, stdin, args...)
}

func (r *Repo) gitIn(ctx context.Context, dir string, stdin []byte, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", append(baseGitArgs(r.repoPath), args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		// Never sit at a credential prompt inside a background loop.
		"GIT_TERMINAL_PROMPT=0",
		// Stable, parseable messages.
		"LC_ALL=C",
	)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// baseGitArgs pins everything that would otherwise be inherited from whatever
// git configuration happens to exist on the host, so the projection behaves the
// same on a developer's laptop and in the agentd container.
func baseGitArgs(repoPath string) []string {
	return []string{
		"-c", "core.hooksPath=/dev/null", // never run a repo's hooks
		"-c", "core.autocrlf=false", // byte-for-byte determinism
		"-c", "core.safecrlf=false",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "gc.auto=0",
		"-c", "advice.detachedHead=false",
		"-c", "user.name=" + AuthorName,
		"-c", "user.email=" + AuthorEmail,
		"-c", "safe.directory=" + repoPath,
	}
}

func gitExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// cleanSubfolder normalises the write scope. "" means the whole repository.
func cleanSubfolder(sub string) (string, error) {
	sub = strings.Trim(strings.TrimSpace(sub), "/")
	if sub == "" {
		return "", nil
	}
	clean := path.Clean(sub)
	if clean != sub || clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: subfolder %q", ErrPathOutsideSubfolder, sub)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == "" || seg == "." || seg == ".." || seg == ".git" {
			return "", fmt.Errorf("%w: subfolder %q", ErrPathOutsideSubfolder, sub)
		}
	}
	return clean, nil
}

// containedPath is a defence-in-depth containment check on top of whatever the
// renderer already validated: a path leaving the subfolder could write into
// .github/workflows and execute with the host repo's secrets (design §D).
func containedPath(p, sub string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: empty path", ErrPathOutsideSubfolder)
	}
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/") || filepath.IsAbs(p) {
		return "", fmt.Errorf("%w: %q is absolute", ErrPathOutsideSubfolder, p)
	}
	clean := path.Clean(slash)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideSubfolder, p)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == "" || seg == "." || seg == ".." || seg == ".git" {
			return "", fmt.Errorf("%w: %q", ErrPathOutsideSubfolder, p)
		}
	}
	if sub != "" && !strings.HasPrefix(clean, sub+"/") {
		return "", fmt.Errorf("%w: %q is not under %q", ErrPathOutsideSubfolder, p, sub)
	}
	return clean, nil
}

// pruneEmptyDirs removes directories left empty by deletions. git does not track
// them, so this is tidiness with no effect on the tree; failures are ignored.
func pruneEmptyDirs(root, sub string) {
	if sub == "" {
		return
	}
	base := filepath.Join(root, filepath.FromSlash(sub))
	var dirs []string
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != base {
			dirs = append(dirs, p)
		}
		return nil
	})
	// Deepest first, so a chain of empty directories collapses in one pass.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err == nil && len(entries) == 0 {
			_ = os.Remove(d)
		}
	}
}

func dirHasEntries(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("gitproj: inspect %s: %w", dir, err)
	}
	return len(entries) > 0, nil
}

func splitNUL(s string) []string {
	var out []string
	for _, f := range strings.Split(s, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func chunk(in []string, size int) [][]string {
	var out [][]string
	for len(in) > size {
		out = append(out, in[:size])
		in = in[size:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
