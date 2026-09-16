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

/** G5: base image, the JSON editors and the git fields moved behind the
 *  collapsed "Advanced" tier — open it before a test reaches into any of
 *  them. */
const openAdvanced = async () => {
  await userEvent.click(await screen.findByRole('button', { name: /^advanced/i }))
  await screen.findByTestId('advanced-settings')
}

beforeEach(() => {
  requests = []
  // The Advanced tier's open/closed state is sticky in localStorage (G5);
  // clear it so one test's toggle cannot leak into the next.
  window.localStorage.clear()
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
    await waitFor(() => expect(screen.getByLabelText(/project system prompt/i)).toHaveValue('be helpful'))
    expect(requests[0]!.url).toContain('/agent/project-settings')

    await openAdvanced()
    expect(screen.getByLabelText(/base image/i)).toHaveValue('core:1')
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
    await openAdvanced()
    await userEvent.type(screen.getByLabelText(/base image/i), '2')
    expect(screen.getByRole('button', { name: /save settings/i })).toBeDisabled()

    await explain('pinning the newer core image')
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(puts()[0]!.body).toMatchObject({ rationale: 'pinning the newer core image' })
  })

  it('clears the reason after a save, so the next edit writes its own', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    await userEvent.type(screen.getByLabelText(/base image/i), '2')
    await explain()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    await waitFor(() => expect(screen.getByLabelText('Why?')).toHaveValue(''))
  })
})

describe('MCP JSON validation', () => {
  it('shows the parse error inline and refuses to save', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const editor = screen.getByLabelText(/MCP servers/i)

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
    await openAdvanced()
    const editor = screen.getByLabelText(/MCP servers/i)
    fireEvent.change(editor, { target: { value: '[1]' } })
    expect(await screen.findByText(/must be a JSON object/i)).toBeInTheDocument()
  })

  it('clears the error and saves once the JSON is valid again', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const editor = screen.getByLabelText(/MCP servers/i)

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

// G5 moved the two daily-token-budget fields into BudgetPanel (a separate
// component, its own hook instance, its own Save button — see
// BudgetPanel.test.tsx for their "0 means off" copy and negative-budget
// guard). What is left on this page's "Advanced" tier is the three
// remaining numeric caps.
describe('cap fields (Advanced)', () => {
  it('explains "0 means never" for the snapshot TTL, live', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()

    expect(screen.queryByText(/0 means off — no soft-budget notification/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/0 means never/i)).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText(/snapshot ttl/i), { target: { value: '0' } })
    expect(await screen.findByText(/0 means never — snapshots are kept forever/i)).toBeInTheDocument()
  })

  it('distinguishes "0 = off" from "0 = the server default"', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const cap = screen.getByLabelText(/briefing section cap/i)
    fireEvent.change(cap, { target: { value: '0' } })
    // briefing_max_bytes reads 0 as unset, so the copy must say so — not "off".
    expect(await screen.findByText(/not set.*default of 2048/i)).toBeInTheDocument()
  })

  it('sends the whole object on save, including a zero the human chose', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    fireEvent.change(screen.getByLabelText(/snapshot ttl/i), { target: { value: '0' } })
    await explain()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    const body = puts()[0]!.body as Record<string, unknown>
    expect(body.snapshot_ttl_days).toBe(0)
    // Whole-object PUT: every field travels, not just the edited one —
    // including the two budget fields this page no longer shows an editor
    // for, echoed back from the load exactly as BudgetPanel's own row is.
    expect(body.base_image).toBe('core:1')
    expect(body.system_prompt).toBe('be helpful')
    expect(body.daily_tokens_soft).toBe(0)
    expect(body.daily_tokens_hard).toBe(0)
    // …but never the server-owned ones.
    expect('project' in body).toBe(false)
    expect('updated_at' in body).toBe(false)
  })
})

