import { describe, expect, it } from 'vitest'
import {
  coerceGitProjectionStatus,
  describeGitProjection,
  isGitProjectionHealth,
  shortSha,
  GIT_PROJECTION_ENDPOINT,
  GIT_PROJECTION_HEALTHS,
  type GitProjectionHealth,
  type GitProjectionStatus,
} from './gitProjection.js'

/** A healthy status; each test overrides the fields it is about. */
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
    last_imported_sha: '',
    updated_at: 1700000000,
    health: 'ok',
    last_error: '',
    last_error_at: 0,
    push_behind: false,
    quarantine: [],
    ignored: [],
    ...patch,
  })
}

describe('coerceGitProjectionStatus', () => {
  it('fills every field from an empty body, so nothing branches on undefined', () => {
    const s = coerceGitProjectionStatus({})
    expect(s).toEqual({
      project: '',
      enabled: false,
      remote: '',
      branch: '',
      subfolder: '',
      token_env: '',
      browse_url: '',
      state_available: false,
      last_rendered_seq: 0,
      last_rendered_sha: '',
      last_pushed_sha: '',
      last_imported_sha: '',
      updated_at: 0,
      health: 'off',
      last_error: '',
      last_error_at: 0,
      unrenderable_field: '',
      push_behind: false,
      quarantine: [],
      ignored: [],
    })
  })

  it('survives null, a string and an array where an object was expected', () => {
    for (const raw of [null, undefined, 'nope', [1, 2, 3], 7]) {
      expect(coerceGitProjectionStatus(raw).health).toBe('off')
    }
  })

  it('coerces the note lists and drops non-objects inside them', () => {
    const s = coerceGitProjectionStatus({
      quarantine: [{ path: 'orange/workers/scout.md', reason: 'bad frontmatter', at: 5 }, null],
      ignored: 'not an array',
    })
    expect(s.quarantine).toEqual([
      { path: 'orange/workers/scout.md', reason: 'bad frontmatter', at: 5 },
      { path: '', reason: '', at: 0 },
    ])
    expect(s.ignored).toEqual([])
  })

  // A newer server is the case that matters: reporting "fine" because the word
  // was unfamiliar is the one answer that must never be produced.
  it('reports an unrecognised health word as unknown, never as ok', () => {
    expect(coerceGitProjectionStatus({ enabled: true, health: 'exploded' }).health).toBe('unknown')
    expect(coerceGitProjectionStatus({ enabled: true, health: 42 }).health).toBe('unknown')
    // …unless projection is off, where there is nothing to be unsure about.
    expect(coerceGitProjectionStatus({ enabled: false, health: 'exploded' }).health).toBe('off')
  })

  it('accepts every health word the engine emits', () => {
    for (const h of GIT_PROJECTION_HEALTHS) {
      expect(isGitProjectionHealth(h)).toBe(true)
      expect(coerceGitProjectionStatus({ enabled: true, health: h }).health).toBe(h)
    }
    expect(isGitProjectionHealth('nope')).toBe(false)
  })

  it('names the endpoint the engine registers', () => {
    expect(GIT_PROJECTION_ENDPOINT).toBe('/agent/git-projection')
  })
})

describe('shortSha', () => {
  it('shortens the way git does, and leaves a short or empty sha alone', () => {
    expect(shortSha('abc1234567890')).toBe('abc1234')
    expect(shortSha('abc12')).toBe('abc12')
    expect(shortSha('')).toBe('')
  })
})

