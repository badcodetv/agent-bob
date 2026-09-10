package main

// Tests for the outbound git door (G8) and the push loop (G9). Everything here
// runs against a local bare repository in t.TempDir() and in-memory fakes: no
// network, no database, no Docker.
//
// Five of these exist because the design says the code would be wrong without
// them, not because it looked untested:
//
//   - TestGitProjectionHookDoesNoGitWork is rule 1. The post-commit hook runs on
//     the mutating goroutine — a model's blocking tool call — so it must return
//     before any git work has happened.
//   - TestGitProjectionOneMutationOneCommit is DI4's other half: the trailers
//     must be the config event's, byte for byte.
//   - TestGitProjectionNoChangeNoCommit is the loop-termination property.
//   - TestGitProjectionUnrenderableProjectDoesNotStopTheOthers is rule 4.
//   - TestGitPushRefusesDivergedRemote is §C: no force, no rebase, and the local
//     commits survive.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
	"github.com/badcodetv/agent-bob/httpapi"
)

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeProjectionStore struct {
	mu       sync.Mutex
	settings map[string]*agentdb.ProjectSettings
	workers  map[string][]*agentdb.Worker
	events   map[string][]*agentdb.ConfigEvent
	// docs are §E's named documents. SearchMemories hands back a 500-byte
	// SNIPPET, exactly as the real store does, and GetMemory hands back the
	// full row — the difference the loader exists to respect.
	docs map[string][]*agentdb.Memory
}

func newFakeProjectionStore() *fakeProjectionStore {
	return &fakeProjectionStore{
		settings: map[string]*agentdb.ProjectSettings{},
		workers:  map[string][]*agentdb.Worker{},
		events:   map[string][]*agentdb.ConfigEvent{},
		docs:     map[string][]*agentdb.Memory{},
	}
}

func (f *fakeProjectionStore) GetProjectSettings(_ context.Context, project string) (*agentdb.ProjectSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ps, ok := f.settings[project]; ok {
		copied := *ps
		return &copied, nil
	}
	return agentdb.DefaultProjectSettings(project), nil
}

func (f *fakeProjectionStore) ListWorkers(_ context.Context, project string) ([]*agentdb.Worker, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.workers[project], nil
}

func (f *fakeProjectionStore) ListProjectSkills(context.Context, agentdb.SkillCatalogQuery) ([]*agentdb.SkillSummary, error) {
	return nil, nil
}

func (f *fakeProjectionStore) GetProjectSkill(context.Context, string, string) (*agentdb.Skill, error) {
	return nil, agentdb.ErrSkillNotFound
}

func (f *fakeProjectionStore) ListSubscriptions(context.Context, string) ([]*agentdb.Subscription, error) {
	return nil, nil
}

func (f *fakeProjectionStore) ListSchedules(context.Context, string) ([]*agentdb.Schedule, error) {
	return nil, nil
}

func (f *fakeProjectionStore) ListCustomImageVersions(context.Context, agentdb.ImageCatalogQuery) ([]*agentdb.CustomImage, error) {
	return nil, nil
}

func (f *fakeProjectionStore) SearchMemories(_ context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*agentdb.MemorySearchResult
	for _, m := range f.docs[q.Project] {
		snippet := m.Content
		if len(snippet) > 500 {
			snippet = snippet[:500] // what the real query returns, and why it may never be rendered
		}
		out = append(out, &agentdb.MemorySearchResult{ID: m.ID, Labels: m.Labels, Snippet: snippet})
	}
	return out, nil
}

func (f *fakeProjectionStore) GetMemory(_ context.Context, project, id string) (*agentdb.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.docs[project] {
		if m.ID == id {
			copied := *m
			return &copied, nil
		}
	}
	return nil, agentdb.ErrMemoryNotFound
}

func (f *fakeProjectionStore) ListConfigEvents(_ context.Context, q agentdb.ConfigEventQuery) ([]*agentdb.ConfigEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	evs := f.events[q.Project]
	if len(evs) == 0 {
		return nil, nil
	}
	// Newest first, like the real one.
	out := make([]*agentdb.ConfigEvent, 0, len(evs))
	for i := len(evs) - 1; i >= 0; i-- {
		out = append(out, evs[i])
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

// project registers a project that projects into remote.
func (f *fakeProjectionStore) project(name, remote string) *agentdb.ProjectSettings {
	f.mu.Lock()
	defer f.mu.Unlock()
	ps := agentdb.DefaultProjectSettings(name)
	ps.GitRemote = remote
	ps.GitBranch = "main"
	f.settings[name] = ps
	return ps
}

// mutate records a config event, exactly as WithConfigEvent would have.
func (f *fakeProjectionStore) mutate(project string, ev *agentdb.ConfigEvent) *agentdb.ConfigEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev.Project = project
	ev.Seq = int64(len(f.events[project]) + 1)
	f.events[project] = append(f.events[project], ev)
	return ev
}

var _ gitProjectionStore = (*fakeProjectionStore)(nil)

// fakeProjectionState is the durable half, in memory.
type fakeProjectionState struct {
	// quarantineCleared records every ClearQuarantine call, so a test can assert
	// a clean path clears the row's own error and not only its notes.
	quarantineCleared []string
	mu                sync.Mutex
	rows              map[string]*gitProjectionRecord
	projects          []string
	// notes are the per-file quarantine/ignored lists, keyed "project/kind".
	notes map[string][]agentdb.GitProjectionNote
	// failAcquire makes AcquireLease answer "somebody else holds it".
	failAcquire bool
}

func newFakeProjectionState(projects ...string) *fakeProjectionState {
	return &fakeProjectionState{rows: map[string]*gitProjectionRecord{}, projects: projects}
}

func (f *fakeProjectionState) row(project string) *gitProjectionRecord {
	if r, ok := f.rows[project]; ok {
		return r
	}
	r := &gitProjectionRecord{Project: project}
	f.rows[project] = r
	return r
}

func (f *fakeProjectionState) Get(_ context.Context, project string) (gitProjectionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.row(project), nil
}

func (f *fakeProjectionState) AcquireLease(_ context.Context, project, owner string, until int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAcquire {
		return false, nil
	}
	r := f.row(project)
	if r.LeaseOwner != "" && r.LeaseOwner != owner && r.LeaseExpiresAt > time.Now().Unix() {
		return false, nil
	}
	r.LeaseOwner, r.LeaseExpiresAt = owner, until
	return true, nil
}

func (f *fakeProjectionState) ReleaseLease(_ context.Context, project, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(project)
	if r.LeaseOwner == owner {
		r.LeaseOwner, r.LeaseExpiresAt = "", 0
	}
	return nil
}

func (f *fakeProjectionState) MarkRendered(_ context.Context, project string, seq int64, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(project)
	r.LastRenderedSeq, r.LastRenderedSHA = seq, sha
	r.LastError, r.LastErrorAt = "", 0
	return nil
}

func (f *fakeProjectionState) MarkPushed(_ context.Context, project, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(project)
	r.LastPushedSHA = sha
	r.LastError, r.LastErrorAt = "", 0
	return nil
}

func (f *fakeProjectionState) MarkImported(_ context.Context, project, sha string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sha == "" {
		return nil
	}
	f.row(project).LastImportedSHA = sha
	return nil
}

func (f *fakeProjectionState) NoteFailure(ctx context.Context, project, reason string) error {
	// The fallback path (gitbackfill.go): the kind is re-derived from the text.
	return f.NoteFailureKind(ctx, project, gitProjectionErrorKindFromText(reason), reason)
}

func (f *fakeProjectionState) NoteFailureKind(_ context.Context, project, kind, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.row(project)
	r.LastError, r.LastErrorAt, r.LastErrorKind = reason, time.Now().Unix(), kind
	return nil
}

// PutNotes keeps the per-file notes of the last run, by kind (G23).
// ClearQuarantine records the call. The behaviour under test is that a clean
// path CALLS it; the narrowing to a quarantine kind lives in the real store's
// WHERE clause, where a push failure racing the clear cannot be erased.
func (f *fakeProjectionState) ClearQuarantine(_ context.Context, project string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quarantineCleared = append(f.quarantineCleared, project)
	return nil
}

func (f *fakeProjectionState) PutNotes(_ context.Context, project, kind string, notes []agentdb.GitProjectionNote) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notes == nil {
		f.notes = map[string][]agentdb.GitProjectionNote{}
	}
	// Replacement, not accumulation — the store's contract, and what makes a
	// clean import clear the previous quarantine.
	f.notes[project+"/"+kind] = append([]agentdb.GitProjectionNote(nil), notes...)
	return nil
}