describe('git projection fields (G24)', () => {
  it('presents an empty repository as projection being off', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const remote = screen.getByLabelText(/repository/i)
    expect(remote).toHaveValue('')
    expect(screen.getByText(/git projection is off/i)).toBeInTheDocument()
  })

  it('says projection is on once a repository is typed', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const remote = screen.getByLabelText(/repository/i)
    await userEvent.type(remote, 'https://github.com/acme/orange')
    expect(await screen.findByText(/renders to this repository/i)).toBeInTheDocument()
  })

  it('flags an invalid git_token_env inline, without needing a save attempt', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const tokenEnv = screen.getByLabelText(/push token.*environment variable name/i)
    fireEvent.change(tokenEnv, { target: { value: '1-not-valid' } })
    expect(await screen.findByText(/not a valid environment variable name/i)).toBeInTheDocument()
  })

  it('accepts a valid git_token_env with no error', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const tokenEnv = screen.getByLabelText(/push token.*environment variable name/i)
    fireEvent.change(tokenEnv, { target: { value: 'GIT_PUSH_TOKEN' } })
    expect(screen.queryByText(/not a valid environment variable name/i)).not.toBeInTheDocument()
  })

  it('flags a git_subfolder that is not a single path segment', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const subfolder = screen.getByLabelText(/subfolder/i)
    fireEvent.change(subfolder, { target: { value: '../escape' } })
    expect(await screen.findByText(/single path segment/i)).toBeInTheDocument()
  })

  it('flags an invalid git_webhook_secret_env inline, without needing a save attempt', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const webhookSecretEnv = screen.getByLabelText(/webhook secret.*environment variable name/i)
    fireEvent.change(webhookSecretEnv, { target: { value: '1-not-valid' } })
    expect(await screen.findByText(/not a valid environment variable name/i)).toBeInTheDocument()
  })

  it('accepts a valid git_webhook_secret_env with no error', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    const webhookSecretEnv = screen.getByLabelText(/webhook secret.*environment variable name/i)
    fireEvent.change(webhookSecretEnv, { target: { value: 'GIT_WEBHOOK_SECRET' } })
    expect(screen.queryByText(/not a valid environment variable name/i)).not.toBeInTheDocument()
  })

  it('presents an empty git_webhook_secret_env as inbound webhooks not being configured', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    expect(await screen.findByText(/inbound webhooks are not configured/i)).toBeInTheDocument()
  })

  it('sends the git fields on save, whole-object', async () => {
    render(<ProjectSettingsPage />)
    await openAdvanced()
    // fireEvent.change rather than userEvent.type, as in the tests above: this is
    // about what the save SENDS, not about typing. Typed a keystroke at a time,
    // these three values were 62 re-renders of an eleven-field form that
    // validates live; it took ~2s here and timed out at 5s on CI's slower
    // runners. The typing behaviour itself is covered by "says projection is on
    // once a repository is typed", above.
    fireEvent.change(screen.getByLabelText(/repository/i), {
      target: { value: 'https://github.com/acme/orange' },
    })
    fireEvent.change(screen.getByLabelText(/push token.*environment variable name/i), {
      target: { value: 'GIT_PUSH_TOKEN' },
    })
    fireEvent.change(screen.getByLabelText(/webhook secret.*environment variable name/i), {
      target: { value: 'GIT_WEBHOOK_SECRET' },
    })
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
    await openAdvanced()
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
    await openAdvanced()

    await userEvent.type(screen.getByLabelText(/base image/i), '2')
    await explainIt()
    await userEvent.click(screen.getByRole('button', { name: /save settings/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0].body as { briefing: string[] }).briefing).toEqual(['name=label-registry'])
  })
})

// G5 (A5): Advanced starts collapsed and its open/closed state survives a
// remount ("reload"), keyed per project.
describe('the Advanced tier (G5)', () => {
  it('starts collapsed', async () => {
    render(<ProjectSettingsPage />)
    await screen.findByLabelText(/project system prompt/i)
    expect(screen.queryByTestId('advanced-settings')).not.toBeInTheDocument()
  })

  it('survives a remount once opened, keyed by projectId', async () => {
    const { unmount } = render(<ProjectSettingsPage projectId="acme" />)
    await openAdvanced()
    unmount()

    render(<ProjectSettingsPage projectId="acme" />)
    await screen.findByLabelText(/project system prompt/i)
    expect(screen.getByTestId('advanced-settings')).toBeInTheDocument()
  })

  it('does not leak one project’s open Advanced into another’s', async () => {
    const { unmount } = render(<ProjectSettingsPage projectId="acme" />)
    await openAdvanced()
    unmount()

    render(<ProjectSettingsPage projectId="other-project" />)
    await screen.findByLabelText(/project system prompt/i)
    expect(screen.queryByTestId('advanced-settings')).not.toBeInTheDocument()
  })
})

