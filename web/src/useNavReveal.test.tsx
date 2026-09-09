// @vitest-environment jsdom
// B4: the plumbing behind progressive navigation — the counts, and the sticky
// set that means a revealed entry stays revealed (design 28 §3.3, K9).

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import useNavReveal, { navRevealKey, readRevealed, writeRevealed } from './useNavReveal.js'
import { everythingRevealed, NAV_CONDITIONAL, type NavEntry } from './navReveal.js'

let originalFetch: typeof globalThis.fetch
let workers: unknown[]
let memories: unknown[]
let events: unknown[]
let subscriptions: unknown[]

function Probe({ projectId = 'acme', pollMs }: { projectId?: string; pollMs?: number }) {
  const { visible, appeared } = useNavReveal({ projectId, pollMs })
  return (
    <div>
      <span data-testid="visible">{visible.join(',')}</span>
      <span data-testid="appeared">{appeared.join(',')}</span>
    </div>
  )
}

beforeEach(() => {
  window.localStorage.clear()
  workers = []
  memories = []
  events = []
  subscriptions = []

  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
    const u = String(url)
    const json = (v: unknown) =>
      new Response(JSON.stringify(v), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    if (u.includes('/agent/memories')) return json({ memories })
    if (u.includes('/agent/subscriptions')) return json({ subscriptions })
    if (u.includes('/agent/workers')) return json({ workers })
    if (u.includes('/agent/events')) return json({ events })
    if (u.includes('/agent/deliveries')) return json({ deliveries: [] })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
  window.localStorage.clear()
})

describe('day one', () => {
  it('shows the four an empty project always has', async () => {
    render(<Probe />)
    await waitFor(() =>
      expect(screen.getByTestId('visible')).toHaveTextContent('desk,chat,workers,settings'),
    )
  })

  it('announces nothing when nothing was revealed', async () => {
    render(<Probe />)
    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('desk'))
    expect(screen.getByTestId('appeared')).toHaveTextContent('')
  })
})

describe('reveal', () => {
  it('reveals Activity on the first event and says so', async () => {
    events = [{ id: 'e1', project: 'acme', type: 'x', text: '', envelope: {}, occurred_at: 1, created_at: 1 }]
    render(<Probe />)
    await waitFor(() => expect(screen.getByTestId('appeared')).toHaveTextContent('activity'))
    expect(screen.getByTestId('visible')).toHaveTextContent(
      'desk,chat,workers,activity,settings',
    )
  })

  it('reveals the Chart on the first subscription, not on workers alone', async () => {
    workers = [{ name: 'a', project: 'acme', system_prompt: '', enabled: true }]
    const { unmount } = render(<Probe />)
    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('workers'))
    expect(screen.getByTestId('visible')).not.toHaveTextContent('chart')
    unmount()

    window.localStorage.clear()
    subscriptions = [
      { id: 's1', project: 'acme', event_type: 'x', filter: {}, worker: 'a', enabled: true },
    ]
    render(<Probe />)
    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('chart'))
  })

  it('persists what it revealed', async () => {
    memories = [{ id: 'm1', project: 'acme', name: 'n', value: 'v', labels: {}, created_at: 1 }]
    render(<Probe />)
    await waitFor(() => expect(readRevealed('acme')).toEqual(['memory']))
  })
})

describe('stickiness', () => {
  it('keeps a revealed entry when its count returns to zero', async () => {
    writeRevealed('acme', ['activity'] as NavEntry[])
    render(<Probe />)
    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('activity'))
    // And it was already known, so nothing is announced a second time.
    expect(screen.getByTestId('appeared')).toHaveTextContent('')
  })

  it('keys the sticky set per project', async () => {
    writeRevealed('acme', ['chart'] as NavEntry[])
    render(<Probe projectId="other" />)
    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('desk'))
    expect(screen.getByTestId('visible')).not.toHaveTextContent('chart')
    expect(navRevealKey('acme')).not.toBe(navRevealKey('other'))
  })

  it('survives unreadable storage rather than throwing', () => {
    window.localStorage.setItem(navRevealKey('acme'), 'not json')
    expect(readRevealed('acme')).toEqual([])
  })
})

