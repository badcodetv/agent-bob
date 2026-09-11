package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/extension"
	"github.com/badcodetv/agent-bob/extension/devclaims"
	"github.com/badcodetv/agent-bob/httpapi"
)

// noKeys is the key index of a deployment with no project map — what every
// pre-existing agentd deployment has.
func noKeys(t *testing.T) *projectKeyIndex {
	t.Helper()
	keys, err := newProjectKeys(nil, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatalf("newProjectKeys: %v", err)
	}
	return keys
}

// wolfKeys is a one-project index whose key is goodKey (apikey_test.go).
func wolfKeys(t *testing.T) *projectKeyIndex {
	t.Helper()
	keys, err := newProjectKeys(
		map[string]projectConfig{"wolf": {APIKeyEnv: "WOLF_API_KEY"}},
		envFrom(map[string]string{"WOLF_API_KEY": goodKey}), nil)
	if err != nil {
		t.Fatalf("newProjectKeys: %v", err)
	}
	return keys
}

// captureIdentity mounts the middleware around a handler that records the
// identity the request arrived with.
func captureIdentity(secret []byte, keys projectKeys, got *httpapi.Identity) http.Handler {
	return apiAuthMiddleware(secret, keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := identityFromRequest(r)
		*got = id
		w.WriteHeader(http.StatusOK)
	}))
}

// --- the JWT path, unchanged ---

func TestAuthMiddleware_ValidTokenSetsPrincipal(t *testing.T) {
	secret := []byte("test-secret")
	tok, err := devclaims.New(secret).Issue(context.Background(),
		extensionScope("alice@acme.com", "acme"), "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	var got httpapi.Identity
	h := captureIdentity(secret, noKeys(t), &got)

	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got.UserEmail != "alice@acme.com" || got.Customer != "acme" {
		t.Fatalf("identity = %+v, want (alice@acme.com, acme)", got)
	}
	if got.SessionScope != "" {
		t.Fatalf("an ordinary login token arrived scoped to %q", got.SessionScope)
	}
}

func TestAuthMiddleware_RejectsMissingAndBadToken(t *testing.T) {
	secret := []byte("test-secret")
	var got httpapi.Identity
	h := captureIdentity(secret, noKeys(t), &got)
	for _, tc := range []struct{ name, auth string }{
		{"missing", ""},
		{"garbage", "Bearer not-a-jwt"},
		{"wrong scheme", "Basic abc"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", tc.name, rr.Code)
		}
	}
}

func TestAuthMiddleware_EmptySecretIsDevOpen(t *testing.T) {
	var got httpapi.Identity
	h := captureIdentity(nil, noKeys(t), &got)
	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || got.Customer == "" {
		t.Fatalf("dev-open should pass with a default principal; status=%d identity=%+v", rr.Code, got)
	}
}

// --- the API-key path ---

func TestAuthMiddleware_APIKeyAuthenticatesItsProject(t *testing.T) {
	var got httpapi.Identity
	h := captureIdentity([]byte("test-secret"), wolfKeys(t), &got)

	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set(apiKeyHeader, goodKey)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got.Customer != "wolf" {
		t.Fatalf("customer = %q, want wolf", got.Customer)
	}
	if got.UserEmail != "api-key:wolf" {
		t.Fatalf("user email = %q, want api-key:wolf", got.UserEmail)
	}
	if got.SessionScope != "" {
		t.Fatalf("an API key arrived scoped to %q — a key grants the whole project", got.SessionScope)
	}
	if !got.APIKey {
		t.Fatalf("an API-key request must set Identity.APIKey (T7)")
	}
}

// T7 (design/2026-09-11-project-connections.md): PutWorker's `connections`
// gate needs to tell an API key apart from a console login, so the flag it
// checks (Identity.APIKey) must be true for exactly one of the two credential
// classes this middleware issues.
func TestAuthMiddleware_APIKeyFlagDistinguishesCredentialClasses(t *testing.T) {
	var gotKey httpapi.Identity
	h := captureIdentity([]byte("test-secret"), wolfKeys(t), &gotKey)
	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set(apiKeyHeader, goodKey)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !gotKey.APIKey {
		t.Fatalf("API-key request: Identity.APIKey = false, want true")
	}

	var gotJWT httpapi.Identity
	h = captureIdentity([]byte("test-secret"), noKeys(t), &gotJWT)
	tok, err := devclaims.New([]byte("test-secret")).Issue(context.Background(),
		extensionScope("alice@acme.com", "acme"), "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if gotJWT.APIKey {
		t.Fatalf("console JWT: Identity.APIKey = true, want false")
	}
}

func TestAuthMiddleware_InvalidAPIKeyIs401(t *testing.T) {
	var got httpapi.Identity
	h := captureIdentity([]byte("test-secret"), wolfKeys(t), &got)
	for _, key := range []string{"wrong-but-long-enough-key-value-x", "short", goodKey + "x"} {
		req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
		req.Header.Set(apiKeyHeader, key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: status = %d, want 401", key, rr.Code)
		}
	}
}

// A caller that sent a key meant to use it. Falling through to the JWT path (or
// worse, to dev-open) on a bad key would turn a typo into a silent downgrade.
func TestAuthMiddleware_BadAPIKeyDoesNotFallThroughToJWT(t *testing.T) {
	secret := []byte("test-secret")
	tok, err := devclaims.New(secret).Issue(context.Background(),
		extensionScope("alice@acme.com", "acme"), "")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var got httpapi.Identity
	h := captureIdentity(secret, wolfKeys(t), &got)

	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set(apiKeyHeader, "definitely-not-the-right-key-value")
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (a bad key must not fall back to the bearer token)", rr.Code)
	}
}

