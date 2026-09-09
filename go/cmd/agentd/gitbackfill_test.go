package main

// Tests for G10, the backfill: a project with history gets a git history, one
// commit per config event, in seq order.
//
// Everything here runs against a local bare repository in t.TempDir() and
// in-memory fakes — no network, no database, no Docker.
//
// Four of these exist because the design says the code would be wrong without
// them:
//
//   - TestGitBackfillReplaysTheLogInSeqOrder is the whole point: `git log` must
//     BE the config log, subjects and trailers included, in seq order.
//   - TestGitBackfillNoOpEventCommitsNothingAndStillAdvances is the property
//     that stops a mutation which changes nothing renderable from either
//     producing an empty commit or stalling the walk forever.
//   - TestGitBackfillResumesAfterACrashWithoutDuplicating is rule 2. It crashes
//     in the real window — commit landed, watermark did not.
//   - TestGitBackfillStopsAtAnUnrenderableState is rule 1: NOT skip-and-carry-on,
//     because every later commit would then rest on a state that never existed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/gitproj"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// backfillStore is the read side: a config log, a settings row and the named
// documents. The live-render reads (workers, skills, …) are never called by the
// backfill — it reads the log and folds — so they answer empty, and a test that
// started depending on them would be testing the wrong path.
type backfillStore struct {
	mu       sync.Mutex
	settings map[string]*agentdb.ProjectSettings
	events   map[string][]*agentdb.ConfigEvent // ascending seq
	docs     map[string][]*agentdb.Memory
	pages    int // how many ListConfigEvents calls have been served
}

func newBackfillStore() *backfillStore {
	return &backfillStore{
		settings: map[string]*agentdb.ProjectSettings{},
		events:   map[string][]*agentdb.ConfigEvent{},
		docs:     map[string][]*agentdb.Memory{},
	}
}

func (f *backfillStore) GetProjectSettings(_ context.Context, project string) (*agentdb.ProjectSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ps, ok := f.settings[project]; ok {
		copied := *ps
		return &copied, nil
	}
	return agentdb.DefaultProjectSettings(project), nil
}

func (f *backfillStore) ListWorkers(context.Context, string) ([]*agentdb.Worker, error) { return nil, nil }
func (f *backfillStore) ListProjectSkills(context.Context, agentdb.SkillCatalogQuery) ([]*agentdb.SkillSummary, error) {
	return nil, nil
}
func (f *backfillStore) GetProjectSkill(context.Context, string, string) (*agentdb.Skill, error) {
	return nil, nil
}
func (f *backfillStore) ListSubscriptions(context.Context, string) ([]*agentdb.Subscription, error) {
	return nil, nil
}
func (f *backfillStore) ListSchedules(context.Context, string) ([]*agentdb.Schedule, error) {
	return nil, nil
}
func (f *backfillStore) ListCustomImageVersions(context.Context, agentdb.ImageCatalogQuery) ([]*agentdb.CustomImage, error) {
	return nil, nil
}

func (f *backfillStore) SearchMemories(_ context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*agentdb.MemorySearchResult
	for _, m := range f.docs[q.Project] {
		out = append(out, &agentdb.MemorySearchResult{ID: m.ID, Labels: m.Labels, Snippet: m.Content})
	}
	return out, nil
}

