package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
	"github.com/badcodetv/agent-bob/httpapi"
)

// ── fakes ───────────────────────────────────────────────────────────────────

type fakeGitWebhookStore struct {
	targets []agentdb.GitProjectionTarget
	err     error
}

func (f *fakeGitWebhookStore) ListGitProjectionTargets(context.Context) ([]agentdb.GitProjectionTarget, error) {
	return f.targets, f.err
}

func (f *fakeGitWebhookStore) ListProjectsWithGitRemote(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, 0, len(f.targets))
	for _, t := range f.targets {
		out = append(out, t.Project)
	}
	return out, nil
}

// testWiring builds a wiring with no projector behind it: the trigger is
// replaced, so the route, the resolver and the poll can be exercised without a
// clone or a remote.
func testWiring(t *testing.T, targets []agentdb.GitProjectionTarget, env map[string]string) (*gitWebhookWiring, chan string) {
	t.Helper()
	triggered := make(chan string, 8)
	w := &gitWebhookWiring{
		store:  &fakeGitWebhookStore{targets: targets},
		getenv: func(k string) string { return env[k] },
		logf:   func(string, ...any) {},
	}
	w.trigger = func(_ context.Context, project string) { triggered <- project }
	return w, triggered
}

func signedGitHubDelivery(t *testing.T, secret, body string) *http.Request {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	req := httptest.NewRequest(http.MethodPost, "/agent/git/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return req
}

func pushBody(fullName, cloneURL string) string {
	return `{"ref":"refs/heads/main","repository":{"full_name":"` + fullName + `","clone_url":"` + cloneURL + `"}}`
}

// ── the mount ───────────────────────────────────────────────────────────────

// TestGitWebhookRouteIsReachableWithoutAJWT is the whole reason this route is
// mounted by hand on the root mux instead of by httpapi.Mux(): GitHub holds no
// console JWT and no project API key. It authenticates per delivery, by
// signature. The same mux answers 401 to anything else, so this is not a mux
// with the auth accidentally left off.
func TestGitWebhookRouteIsReachableWithoutAJWT(t *testing.T) {
	w, triggered := testWiring(t, []agentdb.GitProjectionTarget{
		{Project: "wolf", GitRemote: "https://github.com/badcode/wolf", GitWebhookSecretEnv: "WOLF_WEBHOOK_SECRET"},
	}, map[string]string{
		"WOLF_WEBHOOK_SECRET":     "s3cret",
		gitWebhookPollIntervalVar: "off", // no background sweep in a unit test
	})

	root := http.NewServeMux()
	if err := w.mount(root, context.Background()); err != nil {
		t.Fatalf("mount: %v", err)
	}
	// Everything else is behind auth, exactly as main.go arranges it.
	root.Handle("/", apiAuthMiddleware([]byte("test-secret"), wolfKeys(t), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// Sanity: the middleware is real.
	rec := httptest.NewRecorder()
	root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/agent/workers", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an ordinary route answered %d without a JWT — this mux is not enforcing auth, so the next assertion proves nothing", rec.Code)
	}

	body := pushBody("badcode/wolf", "https://github.com/badcode/wolf.git")

	// A correctly signed delivery, with NO Authorization header at all.
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, signedGitHubDelivery(t, "s3cret", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("a signed delivery answered %d, want 202: %s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-triggered:
		if got != "wolf" {
			t.Fatalf("imported %q, want wolf", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a verified delivery triggered no import")
	}

	// A BAD signature is 401 — and nothing is imported.
	rec = httptest.NewRecorder()
	root.ServeHTTP(rec, signedGitHubDelivery(t, "not-the-secret", body))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a forged signature answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-triggered:
		t.Fatalf("a forged delivery triggered an import of %q", got)
	case <-time.After(100 * time.Millisecond):
	}

	// A missing signature is 401 too, and never reaches the middleware.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agent/git/webhook", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	root.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unsigned delivery answered %d, want 401", rec.Code)
	}
}

// TestGitWebhookPollInterval covers the one env var this ticket adds. httpapi
// reads no environment variables; the HOST does, in gc.go's house style.
func TestGitWebhookPollInterval(t *testing.T) {
	mount := func(raw string) error {
		w, _ := testWiring(t, nil, map[string]string{gitWebhookPollIntervalVar: raw})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		return w.mount(http.NewServeMux(), ctx)
	}
	if err := mount("15m"); err != nil {
		t.Fatalf("a valid duration was refused: %v", err)
	}
	if err := mount("off"); err != nil {
		t.Fatalf("\"off\" was refused: %v", err)
	}
	if err := mount("300"); err == nil {
		t.Error("a bare number was accepted; it has no unit and must be refused at boot")
	}
	if httpapi.DefaultGitWebhookPollInterval != 5*time.Minute {
		t.Errorf("the documented default is 5m, got %s", httpapi.DefaultGitWebhookPollInterval)
	}
}

// ── the resolver ────────────────────────────────────────────────────────────

func TestGitWebhookResolver(t *testing.T) {
	targets := []agentdb.GitProjectionTarget{
		{Project: "wolf", GitRemote: "https://github.com/badcode/wolf.git", GitWebhookSecretEnv: "WOLF_SECRET"},
		{Project: "acme", GitRemote: "git@github.com:badcode/acme.git", GitWebhookSecretEnv: "ACME_SECRET"},
		{Project: "nosecret", GitRemote: "https://github.com/badcode/nosecret", GitWebhookSecretEnv: ""},
		{Project: "unset", GitRemote: "https://github.com/badcode/unset", GitWebhookSecretEnv: "MISSING_FROM_ENV"},
	}
	env := map[string]string{"WOLF_SECRET": "w", "ACME_SECRET": "a"}
	w, _ := testWiring(t, targets, env)

	cases := []struct {
		name        string
		fullName    string
		cloneURL    string
		wantProject string
		wantSecret  string
	}{
		{"clone url, .git and case ignored", "BadCode/Wolf", "https://GitHub.com/BadCode/Wolf.git", "wolf", "w"},
		{"an ssh remote matches an https delivery", "badcode/acme", "https://github.com/badcode/acme.git", "acme", "a"},
		{"full name alone still routes", "badcode/wolf", "", "wolf", "w"},
		{"a repository no project claims", "someone/else", "https://github.com/someone/else.git", "", ""},
		{"a project with no secret named", "badcode/nosecret", "https://github.com/badcode/nosecret.git", "", ""},
		{"a secret variable that is empty in the environment", "badcode/unset", "https://github.com/badcode/unset.git", "", ""},
		{"an empty payload matches nothing", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, secret, ok := w.ResolveGitWebhookProject(context.Background(), tc.fullName, tc.cloneURL)
			if tc.wantProject == "" {
				if ok {
					t.Fatalf("resolved to %q; an unroutable delivery must resolve to nothing (the route answers 404 and does no work)", project)
				}
				return
			}
			if !ok || project != tc.wantProject || string(secret) != tc.wantSecret {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, true)", project, secret, ok, tc.wantProject, tc.wantSecret)
			}
		})
	}
}

