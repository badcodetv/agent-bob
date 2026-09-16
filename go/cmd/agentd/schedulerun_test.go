package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// ── RunNow: the scheduler's own firing, at the current minute ───────────────

// runNowScheduler is newTestScheduler with a synchronous spawn, so the detached
// dispatch has happened by the time RunNow returns.
func runNowScheduler(t *testing.T, store *fakeDispatchStore, starter sessionStarter, now time.Time) *scheduler {
	t.Helper()
	s, _ := newTestScheduler(t, store, starter, now)
	s.spawn = func(fn func()) { fn() }
	return s
}

func TestRunNow(t *testing.T) {
	// 14:03 — deliberately NOT a minute the 09:00 cron matches.
	offCron := time.Date(2026, 9, 13, 14, 3, 20, 0, time.UTC)

	t.Run("fires an off-cron minute through the ordinary gate", func(t *testing.T) {
		store := newFakeDispatchStore()
		store.addWorker(agentdb.NewWorker("acme", "scout"))
		sch := store.addSchedule(agentdb.NewSchedule("acme", "scout", "0 9 * * *", "look for new leads"))
		starter := &recordingStarter{}
		s := runNowScheduler(t, store, starter, offCron)

		res, err := s.RunNow(context.Background(), sch)
		if err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		if res.Outcome != firingRequested || res.EventID == "" || res.DeliveryID == "" {
			t.Fatalf("result = %+v, want requested with event and delivery ids", res)
		}
		if len(starter.jobs) != 1 {
			t.Fatalf("jobs started = %d, want 1", len(starter.jobs))
		}
		job := starter.jobs[0]
		if job.Event.Type != agentdb.EventTypeScheduleFired || job.Event.Text != "look for new leads" {
			t.Errorf("event = %s %q, want the schedule's own firing and input", job.Event.Type, job.Event.Text)
		}
		if job.Event.Envelope.Source != agentdb.EventSourceSchedule {
			t.Errorf("envelope source = %q, want schedule", job.Event.Envelope.Source)
		}
	})

	t.Run("a second press in the same minute fires nothing", func(t *testing.T) {
		store := newFakeDispatchStore()
		store.addWorker(agentdb.NewWorker("acme", "scout"))
		sch := store.addSchedule(agentdb.NewSchedule("acme", "scout", "0 9 * * *", "go"))
		starter := &recordingStarter{}
		s := runNowScheduler(t, store, starter, offCron)

		if _, err := s.RunNow(context.Background(), sch); err != nil {
			t.Fatalf("first: %v", err)
		}
		res, err := s.RunNow(context.Background(), sch)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if res.Outcome != firingAlreadyFired {
			t.Errorf("second outcome = %q, want %q", res.Outcome, firingAlreadyFired)
		}
		if len(starter.jobs) != 1 {
			t.Errorf("jobs started = %d, want 1", len(starter.jobs))
		}
	})

	t.Run("a manual run on a cron minute is the same occurrence as the tick", func(t *testing.T) {
		onCron := time.Date(2026, 9, 13, 9, 0, 5, 0, time.UTC)
		store := newFakeDispatchStore()
		store.addWorker(agentdb.NewWorker("acme", "scout"))
		sch := store.addSchedule(agentdb.NewSchedule("acme", "scout", "0 9 * * *", "go"))
		starter := &recordingStarter{}
		s := runNowScheduler(t, store, starter, onCron)

		if _, err := s.RunNow(context.Background(), sch); err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		if err := s.Tick(context.Background()); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if len(starter.jobs) != 1 {
			t.Errorf("jobs started = %d, want 1 (one occurrence, one job)", len(starter.jobs))
		}
	})

	t.Run("a missing worker disables the schedule, as the tick would", func(t *testing.T) {
		store := newFakeDispatchStore()
		sch := store.addSchedule(agentdb.NewSchedule("acme", "gone", "0 9 * * *", "go"))
		starter := &recordingStarter{}
		s := runNowScheduler(t, store, starter, offCron)

		res, err := s.RunNow(context.Background(), sch)
		if err != nil {
			t.Fatalf("RunNow: %v", err)
		}
		if res.Outcome != firingTargetMissing {
			t.Errorf("outcome = %q, want %q", res.Outcome, firingTargetMissing)
		}
		if _, ok := store.disabled[sch.ID]; !ok {
			t.Error("the schedule was not disabled")
		}
		if len(starter.jobs) != 0 {
			t.Errorf("jobs started = %d, want 0", len(starter.jobs))
		}
	})
}

