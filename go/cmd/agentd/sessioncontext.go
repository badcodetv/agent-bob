package main

// sessioncontext.go — agentd's real SessionContextProvider (docs/product/01-session-config.md §5).
//
// The Runner asks this seam what a session's *defaults* are. agentd answers from
// the product-layer tables: `project_settings` (B1) for the project-wide
// defaults and `workers` (C1) for the worker the session runs as. Precedence,
// per §5:
//
//	system prompt : project prompt PREPENDED to the worker prompt (concatenative)
//	base image    : worker.Image > project_settings.base_image > global Policy.BaseImage
//	MCP servers   : project MCP ∪ worker MCP (union; worker wins on name collision)
//
// The MCP rule is a union on purpose: project MCP config is "granted to **all**
// workers, no exceptions, no filtering" (§5) — a worker can add tools, never
// subtract the project's. Nothing resolved here is a secret: MCP values only
// ever *name* environment variables (`${VAR}`, §4.4); the values themselves
// reach the container through the AGENTKIT_MCP_ENV allowlist (mcpenv.go).
//
// Scope mapping (extension.ContextScope is the engine's generic identity):
//
//	scope.Customer → project (the tenancy namespace, the JWT `customer` claim)
//	scope.Persona  → worker name ((project, name) is a worker's identity)
//
// A persona naming no configured worker is not an error: personas predate the
// workers table, so an unknown one simply contributes no worker layer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/extension"
)

// projectConfigStore is the narrow read seam the provider needs. `*agentdb.Store`
// satisfies it; tests supply a fake, so §5's precedence rules are unit-testable
// with no database (the seam pattern B1 introduced for httpapi handlers).
type projectConfigStore interface {
	GetProjectSettings(ctx context.Context, project string) (*agentdb.ProjectSettings, error)
	GetWorker(ctx context.Context, project, name string) (*agentdb.Worker, error)
}

// sessionContextProvider implements extension.SessionContextProvider against the
// product-layer tables. globalBaseImage is the stack-wide fallback
// (Policy.BaseImage / AGENTKIT_IMAGE) — the last link of the §5 image chain.
type sessionContextProvider struct {
	store           projectConfigStore
	globalBaseImage string
	// connectionServers resolves the grants a worker holds into MCP server
	// entries pointing at agentd's /connect/ proxy
	// (design/2026-09-11-project-connections.md, T2's connections.Servers).
	// nil means no connections layer — every provider built before T12 wires
	// the real registry-backed closure, and the SQLite fallback, which has no
	// project map to build a Registry from.
	connectionServers func(project string, grants []string) agentdb.MCPServers
}

// newSessionContextProvider builds the provider. store must be non-nil.
func newSessionContextProvider(store projectConfigStore, globalBaseImage string) *sessionContextProvider {
	return &sessionContextProvider{store: store, globalBaseImage: globalBaseImage}
}

// withConnectionServers wires T12's registry-backed connections resolver
// after construction, so every existing call to newSessionContextProvider
// (main.go, and every test that does not care about connections) keeps
// compiling unchanged. Returns the receiver for one-line construction in
// tests.
func (p *sessionContextProvider) withConnectionServers(f func(project string, grants []string) agentdb.MCPServers) *sessionContextProvider {
	p.connectionServers = f
	return p
}

// resolvedContext is the full §5 resolution: prompt, image, and the project ∪
// worker MCP defaults, all of which travel on extension.SessionContext.
type resolvedContext struct {
	SystemPrompt string
	BaseImage    string
	// WorkerImage is the worker's §13 pointer, carried UNRESOLVED and separately
	// from BaseImage (which also holds it, as the winner of the §5 chain).
	// Resolution — bare name → latest, `name:version` → pinned — belongs to the
	// catalogue, and happens once, at launch: runner.go:resolveLaunchImage
	// through Deps.Images (imageresolver.go). Doing it here instead would
	// materialise an image for every create, including the worker jobs that
	// arrive with a composed image already chosen.
	WorkerImage string
	// ProjectBaseImage is `project_settings.base_image`, carried UNRESOLVED for
	// the same reason and to the same place as WorkerImage. It may be a §13
	// pointer or a literal registry reference; which one it is, is a question
	// only the catalogue can answer, and it is asked once, at launch.
	ProjectBaseImage string
	MCPServers       agentdb.MCPServers
}

