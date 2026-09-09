package main

// gitprojection.go — the OUTBOUND door of the git projection (G8) and the push
// loop that publishes it (G9). design/2026-09-09-git-projection.md §B, §C, §F.
//
// # The one rule (§A)
//
// The database is the only place where writes are put in order. Git is a door
// out (this file) and a door in (gitimport.go). Neither door writes anything
// the store did not serialise, and neither of them ever asks git what order
// things happened in.
//
// # The shape of the thing
//
//	a mutation ──WithConfigEvent──▶ [ projection row + config_events row ]  ONE transaction
//	                                             │ commit
//	                                             ▼
//	                           the store's post-commit hook (agentdb)
//	                                    ╱                 ╲
//	              configChangeEmitter ◀╯                   ╰▶ gitProjector.Hook()
//	              (project_events)                            marks the project DIRTY
//	                                                          ── and does nothing else ──
//	                                                                    │
//	                                                     (a background goroutine)
//	                                                                    ▼
//	                              read state ▶ gitproj.RenderTree ▶ WriteTree ▶ Commit
//	                                                                    │
//	                                             (a SECOND background goroutine)
//	                                                                    ▼
//	                                                      fetch ▶ fast-forward-only push
//
// # Five rules that are load-bearing, not stylistic
//
//  1. 🔴 NO GIT INSIDE THE HOOK. The post-commit hook documented in
//     agentdb/config_events.go runs SYNCHRONOUSLY, on the mutating goroutine —
//     which, for a worker rewriting another worker's prompt, is a model's
//     blocking tool call. Cloning, staging, committing or pushing there would
//     put a network round trip and an index.lock inside that call. So the hook
//     does exactly one thing: record which project is dirty. It performs no IO
//     of any kind, returns no error into the mutation (it cannot: the change is
//     already committed), and never blocks — see markDirty.
//
//  2. 🔴 PROVENANCE IS COPIED OUT OF THE LOG, NEVER FROM MODEL TEXT (DI4). The
//     commit's trailers are built from the *agentdb.ConfigEvent the hook was
//     handed: its seq, its id, its action, its server-stamped actor. The
//     rationale — which IS model text — goes in the body, where git's trailer
//     parser cannot see it, because gitproj.Commit always emits our trailer
//     block as the final paragraph. That defence holds ONLY while the trailer
//     map is non-empty, so commitTrailers can never return an empty map.
//
//  3. NO CHANGE ⇒ NO COMMIT. WriteTree byte-compares the rendered tree against
//     what is already checked in and reports whether anything moved. When
//     nothing did, we do not commit. That is what terminates the import→render
//     loop (§C) and it is why gitproj.RenderTree must stay a pure function.
//
//  4. AN UNRENDERABLE PROJECT IS SKIPPED, NOT FATAL. A literal secret in a
//     credential-bearing field makes RenderTree refuse the whole tree (§D:
//     refusal, not redaction). That is one project's problem: the reason is
//     recorded on its state row and logged, and every other project in the same
//     pass still renders.
//
//  5. THE WATERMARK IS DURABLE AND LIVES IN THE DATABASE (§F), not in the
//     working tree, so a lost disk costs a re-render and not a divergence. A
//     crash between "committed locally" and "wrote the watermark" is repaired
//     by boot reconciliation, which is why the render must be idempotent — and
//     it is, because rule 3 makes a redundant render a no-op.
//
// # What this file does NOT own
//
// The renderer itself (gitproj, G1–G6), the inbound door (gitimport.go, G11),
// the webhook route (G12), the backfill (G10) and the console surface (G16).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/gitproj"
)

// ── the hook fan-out ────────────────────────────────────────────────────────

// fanOutConfigEvents joins several post-commit consumers into the ONE hook the
// store accepts (there is a single slot: SetConfigEventHook).
//
// Order is the order given, and it matters a little: `config.changed` emission
// is the older, load-bearing consumer, so it goes first and the projection
// marks its dirty flag after. Neither may panic — a panic here would unwind
// through a mutation whose transaction has already committed — so each call is
// wrapped, and a nil consumer is skipped rather than crashing at boot.
func fanOutConfigEvents(logf func(string, ...any), hooks ...agentdb.ConfigEventHook) agentdb.ConfigEventHook {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	live := make([]agentdb.ConfigEventHook, 0, len(hooks))
	for _, h := range hooks {
		if h != nil {
			live = append(live, h)
		}
	}
	return func(ctx context.Context, ev *agentdb.ConfigEvent) {
		for i, h := range live {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logf("[agentd] config-event hook %d panicked: %v — the change IS committed", i, r)
					}
				}()
				h(ctx, ev)
			}()
		}
	}
}

// ── the read side ───────────────────────────────────────────────────────────

// gitProjectionStore is the narrow slice of *agentdb.Store the renderer reads.
// Every method is a plain read: the outbound door writes nothing to the
// product-layer tables, ever.
type gitProjectionStore interface {
	GetProjectSettings(ctx context.Context, project string) (*agentdb.ProjectSettings, error)
	ListWorkers(ctx context.Context, project string) ([]*agentdb.Worker, error)
	ListProjectSkills(ctx context.Context, q agentdb.SkillCatalogQuery) ([]*agentdb.SkillSummary, error)
	GetProjectSkill(ctx context.Context, project, name string) (*agentdb.Skill, error)
	ListSubscriptions(ctx context.Context, project string) ([]*agentdb.Subscription, error)
	ListSchedules(ctx context.Context, project string) ([]*agentdb.Schedule, error)
	ListCustomImageVersions(ctx context.Context, q agentdb.ImageCatalogQuery) ([]*agentdb.CustomImage, error)
	// SearchMemories and GetMemory are the two halves of §E's document load,
	// and they are exactly the pair loadGitDocuments (gitdocuments.go, G14)
	// needs: SearchMemories enumerates the `name=` values — its rows carry a
	// 500-byte SNIPPET, never the content, so they must never be rendered —
	// and GetMemory re-reads each winner IN FULL. Publishing the snippet would
	// put a truncated document in the repository and the importer would then
	// read that back as the document's real content.
	SearchMemories(ctx context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error)
	GetMemory(ctx context.Context, project, id string) (*agentdb.Memory, error)
	// ListConfigEvents answers "is this project's projection behind?" at boot.
	ListConfigEvents(ctx context.Context, q agentdb.ConfigEventQuery) ([]*agentdb.ConfigEvent, error)
}

var _ gitProjectionStore = (*agentdb.Store)(nil)

// ── durable state: the watermark, the lease and the last failure (§F) ───────

// gitProjectionRecord is one project's projection state.
//
// It is an ALIAS for agentdb.GitProjectionState, not a copy: the row, its
// columns and its accessors live in agentdb (agentdb/gitprojection.go,
// migration 048), where every other table in this product lives. G8 created
// this table with AutoMigrate at boot because its file budget was three files
// here; G22 moved it, because a table that appears by side effect is invisible
// in the migration list, cannot be reviewed as a schema change, and can differ
// between a fresh database and an upgraded one.
type gitProjectionRecord = agentdb.GitProjectionState