// A configured key means a real deployment. Dev-open there would hand every
// unauthenticated request the "demo" project alongside a live third-party key.
func TestAuthMiddleware_ConfiguredKeyDisablesDevOpen(t *testing.T) {
	var got httpapi.Identity
	h := captureIdentity(nil, wolfKeys(t), &got) // no JWT secret at all

	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — dev-open must be off when a key exists", rr.Code)
	}

	// The key itself still works with no JWT secret set.
	req = httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set(apiKeyHeader, goodKey)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || got.Customer != "wolf" {
		t.Fatalf("status = %d identity = %+v, want 200 / wolf", rr.Code, got)
	}
}

// --- the embed scope ---

func TestAuthMiddleware_ScopedTokenCarriesItsSession(t *testing.T) {
	secret := []byte("test-secret")
	tok, err := devclaims.New(secret).IssueScoped(context.Background(),
		extension.ContextScope{Customer: "wolf", UserEmail: "api-key:wolf", Job: "embed"},
		"", devclaims.SessionScope("s-hyp-a"))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var got httpapi.Identity
	h := captureIdentity(secret, wolfKeys(t), &got)

	req := httptest.NewRequest(http.MethodGet, "/agent/session/s-hyp-a/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got.Customer != "wolf" {
		t.Fatalf("customer = %q, want wolf", got.Customer)
	}
	if got.SessionScope != "s-hyp-a" {
		t.Fatalf("SessionScope = %q, want s-hyp-a (this is what httpapi enforces)", got.SessionScope)
	}
}

// A scope this middleware does not understand must not be silently read as
// "unrestricted" — it reads as no session scope, and any future scope kind gets
// its own explicit handling.
func TestAuthMiddleware_UnknownScopeKindIsNotASessionScope(t *testing.T) {
	secret := []byte("test-secret")
	tok, err := devclaims.New(secret).IssueScoped(context.Background(),
		extension.ContextScope{Customer: "wolf"}, "", "project:wolf")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	var got httpapi.Identity
	h := captureIdentity(secret, noKeys(t), &got)
	req := httptest.NewRequest(http.MethodGet, "/agent/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got.SessionScope != "" {
		t.Fatalf("SessionScope = %q, want empty for a non-session scope", got.SessionScope)
	}
}

