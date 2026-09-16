import { describe, expect, it } from 'vitest'
import type { ActivityRecord } from './activity.js'
import type { Subscription } from './events.js'
import type { Schedule } from './schedules.js'
import {
  buildWakeRules,
  describeClock,
  describeHandler,
  wakeRulesFor,
  workerRoleLine,
  workerStatus,
} from './team.js'

const schedule = (over: Partial<Schedule>): Schedule => ({
  id: 'sch',
  project: 'acme',
  worker: 'scout',
  cron: '30 7 * * *',
  input: '',
  enabled: true,
  created_at: 0,
  updated_at: 0,
  ...over,
})

const sub = (over: Partial<Subscription>): Subscription => ({
  id: 'sub',
  project: 'acme',
  event_type: 'worker.finished',
  filter: {},
  worker: 'writer',
  max_firings_per_hour: 0,
  enabled: true,
  created_at: 0,
  updated_at: 0,
  ...over,
})

const record = (over: Partial<ActivityRecord>): ActivityRecord => ({
  id: 'r',
  kind: 'job',
  lens: 'jobs',
  glyph: 'agent',
  atMs: 0,
  headline: '',
  detail: '',
  detailIsQuote: false,
  meta: '',
  worker: '',
  sessionId: '',
  eventType: '',
  gapBeforeLabel: '',
  entry: null,
  ...over,
} as ActivityRecord)

describe('workerRoleLine', () => {
  it('prefers the description', () => {
    expect(workerRoleLine({ description: '  Finds  creators. ', system_prompt: 'You are scout.' })).toBe(
      'Finds creators.',
    )
  })

  it('falls back to the first sentence of the prompt, skipping headings', () => {
    const prompt = '# Scout\n\nYou find creators worth contacting. Then you write them down.\n\nMore.'
    expect(workerRoleLine({ description: '', system_prompt: prompt })).toBe('You find creators worth contacting.')
  })

  it('does not split a sentence on a decimal or version number', () => {
    expect(workerRoleLine({ description: '', system_prompt: 'Track v1.2 releases. Then stop.' })).toBe(
      'Track v1.2 releases.',
    )
  })

  it('clamps a long line at a word boundary', () => {
    const line = workerRoleLine({ description: 'word '.repeat(100), system_prompt: '' }, 40)
    expect(line.length).toBeLessThanOrEqual(41)
    expect(line.endsWith('word…')).toBe(true)
  })

  it('is empty when there is nothing to say', () => {
    expect(workerRoleLine({ description: '', system_prompt: '## Heading only' })).toBe('')
  })
})

describe('describeClock', () => {
  it('reads by when, scope first', () => {
    expect(describeClock('30 7 * * *')).toBe('Every day at 07:30')
    expect(describeClock('0 9 * * 1-5')).toBe('On weekdays at 09:00')
    expect(describeClock('0 9 * * 1')).toBe('On Monday at 09:00')
  })

  it('keeps shapes it cannot reorder as describeCron wrote them', () => {
    expect(describeClock('*/15 * * * *')).toBe('Every 15 minutes')
  })

  it('shows an unparseable expression as itself', () => {
    expect(describeClock('not a cron')).toBe('not a cron')
  })
})

describe('describeHandler', () => {
  it('names the worker a worker.finished filter waits on', () => {
    expect(describeHandler(sub({ filter: { worker: 'scout' } }))).toEqual({
      sentence: 'When scout finishes',
      detail: 'worker.finished · worker=scout',
    })
  })

  it('says any worker when the filter is empty, and carries the rate limit', () => {
    expect(describeHandler(sub({ max_firings_per_hour: 6 }))).toEqual({
      sentence: 'When any worker finishes',
      detail: 'worker.finished · max 6/hour',
    })
  })

  it('handles custom types and wildcards', () => {
    expect(describeHandler(sub({ event_type: 'draft.ready' })).sentence).toBe('When draft.ready happens')
    expect(describeHandler(sub({ event_type: 'email.*' })).sentence).toBe('When any email event happens')
    expect(describeHandler(sub({ event_type: '*' })).sentence).toBe('When anything happens')
  })
})

describe('buildWakeRules', () => {
  it('turns schedules and subscriptions into sentences, grouped by worker', () => {
    const rules = buildWakeRules(
      [schedule({ id: 'b', worker: 'scout' }), schedule({ id: 'a', worker: 'architect', cron: '0 6 * * *' })],
      [sub({ id: 's', filter: { worker: 'scout' } })],
    )
    expect(rules.clocks.map((r) => [r.worker, r.sentence])).toEqual([
      ['architect', 'Every day at 06:00'],
      ['scout', 'Every day at 07:30'],
    ])
    expect(rules.handlers[0]).toMatchObject({ kind: 'event', worker: 'writer', sentence: 'When scout finishes' })
    expect(wakeRulesFor('scout', rules).map((r) => r.id)).toEqual(['b'])
  })

  it('keeps a session-mode schedule legible', () => {
    const rules = buildWakeRules([schedule({ worker: '', target_session: 'hypothesis-a' })], [])
    expect(rules.clocks[0]).toMatchObject({ worker: '', targetSession: 'hypothesis-a' })
  })
})

describe('workerStatus', () => {
  const now = 10 * 3600_000

  it('reads the newest record naming the worker', () => {
    const records = [
      record({ worker: 'writer', glyph: 'attention', atMs: now - 60_000 }),
      record({ worker: 'scout', glyph: 'agent', atMs: now - 3600_000 }),
      record({ worker: 'scout', glyph: 'failure', atMs: now - 7200_000 }),
    ]
    expect(workerStatus({ name: 'writer', enabled: true }, records, now)).toEqual({
      tone: 'attention',
      label: 'waiting on you',
    })
    expect(workerStatus({ name: 'scout', enabled: true }, records, now)).toEqual({
      tone: 'agent',
      label: 'active 1h ago',
    })
  })

  it('says a failure, an idle worker and a switched-off worker plainly', () => {
    const records = [record({ worker: 'analyst', glyph: 'failure', atMs: now - 7200_000 })]
    expect(workerStatus({ name: 'analyst', enabled: true }, records, now).label).toBe('last run failed · 2h ago')
    expect(workerStatus({ name: 'editor', enabled: true }, records, now)).toEqual({
      tone: 'idle',
      label: 'not run yet',
    })
    expect(workerStatus({ name: 'analyst', enabled: false }, records, now).tone).toBe('off')
  })
})
