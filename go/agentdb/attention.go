package agentdb

// attention.go — the durable half of `request_human_attention`
// (spec §9, docs/product/05-management-tools.md; §8.2 for the timeout event).
//
// The tool itself is "deliberately almost nothing": post `{message,
// session_url}` to the project's attention channel, stamp the session, echo the
// permalink, end the turn. Two things nevertheless have to survive a process
// restart, and they are what this file stores:
//
//	agent_sessions.attention_requested  — the stamp §9 puts on the session, and
//	                                      the flag §8.2 copies onto the
//	                                      `worker.finished` envelope so reviewers
//	                                      can skip deliberately half-done work.
//	attention_requests                  — one row per call, carrying the optional
//	                                      `expires_in` deadline. The sweep reads
//	                                      it, and a request that lapses unanswered
//	                                      becomes a `human.attention.timeout`
//	                                      event (§8.2) so the *worker's prompt*
//	                                      decides the fallback.
//
// This is runtime state, not project configuration: §15.3's closed vocabulary
// has no verb for it and nothing here is a setting, so — like event_deliveries —
// it writes no config event (§15.3 rule 3).
//
// What is NOT here, on purpose: any approval state machine, draft queue or
// pending-items projection. The thread itself is the review surface (§9); a
// request is answered by a human simply typing the next message.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EventTypeHumanAttentionTimeout is emitted by the attention sweep when a
// request made with `expires_in` lapses unanswered (§8.2). Its envelope is
// {source: "core", depth: 0} plus the worker and session of the paused job.
const EventTypeHumanAttentionTimeout = "human.attention.timeout"

// The two kinds of request (migration 051).
//
// An ASK is a question: a person owes the worker an answer, the job parks at
// `awaiting_human` until one arrives, and the Desk lists it under Asks.
//
// A NOTICE is act-then-notify: the worker has already done the thing and is
// telling a person so. Nobody owes it an answer, so it never parks a job, it
// never times out into a `human.attention.timeout` wake-up, and the Desk shows
// it as a note a person acknowledges rather than a question left unanswered.
const (
	AttentionKindAsk    = "ask"
	AttentionKindNotice = "notice"
)

// ErrAttentionRequestNotFound is returned when no request matches.
var ErrAttentionRequestNotFound = errors.New("attention request not found")

// AttentionRequest is one `request_human_attention` call (§9).
//
// AnsweredAt and TimedOutAt are terminal and mutually exclusive: a request is
// open while both are 0. ExpiresAt 0 means "no deadline" — the majority case,
// since `expires_in` is optional and a request without one simply waits.
type AttentionRequest struct {
	ID        string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Project   string `json:"project" gorm:"type:varchar(255);not null;index:idx_attention_requests_project"`
	SessionID string `json:"session_id" gorm:"type:varchar(36);not null;index:idx_attention_requests_session"`
	Worker    string `json:"worker" gorm:"type:varchar(255)"`
	// Message is what the worker asked the human for, verbatim.
	Message string `json:"message" gorm:"type:text"`
	// Kind is AttentionKindAsk or AttentionKindNotice. Rows written before
	// migration 051 read as asks, which is what they were treated as.
	Kind string `json:"kind" gorm:"type:varchar(16);not null;default:ask"`
	// SessionURL is the permalink minted at request time (§9, F3). Stored rather
	// than recomputed so a later change of AGENTKIT_PUBLIC_BASE_URL cannot
	// rewrite history.
	SessionURL string `json:"session_url" gorm:"type:text"`
	// Channel records where the notification actually went: "webhook" when a
	// channel was configured and posted to, "none" for the log-only fallback.
	Channel string `json:"channel" gorm:"type:varchar(30)"`
	// Delivered is false when the channel was unset (log-only) or the post
	// failed — the tool still succeeds either way (§9), but the record says so.
	Delivered  bool  `json:"delivered"`
	ExpiresAt  int64 `json:"expires_at"` // unix seconds; 0 = never expires
	CreatedAt  int64 `json:"created_at" gorm:"autoCreateTime"`
	AnsweredAt int64 `json:"answered_at"`
	TimedOutAt int64 `json:"timed_out_at"`
}

func (AttentionRequest) TableName() string { return "attention_requests" }

