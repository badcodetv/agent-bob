package main

// Tests for the inbound git door (G11). Everything here runs against a local
// bare repository in t.TempDir() and an in-memory store: no network, no
// database, no Docker.
//
// Three of these tests exist because the design says the code would be wrong
// without them, not because the code looked untested:
//
//   - TestGitImportFieldMergeKeepsUnrenderedFields is the DI2 regression. Build
//     a Worker out of frontmatter and hand it to UpsertWorker and every field
//     the projection does not render is wiped, silently, on every human commit.
//   - TestGitImportQuarantineWritesNothing is rule 3: one bad file among good
//     ones must leave the configuration exactly as it was.
//   - TestGitImportTrailerForgeryIsStillImported is DI4. A `grep` for
//     "Bob-Seq:" passes every test written from our own commits and fails
//     this one, which is built from a forged rationale.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

const gitImportProject = "wolf"

// ── the fake store ──────────────────────────────────────────────────────────

type gitImportCall struct {
	Method string
	Name   string
	CW     agentdb.ConfigWrite
}

type fakeGitImportStore struct {
	workers  map[string]agentdb.Worker
	settings agentdb.ProjectSettings
	skills   map[string]agentdb.Skill
	subs     map[string]agentdb.Subscription
	scheds   map[string]agentdb.Schedule
	memories []agentdb.Memory

	calls []gitImportCall
	// failMethod makes that one method return an error, for the partial-apply
	// case.
	failMethod string
}

func newFakeGitImportStore() *fakeGitImportStore {
	return &fakeGitImportStore{
		workers:  map[string]agentdb.Worker{},
		settings: *agentdb.DefaultProjectSettings(gitImportProject),
		skills:   map[string]agentdb.Skill{},
		subs:     map[string]agentdb.Subscription{},
		scheds:   map[string]agentdb.Schedule{},
	}
}

func (f *fakeGitImportStore) note(method, name string, cw agentdb.ConfigWrite) error {
	f.calls = append(f.calls, gitImportCall{Method: method, Name: name, CW: cw})
	if f.failMethod == method {
		return fmt.Errorf("fake store: %s failed", method)
	}
	return nil
}

