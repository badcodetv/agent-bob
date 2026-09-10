package main

// Tests for G15 — bootstrapping a project from a folder.
//
// Everything here runs against a local bare repository in t.TempDir() and the
// in-memory store from gitimport_test.go: no network, no database, no Docker.
// Reusing that fake is deliberate — the bootstrap door writes through exactly
// the same store methods the import door does, and a second fake would be a
// second definition of what "the store" means.
//
// The headline test is TestGitBootstrapRoundTrip. It is what "project as code"
// means, stated as an assertion: render project A's whole state to a tree,
// bootstrap project B from that tree, fold both configurations and require them
// to be equal. Everything else in this file is a boundary condition on it.

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

const gitBootstrapSubfolder = gitproj.DefaultSubfolder

// ── the fixture: a project with something to lose ───────────────────────────

// gitBootstrapStateA is project A: the source of truth a folder gets exported
// from. It is deliberately not minimal — several workers, a populated
// mcp_config on both a worker and the project, briefing selector lists,
// subscriptions, schedules, a skill and a named memory document — because a
// round-trip test over a thin fixture proves only that thin things round-trip.
//
// Two shapes in here are load-bearing rather than decorative:
//
//   - every credential-bearing leaf (an MCP url, the attention webhook) is a
//     whole-value ${VAR} reference, because the allowlist refuses to render
//     anything else (§D) and a literal here would fail the render, not the
//     round-trip;
//   - the skill's Revision and provenance (CreatedByWorker/CreatedBySession)
//     are left at values that do not survive bootstrap — a bootstrapped
//     project has no history to inherit, and provenance is server-stamped,
//     never taken from a file. Visibility and RequiresBuild DO round-trip.
//     All of this is pinned in its own test,
//     TestGitBootstrapDoesNotRestoreServerOwnedSkillFields.
func gitBootstrapStateA(project string) gitproj.ProjectState {
	settings := agentdb.DefaultProjectSettings(project)
	settings.BaseImage = "wolf-base:7"
	settings.SystemPrompt = "You are part of the Wolf trading-hypothesis project.\nSay what you measured."
	settings.MaxConcurrentJobs = 4
	settings.DailyTokensSoft = 250000
	settings.DailyTokensHard = 900000
	settings.BriefingMaxBytes = 24000
	settings.SnapshotTTLDays = 21
	settings.Briefing = agentdb.SelectorList{"name=label-registry", "kind=charter"}
	settings.MCPConfig = agentdb.JSONMap{
		"core": map[string]interface{}{"type": "http", "url": "${WOLF_CORE_MCP_URL}"},
	}
	settings.AttentionChannel = agentdb.JSONMap{
		"kind": "webhook", "url": "${WOLF_SLACK_WEBHOOK}",
	}
	// The four git fields are set on A precisely so the round-trip has to prove
	// they do NOT travel: they name a repository and a credential's variable,
	// and a folder that could set them would redirect a project's projection.
	settings.GitRemote = "https://github.com/binocarlos/wolf-projection.git"
	settings.GitBranch = "main"
	settings.GitSubfolder = gitBootstrapSubfolder
	settings.GitTokenEnv = "WOLF_GITHUB_TOKEN"

	return gitproj.ProjectState{
		Settings: settings,
		Workers: []*agentdb.Worker{
			{
				Project: project, Name: "architect",
				Description:  "designs the roster",
				SystemPrompt: "You are the architect. Design the roster; change one thing at a time.",
				Image:        "wolf-base:7",
				Briefing:     agentdb.SelectorList{"kind=charter", "name=message-board"},
				MaxInstances: 1,
				Enabled:      true,
				Frozen:       true,
				MCPConfig: agentdb.JSONMap{
					"core": map[string]interface{}{"type": "http", "url": "${WOLF_CORE_MCP_URL}"},
				},
			},
			{
				Project: project, Name: "copywriter",
				Description:  "writes the words",
				SystemPrompt: "You write copy. Keep it short.",
				Briefing:     agentdb.SelectorList{"kind=lesson"},
				MaxInstances: 3,
				Enabled:      true,
			},
			{
				Project: project, Name: "night-shift",
				Description:  "runs while nobody is watching",
				SystemPrompt: "You run overnight. Report what you could not decide.",
				MaxInstances: 1,
				// Enabled false is configuration, not an omission: the renderer
				// always emits bools for exactly this reason (DI9 item 6), and a
				// round-trip that lost it would silently switch a worker on.
				Enabled: false,
			},
		},
		Skills: []*agentdb.Skill{{
			Customer: project, Name: "hypothesis-format", Revision: 1,
			Description: "how a trading hypothesis is written down",
			Labels:      agentdb.LabelSet{"kind": "format", "surface": "memory"},
			Markdown:    "# Hypothesis format\n\nOne claim, one measure, one horizon.",
			InstallSh:   "",
		}},
		Subscriptions: []*agentdb.Subscription{
			{
				ID: "sub-architect-run", Project: project,
				EventType: "architect.run", Worker: "architect",
				Filter:            agentdb.JSONMap{"reason": "daily"},
				MaxFiringsPerHour: 4, Enabled: true,
			},
			{
				ID: "sub-copy-review", Project: project,
				EventType: "delivery.awaiting-human", Worker: "copywriter",
				Enabled: false,
			},
		},
		Schedules: []*agentdb.Schedule{
			{
				ID: "sch-architect-daily", Project: project,
				Worker: "architect", Cron: "0 7 * * *",
				Input:   "Review yesterday and change at most one thing.",
				Enabled: true,
			},
			{
				ID: "sch-session-nudge", Project: project,
				TargetSession: "wolf-console", Cron: "*/30 * * * *",
				Input:   "Anything waiting on a human?",
				Enabled: true,
			},
		},
		Documents: []*agentdb.Memory{{
			ID: "mem-board", Project: project,
			Labels:  agentdb.LabelSet{"name": "message-board", "kind": "document"},
			Content: "Board: the Q3 hypothesis is in review.",
		}},
	}
}

