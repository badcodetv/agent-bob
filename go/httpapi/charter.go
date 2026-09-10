package httpapi

// charter.go — the two routes onboarding needs (T11 of
// design/2026-09-08-memory-coordinated-organisation.md):
//
//	GET  /agent/charter/current?session=<id>  — read the deposited charter
//	POST /agent/charter/apply                 — approve it, once, atomically
//
// The rule that shapes both: the charter is read from the STORE, never from
// the request body. The interviewer runs inside a container, and a container
// is the untrusted party (§6.2.4) — so the console shows what was deposited,
// the human approves what the console showed, and apply re-reads the same row
// and re-validates it. A body-supplied charter would let anything holding a
// project credential apply a charter no interview ever produced and no human
// ever read.
//
// Apply is one ApplyTopology transaction plus one follow-up write. The
// follow-up — disabling the interviewer (Decision A8) — is deliberately
// OUTSIDE the transaction: it is not part of what the charter says, and a
// project whose charter applied but whose interviewer is still enabled is
// merely untidy, whereas a rollback of the whole charter because a cleanup
// write failed would be a much worse outcome.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/charter"
	"github.com/badcodetv/agent-bob/topology"
)

// charterMissing is the 404 body. It is a sentence rather than a code because
// the console renders it directly, and because "not found" on this route means
// something specific and recoverable: the interview has not finished yet.
const charterMissing = "no charter has been proposed yet — the interview has to deposit an org-charter memory first"

// charterResp is what GET /agent/charter/current answers. It carries the
// parsed charter, the verdict, and — only when valid — a summary of what
// approving would do. Never the resolved bundle: see charter.Summarise.
type charterResp struct {
	Charter          *charter.Charter `json:"charter"`
	Summary          string           `json:"summary"`
	MemoryID         string           `json:"memory_id"`
	CreatedAt        int64            `json:"created_at"`
	Valid            bool             `json:"valid"`
	Errors           []charter.Issue  `json:"errors,omitempty"`
	SummaryOfEffects *charter.Effects `json:"summary_of_effects,omitempty"`
}

// charterApplyBody is the approval. `session` and `memory_id` identify WHICH
// deposit is being approved — the interview may have revised it several times,
// and approving "the newest" without saying which one that was would mean the
// human can approve something they never saw.
type charterApplyBody struct {
	Session   string `json:"session"`
	MemoryID  string `json:"memory_id"`
	Rationale string `json:"rationale,omitempty"`
}

// charterSession validates the interview session id before it is spliced into
// a label selector. Without this a caller could smuggle a second term into the
// selector — a comma is the selector language's AND — and widen the read past
// the deposit it named. The memory routes apply the identical guard for the
// identical reason (memories.go:387, mcp_memory.go:384-389).
func charterSession(w http.ResponseWriter, raw string) (string, bool) {
	session := strings.TrimSpace(raw)
	if session == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return "", false
	}
	if err := agentdb.ValidateLabelValue(session); err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusBadRequest)
		return "", false
	}
	return session, true
}

// charterSelector is the label selector a deposit is found by: its kind, and
// the interview session's id as its name. `name=` is the newest-wins
// convention, so re-depositing simply supersedes (Decision A6).
func charterSelector(session string) string {
	return "kind=" + charter.MemoryKindCharter + ",name=" + session
}

