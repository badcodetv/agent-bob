// @vitest-environment jsdom
// The onboarding rail, with the REAL chat underneath it.
//
// OnboardingPage.test.tsx mocks AgentChat and checks the session id it is
// handed — and that test passed the whole time the rail was broken. Passing an
// id to <AgentChat/> names a session; it does not bind one. Messages and `send`
// come from AgentChatProvider's current session, which nothing set, so in real
// use the interviewer's first reply never appeared and Send did nothing
// (reported 2026-09-13). This file mounts the provider, the page and the real
// AgentChat over a fake server, and asserts what a human would see: the reply
// in the rail, and a typed message arriving at the right session.

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import OnboardingPage from './OnboardingPage.js'
import { AgentChatProvider } from '../AgentChatProvider.js'
import { buildOnboardingSeed } from '../charter.js'

const SESSION = 'onboard-1'

function json(v: unknown, status = 200): Response {
  return new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } })
}

/** One assistant turn saying `text`, ending cleanly. */
function turn(text: string, id: string): Response {
  const events = [
    { type: 'message_start', data: { role: 'assistant', messageId: id } },
    { type: 'content_delta', data: { delta: text } },
    { type: 'message_end', data: {} },
    { type: 'query_complete', data: {} },
  ]
  const body = events
    .map((e) => `data: ${JSON.stringify({ ...e, timestamp: new Date().toISOString() })}\n\n`)
    .join('')
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

let originalFetch: typeof globalThis.fetch
let sent: { url: string; content: string }[] = []
/** The persisted transcript `resumeSession` replays. */
let history: unknown[] = []
let sessionStatus = 'running'

beforeEach(() => {
  sent = []
  history = []
  sessionStatus = 'running'
  originalFetch = globalThis.fetch
  vi.spyOn(console, 'log').mockImplementation(() => {})
  // jsdom has no layout, so AgentChat's autoscroll has nothing to call.
  Element.prototype.scrollIntoView = () => {}
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url)
    if (u.includes('/agent/charter/current')) return new Response(null, { status: 204 })
    if (u.endsWith('/status')) return json({ sandboxState: 'running', activeQuery: null })
    if (u.includes('/query-events')) return json({ events: history.length ? [{ events: history }] : [] })
    if (u.includes('/messages')) return json({ messages: [], total: 0 })
    if (u.endsWith('/message') && init?.method === 'POST') {
      const content = (JSON.parse(String(init.body)) as { content: string }).content
      sent.push({ url: u, content })
      return sent.length === 1
        ? turn('What is this project for, in one sentence?', 'a-1')
        : turn('Thanks — who is it for?', 'a-2')
    }
    if (u.endsWith('/artifacts')) return json({ artifacts: [] })
    if (u.endsWith(`/agent/session/${SESSION}`)) {
      return json({ id: SESSION, status: sessionStatus, workflowId: 'agent', persona: 'interviewer' })
    }
    return json({})
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

function mount(goal: string | null | undefined) {
  return render(
    <AgentChatProvider config={{ apiBaseUrl: 'http://api.test', models: [{ id: 'm', label: 'M' }] }}>
      <OnboardingPage sessionId={SESSION} goal={goal} refreshMs={0} />
    </AgentChatProvider>,
  )
}

describe('OnboardingPage inside a real chat provider', () => {
  it('seeds the interview through the rail, shows the reply, and sends what the human types', async () => {
    mount('Send one newsletter a week.')

    // The seed went to THIS session, carrying the goal and the session id the
    // interviewer must label its charter with.
    await screen.findByText('What is this project for, in one sentence?')
    expect(sent).toHaveLength(1)
    expect(sent[0].url).toContain(`/agent/session/${SESSION}/message`)
    expect(sent[0].content).toContain('Send one newsletter a week.')
    expect(sent[0].content).toContain(SESSION)

    // ...but the person sees their goal, not the interviewer's instructions.
    expect(screen.getByTestId('onboarding-seed').textContent).toBe('You set the goal: Send one newsletter a week.')
    expect(screen.queryByText(/Deposit the charter/)).toBeNull()

    const input = screen.getByTestId('chat-input') as HTMLTextAreaElement
    await waitFor(() => expect(input.disabled).toBe(false))
    await userEvent.type(input, 'For our readers.')
    await userEvent.click(screen.getByRole('button', { name: 'Send' }))

    await screen.findByText('Thanks — who is it for?')
    expect(sent).toHaveLength(2)
    expect(sent[1]).toEqual({ url: expect.stringContaining(`/agent/session/${SESSION}/message`), content: 'For our readers.' })
  })

  it('does not seed a second time when the transcript already has a human message (a reload)', async () => {
    history = [
      { type: 'user_message', data: { id: 'u-1', content: 'the earlier seed' }, timestamp: '2026-09-13T10:00:00Z' },
      { type: 'message_start', data: { role: 'assistant', messageId: 'a-0' }, timestamp: '2026-09-13T10:00:01Z' },
      { type: 'content_delta', data: { delta: 'Earlier question.' }, timestamp: '2026-09-13T10:00:01Z' },
      { type: 'message_end', data: {}, timestamp: '2026-09-13T10:00:02Z' },
      { type: 'query_complete', data: {}, timestamp: '2026-09-13T10:00:02Z' },
    ]
    mount('Send one newsletter a week.')

    await screen.findByText('Earlier question.')
    // Give a wrongly-firing seed effect every chance to run.
    await new Promise((r) => setTimeout(r, 50))
    expect(sent).toHaveLength(0)
  })

  it('shows a replayed seed as the goal line too', async () => {
    history = [
      { type: 'user_message', data: { id: 'u-1', content: buildOnboardingSeed(SESSION, 'Sell more books.') }, timestamp: '2026-09-13T10:00:00Z' },
      { type: 'message_start', data: { role: 'assistant', messageId: 'a-0' }, timestamp: '2026-09-13T10:00:01Z' },
      { type: 'content_delta', data: { delta: 'Earlier question.' }, timestamp: '2026-09-13T10:00:01Z' },
      { type: 'message_end', data: {}, timestamp: '2026-09-13T10:00:02Z' },
      { type: 'query_complete', data: {}, timestamp: '2026-09-13T10:00:02Z' },
    ]
    mount('Sell more books.')
    await screen.findByText('Earlier question.')
    expect(screen.getByTestId('onboarding-seed').textContent).toBe('You set the goal: Sell more books.')
    expect(screen.queryByText(new RegExp(SESSION + '\\.'))).toBeNull()
  })

  it('sends nothing on its own when no goal prop is given', async () => {
    mount(undefined)
    const input = (await screen.findByTestId('chat-input')) as HTMLTextAreaElement
    await waitFor(() => expect(input.disabled).toBe(false))
    await new Promise((r) => setTimeout(r, 50))
    expect(sent).toHaveLength(0)
  })

  it('does not seed a session that failed to start', async () => {
    sessionStatus = 'error'
    mount('Send one newsletter a week.')
    await screen.findByText(/This session failed to start/)
    await new Promise((r) => setTimeout(r, 50))
    expect(sent).toHaveLength(0)
  })
})