// ── the fold both sides are compared through ────────────────────────────────

// gitBootstrapFold is a project's configuration reduced to the fields the
// projection is responsible for, in a form two projects can be compared in.
//
// It is a fold in the sense the ticket means: the configuration you would get
// by replaying every config event, with the things that are NOT configuration
// removed. What it drops, and why each is not a hole in the test:
//
//   - project/customer identity and row ids, because the whole point is that
//     the same configuration now lives under a different project name;
//   - created_at / updated_at, which the store owns and which the renderer
//     deliberately never emits (rendering a clock would commit forever);
//   - the four git-configuration fields, which are not importable BY DESIGN
//     (§D, DI3) — asserted separately and explicitly in
//     TestGitBootstrapDoesNotTakeGitConfigurationFromTheFolder;
//   - a skill's revision and provenance, which the store stamps —
//     TestGitBootstrapDoesNotRestoreServerOwnedSkillFields pins that;
//   - the memory LOG, which is not exported at all (§E, confirmed and marked
//     do-not-re-open). Only named documents are compared, because only named
//     documents are in the repository.
//
// Markdown bodies are compared with trailing newlines trimmed: the renderer
// normalises a body to end in exactly one newline, so the newline is a property
// of the file format rather than of the configuration.
type gitBootstrapFold struct {
	Settings gitBootstrapSettings
	Workers  map[string]gitBootstrapWorker
	Skills   map[string]gitBootstrapSkill
	Subs     map[string]gitBootstrapSub
	Scheds   map[string]gitBootstrapSched
	Docs     map[string]gitBootstrapDoc
}

type gitBootstrapSettings struct {
	BaseImage         string
	SystemPrompt      string
	MCPConfig         agentdb.JSONMap
	AttentionChannel  agentdb.JSONMap
	MaxConcurrentJobs int
	DailyTokensSoft   int64
	DailyTokensHard   int64
	BriefingMaxBytes  int
	SnapshotTTLDays   int
	Briefing          agentdb.SelectorList
}

type gitBootstrapWorker struct {
	Name         string
	Description  string
	SystemPrompt string
	MCPConfig    agentdb.JSONMap
	Image        string
	Briefing     agentdb.SelectorList
	MaxInstances int
	Enabled      bool
	Frozen       bool
}

