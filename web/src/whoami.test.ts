// @vitest-environment jsdom
// A5 — the operator gate BudgetPanel's limits form must never guess at
// (design/2026-09-11-onboarding-work-plan.md §1.1, §1.2).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import useWhoami, { coerceWhoami, defaultWhoami } from './whoami.js'

describe('coerceWhoami', () => {
  it('reads the route’s shape straight through', () => {
    expect(coerceWhoami({ email: 'kai@example.com', project: 'acme', operator: true })).toEqual({
      email: 'kai@example.com',
      project: 'acme',
      operator: true,
    })
  })

  it('only a literal true counts as operator — never a truthy guess', () => {
    expect(coerceWhoami({ operator: 'true' }).operator).toBe(false)
    expect(coerceWhoami({ operator: 1 }).operator).toBe(false)
    expect(coerceWhoami({}).operator).toBe(false)
  })

  it('falls back to the safe (non-operator) default on a wild shape', () => {
    expect(coerceWhoami(null)).toEqual(defaultWhoami())
    expect(defaultWhoami().operator).toBe(false)
  })
})

describe('useWhoami', () => {
  let originalFetch: typeof globalThis.fetch
  beforeEach(() => {
    originalFetch = globalThis.fetch
  })
  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('loads the route', async () => {
    globalThis.fetch = vi.fn(
      async () =>
        new Response(JSON.stringify({ email: 'kai@example.com', project: 'acme', operator: true }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    ) as typeof globalThis.fetch

    const { result } = renderHook(() => useWhoami())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.whoami).toEqual({
      email: 'kai@example.com',
      project: 'acme',
      operator: true,
    })
  })

  it('fails closed: a load failure leaves operator false, never true', async () => {
    globalThis.fetch = vi.fn(
      async () => new Response('database is down', { status: 500 }),
    ) as typeof globalThis.fetch

    const { result } = renderHook(() => useWhoami())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.whoami.operator).toBe(false)
    expect(result.current.error).toMatch(/database is down/)
  })
})
