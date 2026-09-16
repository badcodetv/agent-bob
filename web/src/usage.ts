// usage — the browser's mirror of `GET /agent/usage`
// (design/2026-09-11-onboarding-work-plan.md §1.4, go/httpapi/usage.go).
//
// The wire shape is byte-pinned by `go/httpapi/usage_test.go`
// (TestGetUsage_ResponseShape) — the field names below are copied from that
// test, not paraphrased from the ticket prose, and must not drift from it.
//
// Two things this module gets right:
//
//  1. `cost_known: false` means "a transport reported tokens spent but no
//     cost" (a captured envelope with no `totalCostUsd`) — it is a statement
//     about the DATA, not about whether spend is zero, and `formatCost` must
//     never render it as `$0.00`.
//  2. The hairline bar's colour is a function of `today` against the two
//     operator-set ceilings, never of `error`/`loading` — `budgetTier` takes
//     plain numbers so it is testable without a fetch.
//
// Fetch + hook live here alongside the pure formatting, unlike the
// project-settings split (`projectSettings.ts` / `useProjectSettings.ts`):
// A5 asked for one file, and there is far less here to keep pure.

import { useCallback, useRef, useState } from 'react'
import { useConfigApi, type ConfigApiOptions } from './configApi.js'

/** Default endpoint for the usage route. */
export const USAGE_ENDPOINT = '/agent/usage'

/** One of `today` / `last_7d` / `last_30d`. */
export interface UsageWindow {
  input_tokens: number
  output_tokens: number
  cost_usd: number
  queries: number
}

/** The two operator-set ceilings (§1.2/§1.3 of the work plan). 0 = off. */
export interface UsageBudget {
  daily_tokens_soft: number
  daily_tokens_hard: number
}

/** `GET /agent/usage`'s whole response. */
export interface Usage {
  project: string
  /** Unix MILLISECONDS — stack-local midnight, the same zero `today` counts from. */
  day_starts_at: number
  today: UsageWindow
  last_7d: UsageWindow
  last_30d: UsageWindow
  budget: UsageBudget
  /** "api-key" | "subscription" | "mock", but carried as a plain string here
   *  (this module deliberately does not import CredentialModeBadge.tsx's
   *  narrower type — that is a component, and usage.ts must not depend on
   *  one). `credentialModeSentence` below handles the three known values and
   *  falls back to '' for anything else. */
  credential_mode: string
  cost_known: boolean
}

function zeroWindow(): UsageWindow {
  return { input_tokens: 0, output_tokens: 0, cost_usd: 0, queries: 0 }
}

/** A safe "nothing known yet" value — every count zero, cost known true (an
 *  unspent project, not a suspicious one), so a caller that renders before
 *  the first load completes shows zeros rather than a misleading dash. */
export function defaultUsage(): Usage {
  return {
    project: '',
    day_starts_at: 0,
    today: zeroWindow(),
    last_7d: zeroWindow(),
    last_30d: zeroWindow(),
    budget: { daily_tokens_soft: 0, daily_tokens_hard: 0 },
    credential_mode: '',
    cost_known: true,
  }
}

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

function coerceWindow(raw: unknown): UsageWindow {
  const r = (raw ?? {}) as Record<string, unknown>
  return {
    input_tokens: num(r.input_tokens),
    output_tokens: num(r.output_tokens),
    cost_usd: num(r.cost_usd),
    queries: num(r.queries),
  }
}

/** Tolerant coercion — an unexpected shape becomes the all-zero default
 *  rather than a thrown error, same posture as every other reader here. */
export function coerceUsage(raw: unknown): Usage {
  const r = (raw ?? {}) as Record<string, unknown>
  const budget = (r.budget ?? {}) as Record<string, unknown>
  return {
    project: typeof r.project === 'string' ? r.project : '',
    day_starts_at: num(r.day_starts_at),
    today: coerceWindow(r.today),
    last_7d: coerceWindow(r.last_7d),
    last_30d: coerceWindow(r.last_30d),
    budget: {
      daily_tokens_soft: num(budget.daily_tokens_soft),
      daily_tokens_hard: num(budget.daily_tokens_hard),
    },
    credential_mode: typeof r.credential_mode === 'string' ? r.credential_mode : '',
    cost_known: r.cost_known !== false,
  }
}

