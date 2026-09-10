package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeGitWebhookResolver maps a fixed repository identity to a fixed
// project+secret, the way a real implementation would match
// agentdb.ProjectSettings.GitRemote.
type fakeGitWebhookResolver struct {
	repoFullName string
	project      string
	secret       []byte
}

func (f *fakeGitWebhookResolver) ResolveGitWebhookProject(_ context.Context, repoFullName, _ string) (string, []byte, bool) {
	if repoFullName != f.repoFullName {
		return "", nil, false
	}
	return f.project, f.secret, true
}

// fakeGitWebhookImporter records every TriggerImport call. Calls may arrive
// on a goroutine (the handler hands off asynchronously), so it is guarded and
// exposes a channel tests can wait on instead of sleeping.
type fakeGitWebhookImporter struct {
	mu    sync.Mutex
	calls []string
	seen  chan string
}

func newFakeGitWebhookImporter() *fakeGitWebhookImporter {
	return &fakeGitWebhookImporter{seen: make(chan string, 8)}
}

func (f *fakeGitWebhookImporter) TriggerImport(_ context.Context, project string) {
	f.mu.Lock()
	f.calls = append(f.calls, project)
	f.mu.Unlock()
	f.seen <- project
}

func (f *fakeGitWebhookImporter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// waitForImport blocks briefly for exactly one TriggerImport call, since the
// handler dispatches it from a detached goroutine after responding.
func waitForImport(t *testing.T, f *fakeGitWebhookImporter) string {
	t.Helper()
	select {
	case p := <-f.seen:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TriggerImport")
		return ""
	}
}

// assertNoImport asserts TriggerImport is never called, waiting briefly to
// give a wrongly-async success path a chance to show up.
func assertNoImport(t *testing.T, f *fakeGitWebhookImporter) {
	t.Helper()
	select {
	case p := <-f.seen:
		t.Fatalf("TriggerImport(%q) called; expected none", p)
	case <-time.After(150 * time.Millisecond):
	}
}

func signBody(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func pushPayload(t *testing.T, repoFullName, ref string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"ref": ref,
		"repository": map[string]any{
			"full_name": repoFullName,
			"clone_url": "https://github.com/" + repoFullName + ".git",
		},
		// A commit list is included to prove it is never read for anything
		// but JSON validity — TriggerImport's signature carries no way to
		// pass it through even if something tried.
		"commits": []map[string]any{{"id": "deadbeef", "message": "not the rationale"}},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}

func newGitWebhookRequest(body []byte, sig string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/agent/git/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gitHubEventHeader, "push")
	if sig != "" {
		req.Header.Set(gitHubSignatureHeader, sig)
	}
	return req
}

func TestGitWebhookValidSignatureTriggersImport(t *testing.T) {
	secret := []byte("s3cret")
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: secret}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	body := pushPayload(t, "acme/wolf", "refs/heads/main")
	req := newGitWebhookRequest(body, signBody(secret, body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}
	if got := waitForImport(t, importer); got != "wolf" {
		t.Fatalf("TriggerImport project = %q, want %q", got, "wolf")
	}
	// Exactly one attempt — a second delivery-shaped call must not have
	// snuck in (e.g. from a retried internal path).
	time.Sleep(50 * time.Millisecond)
	if n := importer.count(); n != 1 {
		t.Fatalf("TriggerImport called %d times, want 1", n)
	}
}

func TestGitWebhookBadSignatureRejected(t *testing.T) {
	secret := []byte("s3cret")
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: secret}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	body := pushPayload(t, "acme/wolf", "refs/heads/main")
	// Signed with the WRONG secret.
	req := newGitWebhookRequest(body, signBody([]byte("not-the-secret"), body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	assertNoImport(t, importer)
}

func TestGitWebhookMissingSignatureRejected(t *testing.T) {
	secret := []byte("s3cret")
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: secret}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	body := pushPayload(t, "acme/wolf", "refs/heads/main")
	req := newGitWebhookRequest(body, "") // no signature header at all
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	assertNoImport(t, importer)
}

func TestGitWebhookOversizedBodyRejected(t *testing.T) {
	secret := []byte("s3cret")
	// resolveCalled proves the oversized body is refused BEFORE any routing
	// or signature work — the resolver must never even be consulted.
	resolveCalled := false
	resolver := gitWebhookResolverFunc(func(context.Context, string, string) (string, []byte, bool) {
		resolveCalled = true
		return "wolf", secret, true
	})
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{
		Resolver:     resolver,
		Importer:     importer,
		MaxBodyBytes: 16, // tiny, so an ordinary push payload trivially exceeds it
	})

	body := pushPayload(t, "acme/wolf", "refs/heads/main")
	// A plausible-looking signature header is present so the test proves the
	// SIZE check runs before signature verification, not merely before an
	// absent header short-circuits everything.
	req := newGitWebhookRequest(body, signBody(secret, body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body: %s", rec.Code, rec.Body.String())
	}
	if resolveCalled {
		t.Fatal("resolver was consulted for an oversized body; size limiting must come first")
	}
	assertNoImport(t, importer)
}

func TestGitWebhookUnknownProjectRejected(t *testing.T) {
	secret := []byte("s3cret")
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: secret}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	// A repository no project claims.
	body := pushPayload(t, "someone-else/unrelated", "refs/heads/main")
	req := newGitWebhookRequest(body, signBody(secret, body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
	assertNoImport(t, importer)
}

// TestGitWebhookPayloadCarriesNoAuthority proves the design's central claim
// (§C): a payload naming a different ref, and a bogus commit list, changes
// NOTHING about what gets imported. TriggerImport's signature carries only a
// project name — there is no channel through which a ref or a commit list
// could reach the importer even if this handler tried to forward them.
func TestGitWebhookPayloadCarriesNoAuthority(t *testing.T) {
	secret := []byte("s3cret")
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: secret}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	// A payload claiming a branch that is not the project's configured
	// branch, and commits that were never pushed.
	body := pushPayload(t, "acme/wolf", "refs/heads/some-attacker-branch")
	req := newGitWebhookRequest(body, signBody(secret, body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", rec.Code, rec.Body.String())
	}
	got := waitForImport(t, importer)
	if got != "wolf" {
		t.Fatalf("TriggerImport project = %q, want %q", got, "wolf")
	}

	// A payload claiming an entirely different repository, still signed with
	// this project's secret, resolves to no project at all — the repository
	// name in the payload cannot redirect an import to a different project's
	// secret being reused, because the resolver looks the project up FIRST
	// and only that project's secret is ever tried.
	importer2 := newFakeGitWebhookImporter()
	h2 := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer2})
	forged := pushPayload(t, "acme/some-other-project", "refs/heads/main")
	req2 := newGitWebhookRequest(forged, signBody(secret, forged))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unrecognised repository; body: %s", rec2.Code, rec2.Body.String())
	}
	assertNoImport(t, importer2)
}

