package httpapi

// charter_applied_test.go — the `applied` field on GET /agent/charter/current
// (DI10 of design/2026-09-11-onboarding-work-plan.md).
//
// The defect this closes: nothing told the console whether a charter had been
// approved, so the console guessed by asking whether a worker literally named
// `architect` existed. A charter naming its architect anything else left the
// project reading as still-in-interview forever, with "Finish setting up this
// project" and no way past it.
//
// What must hold, and why each one is here rather than inferred:
//
//   - the answer comes from the config log's `topology_apply` bracket, which is
//     append-only and server-stamped — not from a name, and not from anything a
//     container can write;
//   - it matches the INTERVIEW SESSION, so a different interview's approval in
//     the same project does not count;
//   - it survives a later malformed re-deposit, because an approved project must
//     never be sent back into onboarding;
//   - and it degrades to false — never to a 500 — when there is no log, or the
//     log read fails, because this field is an extra on a response whose job is
//     to render a charter to a human.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// charterHandlersWithLog is charterHandlers plus the config log. Separate
// helper rather than a changed signature: every other charter test predates
// this field and does not care about the log.
func charterHandlersWithLog(
	t *testing.T,
	mems MemoryStore,
	topos TopologyStore,
	workers WorkersStore,
	log ConfigLogStore,
) *Handlers {
	t.Helper()
	return newHandlers(t, Config{
		Runner:     stubRunner{},
		Store:      stubStore{},
		Identity:   identityFor("acme"),
		Memories:   mems,
		Topologies: topos,
		Workers:    workers,
		ConfigLog:  log,
	})
}

// applyEvent builds the bracket event ApplyTopology writes, with the answers
// shape charter.go's ApplyCharter actually supplies.
func applyEvent(seq int64, session, memoryID string, at int64) *agentdb.ConfigEvent {
	return &agentdb.ConfigEvent{
		ID:      "ce-apply-" + session,
		Project: "acme",
		Seq:     seq,
		Action:  agentdb.ActionTopologyApply,
		Payload: agentdb.JSONMap{
			"topology": "charter",
			"answers":  map[string]any{"session": session, "memory_id": memoryID},
		},
		CreatedAt: at,
	}
}

func decodeCharter(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not JSON: %v — %s", err, body)
	}
	return got
}

