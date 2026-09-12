package agentdb

// GetProjectUsageSince — the read behind GET /agent/usage (ticket A4,
// design/2026-09-11-onboarding-work-plan.md §1.4).
//
// It is deliberately NOT a third reader of the jsonb usage shape:
// usageInputSQL/usageOutputSQL and the usageEnvelopes join are the exact
// expressions CountProjectTokensSince (leases.go) uses for the router's
// budget gate. This file adds one more expression, usageCostSQL
// (token_usage.go), over the SAME join, because the route also reports a
// truthful cost and a query count that the budget gate never needed.

import (
	"context"
	"fmt"
)

// Usage is one time window's totals over a project's query events.
type Usage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	Queries      int64   `json:"queries"`
}

// GetProjectUsageSince sums a project's token usage, recorded cost and query
// count for agent_query_events created at or after `since` (unix SECONDS —
// the same unit CountProjectTokensSince takes, because agent_query_events'
// created_at column is gorm's default `autoCreateTime`, which is seconds).
//
// Queries counts DISTINCT qe.id: usageEnvelopes expands one agent_query_events
// row into one row per stored envelope, so a plain COUNT(*) over the same FROM
// clause would count envelopes, not queries.
//
// Postgres-only SQL (jsonb operators plus LATERAL), like every other reader of
// this shape — on a store without them the caller gets an error, and the HTTP
// route this backs answers 501 rather than pretending.
func (s *Store) GetProjectUsageSince(ctx context.Context, project string, since int64) (Usage, error) {
	if project == "" {
		return Usage{}, fmt.Errorf("project is required")
	}
	var row struct {
		InputTokens  int64
		OutputTokens int64
		CostUSD      float64
		Queries      int64
	}
	err := s.gdb.WithContext(ctx).Raw(`
		SELECT
			COALESCE(SUM(`+usageInputSQL+`), 0)  AS input_tokens,
			COALESCE(SUM(`+usageOutputSQL+`), 0) AS output_tokens,
			COALESCE(SUM(`+usageCostSQL+`), 0)   AS cost_usd,
			COUNT(DISTINCT qe.id)                AS queries
		FROM agent_query_events AS qe
		JOIN agent_sessions AS sess ON sess.id = qe.session_id
		`+usageEnvelopes("qe")+`
		WHERE sess.customer = ? AND qe.created_at >= ?`, project, since).
		Scan(&row).Error
	if err != nil {
		return Usage{}, fmt.Errorf("failed to get project usage: %w", err)
	}
	return Usage{
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		CostUSD:      row.CostUSD,
		Queries:      row.Queries,
	}, nil
}
