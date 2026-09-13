// @vitest-environment jsdom
// "Your team is forming": the architect's state, each hire as it lands, and a
// clear way on when the architect has said what it built.

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import TeamFormingPanel from './TeamFormingPanel.js'

let originalFetch: typeof globalThis.fetch
let workers: unknown[]
let deliveries: unknown[]
let events: unknown[]

beforeEach(() => {
  workers = [{ name: 'architect', enabled: true, created_at: 1 }]
  deliveries = []
  events = []
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
    const u = String(url)
    const json = (v: unknown) =>
      new Response(JSON.stringify(v), { status: 200, headers: { 'Content-Type': 'application/json' } })
    if (u.includes('/agent/workers')) return json({ workers })
    if (u.includes('/agent/deliveries')) return json({ deliveries })
    if (u.includes('/agent/events')) return json({ events })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('TeamFormingPanel', () => {
  it('says the architect is starting before its job exists', async () => {
    render(<TeamFormingPanel refreshMs={0} />)
    await waitFor(() => expect(screen.getByTestId('team-forming-empty').textContent).toMatch(/no workers yet/i))
    expect(screen.getByTestId('team-forming').getAttribute('data-phase')).toBe('starting')
    expect(screen.getByTestId('team-forming-title').textContent).toMatch(/team is forming/i)
    expect(screen.queryByTestId('team-forming-open-desk')).toBeNull()
  })

  it('lists workers as the poll finds them, with what each is for', async () => {
    deliveries = [{ id: 'd1', worker: 'architect', status: 'running', created_at: 10 }]
    render(<TeamFormingPanel refreshMs={20} />)
    await waitFor(() => expect(screen.getByTestId('team-forming').getAttribute('data-phase')).toBe('designing'))
    expect(screen.queryAllByTestId('team-member')).toHaveLength(0)

    workers = [
      ...workers,
      { name: 'scout', enabled: true, created_at: 5, description: 'Finds new leads every morning.' },
    ]
    await waitFor(() => expect(screen.getAllByTestId('team-member')).toHaveLength(1), { timeout: 2000 })
    expect(screen.getByText('scout')).toBeTruthy()
    expect(screen.getByText('Finds new leads every morning.')).toBeTruthy()
    expect(screen.getByTestId('team-member-status').textContent).toMatch(/has not run yet/)
  })

  it('offers the Desk and a cycle once the architect has finished', async () => {
    deliveries = [{ id: 'd1', worker: 'architect', status: 'awaiting_human', created_at: 10 }]
    const onOpenDesk = vi.fn()
    render(<TeamFormingPanel refreshMs={0} onOpenDesk={onOpenDesk} />)
    const button = await screen.findByTestId('team-forming-open-desk')
    expect(button.textContent).toMatch(/team is ready/i)
    expect(screen.getByTestId('run-cycle')).toBeTruthy()
    await userEvent.click(button)
    expect(onOpenDesk).toHaveBeenCalledTimes(1)
  })

  it('shows a failed first run with its reason and a way to run it again', async () => {
    deliveries = [{ id: 'd1', worker: 'architect', status: 'failed', failure_reason: 'host port pool is exhausted', created_at: 10 }]
    render(<TeamFormingPanel refreshMs={0} />)
    await screen.findByTestId('team-forming-failed')
    expect(screen.getByText('host port pool is exhausted')).toBeTruthy()
    expect(screen.getByTestId('run-architect')).toBeTruthy()
  })

  it('when approval could not start the architect, says so and offers the manual control', async () => {
    render(<TeamFormingPanel refreshMs={0} architectRunError="events are not configured on this host" />)
    await screen.findByTestId('team-forming-not-started')
    expect(screen.getByTestId('run-architect')).toBeTruthy()
  })

  it('shows what just happened, without config churn', async () => {
    events = [
      { id: 'e1', type: 'worker.finished', occurred_at: 100, envelope: { worker: 'scout' } },
      { id: 'e2', type: 'config.changed', occurred_at: 200, envelope: {} },
    ]
    render(<TeamFormingPanel refreshMs={0} nowMs={100_000} />)
    const strip = await screen.findByTestId('team-forming-activity')
    expect(strip.textContent).toMatch(/worker\.finished from scout/)
    expect(strip.textContent).not.toMatch(/config\.changed/)
  })
})