func (f *fakeGitImportStore) writes() []gitImportCall {
	var out []gitImportCall
	for _, c := range f.calls {
		if strings.HasPrefix(c.Method, "Get") || strings.HasPrefix(c.Method, "List") || c.Method == "NewestMemory" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func (f *fakeGitImportStore) methods() []string {
	var out []string
	for _, c := range f.writes() {
		out = append(out, c.Method)
	}
	return out
}

func (f *fakeGitImportStore) GetWorker(_ context.Context, project, name string) (*agentdb.Worker, error) {
	w, ok := f.workers[name]
	if !ok {
		return nil, agentdb.ErrWorkerNotFound
	}
	return &w, nil
}

func (f *fakeGitImportStore) UpsertWorker(_ context.Context, w *agentdb.Worker, cw agentdb.ConfigWrite) (*agentdb.Worker, error) {
	if err := f.note("UpsertWorker", w.Name, cw); err != nil {
		return nil, err
	}
	f.workers[w.Name] = *w
	return w, nil
}

func (f *fakeGitImportStore) SetWorkerPrompt(_ context.Context, project, name, prompt string, cw agentdb.ConfigWrite) (*agentdb.Worker, string, error) {
	if err := f.note("SetWorkerPrompt", name, cw); err != nil {
		return nil, "", err
	}
	w, ok := f.workers[name]
	if !ok {
		return nil, "", agentdb.ErrWorkerNotFound
	}
	prev := w.SystemPrompt
	w.SystemPrompt = prompt
	f.workers[name] = w
	return &w, prev, nil
}

func (f *fakeGitImportStore) DeleteWorker(_ context.Context, project, name string, cw agentdb.ConfigWrite) error {
	if err := f.note("DeleteWorker", name, cw); err != nil {
		return err
	}
	delete(f.workers, name)
	return nil
}

func (f *fakeGitImportStore) GetProjectSettings(context.Context, string) (*agentdb.ProjectSettings, error) {
	ps := f.settings
	return &ps, nil
}

func (f *fakeGitImportStore) PutProjectSettings(_ context.Context, ps *agentdb.ProjectSettings, cw agentdb.ConfigWrite) (*agentdb.ProjectSettings, error) {
	if err := f.note("PutProjectSettings", ps.Project, cw); err != nil {
		return nil, err
	}
	f.settings = *ps
	return ps, nil
}

func (f *fakeGitImportStore) SetProjectPrompt(_ context.Context, project, prompt string, cw agentdb.ConfigWrite) (*agentdb.ProjectSettings, string, error) {
	if err := f.note("SetProjectPrompt", project, cw); err != nil {
		return nil, "", err
	}
	prev := f.settings.SystemPrompt
	f.settings.SystemPrompt = prompt
	ps := f.settings
	return &ps, prev, nil
}

func (f *fakeGitImportStore) GetProjectSkill(_ context.Context, project, name string) (*agentdb.Skill, error) {
	sk, ok := f.skills[name]
	if !ok {
		return nil, agentdb.ErrSkillNotFound
	}
	return &sk, nil
}

func (f *fakeGitImportStore) CreateSkill(_ context.Context, sk *agentdb.Skill, cw agentdb.ConfigWrite) (*agentdb.Skill, error) {
	if err := f.note("CreateSkill", sk.Name, cw); err != nil {
		return nil, err
	}
	prev := f.skills[sk.Name]
	sk.Revision = prev.Revision + 1
	f.skills[sk.Name] = *sk
	return sk, nil
}

func (f *fakeGitImportStore) ListSubscriptions(context.Context, string) ([]*agentdb.Subscription, error) {
	var out []*agentdb.Subscription
	for _, s := range f.subs {
		sub := s
		out = append(out, &sub)
	}
	return out, nil
}

func (f *fakeGitImportStore) CreateSubscription(_ context.Context, sub *agentdb.Subscription, cw agentdb.ConfigWrite) (*agentdb.Subscription, error) {
	if err := f.note("CreateSubscription", sub.ID, cw); err != nil {
		return nil, err
	}
	f.subs[sub.ID] = *sub
	return sub, nil
}

func (f *fakeGitImportStore) UpdateSubscription(_ context.Context, sub *agentdb.Subscription, cw agentdb.ConfigWrite) (*agentdb.Subscription, error) {
	if err := f.note("UpdateSubscription", sub.ID, cw); err != nil {
		return nil, err
	}
	f.subs[sub.ID] = *sub
	return sub, nil
}

func (f *fakeGitImportStore) DeleteSubscription(_ context.Context, project, id string, cw agentdb.ConfigWrite) error {
	if err := f.note("DeleteSubscription", id, cw); err != nil {
		return err
	}
	delete(f.subs, id)
	return nil
}

func (f *fakeGitImportStore) ListSchedules(context.Context, string) ([]*agentdb.Schedule, error) {
	var out []*agentdb.Schedule
	for _, s := range f.scheds {
		sch := s
		out = append(out, &sch)
	}
	return out, nil
}

func (f *fakeGitImportStore) CreateSchedule(_ context.Context, sch *agentdb.Schedule, cw agentdb.ConfigWrite) (*agentdb.Schedule, error) {
	if err := f.note("CreateSchedule", sch.ID, cw); err != nil {
		return nil, err
	}
	f.scheds[sch.ID] = *sch
	return sch, nil
}

func (f *fakeGitImportStore) UpdateSchedule(_ context.Context, sch *agentdb.Schedule, cw agentdb.ConfigWrite) (*agentdb.Schedule, error) {
	if err := f.note("UpdateSchedule", sch.ID, cw); err != nil {
		return nil, err
	}
	f.scheds[sch.ID] = *sch
	return sch, nil
}

func (f *fakeGitImportStore) DeleteSchedule(_ context.Context, project, id string, cw agentdb.ConfigWrite) error {
	if err := f.note("DeleteSchedule", id, cw); err != nil {
		return err
	}
	delete(f.scheds, id)
	return nil
}

func (f *fakeGitImportStore) NewestMemory(_ context.Context, project, selector string) (*agentdb.Memory, error) {
	want := strings.TrimPrefix(selector, "name=")
	for i := len(f.memories) - 1; i >= 0; i-- {
		if f.memories[i].Labels["name"] == want {
			m := f.memories[i]
			return &m, nil
		}
	}
	return nil, agentdb.ErrMemoryNotFound
}

func (f *fakeGitImportStore) CreateMemory(_ context.Context, m *agentdb.Memory, _ []float32) (*agentdb.Memory, bool, error) {
	if err := f.note("CreateMemory", m.Labels["name"], agentdb.ConfigWrite{}); err != nil {
		return nil, false, err
	}
	m.ID = fmt.Sprintf("mem-%d", len(f.memories)+1)
	f.memories = append(f.memories, *m)
	return m, false, nil
}

// ── the repo harness ────────────────────────────────────────────────────────

// gitImportRepo is a clone of a bare repository, both under t.TempDir(). The
// bare one stands in for GitHub.
type gitImportRepo struct {
	t      *testing.T
	repo   *gitproj.Repo
	work   string
	remote string
}

func newGitImportRepo(t *testing.T) *gitImportRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGitImportGit(t, root, "init", "--bare", "-b", "main", remote)
	repo, err := gitproj.Clone(context.Background(), filepath.Join(root, "clone"), remote, "main")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	return &gitImportRepo{t: t, repo: repo, work: repo.Path(), remote: remote}
}

// commit writes files (nil content deletes) and commits them with the given
// author identity and message, returning the new SHA.
func (g *gitImportRepo) commit(authorName, authorEmail, message string, files map[string]*string) string {
	g.t.Helper()
	for p, content := range files {
		full := filepath.Join(g.work, filepath.FromSlash(p))
		if content == nil {
			if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
				g.t.Fatalf("remove %s: %v", p, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			g.t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(full, []byte(*content), 0o644); err != nil {
			g.t.Fatalf("write %s: %v", p, err)
		}
	}
	runGitImportGit(g.t, g.work, "add", "-A")
	cmd := exec.Command("git",
		"-c", "user.name="+authorName, "-c", "user.email="+authorEmail,
		"-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "--cleanup=whitespace", "-F", "-")
	cmd.Dir = g.work
	cmd.Stdin = strings.NewReader(message)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	if out, err := cmd.CombinedOutput(); err != nil {
		g.t.Fatalf("commit: %v: %s", err, out)
	}
	return strings.TrimSpace(runGitImportGit(g.t, g.work, "rev-parse", "HEAD"))
}

// human commits as a person; ours commits as the renderer, with a real trailer
// paragraph.
func (g *gitImportRepo) human(message string, files map[string]*string) string {
	return g.commit("Kai Davenport", "kai@example.com", message, files)
}

func (g *gitImportRepo) ours(subject, rationale string, seq int, files map[string]*string) string {
	msg := subject + "\n"
	if rationale != "" {
		msg += "\n" + rationale + "\n"
	}
	msg += fmt.Sprintf("\nBob-Project: %s\nBob-Seq: %d\nBob-Event: ev-%d\n", gitImportProject, seq, seq)
	return g.commit(gitproj.AuthorName, gitproj.AuthorEmail, msg, files)
}

func (g *gitImportRepo) push() {
	g.t.Helper()
	runGitImportGit(g.t, g.work, "push", "-q", "origin", "main")
}

func runGitImportGit(t *testing.T, dir string, args ...string) string {
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

func ptr(s string) *string { return &s }

func file(frontmatter, body string) *string {
	if frontmatter == "" {
		return ptr("---\n---\n\n" + body)
	}
	return ptr("---\n" + strings.TrimRight(frontmatter, "\n") + "\n---\n\n" + body)
}

// runImport is the whole flow between two commits, with the default subfolder.
func runImport(t *testing.T, store gitImportStore, g *gitImportRepo, from, to string) *gitImportResult {
	t.Helper()
	res, err := newGitImporter(store).ImportRange(context.Background(), gitImportInput{
		Project: gitImportProject,
		Repo:    g.repo,
		FromSHA: from,
		ToSHA:   to,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res
}

// ── 🔴 DI2: a file that changes one field must not wipe the others ──────────

func TestGitImportFieldMergeKeepsUnrenderedFields(t *testing.T) {
	store := newFakeGitImportStore()
	// The stored row carries more than the file does: a briefing selector list
	// and an mcp_config that this project's render happens not to emit, plus a
	// prompt. All of it must survive a human toggling `enabled`.
	store.workers["copywriter"] = agentdb.Worker{
		Project:      gitImportProject,
		Name:         "copywriter",
		Description:  "writes the words",
		SystemPrompt: "You write copy.",
		Briefing:     agentdb.SelectorList{"kind=lesson", "name=message-board"},
		MCPConfig:    agentdb.JSONMap{"core": map[string]any{"type": "http"}},
		Image:        "wolf-base",
		MaxInstances: 3,
		Enabled:      true,
	}

	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("description: writes the words\nenabled: true", "You write copy."),
	})
	head := g.human("Pause the copywriter while we rewrite the brief", map[string]*string{
		"bob/workers/copywriter.md": file("description: writes the words\nenabled: false", "You write copy."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if got := store.methods(); !reflect.DeepEqual(got, []string{"UpsertWorker"}) {
		t.Fatalf("store calls = %v, want exactly one UpsertWorker", got)
	}

	got := store.workers["copywriter"]
	if got.Enabled {
		t.Fatalf("enabled was not applied")
	}
	// The DI2 assertions.
	if len(got.Briefing) != 2 || got.Briefing[0] != "kind=lesson" {
		t.Fatalf("briefing was wiped: %#v", got.Briefing)
	}
	if len(got.MCPConfig) != 1 {
		t.Fatalf("mcp_config was wiped: %#v", got.MCPConfig)
	}
	if got.SystemPrompt != "You write copy." {
		t.Fatalf("system prompt was clobbered: %q", got.SystemPrompt)
	}
	if got.MaxInstances != 3 || got.Image != "wolf-base" {
		t.Fatalf("unrendered scalars were wiped: %+v", got)
	}
}

// 🔴 DI7: a key the human DELETED means clear that field, not "unchanged".
func TestGitImportRemovedKeyClearsField(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		Description: "writes the words", Image: "wolf-base",
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("description: writes the words\nenabled: true\nimage: wolf-base", "You write copy."),
	})
	head := g.human("Unpin the image", map[string]*string{
		"bob/workers/copywriter.md": file("description: writes the words\nenabled: true", "You write copy."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if got := store.workers["copywriter"].Image; got != "" {
		t.Fatalf("image = %q, want it cleared", got)
	}
	if store.workers["copywriter"].Description != "writes the words" {
		t.Fatalf("description should be untouched")
	}
}

// ── 🔴 rule 3: quarantine is all-or-nothing ─────────────────────────────────

func TestGitImportQuarantineWritesNothing(t *testing.T) {
	cases := []struct {
		name string
		bad  *string
		want string
	}{
		{
			name: "unterminated frontmatter fence",
			bad:  ptr("---\nenabled: true\n\nno closing fence here\n"),
			want: "frontmatter",
		},
		{
			name: "a field that cannot be read as its type",
			bad:  file("enabled: true\nmax_instances: as many as it takes", "prompt"),
			want: "max_instances",
		},
		{
			name: "frontmatter naming a different entity",
			bad:  file("name: somebody-else\nenabled: true", "prompt"),
			want: "does not match the filename",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeGitImportStore()
			store.workers["copywriter"] = agentdb.Worker{
				Project: gitImportProject, Name: "copywriter", Enabled: true,
				SystemPrompt: "You write copy.", MaxInstances: 1,
			}
			g := newGitImportRepo(t)
			base := g.human("seed", map[string]*string{
				"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
				"bob/settings.md":           file("base_image: wolf-base", "The project."),
			})
			head := g.human("A push with one bad file in it", map[string]*string{
				// Two perfectly good edits...
				"bob/workers/copywriter.md": file("enabled: false", "You write copy."),
				"bob/settings.md":           file("base_image: wolf-next", "The project."),
				// ...and one that is not.
				"bob/workers/broken.md": tc.bad,
			})

			res := runImport(t, store, g, base, head)
			if !res.Quarantined {
				t.Fatalf("expected quarantine, got applied=%+v", res.Applied)
			}
			if len(res.Failures) != 1 || res.Failures[0].Path != "bob/workers/broken.md" {
				t.Fatalf("failures = %+v", res.Failures)
			}
			if !strings.Contains(res.Failures[0].Reason, tc.want) {
				t.Fatalf("reason %q does not mention %q", res.Failures[0].Reason, tc.want)
			}
			// NOTHING was written...
			if got := store.writes(); len(got) != 0 {
				t.Fatalf("quarantined push still wrote: %+v", got)
			}
			if !store.workers["copywriter"].Enabled {
				t.Fatalf("the good edit leaked through")
			}
			if store.settings.BaseImage != "" {
				t.Fatalf("settings were written during a quarantine")
			}
			// ...and the watermark did not move, so the fixed push is retried.
			if res.Watermark != base {
				t.Fatalf("watermark = %s, want it left at %s", res.Watermark, base)
			}
		})
	}
}

// ── 🔴 DI4: trailers are read with git's parser, never grepped ──────────────

func TestGitImportTrailerForgeryIsStillImported(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})

	// A human's commit whose message body ends with a line that LOOKS like our
	// trailer. git does not read it as one: the paragraph also holds prose, so
	// the trailer block is not a trailer block. A grep would find it, decide
	// the commit was ours, and throw this person's edit away.
	forged := "Pause the copywriter\n\nI am quoting the log line that confused me:\nBob-Seq: 999999\n"
	head := g.human(forged, map[string]*string{
		"bob/workers/copywriter.md": file("enabled: false", "You write copy."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if res.OursSkipped != 0 {
		t.Fatalf("a forged trailer made the importer skip a human commit (OursSkipped=%d)", res.OursSkipped)
	}
	if res.HumanCommits != 1 {
		t.Fatalf("HumanCommits = %d, want 1", res.HumanCommits)
	}
	if store.workers["copywriter"].Enabled {
		t.Fatalf("the human's edit was discarded")
	}
	if !strings.Contains(store.writes()[0].CW.Rationale, "Bob-Seq: 999999") {
		t.Fatalf("the rationale should be the commit message verbatim: %q", store.writes()[0].CW.Rationale)
	}
}

// The other half of the same rule: a REAL renderer commit is skipped, and a
// range made only of those writes nothing while still advancing the watermark.
func TestGitImportSkipsOurOwnCommits(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	head := g.ours("worker_prompt_write: copywriter",
		"The architect decided the copy was too long.\nBob-Seq: 999999", 1207,
		map[string]*string{
			"bob/workers/copywriter.md": file("enabled: true", "You write short copy."),
		})

	res := runImport(t, store, g, base, head)
	if res.OursSkipped != 1 || res.HumanCommits != 0 {
		t.Fatalf("ours=%d human=%d, want 1/0", res.OursSkipped, res.HumanCommits)
	}
	if got := store.writes(); len(got) != 0 {
		t.Fatalf("re-imported our own commit: %+v", got)
	}
	if res.Watermark != head {
		t.Fatalf("watermark = %s, want it advanced to %s", res.Watermark, head)
	}
}

// ── the ordinary path: a human edit becomes config event N+1 ────────────────

func TestGitImportHumanEditIsAnEmptyActorConfigWrite(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	msg := "Tighten the copywriter's brief\n\nMarketing asked for shorter sentences and no exclamation marks."
	head := g.human(msg, map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write short copy. No exclamation marks."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	// Body-only ⇒ the dedicated prompt-write path, which is the only one that
	// records worker_prompt_write with a rationale.
	if got := store.methods(); !reflect.DeepEqual(got, []string{"SetWorkerPrompt"}) {
		t.Fatalf("store calls = %v, want exactly one SetWorkerPrompt", got)
	}
	cw := store.writes()[0].CW
	if cw.Worker != "" || cw.Session != "" {
		t.Fatalf("actor should be empty for a human edit, got %+v", cw)
	}
	if cw.Rationale != msg {
		t.Fatalf("rationale = %q, want the commit message %q", cw.Rationale, msg)
	}
	if store.workers["copywriter"].SystemPrompt != "You write short copy. No exclamation marks." {
		t.Fatalf("prompt not applied: %q", store.workers["copywriter"].SystemPrompt)
	}
	if len(res.Applied) != 1 || res.Applied[0].Action != "prompt_write" {
		t.Fatalf("applied = %+v", res.Applied)
	}
}

// A file edited in both halves is two config events: an ordinary update that
// leaves the prompt alone, then the prompt write.
func TestGitImportBodyAndFieldsAreTwoWrites(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	head := g.human("Retune and pause", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: false", "You write short copy."),
	})

	runImport(t, store, g, base, head)
	if got := store.methods(); !reflect.DeepEqual(got, []string{"UpsertWorker", "SetWorkerPrompt"}) {
		t.Fatalf("store calls = %v", got)
	}
	if store.workers["copywriter"].SystemPrompt != "You write short copy." {
		t.Fatalf("prompt not applied")
	}
	if store.workers["copywriter"].Enabled {
		t.Fatalf("fields not applied")
	}
}

// ── 🔴 DI3: the four git-configuration fields are not importable ────────────

func TestGitImportGitConfigFieldsAreIgnored(t *testing.T) {
	store := newFakeGitImportStore()
	store.settings = agentdb.ProjectSettings{
		Project:      gitImportProject,
		BaseImage:    "wolf-base",
		SystemPrompt: "The project.",
		GitRemote:    "https://github.com/badcode/wolf.git",
		GitBranch:    "main",
		GitSubfolder: "bob",
		GitTokenEnv:  "WOLF_GITHUB_TOKEN",
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/settings.md": file(strings.Join([]string{
			"base_image: wolf-base",
			"git_branch: main",
			"git_remote: https://github.com/badcode/wolf.git",
			"git_subfolder: bob",
			"git_token_env: WOLF_GITHUB_TOKEN",
		}, "\n"), "The project."),
	})
	head := g.human("point the projection at my fork", map[string]*string{
		"bob/settings.md": file(strings.Join([]string{
			"base_image: wolf-next",
			"git_branch: attacker",
			"git_remote: https://github.com/attacker/wolf.git",
			"git_subfolder: elsewhere",
			"git_token_env: ATTACKER_TOKEN",
		}, "\n"), "The project."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	// The importable field landed...
	if store.settings.BaseImage != "wolf-next" {
		t.Fatalf("base_image = %q, want wolf-next", store.settings.BaseImage)
	}
	// ...and none of the four did.
	got := store.settings
	if got.GitRemote != "https://github.com/badcode/wolf.git" || got.GitBranch != "main" ||
		got.GitSubfolder != "bob" || got.GitTokenEnv != "WOLF_GITHUB_TOKEN" {
		t.Fatalf("git configuration was redirected by a commit: %+v", got)
	}
	var told bool
	for _, n := range res.Ignored {
		if strings.Contains(n.Reason, "git configuration is not importable") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the operator was not told the git fields were ignored: %+v", res.Ignored)
	}
	// The list gitproj drops must stay exactly these six (project-connections
	// T11 added "connections" as the sixth, worker-only entry).
	if want := []string{"connections", "git_branch", "git_remote", "git_subfolder", "git_token_env", "git_webhook_secret_env"}; !reflect.DeepEqual(gitproj.NotImportableFields(), want) {
		t.Fatalf("NotImportableFields = %v, want %v", gitproj.NotImportableFields(), want)
	}
}

// TestGitImportConnectionsFieldIsIgnored is TestGitImportGitConfigFieldsAreIgnored's
// counterpart for project-connections T11 (Decision 3): a human commit editing
// a worker's `connections` frontmatter key must leave the stored grant
// untouched and be reported as an ignored edit, exactly like the five git_*
// keys — otherwise anyone with push access to the mirror could grant a worker
// "*" (every connection the project has) without going through CanGrant.
func TestGitImportConnectionsFieldIsIgnored(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["architect"] = agentdb.Worker{
		Project: gitImportProject, Name: "architect", Enabled: true,
		MaxInstances: 1, SystemPrompt: "You are the architect.",
		Connections: agentdb.ConnectionList{"github"},
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/architect.md": file(strings.Join([]string{
			"connections:",
			"  - github",
			"enabled: true",
			"max_instances: 1",
			"name: architect",
		}, "\n"), "You are the architect."),
	})
	head := g.human("grant myself everything", map[string]*string{
		"bob/workers/architect.md": file(strings.Join([]string{
			"connections:",
			"  - \"*\"",
			"enabled: true",
			"max_instances: 1",
			"name: architect",
		}, "\n"), "You are the architect."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	got := store.workers["architect"]
	if !reflect.DeepEqual([]string(got.Connections), []string{"github"}) {
		t.Fatalf("connections was rewritten by a commit: %v", got.Connections)
	}
	var told bool
	for _, n := range res.Ignored {
		if strings.Contains(n.Reason, "connections") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the operator was not told connections was ignored: %+v", res.Ignored)
	}
}

// ── termination: a reformat, or a file that already says what the DB says ───

func TestGitImportNoOpReformatWritesNothing(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("description: ''\nenabled: true", "You write copy."),
	})
	// Requoted, reordered, reindented — and meaning exactly the same thing.
	head := g.human("tidy the file", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: \"true\"\ndescription: \"\"", "You write copy."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if got := store.writes(); len(got) != 0 {
		t.Fatalf("a reformat produced writes: %+v", got)
	}
	if res.Watermark != head {
		t.Fatalf("watermark should still advance past a no-op")
	}
}

// A human commit that changes a file back to what the database already holds
// writes nothing either — which is what keeps a mixed push (one of ours plus
// one of theirs) from re-writing every field we rendered.
func TestGitImportSameValueWritesNothing(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: false,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	head := g.human("match the database", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: false", "You write copy."),
	})

	runImport(t, store, g, base, head)
	if got := store.writes(); len(got) != 0 {
		t.Fatalf("wrote a value the store already held: %+v", got)
	}
}

// ── the other kinds ─────────────────────────────────────────────────────────

func TestGitImportCreatesWorkerAndDeletesIt(t *testing.T) {
	store := newFakeGitImportStore()
	g := newGitImportRepo(t)
	empty := g.human("empty", map[string]*string{"README.md": ptr("hi\n")})
	created := g.human("Hire a proofreader", map[string]*string{
		"bob/workers/proofreader.md": file("description: checks the words\nenabled: true\nmax_instances: 2", "You proofread."),
	})

	res := runImport(t, store, g, empty, created)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	w, ok := store.workers["proofreader"]
	if !ok {
		t.Fatalf("worker not created")
	}
	if w.SystemPrompt != "You proofread." || w.MaxInstances != 2 || w.Description != "checks the words" {
		t.Fatalf("worker created wrong: %+v", w)
	}

	deleted := g.human("Retire the proofreader", map[string]*string{
		"bob/workers/proofreader.md": nil,
	})
	res = runImport(t, store, g, created, deleted)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if _, still := store.workers["proofreader"]; still {
		t.Fatalf("worker not deleted")
	}
	if len(res.Applied) != 1 || res.Applied[0].Action != "delete" {
		t.Fatalf("applied = %+v", res.Applied)
	}
}

func TestGitImportSkillEditIsANewRevision(t *testing.T) {
	store := newFakeGitImportStore()
	store.skills["ffmpeg"] = agentdb.Skill{
		Customer: gitImportProject, Name: "ffmpeg", Revision: 3,
		Description: "video", Markdown: "old doc", InstallSh: "apt-get install ffmpeg",
		Labels: agentdb.LabelSet{"kind": "tool"},
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/skills/ffmpeg.md": file("description: video\nlabels:\n  kind: tool", "old doc"),
	})
	head := g.human("Explain the crop filter", map[string]*string{
		"bob/skills/ffmpeg.md": file("description: video\nlabels:\n  kind: tool", "new doc, with crop"),
	})

	runImport(t, store, g, base, head)
	if got := store.methods(); !reflect.DeepEqual(got, []string{"CreateSkill"}) {
		t.Fatalf("store calls = %v, want one CreateSkill (skills are append-only)", got)
	}
	sk := store.skills["ffmpeg"]
	if sk.Revision != 4 || sk.Markdown != "new doc, with crop" {
		t.Fatalf("skill = %+v", sk)
	}
	// The install script is not rendered; it must survive the new revision.
	if sk.InstallSh != "apt-get install ffmpeg" {
		t.Fatalf("install_sh was lost across the revision: %q", sk.InstallSh)
	}
}

func TestGitImportMemoryDocumentAppendsANewMemory(t *testing.T) {
	store := newFakeGitImportStore()
	store.memories = []agentdb.Memory{{
		ID: "mem-0", Project: gitImportProject, Content: "old board",
		Labels: agentdb.LabelSet{"name": "message-board", "kind": "document"},
		// Provenance from an agent write; the human's edit must NOT inherit it.
		CreatedByWorker: "architect", CreatedBySession: "sess-1",
	}}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/memory/message-board.md": file("labels:\n  kind: document\n  name: message-board", "old board"),
	})
	head := g.human("Add the Q3 target to the board", map[string]*string{
		"bob/memory/message-board.md": file("labels:\n  kind: document\n  name: message-board", "old board\n\nQ3 target: 40 posts."),
	})

	runImport(t, store, g, base, head)
	if len(store.memories) != 2 {
		t.Fatalf("want a NEW memory (immutability, §E), got %d", len(store.memories))
	}
	got := store.memories[1]
	if got.Content != "old board\n\nQ3 target: 40 posts." {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Labels["name"] != "message-board" || got.Labels["kind"] != "document" {
		t.Fatalf("labels = %+v", got.Labels)
	}
	if got.CreatedByWorker != "" || got.CreatedBySession != "" {
		t.Fatalf("provenance must be stamped empty for a human edit, got %+v", got)
	}
	if store.memories[0].Content != "old board" {
		t.Fatalf("the previous memory was mutated")
	}
}

func TestGitImportSubscriptionAndScheduleRoundTrip(t *testing.T) {
	store := newFakeGitImportStore()
	store.subs["11111111-2222-3333-4444-555555555555"] = agentdb.Subscription{
		ID: "11111111-2222-3333-4444-555555555555", Project: gitImportProject,
		EventType: "architect.run", Worker: "architect", Enabled: true,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/subscriptions/11111111-2222-3333-4444-555555555555.md": file("enabled: true\nevent_type: architect.run\nworker: architect", ""),
	})
	head := g.human("Pause the architect subscription and add a nightly review", map[string]*string{
		"bob/subscriptions/11111111-2222-3333-4444-555555555555.md": file("enabled: false\nevent_type: architect.run\nworker: architect", ""),
		"bob/schedules/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.md":     file("cron: 0 3 * * *\nenabled: true\ninput: review the day\nworker: architect", ""),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if store.subs["11111111-2222-3333-4444-555555555555"].Enabled {
		t.Fatalf("subscription still enabled")
	}
	sch, ok := store.scheds["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"]
	if !ok {
		t.Fatalf("schedule not created")
	}
	if sch.Cron != "0 3 * * *" || sch.Worker != "architect" || sch.Input != "review the day" || !sch.Enabled {
		t.Fatalf("schedule = %+v", sch)
	}
}

// Images render for a human to read; nothing about them can be imported.
func TestGitImportIgnoresImagesAndPathsOutsideTheSubfolder(t *testing.T) {
	store := newFakeGitImportStore()
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{"README.md": ptr("hi\n")})
	head := g.human("touch everything", map[string]*string{
		"bob/images/wolf-base.md":  file("version: 4", ""),
		".github/workflows/ci.yml": ptr("on: push\n"),
		"docs/notes.md":            ptr("unrelated\n"),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if got := store.writes(); len(got) != 0 {
		t.Fatalf("wrote something for an image or an out-of-subfolder path: %+v", got)
	}
	var sawImage bool
	for _, n := range res.Ignored {
		if n.Path == "bob/images/wolf-base.md" {
			sawImage = true
		}
		if strings.HasPrefix(n.Path, ".github/") {
			t.Fatalf("the importer looked outside its subfolder: %+v", n)
		}
	}
	if !sawImage {
		t.Fatalf("the image edit was dropped without telling anyone: %+v", res.Ignored)
	}
}

// 🔴 The renderer writes a generated bob/README.md and ParsePath refuses it
// (it is not an entity shape). If the importer did not skip it, that one file
// would quarantine every push forever — the renderer rewrites it, so the bad
// path never goes away and the watermark never advances. This test pins the
// skip AND pins that the good edit in the same push still lands.
func TestGitImportSkipsTheGeneratedReadme(t *testing.T) {
	// The premise: gitproj really does refuse this path.
	if _, _, err := gitproj.ParsePath("bob", "bob/README.md"); err == nil {
		t.Fatalf("gitproj.ParsePath now accepts bob/README.md; this skip may no longer be needed")
	}

	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/README.md":             ptr("# wolf\n\nThis folder is written by Agent Bob.\n"),
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	head := g.human("Pause the copywriter", map[string]*string{
		"bob/README.md":             ptr("# wolf\n\nThis folder is written by Agent Bob. Edit it and the change is applied.\n"),
		"bob/workers/copywriter.md": file("enabled: false", "You write copy."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("the generated README quarantined the push: %+v", res.Failures)
	}
	if store.workers["copywriter"].Enabled {
		t.Fatalf("the real edit alongside the README did not land")
	}
	for _, n := range res.Ignored {
		if strings.HasSuffix(n.Path, "README.md") {
			t.Fatalf("the generated README should be skipped silently, not reported: %+v", n)
		}
	}
}

// ── the fetch path, against a local bare "remote" ───────────────────────────

func TestGitImportFromRemoteTip(t *testing.T) {
	store := newFakeGitImportStore()
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	g.push()

	// A second clone stands in for a person working on their laptop.
	other := filepath.Join(t.TempDir(), "laptop")
	runGitImportGit(t, t.TempDir(), "clone", "-q", g.remote, other)
	if err := os.WriteFile(filepath.Join(other, "bob/workers/copywriter.md"),
		[]byte(*file("enabled: false", "You write copy.")), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGitImportGit(t, other, "-c", "user.name=Kai", "-c", "user.email=kai@example.com",
		"commit", "-q", "-am", "Pause the copywriter")
	runGitImportGit(t, other, "push", "-q", "origin", "main")

	res, err := newGitImporter(store).Import(context.Background(), gitImportInput{
		Project: gitImportProject,
		Repo:    g.repo,
		FromSHA: base,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if store.workers["copywriter"].Enabled {
		t.Fatalf("the pushed edit was not imported")
	}
	if res.Watermark == base || res.Watermark == "" {
		t.Fatalf("watermark did not advance: %q", res.Watermark)
	}
}

// A store failure mid-apply leaves the watermark where it was, so the next run
// re-derives the remainder from the tree. It is NOT reported as a quarantine:
// the writes that already landed are real config events and saying otherwise
// would be a lie.
func TestGitImportStoreFailureLeavesWatermark(t *testing.T) {
	store := newFakeGitImportStore()
	store.failMethod = "UpsertWorker"
	store.workers["copywriter"] = agentdb.Worker{
		Project: gitImportProject, Name: "copywriter", Enabled: true,
		SystemPrompt: "You write copy.", MaxInstances: 1,
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: true", "You write copy."),
	})
	head := g.human("Pause it", map[string]*string{
		"bob/workers/copywriter.md": file("enabled: false", "You write copy."),
	})

	res, err := newGitImporter(store).ImportRange(context.Background(), gitImportInput{
		Project: gitImportProject, Repo: g.repo, FromSHA: base, ToSHA: head,
	})
	if err == nil {
		t.Fatalf("expected the store failure to surface")
	}
	if res == nil || res.Watermark != base {
		t.Fatalf("watermark must not advance past a failed apply: %+v", res)
	}
	if res.Quarantined {
		t.Fatalf("a store failure is not a quarantine")
	}
}

func TestGitImportRejectsMissingProject(t *testing.T) {
	_, err := newGitImporter(newFakeGitImportStore()).ImportRange(context.Background(), gitImportInput{
		Repo: newGitImportRepo(t).repo, ToSHA: "HEAD",
	})
	if err == nil || !strings.Contains(err.Error(), "project is required") {
		t.Fatalf("err = %v", err)
	}
}

// Guard: the sentinel the planner relies on to tell "absent" from "broken".
func TestGitImportWorkerNotFoundSentinel(t *testing.T) {
	_, err := newFakeGitImportStore().GetWorker(context.Background(), gitImportProject, "nobody")
	if !errors.Is(err, agentdb.ErrWorkerNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// TestGitImportRenderedBodiesAreNotHumanEdits is the regression test for DI23,
// found only when both epics' browser suites ran together. One human push that
// edited ONE worker produced TWO worker_prompt_write events, and the second
// rewrote a worker the human never touched — with the human's commit message as
// its rationale. The changelog recorded a person doing something they did not do.
//
// The mechanism: the renderer gives every body exactly one trailing newline, and
// values written by the console or the API have none. A raw compare between a
// rendered file and its stored value is therefore ALWAYS unequal, so whenever
// one of our own commits fell inside an import's diff range — which happens
// when the push loop lags under load — the same-value suppression built for
// exactly that case could not fire. A solo rerun of the browser spec passed; the
// loaded batch did not.
//
// Every other test in this file builds bodies with file(), which adds no
// trailing newline, so none of them looked like a real rendered file — which is
// why the suite was green while the bug lived. These bodies are rendered().
func TestGitImportRenderedBodiesAreNotHumanEdits(t *testing.T) {
	rendered := func(body string) string { return body + "\n" } // exactly as RenderTree writes a body

	t.Run("a human edit to one worker writes that worker, and only with its own rationale", func(t *testing.T) {
		store := newFakeGitImportStore()
		store.workers["editor"] = agentdb.Worker{Project: gitImportProject, Name: "editor", Enabled: true, MaxInstances: 1, SystemPrompt: "You edit copy."}
		store.workers["scribe"] = agentdb.Worker{Project: gitImportProject, Name: "scribe", Enabled: true, MaxInstances: 1, SystemPrompt: "You take notes. Timestamp every one."}
		g := newGitImportRepo(t)
		base := g.ours("seed", "", 2, map[string]*string{
			"bob/workers/editor.md": file("enabled: true", rendered("You edit copy.")),
			"bob/workers/scribe.md": file("enabled: true", rendered("You take notes.")),
		})
		// Our own render of scribe's newer prompt, inside the import's range
		// because the push loop had not recorded it yet.
		g.ours("worker_update: scribe", "", 6, map[string]*string{
			"bob/workers/scribe.md": file("enabled: true", rendered("You take notes. Timestamp every one.")),
		})
		head := g.human("Tighten the editor prompt\n\nThe old one said nothing about adjectives.", map[string]*string{
			"bob/workers/editor.md": file("enabled: true", rendered("You edit copy. Cut every second adjective.")),
		})

		runImport(t, store, g, base, head)
		got := store.writes()
		if len(got) != 1 {
			t.Fatalf("one human edit to ONE worker produced %d writes, want exactly 1: %+v", len(got), got)
		}
		if got[0].Name != "editor" {
			t.Fatalf("the only write went to %q, a worker the human never touched", got[0].Name)
		}
		if !strings.Contains(got[0].CW.Rationale, "Tighten the editor prompt") {
			t.Fatalf("rationale = %q, want the human's commit message", got[0].CW.Rationale)
		}
	})

	// One case per door the fix touched: our own rendered file in range, the
	// store already holding that value without the newline, nothing written.
	for _, tc := range []struct {
		name  string
		seed  func(*fakeGitImportStore)
		path  string
		front string
		old   string
		now   string
	}{
		{
			name: "project prompt",
			seed: func(s *fakeGitImportStore) {
				s.settings = agentdb.ProjectSettings{Project: gitImportProject, BaseImage: "wolf-base", SystemPrompt: "The project."}
			},
			path: "bob/settings.md", front: "base_image: wolf-base",
			old: "An older project prompt.", now: "The project.",
		},
		{
			name: "skill (a spurious write would be a new revision)",
			seed: func(s *fakeGitImportStore) {
				s.skills["ffmpeg"] = agentdb.Skill{
					Customer: gitImportProject, Name: "ffmpeg", Revision: 3, Description: "video",
					Markdown: "new doc", InstallSh: "apt-get install ffmpeg", Labels: agentdb.LabelSet{"kind": "tool"},
				}
			},
			path: "bob/skills/ffmpeg.md", front: "description: video\nlabels:\n  kind: tool",
			old: "old doc", now: "new doc",
		},
		{
			name: "named memory (a spurious write would be a new memory)",
			seed: func(s *fakeGitImportStore) {
				s.memories = []agentdb.Memory{{
					ID: "mem-0", Project: gitImportProject, Content: "new board",
					Labels: agentdb.LabelSet{"name": "message-board", "kind": "document"},
				}}
			},
			path: "bob/memory/message-board.md", front: "labels:\n  kind: document\n  name: message-board",
			old: "old board", now: "new board",
		},
	} {
		t.Run("our own render of a "+tc.name+" in range is not re-imported", func(t *testing.T) {
			store := newFakeGitImportStore()
			store.workers["editor"] = agentdb.Worker{Project: gitImportProject, Name: "editor", Enabled: true, MaxInstances: 1, SystemPrompt: "You edit copy."}
			tc.seed(store)
			g := newGitImportRepo(t)
			base := g.ours("seed", "", 1, map[string]*string{
				tc.path:                 file(tc.front, rendered(tc.old)),
				"bob/workers/editor.md": file("enabled: true", rendered("You edit copy.")),
			})
			// Our re-render of this door's file, inside the range...
			g.ours("re-render", "", 2, map[string]*string{tc.path: file(tc.front, rendered(tc.now))})
			// ...AND a human edit to something else. An all-ours range imports
			// nothing at all, so without this the case passes with the fix
			// reverted — it did, which is how this line came to exist.
			head := g.human("an unrelated human edit", map[string]*string{
				"bob/workers/editor.md": file("enabled: true", rendered("You edit copy. Briefly.")),
			})
			runImport(t, store, g, base, head)
			got := store.writes()
			if len(got) != 1 || got[0].Name != "editor" {
				t.Fatalf("want exactly one write, to editor; got %d: %+v — our own rendered %s, differing from the store only by a trailing newline, was re-imported", len(got), got, tc.name)
			}
		})
	}
}
