// A1: the Activity fold — the whole project on one rail, in time order
// (docs/product/28-console-ia-design.md §1).
//
// The table that matters most is J1: config events stamp MILLISECONDS while
// everything else stamps SECONDS, and a rail sorts by time. A fold that gets
// this wrong sorts every configuration change fifty thousand years into the
// future, silently, looking entirely plausible — so it is tested first and with
// one record of every kind in the same list.

import { describe, it, expect } from 'vitest'
import {
  ACTIVITY_GAP_THRESHOLD_MS,
  ACTIVITY_HOME,
  ACTIVITY_LENSES,
  buildActivity,
  oldestShownMs,
  toMs,
  type ActivityRecord,
  type BuildActivityInput,
} from './activity.js'
import { coerceAttentionRequest, type AttentionRequest } from './desk.js'
import { coerceConfigEvent, type ConfigEvent } from './configLog.js'
import {
  coerceDelivery,
  coerceProjectEvent,
  coerceSubscription,
  type EventDelivery,
  type ProjectEvent,
  type Subscription,
} from './events.js'
import { coerceSchedule, type Schedule } from './schedules.js'

// The clock every case measures against: unix seconds, and its millisecond
// twin for the config log.
const NOW = 1_789_000_000
const NOW_MS = NOW * 1000

const delivery = (over: Partial<EventDelivery> = {}): EventDelivery =>
  coerceDelivery({
    id: 'd1',
    project: 'acme',
    event_id: 'e1',
    subscription_id: 's1',
    session_id: 'sess-1',
    worker: 'archivist',
    status: 'ok',
    started_at: NOW - 3600,
    ended_at: NOW - 3559, // 41s
    created_at: NOW - 3600,
    updated_at: NOW - 3559,
    ...over,
  })

const projectEvent = (over: Partial<ProjectEvent> = {}): ProjectEvent =>
  coerceProjectEvent({
    id: 'e1',
    project: 'acme',
    type: 'worker.finished',
    text: 'email-answerer finished.',
    envelope: {
      depth: 0,
      source: 'core',
      worker: 'email-answerer',
      session_id: 'sess-0',
      interactive: false,
      attention_requested: false,
    },
    occurred_at: NOW - 3600,
    created_at: NOW - 3600,
    delivered: true,
    ...over,
  })

const subscription = (over: Partial<Subscription> = {}): Subscription =>
  coerceSubscription({
    id: 's1',
    project: 'acme',
    event_type: 'worker.finished',
    filter: {},
    worker: 'archivist',
    max_firings_per_hour: 0,
    enabled: true,
    created_at: 0,
    updated_at: 0,
    ...over,
  })

const request = (over: Partial<AttentionRequest> = {}): AttentionRequest =>
  coerceAttentionRequest({
    id: 'a1',
    project: 'acme',
    session_id: 'sess-1',
    worker: 'email-answerer',
    message: "Reply drafted for the Ridley invoice query, but the amount doesn't match.",
    session_url: 'https://console.example/p/acme/s/sess-1',
    channel: 'webhook',
    delivered: true,
    expires_at: 0,
    created_at: NOW - 9600,
    answered_at: 0,
    timed_out_at: 0,
    ...over,
  })

const configEvent = (over: Partial<ConfigEvent> = {}): ConfigEvent =>
  coerceConfigEvent({
    id: 'c1',
    project: 'acme',
    actor_worker: 'email-reviewer',
    actor_session: 'sess-5',
    action: 'worker_prompt_write',
    payload: { name: 'email-answerer', prompt: 'Be brief.' },
    rationale: 'narrowing yesterday’s rule',
    created_at: NOW_MS,
    ...over,
  })

const schedule = (over: Partial<Schedule> = {}): Schedule =>
  coerceSchedule({
    id: 'sch-1',
    project: 'acme',
    worker: 'nightly-sweep',
    cron: '0 2 * * *',
    input: 'Sweep.',
    enabled: false,
    created_at: NOW - 100_000,
    updated_at: NOW - 1000,
    provision_failures: 5,
    last_provision_error: 'image "toolbox:9" names no image in the catalogue',
    ...over,
  })

const input = (over: Partial<BuildActivityInput> = {}): BuildActivityInput => ({
  events: [],
  deliveries: [],
  configEvents: [],
  attentionRequests: [],
  subscriptions: [],
  schedules: [],
  nowMs: NOW_MS,
  projectId: 'acme',
  ...over,
})

const ids = (records: ActivityRecord[]): string[] => records.map((r) => r.id)
const kinds = (records: ActivityRecord[]): string[] => records.map((r) => r.kind)

// ---------------------------------------------------------------------------

