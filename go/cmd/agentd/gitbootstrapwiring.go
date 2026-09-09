package main

// gitbootstrapwiring.go — the host half of POST /agent/git-bootstrap
// (design/2026-09-09-git-projection.md §C, ticket G26, DI20).
//
// httpapi owns the door (httpapi/gitbootstraproute.go): the tenancy rule, the
// refusal shapes and the wire types. This file owns everything the door cannot
// reach from there, because httpapi may not import cmd/agentd:
//
//   - THE CLONE. gitProjector.ensureRepo, which is also what decides where the
//     clone lives and which environment variable holds its credential.
//   - THE LOCKS. The same per-project lease and work-lock the render loop and
//     the webhook import take. A bootstrap running beside a render of the same
//     project would be two writers in one working tree.
//   - THE REFUSAL. Whether the project already has configuration, which needs
//     four store reads httpapi has no seam for.
//   - THE DURABLE RESULT. The import watermark, and the per-file notes.
//
// # 🔴 Bootstrap reads the TREE. Do not route it through the ordinary import.
//
// gitbootstrap.go deliberately ignores commit trailers (its file header, and
// DI12). An exported folder is made *entirely* of the renderer's own commits,
// so ImportRange's Orange-Seq skip — which is right for an incremental import,
// because our own commits are already in the database by construction — would
// import nothing here and report success. That is why this file calls
// Bootstrap and not Import, and why "just reuse the import path" is a
// regression rather than a simplification.
//
// # Why the wiring is late-bound
//
// main.go builds httpapi.Config BEFORE the product-layer block that builds the
// projector, so there is no projector to hand over at the moment the handlers
// are constructed. Rather than reorder boot (the projector needs the config
// hook installed before anything can serve a request), this seam is created
// empty, handed to httpapi, and filled in when the projector exists. Until
// then — and forever, on the sqlite fallback where there is no product layer at
// all — it answers ErrGitBootstrapUnavailable and the route 501s.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
)

// gitBootstrapStore is the slice of *agentdb.Store a bootstrap needs: every
// write the importer performs (gitImportStore), plus the four reads that answer
// "does this project already have configuration?".
//
// ListSubscriptions and ListSchedules come from gitImportStore; only the worker
// and skill listings are added here.
type gitBootstrapStore interface {
	gitImportStore
	ListWorkers(ctx context.Context, project string) ([]*agentdb.Worker, error)
	ListProjectSkills(ctx context.Context, q agentdb.SkillCatalogQuery) ([]*agentdb.SkillSummary, error)
}

var _ gitBootstrapStore = (*agentdb.Store)(nil)

// gitBootstrapDeps is everything a real bootstrap needs. Nil until enable().
type gitBootstrapDeps struct {
	store gitBootstrapStore
	proj  *gitProjector
}

// gitBootstrapWiring implements httpapi.GitBootstrapper.
type gitBootstrapWiring struct {
	logf func(string, ...any)

	mu   sync.RWMutex
	deps *gitBootstrapDeps
}

var _ httpapi.GitBootstrapper = (*gitBootstrapWiring)(nil)

func newGitBootstrapWiring(logf func(string, ...any)) *gitBootstrapWiring {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &gitBootstrapWiring{logf: logf}
}

// enable arms the seam once the projector exists. Called from main.go's
// product-layer block; never called at all on the sqlite fallback, where the
// route then answers 501 rather than pretending it could bootstrap.
func (w *gitBootstrapWiring) enable(store gitBootstrapStore, proj *gitProjector) {
	if store == nil || proj == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deps = &gitBootstrapDeps{store: store, proj: proj}
}

func (w *gitBootstrapWiring) snapshot() *gitBootstrapDeps {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.deps
}

