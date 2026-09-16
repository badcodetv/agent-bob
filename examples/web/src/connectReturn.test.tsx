// @vitest-environment jsdom
// T26 (design/2026-09-11-project-connections.md): coming back from Google.
//
// agentd's callback ends with a 303 to `/p/<project>/settings?connect=<account>
// &result=…`. The shell has no router, so nothing opened Settings for that URL:
// Richard pressed Connect Google on Settings, approved on Google, and landed on
// the Desk with no word about whether it had worked. These tests pin the four
// things the shell now owes that URL: the project it names is selected, the
// Settings view opens, the parsed result reaches the page (which shows the
// banner), and the query is removed so a reload does not show the banner again.

import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'

// The pages themselves are T25's and are tested in web/. Here they are stand-ins
// that report what the shell handed them, so the test sees the shell's decision
// and nothing else.
vi.mock('@agentkit/chat-ui', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@agentkit/chat-ui')>()
  return {
    ...actual,
    ProjectSettingsPage: (props: { projectId: string; connectResult?: unknown }) => (
      <div data-testid="settings-page" data-project={props.projectId}>
        {JSON.stringify(props.connectResult ?? null)}
      </div>
    ),
    DeskPage: (props: { projectId: string }) => <div data-testid="desk-page" data-project={props.projectId} />,
  }
})

import App, { connectReturnFromLocation } from './App.js'

const AUTH_KEY = 'agent-bob-auth'
const originalFetch = globalThis.fetch

// loadAuthState throws away a token it cannot read an unexpired `exp` from, so
// the fixture token is a JWT-shaped string with one an hour out.
function fakeToken(project: string): string {
  const payload = btoa(JSON.stringify({ customer: project, exp: Math.floor(Date.now() / 1000) + 3600 }))
  return `header.${payload}.signature`
}

function signedIn(projects: string[], selectedProject: string | null) {
  window.localStorage.setItem(
    AUTH_KEY,
    JSON.stringify({
      email: 'richard@example.com',
      projects: projects.map((id) => ({ id, token: fakeToken(id) })),
      selectedProject,
    }),
  )
}

function goTo(url: string) {
  window.history.replaceState(null, '', url)
}

beforeEach(() => {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/auth/config')) {
      return new Response(JSON.stringify({ modes: ['password'], google_client_id: '' }), { status: 200 })
    }
    return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  window.localStorage.clear()
  goTo('/')
  vi.restoreAllMocks()
})

describe('connectReturnFromLocation', () => {
  it('reads a successful return', () => {
    expect(connectReturnFromLocation('/p/enc/settings', '?connect=google&result=connected')).toEqual({
      project: 'enc',
      result: { account: 'google', ok: true },
    })
  })

  it('reads an error return, including the missing permissions', () => {
    expect(
      connectReturnFromLocation(
        '/p/enc/settings',
        '?connect=google&result=error&reason=missing_scopes&missing=gmail.compose',
      ),
    ).toEqual({
      project: 'enc',
      result: { account: 'google', ok: false, reason: 'missing_scopes', missing: ['gmail.compose'] },
    })
  })

  it('decodes the project segment', () => {
    expect(connectReturnFromLocation('/p/my%20project/settings', '?connect=google&result=connected')?.project).toBe(
      'my project',
    )
  })

  it('still opens Settings for a connect= whose result it cannot read, with no banner', () => {
    expect(connectReturnFromLocation('/p/enc/settings', '?connect=google&result=bogus')).toEqual({
      project: 'enc',
      result: null,
    })
  })

  it('ignores anything that is not a connect return to Settings', () => {
    expect(connectReturnFromLocation('/p/enc/settings', '')).toBeNull()
    expect(connectReturnFromLocation('/p/enc/settings', '?connect=')).toBeNull()
    expect(connectReturnFromLocation('/p/enc', '?connect=google&result=connected')).toBeNull()
    expect(connectReturnFromLocation('/p/enc/s/sess-1', '?connect=google&result=connected')).toBeNull()
    expect(connectReturnFromLocation('/p/enc/settings/extra', '?connect=google&result=connected')).toBeNull()
    expect(connectReturnFromLocation('/settings', '?connect=google&result=connected')).toBeNull()
  })
})

describe('App: returning from Google', () => {
  it('selects the named project, opens Settings, passes the result and strips the query', async () => {
    // The last project used was a different one: the URL must win.
    signedIn(['other', 'enc'], 'other')
    goTo('/p/enc/settings?connect=google&result=error&reason=missing_scopes&missing=gmail.compose#top')

    render(<App />)

    const page = await screen.findByTestId('settings-page')
    expect(page.getAttribute('data-project')).toBe('enc')
    expect(JSON.parse(page.textContent ?? 'null')).toEqual({
      account: 'google',
      ok: false,
      reason: 'missing_scopes',
      missing: ['gmail.compose'],
    })
    await waitFor(() => expect(window.location.search).toBe(''))
    expect(window.location.pathname).toBe('/p/enc/settings')
    expect(window.location.hash).toBe('#top')
    expect(JSON.parse(window.localStorage.getItem(AUTH_KEY) ?? '{}').selectedProject).toBe('enc')
  })

  it('keeps unrelated query parameters when stripping', async () => {
    signedIn(['enc'], 'enc')
    goTo('/p/enc/settings?keep=1&connect=google&result=connected')

    render(<App />)

    const page = await screen.findByTestId('settings-page')
    expect(JSON.parse(page.textContent ?? 'null')).toEqual({ account: 'google', ok: true })
    await waitFor(() => expect(window.location.search).toBe('?keep=1'))
  })

  it('does not show the banner again after the query is gone (a reload)', async () => {
    signedIn(['enc'], 'enc')
    goTo('/p/enc/settings')

    render(<App />)

    expect(await screen.findByTestId('desk-page')).toBeTruthy()
    expect(screen.queryByTestId('settings-page')).toBeNull()
  })

  it('drops the banner once the human walks away from Settings', async () => {
    signedIn(['enc'], 'enc')
    goTo('/p/enc/settings?connect=google&result=connected')

    render(<App />)

    await screen.findByTestId('settings-page')
    fireEvent.click(screen.getByTestId('nav-desk'))
    await screen.findByTestId('desk-page')
    fireEvent.click(screen.getByTestId('nav-settings'))
    expect(JSON.parse((await screen.findByTestId('settings-page')).textContent ?? '')).toBeNull()
  })

  it('leaves a user with no token for that project where a foreign permalink leaves them', async () => {
    signedIn(['other'], 'other')
    goTo('/p/enc/settings?connect=google&result=connected')

    render(<App />)

    const desk = await screen.findByTestId('desk-page')
    expect(desk.getAttribute('data-project')).toBe('other')
    expect(screen.queryByTestId('settings-page')).toBeNull()
    // And the result does not leak into another project's Settings later.
    fireEvent.click(screen.getByTestId('nav-settings'))
    const page = await screen.findByTestId('settings-page')
    expect(page.getAttribute('data-project')).toBe('other')
    expect(JSON.parse(page.textContent ?? '')).toBeNull()
  })

  it('opens Settings once the human has picked the project from the picker', async () => {
    // Signed in, nothing selected yet: the permalink path selects it.
    signedIn(['enc', 'other'], null)
    goTo('/p/enc/settings?connect=google&result=connected')

    render(<App />)

    const page = await screen.findByTestId('settings-page')
    expect(page.getAttribute('data-project')).toBe('enc')
    expect(JSON.parse(page.textContent ?? 'null')).toEqual({ account: 'google', ok: true })
  })
})
