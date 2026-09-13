// @vitest-environment jsdom
// T16 — the onboarding screen. What is worth a test here: the waiting state
// names its cause (a screen that just sits there gets clicked again, which is
// how you end up with two interviews); the charter appears without a reload,
// because the deposit happens outside the browser with no end signal; and the
// architect is started by an EVENT, never by a chat, because a chat receives
// no briefing at all.

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import OnboardingPage from './OnboardingPage.js'

// AgentChat drags a whole session client behind it, so here the stub records
// the session id it was given and nothing else. That is NOT proof the rail
// works — this stub passed throughout the time the real rail showed nothing and
// sent nothing. OnboardingPage.chat.test.tsx mounts the real chat for that.
let chatSessionIds: (string | undefined)[] = []
vi.mock('./AgentChat.js', () => ({
  default: (props: { sessionId?: string }) => {
    chatSessionIds.push(props.sessionId)
    return <div data-testid="agent-chat">chat for {props.sessionId ?? '(context)'}</div>
  },
}))

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string; body: unknown }[] = []
let charterResponse: { status: number; body: unknown }

const validCharterBody = {
  charter: {
    goal: 'Send one newsletter a week.',
    measure: 'Four in a month.',
    label_rules: 'kind=draft — written but not sent.',
    rationale: 'Repeat visits are the problem.',
  },
  summary: 'Charter v1: a weekly newsletter.',
  memory_id: 'mem-1',
  created_at: 1789000000123,
  valid: true,
  summary_of_effects: {
    architect_name: 'architect',
    architect_cron: '0 9 * * *',
    schedule_enabled: false,
    subscription_event: 'architect.run',
    memory_seed_labels: ['kind=project-goal,name=project-goal'],
    settings_fields: ['system_prompt', 'briefing'],
    worker_count: 1,
  },
}