type gitBootstrapSkill struct {
	Name        string
	Description string
	Labels      agentdb.LabelSet
	Markdown    string
	InstallSh   string
}

type gitBootstrapSub struct {
	ID                string
	EventType         string
	Filter            agentdb.JSONMap
	Worker            string
	MaxFiringsPerHour int
	Enabled           bool
}

type gitBootstrapSched struct {
	ID            string
	Worker        string
	TargetSession string
	Cron          string
	Input         string
	Enabled       bool
}

type gitBootstrapDoc struct {
	Labels  agentdb.LabelSet
	Content string
}

func gitBootstrapTrim(s string) string { return strings.TrimRight(s, "\n") }

// foldState folds project A, which is held as the state the renderer was given.
func foldState(st gitproj.ProjectState) gitBootstrapFold {
	f := gitBootstrapFold{
		Workers: map[string]gitBootstrapWorker{},
		Skills:  map[string]gitBootstrapSkill{},
		Subs:    map[string]gitBootstrapSub{},
		Scheds:  map[string]gitBootstrapSched{},
		Docs:    map[string]gitBootstrapDoc{},
	}
	if s := st.Settings; s != nil {
		f.Settings = gitBootstrapSettings{
			BaseImage: s.BaseImage, SystemPrompt: gitBootstrapTrim(s.SystemPrompt),
			MCPConfig: s.MCPConfig, AttentionChannel: s.AttentionChannel,
			MaxConcurrentJobs: s.MaxConcurrentJobs,
			DailyTokensSoft:   s.DailyTokensSoft, DailyTokensHard: s.DailyTokensHard,
			BriefingMaxBytes: s.BriefingMaxBytes, SnapshotTTLDays: s.SnapshotTTLDays,
			Briefing: s.Briefing,
		}
	}
	for _, w := range st.Workers {
		f.Workers[w.Name] = gitBootstrapWorker{
			Name: w.Name, Description: w.Description,
			SystemPrompt: gitBootstrapTrim(w.SystemPrompt), MCPConfig: w.MCPConfig,
			Image: w.Image, Briefing: w.Briefing, MaxInstances: w.MaxInstances,
			Enabled: w.Enabled, Frozen: w.Frozen,
		}
	}
	for _, sk := range st.Skills {
		f.Skills[sk.Name] = gitBootstrapSkill{
			Name: sk.Name, Description: sk.Description, Labels: sk.Labels,
			Markdown: gitBootstrapTrim(sk.Markdown), InstallSh: sk.InstallSh,
		}
	}
	for _, s := range st.Subscriptions {
		f.Subs[s.ID] = gitBootstrapSub{
			ID: s.ID, EventType: s.EventType, Filter: s.Filter, Worker: s.Worker,
			MaxFiringsPerHour: s.MaxFiringsPerHour, Enabled: s.Enabled,
		}
	}
	for _, s := range st.Schedules {
		f.Scheds[s.ID] = gitBootstrapSched{
			ID: s.ID, Worker: s.Worker, TargetSession: s.TargetSession,
			Cron: s.Cron, Input: s.Input, Enabled: s.Enabled,
		}
	}
	for _, d := range st.Documents {
		f.Docs[d.Labels[agentdb.MemoryNameLabel]] = gitBootstrapDoc{
			Labels: d.Labels, Content: gitBootstrapTrim(d.Content),
		}
	}
	return f
}

// foldStore folds project B, which is held as rows in the store the bootstrap
// wrote through. For memories it takes the NEWEST per name= — the same
// "current value of X" rule the renderer uses (§E) — so a document written
// twice folds to what a reader would see.
func foldStore(s *fakeGitImportStore) gitBootstrapFold {
	st := gitproj.ProjectState{Settings: &s.settings}
	for _, w := range s.workers {
		w := w
		st.Workers = append(st.Workers, &w)
	}
	for _, sk := range s.skills {
		sk := sk
		st.Skills = append(st.Skills, &sk)
	}
	for _, sub := range s.subs {
		sub := sub
		st.Subscriptions = append(st.Subscriptions, &sub)
	}
	for _, sch := range s.scheds {
		sch := sch
		st.Schedules = append(st.Schedules, &sch)
	}
	newest := map[string]agentdb.Memory{}
	for _, m := range s.memories {
		if n := m.Labels[agentdb.MemoryNameLabel]; n != "" {
			newest[n] = m // s.memories is append-ordered, so the last write wins
		}
	}
	for _, m := range newest {
		m := m
		st.Documents = append(st.Documents, &m)
	}
	return foldState(st)
}