// ── The route ───────────────────────────────────────────────────────────────

type fakeScheduleGetter struct {
	rows map[string]*agentdb.Schedule // key: project + "/" + id
	err  error
}

func (f fakeScheduleGetter) GetSchedule(_ context.Context, project, id string) (*agentdb.Schedule, error) {
	if f.err != nil {
		return nil, f.err
	}
	if row, ok := f.rows[project+"/"+id]; ok {
		return row, nil
	}
	return nil, agentdb.ErrScheduleNotFound
}

type fakeScheduleRunner struct {
	calls []*agentdb.Schedule
	res   firingResult
	err   error
}

func (f *fakeScheduleRunner) RunNow(_ context.Context, sch *agentdb.Schedule) (firingResult, error) {
	f.calls = append(f.calls, sch)
	return f.res, f.err
}

func TestScheduleRunNowHandler(t *testing.T) {
	enabled := &agentdb.Schedule{ID: "s1", Project: "acme", Worker: "scout", Cron: "0 9 * * *", Enabled: true}
	disabled := &agentdb.Schedule{ID: "s2", Project: "acme", Worker: "scout", Cron: "0 9 * * *", Enabled: false}
	rows := map[string]*agentdb.Schedule{"acme/s1": enabled, "acme/s2": disabled}

	cases := []struct {
		name       string
		principal  principal
		id         string
		getErr     error
		runErr     error
		wantStatus int
		wantRuns   int
	}{
		{name: "runs an enabled schedule", principal: principal{customer: "acme"}, id: "s1",
			wantStatus: http.StatusOK, wantRuns: 1},
		{name: "another project's schedule is not found", principal: principal{customer: "other"}, id: "s1",
			wantStatus: http.StatusNotFound},
		{name: "a disabled schedule is refused, not overruled", principal: principal{customer: "acme"}, id: "s2",
			wantStatus: http.StatusConflict},
		{name: "an embed token is refused", principal: principal{customer: "acme", embedSession: "sess-1"}, id: "s1",
			wantStatus: http.StatusNotFound},
		{name: "no project", principal: principal{}, id: "s1", wantStatus: http.StatusForbidden},
		{name: "a store failure is not a missing schedule", principal: principal{customer: "acme"}, id: "s1",
			getErr: errors.New("db down"), wantStatus: http.StatusInternalServerError},
		{name: "a firing failure is a 500", principal: principal{customer: "acme"}, id: "s1",
			runErr: errors.New("gate down"), wantStatus: http.StatusInternalServerError, wantRuns: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeScheduleRunner{
				res: firingResult{Outcome: firingRequested, EventID: "ev-1", DeliveryID: "del-1"},
				err: tc.runErr,
			}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /agent/schedules/{id}/run",
				scheduleRunNowHandler(fakeScheduleGetter{rows: rows, err: tc.getErr}, runner))

			req := httptest.NewRequest(http.MethodPost, "/agent/schedules/"+tc.id+"/run", nil)
			req = req.WithContext(contextWithPrincipal(req.Context(), tc.principal))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if len(runner.calls) != tc.wantRuns {
				t.Fatalf("runs = %d, want %d", len(runner.calls), tc.wantRuns)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body["schedule_id"] != "s1" || body["worker"] != "scout" ||
				body["outcome"] != firingRequested || body["event_id"] != "ev-1" {
				t.Errorf("body = %v", body)
			}
		})
	}
}