// gitProjectionState is the durable half, behind an interface so the tests
// drive an in-memory one and the loops are exercised without a database.
type gitProjectionState interface {
	Get(ctx context.Context, project string) (gitProjectionRecord, error)
	// AcquireLease is a compare-and-swap: it succeeds when the row holds no
	// lease, holds an EXPIRED one, or already holds THIS owner's. Re-entrancy
	// for the same owner is deliberate — the render loop and the push loop are
	// two goroutines in one process and must not deadlock each other over a
	// row. Cross-process exclusion is what the lease is for.
	AcquireLease(ctx context.Context, project, owner string, until int64) (bool, error)
	ReleaseLease(ctx context.Context, project, owner string) error
	MarkRendered(ctx context.Context, project string, seq int64, sha string) error
	MarkPushed(ctx context.Context, project, sha string) error
	// MarkImported advances the INBOUND watermark (§F). It takes the SHA the
	// importer returned as its watermark — which on a quarantined push is the
	// OLD one, because a watermark that advanced past a rejected commit would
	// swallow the human's edit forever.
	MarkImported(ctx context.Context, project, sha string) error
	// NoteFailure records BOTH what kind of failure this was and why.
	//
	// 🔴 The kind is decided HERE, by the caller, at the point the error is
	// raised and its Go type is still in hand — gitProjectionErrorKind reads
	// gitproj's own sentinels with errors.Is. It is not a label for the console
	// to be polite with: it is what a consumer classifies on, and it exists
	// because the alternative — which is what shipped first — was the status
	// route re-deriving the kind by string-matching gitproj's error text, so
	// that rewording one sentence silently reclassified a production failure.
	NoteFailureKind(ctx context.Context, project, kind, reason string) error
	// NoteFailure is NoteFailureKind for the one caller that cannot name a
	// kind: gitbackfill.go, which reports a stopped walk as a formatted
	// sentence with the cause already flattened into it.
	//
	// 🔴 It is the FALLBACK, and the only place text matching survives: the
	// kind is re-derived from the message with gitProjectionErrorKindFromText.
	// Everything else calls NoteFailureKind with a kind decided from the
	// error's TYPE. Collapse this away when that caller can pass one.
	NoteFailure(ctx context.Context, project, reason string) error
	// PutNotes replaces this project's per-file notes of one kind (G23):
	// agentdb.GitProjectionNoteQuarantine or ...NoteIgnored. Called on EVERY
	// import, including a clean one — passing no entries is what clears a stale
	// quarantine, and a red banner that outlives the push that caused it
	// teaches an operator to ignore the banner.
	PutNotes(ctx context.Context, project, kind string, notes []agentdb.GitProjectionNote) error
	ProjectsWithRemote(ctx context.Context) ([]string, error)
}

// gitProjectionErrorKind names WHICH KIND of failure an error is, for
// agentdb.GitProjectionState.LastErrorKind.
//
// It reads gitproj's sentinels with errors.Is rather than its messages with
// strings.Contains, which is the entire point: a sentinel may be reworded at
// any time, and the wording is not what anything downstream should depend on.
// gitProjectionErrorKindFromText is the LAST RESORT: reading the kind back out
// of a message, for a caller that has already flattened its error into prose.
// It is the exact thing the kind column exists to stop being the mechanism, and
// it is confined to this one function so that it is visible.
func gitProjectionErrorKindFromText(reason string) string {
	switch {
	case strings.Contains(reason, gitUnrenderableMarker):
		return agentdb.GitProjectionErrorUnrenderable
	case strings.Contains(reason, gitproj.ErrNotFastForward.Error()):
		return agentdb.GitProjectionErrorNotFastForward
	default:
		return agentdb.GitProjectionErrorOther
	}
}

func gitProjectionErrorKind(err error) string {
	switch {
	case err == nil:
		return agentdb.GitProjectionErrorNone
	case errors.Is(err, gitproj.ErrUnrenderable):
		return agentdb.GitProjectionErrorUnrenderable
	case errors.Is(err, gitproj.ErrNotFastForward):
		return agentdb.GitProjectionErrorNotFastForward
	default:
		return agentdb.GitProjectionErrorOther
	}
}

// gitProjectionStateStore is the slice of *agentdb.Store that backs the
// interface above. Declared as an interface so the wiring can be tested and so
// this file keeps depending on behaviour rather than on a concrete store.
type gitProjectionStateStore interface {
	GetGitProjectionState(ctx context.Context, project string) (*agentdb.GitProjectionState, error)
	AcquireGitProjectionLease(ctx context.Context, project, owner string, until int64) (bool, error)
	ReleaseGitProjectionLease(ctx context.Context, project, owner string) error
	MarkGitProjectionRendered(ctx context.Context, project string, seq int64, sha string) error
	MarkGitProjectionPushed(ctx context.Context, project, sha string) error
	MarkGitProjectionImported(ctx context.Context, project, sha string) error
	NoteGitProjectionFailure(ctx context.Context, project, kind, reason string) error
	PutGitProjectionNotes(ctx context.Context, project, kind string, notes []agentdb.GitProjectionNote) error
	ListProjectsWithGitRemote(ctx context.Context) ([]string, error)
}

// gitProjectionTable adapts the store's accessors to the loops' vocabulary. It
// creates nothing: the table is migration 048's.
type gitProjectionTable struct{ store gitProjectionStateStore }

func newGitProjectionTable(store gitProjectionStateStore) (*gitProjectionTable, error) {
	if store == nil {
		return nil, errors.New("gitprojection: a store is required")
	}
	return &gitProjectionTable{store: store}, nil
}

func (t *gitProjectionTable) Get(ctx context.Context, project string) (gitProjectionRecord, error) {
	rec, err := t.store.GetGitProjectionState(ctx, project)
	if err != nil {
		return gitProjectionRecord{}, err
	}
	return *rec, nil
}

func (t *gitProjectionTable) AcquireLease(ctx context.Context, project, owner string, until int64) (bool, error) {
	return t.store.AcquireGitProjectionLease(ctx, project, owner, until)
}

func (t *gitProjectionTable) ReleaseLease(ctx context.Context, project, owner string) error {
	return t.store.ReleaseGitProjectionLease(ctx, project, owner)
}

func (t *gitProjectionTable) MarkRendered(ctx context.Context, project string, seq int64, sha string) error {
	return t.store.MarkGitProjectionRendered(ctx, project, seq, sha)
}

func (t *gitProjectionTable) MarkPushed(ctx context.Context, project, sha string) error {
	return t.store.MarkGitProjectionPushed(ctx, project, sha)
}

func (t *gitProjectionTable) MarkImported(ctx context.Context, project, sha string) error {
	return t.store.MarkGitProjectionImported(ctx, project, sha)
}

func (t *gitProjectionTable) NoteFailureKind(ctx context.Context, project, kind, reason string) error {
	return t.store.NoteGitProjectionFailure(ctx, project, kind, reason)
}

func (t *gitProjectionTable) NoteFailure(ctx context.Context, project, reason string) error {
	return t.store.NoteGitProjectionFailure(ctx, project, gitProjectionErrorKindFromText(reason), reason)
}

func (t *gitProjectionTable) PutNotes(ctx context.Context, project, kind string, notes []agentdb.GitProjectionNote) error {
	return t.store.PutGitProjectionNotes(ctx, project, kind, notes)
}

func (t *gitProjectionTable) ProjectsWithRemote(ctx context.Context) ([]string, error) {
	return t.store.ListProjectsWithGitRemote(ctx)
}

var _ gitProjectionState = (*gitProjectionTable)(nil)

