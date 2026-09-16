package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// fakeUsageStore records every `since` it was called with (in call order:
// today, last_7d, last_30d) and answers a canned agentdb.Usage per call.
type fakeUsageStore struct {
	sinces []int64
	byCall []agentdb.Usage // indexed by call order; zero value if short
	err    error
}

func (f *fakeUsageStore) GetProjectUsageSince(_ context.Context, _ string, since int64) (agentdb.Usage, error) {
	i := len(f.sinces)
	f.sinces = append(f.sinces, since)
	if f.err != nil {
		return agentdb.Usage{}, f.err
	}
	if i < len(f.byCall) {
		return f.byCall[i], nil
	}
	return agentdb.Usage{}, nil
}

func newUsageHandlers(t *testing.T, store UsageStore, settings ProjectSettingsStore, identity IdentityFunc, opts ...func(*Config)) *Handlers {
	t.Helper()
	cfg := Config{
		Runner:          stubRunner{},
		Store:           stubStore{},
		Identity:        identity,
		Usage:           store,
		ProjectSettings: settings,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return newHandlers(t, cfg)
}

// TestGetUsage_ComputesThreeWindowsFromStackLocalMidnight is the boundary the
// route's contract (§1.4) singles out: "today" is anchored to stack-local
// midnight in the SAME time.Location the router's tokenBudget uses, and
// last_7d/last_30d are rolling windows from now — not also midnight-anchored.
func TestGetUsage_ComputesThreeWindowsFromStackLocalMidnight(t *testing.T) {
	loc := time.UTC
	fixedNow := time.Date(2026, 9, 11, 14, 30, 0, 0, loc) // mid-afternoon UTC
	store := &fakeUsageStore{}
	h := newUsageHandlers(t, store, newFakeProjectSettings(), identityFor("acme"), func(c *Config) {
		c.UsageLocation = loc
		c.UsageNow = func() time.Time { return fixedNow }
	})

	rec := do(h, http.MethodGet, "/agent/usage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if len(store.sinces) != 3 {
		t.Fatalf("want 3 store calls (today, last_7d, last_30d), got %d: %v", len(store.sinces), store.sinces)
	}
	wantToday := time.Date(2026, 9, 11, 0, 0, 0, 0, loc).Unix()
	wantLast7 := fixedNow.AddDate(0, 0, -7).Unix()
	wantLast30 := fixedNow.AddDate(0, 0, -30).Unix()
	if store.sinces[0] != wantToday {
		t.Fatalf("today `since` = %d, want stack-local midnight %d", store.sinces[0], wantToday)
	}
	if store.sinces[1] != wantLast7 {
		t.Fatalf("last_7d `since` = %d, want %d (rolling from now, not midnight-anchored)", store.sinces[1], wantLast7)
	}
	if store.sinces[2] != wantLast30 {
		t.Fatalf("last_30d `since` = %d, want %d", store.sinces[2], wantLast30)
	}

	var body usageResponse
	decodeInto(t, rec, &body)
	if body.DayStartsAt != time.Date(2026, 9, 11, 0, 0, 0, 0, loc).UnixMilli() {
		t.Fatalf("day_starts_at = %d, want stack-local midnight in milliseconds", body.DayStartsAt)
	}
}

// TestGetUsage_ResponseShape byte-pins the wire shape the way the core
// preamble is pinned — A5 builds the console panel against these exact field
// names.
func TestGetUsage_ResponseShape(t *testing.T) {
	loc := time.UTC
	fixedNow := time.Date(2026, 9, 11, 0, 0, 0, 0, loc)
	store := &fakeUsageStore{byCall: []agentdb.Usage{
		{InputTokens: 100, OutputTokens: 20, CostUSD: 0.01, Queries: 3},
		{InputTokens: 500, OutputTokens: 90, CostUSD: 0.05, Queries: 12},
		{InputTokens: 900, OutputTokens: 150, CostUSD: 0.09, Queries: 40},
	}}
	settings := newFakeProjectSettings()
	settings.rows["acme"] = &agentdb.ProjectSettings{Project: "acme", DailyTokensSoft: 1_000_000, DailyTokensHard: 4_000_000}
	h := newUsageHandlers(t, store, settings, identityFor("acme"), func(c *Config) {
		c.UsageLocation = loc
		c.UsageNow = func() time.Time { return fixedNow }
		c.CredentialMode = "api-key"
	})

	rec := do(h, http.MethodGet, "/agent/usage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	got := rec.Body.String()
	want := `{"project":"acme","day_starts_at":1789084800000,` +
		`"today":{"input_tokens":100,"output_tokens":20,"cost_usd":0.01,"queries":3},` +
		`"last_7d":{"input_tokens":500,"output_tokens":90,"cost_usd":0.05,"queries":12},` +
		`"last_30d":{"input_tokens":900,"output_tokens":150,"cost_usd":0.09,"queries":40},` +
		`"budget":{"daily_tokens_soft":1000000,"daily_tokens_hard":4000000},` +
		`"credential_mode":"api-key","cost_known":true}` + "\n"
	if got != want {
		t.Fatalf("response shape:\n got  %s\n want %s", got, want)
	}
}

// TestGetUsage_CostKnownFalseWhenTokensSpentButCostZero: a transport that
// never wrote totalCostUsd (or wrote it as 0) must not be reported as free.
func TestGetUsage_CostKnownFalseWhenTokensSpentButCostZero(t *testing.T) {
	store := &fakeUsageStore{byCall: []agentdb.Usage{
		{InputTokens: 100, OutputTokens: 20, CostUSD: 0}, // today: tokens but no cost
		{InputTokens: 100, OutputTokens: 20, CostUSD: 0},
		{InputTokens: 100, OutputTokens: 20, CostUSD: 0},
	}}
	h := newUsageHandlers(t, store, newFakeProjectSettings(), identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/usage", "")
	var body usageResponse
	decodeInto(t, rec, &body)
	if body.CostKnown {
		t.Fatalf("cost_known must be false when tokens > 0 and cost sums to zero")
	}
}

// TestGetUsage_CostKnownTrueForGenuinelyFreeProject: zero tokens and zero cost
// together is an unspent project, not an unknown-cost transport.
func TestGetUsage_CostKnownTrueForGenuinelyFreeProject(t *testing.T) {
	h := newUsageHandlers(t, &fakeUsageStore{}, newFakeProjectSettings(), identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/usage", "")
	var body usageResponse
	decodeInto(t, rec, &body)
	if !body.CostKnown {
		t.Fatalf("cost_known must be true for a project with no tokens spent at all")
	}
}

// TestGetUsage_AuthAndAvailability mirrors the other optional read routes: 401
// without identity, 403 for a token carrying no project, 501 when the host
// wired no store.
func TestGetUsage_AuthAndAvailability(t *testing.T) {
	t.Run("401 without identity", func(t *testing.T) {
		h := newUsageHandlers(t, &fakeUsageStore{}, newFakeProjectSettings(),
			func(*http.Request) (Identity, error) { return Identity{}, http.ErrNoCookie })
		if rec := do(h, http.MethodGet, "/agent/usage", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
	t.Run("403 with no project in token", func(t *testing.T) {
		h := newUsageHandlers(t, &fakeUsageStore{}, newFakeProjectSettings(), identityFor(""))
		if rec := do(h, http.MethodGet, "/agent/usage", ""); rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body)
		}
	})
	t.Run("501 with no store", func(t *testing.T) {
		h := newUsageHandlers(t, nil, nil, identityFor("acme"))
		if rec := do(h, http.MethodGet, "/agent/usage", ""); rec.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501 (body %s)", rec.Code, rec.Body)
		}
	})
}

// TestGetUsage_BudgetDefaultsWhenSettingsUnconfigured: a host with no
// ProjectSettings seam wired still answers 200 with a zero-value (off) budget
// rather than 500ing or 501ing — the budget is a courtesy on this route, not
// its reason to exist.
func TestGetUsage_BudgetDefaultsWhenSettingsUnconfigured(t *testing.T) {
	h := newUsageHandlers(t, &fakeUsageStore{}, nil, identityFor("acme"))
	rec := do(h, http.MethodGet, "/agent/usage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var body usageResponse
	decodeInto(t, rec, &body)
	if body.Budget.DailyTokensSoft != 0 || body.Budget.DailyTokensHard != 0 {
		t.Fatalf("want the zero (off) budget with no ProjectSettings seam, got %+v", body.Budget)
	}
}
