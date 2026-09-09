// @vitest-environment jsdom
// T15 — the human gate. What is worth a test here: an unfinished charter
// cannot be approved and says why in words; a finished one can, once; and a
// refusal reaches the human in the server's own words rather than as
// "something went wrong".

import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi } from 'vitest'
import CharterPanel from './CharterPanel.js'
import { coerceCharterCurrent, type CharterCurrent } from '../charter.js'

const LABEL_RULES =
  'kind=decision — a choice that was made, and why.\n' +
  'kind=summary — what happened in one thread of work. name=<thread-slug>.\n' +
  'kind=lesson — something that should change how this project acts next time.\n' +
  'kind=draft — a newsletter written but not sent. name=<issue-slug>.'

function validCharter(overrides: Record<string, unknown> = {}): CharterCurrent {
  return coerceCharterCurrent({
    charter: {
      goal: 'Send one newsletter a week to the shop mailing list.',
      measure: 'Four have gone out in a month and the list is bigger than 430.',
      label_rules: LABEL_RULES,
      project_background: 'An independent bookshop in Bristol.',
      rationale: 'Ellen said repeat visits are the problem, not new customers.',
      architect_cron: '0 9 * * 1',
    },
    summary: 'Charter v1: a weekly bookshop newsletter.',
    memory_id: 'mem-1',
    created_at: 1789000000123,
    valid: true,
    summary_of_effects: {
      architect_name: 'architect',
      architect_cron: '0 9 * * 1',
      schedule_enabled: false,
      subscription_event: 'architect.run',
      memory_seed_labels: ['kind=project-goal,name=project-goal'],
      settings_fields: ['system_prompt', 'briefing'],
      worker_count: 1,
    },
    ...overrides,
  })
}

describe('CharterPanel', () => {
  it('shows the four substantive parts, and the labelling rules IN FULL', () => {
    render(<CharterPanel charter={validCharter()} onApprove={vi.fn()} />)

    expect(screen.getByText(/Send one newsletter a week/)).toBeTruthy()
    expect(screen.getByText(/bigger than 430/)).toBeTruthy()
    expect(screen.getByText(/independent bookshop/)).toBeTruthy()

    // Every rule, not a truncation: these are the thing being agreed, and a
    // human who cannot read the last one has not agreed to it.
    const rules = screen.getByText(/kind=decision/)
    for (const line of LABEL_RULES.split('\n')) {
      expect(rules.textContent).toContain(line)
    }
    expect(rules.textContent).not.toContain('…')
  })

  it('says the schedule starts switched off, and reads the cron as a phrase', () => {
    render(<CharterPanel charter={validCharter()} onApprove={vi.fn()} />)
    expect(screen.getByText('every Monday at 09:00')).toBeTruthy()
    expect(screen.getByText(/switched off/)).toBeTruthy()
  })

  it('shows the rationale as the commit message it is', () => {
    render(<CharterPanel charter={validCharter()} onApprove={vi.fn()} />)
    expect(screen.getByText(/Ellen said repeat visits/)).toBeTruthy()
  })

  it('offers no Approve for an invalid charter, and lists the issues in words', () => {
    const invalid = coerceCharterCurrent({
      charter: { goal: 'Send a newsletter.' },
      summary: 'Charter draft.',
      valid: false,
      errors: [
        { path: 'measure', message: 'measure is required' },
        { path: 'label_rules', message: 'label_rules is required' },
      ],
    })
    render(<CharterPanel charter={invalid} onApprove={vi.fn()} />)

    expect(screen.queryByTestId('charter-approve')).toBeNull()
    expect(screen.getByText('measure is required')).toBeTruthy()
    expect(screen.getByText('label_rules is required')).toBeTruthy()
  })

  it('approves once when the charter is valid', async () => {
    const onApprove = vi.fn().mockResolvedValue({})
    render(<CharterPanel charter={validCharter()} onApprove={onApprove} />)

    await userEvent.click(screen.getByTestId('charter-approve'))
    await waitFor(() => expect(onApprove).toHaveBeenCalledTimes(1))
  })

  it('renders a refusal verbatim rather than paraphrasing it', () => {
    render(
      <CharterPanel
        charter={validCharter()}
        onApprove={vi.fn()}
        applyError={'topology not applicable: worker architect already exists; nothing was changed'}
      />,
    )
    expect(
      screen.getByText(
        'topology not applicable: worker architect already exists; nothing was changed',
      ),
    ).toBeTruthy()
  })

  // A charter can be revised between the render and the click, so a 422 is a
  // live possibility on a panel that looked approvable. Its issues supersede
  // the ones the read reported.
  it('prefers the apply issues over the stale read issues', () => {
    render(
      <CharterPanel
        charter={validCharter({ errors: [{ path: 'goal', message: 'stale' }] })}
        onApprove={vi.fn()}
        applyIssues={[{ path: 'measure', message: 'measure is required' }]}
      />,
    )
    expect(screen.getByText('measure is required')).toBeTruthy()
    expect(screen.queryByText('stale')).toBeNull()
  })

  it('stops offering to approve once it has been approved', () => {
    render(<CharterPanel charter={validCharter()} onApprove={vi.fn()} applied />)
    expect(screen.queryByTestId('charter-approve')).toBeNull()
    expect(screen.getByTestId('charter-applied')).toBeTruthy()
  })

  it('cannot be double-clicked into two approvals', async () => {
    let resolve: (v: unknown) => void = () => {}
    const onApprove = vi.fn().mockReturnValue(new Promise((r) => { resolve = r }))
    render(<CharterPanel charter={validCharter()} onApprove={onApprove} />)

    const button = screen.getByTestId('charter-approve') as HTMLButtonElement
    await userEvent.click(button)
    await waitFor(() => expect(button.disabled).toBe(true))
    // fireEvent, not userEvent: userEvent refuses to click a disabled control
    // at all, which would make this assert the test library's behaviour
    // rather than the panel's. fireEvent dispatches regardless, so what is
    // being proved is that a second click reaches nothing.
    fireEvent.click(button)
    expect(onApprove).toHaveBeenCalledTimes(1)
    resolve({})
  })
})
