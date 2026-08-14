// @vitest-environment jsdom
// B1: the Activity view — one rail carrying every kind of record, with the
// filter chips subsetting it IN PLACE (design 28 §1.3).
//
// The load-bearing test in this file is "the chips do not swap the surface":
// if a chip re-mounts the rail, the merge is a rename and the ticket is not
// done. It is asserted by identity on the rail element itself.

import React from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AgentChatProvider } from '../AgentChatProvider.js'
import ActivityPage from './ActivityPage.js'
import { activityLastSeenKey } from '../useActivity.js'
import { deskLastSeenKey } from '../useDesk.js'

const NOW = 1_700_000_000
const NOW_MS = NOW * 1000

let originalFetch: typeof globalThis.fetch
let deliveries: Record<string, unknown>[]
let events: Record<string, unknown>[]
let subscriptions: Record<string, unknown>[]
let schedules: Record<string, unknown>[]
let configEvents: Record<string, unknown>[]
let attentionRequests: Record<string, unknown>[]
let attentionStatus: number

const envelope = (over: Record<string, unknown> = {}) => ({
  depth: 0,
  source: 'external',
  worker: '',
  session_id: '',
  interactive: false,
  attention_requested: false,
  ...over,
})

beforeEach(() => {
  window.localStorage.clear()
  subscriptions = [
    {
      id: 's1',
      project: 'acme',
      event_type: 'worker.finished',
      filter: {},
      worker: 'archivist',
      max_firings_per_hour: 0,
      enabled: true,
    },
  ]
  events = [
    {
      id: 'e1',
      project: 'acme',
      type: 'worker.finished',
      text: 'email-answerer finished.',
      envelope: envelope({ source: 'core', worker: 'email-answerer' }),
      occurred_at: NOW - 3600,
      created_at: NOW - 3600,
      delivered: true,
    },
  ]
  deliveries = [
    {
      id: 'd1',
      project: 'acme',
      event_id: 'e1',
      subscription_id: 's1',
      session_id: 'sess-1',
      worker: 'archivist',
      status: 'ok',
      started_at: NOW - 3600,
      ended_at: NOW - 3559,
      created_at: NOW - 3600,
      updated_at: NOW - 3559,
    },
  ]
  schedules = []
  configEvents = [
    {
      id: 'c1',
      project: 'acme',
      actor_worker: 'email-reviewer',
      actor_session: 'sess-7',
      action: 'worker_prompt_write',
      payload: { name: 'email-answerer', system_prompt: 'Answer.\nQuote the reference.' },
      rationale: 'answers kept omitting the ticket reference',
      created_at: NOW_MS - 1000,
    },
  ]
  attentionRequests = []
  attentionStatus = 200

  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
    const u = String(url)
    const json = (v: unknown) =>
      new Response(JSON.stringify(v), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    if (u.includes('/agent/attention-requests')) {
      if (attentionStatus !== 200) {
        return new Response('attention requests are not configured on this host', {
          status: attentionStatus,
        })
      }
      return json({ attention_requests: attentionRequests })
    }
    if (u.includes('/agent/config-events')) return json({ config_events: configEvents })
    if (u.includes('/agent/deliveries')) return json({ deliveries })
    if (u.includes('/agent/subscriptions')) return json({ subscriptions })
    if (u.includes('/agent/schedules')) return json({ schedules })
    if (u.includes('/agent/events')) return json({ events })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
  window.localStorage.clear()
})

function renderActivity(props: Partial<React.ComponentProps<typeof ActivityPage>> = {}) {
  return render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <ActivityPage projectId="acme" nowSeconds={NOW} {...props} />
    </AgentChatProvider>,
  )
}

describe('one rail, every kind of record', () => {
  it('shows a job and a config change on the same rail', async () => {
    renderActivity()
    const rail = await screen.findByTestId('activity-rail')
    expect(await within(rail).findByText('worker.finished woke archivist')).toBeInTheDocument()
    expect(within(rail).getByText('email-reviewer rewrote email-answerer')).toBeInTheDocument()
  })

  it('orders them newest first across the two unit systems (J1)', async () => {
    renderActivity()
    const rail = await screen.findByTestId('activity-rail')
    await within(rail).findByText('worker.finished woke archivist')
    const rows = within(rail).getAllByRole('listitem')
    // The config change is stamped 1s before "now" in ms; the job an hour ago
    // in seconds. The change is newer, so it leads.
    expect(rows[0]).toHaveTextContent('email-reviewer rewrote email-answerer')
    expect(rows[1]).toHaveTextContent('worker.finished woke archivist')
  })

  it('carries the rationale a worker wrote', async () => {
    renderActivity()
    expect(
      await screen.findByText('answers kept omitting the ticket reference'),
    ).toBeInTheDocument()
  })

  it('says so when a human change carried no reason', async () => {
    configEvents = [
      {
        id: 'c9',
        project: 'acme',
        actor_worker: '',
        actor_session: '',
        action: 'schedule_update',
        payload: { id: 'sch-1', worker: 'daily-brief' },
        rationale: '',
        created_at: NOW_MS - 500,
      },
    ]
    renderActivity()
    expect(await screen.findByText('(no reason given)')).toBeInTheDocument()
  })
})