func (f *fakeProjectionState) notesFor(project, kind string) []agentdb.GitProjectionNote {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentdb.GitProjectionNote(nil), f.notes[project+"/"+kind]...)
}

func (f *fakeProjectionState) ProjectsWithRemote(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.projects...), nil
}

func (f *fakeProjectionState) lastError(project string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.row(project).LastError
}

var _ gitProjectionState = (*fakeProjectionState)(nil)

// ── the harness ─────────────────────────────────────────────────────────────

type projectionRig struct {
	t         *testing.T
	proj      *gitProjector
	store     *fakeProjectionStore
	state     *fakeProjectionState
	root      string
	remotes   map[string]string // project → bare repo path
	cloneRoot string
	logs      []string
}

func newProjectionRig(t *testing.T, projects ...string) *projectionRig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	rig := &projectionRig{
		t: t, root: root,
		store:     newFakeProjectionStore(),
		state:     newFakeProjectionState(projects...),
		remotes:   map[string]string{},
		cloneRoot: filepath.Join(root, "clones"),
	}
	for _, p := range projects {
		bare := filepath.Join(root, p+".git")
		runProjectionGit(t, root, "init", "--bare", "-b", "main", bare)
		rig.remotes[p] = bare
		rig.store.project(p, bare)
	}
	proj, err := newGitProjector(gitProjectorConfig{
		Store:  rig.store,
		State:  rig.state,
		Root:   rig.cloneRoot,
		Owner:  "test/1",
		Getenv: func(string) string { return "" },
		Logf:   func(f string, a ...any) { rig.logs = append(rig.logs, fmt.Sprintf(f, a...)) },
	})
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	rig.proj = proj
	return rig
}

func (r *projectionRig) clone(project string) string {
	return filepath.Join(r.cloneRoot, gitCloneDirName(project))
}

// commitCount is the number of commits on the clone's branch. -1 when there are
// none at all (an unborn HEAD, or no clone yet).
func (r *projectionRig) commitCount(project string) int {
	r.t.Helper()
	dir := r.clone(project)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return -1
	}
	out, _, err := runGit(context.Background(), dir, "rev-list", "--count", "HEAD")
	if err != nil {
		return -1
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return -1
	}
	return n
}

func (r *projectionRig) head(project string) string {
	r.t.Helper()
	out, _, err := runGit(context.Background(), r.clone(project), "rev-parse", "HEAD")
	if err != nil {
		r.t.Fatalf("rev-parse HEAD in %s: %v", project, err)
	}
	return strings.TrimSpace(out)
}

// trailer reads one trailer off HEAD with GIT'S OWN parser, which reads only the
// last paragraph. Never a grep — that is DI4.
func (r *projectionRig) trailer(project, key string) string {
	r.t.Helper()
	out, _, err := runGit(context.Background(), r.clone(project),
		"log", "-1", "--format=%(trailers:key="+key+",valueonly,only)")
	if err != nil {
		r.t.Fatalf("read trailer %s: %v", key, err)
	}
	return strings.TrimSpace(out)
}

func (r *projectionRig) subject(project string) string {
	r.t.Helper()
	out, _, err := runGit(context.Background(), r.clone(project), "log", "-1", "--format=%s")
	if err != nil {
		r.t.Fatalf("read subject: %v", err)
	}
	return strings.TrimSpace(out)
}

func runProjectionGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// workerPromptWrite is the config event a worker rewriting a prompt produces.
func workerPromptWrite(name, rationale string) *agentdb.ConfigEvent {
	return &agentdb.ConfigEvent{
		ID:           "ev-" + name,
		ActorWorker:  "architect",
		ActorSession: "sess-42",
		Action:       agentdb.ActionWorkerPromptWrite,
		Payload:      agentdb.JSONMap{"name": name},
		Rationale:    rationale,
		CreatedAt:    time.Now().UnixMilli(),
	}
}

// ── 🔴 rule 1: the hook must not do git work ────────────────────────────────

func TestGitProjectionHookDoesNoGitWork(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "because"))

	// The hook is what agentdb calls on the mutating goroutine.
	rig.proj.Hook()(context.Background(), ev)

	// Nothing may have happened on disk yet: no clone, therefore no index.lock
	// and no commit inside the model's tool call.
	if _, err := os.Stat(rig.clone("wolf")); !os.IsNotExist(err) {
		t.Fatalf("the hook created a clone at %s — no git operation may happen inside a mutation", rig.clone("wolf"))
	}
	if n := rig.commitCount("wolf"); n != -1 {
		t.Fatalf("the hook produced %d commit(s); it must only mark the project dirty", n)
	}
	// What it DID do is mark the project dirty.
	rig.proj.RenderPending(context.Background())
	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("after the loop drained, want 1 commit, got %d", n)
	}
}

