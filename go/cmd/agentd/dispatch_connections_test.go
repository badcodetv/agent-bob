package main

// dispatch_connections_test.go — T8: a dispatched job's MCP map carries the
// dispatching worker's connections (design/2026-09-11-project-connections.md).
//
// dispatch.go computes ComposeJobInput.Connections from worker.Connections via
// dispatcherConfig.ConnectionServers; this is what proves that wire end to end
// through the real router → dispatcher → ComposeJob path, not just compose.go
// in isolation.

import (
	"context"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// TestDispatchIncludesConnectionServers proves a worker holding a grant gets
// the resolved MCP entry on its composed job, keyed on the worker (never
// persona, which a dispatched job leaves empty — dispatch_prompt_test.go).
func TestDispatchIncludesConnectionServers(t *testing.T) {
	store := newFakeRouterStore()
	w := seedWorker(store, "acme", "researcher", 1)
	w.Connections = agentdb.ConnectionList{"github"}
	store.addSubscription(&agentdb.Subscription{
		Project: "acme", EventType: "email.received", Worker: "researcher", Enabled: true,
	})

	var gotProject string
	var gotGrants []string
	runner := &stubRunner{}
	starter := newRunnerSessionStarter(runner, store).withLeases(store)
	starter.now = store.now
	starter.logf = quietf
	starter.run = func(fn func()) { fn() }
	starter.newID = func() string { return "job-1" }

	rt, _ := newTestRouter(store, starter, func(cfg *dispatcherConfig) {
		cfg.ConnectionServers = func(project string, grants []string) agentdb.MCPServers {
			gotProject, gotGrants = project, grants
			out := agentdb.MCPServers{}
			for _, g := range grants {
				out[g] = agentdb.MCPServerConfig{URL: "https://self.example/connect/" + g + "/"}
			}
			return out
		}
	})

	postEvent(t, store, "acme", "email.received", "a customer wrote in", agentdb.EventEnvelope{Depth: 0})
	if err := rt.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(runner.created) != 1 {
		t.Fatalf("expected exactly one CreateSession, got %d", len(runner.created))
	}
	created := runner.created[0]

	if gotProject != "acme" {
		t.Errorf("ConnectionServers project = %q, want acme", gotProject)
	}
	if len(gotGrants) != 1 || gotGrants[0] != "github" {
		t.Errorf("ConnectionServers grants = %v, want [github]", gotGrants)
	}
	got, ok := created.MCPServers["github"]
	if !ok {
		t.Fatalf("composed MCP map is missing the github connection: %v", created.MCPServers)
	}
	if got.URL != "https://self.example/connect/github/" {
		t.Errorf("github entry URL = %q, want the /connect/ URL", got.URL)
	}
}

// TestDispatchWithNoConnectionServersGetsNone pins the pre-T12 default: a nil
// ConnectionServers (every dispatcherConfig in every OTHER existing test, and
// the wiring before T12 lands) must not add anything, so this ticket cannot
// silently change any existing job's MCP map.
func TestDispatchWithNoConnectionServersGetsNone(t *testing.T) {
	sess, created := startOneJob(t, "acme", "answerer")
	_ = sess
	if len(created.MCPServers) != 0 {
		t.Errorf("expected no MCP servers with no ConnectionServers wired, got %v", created.MCPServers)
	}
}

// TestDispatchConnectionsAreKeyedOnTheWorkerNotPersona pins Decision 6 for the
// dispatch path specifically: a dispatched job leaves session.persona empty
// (dispatch_prompt_test.go) and identifies purely by worker, so the grants
// resolved here must come from worker.Connections regardless of any persona
// concept — there is none on this path.
func TestDispatchConnectionsAreKeyedOnTheWorkerNotPersona(t *testing.T) {
	store := newFakeRouterStore()
	holder := seedWorker(store, "acme", "holder", 1)
	holder.Connections = agentdb.ConnectionList{"gmail"}
	bystander := seedWorker(store, "acme", "bystander", 1)
	bystander.Connections = nil
	store.addSubscription(&agentdb.Subscription{
		Project: "acme", EventType: "email.received", Worker: "bystander", Enabled: true,
	})

	runner := &stubRunner{}
	starter := newRunnerSessionStarter(runner, store).withLeases(store)
	starter.now = store.now
	starter.logf = quietf
	starter.run = func(fn func()) { fn() }
	starter.newID = func() string { return "job-1" }

	rt, _ := newTestRouter(store, starter, func(cfg *dispatcherConfig) {
		cfg.ConnectionServers = func(project string, grants []string) agentdb.MCPServers {
			out := agentdb.MCPServers{}
			for _, g := range grants {
				out[g] = agentdb.MCPServerConfig{URL: "https://self.example/connect/" + g + "/"}
			}
			return out
		}
	})

	postEvent(t, store, "acme", "email.received", "hello", agentdb.EventEnvelope{Depth: 0})
	if err := rt.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(runner.created) != 1 {
		t.Fatalf("expected exactly one CreateSession, got %d", len(runner.created))
	}
	created := runner.created[0]
	if _, ok := created.MCPServers["gmail"]; ok {
		t.Errorf("bystander's job got holder's connection: %v", created.MCPServers)
	}
	if len(created.MCPServers) != 0 {
		t.Errorf("bystander holds no connections, want none on the composed job, got %v", created.MCPServers)
	}
}
