package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/charter"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
	"github.com/binocarlos/badcode-agent-orange/orgprompts"
)

// ---------------------------------------------------------------------------
// T10 — `charter_validate`.
//
// What is worth a test here: a bad charter comes back as a RESULT the model can
// act on rather than a tool error; every problem is reported at once, because
// one-per-call is the failure this tool exists to prevent; the response never
// carries the architect's prompt, which is the difference between a summary and
// the resolved bundle; and the tool cannot write.
// ---------------------------------------------------------------------------

func charterValidateCall(t *testing.T, arg any) charterValidateResult {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"charter": arg})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := newCharterTools().validate(context.Background(), mcpCaller{Project: "acme"}, raw)
	if err != nil {
		t.Fatalf("validate returned a tool error: %v", err)
	}
	res, ok := out.(charterValidateResult)
	if !ok {
		t.Fatalf("validate returned %T, want charterValidateResult", out)
	}
	return res
}

const goodDeposit = `Charter v1: a weekly newsletter, judged on it going out.
{
  "goal": "Send one newsletter a week.",
  "measure": "Four have gone out in a month and the list is bigger than 430.",
  "label_rules": "kind=draft — written but not sent. name=<issue-slug>.",
  "rationale": "Repeat visits are the problem, not new customers."
}`

// The happy path, and the shape of the answer: valid, plus a description of
// what approving it would do in the terms the console panel shows.
func TestCharterValidateAcceptsAWholeDeposit(t *testing.T) {
	res := charterValidateCall(t, goodDeposit)

	if !res.Valid {
		t.Fatalf("want valid, got errors: %+v", res.Errors)
	}
	if res.Summary == nil {
		t.Fatal("a valid charter must come back with a summary of its effects")
	}
	s := res.Summary
	if s.ArchitectName != charter.DefaultArchitect {
		t.Errorf("architect_name: want %q, got %q", charter.DefaultArchitect, s.ArchitectName)
	}
	if s.ArchitectCron != charter.DefaultArchitectCron {
		t.Errorf("architect_cron: want %q, got %q", charter.DefaultArchitectCron, s.ArchitectCron)
	}
	if s.ScheduleEnabled {
		t.Error("schedule_enabled must be false — the daily loop ships off (Decision C5)")
	}
	if s.SubscriptionEvent != charter.EventArchitectRun {
		t.Errorf("subscription_event: want %q, got %q", charter.EventArchitectRun, s.SubscriptionEvent)
	}
	if s.WorkerCount != 1 {
		t.Errorf("worker_count: want 1 (the architect, and no roster), got %d", s.WorkerCount)
	}
	wantSeeds := []string{"kind=project-goal,name=project-goal", "kind=registry,name=label-registry"}
	if !reflect.DeepEqual(s.MemorySeedLabels, wantSeeds) {
		t.Errorf("memory_seed_labels: want %v, got %v", wantSeeds, s.MemorySeedLabels)
	}
	wantFields := []string{"system_prompt", "briefing"}
	if !reflect.DeepEqual(s.SettingsFields, wantFields) {
		t.Errorf("settings_fields: want %v, got %v", wantFields, s.SettingsFields)
	}

	// The read-back exists so a model can see its summary line was found.
	if res.Charter == nil || !strings.HasPrefix(res.Charter.Summary, "Charter v1:") {
		t.Errorf("charter echo: want the summary line back, got %+v", res.Charter)
	}
}

// THE assertion this tool's interface decision rests on: a summary, never the
// resolved bundle. The bundle carries orgprompts.Architect() — the whole run
// structure and every tool name — and the interviewer calls this repeatedly
// while a charter converges.
func TestCharterValidateNeverReturnsTheArchitectsPrompt(t *testing.T) {
	res := charterValidateCall(t, goodDeposit)
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}

	// A distinctive line from the middle of the prompt, not its first words:
	// a truncation bug would still pass a prefix check.
	const fromTheArchitectsPrompt = "The verdict is written before the change, always."
	if !strings.Contains(orgprompts.Architect(), fromTheArchitectsPrompt) {
		t.Fatalf("test is stale: architect.md no longer contains %q", fromTheArchitectsPrompt)
	}
	if strings.Contains(string(body), fromTheArchitectsPrompt) {
		t.Error("the validate response contains the architect's system prompt")
	}
	if strings.Contains(string(body), "STEP 0 — BOOTSTRAP") {
		t.Error("the validate response contains the architect's prompt structure")
	}
}

