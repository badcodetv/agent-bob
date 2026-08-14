// A2: one worker's runs and rewrites on the same rail
// (docs/product/28-console-ia-design.md §2.3).
//
// The case that carries the whole argument for the merge is
// "a rewrite lands between the run before it and the run after it" — that
// adjacency is what BeforeAfterView currently re-joins by hand.

import { describe, it, expect } from 'vitest'
import { buildWorkerHistory, runsAround, type BuildWorkerHistoryInput } from './workerHistory.js'
import { coerceConfigEvent, type ConfigEvent } from './configLog.js'
import {
  coerceDelivery,
  coerceProjectEvent,
  coerceSubscription,
  type EventDelivery,
  type ProjectEvent,
  type Subscription,
} from './events.js'

const NOW = 1_789_000_000
const NOW_MS = NOW * 1000
const WORKER = 'email-answerer'

const delivery = (over: Partial<EventDelivery> = {}): EventDelivery =>
  coerceDelivery({
    id: 'd1',
    project: 'acme',
    event_id: 'e1',
    subscription_id: 's1',
    session_id: 'sess-1',
    worker: WORKER,
    status: 'ok',
    started_at: NOW - 3600,
    ended_at: NOW - 3559,
    created_at: NOW - 3600,
    updated_at: NOW - 3559,
    ...over,
  })

const projectEvent = (over: Partial<ProjectEvent> = {}): ProjectEvent =>
  coerceProjectEvent({
    id: 'e1',
    project: 'acme',
    type: 'email.received',
    text: 'mail',
    envelope: {
      depth: 0,
      source: 'core',
      worker: '',
      session_id: '',
      interactive: false,
      attention_requested: false,
    },
    occurred_at: NOW - 3600,
    created_at: NOW - 3600,
    delivered: true,
    ...over,
  })

const promptWrite = (over: Partial<ConfigEvent> = {}): ConfigEvent =>
  coerceConfigEvent({
    id: 'c1',
    project: 'acme',
    actor_worker: 'email-reviewer',
    actor_session: 'sess-5',
    action: 'worker_prompt_write',
    payload: { name: WORKER, prompt: 'Be brief.' },
    rationale: 'answers kept omitting the ticket reference',
    created_at: NOW_MS,
    ...over,
  })

const input = (over: Partial<BuildWorkerHistoryInput> = {}): BuildWorkerHistoryInput => ({
  workerName: WORKER,
  deliveries: [],
  events: [],
  subscriptions: [],
  configEvents: [],
  nowMs: NOW_MS,
  projectId: 'acme',
  ...over,
})

describe('the merge — runs and rewrites on one rail', () => {
  it('puts a rewrite between the run before it and the run after it', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [
          delivery({ id: 'd-before', created_at: NOW - 600 }),
          delivery({ id: 'd-after', created_at: NOW - 60 }),
        ],
        configEvents: [
          promptWrite({ id: 'c0', created_at: (NOW - 9000) * 1000, payload: { name: WORKER, prompt: 'Old.' } }),
          promptWrite({ id: 'c1', created_at: (NOW - 300) * 1000 }),
        ],
      }),
    )

    expect(history.records.map((r) => r.id)).toEqual([
      'job:d-after',
      'change:c1',
      'job:d-before',
      'change:c0',
    ])
  })

  it('reads the runs either side of a version straight off the rail', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [
          delivery({ id: 'd-before', created_at: NOW - 600 }),
          delivery({ id: 'd-after', created_at: NOW - 60 }),
        ],
        configEvents: [
          promptWrite({ id: 'c0', created_at: (NOW - 9000) * 1000, payload: { name: WORKER, prompt: 'Old.' } }),
          promptWrite({ id: 'c1', created_at: (NOW - 300) * 1000 }),
        ],
      }),
    )

    const { before, after } = runsAround(history.records, 'change:c1')
    expect(before?.id).toBe('job:d-before')
    expect(after?.id).toBe('job:d-after')
  })

  it('says nothing rather than inventing a run that has not happened yet', () => {
    const history = buildWorkerHistory(
      input({ configEvents: [promptWrite()] }),
    )
    const { before, after } = runsAround(history.records, 'change:c1')
    expect(before).toBeNull()
    expect(after).toBeNull()
  })

  it('returns nulls for a change that is not on this rail', () => {
    const history = buildWorkerHistory(input({ configEvents: [promptWrite()] }))
    expect(runsAround(history.records, 'change:nope')).toEqual({ before: null, after: null })
  })
})