// TestGitWebhookResolverRefusesAnAmbiguousRepository: two projects naming the
// same remote must resolve to NOTHING. Picking one would silently decide whose
// secret is authoritative for a repository they share.
func TestGitWebhookResolverRefusesAnAmbiguousRepository(t *testing.T) {
	w, _ := testWiring(t, []agentdb.GitProjectionTarget{
		{Project: "wolf", GitRemote: "https://github.com/badcode/shared", GitWebhookSecretEnv: "A"},
		{Project: "acme", GitRemote: "https://github.com/badcode/shared.git", GitWebhookSecretEnv: "B"},
	}, map[string]string{"A": "a", "B": "b"})

	if project, _, ok := w.ResolveGitWebhookProject(context.Background(), "badcode/shared", "https://github.com/badcode/shared.git"); ok {
		t.Fatalf("an ambiguous repository resolved to %q", project)
	}
}

// TestGitWebhookResolverNeverTriesEverySecret is the anti-oracle rule stated
// directly: an unknown repository must not cause any other project's secret to
// be read at all.
func TestGitWebhookResolverNeverTriesEverySecret(t *testing.T) {
	var read []string
	w := &gitWebhookWiring{
		store: &fakeGitWebhookStore{targets: []agentdb.GitProjectionTarget{
			{Project: "wolf", GitRemote: "https://github.com/badcode/wolf", GitWebhookSecretEnv: "WOLF_SECRET"},
		}},
		getenv: func(k string) string { read = append(read, k); return "w" },
		logf:   func(string, ...any) {},
	}
	if _, _, ok := w.ResolveGitWebhookProject(context.Background(), "attacker/repo", "https://github.com/attacker/repo.git"); ok {
		t.Fatal("an unknown repository resolved")
	}
	if len(read) != 0 {
		t.Fatalf("secrets were read for a repository no project claims: %v", read)
	}
}