// ── 🔴 DI4: one mutation, one commit, trailers copied out of the log ────────

func TestGitProjectionOneMutationOneCommit(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "the old prompt buried the offer"))

	rig.proj.Hook()(context.Background(), ev)
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("want exactly 1 commit for 1 config event, got %d", n)
	}
	if got, want := rig.subject("wolf"), "worker_prompt_write: copywriter"; got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
	for _, tc := range []struct{ key, want string }{
		{"Bob-Project", "wolf"},
		{"Bob-Seq", "1"},
		{"Bob-Event", ev.ID},
		{"Bob-Action", agentdb.ActionWorkerPromptWrite},
		{"Bob-Actor-Worker", "architect"},
		{"Bob-Actor-Session", "sess-42"},
	} {
		if got := rig.trailer("wolf", tc.key); got != tc.want {
			t.Errorf("trailer %s = %q, want %q", tc.key, got, tc.want)
		}
	}
	// The rationale is the body, verbatim.
	body, _, err := runGit(context.Background(), rig.clone("wolf"), "log", "-1", "--format=%b")
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(body, "the old prompt buried the offer") {
		t.Errorf("the rationale is not in the commit body: %q", body)
	}
}

// A rationale ending in a forged trailer line must not become a trailer: the
// driver's own block is the last paragraph, so git's parser reads ours.
func TestGitProjectionForgedTrailerInRationaleIsInert(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter",
		"tidy up\n\nBob-Seq: 999999\nBob-Actor-Worker: nobody"))

	rig.proj.Hook()(context.Background(), ev)
	rig.proj.RenderPending(context.Background())

	if got := rig.trailer("wolf", "Bob-Seq"); got != "1" {
		t.Fatalf("Bob-Seq = %q — a model-written rationale forged the trailer", got)
	}
	if got := rig.trailer("wolf", "Bob-Actor-Worker"); got != "architect" {
		t.Fatalf("Bob-Actor-Worker = %q — the forged line won", got)
	}
}

// ── 🔴 rule 3: the loop terminates ──────────────────────────────────────────

func TestGitProjectionNoChangeNoCommit(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	first := rig.store.mutate("wolf", workerPromptWrite("copywriter", "first"))
	rig.proj.Hook()(context.Background(), first)
	rig.proj.RenderPending(context.Background())
	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("setup: want 1 commit, got %d", n)
	}
	head := rig.head("wolf")

	// A second config event that changes NOTHING the renderer publishes. The
	// tree is byte-identical, so there must be no commit at all.
	second := rig.store.mutate("wolf", workerPromptWrite("copywriter", "second"))
	rig.proj.Hook()(context.Background(), second)
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("an unchanged tree produced %d commits; no change must mean no commit", n)
	}
	if rig.head("wolf") != head {
		t.Fatalf("HEAD moved without a change")
	}
	// The watermark still advances: this render happened and found nothing.
	rec, _ := rig.state.Get(context.Background(), "wolf")
	if rec.LastRenderedSeq != second.Seq {
		t.Errorf("watermark = %d, want %d — a no-op render still advances it", rec.LastRenderedSeq, second.Seq)
	}
}

// ── 🔴 rule 4: one bad project must not stop the others ─────────────────────

func TestGitProjectionUnrenderableProjectDoesNotStopTheOthers(t *testing.T) {
	rig := newProjectionRig(t, "broken", "healthy")

	// A literal Slack webhook URL in attention_channel: the URL IS a bearer
	// token, so §D refuses the whole tree rather than redacting it.
	broken, _ := rig.store.GetProjectSettings(context.Background(), "broken")
	broken.AttentionChannel = agentdb.JSONMap{
		"kind": "webhook",
		"url":  "https://hooks.slack.com/services/T000/B000/XXXXXXXX",
	}
	rig.store.mu.Lock()
	rig.store.settings["broken"] = broken
	rig.store.mu.Unlock()

	rig.proj.Hook()(context.Background(), rig.store.mutate("broken", workerPromptWrite("a", "why")))
	rig.proj.Hook()(context.Background(), rig.store.mutate("healthy", workerPromptWrite("b", "why")))
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("broken"); n > 0 {
		t.Fatalf("the unrenderable project published %d commit(s) — §D is refusal, not redaction", n)
	}
	reason := rig.state.lastError("broken")
	if reason == "" {
		t.Fatal("no reason was recorded for the unrenderable project")
	}
	if !strings.Contains(reason, "AttentionChannel.url") {
		t.Errorf("the recorded reason does not name the field: %q", reason)
	}
	if strings.Contains(reason, "XXXXXXXX") {
		t.Errorf("the recorded reason quotes the secret: %q", reason)
	}
	if n := rig.commitCount("healthy"); n != 1 {
		t.Fatalf("the healthy project rendered %d commit(s); one project's refusal must not stop another's", n)
	}
}

// ── boot reconciliation ─────────────────────────────────────────────────────

// A crash between "committed locally" and "wrote the watermark" is repaired by
// boot reconciliation: the re-render finds no change and advances it.
func TestGitProjectionReconcileRendersForward(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "why"))

	// Nothing has ever rendered, and the hook never fired (agentd was down).
	if err := rig.proj.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("boot reconciliation rendered %d commit(s), want 1", n)
	}
	if got := rig.trailer("wolf", "Bob-Seq"); got != "1" {
		t.Errorf("Bob-Seq = %q, want 1", got)
	}
	_ = ev

	// Re-running it is a no-op: the watermark is caught up.
	if err := rig.proj.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile again: %v", err)
	}
	rig.proj.RenderPending(context.Background())
	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("a caught-up project re-rendered into %d commits", n)
	}
}

// A project whose git_remote is empty does not project, and says nothing.
func TestGitProjectionOffWithoutARemote(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.store.mu.Lock()
	rig.store.settings["wolf"].GitRemote = ""
	rig.store.mu.Unlock()

	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "why"))
	rig.proj.Hook()(context.Background(), ev)
	rig.proj.RenderPending(context.Background())

	if _, err := os.Stat(rig.clone("wolf")); !os.IsNotExist(err) {
		t.Fatal("a project with no git_remote created a clone")
	}
	if reason := rig.state.lastError("wolf"); reason != "" {
		t.Errorf("projection being off was recorded as a failure: %q", reason)
	}
}

