package main

// Tests for G23's half of GET /agent/git-projection: the per-file notes reach
// the console, and HEALTH is decided by the state row's KIND rather than by
// matching the engine's error text.
//
// These go through the REAL mux and the REAL route — httpapi.New, the same
// authentication middleware main() wires — because the thing being proved is
// that a finished route nobody edited starts reporting real data once the
// adapter in this package is handed to it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/gitproj"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
)

// ── a store that behaves like agentdb's, without a database ──────────────────

type fakeGitStatusStore struct {
	state      map[string]*agentdb.GitProjectionState
	quarantine map[string][]agentdb.GitProjectionNote
	ignored    map[string][]agentdb.GitProjectionNote
}

func (f *fakeGitStatusStore) GetGitProjectionState(_ context.Context, project string) (*agentdb.GitProjectionState, error) {
	if st, ok := f.state[project]; ok {
		return st, nil
	}
	return &agentdb.GitProjectionState{Project: project}, nil
}

func (f *fakeGitStatusStore) GetGitProjectionNotes(_ context.Context, project string) ([]agentdb.GitProjectionNote, []agentdb.GitProjectionNote, error) {
	return f.quarantine[project], f.ignored[project], nil
}

// fakeGitStatusSettings is httpapi.ProjectSettingsStore over one row.
type fakeGitStatusSettings struct{ ps *agentdb.ProjectSettings }

func (f *fakeGitStatusSettings) GetProjectSettings(context.Context, string) (*agentdb.ProjectSettings, error) {
	return f.ps, nil
}

func (f *fakeGitStatusSettings) PutProjectSettings(_ context.Context, ps *agentdb.ProjectSettings, _ agentdb.ConfigWrite) (*agentdb.ProjectSettings, error) {
	f.ps = ps
	return ps, nil
}

