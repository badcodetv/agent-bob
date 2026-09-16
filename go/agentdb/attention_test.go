package agentdb

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newAttentionStore returns a sqlite Store with the attention table plus the
// session and message tables the request path stamps and the sweep reads.
func newAttentionStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "attention_test.sqlite")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&AttentionRequest{}, &Session{}, &Message{}, &ProjectEvent{}, &EventDelivery{}); err != nil {
		t.Fatalf("automigrate attention tables: %v", err)
	}
	return &Store{gdb: db}
}

func seedAttentionSession(t *testing.T, s *Store, id, project string) *Session {
	t.Helper()
	sess := &Session{ID: id, Customer: project, UserEmail: "u@x.com", WorkflowID: "agent", Worker: "tweet-author"}
	if err := s.gdb.Create(sess).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess
}

func TestAttentionRequestStampsTheSession(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")

	req, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
		Project: "acme", SessionID: "s-1", Worker: "tweet-author",
		Message: "sign off on this draft", SessionURL: "http://localhost:8080/p/acme/s/s-1",
		Channel: "webhook", Delivered: true, ExpiresAt: 1000,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if req.ID == "" {
		t.Fatalf("create must allocate an id")
	}

	// The §9 stamp landed on the session in the same transaction.
	var sess Session
	if err := s.gdb.Where("id = ?", "s-1").First(&sess).Error; err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !sess.AttentionRequested {
		t.Fatalf("the session must be stamped attention_requested")
	}

	open, err := s.ListOpenAttentionRequests(ctx, "acme")
	if err != nil || len(open) != 1 {
		t.Fatalf("open list: %d rows err=%v", len(open), err)
	}

	// Resolving clears the stamp.
	if err := s.MarkAttentionAnswered(ctx, req.ID, 2000); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := s.gdb.Where("id = ?", "s-1").First(&sess).Error; err != nil {
		t.Fatalf("re-read session: %v", err)
	}
	if sess.AttentionRequested {
		t.Fatalf("answering must clear the session stamp")
	}
	open, _ = s.ListOpenAttentionRequests(ctx, "acme")
	if len(open) != 0 {
		t.Fatalf("an answered request is not open: %+v", open)
	}

	// Resolution is idempotent: a second sweep pass changes nothing.
	if err := s.MarkAttentionTimedOut(ctx, req.ID, 3000); err != nil {
		t.Fatalf("second resolution must be a no-op: %v", err)
	}
	got, err := s.GetAttentionRequest(ctx, "acme", req.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AnsweredAt != 2000 || got.TimedOutAt != 0 {
		t.Fatalf("a resolved request must not be re-resolved: %+v", got)
	}
}

func TestAttentionRequestValidation(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		req  *AttentionRequest
	}{
		{"nil", nil},
		{"no project", &AttentionRequest{SessionID: "s", Message: "m"}},
		{"no session", &AttentionRequest{Project: "acme", Message: "m"}},
		{"no message", &AttentionRequest{Project: "acme", SessionID: "s"}},
		{"negative expiry", &AttentionRequest{Project: "acme", SessionID: "s", Message: "m", ExpiresAt: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateAttentionRequest(ctx, tc.req); err == nil {
				t.Fatalf("expected a refusal")
			}
		})
	}
}

