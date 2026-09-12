package httpapi

// The usage + budget read (ticket A4, design/2026-09-11-onboarding-work-plan.md
// §1.4) — the console's budget panel reads this route, and the field names
// here are the contract another ticket (A5) builds against, so do not rename
// them.
//
//	GET /agent/usage
//	  auth : the ordinary session JWT; the project comes from the Customer
//	         claim, never from the query (P5) — same posture as every other
//	         project-scoped read in this package.
//	  200  : {"project", "day_starts_at", "today", "last_7d", "last_30d",
//	          "budget", "credential_mode", "cost_known"}
//	  501  : usage is not configured on this host (no AgentDB — the sqlite
//	         fallback has no jsonb ledger to sum, same as the budget gate)
//
// Tokens and cost reuse the EXACT SQL agentdb.GetProjectUsageSince runs, which
// itself reuses the per-envelope expressions the router's budget gate reads
// (agentdb/token_usage.go) — this file computes no SQL of its own, only the
// three window boundaries and the wire shape.

import (
	"context"
	"net/http"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
)

// UsageStore is the slice of agentdb.Store GET /agent/usage needs.
type UsageStore interface {
	GetProjectUsageSince(ctx context.Context, project string, since int64) (agentdb.Usage, error)
}

var _ UsageStore = (*agentdb.Store)(nil)

// usageWindow is one of "today"/"last_7d"/"last_30d" on the wire — the same
// four fields agentdb.Usage carries, restated here so the wire shape is
// defined in this file rather than borrowed from a store-internal type.
type usageWindow struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	Queries      int64   `json:"queries"`
}

func windowFromUsage(u agentdb.Usage) usageWindow {
	return usageWindow{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CostUSD:      u.CostUSD,
		Queries:      u.Queries,
	}
}

// usageBudgetWire is the two operator-set ceilings (§1.2/§1.3), 0 = off.
type usageBudgetWire struct {
	DailyTokensSoft int64 `json:"daily_tokens_soft"`
	DailyTokensHard int64 `json:"daily_tokens_hard"`
}

type usageResponse struct {
	Project        string          `json:"project"`
	DayStartsAt    int64           `json:"day_starts_at"` // unix MILLISECONDS, like every other timestamp this package emits
	Today          usageWindow     `json:"today"`
	Last7d         usageWindow     `json:"last_7d"`
	Last30d        usageWindow     `json:"last_30d"`
	Budget         usageBudgetWire `json:"budget"`
	CredentialMode string          `json:"credential_mode"`
	CostKnown      bool            `json:"cost_known"`
}

// usageLocation is the stack-local zone "today" resets in. h.cfg.UsageLocation
// is wired from the SAME *time.Location the router's tokenBudget uses
// (cmd/agentd/main.go), so this route's midnight and the budget gate's
// midnight can never silently disagree; nil falls back to time.Local, the
// same default tokenBudget itself applies when unconfigured.
func (h *Handlers) usageLocation() *time.Location {
	if h.cfg.UsageLocation != nil {
		return h.cfg.UsageLocation
	}
	return time.Local
}

func (h *Handlers) usageNow() time.Time {
	if h.cfg.UsageNow != nil {
		return h.cfg.UsageNow()
	}
	return time.Now()
}

// GetUsage serves GET /agent/usage.
func (h *Handlers) GetUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.Usage == nil {
		http.Error(w, "usage is not configured on this host", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "no project in token", http.StatusForbidden)
		return
	}

	loc := h.usageLocation()
	now := h.usageNow().In(loc)
	// "today" starts at stack-local midnight — the exact rule
	// cmd/agentd/router.go's tokenBudget.Allow / startOfDay applies, over the
	// SAME Location, so the two can never disagree about when the day turns
	// over. last_7d/last_30d are rolling windows from now, not calendar-aligned.
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	ctx := r.Context()
	today, err := h.cfg.Usage.GetProjectUsageSince(ctx, id.Customer, dayStart.Unix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	last7, err := h.cfg.Usage.GetProjectUsageSince(ctx, id.Customer, now.AddDate(0, 0, -7).Unix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	last30, err := h.cfg.Usage.GetProjectUsageSince(ctx, id.Customer, now.AddDate(0, 0, -30).Unix())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var budget usageBudgetWire
	if h.cfg.ProjectSettings != nil {
		if settings, err := h.cfg.ProjectSettings.GetProjectSettings(ctx, id.Customer); err == nil && settings != nil {
			budget = usageBudgetWire{
				DailyTokensSoft: settings.DailyTokensSoft,
				DailyTokensHard: settings.DailyTokensHard,
			}
		}
	}

	// cost_known is false the moment ANY window shows tokens spent with zero
	// cost recorded against them — a transport that reports no cost, per
	// §1.4. Checked per-window rather than on one combined total: last_30d can
	// carry cost from envelopes outside last_7d/today, so a wider window's
	// non-zero cost does not prove a narrower window's zero was truthful.
	costKnown := true
	for _, wnd := range []agentdb.Usage{today, last7, last30} {
		if wnd.InputTokens+wnd.OutputTokens > 0 && wnd.CostUSD == 0 {
			costKnown = false
		}
	}

	writeJSON(w, usageResponse{
		Project:        id.Customer,
		DayStartsAt:    dayStart.UnixMilli(),
		Today:          windowFromUsage(today),
		Last7d:         windowFromUsage(last7),
		Last30d:        windowFromUsage(last30),
		Budget:         budget,
		CredentialMode: h.cfg.CredentialMode,
		CostKnown:      costKnown,
	})
}