// The ordinary mid-interview state: a charter is deposited, nothing approved.
func TestGetCharter_NotAppliedWhileTheLogHasNoApproval(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	log := &fakeConfigLog{}
	h := charterHandlersWithLog(t, mems, newFakeTopologyStore(), newFakeWorkerStore(), log)

	rec := getCharter(t, h, "?session=onboard-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeCharter(t, rec.Body.Bytes())
	if got["applied"] != false {
		t.Errorf("applied = %v, want false", got["applied"])
	}
	// Omitted, not zero: the field is `omitempty` precisely so a client cannot
	// read "approved at the epoch".
	if _, present := got["applied_at"]; present {
		t.Errorf("applied_at must be omitted while applied is false, got %v", got["applied_at"])
	}

	// The query is the contract with the store: the project comes from the
	// credential (P5), the action narrows in SQL, and the scan is capped.
	if log.got.Project != "acme" {
		t.Errorf("project = %q, want acme (from the token, never the query string)", log.got.Project)
	}
	if log.got.Action != agentdb.ActionTopologyApply {
		t.Errorf("action = %q, want %q", log.got.Action, agentdb.ActionTopologyApply)
	}
	if log.got.Limit != charterAppliedScanCap {
		t.Errorf("limit = %d, want %d", log.got.Limit, charterAppliedScanCap)
	}
}

// The state DI10 could not represent: approved, and the server says so.
func TestGetCharter_AppliedWhenTheLogCarriesThisSessionsApproval(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	log := &fakeConfigLog{out: []*agentdb.ConfigEvent{
		applyEvent(7, "onboard-1", "mem-charter-1", 1789000999000),
	}}
	h := charterHandlersWithLog(t, mems, newFakeTopologyStore(), newFakeWorkerStore(), log)

	got := decodeCharter(t, getCharter(t, h, "?session=onboard-1").Body.Bytes())
	if got["applied"] != true {
		t.Fatalf("applied = %v, want true", got["applied"])
	}
	if at, _ := got["applied_at"].(float64); int64(at) != 1789000999000 {
		t.Errorf("applied_at = %v, want 1789000999000", got["applied_at"])
	}
	// Still a fully rendered charter — the flag is additive.
	if got["valid"] != true {
		t.Errorf("valid = %v, want true: %v", got["valid"], got)
	}
}

// A worker named something other than `architect` is exactly the case the old
// name-based guess got wrong. Here it is irrelevant: the answer does not read
// the roster at all, so a charter with a custom architect_name reports applied.
func TestGetCharter_AppliedIgnoresWhatTheArchitectIsCalled(t *testing.T) {
	const customName = `Charter v1: a weekly newsletter, judged on it going out.
{
  "goal": "Send one newsletter a week to the shop's list.",
  "measure": "Four have gone out in a month and the list is bigger than 430.",
  "label_rules": "kind=draft — written but not sent. name=<issue-slug>.",
  "rationale": "Repeat visits are the problem, not new customers.",
  "architect_name": "head-of-newsletter"
}`
	mems := &fakeMemories{one: charterMemoryRow(customName)}
	// The roster deliberately holds NO worker called `architect`.
	workers := newFakeWorkerStore()
	log := &fakeConfigLog{out: []*agentdb.ConfigEvent{
		applyEvent(7, "onboard-1", "mem-charter-1", 1789000999000),
	}}
	h := charterHandlersWithLog(t, mems, newFakeTopologyStore(), workers, log)

	got := decodeCharter(t, getCharter(t, h, "?session=onboard-1").Body.Bytes())
	// `valid` first, and it is load-bearing: if `architect_name` were rejected
	// by the parser this deposit would be unparseable, and the case above
	// ("applied survives an unparseable deposit") would carry the assertion
	// below for the wrong reason. Asserting valid pins that this really is a
	// VALID charter that names its architect something else.
	if got["valid"] != true {
		t.Fatalf("valid = %v, want true — a custom architect_name is legal: %v", got["valid"], got)
	}
	effects, _ := got["summary_of_effects"].(map[string]any)
	if effects == nil || effects["architect_name"] != "head-of-newsletter" {
		t.Fatalf("architect_name in effects = %v, want head-of-newsletter", effects)
	}
	if got["applied"] != true {
		t.Fatalf("applied = %v, want true — the architect's NAME must not decide this (DI10)", got["applied"])
	}
}

// Another interview's approval in the same project is not this interview's.
func TestGetCharter_AppliedMatchesTheSessionNotJustTheProject(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	log := &fakeConfigLog{out: []*agentdb.ConfigEvent{
		applyEvent(7, "onboard-someone-else", "mem-other", 1789000999000),
	}}
	h := charterHandlersWithLog(t, mems, newFakeTopologyStore(), newFakeWorkerStore(), log)

	got := decodeCharter(t, getCharter(t, h, "?session=onboard-1").Body.Bytes())
	if got["applied"] != false {
		t.Errorf("applied = %v, want false — a different session's apply is not this one's", got["applied"])
	}
}

// An approved project whose interview later deposited something unparseable
// must still report applied. The opposite would send a finished project back
// into onboarding on the strength of a stray re-deposit.
func TestGetCharter_AppliedSurvivesAnUnparseableNewerDeposit(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow("this is not a charter at all")}
	log := &fakeConfigLog{out: []*agentdb.ConfigEvent{
		applyEvent(7, "onboard-1", "mem-charter-1", 1789000999000),
	}}
	h := charterHandlersWithLog(t, mems, newFakeTopologyStore(), newFakeWorkerStore(), log)

	rec := getCharter(t, h, "?session=onboard-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeCharter(t, rec.Body.Bytes())
	if got["valid"] != false {
		t.Errorf("valid = %v, want false — the deposit does not parse", got["valid"])
	}
	if got["applied"] != true {
		t.Errorf("applied = %v, want true even so", got["applied"])
	}
}

// Both quiet degradations, in one table. Neither may fail the route: the
// response's primary job is to render a charter to a human.
func TestGetCharter_AppliedDegradesToFalseWithoutFailingTheRoute(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  ConfigLogStore
	}{
		{
			// The sqlite fallback, where the product layer is not wired at all:
			// there is no charter to have been applied.
			name: "no config log wired",
			log:  nil,
		},
		{
			name: "the log read fails",
			log:  &fakeConfigLog{err: errors.New("connection refused")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
			h := charterHandlersWithLog(t, mems, newFakeTopologyStore(), newFakeWorkerStore(), tc.log)

			rec := getCharter(t, h, "?session=onboard-1")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			got := decodeCharter(t, rec.Body.Bytes())
			if got["applied"] != false {
				t.Errorf("applied = %v, want false", got["applied"])
			}
			if got["valid"] != true {
				t.Errorf("valid = %v, want true — the charter still renders", got["valid"])
			}
		})
	}
}