// GetCurrentCharter reads the newest charter deposited by one interview
// session and reports whether it is fit to apply.
func (h *Handlers) GetCurrentCharter(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.memoryReadable(w, id) {
		return
	}
	session, ok := charterSession(w, r.URL.Query().Get("session"))
	if !ok {
		return
	}

	mem, err := h.cfg.Memories.NewestMemory(r.Context(), id.Customer, charterSelector(session))
	// A session in another project reaches the not-found branch, because
	// NewestMemory takes the project as an argument rather than as a filter
	// applied afterwards. So a wrong project is "no charter", never "there is
	// one but you may not see it" — no existence oracle. Anything else is a
	// 500: a database refusing connections is not a charter that is missing.
	if errors.Is(err, agentdb.ErrMemoryNotFound) {
		http.Error(w, charterMissing, http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := charterResp{MemoryID: mem.ID, CreatedAt: mem.CreatedAt}
	parsed, summary, err := charter.Parse(mem.Content)
	if err != nil {
		// A deposit that does not parse is a 200 with valid:false, not a 500.
		// The console's job here is to show the human why the interview has
		// not produced something approvable yet, and "the agent wrote
		// something malformed" is one of the answers to that.
		resp.Errors = []charter.Issue{{Path: "charter", Message: err.Error()}}
		writeJSON(w, resp)
		return
	}
	resp.Charter, resp.Summary = parsed, summary

	if issues := charter.Validate(parsed); len(issues) > 0 {
		resp.Errors = issues
		writeJSON(w, resp)
		return
	}
	bundle, err := charter.Resolve(parsed)
	if err != nil {
		resp.Errors = []charter.Issue{{Path: "charter", Message: err.Error()}}
		writeJSON(w, resp)
		return
	}
	resp.Valid = true
	resp.SummaryOfEffects = charter.Summarise(bundle)
	writeJSON(w, resp)
}

// ApplyCharter is the one approval (Decision A4). It re-reads the stored
// deposit, re-validates it, and writes the whole charter in one transaction.
func (h *Handlers) ApplyCharter(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if !h.memoryReadable(w, id) {
		return
	}
	store := h.topologies(w)
	if store == nil {
		return
	}
	var body charterApplyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	session, ok := charterSession(w, body.Session)
	if !ok {
		return
	}

	mem, err := h.charterMemory(r, id.Customer, session, strings.TrimSpace(body.MemoryID))
	if errors.Is(err, agentdb.ErrMemoryNotFound) {
		http.Error(w, charterMissing, http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	parsed, _, err := charter.Parse(mem.Content)
	if err != nil {
		writeCharterIssues(w, []charter.Issue{{Path: "charter", Message: err.Error()}})
		return
	}
	if issues := charter.Validate(parsed); len(issues) > 0 {
		writeCharterIssues(w, issues)
		return
	}
	bundle, err := charter.Resolve(parsed)
	if err != nil {
		writeCharterIssues(w, []charter.Issue{{Path: "charter", Message: err.Error()}})
		return
	}

	app := agentdb.TopologyApplication{
		Project: id.Customer,
		// Not a name@version: a charter is not a built-in topology, and
		// writing one here would make the changelog claim a catalogue entry
		// that does not exist. The apply's answers record the deposit it came
		// from, which is what "where did this organisation come from?" wants.
		Topology: "charter",
		Answers: agentdb.JSONMap{
			"session":   session,
			"memory_id": mem.ID,
		},
		SettingsPatch:  bundle.SettingsPatch,
		RequiredImages: bundle.Preconditions.Images,
		RequiredSkills: bundle.Preconditions.Skills,
	}
	appendBundle(&app, bundle)

	rationale := strings.TrimSpace(body.Rationale)
	if rationale == "" {
		rationale = "approved from the onboarding charter deposited in session " + session
	}
	result, err := store.ApplyTopology(r.Context(), app, humanEditBecause(rationale))
	if err != nil {
		writeTopologyApplyErr(w, err)
		return
	}

	h.disableInterviewer(r, id.Customer, session)
	writeJSON(w, result)
}

// charterMemory resolves which deposit is being approved: the one the console
// showed, by id, if the caller named it, and otherwise the newest.
//
// A named id is still checked against the selector rather than fetched blind.
// Without that check the route would apply any memory in the project whose
// content happened to parse as a charter — including one a worker wrote itself.
func (h *Handlers) charterMemory(r *http.Request, project, session, memoryID string) (*agentdb.Memory, error) {
	if memoryID == "" {
		return h.cfg.Memories.NewestMemory(r.Context(), project, charterSelector(session))
	}
	mem, err := h.cfg.Memories.GetMemory(r.Context(), project, memoryID)
	if err != nil {
		return nil, err
	}
	if mem == nil ||
		mem.Labels["kind"] != charter.MemoryKindCharter ||
		mem.Labels["name"] != session {
		return nil, agentdb.ErrMemoryNotFound
	}
	return mem, nil
}

// appendBundle copies the bundle's rows onto the application. The loops take
// addresses of the slice elements, exactly as ApplyTopologyHandler does.
func appendBundle(app *agentdb.TopologyApplication, b *topology.Bundle) {
	for i := range b.Workers {
		app.Workers = append(app.Workers, &b.Workers[i])
	}
	for i := range b.Subscriptions {
		app.Subscriptions = append(app.Subscriptions, &b.Subscriptions[i])
	}
	for i := range b.Schedules {
		app.Schedules = append(app.Schedules, &b.Schedules[i])
	}
	for i := range b.MemorySeeds {
		app.MemorySeeds = append(app.MemorySeeds, &b.MemorySeeds[i])
	}
}

// disableInterviewer is Decision A8. Left enabled, the interviewer persists as
// a worker with no subscription and no schedule — and the architect's first
// reconciliation pass would find an orphan and be free to rewrite or delete
// it. Disabling rather than deleting keeps the charter's provenance (its
// memory names the interview session) pointing at something that still exists,
// and re-entering onboarding re-enables it.
//
// Failure is deliberately silent. It happens after the charter has already
// committed; reporting it as an error on this route would tell the operator
// the approval failed when it did not.
func (h *Handlers) disableInterviewer(r *http.Request, project, session string) {
	if h.cfg.Workers == nil {
		return
	}
	worker, err := h.cfg.Workers.GetWorker(r.Context(), project, topology.OnboardingWorker)
	if err != nil || worker == nil || !worker.Enabled {
		return
	}
	// Read-modify-write: UpsertWorker is a whole-object replace, so the stored
	// row is the base and only Enabled moves.
	worker.Enabled = false
	_, _ = h.cfg.Workers.UpsertWorker(r.Context(), worker,
		humanEditBecause("the onboarding charter from session "+session+" was approved; the interview is over"))
}

// writeCharterIssues answers 422 with every problem at once — the same list
// charter_validate returns, so a charter the interviewer was told was invalid
// is refused here for exactly the same reasons (Decision A5).
func writeCharterIssues(w http.ResponseWriter, issues []charter.Issue) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": issues})
}