func TestGitRepoIdentity(t *testing.T) {
	cases := []struct{ in, host, path string }{
		{"https://github.com/badcode/wolf.git", "github.com", "badcode/wolf"},
		{"https://x-access-token:tok@github.com/badcode/wolf", "github.com", "badcode/wolf"},
		{"git@github.com:badcode/wolf.git", "github.com", "badcode/wolf"},
		{"ssh://git@github.com/badcode/wolf.git", "github.com", "badcode/wolf"},
		{"BadCode/Wolf", "", "badcode/wolf"},
		{"", "", ""},
		{"   ", "", ""},
	}
	for _, c := range cases {
		host, path := gitRepoIdentity(c.in)
		if host != c.host || path != c.path {
			t.Errorf("gitRepoIdentity(%q) = (%q, %q), want (%q, %q)", c.in, host, path, c.host, c.path)
		}
	}
}

// ── the settings field ──────────────────────────────────────────────────────

// TestGitWebhookSecretEnvIsNotImportable is the G20 half of DI3, and the
// sharpest case of it: if a commit could rewrite git_webhook_secret_env, then
// commit access to the mirror would be the power to choose which secret makes a
// forged delivery verify.
func TestGitWebhookSecretEnvIsNotImportable(t *testing.T) {
	store := newFakeGitImportStore()
	store.settings = agentdb.ProjectSettings{
		Project:             gitImportProject,
		BaseImage:           "wolf-base",
		SystemPrompt:        "The project.",
		GitRemote:           "https://github.com/badcode/wolf.git",
		GitBranch:           "main",
		GitSubfolder:        "bob",
		GitTokenEnv:         "WOLF_GITHUB_TOKEN",
		GitWebhookSecretEnv: "WOLF_WEBHOOK_SECRET",
	}
	g := newGitImportRepo(t)
	base := g.human("seed", map[string]*string{
		"bob/settings.md": file(strings.Join([]string{
			"base_image: wolf-base",
			"git_webhook_secret_env: WOLF_WEBHOOK_SECRET",
		}, "\n"), "The project."),
	})
	head := g.human("point verification at a secret I control", map[string]*string{
		"bob/settings.md": file(strings.Join([]string{
			"base_image: wolf-next",
			"git_webhook_secret_env: ATTACKER_SECRET",
		}, "\n"), "The project."),
	})

	res := runImport(t, store, g, base, head)
	if res.Quarantined {
		t.Fatalf("unexpected quarantine: %+v", res.Failures)
	}
	if store.settings.BaseImage != "wolf-next" {
		t.Fatalf("the importable field did not land: %+v", store.settings)
	}
	if store.settings.GitWebhookSecretEnv != "WOLF_WEBHOOK_SECRET" {
		t.Fatalf("a commit rewrote the webhook secret's variable name to %q — forged deliveries would now verify",
			store.settings.GitWebhookSecretEnv)
	}
	// project-connections T11 added "connections" as a sixth, worker-only
	// not-importable key.
	if want := []string{"connections", "git_branch", "git_remote", "git_subfolder", "git_token_env", "git_webhook_secret_env"}; !reflect.DeepEqual(gitproj.NotImportableFields(), want) {
		t.Fatalf("NotImportableFields = %v, want %v", gitproj.NotImportableFields(), want)
	}
}

// ── the watermark table ─────────────────────────────────────────────────────