// GetMemory re-reads one memory IN FULL — the half of §E's load that
// SearchMemories cannot do, because its rows carry a 500-byte snippet.
func (f *backfillStore) GetMemory(_ context.Context, project, id string) (*agentdb.Memory, error) {
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

// ListConfigEvents answers NEWEST FIRST and honours Limit and BeforeSeq, which
// is what the real store does and what the backfill's paging depends on.
func (f *backfillStore) ListConfigEvents(_ context.Context, q agentdb.ConfigEventQuery) ([]*agentdb.ConfigEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages++
	all := append([]*agentdb.ConfigEvent(nil), f.events[q.Project]...)
	sort.Slice(all, func(i, j int) bool { return all[i].Seq > all[j].Seq })
	var out []*agentdb.ConfigEvent
	for _, ev := range all {
		if q.BeforeSeq > 0 && ev.Seq >= q.BeforeSeq {
			continue
		}
		out = append(out, ev)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

var _ gitProjectionStore = (*backfillStore)(nil)

// project registers a project that renders to a remote.
func (f *backfillStore) project(name, remote string) *agentdb.ProjectSettings {
	f.mu.Lock()
	defer f.mu.Unlock()
	ps := agentdb.DefaultProjectSettings(name)
	ps.GitRemote = remote
	ps.GitBranch = "main"
	ps.GitSubfolder = "orange"
	f.settings[name] = ps
	return ps
}

// append adds a config event with the next seq, exactly as the store's own
// in-transaction allocation would.
func (f *backfillStore) append(project string, ev *agentdb.ConfigEvent) *agentdb.ConfigEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev.Project = project
	ev.Seq = int64(len(f.events[project]) + 1)
	if ev.ID == "" {
		ev.ID = fmt.Sprintf("ev-%s-%d", project, ev.Seq)
	}
	if ev.CreatedAt == 0 {
		ev.CreatedAt = time.Now().UnixMilli()
	}
	f.events[project] = append(f.events[project], ev)
	return ev
}

// backfillState is the durable half: watermark, lease and last failure.
type backfillState struct {
	mu   sync.Mutex
	rows map[string]*gitProjectionRecord
	// failMarkRenderedAt makes MarkRendered fail once, at one seq. That is the
	// real crash window — the commit landed and the watermark did not — and it
	// is the only way to test resumability without killing a process.
	failMarkRenderedAt int64
	projects           []string
}

func newBackfillState(projects ...string) *backfillState {
	s := &backfillState{rows: map[string]*gitProjectionRecord{}, projects: projects}
	for _, p := range projects {
		s.rows[p] = &gitProjectionRecord{Project: p}
	}
	return s
}

func (s *backfillState) row(project string) *gitProjectionRecord {
	if s.rows[project] == nil {
		s.rows[project] = &gitProjectionRecord{Project: project}
	}
	return s.rows[project]
}

func (s *backfillState) Get(_ context.Context, project string) (gitProjectionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.row(project), nil
}

func (s *backfillState) AcquireLease(_ context.Context, project, owner string, until int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(project)
	now := time.Now().Unix()
	if r.LeaseOwner != "" && r.LeaseOwner != owner && r.LeaseExpiresAt >= now {
		return false, nil
	}
	r.LeaseOwner, r.LeaseExpiresAt = owner, until
	return true, nil
}

func (s *backfillState) ReleaseLease(_ context.Context, project, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(project)
	if r.LeaseOwner == owner {
		r.LeaseOwner, r.LeaseExpiresAt = "", 0
	}
	return nil
}

func (s *backfillState) MarkRendered(_ context.Context, project string, seq int64, sha string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failMarkRenderedAt != 0 && seq == s.failMarkRenderedAt {
		s.failMarkRenderedAt = 0 // one crash, then the world comes back
		return errors.New("simulated crash: the watermark write did not land")
	}
	r := s.row(project)
	r.LastRenderedSeq, r.LastRenderedSHA = seq, sha
	r.LastError, r.LastErrorAt = "", 0
	return nil
}

func (s *backfillState) MarkPushed(_ context.Context, project, sha string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row(project).LastPushedSHA = sha
	return nil
}

// MarkImported is the INBOUND watermark, which the backfill never touches: git
// is not a write path, and a replay of the config log imports nothing.
func (s *backfillState) MarkImported(_ context.Context, project, sha string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row(project).LastImportedSHA = sha
	return nil
}

func (s *backfillState) NoteFailure(ctx context.Context, project, reason string) error {
	return s.NoteFailureKind(ctx, project, gitProjectionErrorKindFromText(reason), reason)
}

func (s *backfillState) NoteFailureKind(_ context.Context, project, kind, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(project)
	r.LastError, r.LastErrorAt, r.LastErrorKind = reason, time.Now().Unix(), kind
	return nil
}

// PutNotes: the backfill writes none — it replays the config log, it does not
// import anything — but the seam it uses carries them (G23).
func (s *backfillState) PutNotes(context.Context, string, string, []agentdb.GitProjectionNote) error {
	return nil
}

func (s *backfillState) ProjectsWithRemote(context.Context) ([]string, error) {
	return append([]string(nil), s.projects...), nil
}

func (s *backfillState) lastError(project string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.row(project).LastError
}

func (s *backfillState) renderedSeq(project string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.row(project).LastRenderedSeq
}

var _ gitProjectionState = (*backfillState)(nil)

// ── the harness ─────────────────────────────────────────────────────────────

type backfillRig struct {
	t         *testing.T
	proj      *gitProjector
	store     *backfillStore
	state     *backfillState
	cloneRoot string
	logs      []string
}

func newBackfillRig(t *testing.T, projects ...string) *backfillRig {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	rig := &backfillRig{
		t:         t,
		store:     newBackfillStore(),
		state:     newBackfillState(projects...),
		cloneRoot: filepath.Join(root, "clones"),
	}
	for _, p := range projects {
		bare := filepath.Join(root, p+".git")
		cmd := exec.Command("git", "init", "--bare", "-b", "main", bare)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init --bare: %v: %s", err, out)
		}
		rig.store.project(p, bare)
	}
	proj, err := newGitProjector(gitProjectorConfig{
		Store:  rig.store,
		State:  rig.state,
		Root:   rig.cloneRoot,
		Owner:  "backfill-test/1",
		Getenv: func(string) string { return "" },
		Logf:   func(f string, a ...any) { rig.logs = append(rig.logs, fmt.Sprintf(f, a...)) },
	})
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	rig.proj = proj
	return rig
}

func (r *backfillRig) clone(project string) string {
	return filepath.Join(r.cloneRoot, gitCloneDirName(project))
}

// log returns every commit on the branch, OLDEST FIRST, as "<subject>" plus the
// trailers git itself parses (never a grep — DI4).
type backfillCommit struct {
	subject  string
	body     string
	trailers map[string]string
}

func (r *backfillRig) history(project string) []backfillCommit {
	r.t.Helper()
	dir := r.clone(project)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil
	}
	out, _, err := runGit(context.Background(), dir, "log", "--reverse", "--format=%H")
	if err != nil {
		return nil
	}
	var commits []backfillCommit
	for _, sha := range strings.Fields(out) {
		subject, _, err := runGit(context.Background(), dir, "log", "-1", "--format=%s", sha)
		if err != nil {
			r.t.Fatalf("read subject of %s: %v", sha, err)
		}
		body, _, err := runGit(context.Background(), dir, "log", "-1", "--format=%b", sha)
		if err != nil {
			r.t.Fatalf("read body of %s: %v", sha, err)
		}
		c := backfillCommit{subject: strings.TrimSpace(subject), body: body, trailers: map[string]string{}}
		for _, key := range []string{"Orange-Project", "Orange-Seq", "Orange-Event", "Orange-Action", "Orange-Actor-Worker", "Orange-Actor-Session"} {
			v, _, err := runGit(context.Background(), dir, "log", "-1",
				"--format=%(trailers:key="+key+",valueonly,only)", sha)
			if err != nil {
				r.t.Fatalf("read trailer %s: %v", key, err)
			}
			if s := strings.TrimSpace(v); s != "" {
				c.trailers[key] = s
			}
		}
		commits = append(commits, c)
	}
	return commits
}