export interface UseUsageOptions extends ConfigApiOptions {
  /** Override the endpoint (default `/agent/usage`). */
  endpoint?: string
}

export interface UsageApi {
  usage: Usage
  loading: boolean
  /** Load failure, as the server phrased it — includes the 501 a sqlite
   *  deployment answers (§1.4: usage is Postgres-only). */
  error: string | null
  reload: () => Promise<void>
}

export default function useUsage(options: UseUsageOptions = {}): UsageApi {
  const { endpoint = USAGE_ENDPOINT } = options
  const { request } = useConfigApi(options)

  const [usage, setUsage] = useState<Usage>(() => defaultUsage())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      setUsage(coerceUsage(await request<unknown>(endpoint)))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to load usage')
    } finally {
      setLoading(false)
    }
  }, [endpoint, request])

  // Ref-guard rather than useEffect — see the note in useProjectSettings.ts.
  const didLoad = useRef(false)
  if (!didLoad.current) {
    didLoad.current = true
    void reload()
  }

  return { usage, loading, error, reload }
}

// ---------------------------------------------------------------------------
// Pure formatting — no React, no window, no fetch below this line.
// ---------------------------------------------------------------------------

// Thousands-separated token counts already have one definition
// (`events.ts`'s `formatTokens`, used by the token-usage strip elsewhere in
// this package) — re-exported here rather than duplicated, per this
// package's "one definition" convention. Not re-exported a THIRD time from
// `index.ts`, where `events.ts`'s copy already is.
export { formatTokens } from './events.js'
import { formatTokens as formatTokensLocal } from './events.js'

/** "$1.23", or "cost not reported" when the transport never recorded a cost
 *  for tokens it admits were spent (§1.4's `cost_known: false`) — never
 *  `$0.00`, which would tell an operator a paid call was free. */
export function formatCost(costUsd: number, costKnown: boolean): string {
  if (!costKnown) return 'cost not reported'
  return `$${costUsd.toFixed(2)}`
}

/**
 * One spend figure: `1,252,927 tokens · $1.78`. On the subscription the dollar
 * figure is left out and the tokens say why — `1,252,927 tokens (subscription
 * — not billed per token)` — because a dollar amount beside a subscription's
 * usage reads as a bill that will never arrive.
 */
export function formatSpend(tokens: number, costUsd: number, costKnown: boolean, credentialMode: string): string {
  if (credentialMode === 'subscription') return `${formatTokensLocal(tokens)} tokens (subscription — not billed per token)`
  return `${formatTokensLocal(tokens)} tokens · ${formatCost(costUsd, costKnown)}`
}

/** The credential-mode sentence, verbatim per the ticket (A5 scope): which of
 *  the three ways a session can be billed is in force. Unknown/empty renders
 *  '' — silence over a guess, same posture as CredentialModeBadge. */
export function credentialModeSentence(mode: string): string {
  switch (mode) {
    case 'api-key':
      return 'Billed to the API key'
    case 'subscription':
      return 'Running on the subscription — tokens are counted against its limits, not billed one by one'
    case 'mock':
      return 'Mock model — nothing is billed'
    default:
      return ''
  }
}

/** The hairline bar's tier: 'ok' draws no colour, 'soft' draws steel (over
 *  the soft tier, nothing has stopped), 'stopped' draws rose (a hard stop is
 *  in force — the router will not create new non-interactive jobs today).
 *  A limit of 0 means "off" (§1.3), so it can never itself be crossed. */
export type BudgetTier = 'ok' | 'soft' | 'stopped'

export function budgetTier(todayTokens: number, softLimit: number, hardLimit: number): BudgetTier {
  if (hardLimit > 0 && todayTokens >= hardLimit) return 'stopped'
  if (softLimit > 0 && todayTokens >= softLimit) return 'soft'
  return 'ok'
}

/** The bar's fill, 0..1. No hard limit means nothing to measure against —
 *  callers should hide the bar rather than render one that is always empty. */
export function budgetFraction(todayTokens: number, hardLimit: number): number {
  if (hardLimit <= 0) return 0
  return Math.min(1, Math.max(0, todayTokens / hardLimit))
}
