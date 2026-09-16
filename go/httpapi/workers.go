package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/badcodetv/agent-bob/agentdb"
)

// connectionsWildcard mirrors connections.Wildcard / agentdb's own
// unexported connectionWildcard. Duplicated as a literal for the same reason
// agentdb duplicates it: this package must not import go/connections (which
// itself would need to reach back into agentdb) just to read one constant.
const connectionsWildcard = "*"

// contains reports whether name is in list. Small enough not to pull in a
// generic slices helper for one call site.
func contains(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}

// connectionsEqual compares two connection lists structurally, the same way
// agentdb.jsonValueEqual compares stored columns: nil and [] are different,
// order matters. It is what tells "the caller re-sent the stored list
// unchanged" (allowed for any caller) apart from "the caller is trying to
// change it" (refused to an API key or scoped token).
func connectionsEqual(a, b agentdb.ConnectionList) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

// WorkersStore is the worker-catalogue seam the CRUD handlers need.
// *agentdb.Store implements it; hosts may substitute their own.
type WorkersStore interface {
	ListWorkers(ctx context.Context, project string) ([]*agentdb.Worker, error)
	GetWorker(ctx context.Context, project, name string) (*agentdb.Worker, error)
	UpsertWorker(ctx context.Context, w *agentdb.Worker, cw agentdb.ConfigWrite) (*agentdb.Worker, error)
	DeleteWorker(ctx context.Context, project, name string, cw agentdb.ConfigWrite) error
}

// workerBody is the PUT payload. ONE rule for every field: leaving a field out
// keeps whatever the stored row holds, and on create — when there is no stored
// row — it takes the default (NewWorker's). This route is create-or-KEEP, not
// create-or-replace.
//
// It took two steps to get here. T27 (from DI2) made Description, SystemPrompt,
// MCPConfig, Image and Briefing keep on absent, because sending one field used
// to erase the other four without saying so. DI11 then brought MaxInstances,
// Enabled and Frozen into line: while they still replaced on absent, a caller
// saving only a prompt silently thawed a frozen worker, re-enabled a disabled
// one and reset its concurrency to 1 — the three fields a human uses to
// CONTROL a worker, each failing in the unsafe direction. Every other writer
// already kept (the agents' worker_update reads and patches; the git importer
// field-merges), so this route was the only one that did not.
//
// Clearing stays explicit: "" / {} / [] for the five that can be empty, and an
// explicit value for the three that cannot. The pointers exist only so that a
// meaningful zero ("", 0, false) can be told apart from "not supplied".
//
// Frozen rides THIS route deliberately: the JWT-guarded HTTP API is the human
// path, so freeze and unfreeze are one more field on the ordinary worker write
// (mirroring how Enabled is toggled) rather than a parallel endpoint. The core
// MCP server — the workers' path — never exposes the field at all.
type workerBody struct {
	// Every field keeps on absent (see above). Absent or JSON `null` keeps the
	// stored value; an explicit "", {} or [] clears the five that can be empty.
	//
	// The three strings need pointers to tell "" apart from absent. The two
	// composites do not: encoding/json already leaves a slice or map nil for
	// absent and for `null`, and allocates a non-nil empty one for [] or {},
	// which IS the distinction. A *agentdb.SelectorList would add a pointer to
	// a slice and buy nothing.
	Description  *string              `json:"description"`   // nil → keep
	SystemPrompt *string              `json:"system_prompt"` // nil → keep
	MCPConfig    agentdb.JSONMap      `json:"mcp_config"`    // nil → keep, {} → clear
	Image        *string              `json:"image"`         // nil → keep
	Briefing     agentdb.SelectorList `json:"briefing"`      // nil → keep, [] → clear
	MaxInstances *int                 `json:"max_instances"` // nil → keep (1 on create)
	Enabled      *bool                `json:"enabled"`       // nil → keep (true on create)
	Frozen       *bool                `json:"frozen"`        // nil → keep (false on create)
	// Connections is the list of connection names (or "*") this worker holds
	// (design/2026-09-11-project-connections.md T7). Same nil-keeps/[]-clears
	// rule as Briefing. Unlike every other field, CHANGING it is refused to a
	// caller that is not a logged-in person (see the APIKey/Scope check in
	// PutWorker) — granting reach is a trust decision the design reserves for
	// a human at the console, never for an API key or a scoped token.
	Connections agentdb.ConnectionList `json:"connections"` // nil → keep, [] → clear
	// Rationale is the operator's one-line reason, threaded into the config
	// event (design B3). Optional on the wire — the UI asks for one, the route
	// does not refuse a write without one.
	Rationale string `json:"rationale"`
}

// workers returns the configured store, or writes 501 and returns nil when the
// host has not wired one (mirrors the optional-Artifacts contract).
func (h *Handlers) workers(w http.ResponseWriter) WorkersStore {
	if h.cfg.Workers == nil {
		http.Error(w, "worker store not configured", http.StatusNotImplemented)
		return nil
	}
	return h.cfg.Workers
}

