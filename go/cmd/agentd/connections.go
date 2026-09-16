package main

// connections.go — agentd's wiring of project connections
// (design/2026-09-11-project-connections.md, T12).
//
// go/connections owns the rules: what a spec is, who holds what, how a grant
// becomes an MCP entry, and the /connect/ proxy itself. What lives HERE is the
// part only agentd can supply — the Registry built from the project map and
// agentd's own environment, the session-token verifier, and the worker table —
// and the few boot-time decisions main.go makes about them.
//
// Decision 1: the Registry is built ONCE, at boot. The project map itself
// reloads on SIGHUP and a timer (A6, projectSettingsHolder), but a reload does
// NOT rebuild connections: editing a connection needs an agentd restart. That
// is deliberate for one operator — a token source mid-refresh should not be
// swapped out from under a live request — and it is why this file takes a
// plain *connections.Registry rather than a holder.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/connections"
)

// connectPath is where the proxy is mounted on the ROOT mux, beside /mcp and
// outside apiAuthMiddleware: its caller is a session container holding a
// session token, not a console JWT or an API key. connections.NewProxy parses
// the full path (/connect/{name}/{rest}), so it is mounted without StripPrefix.
const connectPath = "/connect/"

// buildConnectionRegistry builds the process-wide Registry from the project
// map's connections blocks and logs one line per project naming each
// connection and whether it is available. Names and env var NAMES only —
// never a value: agentd's log is read by more eyes than its environment.
//
// A spec that fails validation is a boot error (NewRegistry); a missing env var
// is not — that connection is simply unavailable, which the line says. A nil
// specs map (no project map, or no project with connections) yields an empty,
// non-nil Registry, so every consumer sees "this project has no connections"
// rather than having to special-case the demo stack.
func buildConnectionRegistry(specs map[string]map[string]connections.Spec, getenv func(string) string, logf func(string, ...any)) (*connections.Registry, error) {
	reg, err := connections.NewRegistry(specs, getenv, func(format string, args ...any) {
		logf("[agentd] "+format, args...)
	})
	if err != nil {
		return nil, fmt.Errorf("project map: connections: %w", err)
	}
	if len(specs) == 0 {
		logf("[agentd] connections: no project has connections (add a connections block to the project map)")
		return reg, nil
	}
	projects := make([]string, 0, len(specs))
	for project := range specs {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	for _, project := range projects {
		parts := []string{}
		for _, info := range reg.List(project) {
			// A google_account connection's availability is decided at use
			// (Connect Google stores its credential while agentd runs), so
			// the boot line names its account instead.
			if conn, ok := reg.Get(project, info.Name); ok && conn.Spec.Auth.Type == connections.AuthGoogleAccount {
				parts = append(parts, fmt.Sprintf("%s=google_account(%s)", info.Name, conn.Spec.Auth.Account))
				continue
			}
			if info.Available {
				parts = append(parts, info.Name+"=available")
			} else {
				parts = append(parts, fmt.Sprintf("%s=UNAVAILABLE (%s)", info.Name, info.Unavailable))
			}
		}
		logf("[agentd] connections: project %q: %s", project, strings.Join(parts, ", "))
	}
	return reg, nil
}

// warnIfBothProjectMaps logs a loud warning when AGENTKIT_PROJECT_MAP and
// AGENTKIT_PROJECT_MAP_FILE are both set, and reports whether it did.
//
// loadProjectSettings lets the inline map win and never opens the file, which
// was harmless while the two held the same handful of users. It stops being
// harmless now the file is where connections are declared (Decision 8: the
// local-config/ mount): a connections block added to the file would silently
// do nothing, and "my GitHub tools never appeared" is a long way from "an old
// inline map in .env shadows the file". Neither value is echoed — the inline
// map is operator config, and the file path is all the operator needs.
func warnIfBothProjectMaps(getenv func(string) string, logf func(string, ...any)) bool {
	inline := strings.TrimSpace(getenv("AGENTKIT_PROJECT_MAP"))
	file := strings.TrimSpace(getenv("AGENTKIT_PROJECT_MAP_FILE"))
	if inline == "" || file == "" {
		return false
	}
	logf("[agentd] WARNING: both AGENTKIT_PROJECT_MAP and AGENTKIT_PROJECT_MAP_FILE are set — the inline "+
		"AGENTKIT_PROJECT_MAP wins and %s is ignored entirely (its users, projects and connections). "+
		"Unset AGENTKIT_PROJECT_MAP to use the file", file)
	return true
}

// connectWorkerLookup is the narrow read seam the /connect/ adapter needs: the
// worker row, for its grants and its enabled flag. *agentdb.Store satisfies it.
type connectWorkerLookup interface {
	GetWorker(ctx context.Context, project, name string) (*agentdb.Worker, error)
}

var _ connectWorkerLookup = (*agentdb.Store)(nil)

// newConnectHandler adapts agentd's session-token verifier and worker table to
// connections.ProxyConfig.
//
// Authenticate: the token is the bare ${SESSION_TOKEN} the container's MCP
// entry sends in Authorization (connections.Servers), verified by the SAME
// verifyToken /mcp and the real-key /agent-proxy/ use, under requireLive=true —
// an expired token is honoured only while its session is live (Decision 3's
// last bullet), so a token read with Bash and sent out over open egress stops
// working once the session is done. The caller's Worker is the session ROW's
// Worker, which verifyToken reads; nothing the request carries is consulted —
// not a header, and not Persona (Decision 6).
//
// Grants: re-read on every request, so revoking a grant takes effect
// mid-session. A deleted or disabled worker holds nothing and is reported as
// connections.ErrWorkerGone (the 403 row), not as a store failure; any other
// read error is returned as-is (the 503 "retry" row).
func newConnectHandler(reg *connections.Registry, auth *sessionTokenAuth, workers connectWorkerLookup, logf func(string, ...any)) http.Handler {
	return connections.NewProxy(connections.ProxyConfig{
		Registry: reg,
		Authenticate: func(r *http.Request) (connections.Caller, error) {
			caller, err := auth.verifyToken(r.Context(), bearerToken(r.Header.Get("Authorization")), true)
			if err != nil {
				if errors.Is(err, errSessionTokenExpired) {
					return connections.Caller{}, fmt.Errorf("%w: %v", connections.ErrSessionExpired, err)
				}
				return connections.Caller{}, err
			}
			return connections.Caller{Project: caller.Project, SessionID: caller.SessionID, Worker: caller.Worker}, nil
		},
		Grants: func(ctx context.Context, project, worker string) ([]string, error) {
			w, err := workers.GetWorker(ctx, project, worker)
			switch {
			case errors.Is(err, agentdb.ErrWorkerNotFound):
				return []string{}, connections.ErrWorkerGone
			case err != nil:
				return nil, err
			case w == nil || !w.Enabled:
				return []string{}, connections.ErrWorkerGone
			}
			return w.Connections, nil
		},
		Logf: func(format string, args ...any) { logf("[agentd] "+format, args...) },
	})
}

// mountConnectRoute mounts /connect/ on root when there is a worker store, and
// reports whether it did. Without one (the SQLite fallback) there are no
// workers to hold a grant, so the route is left ABSENT — a 404 — rather than
// mounted as a handler that could only ever refuse.
//
// workers may arrive as a nil *agentdb.Store boxed into the interface (main.go's
// agentDB on the fallback); that is unwrapped here and counts as no store,
// because its GetWorker would panic on the first request.
func mountConnectRoute(root *http.ServeMux, reg *connections.Registry, auth *sessionTokenAuth, workers connectWorkerLookup, logf func(string, ...any)) bool {
	if s, ok := workers.(*agentdb.Store); ok && s == nil {
		workers = nil
	}
	if workers == nil {
		logf("[agentd] connections proxy DISABLED (no DATABASE_URL): grants live in the workers table")
		return false
	}
	root.Handle(connectPath, newConnectHandler(reg, auth, workers, logf))
	return true
}

// connectionServersFor is the resolver main.go hands both the dispatcher and
// the session-context provider: a worker's grants → MCP entries pointing at
// selfURL/connect/<name>/, via connections.Servers. nil registry → nil
// resolver, which both consumers read as "no connections layer".
//
// connections.Servers skips grants it cannot honour silently (it has no
// logger); this logs them, one line per resolution, so "the worker never got
// its github tools" is answerable from agentd's log. Two kinds are skipped: a
// connection that exists but is unavailable (Registry.Availability's reason:
// the env var, or for google_account why the account cannot be used),
// and a grant naming no connection this project has — a stale name left on a
// worker after the map dropped it.
func connectionServersFor(reg *connections.Registry, selfURL string, logf func(string, ...any)) func(project string, grants []string) agentdb.MCPServers {
	if reg == nil {
		return nil
	}
	return func(project string, grants []string) agentdb.MCPServers {
		servers := connections.Servers(reg, project, grants, selfURL)
		skipped := []string{}
		for _, g := range grants {
			if g == connections.Wildcard {
				continue
			}
			if _, ok := reg.Get(project, g); !ok {
				skipped = append(skipped, g+" (no such connection in this project)")
			}
		}
		for _, name := range connections.ExpandGrants(reg, project, grants) {
			if _, ok := servers[name]; ok {
				continue
			}
			reason := "unavailable"
			if ok, why := reg.Availability(project, name); !ok && why != "" {
				reason = why
			}
			skipped = append(skipped, fmt.Sprintf("%s (%s)", name, reason))
		}
		if len(skipped) > 0 {
			sort.Strings(skipped)
			logf("[agentd] connections: project %q: skipped grant(s) %s", project, strings.Join(skipped, ", "))
		}
		return servers
	}
}