// BootstrapProject creates the calling project's configuration from the folder
// in its own repository, at the remote tip.
//
// The project comes from the route's `customer` claim and is passed straight
// through — this function never infers it from a repository, a payload or a
// file (P5, and gitbootstrap.go's own rule).
func (w *gitBootstrapWiring) BootstrapProject(ctx context.Context, project string) (*httpapi.GitBootstrapReport, error) {
	deps := w.snapshot()
	if deps == nil {
		return nil, httpapi.ErrGitBootstrapUnavailable
	}
	if strings.TrimSpace(project) == "" {
		return nil, errors.New("gitbootstrap: project is required")
	}

	ps, err := deps.store.GetProjectSettings(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("read project settings: %w", err)
	}
	if ps == nil || strings.TrimSpace(ps.GitRemote) == "" {
		return nil, httpapi.ErrGitBootstrapNotConfigured
	}

	// The SAME lease the render loop and the webhook import take (§F): one
	// writer per clone. Re-entrant for this owner, so this process's own render
	// loop does not deadlock the operator's request — cross-process exclusion
	// is what the lease is for.
	//
	// 🔴 Unlike the import path, a lost race is an ERROR here, not a silent
	// skip. The import is a background convergence loop and skipping a tick
	// costs nothing; a bootstrap is a human pressing a button once, and
	// answering 200 with an empty report would tell them their folder was
	// empty.
	state := deps.proj.cfg.State
	deadline := time.Now().Add(deps.proj.cfg.LeaseTTL).Unix()
	ok, err := state.AcquireLease(ctx, project, deps.proj.cfg.Owner, deadline)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpapi.ErrGitBootstrapBusy
	}
	defer func() { _ = state.ReleaseLease(context.WithoutCancel(ctx), project, deps.proj.cfg.Owner) }()

	// And the same work-lock: "fetch, read the tree, write the configuration"
	// must not interleave with a render's "fast-forward, write tree, commit" in
	// one working tree.
	work := deps.proj.workLock(project)
	work.Lock()
	defer work.Unlock()

	// 🔴 The refusal, decided INSIDE the lock and immediately before the first
	// write, so it is answered against the same state the write would have
	// used. See the route's file header for why this is a refusal and not a
	// merge.
	found, err := gitBootstrapExistingConfiguration(ctx, deps.store, project)
	if err != nil {
		return nil, err
	}
	if len(found) > 0 {
		return nil, &httpapi.GitBootstrapConflictError{Found: found}
	}

	repo, err := deps.proj.ensureRepo(ctx, project, ps)
	if err != nil {
		return nil, err
	}

	// SHA is left empty on purpose: Bootstrap fetches and reads the remote tip,
	// which is what an operator means by "bootstrap from the repository". The
	// subfolder is the project's own configured one, read through the same
	// defaulting helper every other git path uses.
	res, err := newGitBootstrapper(deps.store).Bootstrap(ctx, gitBootstrapInput{
		Project:   project,
		Repo:      repo,
		Subfolder: gitSubfolderOf(ps),
	})
	if err != nil {
		_ = state.NoteFailureKind(ctx, project, gitProjectionErrorKind(err), err.Error())
		return nil, err
	}
	if res == nil {
		return nil, errors.New("gitbootstrap: no result")
	}

	if res.Quarantined {
		// NOTHING was written and the watermark did not move — the whole folder
		// is retried once the bad file is fixed. Recorded per file, because
		// "your folder was rejected" with no file and no reason is not
		// something anybody can act on.
		reasons := make([]string, 0, len(res.Failures))
		for _, f := range res.Failures {
			reasons = append(reasons, f.Path+": "+f.Reason)
		}
		w.logf("[agentd] git bootstrap: %s quarantined %s — nothing was applied: %s",
			project, shortSHA(res.SHA), strings.Join(reasons, "; "))
		w.putNotes(ctx, state, project, agentdb.GitProjectionNoteQuarantine, gitImportQuarantineNotes(res.Failures))
		_ = state.NoteFailureKind(ctx, project, agentdb.GitProjectionErrorQuarantined,
			"bootstrap quarantined: "+strings.Join(reasons, "; "))
		return gitBootstrapReport(project, res), nil
	}

	// A clean run. BOTH lists are written even when EMPTY, exactly as
	// gitwebhookwiring.go does it: an empty quarantine is how a stale one is
	// cleared, and a red banner that outlives the push — or the bootstrap —
	// that fixed it teaches an operator to ignore banners.
	// The notes are gone; the row's own error must go too, or the console keeps
	// reporting a file this run has just fixed. Narrow on purpose: it clears a
	// quarantine and cannot touch a push failure (see the store method).
	w.putNotes(ctx, state, project, agentdb.GitProjectionNoteQuarantine, nil)
	if err := state.ClearQuarantine(ctx, project); err != nil {
		w.logf("gitbootstrap: clear quarantine for %s: %v", project, err)
	}
	w.putNotes(ctx, state, project, agentdb.GitProjectionNoteIgnored, gitImportIgnoredNotes(res.Ignored))

	// 🔴 The watermark is the whole reason the NEXT ordinary import is a diff of
	// nothing rather than a re-import of everything as though a human had
	// hand-written it. Without it the first webhook delivery after a bootstrap
	// would replay the entire folder.
	if err := state.MarkImported(ctx, project, res.Watermark); err != nil {
		return nil, fmt.Errorf("advance the import watermark: %w", err)
	}
	w.logf("[agentd] git bootstrap: %s created %d thing(s) from %d file(s) at %s (%d ignored)",
		project, len(res.Applied), res.Files, shortSHA(res.SHA), len(res.Ignored))
	return gitBootstrapReport(project, res), nil
}