// TestGitProjectionTableDelegatesToTheStore pins G22: the loops' state lives in
// agentdb (migration 048), and nothing in cmd/agentd creates it. The adapter
// must be a straight pass-through — a lease it dropped or a watermark it
// swallowed would show up only as a project that silently stops publishing.
func TestGitProjectionTableDelegatesToTheStore(t *testing.T) {
	spy := &spyProjectionStateStore{}
	table, err := newGitProjectionTable(spy)
	if err != nil {
		t.Fatalf("new table: %v", err)
	}
	ctx := context.Background()

	if _, err := table.AcquireLease(ctx, "wolf", "host/1", 99); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := table.MarkRendered(ctx, "wolf", 7, "aaa"); err != nil {
		t.Fatalf("mark rendered: %v", err)
	}
	if err := table.MarkPushed(ctx, "wolf", "aaa"); err != nil {
		t.Fatalf("mark pushed: %v", err)
	}
	if err := table.MarkImported(ctx, "wolf", "bbb"); err != nil {
		t.Fatalf("mark imported: %v", err)
	}
	if err := table.NoteFailureKind(ctx, "wolf", agentdb.GitProjectionErrorQuarantined, "boom"); err != nil {
		t.Fatalf("note failure: %v", err)
	}
	if spy.lastKind != agentdb.GitProjectionErrorQuarantined {
		t.Errorf("the KIND must reach the store, got %q", spy.lastKind)
	}
	// The 3-arg fallback re-derives the kind from the message, and is the only
	// place text matching survives (gitbackfill.go's caller).
	if err := table.NoteFailure(ctx, "wolf", gitproj.ErrNotFastForward.Error()+": origin is ahead"); err != nil {
		t.Fatalf("note failure (text): %v", err)
	}
	if spy.lastKind != agentdb.GitProjectionErrorNotFastForward {
		t.Errorf("the text fallback must classify a divergence, got %q", spy.lastKind)
	}
	if err := table.PutNotes(ctx, "wolf", agentdb.GitProjectionNoteIgnored,
		[]agentdb.GitProjectionNote{{Path: "bob/images/base.md", Reason: "images are not importable"}}); err != nil {
		t.Fatalf("put notes: %v", err)
	}
	if len(spy.lastNotes) != 1 || spy.lastNotesKind != agentdb.GitProjectionNoteIgnored {
		t.Errorf("the notes must reach the store unchanged, got %d of kind %q", len(spy.lastNotes), spy.lastNotesKind)
	}
	if err := table.ReleaseLease(ctx, "wolf", "host/1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := table.ProjectsWithRemote(ctx); err != nil {
		t.Fatalf("list: %v", err)
	}
	want := []string{"acquire", "rendered", "pushed", "imported", "failure", "failure", "notes", "release", "list"}
	if !reflect.DeepEqual(spy.calls, want) {
		t.Fatalf("calls = %v, want %v", spy.calls, want)
	}

	if _, err := newGitProjectionTable(nil); err == nil {
		t.Error("a table with no store must be refused rather than nil-panicking at the first render")
	}
}

type spyProjectionStateStore struct {
	calls         []string
	rec           agentdb.GitProjectionState
	lastKind      string
	lastNotes     []agentdb.GitProjectionNote
	lastNotesKind string
}

func (s *spyProjectionStateStore) GetGitProjectionState(context.Context, string) (*agentdb.GitProjectionState, error) {
	s.calls = append(s.calls, "get")
	return &s.rec, nil
}

func (s *spyProjectionStateStore) AcquireGitProjectionLease(context.Context, string, string, int64) (bool, error) {
	s.calls = append(s.calls, "acquire")
	return true, nil
}

func (s *spyProjectionStateStore) ReleaseGitProjectionLease(context.Context, string, string) error {
	s.calls = append(s.calls, "release")
	return nil
}

func (s *spyProjectionStateStore) MarkGitProjectionRendered(context.Context, string, int64, string) error {
	s.calls = append(s.calls, "rendered")
	return nil
}

func (s *spyProjectionStateStore) MarkGitProjectionPushed(context.Context, string, string) error {
	s.calls = append(s.calls, "pushed")
	return nil
}

func (s *spyProjectionStateStore) MarkGitProjectionImported(context.Context, string, string) error {
	s.calls = append(s.calls, "imported")
	return nil
}

// ClearGitProjectionQuarantine: the spy records nothing. The narrowing that
// matters — clearing only a quarantine, never a push failure — is a WHERE
// clause in the real store and is tested there.
func (s *spyProjectionStateStore) ClearGitProjectionQuarantine(context.Context, string) error {
	return nil
}

func (s *spyProjectionStateStore) NoteGitProjectionFailure(_ context.Context, _, kind, _ string) error {
	s.calls = append(s.calls, "failure")
	s.lastKind = kind
	return nil
}

func (s *spyProjectionStateStore) PutGitProjectionNotes(_ context.Context, _, kind string, notes []agentdb.GitProjectionNote) error {
	s.calls = append(s.calls, "notes")
	s.lastNotes = append([]agentdb.GitProjectionNote(nil), notes...)
	s.lastNotesKind = kind
	return nil
}

func (s *spyProjectionStateStore) ListProjectsWithGitRemote(context.Context) ([]string, error) {
	s.calls = append(s.calls, "list")
	return nil, nil
}

// TestGitProjectionCreatesNoSchema is G22 stated as a test: nothing in this
// package may create a table. The projection's own state comes from migration
// 048 like every other table in the product.
func TestGitProjectionCreatesNoSchema(t *testing.T) {
	for _, f := range []string{"gitprojection.go", "gitwebhookwiring.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		// The CALL, not the word: the file comment explains why the table
		// stopped being created here, and that sentence must stay readable.
		if bytes.Contains(src, []byte("AutoMigrate(")) {
			t.Errorf("%s calls AutoMigrate: schema belongs in agentdb/migrations.go, "+
				"where a table can be reviewed and a fresh database matches an upgraded one", f)
		}
	}
}