describe('toMs — the J1 boundary', () => {
  it('scales seconds and leaves milliseconds alone', () => {
    expect(toMs(NOW, 'seconds')).toBe(NOW_MS)
    expect(toMs(NOW_MS, 'ms')).toBe(NOW_MS)
  })

  it('treats missing, zero and nonsense as "no time known"', () => {
    expect(toMs(0, 'seconds')).toBe(0)
    expect(toMs(undefined, 'ms')).toBe(0)
    expect(toMs(null, 'seconds')).toBe(0)
    expect(toMs(Number.NaN, 'ms')).toBe(0)
    expect(toMs(-5, 'seconds')).toBe(0)
  })
})

describe('J1 — one record of every kind, sorted together', () => {
  // The whole point: these are authored one second apart, in two different
  // unit systems. Any missed conversion throws the config change to the top
  // (or the bottom) by a factor of a thousand.
  it('interleaves seconds-stamped and millisecond-stamped records correctly', () => {
    const records = buildActivity(
      input({
        // newest → oldest, deliberately supplied out of order
        deliveries: [delivery({ id: 'd-mid', created_at: NOW - 2, started_at: NOW - 2, ended_at: NOW - 1 })],
        events: [projectEvent({ id: 'e1', occurred_at: NOW - 2, created_at: NOW - 2 })],
        configEvents: [
          configEvent({ id: 'c-newest', created_at: (NOW - 1) * 1000 }),
          configEvent({ id: 'c-oldest', created_at: (NOW - 3) * 1000 }),
        ],
        attentionRequests: [],
      }),
    )

    expect(ids(records)).toEqual(['change:c-newest', 'job:d-mid', 'change:c-oldest'])
    // And every record is held in the same unit, so the caller never has to ask.
    expect(records.map((r) => r.atMs)).toEqual([(NOW - 1) * 1000, (NOW - 2) * 1000, (NOW - 3) * 1000])
  })

  it('a config change one second newer than a job sorts above it, not below', () => {
    const records = buildActivity(
      input({
        deliveries: [delivery({ id: 'd1', created_at: NOW - 10 })],
        events: [projectEvent()],
        configEvents: [configEvent({ id: 'c1', created_at: (NOW - 9) * 1000 })],
      }),
    )
    expect(ids(records)).toEqual(['change:c1', 'job:d1'])
  })
})

describe('the occurrence model', () => {
  it('folds an event, its delivery and its job into ONE row', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [delivery()],
        subscriptions: [subscription()],
      }),
    )

    expect(records).toHaveLength(1)
    const [row] = records
    expect(row!.kind).toBe('job')
    // The event is the headline, the delivery the preposition, the job the outcome.
    expect(row!.headline).toBe('worker.finished woke archivist')
    expect(row!.meta).toBe('ran 41s')
    expect(row!.sessionId).toBe('sess-1')
    expect(row!.glyph).toBe('agent')
  })

  it('is honest about fan-out: one event waking three workers is three rows', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [
          delivery({ id: 'd1', worker: 'archivist', session_id: 'sess-1' }),
          delivery({ id: 'd2', worker: 'reviewer', session_id: 'sess-2' }),
          delivery({ id: 'd3', worker: 'scorer', session_id: 'sess-3', status: 'failed' }),
        ],
      }),
    )
    // Three jobs that can end three different ways — and one of them did.
    expect(records).toHaveLength(3)
    expect(records.filter((r) => r.glyph === 'failure')).toHaveLength(1)
  })

  it('gives an event that woke nobody its own row', () => {
    const records = buildActivity(
      input({ events: [projectEvent({ id: 'e-lonely', delivered: false })] }),
    )
    expect(kinds(records)).toEqual(['event'])
    expect(records[0]!.headline).toBe('worker.finished woke nobody')
  })

  it('does not list a delivered event twice', () => {
    const records = buildActivity(
      input({ events: [projectEvent()], deliveries: [delivery()] }),
    )
    expect(kinds(records)).toEqual(['job'])
  })
})

