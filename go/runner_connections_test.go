package agentkit

// runner_connections_test.go — the wire T8 is actually about
// (design/2026-09-11-project-connections.md, Decision 6): a
// CreateSessionRequest carrying Persona and Worker must reach the
// SessionContextProvider with scope.Worker set to the WORKER, not the
// persona, because agentd's sessionContextProvider keys connection grants off
// scope.Worker alone (sessioncontext.go, sessioncontext_test.go's
// TestSessionContextProvider_Connections). That policy is unit-tested in
// isolation, but nothing before this file checked that resolveSessionContext
// (runner.go) actually plumbs req.Worker onto the scope it hands the
// provider — the criterion "a session with persona X and worker Y gets Y's
// grants" has no test for the one production line that makes it possible.
//
// scopeRecordingProvider below plays the part of agentd's real provider, but
// records what it was called with instead of resolving anything, so the
// assertion is purely about the scope the Runner builds — not about §5's
// precedence policy, which sessioncontext_test.go already owns.

import (
	"context"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/agentkittest"
	"github.com/badcodetv/agent-bob/artifacts"
	"github.com/badcodetv/agent-bob/events"
	"github.com/badcodetv/agent-bob/execenv"
	"github.com/badcodetv/agent-bob/extension"
	"github.com/badcodetv/agent-bob/imageregistry"
)

// scopeRecordingProvider is a host SessionContextProvider that records the
// scope it was resolved with and returns a fixed (possibly nil) context.
type scopeRecordingProvider struct {
	scope extension.ContextScope
	sc    *extension.SessionContext
}

func (p *scopeRecordingProvider) Resolve(_ context.Context, scope extension.ContextScope) (*extension.SessionContext, error) {
	p.scope = scope
	if p.sc != nil {
		return p.sc, nil
	}
	return &extension.SessionContext{}, nil
}

// newConnectionsTestRunner builds a hermetic runner (mock env, mock registry,
// in-memory store) wired to provider — CreateSession never needs a real
// sandbox to reach resolveSessionContext, so the mock env's default address
// (not an http:// URL) is enough: runner.go skips the POST /sessions leg for
// it.
func newConnectionsTestRunner(t *testing.T, provider extension.SessionContextProvider) (*runnerImpl, *agentkittest.MemStore) {
	t.Helper()
	store := agentkittest.NewMemStore()
	runner, err := NewRunner(Deps{
		Env:            execenv.NewMock(),
		Registry:       imageregistry.NewMock(),
		Store:          store,
		Artifacts:      artifacts.NewMock(),
		Claims:         agentkittest.StaticClaims{Token: "test-token"},
		Events:         events.NewPipeline(events.NewMockSink()),
		SessionContext: provider,
		Policy:         Policy{BaseImage: "agentkit-sandbox:test"},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return runner.(*runnerImpl), store
}

// TestCreateSessionScopeCarriesWorkerDistinctFromPersona pins the production
// wire at runner.go:resolveSessionContext: Worker must reach the provider's
// scope as its own field, not folded into or replaced by Persona. Removing
// `Worker: req.Worker` from that call site (verifier's repro) leaves every
// assertion here failing except the persona-only case, which is exactly the
// gap the rejection named.
func TestCreateSessionScopeCarriesWorkerDistinctFromPersona(t *testing.T) {
	t.Run("persona X, worker Y: scope carries both, distinctly", func(t *testing.T) {
		provider := &scopeRecordingProvider{}
		r, store := newConnectionsTestRunner(t, provider)
		store.Seed(&agentdb.Session{ID: "s1", Customer: "acme"})

		if _, err := r.CreateSession(context.Background(), CreateSessionRequest{
			SessionID: "s1", Customer: "acme", Persona: "researcher", Worker: "writer",
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		if provider.scope.Persona != "researcher" {
			t.Errorf("scope.Persona = %q, want %q", provider.scope.Persona, "researcher")
		}
		if provider.scope.Worker != "writer" {
			t.Errorf("scope.Worker = %q, want %q (the request's Worker, not its Persona)", provider.scope.Worker, "writer")
		}
	})

	t.Run("persona-only request: scope.Worker is empty", func(t *testing.T) {
		provider := &scopeRecordingProvider{}
		r, store := newConnectionsTestRunner(t, provider)
		store.Seed(&agentdb.Session{ID: "s2", Customer: "acme"})

		if _, err := r.CreateSession(context.Background(), CreateSessionRequest{
			SessionID: "s2", Customer: "acme", Persona: "researcher",
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		if provider.scope.Persona != "researcher" {
			t.Errorf("scope.Persona = %q, want %q", provider.scope.Persona, "researcher")
		}
		if provider.scope.Worker != "" {
			t.Errorf("scope.Worker = %q, want empty for a persona-only request", provider.scope.Worker)
		}
	})

	t.Run("worker-only request (dispatch path): scope carries Worker with no Persona", func(t *testing.T) {
		provider := &scopeRecordingProvider{}
		r, store := newConnectionsTestRunner(t, provider)
		store.Seed(&agentdb.Session{ID: "s3", Customer: "acme"})

		if _, err := r.CreateSession(context.Background(), CreateSessionRequest{
			SessionID: "s3", Customer: "acme", Worker: "writer",
		}); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}

		if provider.scope.Persona != "" {
			t.Errorf("scope.Persona = %q, want empty (dispatch leaves persona empty)", provider.scope.Persona)
		}
		if provider.scope.Worker != "writer" {
			t.Errorf("scope.Worker = %q, want %q", provider.scope.Worker, "writer")
		}
	})
}