// A defect the team lead caught in review: before this fix, BudgetPanel held
// its OWN `useProjectSettings` instance even when mounted inside this page.
// The route is whole-object (`useProjectSettings.ts`'s own doc comment), so
// the main form's "Save settings" button always PUTs its own full draft —
// including whatever `daily_tokens_hard` it read when IT mounted. An operator
// who raised the hard limit in the panel, then saved anything at all from the
// main form, would silently revert the limit: a success message, a changelog
// entry, and the WRONG number on the server. The fix is `BudgetPanel`'s
// `settings` prop (see its doc comment) — this page now hands it the same
// `useProjectSettings` instance the main form uses, so there is exactly one
// draft on this screen.
describe('the budget panel shares this page’s settings instance (regression)', () => {
  it('a hard-limit edit in the panel survives a save from the main form', async () => {
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      const href = String(url)
      const path = href.split('?')[0]!
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      requests.push({ url: href, method, body })

      if (path === '/agent/whoami') {
        return new Response(JSON.stringify({ email: 'kai@example.com', project: 'acme', operator: true }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (path === '/agent/usage') {
        return new Response(
          JSON.stringify({
            project: 'acme',
            day_starts_at: 0,
            today: { input_tokens: 0, output_tokens: 0, cost_usd: 0, queries: 0 },
            last_7d: { input_tokens: 0, output_tokens: 0, cost_usd: 0, queries: 0 },
            last_30d: { input_tokens: 0, output_tokens: 0, cost_usd: 0, queries: 0 },
            budget: { daily_tokens_soft: stored.daily_tokens_soft, daily_tokens_hard: stored.daily_tokens_hard },
            credential_mode: 'api-key',
            cost_known: true,
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      // /agent/project-settings, both GET and PUT — same merge-and-echo
      // behaviour as the file's default mock.
      if (method === 'PUT') {
        stored = { ...stored, ...(body as object), project: 'acme', updated_at: 2 }
      }
      return new Response(JSON.stringify(stored), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as typeof globalThis.fetch

    render(<ProjectSettingsPage />)

    // Raise the hard limit through the panel — no save yet.
    const hardField = await screen.findByLabelText('Hard limit')
    fireEvent.change(hardField, { target: { value: '9000' } })

    // Now touch something unrelated in Advanced and save from the MAIN form.
    // Before the fix, this PUT carried the panel's OWN stale draft's
    // daily_tokens_hard (0, read at the panel's own mount) — not the 9000
    // just typed above.
    await openAdvanced()
    await userEvent.type(screen.getByLabelText(/base image/i), '2')
    // Exactly ONE "Why?" on this screen. There were two until D2: the panel
    // rendered its own alongside the page's, and because the fix above made
    // them share one instance they were two visible inputs bound to the SAME
    // `rationale` — mirroring each other's keystrokes, and both answering to
    // the accessible name "Why?", which is ambiguous to a screen reader and
    // to Playwright alike (`getByLabel('Why?')` was a strict-mode violation).
    // Inside this page the panel now delegates the reason and the save; on the
    // Desk, where it is the only settings consumer, it still owns both.
    const whyFields = screen.getAllByLabelText('Why?')
    expect(whyFields).toHaveLength(1)
    expect(screen.queryByRole('button', { name: /save limits/i })).toBeNull()
    await userEvent.type(whyFields[0]!, 'unrelated advanced edit')
    await userEvent.click(screen.getByRole('button', { name: /^save settings$/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect((puts()[0]!.body as Record<string, unknown>).daily_tokens_hard).toBe(9000)
  })
})

/** Routes GET /agent/whoami to a fixed operator flag; everything else keeps
 *  the file's default merge-and-echo behaviour against `stored`. */
function mockWhoamiOperator(operator: boolean) {
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const href = String(url)
    const path = href.split('?')[0]!
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    requests.push({ url: href, method, body })

    if (path === '/agent/whoami') {
      return new Response(JSON.stringify({ email: 'kai@example.com', project: 'acme', operator }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (method === 'PUT') {
      stored = { ...stored, ...(body as object), project: 'acme', updated_at: 2 }
    }
    return new Response(JSON.stringify(stored), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as typeof globalThis.fetch
}

// project_settings.go:96-106 operator-gates THREE fields, not two:
// daily_tokens_soft, daily_tokens_hard AND max_concurrent_jobs. The first two
// left this page entirely (BudgetPanel owns them); this one stays in Advanced
// but must not be freely editable by a non-operator, or their save 403s the
// WHOLE draft with no indication of which field caused it.
describe('max_concurrent_jobs is operator-gated in Advanced too', () => {
  it('a non-operator gets no editable control, only the fixed sentence', async () => {
    mockWhoamiOperator(false)
    render(<ProjectSettingsPage />)
    await openAdvanced()

    expect(screen.queryByLabelText(/max concurrent jobs/i)).not.toBeInTheDocument()
    expect(screen.getByText(/max concurrent jobs: 4/i)).toBeInTheDocument()
    // Two "Only the operator can change this." sentences now coexist on a
    // non-operator's screen — BudgetPanel's own (for the two token budgets)
    // and this one (for the cap) — both fixed, both server-answer-gated.
    expect(screen.getAllByText(/only the operator can change this/i).length).toBeGreaterThanOrEqual(2)
  })

  it('an operator still gets the editable control', async () => {
    mockWhoamiOperator(true)
    render(<ProjectSettingsPage />)
    await openAdvanced()

    expect(await screen.findByLabelText(/max concurrent jobs/i)).toBeInTheDocument()
  })
})
