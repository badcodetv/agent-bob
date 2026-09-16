// @vitest-environment jsdom
// T25: the Connections panel against a stubbed /agent/connections.

import React from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ConnectionsPanel from './ConnectionsPanel.js'
import ProjectSettingsPage from './ProjectSettingsPage.js'
import { CONNECT_ERROR_REASONS, describeConnectError } from '../connections.js'

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string }[] = []
let list: unknown
let postHandler: Handler
let deleteHandler: Handler

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function notConnected(canConnect = true, reason?: string) {
  return {
    connections: [
      { name: 'gmail', description: 'ENC Gmail', account: 'google', available: false, unavailable: 'gmail uses the Google account google, which is not connected' },
      { name: 'drive', description: 'ENC Drive', account: 'google', available: false, unavailable: 'drive uses the Google account google, which is not connected' },
      { name: 'linear', description: 'Linear issues', available: true },
    ],
    accounts: [{ account: 'google', provider: 'google', connected: false, connections: ['drive', 'gmail'] }],
    can_connect: canConnect,
    ...(reason ? { connect_disabled_reason: reason } : {}),
  }
}

function connected() {
  return {
    connections: [{ name: 'gmail', description: 'ENC Gmail', account: 'google', available: true }],
    accounts: [
      {
        account: 'google',
        provider: 'google',
        connected: true,
        account_email: 'x@y.example',
        connected_by: 'r@y.example',
        connected_at: 1700000000000,
        connections: ['gmail'],
      },
    ],
    can_connect: true,
  }
}

beforeEach(() => {
  requests = []
  list = notConnected()
  postHandler = () => json({ authorize_url: 'https://accounts.google.com/o/oauth2/v2/auth?state=s' })
  deleteHandler = () => json({ revoked: true })
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init: RequestInit = {}) => {
    const method = init.method ?? 'GET'
    requests.push({ url: String(url), method })
    if (method === 'POST') return postHandler(String(url), init)
    if (method === 'DELETE') return deleteHandler(String(url), init)
    return json(list)
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('not connected', () => {
  it('shows an enabled Connect Google when can_connect', async () => {
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByText('Google — Not connected')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Connections' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Connect Google' })).toBeEnabled()
    expect(requests[0]!.url).toBe('/agent/connections')
  })

  it('shows a disabled Connect Google with the reason otherwise', async () => {
    list = notConnected(false, 'Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set')
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByRole('button', { name: 'Connect Google' })).toBeDisabled()
    expect(screen.getByText('Connect Google is off: AGENTKIT_CONNECTIONS_KEY is not set')).toBeInTheDocument()
  })

  it('fails closed when can_connect is not literally true', async () => {
    list = { ...notConnected(), can_connect: 'true' }
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByRole('button', { name: 'Connect Google' })).toBeDisabled()
  })

  it('lists the connections under the account and env-var connections read-only', async () => {
    render(<ConnectionsPanel projectId="enc" />)
    const account = await screen.findByTestId('connection-account-google')
    expect(within(account).getByText('gmail')).toBeInTheDocument()
    expect(within(account).getByText('drive')).toBeInTheDocument()
    expect(within(account).getByText(/gmail uses the Google account google/)).toBeInTheDocument()
    const other = screen.getByTestId('connection-other')
    expect(within(other).getByText('linear')).toBeInTheDocument()
    expect(within(other).queryByRole('button')).toBeNull()
  })
})

describe('connect', () => {
  it('POSTs, then sends the browser to the authorize URL', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Connect Google' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/o/oauth2/v2/auth?state=s'))
    const post = requests.find((r) => r.method === 'POST')!
    expect(post.url).toBe('/agent/connections/google/connect')
  })

  it('re-enables the buttons when Back restores the page from the back/forward cache', async () => {
    vi.stubGlobal('location', { ...window.location, assign: vi.fn() })
    render(<ConnectionsPanel projectId="enc" />)
    const button = await screen.findByRole('button', { name: 'Connect Google' })
    await userEvent.click(button)
    await waitFor(() => expect(button).toBeDisabled())
    const restored = new Event('pageshow') as PageTransitionEvent
    Object.defineProperty(restored, 'persisted', { value: true })
    window.dispatchEvent(restored)
    await waitFor(() => expect(button).toBeEnabled())
  })

  it('shows a 403 message from the POST and does not navigate', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    postHandler = () => json({ error: 'only a project operator may connect Google' }, 403)
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Connect Google' }))
    expect(await screen.findByText('only a project operator may connect Google')).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })

  it('refuses a non-http authorize URL', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    postHandler = () => json({ authorize_url: 'javascript:alert(1)' })
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Connect Google' }))
    expect(await screen.findByText(/did not return a Google sign-in link/)).toBeInTheDocument()
    expect(assign).not.toHaveBeenCalled()
  })
})