// Resolve implements extension.SessionContextProvider: the system prompt, the
// launch image, and the MCP servers this session defaults to.
//
// MCPServers is load-bearing and easy to drop: A2 added the field and the
// Runner's merge, B2 computed the union — but nothing filled it in between, so
// a project's configured tools resolved correctly and then reached no container
// at all. Populating it here is the whole connection (§5).
func (p *sessionContextProvider) Resolve(ctx context.Context, scope extension.ContextScope) (*extension.SessionContext, error) {
	rc, err := p.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	return &extension.SessionContext{
		SystemPrompt:     rc.SystemPrompt,
		BaseImage:        rc.BaseImage,
		WorkerImage:      rc.WorkerImage,
		ProjectBaseImage: rc.ProjectBaseImage,
		MCPServers:       rc.MCPServers,
	}, nil
}

// ResolveMCPServers returns the project ∪ worker MCP defaults for a scope. The
// session-create path lays request-supplied servers *over* these (§5:
// request-supplied values are additive for MCP) — see MergeMCPServers.
func (p *sessionContextProvider) ResolveMCPServers(ctx context.Context, scope extension.ContextScope) (agentdb.MCPServers, error) {
	rc, err := p.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	return rc.MCPServers, nil
}

// resolve reads both layers and applies §5's precedence.
func (p *sessionContextProvider) resolve(ctx context.Context, scope extension.ContextScope) (*resolvedContext, error) {
	rc := &resolvedContext{BaseImage: p.globalBaseImage, MCPServers: agentdb.MCPServers{}}
	project := scope.Customer
	if project == "" {
		// No tenancy in scope: nothing project-specific to apply. The global
		// default still stands, so such a session starts rather than failing.
		return rc, nil
	}

	// ── Project layer ────────────────────────────────────────────────────────
	// GetProjectSettings returns the spec defaults (never "not found") for a
	// project whose settings nobody has written yet.
	ps, err := p.store.GetProjectSettings(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("session context: project settings for %q: %w", project, err)
	}
	projectMCP, err := decodeMCPConfig(ps.MCPConfig)
	if err != nil {
		return nil, fmt.Errorf("session context: project %q mcp_config: %w", project, err)
	}
	prompts := []string{ps.SystemPrompt}
	if ps.BaseImage != "" {
		// Verbatim here, for the same reason as the worker pointer below: this
		// string may be a §13 name or a plain registry reference, and only the
		// catalogue can tell. Carrying it separately is what lets the Runner
		// ask — before I4 it travelled on BaseImage alone, where a curated
		// name was indistinguishable from a docker ref and was therefore
		// pulled as one, and every session in the project failed to launch.
		rc.BaseImage = ps.BaseImage
		rc.ProjectBaseImage = ps.BaseImage
	}
	rc.MCPServers = MergeMCPServers(rc.MCPServers, projectMCP)

	// ── Worker layer (wins on every axis) ────────────────────────────────────
	var personaWorker *agentdb.Worker
	if workerName := scope.Persona; workerName != "" {
		w, err := p.store.GetWorker(ctx, project, workerName)
		switch {
		case errors.Is(err, agentdb.ErrWorkerNotFound):
			// A persona with no worker row: project defaults only.
		case err != nil:
			return nil, fmt.Errorf("session context: worker %q/%q: %w", project, workerName, err)
		default:
			personaWorker = w
			workerMCP, decErr := decodeMCPConfig(w.MCPConfig)
			if decErr != nil {
				return nil, fmt.Errorf("session context: worker %q/%q mcp_config: %w", project, workerName, decErr)
			}
			prompts = append(prompts, w.SystemPrompt)
			if w.Image != "" {
				// Passed through verbatim: §13's bare-name → latest /
				// name:version → pinned resolution belongs to the image
				// catalogue, not to this seam. WorkerImage is what says
				// "this string is a §13 pointer, not an image ref" — without
				// it the Runner could only guess, and guessing wrong means
				// either refusing a legitimate docker ref or silently
				// launching a worker somewhere it was not pointed (§13.3).
				rc.BaseImage = w.Image
				rc.WorkerImage = w.Image
			}
			// Union, worker wins on collision — never a filter of project tools.
			rc.MCPServers = MergeMCPServers(rc.MCPServers, workerMCP)
		}
	}

	// ── Connections layer (design/2026-09-11-project-connections.md, T8) ────
	// Deliberately keyed on scope.Worker, NEVER scope.Persona: the /connect/
	// proxy authorises every request against Session.Worker (Decision 6), so
	// an entry keyed on Persona would be granted here and 403 forever there.
	// A plain chat (persona only, Worker empty) gets none; a session whose
	// persona and worker differ gets the WORKER's grants, which may mean a
	// second store read when they are not the same name.
	if p.connectionServers != nil && scope.Worker != "" {
		grants, err := p.connectionsHeldBy(ctx, project, scope.Worker, personaWorker)
		if err != nil {
			return nil, err
		}
		rc.MCPServers = MergeMCPServers(rc.MCPServers, p.connectionServers(project, grants))
	}

	rc.SystemPrompt = joinPrompts(prompts...)
	// Fail loudly on config that cannot work rather than handing the sandbox a
	// server it would mis-spawn with a literal "${VAR}" credential (§4.1).
	if err := rc.MCPServers.Validate(); err != nil {
		return nil, fmt.Errorf("session context: project %q mcp config: %w", project, err)
	}
	return rc, nil
}