// Every problem at once. One-error-per-call is the documented failure this
// tool exists to prevent (Agent Wolf's R269: thirteen errors on one attempt).
func TestCharterValidateReportsEveryProblemAtOnce(t *testing.T) {
	res := charterValidateCall(t, `Charter v1: incomplete.
{"goal": "", "measure": "", "label_rules": "", "rationale": "", "architect_name": "Not A Name"}`)

	if res.Valid {
		t.Fatal("want invalid")
	}
	got := map[string]bool{}
	for _, issue := range res.Errors {
		got[issue.Path] = true
		if strings.TrimSpace(issue.Message) == "" {
			t.Errorf("issue at %q has no message", issue.Path)
		}
	}
	for _, want := range []string{"goal", "measure", "label_rules", "rationale", "architect_name"} {
		if !got[want] {
			t.Errorf("no issue reported for %q; got %+v", want, res.Errors)
		}
	}
	if res.Summary != nil {
		t.Error("an invalid charter must not come back with a summary of effects")
	}
}

// A malformed deposit is an ANSWER, not a tool failure. The model is asking
// "is this right?"; a transport-level error reads as "the tool is broken" and
// invites it to stop calling.
func TestCharterValidateTreatsMalformedInputAsAResult(t *testing.T) {
	for name, arg := range map[string]any{
		"no JSON body":    "Charter v1: just a summary line and nothing else.",
		"not JSON":        "Charter v1: a summary.\nthis is not JSON at all",
		"unknown field":   "Charter v1: a summary.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"why\",\"workers\":[\"copywriter\"]}",
		"empty string":    "",
		"a bare number":   42,
		"invalid cron":    "Charter v1: a summary.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"why\",\"architect_cron\":\"@daily\"}",
		"an empty object": map[string]any{},
	} {
		t.Run(name, func(t *testing.T) {
			res := charterValidateCall(t, arg)
			if res.Valid {
				t.Fatal("want invalid")
			}
			if len(res.Errors) == 0 {
				t.Fatal("invalid, but no errors to act on")
			}
		})
	}
}

// The object form: the interviewer holds a bare object before it has composed
// a deposit, and being told "wrong type" at the moment it is checking its work
// is the least useful possible answer.
func TestCharterValidateAcceptsABareObject(t *testing.T) {
	res := charterValidateCall(t, map[string]any{
		"goal":        "Send one newsletter a week.",
		"measure":     "Four in a month.",
		"label_rules": "kind=draft — written but not sent.",
		"rationale":   "Repeat visits are the problem.",
	})
	if !res.Valid {
		t.Fatalf("want valid, got %+v", res.Errors)
	}
	// No summary line was supplied, so none is echoed — and that is not an
	// error, because the deposit has not been composed yet.
	if res.Charter == nil || res.Charter.Summary != "" {
		t.Errorf("want an empty echoed summary line, got %+v", res.Charter)
	}
	if res.Charter.Goal != "Send one newsletter a week." {
		t.Errorf("echoed goal: got %q", res.Charter.Goal)
	}
}

// The tool cannot write, and this is stronger than counting config events
// after a call: charterTools has no fields, so there is no store, no runner
// and no seam of any kind through which a write could reach the database.
// A row count would only prove that this call did not write.
func TestCharterValidateHasNoWriteSeam(t *testing.T) {
	typ := reflect.TypeOf(charterTools{})
	if n := typ.NumField(); n != 0 {
		t.Errorf("charterTools has %d field(s); it must hold no store, so that it cannot write", n)
	}
	tools := newCharterTools().tools()
	if len(tools) != 1 {
		t.Fatalf("want exactly one tool, got %d", len(tools))
	}
	if tools[0].Name != "charter_validate" {
		t.Errorf("tool name: got %q", tools[0].Name)
	}
	if !strings.Contains(tools[0].Description, "writes NOTHING") {
		t.Error("the description must tell the model the tool does not write")
	}
}