func TestAttentionExpiryListOnlyCoversDeadlinedRequests(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	seedAttentionSession(t, s, "s-2", "acme")
	seedAttentionSession(t, s, "s-3", "globex")

	// No deadline: waits forever by design (§9 — expires_in is optional).
	if _, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
		Project: "acme", SessionID: "s-1", Message: "no deadline",
	}); err != nil {
		t.Fatalf("seed open: %v", err)
	}
	lapsed, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
		Project: "acme", SessionID: "s-2", Message: "lapsed", ExpiresAt: 100,
	})
	if err != nil {
		t.Fatalf("seed lapsed: %v", err)
	}
	// Another project's lapsed request: the sweep is core, so it sees it too.
	if _, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
		Project: "globex", SessionID: "s-3", Message: "theirs", ExpiresAt: 100,
	}); err != nil {
		t.Fatalf("seed globex: %v", err)
	}

	// Before the deadline: nothing lapsed.
	due, err := s.ListExpiredAttentionRequests(ctx, 99, 0)
	if err != nil || len(due) != 0 {
		t.Fatalf("before the deadline: %d rows err=%v", len(due), err)
	}
	// After: exactly the two with deadlines, never the deadline-free one.
	due, err = s.ListExpiredAttentionRequests(ctx, 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected two lapsed requests, got %d: %+v", len(due), due)
	}
	for _, r := range due {
		if r.ExpiresAt == 0 {
			t.Fatalf("a request with no deadline must never lapse: %+v", r)
		}
	}

	// Resolving one takes it out of the sweep.
	if err := s.MarkAttentionTimedOut(ctx, lapsed.ID, 101); err != nil {
		t.Fatalf("time out: %v", err)
	}
	due, _ = s.ListExpiredAttentionRequests(ctx, 200, 0)
	if len(due) != 1 || due[0].Project != "globex" {
		t.Fatalf("a resolved request must leave the sweep: %+v", due)
	}
}

func TestAttentionProjectIsolation(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	seedAttentionSession(t, s, "s-2", "globex")

	mine, err := s.CreateAttentionRequest(ctx, &AttentionRequest{Project: "acme", SessionID: "s-1", Message: "mine"})
	if err != nil {
		t.Fatalf("seed acme: %v", err)
	}
	theirs, err := s.CreateAttentionRequest(ctx, &AttentionRequest{Project: "globex", SessionID: "s-2", Message: "theirs"})
	if err != nil {
		t.Fatalf("seed globex: %v", err)
	}

	if _, err := s.GetAttentionRequest(ctx, "acme", theirs.ID); !errors.Is(err, ErrAttentionRequestNotFound) {
		t.Fatalf("cross-project read: want not-found, got %v", err)
	}
	open, err := s.ListOpenAttentionRequests(ctx, "acme")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(open) != 1 || open[0].ID != mine.ID {
		t.Fatalf("list leaked another project's rows: %+v", open)
	}
}

func TestAttentionUserReplyDetection(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")

	if err := s.CreateMessages(ctx, []*Message{
		{SessionID: "s-1", Role: "user", Content: "before", CreatedAt: 50, SequenceNum: 1},
		{SessionID: "s-1", Role: "assistant", Content: "draft", CreatedAt: 150, SequenceNum: 2},
	}); err != nil {
		t.Fatalf("seed messages: %v", err)
	}

	// The assistant's own turn is not an answer, and neither is a human turn from
	// before the request.
	n, err := s.CountUserMessagesSince(ctx, "s-1", 100)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no human reply after the request, got %d", n)
	}

	if err := s.CreateMessages(ctx, []*Message{
		{SessionID: "s-1", Role: "user", Content: "post it", CreatedAt: 200, SequenceNum: 3},
	}); err != nil {
		t.Fatalf("seed reply: %v", err)
	}
	n, err = s.CountUserMessagesSince(ctx, "s-1", 100)
	if err != nil || n != 1 {
		t.Fatalf("expected one human reply, got %d err=%v", n, err)
	}
}

func TestAttentionSessionStampHelper(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")

	if err := s.SetSessionAttentionRequested(ctx, "s-1", true); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	var sess Session
	if err := s.gdb.Where("id = ?", "s-1").First(&sess).Error; err != nil {
		t.Fatalf("read: %v", err)
	}
	if !sess.AttentionRequested {
		t.Fatalf("stamp did not stick")
	}
	// Clearing must work too: the flag carries no gorm default, so false writes.
	if err := s.SetSessionAttentionRequested(ctx, "s-1", false); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s.gdb.Where("id = ?", "s-1").First(&sess).Error; err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if sess.AttentionRequested {
		t.Fatalf("clearing the stamp must stick (a gorm default: tag would break this)")
	}
	if err := s.SetSessionAttentionRequested(ctx, "nope", true); err == nil {
		t.Fatalf("stamping an unknown session must fail loudly")
	}
}

