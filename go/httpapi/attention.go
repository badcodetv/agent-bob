package httpapi

// The attention-request read route (operator console design B1, spec §9).
//
//	GET /agent/attention-requests
//	  query: ?state=open|all  (default open — answered_at = 0 AND timed_out_at = 0)
//	         ?limit=<n>
//	  auth : the ordinary session JWT; the project comes from the Customer claim,
//	         never from the query (P5) — same posture as GET /agent/config-events.
//	  200  : {"attention_requests": [AttentionRequest, …]}   // newest-first
//
//	POST /agent/attention-requests/{id}/resolve
//	  auth : the ordinary session JWT; the project comes from the Customer claim.
//	  200  : the AttentionRequest as it now stands (answered_at stamped)
//	  404  : no such request in this project
//
// A request is answered by a human typing the next message in the thread (§9)
// — SendMessage closes the session's open requests as the reply arrives — and
// timed out by the sweep. The resolve write is the Desk's "Got it" on a notice
// (nothing was asked, so there is no reply to type) and its "Dismiss" on an
// ask a person dealt with elsewhere. It records the same terminal state a
// reply does; it is not an approval, and nothing downstream waits on it.

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// AttentionStore is the slice of agentdb.Store this route needs. It exists so a
// host can supply its own implementation and so the handler can be tested
// without a database; *agentdb.Store satisfies it.
type AttentionStore interface {
	ListAttentionRequests(ctx context.Context, q agentdb.AttentionRequestQuery) ([]*agentdb.AttentionRequest, error)
}

// The concrete store must always satisfy the seam.
var _ AttentionStore = (*agentdb.Store)(nil)

// ListAttentionRequests serves GET /agent/attention-requests — the Desk's Asks
// stack, carrying the message the worker wrote.
func (h *Handlers) ListAttentionRequests(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.Attention == nil {
		http.Error(w, "attention requests are not configured on this host", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "no project in token", http.StatusForbidden)
		return
	}
	// Anything other than the two known words is the default rather than a 400:
	// a widened read is opt-in, so an unrecognised state can only ever narrow.
	all := r.URL.Query().Get("state") == "all"
	reqs, err := h.cfg.Attention.ListAttentionRequests(r.Context(), agentdb.AttentionRequestQuery{
		Project:         id.Customer,
		IncludeResolved: all,
		Limit:           queryInt(r, "limit", 0),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if reqs == nil {
		reqs = []*agentdb.AttentionRequest{}
	}
	writeJSON(w, map[string]any{"attention_requests": reqs})
}

// AttentionAnswerer is the optional write half of the seam. A host whose
// Attention store also implements it gets the resolve route and the
// reply-closes-the-request hook on SendMessage; one that does not keeps the
// read route and loses only those. *agentdb.Store satisfies it.
type AttentionAnswerer interface {
	AnswerSessionAttention(ctx context.Context, project, sessionID string, at int64) (int, error)
	AcknowledgeAttentionRequest(ctx context.Context, project, id string, at int64) (*agentdb.AttentionRequest, error)
}

var _ AttentionAnswerer = (*agentdb.Store)(nil)

func (h *Handlers) attentionAnswerer() AttentionAnswerer {
	if h.cfg.Attention == nil {
		return nil
	}
	a, _ := h.cfg.Attention.(AttentionAnswerer)
	return a
}

// ResolveAttentionRequest serves POST /agent/attention-requests/{id}/resolve.
func (h *Handlers) ResolveAttentionRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	answerer := h.attentionAnswerer()
	if answerer == nil {
		http.Error(w, "attention requests are not configured on this host", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "no project in token", http.StatusForbidden)
		return
	}
	reqID := strings.TrimSpace(r.PathValue("id"))
	if reqID == "" {
		http.Error(w, "attention request id is required", http.StatusBadRequest)
		return
	}
	req, err := answerer.AcknowledgeAttentionRequest(r.Context(), id.Customer, reqID, time.Now().Unix())
	if err != nil {
		if errors.Is(err, agentdb.ErrAttentionRequestNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, req)
}

// answerAttentionOnReply closes a session's open attention requests because a
// person has just sent it a message. Called BEFORE the message is handed to
// the runner: the turn the reply starts may itself ask again, and that new
// request must not be closed by the reply that preceded it.
//
// Best-effort. A failure here must never stop a person's message reaching the
// session; the worst it costs is an ask that lingers on the Desk.
func (h *Handlers) answerAttentionOnReply(ctx context.Context, project, sessionID string) {
	answerer := h.attentionAnswerer()
	if answerer == nil || project == "" || sessionID == "" {
		return
	}
	if _, err := answerer.AnswerSessionAttention(ctx, project, sessionID, time.Now().Unix()); err != nil {
		log.Printf("[httpapi] could not close attention requests on session %s: %v", sessionID, err)
	}
}