// DI12: the nav has to notice things that happen AFTER the page loaded.
//
// It used to count once and never again. Every hook feeding it is a one-shot
// ref-guarded fetch, and they are separate instances from the ones the pages
// hold, so hiring a worker on the Workers page could not reach the nav's copy —
// the Chart tab appeared on the next page load, not when you earned it. Which
// means `appeared` and `navRevealSentence`, whose whole job is to name a new
// entry beside the action that caused it, could never once have fired.
//
// Deliberately no fake timers: the thing under test IS the timer, and a test
// that installs its own proves the interval was requested rather than that it
// ever runs. A 20ms period keeps that honest and still quick.
describe('re-counting after mount (DI12)', () => {
  it('reveals the Chart when the project earns it, with no reload', async () => {
    render(<Probe pollMs={20} />)
    await waitFor(() =>
      expect(screen.getByTestId('visible')).toHaveTextContent('desk,chat,workers,settings'),
    )
    expect(screen.getByTestId('visible')).not.toHaveTextContent('chart')

    // The project earns it while the page sits there — two workers is the rule.
    workers = [
      { name: 'a', project: 'acme', system_prompt: '', enabled: true },
      { name: 'b', project: 'acme', system_prompt: '', enabled: true },
    ]

    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('chart'), {
      timeout: 4000,
    })
    // And it is announced, which is the half that was unreachable before.
    expect(screen.getByTestId('appeared')).toHaveTextContent('chart')
  })

  it('stops polling once every entry has been revealed', async () => {
    // Everything already earned, so the first evaluation settles it.
    workers = [
      { name: 'a', project: 'acme', system_prompt: '', enabled: true },
      { name: 'b', project: 'acme', system_prompt: '', enabled: true },
    ]
    memories = [{ id: 'm1', project: 'acme', content: 'x', labels: {}, created_at: 1 }]
    events = [
      { id: 'e1', project: 'acme', type: 'x', text: '', envelope: {}, occurred_at: 1, created_at: 1 },
    ]
    subscriptions = [{ id: 's1', project: 'acme', event_type: 'x', worker: 'a', enabled: true }]

    render(<Probe pollMs={20} />)
    await waitFor(() => expect(screen.getByTestId('visible')).toHaveTextContent('chart'))

    const after = (globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length
    // Long enough for many ticks of a 20ms interval, had one still been armed.
    await new Promise((r) => setTimeout(r, 250))
    expect((globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length).toBe(
      after,
    )
  })

  it('does not poll at all when switched off', async () => {
    render(<Probe pollMs={0} />)
    await waitFor(() =>
      expect(screen.getByTestId('visible')).toHaveTextContent('desk,chat,workers,settings'),
    )
    const after = (globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length
    workers = [
      { name: 'a', project: 'acme', system_prompt: '', enabled: true },
      { name: 'b', project: 'acme', system_prompt: '', enabled: true },
    ]
    await new Promise((r) => setTimeout(r, 150))
    expect((globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length).toBe(
      after,
    )
    expect(screen.getByTestId('visible')).not.toHaveTextContent('chart')
  })
})

describe('everythingRevealed', () => {
  it('is false until every conditional entry is held, then true', () => {
    expect(everythingRevealed([])).toBe(false)
    expect(everythingRevealed(['memory'] as NavEntry[])).toBe(false)
    expect(everythingRevealed(NAV_CONDITIONAL as NavEntry[])).toBe(true)
    // The always-present entries are not part of the question.
    expect(everythingRevealed(['desk', 'chat', 'workers', 'settings'] as NavEntry[])).toBe(false)
  })

  it('lists exactly the entries that have a reveal rule', () => {
    expect([...NAV_CONDITIONAL].sort()).toEqual(['activity', 'chart', 'memory'])
  })
})