// A second agentd holding the lease must not render the same clone.
func TestGitProjectionRefusesWithoutTheLease(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.state.failAcquire = true

	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "why"))
	rig.proj.Hook()(context.Background(), ev)
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("wolf"); n > 0 {
		t.Fatalf("rendered %d commit(s) without holding the lease — §F is one writer per clone", n)
	}
}

// ── the push loop (G9) ──────────────────────────────────────────────────────

func TestGitPushPublishesFastForward(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())

	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("push: %v", err)
	}
	remoteHead := strings.TrimSpace(runProjectionGit(t, rig.remotes["wolf"], "rev-parse", "main"))
	if remoteHead != rig.head("wolf") {
		t.Fatalf("remote main = %s, local HEAD = %s", remoteHead, rig.head("wolf"))
	}
	rec, _ := rig.state.Get(context.Background(), "wolf")
	if rec.LastPushedSHA != remoteHead {
		t.Errorf("last_pushed_sha = %q, want %q", rec.LastPushedSHA, remoteHead)
	}
}

// 🔴 §C. Never force, never force-with-lease, never rebase, never merge.
func TestGitPushRefusesDivergedRemote(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())
	localHead := rig.head("wolf")

	// Somebody else publishes an unrelated history to the same branch.
	other := filepath.Join(rig.root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	runProjectionGit(t, other, "init", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(other, "THEIRS.md"), []byte("a human's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runProjectionGit(t, other, "add", "-A")
	runProjectionGit(t, other, "-c", "user.name=Kai", "-c", "user.email=kai@example.com",
		"-c", "commit.gpgsign=false", "commit", "-m", "their work")
	runProjectionGit(t, other, "remote", "add", "origin", rig.remotes["wolf"])
	runProjectionGit(t, other, "push", "-q", "origin", "main")
	theirs := strings.TrimSpace(runProjectionGit(t, rig.remotes["wolf"], "rev-parse", "main"))

	// The push must REFUSE. It is reported so the loop backs off, because only
	// a human reconciling the remote can clear it.
	err := rig.proj.PushProject(context.Background(), "wolf")
	if !errors.Is(err, gitproj.ErrNotFastForward) {
		t.Fatalf("a diverged remote must be refused with ErrNotFastForward, got %v", err)
	}

	if got := strings.TrimSpace(runProjectionGit(t, rig.remotes["wolf"], "rev-parse", "main")); got != theirs {
		t.Fatalf("the remote moved: %s → %s. The push was NOT fast-forward-only", theirs, got)
	}
	if rig.head("wolf") != localHead {
		t.Fatalf("local HEAD moved from %s to %s — the loop rebased or reset our commits", localHead, rig.head("wolf"))
	}
	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("local commit count is %d; the projection's commits must survive a divergence", n)
	}
	// Surfaced through the whole loop, and backed off rather than hammering.
	rig.proj.PushAll(context.Background())
	reason := rig.state.lastError("wolf")
	if !strings.Contains(strings.ToLower(reason), "ancestor") {
		t.Errorf("the divergence was not surfaced: %q", reason)
	}
	if rig.proj.pushDue("wolf") {
		t.Error("a failed push did not back off")
	}
	if got := strings.TrimSpace(runProjectionGit(t, rig.remotes["wolf"], "rev-parse", "main")); got != theirs {
		t.Fatalf("the full loop moved the remote: %s → %s", theirs, got)
	}
}

// A working remote that is simply unreachable must not block writes: the render
// keeps committing locally and the loop catches up later.
func TestGitPushFailureDoesNotBlockRendering(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())

	// Delete the remote out from under it.
	if err := os.RemoveAll(rig.remotes["wolf"]); err != nil {
		t.Fatal(err)
	}
	if err := rig.proj.PushProject(context.Background(), "wolf"); err == nil {
		t.Fatal("a vanished remote should be reported")
	}

	// Rendering still works: a new worker appears and is committed locally.
	rig.store.mu.Lock()
	rig.store.workers["wolf"] = []*agentdb.Worker{{
		Project: "wolf", Name: "copywriter", SystemPrompt: "write well", Enabled: true,
	}}
	rig.store.mu.Unlock()
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "second")))
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("wolf"); n != 2 {
		t.Fatalf("want 2 local commits with the remote gone, got %d — a GitHub outage must never block a write", n)
	}
}

// ── 🔴 DI3: token precedence, resolved in exactly one place ─────────────────

func TestGitPushTokenPrecedence(t *testing.T) {
	env := map[string]string{
		"WOLF_SETTINGS_TOKEN": "ghp_from_settings",
		"WOLF_MAP_TOKEN":      "ghp_from_map",
	}
	getenv := func(k string) string { return env[k] }

	t.Run("the settings column wins over the project map", func(t *testing.T) {
		ps := agentdb.DefaultProjectSettings("wolf")
		ps.GitTokenEnv = "WOLF_SETTINGS_TOKEN"
		got, err := resolveGitTokenEnv("wolf", ps,
			func(string) string { return "WOLF_MAP_TOKEN" }, getenv)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != "WOLF_SETTINGS_TOKEN" {
			t.Fatalf("resolved %q; DI3 says the settings column wins", got)
		}
	})

	t.Run("the project map is the fallback", func(t *testing.T) {
		ps := agentdb.DefaultProjectSettings("wolf") // no GitTokenEnv
		got, err := resolveGitTokenEnv("wolf", ps,
			func(string) string { return "WOLF_MAP_TOKEN" }, getenv)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != "WOLF_MAP_TOKEN" {
			t.Fatalf("resolved %q, want the map's value", got)
		}
	})

	t.Run("neither set is a loud failure naming both candidates", func(t *testing.T) {
		ps := agentdb.DefaultProjectSettings("wolf")
		_, err := resolveGitTokenEnv("wolf", ps, func(string) string { return "" }, getenv)
		if !errors.Is(err, ErrNoGitToken) {
			t.Fatalf("want ErrNoGitToken, got %v", err)
		}
		for _, want := range []string{"git_token_env", "github_token_env"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the failure does not name %s: %v", want, err)
			}
		}
	})

	t.Run("a named but unset variable is a loud failure", func(t *testing.T) {
		ps := agentdb.DefaultProjectSettings("wolf")
		ps.GitTokenEnv = "WOLF_MISSING_TOKEN"
		_, err := resolveGitTokenEnv("wolf", ps, nil, getenv)
		if !errors.Is(err, ErrNoGitToken) {
			t.Fatalf("want ErrNoGitToken, got %v", err)
		}
		if !strings.Contains(err.Error(), "WOLF_MISSING_TOKEN") {
			t.Errorf("the failure does not name the variable: %v", err)
		}
	})

	t.Run("a token value is never quoted in an error", func(t *testing.T) {
		ps := agentdb.DefaultProjectSettings("wolf")
		ps.GitTokenEnv = "WOLF-BAD-NAME"
		_, err := resolveGitTokenEnv("wolf", ps, nil, getenv)
		if err == nil {
			t.Fatal("an invalid variable name must be refused before it reaches a shell")
		}
		for _, secret := range []string{"ghp_from_settings", "ghp_from_map"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("the error leaks a token value: %v", err)
			}
		}
	})
}