// CreateAttentionRequest records one call and stamps the session in the same
// breath, so a crash can never leave a stamped session with no record or a
// record with an unstamped session.
func (s *Store) CreateAttentionRequest(ctx context.Context, req *AttentionRequest) (*AttentionRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("attention request is required")
	}
	if strings.TrimSpace(req.Project) == "" {
		return nil, fmt.Errorf("project is required")
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(req.Message) == "" {
		return nil, fmt.Errorf("message is required (a human needs to know what you need)")
	}
	if req.ExpiresAt < 0 {
		return nil, fmt.Errorf("expires_at must not be negative")
	}
	switch req.Kind {
	case "":
		req.Kind = AttentionKindAsk
	case AttentionKindAsk:
	case AttentionKindNotice:
		// A notice waits on nobody, so there is nothing for a deadline to lapse.
		req.ExpiresAt = 0
	default:
		return nil, fmt.Errorf("kind must be %q or %q, not %q", AttentionKindAsk, AttentionKindNotice, req.Kind)
	}
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(req).Error; err != nil {
			return err
		}
		return tx.Model(&Session{}).
			Where("id = ? AND customer = ?", req.SessionID, req.Project).
			Update("attention_requested", true).Error
	}); err != nil {
		return nil, fmt.Errorf("failed to create attention request: %w", err)
	}
	return req, nil
}

// GetAttentionRequest reads one request within a project. Another project's row
// looks like a missing row.
func (s *Store) GetAttentionRequest(ctx context.Context, project, id string) (*AttentionRequest, error) {
	if project == "" || id == "" {
		return nil, fmt.Errorf("project and id are required")
	}
	var req AttentionRequest
	err := s.gdb.WithContext(ctx).Where("project = ? AND id = ?", project, id).First(&req).Error
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("%w: %s/%s", ErrAttentionRequestNotFound, project, id)
		}
		return nil, fmt.Errorf("failed to get attention request: %w", err)
	}
	return &req, nil
}

// AttentionRequestQuery is the read filter behind GET /agent/attention-requests.
// It is deliberately tiny: a request is either open or it isn't, and the console
// only ever asks for one project's rows.
type AttentionRequestQuery struct {
	Project string
	// IncludeResolved widens the read to answered and timed-out rows too — the
	// route's ?state=all. Open-only (both stamps 0) is the default because that
	// is what the Desk asks for.
	IncludeResolved bool
	// Limit caps the page; 0 means unlimited, matching ListOpenAttentionRequests'
	// original behaviour (a project's open requests are few by construction).
	Limit int
}

// ListAttentionRequests returns a project's requests newest-first, open-only
// unless the query says otherwise.
func (s *Store) ListAttentionRequests(ctx context.Context, q AttentionRequestQuery) ([]*AttentionRequest, error) {
	if q.Project == "" {
		return nil, fmt.Errorf("project is required")
	}
	out := []*AttentionRequest{}
	tx := s.gdb.WithContext(ctx).Model(&AttentionRequest{}).Where("project = ?", q.Project)
	if !q.IncludeResolved {
		tx = tx.Where("answered_at = 0 AND timed_out_at = 0")
	}
	if q.Limit > 0 {
		tx = tx.Limit(clampLimit(q.Limit))
	}
	if err := tx.Order("created_at DESC, id DESC").Find(&out).Error; err != nil {
		return nil, fmt.Errorf("failed to list attention requests: %w", err)
	}
	return out, nil
}

// ListOpenAttentionRequests returns the project's unresolved requests,
// newest-first.
func (s *Store) ListOpenAttentionRequests(ctx context.Context, project string) ([]*AttentionRequest, error) {
	return s.ListAttentionRequests(ctx, AttentionRequestQuery{Project: project})
}

// ListExpiredAttentionRequests returns every open request whose deadline has
// passed, across every project — the sweep's poll. Deliberately unscoped for the
// same reason as the scheduler's poll: the sweep is core, not a tenant.
//
// Requests with no deadline (expires_at = 0) are never returned: `expires_in` is
// optional, and a request without one waits indefinitely by design (§9).
func (s *Store) ListExpiredAttentionRequests(ctx context.Context, now int64, limit int) ([]*AttentionRequest, error) {
	out := []*AttentionRequest{}
	if err := s.gdb.WithContext(ctx).Model(&AttentionRequest{}).
		Where("expires_at > 0 AND expires_at <= ? AND kind <> ? AND answered_at = 0 AND timed_out_at = 0", now, AttentionKindNotice).
		Order("expires_at ASC, id ASC").Limit(clampLimit(limit)).Find(&out).Error; err != nil {
		return nil, fmt.Errorf("failed to list expired attention requests: %w", err)
	}
	return out, nil
}