// fileAtHead reads one repo path out of the checked-out tree.
func (r *backfillRig) fileAtHead(project, path string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(r.clone(project), filepath.FromSlash(path)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ── payload builders: the events a real store would have written ────────────

func backfillWorkerEvent(action string, w *agentdb.Worker, rationale, actor string) *agentdb.ConfigEvent {
	return &agentdb.ConfigEvent{
		Action:       action,
		Payload:      backfillPayload(w),
		Rationale:    rationale,
		ActorWorker:  actor,
		ActorSession: "sess-" + actor,
	}
}

func backfillSettingsEvent(ps *agentdb.ProjectSettings, rationale string) *agentdb.ConfigEvent {
	return &agentdb.ConfigEvent{
		Action:    agentdb.ActionProjectSettingsPut,
		Payload:   backfillPayload(ps),
		Rationale: rationale,
	}
}

// backfillPayload marshals a row the way agentdb.configPayload does, so the
// test's payloads have exactly the shape the fold will meet in production.
func backfillPayload(v any) agentdb.JSONMap {
	var m agentdb.JSONMap
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m
}

func backfillWorker(name, prompt string) *agentdb.Worker {
	w := agentdb.NewWorker("wolf", name)
	w.SystemPrompt = prompt
	return w
}

// ── 🔴 the point of the ticket: git log IS the config log ───────────────────

func TestGitBackfillReplaysTheLogInSeqOrder(t *testing.T) {
	rig := newBackfillRig(t, "wolf")

	ps := agentdb.DefaultProjectSettings("wolf")
	ps.GitRemote = rig.store.settings["wolf"].GitRemote
	ps.GitBranch, ps.GitSubfolder = "main", "orange"
	ps.SystemPrompt = "we trade hypotheses"

	want := []struct{ action, subject, actor string }{
		// A singleton entity has no key, so the subject is the bare action.
		{agentdb.ActionProjectSettingsPut, "project_settings_put", ""},
		{agentdb.ActionWorkerCreate, "worker_create: copywriter", "architect"},
		{agentdb.ActionWorkerCreate, "worker_create: reviewer", "architect"},
		{agentdb.ActionWorkerPromptWrite, "worker_prompt_write: copywriter", "architect"},
		{agentdb.ActionWorkerDelete, "worker_delete: reviewer", "architect"},
	}

	rig.store.append("wolf", backfillSettingsEvent(ps, "point the project at its repo"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerCreate, backfillWorker("copywriter", "v1"), "the roster needs a writer", "architect"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerCreate, backfillWorker("reviewer", "check it"), "and a reviewer", "architect"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite, backfillWorker("copywriter", "v2 — lead with the offer"), "the old prompt buried the offer", "architect"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerDelete, backfillWorker("reviewer", "check it"), "folded into the writer", "architect"))

	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	got := rig.history("wolf")
	if len(got) != len(want) {
		t.Fatalf("want %d commits (one per config event), got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		c := got[i]
		if c.subject != w.subject {
			t.Errorf("commit %d subject = %q, want %q", i+1, c.subject, w.subject)
		}
		if c.trailers["Orange-Seq"] != strconv.Itoa(i+1) {
			t.Errorf("commit %d Orange-Seq = %q, want %d — seq order IS commit order", i+1, c.trailers["Orange-Seq"], i+1)
		}
		if c.trailers["Orange-Action"] != w.action {
			t.Errorf("commit %d Orange-Action = %q, want %q", i+1, c.trailers["Orange-Action"], w.action)
		}
		if c.trailers["Orange-Project"] != "wolf" {
			t.Errorf("commit %d Orange-Project = %q, want wolf", i+1, c.trailers["Orange-Project"])
		}
		if c.trailers["Orange-Event"] != fmt.Sprintf("ev-wolf-%d", i+1) {
			t.Errorf("commit %d Orange-Event = %q, want ev-wolf-%d", i+1, c.trailers["Orange-Event"], i+1)
		}
		if c.trailers["Orange-Actor-Worker"] != w.actor {
			t.Errorf("commit %d Orange-Actor-Worker = %q, want %q", i+1, c.trailers["Orange-Actor-Worker"], w.actor)
		}
	}
	// The rationale of THAT event is the body of THAT commit.
	if !strings.Contains(got[3].body, "the old prompt buried the offer") {
		t.Errorf("commit 4 body does not carry its own rationale: %q", got[3].body)
	}

	// The end state is the configuration as it stands: the deleted worker's file
	// is gone, the surviving one carries the newest prompt.
	if _, ok := rig.fileAtHead("wolf", "orange/workers/reviewer.md"); ok {
		t.Error("the deleted worker's file survived the replay; a tombstone must remove the key")
	}
	body, ok := rig.fileAtHead("wolf", "orange/workers/copywriter.md")
	if !ok {
		t.Fatal("orange/workers/copywriter.md was not rendered")
	}
	if !strings.Contains(body, "v2 — lead with the offer") {
		t.Errorf("the worker file does not hold the newest prompt:\n%s", body)
	}

	// And the watermark hands the live loop a project that is caught up.
	if got, want := rig.state.renderedSeq("wolf"), int64(5); got != want {
		t.Errorf("watermark = %d, want %d", got, want)
	}
}

// A no-op mutation — one that changes nothing the allowlist publishes — must
// produce no commit, and the walk must still advance past it.
func TestGitBackfillNoOpEventCommitsNothingAndStillAdvances(t *testing.T) {
	rig := newBackfillRig(t, "wolf")

	w := backfillWorker("copywriter", "v1")
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerCreate, w, "create", "architect"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite, backfillWorker("copywriter", "v2"), "sharpen", "architect"))
	// The same row again, with only a runtime timestamp moved. UpdatedAt is
	// deliberately not renderable (DI8: a rendered clock would commit forever),
	// so this event has nothing to publish. It is LAST on purpose: a walk that
	// only advanced its watermark on a commit would leave the project stuck
	// here, replaying this event on every run for ever.
	same := backfillWorker("copywriter", "v2")
	same.UpdatedAt = time.Now().Unix() + 60
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerUpdate, same, "touch", "architect"))

	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	got := rig.history("wolf")
	if len(got) != 2 {
		t.Fatalf("want 2 commits for 3 events (one changed nothing renderable), got %d: %+v", len(got), got)
	}
	if got[0].trailers["Orange-Seq"] != "1" || got[1].trailers["Orange-Seq"] != "2" {
		t.Errorf("commits are seq %s and %s; want 1 and 2 — the no-op event must not commit",
			got[0].trailers["Orange-Seq"], got[1].trailers["Orange-Seq"])
	}
	// 🔴 The walk advanced past the no-op.
	if got, want := rig.state.renderedSeq("wolf"), int64(3); got != want {
		t.Errorf("watermark = %d, want %d — a no-op event must still advance the watermark, or the walk stalls on it for ever", got, want)
	}
	// And a second run has nothing left to do.
	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n := len(rig.history("wolf")); n != 2 {
		t.Errorf("a re-run produced %d commits, want 2", n)
	}
}

