// @vitest-environment jsdom
// G16 — the console surface for the git projection.
//
// What is worth a test here is not the layout. It is that each way the
// projection can silently stop working reaches a human as a sentence they can
// act on, that "off" does not read as broken, and — the one hard rule — that
// the unrenderable case never puts the offending secret on the screen.

import React from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, vi } from 'vitest'
import GitProjectionPanel from './GitProjectionPanel.js'
import { coerceGitProjectionStatus, type GitProjectionStatus } from '../gitProjection.js'

function status(patch: Partial<GitProjectionStatus> = {}): GitProjectionStatus {
  return coerceGitProjectionStatus({
    project: 'acme',
    enabled: true,
    remote: 'https://github.com/badcode/acme.git',
    branch: 'main',
    subfolder: 'orange',
    token_env: 'ACME_GITHUB_TOKEN',
    browse_url: 'https://github.com/badcode/acme/tree/main/orange',
    state_available: true,
    last_rendered_seq: 42,
    last_rendered_sha: 'abc1234567890',
    last_pushed_sha: 'abc1234567890',
    last_imported_sha: 'def9876543210',
    updated_at: 1700000000,
    health: 'ok',
    ...patch,
  })
}

const health = () => screen.getByTestId('git-projection-health')

describe('GitProjectionPanel', () => {
  it('off: says projection is a setting rather than a fault, and shows no repo row', () => {
    render(
      <GitProjectionPanel
        status={coerceGitProjectionStatus({ project: 'acme', enabled: false, health: 'off' })}
      />,
    )
    expect(health()).toHaveAttribute('data-health', 'off')
    expect(screen.getByText(/Not published to git/)).toBeInTheDocument()
    expect(screen.getByText(/setting, not a fault/)).toBeInTheDocument()
    expect(screen.queryByTestId('git-projection-where')).not.toBeInTheDocument()
    expect(screen.queryByTestId('git-projection-watermarks')).not.toBeInTheDocument()
  })

  it('healthy: the repo is a link, and the two watermarks are shown', () => {
    render(<GitProjectionPanel status={status()} />)
    expect(health()).toHaveAttribute('data-health', 'ok')

    const link = screen.getByRole('link', { name: 'https://github.com/badcode/acme.git' })
    expect(link).toHaveAttribute('href', 'https://github.com/badcode/acme/tree/main/orange')

    const marks = screen.getByTestId('git-projection-watermarks')
    // The config-log seq, not a git date: seq is the projection's only
    // authority on order.
    expect(marks).toHaveTextContent('configuration change #42')
    expect(marks).toHaveTextContent('abc1234')
    expect(marks).toHaveTextContent('def9876')
    expect(screen.getByText('branch main')).toBeInTheDocument()
    expect(screen.getByText('orange/')).toBeInTheDocument()
  })

  it('healthy: shows the NAME of the push credential variable, never a token', () => {
    render(<GitProjectionPanel status={status()} />)
    expect(screen.getByText('token from $ACME_GITHUB_TOKEN')).toBeInTheDocument()
  })

  it('a remote with no browsable form is plain text, not a broken link', () => {
    render(<GitProjectionPanel status={status({ browse_url: '' })} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByText('https://github.com/badcode/acme.git')).toBeInTheDocument()
  })

  it('push failing: says the repository is going stale and names the credential', () => {
    render(
      <GitProjectionPanel
        status={status({
          health: 'push_failing',
          push_behind: true,
          last_pushed_sha: 'old0000000',
          last_error: 'push: could not resolve host github.com',
        })}
      />,
    )
    expect(health()).toHaveAttribute('data-health', 'push_failing')
    expect(screen.getByText(/going stale/)).toBeInTheDocument()
    expect(screen.getByText(/piling up/)).toBeInTheDocument()
    expect(health()).toHaveTextContent('ACME_GITHUB_TOKEN')
  })

  it('diverged: says a human has to reconcile it', () => {
    render(<GitProjectionPanel status={status({ health: 'diverged' })} />)
    expect(health()).toHaveAttribute('data-health', 'diverged')
    expect(screen.getByText(/will not resolve on its own/)).toBeInTheDocument()
    expect(screen.getByText(/never force, rebase or merge/)).toBeInTheDocument()
    expect(screen.getByText(/A human has to reconcile/)).toBeInTheDocument()
  })

  it('unrenderable: names the field and tells the operator what to do', () => {
    render(
      <GitProjectionPanel
        status={status({
          health: 'unrenderable',
          unrenderable_field: 'ProjectSettings.AttentionChannel.url',
          last_error:
            'gitproj: ProjectSettings.AttentionChannel.url cannot be rendered: credential-bearing field',
        })}
      />,
    )
    expect(health()).toHaveAttribute('data-health', 'unrenderable')
    expect(screen.getByText(/ProjectSettings\.AttentionChannel\.url/)).toBeInTheDocument()
    expect(screen.getByText(/Move the value into an environment variable/)).toBeInTheDocument()
  })

  // The hard rule, and the reason the unrenderable branch is the ONE state that
  // does not echo `last_error`. gitproj.UnrenderableError carries no value by
  // construction — so this feeds the panel a message that DOES carry one, the
  // shape a future careless server would produce, and asserts it still never
  // reaches the screen. Defence in depth over the exact error the whole check
  // exists to keep out of the repository.
  it('unrenderable: a secret in the error message never reaches the DOM', () => {
    const secret = 'https://hooks.slack.com/services/T00/B00/XXXXsupersecret'
    const { container } = render(
      <GitProjectionPanel
        status={status({
          health: 'unrenderable',
          unrenderable_field: 'ProjectSettings.AttentionChannel.url',
          last_error:
            'gitproj: ProjectSettings.AttentionChannel.url cannot be rendered: ' +
            `credential-bearing field: the stored value ${secret} is not a whole-value \${VAR} reference`,
        })}
      />,
    )
    expect(container.innerHTML).not.toContain(secret)
    expect(container.innerHTML).not.toContain('XXXXsupersecret')
    expect(container.innerHTML).not.toContain('hooks.slack.com')
    // …and the operator is still told what to fix.
    expect(container.innerHTML).toContain('ProjectSettings.AttentionChannel.url')
  })

  it('quarantine: lists the file and the reason, and says the push was refused in full', () => {
    render(
      <GitProjectionPanel
        status={status({
          health: 'quarantined',
          quarantine: [
            { path: 'orange/workers/scout.md', reason: 'frontmatter is not a mapping', at: 1 },
          ],
        })}
      />,
    )
    const list = screen.getByTestId('git-projection-quarantine')
    expect(list).toHaveTextContent('orange/workers/scout.md')
    expect(list).toHaveTextContent('frontmatter is not a mapping')
    expect(screen.getByText(/NOTHING in it was applied/)).toBeInTheDocument()
  })

  // DI10: an operator who deletes a skill file and sees nothing happen assumes
  // the whole system is broken. The panel has to say what happened instead.
  it('ignored: explains that append-only deletes and image edits do nothing', () => {
    render(
      <GitProjectionPanel
        status={status({
          ignored: [
            { path: 'orange/skills/research.md', reason: 'skills are append-only; deleting the file removes nothing', at: 1 },
            { path: 'orange/images/base.md', reason: 'images are not importable', at: 1 },
          ],
        })}
      />,
    )
    const list = screen.getByTestId('git-projection-ignored')
    expect(list).toHaveTextContent('orange/skills/research.md')
    expect(list).toHaveTextContent('orange/images/base.md')
    expect(list).toHaveTextContent(/append-only/)
    expect(list).toHaveTextContent(/the change you made is not in the project/i)
  })

  it('no quarantine or ignored entries renders neither list', () => {
    render(<GitProjectionPanel status={status()} />)
    expect(screen.queryByTestId('git-projection-quarantine')).not.toBeInTheDocument()
    expect(screen.queryByTestId('git-projection-ignored')).not.toBeInTheDocument()
  })

  it('unknown: warns that git may be stale, and hides the watermarks it cannot read', () => {
    render(<GitProjectionPanel status={status({ health: 'unknown', state_available: false })} />)
    expect(screen.getByText(/possibly stale/)).toBeInTheDocument()
    expect(screen.queryByTestId('git-projection-watermarks')).not.toBeInTheDocument()
    // …but the repo link is still there, which is the point of answering at all.
    expect(screen.getByTestId('git-projection-where')).toBeInTheDocument()
  })

  it('a load failure is shown in the server\'s own words', () => {
    render(<GitProjectionPanel status={null} error="project settings not configured" />)
    expect(screen.getByText('project settings not configured')).toBeInTheDocument()
    expect(screen.queryByTestId('git-projection-health')).not.toBeInTheDocument()
  })

  it('shows a spinner while the first load is in flight', () => {
    render(<GitProjectionPanel status={null} loading />)
    expect(screen.getByLabelText('Loading git projection status')).toBeInTheDocument()
  })

  it('offers "Check again" only when a refresh is wired', async () => {
    const onRefresh = vi.fn()
    const { rerender } = render(<GitProjectionPanel status={status()} onRefresh={onRefresh} />)
    await userEvent.click(screen.getByRole('button', { name: 'Check again' }))
    expect(onRefresh).toHaveBeenCalledTimes(1)

    rerender(<GitProjectionPanel status={status()} />)
    expect(screen.queryByRole('button', { name: 'Check again' })).not.toBeInTheDocument()
  })
})