// ── the harness ─────────────────────────────────────────────────────────────

// gitBootstrapExport renders a state and commits the resulting tree as the
// RENDERER would have: the fixed projection author and a real trailer block.
//
// Committing as ours rather than as a human is the point of doing it this way.
// An ordinary import skips commits carrying Bob-Seq, because their content
// is already in the database; a folder exported by a render is made ENTIRELY of
// such commits, so a bootstrap that reused that rule would import nothing and
// report success. This harness makes that mistake fail.
func gitBootstrapExport(t *testing.T, g *gitImportRepo, st gitproj.ProjectState, extra map[string]*string) string {
	t.Helper()
	tree, err := gitproj.RenderTree(st, gitBootstrapSubfolder)
	if err != nil {
		t.Fatalf("render project A: %v", err)
	}
	files := map[string]*string{}
	for path, content := range tree {
		s := string(content)
		files[path] = &s
	}
	for path, content := range extra {
		files[path] = content
	}
	return g.ours("worker_prompt_write: architect", "the export of project A", 42, files)
}

func runBootstrap(t *testing.T, store gitImportStore, g *gitImportRepo, sha string) *gitBootstrapResult {
	t.Helper()
	res, err := newGitBootstrapper(store).Bootstrap(context.Background(), gitBootstrapInput{
		Project: gitImportProject,
		Repo:    g.repo,
		SHA:     sha,
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return res
}

// ── 🔴 the one that means "project as code" ─────────────────────────────────

func TestGitBootstrapRoundTrip(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")

	g := newGitImportRepo(t)
	sha := gitBootstrapExport(t, g, stateA, nil)

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined: %+v", res.Failures)
	}
	if res.Watermark != sha {
		t.Fatalf("watermark = %q, want the bootstrapped commit %q", res.Watermark, sha)
	}

	got, want := foldStore(storeB), foldState(stateA)
	if !reflect.DeepEqual(got, want) {
		// Report the first difference by section, because a whole-fold dump is
		// unreadable and the useful question is always "which entity".
		if !reflect.DeepEqual(got.Settings, want.Settings) {
			t.Errorf("settings differ:\n got %#v\nwant %#v", got.Settings, want.Settings)
		}
		for name, w := range want.Workers {
			if !reflect.DeepEqual(got.Workers[name], w) {
				t.Errorf("worker %q differs:\n got %#v\nwant %#v", name, got.Workers[name], w)
			}
		}
		for name, s := range want.Skills {
			if !reflect.DeepEqual(got.Skills[name], s) {
				t.Errorf("skill %q differs:\n got %#v\nwant %#v", name, got.Skills[name], s)
			}
		}
		for id, s := range want.Subs {
			if !reflect.DeepEqual(got.Subs[id], s) {
				t.Errorf("subscription %q differs:\n got %#v\nwant %#v", id, got.Subs[id], s)
			}
		}
		for id, s := range want.Scheds {
			if !reflect.DeepEqual(got.Scheds[id], s) {
				t.Errorf("schedule %q differs:\n got %#v\nwant %#v", id, got.Scheds[id], s)
			}
		}
		for name, d := range want.Docs {
			if !reflect.DeepEqual(got.Docs[name], d) {
				t.Errorf("document %q differs:\n got %#v\nwant %#v", name, got.Docs[name], d)
			}
		}
		if len(got.Workers) != len(want.Workers) || len(got.Subs) != len(want.Subs) ||
			len(got.Scheds) != len(want.Scheds) || len(got.Docs) != len(want.Docs) ||
			len(got.Skills) != len(want.Skills) {
			t.Errorf("entity counts differ: got %d workers %d skills %d subs %d scheds %d docs",
				len(got.Workers), len(got.Skills), len(got.Subs), len(got.Scheds), len(got.Docs))
		}
		t.FailNow()
	}

	// And the same claim from the other end: re-rendering the bootstrapped
	// project reproduces the folder byte-for-byte, apart from settings.md,
	// which cannot match because the git fields deliberately did not travel.
	// This is the property that makes the import→render loop terminate (§C):
	// bootstrap, re-render, and there is nothing new to commit.
	treeA, err := gitproj.RenderTree(stateA, gitBootstrapSubfolder)
	if err != nil {
		t.Fatalf("render A: %v", err)
	}
	stB := gitproj.ProjectState{Settings: &storeB.settings}
	for _, w := range storeB.workers {
		w := w
		stB.Workers = append(stB.Workers, &w)
	}
	for _, sk := range storeB.skills {
		sk := sk
		stB.Skills = append(stB.Skills, &sk)
	}
	for _, s := range storeB.subs {
		s := s
		stB.Subscriptions = append(stB.Subscriptions, &s)
	}
	for _, s := range storeB.scheds {
		s := s
		stB.Schedules = append(stB.Schedules, &s)
	}
	for i := range storeB.memories {
		m := storeB.memories[i]
		stB.Documents = append(stB.Documents, &m)
	}
	treeB, err := gitproj.RenderTree(stB, gitBootstrapSubfolder)
	if err != nil {
		t.Fatalf("render B: %v", err)
	}
	settingsPath := gitproj.SettingsPath(gitBootstrapSubfolder)
	for path, want := range treeA {
		if path == settingsPath {
			continue
		}
		if string(treeB[path]) != string(want) {
			t.Errorf("re-render of %s differs\n got %q\nwant %q", path, treeB[path], want)
		}
	}
	if len(treeA) != len(treeB) {
		t.Errorf("re-rendered tree has %d files, the folder had %d", len(treeB), len(treeA))
	}
}

// ── all-or-nothing ──────────────────────────────────────────────────────────

// A half-bootstrapped project is worse than a failed one: it looks configured.
func TestGitBootstrapOneBadFileBootstrapsNothing(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")

	g := newGitImportRepo(t)
	bad := "---\nenabled: [this is not a bool\n---\n\nYou break things.\n"
	sha := gitBootstrapExport(t, g, stateA, map[string]*string{
		gitBootstrapSubfolder + "/workers/saboteur.md": &bad,
	})

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)

	if !res.Quarantined {
		t.Fatalf("a malformed file did not quarantine the bootstrap")
	}
	if len(res.Failures) != 1 || !strings.Contains(res.Failures[0].Path, "saboteur") {
		t.Fatalf("failures = %+v, want exactly the saboteur file", res.Failures)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("applied = %+v, want nothing applied", res.Applied)
	}
	if got := storeB.writes(); len(got) != 0 {
		t.Fatalf("store was written %d times during a quarantined bootstrap: %+v", len(got), got)
	}
	// A quarantined bootstrap must be retried in full. A watermark here would
	// tell the next import that this folder had already been read, and the
	// whole configuration would be lost silently.
	if res.Watermark != "" {
		t.Fatalf("watermark = %q, want empty after a quarantine", res.Watermark)
	}
}