// Rule 2: interrupted after k commits, a re-run continues and duplicates
// nothing. The crash is in the real window — the commit landed, the watermark
// write did not.
func TestGitBackfillResumesAfterACrashWithoutDuplicating(t *testing.T) {
	rig := newBackfillRig(t, "wolf")
	for i := 1; i <= 5; i++ {
		rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite,
			backfillWorker("copywriter", fmt.Sprintf("v%d", i)), fmt.Sprintf("revision %d", i), "architect"))
	}
	rig.state.failMarkRenderedAt = 3 // crash after the third commit

	err := rig.proj.BackfillProject(context.Background(), "wolf")
	if err == nil {
		t.Fatal("want the interrupted run to report the failure, got nil")
	}
	if n := len(rig.history("wolf")); n != 3 {
		t.Fatalf("after the crash want 3 commits, got %d", n)
	}
	if got := rig.state.renderedSeq("wolf"); got != 2 {
		t.Fatalf("after the crash the watermark is %d, want 2 (the commit landed, the watermark did not)", got)
	}

	// Re-run: it resumes at seq 3, re-renders the same tree, finds nothing
	// changed, and carries on — no duplicate commit for seq 3.
	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("resumed backfill: %v", err)
	}
	got := rig.history("wolf")
	if len(got) != 5 {
		t.Fatalf("want 5 commits after the resume, got %d: %+v", len(got), got)
	}
	var seqs []string
	for _, c := range got {
		seqs = append(seqs, c.trailers["Orange-Seq"])
	}
	if strings.Join(seqs, ",") != "1,2,3,4,5" {
		t.Errorf("commit seqs = %s, want 1,2,3,4,5 with no duplicate", strings.Join(seqs, ","))
	}
	if got, want := rig.state.renderedSeq("wolf"), int64(5); got != want {
		t.Errorf("watermark = %d, want %d", got, want)
	}

	// Idempotent: a third run over the same log changes nothing at all.
	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("third run: %v", err)
	}
	if n := len(rig.history("wolf")); n != 5 {
		t.Errorf("a re-run over an up-to-date log produced %d commits, want 5", n)
	}
}

