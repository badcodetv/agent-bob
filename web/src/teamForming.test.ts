import { describe, expect, it } from 'vitest'
import { describeFormingTool, splitCycleByRecentRuns, summariseFormingSteps } from './teamForming.js'
import { coerceDelivery, coerceProjectEvent } from './events.js'
import { coerceSchedule } from './schedules.js'
import { coerceWorker } from './workers.js'
import {
  describeRunOutcome,
  firstLine,
  planCycle,
  recentActivity,
  scheduleRunEndpoint,
  summariseTeamForming,
} from './teamForming.js'

const worker = (name: string, extra: Record<string, unknown> = {}) =>
  coerceWorker({ name, enabled: true, created_at: 100, ...extra })
const job = (w: string, status: string, created_at = 1000, extra: Record<string, unknown> = {}) =>
  coerceDelivery({ id: `${w}-${status}-${created_at}`, worker: w, status, created_at, ...extra })

describe('firstLine', () => {
  it('skips blank lines and heading marks, and caps long lines', () => {
    expect(firstLine('\n\n# Scout\nfinds leads')).toBe('Scout')
    expect(firstLine('')).toBe('')
    const long = 'x'.repeat(200)
    expect(firstLine(long, 20)).toHaveLength(20)
    expect(firstLine(long, 20).endsWith('…')).toBe(true)
  })
})

describe('summariseTeamForming', () => {
  const cases: Array<{ name: string; deliveries: ReturnType<typeof job>[]; phase: string }> = [
    { name: 'no architect job yet is starting', deliveries: [], phase: 'starting' },
    { name: 'a queued architect job is designing', deliveries: [job('architect', 'pending')], phase: 'designing' },
    { name: 'a running architect job is designing', deliveries: [job('architect', 'running')], phase: 'designing' },
    { name: 'a finished architect job is ready', deliveries: [job('architect', 'ok')], phase: 'ready' },
    // request_human_attention parks the job: the architect has said what it built.
    { name: 'a parked architect job is ready', deliveries: [job('architect', 'awaiting_human')], phase: 'ready' },
    { name: 'a failed architect job is failed', deliveries: [job('architect', 'failed')], phase: 'failed' },
    {
      name: 'the newest architect job decides',
      deliveries: [job('architect', 'ok', 900), job('architect', 'running', 1100)],
      phase: 'designing',
    },
  ]
  for (const tc of cases) {
    it(tc.name, () => {
      const s = summariseTeamForming({ workers: [worker('architect')], deliveries: tc.deliveries })
      expect(s.phase).toBe(tc.phase)
    })
  }

  it('ignores architect jobs from before the approval', () => {
    const s = summariseTeamForming({
      workers: [worker('architect')],
      deliveries: [job('architect', 'ok', 500)],
      sinceSeconds: 1000,
    })
    expect(s.phase).toBe('starting')
  })

  it('honours a custom architect name', () => {
    const s = summariseTeamForming({
      workers: [worker('chief'), worker('architect')],
      deliveries: [job('chief', 'running')],
      architectName: 'chief',
    })
    expect(s.phase).toBe('designing')
    expect(s.architect?.name).toBe('chief')
    // A worker merely NAMED architect is just a member when the charter chose another name.
    expect(s.members.map((m) => m.name)).toEqual(['architect'])
  })

  it('lists the team in hiring order, without the architect or the interviewer, with live status', () => {
    const s = summariseTeamForming({
      workers: [
        worker('writer', { created_at: 300, system_prompt: 'You write the newsletter.\nMore.' }),
        worker('interviewer', { created_at: 1 }),
        worker('architect', { created_at: 2 }),
        worker('scout', { created_at: 200, description: 'Finds leads', system_prompt: 'ignored' }),
      ],
      deliveries: [job('writer', 'running', 1200, { started_at: 1210 }), job('scout', 'ok', 1100)],
    })
    expect(s.members.map((m) => m.name)).toEqual(['scout', 'writer'])
    expect(s.members[0]).toMatchObject({ line: 'Finds leads', status: 'finished', lastRunAt: 1100 })
    expect(s.members[1]).toMatchObject({ line: 'You write the newsletter.', status: 'running', lastRunAt: 1210 })
  })

  it('a worker that never ran is idle', () => {
    const s = summariseTeamForming({ workers: [worker('architect'), worker('scout')], deliveries: [] })
    expect(s.members[0].status).toBe('idle')
    expect(s.members[0].lastRunAt).toBe(0)
  })
})

describe('recentActivity', () => {
  it('drops config churn, sorts newest first and caps', () => {
    const events = [
      coerceProjectEvent({ id: 'a', type: 'worker.finished', occurred_at: 10 }),
      coerceProjectEvent({ id: 'b', type: 'config.changed', occurred_at: 30 }),
      coerceProjectEvent({ id: 'c', type: 'schedule.fired', occurred_at: 20 }),
    ]
    expect(recentActivity(events).map((e) => e.id)).toEqual(['c', 'a'])
    expect(recentActivity(events, 1).map((e) => e.id)).toEqual(['c'])
  })
})

describe('planCycle', () => {
  it('fires every enabled schedule and never a disabled one', () => {
    const plan = planCycle([
      coerceSchedule({ id: 's2', worker: 'writer', enabled: true }),
      coerceSchedule({ id: 's1', worker: 'scout', enabled: false }),
      coerceSchedule({ id: 's3', worker: 'architect', enabled: true }),
    ])
    expect(plan.map((s) => s.id)).toEqual(['s3', 's2'])
  })

  it('names the route per schedule', () => {
    expect(scheduleRunEndpoint('a b')).toBe('/agent/schedules/a%20b/run')
  })
})