// resolveAttention closes a request and clears the session stamp in one
// transaction. column is answered_at or timed_out_at.
func (s *Store) resolveAttention(ctx context.Context, id, column string, at int64) error {
	if id == "" {
		return fmt.Errorf("attention request id is required")
	}
	return s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var req AttentionRequest
		if err := tx.Where("id = ?", id).First(&req).Error; err != nil {
			if isNotFound(err) {
				return fmt.Errorf("%w: %s", ErrAttentionRequestNotFound, id)
			}
			return err
		}
		return resolveAttentionTx(tx, &req, column, at)
	})
}

// resolveAttentionTx is resolveAttention's body, for callers that already hold
// the row and a transaction.
//
// When an ANSWER closes the last open ask on a session, the deliveries parked
// on that session leave `awaiting_human` for `ok`. That is the close the
// parked-row wart was missing (docs/18 §9): the human's reply resumed the
// session, but nothing told the job-history row, so it stayed parked with no
// `ended_at` forever. A timeout does not settle the row — the timeout event
// wakes the worker, and whatever it does next is its own job.
func resolveAttentionTx(tx *gorm.DB, req *AttentionRequest, column string, at int64) error {
	if req.AnsweredAt != 0 || req.TimedOutAt != 0 {
		// Already resolved: the sweep is at-least-once, so a second pass over
		// the same row must be a no-op rather than a second timeout event.
		return nil
	}
	if err := tx.Model(&AttentionRequest{}).Where("id = ?", req.ID).Update(column, at).Error; err != nil {
		return err
	}
	if column == "answered_at" {
		req.AnsweredAt = at
	} else {
		req.TimedOutAt = at
	}
	// The stamp is per-request: with this one closed the session is no longer
	// waiting on a human, unless another open request says otherwise.
	var open int64
	if err := tx.Model(&AttentionRequest{}).
		Where("session_id = ? AND id <> ? AND answered_at = 0 AND timed_out_at = 0", req.SessionID, req.ID).
		Count(&open).Error; err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	if err := tx.Model(&Session{}).Where("id = ?", req.SessionID).
		Update("attention_requested", false).Error; err != nil {
		return err
	}
	if column != "answered_at" {
		return nil
	}
	return settleParkedDeliveriesTx(tx, req.Project, req.SessionID, at)
}

// settleParkedDeliveriesTx moves a session's `awaiting_human` deliveries to
// `ok`, stamping `ended_at`. Runtime state, like every delivery write — no
// config event (§15.3 rule 3).
func settleParkedDeliveriesTx(tx *gorm.DB, project, sessionID string, at int64) error {
	if project == "" || sessionID == "" {
		return nil
	}
	return tx.Model(&EventDelivery{}).
		Where("project = ? AND session_id = ? AND status = ?", project, sessionID, DeliveryAwaitingHuman).
		Updates(map[string]any{"status": DeliveryOK, "ended_at": at, "failure_reason": ""}).Error
}

// AnswerSessionAttention closes every open request on a session because a
// person has just replied in it — §9's "a reply IS the answer", applied at the
// moment the reply arrives rather than only when a deadline makes the sweep
// look. Without it a request made with no `expires_in` (the common case) was
// never closed by anything, and its job sat parked forever.
//
// Notices on the session close too: someone reading and replying to the thread
// has read what it said. Returns how many requests it closed. Idempotent.
func (s *Store) AnswerSessionAttention(ctx context.Context, project, sessionID string, at int64) (int, error) {
	if project == "" || sessionID == "" {
		return 0, fmt.Errorf("project and session id are required")
	}
	closed := 0
	err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var open []*AttentionRequest
		if err := tx.Where("project = ? AND session_id = ? AND answered_at = 0 AND timed_out_at = 0", project, sessionID).
			Order("created_at ASC, id ASC").Find(&open).Error; err != nil {
			return err
		}
		for _, req := range open {
			if err := resolveAttentionTx(tx, req, "answered_at", at); err != nil {
				return err
			}
			closed++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("failed to answer session attention: %w", err)
	}
	return closed, nil
}

