package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
)

// WorkersStore is the worker-catalogue seam the CRUD handlers need.
// *agentdb.Store implements it; hosts may substitute their own.
type WorkersStore interface {
	ListWorkers(ctx context.Context, project string) ([]*agentdb.Worker, error)
	GetWorker(ctx context.Context, project, name string) (*agentdb.Worker, error)
	UpsertWorker(ctx context.Context, w *agentdb.Worker, cw agentdb.ConfigWrite) (*agentdb.Worker, error)
	DeleteWorker(ctx context.Context, project, name string, cw agentdb.ConfigWrite) error
}

// workerBody is the PUT payload, and it has TWO absent-field rules. Which one
// applies to a field is stated on the field.
//
// Description, SystemPrompt, MCPConfig, Image and Briefing are KEEP-ON-ABSENT
// (T27, from DI2): leaving one out changes nothing about it. They used to be
// replace-on-absent like the rest, which meant a caller sending one field
// erased the other four without saying so.
//
// MaxInstances, Enabled and Frozen remain REPLACE-ON-ABSENT: leaving one out
// writes this route's default (1, true, false). They are pointers so that a
// meaningful zero value (0, false) is distinguishable from "not supplied".
// TestWorkersHTTP_FreezeAndUnfreezeRoundTrip pins that directly. The split is
// not satisfying, and DI11 records it as a decision still to be taken rather
// than as a thing anyone intended.
//
// Frozen rides THIS route deliberately: the JWT-guarded HTTP API is the human
// path, so freeze and unfreeze are one more field on the ordinary worker write
// (mirroring how Enabled is toggled) rather than a parallel endpoint. The core
// MCP server — the workers' path — never exposes the field at all.
type workerBody struct {
	// The five keep-on-absent fields (T27, from DI2). Absent or JSON `null`
	// keeps whatever the stored row holds; an explicit "", {} or [] clears it.
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
	MaxInstances *int                 `json:"max_instances"` // nil → 1
	Enabled      *bool                `json:"enabled"`       // nil → true
	Frozen       *bool                `json:"frozen"`        // nil → false
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

	// Seed the five keep-on-absent fields from the stored row (T27, from DI2).
	// Before this, the handler assigned all five from the body unconditionally,
	// so installing a new prompt with {"system_prompt": …} wrote the other four
	// as their zero values — silently, with a 200 and a read-back echo that
	// looked correct because it echoed what had just been stored. That is how
	// the architect lost its briefing during T1.
	//
	// A store error that is NOT "no such row" fails the request rather than
	// falling through to the defaults: falling through would reintroduce the
	// exact wipe this guards against, and do it only intermittently.
	//
	// MaxInstances, Enabled and Frozen are deliberately NOT seeded here. They
	// keep this route's older replace semantics, pinned with a reason by
	// TestWorkersHTTP_FreezeAndUnfreezeRoundTrip ("an omitted frozen must read
	// as false"). Unifying the two halves is a product decision about what
	// freeze means, not a bug fix — see DI11.
	switch prev, err := store.GetWorker(r.Context(), id.Customer, name); {
	case err == nil && prev != nil:
		worker.Description = prev.Description
		worker.SystemPrompt = prev.SystemPrompt
		worker.MCPConfig = prev.MCPConfig
		worker.Image = prev.Image
		worker.Briefing = prev.Briefing
	case err != nil && !errors.Is(err, agentdb.ErrWorkerNotFound):
		writeWorkerErr(w, err)
		return
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
