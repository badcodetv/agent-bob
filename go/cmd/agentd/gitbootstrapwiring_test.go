package main

// Tests for G26's host half: the thing that actually runs a bootstrap when an
// operator asks for one.
//
// Everything here runs against a local bare repository in t.TempDir() and the
// in-memory stores the import and projection tests already use — no network, no
// database, no Docker. The bootstrap itself is proven in gitbootstrap_test.go
// (the render→bootstrap→fold-equal round trip); what is proven HERE is the four
// things that made it unreachable and unsafe to call:
//
//   - it refuses a project that already has configuration, and writes nothing;
//   - it advances the import watermark, so the next ordinary import is a diff
//     of nothing rather than a re-import of the whole folder as human edits;
//   - it records what it IGNORED, durably, and that record reaches the console;
//   - it takes the projection lease, so it cannot run beside a render.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
)

// ── fakes ───────────────────────────────────────────────────────────────────

// fakeGitBootstrapStore is the import store plus the two listings the refusal
// needs. Embedding rather than reimplementing is deliberate: the bootstrap door
// writes through exactly the same store methods the import door does, and a
// second definition of "the store" would let the two drift.
type fakeGitBootstrapStore struct {
	*fakeGitImportStore
	workerRows []*agentdb.Worker
	skillRows  []*agentdb.SkillSummary
	listErr    error
}

func (f *fakeGitBootstrapStore) ListWorkers(context.Context, string) ([]*agentdb.Worker, error) {
	return f.workerRows, f.listErr
}

func (f *fakeGitBootstrapStore) ListProjectSkills(_ context.Context, _ agentdb.SkillCatalogQuery) ([]*agentdb.SkillSummary, error) {
	return f.skillRows, f.listErr
}

var _ gitBootstrapStore = (*fakeGitBootstrapStore)(nil)

// gitBootstrapFakeState is the projection state fake, plus the two accessors
// the STATUS adapter reads. One object, so a test can bootstrap and then ask
// the real status route what an operator would see.
type gitBootstrapFakeState struct {
	*fakeProjectionState
}