// TestSessionAwaitsHuman pins the read the §8.4 dispatch gate parks a delivery
// on. It is deliberately NOT the session's per-turn `attention_requested`
// column — that one is cleared as soon as the §8.2 emitter has copied it onto a
// `worker.finished` envelope, so by the time a job settles it says nothing about
// whether a human has replied. The open rows of `attention_requests` are the
// durable fact.
func TestSessionAwaitsHuman(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	seedAttentionSession(t, s, "s-2", "acme")

	awaits, err := s.SessionAwaitsHuman(ctx, "acme", "s-1")
	if err != nil || awaits {
		t.Fatalf("a session with no request awaits nobody: %v err=%v", awaits, err)
	}

	req, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
		Project: "acme", SessionID: "s-1", Worker: "tweet-author",
		Message: "sign off on this draft", Channel: "webhook", Delivered: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if awaits, err = s.SessionAwaitsHuman(ctx, "acme", "s-1"); err != nil || !awaits {
		t.Fatalf("an open request means the session awaits a human: %v err=%v", awaits, err)
	}
	// The per-turn stamp being cleared (what the emitter does) must NOT change
	// the answer — that is the whole reason this read exists.
	if err := s.SetSessionAttentionRequested(ctx, "s-1", false); err != nil {
		t.Fatalf("clear stamp: %v", err)
	}
	if awaits, err = s.SessionAwaitsHuman(ctx, "acme", "s-1"); err != nil || !awaits {
		t.Fatalf("clearing the per-turn stamp must not close the request: %v err=%v", awaits, err)
	}

	// Tenancy: another project's session looks like no request at all.
	if awaits, err = s.SessionAwaitsHuman(ctx, "other-co", "s-1"); err != nil || awaits {
		t.Fatalf("cross-project read leaked: %v err=%v", awaits, err)
	}
	// A sibling session is unaffected.
	if awaits, err = s.SessionAwaitsHuman(ctx, "acme", "s-2"); err != nil || awaits {
		t.Fatalf("the request must be scoped to its own session: %v err=%v", awaits, err)
	}

	// Resolving closes it.
	if err := s.MarkAttentionAnswered(ctx, req.ID, 2000); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if awaits, err = s.SessionAwaitsHuman(ctx, "acme", "s-1"); err != nil || awaits {
		t.Fatalf("an answered request is not outstanding: %v err=%v", awaits, err)
	}

	if _, err := s.SessionAwaitsHuman(ctx, "", "s-1"); err == nil {
		t.Fatalf("an unscoped read must be refused")
	}
	if _, err := s.SessionAwaitsHuman(ctx, "acme", ""); err == nil {
		t.Fatalf("a read with no session must be refused")
	}
}

// The read variant behind GET /agent/attention-requests: open-only by default,
// resolved rows included on request, capped by Limit, scoped to one project.
func TestListAttentionRequests(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	seedAttentionSession(t, s, "s-2", "acme")
	seedAttentionSession(t, s, "s-3", "other")

	mk := func(id, project, session string, createdAt int64) *AttentionRequest {
		t.Helper()
		req, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
			ID: id, Project: project, SessionID: session, Worker: "tweet-author",
			Message: "which draft?", CreatedAt: createdAt,
		})
		if err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		return req
	}
	mk("a", "acme", "s-1", 1000)
	answered := mk("b", "acme", "s-2", 2000)
	mk("c", "other", "s-3", 3000)
	if err := s.MarkAttentionAnswered(ctx, answered.ID, 2500); err != nil {
		t.Fatalf("answer: %v", err)
	}

	for _, tc := range []struct {
		name string
		q    AttentionRequestQuery
		want []string
	}{
		{"open only by default", AttentionRequestQuery{Project: "acme"}, []string{"a"}},
		{"resolved rows on request", AttentionRequestQuery{Project: "acme", IncludeResolved: true}, []string{"b", "a"}},
		{"limit caps the page", AttentionRequestQuery{Project: "acme", IncludeResolved: true, Limit: 1}, []string{"b"}},
		{"another project's rows stay invisible", AttentionRequestQuery{Project: "acme", IncludeResolved: true, Limit: 10}, []string{"b", "a"}},
		{"the other project sees its own", AttentionRequestQuery{Project: "other", IncludeResolved: true}, []string{"c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListAttentionRequests(ctx, tc.q)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			ids := []string{}
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			if len(ids) != len(tc.want) {
				t.Fatalf("ids=%v want %v", ids, tc.want)
			}
			for i := range ids {
				if ids[i] != tc.want[i] {
					t.Fatalf("ids=%v want %v (newest-first)", ids, tc.want)
				}
			}
		})
	}

	if _, err := s.ListAttentionRequests(ctx, AttentionRequestQuery{}); err == nil {
		t.Fatalf("an unscoped list must be refused")
	}
}

