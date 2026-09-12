// @vitest-environment jsdom
// The regression test for DI29 — the worst defect of the onboarding wave
// (design/2026-09-11-onboarding-work-plan.md, A2 and its Discovered Issues
// Log).
//
// WHAT WENT WRONG. `useInterviewState` polls two cheap GETs to answer one
// question: is this project still in its onboarding interview? It computed
// `inInterview = (an `onboard` session exists) && (no `architect` worker yet)`,
// and then latched its own interval off with `if (!inInterview) settled.current
// = true`. The first check runs at mount, which for any ordinary project is
// BEFORE the interview has started — so `inInterview` was false for the
// innocent reason, the latch fired, the interval never ran again, and the
// console could never learn that an interview had begun. That is precisely the
// failure A2 was written to prevent.
//
// WHY NOTHING CAUGHT IT. `web/`'s suite covers `web/src`; A2's own 59 tests fed
// `inInterview` in as a prop rather than computing it; and this package had no
// test runner at all. The defect was found by a human-driven browser pass
// against the real stack. This file is the cheap gate that would have caught it
// in a second, and it is the reason `examples/web` now has vitest.
//
// The distinction under test, in one line: "not in an interview" and "the
// interview is over" are different states, and only the second may stop the
// watch.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useInterviewState, ONBOARD_SESSION_NAME } from './onboarding.js'

const API = 'http://api.test'
const TOKEN = 'test-token'
// Far below the hook's 4s default so a test that must observe several polls
// finishes in milliseconds. The hook takes `refreshMs` for exactly this.
const FAST_POLL_MS = 15

/** The two routes the hook reads, as a mutable little world a test can move. */
interface World {
  /** The `onboard` session's id, or null for "no such session" (HTTP 404). */
  onboardSessionId: string | null
  /** Worker names `GET /agent/workers` reports. */
  workerNames: string[]
  /** Every request fails while true — the transient-failure posture. */
  down: boolean
}

let restoreFetch: (() => void) | null = null

/** Installs a fake `fetch` over `world` and returns a per-path call counter. */
function serve(world: World): { calls: Record<string, number> } {
  const calls: Record<string, number> = {}
  const fake = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input)
    const path = url.slice(API.length)
    calls[path] = (calls[path] ?? 0) + 1
    if (world.down) throw new Error('network down')
    if (path === `/agent/sessions/by-name/${ONBOARD_SESSION_NAME}`) {
      return world.onboardSessionId === null
        ? new Response('no such session', { status: 404 })
        : new Response(JSON.stringify({ id: world.onboardSessionId, status: 'ready' }), {
            status: 200,
          })
    }
    if (path === '/agent/workers') {
      return new Response(JSON.stringify({ workers: world.workerNames.map((name) => ({ name })) }), {
        status: 200,
      })
    }
    return new Response('unexpected path', { status: 500 })
  })
  const original = globalThis.fetch
  globalThis.fetch = fake as unknown as typeof fetch
  restoreFetch = () => {
    globalThis.fetch = original
  }
  return { calls }
}

const mount = () =>
  renderHook(() => useInterviewState({ apiBase: API, token: TOKEN, refreshMs: FAST_POLL_MS }))

/** Waits out several poll intervals of wall-clock time. */
const waitPolls = (n: number) => new Promise((r) => setTimeout(r, FAST_POLL_MS * n + 20))

describe('useInterviewState (DI29)', () => {
  afterEach(() => {
    restoreFetch?.()
    restoreFetch = null
  })

  it('keeps polling when the first check finds no interview, and notices one starting', async () => {
    // The ordinary project at mount: no `onboard` session, no architect. The
    // old code read this as "settled" and stopped.
    const world: World = { onboardSessionId: null, workerNames: ['interviewer'], down: false }
    serve(world)

    const { result } = mount()

    await waitFor(() => expect(result.current.resolved).toBe(true))
    expect(result.current.inInterview).toBe(false)
    expect(result.current.onboardSessionId).toBeNull()

    // The human clicks "set up this project" and the session appears. Nothing
    // in the chat stream announces it — only the poll can see it.
    world.onboardSessionId = 'sess-onboard-1'

    await waitFor(
      () => {
        expect(result.current.inInterview).toBe(true)
        expect(result.current.onboardSessionId).toBe('sess-onboard-1')
      },
      { timeout: 2000 },
    )
  })

  it('stops polling only once the architect worker exists', async () => {
    // Mid-interview: the session is there and the architect is not.
    const world: World = {
      onboardSessionId: 'sess-onboard-2',
      workerNames: ['interviewer'],
      down: false,
    }
    const { calls } = serve(world)

    const { result } = mount()
    await waitFor(() => expect(result.current.inInterview).toBe(true))

    // Still watching: the count is rising while the charter is unapproved.
    const before = calls['/agent/workers'] ?? 0
    await waitPolls(3)
    expect(calls['/agent/workers'] ?? 0).toBeGreaterThan(before)

    // The charter is approved, which creates exactly one worker — the
    // architect. That, and only that, ends the watch.
    world.workerNames = ['interviewer', 'architect']
    await waitFor(() => expect(result.current.inInterview).toBe(false))
    expect(result.current.onboardSessionId).toBe('sess-onboard-2')

    // The interval must now be idle — proved by wall-clock, not by reading the
    // ref, so it tests the behaviour rather than the implementation.
    const settledAt = calls['/agent/workers'] ?? 0
    await waitPolls(4)
    expect(calls['/agent/workers'] ?? 0).toBe(settledAt)
  })

  it('holds the last known state through a transient failure', async () => {
    // A shell-level gate flickering off because one request dropped would hide
    // "finish setting up this project" for the one navigation that needed it,
    // so the hook swallows errors and keeps the previous answer.
    const world: World = {
      onboardSessionId: 'sess-onboard-3',
      workerNames: ['interviewer'],
      down: false,
    }
    serve(world)

    const { result } = mount()
    await waitFor(() => expect(result.current.inInterview).toBe(true))

    world.down = true
    await waitPolls(3)
    expect(result.current.inInterview).toBe(true)
    expect(result.current.onboardSessionId).toBe('sess-onboard-3')
    expect(result.current.resolved).toBe(true)
  })

  it('never calls the API without a token', async () => {
    const world: World = { onboardSessionId: null, workerNames: [], down: false }
    const { calls } = serve(world)

    const { result } = renderHook(() =>
      useInterviewState({ apiBase: API, token: '', refreshMs: FAST_POLL_MS }),
    )
    await waitPolls(3)

    expect(Object.keys(calls)).toEqual([])
    expect(result.current.resolved).toBe(false)
  })
})