// connectionsHeldBy returns the grants of the worker named workerName. When
// workerName is the same worker already fetched for the persona layer
// (personaWorker, which may be nil), it is reused rather than read twice —
// the common case, since a worker-attached chat sets persona == worker. A
// worker that does not exist, or is disabled, holds nothing: neither is an
// error here, matching "an unknown persona contributes no worker layer"
// above (the /connect/ proxy is what actually enforces "disabled holds
// nothing" for live traffic — this seam only feeds a default MCP map at
// create time).
func (p *sessionContextProvider) connectionsHeldBy(ctx context.Context, project, workerName string, personaWorker *agentdb.Worker) ([]string, error) {
	if personaWorker != nil && personaWorker.Name == workerName {
		if !personaWorker.Enabled {
			return nil, nil
		}
		return personaWorker.Connections, nil
	}
	w, err := p.store.GetWorker(ctx, project, workerName)
	switch {
	case errors.Is(err, agentdb.ErrWorkerNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("session context: worker %q/%q: %w", project, workerName, err)
	}
	if !w.Enabled {
		return nil, nil
	}
	return w.Connections, nil
}

// joinPrompts concatenates the non-empty layers in precedence order, project
// first ("the project-level system prompt, prepended to every worker's prompt").
func joinPrompts(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n\n")
}

// MergeMCPServers returns base ∪ over, with `over` winning on name collision.
// Neither input is mutated. Used for both layers of the §5 union, and by the
// session-create path to lay request-supplied servers over the defaults.
func MergeMCPServers(base, over agentdb.MCPServers) agentdb.MCPServers {
	merged := make(agentdb.MCPServers, len(base)+len(over))
	for name, cfg := range base {
		merged[name] = cfg
	}
	for name, cfg := range over {
		merged[name] = cfg
	}
	return merged
}

// decodeMCPConfig turns the opaque `mcp_config` jsonb (agentdb stores it as a
// JSONMap so the store stays free of A1's types) into typed MCP servers.
// A decode failure is an error, not a silent drop: a project whose tool config
// is unreadable must be fixed, not quietly run without its tools.
func decodeMCPConfig(raw agentdb.JSONMap) (agentdb.MCPServers, error) {
	if len(raw) == 0 {
		return agentdb.MCPServers{}, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("re-encode: %w", err)
	}
	var servers agentdb.MCPServers
	if err := json.Unmarshal(b, &servers); err != nil {
		return nil, fmt.Errorf("decode into MCPServers: %w", err)
	}
	if servers == nil {
		servers = agentdb.MCPServers{}
	}
	return servers, nil
}

// logSessionContextWiring reports which config layers agentd applies, so an
// operator can see at a glance whether project settings are live.
func logSessionContextWiring(enabled bool) {
	if enabled {
		log.Printf("[agentd] session context=project_settings+workers (base image, system prompt, MCP defaults)")
		return
	}
	log.Printf("[agentd] session context=global only (no Postgres store → project settings/workers unavailable)")
}