beforeEach(() => {
  chatSessionIds = []
  requests = []
  charterResponse = { status: 204, body: null }
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url)
    const method = init?.method ?? 'GET'
    requests.push({ url: u, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    const json = (v: unknown, status = 200) =>
      new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } })

    if (u.includes('/agent/charter/current')) {
      if (charterResponse.status === 204) return new Response(null, { status: 204 })
      if (charterResponse.status !== 200) {
        return new Response(String(charterResponse.body), { status: charterResponse.status })
      }
      return json(charterResponse.body)
    }
    if (u.includes('/agent/charter/apply')) {
      return json({ workers: [{ name: 'architect' }], event: { id: 'ce-1' } })
    }
    if (u.includes('/agent/events')) return json({ id: 'ev-1', type: 'architect.run' })
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('OnboardingPage', () => {
  it('names the cause while the session is still starting', () => {
    render(<OnboardingPage sessionId="" />)
    const waiting = screen.getByTestId('onboarding-waiting')
    expect(waiting.textContent).toMatch(/container/i)
    expect(waiting.textContent).toMatch(/minute/i)
    expect(screen.queryByTestId('agent-chat')).toBeNull()
  })

  it('binds the rail to the named session explicitly, not to whatever the provider has', async () => {
    render(<OnboardingPage sessionId="onboard-1" />)
    await screen.findByTestId('agent-chat')
    expect(chatSessionIds).toContain('onboard-1')
    // Undefined would mean it fell through to AgentChatProvider's current
    // session, which is whichever one the shell happened to have selected.
    expect(chatSessionIds).not.toContain(undefined)
  })

  it('surfaces a session-creation failure verbatim', () => {
    render(<OnboardingPage sessionId="" sessionError="host port pool is exhausted" />)
    expect(screen.getByText('host port pool is exhausted')).toBeTruthy()
    expect(screen.queryByTestId('onboarding-waiting')).toBeNull()
  })

  it('says there is no charter yet, without calling it an error', async () => {
    render(<OnboardingPage sessionId="onboard-1" refreshMs={0} />)
    await screen.findByTestId('onboarding-no-charter')
    expect(screen.queryByTestId('onboarding-charter-error')).toBeNull()
    expect(screen.queryByTestId('charter-panel')).toBeNull()
  })

  it('reads an older server\'s 404 as "no charter yet" too', async () => {
    charterResponse = { status: 404, body: 'no charter has been proposed yet' }
    render(<OnboardingPage sessionId="onboard-1" refreshMs={0} />)
    await screen.findByTestId('onboarding-no-charter')
    expect(screen.queryByTestId('onboarding-charter-error')).toBeNull()
  })

  it('shows the charter when the poll finds one, with no reload', async () => {
    render(<OnboardingPage sessionId="onboard-1" refreshMs={20} />)
    await screen.findByTestId('onboarding-no-charter')

    // The interviewer deposits mid-conversation; nothing in the chat stream
    // says so, which is why the screen polls at all.
    charterResponse = { status: 200, body: validCharterBody }
    await screen.findByTestId('charter-panel', {}, { timeout: 2000 })
    expect(screen.getByText(/Send one newsletter a week/)).toBeTruthy()
  })

  it('offers to run the architect only after approval, and does it with an event', async () => {
    charterResponse = { status: 200, body: validCharterBody }
    render(<OnboardingPage sessionId="onboard-1" refreshMs={0} />)
    await screen.findByTestId('charter-panel')

    expect(screen.queryByTestId('run-architect')).toBeNull()

    await userEvent.click(screen.getByTestId('charter-approve'))
    await screen.findByTestId('onboarding-next')

    await userEvent.click(screen.getByTestId('run-architect'))
    await userEvent.click(screen.getByRole('button', { name: /run it/i }))
    await screen.findByTestId('run-architect-emitted')

    // An EVENT on /agent/events — not a message to a chat session. A chat
    // receives no briefing, so an architect talked to has never seen the
    // label registry.
    const posted = requests.filter((r) => r.method === 'POST' && r.url.includes('/agent/events'))
    expect(posted).toHaveLength(1)
    expect((posted[0].body as { type: string }).type).toBe('architect.run')
    expect(requests.some((r) => r.url.includes('/agent/session'))).toBe(false)
  })

  // The same screen, reloaded after someone else approved — or after this
  // person closed the tab between approving and reading. `applied` on
  // GET /agent/charter/current is a server fact now (DI10); before it existed
  // this hook only knew about an apply IT had performed, so a reload forgot the
  // approval had happened and offered "Approve" on an already-approved charter.
  // Nothing is clicked here: the state has to come off the wire.
  it('treats the server saying applied as approved, with no apply of its own', async () => {
    charterResponse = {
      status: 200,
      body: { ...validCharterBody, applied: true, applied_at: 1789000999000 },
    }
    render(<OnboardingPage sessionId="onboard-1" refreshMs={0} />)

    await screen.findByTestId('onboarding-next')
    expect(screen.getByTestId('run-architect')).toBeTruthy()
    // And it did not apply anything to learn that.
    expect(requests.some((r) => r.url.includes('/agent/charter/apply'))).toBe(false)
  })

  it('reports a failure to run the architect in the server words', async () => {
    charterResponse = { status: 200, body: validCharterBody }
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      const u = String(url)
      if (u.includes('/agent/charter/current')) {
        return new Response(JSON.stringify(validCharterBody), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (u.includes('/agent/charter/apply')) {
        return new Response(JSON.stringify({ workers: [], event: {} }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (u.includes('/agent/events')) {
        return new Response('host port pool is exhausted', { status: 500 })
      }
      return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
    }) as typeof globalThis.fetch

    render(<OnboardingPage sessionId="onboard-1" refreshMs={0} />)
    await screen.findByTestId('charter-panel')
    await userEvent.click(screen.getByTestId('charter-approve'))
    await screen.findByTestId('onboarding-next')
    await userEvent.click(screen.getByTestId('run-architect'))
    await userEvent.click(screen.getByRole('button', { name: /run it/i }))

    await waitFor(() => expect(screen.getByText('host port pool is exhausted')).toBeTruthy())
  })
})
