// @vitest-environment jsdom
// C1: the emit flow, extracted from the retired Replay tab so the org chart's
// propagation panel can mount it (design 28 §4.2).
//
// This is the ONE irreversible action in the console — it wakes real workers
// and spends real tokens — so the tests here are mostly about it refusing to
// happen by accident.

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import EmitEventControl from './EmitEventControl.js'
import { coerceSubscription, type Subscription } from '../events.js'

const EVENT = { type: 'email.received', text: 'hi', envelope: { source: 'external' } as never }

const subs = (): Subscription[] => [
  coerceSubscription({
    id: 's1',
    project: 'acme',
    event_type: 'email.received',
    filter: {},
    worker: 'email-answerer',
    max_firings_per_hour: 0,
    enabled: true,
    created_at: 0,
    updated_at: 0,
  }),
  coerceSubscription({
    id: 's2',
    project: 'acme',
    event_type: 'invoice.received',
    filter: {},
    worker: 'invoice-parser',
    max_firings_per_hour: 0,
    enabled: true,
    created_at: 0,
    updated_at: 0,
  }),
]

let originalFetch: typeof globalThis.fetch
let posted: { url: string; body: unknown }[]
let respond: () => Response

beforeEach(() => {
  posted = []
  respond = () =>
    new Response(
      JSON.stringify({ id: 'ev-1', project: 'acme', type: 'email.received', text: 'hi' }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === 'POST') {
      posted.push({ url: String(url), body: JSON.parse(String(init.body)) })
      return respond()
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

const renderControl = (
  props: Partial<React.ComponentProps<typeof EmitEventControl>> = {},
) =>
  render(
    <EmitEventControl
      event={EVENT}
      subscriptions={subs()}
      matchedCount={1}
      apiBaseUrl=""
      {...props}
    />,
  )

describe('it refuses to fire by accident', () => {
  it('asks before writing anything', async () => {
    renderControl()
    await userEvent.click(screen.getByTestId('emit-event'))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText('Emit a real event?')).toBeInTheDocument()
    expect(posted).toHaveLength(0)
  })

  it('writes nothing when the confirmation is cancelled', async () => {
    renderControl()
    await userEvent.click(screen.getByTestId('emit-event'))
    await userEvent.click(await screen.findByRole('button', { name: /cancel/i }))
    expect(posted).toHaveLength(0)
  })

  it('is disabled while the draft does not parse', () => {
    renderControl({ event: null })
    expect(screen.getByTestId('emit-event')).toBeDisabled()
  })

  it('is never the primary button — the irreversible one must not be the default', () => {
    renderControl()
    // Trace is `contained` on the panel; this stays outlined.
    expect(screen.getByTestId('emit-event').className).toContain('outlined')
  })

  it('says how many subscriptions would fire, before it fires them', async () => {
    renderControl({ matchedCount: 1 })
    await userEvent.click(screen.getByTestId('emit-event'))
    expect(await screen.findByText(/1 of 2 subscriptions match/)).toBeInTheDocument()
  })

  it('says plainly when nothing would start', async () => {
    renderControl({ matchedCount: 0 })
    await userEvent.click(screen.getByTestId('emit-event'))
    expect(
      await screen.findByText(/no subscription matches, so nothing would start/i),
    ).toBeInTheDocument()
  })
})

describe('when it does fire', () => {
  it('sends only type and text — core stamps the envelope', async () => {
    renderControl()
    await userEvent.click(screen.getByTestId('emit-event'))
    await userEvent.click(await screen.findByRole('button', { name: /^emit it$/i }))

    await waitFor(() => expect(posted).toHaveLength(1))
    expect(posted[0]!.body).toEqual({ type: 'email.received', text: 'hi' })
    // The drafted envelope is deliberately NOT sent: sending it would imply it
    // was honoured, and core ignores it.
    expect(Object.keys(posted[0]!.body as object)).not.toContain('envelope')
  })

  it('keeps the verb through the flow, and names what it wrote', async () => {
    const onEmitted = vi.fn()
    renderControl({ onEmitted })
    await userEvent.click(screen.getByTestId('emit-event'))
    await userEvent.click(await screen.findByRole('button', { name: /^emit it$/i }))

    expect(await screen.findByText(/Emitted/)).toBeInTheDocument()
    expect(screen.getByText('ev-1')).toBeInTheDocument()
    await waitFor(() => expect(onEmitted).toHaveBeenCalled())
  })

  it('renders the server’s own words when ingestion is refused, and keeps the draft', async () => {
    respond = () => new Response('type is required', { status: 400 })
    renderControl()
    await userEvent.click(screen.getByTestId('emit-event'))
    await userEvent.click(await screen.findByRole('button', { name: /^emit it$/i }))

    expect(await screen.findByText('type is required')).toBeInTheDocument()
    // The dialog stays open on failure — nothing was written, and the operator
    // keeps the draft and the retry.
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
