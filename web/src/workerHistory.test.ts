// A2: one worker's runs and rewrites on the same rail
// (docs/product/28-console-ia-design.md §2.3).
//
// The case that carries the whole argument for the merge is
// "a rewrite lands between the run before it and the run after it" — that
// adjacency is what BeforeAfterView currently re-joins by hand.

import { describe, it, expect } from 'vitest'
import {
  buildWorkerHistory,
  runsAround,
  type BuildWorkerHistoryInput,
  type WorkerJobRow,
} from './workerHistory.js'
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

const job = (over: Partial<WorkerJobRow> = {}): WorkerJobRow => ({
  id: 'sess-1',
  created_at: NOW - 3600,
  title: '',
  status: 'ok',
  worker: WORKER,
  ...over,
})

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
  jobs: [],
  deliveries: [],
  events: [],
  subscriptions: [],
  configEvents: [],
  nowMs: NOW_MS,
  projectId: 'acme',
  ...over,
})

describe('the merge — runs and rewrites on one rail', () => {
  const withRunsAndRewrites = (): Partial<BuildWorkerHistoryInput> => ({
    jobs: [
      job({ id: 'sess-before', created_at: NOW - 600 }),
      job({ id: 'sess-after', created_at: NOW - 60 }),
    ],
    configEvents: [
      promptWrite({ id: 'c0', created_at: (NOW - 9000) * 1000, payload: { name: WORKER, prompt: 'Old.' } }),
      promptWrite({ id: 'c1', created_at: (NOW - 300) * 1000 }),
    ],
  })

  it('puts a rewrite between the run before it and the run after it', () => {
    const history = buildWorkerHistory(input(withRunsAndRewrites()))
    expect(history.records.map((r) => r.id)).toEqual([
      'job:sess-after',
      'change:c1',
      'job:sess-before',
      'change:c0',
    ])
  })

  it('reads the runs either side of a version straight off the rail', () => {
    const history = buildWorkerHistory(input(withRunsAndRewrites()))
    const { before, after } = runsAround(history.records, 'change:c1')
    expect(before?.id).toBe('job:sess-before')
    expect(after?.id).toBe('job:sess-after')
  })

  it('says nothing rather than inventing a run that has not happened yet', () => {
    const history = buildWorkerHistory(input({ configEvents: [promptWrite()] }))
    const { before, after } = runsAround(history.records, 'change:c1')
    expect(before).toBeNull()
    expect(after).toBeNull()
  })

  it('returns nulls for a change that is not on this rail', () => {
    const history = buildWorkerHistory(input({ configEvents: [promptWrite()] }))
    expect(runsAround(history.records, 'change:nope')).toEqual({ before: null, after: null })
  })
})

describe('runs come from the session route, deliveries only enrich', () => {
  it('shows a run that has no delivery at all — started by hand or by a clock', () => {
    // The reason sessions are the source: a scheduled or hand-started run has
    // no event and may have no delivery in the fetched window, and it is still
    // something this worker did.
    const history = buildWorkerHistory(input({ jobs: [job({ id: 'sess-manual' })] }))
    expect(history.records.map((r) => r.id)).toEqual(['job:sess-manual'])
    expect(history.jobs).toBe(1)
  })

  it('names the event and the duration when a delivery is there to say so', () => {
    const history = buildWorkerHistory(
      input({
        jobs: [job({ id: 'sess-1' })],
        deliveries: [delivery({ session_id: 'sess-1' })],
        events: [projectEvent()],
      }),
    )
    expect(history.records[0]!.headline).toBe('email.received woke email-answerer')
    expect(history.records[0]!.meta).toBe('ran 41s')
  })

  it('still lists the run when the delivery window does not reach it', () => {
    // A busy project's recent-deliveries page may hold none of this worker's
    // rows. Before sessions were the source, this run vanished.
    const history = buildWorkerHistory(
      input({
        jobs: [job({ id: 'sess-old', created_at: NOW - 90_000, title: 'the nightly sweep' })],
        deliveries: [delivery({ session_id: 'sess-someone-else' })],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['job:sess-old'])
    expect(history.records[0]!.headline).toBe('email-answerer ran a job')
    // The title rides on the record, for the permalink's text — printing it in
    // the headline as well would be the duplicated label again.
    expect(history.records[0]!.title).toBe('the nightly sweep')
  })

  it('keeps another worker’s run off this rail', () => {
    const history = buildWorkerHistory(
      input({
        jobs: [job({ id: 'sess-mine' }), job({ id: 'sess-theirs', worker: 'archivist' })],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['job:sess-mine'])
  })

  it('keeps a run that names nobody — an unlabelled row is still ours', () => {
    const history = buildWorkerHistory(input({ jobs: [job({ id: 'sess-plain', worker: '' })] }))
    expect(history.records.map((r) => r.id)).toEqual(['job:sess-plain'])
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
    expect(history.records.map((r) => r.version)).toEqual([3, 2, 1])
    // The number is carried by `version`, not printed into `meta` — the row
    // renders it once, as a chip (doc 21 X4: no duplicated labels).
    expect(history.records[0]!.meta).not.toContain('v3')
  })

  it('passes the lineage through so the banner and the counts cannot disagree', () => {
    const history = buildWorkerHistory(
      input({ configEvents: [promptWrite({ id: 'c1' })] }),
    )
    expect(history.lineage.versions).toBe(history.versions)
    expect(history.lineage.workerName).toBe(WORKER)
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

  it('says so when a failed run records no reason', () => {
    const history = buildWorkerHistory(
      input({
        jobs: [job({ id: 'sess-1' })],
        deliveries: [delivery({ session_id: 'sess-1', status: 'failed', failure_reason: '' })],
        events: [projectEvent()],
      }),
    )
    expect(history.records[0]!.glyph).toBe('failure')
    expect(history.records[0]!.detail).toContain('No reason is recorded')
  })

  it('draws a parked run as waiting, not as a completed one', () => {
    const history = buildWorkerHistory(
      input({
        jobs: [job({ id: 'sess-1' })],
        deliveries: [delivery({ session_id: 'sess-1', status: 'awaiting_human', ended_at: 0 })],
        events: [projectEvent()],
      }),
    )
    expect(history.records[0]!.kind).toBe('ask')
    expect(history.records[0]!.glyph).toBe('attention')
  })
})

describe('J1 holds here too', () => {
  it('sorts a millisecond-stamped rewrite against a second-stamped run correctly', () => {
    const history = buildWorkerHistory(
      input({
        jobs: [job({ id: 'sess-1', created_at: NOW - 10 })],
        configEvents: [promptWrite({ id: 'c1', created_at: (NOW - 9) * 1000 })],
      }),
    )
    expect(history.records.map((r) => r.id)).toEqual(['change:c1', 'job:sess-1'])
  })

  it('is empty for a worker with no history rather than throwing', () => {
    expect(buildWorkerHistory(input()).records).toEqual([])
  })
})