// An https remote with no credential must fail loudly and attempt NO push.
func TestGitPushRefusesWithoutACredential(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.store.mu.Lock()
	rig.store.settings["wolf"].GitRemote = "https://github.com/badcode/wolf.git"
	rig.store.mu.Unlock()

	err := rig.proj.PushProject(context.Background(), "wolf")
	if !errors.Is(err, ErrNoGitToken) {
		t.Fatalf("want ErrNoGitToken before any network work, got %v", err)
	}
	// Nothing was published, and no anonymous attempt was made: the clone never
	// got past configuration.
	if n := rig.commitCount("wolf"); n > 0 {
		t.Fatalf("a project with no credential committed %d time(s)", n)
	}
}

// The credential reaches git by NAME, never by value: what lands in .git/config
// is the variable name inside a helper snippet.
func TestGitCredentialHelperCarriesOnlyTheVariableName(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	const token = "ghp_supersecret_value"
	rig.proj.cfg.Getenv = func(k string) string {
		if k == "WOLF_GITHUB_TOKEN" {
			return token
		}
		return ""
	}
	ps := agentdb.DefaultProjectSettings("wolf")
	ps.GitRemote = "https://github.com/badcode/wolf.git"
	ps.GitBranch = "main"
	ps.GitTokenEnv = "WOLF_GITHUB_TOKEN"
	rig.store.mu.Lock()
	rig.store.settings["wolf"] = ps
	rig.store.mu.Unlock()

	dir := rig.clone("wolf")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runGit(context.Background(), dir, "init", "-b", "main"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := rig.proj.configureCredentials(context.Background(), dir, "wolf", ps); err != nil {
		t.Fatalf("configure credentials: %v", err)
	}

	cfg, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), token) {
		t.Fatal("the token VALUE was written into .git/config")
	}
	if !strings.Contains(string(cfg), "WOLF_GITHUB_TOKEN") {
		t.Fatalf("the variable name is not in the config:\n%s", cfg)
	}
	// It is scoped to the remote's scheme://host, not installed globally.
	if !strings.Contains(string(cfg), "https://github.com") {
		t.Errorf("the helper is not scoped to the remote host:\n%s", cfg)
	}
	// And the remote URL is untouched — a token in the URL would reach
	// `git remote -v` and every subprocess's argv.
	remote, _, err := runGit(context.Background(), dir, "remote", "get-url", "origin")
	if err == nil && strings.Contains(remote, token) {
		t.Fatal("the token was written into the remote URL")
	}
}

// ── the fan-out ─────────────────────────────────────────────────────────────

// The store takes ONE hook. Both consumers must see every event, and one that
// panics must not unwind through a mutation that has already committed.
func TestFanOutConfigEventsCallsEveryConsumer(t *testing.T) {
	var seen []string
	hook := fanOutConfigEvents(func(string, ...any) {},
		func(context.Context, *agentdb.ConfigEvent) { seen = append(seen, "emitter") },
		nil,
		func(context.Context, *agentdb.ConfigEvent) { panic("boom") },
		func(context.Context, *agentdb.ConfigEvent) { seen = append(seen, "projection") },
	)
	hook(context.Background(), &agentdb.ConfigEvent{ID: "e1", Project: "wolf", Seq: 1})

	if len(seen) != 2 || seen[0] != "emitter" || seen[1] != "projection" {
		t.Fatalf("consumers seen = %v, want both, in order, despite the panicking one", seen)
	}
}

// commitTrailers must never return an empty map: gitproj.Commit's forgery
// defence is "our trailer paragraph is always last", which needs one to exist.
func TestCommitTrailersAreNeverEmpty(t *testing.T) {
	for _, ev := range []*agentdb.ConfigEvent{
		{ID: "e", Project: "wolf", Seq: 1, Action: agentdb.ActionWorkerCreate},
		{}, // the degenerate case
	} {
		if got := commitTrailers(ev); len(got) == 0 {
			t.Fatalf("commitTrailers returned no trailers for %+v — the DI4 defence needs a trailer paragraph", ev)
		}
	}
}

// A human edit (empty actor, the log's own encoding) omits the actor trailers
// rather than asserting an empty person.
func TestCommitTrailersOmitEmptyActors(t *testing.T) {
	got := commitTrailers(&agentdb.ConfigEvent{
		ID: "e1", Project: "wolf", Seq: 7, Action: agentdb.ActionProjectSettingsPut,
	})
	if _, ok := got["Bob-Actor-Worker"]; ok {
		t.Error("an empty actor was rendered as a trailer")
	}
	if got["Bob-Seq"] != "7" || got["Bob-Event"] != "e1" {
		t.Errorf("derived trailers are wrong: %v", got)
	}
}

var _ = gitproj.DefaultSubfolder

// ── 🔴 G11's requirement: the local branch is fast-forwarded onto the remote ──
//
// The importer reads and imports from the REMOTE tip and deliberately leaves the
// local branch alone. If nothing advances it, the two histories part company the
// moment a human pushes, and the push loop then refuses every push as
// non-fast-forward — correctly, and for ever.

