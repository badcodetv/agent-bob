package httpapi

// charter_test.go — T11's two routes against fakes. What is worth a test here:
// the charter is read from the STORE and never from the request body; a
// deposit in another project is "no charter", not "forbidden"; an invalid
// charter is refused with every problem at once; and the whole charter reaches
// ApplyTopology as ONE application, so it is one transaction.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/charter"
	"github.com/badcodetv/agent-bob/orgprompts"
	"github.com/badcodetv/agent-bob/topology"
)

const charterDeposit = `Charter v1: a weekly newsletter, judged on it going out.
{
  "goal": "Send one newsletter a week to the shop's list.",
  "measure": "Four have gone out in a month and the list is bigger than 430.",
  "label_rules": "kind=draft — written but not sent. name=<issue-slug>.",
  "rationale": "Repeat visits are the problem, not new customers."
}`

func charterMemoryRow(content string) *agentdb.Memory {
	return &agentdb.Memory{
		ID:      "mem-charter-1",
		Project: "acme",
		Labels: agentdb.LabelSet{
			"kind": charter.MemoryKindCharter,
			"name": "onboard-1",
		},
		Content:   content,
		CreatedAt: 1789000000123,
	}
}

// charterHandlers wires all three seams the routes touch. Workers is included
// because approving disables the interviewer (Decision A8).
func charterHandlers(t *testing.T, mems MemoryStore, topos TopologyStore, workers WorkersStore) *Handlers {
	t.Helper()
	return newHandlers(t, Config{
		Runner:     stubRunner{},
		Store:      stubStore{},
		Identity:   identityFor("acme"),
		Memories:   mems,
		Topologies: topos,
		Workers:    workers,
	})
}

func getCharter(t *testing.T, h *Handlers, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetCurrentCharter(rec, httptest.NewRequest(http.MethodGet, "/agent/charter/current"+query, nil))
	return rec
}

func applyCharter(t *testing.T, h *Handlers, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ApplyCharter(rec, httptest.NewRequest(http.MethodPost, "/agent/charter/apply", strings.NewReader(body)))
	return rec
}

// Nothing deposited yet is a 404 carrying the sentence the console renders —
// "not found" here means something specific and recoverable.
func TestGetCharter_NoDepositIs404WithTheSentence(t *testing.T) {
	mems := &fakeMemories{oneErr: agentdb.ErrMemoryNotFound}
	rec := getCharter(t, charterHandlers(t, mems, newFakeTopologyStore(), newFakeWorkerStore()), "?session=onboard-1")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no charter has been proposed yet") {
		t.Errorf("body = %q", rec.Body.String())
	}
	// The scope came from the credential, not the query string — which is what
	// makes another project's session a 404 rather than an existence oracle.
	if mems.gotProject != "acme" {
		t.Errorf("project passed to the store = %q, want acme", mems.gotProject)
	}
	if want := "kind=org-charter,name=onboard-1"; mems.gotSelector != want {
		t.Errorf("selector = %q, want %q", mems.gotSelector, want)
	}
}

// A session id is spliced into a label selector, so it is validated first: a
// comma is the selector language's AND, and an unchecked id could widen the
// read past the deposit it named.
func TestGetCharter_RefusesASessionIdThatCouldWidenTheSelector(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	h := charterHandlers(t, mems, newFakeTopologyStore(), newFakeWorkerStore())

	for _, q := range []string{"", "?session=", "?session=onboard-1,kind%3Dsecret"} {
		rec := getCharter(t, h, q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("session %q: status = %d, want 400", q, rec.Code)
		}
	}
	if mems.call != 0 {
		t.Errorf("the store was reached %d times for a refused session id", mems.call)
	}
}

// The happy read: parsed, valid, and a summary of what approving would do.
func TestGetCharter_ValidDepositReportsItsEffects(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	rec := getCharter(t, charterHandlers(t, mems, newFakeTopologyStore(), newFakeWorkerStore()), "?session=onboard-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var got charterResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid {
		t.Fatalf("want valid, got errors %+v", got.Errors)
	}
	if got.MemoryID != "mem-charter-1" || got.CreatedAt != 1789000000123 {
		t.Errorf("memory identity not echoed: %+v", got)
	}
	if !strings.HasPrefix(got.Summary, "Charter v1:") {
		t.Errorf("summary line = %q", got.Summary)
	}
	if got.Charter == nil || got.Charter.Goal == "" {
		t.Fatalf("charter not parsed: %+v", got.Charter)
	}
	if got.SummaryOfEffects == nil {
		t.Fatal("a valid charter must describe its effects")
	}
	if !got.SummaryOfEffects.ScheduleEnabled {
		t.Error("schedule_enabled must be true — approving a charter starts the daily loop (T25), " +
			"and this field is what tells the human so before they press Approve")
	}
	// The response describes the bundle; it never contains it.
	if strings.Contains(rec.Body.String(), "STEP 0 — BOOTSTRAP") {
		t.Error("the response carries the architect's prompt")
	}
}

