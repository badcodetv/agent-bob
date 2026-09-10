package httpapi

// Tests for the bootstrap DOOR (G26). The work itself — reading a folder,
// parsing it, writing the configuration — is cmd/agentd's and is tested there
// against a real repository. What is tested here is everything the door
// promises: whose project it acts on, who is refused, and which status code
// each outcome gets, because those are the parts an operator meets.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitBootstrapper records which project it was asked for — the tenancy
// assertion reads that — and returns whatever the test wants.
type fakeGitBootstrapper struct {
	asked  []string
	report *GitBootstrapReport
	err    error
}

func (f *fakeGitBootstrapper) BootstrapProject(_ context.Context, project string) (*GitBootstrapReport, error) {
	f.asked = append(f.asked, project)
	if f.err != nil {
		return nil, f.err
	}
	if f.report != nil {
		return f.report, nil
	}
	return &GitBootstrapReport{Project: project}, nil
}

func newGitBootstrapHandlers(t *testing.T, b GitBootstrapper, identity IdentityFunc) *Handlers {
	t.Helper()
	cfg := Config{
		Runner:   stubRunner{},
		Store:    stubStore{},
		Identity: identity,
	}
	if b != nil {
		cfg.GitBootstrap = b
	}
	return newHandlers(t, cfg)
}

func postGitBootstrap(t *testing.T, h *Handlers) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	// A body is sent deliberately: this route must never read one. If it ever
	// starts taking a project from a body, this is the request that finds out.
	body := strings.NewReader(`{"project":"someone-else","sha":"deadbeef"}`)
	h.GitBootstrap(rec, httptest.NewRequest(http.MethodPost, "/agent/git-bootstrap", body))
	var out map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		return rec, nil
	}
	return rec, out
}

// ── 🔴 tenancy ──────────────────────────────────────────────────────────────

// The project is the `customer` claim and nothing else. The request body names
// a different project on purpose: this route is the most destructive one in the
// feature, and a body-supplied project would let any token recreate any
// project's configuration from any folder.
func TestGitBootstrapUsesTheClaimAndNeverTheBody(t *testing.T) {
	boot := &fakeGitBootstrapper{}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))

	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(boot.asked) != 1 || boot.asked[0] != "acme" {
		t.Fatalf("bootstrapped %v, want exactly [acme] — the body named someone-else", boot.asked)
	}
}

// A session-scoped embed token is minted for one session inside somebody else's
// page. It must not be able to recreate the project's whole configuration.
func TestGitBootstrapRefusesASessionScopedToken(t *testing.T) {
	boot := &fakeGitBootstrapper{}
	h := newGitBootstrapHandlers(t, boot, func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "embed", Customer: "acme", SessionScope: "sess-1"}, nil
	})

	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an embed token: %s", rec.Code, rec.Body.String())
	}
	if len(boot.asked) != 0 {
		t.Fatalf("an embed token reached the bootstrapper: %v", boot.asked)
	}
}

func TestGitBootstrapRequiresAProjectScope(t *testing.T) {
	boot := &fakeGitBootstrapper{}
	h := newGitBootstrapHandlers(t, boot, func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "nobody"}, nil
	})

	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 with no customer claim: %s", rec.Code, rec.Body.String())
	}
	if len(boot.asked) != 0 {
		t.Fatalf("an unscoped request reached the bootstrapper: %v", boot.asked)
	}
}

// No seam wired (the sqlite fallback, or any deployment with no projector):
// 501, the same shape every other unwired seam uses. Never a 500, and never a
// silent success.
func TestGitBootstrapWithoutASeamIs501(t *testing.T) {
	h := newGitBootstrapHandlers(t, nil, identityFor("acme"))
	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", rec.Code, rec.Body.String())
	}
}

// ── 🔴 the refusal ──────────────────────────────────────────────────────────

// A project that already has configuration is refused, and the refusal names
// what was found and says what to do instead. Both halves matter: "already
// configured" alone is not actionable, and an operator who is not told about
// the import path will reach for something worse.
func TestGitBootstrapConflictIs409AndSaysWhatToDo(t *testing.T) {
	boot := &fakeGitBootstrapper{err: &GitBootstrapConflictError{Found: []string{"3 workers", "1 schedule"}}}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))

	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	msg := rec.Body.String()
	for _, want := range []string{"3 workers", "1 schedule", "push it to the project's own git remote"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the 409 does not mention %q: %s", want, msg)
		}
	}
}

func TestGitBootstrapBusyIs409(t *testing.T) {
	boot := &fakeGitBootstrapper{err: ErrGitBootstrapBusy}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))
	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while another writer holds the lease: %s", rec.Code, rec.Body.String())
	}
}