// ── 🔴 the folder must not be able to retarget the projection ───────────────

// This is the most dangerous version of DI3's hazard: bootstrap is exactly when
// a human points the system at a folder somebody else wrote. If git_remote and
// git_token_env travelled, that folder would redirect the project's projection,
// and its push credential, at a repository its author controls.
func TestGitBootstrapDoesNotTakeGitConfigurationFromTheFolder(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")
	stateA.Settings.GitRemote = "https://github.com/attacker/exfil.git"
	stateA.Settings.GitTokenEnv = "ATTACKER_TOKEN"
	stateA.Settings.GitBranch = "steal"

	g := newGitImportRepo(t)
	sha := gitBootstrapExport(t, g, stateA, nil)

	storeB := newFakeGitImportStore()
	// Project B already publishes somewhere. Those values are the console's and
	// must survive untouched.
	storeB.settings.GitRemote = "https://github.com/binocarlos/wolf-b.git"
	storeB.settings.GitTokenEnv = "WOLF_B_TOKEN"
	storeB.settings.GitBranch = "main"
	storeB.settings.GitSubfolder = gitBootstrapSubfolder

	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined: %+v", res.Failures)
	}

	if storeB.settings.GitRemote != "https://github.com/binocarlos/wolf-b.git" {
		t.Fatalf("git_remote was taken from the folder: %q", storeB.settings.GitRemote)
	}
	if storeB.settings.GitTokenEnv != "WOLF_B_TOKEN" {
		t.Fatalf("git_token_env was taken from the folder: %q", storeB.settings.GitTokenEnv)
	}
	if storeB.settings.GitBranch != "main" || storeB.settings.GitSubfolder != gitBootstrapSubfolder {
		t.Fatalf("git branch/subfolder were taken from the folder: %q %q",
			storeB.settings.GitBranch, storeB.settings.GitSubfolder)
	}

	// The operator is TOLD, rather than left to discover it: the folder carried
	// those lines and they were ignored.
	var told bool
	for _, n := range res.Ignored {
		if strings.HasSuffix(n.Path, "settings.md") && strings.Contains(n.Reason, "git_remote") {
			told = true
		}
	}
	if !told {
		t.Fatalf("the ignored git fields were not reported: %+v", res.Ignored)
	}
}

