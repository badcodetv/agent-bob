package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

// fakeGitProjectionStore is a project-keyed stand-in for whatever the host
// wires the state table to. It records which project it was asked for, which is
// what the tenancy test reads.
type fakeGitProjectionStore struct {
	rows  map[string]*agentdb.GitProjectionState
	err   error
	asked []string
}

func (f *fakeGitProjectionStore) GetGitProjectionState(_ context.Context, project string) (*agentdb.GitProjectionState, error) {
	f.asked = append(f.asked, project)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[project], nil
}

// fakeGitProjectionNotesStore is a store that ALSO persists the inbound half —
// the optional extension nothing implements in production yet.
type fakeGitProjectionNotesStore struct {
	fakeGitProjectionStore
	quarantine map[string][]GitProjectionNote
	ignored    map[string][]GitProjectionNote
	notesErr   error
}

func (f *fakeGitProjectionNotesStore) GitProjectionNotes(_ context.Context, project string) ([]GitProjectionNote, []GitProjectionNote, error) {
	if f.notesErr != nil {
		return nil, nil, f.notesErr
	}
	return f.quarantine[project], f.ignored[project], nil
}

// The real store satisfies the seam, which is what makes the auto-fill in New()
// legitimate rather than a hopeful assignment.
var _ GitProjectionStore = (*agentdb.Store)(nil)

func newGitProjectionHandlers(t *testing.T, settings ProjectSettingsStore, state GitProjectionStore, identity IdentityFunc) *Handlers {
	t.Helper()
	return newHandlers(t, Config{
		Runner:          stubRunner{},
		Store:           stubStore{},
		Identity:        identity,
		ProjectSettings: settings,
		GitProjection:   state,
	})
}

func getGitProjection(t *testing.T, h *Handlers) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetGitProjectionStatus(rec, httptest.NewRequest(http.MethodGet, "/agent/git-projection", nil))
	if rec.Code != http.StatusOK {
		return rec, nil
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return rec, body
}

func settingsWithRemote(project, remote string) *fakeProjectSettingsStore {
	f := newFakeProjectSettings()
	ps := agentdb.DefaultProjectSettings(project)
	ps.GitRemote = remote
	f.rows[project] = ps
	return f
}

// --- the shape ---------------------------------------------------------------

// An empty git_remote is OFF, and off must be a complete, calm answer: no
// state is read at all, and nothing on the response suggests a fault.
func TestGitProjectionStatusOffReadsNoState(t *testing.T) {
	settings := newFakeProjectSettings() // never written → defaults, empty remote
	state := &fakeGitProjectionStore{rows: map[string]*agentdb.GitProjectionState{}}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec, body := getGitProjection(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if body["enabled"] != false {
		t.Fatalf("enabled = %v, want false", body["enabled"])
	}
	if body["health"] != GitProjectionOff {
		t.Fatalf("health = %v, want %q", body["health"], GitProjectionOff)
	}
	if body["browse_url"] != "" {
		t.Fatalf("browse_url = %v, want empty when projection is off", body["browse_url"])
	}
	if len(state.asked) != 0 {
		t.Fatalf("read projection state for an unprojected project: %v", state.asked)
	}
	// The two lists are always present, so the browser never branches on
	// undefined for them.
	for _, k := range []string{"quarantine", "ignored"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("response has no %q key: %s", k, rec.Body.String())
		}
	}
}

// Branch and subfolder are stored empty and defaulted at read time; the console
// must be told the EFFECTIVE values, not the raw columns.
func TestGitProjectionStatusAppliesEngineDefaults(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme-org.git")
	h := newGitProjectionHandlers(t, settings, &fakeGitProjectionStore{}, identityFor("acme"))

	_, body := getGitProjection(t, h)
	if body["branch"] != agentdb.DefaultGitBranch {
		t.Fatalf("branch = %v, want %q", body["branch"], agentdb.DefaultGitBranch)
	}
	if body["subfolder"] != agentdb.DefaultGitSubfolder {
		t.Fatalf("subfolder = %v, want %q", body["subfolder"], agentdb.DefaultGitSubfolder)
	}
	want := "https://github.com/badcode/acme-org/tree/main/orange"
	if body["browse_url"] != want {
		t.Fatalf("browse_url = %v, want %q", body["browse_url"], want)
	}
}