// No git_remote is an operator step that has not been done, not a fault. It
// names the field so the next click is obvious.
func TestGitBootstrapUnconfiguredIs400NamingTheField(t *testing.T) {
	boot := &fakeGitBootstrapper{err: ErrGitBootstrapNotConfigured}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))
	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "git_remote") {
		t.Fatalf("the 400 does not name git_remote: %s", rec.Body.String())
	}
}

// ── the result ──────────────────────────────────────────────────────────────

// 🔴 The ignored list is half the reason this route returns a body at all. An
// operator who bootstraps twelve files and gets ten things must be told why
// here — not in agentd's logs.
func TestGitBootstrapReturnsWhatWasIgnored(t *testing.T) {
	boot := &fakeGitBootstrapper{report: &GitBootstrapReport{
		Project:   "acme",
		SHA:       "abc123",
		Watermark: "abc123",
		Files:     3,
		Applied: []GitBootstrapWrite{
			{Path: "orange/workers/architect.md", Kind: "worker", Name: "architect", Action: "create"},
		},
		Ignored: []GitProjectionNote{
			{Path: "orange/images/wolf-base.md", Reason: "images are not importable"},
			{Path: "orange/settings.md", Reason: "git configuration is not importable; ignored: git_remote"},
		},
	}}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))

	rec, body := postGitBootstrap(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if body["watermark"] != "abc123" {
		t.Fatalf("watermark = %v, want abc123", body["watermark"])
	}
	ignored, _ := body["ignored"].([]any)
	if len(ignored) != 2 {
		t.Fatalf("ignored = %v, want the two entries the run reported", body["ignored"])
	}
	first, _ := ignored[0].(map[string]any)
	if first["path"] != "orange/images/wolf-base.md" || first["reason"] == "" {
		t.Fatalf("an ignored entry lost its path or reason: %v", first)
	}
	applied, _ := body["applied"].([]any)
	if len(applied) != 1 {
		t.Fatalf("applied = %v, want one write", body["applied"])
	}
}

// A quarantine wrote NOTHING. Answering 200 would tell an operator running curl
// that their folder had been imported, so it is 422 — with the full per-file
// report as the body, because the reasons are the whole point of answering.
func TestGitBootstrapQuarantineIs422WithTheReasons(t *testing.T) {
	boot := &fakeGitBootstrapper{report: &GitBootstrapReport{
		Project:     "acme",
		SHA:         "abc123",
		Quarantined: true,
		Failures: []GitProjectionNote{
			{Path: "orange/workers/saboteur.md", Reason: "enabled: cannot unmarshal"},
		},
	}}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))

	rec, body := postGitBootstrap(t, h)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for a quarantined bootstrap: %s", rec.Code, rec.Body.String())
	}
	if body["quarantined"] != true {
		t.Fatalf("quarantined = %v, want true", body["quarantined"])
	}
	if body["watermark"] != "" {
		t.Fatalf("watermark = %v, want empty after a quarantine", body["watermark"])
	}
	failures, _ := body["failures"].([]any)
	if len(failures) != 1 {
		t.Fatalf("failures = %v, want the one bad file", body["failures"])
	}
	f, _ := failures[0].(map[string]any)
	if f["path"] != "orange/workers/saboteur.md" {
		t.Fatalf("the failure lost its path: %v", f)
	}
}

// Empty lists must serialise as [], never null: the console iterates them.
func TestGitBootstrapEmptyListsAreArrays(t *testing.T) {
	boot := &fakeGitBootstrapper{report: &GitBootstrapReport{Project: "acme", SHA: "abc"}}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))

	rec, _ := postGitBootstrap(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, key := range []string{`"applied":[]`, `"ignored":[]`, `"failures":[]`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Fatalf("body does not contain %s: %s", key, rec.Body.String())
		}
	}
}

// The route is on the authenticated mux, under the endpoint the Endpoints table
// names. A route nothing registers is exactly the bug G26 exists to fix.
func TestGitBootstrapIsRegisteredOnTheMux(t *testing.T) {
	boot := &fakeGitBootstrapper{}
	h := newGitBootstrapHandlers(t, boot, identityFor("acme"))

	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/agent/git-bootstrap", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /agent/git-bootstrap through the mux = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(boot.asked) != 1 {
		t.Fatalf("the mux did not reach the handler: %v", boot.asked)
	}
	// GET is not this route.
	rec = httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agent/git-bootstrap", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("GET /agent/git-bootstrap answered 200; the endpoint is POST-only")
	}
}