// ── the watermark ───────────────────────────────────────────────────────────

// Bootstrap is one door, not two: the SHA it reached is what makes the next
// ordinary import a diff of nothing rather than a re-import of everything.
func TestGitBootstrapWatermarkMakesTheNextImportANoOp(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")

	g := newGitImportRepo(t)
	sha := gitBootstrapExport(t, g, stateA, nil)

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined: %+v", res.Failures)
	}
	if res.Watermark != sha {
		t.Fatalf("watermark = %q, want %q", res.Watermark, sha)
	}
	before := len(storeB.writes())

	// The importer, run from the watermark to the same commit, has an empty
	// range and must do nothing at all.
	imported := runImport(t, storeB, g, res.Watermark, sha)
	if imported.Quarantined {
		t.Fatalf("follow-up import quarantined: %+v", imported.Failures)
	}
	if len(imported.Applied) != 0 {
		t.Fatalf("follow-up import applied %+v, want nothing", imported.Applied)
	}
	if got := len(storeB.writes()); got != before {
		t.Fatalf("follow-up import wrote to the store: %d writes, was %d", got, before)
	}

	// And a human commit on top of the watermark is picked up as an ordinary
	// import — the bootstrap left the project in the state the import door
	// expects, not in a special one.
	//
	// The edit is made to the RENDERED file, one word changed, which is what a
	// human editing the mirror actually does. Hand-writing a smaller file
	// instead would test something else entirely: a key a human removes means
	// "clear this field" (DI7), so an abbreviated file legitimately wipes what
	// it leaves out.
	tree, err := gitproj.RenderTree(stateA, gitBootstrapSubfolder)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	copywriterPath := gitBootstrapSubfolder + "/workers/copywriter.md"
	edited := strings.Replace(string(tree[copywriterPath]), "enabled: true", "enabled: false", 1)
	if edited == string(tree[copywriterPath]) {
		t.Fatalf("the fixture no longer renders `enabled: true` for copywriter")
	}
	head := g.human("Pause the copywriter", map[string]*string{copywriterPath: &edited})

	after := runImport(t, storeB, g, res.Watermark, head)
	if after.Quarantined {
		t.Fatalf("import after bootstrap quarantined: %+v", after.Failures)
	}
	if storeB.workers["copywriter"].Enabled {
		t.Fatalf("the human's edit did not land after a bootstrap")
	}
	// DI2, at the seam that matters here: a field the human's commit did not
	// touch survives it, on a row that a bootstrap rather than the console
	// created.
	if len(storeB.workers["copywriter"].Briefing) != 1 {
		t.Fatalf("bootstrapped briefing was wiped by the next import: %#v",
			storeB.workers["copywriter"].Briefing)
	}
	// Trimmed, because a body read out of a file keeps the trailing newline the
	// renderer normalised it to. That newline is a property of the file format,
	// it is idempotent under re-render, and it is the only respect in which a
	// bootstrapped prompt differs from its source.
	if gitBootstrapTrim(storeB.workers["copywriter"].SystemPrompt) != "You write copy. Keep it short." {
		t.Fatalf("the bootstrapped prompt did not survive the next import: %q",
			storeB.workers["copywriter"].SystemPrompt)
	}
}

