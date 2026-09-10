package httpapi

// The config-log read route (spec §15.10, docs/product/09-config-log.md).
//
//	GET /agent/config-events
//	  query: ?action=<exact|prefix*>&actor_worker=<name>&since=<ms>&until=<ms>
//	         &limit=<n>&before_seq=<seq>&entity=<ref>&seq=<seq>
//	  auth : the ordinary session JWT; the project comes from the Customer claim,
//	         never from the query (P5) — same posture as GET /agent/events.
//	  200  : {"config_events": [ConfigEvent, …]}   // newest-first
//
//	GET /agent/config-events/{id}
//	  200  : one ConfigEvent
//	  404  : no such record IN THIS PROJECT — a record belonging to another
//	         project answers identically, so an id cannot be probed for
//	         existence.
//
// `entity` and `seq` exist so a changelog entry is ADDRESSABLE. The store has
// supported the entity filter since J2 but nothing exposed it over HTTP, so the
// browser could ask "what changed" and never "what happened to this worker" —
// and reverting a change (T22) starts by fetching the chain for one entity.
//
//	POST /agent/config-events/{id}/revert   {rationale}
//	  200  : the ConfigEvent the compensating change appended
//	  409  : the target is not the newest change to its entity — naming what
//	         came after it — or its kind has no inverse
//	  404  : no such record in this project
//
// The log itself stays read-only, and deliberately so: a config event exists
// only as the shadow of a real configuration mutation (§15.4). The revert route
// is not an exception — it performs an ordinary worker / subscription /
// schedule / settings write, and the record it returns is that write's own
// shadow. Nothing here writes a config event directly, which would be forging
// history.
//
// There is deliberately NO MCP equivalent (Decision D4): giving revert to the
// architect would let it undo the human who had just corrected it.
//
// Ordering is by `seq`, not by `created_at` (J2): `created_at` is a millisecond
// wall clock and the id is a random uuid, so two writes inside one millisecond
// have no total order. `seq` is allocated inside the config-event transaction,
// so seq order IS commit order. That is why the page cursor is `before_seq` and
// not a timestamp — `since`/`until` still filter on the clock, because a human
// asking "what changed on Tuesday" means the clock.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/badcodetv/agent-bob/agentdb"
)

// ConfigLogStore is the slice of agentdb.Store this route needs. It exists so a
// host can supply its own implementation and so the handler can be tested
// without a database; *agentdb.Store satisfies it.
type ConfigLogStore interface {
	ListConfigEvents(ctx context.Context, q agentdb.ConfigEventQuery) ([]*agentdb.ConfigEvent, error)
	GetConfigEvent(ctx context.Context, project, id string) (*agentdb.ConfigEvent, error)
	// RevertEvent is the ONE write reachable from this file, and it is not a
	// write to the log: it performs the compensating configuration change, and
	// the log records that change like any other. Nothing here can forge
	// history.
	RevertEvent(ctx context.Context, project, eventID string, cw agentdb.ConfigWrite) (*agentdb.ConfigEvent, error)
}

// The concrete store must always satisfy the seam.
var _ ConfigLogStore = (*agentdb.Store)(nil)

// ListConfigEvents serves GET /agent/config-events — the changelog's read path.
func (h *Handlers) ListConfigEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.ConfigLog == nil {
		http.Error(w, "the config log is not configured on this host", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "no project in token", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	events, err := h.cfg.ConfigLog.ListConfigEvents(r.Context(), agentdb.ConfigEventQuery{
		Project:     id.Customer,
		Action:      q.Get("action"),
		ActorWorker: q.Get("actor_worker"),
		Entity:      q.Get("entity"),
		Since:       queryInt64(r, "since"),
		Until:       queryInt64(r, "until"),
		BeforeSeq:   queryInt64(r, "before_seq"),
		Seq:         queryInt64(r, "seq"),
		Limit:       queryInt(r, "limit", 0),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if events == nil {
		events = []*agentdb.ConfigEvent{}
	}
	writeJSON(w, map[string]any{"config_events": events})
}

// GetConfigEvent serves GET /agent/config-events/{id} — one record, by id.
//
// It exists because the changelog's revert action (T23) has to name the event
// it is reverting, and a UI that could only page through a list would be
// reverting whatever happened to be at a position.
func (h *Handlers) GetConfigEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.ConfigLog == nil {
		http.Error(w, "the config log is not configured on this host", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "no project in token", http.StatusForbidden)
		return
	}
	ev, err := h.cfg.ConfigLog.GetConfigEvent(r.Context(), id.Customer, r.PathValue("id"))
	if err != nil {
		// A record in another project is reported as not-found, never as
		// forbidden: the store already answers that way, and the handler must
		// not turn it back into an existence oracle.
		if errors.Is(err, agentdb.ErrConfigEventNotFound) {
			http.Error(w, "no such config event in this project", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ev)
}

// revertBody is the human's reason. It is not required by the route — the store
// fills in "revert of <action> (seq N, event <id>)" when it is blank, which is
// more use in a changelog than an empty cell — but the console asks for one.
type revertBody struct {
	Rationale string `json:"rationale"`
}

// RevertConfigEvent serves POST /agent/config-events/{id}/revert.
//
// A FROZEN worker can be reverted through this route, and that is deliberate:
// the freeze blocks agents, not people (the `Frozen` comment in
// agentdb/workers.go), and this route is the human path. The store's ordinary
// mutations enforce whatever else applies.
func (h *Handlers) RevertConfigEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.ConfigLog == nil {
		http.Error(w, "the config log is not configured on this host", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "no project in token", http.StatusForbidden)
		return
	}
	// An absent or unparseable body is fine — the reason is optional here, and
	// refusing a revert over a missing JSON object would be pedantry.
	var body revertBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	written, err := h.cfg.ConfigLog.RevertEvent(r.Context(), id.Customer, r.PathValue("id"),
		humanEditBecause(body.Rationale))
	if err != nil {
		switch {
		case errors.Is(err, agentdb.ErrConfigEventNotFound):
			http.Error(w, "no such config event in this project", http.StatusNotFound)
		case errors.Is(err, agentdb.ErrRevertRefused):
			// The store's own sentence, verbatim: it names the intervening
			// records, and a paraphrase would drop exactly the part a human
			// needs in order to act.
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, written)
}