// A project with a remote but no state row has never rendered. That is a zero
// state, not an error and not "unknown".
func TestGitProjectionStatusMissingRowIsZeroState(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme-org.git")
	state := &fakeGitProjectionStore{rows: map[string]*agentdb.GitProjectionState{}}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	_, body := getGitProjection(t, h)
	if body["state_available"] != true {
		t.Fatalf("state_available = %v, want true", body["state_available"])
	}
	if body["health"] != GitProjectionOK {
		t.Fatalf("health = %v, want %q", body["health"], GitProjectionOK)
	}
	if body["last_rendered_seq"] != float64(0) {
		t.Fatalf("last_rendered_seq = %v, want 0", body["last_rendered_seq"])
	}
}

// No state seam wired: the route still answers with the repo link, and says
// plainly that it cannot see whether the projection is working.
func TestGitProjectionStatusWithoutStateStoreIsUnknown(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme-org.git")
	h := newGitProjectionHandlers(t, settings, nil, identityFor("acme"))

	rec, body := getGitProjection(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a missing state seam must not 501 away the repo link", rec.Code)
	}
	if body["state_available"] != false {
		t.Fatalf("state_available = %v, want false", body["state_available"])
	}
	if body["health"] != GitProjectionUnknown {
		t.Fatalf("health = %v, want %q", body["health"], GitProjectionUnknown)
	}
	if body["browse_url"] == "" {
		t.Fatal("browse_url is empty; the settings half must answer without the state half")
	}
}