// putNotes records one kind of note, best effort — the same rule the import
// path states: these are diagnostics ABOUT a run, and failing the run because a
// note could not be written would turn a display problem into an outage.
func (w *gitBootstrapWiring) putNotes(ctx context.Context, state gitProjectionState, project, kind string, notes []agentdb.GitProjectionNote) {
	if err := state.PutNotes(ctx, project, kind, notes); err != nil {
		w.logf("[agentd] git bootstrap: %s: record %s notes: %v", project, kind, err)
	}
}

// gitBootstrapExistingConfiguration answers "is there anything here already?"
// in an operator's words.
//
// Project SETTINGS are deliberately not counted. Every project has a settings
// row — this one necessarily has a populated git_remote, or we would not have
// got this far — so counting it would refuse every bootstrap that ever reached
// the door. What makes a project *configured* is the things a human or an
// architect created: workers, skills, subscriptions, schedules.
func gitBootstrapExistingConfiguration(ctx context.Context, store gitBootstrapStore, project string) ([]string, error) {
	var found []string
	add := func(n int, singular, plural string) {
		switch {
		case n == 1:
			found = append(found, "1 "+singular)
		case n > 1:
			found = append(found, fmt.Sprintf("%d %s", n, plural))
		}
	}

	workers, err := store.ListWorkers(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	add(len(workers), "worker", "workers")

	skills, err := store.ListProjectSkills(ctx, agentdb.SkillCatalogQuery{Project: project})
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	add(len(skills), "skill", "skills")

	subs, err := store.ListSubscriptions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	add(len(subs), "subscription", "subscriptions")

	scheds, err := store.ListSchedules(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	add(len(scheds), "schedule", "schedules")

	return found, nil
}

// gitBootstrapReport translates one run into the wire shape. The Ignored list
// travels in full: an operator who bootstraps a folder of twelve files and gets
// ten things must be told why here, not in agentd's logs (DI10).
func gitBootstrapReport(project string, res *gitBootstrapResult) *httpapi.GitBootstrapReport {
	out := &httpapi.GitBootstrapReport{
		Project:     project,
		SHA:         res.SHA,
		Watermark:   res.Watermark,
		Files:       res.Files,
		Quarantined: res.Quarantined,
		Applied:     make([]httpapi.GitBootstrapWrite, 0, len(res.Applied)),
		Ignored:     make([]httpapi.GitProjectionNote, 0, len(res.Ignored)),
		Failures:    make([]httpapi.GitProjectionNote, 0, len(res.Failures)),
	}
	for _, a := range res.Applied {
		out.Applied = append(out.Applied, httpapi.GitBootstrapWrite{
			Path: a.Path, Kind: a.Kind, Name: a.Name, Action: a.Action,
		})
	}
	for _, n := range res.Ignored {
		out.Ignored = append(out.Ignored, httpapi.GitProjectionNote{Path: n.Path, Reason: n.Reason})
	}
	for _, f := range res.Failures {
		out.Failures = append(out.Failures, httpapi.GitProjectionNote{Path: f.Path, Reason: f.Reason})
	}
	return out
}
