// @vitest-environment jsdom
// DI9: opening "New schedule" or "New subscription" from a worker's own
// Triggers tab must already know which worker it is for.
//
// The bug this pins was a passing-through failure, not a component failure.
// `WorkerTriggers` knows `workerName` — the whole surface is scoped to it, and
// its own list filters on it — but it handed both editors a null draft, so the
// Worker field came up empty and `validateSchedule`, which requires a worker,
// held Save disabled until the human retyped a name the page was already
// displaying two inches away. It also survived every existing test, because
// nothing rendered this component.
//
// So the assertions here are deliberately about the SEAM: that the name
// reaches the editor's field, and that Save is reachable without anyone
// touching the Worker input. A test that only checked the new `defaultWorker`
// prop on the editors would have passed against the broken wiring.

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AgentChatProvider } from '../AgentChatProvider.js'
import WorkerTriggers from './WorkerTriggers.js'

const WORKER = 'tweet-author'

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string; body: any }[] = []

beforeEach(() => {
  requests = []
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url)
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    requests.push({ url: u, method, body })
    const json = (v: unknown, status = 200) =>
      new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } })

    if (u.includes('/agent/subscriptions')) {
      if (method === 'POST') return json({ ...body, id: 'sub-new', project: 'acme' }, 201)
      // Empty on purpose: the worker starts with nothing, which is exactly the
      // state a human is in when they press "New schedule".
      return json({ subscriptions: [] })
    }
    if (u.includes('/agent/schedules')) {
      if (method === 'POST') return json({ ...body, id: 'sch-new', project: 'acme' }, 201)
      return json({ schedules: [] })
    }
    if (u.includes('/agent/events')) return json({ events: [] })
    if (u.includes('/agent/deliveries')) return json({ deliveries: [] })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

const writes = () => requests.filter((r) => r.method === 'POST' || r.method === 'PUT')

function renderTriggers(props: Partial<React.ComponentProps<typeof WorkerTriggers>> = {}) {
  return render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <WorkerTriggers
        workerName={WORKER}
        workerOptions={[WORKER, 'someone-else']}
        {...props}
      />
    </AgentChatProvider>,
  )
}

describe('WorkerTriggers — the new-trigger draft knows its worker (DI9)', () => {
  it('prefills the worker when a schedule is created from the worker page', async () => {
    renderTriggers()
    await userEvent.click(await screen.findByTestId('new-schedule'))

    expect(await screen.findByLabelText('Worker')).toHaveValue(WORKER)
  })

  it('prefills the worker when a subscription is created from the worker page', async () => {
    renderTriggers()
    await userEvent.click(await screen.findByTestId('new-subscription'))

    expect(await screen.findByLabelText('Worker')).toHaveValue(WORKER)
  })

  it('saves a schedule without the worker field ever being touched', async () => {
    renderTriggers()
    await userEvent.click(await screen.findByTestId('new-schedule'))

    // Everything the human still has to say — and nothing about the worker.
    await userEvent.type(await screen.findByLabelText('Cron'), '0 9 * * 1-5')
    await userEvent.type(screen.getByLabelText('Instruction'), 'Write the morning tweet.')

    const save = screen.getByRole('button', { name: 'Create schedule' })
    // The DI9 symptom: this button stayed disabled, with nothing on screen
    // saying which field was missing.
    await waitFor(() => expect(save).toBeEnabled())
    await userEvent.click(save)

    await waitFor(() => expect(writes()).toHaveLength(1))
    const posted = writes()[0]!
    expect(posted.url).toContain('/agent/schedules')
    expect(posted.body.worker).toBe(WORKER)
  })

  it('does not prefill over a worker that a stored row already names', async () => {
    // The guard on the fix: `defaultWorker` is for NEW drafts only. If it ever
    // leaked into the editing path it would silently rewrite the worker of a
    // row opened from a project-wide list.
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const u = String(url)
      const json = (v: unknown) =>
        new Response(JSON.stringify(v), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (u.includes('/agent/schedules'))
        return json({
          schedules: [
            {
              id: 'sch-1',
              project: 'acme',
              worker: WORKER,
              cron: '0 9 * * 1-5',
              input: 'Write the morning tweet.',
              enabled: true,
              created_at: 1,
              updated_at: 1,
            },
          ],
        })
      if (u.includes('/agent/subscriptions')) return json({ subscriptions: [] })
      if (u.includes('/agent/events')) return json({ events: [] })
      return json({})
    }) as typeof globalThis.fetch

    renderTriggers({ selectedId: 'sch-1' })

    expect(await screen.findByLabelText('Worker')).toHaveValue(WORKER)
    expect(screen.getByLabelText('Cron')).toHaveValue('0 9 * * 1-5')
  })
})