describe('asks — a parked delivery and its request are one occurrence', () => {
  it('joins them into a single attention row', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ status: 'awaiting_human', ended_at: 0, started_at: NOW - 9600 })],
        attentionRequests: [request()],
      }),
    )

    expect(records).toHaveLength(1)
    const [row] = records
    expect(row!.kind).toBe('ask')
    expect(row!.glyph).toBe('attention')
    expect(row!.detail).toContain('Ridley')
    expect(row!.detailIsQuote).toBe(true)
    expect(row!.meta).toBe('waiting 2h 40m')
  })

  it('drops the attention state once the request is answered', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ status: 'awaiting_human' })],
        attentionRequests: [request({ answered_at: NOW - 10 })],
      }),
    )
    // The delivery row itself is never rewritten — it just stops being drawn as
    // a live question.
    expect(kinds(records)).toEqual(['job'])
    expect(records[0]!.glyph).toBe('agent')
  })

  it('shows a request from an interactive session that has no delivery at all', () => {
    // The chat case: request_human_attention called with no subscription and no
    // delivery anywhere. The Desk cannot see this; the rail must.
    const records = buildActivity(
      input({ attentionRequests: [request({ id: 'a-chat', session_id: 'sess-chat' })] }),
    )
    expect(ids(records)).toEqual(['ask:request:a-chat'])
    expect(records[0]!.glyph).toBe('attention')
    expect(records[0]!.sessionId).toBe('sess-chat')
  })

  it('does not double-list a request that a parked delivery already carries', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ status: 'awaiting_human' })],
        attentionRequests: [request()],
      }),
    )
    expect(records.filter((r) => r.kind === 'ask')).toHaveLength(1)
  })
})

describe('changes, refusals and halted schedules', () => {
  it('reads a rewrite as a sentence with its rationale and diff', () => {
    const records = buildActivity(
      input({
        configEvents: [
          configEvent({ id: 'c0', created_at: NOW_MS - 2000, payload: { name: 'email-answerer', prompt: 'Old.' } }),
          configEvent({ id: 'c1', created_at: NOW_MS }),
        ],
      }),
    )
    const rewrite = records.find((r) => r.id === 'change:c1')!
    expect(rewrite.headline).toBe('email-reviewer rewrote email-answerer')
    expect(rewrite.glyph).toBe('agent')
    expect(rewrite.detail).toBe('narrowing yesterday’s rule')
    expect(rewrite.meta).toMatch(/^\+\d+ −\d+ lines$/)
    expect(rewrite.entry).not.toBeNull()
  })

  it('marks a human edit hollow and says so when no reason was given', () => {
    const records = buildActivity(
      input({
        configEvents: [configEvent({ actor_worker: '', actor_session: '', rationale: '' })],
      }),
    )
    expect(records[0]!.glyph).toBe('human')
    expect(records[0]!.headline).toBe('you rewrote email-answerer')
    expect(records[0]!.detail).toBe('(no reason given)')
    expect(records[0]!.detailIsQuote).toBe(false)
  })

  it('gives a freeze refusal its own glyph and does not also list it as an event', () => {
    const records = buildActivity(
      input({
        events: [
          projectEvent({
            id: 'e-freeze',
            type: 'worker.freeze_refused',
            text: 'Refused worker_prompt_write against frozen worker "fee-scorer".',
            envelope: {
              depth: 0,
              source: 'core',
              worker: 'tuner',
              session_id: 'sess-9',
              interactive: false,
              attention_requested: false,
            },
          }),
        ],
      }),
    )
    expect(records).toHaveLength(1)
    expect(records[0]!.kind).toBe('freeze-refusal')
    expect(records[0]!.glyph).toBe('freeze')
    expect(records[0]!.headline).toBe('fee-scorer refused a rewrite from tuner')
  })

  it('reports a five-strike schedule with the reason the row actually carries', () => {
    const records = buildActivity(input({ schedules: [schedule()] }))
    expect(records[0]!.kind).toBe('schedule-halted')
    expect(records[0]!.glyph).toBe('failure')
    expect(records[0]!.detail).toContain('toolbox:9')
  })

  it('leaves a schedule below the strike threshold alone', () => {
    const records = buildActivity(input({ schedules: [schedule({ provision_failures: 4 })] }))
    expect(records).toEqual([])
  })

  it('says so when a failed delivery records no reason', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ status: 'failed', failure_reason: '' })],
      }),
    )
    expect(records[0]!.detail).toContain('No reason is recorded')
  })
})

describe('lenses subset the rail', () => {
  const populated = (): Partial<BuildActivityInput> => ({
    events: [projectEvent(), projectEvent({ id: 'e-lonely', delivered: false })],
    deliveries: [delivery()],
    configEvents: [configEvent()],
    schedules: [schedule()],
  })

  it('every kind has a home, and the homes are the chips', () => {
    for (const home of Object.values(ACTIVITY_HOME)) {
      expect(ACTIVITY_LENSES).toContain(home)
    }
    expect(ACTIVITY_LENSES[0]).toBe('all')
  })

  it('all shows everything', () => {
    expect(buildActivity(input({ ...populated(), lens: 'all' }))).toHaveLength(4)
  })

  it('jobs shows work — that ran, and that failed to start', () => {
    const records = buildActivity(input({ ...populated(), lens: 'jobs' }))
    expect(kinds(records).sort()).toEqual(['job', 'schedule-halted'])
  })

  it('events shows what arrived, changes shows configuration', () => {
    expect(kinds(buildActivity(input({ ...populated(), lens: 'events' })))).toEqual(['event'])
    expect(kinds(buildActivity(input({ ...populated(), lens: 'changes' })))).toEqual(['change'])
  })
})

