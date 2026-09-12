// @vitest-environment jsdom
// MB1: the memory browser — the selector bar as chips, the honesty notes that
// come with a text query, the `name=` fold, provenance, and the two failure
// modes told apart (a bad selector vs a route that is not served here).

import React from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AgentChatProvider } from '../AgentChatProvider.js'
import MemoryBrowserPage from './MemoryBrowserPage.js'

const MS = 1_700_000_000_000

let originalFetch: typeof globalThis.fetch
let memories: Record<string, unknown>[]
let status: number
let body: string
let urls: string[]
let posted: { url: string; body: unknown }[]
/** What POST /agent/memories answers with — a test overrides it to return the
 *  row the write should be seen to have created. */
let postResponse: () => Record<string, unknown>

const memory = (over: Record<string, unknown> = {}) => ({
  id: 'm1',
  labels: { kind: 'lesson' },
  snippet: 'Quote the ticket reference in every reply.',
  score: 0.03,
  created_by_worker: 'email-reviewer',
  created_by_session: 'sess-7',
  created_at: MS,
  ...over,
})

beforeEach(() => {
  memories = [memory()]
  status = 200
  body = ''
  urls = []
  posted = []
  postResponse = () => ({
    id: 'new-1',
    labels: {},
    content: '',
    created_by_worker: '',
    created_by_session: '',
    created_at: MS,
  })
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const u = String(url)
    urls.push(u)
    if (init?.method === 'POST' && u.includes('/agent/memories')) {
      posted.push({ url: u, body: JSON.parse(String(init.body)) })
      return new Response(JSON.stringify(postResponse()), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (u.includes('/agent/memories')) {
      if (status !== 200) return new Response(body, { status })
      return new Response(JSON.stringify({ memories }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

function renderBrowser(props: Partial<React.ComponentProps<typeof MemoryBrowserPage>> = {}) {
  return render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <MemoryBrowserPage {...props} />
    </AgentChatProvider>,
  )
}

describe('the selector bar', () => {
  it('teaches the grammar: no OR, and clauses become chips', async () => {
    renderBrowser({ selector: 'kind=lesson,worker=email-answerer' })
    expect(await screen.findByText(/There is no OR/)).toBeInTheDocument()
    const chips = screen.getByTestId('selector-chips')
    expect(within(chips).getByText('kind=lesson')).toBeInTheDocument()
    expect(within(chips).getByText('worker=email-answerer')).toBeInTheDocument()
  })

  it('names an invalid clause the way the parser does, before the round trip', async () => {
    renderBrowser()
    const field = await screen.findByTestId('memory-selector')
    await userEvent.type(field, 'kind in (a')
    await waitFor(() =>
      expect(screen.getByText(`selector "kind in (a": unbalanced '('`)).toBeInTheDocument(),
    )
  })

  it('searches the route with the selector and the text', async () => {
    renderBrowser()
    await screen.findByText(/Quote the ticket reference/)
    await userEvent.type(await screen.findByTestId('memory-selector'), 'kind=lesson')
    await userEvent.type(await screen.findByTestId('memory-query'), 'reference')
    await userEvent.click(screen.getByRole('button', { name: 'Search' }))
    await waitFor(() =>
      expect(urls.some((u) => u.includes('selector=kind%3Dlesson') && u.includes('query=reference')))
        .toBe(true),
    )
  })

  it('deleting a chip re-runs the search without that clause', async () => {
    renderBrowser({ selector: 'kind=lesson,worker=w' })
    const chips = await screen.findByTestId('selector-chips')
    const chip = within(chips).getByText('kind=lesson').closest('.MuiChip-root') as HTMLElement
    await userEvent.click(within(chip).getByTestId('CancelIcon'))
    await waitFor(() =>
      expect(urls.some((u) => u.includes('selector=worker%3Dw') && !u.includes('kind'))).toBe(true),
    )
  })
})

describe('honesty about relevance', () => {
  it('says nothing about RRF when there is no text query', async () => {
    renderBrowser()
    await screen.findByText(/Quote the ticket reference/)
    expect(screen.queryByText(/Ranked by RRF/)).not.toBeInTheDocument()
  })

  it('with a query, states that a low score means nothing good matched', async () => {
    renderBrowser({ query: 'reference' })
    expect(await screen.findByText(/no relevance threshold/)).toBeInTheDocument()
  })

  it('flags a keyword-only-looking result set', async () => {
    renderBrowser({ query: 'invoices' })
    expect(await screen.findByText(/semantic leg is off/)).toBeInTheDocument()
  })
})

describe('rows', () => {
  it('folds the name= convention: current value first, superseded beneath', async () => {
    memories = [
      memory({ id: 'a', labels: { name: 'tone' }, snippet: 'Older tone.', created_at: MS - 5000 }),
      memory({ id: 'b', labels: { name: 'tone' }, snippet: 'Current tone.', created_at: MS }),
    ]
    renderBrowser()
    expect(await screen.findByText('name=tone')).toBeInTheDocument()
    expect(screen.getByText('Current tone.')).toBeInTheDocument()
    expect(screen.getByText(/1 superseded value/)).toBeInTheDocument()
    expect(screen.getByText(/nothing was deleted/)).toBeInTheDocument()
  })

  it('carries provenance: the worker, the time, and a link to the thread', async () => {
    const onOpenSession = vi.fn()
    renderBrowser({ onOpenSession })
    expect(await screen.findByText('email-reviewer')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'open the session' }))
    expect(onOpenSession).toHaveBeenCalledWith('sess-7')
  })

  it('renders labels as chips on an unnamed row', async () => {
    renderBrowser()
    const row = await screen.findByTestId('memory-row')
    expect(within(row).getByText('kind=lesson')).toBeInTheDocument()
  })
})

describe('failures are told apart', () => {
  it('a 400 is the operator’s selector, shown with the parser’s own words', async () => {
    status = 400
    body = 'selector term "no spaces": label key "no spaces" is invalid'
    renderBrowser({ selector: 'x=1' })
    expect(await screen.findByTestId('memory-selector-error')).toHaveTextContent(
      /label key "no spaces" is invalid/,
    )
  })

  it('a 501 says memory is not available here, not that something failed', async () => {
    status = 501
    body = 'the memory store is not configured on this host'
    renderBrowser()
    expect(await screen.findByText(/Memory is not available on this host/)).toBeInTheDocument()
  })

  it('an empty project says memories are written by workers, not here', async () => {
    memories = []
    renderBrowser()
    expect(await screen.findByText(/Nothing has been remembered in this project yet/)).toBeInTheDocument()
  })

  // RD27/RD28's class: the route IS served (500, not 501), so the empty list is
  // the failure's residue, not an answer about the project.
  it('a failed search never claims the project has remembered nothing', async () => {
    status = 500
    body = 'memory search: database is down'
    renderBrowser()
    expect(await screen.findByText(/database is down/)).toBeInTheDocument()
    expect(screen.queryByText(/Nothing has been remembered in this project yet/)).toBeNull()
    expect(screen.queryByText(/No memory matches/)).toBeNull()
  })

  // B6: the status decides, not the prose. A 500 whose body happens to carry
  // the words the old classifier keyed on is still a failure — otherwise a
  // broken memory store is reported as "this host does not have one", and the
  // operator stops looking.
  it('a 500 saying "not found" is still a failure, not an absent feature', async () => {
    status = 500
    body = 'memory search: relation "memories" not found'
    renderBrowser()
    expect(await screen.findByText(/relation "memories" not found/)).toBeInTheDocument()
    expect(screen.queryByText(/Memory is not available on this host/)).toBeNull()
  })

  it('the empty state invites the first note rather than shrugging', async () => {
    memories = []
    renderBrowser()
    expect(await screen.findByText(/Nothing has been remembered in this project yet/)).toBeInTheDocument()
    expect(screen.getByText(/write the first one yourself with Write a note/)).toBeInTheDocument()
  })
})

describe('writing a note (G8 / work plan C4)', () => {
  it('opens a form, and a malformed label line blocks submit with the reason', async () => {
    renderBrowser()
    await screen.findByText(/Quote the ticket reference/)
    await userEvent.click(screen.getByTestId('write-a-note'))

    await userEvent.type(screen.getByTestId('note-content'), 'A note.')
    await userEvent.type(screen.getByTestId('note-why'), 'testing')
    await userEvent.type(screen.getByTestId('note-labels'), 'not a label line')

    expect(screen.getByText(/expected key=value/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Write it' })).toBeDisabled()
  })

  it('requires content and a reason before Write it is enabled', async () => {
    renderBrowser()
    await screen.findByText(/Quote the ticket reference/)
    await userEvent.click(screen.getByTestId('write-a-note'))
    expect(screen.getByRole('button', { name: 'Write it' })).toBeDisabled()
    await userEvent.type(screen.getByTestId('note-content'), 'A note.')
    expect(screen.getByRole('button', { name: 'Write it' })).toBeDisabled()
    await userEvent.type(screen.getByTestId('note-why'), 'because')
    expect(screen.getByRole('button', { name: 'Write it' })).toBeEnabled()
  })

  it('sends exactly {labels, content} — no provenance field, ever', async () => {
    postResponse = () => ({
      id: 'new-1',
      labels: { kind: 'lesson' },
      content: 'Always say thanks.',
      created_by_worker: '',
      created_by_session: '',
      created_at: MS + 1,
    })
    renderBrowser()
    await screen.findByText(/Quote the ticket reference/)
    await userEvent.click(screen.getByTestId('write-a-note'))
    await userEvent.type(screen.getByTestId('note-content'), 'Always say thanks.')
    await userEvent.type(screen.getByTestId('note-labels'), 'kind=lesson')
    await userEvent.type(screen.getByTestId('note-why'), 'a human noticed a pattern')

    await userEvent.click(screen.getByRole('button', { name: 'Write it' }))

    await waitFor(() => expect(posted).toHaveLength(1))
    expect(posted[0]!.url).toContain('/agent/memories')
    expect(posted[0]!.body).toEqual({ labels: { kind: 'lesson' }, content: 'Always say thanks.' })
    const keys = Object.keys(posted[0]!.body as object)
    expect(keys.sort()).toEqual(['content', 'labels'])
    expect(keys).not.toContain('created_by_worker')
    expect(keys).not.toContain('created_by_session')
    // The "Why?" text is required by the form but has no wire home (no
    // rationale field on this route, no config event for a memory append) —
    // it must never leak into the body either.
    expect(JSON.stringify(posted[0]!.body)).not.toContain('a human noticed a pattern')
  })

  it('after a successful write the list refreshes and the new row is highlighted', async () => {
    postResponse = () => ({
      id: 'new-1',
      labels: { kind: 'note' },
      content: 'Freshly written.',
      created_by_worker: '',
      created_by_session: '',
      created_at: MS + 1,
    })
    renderBrowser()
    await screen.findByText(/Quote the ticket reference/)

    // The reload after the write answers with the new row included — the
    // route really would, since it just appended it.
    await userEvent.click(screen.getByTestId('write-a-note'))
    await userEvent.type(screen.getByTestId('note-content'), 'Freshly written.')
    await userEvent.type(screen.getByTestId('note-labels'), 'kind=note')
    await userEvent.type(screen.getByTestId('note-why'), 'because')
    memories = [
      ...memories,
      memory({ id: 'new-1', labels: { kind: 'note' }, snippet: 'Freshly written.', created_at: MS + 1 }),
    ]
    await userEvent.click(screen.getByRole('button', { name: 'Write it' }))

    expect(await screen.findByText('Freshly written.')).toBeInTheDocument()
    // The dialog closes on success (MUI unmounts it after its exit transition).
    await waitFor(() => expect(screen.queryByTestId('note-content')).not.toBeInTheDocument())
  })

  it('"Publish a new version" pre-fills the form from the current label-registry row', async () => {
    memories = [
      memory({
        id: 'reg-1',
        labels: { name: 'label-registry', kind: 'registry' },
        snippet: 'kind: lesson\nworker: email-answerer',
      }),
    ]
    renderBrowser()
    await screen.findByText('name=label-registry')
    await userEvent.click(screen.getByTestId('publish-registry-version'))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Publish a new version')).toBeInTheDocument()
    expect(screen.getByTestId('note-content')).toHaveValue('kind: lesson\nworker: email-answerer')
    expect(screen.getByTestId('note-labels')).toHaveValue('kind=registry\nname=label-registry')
    // Still gated on a reason, same as a blank note.
    expect(screen.getByRole('button', { name: 'Publish it' })).toBeDisabled()
  })
})