// humanPushes adds a commit to the project's bare remote from an independent
// working copy, the way a person editing the mirror repo would.
func (r *projectionRig) humanPushes(project, filename, message string) string {
	r.t.Helper()
	dir := r.t.TempDir()
	runProjectionGit(r.t, dir, "clone", "-q", "--branch", "main", r.remotes[project], ".")
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("a human wrote this\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	runProjectionGit(r.t, dir, "add", "-A")
	runProjectionGit(r.t, dir, "-c", "user.name=Kai", "-c", "user.email=kai@example.com",
		"-c", "commit.gpgsign=false", "commit", "-m", message)
	runProjectionGit(r.t, dir, "push", "-q", "origin", "main")
	return strings.TrimSpace(runProjectionGit(r.t, r.remotes[project], "rev-parse", "main"))
}

func TestGitProjectionFastForwardsOntoAHumanCommit(t *testing.T) {
	rig := newProjectionRig(t, "wolf")

	// A first render, published, so the human has something to build on.
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "first")))
	rig.proj.RenderPending(context.Background())
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// A human commits on the remote.
	theirs := rig.humanPushes("wolf", "NOTES.md", "a human's note")

	// A local render must fast-forward onto their commit first, and sit on top.
	rig.store.mu.Lock()
	rig.store.workers["wolf"] = []*agentdb.Worker{{
		Project: "wolf", Name: "copywriter", SystemPrompt: "write well", Enabled: true,
	}}
	rig.store.mu.Unlock()
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "second")))
	rig.proj.RenderPending(context.Background())

	ours := rig.head("wolf")
	if ours == theirs {
		t.Fatal("the render produced no commit; the test proves nothing")
	}
	if _, _, err := runGit(context.Background(), rig.clone("wolf"),
		"merge-base", "--is-ancestor", theirs, ours); err != nil {
		t.Fatalf("our commit %s is not on top of the human's %s — the local branch was not fast-forwarded",
			short(ours), short(theirs))
	}
	// The human's file survived our render (it is outside the subfolder).
	if _, err := os.Stat(filepath.Join(rig.clone("wolf"), "NOTES.md")); err != nil {
		t.Errorf("the human's file was deleted by the render: %v", err)
	}

	// And the push now SUCCEEDS, which is the whole point.
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("push after a human commit must succeed, got: %v", err)
	}
	if got := strings.TrimSpace(runProjectionGit(t, rig.remotes["wolf"], "rev-parse", "main")); got != ours {
		t.Fatalf("remote main = %s, want our commit %s", got, ours)
	}
}

// A project whose watermark is already caught up still picks up a human's
// commit at boot — otherwise the first later render would diverge.
func TestGitProjectionReconcileFastForwardsACaughtUpProject(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "first")))
	rig.proj.RenderPending(context.Background())
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("push: %v", err)
	}
	theirs := rig.humanPushes("wolf", "NOTES.md", "a human's note")

	// Nothing to render: the watermark equals the newest config event.
	if err := rig.proj.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rig.proj.RenderPending(context.Background())

	if rig.head("wolf") != theirs {
		t.Fatalf("local HEAD = %s, want the human's %s — a caught-up project was never fast-forwarded",
			short(rig.head("wolf")), short(theirs))
	}
}

// The push loop notices a human commit too, not just boot.
func TestGitPushFastForwardsBeforePushing(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "first")))
	rig.proj.RenderPending(context.Background())
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("push: %v", err)
	}
	theirs := rig.humanPushes("wolf", "NOTES.md", "a human's note")

	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("push with nothing of ours to publish: %v", err)
	}
	if rig.head("wolf") != theirs {
		t.Fatalf("the push loop did not fast-forward: local %s, remote %s",
			short(rig.head("wolf")), short(theirs))
	}
}

// A half-written render (staged index, no commit — a crash) must not be able to
// block the fast-forward for ever. The render is redone from the database.
func TestGitProjectionFastForwardClearsAHalfWrittenRender(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "first")))
	rig.proj.RenderPending(context.Background())
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("push: %v", err)
	}
	theirs := rig.humanPushes("wolf", "NOTES.md", "a human's note")

	// Simulate the crash: a tracked file changed and staged, never committed.
	readme := filepath.Join(rig.clone("wolf"), gitproj.DefaultSubfolder, "README.md")
	if err := os.WriteFile(readme, []byte("half-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runProjectionGit(t, rig.clone("wolf"), "add", "-A")

	if err := rig.proj.SyncProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("sync over a half-written render: %v", err)
	}
	if rig.head("wolf") != theirs {
		t.Fatalf("a staged leftover blocked the fast-forward: local %s, remote %s",
			short(rig.head("wolf")), short(theirs))
	}
}

// ── §E documents: the full content, and the ones that cannot be files ───────

// TestGitProjectionPublishesFullDocumentsNotSnippets is the reason the render
// path was moved onto loadGitDocuments (G14). SearchMemories answers with a
// 500-byte SNIPPET; publishing that would put a truncated document in the
// repository, and the importer would read it back as the document's real
// content — silently losing everything past the cut, permanently.
func TestGitProjectionPublishesFullDocumentsNotSnippets(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	long := strings.Repeat("the board says hold. ", 60) + "AND THE LAST LINE MATTERS."
	if len(long) <= 500 {
		t.Fatalf("fixture is not long enough to be truncated (%d bytes)", len(long))
	}
	rig.store.docs["wolf"] = []*agentdb.Memory{{
		ID:      "mem-1",
		Project: "wolf",
		Labels:  agentdb.LabelSet{agentdb.MemoryNameLabel: "message-board"},
		Content: long,
	}}

	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "why"))
	rig.proj.Hook()(context.Background(), ev)
	rig.proj.RenderPending(context.Background())

	published := readProjectionFile(t, rig, "wolf", "orange/memory/message-board.md")
	if !strings.Contains(published, "AND THE LAST LINE MATTERS.") {
		t.Fatalf("the published document is truncated — a snippet was rendered instead of the memory:\n%s", published)
	}
}

// TestGitProjectionSkipsUnfilableDocuments: a `name=` label may contain "." and
// "_", a path segment may not. Such a document is skipped and REPORTED — never
// silently dropped, and never fatal, because any worker holding memory_create
// could otherwise stop a whole project from projecting.
func TestGitProjectionSkipsUnfilableDocuments(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.store.docs["wolf"] = []*agentdb.Memory{
		{ID: "mem-1", Project: "wolf", Labels: agentdb.LabelSet{agentdb.MemoryNameLabel: "Message.Board"}, Content: "not a file name"},
		{ID: "mem-2", Project: "wolf", Labels: agentdb.LabelSet{agentdb.MemoryNameLabel: "message-board"}, Content: "a fine file name"},
	}

	ev := rig.store.mutate("wolf", workerPromptWrite("copywriter", "why"))
	rig.proj.Hook()(context.Background(), ev)
	rig.proj.RenderPending(context.Background())

	// The project still published, and the renderable document is in it.
	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("one badly-named document stopped the project rendering (%d commits)", n)
	}
	if got := readProjectionFile(t, rig, "wolf", "orange/memory/message-board.md"); !strings.Contains(got, "a fine file name") {
		t.Fatalf("the renderable document did not publish: %q", got)
	}

	// And the operator is told, on the state row and in the log.
	if note := rig.state.lastError("wolf"); !strings.Contains(note, "Message.Board") {
		t.Fatalf("the state row does not name the unpublished document: %q", note)
	}
	var logged bool
	for _, line := range rig.logs {
		if strings.Contains(line, "Message.Board") && strings.Contains(line, "not published") {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("the skip was not logged: %v", rig.logs)
	}
}

