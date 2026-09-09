// @vitest-environment jsdom
// B3: the project-settings page against a stubbed /agent/project-settings.
// The two behaviours worth pinning: bad JSON is reported inline and blocks the
// PUT, and the "0 means…" sentence tracks the value that is actually typed.

import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import ProjectSettingsPage from './ProjectSettingsPage.js'

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string; body: unknown }[] = []
let stored: Record<string, unknown>

beforeEach(() => {
  requests = []
  stored = {
    project: 'acme',
    base_image: 'core:1',
    system_prompt: 'be helpful',
    mcp_config: { gmail: { url: 'https://mcp.example/gmail' } },
    attention_channel: {},
    max_concurrent_jobs: 4,
    daily_tokens_soft: 0,
    daily_tokens_hard: 0,
    briefing_max_bytes: 2048,
    snapshot_ttl_days: 30,
    updated_at: 1,
  }
  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    requests.push({ url: String(url), method, body })
    if (method === 'PUT') {
      stored = { ...stored, ...(body as object), project: 'acme', updated_at: 2 }
    }
    return new Response(JSON.stringify(stored), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

const puts = () => requests.filter((r) => r.method === 'PUT')

describe('loading', () => {
  it('GETs the settings and fills the fields', async () => {
    render(<ProjectSettingsPage />)
    await waitFor(() => expect(screen.getByLabelText(/base image/i)).toBeInTheDocument())
    expect(requests[0]!.url).toContain('/agent/project-settings')
    expect(screen.getByLabelText(/base image/i)).toHaveValue('core:1')
    expect(screen.getByLabelText(/project system prompt/i)).toHaveValue('be helpful')
    expect(screen.getByLabelText(/MCP servers/i)).toHaveValue(
      JSON.stringify({ gmail: { url: 'https://mcp.example/gmail' } }, null, 2),
    )
  })

  it('surfaces a server error instead of a blank form', async () => {
    globalThis.fetch = vi.fn(
      async () => new Response('project settings not configured', { status: 501 }),
    ) as typeof globalThis.fetch
    render(<ProjectSettingsPage />)
    expect(await screen.findByText(/project settings not configured/i)).toBeInTheDocument()
  })
})

// K2: a settings edit carries a reason, so the save button stays disabled
// until the "Why?" field has one.
const explain = (why = 'the morning queue was backing up') =>
  userEvent.type(screen.getByLabelText('Why?'), why)

describe('the reason (K2)', () => {
  it('refuses an otherwise valid save until a reason is given, and sends it', async () => {
    render(<ProjectSettingsPage />)
    await userEvent.type(await screen.findByLabelText(/base image/i), '2')
    expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled()

    await explain('pinning the newer core image')
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(puts()[0]!.body).toMatchObject({ rationale: 'pinning the newer core image' })
  })

  it('clears the reason after a save, so the next edit writes its own', async () => {
    render(<ProjectSettingsPage />)
    await userEvent.type(await screen.findByLabelText(/base image/i), '2')
    await explain()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    await waitFor(() => expect(screen.getByLabelText('Why?')).toHaveValue(''))
  })
})

describe('MCP JSON validation', () => {
  it('shows the parse error inline and refuses to save', async () => {
    render(<ProjectSettingsPage />)
    const editor = await screen.findByLabelText(/MCP servers/i)

    // fireEvent.change rather than userEvent.type: `{`, `[` and `>` are key
    // descriptors in user-event, and this field is nothing but those characters.
    fireEvent.change(editor, { target: { value: '{"broken"' } })

    expect(await screen.findByText(/invalid json/i)).toBeInTheDocument()
    // Disabled, so the PUT is unreachable — asserting on the click would only
    // prove that user-event refuses to click disabled buttons.
    expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled()
    expect(puts()).toHaveLength(0)
  })

  it('rejects a JSON array — the field is a name-keyed map', async () => {
    render(<ProjectSettingsPage />)
    const editor = await screen.findByLabelText(/MCP servers/i)
    fireEvent.change(editor, { target: { value: '[1]' } })
    expect(await screen.findByText(/must be a JSON object/i)).toBeInTheDocument()
  })

  it('clears the error and saves once the JSON is valid again', async () => {
    render(<ProjectSettingsPage />)
    const editor = await screen.findByLabelText(/MCP servers/i)

    fireEvent.change(editor, { target: { value: '{"broken"' } })
    expect(await screen.findByText(/invalid json/i)).toBeInTheDocument()

    fireEvent.change(editor, { target: { value: '{}' } })
    await waitFor(() => expect(screen.queryByText(/invalid json/i)).not.toBeInTheDocument())

    await explain()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))
    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(puts()[0]!.body).toMatchObject({ mcp_config: {} })
  })
})

describe('budget/cap fields', () => {
  it('explains what 0 means, per field, as soon as it is typed', async () => {
    render(<ProjectSettingsPage />)
    await screen.findByLabelText(/base image/i)

    // Loaded state: the two token budgets are already 0 = off; TTL is 30 = on.
    expect(screen.getByText(/0 means off — no soft-budget notification/i)).toBeInTheDocument()
    expect(screen.getByText(/0 means off — jobs are never stopped/i)).toBeInTheDocument()
    expect(screen.queryByText(/0 means never/i)).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText(/snapshot ttl/i), { target: { value: '0' } })
    expect(await screen.findByText(/0 means never — snapshots are kept forever/i)).toBeInTheDocument()
  })

  it('distinguishes "0 = off" from "0 = the server default"', async () => {
    render(<ProjectSettingsPage />)
    const cap = await screen.findByLabelText(/briefing section cap/i)
    fireEvent.change(cap, { target: { value: '0' } })
    // briefing_max_bytes reads 0 as unset, so the copy must say so — not "off".
    expect(await screen.findByText(/not set.*default of 2048/i)).toBeInTheDocument()
  })

  it('sends the whole object on save, including a zero the human chose', async () => {
    render(<ProjectSettingsPage />)
    fireEvent.change(await screen.findByLabelText(/snapshot ttl/i), { target: { value: '0' } })
    await explain()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    const body = puts()[0]!.body as Record<string, unknown>
    expect(body.snapshot_ttl_days).toBe(0)
    // Whole-object PUT: every field travels, not just the edited one.
    expect(body.base_image).toBe('core:1')
    expect(body.system_prompt).toBe('be helpful')
    // …but never the server-owned ones.
    expect('project' in body).toBe(false)
    expect('updated_at' in body).toBe(false)
  })

  it('blocks the save on a negative budget', async () => {
    render(<ProjectSettingsPage />)
    const soft = await screen.findByLabelText(/daily token budget — soft/i)
    fireEvent.change(soft, { target: { value: '-5' } })
    await explain()
    expect(await screen.findByText(/must not be negative/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled()
  })
})

describe('git projection fields (G24)', () => {
  it('presents an empty repository as projection being off', async () => {
    render(<ProjectSettingsPage />)
    const remote = await screen.findByLabelText(/repository/i)
    expect(remote).toHaveValue('')
    expect(screen.getByText(/git projection is off/i)).toBeInTheDocument()
  })

  it('says projection is on once a repository is typed', async () => {
    render(<ProjectSettingsPage />)
    const remote = await screen.findByLabelText(/repository/i)
    await userEvent.type(remote, 'https://github.com/acme/orange')
    expect(await screen.findByText(/renders to this repository/i)).toBeInTheDocument()
  })

  it('flags an invalid git_token_env inline, without needing a save attempt', async () => {
    render(<ProjectSettingsPage />)
    const tokenEnv = await screen.findByLabelText(/push token.*environment variable name/i)
    fireEvent.change(tokenEnv, { target: { value: '1-not-valid' } })
    expect(await screen.findByText(/not a valid environment variable name/i)).toBeInTheDocument()
  })

  it('accepts a valid git_token_env with no error', async () => {
    render(<ProjectSettingsPage />)
    const tokenEnv = await screen.findByLabelText(/push token.*environment variable name/i)
    fireEvent.change(tokenEnv, { target: { value: 'GIT_PUSH_TOKEN' } })
    expect(screen.queryByText(/not a valid environment variable name/i)).not.toBeInTheDocument()
  })

  it('flags a git_subfolder that is not a single path segment', async () => {
    render(<ProjectSettingsPage />)
    const subfolder = await screen.findByLabelText(/subfolder/i)
    fireEvent.change(subfolder, { target: { value: '../escape' } })
    expect(await screen.findByText(/single path segment/i)).toBeInTheDocument()
  })

  it('flags an invalid git_webhook_secret_env inline, without needing a save attempt', async () => {
    render(<ProjectSettingsPage />)
    const webhookSecretEnv = await screen.findByLabelText(/webhook secret.*environment variable name/i)
    fireEvent.change(webhookSecretEnv, { target: { value: '1-not-valid' } })
    expect(await screen.findByText(/not a valid environment variable name/i)).toBeInTheDocument()
  })

  it('accepts a valid git_webhook_secret_env with no error', async () => {
    render(<ProjectSettingsPage />)
    const webhookSecretEnv = await screen.findByLabelText(/webhook secret.*environment variable name/i)
    fireEvent.change(webhookSecretEnv, { target: { value: 'GIT_WEBHOOK_SECRET' } })
    expect(screen.queryByText(/not a valid environment variable name/i)).not.toBeInTheDocument()
  })

  it('presents an empty git_webhook_secret_env as inbound webhooks not being configured', async () => {
    render(<ProjectSettingsPage />)
    expect(await screen.findByText(/inbound webhooks are not configured/i)).toBeInTheDocument()
  })

  it('sends the git fields on save, whole-object', async () => {
    render(<ProjectSettingsPage />)
    const remote = await screen.findByLabelText(/repository/i)
    await userEvent.type(remote, 'https://github.com/acme/orange')
    const tokenEnv = await screen.findByLabelText(/push token.*environment variable name/i)
    await userEvent.type(tokenEnv, 'GIT_PUSH_TOKEN')
    const webhookSecretEnv = await screen.findByLabelText(/webhook secret.*environment variable name/i)
    await userEvent.type(webhookSecretEnv, 'GIT_WEBHOOK_SECRET')
    await explain()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(puts()[0]!.body).toMatchObject({
      git_remote: 'https://github.com/acme/orange',
      git_token_env: 'GIT_PUSH_TOKEN',
      git_webhook_secret_env: 'GIT_WEBHOOK_SECRET',
    })
  })
})

describe('dirty tracking', () => {
  it('disables save until something changes, and again after saving', async () => {
    render(<ProjectSettingsPage />)
    await screen.findByLabelText(/base image/i)
    expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled()

    await userEvent.type(screen.getByLabelText(/base image/i), '2')
    await explain()
    expect(screen.getByRole('button', { name: /save settings/i })).toBeEnabled()

    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))
    await waitFor(() => expect(puts()).toHaveLength(1))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled(),
    )
  })
})