// truncateReason keeps a failure readable in a console cell and stops a git
// error page from becoming a database row nobody can display.
func truncateReason(s string) string {
	const max = 2000
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// ── the projector ───────────────────────────────────────────────────────────

// gitPending is what the hook accumulated for one project between two drains.
//
// Several config events can land while a render is in flight. We render ONCE —
// RenderTree is a pure function of CURRENT state, so rendering per-event would
// produce commits whose trailers claimed seq N while their content was the
// state at seq N+3, which is worse than coalescing and saying so. The newest
// event is attributed; Count records that others were folded into it.
type gitPending struct {
	newest *agentdb.ConfigEvent
	count  int
}

type gitProjectorConfig struct {
	Store gitProjectionStore
	State gitProjectionState
	// Root is the directory holding one clone per project. On the cluster it
	// is on the agentd-data PVC (deploy/k8s/20-agent-orange.yaml).
	Root string
	// Owner identifies this process for the lease. Two agentd processes must
	// never share one.
	Owner string
	// MapTokenEnv returns the project map's `github_token_env` for a project,
	// or "". It is the FALLBACK half of DI3's precedence rule; the settings
	// column wins. See resolveGitTokenEnv, which is the single place that
	// decides.
	MapTokenEnv func(project string) string
	// Getenv reads the token value by variable name. Injected so tests never
	// touch the process environment.
	Getenv func(string) string
	Logf   func(string, ...any)
	// Interval is the render loop's safety tick (the hook is what normally
	// wakes it) and, doubled through backoff, the push loop's cadence.
	Interval time.Duration
	// LeaseTTL bounds how long a crashed process holds a project.
	LeaseTTL time.Duration
}

type gitProjector struct {
	cfg gitProjectorConfig

	mu    sync.Mutex
	dirty map[string]*gitPending
	wake  chan struct{}

	// repos caches one *gitproj.Repo per project. The Repo owns its own mutex;
	// this one only guards the map.
	repoMu sync.Mutex
	repos  map[string]*gitproj.Repo
	// work serialises the working-tree operations of the two loops WITHIN this
	// process. gitproj.Repo has its own mutex per method, which is not enough:
	// "fast-forward then render then commit" must be one uninterrupted sequence
	// or a push loop's `merge --ff-only` can land between a WriteTree and its
	// Commit. 🔴 The push loop takes it for fetch and fast-forward and RELEASES
	// it before the network push (kill #5, design §F).
	works map[string]*sync.Mutex

	// pushAfter is the per-project backoff deadline. A diverged remote or a
	// GitHub outage must never block a write — the commits pile up locally and
	// the loop catches up — so failure here only slows this loop down.
	pushMu    sync.Mutex
	pushAfter map[string]time.Time
	pushFails map[string]int
}

const (
	gitProjectionDefaultInterval = 30 * time.Second
	gitProjectionDefaultLease    = 5 * time.Minute
	gitPushMaxBackoff            = 15 * time.Minute
)

func newGitProjector(cfg gitProjectorConfig) (*gitProjector, error) {
	if cfg.Store == nil {
		return nil, errors.New("gitprojection: a store is required")
	}
	if cfg.State == nil {
		return nil, errors.New("gitprojection: a state store is required")
	}
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, errors.New("gitprojection: a clone root is required")
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return nil, errors.New("gitprojection: a lease owner is required")
	}
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if cfg.MapTokenEnv == nil {
		cfg.MapTokenEnv = func(string) string { return "" }
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Interval <= 0 {
		cfg.Interval = gitProjectionDefaultInterval
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = gitProjectionDefaultLease
	}
	return &gitProjector{
		cfg:       cfg,
		dirty:     map[string]*gitPending{},
		wake:      make(chan struct{}, 1),
		repos:     map[string]*gitproj.Repo{},
		works:     map[string]*sync.Mutex{},
		pushAfter: map[string]time.Time{},
		pushFails: map[string]int{},
	}, nil
}

// Hook is the post-commit consumer. 🔴 Rule 1: it does no git and no IO. It
// takes a mutex held for a handful of instructions and a non-blocking send.
func (p *gitProjector) Hook() agentdb.ConfigEventHook {
	return func(_ context.Context, ev *agentdb.ConfigEvent) { p.markDirty(ev) }
}

func (p *gitProjector) markDirty(ev *agentdb.ConfigEvent) {
	if ev == nil || strings.TrimSpace(ev.Project) == "" {
		return
	}
	p.mu.Lock()
	pend := p.dirty[ev.Project]
	if pend == nil {
		pend = &gitPending{}
		p.dirty[ev.Project] = pend
	}
	pend.count++
	// Seq is the authority on order (§F), so "newest" is a numeric comparison
	// and never a timestamp or an arrival order.
	if pend.newest == nil || ev.Seq >= pend.newest.Seq {
		pend.newest = ev
	}
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default: // already awake; one wake covers any number of dirty projects
	}
}

func (p *gitProjector) drain() map[string]*gitPending {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.dirty) == 0 {
		return nil
	}
	out := p.dirty
	p.dirty = map[string]*gitPending{}
	return out
}

// requeue puts a project back after a transient failure, without losing an
// event that arrived while we were working.
func (p *gitProjector) requeue(project string, pend *gitPending) {
	if pend == nil || pend.newest == nil {
		return
	}
	p.markDirty(pend.newest)
}

// Run is the render loop. It reconciles once at boot and then drains the dirty
// set whenever the hook wakes it, with a slow tick as the backstop.
func (p *gitProjector) Run(ctx context.Context) {
	if err := p.Reconcile(ctx); err != nil {
		p.cfg.Logf("[agentd] git projection: boot reconciliation: %v", err)
	}
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-ticker.C:
		}
		p.RenderPending(ctx)
	}
}

// Reconcile is boot repair (§F). For every project that projects, it compares
// the durable watermark against the newest config event and enqueues the ones
// that are behind — including a project whose process died between the local
// commit and the watermark write, which re-renders to no change and simply
// advances the watermark.
//
// A project with a git_remote always has at least one config event, because
// setting git_remote goes through PutProjectSettings. A project with none is
// therefore nothing to publish, and is skipped rather than committed with a
// synthesised provenance.
func (p *gitProjector) Reconcile(ctx context.Context) error {
	projects, err := p.cfg.State.ProjectsWithRemote(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		// 🔴 Every projected project is fast-forwarded onto the remote, whether
		// or not it has anything to render. A project whose watermark is
		// already caught up would otherwise never pick up a human's commit —
		// and the first later render would then diverge.
		if err := p.SyncProject(ctx, project); err != nil {
			p.cfg.Logf("[agentd] git projection: %s: %v", project, err)
		}
		rec, err := p.cfg.State.Get(ctx, project)
		if err != nil {
			p.cfg.Logf("[agentd] git projection: %s: %v", project, err)
			continue
		}
		events, err := p.cfg.Store.ListConfigEvents(ctx, agentdb.ConfigEventQuery{Project: project, Limit: 1})
		if err != nil {
			p.cfg.Logf("[agentd] git projection: %s: read config log: %v", project, err)
			continue
		}
		if len(events) == 0 {
			continue
		}
		newest := events[0]
		if rec.LastRenderedSHA != "" && rec.LastRenderedSeq >= newest.Seq {
			continue
		}
		p.cfg.Logf("[agentd] git projection: %s is behind (rendered seq %d, log at seq %d) — rendering forward",
			project, rec.LastRenderedSeq, newest.Seq)
		p.markDirty(newest)
	}
	return nil
}

