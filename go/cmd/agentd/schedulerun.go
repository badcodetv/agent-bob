package main

// schedulerun.go — POST /agent/schedules/{id}/run: fire one schedule now.
//
// This is "hurry the clock". A new project's first hour is the hour a human
// most wants to watch the organisation work, and the daily 09:00 cron is the
// wrong clock for it. The console's "Run a cycle now" loops over the project's
// enabled schedules and calls this once for each.
//
// It is the scheduler's OWN firing, not a second path: the same claim, the same
// `schedule.fired` event with the schedule's input as its text, the same
// delivery, the same dispatch gate (capacity, budget, max_instances). The only
// differences are WHICH minute it claims — the current one, whether or not the
// cron matches it — and that the gate runs off the request, because the gate
// may provision a container and a human is waiting for an answer.
//
// Three consequences of claiming the current minute, all deliberate:
//
//   - Pressing it twice inside one minute fires once; the second answer is
//     `already_fired`. That is the idempotency guard doing its job, and it is a
//     better double-click guard than anything the client could add.
//   - If the cron happens to match this minute too, the scheduled occurrence
//     and the manual one are the same occurrence. It runs once.
//   - The catch-up watermark is untouched: running a schedule by hand does not
//     claim that the tick evaluated anything.
//
// Running is not configuration, so nothing here writes a config event — the
// same posture as POST /agent/events. Tenancy is the usual rule: the project
// comes from the credential, and another project's schedule is simply not
// found. An embed token (a credential minted for one session inside somebody
// else's page) is refused the same way the other project-wide routes refuse it.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// What one firing came to, in words the console shows.
const (
	// firingRequested: the rows exist and the job (or session message) is on
	// its way through the gate. It may still queue behind a busy worker.
	firingRequested = "requested"
	// firingAlreadyFired: this occurrence was already claimed.
	firingAlreadyFired = "already_fired"
	// firingTargetMissing: the worker or session is gone; the schedule was
	// disabled, loudly, exactly as the tick would have done.
	firingTargetMissing = "target_missing"
	// firingBusy: a session-mode target is mid-turn, so nothing was sent.
	firingBusy = "busy"
)

// firingResult is what fire reports. The tick ignores it; run-now returns it.
type firingResult struct {
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	EventID    string `json:"event_id,omitempty"`
	DeliveryID string `json:"delivery_id,omitempty"`
}

// RunNow fires one schedule at the current minute. The caller has already
// resolved the row within the right project and checked it is enabled.
func (s *scheduler) RunNow(ctx context.Context, sch *agentdb.Schedule) (firingResult, error) {
	minute := s.now().In(s.loc).Truncate(time.Minute)
	return s.fireOccurrence(ctx, sch, minute, true)
}

// scheduleGetter is the one read the route needs before it hands off.
type scheduleGetter interface {
	GetSchedule(ctx context.Context, project, id string) (*agentdb.Schedule, error)
}

// scheduleRunner is the scheduler, narrowed, so the handler is testable alone.
type scheduleRunner interface {
	RunNow(ctx context.Context, sch *agentdb.Schedule) (firingResult, error)
}

// scheduleRunResponse is the route's answer: which schedule, and what came of it.
type scheduleRunResponse struct {
	ScheduleID    string `json:"schedule_id"`
	Worker        string `json:"worker,omitempty"`
	TargetSession string `json:"target_session,omitempty"`
	firingResult
}

func scheduleRunNowHandler(store scheduleGetter, runner scheduleRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id, err := identityFromRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if id.SessionScope != "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if id.Customer == "" {
			http.Error(w, "no project in token", http.StatusForbidden)
			return
		}
		scheduleID := strings.TrimSpace(r.PathValue("id"))
		if scheduleID == "" {
			http.Error(w, "schedule id is required", http.StatusBadRequest)
			return
		}
		sch, err := store.GetSchedule(r.Context(), id.Customer, scheduleID)
		switch {
		case errors.Is(err, agentdb.ErrScheduleNotFound):
			http.Error(w, "schedule not found", http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		case sch == nil:
			http.Error(w, "schedule not found", http.StatusNotFound)
			return
		}
		if !sch.Enabled {
			// A disabled schedule was switched off by someone — possibly the
			// scheduler itself, with a reason in the changelog. Running it by
			// hand would quietly overrule that.
			http.Error(w, "this schedule is disabled; enable it before running it", http.StatusConflict)
			return
		}
		res, err := runner.RunNow(r.Context(), sch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(scheduleRunResponse{
			ScheduleID:    sch.ID,
			Worker:        sch.Worker,
			TargetSession: sch.TargetSession,
			firingResult:  res,
		})
	}
}
