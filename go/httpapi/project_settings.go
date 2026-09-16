package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/badcodetv/agent-bob/agentdb"
)

// ProjectSettingsStore is the store seam behind GET/PUT /agent/project-settings
// (docs/product/01-session-config.md §5). *agentdb.Store satisfies it; Config
// wires it from AgentDB automatically, and tests substitute a fake.
type ProjectSettingsStore interface {
	GetProjectSettings(ctx context.Context, project string) (*agentdb.ProjectSettings, error)
	PutProjectSettings(ctx context.Context, ps *agentdb.ProjectSettings, cw agentdb.ConfigWrite) (*agentdb.ProjectSettings, error)
}

// projectScope resolves the project these routes act on. It is ALWAYS the
// authenticated principal's customer claim — never a path, query, or body value
// — which is what keeps one project's settings unreachable from another's token.
// It also resolves the store, writing 501 when none is configured.
//
// It returns the whole Identity, not just the project string, because
// PutProjectSettings also needs Identity.Operator (§1.2) to decide whether a
// budget/cap change may proceed.
func (h *Handlers) projectScope(w http.ResponseWriter, r *http.Request) (ProjectSettingsStore, Identity, bool) {
	id, ok := h.identify(w, r)
	if !ok {
		return nil, Identity{}, false
	}
	if h.cfg.ProjectSettings == nil {
		http.Error(w, "project settings not configured", http.StatusNotImplemented)
		return nil, Identity{}, false
	}
	if id.Customer == "" {
		http.Error(w, "project scope required", http.StatusBadRequest)
		return nil, Identity{}, false
	}
	return h.cfg.ProjectSettings, id, true
}

// GetProjectSettings returns the calling project's settings, or the spec
// defaults when nothing has been written yet (the row is created lazily).
func (h *Handlers) GetProjectSettings(w http.ResponseWriter, r *http.Request) {
	store, id, ok := h.projectScope(w, r)
	if !ok {
		return
	}
	ps, err := store.GetProjectSettings(r.Context(), id.Customer)
	if err != nil {
		writeProjectSettingsError(w, err)
		return
	}
	writeJSON(w, ps)
}

// projectSettingsBody is the settings row plus the operator's optional reason
// (design B3). Embedded rather than listed field-by-field so the settings shape
// stays defined in exactly one place: encoding/json promotes the embedded
// struct's fields, so the wire shape is the row's fields plus `rationale`.
type projectSettingsBody struct {
	agentdb.ProjectSettings
	Rationale string `json:"rationale"`
}

// operatorOnlyMessage is the exact 403 body a non-operator gets for touching a
// budget/cap field (onboarding-work-plan §1.2). Pinned by test.
const operatorOnlyMessage = "only the operator may change budgets and caps"

// PutProjectSettings writes the whole settings object (§5: no patch semantics).
// A `project` field in the body is ignored: the JWT decides the project.
//
// §1.2: a non-operator identity may PUT freely EXCEPT for the three
// budget/cap fields — daily_tokens_soft, daily_tokens_hard,
// max_concurrent_jobs. Because the route has no patch semantics, "may not
// change" is checked against what is already stored (or, for a brand-new
// project, the defaults GetProjectSettings would answer): a non-operator PUT
// must echo those three fields back unchanged, the same way it must echo back
// every other field it isn't editing.
func (h *Handlers) PutProjectSettings(w http.ResponseWriter, r *http.Request) {
	store, id, ok := h.projectScope(w, r)
	if !ok {
		return
	}
	var wire projectSettingsBody
	if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body := wire.ProjectSettings
	body.Project = id.Customer // identity wins for tenancy, as on every other write
	body.UpdatedAt = 0         // stamped by the store, never by the caller

	if !id.Operator {
		current, err := store.GetProjectSettings(r.Context(), id.Customer)
		if err != nil {
			writeProjectSettingsError(w, err)
			return
		}
		if body.DailyTokensSoft != current.DailyTokensSoft ||
			body.DailyTokensHard != current.DailyTokensHard ||
			body.MaxConcurrentJobs != current.MaxConcurrentJobs {
			http.Error(w, operatorOnlyMessage, http.StatusForbidden)
			return
		}
	}

	ps, err := store.PutProjectSettings(r.Context(), &body, humanEditBecause(wire.Rationale))
	if err != nil {
		writeProjectSettingsError(w, err)
		return
	}
	writeJSON(w, ps)
}

// writeProjectSettingsError maps caller mistakes to 400 and everything else to 500.
func writeProjectSettingsError(w http.ResponseWriter, err error) {
	if errors.Is(err, agentdb.ErrInvalidProjectSettings) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