// seedParkedDelivery writes a delivery parked at awaiting_human on a session.
func seedParkedDelivery(t *testing.T, s *Store, id, project, sessionID string) {
	t.Helper()
	d := &EventDelivery{ID: id, Project: project, EventID: "ev-" + id, SubscriptionID: "sub-" + id,
		SessionID: sessionID, Worker: "architect", Status: DeliveryAwaitingHuman, StartedAt: 100}
	if err := s.gdb.Create(d).Error; err != nil {
		t.Fatalf("seed delivery: %v", err)
	}
}

func deliveryRow(t *testing.T, s *Store, id string) EventDelivery {
	t.Helper()
	var d EventDelivery
	if err := s.gdb.Where("id = ?", id).First(&d).Error; err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	return d
}

func TestAttentionKinds(t *testing.T) {
	cases := []struct {
		name        string
		kind        string
		expiresAt   int64
		wantKind    string
		wantExpires int64
		wantAwaits  bool
		wantErr     bool
	}{
		{name: "blank is an ask", kind: "", expiresAt: 500, wantKind: AttentionKindAsk, wantExpires: 500, wantAwaits: true},
		{name: "ask parks the job", kind: AttentionKindAsk, wantKind: AttentionKindAsk, wantAwaits: true},
		{name: "notice parks nothing and drops its deadline", kind: AttentionKindNotice, expiresAt: 500, wantKind: AttentionKindNotice, wantExpires: 0},
		{name: "unknown kind is refused", kind: "fyi", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newAttentionStore(t)
			ctx := context.Background()
			seedAttentionSession(t, s, "s-1", "acme")
			req, err := s.CreateAttentionRequest(ctx, &AttentionRequest{
				Project: "acme", SessionID: "s-1", Message: "hello", Kind: tc.kind, ExpiresAt: tc.expiresAt,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want an error for kind %q", tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if req.Kind != tc.wantKind || req.ExpiresAt != tc.wantExpires {
				t.Fatalf("kind=%q expires=%d, want %q %d", req.Kind, req.ExpiresAt, tc.wantKind, tc.wantExpires)
			}
			awaits, err := s.SessionAwaitsHuman(ctx, "acme", "s-1")
			if err != nil || awaits != tc.wantAwaits {
				t.Fatalf("awaits=%v err=%v, want %v", awaits, err, tc.wantAwaits)
			}
			// A notice is never swept into a timeout, deadline or not.
			expired, err := s.ListExpiredAttentionRequests(ctx, 10_000, 0)
			if err != nil {
				t.Fatalf("expired: %v", err)
			}
			if tc.kind == AttentionKindNotice && len(expired) != 0 {
				t.Fatalf("a notice must never lapse: %+v", expired)
			}
		})
	}
}