// --- the dataset download token (O5 of design/2026-08-20-agent-wolf.md) ---
//
// Two halves of one lock, and both are graded against the REAL middleware
// rather than a handler, because the whole point of each is what happens
// BEFORE any handler runs:
//
//  1. a dataset token in ?token= authenticates GET
//     /agent/datasets/{name}/download, and nothing else anywhere;
//  2. the same token presented as `Authorization: Bearer` is refused outright,
//     everywhere, download route included.
//
// Without (1) the agent's `curl` — which carries no headers at all — is 401'd
// before it reaches the route it was handed. Without (2) that same token, which
// is signed with the API secret and carries the same empty `sid` an embed token
// does, authenticates as an UNRESTRICTED project principal on every /agent/*
// route, because the scope claim is otherwise read only through
// ParseSessionScope.

const datasetDownloadPathForTests = "/agent/datasets/basket/download"

// datasetToken mints a real one, through the same helper the tools use.
func datasetToken(t *testing.T, secret []byte, project, name string) string {
	t.Helper()
	tok, _, err := mintDatasetToken(secret, project, name, 0)
	if err != nil {
		t.Fatalf("mintDatasetToken: %v", err)
	}
	return tok
}

func TestDatasetDownloadAuth_QueryTokenReachesTheHandler(t *testing.T) {
	secret := []byte("test-secret")
	var got httpapi.Identity
	h := captureIdentity(secret, wolfKeys(t), &got)

	// No X-API-Key and no Authorization header — exactly what an agent's
	// `curl <download_url>` sends from inside a container.
	req := httptest.NewRequest(http.MethodGet,
		datasetDownloadPathForTests+"?token="+datasetToken(t, secret, "wolf", "basket"), nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the token leg must reach the handler", rr.Code)
	}
	if got.Customer != "wolf" {
		t.Fatalf("customer = %q, want wolf (the token's pinned project)", got.Customer)
	}
	if got.DatasetScope != "wolf/basket" {
		t.Fatalf("DatasetScope = %q, want wolf/basket — this is what httpapi enforces", got.DatasetScope)
	}
	if got.SessionScope != "" {
		t.Fatalf("SessionScope = %q, want empty — a dataset token is not a session credential", got.SessionScope)
	}
	// A ?version= alongside the token changes nothing: the pin is (project,
	// name) and carries no version, so a URL minted before a tick still works
	// after it.
	req = httptest.NewRequest(http.MethodGet,
		datasetDownloadPathForTests+"?version=3&token="+datasetToken(t, secret, "wolf", "basket"), nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || got.DatasetScope != "wolf/basket" {
		t.Fatalf("with ?version=: status=%d scope=%q", rr.Code, got.DatasetScope)
	}
}

// ?token= is not a credential anywhere else in the tree. The three metadata
// routes read no query credential, so a request carrying only a valid token is
// 401 at the middleware — it never reaches a handler that could decide
// otherwise.
func TestDatasetDownloadAuth_QueryTokenIsAcceptedOnlyOnTheDownloadPath(t *testing.T) {
	secret := []byte("test-secret")
	var got httpapi.Identity
	h := captureIdentity(secret, wolfKeys(t), &got)
	tok := datasetToken(t, secret, "wolf", "basket")

	for _, tc := range []struct{ name, method, path string }{
		{"the list route", http.MethodGet, "/agent/datasets?token=" + tok},
		{"the metadata route", http.MethodGet, "/agent/datasets/basket?token=" + tok},
		{"the versions route", http.MethodGet, "/agent/datasets/basket/versions?token=" + tok},
		{"a memory read", http.MethodGet, "/agent/memories?token=" + tok},
		{"the sessions list", http.MethodGet, "/agent/sessions?token=" + tok},
		// Two segments where the route has one: the mux would route this
		// somewhere else entirely, so the leg must not authenticate it.
		{"a nested name", http.MethodGet, "/agent/datasets/a/b/download?token=" + tok},
		{"an empty name", http.MethodGet, "/agent/datasets//download?token=" + tok},
		{"a prefix match that is not the route", http.MethodGet, "/agent/datasets/basket/downloadx?token=" + tok},
		// The leg is GET-only: a write must never be authenticated by a
		// credential that lives in a URL and ends up in logs and referrers.
		{"POST to the download path", http.MethodPost, datasetDownloadPathForTests + "?token=" + tok},
		{"DELETE to the download path", http.MethodDelete, datasetDownloadPathForTests + "?token=" + tok},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", tc.name, rr.Code)
		}
	}
}

