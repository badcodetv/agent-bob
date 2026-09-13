// @vitest-environment jsdom
// A5 — the budget panel's data source (design/2026-09-11-onboarding-work-plan.md
// §1.4). Field names below are copied from go/httpapi/usage_test.go's
// byte-pinned golden body, not paraphrased.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import useUsage, {
  budgetFraction,
  budgetTier,
  coerceUsage,
  credentialModeSentence,
  defaultUsage,
  formatCost,
  formatSpend,
  formatTokens,
} from './usage.js'

const GOLDEN = {
  project: 'acme',
  day_starts_at: 1789084800000,
  today: { input_tokens: 100, output_tokens: 20, cost_usd: 0.01, queries: 3 },
  last_7d: { input_tokens: 500, output_tokens: 90, cost_usd: 0.05, queries: 12 },
  last_30d: { input_tokens: 900, output_tokens: 150, cost_usd: 0.09, queries: 40 },
  budget: { daily_tokens_soft: 1_000_000, daily_tokens_hard: 4_000_000 },
  credential_mode: 'api-key',
  cost_known: true,
}

describe('coerceUsage', () => {
  it('reads the golden response shape straight through', () => {
    expect(coerceUsage(GOLDEN)).toEqual(GOLDEN)
  })

  it('falls back to the all-zero default on a wild shape, never throwing', () => {
    expect(coerceUsage(null)).toEqual(defaultUsage())
    expect(coerceUsage({ today: 'not an object', cost_known: 'yes' })).toEqual({
      ...defaultUsage(),
      cost_known: true, // only a literal `false` turns it off
    })
  })

  it('treats only a literal false as cost_known: false', () => {
    expect(coerceUsage({ cost_known: false }).cost_known).toBe(false)
  })
})

describe('formatTokens', () => {
  it('adds thousands separators', () => {
    expect(formatTokens(1234567)).toBe('1,234,567')
    expect(formatTokens(0)).toBe('0')
  })
})

describe('formatCost', () => {
  it('renders two decimals when cost is known', () => {
    expect(formatCost(1.5, true)).toBe('$1.50')
    expect(formatCost(0, true)).toBe('$0.00')
  })

  it('never renders $0.00 for an unknown cost — "cost not reported" instead', () => {
    expect(formatCost(0, false)).toBe('cost not reported')
    expect(formatCost(12.34, false)).toBe('cost not reported')
  })
})

describe('credentialModeSentence', () => {
  it('gives the exact three sentences the ticket specifies', () => {
    expect(credentialModeSentence('api-key')).toBe('Billed to the API key')
    expect(credentialModeSentence('subscription')).toBe(
      'Running on the subscription — tokens are counted against its limits, not billed one by one',
    )
    expect(credentialModeSentence('mock')).toBe('Mock model — nothing is billed')
  })

  it('answers silence for an unknown mode rather than guessing', () => {
    expect(credentialModeSentence('')).toBe('')
    expect(credentialModeSentence('something-new')).toBe('')
  })
})

describe('budgetTier', () => {
  it('is "ok" under both limits, and when both limits are off', () => {
    expect(budgetTier(500, 1000, 2000)).toBe('ok')
    expect(budgetTier(1_000_000, 0, 0)).toBe('ok')
  })

  it('is "soft" at or over the soft limit but under the hard limit', () => {
    expect(budgetTier(1000, 1000, 2000)).toBe('soft')
    expect(budgetTier(1500, 1000, 2000)).toBe('soft')
  })

  it('is "stopped" at or over the hard limit, regardless of the soft one', () => {
    expect(budgetTier(2000, 1000, 2000)).toBe('stopped')
    expect(budgetTier(5000, 0, 2000)).toBe('stopped')
  })

  it('a hard limit of 0 can never be reached', () => {
    expect(budgetTier(Number.MAX_SAFE_INTEGER, 1000, 0)).toBe('soft')
  })
})

describe('budgetFraction', () => {
  it('is the ratio of today to the hard limit, clamped to [0,1]', () => {
    expect(budgetFraction(500, 1000)).toBe(0.5)
    expect(budgetFraction(2000, 1000)).toBe(1)
    expect(budgetFraction(-5, 1000)).toBe(0)
  })

  it('is 0 with no hard limit set — nothing to measure against', () => {
    expect(budgetFraction(1000, 0)).toBe(0)
  })
})

describe('useUsage', () => {
  let originalFetch: typeof globalThis.fetch
  beforeEach(() => {
    originalFetch = globalThis.fetch
  })
  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('loads and parses the route', async () => {
    globalThis.fetch = vi.fn(
      async () =>
        new Response(JSON.stringify(GOLDEN), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    ) as typeof globalThis.fetch

    const { result } = renderHook(() => useUsage())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.usage).toEqual(GOLDEN)
    expect(result.current.error).toBeNull()
  })

  it('reports a load failure in the server’s own words (e.g. the 501 a sqlite host answers)', async () => {
    globalThis.fetch = vi.fn(
      async () => new Response('usage is not configured on this host', { status: 501 }),
    ) as typeof globalThis.fetch

    const { result } = renderHook(() => useUsage())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toMatch(/usage is not configured/)
  })
})

describe('formatSpend', () => {
  it.each([
    ['api-key', 1252927, 1.78, true, '1,252,927 tokens · $1.78'],
    ['mock', 120, 0, true, '120 tokens · $0.00'],
    ['', 120, 0, false, '120 tokens · cost not reported'],
    ['subscription', 2887489, 4.34, true, '2,887,489 tokens (subscription — not billed per token)'],
  ])('%s', (mode, tokens, cost, known, want) => {
    expect(formatSpend(tokens, cost, known, mode)).toBe(want)
  })
})