// SyncProject fetches one project and fast-forwards its local branch onto the
// remote. It renders and commits nothing.
//
// It is the half of boot reconciliation that G11 depends on: the importer reads
// from the remote tip and leaves the local branch where it is, so somebody has
// to advance it, and doing so is safe only here — where the lease is held and
// no render is in flight.
func (p *gitProjector) SyncProject(ctx context.Context, project string) error {
	ps, err := p.cfg.Store.GetProjectSettings(ctx, project)
	if err != nil {
		return fmt.Errorf("read project settings: %w", err)
	}
	if ps == nil || strings.TrimSpace(ps.GitRemote) == "" {
		return nil
	}
	ok, err := p.cfg.State.AcquireLease(ctx, project, p.cfg.Owner, time.Now().Add(p.cfg.LeaseTTL).Unix())
	if err != nil || !ok {
		return err
	}
	defer func() { _ = p.cfg.State.ReleaseLease(context.WithoutCancel(ctx), project, p.cfg.Owner) }()

	work := p.workLock(project)
	work.Lock()
	defer work.Unlock()

	repo, err := p.ensureRepo(ctx, project, ps)
	if err != nil {
		return err
	}
	_, err = p.syncFromRemote(ctx, project, repo)
	return err
}

// RenderPending drains the dirty set and renders each project. One project's
// failure never stops another's (rule 4): the loop records the reason and moves
// on, in a deterministic project order so a log is readable.
func (p *gitProjector) RenderPending(ctx context.Context) {
	pending := p.drain()
	if len(pending) == 0 {
		return
	}
	projects := make([]string, 0, len(pending))
	for project := range pending {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		if err := p.RenderProject(ctx, project, pending[project]); err != nil {
			p.cfg.Logf("[agentd] git projection: %s: %v", project, err)
			_ = p.cfg.State.NoteFailureKind(ctx, project, gitProjectionErrorKind(err), err.Error())
			p.requeue(project, pending[project])
		}
	}
}

// RenderProject renders one project and commits if anything moved.
//
// The returned error means "try again": a transient store or git failure. A
// project that simply cannot render (rule 4) is NOT an error — the reason is
// recorded on its state row and nil comes back, because retrying a literal
// secret forever would only fill the log.
func (p *gitProjector) RenderProject(ctx context.Context, project string, pend *gitPending) error {
	if pend == nil || pend.newest == nil {
		return nil
	}
	ps, err := p.cfg.Store.GetProjectSettings(ctx, project)
	if err != nil {
		return fmt.Errorf("read project settings: %w", err)
	}
	if ps == nil || strings.TrimSpace(ps.GitRemote) == "" {
		return nil // projection is off for this project — do nothing, quietly
	}

	ok, err := p.cfg.State.AcquireLease(ctx, project, p.cfg.Owner, time.Now().Add(p.cfg.LeaseTTL).Unix())
	if err != nil {
		return err
	}
	if !ok {
		// Another agentd holds this project. §F: one writer per clone. Not an
		// error and not a retry — the holder is doing the work.
		p.cfg.Logf("[agentd] git projection: %s is held by another writer; skipping", project)
		return nil
	}
	defer func() { _ = p.cfg.State.ReleaseLease(context.WithoutCancel(ctx), project, p.cfg.Owner) }()

	// One uninterrupted sequence: fast-forward, render, commit. See workLock.
	work := p.workLock(project)
	work.Lock()
	defer work.Unlock()

	repo, err := p.ensureRepo(ctx, project, ps)
	if err != nil {
		return err
	}
	// Pick up anything a human pushed BEFORE committing, so our commit sits on
	// top of theirs rather than beside it.
	if _, err := p.syncFromRemote(ctx, project, repo); err != nil {
		return err
	}
	sub := gitSubfolderOf(ps)

	st, skipped, err := p.loadProjectState(ctx, project, ps)
	if err != nil {
		return fmt.Errorf("read project state: %w", err)
	}
	p.logSkippedDocuments(project, skipped)

	tree, err := gitproj.RenderTree(st, sub)
	if err != nil {
		if errors.Is(err, gitproj.ErrUnrenderable) {
			// §D. The tree is not written and NOTHING is published — refusal,
			// not redaction. The error names the dotted field path and never
			// quotes the offending value (DI8), so it is safe to log and to
			// show in the console.
			p.cfg.Logf("[agentd] git projection: %s does not render: %v — fix the field and it will publish", project, err)
			return p.cfg.State.NoteFailureKind(ctx, project, agentdb.GitProjectionErrorUnrenderable, err.Error())
		}
		return fmt.Errorf("render: %w", err)
	}

	changed, err := repo.WriteTree(ctx, tree, sub)
	if err != nil {
		return fmt.Errorf("write tree: %w", err)
	}
	if !changed {
		// 🔴 Rule 3. The published tree already says what the database says, so
		// there is nothing to record. The watermark still advances: this render
		// DID happen and re-doing it would find the same nothing.
		head, err := repo.HeadSHA(ctx)
		if err != nil && !errors.Is(err, gitproj.ErrNoCommits) {
			return err
		}
		if err := p.cfg.State.MarkRendered(ctx, project, pend.newest.Seq, head); err != nil {
			return err
		}
		return p.noteSkippedDocuments(ctx, project, skipped)
	}

	sha, err := repo.Commit(ctx,
		gitCommitSubject(pend.newest),
		gitCommitBody(pend.newest, pend.count),
		commitTrailers(pend.newest))
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if err := p.cfg.State.MarkRendered(ctx, project, pend.newest.Seq, sha); err != nil {
		// The commit landed and the watermark did not. Boot reconciliation
		// repairs exactly this: the re-render finds no change and advances the
		// watermark. Reported, not hidden.
		return fmt.Errorf("commit %s landed but the watermark did not advance: %w", short(sha), err)
	}
	p.cfg.Logf("[agentd] git projection: %s committed %s (seq %d, %s)", project, short(sha), pend.newest.Seq, pend.newest.Action)
	return p.noteSkippedDocuments(ctx, project, skipped)
}

// workLock is the per-project working-tree lock. Never held across a push.
func (p *gitProjector) workLock(project string) *sync.Mutex {
	p.repoMu.Lock()
	defer p.repoMu.Unlock()
	mu, ok := p.works[project]
	if !ok {
		mu = &sync.Mutex{}
		p.works[project] = mu
	}
	return mu
}

// syncFromRemote fetches and, when the remote is strictly ahead, fast-forwards
// the LOCAL branch onto it. The caller must hold the project's work lock.
//
// 🔴 This exists because G11 (the importer) reads and imports from the REMOTE
// tip and deliberately does not move the local branch — advancing it is this
// loop's job (design §F, boot reconciliation). Skip it and the two histories
// part company the instant any human pushes: the next render commits on a stale
// base, and G9 then refuses every push as non-fast-forward, correctly and
// permanently, with no resolution that does not destroy somebody's work.
//
// It is FAST-FORWARD ONLY, like everything else on this path. A remote that has
// genuinely diverged is left exactly as it is and reported by the push loop; a
// remote we are already ahead of is left alone; nothing is ever rebased, merged
// or reset onto a foreign history.
//
// A fetch that fails is NOT an error: a GitHub outage must never block a write,
// so the render carries on against the local clone and catches up later.
func (p *gitProjector) syncFromRemote(ctx context.Context, project string, repo *gitproj.Repo) (bool, error) {
	if err := repo.Fetch(ctx); err != nil {
		p.cfg.Logf("[agentd] git projection: %s: fetch failed (%v) — rendering against the local clone", project, err)
		return false, nil
	}
	return p.fastForwardLocal(ctx, project, repo)
}

