// @vitest-environment jsdom
// `useInterviewState` — the shell's answer to "is this project still being set
// up?". Two defects from the onboarding wave live here, and both were the same
// mistake: the shell deciding a server fact for itself.
//
// DI29 (the worst finding of the wave). The hook latched its own poll off with
// `if (!inInterview) settled.current = true`. The first check runs at mount,
// which for any ordinary project is BEFORE the interview has started — so
// `inInterview` was false for the innocent reason, the latch fired, the
// interval never ran again, and the console could never learn that an interview
// had begun. Nothing offline could catch it: `web/`'s suite cannot see this
// directory, this package had no test runner at all, and the 59 unit tests that
// did exist fed `inInterview` in as a prop rather than computing it.
//
// DI10. "Has the charter been approved?" was inferred by asking
// `GET /agent/workers` whether a worker literally named `architect` existed.
// The charter schema lets an interview name its architect anything; one that
// did left the project reading as still-in-interview forever. The server now
// reports `applied` on `GET /agent/charter/current`, and this hook reads it.
//
// The distinction both defects turn on, in one line: "not in an interview",
// "in an interview" and "the interview is over" are three states, and only the
// third may stop the watch.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useInterviewState, ONBOARD_SESSION_NAME } from './onboarding.js'

const API = 'http://api.test'
const TOKEN = 'test-token'
// Far below the hook's 4s default so a test that must observe several polls
// finishes in milliseconds. The hook takes `refreshMs` for exactly this.
const FAST_POLL_MS = 15

const SESSION_PATH = `/agent/sessions/by-name/${ONBOARD_SESSION_NAME}`
const CHARTER_PATH = '/agent/charter/current'

/** The two routes the hook reads, as a mutable little world a test can move. */
interface World {
  /** The `onboard` session's id, or null for "no such session" (HTTP 404). */
  onboardSessionId: string | null
  /** What `GET /agent/charter/current` answers:
   *  - 'none'     — 404, the interview is running and has deposited nothing
   *  - 'proposed' — 200 applied:false, deposited and awaiting a human
   *  - 'applied'  — 200 applied:true, approved; the interview is over */
  charter: 'none' | 'proposed' | 'applied'
  /** Every request fails while true — the transient-failure posture. */
  down: boolean
}

let restoreFetch: (() => void) | null = null

/** Installs a fake `fetch` over `world` and returns a per-path call counter. */
function serve(world: World): { calls: Record<string, number>; charterQueries: string[] } {
  const calls: Record<string, number> = {}
  const charterQueries: string[] = []
  const fake = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const url = new URL(String(input))
    const path = url.pathname
    calls[path] = (calls[path] ?? 0) + 1
    if (world.down) throw new Error('network down')

    if (path === SESSION_PATH) {
      return world.onboardSessionId === null
        ? new Response('no such session', { status: 404 })
        : new Response(JSON.stringify({ id: world.onboardSessionId, status: 'ready' }), {
            status: 200,
          })
    }
    if (path === CHARTER_PATH) {
      charterQueries.push(url.searchParams.get('session') ?? '')
      if (world.charter === 'none') {
        return new Response(
          'no charter has been proposed yet — the interview has to deposit an org-charter memory first',
          { status: 404 },
        )
      }
      return new Response(
        JSON.stringify({
          memory_id: 'mem-charter-1',
          valid: true,
          applied: world.charter === 'applied',
          ...(world.charter === 'applied' ? { applied_at: 1_789_000_999_000 } : {}),
        }),
        { status: 200 },
      )
    }
    return new Response('unexpected path ' + path, { status: 500 })
  })
  const original = globalThis.fetch
  globalThis.fetch = fake as unknown as typeof fetch
  restoreFetch = () => {
    globalThis.fetch = original
  }
  return { calls, charterQueries }
}

const mount = () =>
  renderHook(() => useInterviewState({ apiBase: API, token: TOKEN, refreshMs: FAST_POLL_MS }))