// Every verification failure falls through to the ordinary 401 rather than
// answering something more specific: an attacker probing this route must not be
// able to tell "expired" from "wrong secret" from "not a dataset token".
func TestDatasetDownloadAuth_UnverifiableTokenFallsThroughTo401(t *testing.T) {
	secret := []byte("test-secret")
	var got httpapi.Identity
	h := captureIdentity(secret, wolfKeys(t), &got)

	expired, err := devclaims.NewWithTTL(secret, -time.Minute).IssueScoped(context.Background(),
		extension.ContextScope{Customer: "wolf", Job: "dataset-download"}, "",
		devclaims.DatasetScope("wolf", "basket"))
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}
	sessionScoped, err := devclaims.New(secret).IssueScoped(context.Background(),
		extension.ContextScope{Customer: "wolf"}, "", devclaims.SessionScope("s-1"))
	if err != nil {
		t.Fatalf("issue session-scoped: %v", err)
	}
	withSID, err := devclaims.New(secret).IssueScoped(context.Background(),
		extension.ContextScope{Customer: "wolf"}, "s-1", devclaims.DatasetScope("wolf", "basket"))
	if err != nil {
		t.Fatalf("issue with sid: %v", err)
	}

	for _, tc := range []struct{ name, token string }{
		{"empty", ""},
		{"garbage", "not-a-jwt"},
		{"signed with another secret", datasetToken(t, []byte("other-secret"), "wolf", "basket")},
		{"expired", expired},
		{"a session scope, not a dataset one", sessionScoped},
		{"a dataset scope carrying a session id", withSID},
	} {
		req := httptest.NewRequest(http.MethodGet, datasetDownloadPathForTests+"?token="+tc.token, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", tc.name, rr.Code)
		}
	}
}

// The bearer lock. A dataset token is signed with the SAME secret as every
// API-class token and carries the same empty `sid` an embed token does, so
// before this lock existed it authenticated as a full, unrestricted project
// principal on every /agent/* route.
func TestDatasetDownloadAuth_BearerDatasetTokenIsRejectedEverywhere(t *testing.T) {
	secret := []byte("test-secret")
	var got httpapi.Identity
	h := captureIdentity(secret, wolfKeys(t), &got)
	tok := datasetToken(t, secret, "wolf", "basket")

	for _, path := range []string{
		"/agent/memories",
		"/agent/sessions",
		"/agent/datasets",
		datasetDownloadPathForTests, // its OWN route included: this credential
		// travels in ?token= or not at all.
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401 — a dataset token is not API-class", path, rr.Code)
		}
	}

	// And it is refused on the shape as well as on the substance: a token whose
	// scope is in the dataset namespace but malformed must not slip past the
	// lock into the unrestricted principal, which is exactly what would happen
	// if the check ran through ParseDatasetScope (ok=false) instead of the
	// namespace.
	for _, scope := range []string{"dataset:", "dataset:wolf", "dataset:wolf/a/b", "dataset:/basket"} {
		bad, err := devclaims.New(secret).IssueScoped(context.Background(),
			extension.ContextScope{Customer: "wolf"}, "", scope)
		if err != nil {
			t.Fatalf("issue %q: %v", scope, err)
		}
		req := httptest.NewRequest(http.MethodGet, "/agent/memories", nil)
		req.Header.Set("Authorization", "Bearer "+bad)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("scope %q: status = %d, want 401", scope, rr.Code)
		}
	}
}

// The lock is a second spelling of a string devclaims keeps unexported. This is
// what stops the two drifting: a rename there without one here would silently
// unlock the bearer path.
func TestDatasetDownloadAuth_ScopePrefixMatchesDevclaims(t *testing.T) {
	if got := devclaims.DatasetScope("wolf", "basket"); !strings.HasPrefix(got, datasetScopeBearerPrefix) {
		t.Fatalf("devclaims.DatasetScope produces %q, which does not start with %q",
			got, datasetScopeBearerPrefix)
	}
	// And it must not swallow the session namespace, or every embed token would
	// stop working.
	if strings.HasPrefix(devclaims.SessionScope("s-1"), datasetScopeBearerPrefix) {
		t.Fatal("the dataset lock would also reject session-scoped (embed) tokens")
	}
}