// fastForwardLocal is syncFromRemote without the fetch, for the push loop,
// which needs a fetch failure to be an error rather than a shrug. The caller
// must hold the project's work lock.
func (p *gitProjector) fastForwardLocal(ctx context.Context, project string, repo *gitproj.Repo) (bool, error) {
	remoteHead, err := repo.RemoteSHA(ctx)
	if err != nil {
		return false, err
	}
	if remoteHead == "" {
		return false, nil // the branch does not exist on the remote yet
	}
	head, err := repo.HeadSHA(ctx)
	if err != nil {
		if !errors.Is(err, gitproj.ErrNoCommits) {
			return false, err
		}
		head = "" // unborn: everything on the remote is new to us
	}
	if head == remoteHead {
		return false, nil
	}
	if head != "" {
		ahead, err := repo.IsAncestor(ctx, remoteHead, head)
		if err != nil {
			return false, err
		}
		if ahead {
			return false, nil // we hold unpushed commits; the push loop's business
		}
		behind, err := repo.IsAncestor(ctx, head, remoteHead)
		if err != nil {
			return false, err
		}
		if !behind {
			// Genuinely diverged. Say so once, here, and change nothing.
			p.cfg.Logf("[agentd] git projection: %s has diverged from origin/%s — not fast-forwarding. "+
				"A human must reconcile the remote; the local commits are intact.", project, repo.Branch())
			return false, nil
		}
		// A render that crashed between WriteTree and Commit leaves a staged
		// index, which blocks a fast-forward. Discarding it is safe and is the
		// right answer: the render is about to be redone from the database,
		// which is the only place the truth lives.
		if p.workTreeDirty(ctx, repo) {
			if _, stderr, err := runGit(ctx, repo.Path(), "reset", "--hard", "HEAD"); err != nil {
				return false, fmt.Errorf("clear a half-written render before fast-forwarding: %w: %s", err, strings.TrimSpace(stderr))
			}
		}
	}
	if _, stderr, err := runGit(ctx, repo.Path(), "merge", "--ff-only", remoteHead); err != nil {
		return false, fmt.Errorf("fast-forward onto origin/%s: %w: %s", repo.Branch(), err, strings.TrimSpace(stderr))
	}
	p.cfg.Logf("[agentd] git projection: %s fast-forwarded to %s from origin/%s", project, short(remoteHead), repo.Branch())
	return true, nil
}

// workTreeDirty reports whether tracked files differ from HEAD, in the index or
// on disk. Untracked files are excluded: WriteTree sweeps those itself.
func (p *gitProjector) workTreeDirty(ctx context.Context, repo *gitproj.Repo) bool {
	_, _, err := runGit(ctx, repo.Path(), "diff", "--quiet", "HEAD")
	return err != nil
}

// ── the commit message ──────────────────────────────────────────────────────

// gitCommitSubject is `<action>: <entity key>` — the §15.9 entity form, so a
// `git log --oneline` reads like the changelog it mirrors.
func gitCommitSubject(ev *agentdb.ConfigEvent) string {
	if ref, err := agentdb.EntityRefFor(ev); err == nil && ref.Key != "" {
		return ev.Action + ": " + oneLine(ref.Key)
	}
	return ev.Action
}

// gitCommitBody is the rationale, verbatim.
//
// 🔴 It is MODEL TEXT (a worker's reason for rewriting another worker's
// prompt), which is exactly why it goes here and not into the trailers. See
// DI4 and gitproj.Commit: our trailer block is always the final paragraph, so a
// rationale ending in `Orange-Seq: 999999` lands in an earlier one, where git's
// trailer parser does not look.
func gitCommitBody(ev *agentdb.ConfigEvent, coalesced int) string {
	body := strings.TrimSpace(ev.Rationale)
	if coalesced > 1 {
		note := fmt.Sprintf("(%d configuration changes were published together; this commit is attributed to the newest, seq %d.)",
			coalesced, ev.Seq)
		if body == "" {
			return note
		}
		return body + "\n\n" + note
	}
	return body
}

// commitTrailers derives the commit's provenance FROM THE CONFIG EVENT (DI4).
// Nothing here is read from a file, a commit message or model output.
//
// 🔴 The map is never empty: gitproj.Commit's forgery defence — always emitting
// our own trailer paragraph last — holds only while there is a trailer
// paragraph to emit. Project, Seq, Event and Action are therefore
// unconditional. The two actor trailers are omitted when empty, which is the
// log's own encoding for a human/UI/API edit; an empty `Orange-Actor-Worker:`
// would read as an assertion about a person rather than the absence of one.
func commitTrailers(ev *agentdb.ConfigEvent) map[string]string {
	t := map[string]string{
		"Orange-Project": oneLine(ev.Project),
		"Orange-Seq":     strconv.FormatInt(ev.Seq, 10),
		"Orange-Event":   oneLine(ev.ID),
		"Orange-Action":  oneLine(ev.Action),
	}
	if w := strings.TrimSpace(ev.ActorWorker); w != "" {
		t["Orange-Actor-Worker"] = oneLine(w)
	}
	if s := strings.TrimSpace(ev.ActorSession); s != "" {
		t["Orange-Actor-Session"] = oneLine(s)
	}
	return t
}

// oneLine flattens a value so it cannot break out of its trailer line. Every
// value here comes from the database, but a trailer that could carry a newline
// would be a second forgery route and the cost of closing it is one function.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// ── reading the project's state ─────────────────────────────────────────────

func gitSubfolderOf(ps *agentdb.ProjectSettings) string {
	// DI3 rule 2: the column is stored empty rather than defaulted on write, so
	// the default is applied at READ time. An empty subfolder reaching path
	// construction would render at the repository root, over the project's own
	// files and next to `.github/`.
	if ps != nil && strings.TrimSpace(ps.GitSubfolder) != "" {
		return ps.GitSubfolder
	}
	return agentdb.DefaultGitSubfolder
}

func gitBranchOf(ps *agentdb.ProjectSettings) string {
	if ps != nil && strings.TrimSpace(ps.GitBranch) != "" {
		return ps.GitBranch
	}
	return agentdb.DefaultGitBranch
}

// loadProjectState collects everything RenderTree publishes. It is all reads,
// and it is deliberately one function so the set of things that can appear in
// the repository is visible in one place.
// It also returns the documents that could NOT be rendered — a memory whose
// `name=` label is a legal label value but not a legal path segment. They are
// reported, never fatal: failing the render would let any worker holding
// memory_create stop a whole project from projecting.
func (p *gitProjector) loadProjectState(ctx context.Context, project string, ps *agentdb.ProjectSettings) (gitproj.ProjectState, []gitDocumentSkip, error) {
	st := gitproj.ProjectState{Settings: ps}

	workers, err := p.cfg.Store.ListWorkers(ctx, project)
	if err != nil {
		return st, nil, fmt.Errorf("workers: %w", err)
	}
	st.Workers = workers

	// Skills: list the names (cheap columns only), then fetch each newest
	// revision in full. ListProjectSkills already reduces to one entry per name.
	summaries, err := p.cfg.Store.ListProjectSkills(ctx, agentdb.SkillCatalogQuery{Project: project})
	if err != nil {
		return st, nil, fmt.Errorf("skills: %w", err)
	}
	for _, s := range summaries {
		sk, err := p.cfg.Store.GetProjectSkill(ctx, project, s.Name)
		if err != nil {
			return st, nil, fmt.Errorf("skill %q: %w", s.Name, err)
		}
		st.Skills = append(st.Skills, sk)
	}

	subs, err := p.cfg.Store.ListSubscriptions(ctx, project)
	if err != nil {
		return st, nil, fmt.Errorf("subscriptions: %w", err)
	}
	st.Subscriptions = subs

	scheds, err := p.cfg.Store.ListSchedules(ctx, project)
	if err != nil {
		return st, nil, fmt.Errorf("schedules: %w", err)
	}
	st.Schedules = scheds

	images, err := p.cfg.Store.ListCustomImageVersions(ctx, agentdb.ImageCatalogQuery{Project: project})
	if err != nil {
		return st, nil, fmt.Errorf("images: %w", err)
	}
	st.Images = images

	docs, err := loadGitDocuments(ctx, p.cfg.Store, project)
	if err != nil {
		return st, nil, err
	}
	st.Documents = docs.Documents
	return st, docs.Skipped, nil
}