describe('describeRunOutcome', () => {
  it('puts each engine outcome in words', () => {
    expect(describeRunOutcome('scout', 'requested')).toEqual({ target: 'scout', ok: true, text: 'scout — started' })
    expect(describeRunOutcome('scout', 'already_fired').ok).toBe(true)
    expect(describeRunOutcome('scout', 'target_missing').ok).toBe(false)
    expect(describeRunOutcome('scout', 'error', 'schedule not found').text).toBe('scout — schedule not found')
  })
})

describe('summariseFormingSteps', () => {
  const start = (id: string, toolName: string, input: Record<string, unknown> = {}) => ({
    type: 'tool_use_start',
    data: { toolCallId: id, toolName, input },
  })
  const run = (...evs: unknown[]) => ({ events: [{ query_id: 'q', events: evs }] })

  it('is one starting step before the job exists', () => {
    expect(summariseFormingSteps({ phase: 'starting', events: null })).toEqual([
      { key: 'starting', text: 'Starting the architect', current: true },
    ])
  })

  it('reads the goal before the first tool call', () => {
    const steps = summariseFormingSteps({ phase: 'designing', events: run() })
    expect(steps.map((s) => s.text)).toEqual(['Starting the architect', 'Reading your goal and the charter'])
    expect(steps.map((s) => s.current)).toEqual([false, true])
  })

  it('names each tool call in words, collapses repeats, skips validation, and keeps the last few', () => {
    const steps = summariseFormingSteps({
      phase: 'designing',
      limit: 4,
      events: run(
        start('a', 'mcp__agentkit-core__memory_current'),
        start('b', 'mcp__agentkit-core__memory_search'),
        start('c', 'mcp__agentkit-core__charter_validate'),
        start('d', 'mcp__agentkit-core__worker_create', { name: 'scribe' }),
        start('e', 'mcp__agentkit-core__schedule_create', { worker: 'scribe', cron: '0 9 * * *' }),
        start('f', 'mcp__agentkit-core__subscription_create', { worker: 'scribe', event_type: 'worker.finished' }),
      ),
    })
    expect(steps.map((s) => s.text)).toEqual([
      'Reading what the project has written down',
      'Creating scribe',
      expect.stringMatching(/^Putting scribe on a schedule — /),
      'Wiring scribe to wake on worker.finished',
    ])
    expect(steps.filter((s) => s.current).map((s) => s.key)).toEqual(['f'])
  })

  it('marks nothing current once the run has settled', () => {
    const steps = summariseFormingSteps({ phase: 'ready', events: run(start('a', 'worker_list')) })
    expect(steps.some((s) => s.current)).toBe(false)
  })
})

describe('describeFormingTool', () => {
  it.each([
    ['mcp__agentkit-core__worker_prompt_write', { name: 'copywriter' }, "Writing copywriter's instructions"],
    ['mcp__agentkit-core__memory_create', { labels: { kind: 'architect-verdict' } }, 'Writing a note down: architect verdict'],
    ['mcp__agentkit-core__request_human_attention', {}, 'Writing you a note about what it did'],
    ['mcp__agentkit-core__config_history', {}, 'Checking what changed last time'],
    ['mcp__agentkit-core__charter_validate', {}, ''],
  ])('%s', (tool, input, want) => {
    expect(describeFormingTool(tool, input)).toBe(want)
  })
})

describe('splitCycleByRecentRuns', () => {
  const NOW = 10_000
  const plan = [
    coerceSchedule({ id: 'arch', worker: 'architect', cron: '0 9 * * *', enabled: true }),
    coerceSchedule({ id: 'clerk', worker: 'numbers-clerk', cron: '0 17 * * 0', enabled: true }),
    coerceSchedule({ id: 'copy', worker: 'copywriter', cron: '30 9 * * 2', enabled: true }),
    coerceSchedule({ id: 'review', worker: 'weekly-review', cron: '0 7 * * 1', enabled: true }),
    coerceSchedule({ id: 'sess', worker: '', target_session: 'desk-bot', cron: '0 8 * * *', enabled: true }),
  ]
  const deliveries = [
    coerceDelivery({ id: 'd1', worker: 'architect', status: 'ok', started_at: NOW - 400, ended_at: NOW - 240 }),
    coerceDelivery({ id: 'd2', worker: 'numbers-clerk', status: 'awaiting_human', started_at: NOW - 20, ended_at: 0 }),
    coerceDelivery({ id: 'd3', worker: 'copywriter', status: 'ok', started_at: NOW - 7200, ended_at: NOW - 3600 }),
    coerceDelivery({ id: 'd4', worker: 'weekly-review', status: 'running', started_at: NOW - 60 }),
  ]

  it('skips a worker that ran in the last 30 minutes or is running now, and says why', () => {
    const { run, skipped } = splitCycleByRecentRuns(plan, deliveries, NOW)
    expect(run.map((s) => s.id)).toEqual(['copy', 'sess'])
    expect(skipped.map((s) => [s.schedule.id, s.reason])).toEqual([
      ['arch', 'ran 4 min ago — skipped'],
      ['clerk', 'ran just now — skipped'],
      ['review', 'running now — skipped'],
    ])
  })

  it('a failed last run is not a reason to skip', () => {
    const failed = [coerceDelivery({ id: 'f', worker: 'architect', status: 'failed', started_at: NOW - 60, ended_at: NOW - 30 })]
    expect(splitCycleByRecentRuns(plan.slice(0, 1), failed, NOW).run).toHaveLength(1)
  })
})