func (f *gitBootstrapFakeState) GetGitProjectionState(ctx context.Context, project string) (*agentdb.GitProjectionState, error) {
	st, err := f.Get(ctx, project)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func (f *gitBootstrapFakeState) GetGitProjectionNotes(_ context.Context, project string) ([]agentdb.GitProjectionNote, []agentdb.GitProjectionNote, error) {
	return f.notesFor(project, agentdb.GitProjectionNoteQuarantine),
		f.notesFor(project, agentdb.GitProjectionNoteIgnored), nil
}

var (
	_ gitProjectionState       = (*gitBootstrapFakeState)(nil)
	_ gitProjectionStatusStore = (*gitBootstrapFakeState)(nil)
)

// ── the rig ─────────────────────────────────────────────────────────────────

type bootstrapRig struct {
	t     *testing.T
	repo  *gitImportRepo
	store *fakeGitBootstrapStore
	state *gitBootstrapFakeState
	proj  *gitProjector
	w     *gitBootstrapWiring
	logs  []string
}

// newBootstrapRig builds a project whose git_remote is a real bare repository
// in t.TempDir(), with a projector that will clone it exactly as production
// does.
func newBootstrapRig(t *testing.T) *bootstrapRig {
	t.Helper()
	g := newGitImportRepo(t) // skips when git is not installed

	rig := &bootstrapRig{
		t:     t,
		repo:  g,
		store: &fakeGitBootstrapStore{fakeGitImportStore: newFakeGitImportStore()},
		state: &gitBootstrapFakeState{fakeProjectionState: newFakeProjectionState(gitImportProject)},
	}
	// The project points at the bare repository. The subfolder is the default
	// one the export was rendered into.
	rig.store.settings.GitRemote = g.remote
	rig.store.settings.GitBranch = "main"
	rig.store.settings.GitSubfolder = gitBootstrapSubfolder

	proj, err := newGitProjector(gitProjectorConfig{
		Store:  newFakeProjectionStore(),
		State:  rig.state,
		Root:   t.TempDir(),
		Owner:  "test/1",
		Getenv: func(string) string { return "" },
		Logf:   func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	rig.proj = proj

	rig.w = newGitBootstrapWiring(func(f string, a ...any) {
		rig.logs = append(rig.logs, f)
	})
	rig.w.enable(rig.store, proj)
	return rig
}

// publish renders a whole project state into the bare repository, plus any
// extra files, and returns the commit. This is "somebody exported a project and
// pushed the folder", which is the only way a bootstrap is ever reached.
func (r *bootstrapRig) publish(extra map[string]*string) string {
	r.t.Helper()
	sha := gitBootstrapExport(r.t, r.repo, gitBootstrapStateA("wolf-a"), extra)
	r.repo.push()
	return sha
}

func (r *bootstrapRig) run() (*httpapi.GitBootstrapReport, error) {
	return r.w.BootstrapProject(context.Background(), gitImportProject)
}

func (r *bootstrapRig) watermark() string {
	r.t.Helper()
	st, err := r.state.Get(context.Background(), gitImportProject)
	if err != nil {
		r.t.Fatalf("read state: %v", err)
	}
	return st.LastImportedSHA
}

// ── 🔴 the entry point works end to end ─────────────────────────────────────

// The headline: an operator points an empty project at a folder somebody
// exported, calls the route's seam once, and gets a configured project — with
// the watermark set and the ignored list recorded.
func TestGitBootstrapWiringCreatesTheProject(t *testing.T) {
	rig := newBootstrapRig(t)
	sha := rig.publish(nil)

	// A stale quarantine from some earlier attempt. A clean run must clear it:
	// a red banner outliving the push that fixed it teaches an operator to
	// ignore banners.
	if err := rig.state.PutNotes(context.Background(), gitImportProject,
		agentdb.GitProjectionNoteQuarantine,
		[]agentdb.GitProjectionNote{{Path: "orange/workers/old.md", Reason: "stale"}}); err != nil {
		t.Fatalf("seed quarantine: %v", err)
	}

	res, err := rig.run()
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if res.Quarantined {
		t.Fatalf("clean folder quarantined: %+v", res.Failures)
	}
	if res.SHA != sha || res.Watermark != sha {
		t.Fatalf("sha/watermark = %q/%q, want %q", res.SHA, res.Watermark, sha)
	}
	if len(res.Applied) == 0 {
		t.Fatalf("nothing was applied from a folder of %d files", res.Files)
	}
	if len(rig.store.workers) == 0 {
		t.Fatalf("the store has no workers after a bootstrap: %+v", rig.store.methods())
	}

	// 🔴 The watermark is what makes the next ordinary import a diff of nothing
	// instead of a re-import of the whole folder as though a human had written
	// it by hand.
	if got := rig.watermark(); got != sha {
		t.Fatalf("import watermark = %q, want the bootstrapped commit %q", got, sha)
	}

	// Both note lists were written, INCLUDING the empty one.
	if got := rig.state.notesFor(gitImportProject, agentdb.GitProjectionNoteQuarantine); len(got) != 0 {
		t.Fatalf("a clean bootstrap left a stale quarantine in place: %+v", got)
	}
	if len(res.Ignored) == 0 {
		t.Fatalf("the export contained an images/ file and the four git fields; nothing was reported as ignored")
	}
	if got := rig.state.notesFor(gitImportProject, agentdb.GitProjectionNoteIgnored); len(got) != len(res.Ignored) {
		t.Fatalf("recorded %d ignored notes, the run reported %d", len(got), len(res.Ignored))
	}
}

// The recorded ignored list is only worth writing if an operator can see it.
// This drives the REAL status route, through the REAL adapter, exactly as
// main() wires them.
func TestGitBootstrapIgnoredNotesReachTheStatusRoute(t *testing.T) {
	rig := newBootstrapRig(t)
	rig.publish(nil)
	if _, err := rig.run(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	api, err := httpapi.New(httpapi.Config{
		Runner:          &stubRunner{},
		Store:           newFakeRouterStore(),
		Identity:        identityFromRequest,
		ProjectSettings: &fakeGitStatusSettings{ps: &rig.store.settings},
		GitProjection:   &gitProjectionStatusSource{store: rig.state},
	})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	h := apiAuthMiddleware([]byte("test-secret"), wolfKeys(t), api.Mux())

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agent/git-projection", nil)
	req.Header.Set("X-API-Key", goodKey)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status route = %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ignored, _ := body["ignored"].([]any)
	if len(ignored) == 0 {
		t.Fatalf("the console cannot see what the bootstrap ignored: %s", rr.Body.String())
	}
	if q, _ := body["quarantine"].([]any); len(q) != 0 {
		t.Fatalf("a clean bootstrap left a quarantine on the status route: %v", q)
	}
}

// ── 🔴 the refusal ──────────────────────────────────────────────────────────

// Bootstrap creates a project FROM a folder. Merging a foreign folder into a
// live project would rewrite prompts an architect wrote and wake subscriptions
// nobody reviewed, one config event at a time, with no single act to undo. So
// it refuses — and NOTHING is written, including the watermark, so the refusal
// leaves no trace that could make the next import behave differently.
func TestGitBootstrapWiringRefusesAConfiguredProject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		seed  func(*fakeGitBootstrapStore)
		wants string
	}{
		{"workers", func(s *fakeGitBootstrapStore) {
			s.workerRows = []*agentdb.Worker{{Name: "architect"}, {Name: "copywriter"}}
		}, "2 workers"},
		{"one skill", func(s *fakeGitBootstrapStore) {
			s.skillRows = []*agentdb.SkillSummary{{Name: "house-style"}}
		}, "1 skill"},
		{"schedules", func(s *fakeGitBootstrapStore) {
			s.scheds["daily"] = agentdb.Schedule{ID: "daily"}
		}, "1 schedule"},
		{"subscriptions", func(s *fakeGitBootstrapStore) {
			s.subs["sub-1"] = agentdb.Subscription{ID: "sub-1"}
		}, "1 subscription"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newBootstrapRig(t)
			rig.publish(nil)
			tc.seed(rig.store)

			res, err := rig.run()
			if res != nil {
				t.Fatalf("a refused bootstrap returned a report: %+v", res)
			}
			var conflict *httpapi.GitBootstrapConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("error = %v, want a conflict", err)
			}
			if !strings.Contains(conflict.Error(), tc.wants) {
				t.Fatalf("the refusal does not name %q: %s", tc.wants, conflict.Error())
			}

			// 🔴 Nothing written. Not one config event, not the watermark.
			if got := rig.store.writes(); len(got) != 0 {
				t.Fatalf("a refused bootstrap wrote to the store %d time(s): %+v", len(got), got)
			}
			if got := rig.watermark(); got != "" {
				t.Fatalf("a refused bootstrap advanced the watermark to %q", got)
			}
			// And it let go of the lease, so a render is not locked out by a
			// refusal.
			st, _ := rig.state.Get(context.Background(), gitImportProject)
			if st.LeaseOwner != "" {
				t.Fatalf("the lease is still held by %q after a refusal", st.LeaseOwner)
			}
		})
	}
}