// 🔴 Rule 1: an unrenderable state STOPS the backfill. Not skip-and-carry-on:
// every later commit would rest on a state the project never passed through.
func TestGitBackfillStopsAtAnUnrenderableState(t *testing.T) {
	rig := newBackfillRig(t, "wolf")
	const secret = "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX"

	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerCreate, backfillWorker("copywriter", "v1"), "create", "architect"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite, backfillWorker("copywriter", "v2"), "sharpen", "architect"))

	bad := agentdb.DefaultProjectSettings("wolf")
	bad.GitRemote = rig.store.settings["wolf"].GitRemote
	// A Slack incoming-webhook URL IS a bearer token (§D). A literal one is a
	// render refusal, not a redaction.
	bad.AttentionChannel = agentdb.JSONMap{"kind": "webhook", "url": secret}
	rig.store.append("wolf", backfillSettingsEvent(bad, "wire up alerts"))

	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite, backfillWorker("copywriter", "v3"), "later still", "architect"))

	err := rig.proj.BackfillProject(context.Background(), "wolf")
	if err == nil {
		t.Fatal("an unrenderable state backfilled without complaint")
	}
	if !errors.Is(err, gitproj.ErrUnrenderable) {
		t.Fatalf("error does not wrap ErrUnrenderable, so a caller cannot tell a refusal from an outage: %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "hooks.slack.com") {
		t.Errorf("the failure quotes the secret it refused to publish: %v", err)
	}

	// What was already committed is intact, and nothing past the refusal ran.
	got := rig.history("wolf")
	if len(got) != 2 {
		t.Fatalf("want the 2 commits made before the refusal, got %d: %+v", len(got), got)
	}
	if got[1].trailers["Orange-Seq"] != "2" {
		t.Errorf("last commit is seq %s, want 2 — nothing after the refusal may be committed", got[1].trailers["Orange-Seq"])
	}
	if got, want := rig.state.renderedSeq("wolf"), int64(2); got != want {
		t.Errorf("watermark = %d, want %d — it must name the last event that DID publish", got, want)
	}

	// The reason is recorded, and it names the seq the walk stopped at.
	reason := rig.state.lastError("wolf")
	if !strings.Contains(reason, "seq 3") || !strings.Contains(reason, "project_settings_put") {
		t.Errorf("recorded reason does not say where the walk stopped: %q", reason)
	}
	if strings.Contains(reason, secret) {
		t.Errorf("the recorded reason quotes the secret: %q", reason)
	}
}

