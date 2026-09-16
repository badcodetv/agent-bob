package main

// runner_sessioncontext_connections_test.go — T8's "stronger test": wires the
// REAL agentkit.Runner to the REAL sessionContextProvider (with
// withConnectionServers), drives an actual CreateSession with Persona X /
// Worker Y, and asserts Y's connection lands on the PERSISTED session row's
// MCPServers — the end-to-end version of what
// TestCreateSessionScopeCarriesWorkerDistinctFromPersona (go/runner_connections_test.go)
// proves at the scope level and TestSessionContextProvider_Connections
// (sessioncontext_test.go) proves at the policy level. No Docker, no
// Postgres: agentkittest.MemStore plus execenv.NewMock() stand in for the
// host and the sandbox, same as the rest of this package's non-integration
// tests (see mcp_datasets_test.go).
import (
	"context"
	"testing"

	agentkit "github.com/badcodetv/agent-bob"
	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/agentkittest"
	"github.com/badcodetv/agent-bob/artifacts"
	"github.com/badcodetv/agent-bob/events"
	"github.com/badcodetv/agent-bob/execenv"
	"github.com/badcodetv/agent-bob/imageregistry"
)

// TestRunnerCreateSessionAppliesWorkerConnectionsNotPersona drives the real
// wiring end to end: persona "researcher" holds "github", worker "writer"
// holds "gmail". A session created with Persona: "researcher", Worker:
// "writer" must persist "gmail" on its MCPServers and must NOT persist
// "github" — Decision 6 (connections key on Worker, never Persona), proved
// against the real provider and the real Runner rather than a fake standing
// in for either.
func TestRunnerCreateSessionAppliesWorkerConnectionsNotPersona(t *testing.T) {
	store := &fakeConfigStore{
		workers: map[string]*agentdb.Worker{
			"acme/researcher": {Project: "acme", Name: "researcher", Enabled: true,
				SystemPrompt: "you research", Connections: agentdb.ConnectionList{"github"}},
			"acme/writer": {Project: "acme", Name: "writer", Enabled: true,
				SystemPrompt: "you write", Connections: agentdb.ConnectionList{"gmail"}},
		},
	}
	connectionServers := func(project string, grants []string) agentdb.MCPServers {
		out := agentdb.MCPServers{}
		for _, g := range grants {
			out[g] = agentdb.MCPServerConfig{URL: "https://self.example/connect/" + g + "/"}
		}
		return out
	}
	provider := newSessionContextProvider(store, "base:dev").withConnectionServers(connectionServers)

	memStore := agentkittest.NewMemStore()
	runner, err := agentkit.NewRunner(agentkit.Deps{
		Env:            execenv.NewMock(),
		Registry:       imageregistry.NewMock(),
		Store:          memStore,
		Artifacts:      artifacts.NewMock(),
		Claims:         agentkittest.StaticClaims{Token: "test-token"},
		Events:         events.NewPipeline(events.NewMockSink()),
		SessionContext: provider,
		Policy:         agentkit.Policy{BaseImage: "agentkit-sandbox:test"},
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	memStore.Seed(&agentdb.Session{ID: "s1", Customer: "acme"})

	if _, err := runner.CreateSession(context.Background(), agentkit.CreateSessionRequest{
		SessionID: "s1", Customer: "acme", Persona: "researcher", Worker: "writer",
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := memStore.GetSession(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if _, ok := sess.MCPServers["gmail"]; !ok {
		t.Errorf("persisted session is missing the WORKER's (writer) connection %q: %v", "gmail", sess.MCPServers)
	}
	if _, ok := sess.MCPServers["github"]; ok {
		t.Errorf("persisted session carries the PERSONA's (researcher) connection %q, want only the worker's: %v", "github", sess.MCPServers)
	}
}
