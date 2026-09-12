// @vitest-environment jsdom
// DK2: the Desk — the three stacks on the spine, the "since you last looked"
// mark in localStorage, the first-run state, and the degraded render when the
// attention route is not mounted.

import React from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AgentChatProvider } from '../AgentChatProvider.js'
import DeskPage from './DeskPage.js'
import { deskLastSeenKey, readDeskLastSeen, writeDeskLastSeen } from '../useDesk.js'
import { firstsSeenKey, writeFirstsSeen } from '../useFirsts.js'
import { GuideProvider, type GuideParagraphMap } from '../guide/GuideProvider.js'

// A moment safely in the past: `markSeen` stamps the real wall clock, and a
// fixture newer than today would never fall behind the mark.
const NOW = 1_700_000_000
const NOW_MS = NOW * 1000

let originalFetch: typeof globalThis.fetch
let workers: Record<string, unknown>[]
let deliveries: Record<string, unknown>[]
let subscriptions: Record<string, unknown>[]
let events: Record<string, unknown>[]
let schedules: Record<string, unknown>[]
let configEvents: Record<string, unknown>[]
let attentionRequests: Record<string, unknown>[]
let memories: Record<string, unknown>[]
let attentionStatus: number
let workersStatus: number
let deliveriesStatus: number
let configEventsStatus: number

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
  workers = [{ name: 'email-answerer', project: 'acme', system_prompt: 'Answer.', enabled: true }]
  subscriptions = [
    {
      id: 's1',
      project: 'acme',
      event_type: 'email.received',
      filter: {},
      worker: 'email-answerer',
      max_firings_per_hour: 0,
      enabled: true,
    },
  ]
  deliveries = [
    {
      id: 'd1',
      project: 'acme',
      event_id: 'e1',
      subscription_id: 's1',
      session_id: 'sess-1',
      status: 'awaiting_human',
      started_at: NOW - 9600,
      ended_at: 0,
      created_at: NOW - 9600,
      updated_at: NOW - 9600,
    },
    {
      id: 'd2',
      project: 'acme',
      event_id: 'e2',
      subscription_id: 's1',
      session_id: 'sess-2',
      status: 'failed',
      started_at: NOW - 7200,
      ended_at: NOW - 7100,
      created_at: NOW - 7200,
      updated_at: NOW - 7100,
    },
  ]
  events = [
    {
      id: 'pe1',
      project: 'acme',
      type: 'worker.freeze_refused',
      text: 'Refused worker_prompt_write against frozen worker "fee-scorer". Attempted by worker "tuner".',
      envelope: envelope({ source: 'core', worker: 'tuner', session_id: 'sess-9' }),
      occurred_at: NOW - 3600,
      created_at: NOW - 3600,
      delivered: false,
    },
  ]
  schedules = [
    {
      id: 'sch-1',
      project: 'acme',
      worker: 'nightly-sweep',
      cron: '0 2 * * *',
      input: 'Sweep.',
      enabled: false,
      created_at: NOW - 100_000,
      updated_at: NOW - 1000,
      provision_failures: 5,
      last_provision_error: 'image "toolbox:9" names no image in the catalogue',
    },
  ]
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
  // Empty by default: most tests here predate `first-memory` and must see no
  // change in behaviour from a route they never asked about.
  memories = []
  attentionStatus = 200
  workersStatus = 200
  deliveriesStatus = 200
  configEventsStatus = 200

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
        // The STATUS decides "unwired vs failed" since B6 — 404/501 mean the
        // host does not serve the route and everything else is a failure. The
        // bodies below deliberately share the words the old text match keyed
        // on, so a regression to prose-matching fails these tests.
        return new Response(
          attentionStatus === 501
            ? 'attention requests are not configured on this host'
            : 'attention requests: relation "attention_requests" not found, database is down',
          { status: attentionStatus },
        )
      }
      return json({ attention_requests: attentionRequests })
    }
    if (u.includes('/agent/config-events')) {
      if (configEventsStatus !== 200) {
        return new Response('config log: the changelog table was not found', {
          status: configEventsStatus,
        })
      }
      return json({ config_events: configEvents })
    }
    if (u.includes('/agent/deliveries')) {
      if (deliveriesStatus !== 200) {
        return new Response('deliveries: database is down', { status: deliveriesStatus })
      }
      return json({ deliveries })
    }
    if (u.includes('/agent/memories')) return json({ memories })
    if (u.includes('/agent/subscriptions')) return json({ subscriptions })
    if (u.includes('/agent/schedules')) return json({ schedules })
    if (u.includes('/agent/workers')) {
      if (workersStatus !== 200) {
        return new Response('workers: connection refused', { status: workersStatus })
      }
      return json({ workers })
    }
    if (u.includes('/agent/events')) return json({ events })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
  window.localStorage.clear()
})