// The fold is agentdb's, not a second copy: a settings row and a later
// prompt-write describe ONE object, so settings.md carries both.
func TestGitBackfillFoldsTheProjectPromptOntoTheSettingsRow(t *testing.T) {
	rig := newBackfillRig(t, "wolf")

	ps := agentdb.DefaultProjectSettings("wolf")
	ps.GitRemote = rig.store.settings["wolf"].GitRemote
	ps.SystemPrompt = "first prompt"
	ps.MaxConcurrentJobs = 7
	rig.store.append("wolf", backfillSettingsEvent(ps, "settings"))
	rig.store.append("wolf", &agentdb.ConfigEvent{
		Action:    agentdb.ActionProjectPromptWrite,
		Payload:   agentdb.JSONMap{"project": "wolf", "system_prompt": "second prompt"},
		Rationale: "sharpen the project prompt",
	})

	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	body, ok := rig.fileAtHead("wolf", "orange/settings.md")
	if !ok {
		t.Fatal("orange/settings.md was not rendered")
	}
	if !strings.Contains(body, "second prompt") {
		t.Errorf("settings.md does not carry the newest project prompt:\n%s", body)
	}
	if !strings.Contains(body, "max_concurrent_jobs: 7") {
		t.Errorf("the prompt write dropped the rest of the settings row:\n%s", body)
	}
}

