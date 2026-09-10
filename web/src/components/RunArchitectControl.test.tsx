// @vitest-environment jsdom
// The one control that spends real tokens on this screen: it confirms first,
// it posts an EVENT rather than opening a chat, and it reports a refusal in
// the server's own words.

import React from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import RunArchitectControl from './RunArchitectControl.js'

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string; body: unknown }[] = []
let response: { status: number; body: string }

beforeEach(() => {
  requests = []
  response = { status: 200, body: JSON.stringify({ id: 'ev-1', type: 'architect.run' }) }
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    requests.push({
      url: String(url),
      method: init?.method ?? 'GET',
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    })
    return new Response(response.body, {
      status: response.status,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('RunArchitectControl', () => {
  it('writes nothing until the confirmation is accepted', async () => {
    render(<RunArchitectControl />)
    await userEvent.click(screen.getByTestId('run-architect'))
    expect(requests).toHaveLength(0)

    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(requests).toHaveLength(0)
  })

  it('posts architect.run to the events route, and only that', async () => {
    render(<RunArchitectControl />)
    await userEvent.click(screen.getByTestId('run-architect'))
    await userEvent.click(screen.getByRole('button', { name: /run it/i }))
    await screen.findByTestId('run-architect-emitted')

    expect(requests).toHaveLength(1)
    expect(requests[0].method).toBe('POST')
    expect(requests[0].url).toContain('/agent/events')
    expect((requests[0].body as { type: string }).type).toBe('architect.run')
  })

  it('names the architect in the confirmation when it has one', async () => {
    render(<RunArchitectControl architectName="chief-of-staff" />)
    await userEvent.click(screen.getByTestId('run-architect'))
    expect(screen.getByText(/run chief-of-staff now\?/i)).toBeTruthy()
  })

  // The confirmation has to say what will actually happen: this thing changes
  // the project by itself, and revert is the whole control.
  it('says the architect makes the changes itself, and that they can be reverted', async () => {
    render(<RunArchitectControl />)
    await userEvent.click(screen.getByTestId('run-architect'))
    const dialog = screen.getByRole('dialog')
    expect(dialog.textContent).toMatch(/make those changes itself/i)
    expect(dialog.textContent).toMatch(/reverted from the changelog/i)
  })

  it('shows a refusal in the server words', async () => {
    response = { status: 500, body: 'host port pool is exhausted' }
    render(<RunArchitectControl />)
    await userEvent.click(screen.getByTestId('run-architect'))
    await userEvent.click(screen.getByRole('button', { name: /run it/i }))
    await waitFor(() => expect(screen.getByText('host port pool is exhausted')).toBeTruthy())
    expect(screen.queryByTestId('run-architect-emitted')).toBeNull()
  })
})