// loadDocuments is §E: the NEWEST memory per `name=` label, in full, and
// nothing else. The raw memory log does not render — it is written once and
// never edited, so a diff would show nothing, and it is the high-volume path
// where git's costs land.
//
// It is a thin wrapper over loadGitDocuments (gitdocuments.go, G14), which owns
// the two things the interim load here got wrong: it re-reads every winner with
// GetMemory (a search row carries a 500-byte snippet, so rendering search
// results published TRUNCATED documents) and it pages the whole set rather than
// stopping at the newest hundred and dropping the rest in silence.
//
// The skips are LOGGED here rather than returned, because this signature is the
// backfill's (gitbackfill.go, G10). The render path calls loadProjectState,
// which hands them back so the state row can carry them.
func (p *gitProjector) loadDocuments(ctx context.Context, project string) ([]*agentdb.Memory, error) {
	docs, err := loadGitDocuments(ctx, p.cfg.Store, project)
	if err != nil {
		return nil, err
	}
	p.logSkippedDocuments(project, docs.Skipped)
	return docs.Documents, nil
}

// logSkippedDocuments says, once per render, which named memories exist but
// cannot become files. The name is safe to log: it is a label value, and the
// reason names no content.
func (p *gitProjector) logSkippedDocuments(project string, skips []gitDocumentSkip) {
	for _, sk := range skips {
		p.cfg.Logf("[agentd] git projection: %s: document %q is not published: %s", project, sk.Name, sk.Reason)
	}
}

// skippedDocumentNotice is what the state row carries for the console: a
// project renders, and separately some of its documents do not. There is one
// durable channel for that (LastError), so the notice is written AFTER the
// render's MarkRendered has cleared it, and its wording says plainly that the
// render itself succeeded.
// noteSkippedDocuments records the notice, or nothing at all when every
// document rendered. It runs AFTER MarkRendered, which clears the reason: a
// render that succeeded must not leave a stale failure, and a document that is
// still unpublished must not vanish from the console.
func (p *gitProjector) noteSkippedDocuments(ctx context.Context, project string, skips []gitDocumentSkip) error {
	if len(skips) == 0 {
		return nil
	}
	// "other", not "unrenderable": the project's tree WAS published, minus some
	// documents. "unrenderable" is the console's word for §D refusing to write
	// any tree at all, and telling an operator the repo has stopped moving when
	// it has not would send them looking for the wrong thing.
	return p.cfg.State.NoteFailureKind(ctx, project, agentdb.GitProjectionErrorOther,
		skippedDocumentNotice(skips))
}