/** Waits out several poll intervals of wall-clock time. */
const waitPolls = (n: number) => new Promise((r) => setTimeout(r, FAST_POLL_MS * n + 20))

describe('useInterviewState', () => {
  afterEach(() => {
    restoreFetch?.()
    restoreFetch = null
  })

  it('keeps polling when the first check finds no interview, and notices one starting (DI29)', async () => {
    // The ordinary project at mount: no `onboard` session at all. The old code
    // read this as "settled" and stopped watching for good.
    const world: World = { onboardSessionId: null, charter: 'none', down: false }
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

  it('stops polling only once the server says the charter is applied (DI10)', async () => {
    // Mid-interview: a charter is deposited and the human has not approved it.
    const world: World = {
      onboardSessionId: 'sess-onboard-2',
      charter: 'proposed',
      down: false,
    }
    const { calls, charterQueries } = serve(world)

    const { result } = mount()
    await waitFor(() => expect(result.current.inInterview).toBe(true))

    // The charter is read for THIS interview's session, not for the project at
    // large — that is what stops one project's approval answering for another.
    expect(charterQueries[0]).toBe('sess-onboard-2')

    // Still watching while the charter is unapproved.
    const before = calls[CHARTER_PATH] ?? 0
    await waitPolls(3)
    expect(calls[CHARTER_PATH] ?? 0).toBeGreaterThan(before)

    // Approved. That, and only that, ends the watch.
    world.charter = 'applied'
    await waitFor(() => expect(result.current.inInterview).toBe(false))
    expect(result.current.onboardSessionId).toBe('sess-onboard-2')

    // The interval must now be idle — proved by wall-clock, not by reading the
    // ref, so it tests the behaviour rather than the implementation.
    const settledAt = calls[CHARTER_PATH] ?? 0
    await waitPolls(4)
    expect(calls[CHARTER_PATH] ?? 0).toBe(settledAt)
  })

  it('never reads the roster, so what the architect is called cannot matter (DI10)', async () => {
    // The fake `fetch` answers 500 for any path it does not know, and
    // `/agent/workers` is deliberately not one of them. If a name check ever
    // comes back, this test fails on the call count rather than on a guess
    // about what the name would have been.
    const world: World = {
      onboardSessionId: 'sess-onboard-3',
      charter: 'applied',
      down: false,
    }
    const { calls } = serve(world)

    const { result } = mount()
    await waitFor(() => expect(result.current.resolved).toBe(true))
    await waitPolls(2)

    expect(result.current.inInterview).toBe(false)
    expect(calls['/agent/workers']).toBeUndefined()
    expect(Object.keys(calls).sort()).toEqual([CHARTER_PATH, SESSION_PATH].sort())
  })

  it('holds the last known state through a transient failure', async () => {
    // A shell-level gate flickering off because one request dropped would hide
    // "finish setting up this project" for the one navigation that needed it,
    // so the hook swallows errors and keeps the previous answer.
    const world: World = {
      onboardSessionId: 'sess-onboard-4',
      charter: 'proposed',
      down: false,
    }
    serve(world)

    const { result } = mount()
    await waitFor(() => expect(result.current.inInterview).toBe(true))

    world.down = true
    await waitPolls(3)
    expect(result.current.inInterview).toBe(true)
    expect(result.current.onboardSessionId).toBe('sess-onboard-4')
    expect(result.current.resolved).toBe(true)
  })

  it('never calls the API without a token', async () => {
    const world: World = { onboardSessionId: null, charter: 'none', down: false }
    const { calls } = serve(world)

    const { result } = renderHook(() =>
      useInterviewState({ apiBase: API, token: '', refreshMs: FAST_POLL_MS }),
    )
    await waitPolls(3)

    expect(Object.keys(calls)).toEqual([])
    expect(result.current.resolved).toBe(false)
  })
})