// AcknowledgeAttentionRequest closes one request because a person said so on
// the Desk: "Got it" on a notice, or "Dismiss" on an ask they have dealt with
// some other way. It is recorded as answered — the same terminal state a reply
// produces — and settles the session's parked deliveries when it was the last
// open request there. Tenancy-scoped: another project's row is not found.
// Returns the row as it now stands; acknowledging a resolved row is a no-op.
func (s *Store) AcknowledgeAttentionRequest(ctx context.Context, project, id string, at int64) (*AttentionRequest, error) {
	if project == "" || id == "" {
		return nil, fmt.Errorf("project and id are required")
	}
	var req AttentionRequest
	err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project = ? AND id = ?", project, id).First(&req).Error; err != nil {
			if isNotFound(err) {
				return fmt.Errorf("%w: %s/%s", ErrAttentionRequestNotFound, project, id)
			}
			return err
		}
		return resolveAttentionTx(tx, &req, "answered_at", at)
	})
	if err != nil {
		if errors.Is(err, ErrAttentionRequestNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to acknowledge attention request: %w", err)
	}
	return &req, nil
}

// MarkAttentionAnswered closes a request because a human replied. Idempotent:
// re-resolving an already-resolved request changes nothing.
func (s *Store) MarkAttentionAnswered(ctx context.Context, id string, at int64) error {
	if err := s.resolveAttention(ctx, id, "answered_at", at); err != nil {
		return fmt.Errorf("failed to mark attention answered: %w", err)
	}
	return nil
}

// MarkAttentionTimedOut closes a request because its deadline passed unanswered.
// Idempotent, which is what stops the sweep emitting a second
// `human.attention.timeout` for the same request after a crash.
func (s *Store) MarkAttentionTimedOut(ctx context.Context, id string, at int64) error {
	if err := s.resolveAttention(ctx, id, "timed_out_at", at); err != nil {
		return fmt.Errorf("failed to mark attention timed out: %w", err)
	}
	return nil
}

// CountUserMessagesSince counts the human turns a session received at or after
// `since` (unix seconds). It is how the sweep tells "answered" from "lapsed":
// §9 has no approval state machine, so a reply IS the answer — whatever the
// human typed.
func (s *Store) CountUserMessagesSince(ctx context.Context, sessionID string, since int64) (int64, error) {
	if sessionID == "" {
		return 0, fmt.Errorf("session id is required")
	}
	var n int64
	if err := s.gdb.WithContext(ctx).Model(&Message{}).
		Where("session_id = ? AND role = ? AND created_at >= ?", sessionID, "user", since).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("failed to count user messages: %w", err)
	}
	return n, nil
}

// SessionAwaitsHuman reports whether a session still has an open attention
// request — i.e. whether a human owes it an answer.
//
// This is DELIBERATELY not `agent_sessions.attention_requested`. That column is
// the per-TURN carrier §8.2 copies onto the `worker.finished` envelope and the
// emitter clears the moment it has copied it, so by the time a job settles it
// says nothing about whether anyone replied. The open rows in
// `attention_requests` are the durable fact, and they are what the dispatch gate
// parks a delivery at `awaiting_human` on (§8.4).
//
// No approval state: "open" means created and neither answered nor timed out,
// and a human simply typing the next message is what closes it (§9).
//
// Notices do not count: a worker that has told a person what it did is not
// waiting on them, so its job ends `ok` rather than parking.
func (s *Store) SessionAwaitsHuman(ctx context.Context, project, sessionID string) (bool, error) {
	if project == "" || sessionID == "" {
		return false, fmt.Errorf("project and session id are required")
	}
	var n int64
	if err := s.gdb.WithContext(ctx).Model(&AttentionRequest{}).
		Where("project = ? AND session_id = ? AND kind <> ? AND answered_at = 0 AND timed_out_at = 0",
			project, sessionID, AttentionKindNotice).
		Count(&n).Error; err != nil {
		return false, fmt.Errorf("failed to count open attention requests: %w", err)
	}
	return n > 0, nil
}

// SetSessionAttentionRequested writes the session stamp directly. The tool path
// does not need it (CreateAttentionRequest stamps transactionally) — it exists
// for E2's emitter, which clears the per-turn flag after copying it onto the
// `worker.finished` envelope (§8.2).
func (s *Store) SetSessionAttentionRequested(ctx context.Context, sessionID string, requested bool) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	res := s.gdb.WithContext(ctx).Model(&Session{}).
		Where("id = ?", sessionID).Update("attention_requested", requested)
	if res.Error != nil {
		return fmt.Errorf("failed to stamp session attention: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}