// ── what the folder did not carry ───────────────────────────────────────────

// Images, deleted-nothing append-only entities and the memory log are all
// reported rather than dropped silently, so an operator can see the shape of
// what a bootstrap did not restore.
func TestGitBootstrapReportsWhatItCannotRestore(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")
	stateA.Images = []*agentdb.CustomImage{{
		Name: "wolf-base", Version: 7, Description: "the project's own image",
		BaseInstallation: "example", Focus: "trading",
	}}

	g := newGitImportRepo(t)
	sha := gitBootstrapExport(t, g, stateA, nil)

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined: %+v", res.Failures)
	}

	var sawImage bool
	for _, n := range res.Ignored {
		if strings.Contains(n.Path, "/images/") && strings.Contains(n.Reason, "not importable") {
			sawImage = true
		}
	}
	if !sawImage {
		t.Fatalf("the image file was not reported as ignored: %+v", res.Ignored)
	}

	// 🔴 §E, confirmed and marked do-not-re-open: only NAMED memories are in
	// the repository. A bootstrapped project has its named documents and no
	// memory log — a brain-state, not a past. Exactly one memory exists here,
	// and it is the named document; nothing reconstructed a history.
	if len(storeB.memories) != 1 {
		t.Fatalf("memories = %d, want exactly the one named document", len(storeB.memories))
	}
	if storeB.memories[0].Labels[agentdb.MemoryNameLabel] != "message-board" {
		t.Fatalf("the restored memory is not the named document: %#v", storeB.memories[0].Labels)
	}
	// Provenance is server-stamped EMPTY: a bootstrap is a human/API act, and
	// claiming a worker wrote it would break the trust rule an embedding
	// application depends on (docs/20-datasets.md §9).
	if storeB.memories[0].CreatedByWorker != "" || storeB.memories[0].CreatedBySession != "" {
		t.Fatalf("bootstrap asserted provenance it does not have: %#v", storeB.memories[0])
	}
}

// A skill's revision and provenance are the store's to stamp, and the importer
// does not carry visibility or requires_build. This test exists so those limits
// are pinned rather than hidden by the round-trip fixture: if the import door
// later learns any of them, it goes red and says so here.
func TestGitBootstrapDoesNotRestoreServerOwnedSkillFields(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")
	sk := stateA.Skills[0]
	sk.Revision = 9
	// Non-default on purpose (G21): the default a fresh CreateSkill would
	// apply is "organizational", so asserting that value back would not prove
	// the importer applied it rather than just defaulting it.
	sk.Visibility = "private"
	sk.RequiresBuild = true
	sk.InstallSh = "#!/bin/sh\napt-get install -y jq\n"
	sk.CreatedByWorker = "architect"
	sk.CreatedBySession = "sess-abc"

	g := newGitImportRepo(t)
	sha := gitBootstrapExport(t, g, stateA, nil)

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined: %+v", res.Failures)
	}

	got := storeB.skills["hypothesis-format"]
	// What DOES come back: the parts of a skill a human writes, including the
	// two settings that render but did not used to be applied by the importer.
	if got.Description != sk.Description || gitBootstrapTrim(got.Markdown) != gitBootstrapTrim(sk.Markdown) {
		t.Fatalf("the skill's authored content did not round-trip: %#v", got)
	}
	if got.InstallSh != sk.InstallSh {
		t.Fatalf("install_sh did not round-trip: %q", got.InstallSh)
	}
	if got.Visibility != "private" {
		t.Fatalf("visibility did not round-trip: got %q, want %q", got.Visibility, "private")
	}
	if !got.RequiresBuild {
		t.Fatalf("requires_build did not round-trip: got %v, want true", got.RequiresBuild)
	}
	// What does not, and must not be quietly assumed to.
	if got.Revision != 1 {
		t.Fatalf("revision = %d; a bootstrapped skill is the project's first revision, "+
			"not the source project's ninth", got.Revision)
	}
	if got.CreatedByWorker != "" || got.CreatedBySession != "" {
		t.Fatalf("bootstrap asserted skill provenance: %#v", got)
	}
}