func TestGitWebhookNonPushEventIgnoredWith2xx(t *testing.T) {
	secret := []byte("s3cret")
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: secret}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	body := pushPayload(t, "acme/wolf", "refs/heads/main")
	req := newGitWebhookRequest(body, signBody(secret, body))
	req.Header.Set(gitHubEventHeader, "ping")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("status = %d, want 2xx for an event GitHub should not retry", rec.Code)
	}
	assertNoImport(t, importer)
}

func TestGitWebhookWrongMethodRejected(t *testing.T) {
	resolver := &fakeGitWebhookResolver{repoFullName: "acme/wolf", project: "wolf", secret: []byte("s")}
	importer := newFakeGitWebhookImporter()
	h := NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver, Importer: importer})

	req := httptest.NewRequest(http.MethodGet, "/agent/git/webhook", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestGitWebhookPanicsWithoutResolverOrImporter(t *testing.T) {
	importer := newFakeGitWebhookImporter()
	resolver := &fakeGitWebhookResolver{}

	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected a panic", name)
			}
		}()
		fn()
	}
	mustPanic("missing resolver", func() { NewGitWebhookHandler(GitWebhookConfig{Importer: importer}) })
	mustPanic("missing importer", func() { NewGitWebhookHandler(GitWebhookConfig{Resolver: resolver}) })
}

// ── poll fallback ───────────────────────────────────────────────────────────

type fakeGitWebhookLister struct {
	mu       sync.Mutex
	projects []string
	err      error
	calls    int
}

func (f *fakeGitWebhookLister) ProjectsWithGitRemote(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.projects, nil
}

func TestGitWebhookPollSweepsListedProjects(t *testing.T) {
	lister := &fakeGitWebhookLister{projects: []string{"wolf", "acme"}}
	importer := newFakeGitWebhookImporter()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunGitWebhookPoll(ctx, GitWebhookPollConfig{
		Lister:   lister,
		Importer: importer,
		Interval: 20 * time.Millisecond,
	})

	seen := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case p := <-importer.seen:
			seen[p] = true
		case <-deadline:
			t.Fatalf("timed out; saw projects %v", seen)
		}
	}
	if !seen["wolf"] || !seen["acme"] {
		t.Fatalf("poll swept %v, want both wolf and acme", seen)
	}
}

func TestGitWebhookPollStopsOnContextCancel(t *testing.T) {
	lister := &fakeGitWebhookLister{projects: []string{"wolf"}}
	importer := newFakeGitWebhookImporter()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunGitWebhookPoll(ctx, GitWebhookPollConfig{
			Lister:   lister,
			Importer: importer,
			Interval: 10 * time.Millisecond,
		})
		close(done)
	}()

	<-importer.seen // let it tick at least once
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunGitWebhookPoll did not return after ctx cancellation")
	}
}

func TestGitWebhookPollPanicsWithoutListerOrImporter(t *testing.T) {
	importer := newFakeGitWebhookImporter()
	lister := &fakeGitWebhookLister{}

	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected a panic", name)
			}
		}()
		fn()
	}
	mustPanic("missing lister", func() {
		RunGitWebhookPoll(context.Background(), GitWebhookPollConfig{Importer: importer})
	})
	mustPanic("missing importer", func() {
		RunGitWebhookPoll(context.Background(), GitWebhookPollConfig{Lister: lister})
	})
}

// ── small test helpers ──────────────────────────────────────────────────────

// gitWebhookResolverFunc adapts a function to GitWebhookProjectResolver.
type gitWebhookResolverFunc func(ctx context.Context, repoFullName, cloneURL string) (string, []byte, bool)

func (f gitWebhookResolverFunc) ResolveGitWebhookProject(ctx context.Context, repoFullName, cloneURL string) (string, []byte, bool) {
	return f(ctx, repoFullName, cloneURL)
}