describe('scoping', () => {
  it('keeps another worker’s job off this rail even if the caller over-fetches', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ id: 'd-mine' }), delivery({ id: 'd-theirs', worker: 'archivist' })],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['job:d-mine'])
    expect(history.jobs).toBe(1)
  })

  it('falls back to the subscription join for rows written before migration 024', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ id: 'd-old', worker: '' })],
        subscriptions: [
          coerceSubscription({
            id: 's1',
            project: 'acme',
            event_type: 'email.received',
            filter: {},
            worker: WORKER,
            max_firings_per_hour: 0,
            enabled: true,
            created_at: 0,
            updated_at: 0,
          }) as Subscription,
        ],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['job:d-old'])
  })

  it('ignores another worker’s config events', () => {
    const history = buildWorkerHistory(
      input({
        configEvents: [
          promptWrite({ id: 'c-mine' }),
          promptWrite({ id: 'c-theirs', payload: { name: 'archivist', prompt: 'x' } }),
        ],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['change:c-mine'])
  })
})

describe('versions and counts come from the one counter', () => {
  it('numbers versions and reports the lineage counts', () => {
    const history = buildWorkerHistory(
      input({
        configEvents: [
          promptWrite({ id: 'c1', created_at: NOW_MS - 3000, payload: { name: WORKER, prompt: 'One.' } }),
          promptWrite({ id: 'c2', created_at: NOW_MS - 2000, payload: { name: WORKER, prompt: 'Two.' } }),
          promptWrite({ id: 'c3', created_at: NOW_MS - 1000, payload: { name: WORKER, prompt: 'Three.' } }),
        ],
      }),
    )
    expect(history.versions).toBe(3)
    expect(history.rewrites).toBe(2)
    // Newest first, so v3 leads.
    expect(history.records.map((r) => r.version)).toEqual([3, 2, 1])
    expect(history.records[0]!.meta).toContain('v3')
  })

  it('marks churn — a rewrite whose text is byte-identical to the one before', () => {
    const history = buildWorkerHistory(
      input({
        configEvents: [
          promptWrite({ id: 'c1', created_at: NOW_MS - 2000, payload: { name: WORKER, prompt: 'Same.' } }),
          promptWrite({ id: 'c2', created_at: NOW_MS - 1000, payload: { name: WORKER, prompt: 'Same.' } }),
        ],
      }),
    )
    const newest = history.records[0]!
    expect(newest.duplicate).toBe(true)
    expect(newest.meta).toContain('no change to the text')
  })

  it('carries the prompt so a version can be restored', () => {
    const history = buildWorkerHistory(input({ configEvents: [promptWrite()] }))
    expect(history.records[0]!.prompt).toBe('Be brief.')
  })
})

describe('authorship and honesty', () => {
  it('marks a worker’s rewrite ember and a human’s hollow', () => {
    const history = buildWorkerHistory(
      input({
        configEvents: [
          promptWrite({ id: 'c-agent', created_at: NOW_MS - 1000 }),
          promptWrite({ id: 'c-human', actor_worker: '', actor_session: '', rationale: '' }),
        ],
      }),
    )
    const byId = new Map(history.records.map((r) => [r.id, r]))
    expect(byId.get('change:c-agent')!.glyph).toBe('agent')
    expect(byId.get('change:c-human')!.glyph).toBe('human')
    expect(byId.get('change:c-human')!.detail).toBe('(no reason given)')
  })

  it('says so when a failed job records no reason', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ status: 'failed', failure_reason: '' })],
      }),
    )
    expect(history.records[0]!.glyph).toBe('failure')
    expect(history.records[0]!.detail).toContain('No reason is recorded')
  })

  it('draws a parked job as waiting, not as a completed run', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ status: 'awaiting_human', ended_at: 0 })],
      }),
    )
    expect(history.records[0]!.kind).toBe('ask')
    expect(history.records[0]!.glyph).toBe('attention')
  })
})

describe('J1 holds here too', () => {
  it('sorts a millisecond-stamped rewrite against a second-stamped job correctly', () => {
    const history = buildWorkerHistory(
      input({
        events: [projectEvent()],
        deliveries: [delivery({ id: 'd1', created_at: NOW - 10 })],
        configEvents: [promptWrite({ id: 'c1', created_at: (NOW - 9) * 1000 })],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['change:c1', 'job:d1'])
  })

  it('is empty for a worker with no history rather than throwing', () => {
    expect(buildWorkerHistory(input()).records).toEqual([])
  })
})
