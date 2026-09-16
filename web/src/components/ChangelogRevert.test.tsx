// @vitest-environment jsdom
// T24 — "Revert to this version" in the changelog.
//
// This control is the WHOLE of a human's authority over a self-revising
// organisation (design C6: there is no mechanical brake on the architect;
// revert is the control). So what is pinned here is mostly what it refuses to
// do: revert without a reason, revert an entry that something newer has since
// written over, and — the one that would quietly break the log's promise —
// call any of this "undo".

import React from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import ChangelogView from './ChangelogView.js'

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string; body: unknown }[] = []
let revertResponse: { status: number; body: string }

const at = 1_789_000_000_000

const event = (
  id: string,
  seq: number,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> => ({
  id,
  seq,
  project: 'acme',
  actor_worker: 'prompt-tuner',
  actor_session: `sess-${id}`,
  action: 'worker_prompt_write',
  payload: { name: 'answerer', system_prompt: `v${seq}` },
  rationale: `because ${id}`,
  created_at: at + seq * 1000,
  ...overrides,
})

let stored: Record<string, unknown>[]

beforeEach(() => {
  requests = []
  revertResponse = { status: 200, body: JSON.stringify({ id: 'ce-new', seq: 99 }) }
  stored = [event('ce-2', 2), event('ce-1', 1)]
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url)
    const method = init?.method ?? 'GET'
    requests.push({ url: u, method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    if (u.includes('/revert')) {
      return new Response(revertResponse.body, { status: revertResponse.status })
    }
    return new Response(JSON.stringify({ config_events: stored }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

const reverts = () => requests.filter((r) => r.method === 'POST' && r.url.includes('/revert'))

describe('reverting a change', () => {
  // D5, and the log's whole promise: nothing is undone. Reverting writes a NEW
  // change, and both stay in the history forever. A button saying "undo" would
  // claim the history can be edited.
  it('never says "undo", anywhere in what it renders', async () => {
    const { container } = render(<ChangelogView />)
    await screen.findByTestId('revert-ce-2')

    await userEvent.click(screen.getByTestId('revert-ce-2'))
    await screen.findByRole('dialog')

    const rendered = document.body.textContent ?? ''
    expect(rendered.toLowerCase()).not.toContain('undo')
    expect(container.textContent?.toLowerCase() ?? '').not.toContain('undo')
  })

  it('offers the action on the newest change to a thing', async () => {
    render(<ChangelogView />)
    const button = await screen.findByTestId('revert-ce-2')
    expect(button).toBeEnabled()
  })

  // Rule 1 from the store, mirrored: every entry carries the whole new state of
  // the row, so putting an older one back would erase everything since.
  it('disables the action on an older change, and says why in words', async () => {
    render(<ChangelogView />)
    await screen.findByTestId('revert-ce-2')

    expect(screen.getByTestId('revert-ce-1')).toBeDisabled()
    const blocked = screen.getByTestId('revert-blocked-ce-1')
    expect(blocked.textContent).toMatch(/changed answerer after this one/i)
    expect(blocked.textContent).toMatch(/revert to the newest version first/i)
  })

  it('will not revert without a reason', async () => {
    render(<ChangelogView />)
    await userEvent.click(await screen.findByTestId('revert-ce-2'))
    await screen.findByRole('dialog')

    expect(screen.getByTestId('revert-confirm')).toBeDisabled()
    expect(reverts()).toHaveLength(0)

    await userEvent.type(screen.getByLabelText(/why\?/i), 'it got worse')
    expect(screen.getByTestId('revert-confirm')).toBeEnabled()
  })

  // "Are you sure?" is not a confirmation. The reader has been scrolling a list
  // of similar entries and the cost of the wrong one is a live worker's prompt.
  it('names what will change, and says nothing is erased', async () => {
    render(<ChangelogView />)
    await userEvent.click(await screen.findByTestId('revert-ce-2'))
    const dialog = await screen.findByRole('dialog')

    expect(within(dialog).getByText('answerer')).toBeInTheDocument()
    expect(dialog.textContent).toMatch(/nothing is erased/i)
    expect(dialog.textContent).toMatch(/revert the revert/i)
  })

  it('sends the reason and reloads the list', async () => {
    render(<ChangelogView />)
    await userEvent.click(await screen.findByTestId('revert-ce-2'))
    await userEvent.type(await screen.findByLabelText(/why\?/i), 'the rewrite made it worse')
    await userEvent.click(screen.getByTestId('revert-confirm'))

    await waitFor(() => expect(reverts()).toHaveLength(1))
    expect(reverts()[0].url).toContain('/agent/config-events/ce-2/revert')
    expect((reverts()[0].body as { rationale: string }).rationale).toBe('the rewrite made it worse')
    await screen.findByTestId('revert-done')
  })

  // The server checks the same rule again and its refusal NAMES the entries in
  // the way. A paraphrase would drop the part that says what to do next.
  it('renders a server refusal verbatim', async () => {
    revertResponse = {
      status: 409,
      body:
        'agentdb: revert refused: worker_prompt_write (seq 12) is not the newest change to ' +
        'worker:answerer — worker_prompt_write (seq 14) came after it',
    }
    render(<ChangelogView />)
    await userEvent.click(await screen.findByTestId('revert-ce-2'))
    await userEvent.type(await screen.findByLabelText(/why\?/i), 'back please')
    await userEvent.click(screen.getByTestId('revert-confirm'))

    const shown = await screen.findByTestId('revert-error')
    expect(shown.textContent).toContain('seq 14')
    expect(shown.textContent).toContain('is not the newest change')
  })

  it('draws no revert control at all when the host mounts it read-only', async () => {
    render(<ChangelogView allowRevert={false} />)
    await screen.findByText(/because ce-2/)
    expect(screen.queryByTestId('revert-ce-2')).toBeNull()
  })
})

describe('entries with no inverse', () => {
  it('refuses a topology apply, and says what to do instead', async () => {
    stored = [
      event('ce-t', 3, {
        action: 'topology_apply',
        payload: { topology: 'solo@v1' },
        actor_worker: '',
      }),
      ...stored,
    ]
    render(<ChangelogView />)
    await screen.findByTestId('revert-blocked-ce-t')
    expect(screen.getByTestId('revert-ce-t')).toBeDisabled()
    expect(screen.getByTestId('revert-blocked-ce-t').textContent).toMatch(
      /revert the individual entries/i,
    )
  })

  it('refuses a Google connect or disconnect, and points at Settings instead', async () => {
    stored = [
      event('ce-d', 4, {
        action: 'connection_disconnect',
        payload: { account: 'google', account_email: 'office@example.com', disconnected_by: 'op@example.com' },
        actor_worker: '',
        rationale: 'Google account disconnected from the console',
      }),
      event('ce-c', 3, {
        action: 'connection_connect',
        payload: { account: 'google', account_email: 'office@example.com', connected_by: 'op@example.com' },
        actor_worker: '',
        rationale: 'Google account connected from the console',
      }),
      ...stored,
    ]
    render(<ChangelogView />)
    for (const id of ['ce-d', 'ce-c']) {
      await screen.findByTestId(`revert-blocked-${id}`)
      expect(screen.getByTestId(`revert-${id}`)).toBeDisabled()
      expect(screen.getByTestId(`revert-blocked-${id}`).textContent).toMatch(
        /use Connect Google or Disconnect in Settings/,
      )
    }
  })
})
