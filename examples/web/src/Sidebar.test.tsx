// @vitest-environment jsdom
// The sidebar's session list is read when it mounts — and for a new project it
// mounts BEFORE the interview's `onboard` session exists, so the list said "No
// sessions yet" for the whole interview. It must re-read when that session
// appears.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { AgentChatProvider } from '@agentkit/chat-ui'
import Sidebar from './Sidebar.js'

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

function renderSidebar(onboardSessionId: string | null) {
  return (
    <AgentChatProvider config={{ apiBaseUrl: 'http://api.test', models: [{ id: 'm', label: 'M' }] }}>
      <Sidebar
        auth={{ email: 'kai@example.com', projects: [{ id: 'bakery', token: 't' }], selectedProject: 'bakery' }}
        project="bakery"
        onSwitchProject={() => {}}
        onCreateProject={async () => {}}
        onSignOut={() => {}}
        onboardSessionId={onboardSessionId}
        inInterview={onboardSessionId !== null}
      />
    </AgentChatProvider>
  )
}

describe('Sidebar', () => {
  it('re-reads the session list when the interview session appears', async () => {
    const listCalls: string[] = []
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const u = String(url)
      if (u.includes('/agent/sessions')) listCalls.push(u)
      return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
    }) as typeof globalThis.fetch

    const { rerender } = render(renderSidebar(null))
    await waitFor(() => expect(listCalls.length).toBe(1))

    rerender(renderSidebar('sess-onboard'))
    await waitFor(() => expect(listCalls.length).toBe(2))
  })
})