func TestAnswerSessionAttentionSettlesParkedDeliveries(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	seedAttentionSession(t, s, "s-2", "acme")
	for _, sid := range []string{"s-1", "s-2"} {
		if _, err := s.CreateAttentionRequest(ctx, &AttentionRequest{Project: "acme", SessionID: sid, Message: "which subject line?"}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	seedParkedDelivery(t, s, "d-1", "acme", "s-1")
	seedParkedDelivery(t, s, "d-2", "acme", "s-2")

	// Another project's reply closes nothing.
	if n, err := s.AnswerSessionAttention(ctx, "other-co", "s-1", 900); err != nil || n != 0 {
		t.Fatalf("cross-project answer closed %d err=%v", n, err)
	}

	n, err := s.AnswerSessionAttention(ctx, "acme", "s-1", 900)
	if err != nil || n != 1 {
		t.Fatalf("answer closed %d err=%v, want 1", n, err)
	}
	if d := deliveryRow(t, s, "d-1"); d.Status != DeliveryOK || d.EndedAt != 900 {
		t.Fatalf("the answered session's delivery must settle: %+v", d)
	}
	if d := deliveryRow(t, s, "d-2"); d.Status != DeliveryAwaitingHuman || d.EndedAt != 0 {
		t.Fatalf("a sibling session's delivery must stay parked: %+v", d)
	}
	if awaits, _ := s.SessionAwaitsHuman(ctx, "acme", "s-1"); awaits {
		t.Fatalf("an answered session awaits nobody")
	}
	// A second reply is a no-op.
	if n, err := s.AnswerSessionAttention(ctx, "acme", "s-1", 950); err != nil || n != 0 {
		t.Fatalf("second answer closed %d err=%v", n, err)
	}
}

func TestAcknowledgeAttentionRequest(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	first, _ := s.CreateAttentionRequest(ctx, &AttentionRequest{Project: "acme", SessionID: "s-1", Message: "one"})
	second, _ := s.CreateAttentionRequest(ctx, &AttentionRequest{Project: "acme", SessionID: "s-1", Message: "two"})
	seedParkedDelivery(t, s, "d-1", "acme", "s-1")

	if _, err := s.AcknowledgeAttentionRequest(ctx, "other-co", first.ID, 700); !errors.Is(err, ErrAttentionRequestNotFound) {
		t.Fatalf("another project's request must be not found, got %v", err)
	}

	got, err := s.AcknowledgeAttentionRequest(ctx, "acme", first.ID, 700)
	if err != nil || got.AnsweredAt != 700 {
		t.Fatalf("acknowledge: %+v err=%v", got, err)
	}
	// One ask is still open on the session, so the job stays parked.
	if d := deliveryRow(t, s, "d-1"); d.Status != DeliveryAwaitingHuman {
		t.Fatalf("a session with an open ask must stay parked: %+v", d)
	}
	if _, err := s.AcknowledgeAttentionRequest(ctx, "acme", second.ID, 800); err != nil {
		t.Fatalf("acknowledge second: %v", err)
	}
	if d := deliveryRow(t, s, "d-1"); d.Status != DeliveryOK || d.EndedAt != 800 {
		t.Fatalf("closing the last ask must settle the delivery: %+v", d)
	}
	// Idempotent: the stamp does not move.
	again, err := s.AcknowledgeAttentionRequest(ctx, "acme", first.ID, 999)
	if err != nil || again.AnsweredAt != 700 {
		t.Fatalf("re-acknowledge must be a no-op: %+v err=%v", again, err)
	}
}

func TestAttentionTimeoutDoesNotSettleTheDelivery(t *testing.T) {
	s := newAttentionStore(t)
	ctx := context.Background()
	seedAttentionSession(t, s, "s-1", "acme")
	req, _ := s.CreateAttentionRequest(ctx, &AttentionRequest{Project: "acme", SessionID: "s-1", Message: "one", ExpiresAt: 10})
	seedParkedDelivery(t, s, "d-1", "acme", "s-1")
	if err := s.MarkAttentionTimedOut(ctx, req.ID, 20); err != nil {
		t.Fatalf("timeout: %v", err)
	}
	if d := deliveryRow(t, s, "d-1"); d.Status != DeliveryAwaitingHuman {
		t.Fatalf("a lapse wakes the worker; it does not settle the row: %+v", d)
	}
}