func readProjectionFile(t *testing.T, rig *projectionRig, project, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rig.clone(project), filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// ── 🔴 G27 / DI21: never render an empty configuration over somebody's export ─
//
// The data-loss path this guards, in full: render is hook-driven, import is
// not. An operator who sets git_remote on a FRESH project and points it at a
// folder that already holds an exported project — the obvious way to try to
// adopt one — fires the config hook. The render loop wakes, renders that
// project's EMPTY configuration, and WriteTree deletes every file under the
// subfolder that is not in the map, before any import or bootstrap could run.
// The push loop then publishes the deletion.
//
// TestGitProjectionRefusesToRenderOverAnUnimportedExport is the regression
// test. It has been watched fail: with the needsAdoption block removed from
// RenderProject, it reports the export's files GONE from the remote.

// seedExport publishes an already-exported project into a project's bare
// remote — what an operator points a fresh project's git_remote at when they
// mean "adopt this". It returns the contents, for a byte-for-byte comparison
// after the loops have had their chance at them.
func seedExport(t *testing.T, rig *projectionRig, project string, files map[string]string) map[string]string {
	t.Helper()
	work := filepath.Join(rig.root, "seed-"+project)
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	runProjectionGit(t, work, "init", "-b", "main", ".")
	for path, body := range files {
		full := filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runProjectionGit(t, work, "add", "-A")
	runProjectionGit(t, work, "-c", "user.name=Kai", "-c", "user.email=kai@example.com",
		"-c", "commit.gpgsign=false", "commit", "-m", "an exported project")
	runProjectionGit(t, work, "remote", "add", "origin", rig.remotes[project])
	runProjectionGit(t, work, "push", "-q", "origin", "main")
	return files
}

// remoteFile reads one path out of the BARE REMOTE's branch tip — not the local
// clone. The claim under test is about the operator's repository.
func remoteFile(t *testing.T, rig *projectionRig, project, path string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", "show", "main:"+path)
	cmd.Dir = rig.remotes[project]
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func remoteHead(t *testing.T, rig *projectionRig, project string) string {
	t.Helper()
	return strings.TrimSpace(runProjectionGit(t, rig.remotes[project], "rev-parse", "main"))
}

func (f *fakeProjectionState) lastErrorKind(project string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.row(project).LastErrorKind
}

func TestGitProjectionRefusesToRenderOverAnUnimportedExport(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	export := map[string]string{
		"orange/project.yaml":            "goal: sell the thing\n",
		"orange/workers/copywriter.md":   "---\nname: copywriter\n---\nthe prompt a human wrote\n",
		"orange/memory/message-board.md": "the board\n",
		"README.md":                      "not the projection's file\n",
	}
	seedExport(t, rig, "wolf", export)
	before := remoteHead(t, rig, "wolf")

	// One config event — setting git_remote is itself one — and both loops get
	// their turn, exactly as they would in agentd.
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())
	rig.proj.PushAll(context.Background())

	// 🔴 The whole point: every file the operator exported is still there, byte
	// for byte, and the remote has not moved.
	for path, want := range export {
		got, ok := remoteFile(t, rig, "wolf", path)
		if !ok {
			t.Fatalf("%s is GONE from the remote — the render deleted an operator's export", path)
		}
		if got != want {
			t.Fatalf("%s changed on the remote: %q → %q", path, want, got)
		}
	}
	if now := remoteHead(t, rig, "wolf"); now != before {
		t.Fatalf("the remote moved %s → %s: the projection published over an unimported export", before, now)
	}
	if head := rig.head("wolf"); head != before {
		t.Fatalf("the local clone committed on top of the export (%s → %s)", before, head)
	}

	// It is recorded as its own state, and the message names the way out.
	if got, want := rig.state.lastErrorKind("wolf"), agentdb.GitProjectionErrorNeedsAdoption; got != want {
		t.Fatalf("last_error_kind = %q, want %q", got, want)
	}
	reason := rig.state.lastError("wolf")
	if !strings.Contains(reason, "POST /agent/git-bootstrap") {
		t.Fatalf("the refusal does not tell the operator what to do next: %q", reason)
	}
	if !strings.Contains(reason, "nothing was deleted") {
		t.Fatalf("the refusal does not say the export is safe: %q", reason)
	}
	// The push loop must not have quietly cleared that row by marking a remote
	// it never pushed to as pushed.
	rig.state.mu.Lock()
	pushed := rig.state.row("wolf").LastPushedSHA
	rig.state.mu.Unlock()
	if pushed != "" {
		t.Fatalf("the push loop marked %s pushed and cleared the refusal", pushed)
	}
}

// The ordinary first run: a brand-new project against an empty remote. A guard
// that blocks this is worse than the bug it fixes.
func TestGitProjectionRendersIntoAnEmptyRemote(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())

	if n := rig.commitCount("wolf"); n != 1 {
		t.Fatalf("a brand-new project against an empty remote did not render (%d commits)", n)
	}
	if kind := rig.state.lastErrorKind("wolf"); kind != agentdb.GitProjectionErrorNone {
		t.Fatalf("the guard fired on the ordinary first run: kind %q, %q", kind, rig.state.lastError("wolf"))
	}
}

// A repository that has history but no orange/ yet — a project's own repo
// gaining a projection — is also the ordinary first run.
func TestGitProjectionRendersWhenTheRemoteHasNoSubfolderYet(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	seedExport(t, rig, "wolf", map[string]string{
		"README.md":   "somebody's application\n",
		"src/main.go": "package main\n",
	})

	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())

	if kind := rig.state.lastErrorKind("wolf"); kind != agentdb.GitProjectionErrorNone {
		t.Fatalf("the guard fired on a repository with no orange/ yet: kind %q, %q", kind, rig.state.lastError("wolf"))
	}
	if n := rig.commitCount("wolf"); n != 2 { // the seeded commit, plus ours
		t.Fatalf("want the projection's commit on top of the existing history, got %d commits", n)
	}
	// It added its own folder and touched nothing else.
	if _, err := os.Stat(filepath.Join(rig.clone("wolf"), "README.md")); err != nil {
		t.Fatalf("the render removed a file outside its subfolder: %v", err)
	}
}

// Once a project HAS imported, the guard must stop applying — otherwise it
// blocks every subsequent render for ever, which is a worse failure than the
// one it prevents.
func TestGitProjectionAdoptionGuardStopsAfterAnImport(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	seedExport(t, rig, "wolf", map[string]string{
		"orange/project.yaml": "goal: sell the thing\n",
	})
	// What a bootstrap or a webhook import leaves behind.
	if err := rig.state.MarkImported(context.Background(), "wolf", remoteHead(t, rig, "wolf")); err != nil {
		t.Fatal(err)
	}

	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())

	if kind := rig.state.lastErrorKind("wolf"); kind == agentdb.GitProjectionErrorNeedsAdoption {
		t.Fatalf("the guard latched on after an import: %q", rig.state.lastError("wolf"))
	}
	if n := rig.commitCount("wolf"); n != 2 {
		t.Fatalf("an adopted project did not render (%d commits)", n)
	}
}