func skippedDocumentNotice(skips []gitDocumentSkip) string {
	names := make([]string, 0, len(skips))
	for _, sk := range skips {
		names = append(names, sk.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("published, but %d document(s) are not: %s — a memory's name= label may contain \".\" and \"_\", a file name may not. Write the document under a renderable name and it appears.",
		len(names), strings.Join(names, ", "))
}

// ── the clone ───────────────────────────────────────────────────────────────

// ensureRepo returns the project's clone, creating it if it is missing.
//
// It does NOT let gitproj.Clone run the network clone, and that is deliberate:
// a private remote needs the credential helper configured BEFORE the first
// fetch, and the helper lives in the clone's own `.git/config`, which does not
// exist until the clone does. So the order is: init, wire the remote, install
// the helper, fetch — and only then hand the directory to gitproj.Clone, which
// finds a valid work tree with the right origin and simply attaches to it and
// checks out the branch.
func (p *gitProjector) ensureRepo(ctx context.Context, project string, ps *agentdb.ProjectSettings) (*gitproj.Repo, error) {
	branch := gitBranchOf(ps)
	remote := strings.TrimSpace(ps.GitRemote)

	p.repoMu.Lock()
	cached := p.repos[project]
	p.repoMu.Unlock()
	if cached != nil && cached.Remote() == remote && cached.Branch() == branch {
		// The credential may have been re-pointed in the console since the
		// clone was opened, so the helper is re-applied every time. It is one
		// `git config` and it writes a variable NAME, never a secret.
		if err := p.configureCredentials(ctx, cached.Path(), project, ps); err != nil {
			return nil, err
		}
		return cached, nil
	}

	path := filepath.Join(p.cfg.Root, gitCloneDirName(project))
	if err := p.prepareClone(ctx, path, project, ps, branch, remote); err != nil {
		return nil, err
	}
	repo, err := gitproj.Clone(ctx, path, remote, branch)
	if err != nil {
		return nil, fmt.Errorf("open clone: %w", err)
	}
	p.repoMu.Lock()
	p.repos[project] = repo
	p.repoMu.Unlock()
	return repo, nil
}

// gitCloneDirName keeps a project id from addressing anything but a child of
// the clone root. Project ids are already kebab-case (validProjectID), so this
// is defence in depth rather than the only check.
func gitCloneDirName(project string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, project)
	if safe == "" || safe == "." || safe == ".." {
		return "_"
	}
	return safe
}

func (p *gitProjector) prepareClone(ctx context.Context, path, project string, ps *agentdb.ProjectSettings, branch, remote string) error {
	if isGitWorkTreeRoot(ctx, path) {
		if err := p.configureCredentials(ctx, path, project, ps); err != nil {
			return err
		}
		if _, _, err := runGit(ctx, path, "remote", "set-url", "origin", remote); err != nil {
			return fmt.Errorf("point origin at the configured remote: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create clone directory: %w", err)
	}
	if _, stderr, err := runGit(ctx, path, "init", "-b", branch); err != nil {
		return fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(stderr))
	}
	if _, stderr, err := runGit(ctx, path, "remote", "add", "origin", remote); err != nil {
		return fmt.Errorf("git remote add: %w: %s", err, strings.TrimSpace(stderr))
	}
	if err := p.configureCredentials(ctx, path, project, ps); err != nil {
		return err
	}
	// A remote that is empty, or a branch that does not exist there yet, is a
	// normal first-projection state and not a failure.
	if _, stderr, err := runGit(ctx, path, "fetch", "--no-tags", "origin", branch); err != nil {
		p.cfg.Logf("[agentd] git projection: %s: initial fetch of %s found nothing to start from (%s)",
			project, branch, strings.TrimSpace(stderr))
	}
	return nil
}

func isGitWorkTreeRoot(ctx context.Context, path string) bool {
	out, _, err := runGit(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	top, err := filepath.EvalSymlinks(strings.TrimSpace(out))
	if err != nil {
		return false
	}
	self, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return top == self
}

// ── the push credential (🔴 DI3) ────────────────────────────────────────────

// ErrNoGitToken is "this project has no push credential". It names both places
// a credential can be configured, because the whole failure mode DI3 records is
// that a deployment sets one of them and nothing says the other was the one
// being read.
var ErrNoGitToken = errors.New("gitprojection: no push credential")

// resolveGitTokenEnv is THE one place that decides which environment variable
// holds a project's push token, and it is the only reason both candidates are
// mentioned anywhere in this file.
//
// 🔴 DI3 rule 1, now enforced: **ProjectSettings.GitTokenEnv wins; the project
// map's `github_token_env` is the fallback.** The column wins because it is the
// one a human can change at runtime without a redeploy, and the one the
// renderer is documented to read.
//
// It returns the variable NAME, never the value, and it refuses loudly when
// neither candidate resolves rather than letting an unauthenticated push
// happen: an anonymous push to a private repository fails with a 404 that reads
// like a missing repository, which is the single most misleading way this could
// go wrong.
func resolveGitTokenEnv(project string, ps *agentdb.ProjectSettings, mapEnv func(string) string, getenv func(string) string) (string, error) {
	settingsName := ""
	if ps != nil {
		settingsName = strings.TrimSpace(ps.GitTokenEnv)
	}
	mapName := ""
	if mapEnv != nil {
		mapName = strings.TrimSpace(mapEnv(project))
	}

	name := settingsName
	source := "project_settings.git_token_env"
	if name == "" {
		name, source = mapName, "the project map's github_token_env"
	}
	if name == "" {
		return "", fmt.Errorf("%w for project %q: neither project_settings.git_token_env nor the project map's "+
			"github_token_env names one (the settings column wins when both are set)", ErrNoGitToken, project)
	}
	// The name is interpolated into a shell snippet below, so it must be a
	// plausible variable name and nothing else. envVarName is the same regexp
	// the project map is validated with at boot (googleauth.go).
	if !envVarName.MatchString(name) {
		return "", fmt.Errorf("%w for project %q: %s is %q, which is not a valid environment variable name",
			ErrNoGitToken, project, source, name)
	}
	if getenv != nil && getenv(name) == "" {
		return "", fmt.Errorf("%w for project %q: %s names %s, which is unset or empty in this process's environment",
			ErrNoGitToken, project, source, name)
	}
	return name, nil
}

// configureCredentials installs a credential helper in the clone's own config.
//
// # How the token reaches git without leaking
//
// The helper is a one-line shell function that reads the variable BY NAME from
// the environment agentd already has. What is written to disk, to `git config`
// and to any process listing is the variable NAME. The value:
//
//   - is never written into the remote URL (which would put it in .git/config,
//     in `git remote -v`, and in every `ps` line of every git subprocess);
//   - is never passed as an argument (`printf` is a POSIX shell BUILTIN, so the
//     helper forks nothing and the expanded value never becomes argv);
//   - is never logged — every error in this file names the variable, not what
//     is in it;
//   - is only offered for the remote's own scheme://host, because the helper is
//     installed under `credential.<base>.helper` rather than as a global one.
//
// The existing helper chain is reset first (an empty `credential.helper` is
// git's documented way to clear the list), so a helper configured on the host
// or in /etc cannot silently supply a different identity.
//
// A remote that is not http(s) — a local path in a test, or an ssh URL — needs
// no helper and requires no token: ssh authenticates by key, and a local path
// by filesystem permission. Demanding one there would make the whole loop
// untestable without inventing a credential that authenticates nothing.
func (p *gitProjector) configureCredentials(ctx context.Context, path, project string, ps *agentdb.ProjectSettings) error {
	base, ok := gitCredentialBase(ps.GitRemote)
	if !ok {
		return nil
	}
	name, err := resolveGitTokenEnv(project, ps, p.cfg.MapTokenEnv, p.cfg.Getenv)
	if err != nil {
		return err
	}
	if _, _, err := runGit(ctx, path, "config", "--local", "--replace-all", "credential.helper", ""); err != nil {
		return fmt.Errorf("reset the credential helper chain: %w", err)
	}
	if _, stderr, err := runGit(ctx, path, "config", "--local", "--replace-all",
		"credential."+base+".helper", gitCredentialHelper(name)); err != nil {
		return fmt.Errorf("install the credential helper: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

// gitCredentialBase is the `scheme://host` the credential is scoped to. It
// returns false for anything that is not http(s) — a local path or an ssh URL,
// neither of which uses a credential helper.
func gitCredentialBase(remote string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(remote))
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

// gitCredentialHelper builds the helper snippet. `name` has already been
// checked against envVarName, so nothing attacker-shaped can reach the shell.
//
// The `test "$1" = get` guard means the helper answers only credential lookups:
// git's `store` and `erase` calls do nothing, so a failed push can never cause
// the token to be written into ~/.git-credentials.
func gitCredentialHelper(name string) string {
	return `!f() { test "$1" = get && printf 'username=x-access-token\npassword=%s\n' "${` + name + `}"; }; f`
}

// ── the push loop (G9) ──────────────────────────────────────────────────────
//
// Separate from the render loop on purpose. 🔴 A GitHub outage must never block
// a write: agents keep working, the config log keeps its order, commits pile up
// in the local clone, and this loop catches up when the remote returns. Nothing
// in the render path waits on this one, and gitproj.Repo.Push does its
// fast-forward check under the repo lock and then RELEASES it before the
// network leg — so a hanging remote cannot wedge a render either.

// RunPush is the push loop.
func (p *gitProjector) RunPush(ctx context.Context) {
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		p.PushAll(ctx)
	}
}

// PushAll pushes every project that projects. One project's failure never stops
// another's, and a failing project backs off instead of hammering the remote.
func (p *gitProjector) PushAll(ctx context.Context) {
	projects, err := p.cfg.State.ProjectsWithRemote(ctx)
	if err != nil {
		p.cfg.Logf("[agentd] git push: list projects: %v", err)
		return
	}
	for _, project := range projects {
		if ctx.Err() != nil {
			return
		}
		if !p.pushDue(project) {
			continue
		}
		if err := p.PushProject(ctx, project); err != nil {
			// A divergence has already said its piece, at length, in
			// PushProject; anything else is reported here.
			if !errors.Is(err, gitproj.ErrNotFastForward) {
				p.cfg.Logf("[agentd] git push: %s: %v", project, err)
			}
			_ = p.cfg.State.NoteFailureKind(ctx, project, gitProjectionErrorKind(err), err.Error())
			p.pushFailed(project)
			continue
		}
		p.pushSucceeded(project)
	}
}

// PushProject fetches and pushes one project, FAST-FORWARD ONLY.
//
// 🔴 On divergence it surfaces the fact and stops. It never forces, never
// force-with-leases, never rebases and never merges: every automatic resolution
// silently destroys somebody's work (§C), and the local commits are the only
// copy of what the database decided. They stay exactly where they are, and the
// next push after a human has reconciled the remote succeeds on its own.
func (p *gitProjector) PushProject(ctx context.Context, project string) error {
	ps, err := p.cfg.Store.GetProjectSettings(ctx, project)
	if err != nil {
		return fmt.Errorf("read project settings: %w", err)
	}
	if ps == nil || strings.TrimSpace(ps.GitRemote) == "" {
		return nil
	}

	ok, err := p.cfg.State.AcquireLease(ctx, project, p.cfg.Owner, time.Now().Add(p.cfg.LeaseTTL).Unix())
	if err != nil {
		return err
	}
	if !ok {
		return nil // another writer owns this clone
	}
	defer func() { _ = p.cfg.State.ReleaseLease(context.WithoutCancel(ctx), project, p.cfg.Owner) }()

	repo, err := p.ensureRepo(ctx, project, ps)
	if err != nil {
		return err
	}

	// The fetch and the fast-forward run under the work lock; 🔴 the push does
	// NOT (kill #5). A hanging remote must never be able to wedge a render.
	head, remoteHead, err := func() (string, string, error) {
		work := p.workLock(project)
		work.Lock()
		defer work.Unlock()
		if err := repo.Fetch(ctx); err != nil {
			return "", "", fmt.Errorf("fetch: %w", err)
		}
		// A human's commit is picked up here too, not only at boot: this is the
		// point where we NOTICE the remote has moved (G11's requirement).
		if _, err := p.fastForwardLocal(ctx, project, repo); err != nil {
			return "", "", err
		}
		head, err := repo.HeadSHA(ctx)
		if err != nil {
			if errors.Is(err, gitproj.ErrNoCommits) {
				return "", "", nil // nothing rendered yet
			}
			return "", "", err
		}
		remoteHead, err := repo.RemoteSHA(ctx)
		if err != nil {
			return "", "", err
		}
		return head, remoteHead, nil
	}()
	if err != nil {
		return err
	}
	if head == "" {
		return nil
	}
	if remoteHead == head {
		return p.cfg.State.MarkPushed(ctx, project, head)
	}

	if err := repo.Push(ctx); err != nil {
		if errors.Is(err, gitproj.ErrNotFastForward) {
			// Surfaced and left alone. The local commits survive untouched, and
			// the error is RETURNED so the caller backs off — this is the one
			// failure a retry cannot fix, and only a human reconciling the
			// remote will clear it.
			p.cfg.Logf("[agentd] git push: %s DIVERGED from origin/%s — refusing to force, rebase or merge. "+
				"The local commits are intact and will push once a human reconciles the remote. (%v)",
				project, repo.Branch(), err)
		}
		return err
	}
	p.cfg.Logf("[agentd] git push: %s published %s to origin/%s", project, short(head), repo.Branch())
	return p.cfg.State.MarkPushed(ctx, project, head)
}

func (p *gitProjector) pushDue(project string) bool {
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	after, ok := p.pushAfter[project]
	return !ok || !time.Now().Before(after)
}

// pushFailed doubles the wait, capped. The cap matters more than the curve: a
// remote that is down for an hour must not leave a project unpublished for a
// day once it comes back.
func (p *gitProjector) pushFailed(project string) {
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	p.pushFails[project]++
	wait := p.cfg.Interval << min(p.pushFails[project], 10)
	if wait > gitPushMaxBackoff || wait <= 0 {
		wait = gitPushMaxBackoff
	}
	p.pushAfter[project] = time.Now().Add(wait)
}

func (p *gitProjector) pushSucceeded(project string) {
	p.pushMu.Lock()
	defer p.pushMu.Unlock()
	delete(p.pushFails, project)
	delete(p.pushAfter, project)
}

// ── plumbing ────────────────────────────────────────────────────────────────

// runGit is the small set of git calls gitproj.Repo does not expose (init,
// remote wiring, config). Same hardening as gitproj and gitimport: no hooks, no
// credential prompt, no locale surprises, and safe.directory set so a clone
// owned by another uid inside a container is still usable.
//
// It deliberately does NOT set user.name/user.email: nothing here commits. The
// fixed projection identity is gitproj.Repo's.
func runGit(ctx context.Context, dir string, args ...string) (string, string, error) {
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "gc.auto=0",
		"-c", "safe.directory=" + dir,
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// ── boot wiring ─────────────────────────────────────────────────────────────

// gitProjectionCloneRoot is where the per-project clones live. On the cluster
// this sits on the existing agentd-data PVC.
func gitProjectionCloneRoot(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("AGENTKIT_GIT_CLONE_ROOT")); v != "" {
		return v
	}
	return "/data/git-projection"
}

// gitProjectionOwner names this process for the lease. Hostname plus pid, so
// two agentd processes on one host still exclude each other.
func gitProjectionOwner() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "agentd"
	}
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}

// gitProjectionFullStore is everything the projection reads and writes: the
// product-layer rows it renders, and its own state row (G22). *agentdb.Store
// satisfies it.
type gitProjectionFullStore interface {
	gitProjectionStore
	gitProjectionStateStore
}

// installGitProjection builds the projector and starts both loops, returning
// the hook to fan the config-event seam into and the projector itself. A nil
// hook means projection is not running (no database, or git is not installed)
// and the caller fans out only the `config.changed` emitter; the projector is
// nil in exactly the same cases, and the webhook route is not mounted.
//
// git being absent is a warning, not a fatal: a deployment that projects no
// project should not fail to boot because a binary it never calls is missing.
func installGitProjection(ctx context.Context, store gitProjectionFullStore, mapTokenEnv func(string) string, logf func(string, ...any)) (agentdb.ConfigEventHook, *gitProjector) {
	if store == nil {
		return nil, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		logf("[agentd] git projection: git is not on PATH — projection and import are OFF")
		return nil, nil
	}
	state, err := newGitProjectionTable(store)
	if err != nil {
		logf("[agentd] git projection: %v — projection is OFF", err)
		return nil, nil
	}
	root := gitProjectionCloneRoot(os.Getenv)
	if err := os.MkdirAll(root, 0o755); err != nil {
		logf("[agentd] git projection: clone root %s is unusable (%v) — projection is OFF", root, err)
		return nil, nil
	}
	proj, err := newGitProjector(gitProjectorConfig{
		Store:       store,
		State:       state,
		Root:        root,
		Owner:       gitProjectionOwner(),
		MapTokenEnv: mapTokenEnv,
		Getenv:      os.Getenv,
		Logf:        logf,
	})
	if err != nil {
		logf("[agentd] git projection: %v — projection is OFF", err)
		return nil, nil
	}
	// 🔴 THE BACKFILL RUNS BEFORE THE RENDER LOOP'S RECONCILE, AND THAT ORDER IS
	// THE WHOLE POINT (G10). Reconcile answers "this project is behind the log"
	// by rendering CURRENT state as one lumped commit. For a project that has
	// never rendered, that throws away the entire history the backfill exists
	// to produce — and it is not recoverable afterwards without deleting the
	// repository, because the watermark then says the project is caught up.
	//
	// Reconcile is the first thing Run does, so the sequencing lives here
	// rather than in main.go: by the time installGitProjection returns, the
	// loop would already have started and the two would race. It runs in this
	// goroutine, not synchronously, so a slow clone cannot hold up boot.
	// BackfillPending is resumable and idempotent — a caught-up project is a
	// no-op — so calling it on every boot is safe and cheap. It does not push;
	// RunPush owns that.
	go func() {
		if err := proj.BackfillPending(ctx); err != nil {
			logf("[agentd] git backfill: %v — the render loop will start anyway", err)
		}
		proj.Run(ctx)
	}()
	go proj.RunPush(ctx)
	logf("[agentd] git projection: rendering into %s (projects with an empty git_remote do not project)", root)
	return proj.Hook(), proj
}

// gitTokenEnvFromProjectMap adapts the parsed project map to the FALLBACK half
// of DI3's precedence rule. It returns the map's `github_token_env` for a
// project, or "" — the settings column is consulted first, in
// resolveGitTokenEnv, which is the one place that decides.
func gitTokenEnvFromProjectMap(cfg *projectSettings) func(string) string {
	return func(project string) string {
		if cfg == nil {
			return ""
		}
		return cfg.projects[project].GitHubTokenEnv
	}
}