describe('the chips are lenses, not tabs', () => {
  it('subsets the rail without swapping the surface', async () => {
    renderActivity()
    const rail = await screen.findByTestId('activity-rail')
    await within(rail).findByText('worker.finished woke archivist')

    await userEvent.click(screen.getByTestId('activity-lens-changes'))

    // The SAME element, by identity — not a re-mounted list. This is the whole
    // difference between a merge and a rename.
    const after = await screen.findByTestId('activity-rail')
    expect(after).toBe(rail)
    await waitFor(() =>
      expect(within(after).queryByText('worker.finished woke archivist')).toBeNull(),
    )
    expect(within(after).getByText('email-reviewer rewrote email-answerer')).toBeInTheDocument()
  })

  it('filters to jobs', async () => {
    renderActivity()
    await screen.findByText('worker.finished woke archivist')
    await userEvent.click(screen.getByTestId('activity-lens-jobs'))
    await waitFor(() =>
      expect(screen.queryByText('email-reviewer rewrote email-answerer')).toBeNull(),
    )
    expect(screen.getByText('worker.finished woke archivist')).toBeInTheDocument()
  })

  it('marks the active chip as pressed', async () => {
    renderActivity()
    await screen.findByTestId('activity-rail')
    expect(screen.getByTestId('activity-lens-all')).toHaveAttribute('aria-pressed', 'true')
    await userEvent.click(screen.getByTestId('activity-lens-events'))
    expect(screen.getByTestId('activity-lens-events')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('activity-lens-all')).toHaveAttribute('aria-pressed', 'false')
  })

  it('names what would fill an empty lens rather than shrugging', async () => {
    renderActivity()
    await screen.findByTestId('activity-rail')
    await userEvent.click(screen.getByTestId('activity-lens-events'))
    // Every event here produced a delivery, so the events lens is genuinely empty.
    expect(await screen.findByText('No events have arrived.')).toBeInTheDocument()
  })
})

describe('asks on the rail', () => {
  it('joins a parked delivery to the sentence the worker wrote', async () => {
    deliveries = [
      {
        id: 'd-parked',
        project: 'acme',
        event_id: 'e1',
        subscription_id: 's1',
        session_id: 'sess-1',
        worker: 'email-answerer',
        status: 'awaiting_human',
        started_at: NOW - 9600,
        ended_at: 0,
        created_at: NOW - 9600,
        updated_at: NOW - 9600,
      },
    ]
    attentionRequests = [
      {
        id: 'a1',
        project: 'acme',
        session_id: 'sess-1',
        worker: 'email-answerer',
        message: 'Reply drafted for the Ridley invoice query — send as-is, or hold?',
        session_url: '/p/acme/s/sess-1',
        channel: 'webhook',
        delivered: true,
        expires_at: 0,
        created_at: NOW - 9600,
        answered_at: 0,
        timed_out_at: 0,
      },
    ]
    renderActivity()
    expect(await screen.findByText('email-answerer is waiting for you')).toBeInTheDocument()
    expect(
      screen.getByText('Reply drafted for the Ridley invoice query — send as-is, or hold?'),
    ).toBeInTheDocument()
  })

  it('shows a chat-session request that no delivery is carrying', async () => {
    // The Desk cannot see this one — it joins through deliveries — so it is the
    // case the rail exists to stop losing.
    deliveries = []
    attentionRequests = [
      {
        id: 'a-chat',
        project: 'acme',
        session_id: 'sess-chat',
        worker: 'marketing-manager',
        message: 'Which of these three headlines?',
        session_url: '',
        channel: 'none',
        delivered: false,
        expires_at: 0,
        created_at: NOW - 600,
        answered_at: 0,
        timed_out_at: 0,
      },
    ]
    renderActivity()
    expect(await screen.findByText('marketing-manager is waiting for you')).toBeInTheDocument()
    expect(screen.getByText('Which of these three headlines?')).toBeInTheDocument()
  })

  it('says the sentence is missing when the attention route is not mounted', async () => {
    attentionStatus = 501
    deliveries = [
      {
        id: 'd-parked',
        project: 'acme',
        event_id: 'e1',
        subscription_id: 's1',
        session_id: 'sess-1',
        worker: 'email-answerer',
        status: 'awaiting_human',
        started_at: NOW - 9600,
        ended_at: 0,
        created_at: NOW - 9600,
        updated_at: NOW - 9600,
      },
    ]
    renderActivity()
    expect(
      await screen.findByText(/does not serve/, { selector: 'div, p, span' }),
    ).toBeInTheDocument()
  })
})

describe('the watermark is its own', () => {
  it('does not share the Desk’s mark', async () => {
    renderActivity()
    await screen.findByTestId('activity-rail')
    await userEvent.click(screen.getByText('Mark these as seen'))

    await waitFor(() =>
      expect(window.localStorage.getItem(activityLastSeenKey('acme'))).not.toBeNull(),
    )
    // Reading Activity must not clear what the Desk is holding for the operator.
    expect(window.localStorage.getItem(deskLastSeenKey('acme'))).toBeNull()
  })
})

describe('failures and empties', () => {
  it('draws a failed job with its reason', async () => {
    deliveries = [
      {
        id: 'd-fail',
        project: 'acme',
        event_id: 'e1',
        subscription_id: 's1',
        session_id: 'sess-2',
        worker: 'archivist',
        status: 'failed',
        failure_reason: 'image "toolbox:9" names no image in the catalogue',
        started_at: NOW - 7200,
        ended_at: NOW - 7100,
        created_at: NOW - 7200,
        updated_at: NOW - 7100,
      },
    ]
    renderActivity()
    expect(
      await screen.findByText('worker.finished woke archivist — and it failed'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('image "toolbox:9" names no image in the catalogue'),
    ).toBeInTheDocument()
  })

  it('says nothing has happened rather than showing an empty rail', async () => {
    deliveries = []
    events = []
    configEvents = []
    renderActivity()
    expect(
      await screen.findByText('Nothing has happened in this project yet.'),
    ).toBeInTheDocument()
    expect(screen.queryByTestId('activity-rail')).toBeNull()
  })
})