// The same, by the other door: a project that has rendered once owns the
// folder, and must keep rendering into it for ever afterwards.
func TestGitProjectionAdoptionGuardDoesNotLatchAfterAFirstRender(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "first")))
	rig.proj.RenderPending(context.Background())
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("first push: %v", err)
	}
	// The remote's orange/ now has content — put there by us.
	if _, ok := remoteFile(t, rig, "wolf", "orange/README.md"); !ok {
		t.Fatal("setup: the first render did not publish into the subfolder")
	}

	// A second mutation that really does change the tree, so "did it render?"
	// is answered by a commit rather than by rule 3's no-change path.
	rig.store.mu.Lock()
	rig.store.workers["wolf"] = []*agentdb.Worker{{
		Project: "wolf", Name: "copywriter", SystemPrompt: "write well", Enabled: true,
	}}
	rig.store.mu.Unlock()
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "second")))
	rig.proj.RenderPending(context.Background())

	if kind := rig.state.lastErrorKind("wolf"); kind == agentdb.GitProjectionErrorNeedsAdoption {
		t.Fatalf("the guard fired on the project's OWN published folder: %q", rig.state.lastError("wolf"))
	}
	if n := rig.commitCount("wolf"); n != 2 {
		t.Fatalf("the second render did not commit (%d commits)", n)
	}
}

// The state reaches an operator through GET /agent/git-projection unchanged,
// bootstrap route and all. gitStatusBody drives the real httpapi handler.
func TestGitProjectionStatusReportsNeedsAdoption(t *testing.T) {
	msg := gitAdoptionRefusal(agentdb.DefaultGitSubfolder, agentdb.DefaultGitBranch)
	body := gitStatusBody(t, &fakeGitStatusStore{
		state: map[string]*agentdb.GitProjectionState{"wolf": {
			Project:       "wolf",
			LastError:     msg,
			LastErrorKind: agentdb.GitProjectionErrorNeedsAdoption,
		}},
	})
	got, _ := body["last_error"].(string)
	if !strings.Contains(got, "POST /agent/git-bootstrap") {
		t.Fatalf("last_error on the wire does not name the bootstrap route: %q", got)
	}
	if !strings.Contains(got, agentdb.DefaultGitSubfolder+"/") {
		t.Fatalf("last_error does not name the folder it refused to write: %q", got)
	}
	if body["health"] == httpapi.GitProjectionOK {
		t.Fatalf("health = ok while the projection is publishing nothing: %v", body)
	}
}

// A kind agentdb does not know must not be silently downgraded to "other" —
// that is the whole reason the column exists.
func TestGitProjectionNeedsAdoptionKindSurvivesTheStore(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	if err := rig.state.NoteFailureKind(context.Background(), "wolf",
		agentdb.GitProjectionErrorNeedsAdoption, "because"); err != nil {
		t.Fatal(err)
	}
	if got := rig.state.lastErrorKind("wolf"); got != agentdb.GitProjectionErrorNeedsAdoption {
		t.Fatalf("kind = %q", got)
	}
}

// ── the smaller leak: noisy for ever ────────────────────────────────────────

// A remote that has gone away must be reported, and then stop being reported.
// Repeating the same sentence on every boot and every tick is how the next real
// failure becomes invisible.
func TestGitProjectionFetchFailureIsSaidOnceNotForEver(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "why")))
	rig.proj.RenderPending(context.Background())

	// Delete the remote out from under a project that still has git_remote set:
	// exactly the "first push never completed" shape.
	if err := os.RemoveAll(rig.remotes["wolf"]); err != nil {
		t.Fatal(err)
	}
	rig.logs = nil
	for i := 0; i < 5; i++ {
		rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", fmt.Sprintf("try %d", i))))
		rig.proj.RenderPending(context.Background())
		rig.proj.PushAll(context.Background())
	}

	fetchLines := 0
	for _, line := range rig.logs {
		if strings.Contains(line, "fetch failed") {
			fetchLines++
		}
	}
	if fetchLines == 0 {
		t.Fatalf("the unreachable remote was never reported at all: %v", rig.logs)
	}
	if fetchLines > 1 {
		t.Fatalf("the same fetch failure was logged %d times across 5 ticks — noisy for ever:\n%s",
			fetchLines, strings.Join(rig.logs, "\n"))
	}
	if !strings.Contains(strings.Join(rig.logs, "\n"), "not be repeated") {
		t.Fatalf("the one line does not say it will not be repeated: %v", rig.logs)
	}
}

// A clone directory that has vanished must be re-created, not fetched against
// for ever.
func TestGitProjectionRecreatesAVanishedClone(t *testing.T) {
	rig := newProjectionRig(t, "wolf")
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "first")))
	rig.proj.RenderPending(context.Background())
	if err := rig.proj.PushProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Somebody clears the clone root — a wiped volume, a reaped tmpdir. The
	// projector still holds the cached *gitproj.Repo pointing at it.
	if err := os.RemoveAll(rig.clone("wolf")); err != nil {
		t.Fatal(err)
	}

	rig.store.mu.Lock()
	rig.store.workers["wolf"] = []*agentdb.Worker{{
		Project: "wolf", Name: "copywriter", SystemPrompt: "write well", Enabled: true,
	}}
	rig.store.mu.Unlock()
	rig.proj.Hook()(context.Background(), rig.store.mutate("wolf", workerPromptWrite("copywriter", "second")))
	rig.proj.RenderPending(context.Background())

	if _, err := os.Stat(filepath.Join(rig.clone("wolf"), ".git")); err != nil {
		t.Fatalf("the clone was not re-created: %v", err)
	}
	// Re-cloned, fetched, and rendered forward: the new worker is a real tree
	// change, so it commits on top of the published history rather than
	// starting a fresh one.
	if n := rig.commitCount("wolf"); n != 2 {
		t.Fatalf("after re-cloning, want the published commit plus the new one, got %d", n)
	}
	if kind := rig.state.lastErrorKind("wolf"); kind != agentdb.GitProjectionErrorNone {
		t.Fatalf("a recoverable missing clone was recorded as a failure: %q / %q", kind, rig.state.lastError("wolf"))
	}
}
