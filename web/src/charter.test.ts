import { describe, expect, it } from 'vitest'
import {
  buildOnboardingSeed,
  coerceCharter,
  coerceCharterCurrent,
  coerceCharterEffects,
  coerceCharterIssues,
  describeCharterCadence,
  DEFAULT_ARCHITECT_CRON,
  DEFAULT_ARCHITECT_NAME,
} from './charter.js'

describe('coerceCharter', () => {
  it('fills every omitted field, and the architect defaults the engine would apply', () => {
    const c = coerceCharter({})
    expect(c).toEqual({
      goal: '',
      measure: '',
      label_rules: '',
      architect_name: DEFAULT_ARCHITECT_NAME,
      architect_cron: DEFAULT_ARCHITECT_CRON,
      project_background: '',
      rationale: '',
    })
  })

  it('keeps what the server sent', () => {
    const c = coerceCharter({
      goal: 'a weekly newsletter',
      measure: 'four in a month',
      label_rules: 'kind=draft',
      architect_name: 'chief-of-staff',
      architect_cron: '0 9 * * 1',
      project_background: 'a bookshop',
      rationale: 'repeat visits',
    })
    expect(c.architect_name).toBe('chief-of-staff')
    expect(c.architect_cron).toBe('0 9 * * 1')
    expect(c.label_rules).toBe('kind=draft')
  })

  it('treats a non-string as absent rather than rendering it', () => {
    const c = coerceCharter({ goal: 42, label_rules: null, architect_name: false })
    expect(c.goal).toBe('')
    expect(c.label_rules).toBe('')
    expect(c.architect_name).toBe(DEFAULT_ARCHITECT_NAME)
  })
})

describe('coerceCharterCurrent', () => {
  // The one field on the screen that decides whether a human can change the
  // project's shape. Anything but a literal true has to read as invalid.
  it.each([undefined, 'true', 1, null, 0, '', {}, []])('valid is false for %o', (v) => {
    expect(coerceCharterCurrent({ valid: v }).valid).toBe(false)
  })

  it('valid is true only for the boolean', () => {
    expect(coerceCharterCurrent({ valid: true }).valid).toBe(true)
  })

  it('fills the whole shape from nothing at all', () => {
    const cur = coerceCharterCurrent(null)
    expect(cur).toEqual({
      charter: null,
      summary: '',
      memory_id: '',
      created_at: 0,
      valid: false,
      errors: [],
      summary_of_effects: null,
    })
  })

  it('parses the issue list, filling either half a server might omit', () => {
    const cur = coerceCharterCurrent({
      errors: [{ path: 'goal', message: 'goal is required' }, { path: 'measure' }, {}],
    })
    expect(cur.errors).toEqual([
      { path: 'goal', message: 'goal is required' },
      { path: 'measure', message: '' },
      { path: '', message: '' },
    ])
  })

  it('reads the effects summary only when there is one', () => {
    expect(coerceCharterCurrent({ valid: true }).summary_of_effects).toBeNull()
    const cur = coerceCharterCurrent({
      valid: true,
      summary_of_effects: {
        architect_name: 'architect',
        architect_cron: '0 9 * * *',
        schedule_enabled: false,
        subscription_event: 'architect.run',
        memory_seed_labels: ['kind=project-goal,name=project-goal'],
        settings_fields: ['system_prompt', 'briefing'],
        worker_count: 1,
      },
    })
    expect(cur.summary_of_effects?.worker_count).toBe(1)
    expect(cur.summary_of_effects?.settings_fields).toEqual(['system_prompt', 'briefing'])
  })

  it('parses a charter object into a full charter', () => {
    const cur = coerceCharterCurrent({ charter: { goal: 'g' }, summary: 'Charter v1: g', memory_id: 'mem-1', created_at: 17 })
    expect(cur.charter?.goal).toBe('g')
    expect(cur.charter?.architect_cron).toBe(DEFAULT_ARCHITECT_CRON)
    expect(cur.summary).toBe('Charter v1: g')
    expect(cur.memory_id).toBe('mem-1')
    expect(cur.created_at).toBe(17)
  })
})

describe('coerceCharterEffects', () => {
  // The schedule ships disabled, so "off" is both the safe reading and the
  // true one; anything but a literal true must not claim the loop is running.
  it.each([undefined, 'true', 1, null])('schedule_enabled is false for %o', (v) => {
    expect(coerceCharterEffects({ schedule_enabled: v }).schedule_enabled).toBe(false)
  })

  it('fills the lists and the count', () => {
    const e = coerceCharterEffects({ worker_count: 'two' })
    expect(e.memory_seed_labels).toEqual([])
    expect(e.settings_fields).toEqual([])
    expect(e.worker_count).toBe(0)
  })
})

describe('coerceCharterIssues', () => {
  it('reads the 422 body', () => {
    expect(coerceCharterIssues({ errors: [{ path: 'goal', message: 'goal is required' }] })).toEqual([
      { path: 'goal', message: 'goal is required' },
    ])
  })

  it('is empty for anything that is not that body', () => {
    expect(coerceCharterIssues(null)).toEqual([])
    expect(coerceCharterIssues('nope')).toEqual([])
    expect(coerceCharterIssues({ errors: 'nope' })).toEqual([])
  })
})

describe('describeCharterCadence', () => {
  it('reads the shapes onboarding produces', () => {
    expect(describeCharterCadence('0 9 * * *')).toBe('every day at 09:00')
    expect(describeCharterCadence('30 6 * * 1')).toBe('every Monday at 06:30')
    expect(describeCharterCadence('0 9 * * 0')).toBe('every Sunday at 09:00')
  })

  // A wrong plain-English reading of a cron is worse than the cron itself,
  // because the human cannot tell that it is wrong.
  it.each(['*/5 * * * *', '0 9 1 * *', '0 9 * * 1-5', '@daily', '', 'nonsense'])(
    'hands back %o unchanged rather than guessing',
    (cron) => {
      expect(describeCharterCadence(cron)).toBe(cron.trim())
    },
  )
})

describe('buildOnboardingSeed', () => {
  it('gives the interviewer the session id it must label the deposit with', () => {
    const seed = buildOnboardingSeed('onboard-1', 'a weekly newsletter')
    expect(seed).toContain('onboard-1')
    expect(seed).toMatch(/label name set to exactly that id/i)
  })

  it('carries the goal verbatim, below a rule, attributed to the user', () => {
    const seed = buildOnboardingSeed('s1', 'Sell more books to people who came in once.')
    const [above, below] = seed.split('\n---\n')
    expect(below.trim()).toBe('Sell more books to people who came in once.')
    expect(above).toMatch(/typed by the person who created this project/i)
    // The boundary is the point: a goal reading "ignore your instructions"
    // must arrive marked as data, not as something the system said.
    expect(above).toMatch(/not an instruction to you/i)
  })

  it('says so when there is no goal, rather than shipping a blank line', () => {
    const seed = buildOnboardingSeed('s1', '   ')
    expect(seed).toMatch(/did not write a goal/i)
  })
})