// Memory documents are rendered as they stand NOW — the fold replays
// configuration and cannot replay memories. The choice is recorded here so a
// later reader sees it was a decision: the tree the backfill leaves behind must
// equal what the live renderer produces, or the watermark lies.
func TestGitBackfillRendersDocumentsAsTheyStandNow(t *testing.T) {
	rig := newBackfillRig(t, "wolf")
	rig.store.docs["wolf"] = []*agentdb.Memory{{
		ID:      "mem-1",
		Project: "wolf",
		Labels:  agentdb.LabelSet{agentdb.MemoryNameLabel: "message-board", "kind": "board"},
		Content: "The board says: hold.",
	}}
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerCreate, backfillWorker("copywriter", "v1"), "create", "architect"))
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite, backfillWorker("copywriter", "v2"), "sharpen", "architect"))

	if err := rig.proj.BackfillProject(context.Background(), "wolf"); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	body, ok := rig.fileAtHead("wolf", "orange/memory/message-board.md")
	if !ok {
		t.Fatal("the named document was not rendered; the backfilled tree must equal what the live renderer produces")
	}
	if !strings.Contains(body, "The board says: hold.") {
		t.Errorf("document content is not the current one:\n%s", body)
	}
	// It appears once, at the first backfilled commit, and never changes again:
	// no fabricated history of a document is written.
	out, _, err := runGit(context.Background(), rig.clone("wolf"),
		"log", "--format=%H", "--", "orange/memory/message-board.md")
	if err != nil {
		t.Fatalf("git log for the document: %v", err)
	}
	if n := len(strings.Fields(out)); n != 1 {
		t.Errorf("the document was touched by %d commits, want exactly 1 — nothing may invent when it was written", n)
	}
}

// A project with no remote does not project, and a project already at the tip
// of its log has nothing to replay.
func TestGitBackfillPendingSkipsWhatItShould(t *testing.T) {
	rig := newBackfillRig(t, "wolf")
	rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerCreate, backfillWorker("copywriter", "v1"), "create", "architect"))

	if err := rig.proj.BackfillPending(context.Background()); err != nil {
		t.Fatalf("backfill pending: %v", err)
	}
	if n := len(rig.history("wolf")); n != 1 {
		t.Fatalf("want 1 commit, got %d", n)
	}
	// Second pass: caught up, so nothing happens.
	if err := rig.proj.BackfillPending(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if n := len(rig.history("wolf")); n != 1 {
		t.Errorf("a caught-up project was replayed again: %d commits", n)
	}

	// Projection off: no remote, no commits, no error.
	off := newBackfillRig(t, "bare")
	off.store.settings["bare"].GitRemote = ""
	off.store.append("bare", backfillWorkerEvent(agentdb.ActionWorkerCreate, backfillWorker("copywriter", "v1"), "create", "architect"))
	if err := off.proj.BackfillProject(context.Background(), "bare"); err != nil {
		t.Fatalf("a project with no remote must be a quiet no-op, got %v", err)
	}
	if n := len(off.history("bare")); n != 0 {
		t.Errorf("a project with no remote produced %d commits", n)
	}
}

// The log is paged newest-first with a BeforeSeq cursor; the walk must still
// come out ascending and complete.
func TestGitBackfillPagesTheWholeLogAscending(t *testing.T) {
	rig := newBackfillRig(t, "wolf")
	const n = gitBackfillPageSize + 7
	for i := 1; i <= n; i++ {
		rig.store.append("wolf", backfillWorkerEvent(agentdb.ActionWorkerPromptWrite,
			backfillWorker("copywriter", fmt.Sprintf("v%d", i)), fmt.Sprintf("revision %d", i), "architect"))
	}
	events, err := rig.proj.backfillEvents(context.Background(), "wolf")
	if err != nil {
		t.Fatalf("read the log: %v", err)
	}
	if len(events) != n {
		t.Fatalf("read %d events, want %d", len(events), n)
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d; the walk must be ascending by seq", i, ev.Seq)
		}
	}
	if rig.store.pages < 2 {
		t.Errorf("the log was read in %d call(s); this test exists to exercise paging", rig.store.pages)
	}
}