// gitStatusBody drives the real route as the "wolf" project and returns the
// decoded response.
func gitStatusBody(t *testing.T, store *fakeGitStatusStore) map[string]any {
	t.Helper()
	api, err := httpapi.New(httpapi.Config{
		Runner:          &stubRunner{},
		Store:           newFakeRouterStore(),
		Identity:        identityFromRequest,
		ProjectSettings: &fakeGitStatusSettings{ps: &agentdb.ProjectSettings{Project: "wolf", GitRemote: "https://github.com/badcode/wolf.git"}},
		GitProjection:   &gitProjectionStatusSource{store: store},
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
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return body
}

// TestGitProjectionStatusReportsQuarantine: the operator whose push was
// rejected wholesale is told WHICH FILE and WHY. Before G23 this list was empty
// forever on any real deployment, because nothing persisted it.
func TestGitProjectionStatusReportsQuarantine(t *testing.T) {
	body := gitStatusBody(t, &fakeGitStatusStore{
		state: map[string]*agentdb.GitProjectionState{"wolf": {
			Project:         "wolf",
			LastRenderedSHA: "aaa111",
			LastPushedSHA:   "aaa111",
			LastError:       "inbound commit quarantined: orange/workers/copywriter.md: cron is not valid",
			LastErrorKind:   agentdb.GitProjectionErrorQuarantined,
		}},
		quarantine: map[string][]agentdb.GitProjectionNote{"wolf": {
			{Path: "orange/workers/copywriter.md", Reason: "cron is not valid", NotedAt: 1789000000},
		}},
	})

	if body["health"] != httpapi.GitProjectionQuarantined {
		t.Fatalf("health = %v, want quarantined (an inbound rejection is not a publish failure)", body["health"])
	}
	list, _ := body["quarantine"].([]any)
	if len(list) != 1 {
		t.Fatalf("want 1 quarantine entry in the response, got %v", body["quarantine"])
	}
	first, _ := list[0].(map[string]any)
	if first["path"] != "orange/workers/copywriter.md" || first["reason"] != "cron is not valid" {
		t.Errorf("the entry must carry the file and the reason, got %v", first)
	}
	if first["at"] != float64(1789000000) {
		t.Errorf("the entry must carry when it happened, got %v", first["at"])
	}
}

// TestGitProjectionStatusReportsIgnored is DI10's silence, closed: a human
// deletes a skill file, nothing happens, and now something says why.
func TestGitProjectionStatusReportsIgnored(t *testing.T) {
	body := gitStatusBody(t, &fakeGitStatusStore{
		state: map[string]*agentdb.GitProjectionState{"wolf": {
			Project: "wolf", LastRenderedSHA: "aaa111", LastPushedSHA: "aaa111",
		}},
		ignored: map[string][]agentdb.GitProjectionNote{"wolf": {
			{Path: "orange/skills/research.md", Reason: "skills are append-only; deleting the file removes nothing"},
		}},
	})

	// An ignored edit is NOT a failure: the projection is healthy and the note
	// is an explanation, not an alarm.
	if body["health"] != httpapi.GitProjectionOK {
		t.Fatalf("health = %v, want ok — an ignored edit is not a failure", body["health"])
	}
	list, _ := body["ignored"].([]any)
	if len(list) != 1 {
		t.Fatalf("want 1 ignored entry, got %v", body["ignored"])
	}
	if first, _ := list[0].(map[string]any); first["path"] != "orange/skills/research.md" {
		t.Errorf("the entry must name the file, got %v", first)
	}
}

// TestGitProjectionStatusHealthComesFromTheKind is the point of the kind
// column. The stored MESSAGE deliberately matches none of the engine's
// sentinels — as it would the day somebody rewords one — and the health must
// still be right, because it is decided by the kind that was recorded where the
// error's Go type was known.
func TestGitProjectionStatusHealthComesFromTheKind(t *testing.T) {
	cases := []struct {
		name, kind, message, want string
	}{
		{
			name:    "unrenderable",
			kind:    agentdb.GitProjectionErrorUnrenderable,
			message: "the attention channel holds a literal and will not be published",
			want:    httpapi.GitProjectionUnrenderable,
		},
		{
			name:    "diverged",
			kind:    agentdb.GitProjectionErrorNotFastForward,
			message: "somebody rewrote the remote branch",
			want:    httpapi.GitProjectionDiverged,
		},
		{
			name:    "anything else",
			kind:    agentdb.GitProjectionErrorOther,
			message: "dial tcp: connection refused",
			want:    httpapi.GitProjectionFailing,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := gitStatusBody(t, &fakeGitStatusStore{
				state: map[string]*agentdb.GitProjectionState{"wolf": {
					Project:         "wolf",
					LastRenderedSHA: "aaa111",
					LastPushedSHA:   "aaa111", // not behind: "failing", not "push_failing"
					LastError:       c.message,
					LastErrorKind:   c.kind,
				}},
			})
			if body["health"] != c.want {
				t.Fatalf("health = %v, want %v — the KIND is the authority, not the wording", body["health"], c.want)
			}
		})
	}
}

// TestGitProjectionStatusCleanImportClearsTheQuarantine: a stale red banner
// after a successful push is its own bug — it teaches an operator to ignore the
// banner, which is the one thing it must never do.
func TestGitProjectionStatusCleanImportClearsTheQuarantine(t *testing.T) {
	body := gitStatusBody(t, &fakeGitStatusStore{
		state: map[string]*agentdb.GitProjectionState{"wolf": {
			Project: "wolf", LastRenderedSHA: "aaa111", LastPushedSHA: "aaa111",
			// The store cleared both when the next run succeeded.
			LastError: "", LastErrorKind: agentdb.GitProjectionErrorNone,
		}},
		quarantine: map[string][]agentdb.GitProjectionNote{}, // replaced with nothing
	})
	if body["health"] != httpapi.GitProjectionOK {
		t.Fatalf("health = %v, want ok after a clean import", body["health"])
	}
	if list, _ := body["quarantine"].([]any); len(list) != 0 {
		t.Fatalf("the quarantine must be empty after a clean import, got %v", list)
	}
}

// TestGitProjectionStatusQuarantineWithoutNotesStillReportsWhy: the one case
// where the summary sentence is kept. With no per-file notes to replace it,
// "failing, and here is the sentence" beats a bare "quarantined" with nothing
// underneath it.
func TestGitProjectionStatusQuarantineWithoutNotesStillReportsWhy(t *testing.T) {
	body := gitStatusBody(t, &fakeGitStatusStore{
		state: map[string]*agentdb.GitProjectionState{"wolf": {
			Project: "wolf", LastRenderedSHA: "aaa111", LastPushedSHA: "aaa111",
			LastError:     "inbound commit quarantined: something did not parse",
			LastErrorKind: agentdb.GitProjectionErrorQuarantined,
		}},
	})
	if body["health"] != httpapi.GitProjectionFailing {
		t.Fatalf("health = %v, want failing when there are no notes to show", body["health"])
	}
	if body["last_error"] == "" {
		t.Fatal("the summary must survive when it is the only thing there is")
	}
}

// TestGitProjectionStatusSourceIsNilWithoutAStore pins the typed-nil trap: on
// the sqlite fallback agentDB is a nil *agentdb.Store, and handing httpapi a
// non-nil interface wrapping it would sail past the route's nil check and call
// through a nil receiver.
func TestGitProjectionStatusSourceIsNilWithoutAStore(t *testing.T) {
	if src := newGitProjectionStatusSource(nil); src != nil {
		t.Fatal("no store must yield a nil INTERFACE, not a typed nil")
	}
}

// TestGitProjectionUnrenderableMarkerTracksTheEngine: the marker this package
// checks for is derived from gitproj.UnrenderableError itself, so a reword
// cannot leave it stale.
func TestGitProjectionUnrenderableMarkerTracksTheEngine(t *testing.T) {
	if gitUnrenderableMarker == "" {
		t.Fatal("the marker must be non-empty")
	}
	msg := (&gitproj.UnrenderableError{
		Struct: "ProjectSettings", Field: "AttentionChannel.url", Reason: "holds a literal",
	}).Error()
	if !strings.Contains(msg, gitUnrenderableMarker) {
		t.Fatalf("the marker %q is not in a real refusal (%q)", gitUnrenderableMarker, msg)
	}
	if gitProjectionErrorKindFromText(msg) != agentdb.GitProjectionErrorUnrenderable {
		t.Fatal("the text fallback must still classify a real refusal")
	}
}

// ── the write point: an import actually records its notes ───────────────────

// newGitNoteImportWiring builds the inbound door over a real repository: a bare
// remote a human has pushed to, a projector that clones it, and the fake state
// row the notes land in.
func newGitNoteImportWiring(t *testing.T, g *gitImportRepo) (*gitWebhookWiring, *fakeProjectionState) {
	t.Helper()
	store := newFakeProjectionStore()
	store.project(gitImportProject, g.remote)
	state := newFakeProjectionState(gitImportProject)
	proj, err := newGitProjector(gitProjectorConfig{
		Store:  store,
		State:  state,
		Root:   filepath.Join(t.TempDir(), "clones"),
		Owner:  "test/1",
		Getenv: func(string) string { return "" },
		Logf:   func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	return &gitWebhookWiring{
		store:    nil, // importProject never routes; it is handed its project
		proj:     proj,
		importer: newGitImporter(newFakeGitImportStore()),
		getenv:   func(string) string { return "" },
		logf:     func(string, ...any) {},
	}, state
}

// TestGitImportRecordsQuarantineNotes is the write point G23 exists for: the
// importer's Failures list used to end with the run. A push that is rejected
// wholesale must leave, on the state row, the FILE and the REASON.
func TestGitImportRecordsQuarantineNotes(t *testing.T) {
	g := newGitImportRepo(t)
	g.human("add a worker with a field that cannot be read", map[string]*string{
		"orange/workers/copywriter.md": file("enabled: true\nmax_instances: as many as it takes", "Write things."),
	})
	g.push()

	w, state := newGitNoteImportWiring(t, g)
	if err := w.importProject(context.Background(), gitImportProject); err != nil {
		t.Fatalf("import: %v", err)
	}

	notes := state.notesFor(gitImportProject, agentdb.GitProjectionNoteQuarantine)
	if len(notes) == 0 {
		t.Fatal("a quarantined push recorded no notes — the operator is told which file from nowhere")
	}
	if !strings.Contains(notes[0].Path, "copywriter.md") {
		t.Errorf("the note must name the file, got %q", notes[0].Path)
	}
	if strings.TrimSpace(notes[0].Reason) == "" {
		t.Error("the note must carry a reason")
	}
	if notes[0].NotedAt == 0 {
		t.Error("the note must be stamped with when it happened")
	}
	if got := state.row(gitImportProject).LastErrorKind; got != agentdb.GitProjectionErrorQuarantined {
		t.Errorf("last_error_kind = %q, want quarantined", got)
	}
}

// TestGitImportCleanRunClearsTheQuarantine: the human fixes the file and pushes
// again. The banner must go away — a red state that outlives its cause teaches
// an operator to ignore the state.
func TestGitImportCleanRunClearsTheQuarantine(t *testing.T) {
	g := newGitImportRepo(t)
	g.human("add a worker with a field that cannot be read", map[string]*string{
		"orange/workers/copywriter.md": file("enabled: true\nmax_instances: as many as it takes", "Write things."),
	})
	g.push()

	w, state := newGitNoteImportWiring(t, g)
	ctx := context.Background()
	if err := w.importProject(ctx, gitImportProject); err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(state.notesFor(gitImportProject, agentdb.GitProjectionNoteQuarantine)) == 0 {
		t.Fatal("precondition: the first push must quarantine")
	}

	g.human("fix the field", map[string]*string{
		"orange/workers/copywriter.md": file("enabled: true\nmax_instances: 3", "Write things."),
	})
	g.push()
	if err := w.importProject(ctx, gitImportProject); err != nil {
		t.Fatalf("second import: %v", err)
	}

	if n := state.notesFor(gitImportProject, agentdb.GitProjectionNoteQuarantine); len(n) != 0 {
		t.Fatalf("a clean import must clear the quarantine, %d note(s) survived: %+v", len(n), n)
	}
}

// TestGitImportRecordsIgnoredNotes closes DI10's silence at the write point: an
// edit git cannot apply leaves an explanation behind instead of nothing.
func TestGitImportRecordsIgnoredNotes(t *testing.T) {
	g := newGitImportRepo(t)
	g.human("seed", map[string]*string{
		"orange/settings.md": file("base_image: wolf-base", "The project."),
	})
	g.push()

	w, state := newGitNoteImportWiring(t, g)
	ctx := context.Background()
	if err := w.importProject(ctx, gitImportProject); err != nil {
		t.Fatalf("import: %v", err)
	}

	// git_remote is a not-importable field (DI3): editing it in the repo is
	// read, understood and deliberately not applied.
	g.human("point the projection somewhere else", map[string]*string{
		"orange/settings.md": file(strings.Join([]string{
			"base_image: wolf-base",
			"git_remote: https://github.com/somebody/else.git",
		}, "\n"), "The project."),
	})
	g.push()
	if err := w.importProject(ctx, gitImportProject); err != nil {
		t.Fatalf("second import: %v", err)
	}

	notes := state.notesFor(gitImportProject, agentdb.GitProjectionNoteIgnored)
	if len(notes) == 0 {
		t.Fatal("an edit that had no effect was reported nowhere — the operator concludes the system is broken")
	}
	if !strings.Contains(notes[0].Reason, "git_remote") {
		t.Errorf("the note must say WHICH field was ignored, got %q", notes[0].Reason)
	}
}
