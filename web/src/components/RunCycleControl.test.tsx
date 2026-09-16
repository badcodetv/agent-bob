// @vitest-environment jsdom
// "Run a cycle now": confirms naming who will run, fires every enabled schedule
// through the run-now route (and nothing else), and says what came of each.

import React from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import RunCycleControl from './RunCycleControl.js'

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string }[]
let schedules: unknown[]
let runAnswers: Record<string, { status: number; body: unknown }>
let deliveries: unknown[]

beforeEach(() => {
  requests = []
  schedules = [
    { id: 's1', worker: 'scout', cron: '0 9 * * *', enabled: true },
    { id: 's2', worker: 'writer', cron: '0 10 * * *', enabled: true },
    { id: 's3', worker: 'retired', cron: '0 11 * * *', enabled: false },
  ]
  runAnswers = {}
  deliveries = []
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url)
    const method = init?.method ?? 'GET'
    requests.push({ url: u, method })
    const json = (v: unknown, status = 200) =>
      new Response(typeof v === 'string' ? v : JSON.stringify(v), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
    const run = u.match(/\/agent\/schedules\/([^/]+)\/run$/)
    if (run && method === 'POST') {
      const a = runAnswers[run[1]]
      if (a) return json(a.body, a.status)
      return json({ schedule_id: run[1], outcome: 'requested', event_id: `ev-${run[1]}` })
    }
    if (u.endsWith('/agent/schedules')) return json({ schedules })
    if (u.includes('/agent/deliveries')) return json({ deliveries })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('RunCycleControl', () => {
  it('names who will run and writes nothing until confirmed', async () => {
    render(<RunCycleControl />)
    await userEvent.click(screen.getByTestId('run-cycle'))
    const plan = await screen.findByTestId('run-cycle-plan')
    expect(plan.textContent).toMatch(/scout/)
    expect(plan.textContent).toMatch(/writer/)
    // A disabled schedule was switched off by someone; a cycle does not overrule that.
    expect(plan.textContent).not.toMatch(/retired/)
    // Each clock is named by when it would have run, so a worker on two reads as two.
    expect(plan.textContent).toMatch(/scout — at \d\d:\d\d/)

    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(requests.filter((r) => r.method === 'POST')).toHaveLength(0)
  })

  it('fires each enabled schedule through the run-now route, and reports each', async () => {
    runAnswers.s2 = { status: 200, body: { schedule_id: 's2', outcome: 'already_fired' } }
    const onRan = vi.fn()
    render(<RunCycleControl onRan={onRan} />)
    await userEvent.click(screen.getByTestId('run-cycle'))
    await userEvent.click(await screen.findByRole('button', { name: /run them/i }))

    const result = await screen.findByTestId('run-cycle-result')
    const posts = requests.filter((r) => r.method === 'POST').map((r) => r.url)
    expect(posts.sort()).toEqual(['/agent/schedules/s1/run', '/agent/schedules/s2/run'])
    // Never a fabricated event: the scheduler's own firing is the only path.
    expect(posts.some((u) => u.includes('/agent/events'))).toBe(false)
    expect(within(result).getByText('scout — started')).toBeTruthy()
    expect(within(result).getByText('writer — already ran this minute')).toBeTruthy()
    expect(onRan).toHaveBeenCalledTimes(1)
  })

  it('reports a refusal in the server words, beside the ones that started', async () => {
    runAnswers.s1 = { status: 409, body: 'this schedule is disabled; enable it before running it' }
    render(<RunCycleControl />)
    await userEvent.click(screen.getByTestId('run-cycle'))
    await userEvent.click(await screen.findByRole('button', { name: /run them/i }))
    const result = await screen.findByTestId('run-cycle-result')
    expect(result.textContent).toMatch(/scout — this schedule is disabled/)
    expect(result.textContent).toMatch(/writer — started/)
  })

  it('leaves out a worker that only just ran, says so, and does not fire it', async () => {
    const NOW_MS = 1_800_000_000_000
    deliveries = [{ id: 'd1', worker: 'scout', status: 'ok', started_at: NOW_MS / 1000 - 400, ended_at: NOW_MS / 1000 - 240 }]
    render(<RunCycleControl nowMs={NOW_MS} />)
    await userEvent.click(screen.getByTestId('run-cycle'))
    const plan = await screen.findByTestId('run-cycle-plan')
    expect(plan.textContent).not.toMatch(/scout/)
    expect(plan.textContent).toMatch(/writer/)
    expect(screen.getByTestId('run-cycle-skipped').textContent).toMatch(/scout — .* · ran 4 min ago — skipped/)
    await userEvent.click(screen.getByRole('button', { name: /run them/i }))
    await screen.findByTestId('run-cycle-result')
    expect(requests.filter((r) => r.method === 'POST').map((r) => r.url)).toEqual(['/agent/schedules/s2/run'])
  })

  it('says everyone just ran when every schedule is skipped', async () => {
    const NOW_MS = 1_800_000_000_000
    schedules = [{ id: 's1', worker: 'scout', cron: '0 9 * * *', enabled: true }]
    deliveries = [{ id: 'd1', worker: 'scout', status: 'running', started_at: NOW_MS / 1000 - 30 }]
    render(<RunCycleControl nowMs={NOW_MS} />)
    await userEvent.click(screen.getByTestId('run-cycle'))
    await screen.findByTestId('run-cycle-all-skipped')
    expect(screen.queryByRole('button', { name: /run them/i })).toBeNull()
  })

  it('says there is nothing on a clock when no schedule is enabled', async () => {
    schedules = [{ id: 's3', worker: 'retired', enabled: false }]
    render(<RunCycleControl />)
    await userEvent.click(screen.getByTestId('run-cycle'))
    await screen.findByTestId('run-cycle-nothing')
    expect(screen.queryByRole('button', { name: /run them/i })).toBeNull()
  })
})