// ── 🔴 all or nothing ───────────────────────────────────────────────────────

// One malformed file bootstraps NOTHING — a half-bootstrapped project is worse
// than a failed one, because it looks configured — and the watermark does not
// move, so the whole folder is retried once the file is fixed.
func TestGitBootstrapWiringQuarantineWritesNothingAndDoesNotMoveTheWatermark(t *testing.T) {
	rig := newBootstrapRig(t)
	bad := "---\nenabled: [this is not a bool\n---\n\nYou break things.\n"
	rig.publish(map[string]*string{
		gitBootstrapSubfolder + "/workers/saboteur.md": &bad,
	})

	res, err := rig.run()
	if err != nil {
		t.Fatalf("a quarantine must be a reported outcome, not an error: %v", err)
	}
	if !res.Quarantined {
		t.Fatalf("a malformed file did not quarantine the bootstrap")
	}
	if len(res.Applied) != 0 || len(rig.store.writes()) != 0 {
		t.Fatalf("a quarantined bootstrap wrote something: applied=%+v writes=%+v", res.Applied, rig.store.writes())
	}
	if res.Watermark != "" {
		t.Fatalf("report watermark = %q, want empty after a quarantine", res.Watermark)
	}
	if got := rig.watermark(); got != "" {
		t.Fatalf("a quarantined bootstrap advanced the watermark to %q — the folder would never be retried", got)
	}
	// The per-file reason is recorded, because "your folder was rejected" with
	// no file and no reason is not something anybody can act on.
	notes := rig.state.notesFor(gitImportProject, agentdb.GitProjectionNoteQuarantine)
	if len(notes) != 1 || !strings.Contains(notes[0].Path, "saboteur") {
		t.Fatalf("quarantine notes = %+v, want the saboteur file", notes)
	}
	st, _ := rig.state.Get(context.Background(), gitImportProject)
	if st.LastErrorKind != agentdb.GitProjectionErrorQuarantined {
		t.Fatalf("last_error_kind = %q, want %q — an inbound rejection must not read as a push failure",
			st.LastErrorKind, agentdb.GitProjectionErrorQuarantined)
	}
}