describe('gaps and the time window', () => {
  it('marks a quiet stretch above the row that follows it', () => {
    const gapSeconds = ACTIVITY_GAP_THRESHOLD_MS / 1000 + 60
    const records = buildActivity(
      input({
        events: [projectEvent({ id: 'e1' }), projectEvent({ id: 'e2' })],
        deliveries: [
          delivery({ id: 'd-new', event_id: 'e1', created_at: NOW - 10 }),
          delivery({ id: 'd-old', event_id: 'e2', created_at: NOW - 10 - gapSeconds }),
        ],
      }),
    )
    expect(records[0]!.gapBeforeLabel).toMatch(/of nothing$/)
    expect(records[1]!.gapBeforeLabel).toBe('')
  })

  it('does not invent a gap above a record whose time is unknown', () => {
    const records = buildActivity(
      input({
        events: [projectEvent({ id: 'e1' }), projectEvent({ id: 'e2' })],
        deliveries: [
          delivery({ id: 'd-new', event_id: 'e1', created_at: NOW - 10 }),
          delivery({ id: 'd-none', event_id: 'e2', created_at: 0, started_at: 0, ended_at: 0 }),
        ],
      }),
    )
    // e2 supplies no time either, so the row lands at 0 — and nothing above it
    // claims fifty years of silence.
    expect(records.every((r) => r.gapBeforeLabel === '')).toBe(true)
  })

  it('recomputes gaps for the filtered list, not the whole one', () => {
    const gapSeconds = ACTIVITY_GAP_THRESHOLD_MS / 1000 + 60
    const shared: Partial<BuildActivityInput> = {
      events: [projectEvent({ id: 'e1' }), projectEvent({ id: 'e2' })],
      deliveries: [
        delivery({ id: 'd-new', event_id: 'e1', created_at: NOW - 10 }),
        delivery({ id: 'd-old', event_id: 'e2', created_at: NOW - 10 - gapSeconds }),
      ],
      // A change sits in the middle of the hole: with changes visible the two
      // jobs are not four hours apart from each other's point of view — but a
      // reader filtering to jobs alone genuinely is looking at a quiet stretch.
      configEvents: [configEvent({ created_at: (NOW - 10 - gapSeconds / 2) * 1000 })],
    }
    const all = buildActivity(input({ ...shared, lens: 'all' }))
    const jobsOnly = buildActivity(input({ ...shared, lens: 'jobs' }))

    expect(all.some((r) => r.gapBeforeLabel !== '')).toBe(false)
    expect(jobsOnly[0]!.gapBeforeLabel).toMatch(/of nothing$/)
  })

  it('drops records below the window floor', () => {
    const records = buildActivity(
      input({
        events: [projectEvent({ id: 'e1' }), projectEvent({ id: 'e2' })],
        deliveries: [
          delivery({ id: 'd-new', event_id: 'e1', created_at: NOW - 10 }),
          delivery({ id: 'd-old', event_id: 'e2', created_at: NOW - 10_000 }),
        ],
        windowStartMs: (NOW - 100) * 1000,
      }),
    )
    expect(ids(records)).toEqual(['job:d-new'])
  })

  it('reports the oldest moment on screen, for "Show earlier"', () => {
    const records = buildActivity(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ created_at: NOW - 60 })],
        configEvents: [configEvent({ created_at: (NOW - 600) * 1000 })],
      }),
    )
    expect(oldestShownMs(records)).toBe((NOW - 600) * 1000)
    expect(oldestShownMs([])).toBe(0)
  })
})

describe('ordering is deterministic', () => {
  it('breaks same-moment ties on id rather than input order', () => {
    const a = buildActivity(
      input({
        events: [projectEvent({ id: 'e1' }), projectEvent({ id: 'e2' })],
        deliveries: [
          delivery({ id: 'd-b', event_id: 'e1', created_at: NOW - 5 }),
          delivery({ id: 'd-a', event_id: 'e2', created_at: NOW - 5 }),
        ],
      }),
    )
    const b = buildActivity(
      input({
        events: [projectEvent({ id: 'e2' }), projectEvent({ id: 'e1' })],
        deliveries: [
          delivery({ id: 'd-a', event_id: 'e2', created_at: NOW - 5 }),
          delivery({ id: 'd-b', event_id: 'e1', created_at: NOW - 5 }),
        ],
      }),
    )
    expect(ids(a)).toEqual(['job:d-a', 'job:d-b'])
    expect(ids(a)).toEqual(ids(b))
  })

  it('is empty for an empty project rather than throwing', () => {
    expect(buildActivity(input())).toEqual([])
  })
})