// Reachable from an HTTP-created chat session — which is the ONLY kind of
// session that will ever call it. The interview runs as a chat with
// persona:"interviewer" and no worker identity (Decision A7), so a tool that
// needed a worker on the session row would be unreachable from the one place
// it is used.
func TestCharterValidateOverHTTPFromAChatSessionWithNoWorker(t *testing.T) {
	secret := []byte("test-secret")
	sessions := &fakeSessionLookup{sessions: map[string]*agentdb.Session{
		"onboard-1": {ID: "onboard-1", Customer: "acme"}, // no Worker: a chat
	}}
	srv := newMCPServer(coreMCPServerName, newSessionTokenAuth(secret, sessions).authenticate)
	srv.register(newCharterTools().tools()...)
	token := mintSessionToken(t, secret, time.Hour, "acme", "onboard-1")

	rec, res := rpc(t, srv, token, "tools/list", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list: %d %s", rec.Code, rec.Body.String())
	}
	result, _ := res["result"].(map[string]any)
	list, _ := result["tools"].([]any)
	found := false
	for _, entry := range list {
		if m, _ := entry.(map[string]any); m["name"] == "charter_validate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("charter_validate absent from tools/list: %#v", list)
	}

	_, res = rpc(t, srv, token, "tools/call", map[string]any{
		"name":      "charter_validate",
		"arguments": map[string]any{"charter": goodDeposit},
	})
	result, _ = res["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("tools/call from a worker-less chat session: %#v", result)
	}
}

// ---------------------------------------------------------------------------
// T12 — the shared-verdict test.
//
// Decision A5 says the MCP validator and the apply route share the gate's code
// exactly. "Exactly" is the load-bearing word: an interviewer told its charter
// is fine, whose charter is then refused on approval, has no way to find out
// why — it is not in the conversation where the refusal happens. So the two
// paths are driven with the same charters and their verdicts compared.
//
// This drives the REAL httpapi handler, not a re-implementation of it, because
// a test that re-implemented the route would agree with itself by construction.
// ---------------------------------------------------------------------------

type verdictMemories struct{ content string }

func (v *verdictMemories) SearchMemories(context.Context, *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error) {
	return nil, nil
}

func (v *verdictMemories) CreateMemory(_ context.Context, m *agentdb.Memory, _ []float32) (*agentdb.Memory, bool, error) {
	return m, false, nil
}

func (v *verdictMemories) GetMemory(_ context.Context, _, _ string) (*agentdb.Memory, error) {
	return v.row(), nil
}

func (v *verdictMemories) NewestMemory(_ context.Context, _, _ string) (*agentdb.Memory, error) {
	return v.row(), nil
}

func (v *verdictMemories) row() *agentdb.Memory {
	return &agentdb.Memory{
		ID:      "mem-1",
		Project: "acme",
		Labels:  agentdb.LabelSet{"kind": charter.MemoryKindCharter, "name": "onboard-1"},
		Content: v.content,
	}
}

// verdictTopologies accepts anything: this test compares VERDICTS, and a store
// refusal is a different question (T11 covers it).
type verdictTopologies struct{ applies int }

func (v *verdictTopologies) ListWorkers(context.Context, string) ([]*agentdb.Worker, error) {
	return nil, nil
}

func (v *verdictTopologies) GetProjectSettings(_ context.Context, project string) (*agentdb.ProjectSettings, error) {
	return agentdb.DefaultProjectSettings(project), nil
}

func (v *verdictTopologies) ResolveCustomImage(_ context.Context, _, ref string) (*agentdb.CustomImage, error) {
	return nil, fmt.Errorf("%w: %s", agentdb.ErrCustomImageNotFound, ref)
}

func (v *verdictTopologies) GetProjectSkill(_ context.Context, _, name string) (*agentdb.Skill, error) {
	return nil, fmt.Errorf("%w: %s", agentdb.ErrSkillNotFound, name)
}

func (v *verdictTopologies) ApplyTopology(_ context.Context, app agentdb.TopologyApplication, _ agentdb.ConfigWrite) (*agentdb.TopologyApplyResult, error) {
	v.applies++
	return &agentdb.TopologyApplyResult{
		Workers: app.Workers,
		Event:   &agentdb.ConfigEvent{ID: "ce-1", Action: agentdb.ActionTopologyApply},
	}, nil
}

