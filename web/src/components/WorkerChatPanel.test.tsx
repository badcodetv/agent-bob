// @vitest-environment jsdom
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { AgentChatProvider } from '../AgentChatProvider.js'
import WorkerChatPanel from './WorkerChatPanel.js'
import type { Worker } from '../workers.js'

const worker: Worker = {
  project: 'acme',
  name: 'email-answerer',
  description: 'answers emails',
  system_prompt: 'Answer emails.',
  mcp_config: {},
  image: '',
  briefing: [],
  max_instances: 1,
  enabled: true,
  frozen: false,
  created_at: 0,
  updated_at: 0,
}

let originalFetch: typeof globalThis.fetch

beforeEach(() => {
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
    if (String(url).endsWith('/agent/session')) {
      return new Response(
        JSON.stringify({ id: 'sess-1', status: 'active', workflowId: 'agent', worker: 'email-answerer' }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }
    return new Response(JSON.stringify({ sandboxState: 'running', activeQuery: null }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

// G9: this panel used to promise "Its prompt, tools and briefing are composed
// by the server exactly as they would be for an automated job" — false, since
// a chat session (worker or not) never receives the project's briefing
// (docs/18-workers-memory-events.md §9, "Known limitations"). It must not say so.
it('does not claim the chat receives a briefing, or that it is exactly like a job', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <WorkerChatPanel worker={worker} projectId="acme" />
    </AgentChatProvider>,
  )
  expect(screen.queryByText(/exactly as they would be for an automated job/)).toBeNull()
  expect(screen.getByText(/none of the project.s briefing/)).toBeInTheDocument()
})

// G3: once the chat starts, AgentChat should know which worker it is talking
// to, so its empty state can name that worker rather than "the base agent".
it('passes the worker name to AgentChat once the chat has started', async () => {
  const user = userEvent.setup()
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <WorkerChatPanel worker={worker} projectId="acme" />
    </AgentChatProvider>,
  )
  await user.click(screen.getByRole('button', { name: /chat with email-answerer/i }))
  expect(await screen.findByText('This is a chat with email-answerer.')).toBeInTheDocument()
})