function renderDesk(props: Partial<React.ComponentProps<typeof DeskPage>> = {}) {
  return render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <DeskPage projectId="acme" nowSeconds={NOW} {...props} />
    </AgentChatProvider>,
  )
}

/** With a `GuideProvider` mounted above the Desk — the state in which a
 *  first-narration line gains its "Read more in the guide →" link. */
function renderDeskWithGuide(
  props: Partial<React.ComponentProps<typeof DeskPage>> = {},
  paragraphs: GuideParagraphMap = { desk: { slug: 'the-desk', text: 'The Desk answers three questions.' } },
) {
  return render(
    <GuideProvider paragraphs={paragraphs}>
      <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
        <DeskPage projectId="acme" nowSeconds={NOW} {...props} />
      </AgentChatProvider>
    </GuideProvider>,
  )
}

describe('the three stacks', () => {
  it('asks first, with the sentence the worker wrote and how long it has waited', async () => {
    renderDesk()
    const asks = await screen.findByRole('region', { name: 'Asks' })
    // The section mounts before its rows do — awaiting the region only proves
    // the heading is there, and its count still reads 0. Every first assertion
    // inside a region has to be a findBy for that reason; the ones after it can
    // be synchronous.
    expect(
      await within(asks).findByText('email-answerer · awaiting_human · 2h 40m'),
    ).toBeInTheDocument()
    expect(within(asks).getByText(/Ridley invoice query/)).toBeInTheDocument()
    expect(within(asks).getByText(/stays parked at awaiting_human/)).toBeInTheDocument()
  })

  it('reads the changes as a changelog: who, what, and why', async () => {
    renderDesk()
    const changes = await screen.findByRole('region', { name: 'Changes' })
    expect(
      await within(changes).findByText('email-reviewer rewrote email-answerer'),
    ).toBeInTheDocument()
    expect(
      within(changes).getByText('answers kept omitting the ticket reference'),
    ).toBeInTheDocument()
  })

  it('collects the three failure shapes, each with the sentence the docs wrote', async () => {
    renderDesk()
    const trouble = await screen.findByRole('region', { name: 'Trouble' })
    expect(
      await within(trouble).findByText('1 delivery failed · worker email-answerer'),
    ).toBeInTheDocument()
    expect(within(trouble).getByText(/No reason is recorded on a delivery row/)).toBeInTheDocument()
    expect(
      within(trouble).getByText('schedule nightly-sweep (0 2 * * *) disabled after 5 failed starts'),
    ).toBeInTheDocument()
    expect(within(trouble).getByText(/toolbox:9/)).toBeInTheDocument()
    expect(within(trouble).getByText('fee-scorer refused 1 rewrite from tuner')).toBeInTheDocument()
  })

  it('never writes: everything it does is a GET', async () => {
    renderDesk()
    await screen.findByText(/Ridley invoice query/)
    const calls = (globalThis.fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls
    expect(calls.length).toBeGreaterThan(0)
    for (const call of calls) {
      const init = call[1] as RequestInit | undefined
      expect(init?.method ?? 'GET').toBe('GET')
    }
  })

  it('opens the thread through the host, rather than navigating itself', async () => {
    const onOpenSession = vi.fn()
    renderDesk({ onOpenSession })
    const asks = await screen.findByRole('region', { name: 'Asks' })
    await userEvent.click(await within(asks).findByRole('button', { name: 'open thread' }))
    expect(onOpenSession).toHaveBeenCalledWith('sess-1')
  })
})

describe('since you last looked', () => {
  it('hides changes older than the stored mark', async () => {
    window.localStorage.setItem(deskLastSeenKey('acme'), String(NOW_MS))
    renderDesk()
    const changes = await screen.findByRole('region', { name: 'Changes' })
    await waitFor(() =>
      expect(
        within(changes).getByText('Nothing has changed since you last looked.'),
      ).toBeInTheDocument(),
    )
  })

  it('stores the mark per project when the operator marks them seen', async () => {
    renderDesk()
    await screen.findByText('email-reviewer rewrote email-answerer')
    await userEvent.click(screen.getByRole('button', { name: 'Mark these changes as seen' }))
    await waitFor(() => expect(readDeskLastSeen('acme')).toBeGreaterThan(0))
    // Another project's Desk is untouched — the mark is keyed, not global.
    expect(readDeskLastSeen('other')).toBe(0)
    expect(screen.getByText('Nothing has changed since you last looked.')).toBeInTheDocument()
  })

  it('treats unreadable or rubbish storage as "never looked"', () => {
    window.localStorage.setItem(deskLastSeenKey('acme'), 'yesterday')
    expect(readDeskLastSeen('acme')).toBe(0)
    writeDeskLastSeen('acme', 42)
    expect(readDeskLastSeen('acme')).toBe(42)
  })
})

describe('degraded and empty', () => {
  it('still shows the asks when the attention route is not mounted, and says why they are wordless', async () => {
    attentionStatus = 501
    renderDesk()
    const asks = await screen.findByRole('region', { name: 'Asks' })
    expect(
      await within(asks).findByText('email-answerer · awaiting_human · 2h 40m'),
    ).toBeInTheDocument()
    expect(within(asks).getByText(/does not serve/)).toBeInTheDocument()
    expect(screen.queryByText(/Ridley invoice query/)).toBeNull()
  })

  // RD27: the route is mounted (500, not 404/501), so `available` stays true.
  // Before the fix the empty list from the failed fetch was believed, and an
  // operator with a parked approval read "Nothing is waiting on you".
  it('never says nothing is waiting when the attention route failed', async () => {
    attentionStatus = 500
    renderDesk()
    const asks = await screen.findByRole('region', { name: 'Asks' })
    expect(
      await within(asks).findByText('email-answerer · awaiting_human · 2h 40m'),
    ).toBeInTheDocument()
    expect(within(asks).queryByText('Nothing is waiting on you.')).toBeNull()
    // The failure is stated, and stated as a failure rather than as "this
    // deployment does not serve it" — which would be false here.
    expect(within(asks).getByText(/did not answer/)).toBeInTheDocument()
    expect(within(asks).queryByText(/does not serve/)).toBeNull()
    expect(await screen.findByText(/database is down/)).toBeInTheDocument()
  })

  // B6: the 500 body above says "not found", which is exactly what the old
  // text-matching classifier keyed on. If the classifier ever goes back to
  // reading prose, the assertions above flip and this deployment's outage is
  // reported to the operator as a deployment limitation.
  it('a failed config log is an error, not "this deployment does not serve it"', async () => {
    configEventsStatus = 500
    renderDesk()
    // useDesk drops `log.error` from its chain whenever the log reads as
    // unmounted, so a misclassified 500 shows the operator nothing at all.
    expect(await screen.findByText(/the changelog table was not found/)).toBeInTheDocument()
  })

  // RD28: a failed worker list leaves the initial [], and the first-run panel
  // replaces the whole Desk — inviting an established project to start over.
  it('does not offer the first-run state when the worker list failed to load', async () => {
    workersStatus = 500
    renderDesk()
    expect(await screen.findByText(/workers: connection refused/)).toBeInTheDocument()
    expect(screen.queryByText('This project has no workers yet')).toBeNull()
    // The Desk itself is still there, with what did load.
    expect(await screen.findByRole('region', { name: 'Asks' })).toBeInTheDocument()
  })

  // The same class one line down: three empty stacks after three failed loads
  // are not evidence that the fleet ran and nobody needed you.
  it('does not claim a quiet Desk when the loads failed', async () => {
    // The workers DO load here (so the first-run panel is not what hides the
    // sentence — this test would otherwise pass against the unfixed code for
    // the wrong reason); it is the delivery list that fails.
    deliveriesStatus = 500
    deliveries = []
    events = []
    schedules = []
    configEvents = []
    attentionRequests = []
    renderDesk()
    expect(await screen.findByText(/deliveries: database is down/)).toBeInTheDocument()
    expect(screen.queryByText(/the fleet ran and nobody needed you/)).toBeNull()
  })

  it('shows the first-run state — the two doors in — when the project has no workers', async () => {
    workers = []
    deliveries = []
    events = []
    schedules = []
    configEvents = []
    attentionRequests = []
    const onStartFromTopology = vi.fn()
    const onOpenChat = vi.fn()
    renderDesk({ onStartFromTopology, onOpenChat })
    expect(await screen.findByText('This project has no workers yet')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Start from an org chart' }))
    expect(onStartFromTopology).toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Open chat' }))
    expect(onOpenChat).toHaveBeenCalled()
    // Never a bare "nothing to show".
    expect(screen.queryByText(/nothing to show/i)).toBeNull()
  })

  // A2 / design §3 G1: while a project is in interview, the first-run panel
  // offers a way back into it instead of the two ordinary doors — and the
  // topology seed, the architect's designed alternative, is withheld.
  it('offers "Finish setting up this project" instead of the two doors while in interview', async () => {
    workers = []
    deliveries = []
    events = []
    schedules = []
    configEvents = []
    attentionRequests = []
    const onStartFromTopology = vi.fn()
    const onOpenChat = vi.fn()
    const onOpenOnboarding = vi.fn()
    renderDesk({ onStartFromTopology, onOpenChat, inInterview: true, onOpenOnboarding })
    expect(await screen.findByText('This project is being set up')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Start from an org chart' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Open chat' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Finish setting up this project' }))
    expect(onOpenOnboarding).toHaveBeenCalled()
  })

  // The interviewer itself is a worker (`go/topology/onboarding.go`), so a
  // real interview never has zero workers — `firstRun` alone would never fire
  // and the row above would be unreachable for the flow it exists for.
  it('offers the same row when the interview already has a worker (the interviewer)', async () => {
    workers = [{ name: 'interviewer', project: 'acme', system_prompt: 'Interview.', enabled: true }]
    deliveries = []
    events = []
    schedules = []
    configEvents = []
    attentionRequests = []
    renderDesk({ inInterview: true, onOpenOnboarding: vi.fn() })
    expect(await screen.findByText('This project is being set up')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Finish setting up this project' })).toBeInTheDocument()
  })

  it('a quiet Desk in a working project says the fleet ran and nobody needed you', async () => {
    deliveries = []
    events = []
    schedules = []
    configEvents = []
    attentionRequests = []
    renderDesk()
    expect(await screen.findByText(/nobody needed you/)).toBeInTheDocument()
    expect(screen.getByText('Nothing is waiting on you.')).toBeInTheDocument()
    expect(screen.getByText('Nothing has failed.')).toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// W4 — feed liveness (doc 21 §4.2)
// ---------------------------------------------------------------------------

describe('liveness', () => {
  it('hangs each stack off a role="log", not a role="feed"', async () => {
    renderDesk()
    await screen.findByText('email-reviewer rewrote email-answerer')
    const logs = screen.getAllByRole('log')
    expect(logs.map((l) => l.getAttribute('aria-label'))).toEqual(
      expect.arrayContaining(['Asks', 'Changes', 'Trouble']),
    )
    // `feed` drags in an article-navigation keyboard contract we do not
    // implement; `log` is chronological and implicitly polite.
    expect(screen.queryAllByRole('feed')).toHaveLength(0)
  })

  it('draws a labelled waterline with the already-read changes beneath it', async () => {
    // A mark AFTER the fixture's config events: everything is "already read",
    // so the line has something to divide.
    window.localStorage.setItem(deskLastSeenKey('acme'), String(NOW_MS))
    renderDesk()
    const changes = await screen.findByRole('region', { name: 'Changes' })
    // The window is still the window — the empty line stays honest…
    expect(
      within(changes).getByText('Nothing has changed since you last looked.'),
    ).toBeInTheDocument()
    // …and the divider now says WHEN, with the change underneath it.
    const rule = await within(changes).findByRole('separator')
    expect(rule.getAttribute('aria-label')).toMatch(/^New since /)
    expect(within(changes).getByText('email-reviewer rewrote email-answerer')).toBeInTheDocument()
  })

  it('carries no waterline on a first visit — a line above everything says nothing', async () => {
    renderDesk()
    const changes = await screen.findByRole('region', { name: 'Changes' })
    expect(within(changes).queryByRole('separator')).toBeNull()
  })

  it('offers the pause toggle exactly where something is actually polling', async () => {
    renderDesk()
    await screen.findByText('email-reviewer rewrote email-answerer')
    // Nothing polls by default, so there is nothing to pause and no switch.
    expect(screen.queryByRole('checkbox', { name: 'Pause live updates' })).toBeNull()
  })

  it('shows the toggle when the host asked for a live Desk', async () => {
    renderDesk({ refreshMs: 60_000 })
    await screen.findByText('email-reviewer rewrote email-answerer')
    expect(screen.getByRole('checkbox', { name: 'Pause live updates' })).toBeInTheDocument()
  })

  it('reports its ask count up, so the shell badge need not fetch again (X7)', async () => {
    const onAsksCount = vi.fn()
    renderDesk({ onAsksCount })
    await screen.findByText('email-answerer · awaiting_human · 2h 40m')
    await waitFor(() => expect(onAsksCount).toHaveBeenCalledWith(1))
  })

  it('announces an ask coarsely while its ticking headline stays aria-hidden', async () => {
    renderDesk()
    const headline = await screen.findByText('email-answerer · awaiting_human · 2h 40m')
    // The digits change every second; a screen reader must not read them out.
    expect(headline).toHaveAttribute('aria-hidden')
    expect(
      screen.getByText('email-answerer · awaiting_human · waiting about 2 hours'),
    ).toBeInTheDocument()
    // And past an hour it says so in words, not only in colour.
    expect(screen.getByText('waiting over 1h')).toBeInTheDocument()
  })
})

describe('firsts — the Desk narrates them once (design §3 G4)', () => {
  it('narrates the first rewrite on its change row, and the first ask on its row', async () => {
    renderDesk()
    const changes = await screen.findByRole('region', { name: 'Changes' })
    expect(
      within(changes).getByTestId('first-first-rewrite'),
    ).toHaveTextContent("This is the project's first rewrite.")

    const asks = await screen.findByRole('region', { name: 'Asks' })
    expect(within(asks).getByTestId('first-first-ask')).toHaveTextContent(
      "This is the project's first ask.",
    )
  })

  it('links to the guide when one is mounted, and omits the link when it is not', async () => {
    const plainRender = renderDesk()
    const plain = await screen.findByTestId('first-first-rewrite')
    expect(within(plain).queryByRole('link')).toBeNull()
    // A real fresh visit, not a second copy layered into the same DOM — the
    // seen-set from the first render must not carry over either, so this is
    // a different project.
    plainRender.unmount()

    renderDeskWithGuide({ projectId: 'acme-2' })
    const linked = await screen.findByTestId('first-first-rewrite')
    const link = within(linked).getByRole('link', { name: /Read more in the guide/ })
    expect(link).toHaveAttribute('href', '#/guide/the-architect')
  })

  it('narrates the first worker on the worker_create row, and the sentence is written once (not in JSX)', async () => {
    configEvents = [
      {
        id: 'w1',
        project: 'acme',
        actor_worker: '',
        actor_session: '',
        action: 'worker_create',
        payload: { name: 'newsletter-writer' },
        rationale: '',
        created_at: NOW_MS - 5000,
      },
    ]
    renderDesk()
    const first = await screen.findByTestId('first-first-worker')
    expect(first).toHaveTextContent(
      "This is the project's first worker. A worker is a set of instructions and a trigger",
    )
  })

  it('narrates first-memory even though a memory write is not a config event', async () => {
    memories = [
      {
        id: 'm1',
        project: 'acme',
        labels: { kind: 'note' },
        snippet: 'Ellen prefers the newsletter to go out on Fridays.',
        score: 0,
        created_by_worker: 'newsletter-writer',
        created_by_session: 'sess-1',
        created_at: NOW_MS - 2000,
      },
    ]
    renderDesk()
    const first = await screen.findByTestId('first-first-memory')
    expect(first).toHaveTextContent("This is the project's first memory.")
    // No checklist, no count.
    expect(screen.queryByText(/\d\s*of\s*7/i)).toBeNull()
  })

  it('never narrates a kind twice per project: a seen-set from a previous visit silences it', async () => {
    writeFirstsSeen('acme', ['first-rewrite', 'first-ask'])
    renderDesk()
    await screen.findByText('email-reviewer rewrote email-answerer')
    expect(screen.queryByTestId('first-first-rewrite')).toBeNull()
    expect(screen.queryByTestId('first-first-ask')).toBeNull()
  })

  it('does not reset the seen-set when the worker that earned it is gone', async () => {
    // First visit: the fixture's rewrite narrates, and the effect persists
    // "first-rewrite" as seen.
    const first = renderDesk()
    await screen.findByTestId('first-first-rewrite')
    await waitFor(() => expect(readWatermarkOf('agentkit.firsts.acme')).toContain('first-rewrite'))
    first.unmount()

    // A second, later visit (a genuinely fresh mount — the worker and its
    // whole history are gone, as if deleted): an empty changelog. The
    // sentence must not return just because there is nothing left to
    // contradict it.
    configEvents = []
    renderDesk()
    await screen.findByRole('region', { name: 'Changes' })
    expect(screen.queryByTestId('first-first-rewrite')).toBeNull()
  })
})

function readWatermarkOf(key: string): string[] {
  const raw = window.localStorage.getItem(key)
  return raw ? (JSON.parse(raw) as string[]) : []
}