// ListWorkers returns every worker in the authenticated principal's project.
// Project is taken from the token (Identity.Customer) and never from the
// request — that is the whole tenancy boundary for this route set.
func (h *Handlers) ListWorkers(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	store := h.workers(w)
	if store == nil {
		return
	}
	list, err := store.ListWorkers(r.Context(), id.Customer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"workers": list})
}

// GetWorker returns one worker. A worker in another project is reported as
// not-found, never as forbidden.
func (h *Handlers) GetWorker(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	store := h.workers(w)
	if store == nil {
		return
	}
	worker, err := store.GetWorker(r.Context(), id.Customer, r.PathValue("name"))
	if err != nil {
		writeWorkerErr(w, err)
		return
	}
	writeJSON(w, worker)
}

// PutWorker creates or replaces a worker, then echoes the stored row back
// (read-back validation, spec 05-management-tools §9).
func (h *Handlers) PutWorker(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	store := h.workers(w)
	if store == nil {
		return
	}
	var body workerBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	name := r.PathValue("name")
	worker := agentdb.NewWorker(id.Customer, name)

	// Seed EVERY writable field from the stored row, so that whatever the body
	// omits is kept (T27 for the five content fields, DI11 for the three control
	// fields). Before T27 the handler assigned the content fields from the body
	// unconditionally, so installing a new prompt with {"system_prompt": …}
	// erased the rest — silently, with a 200 and a read-back echo that looked
	// correct because it echoed what had just been stored. That is how the
	// architect lost its briefing during T1. Until DI11 the same shape of bug
	// remained on the control fields: an omitted `frozen` read as false, so a
	// prompt-only save thawed a frozen worker.
	//
	// A store error that is NOT "no such row" fails the request rather than
	// falling through to the defaults: falling through would reintroduce the
	// exact wipe this guards against, and do it only intermittently.
	switch prev, err := store.GetWorker(r.Context(), id.Customer, name); {
	case err == nil && prev != nil:
		worker.Description = prev.Description
		worker.SystemPrompt = prev.SystemPrompt
		worker.MCPConfig = prev.MCPConfig
		worker.Image = prev.Image
		worker.Briefing = prev.Briefing
		worker.MaxInstances = prev.MaxInstances
		worker.Enabled = prev.Enabled
		worker.Frozen = prev.Frozen
		worker.Connections = prev.Connections
	case err != nil && !errors.Is(err, agentdb.ErrWorkerNotFound):
		writeWorkerErr(w, err)
		return
	}

	// Connections: validated and gated BEFORE any other field is applied, so
	// a refusal here never writes a partial change. Existence is checked only
	// for what the body actually sent — a name already stored (e.g. carried
	// over from before a connection was removed from the project map) is not
	// re-validated on a save that leaves it untouched.
	if body.Connections != nil {
		if h.cfg.ConnectionNames != nil {
			known := h.cfg.ConnectionNames(id.Customer)
			for _, name := range body.Connections {
				if name == connectionsWildcard {
					continue
				}
				if !contains(known, name) {
					http.Error(w, fmt.Sprintf("unknown connection %q", name), http.StatusBadRequest)
					return
				}
			}
		}
		// Only a CHANGE is gated — re-sending the stored list verbatim (a
		// worker-list UI that round-trips every field on save) must still
		// succeed for an API key. connectionsEqual is order-sensitive by
		// design, matching agentdb's own jsonValueEqual: a grant list is
		// operator-authored, not a set a client should be reordering.
		if !connectionsEqual(worker.Connections, body.Connections) && (id.APIKey || id.SessionScope != "" || id.DatasetScope != "") {
			http.Error(w, "connections may only be changed by a logged-in person", http.StatusForbidden)
			return
		}
		worker.Connections = body.Connections
	}

	if body.Description != nil {
		worker.Description = *body.Description
	}
	if body.SystemPrompt != nil {
		worker.SystemPrompt = *body.SystemPrompt
	}
	if body.MCPConfig != nil {
		worker.MCPConfig = body.MCPConfig
	}
	if body.Image != nil {
		worker.Image = *body.Image
	}
	if body.Briefing != nil {
		worker.Briefing = body.Briefing
	}
	if body.MaxInstances != nil {
		worker.MaxInstances = *body.MaxInstances
	}
	if body.Enabled != nil {
		worker.Enabled = *body.Enabled
	}
	if body.Frozen != nil {
		worker.Frozen = *body.Frozen
	}

	stored, err := store.UpsertWorker(r.Context(), worker, humanEditBecause(body.Rationale))
	if err != nil {
		writeWorkerErr(w, err)
		return
	}
	writeJSON(w, stored)
}

// DeleteWorker removes a worker from the caller's project. A DELETE has no
// body, so its reason rides `?rationale=`.
func (h *Handlers) DeleteWorker(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	store := h.workers(w)
	if store == nil {
		return
	}
	if err := store.DeleteWorker(r.Context(), id.Customer, r.PathValue("name"), humanEditBecause(rationaleParam(r))); err != nil {
		writeWorkerErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeWorkerErr maps store errors onto status codes: missing rows are 404,
// validation failures are 400, everything else is 500.
func writeWorkerErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agentdb.ErrWorkerNotFound):
		http.Error(w, "worker not found", http.StatusNotFound)
	case errors.Is(err, agentdb.ErrWorkerInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