describe('the project-wide briefing (B1)', () => {
  const explainIt = async () => {
    await userEvent.type(screen.getByLabelText(/why\?/i), 'adding the rulebook')
  }

  it('shows what the project already has', async () => {
    stored = { ...stored, briefing: ['name=label-registry'] }
    render(<ProjectSettingsPage />)
    expect(await screen.findByDisplayValue('name=label-registry')).toBeInTheDocument()
  })

  // Two blast-radius sentences, and both matter: one field here edits every
  // worker's prompt at once, and a chat receives none of it — which nothing
  // else in the console says, and which is invisible when it bites.
  it('says who gets these, and who does not', async () => {
    render(<ProjectSettingsPage />)
    const panel = await screen.findByTestId('project-briefing')
    expect(panel.textContent).toMatch(/every job in this project/i)
    expect(panel.textContent).toMatch(/chat sessions do not receive these/i)
  })

  it('adds and removes entries, and sends them', async () => {
    render(<ProjectSettingsPage />)
    await screen.findByTestId('project-briefing')

    await userEvent.click(screen.getByRole('button', { name: /add a selector/i }))
    await userEvent.type(screen.getByLabelText(/^briefing selector 1$/i), 'name=label-registry')
    await explainIt()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0].body as { briefing: string[] }).briefing).toEqual(['name=label-registry'])
  })

  it('removes a row', async () => {
    stored = { ...stored, briefing: ['name=label-registry', 'kind=lesson'] }
    render(<ProjectSettingsPage />)
    await screen.findByDisplayValue('kind=lesson')

    await userEvent.click(screen.getByRole('button', { name: /^remove briefing selector 2$/i }))
    await explainIt()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0].body as { briefing: string[] }).briefing).toEqual(['name=label-registry'])
  })

  it('refuses an unparseable selector with the engine own words, and blocks the save', async () => {
    render(<ProjectSettingsPage />)
    await screen.findByTestId('project-briefing')

    await userEvent.click(screen.getByRole('button', { name: /add a selector/i }))
    fireEvent.change(screen.getByLabelText(/^briefing selector 1$/i), {
      target: { value: 'kind in (a' },
    })
    await explainIt()

    expect(await screen.findByText(/unbalanced/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled()
    expect(puts()).toHaveLength(0)
  })

  // PUT is whole-object: before this field existed, every unrelated settings
  // save wrote briefing: null and wiped it.
  it('sends the briefing on a save that had nothing to do with it', async () => {
    stored = { ...stored, briefing: ['name=label-registry'] }
    render(<ProjectSettingsPage />)
    await screen.findByLabelText(/base image/i)

    await userEvent.type(screen.getByLabelText(/base image/i), '2')
    await explainIt()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0].body as { briefing: string[] }).briefing).toEqual(['name=label-registry'])
  })
})