// ── the lease ───────────────────────────────────────────────────────────────

// A bootstrap takes the same lease the render loop and the webhook import take,
// so it cannot run concurrently with a render of the same project. Another
// process holding it is an honest 409, not a silent no-op: the import loop can
// skip a tick for free, but an operator pressing a button once and being told
// "0 files" would conclude their folder was empty.
func TestGitBootstrapWiringRefusesWhenAnotherWriterHoldsTheLease(t *testing.T) {
	rig := newBootstrapRig(t)
	rig.publish(nil)

	ok, err := rig.state.AcquireLease(context.Background(), gitImportProject, "other-process/1",
		time.Now().Add(5*time.Minute).Unix())
	if err != nil || !ok {
		t.Fatalf("seed the lease: ok=%v err=%v", ok, err)
	}

	res, err := rig.run()
	if res != nil {
		t.Fatalf("a bootstrap ran while another writer held the lease: %+v", res)
	}
	if !errors.Is(err, httpapi.ErrGitBootstrapBusy) {
		t.Fatalf("error = %v, want ErrGitBootstrapBusy", err)
	}
	if got := rig.store.writes(); len(got) != 0 {
		t.Fatalf("a locked-out bootstrap wrote to the store: %+v", got)
	}
	// The other process still holds it: we must not have stolen or released it.
	st, _ := rig.state.Get(context.Background(), gitImportProject)
	if st.LeaseOwner != "other-process/1" {
		t.Fatalf("lease owner = %q, want the other process still holding it", st.LeaseOwner)
	}
}

// ── the two states that are not faults ──────────────────────────────────────

// A project with no git_remote has no folder to bootstrap from. That is a
// settings step nobody has done, answered as such — never a 500.
func TestGitBootstrapWiringWithoutARemote(t *testing.T) {
	rig := newBootstrapRig(t)
	rig.store.settings.GitRemote = ""

	if _, err := rig.run(); !errors.Is(err, httpapi.ErrGitBootstrapNotConfigured) {
		t.Fatalf("error = %v, want ErrGitBootstrapNotConfigured", err)
	}
}

// Not armed: the sqlite fallback, where there is no product layer and no
// projector. The route 501s off the back of this rather than pretending.
func TestGitBootstrapWiringIsUnavailableUntilEnabled(t *testing.T) {
	w := newGitBootstrapWiring(nil)
	if _, err := w.BootstrapProject(context.Background(), "wolf"); !errors.Is(err, httpapi.ErrGitBootstrapUnavailable) {
		t.Fatalf("error = %v, want ErrGitBootstrapUnavailable", err)
	}
	// enable() with nothing behind it changes nothing.
	w.enable(nil, nil)
	if _, err := w.BootstrapProject(context.Background(), "wolf"); !errors.Is(err, httpapi.ErrGitBootstrapUnavailable) {
		t.Fatalf("after a no-op enable, error = %v, want ErrGitBootstrapUnavailable", err)
	}
}

// ── project scoping ─────────────────────────────────────────────────────────

// The project is whatever the route's claim said, and it is what every store
// read and every git path is keyed by. A bootstrap of "wolf" must never read or
// write another project's rows.
func TestGitBootstrapWiringActsOnlyOnTheNamedProject(t *testing.T) {
	rig := newBootstrapRig(t)
	rig.publish(nil)

	if _, err := rig.run(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	for name, w := range rig.store.workers {
		if w.Project != gitImportProject {
			t.Fatalf("worker %q was written into project %q, want %q", name, w.Project, gitImportProject)
		}
	}
	if rig.store.settings.Project != gitImportProject {
		t.Fatalf("settings project = %q, want %q", rig.store.settings.Project, gitImportProject)
	}
	// The state row it touched is the named project's and no other.
	if len(rig.state.rows) != 1 {
		t.Fatalf("touched %d projection state rows, want exactly wolf's", len(rig.state.rows))
	}
}