// Nothing else moved. The other three credentials behave exactly as they did,
// including on the download path itself, and none of them arrives carrying a
// dataset scope — which is what keeps httpapi's pin check inert for them.
func TestDatasetDownloadAuth_OtherCredentialsAreUnchanged(t *testing.T) {
	secret := []byte("test-secret")

	t.Run("an API key still grants the whole project on the download path", func(t *testing.T) {
		var got httpapi.Identity
		h := captureIdentity(secret, wolfKeys(t), &got)
		req := httptest.NewRequest(http.MethodGet, datasetDownloadPathForTests, nil)
		req.Header.Set(apiKeyHeader, goodKey)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || got.Customer != "wolf" {
			t.Fatalf("status=%d identity=%+v, want 200 / wolf", rr.Code, got)
		}
		if got.DatasetScope != "" {
			t.Fatalf("DatasetScope = %q — an API key is not confined to one dataset", got.DatasetScope)
		}
	})

	t.Run("an API key wins over a token in the query", func(t *testing.T) {
		var got httpapi.Identity
		h := captureIdentity(secret, wolfKeys(t), &got)
		req := httptest.NewRequest(http.MethodGet,
			datasetDownloadPathForTests+"?token="+datasetToken(t, secret, "wolf", "other"), nil)
		req.Header.Set(apiKeyHeader, goodKey)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || got.DatasetScope != "" {
			t.Fatalf("status=%d scope=%q — the key branch runs first and is unconfined", rr.Code, got.DatasetScope)
		}
	})

	t.Run("a console JWT is untouched", func(t *testing.T) {
		var got httpapi.Identity
		h := captureIdentity(secret, noKeys(t), &got)
		tok, err := devclaims.New(secret).Issue(context.Background(),
			extensionScope("alice@acme.com", "acme"), "")
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, datasetDownloadPathForTests, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || got.Customer != "acme" || got.DatasetScope != "" {
			t.Fatalf("status=%d identity=%+v", rr.Code, got)
		}
	})

	t.Run("an embed token still carries its session scope", func(t *testing.T) {
		var got httpapi.Identity
		h := captureIdentity(secret, wolfKeys(t), &got)
		tok, err := devclaims.New(secret).IssueScoped(context.Background(),
			extension.ContextScope{Customer: "wolf"}, "", devclaims.SessionScope("s-hyp-a"))
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/agent/session/s-hyp-a/stream", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || got.SessionScope != "s-hyp-a" || got.DatasetScope != "" {
			t.Fatalf("status=%d identity=%+v", rr.Code, got)
		}
	})

	t.Run("dev-open needs no token at all", func(t *testing.T) {
		var got httpapi.Identity
		h := captureIdentity(nil, noKeys(t), &got)
		req := httptest.NewRequest(http.MethodGet, datasetDownloadPathForTests, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || got.Customer == "" || got.DatasetScope != "" {
			t.Fatalf("status=%d identity=%+v", rr.Code, got)
		}
	})
}

// ── the whole path, in one process ──────────────────────────────────────────
//
// The two tests above grade the middleware; httpapi/datasets_test.go grades the
// handler. Neither on its own proves the criterion that matters — "a request
// with ?token= and NO auth header reaches the download handler and gets bytes"
// — because the seam between them (principal.datasetScope → Identity.DatasetScope
// → the pin check) lives in the join. This mounts the real middleware in front
// of the real mux and asks for the bytes.
//
// It is not the O9 round trip: no container, no Postgres, no blob backend. It
// is the in-process composition those depend on.

// fakeDatasetRoutes is httpapi.DatasetStore over one project's one dataset.
type fakeDatasetRoutes struct{ row *agentdb.Dataset }

func (f *fakeDatasetRoutes) find(project, name string) (*agentdb.Dataset, error) {
	if f.row == nil || f.row.Project != project || f.row.Name != name {
		return nil, agentdb.ErrDatasetNotFound
	}
	return f.row, nil
}

func (f *fakeDatasetRoutes) ListDatasets(_ context.Context, project, _ string, _ int) ([]*agentdb.Dataset, error) {
	if f.row == nil || f.row.Project != project {
		return nil, nil
	}
	return []*agentdb.Dataset{f.row}, nil
}