describe('describeGitProjection', () => {
  it('off reads as a decision, not a fault, and asks nothing of anyone', () => {
    const s = describeGitProjection(status({ enabled: false, health: 'off' }))
    expect(s.severity).toBe('off')
    expect(s.detail).toContain('setting, not a fault')
    expect(s.action).toBe('')
  })

  it('healthy says what was rendered and what was pushed', () => {
    const s = describeGitProjection(status())
    expect(s.severity).toBe('ok')
    expect(s.detail).toContain('#42')
    expect(s.detail).toContain('abc1234')
    expect(s.action).toBe('')
  })

  it('healthy but not yet pushed is information, not an alarm', () => {
    const s = describeGitProjection(status({ push_behind: true, last_pushed_sha: 'old0000' }))
    expect(s.severity).toBe('info')
    expect(s.detail).toContain('clears itself')
  })

  // The failure that hides: rendering keeps working, so nothing else looks
  // wrong. The prose has to say the repository is going stale in as many words.
  it('push failing says the repository is going stale and names the credential', () => {
    const s = describeGitProjection(
      status({
        health: 'push_failing',
        push_behind: true,
        last_pushed_sha: 'old0000',
        last_error: 'push: could not resolve host github.com',
      }),
    )
    expect(s.severity).toBe('error')
    expect(s.headline).toContain('stale')
    expect(s.detail).toContain('piling up')
    expect(s.detail).toContain('could not resolve host')
    expect(s.action).toContain('ACME_GITHUB_TOKEN')
  })

  it('push failing without a configured token still says what to check', () => {
    const s = describeGitProjection(status({ health: 'push_failing', token_env: '' }))
    expect(s.action).toContain('push credential')
    expect(s.action).not.toContain('undefined')
  })

  it('diverged says plainly that it will not resolve on its own', () => {
    const s = describeGitProjection(status({ health: 'diverged' }))
    expect(s.severity).toBe('error')
    expect(s.headline).toContain('will not resolve on its own')
    expect(s.detail).toContain('never force, rebase or merge')
    expect(s.action).toContain('human')
  })

  it('unrenderable names the field and says to move it to a ${VAR} reference', () => {
    const s = describeGitProjection(
      status({
        health: 'unrenderable',
        unrenderable_field: 'ProjectSettings.AttentionChannel.url',
      }),
    )
    expect(s.severity).toBe('error')
    expect(s.detail).toContain('ProjectSettings.AttentionChannel.url')
    expect(s.detail).toContain('stopped moving')
    expect(s.action).toContain('${VAR}')
    expect(s.action).toContain('environment variable')
  })

  // The unrenderable branch is the one state that must NOT echo last_error:
  // that message exists because a value must not be published.
  it('unrenderable never repeats the error message, even one carrying a secret', () => {
    const secret = 'https://hooks.slack.com/services/T00/B00/XXXXsupersecret'
    const s = describeGitProjection(
      status({
        health: 'unrenderable',
        unrenderable_field: 'ProjectSettings.AttentionChannel.url',
        last_error: `gitproj: cannot be rendered: the stored value ${secret} is a literal`,
      }),
    )
    expect(`${s.headline} ${s.detail} ${s.action}`).not.toContain(secret)
    expect(s.detail).toContain('ProjectSettings.AttentionChannel.url')
  })

  it('unrenderable still explains itself when the field path could not be parsed', () => {
    const s = describeGitProjection(status({ health: 'unrenderable', unrenderable_field: '' }))
    expect(s.detail).toContain('credential-bearing field')
    expect(s.action).toContain('${VAR}')
  })

  it('quarantined says the whole push was refused, not just the broken file', () => {
    const s = describeGitProjection(
      status({
        health: 'quarantined',
        quarantine: [{ path: 'orange/workers/scout.md', reason: 'frontmatter is not a mapping', at: 0 }],
      }),
    )
    expect(s.severity).toBe('error')
    expect(s.headline).toContain('1 file')
    expect(s.detail).toContain('NOTHING in it was applied')
    expect(s.detail).toContain('good ones beside it')
  })

  it('quarantined pluralises', () => {
    const s = describeGitProjection(
      status({
        health: 'quarantined',
        quarantine: [
          { path: 'a.md', reason: 'x', at: 0 },
          { path: 'b.md', reason: 'y', at: 0 },
        ],
      }),
    )
    expect(s.headline).toContain('2 files')
  })

  it('unknown warns that git may be stale rather than claiming health', () => {
    const s = describeGitProjection(status({ health: 'unknown', state_available: false }))
    expect(s.severity).toBe('warning')
    expect(s.detail).toContain('possibly stale')
  })

  it('a failure with no recorded reason says so instead of inventing one', () => {
    const s = describeGitProjection(status({ health: 'failing', last_error: '' }))
    expect(s.severity).toBe('error')
    expect(s.detail).toContain('no reason was recorded')
  })

  it('every health word produces a headline, a detail and a valid severity', () => {
    for (const h of GIT_PROJECTION_HEALTHS as readonly GitProjectionHealth[]) {
      const s = describeGitProjection(status({ health: h, enabled: h !== 'off' }))
      expect(s.headline, h).not.toBe('')
      expect(s.detail, h).not.toBe('')
      expect(['off', 'ok', 'info', 'warning', 'error'], h).toContain(s.severity)
      expect(s.detail, h).not.toContain('undefined')
      expect(s.headline, h).not.toContain('undefined')
    }
  })
})