// The host's "no such table" is the same answer as no seam at all, never a 500.
func TestGitProjectionStatusUnavailableSentinelIsUnknown(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme-org.git")
	state := &fakeGitProjectionStore{err: fmt.Errorf("read state: %w", ErrGitProjectionUnavailable)}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec, body := getGitProjection(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body["health"] != GitProjectionUnknown {
		t.Fatalf("health = %v, want %q", body["health"], GitProjectionUnknown)
	}
}

func TestGitProjectionStatusStoreErrorIs500(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme-org.git")
	state := &fakeGitProjectionStore{err: errors.New("connection refused")}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec, _ := getGitProjection(t, h)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// --- health classification ---------------------------------------------------

func TestGitProjectionStatusHealth(t *testing.T) {
	unrenderable := (&gitproj.UnrenderableError{
		Struct: gitproj.StructProjectSettings,
		Field:  "AttentionChannel.url",
		Reason: "credential-bearing field: the stored value is not a whole-value ${VAR} reference",
	}).Error()

	cases := []struct {
		name       string
		state      agentdb.GitProjectionState
		quarantine []GitProjectionNote
		wantHealth string
		wantBehind bool
		wantField  string
	}{
		{
			name:       "healthy: rendered and pushed agree, no error",
			state:      agentdb.GitProjectionState{LastRenderedSeq: 42, LastRenderedSHA: "abc123", LastPushedSHA: "abc123"},
			wantHealth: GitProjectionOK,
		},
		{
			name: "push failing: rendering moved on, the remote did not",
			state: agentdb.GitProjectionState{
				LastRenderedSeq: 42, LastRenderedSHA: "abc123", LastPushedSHA: "def456",
				LastError: "push: remote host unreachable", LastErrorAt: 1700000000,
			},
			wantHealth: GitProjectionPushFailing,
			wantBehind: true,
		},
		{
			name: "diverged: the one failure a retry cannot fix",
			state: agentdb.GitProjectionState{
				LastRenderedSHA: "abc123", LastPushedSHA: "def456",
				LastError: fmt.Sprintf("%v: origin/main is aaaa, local is bbbb", gitproj.ErrNotFastForward),
			},
			wantHealth: GitProjectionDiverged,
			wantBehind: true,
		},
		{
			name:       "unrenderable: a credential-bearing field holds a literal",
			state:      agentdb.GitProjectionState{LastError: unrenderable},
			wantHealth: GitProjectionUnrenderable,
			wantField:  "ProjectSettings.AttentionChannel.url",
		},
		{
			name:       "quarantined: a human's push was rejected wholesale",
			state:      agentdb.GitProjectionState{LastRenderedSHA: "abc123", LastPushedSHA: "abc123"},
			quarantine: []GitProjectionNote{{Path: "orange/workers/scout.md", Reason: "frontmatter is not a mapping"}},
			wantHealth: GitProjectionQuarantined,
		},
		{
			name:       "failing: an error that is none of the above and nothing is behind",
			state:      agentdb.GitProjectionState{LastError: "read project state: connection refused"},
			wantHealth: GitProjectionFailing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := settingsWithRemote("acme", "git@github.com:badcode/acme-org.git")
			st := tc.state
			state := &fakeGitProjectionNotesStore{
				fakeGitProjectionStore: fakeGitProjectionStore{
					rows: map[string]*agentdb.GitProjectionState{"acme": &st},
				},
				quarantine: map[string][]GitProjectionNote{"acme": tc.quarantine},
			}
			h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

			_, body := getGitProjection(t, h)
			if body["health"] != tc.wantHealth {
				t.Fatalf("health = %v, want %q", body["health"], tc.wantHealth)
			}
			if body["push_behind"] != tc.wantBehind {
				t.Fatalf("push_behind = %v, want %v", body["push_behind"], tc.wantBehind)
			}
			got, _ := body["unrenderable_field"].(string)
			if got != tc.wantField {
				t.Fatalf("unrenderable_field = %q, want %q", got, tc.wantField)
			}
		})
	}
}

// DI10: three things a human can do in git that have no effect. A host that
// records them gets them reported; the operator who deleted a skill file learns
// why nothing happened instead of concluding the system is broken.
func TestGitProjectionStatusReportsIgnoredEdits(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme.git")
	state := &fakeGitProjectionNotesStore{
		fakeGitProjectionStore: fakeGitProjectionStore{
			rows: map[string]*agentdb.GitProjectionState{"acme": {LastRenderedSHA: "a", LastPushedSHA: "a"}},
		},
		ignored: map[string][]GitProjectionNote{"acme": {
			{Path: "orange/skills/research.md", Reason: "skills are append-only; deleting the file removes nothing"},
			{Path: "orange/images/base.md", Reason: "images are not importable"},
		}},
	}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	_, body := getGitProjection(t, h)
	ignored, _ := body["ignored"].([]any)
	if len(ignored) != 2 {
		t.Fatalf("ignored = %v, want two entries", body["ignored"])
	}
	// An ignored edit is not a failure: the projection is still healthy.
	if body["health"] != GitProjectionOK {
		t.Fatalf("health = %v, want %q — an ignored edit is not a fault", body["health"], GitProjectionOK)
	}
}

// A store with no notes extension is the shipped shape today, and it must not
// pretend the lists are known to be empty by failing.
func TestGitProjectionStatusWithoutNotesStoreStillAnswers(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme.git")
	state := &fakeGitProjectionStore{rows: map[string]*agentdb.GitProjectionState{"acme": {LastRenderedSeq: 3}}}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec, body := getGitProjection(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body["last_rendered_seq"] != float64(3) {
		t.Fatalf("last_rendered_seq = %v, want 3", body["last_rendered_seq"])
	}
}

func TestGitProjectionStatusNotesErrorIs500(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme.git")
	state := &fakeGitProjectionNotesStore{
		fakeGitProjectionStore: fakeGitProjectionStore{
			rows: map[string]*agentdb.GitProjectionState{"acme": {}},
		},
		notesErr: errors.New("connection refused"),
	}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec, _ := getGitProjection(t, h)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// The classifier matches the ENGINE'S OWN sentinel text, because the state row
// stores err.Error() and carries no code. If either sentinel is reworded this
// test goes red — which is the whole reason it exists.
func TestGitProjectionStatusSentinelsMatchEngine(t *testing.T) {
	formatted := (&gitproj.UnrenderableError{Struct: "X", Field: "Y", Reason: "z"}).Error()
	if !strings.Contains(formatted, gitUnrenderableMarker) {
		t.Fatalf("UnrenderableError.Error() = %q no longer contains %q — classifyGitProjection would silently "+
			"report every render refusal as a generic failure", formatted, gitUnrenderableMarker)
	}
	if unrenderableField(formatted) != "X.Y" {
		t.Fatalf("unrenderableField(%q) = %q, want %q", formatted, unrenderableField(formatted), "X.Y")
	}
	// And the sentinel's own text is NOT in the formatted message — the trap
	// that made a substring match on ErrUnrenderable the wrong check.
	if strings.Contains(formatted, gitproj.ErrUnrenderable.Error()) {
		t.Fatalf("UnrenderableError.Error() now contains the wrapped sentinel's text; "+
			"gitUnrenderableMarker can be replaced with it: %q", formatted)
	}
}

// The message names the field and never the value. This asserts the property at
// the route, so a future "helpfully include the value" change fails here too.
func TestGitProjectionStatusNeverEchoesTheSecret(t *testing.T) {
	const secret = "https://hooks.slack.com/services/T00/B00/XXXXsupersecret"
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme-org.git")
	settings.rows["acme"].AttentionChannel = agentdb.JSONMap{"kind": "webhook", "url": secret}

	err := gitproj.CheckRenderable(gitproj.StructProjectSettings, map[string]any{
		"AttentionChannel": map[string]any{"kind": "webhook", "url": secret},
	})
	if err == nil {
		t.Fatal("CheckRenderable accepted a literal webhook URL")
	}
	state := &fakeGitProjectionStore{rows: map[string]*agentdb.GitProjectionState{
		"acme": {LastError: err.Error()},
	}}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec, body := getGitProjection(t, h)
	if body["health"] != GitProjectionUnrenderable {
		t.Fatalf("health = %v, want %q", body["health"], GitProjectionUnrenderable)
	}
	if strings.Contains(rec.Body.String(), secret) || strings.Contains(rec.Body.String(), "XXXXsupersecret") {
		t.Fatalf("the response echoed the offending value:\n%s", rec.Body.String())
	}
	if body["unrenderable_field"] == "" {
		t.Fatal("unrenderable_field is empty; the operator is told nothing about what to fix")
	}
}

// --- credentials in the remote ----------------------------------------------

func TestGitProjectionStatusRedactsRemoteUserinfo(t *testing.T) {
	settings := settingsWithRemote("acme", "https://kai:ghp_liveTOKEN@github.com/badcode/acme-org.git")
	h := newGitProjectionHandlers(t, settings, &fakeGitProjectionStore{}, identityFor("acme"))

	rec, body := getGitProjection(t, h)
	if strings.Contains(rec.Body.String(), "ghp_liveTOKEN") {
		t.Fatalf("the response carried a token embedded in the remote:\n%s", rec.Body.String())
	}
	if body["remote"] != "https://github.com/badcode/acme-org.git" {
		t.Fatalf("remote = %v, want the userinfo stripped", body["remote"])
	}
}

func TestGitBrowseURL(t *testing.T) {
	cases := []struct {
		remote, branch, sub, want string
	}{
		{"https://github.com/badcode/acme.git", "main", "orange",
			"https://github.com/badcode/acme/tree/main/orange"},
		{"git@github.com:badcode/acme.git", "prod", "orange",
			"https://github.com/badcode/acme/tree/prod/orange"},
		{"ssh://git@github.com/badcode/acme.git", "main", "orange",
			"https://github.com/badcode/acme/tree/main/orange"},
		// A non-GitHub forge gets the repository root: guessing another host's
		// path grammar lands an operator on a 404 mid-diagnosis.
		{"https://gitlab.com/badcode/acme.git", "main", "orange",
			"https://gitlab.com/badcode/acme"},
		{"git@git.example.com:badcode/acme.git", "main", "orange",
			"https://git.example.com/badcode/acme"},
		{"", "main", "orange", ""},
		{"not a url at all", "main", "orange", ""},
	}
	for _, tc := range cases {
		if got := gitBrowseURL(tc.remote, tc.branch, tc.sub); got != tc.want {
			t.Errorf("gitBrowseURL(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

// --- tenancy -----------------------------------------------------------------

// The project is the customer claim and nothing else. Two projects, two rows,
// two tokens: neither can see the other's, and the store is never even asked
// about a project the caller does not own.
func TestGitProjectionStatusIsTenancyScoped(t *testing.T) {
	settings := newFakeProjectSettings()
	for _, p := range []string{"acme", "rival"} {
		ps := agentdb.DefaultProjectSettings(p)
		ps.GitRemote = "https://github.com/badcode/" + p + ".git"
		settings.rows[p] = ps
	}
	state := &fakeGitProjectionStore{rows: map[string]*agentdb.GitProjectionState{
		"acme":  {LastRenderedSeq: 7, LastRenderedSHA: "acmeSHA", LastPushedSHA: "acmeSHA"},
		"rival": {LastRenderedSeq: 99, LastRenderedSHA: "rivalSHA", LastError: "rival's secret failure"},
	}}

	_, acme := getGitProjection(t, newGitProjectionHandlers(t, settings, state, identityFor("acme")))
	if acme["last_rendered_seq"] != float64(7) {
		t.Fatalf("acme last_rendered_seq = %v, want 7", acme["last_rendered_seq"])
	}
	if !strings.Contains(fmt.Sprint(acme["remote"]), "/acme") {
		t.Fatalf("acme remote = %v", acme["remote"])
	}
	if fmt.Sprint(acme["last_error"]) != "" {
		t.Fatalf("acme saw rival's error: %v", acme["last_error"])
	}

	_, rival := getGitProjection(t, newGitProjectionHandlers(t, settings, state, identityFor("rival")))
	if rival["last_rendered_seq"] != float64(99) {
		t.Fatalf("rival last_rendered_seq = %v, want 99", rival["last_rendered_seq"])
	}

	for _, asked := range state.asked {
		if asked != "acme" && asked != "rival" {
			t.Fatalf("state store was asked for %q", asked)
		}
	}
	if settings.lastGet != "rival" {
		t.Fatalf("settings lastGet = %q, want the caller's own project", settings.lastGet)
	}
}

// A query string naming another project changes nothing: there is no parameter
// to honour, and the customer claim decides.
func TestGitProjectionStatusIgnoresAProjectParameter(t *testing.T) {
	settings := newFakeProjectSettings()
	ps := agentdb.DefaultProjectSettings("acme")
	ps.GitRemote = "https://github.com/badcode/acme.git"
	settings.rows["acme"] = ps
	settings.rows["rival"] = agentdb.DefaultProjectSettings("rival")

	state := &fakeGitProjectionStore{rows: map[string]*agentdb.GitProjectionState{
		"acme":  {LastRenderedSeq: 1},
		"rival": {LastRenderedSeq: 999},
	}}
	h := newGitProjectionHandlers(t, settings, state, identityFor("acme"))

	rec := httptest.NewRecorder()
	h.GetGitProjectionStatus(rec, httptest.NewRequest(http.MethodGet, "/agent/git-projection?project=rival", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["project"] != "acme" || body["last_rendered_seq"] != float64(1) {
		t.Fatalf("a ?project= parameter was honoured: %s", rec.Body.String())
	}
}

// An embed token is minted for one session inside a third party's page. It must
// not be able to read where the project publishes, or which environment
// variable holds its push credential.
func TestGitProjectionStatusRefusesASessionScopedToken(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme.git")
	identity := func(*http.Request) (Identity, error) {
		return Identity{Customer: "acme", SessionScope: "sess-1"}, nil
	}
	h := newGitProjectionHandlers(t, settings, &fakeGitProjectionStore{}, identity)

	rec, _ := getGitProjection(t, h)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a session-scoped credential", rec.Code)
	}
}

func TestGitProjectionStatusRequiresAProject(t *testing.T) {
	identity := func(*http.Request) (Identity, error) { return Identity{}, nil }
	h := newGitProjectionHandlers(t, newFakeProjectSettings(), &fakeGitProjectionStore{}, identity)

	rec, _ := getGitProjection(t, h)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGitProjectionStatusWithoutSettingsStoreIs501(t *testing.T) {
	h := newHandlers(t, Config{
		Runner:   stubRunner{},
		Store:    stubStore{},
		Identity: identityFor("acme"),
	})
	rec, _ := getGitProjection(t, h)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// The route is on the default mux under its documented path.
func TestGitProjectionStatusIsRegistered(t *testing.T) {
	settings := settingsWithRemote("acme", "https://github.com/badcode/acme.git")
	h := newGitProjectionHandlers(t, settings, &fakeGitProjectionStore{}, identityFor("acme"))

	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agent/git-projection", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("mux status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}