describe('connected', () => {
  it('shows Connected as and Disconnect', async () => {
    list = connected()
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByText('Google — Connected as x@y.example')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Disconnect' })).toBeEnabled()
    expect(screen.queryByRole('button', { name: 'Connect Google' })).toBeNull()
  })

  it('shows why a connected account cannot be used', async () => {
    list = connected()
    ;(list as { accounts: { unavailable?: string }[] }).accounts[0]!.unavailable =
      'the stored Google connection for google can no longer be read (the encryption key changed) — connect Google again'
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByText(/encryption key changed/)).toBeInTheDocument()
  })

  it('asks once, then DELETEs and reloads', async () => {
    list = connected()
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Disconnect' }))
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(
        'Disconnect Google? Workers lose Gmail, Drive, Docs and Sheets until someone connects again.',
      ),
    ).toBeInTheDocument()
    expect(requests.some((r) => r.method === 'DELETE')).toBe(false)

    list = notConnected()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Disconnect' }))
    await waitFor(() => expect(requests.filter((r) => r.method === 'DELETE').map((r) => r.url)).toEqual(['/agent/connections/google']))
    expect(await screen.findByText('Google — Not connected')).toBeInTheDocument()
    expect(screen.getByText('Google disconnected.')).toBeInTheDocument()
  })

  it('cancel in the dialog sends nothing', async () => {
    list = connected()
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Disconnect' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(requests.some((r) => r.method === 'DELETE')).toBe(false)
  })

  it('says when Google did not confirm the revoke', async () => {
    list = connected()
    deleteHandler = () => json({ revoked: false })
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Disconnect' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Disconnect' }))
    expect(await screen.findByText(/Google did not confirm/)).toBeInTheDocument()
  })

  it('shows a disconnect failure in the server’s words', async () => {
    list = connected()
    deleteHandler = () => json({ error: 'only a project operator may disconnect Google' }, 403)
    render(<ConnectionsPanel projectId="enc" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Disconnect' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Disconnect' }))
    expect(await screen.findByText('only a project operator may disconnect Google')).toBeInTheDocument()
  })
})

describe('result banner', () => {
  it('renders for connected', async () => {
    list = connected()
    render(<ConnectionsPanel projectId="enc" connectResult={{ account: 'google', ok: true }} />)
    expect(await screen.findByText('Google is connected. Workers can use it from their next run.')).toBeInTheDocument()
  })

  it.each([...CONNECT_ERROR_REASONS])('renders for %s', async (reason) => {
    render(<ConnectionsPanel projectId="enc" connectResult={{ account: 'google', ok: false, reason }} />)
    expect(await screen.findByText(describeConnectError(reason))).toBeInTheDocument()
  })

  it('names missing permissions', async () => {
    render(
      <ConnectionsPanel
        projectId="enc"
        connectResult={{ account: 'google', ok: false, reason: 'missing_scopes', missing: ['gmail.compose'] }}
      />,
    )
    expect(await screen.findByText(/Missing: gmail\.compose\./)).toBeInTheDocument()
  })
})

describe('load failures', () => {
  it('renders nothing on a deployment without the route', async () => {
    globalThis.fetch = vi.fn(async () => json({ error: 'not found' }, 404)) as typeof globalThis.fetch
    const { container } = render(<ConnectionsPanel projectId="enc" />)
    await waitFor(() => expect(container).toBeEmptyDOMElement())
  })

  it('shows a real failure in the server’s words', async () => {
    globalThis.fetch = vi.fn(async () => json({ error: 'could not read the stored connections' }, 500)) as typeof globalThis.fetch
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByText('could not read the stored connections')).toBeInTheDocument()
  })

  it('says so when the project declares no connections', async () => {
    list = { connections: [], accounts: [], can_connect: true }
    render(<ConnectionsPanel projectId="enc" />)
    expect(await screen.findByText('This project has no connections.')).toBeInTheDocument()
  })
})

describe('mounted in ProjectSettingsPage', () => {
  it('sits in the top tier above the budget panel and receives connectResult', async () => {
    const settings = {
      project: 'enc', base_image: '', system_prompt: '', mcp_config: {}, attention_channel: {},
      max_concurrent_jobs: 1, daily_tokens_soft: 0, daily_tokens_hard: 0, briefing_max_bytes: 0,
      snapshot_ttl_days: 0, briefing: [], updated_at: 1,
    }
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init: RequestInit = {}) => {
      requests.push({ url: String(url), method: init.method ?? 'GET' })
      if (String(url).startsWith('/agent/connections')) return json(connected())
      if (String(url).startsWith('/agent/project-settings')) return json(settings)
      return json({})
    }) as typeof globalThis.fetch
    render(<ProjectSettingsPage projectId="enc" connectResult={{ account: 'google', ok: false, reason: 'expired' }} />)
    const panel = await screen.findByTestId('connections-panel')
    expect(await within(panel).findByText(describeConnectError('expired'))).toBeInTheDocument()
    expect(await within(panel).findByText('Google — Connected as x@y.example')).toBeInTheDocument()

    const tierHeading = screen.getByText('You may want to change these')
    const briefing = screen.getByTestId('project-briefing')
    const budget = screen.getAllByText(/budget/i)[0]!
    const advanced = screen.getByRole('button', { name: /^advanced/i })
    const follows = (a: Node, b: Node) => (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0
    expect(follows(tierHeading, panel)).toBe(true)
    expect(follows(briefing, panel)).toBe(true)
    expect(follows(panel, budget)).toBe(true)
    expect(follows(panel, advanced)).toBe(true)
  })
})