// ── ordering, and the generated README ──────────────────────────────────────

// A tree listing is alphabetical, which would wire subscriptions and schedules
// up before the workers they wake. Nothing in the store enforces the order, so
// this is about the config log a human reads afterwards describing a project
// being built rather than assembled backwards.
func TestGitBootstrapAppliesInDependencyOrder(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")

	g := newGitImportRepo(t)
	sha := gitBootstrapExport(t, g, stateA, nil)

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined: %+v", res.Failures)
	}

	rank := map[string]int{
		"PutProjectSettings": 0, "SetProjectPrompt": 0,
		"UpsertWorker": 1, "SetWorkerPrompt": 1,
		"CreateSkill":        2,
		"CreateSubscription": 3,
		"CreateSchedule":     4,
		"CreateMemory":       5,
	}
	var seen []string
	last := -1
	for _, c := range storeB.writes() {
		r, ok := rank[c.Method]
		if !ok {
			t.Fatalf("unexpected store method %q during a bootstrap", c.Method)
		}
		seen = append(seen, c.Method)
		if r < last {
			t.Fatalf("writes are out of dependency order: %v", seen)
		}
		last = r
	}
	// Every kind actually ran, or the ordering assertion above is vacuous.
	for _, want := range []string{"UpsertWorker", "CreateSkill", "CreateSubscription", "CreateSchedule", "CreateMemory"} {
		if !containsString(seen, want) {
			t.Fatalf("%s never ran; the order test proved nothing. writes: %v", want, seen)
		}
	}

	// 🔴 DI9 item 2. RenderTree writes orange/README.md and gitproj.ParsePath
	// rejects it, correctly, because it is not an entity shape. Every folder a
	// bootstrap is pointed at contains it, so failing to skip it would
	// quarantine every bootstrap of an exported project. The round-trip test
	// above would have caught it too; this says out loud what it is testing.
	tree, err := gitproj.RenderTree(stateA, gitBootstrapSubfolder)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, ok := tree[gitBootstrapSubfolder+"/README.md"]; !ok {
		t.Fatalf("the renderer no longer writes README.md; the skip in gitbootstrap.go " +
			"and gitimport.go should be revisited together")
	}
	for _, f := range res.Failures {
		if strings.HasSuffix(f.Path, "README.md") {
			t.Fatalf("the generated README was not skipped: %+v", f)
		}
	}
}

// Files outside the subfolder are the repository's own and are left entirely
// alone — a projection folder normally lives in a repo with a life of its own.
func TestGitBootstrapIgnoresFilesOutsideTheSubfolder(t *testing.T) {
	stateA := gitBootstrapStateA("wolf-a")

	g := newGitImportRepo(t)
	readme := "# The application this project belongs to\n"
	workflow := "name: ci\non: push\n"
	sha := gitBootstrapExport(t, g, stateA, map[string]*string{
		"README.md":                   &readme,
		".github/workflows/ci.yml":    &workflow,
		"docs/orange/workers/fake.md": &readme,
	})

	storeB := newFakeGitImportStore()
	res := runBootstrap(t, storeB, g, sha)
	if res.Quarantined {
		t.Fatalf("bootstrap quarantined by files outside the subfolder: %+v", res.Failures)
	}
	if _, ok := storeB.workers["fake"]; ok {
		t.Fatalf("a file outside the subfolder created a worker")
	}
	names := make([]string, 0, len(storeB.workers))
	for n := range storeB.workers {
		names = append(names, n)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"architect", "copywriter", "night-shift"}) {
		t.Fatalf("workers = %v, want only the three in the projection folder", names)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