func (f *fakeDatasetRoutes) CurrentDataset(_ context.Context, project, name string) (*agentdb.Dataset, error) {
	return f.find(project, name)
}

func (f *fakeDatasetRoutes) GetDatasetVersion(_ context.Context, project, name string, version int) (*agentdb.Dataset, error) {
	row, err := f.find(project, name)
	if err != nil || row.Version != version {
		return nil, agentdb.ErrDatasetNotFound
	}
	return row, nil
}

func (f *fakeDatasetRoutes) ListDatasetVersions(_ context.Context, project, name string, _ int) ([]*agentdb.Dataset, error) {
	row, err := f.find(project, name)
	if err != nil {
		return nil, err
	}
	return []*agentdb.Dataset{row}, nil
}

// fakeDatasetBytes is httpapi.DatasetBlobReader over one key.
type fakeDatasetBytes struct{ key, body string }

func (f *fakeDatasetBytes) Read(_ context.Context, key string) (io.ReadCloser, error) {
	if key != f.key {
		return nil, errors.New("no such blob")
	}
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func TestDatasetDownloadAuth_EndToEndThroughTheRealMux(t *testing.T) {
	const body = "timestamp,value\n2026-08-20T00:00:00Z,143.90\n"
	secret := []byte("test-secret")
	row := &agentdb.Dataset{
		ID: "ds-1", Project: "wolf", Name: "basket", Version: 7,
		BlobPath: agentdb.DatasetBlobPrefix + "abc", SizeBytes: int64(len(body)),
		RowCount: 1, SHA256: "deadbeef", ContentType: "text/csv", CreatedAt: 1789000000123,
	}
	api, err := httpapi.New(httpapi.Config{
		Runner:       &stubRunner{},
		Store:        newFakeRouterStore(),
		Identity:     identityFromRequest, // the SAME reader main() wires
		Datasets:     &fakeDatasetRoutes{row: row},
		DatasetBlobs: &fakeDatasetBytes{key: row.BlobPath, body: body},
	})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	h := apiAuthMiddleware(secret, wolfKeys(t), api.Mux())

	get := func(path string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		return rr
	}

	// The agent's curl: no headers at all.
	rr := get("/agent/datasets/basket/download?token=" + datasetToken(t, secret, "wolf", "basket"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rr.Code, rr.Body)
	}
	if rr.Body.String() != body {
		t.Fatalf("body = %q, want the dataset's bytes", rr.Body)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("Content-Type = %q", ct)
	}

	// A token for another name in the same project: 404, not 403, and not the
	// bytes. The middleware authenticated it; the handler declined it.
	rr = get("/agent/datasets/basket/download?token=" + datasetToken(t, secret, "wolf", "other"))
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "143.90") {
		t.Fatalf("cross-name token: status=%d body=%q, want 404 and no bytes", rr.Code, rr.Body)
	}

	// A token from another project, for a name that exists here. Same answer.
	rr = get("/agent/datasets/basket/download?token=" + datasetToken(t, secret, "elsewhere", "basket"))
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "143.90") {
		t.Fatalf("cross-project token: status=%d body=%q, want 404 and no bytes", rr.Code, rr.Body)
	}

	// And the token buys nothing on the metadata routes: 401 at the middleware,
	// which is a different answer from the handler's 404 and proves the request
	// never reached a handler at all.
	tok := datasetToken(t, secret, "wolf", "basket")
	for _, path := range []string{
		"/agent/datasets?token=" + tok,
		"/agent/datasets/basket?token=" + tok,
		"/agent/datasets/basket/versions?token=" + tok,
	} {
		if rr := get(path); rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", path, rr.Code)
		}
	}

	// The project's own API key reads the same dataset through the front door,
	// unconfined — proof the leg added a credential rather than replacing one.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agent/datasets/basket", nil)
	req.Header.Set(apiKeyHeader, goodKey)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"version":7`) {
		t.Fatalf("API-key metadata read: status=%d body=%s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "blob_path") {
		t.Fatalf("metadata response leaks blob_path: %s", rr.Body)
	}
}