// issueSet reduces a verdict to what the two paths must agree on: which fields
// are wrong. Messages are prose and may legitimately differ in framing; the
// set of addressable paths may not.
func issueSet(issues []charter.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Path)
	}
	sort.Strings(out)
	return out
}

func TestCharterValidateAndApplyAgree(t *testing.T) {
	const valid = `Charter v1: a weekly newsletter.
{"goal":"Send one a week.","measure":"Four in a month.","label_rules":"kind=draft.","rationale":"Repeat visits."}`

	cases := map[string]struct {
		deposit   string
		wantValid bool
	}{
		"valid":                     {valid, true},
		"missing goal":              {"S.\n{\"goal\":\"\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"w\"}", false},
		"missing measure":           {"S.\n{\"goal\":\"g\",\"measure\":\"\",\"label_rules\":\"r\",\"rationale\":\"w\"}", false},
		"missing label_rules":       {"S.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"\",\"rationale\":\"w\"}", false},
		"missing rationale":         {"S.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"\"}", false},
		"bad architect_name":        {"S.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"w\",\"architect_name\":\"Not A Name\"}", false},
		"bad architect_cron":        {"S.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"w\",\"architect_cron\":\"@daily\"}", false},
		"every field empty at once": {"S.\n{\"goal\":\"\",\"measure\":\"\",\"label_rules\":\"\",\"rationale\":\"\",\"architect_cron\":\"nope\"}", false},
		"unknown key":               {"S.\n{\"goal\":\"g\",\"measure\":\"m\",\"label_rules\":\"r\",\"rationale\":\"w\",\"workers\":[]}", false},
		"no JSON body":              {"S.", false},
		"empty deposit":             {"", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			toolValid, toolIssues := verdictFromTool(t, tc.deposit)
			routeValid, routeIssues := verdictFromRoute(t, tc.deposit)

			if toolValid != tc.wantValid {
				t.Errorf("charter_validate says valid=%v, want %v (issues %+v)", toolValid, tc.wantValid, toolIssues)
			}
			if toolValid != routeValid {
				t.Fatalf("the two paths disagree: charter_validate valid=%v, apply valid=%v\n  tool:  %+v\n  route: %+v",
					toolValid, routeValid, toolIssues, routeIssues)
			}
			if got, want := issueSet(routeIssues), issueSet(toolIssues); !reflect.DeepEqual(got, want) {
				t.Errorf("the two paths report different fields:\n  charter_validate: %v\n  apply:            %v", want, got)
			}
		})
	}
}

func verdictFromTool(t *testing.T, deposit string) (bool, []charter.Issue) {
	t.Helper()
	res := charterValidateCall(t, deposit)
	return res.Valid, res.Errors
}

// verdictFromRoute drives httpapi.Handlers.ApplyCharter — the real route.
func verdictFromRoute(t *testing.T, deposit string) (bool, []charter.Issue) {
	t.Helper()
	h, err := httpapi.New(httpapi.Config{
		// The real handler, with the runner and session stubs the router
		// tests already use — a re-implementation of the route would agree
		// with itself by construction and prove nothing.
		Runner:     &stubRunner{},
		Store:      newFakeRouterStore(),
		Memories:   &verdictMemories{content: deposit},
		Topologies: &verdictTopologies{},
		Identity: func(*http.Request) (httpapi.Identity, error) {
			return httpapi.Identity{Customer: "acme"}, nil
		},
	})
	if err != nil {
		t.Fatalf("httpapi.New: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ApplyCharter(rec, httptest.NewRequest(http.MethodPost, "/agent/charter/apply",
		strings.NewReader(`{"session":"onboard-1"}`)))

	switch rec.Code {
	case http.StatusOK:
		return true, nil
	case http.StatusUnprocessableEntity:
		var body struct {
			Errors []charter.Issue `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode 422 body %q: %v", rec.Body.String(), err)
		}
		return false, body.Errors
	default:
		t.Fatalf("apply returned %d, which is neither a verdict nor a refusal this test knows: %s",
			rec.Code, rec.Body.String())
		return false, nil
	}
}
