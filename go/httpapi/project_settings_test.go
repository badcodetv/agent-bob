package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// fakeProjectSettingsStore is a project-keyed in-memory stand-in for
// *agentdb.Store (whose real migrations need Postgres). It mirrors the store
// contract the handlers rely on: unknown project → defaults, whole-object write.
type fakeProjectSettingsStore struct {
	rows    map[string]*agentdb.ProjectSettings
	getErr  error
	putErr  error
	lastGet string
	lastPut *agentdb.ProjectSettings
	// The config-log actor the handler passed down, and how many writes it made.
	lastWrite agentdb.ConfigWrite
	puts      int
}

func newFakeProjectSettings() *fakeProjectSettingsStore {
	return &fakeProjectSettingsStore{rows: map[string]*agentdb.ProjectSettings{}}
}

func (f *fakeProjectSettingsStore) GetProjectSettings(_ context.Context, project string) (*agentdb.ProjectSettings, error) {
	f.lastGet = project
	if f.getErr != nil {
		return nil, f.getErr
	}
	if ps, ok := f.rows[project]; ok {
		return ps, nil
	}
	return agentdb.DefaultProjectSettings(project), nil
}

func (f *fakeProjectSettingsStore) PutProjectSettings(_ context.Context, ps *agentdb.ProjectSettings, cw agentdb.ConfigWrite) (*agentdb.ProjectSettings, error) {
	f.lastPut = ps
	f.lastWrite = cw
	f.puts++
	if f.putErr != nil {
		return nil, f.putErr
	}
	stored := *ps
	stored.UpdatedAt = 1700000000
	f.rows[ps.Project] = &stored
	return &stored, nil
}

// identityFor builds an IdentityFunc pinned to one project (the customer
// claim). Operator: true — most tests across this package are not about the
// §1.2 budget/cap guard, and treating the default test identity as the
// operator (matching the wildcard/test login, which is always one) keeps
// them from tripping over PutProjectSettings' 403. Tests that specifically
// exercise the guard use identityForOperator with an explicit bool instead.
func identityFor(customer string) IdentityFunc {
	return func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "u@" + customer + ".com", Customer: customer, Operator: true}, nil
	}
}

// identityForOperator is identityFor plus Identity.Operator (§1.2's guard).
func identityForOperator(customer string, operator bool) IdentityFunc {
	return func(*http.Request) (Identity, error) {
		return Identity{UserEmail: "u@" + customer + ".com", Customer: customer, Operator: operator}, nil
	}
}

func newProjectSettingsHandlers(t *testing.T, store ProjectSettingsStore, identity IdentityFunc) *Handlers {
	t.Helper()
	return newHandlers(t, Config{
		Runner:          stubRunner{},
		Store:           stubStore{},
		Identity:        identity,
		ProjectSettings: store,
	})
}

func doProjectSettings(h *Handlers, method, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/agent/project-settings", nil)
	} else {
		r = httptest.NewRequest(method, "/agent/project-settings", strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	h.Mux().ServeHTTP(rec, r)
	return rec
}

func decodeProjectSettings(t *testing.T, rec *httptest.ResponseRecorder) *agentdb.ProjectSettings {
	t.Helper()
	var out agentdb.ProjectSettings
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body, err)
	}
	return &out
}

