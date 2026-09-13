package httpapi

// charter_architect_run_test.go — approving a charter starts the architect's
// first run (the team-forming work on feat/first-run-team-forming). The run is
// an ordinary `architect.run` event with the external envelope, written only
// after the apply committed, and never when the apply was refused.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/charter"
)

func TestApplyCharter_StartsTheArchitect(t *testing.T) {
	cases := []struct {
		name      string
		applyErr  error
		createErr error
		noEvents  bool

		wantStatus  int
		wantEvents  int
		wantID      bool
		wantProblem bool
	}{
		{name: "approval emits architect.run", wantStatus: http.StatusOK, wantEvents: 1, wantID: true},
		{name: "a refused re-apply emits nothing", applyErr: agentdb.ErrTopologyNameCollision,
			wantStatus: http.StatusConflict},
		{name: "an event write failure still reports the approval", createErr: errors.New("db unhappy"),
			wantStatus: http.StatusOK, wantProblem: true},
		{name: "no event store: approved, and says the architect was not started", noEvents: true,
			wantStatus: http.StatusOK, wantProblem: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topos := newFakeTopologyStore()
			topos.applyErr = tc.applyErr
			events := newFakeEventStore()
			events.createErr = tc.createErr
			cfg := Config{
				Runner:     stubRunner{},
				Store:      stubStore{},
				Identity:   identityFor("acme"),
				Memories:   &fakeMemories{one: charterMemoryRow(charterDeposit)},
				Topologies: topos,
				Workers:    newFakeWorkerStore(),
			}
			if !tc.noEvents {
				cfg.Events = events
			}
			h := newHandlers(t, cfg)

			rec := applyCharter(t, h, `{"session":"onboard-1"}`)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if len(events.events) != tc.wantEvents {
				t.Fatalf("events written = %d, want %d", len(events.events), tc.wantEvents)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}

			var body struct {
				Workers             []map[string]any `json:"workers"`
				ArchitectRunEventID string           `json:"architect_run_event_id"`
				ArchitectRunError   string           `json:"architect_run_error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			// The read-back the console already consumes is still at the top level.
			if len(body.Workers) != 1 {
				t.Errorf("workers = %d, want the architect at the top level of the response", len(body.Workers))
			}
			if (body.ArchitectRunEventID != "") != tc.wantID {
				t.Errorf("architect_run_event_id = %q, want present=%v", body.ArchitectRunEventID, tc.wantID)
			}
			if (body.ArchitectRunError != "") != tc.wantProblem {
				t.Errorf("architect_run_error = %q, want present=%v", body.ArchitectRunError, tc.wantProblem)
			}

			if tc.wantEvents == 1 {
				ev := events.events[0]
				if body.ArchitectRunEventID != ev.ID {
					t.Errorf("response names event %q, store holds %q", body.ArchitectRunEventID, ev.ID)
				}
				if ev.Project != "acme" || ev.Type != charter.EventArchitectRun {
					t.Errorf("event = %s/%s, want acme/%s", ev.Project, ev.Type, charter.EventArchitectRun)
				}
				// The same envelope POST /agent/events stamps: it is a human act.
				if ev.Envelope.Source != agentdb.EventSourceExternal || ev.Envelope.Depth != 0 {
					t.Errorf("envelope = %+v, want {external, 0}", ev.Envelope)
				}
			}
		})
	}
}