// TestGetCharter_AppliedRoundTripsThroughRealPostgres is the one claim the
// fakes above cannot make.
//
// `charterApplied` reads the interview session out of a `topology_apply`
// payload, and that payload is written as an `agentdb.JSONMap`, stored as
// jsonb, and read back through a json.Unmarshal — so the nested `answers`
// object arrives as one Go type on the write path and potentially another off
// the database. `answered`'s type switch exists for exactly that reason, and a
// test that only ever sees a hand-built event would pass while production
// returned false forever. This one drives the real store: append the charter
// memory, approve it through the real ApplyTopology, then read the route back.
func TestGetCharter_AppliedRoundTripsThroughRealPostgres_LivePG(t *testing.T) {
	url := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL not set — skipping live Postgres test")
	}
	store, err := agentdb.Open(url)
	if err != nil {
		t.Fatalf("open live postgres: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const project = "charter-applied-livepg"
	const session = "onboard-livepg-1"
	t.Cleanup(func() {
		ctx := context.Background()
		_ = store.PurgeConfigEvents(ctx, project)
		for _, table := range []string{
			"memories", "workers", "subscriptions", "schedules", "project_settings",
		} {
			_ = store.DB().Exec("DELETE FROM "+table+" WHERE project = ?", project).Error
		}
	})

	h := newHandlers(t, Config{
		Runner: stubRunner{}, Store: stubStore{},
		Identity: identityFor(project), AgentDB: store,
	})

	// 1. The deposit, through the append route so provenance is server-stamped
	//    empty exactly as a real interview's would not be — the flag does not
	//    care who wrote it, only that it parses and was approved.
	deposit, err := json.Marshal(map[string]any{
		"labels":  map[string]string{"kind": "org-charter", "name": session},
		"content": charterDeposit,
		"embed":   false,
	})
	if err != nil {
		t.Fatalf("marshal deposit: %v", err)
	}
	if rec := do(h, http.MethodPost, "/agent/memories", string(deposit)); rec.Code != http.StatusOK &&
		rec.Code != http.StatusCreated {
		t.Fatalf("append the charter memory: status=%d body=%s", rec.Code, rec.Body)
	}

	// 2. Not applied yet — and this half matters as much as the next. It proves
	//    the false answer is a real read of an empty log rather than the
	//    fallback a broken query would also produce.
	before := decodeCharter(t, getCharter(t, h, "?session="+session).Body.Bytes())
	if before["applied"] != false {
		t.Fatalf("applied = %v before the apply, want false: %v", before["applied"], before)
	}

	// 3. Approve it, through the real transaction.
	rec := applyCharter(t, h, `{"session":"`+session+`","rationale":"live-PG round trip"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: status=%d body=%s", rec.Code, rec.Body)
	}

	// 4. The route now reports it, from the jsonb the apply wrote.
	after := decodeCharter(t, getCharter(t, h, "?session="+session).Body.Bytes())
	if after["applied"] != true {
		t.Fatalf("applied = %v after the apply, want true: %v", after["applied"], after)
	}
	if at, _ := after["applied_at"].(float64); at <= 0 {
		t.Errorf("applied_at = %v, want the approving event's timestamp", after["applied_at"])
	}
}