// GET on a project nobody has configured returns the §5 defaults over the wire.
func TestProjectSettingsGetReturnsDefaults(t *testing.T) {
	store := newFakeProjectSettings()
	h := newProjectSettingsHandlers(t, store, identityFor("acme"))

	rec := doProjectSettings(h, "GET", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	got := decodeProjectSettings(t, rec)
	if got.Project != "acme" {
		t.Fatalf("project must come from the JWT, got %q", got.Project)
	}
	if got.MaxConcurrentJobs != 4 || got.BriefingMaxBytes != 2048 || got.SnapshotTTLDays != 30 {
		t.Fatalf("defaults: want 4/2048/30, got %d/%d/%d",
			got.MaxConcurrentJobs, got.BriefingMaxBytes, got.SnapshotTTLDays)
	}
	if store.lastGet != "acme" {
		t.Fatalf("store queried for %q, want acme", store.lastGet)
	}
}

// PUT round-trips the whole object, including the four §5 budget/cap columns.
func TestProjectSettingsPutRoundTrip(t *testing.T) {
	store := newFakeProjectSettings()
	h := newProjectSettingsHandlers(t, store, identityFor("acme"))

	body := `{
		"base_image": "acme/base:v2",
		"system_prompt": "be excellent",
		"mcp_config": {"gmail": {"url": "http://gmail-mcp:9000"}},
		"attention_channel": {"kind": "webhook", "url": "https://hooks.example/x"},
		"max_concurrent_jobs": 6,
		"daily_tokens_soft": 1000,
		"daily_tokens_hard": 2000,
		"briefing_max_bytes": 4096,
		"snapshot_ttl_days": 0
	}`
	rec := doProjectSettings(h, "PUT", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	got := decodeProjectSettings(t, rec)
	if got.BaseImage != "acme/base:v2" || got.SystemPrompt != "be excellent" {
		t.Fatalf("image/prompt: %+v", got)
	}
	if got.MaxConcurrentJobs != 6 || got.DailyTokensSoft != 1000 || got.DailyTokensHard != 2000 ||
		got.BriefingMaxBytes != 4096 || got.SnapshotTTLDays != 0 {
		t.Fatalf("budget/cap columns: %+v", got)
	}
	if got.MCPConfig["gmail"] == nil || got.AttentionChannel["kind"] != "webhook" {
		t.Fatalf("json columns: %+v", got)
	}

	// And it is readable back through GET.
	rec = doProjectSettings(h, "GET", "")
	if back := decodeProjectSettings(t, rec); back.SystemPrompt != "be excellent" {
		t.Fatalf("GET after PUT: %+v", back)
	}
}

// The project-wide briefing (B1) rides the same whole-object PUT/GET as every
// other field — no separate route, because ProjectSettings is embedded wholesale
// in the wire body (project_settings.go's projectSettingsBody).
func TestProjectSettingsBriefingRoundTrip(t *testing.T) {
	store := newFakeProjectSettings()
	h := newProjectSettingsHandlers(t, store, identityFor("acme"))

	rec := doProjectSettings(h, "PUT", `{"briefing": ["name=label-registry", "kind=house-style"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	got := decodeProjectSettings(t, rec)
	if len(got.Briefing) != 2 || got.Briefing[0] != "name=label-registry" || got.Briefing[1] != "kind=house-style" {
		t.Fatalf("briefing = %#v", got.Briefing)
	}

	rec = doProjectSettings(h, "GET", "")
	if back := decodeProjectSettings(t, rec); len(back.Briefing) != 2 {
		t.Fatalf("GET after PUT: briefing = %#v", back.Briefing)
	}
}

// The caller may not choose its own project: a body-supplied `project` (or an
// updated_at) is overwritten by the JWT-derived scope before the store sees it.
func TestProjectSettingsPutIgnoresBodyProject(t *testing.T) {
	store := newFakeProjectSettings()
	h := newProjectSettingsHandlers(t, store, identityFor("acme"))

	rec := doProjectSettings(h, "PUT", `{"project":"victim","updated_at":99,"system_prompt":"mine"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if store.lastPut.Project != "acme" {
		t.Fatalf("store must be written under the JWT project, got %q", store.lastPut.Project)
	}
	if store.lastPut.UpdatedAt != 0 {
		t.Fatalf("caller-supplied updated_at must be dropped, got %d", store.lastPut.UpdatedAt)
	}
	if _, leaked := store.rows["victim"]; leaked {
		t.Fatalf("body project created a row for another project: %+v", store.rows)
	}
}

// §12 project isolation: a token scoped to project X can neither read nor write
// project Y's settings — not via the body, and not by reading someone else's row.
func TestProjectSettingsProjectIsolation(t *testing.T) {
	store := newFakeProjectSettings()
	alpha := newProjectSettingsHandlers(t, store, identityFor("alpha"))
	beta := newProjectSettingsHandlers(t, store, identityFor("beta"))

	if rec := doProjectSettings(alpha, "PUT", `{"system_prompt":"alpha secrets","base_image":"alpha/base:v1"}`); rec.Code != http.StatusOK {
		t.Fatalf("alpha put: status=%d body=%s", rec.Code, rec.Body)
	}

	// Beta reads its own (default) settings — never alpha's.
	rec := doProjectSettings(beta, "GET", "")
	got := decodeProjectSettings(t, rec)
	if got.Project != "beta" {
		t.Fatalf("beta got project %q", got.Project)
	}
	if got.SystemPrompt != "" || got.BaseImage != "" {
		t.Fatalf("alpha's settings leaked to beta: %+v", got)
	}

	// Beta writing while naming alpha lands on beta's row; alpha is untouched.
	if rec := doProjectSettings(beta, "PUT", `{"project":"alpha","system_prompt":"beta wrote this"}`); rec.Code != http.StatusOK {
		t.Fatalf("beta put: status=%d body=%s", rec.Code, rec.Body)
	}
	if store.rows["alpha"].SystemPrompt != "alpha secrets" {
		t.Fatalf("beta's write reached alpha's row: %+v", store.rows["alpha"])
	}
	if store.rows["beta"].SystemPrompt != "beta wrote this" {
		t.Fatalf("beta's own row: %+v", store.rows["beta"])
	}

	// And alpha still reads what alpha wrote.
	if back := decodeProjectSettings(t, doProjectSettings(alpha, "GET", "")); back.SystemPrompt != "alpha secrets" {
		t.Fatalf("alpha read back: %+v", back)
	}
}

// TestProjectSettingsOperatorGuard pins onboarding-work-plan §1.2's 403
// matrix: a non-operator identity may PUT freely as long as the three
// budget/cap fields echo back what is already stored; changing any of them
// without Identity.Operator is refused with the exact body operatorOnlyMessage
// names. An operator may change all three.
func TestProjectSettingsOperatorGuard(t *testing.T) {
	baseBody := func(soft, hard, maxJobs int, prompt string) string {
		return fmt.Sprintf(`{"system_prompt":%q,"daily_tokens_soft":%d,"daily_tokens_hard":%d,"max_concurrent_jobs":%d}`,
			prompt, soft, hard, maxJobs)
	}

	t.Run("operator establishes a budget on a brand-new project", func(t *testing.T) {
		store := newFakeProjectSettings()
		h := newProjectSettingsHandlers(t, store, identityForOperator("acme", true))
		rec := doProjectSettings(h, "PUT", baseBody(1000, 2000, 6, "be excellent"))
		if rec.Code != http.StatusOK {
			t.Fatalf("operator PUT: status=%d body=%s", rec.Code, rec.Body)
		}
		got := decodeProjectSettings(t, rec)
		if got.DailyTokensSoft != 1000 || got.DailyTokensHard != 2000 || got.MaxConcurrentJobs != 6 {
			t.Fatalf("budgets not written: %+v", got)
		}
	})

	t.Run("non-operator may change the prompt while echoing the stored budgets back unchanged", func(t *testing.T) {
		store := newFakeProjectSettings()
		op := newProjectSettingsHandlers(t, store, identityForOperator("acme", true))
		if rec := doProjectSettings(op, "PUT", baseBody(1000, 2000, 6, "v1")); rec.Code != http.StatusOK {
			t.Fatalf("seed PUT: status=%d body=%s", rec.Code, rec.Body)
		}

		nonOp := newProjectSettingsHandlers(t, store, identityForOperator("acme", false))
		rec := doProjectSettings(nonOp, "PUT", baseBody(1000, 2000, 6, "v2 — new prompt"))
		if rec.Code != http.StatusOK {
			t.Fatalf("non-operator prompt-only PUT: status=%d body=%s", rec.Code, rec.Body)
		}
		got := decodeProjectSettings(t, rec)
		if got.SystemPrompt != "v2 — new prompt" {
			t.Fatalf("prompt not written: %+v", got)
		}
	})

	t.Run("non-operator changing daily_tokens_soft is refused", func(t *testing.T) {
		store := newFakeProjectSettings()
		op := newProjectSettingsHandlers(t, store, identityForOperator("acme", true))
		doProjectSettings(op, "PUT", baseBody(1000, 2000, 6, "v1"))

		nonOp := newProjectSettingsHandlers(t, store, identityForOperator("acme", false))
		rec := doProjectSettings(nonOp, "PUT", baseBody(1500, 2000, 6, "v1"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != operatorOnlyMessage {
			t.Fatalf("body = %q, want %q", got, operatorOnlyMessage)
		}
		if store.puts != 1 {
			t.Fatalf("refused PUT must not reach the store: puts=%d", store.puts)
		}
	})

	t.Run("non-operator changing daily_tokens_hard is refused", func(t *testing.T) {
		store := newFakeProjectSettings()
		op := newProjectSettingsHandlers(t, store, identityForOperator("acme", true))
		doProjectSettings(op, "PUT", baseBody(1000, 2000, 6, "v1"))

		nonOp := newProjectSettingsHandlers(t, store, identityForOperator("acme", false))
		rec := doProjectSettings(nonOp, "PUT", baseBody(1000, 2500, 6, "v1"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body)
		}
	})

	t.Run("non-operator changing max_concurrent_jobs is refused", func(t *testing.T) {
		store := newFakeProjectSettings()
		op := newProjectSettingsHandlers(t, store, identityForOperator("acme", true))
		doProjectSettings(op, "PUT", baseBody(1000, 2000, 6, "v1"))

		nonOp := newProjectSettingsHandlers(t, store, identityForOperator("acme", false))
		rec := doProjectSettings(nonOp, "PUT", baseBody(1000, 2000, 8, "v1"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body)
		}
	})

	t.Run("operator may change all three budget/cap fields", func(t *testing.T) {
		store := newFakeProjectSettings()
		op := newProjectSettingsHandlers(t, store, identityForOperator("acme", true))
		doProjectSettings(op, "PUT", baseBody(1000, 2000, 6, "v1"))

		rec := doProjectSettings(op, "PUT", baseBody(1500, 2500, 8, "v1"))
		if rec.Code != http.StatusOK {
			t.Fatalf("operator PUT: status=%d body=%s", rec.Code, rec.Body)
		}
		got := decodeProjectSettings(t, rec)
		if got.DailyTokensSoft != 1500 || got.DailyTokensHard != 2500 || got.MaxConcurrentJobs != 8 {
			t.Fatalf("budgets not updated: %+v", got)
		}
	})

	t.Run("non-operator PUT on an untouched project is refused if it omits the env-default budget", func(t *testing.T) {
		// GetProjectSettings answers DefaultProjectSettings for an unwritten
		// project; if that default carries a non-zero budget (§1.3), a
		// non-operator body that omits the fields (JSON zero value) reads as
		// a change from the default and is refused, same as any other diff.
		if err := agentdb.SetDefaultBudgets(777, 999); err != nil {
			t.Fatalf("SetDefaultBudgets: %v", err)
		}
		t.Cleanup(func() {
			if err := agentdb.SetDefaultBudgets(0, 0); err != nil {
				t.Fatalf("restore SetDefaultBudgets: %v", err)
			}
		})

		store := newFakeProjectSettings()
		nonOp := newProjectSettingsHandlers(t, store, identityForOperator("brandnew", false))
		rec := doProjectSettings(nonOp, "PUT", `{"system_prompt":"hello"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s, want 403 (body omits the 777/999 default)", rec.Code, rec.Body)
		}
	})
}

func TestProjectSettingsErrorPaths(t *testing.T) {
	invalid := fmt.Errorf("%w: briefing_max_bytes must not be negative", agentdb.ErrInvalidProjectSettings)

	tests := []struct {
		name     string
		method   string
		body     string
		store    ProjectSettingsStore
		identity IdentityFunc
		want     int
	}{
		{
			name: "unauthenticated GET is 401", method: "GET",
			store:    newFakeProjectSettings(),
			identity: func(*http.Request) (Identity, error) { return Identity{}, errors.New("no token") },
			want:     http.StatusUnauthorized,
		},
		{
			name: "unauthenticated PUT is 401", method: "PUT", body: `{}`,
			store:    newFakeProjectSettings(),
			identity: func(*http.Request) (Identity, error) { return Identity{}, errors.New("no token") },
			want:     http.StatusUnauthorized,
		},
		{
			name: "no store configured is 501", method: "GET",
			store: nil, identity: identityFor("acme"), want: http.StatusNotImplemented,
		},
		{
			name: "identity without a project is 400", method: "GET",
			store:    newFakeProjectSettings(),
			identity: func(*http.Request) (Identity, error) { return Identity{UserEmail: "u@x.com"}, nil },
			want:     http.StatusBadRequest,
		},
		{
			name: "malformed body is 400", method: "PUT", body: `{not json`,
			store: newFakeProjectSettings(), identity: identityFor("acme"), want: http.StatusBadRequest,
		},
		{
			name: "store validation error is 400", method: "PUT", body: `{"briefing_max_bytes":-1}`,
			store:    &fakeProjectSettingsStore{rows: map[string]*agentdb.ProjectSettings{}, putErr: invalid},
			identity: identityFor("acme"), want: http.StatusBadRequest,
		},
		{
			name: "store failure is 500", method: "GET",
			store:    &fakeProjectSettingsStore{rows: map[string]*agentdb.ProjectSettings{}, getErr: errors.New("db down")},
			identity: identityFor("acme"), want: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectSettingsHandlers(t, tc.store, tc.identity)
			rec := doProjectSettings(h, tc.method, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// New() adopts AgentDB as the project-settings store so agentd gets the routes
// for free; with neither set the routes answer 501 rather than panicking.
func TestProjectSettingsStoreDefaultsToAgentDB(t *testing.T) {
	h := newHandlers(t, Config{
		Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity,
	})
	if h.cfg.ProjectSettings != nil {
		t.Fatalf("expected no project settings store when AgentDB is nil")
	}
	if rec := doProjectSettings(h, "GET", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("want 501 without a store, got %d", rec.Code)
	}

	var db *agentdb.Store // typed nil must not become a non-nil interface
	h = newHandlers(t, Config{
		Runner: stubRunner{}, Store: stubStore{}, Identity: okIdentity, AgentDB: db,
	})
	if h.cfg.ProjectSettings != nil {
		t.Fatalf("a nil *agentdb.Store must not be adopted as the store")
	}
}

// The routes are mounted on the canonical paths (GET + PUT, same URL).
func TestProjectSettingsRoutesAreMounted(t *testing.T) {
	if DefaultEndpoints.GetProjectSettings != "GET /agent/project-settings" ||
		DefaultEndpoints.PutProjectSettings != "PUT /agent/project-settings" {
		t.Fatalf("unexpected default routes: %q / %q",
			DefaultEndpoints.GetProjectSettings, DefaultEndpoints.PutProjectSettings)
	}
	h := newProjectSettingsHandlers(t, newFakeProjectSettings(), identityFor("acme"))
	// A method with no registered handler on that path is 405, proving the
	// pattern (not a catch-all) is what matched GET/PUT above.
	rec := doProjectSettings(h, "DELETE", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: want 405, got %d body=%s", rec.Code, rec.Body)
	}
}
