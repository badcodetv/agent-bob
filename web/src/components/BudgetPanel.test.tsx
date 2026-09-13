// @vitest-environment jsdom
// A5 — the budget panel. Three things pinned here because they are easy to
// get backwards: the operator gate reads `whoami.operator` and nothing else,
// `cost_known: false` never renders as $0.00, and the limits form is the one
// place a negative budget is refused before a save is attempted.

import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import BudgetPanel from './BudgetPanel.js'

let originalFetch: typeof globalThis.fetch
let requests: { url: string; method: string; body: unknown }[] = []
let usageBody: Record<string, unknown>
let whoamiBody: Record<string, unknown>
let settingsBody: Record<string, unknown>

const zeroWindow = { input_tokens: 0, output_tokens: 0, cost_usd: 0, queries: 0 }

beforeEach(() => {
  requests = []
  usageBody = {
    project: 'acme',
    day_starts_at: 0,
    today: { input_tokens: 100, output_tokens: 20, cost_usd: 0.01, queries: 3 },
    last_7d: { ...zeroWindow },
    last_30d: { ...zeroWindow },
    budget: { daily_tokens_soft: 1000, daily_tokens_hard: 2000 },
    credential_mode: 'api-key',
    cost_known: true,
  }
  whoamiBody = { email: 'kai@example.com', project: 'acme', operator: false }
  settingsBody = {
    project: 'acme',
    base_image: '',
    system_prompt: '',
    mcp_config: {},
    attention_channel: {},
    max_concurrent_jobs: 4,
    daily_tokens_soft: 1000,
    daily_tokens_hard: 2000,
    briefing_max_bytes: 2048,
    snapshot_ttl_days: 30,
    updated_at: 1,
  }

  originalFetch = globalThis.fetch
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const href = String(url)
    const path = href.split('?')[0]!
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    requests.push({ url: href, method, body })
    if (path === '/agent/usage') {
      return new Response(JSON.stringify(usageBody), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (path === '/agent/whoami') {
      return new Response(JSON.stringify(whoamiBody), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    if (path === '/agent/project-settings') {
      if (method === 'PUT') {
        settingsBody = { ...settingsBody, ...(body as object), project: 'acme', updated_at: 2 }
      }
      return new Response(JSON.stringify(settingsBody), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response('not found', { status: 404 })
  }) as typeof globalThis.fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

const puts = () => requests.filter((r) => r.method === 'PUT' && r.url.includes('/agent/project-settings'))

describe('numbers and cost', () => {
  it('formats today’s tokens and cost, and the two rolling windows', async () => {
    render(<BudgetPanel />)
    expect(await screen.findByText(/today: 120 tokens · \$0\.01/i)).toBeInTheDocument()
    expect(screen.getByText(/last 7 days: 0 tokens · \$0\.00/i)).toBeInTheDocument()
    expect(screen.getByText(/last 30 days: 0 tokens · \$0\.00/i)).toBeInTheDocument()
  })

  it('renders "cost not reported" rather than $0.00 when cost_known is false', async () => {
    usageBody.cost_known = false
    render(<BudgetPanel />)
    expect(await screen.findByText(/today: 120 tokens · cost not reported/i)).toBeInTheDocument()
  })

  it('on the subscription, shows tokens and says why there is no dollar figure', async () => {
    usageBody.credential_mode = 'subscription'
    usageBody.today = { ...(usageBody.today as object), cost_usd: 4.34 }
    render(<BudgetPanel collapsible />)
    const line = await screen.findByText(/today: 120 tokens \(subscription — not billed per token\)/i)
    expect(line.textContent).not.toMatch(/\$/)
  })

  it('shows the credential-mode sentence', async () => {
    render(<BudgetPanel />)
    expect(await screen.findByText(/billed to the api key/i)).toBeInTheDocument()
  })
})

describe('the operator gate', () => {
  it('shows only the numbers and the fixed sentence for a non-operator', async () => {
    whoamiBody.operator = false
    render(<BudgetPanel />)
    await screen.findByText(/only the operator can change this/i)
    expect(screen.queryByTestId('budget-limits-form')).not.toBeInTheDocument()
    expect(screen.getByText(/soft limit: 1,000 tokens\/day/i)).toBeInTheDocument()
  })

  it('shows the editable limits form for an operator', async () => {
    whoamiBody.operator = true
    render(<BudgetPanel />)
    expect(await screen.findByTestId('budget-limits-form')).toBeInTheDocument()
    expect(screen.queryByText(/only the operator can change this/i)).not.toBeInTheDocument()
  })

  // The whole point of A5's hard requirement: the gate is server-answered,
  // never a client guess. A whoami read failure must fail CLOSED.
  it('fails closed (no form) when the whoami read fails', async () => {
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL) => {
      const path = String(url).split('?')[0]!
      if (path === '/agent/whoami') return new Response('boom', { status: 500 })
      if (path === '/agent/usage') {
        return new Response(JSON.stringify(usageBody), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      return new Response(JSON.stringify(settingsBody), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }) as typeof globalThis.fetch

    render(<BudgetPanel />)
    await waitFor(() => expect(screen.queryByText(/loading budget/i)).not.toBeInTheDocument())
    expect(screen.queryByTestId('budget-limits-form')).not.toBeInTheDocument()
  })
})

describe('editing the limits', () => {
  it('saves the two fields with a reason, and the numbers refresh', async () => {
    whoamiBody.operator = true
    render(<BudgetPanel />)
    const hard = await screen.findByLabelText('Hard limit')
    fireEvent.change(hard, { target: { value: '5000' } })
    await userEvent.type(screen.getByLabelText('Why?'), 'raising the launch ceiling')
    await userEvent.click(screen.getByRole('button', { name: /save limits/i }))

    await waitFor(() => expect(puts()).toHaveLength(1))
    expect(puts()[0]!.body).toMatchObject({ daily_tokens_hard: 5000, rationale: 'raising the launch ceiling' })
  })

  it('blocks the save on a negative limit, with an inline error', async () => {
    whoamiBody.operator = true
    render(<BudgetPanel />)
    const soft = await screen.findByLabelText('Soft limit')
    fireEvent.change(soft, { target: { value: '-5' } })
    await userEvent.type(screen.getByLabelText('Why?'), 'testing')
    expect(await screen.findByText(/must not be negative/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save limits/i })).toBeDisabled()
    expect(puts()).toHaveLength(0)
  })
})

describe('collapsing on the Desk', () => {
  it('collapses to one line while under budget', async () => {
    render(<BudgetPanel collapsible />)
    expect(await screen.findByTestId('budget-panel-collapsed')).toBeInTheDocument()
    expect(screen.getByText(/under budget/i)).toBeInTheDocument()
  })

  it('shows the full panel once at or over the soft tier, even when collapsible', async () => {
    usageBody.today = { input_tokens: 1000, output_tokens: 0, cost_usd: 0.5, queries: 1 }
    render(<BudgetPanel collapsible />)
    expect(await screen.findByTestId('budget-panel')).toBeInTheDocument()
    expect(screen.queryByTestId('budget-panel-collapsed')).not.toBeInTheDocument()
  })

  it('expands on demand from the collapsed line', async () => {
    render(<BudgetPanel collapsible />)
    await screen.findByTestId('budget-panel-collapsed')
    await userEvent.click(screen.getByRole('button', { name: /details/i }))
    expect(await screen.findByTestId('budget-panel')).toBeInTheDocument()
  })
})
