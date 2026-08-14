// @vitest-environment jsdom
// B4: the plumbing behind progressive navigation — the counts, and the sticky
// set that means a revealed entry stays revealed (design 28 §3.3, K9).

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import useNavReveal, { navRevealKey, readRevealed, writeRevealed } from './useNavReveal.js'
import type { NavEntry } from './navReveal.js'

let originalFetch: typeof globalThis.fetch
let workers: unknown[]
let memories: unknown[]
let events: unknown[]
let subscriptions: unknown[]

function Probe({ projectId = 'acme' }: { projectId?: string }) {
  const { visible, appeared } = useNavReveal({ projectId })
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