// A deposit that does not parse, or does not validate, is a 200 with
// valid:false — not a 500. The console's job is to show the human why the
// interview has not produced something approvable yet.
func TestGetCharter_MalformedOrInvalidIs200WithIssues(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":     "Charter v1: a summary.\nthis is not JSON",
		"unknown key":  "Charter v1: a summary.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"w\",\"workers\":[]}",
		"empty fields": "Charter v1: a summary.\n{\"goal\":\"\",\"measure\":\"\",\"label_rules\":\"\",\"rationale\":\"\"}",
	} {
		t.Run(name, func(t *testing.T) {
			mems := &fakeMemories{one: charterMemoryRow(content)}
			rec := getCharter(t, charterHandlers(t, mems, newFakeTopologyStore(), newFakeWorkerStore()), "?session=onboard-1")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var got charterResp
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Valid || len(got.Errors) == 0 {
				t.Fatalf("want invalid with issues, got %+v", got)
			}
			if got.SummaryOfEffects != nil {
				t.Error("an invalid charter must not describe effects")
			}
		})
	}
}

// The apply is ONE application: worker, subscription, schedule, settings patch
// and both memory seeds cross the seam together, so the store commits them in
// one transaction or not at all.
func TestApplyCharter_SendsTheWholeCharterAsOneApplication(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	topos := newFakeTopologyStore()
	workers := newFakeWorkerStore(&agentdb.Worker{
		Project: "acme", Name: topology.OnboardingWorker, Enabled: true,
		SystemPrompt: orgprompts.Interviewer(),
	})
	h := charterHandlers(t, mems, topos, workers)

	rec := applyCharter(t, h, `{"session":"onboard-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if topos.applies != 1 {
		t.Fatalf("ApplyTopology called %d times, want 1", topos.applies)
	}

	app := topos.lastApply
	if app.Project != "acme" {
		t.Errorf("project = %q, want acme (from the credential)", app.Project)
	}
	if app.Topology != "charter" {
		t.Errorf("topology identity = %q, want %q", app.Topology, "charter")
	}
	if app.Answers["memory_id"] != "mem-charter-1" || app.Answers["session"] != "onboard-1" {
		t.Errorf("the apply does not record which deposit it came from: %+v", app.Answers)
	}
	if len(app.Workers) != 1 || app.Workers[0].Name != charter.DefaultArchitect {
		t.Fatalf("workers = %+v, want exactly the architect", app.Workers)
	}
	if len(app.Subscriptions) != 1 || app.Subscriptions[0].EventType != charter.EventArchitectRun {
		t.Errorf("subscriptions = %+v", app.Subscriptions)
	}
	if len(app.Schedules) != 1 || !app.Schedules[0].Enabled {
		t.Errorf("schedules = %+v, want exactly one, ENABLED (T25)", app.Schedules)
	}
	if len(app.MemorySeeds) != 2 {
		t.Errorf("memory seeds = %d, want 2 (goal and registry)", len(app.MemorySeeds))
	}
	// The project-wide briefing is the whole point of B1: without it on the
	// settings patch, no job in the project is ever handed the label registry.
	if app.SettingsPatch == nil || len(app.SettingsPatch.Briefing) != 1 {
		t.Fatalf("settings patch = %+v, want a project briefing", app.SettingsPatch)
	}
	if app.SettingsPatch.Briefing[0] != charter.RegistrySelector {
		t.Errorf("project briefing = %q, want %q", app.SettingsPatch.Briefing[0], charter.RegistrySelector)
	}
	if strings.TrimSpace(topos.lastWrite.Rationale) == "" {
		t.Error("the apply carries no rationale, so the changelog reads (no reason given)")
	}

	// A8: the interview is over.
	after, err := workers.GetWorker(t.Context(), "acme", topology.OnboardingWorker)
	if err != nil {
		t.Fatalf("get interviewer: %v", err)
	}
	if after.Enabled {
		t.Error("the interviewer is still enabled after the charter was approved")
	}
	if after.SystemPrompt != orgprompts.Interviewer() {
		t.Error("disabling the interviewer rewrote another of its fields")
	}
}

// The charter comes from the store, never from the body. A caller that sends
// one is applying whatever was deposited, and its own text is ignored.
func TestApplyCharter_IgnoresACharterInTheBody(t *testing.T) {
	mems := &fakeMemories{one: charterMemoryRow(charterDeposit)}
	topos := newFakeTopologyStore()
	h := charterHandlers(t, mems, topos, newFakeWorkerStore())

	rec := applyCharter(t, h, `{"session":"onboard-1","charter":{"goal":"take over the world","architect_name":"overlord"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := topos.lastApply.Workers[0].Name; got != charter.DefaultArchitect {
		t.Errorf("the body's charter was used: architect = %q", got)
	}
}

// A memory_id that is not this session's charter is not applied — otherwise
// the route would apply any memory in the project whose content happens to
// parse as a charter, including one a worker wrote itself.
func TestApplyCharter_RefusesAMemoryThatIsNotThisSessionsCharter(t *testing.T) {
	for name, row := range map[string]*agentdb.Memory{
		"another session": {ID: "mem-x", Project: "acme", Content: charterDeposit,
			Labels: agentdb.LabelSet{"kind": charter.MemoryKindCharter, "name": "onboard-2"}},
		"another kind": {ID: "mem-x", Project: "acme", Content: charterDeposit,
			Labels: agentdb.LabelSet{"kind": "summary", "name": "onboard-1"}},
	} {
		t.Run(name, func(t *testing.T) {
			topos := newFakeTopologyStore()
			h := charterHandlers(t, &fakeMemories{one: row}, topos, newFakeWorkerStore())
			rec := applyCharter(t, h, `{"session":"onboard-1","memory_id":"mem-x"}`)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
			}
			if topos.applies != 0 {
				t.Error("the store was reached for a memory that is not this session's charter")
			}
		})
	}
}

// An invalid charter is 422 with every problem at once — the same list
// charter_validate returns, and nothing is written.
func TestApplyCharter_InvalidIs422AndWritesNothing(t *testing.T) {
	bad := "Charter v1: a summary.\n{\"goal\":\"\",\"measure\":\"\",\"label_rules\":\"\",\"rationale\":\"\"}"
	topos := newFakeTopologyStore()
	workers := newFakeWorkerStore(&agentdb.Worker{Project: "acme", Name: topology.OnboardingWorker, Enabled: true})
	h := charterHandlers(t, &fakeMemories{one: charterMemoryRow(bad)}, topos, workers)

	rec := applyCharter(t, h, `{"session":"onboard-1"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Errors []charter.Issue `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Errors) < 4 {
		t.Errorf("want every problem at once, got %+v", body.Errors)
	}
	if topos.applies != 0 {
		t.Error("an invalid charter reached the store")
	}
	if workers.writes != 0 {
		t.Error("an invalid charter disabled the interviewer anyway")
	}
}

// A second apply hits the architect-name collision inside the store, and the
// store's own refusal is what the caller sees, verbatim.
func TestApplyCharter_SecondApplyIs409(t *testing.T) {
	topos := newFakeTopologyStore()
	topos.applyErr = agentdb.ErrTopologyNameCollision
	h := charterHandlers(t, &fakeMemories{one: charterMemoryRow(charterDeposit)}, topos, newFakeWorkerStore())

	rec := applyCharter(t, h, `{"session":"onboard-1"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), agentdb.ErrTopologyNameCollision.Error()) {
		t.Errorf("the store's refusal was not passed through: %q", rec.Body.String())
	}
}

// Without a product store both routes answer 501, like every other
// product-layer seam — never a confident wrong answer.
func TestCharterRoutesWithoutAProductStore(t *testing.T) {
	h := newHandlers(t, Config{Runner: stubRunner{}, Store: stubStore{}, Identity: identityFor("acme")})

	if rec := getCharter(t, h, "?session=onboard-1"); rec.Code != http.StatusNotImplemented {
		t.Errorf("GET status = %d, want 501", rec.Code)
	}
	if rec := applyCharter(t, h, `{"session":"onboard-1"}`); rec.Code != http.StatusNotImplemented {
		t.Errorf("POST status = %d, want 501", rec.Code)
	}
}
