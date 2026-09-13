// DK1: the Desk fold — asks, changes and trouble, computed from the same data
// the rest of the product layer already serves (design §5.2).

import { describe, it, expect } from 'vitest'
import {
  ATTENTION_ENDPOINTS,
  buildDesk,
  countAsks,
  coerceAttentionRequest,
  DESK_ASKS_CAVEAT,
  DESK_FREEZE_REFUSAL_NOTE,
  DESK_GLYPHS,
  DESK_NO_DELIVERY_REASON,
  deskChangeSubject,
  deskChangeVerb,
  deskFirstKindForChange,
  looksLikeRevertRationale,
  frozenTargetFromText,
  isAttentionRequestOpen,
  SCHEDULE_MAX_PROVISION_FAILURES,
  type AttentionRequest,
  type BuildDeskInput,
  deskNotes,
} from './desk.js'
import type { MemoryRow } from './memories.js'
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
    status: 'awaiting_human',
    started_at: NOW - 9600, // 2h 40m
    ended_at: 0,
    created_at: NOW - 9600,
    updated_at: NOW - 9600,
    ...over,
  })

const subscription = (over: Partial<Subscription> = {}): Subscription =>
  coerceSubscription({
    id: 's1',
    project: 'acme',
    event_type: 'email.received',
    filter: {},
    worker: 'email-answerer',
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
    actor_worker: '',
    actor_session: '',
    action: 'worker_update',
    payload: { name: 'email-answerer' },
    rationale: '',
    created_at: NOW_MS,
    ...over,
  })

const projectEvent = (over: Partial<ProjectEvent> = {}): ProjectEvent =>
  coerceProjectEvent({
    id: 'pe1',
    project: 'acme',
    type: 'worker.freeze_refused',
    text: 'Refused worker_prompt_write against frozen worker "fee-scorer". Attempted by worker "tuner".',
    envelope: {
      depth: 0,
      source: 'core',
      worker: 'tuner',
      session_id: 'sess-9',
      interactive: false,
      attention_requested: false,
    },
    occurred_at: NOW - 3600,
    created_at: NOW - 3600,
    delivered: false,
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

const input = (over: Partial<BuildDeskInput> = {}): BuildDeskInput => ({
  deliveries: [],
  events: [],
  subscriptions: [],
  configEvents: [],
  attentionRequests: [],
  nowSeconds: NOW,
  lastSeenMs: 0,
  ...over,
})

describe('the wire shapes', () => {
  it('names the read route the Asks stack needs', () => {
    expect(ATTENTION_ENDPOINTS.list).toBe('/agent/attention-requests')
  })

  it('never binds undefined, and defaults every scalar', () => {
    const r = coerceAttentionRequest(null)
    expect(r).toEqual({
      id: '',
      project: '',
      session_id: '',
      worker: '',
      message: '',
      kind: 'ask',
      session_url: '',
      channel: '',
      delivered: false,
      expires_at: 0,
      created_at: 0,
      answered_at: 0,
      timed_out_at: 0,
    })
    expect(coerceAttentionRequest({ expires_at: 'soon' }).expires_at).toBe(0)
  })

  it('is open only while nobody has answered and the sweep has not timed it out', () => {
    expect(isAttentionRequestOpen(request())).toBe(true)
    expect(isAttentionRequestOpen(request({ answered_at: NOW }))).toBe(false)
    expect(isAttentionRequestOpen(request({ timed_out_at: NOW }))).toBe(false)
  })

  it('keeps the glyph set closed', () => {
    expect(DESK_GLYPHS).toEqual(['agent', 'human', 'attention', 'failure', 'freeze'])
  })
})

// ---------------------------------------------------------------------------

describe('countAsks — the badge\u2019s number (doc 21, X7)', () => {
  it('counts what the Asks stack lists, not the open requests', () => {
    const deliveries = [delivery(), delivery({ id: 'd2', session_id: 'sess-2', status: 'ok' })]
    const requests = [request(), request({ id: 'a2', session_id: 'sess-2' })]
    // Two open requests, one parked delivery: the badge says one.
    expect(requests.filter(isAttentionRequestOpen)).toHaveLength(2)
    expect(countAsks(deliveries, requests)).toBe(1)
    expect(
      buildDesk(input({ deliveries, subscriptions: [subscription()], attentionRequests: requests }))
        .asks,
    ).toHaveLength(1)
  })

  it('drops a parked delivery whose request has been answered', () => {
    expect(countAsks([delivery()], [request({ answered_at: NOW })])).toBe(0)
  })

  // The 2026-09-13 real-model walk: a reply settled referral-campaign's job to
  // ok, the worker carried on in the thread and asked again with the draft.
  it('lists a question asked again after a reply settled the job', () => {
    const settled = delivery({ status: 'ok', ended_at: NOW - 120 })
    const answered = request({ id: 'a1', created_at: NOW - 700, answered_at: NOW - 120 })
    const again = request({ id: 'a2', created_at: NOW - 60, message: 'Draft for Mara is ready to send.' })
    expect(countAsks([settled], [answered, again])).toBe(1)
    const desk = buildDesk(input({ deliveries: [settled], subscriptions: [subscription()], attentionRequests: [answered, again] }))
    expect(desk.asks).toEqual([
      expect.objectContaining({
        requestId: 'a2',
        status: 'awaiting_human',
        waitingSeconds: 60,
        message: 'Draft for Mara is ready to send.',
      }),
    ])
  })

  it('does not list an open request older than the job’s end, or on a failed job', () => {
    const stale = request({ created_at: NOW - 700 })
    expect(countAsks([delivery({ status: 'ok', ended_at: NOW - 120 })], [stale])).toBe(0)
    expect(countAsks([delivery({ status: 'failed', ended_at: NOW - 120 })], [request({ created_at: NOW - 60 })])).toBe(0)
  })

  it('lists a session once when it has both a parked and a settled delivery', () => {
    const deliveries = [delivery({ id: 'old', status: 'ok', ended_at: NOW - 900 }), delivery({ id: 'new' })]
    expect(countAsks(deliveries, [request({ created_at: NOW - 60 })])).toBe(1)
  })
})

describe('notices — reports, not questions', () => {
  it('a notice is never an ask, even on a session with a parked delivery', () => {
    const notice = request({ id: 'n1', kind: 'notice', worker: 'architect', created_at: NOW - 300 })
    expect(countAsks([delivery()], [notice])).toBe(0)
    const desk = buildDesk(input({ deliveries: [delivery()], subscriptions: [subscription()], attentionRequests: [notice] }))
    expect(desk.asks).toHaveLength(0)
    expect(desk.notices).toEqual([
      expect.objectContaining({ id: 'n1', requestId: 'n1', headline: 'note from architect', ageSeconds: 300 }),
    ])
  })

  it('lists open notices newest first and drops acknowledged ones', () => {
    const desk = buildDesk(
      input({
        attentionRequests: [
          request({ id: 'old', kind: 'notice', created_at: NOW - 900 }),
          request({ id: 'new', kind: 'notice', created_at: NOW - 60 }),
          request({ id: 'done', kind: 'notice', created_at: NOW - 30, answered_at: NOW }),
          request({ id: 'q', created_at: NOW - 10 }),
        ],
      }),
    )
    expect(desk.notices.map((n) => n.id)).toEqual(['new', 'old'])
  })

  it('reads a missing or unknown kind as an ask', () => {
    expect(coerceAttentionRequest({ id: 'x' }).kind).toBe('ask')
    expect(coerceAttentionRequest({ id: 'x', kind: 'fyi' }).kind).toBe('ask')
    expect(coerceAttentionRequest({ id: 'x', kind: 'notice' }).kind).toBe('notice')
  })
})

describe('asks', () => {
  it('joins an awaiting_human delivery to its open request, message and all', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription()],
        attentionRequests: [request()],
      }),
    )
    expect(desk.asks).toHaveLength(1)
    const ask = desk.asks[0]!
    expect(ask.worker).toBe('email-answerer')
    expect(ask.status).toBe('awaiting_human')
    expect(ask.message).toMatch(/Ridley invoice/)
    expect(ask.sessionUrl).toBe('https://console.example/p/acme/s/sess-1')
    expect(ask.waitingSeconds).toBe(9600)
    expect(ask.waitingLabel).toBe('2h 40m')
    expect(ask.headline).toBe('email-answerer · awaiting_human · 2h 40m')
    expect(ask.glyph).toBe('attention')
    expect(ask.expiresInSeconds).toBeNull()
    expect(ask.expiresLabel).toBe('')
  })

  it('drops a parked delivery whose request has been answered — the read-time answer to the wart', () => {
    const answered = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription()],
        attentionRequests: [request({ answered_at: NOW - 60 })],
      }),
    )
    expect(answered.asks).toEqual([])
    // …and the same for a timed-out one.
    const timedOut = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription()],
        attentionRequests: [request({ timed_out_at: NOW - 60 })],
      }),
    )
    expect(timedOut.asks).toEqual([])
  })

  it('shows nothing when the route is absent, rather than rows without their sentence', () => {
    const desk = buildDesk(
      input({ deliveries: [delivery()], subscriptions: [subscription()], attentionRequests: [] }),
    )
    expect(desk.asks).toEqual([])
  })

  it('ignores deliveries that are not parked', () => {
    for (const status of ['ok', 'failed', 'running', 'pending', 'rate_limited']) {
      const desk = buildDesk(
        input({
          deliveries: [delivery({ status })],
          subscriptions: [subscription()],
          attentionRequests: [request()],
        }),
      )
      expect(desk.asks, status).toEqual([])
    }
  })

  it('keeps the clock running: awaiting_human never stamps ended_at', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery({ started_at: NOW - 68_400 })],
        subscriptions: [subscription()],
        attentionRequests: [request()],
        nowSeconds: NOW,
      }),
    )
    expect(desk.asks[0]!.waitingLabel).toBe('19h 0m')
  })

  it('reads the expiry as a countdown, and says so when it has passed', () => {
    const soon = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription()],
        attentionRequests: [request({ expires_at: NOW + 18_000 })],
      }),
    )
    expect(soon.asks[0]!.expiresLabel).toBe('expires in 5h 0m')
    const gone = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription()],
        attentionRequests: [request({ expires_at: NOW - 10 })],
      }),
    )
    expect(gone.asks[0]!.expiresLabel).toBe('expired')
  })

  it('falls back to the subscription for the worker when the request names none', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription({ worker: 'invoice-parser' })],
        attentionRequests: [request({ worker: '' })],
      }),
    )
    expect(desk.asks[0]!.worker).toBe('invoice-parser')
  })

  it('names no worker rather than guessing when neither side has one', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [],
        attentionRequests: [request({ worker: '' })],
      }),
    )
    expect(desk.asks[0]!.worker).toBe('')
    expect(desk.asks[0]!.headline).toBe('a worker · awaiting_human · 2h 40m')
  })

  it('takes the newest open request when a session asked twice', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery()],
        subscriptions: [subscription()],
        attentionRequests: [
          request({ id: 'a1', created_at: NOW - 9600, message: 'first' }),
          request({ id: 'a2', created_at: NOW - 600, message: 'second' }),
        ],
      }),
    )
    expect(desk.asks).toHaveLength(1)
    expect(desk.asks[0]!.message).toBe('second')
    expect(desk.asks[0]!.requestId).toBe('a2')
  })

  it('orders newest first, deterministically', () => {
    const desk = buildDesk(
      input({
        deliveries: [
          delivery({ id: 'd1', session_id: 'sess-1' }),
          delivery({ id: 'd2', session_id: 'sess-2' }),
          delivery({ id: 'd3', session_id: 'sess-3' }),
        ],
        subscriptions: [subscription()],
        attentionRequests: [
          request({ id: 'a1', session_id: 'sess-1', created_at: NOW - 9600 }),
          request({ id: 'a2', session_id: 'sess-2', created_at: NOW - 68_400 }),
          request({ id: 'a3', session_id: 'sess-3', created_at: NOW - 9600 }),
        ],
      }),
    )
    expect(desk.asks.map((a) => a.id)).toEqual(['d1', 'd3', 'd2'])
  })

  it('states how an ask leaves the stack once, for the page to render', () => {
    expect(DESK_ASKS_CAVEAT).toMatch(/reply in its thread/)
  })
})

// ---------------------------------------------------------------------------

describe('changes', () => {
  it('windows the changelog to "since you last looked"', () => {
    const desk = buildDesk(
      input({
        configEvents: [
          configEvent({ id: 'old', created_at: NOW_MS - 100_000 }),
          configEvent({ id: 'new', created_at: NOW_MS - 1000 }),
        ],
        lastSeenMs: NOW_MS - 50_000,
      }),
    )
    expect(desk.changes.map((c) => c.id)).toEqual(['new'])
  })

  it('shows everything when the operator has never looked', () => {
    const desk = buildDesk(
      input({
        configEvents: [configEvent({ id: 'c1', created_at: 1 }), configEvent({ id: 'c2', created_at: 2 })],
        lastSeenMs: 0,
      }),
    )
    expect(desk.changes).toHaveLength(2)
  })

  it('diffs against the previous version even when that version is outside the window', () => {
    const desk = buildDesk(
      input({
        configEvents: [
          configEvent({
            id: 'c1',
            action: 'worker_prompt_write',
            payload: { name: 'email-answerer', system_prompt: 'Answer.\nBe brief.' },
            created_at: NOW_MS - 100_000,
            rationale: 'first',
          }),
          configEvent({
            id: 'c2',
            actor_worker: 'email-reviewer',
            action: 'worker_prompt_write',
            payload: { name: 'email-answerer', system_prompt: 'Answer.\nQuote the ticket reference.' },
            created_at: NOW_MS - 1000,
            rationale: 'answers kept omitting the ticket reference',
          }),
        ],
        lastSeenMs: NOW_MS - 50_000,
      }),
    )
    expect(desk.changes).toHaveLength(1)
    const change = desk.changes[0]!
    expect(change.entry.diff?.previousEventId).toBe('c1')
    expect(change.diffLabel).toBe('+1 −1 lines')
    expect(change.sentence).toBe('email-reviewer rewrote email-answerer')
    expect(change.byAgent).toBe(true)
    expect(change.glyph).toBe('agent')
    expect(change.reason).toBe('answers kept omitting the ticket reference')
    expect(change.noReason).toBe(false)
  })

  it("marks a human's edit hollow, and says plainly when it carried no reason", () => {
    const desk = buildDesk(
      input({
        configEvents: [
          configEvent({
            id: 'c9',
            action: 'schedule_update',
            payload: { id: 'daily-brief' },
            rationale: '   ',
          }),
        ],
      }),
    )
    const change = desk.changes[0]!
    expect(change.actor).toBe('you')
    expect(change.byAgent).toBe(false)
    expect(change.glyph).toBe('human')
    expect(change.sentence).toBe('you retuned schedule daily-brief')
    expect(change.reason).toBe('(no reason given)')
    expect(change.noReason).toBe(true)
    expect(change.diffLabel).toBe('')
  })

  it('builds an actor-session permalink when the project is known', () => {
    const desk = buildDesk(
      input({
        configEvents: [configEvent({ actor_worker: 'archivist', actor_session: 'sess-7' })],
        projectId: 'acme',
      }),
    )
    expect(desk.changes[0]!.entry.sessionPath).toBe('/p/acme/s/sess-7')
  })

  it('speaks the operator vocabulary, verb by verb', () => {
    expect(deskChangeVerb('worker_prompt_write')).toBe('rewrote')
    expect(deskChangeVerb('worker_create')).toBe('hired')
    expect(deskChangeVerb('worker_delete')).toBe('retired')
    expect(deskChangeVerb('worker_freeze')).toBe('froze')
    expect(deskChangeVerb('schedule_update')).toBe('retuned')
    expect(deskChangeVerb('image_create')).toBe('published')
    expect(deskChangeVerb('topology_apply')).toBe('applied')
    // Anything the vocabulary has not seen renders as itself, never as a guess.
    expect(deskChangeVerb('worker_teleported')).toBe('worker_teleported')
  })

  it('names the subject the way the operator controls it', () => {
    const subjectOf = (over: Partial<ConfigEvent>): string => {
      const desk = buildDesk(input({ configEvents: [configEvent(over)] }))
      return deskChangeSubject(desk.changes[0]!.entry)
    }
    expect(subjectOf({ action: 'worker_update', payload: { name: 'email-answerer' } })).toBe(
      'email-answerer',
    )
    expect(subjectOf({ action: 'project_settings_put', payload: {} })).toBe('project settings')
    expect(subjectOf({ action: 'project_prompt_write', payload: { prompt: 'x' } })).toBe(
      'the project prompt',
    )
    expect(subjectOf({ action: 'subscription_create', payload: { id: 'sub-3' } })).toBe(
      'subscription sub-3',
    )
    // The uuid is the fallback; the row's own state names it for a person.
    expect(
      subjectOf({
        action: 'subscription_create',
        payload: { id: 'sub-3', worker: 'scribe', event_type: 'worker.finished' },
      }),
    ).toBe("scribe's subscription to worker.finished")
    expect(
      subjectOf({
        action: 'schedule_update',
        payload: { id: '9d33f7c6', worker: 'numbers-clerk', cron: '0 18 * * 4' },
      }),
    ).toBe("numbers-clerk's schedule (At 18:00, on Thursday)")
    expect(subjectOf({ action: 'schedule_update', payload: { id: '9d33f7c6' } })).toBe(
      'schedule 9d33f7c6',
    )
    expect(subjectOf({ action: 'image_create', payload: { name: 'toolbox', version: 4 } })).toBe(
      'toolbox:4',
    )
    expect(subjectOf({ action: 'topology_apply', payload: { topology: 'solo@v1' } })).toBe(
      'topology solo@v1',
    )
  })

  it('publishes the image sentence the design storyboard shows', () => {
    const desk = buildDesk(
      input({
        configEvents: [
          configEvent({
            actor_worker: 'archivist',
            action: 'image_create',
            payload: { name: 'toolbox', version: 4 },
          }),
        ],
      }),
    )
    expect(desk.changes[0]!.sentence).toBe('archivist published toolbox:4')
  })

  it('reads milliseconds for the window and seconds for everything else', () => {
    // lastSeenMs in seconds by mistake would let every change through; the
    // window is milliseconds and the fold does not "helpfully" guess.
    const desk = buildDesk(
      input({
        configEvents: [configEvent({ created_at: NOW_MS - 1000 })],
        lastSeenMs: NOW_MS,
      }),
    )
    expect(desk.changes).toEqual([])
  })
})

// ---------------------------------------------------------------------------

describe('trouble', () => {
  it('groups failed deliveries by worker, and says there is no reason column', () => {
    const desk = buildDesk(
      input({
        deliveries: [
          delivery({ id: 'f1', status: 'failed', created_at: NOW - 7200, session_id: 'sess-a' }),
          delivery({ id: 'f2', status: 'failed', created_at: NOW - 3600, session_id: 'sess-b' }),
          delivery({ id: 'f3', status: 'failed', created_at: NOW - 60, session_id: 'sess-c' }),
          delivery({ id: 'ok1', status: 'ok' }),
        ],
        subscriptions: [subscription({ worker: 'invoice-parser' })],
      }),
    )
    expect(desk.trouble).toHaveLength(1)
    const item = desk.trouble[0]!
    expect(item.kind).toBe('failed-deliveries')
    expect(item.glyph).toBe('failure')
    expect(item.count).toBe(3)
    expect(item.headline).toBe('3 deliveries failed · worker invoice-parser')
    expect(item.detail).toBe(DESK_NO_DELIVERY_REASON)
    expect(item.detail).toMatch(/No reason is recorded on a delivery row/)
    expect(item.sinceSeconds).toBe(NOW - 7200)
    // The newest of the group, for "open last job".
    expect(item.sessionId).toBe('sess-c')
  })

  // RD15/I1: the delivery row names its own worker (migration 024). Grouping
  // by the subscription join alone put every schedule firing — which has no
  // subscription — and every job whose subscription had been deleted into one
  // nameless "worker " pile, so an operator was told the wrong thing was
  // broken.
  it('groups failures by the worker on the delivery row, not only by the join', () => {
    const desk = buildDesk(
      input({
        deliveries: [
          delivery({ id: 'f1', status: 'failed', worker: 'nightly-summariser', subscription_id: 'gone' }),
          delivery({ id: 'f2', status: 'failed', worker: 'nightly-summariser', subscription_id: 'gone' }),
        ],
        subscriptions: [],
      }),
    )
    expect(desk.trouble).toHaveLength(1)
    expect(desk.trouble[0]!.headline).toBe('2 deliveries failed \u00b7 worker nightly-summariser')
  })

  // RD20: the engine records WHY a delivery failed (migration 037). The Desk
  // shows the engine's own words; the "no reason" sentence is the fallback for
  // a row that has none, not the standing answer it used to be.
  it('shows the recorded failure reason, freshest first', () => {
    const desk = buildDesk(
      input({
        deliveries: [
          delivery({
            id: 'f1',
            status: 'failed',
            created_at: NOW - 7200,
            failure_reason: 'start job: worker not found',
          }),
          delivery({
            id: 'f2',
            status: 'failed',
            created_at: NOW - 60,
            failure_reason: 'start job: host port pool is exhausted',
          }),
        ],
        subscriptions: [subscription({ worker: 'invoice-parser' })],
      }),
    )
    expect(desk.trouble[0]!.detail).toBe('start job: host port pool is exhausted')
  })

  it('never lets a blank reason overwrite one that says something', () => {
    const desk = buildDesk(
      input({
        deliveries: [
          delivery({
            id: 'f1',
            status: 'failed',
            created_at: NOW - 7200,
            failure_reason: 'start job: worker not found',
          }),
          // Newer, but recorded nothing — an older agentd, or a path that had
          // no reason to give.
          delivery({ id: 'f2', status: 'failed', created_at: NOW - 60, failure_reason: '' }),
        ],
        subscriptions: [subscription({ worker: 'invoice-parser' })],
      }),
    )
    expect(desk.trouble[0]!.detail).toBe('start job: worker not found')
  })

  it('counts one failure in the singular, and admits when the subscription is gone', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery({ id: 'f1', status: 'failed', subscription_id: 'deleted' })],
        subscriptions: [],
      }),
    )
    expect(desk.trouble[0]!.headline).toBe(
      '1 delivery failed · the subscription that started them is gone',
    )
    expect(desk.trouble[0]!.worker).toBe('')
  })

  it('orders failure groups by size, then by name', () => {
    const desk = buildDesk(
      input({
        deliveries: [
          delivery({ id: 'f1', status: 'failed', subscription_id: 's1' }),
          delivery({ id: 'f2', status: 'failed', subscription_id: 's2' }),
          delivery({ id: 'f3', status: 'failed', subscription_id: 's2' }),
        ],
        subscriptions: [
          subscription({ id: 's1', worker: 'alpha' }),
          subscription({ id: 's2', worker: 'zulu' }),
        ],
      }),
    )
    expect(desk.trouble.map((t) => t.worker)).toEqual(['zulu', 'alpha'])
  })

  it('reports a schedule halted by the five-strike rule, with the reason that field exists for', () => {
    const desk = buildDesk(input({ schedules: [schedule()] }))
    expect(desk.trouble).toHaveLength(1)
    const item = desk.trouble[0]!
    expect(item.kind).toBe('schedule-halted')
    expect(item.headline).toBe('schedule nightly-sweep (0 2 * * *) disabled after 5 failed starts')
    expect(item.detail).toBe('last reason: image "toolbox:9" names no image in the catalogue')
    expect(item.count).toBe(SCHEDULE_MAX_PROVISION_FAILURES)
  })

  it('leaves a schedule short of the threshold alone, and needs no schedules at all', () => {
    expect(buildDesk(input({ schedules: [schedule({ provision_failures: 4 })] })).trouble).toEqual([])
    expect(buildDesk(input({})).trouble).toEqual([])
  })

  it('says a halted schedule recorded nothing rather than showing a blank cell', () => {
    const desk = buildDesk(input({ schedules: [schedule({ last_provision_error: '  ' })] }))
    expect(desk.trouble[0]!.detail).toBe(
      'No reason is recorded on the schedule row — last_provision_error is empty.',
    )
  })

  it('gives freeze refusals their own line, grouped by instrument and attacker', () => {
    const desk = buildDesk(
      input({
        events: [
          projectEvent({ id: 'p1', occurred_at: NOW - 7200 }),
          projectEvent({ id: 'p2', occurred_at: NOW - 3600 }),
          projectEvent({ id: 'p3', type: 'worker.finished', text: 'unrelated' }),
        ],
      }),
    )
    expect(desk.trouble).toHaveLength(1)
    const item = desk.trouble[0]!
    expect(item.kind).toBe('freeze-refusal')
    expect(item.glyph).toBe('freeze')
    expect(item.headline).toBe('fee-scorer refused 2 rewrites from tuner')
    expect(item.detail).toBe(DESK_FREEZE_REFUSAL_NOTE)
    expect(item.sinceSeconds).toBe(NOW - 7200)
    expect(item.sessionId).toBe('sess-9')
  })

  it('separates refusals by attacker, because who tried is the signal', () => {
    const desk = buildDesk(
      input({
        events: [
          projectEvent({ id: 'p1' }),
          projectEvent({
            id: 'p2',
            envelope: {
              depth: 0,
              source: 'core',
              worker: 'other-tuner',
              session_id: 'sess-8',
              interactive: false,
              attention_requested: false,
            },
          }),
        ],
      }),
    )
    expect(desk.trouble.map((t) => t.headline)).toEqual([
      'fee-scorer refused 1 rewrite from other-tuner',
      'fee-scorer refused 1 rewrite from tuner',
    ])
  })

  it('reads the defended worker out of the refusal text, and shrugs honestly when it cannot', () => {
    expect(
      frozenTargetFromText('Refused worker_prompt_write against frozen worker "fee-scorer".'),
    ).toBe('fee-scorer')
    expect(frozenTargetFromText('something else entirely')).toBe('')
    const desk = buildDesk(input({ events: [projectEvent({ text: 'something else entirely' })] }))
    expect(desk.trouble[0]!.headline).toBe('a frozen worker refused 1 rewrite from tuner')
  })

  it('keeps the three shapes in the order the morning reads them', () => {
    const desk = buildDesk(
      input({
        deliveries: [delivery({ id: 'f1', status: 'failed' })],
        subscriptions: [subscription()],
        schedules: [schedule()],
        events: [projectEvent()],
      }),
    )
    expect(desk.trouble.map((t) => t.kind)).toEqual([
      'failed-deliveries',
      'schedule-halted',
      'freeze-refusal',
    ])
    // Every id is unique, so React keys are safe.
    expect(new Set(desk.trouble.map((t) => t.id)).size).toBe(3)
  })
})

// ---------------------------------------------------------------------------

describe('the whole fold', () => {
  it('answers the three questions in order, and nothing else', () => {
    // `earlierChanges` is the waterline's tail (W4): the changes the operator
    // had already read, kept so "since you last looked" can be a visible line
    // rather than only a filter.
    expect(Object.keys(buildDesk(input({})))).toEqual([
      'asks',
      'notices',
      'changes',
      'earlierChanges',
      'trouble',
      'firsts',
    ])
  })

  it('is empty on an empty project — the first-run state is the page\'s job, not the fold\'s', () => {
    expect(buildDesk(input({}))).toEqual({
      asks: [],
      notices: [],
      changes: [],
      earlierChanges: [],
      trouble: [],
      firsts: [],
    })
  })

  it('is pure: the same input twice gives deep-equal output', () => {
    const arg = input({
      deliveries: [delivery(), delivery({ id: 'f1', status: 'failed' })],
      subscriptions: [subscription()],
      attentionRequests: [request()],
      configEvents: [configEvent()],
      events: [projectEvent()],
      schedules: [schedule()],
    })
    expect(buildDesk(arg)).toEqual(buildDesk(arg))
  })

  it('does not mutate what it was handed', () => {
    const deliveries = [delivery({ id: 'f2', status: 'failed', created_at: 1 }), delivery({ id: 'f1', status: 'failed', created_at: 2 })]
    const schedules = [schedule({ id: 'z' }), schedule({ id: 'a' })]
    const before = JSON.stringify({ deliveries, schedules })
    buildDesk(input({ deliveries, schedules, subscriptions: [subscription()] }))
    expect(JSON.stringify({ deliveries, schedules })).toBe(before)
  })
})

// ---------------------------------------------------------------------------

describe('firsts — the fold tags one record of each kind (design §3 G4)', () => {
  it('maps every config action to its kind, or to none', () => {
    expect(deskFirstKindForChange({ action: 'worker_create', rationale: '' })).toBe('first-worker')
    expect(deskFirstKindForChange({ action: 'subscription_create', rationale: '' })).toBe(
      'first-subscription',
    )
    expect(deskFirstKindForChange({ action: 'schedule_create', rationale: '' })).toBe('first-schedule')
    expect(deskFirstKindForChange({ action: 'worker_prompt_write', rationale: '' })).toBe(
      'first-rewrite',
    )
    expect(deskFirstKindForChange({ action: 'project_prompt_write', rationale: '' })).toBe(
      'first-rewrite',
    )
    // Not one of the seven.
    expect(deskFirstKindForChange({ action: 'worker_update', rationale: '' })).toBeNull()
    expect(deskFirstKindForChange({ action: 'project_settings_put', rationale: '' })).toBeNull()
  })

  it('reads a revert off its rationale, regardless of the underlying action', () => {
    expect(looksLikeRevertRationale('revert of worker_prompt_write (seq 4, event c1)')).toBe(true)
    expect(looksLikeRevertRationale('  Revert of worker_create (seq 1, event c0)')).toBe(true)
    expect(looksLikeRevertRationale('the rewrite made the replies worse')).toBe(false)
    expect(
      deskFirstKindForChange({
        action: 'worker_prompt_write',
        rationale: 'revert of worker_prompt_write (seq 4, event c1)',
      }),
    ).toBe('first-revert')
    // A revert always wins over the action's own kind, even for worker_create
    // (restoring a deleted worker looks exactly like hiring a new one).
    expect(
      deskFirstKindForChange({
        action: 'worker_create',
        rationale: 'revert of worker_delete (seq 9, event c8)',
      }),
    ).toBe('first-revert')
  })

  it('tags one record of each of the seven kinds, including the J1 seconds/ms hazard', () => {
    // Every DeskChange-shaped kind stamps unix MILLISECONDS (config events);
    // the ask stamps unix SECONDS (attention_requests) — desk.ts documents
    // this split at the top of the file. `NOW_MS` and `NOW` are the same
    // instant in the two units on purpose: a fold that forgot to convert one
    // of them would still "work" against a fixture where every timestamp is
    // the same raw NUMBER in both units, which is exactly the trap J1 warns
    // about. So the ask here is stamped noticeably EARLIER in wall-clock time
    // than every change, using its own (seconds) unit — if the fold ever
    // compared `DeskAsk.createdAt` against `DeskChange.createdAt` without the
    // ×1000, this ask would look about a thousand times too old (or too new)
    // relative to the changes, and the "earliest ask" test below would still
    // pass only because there is exactly one ask — a second ask exposes it,
    // which the ordering test further down adds.
    const desk = buildDesk(
      input({
        configEvents: [
          configEvent({ id: 'worker', action: 'worker_create', created_at: NOW_MS - 90_000 }),
          configEvent({ id: 'sched', action: 'schedule_create', created_at: NOW_MS - 80_000 }),
          configEvent({ id: 'sub', action: 'subscription_create', created_at: NOW_MS - 70_000 }),
          configEvent({ id: 'rewrite', action: 'worker_prompt_write', created_at: NOW_MS - 60_000 }),
          configEvent({
            id: 'revert',
            action: 'worker_update',
            created_at: NOW_MS - 50_000,
            rationale: 'revert of worker_update (seq 2, event x)',
          }),
        ],
        attentionRequests: [request({ id: 'ask1' })],
        deliveries: [delivery({ session_id: 'sess-1' })],
        memories: [{ created_at: NOW_MS - 40_000 }],
        lastSeenMs: 0,
      }),
    )

    const byKind = new Map(desk.firsts.map((f) => [f.kind, f]))
    expect(byKind.get('first-worker')).toEqual({ kind: 'first-worker', createdAtMs: NOW_MS - 90_000, id: 'worker' })
    expect(byKind.get('first-schedule')).toEqual({ kind: 'first-schedule', createdAtMs: NOW_MS - 80_000, id: 'sched' })
    expect(byKind.get('first-subscription')).toEqual({
      kind: 'first-subscription',
      createdAtMs: NOW_MS - 70_000,
      id: 'sub',
    })
    expect(byKind.get('first-rewrite')).toEqual({ kind: 'first-rewrite', createdAtMs: NOW_MS - 60_000, id: 'rewrite' })
    expect(byKind.get('first-revert')).toEqual({ kind: 'first-revert', createdAtMs: NOW_MS - 50_000, id: 'revert' })
    expect(byKind.get('first-memory')).toEqual({
      kind: 'first-memory',
      createdAtMs: NOW_MS - 40_000,
      id: 'memory:' + (NOW_MS - 40_000),
    })
    // The ask: request.created_at is unix SECONDS (the fixture's default is
    // NOW - 9600, per the `request()` builder above), so its DeskFirstRecord
    // must read that × 1000, not the raw seconds value — the J1 conversion,
    // made an assertion.
    expect(byKind.get('first-ask')).toEqual({
      kind: 'first-ask',
      createdAtMs: (NOW - 9600) * 1000,
      id: 'd1',
    })
    expect(desk.firsts).toHaveLength(7)
  })

  it('tags every open ask, in milliseconds, for firsts.ts to pick the earliest from', () => {
    // desk.ts's job stops at tagging every candidate; picking the earliest
    // per kind is firsts.ts's (`firstsToNarrate`, tested in firsts.test.ts).
    // What THIS fold must get right is that both asks land in `firsts` with
    // the seconds→ms conversion applied to each — a fold that forgot the
    // conversion on even one of them would put it out of order relative to
    // the other, and relative to any config-event-derived kind.
    const desk = buildDesk(
      input({
        attentionRequests: [
          request({ id: 'ask-early', session_id: 'sess-a', created_at: NOW - 5000 }),
          request({ id: 'ask-late', session_id: 'sess-b', created_at: NOW - 1000 }),
        ],
        deliveries: [
          delivery({ id: 'd-a', session_id: 'sess-a' }),
          delivery({ id: 'd-b', session_id: 'sess-b' }),
        ],
      }),
    )
    const asks = desk.firsts.filter((f) => f.kind === 'first-ask')
    expect(asks).toContainEqual({ kind: 'first-ask', createdAtMs: (NOW - 5000) * 1000, id: 'd-a' })
    expect(asks).toContainEqual({ kind: 'first-ask', createdAtMs: (NOW - 1000) * 1000, id: 'd-b' })
  })

  it('is unwindowed: a first outside the earlier-changes cap still shows up', () => {
    const many = Array.from({ length: 12 }, (_, i) =>
      configEvent({ id: `noise-${i}`, action: 'worker_update', created_at: NOW_MS - (200_000 - i * 1000) }),
    )
    const desk = buildDesk(
      input({
        configEvents: [
          configEvent({ id: 'first-worker-ever', action: 'worker_create', created_at: NOW_MS - 500_000 }),
          ...many,
        ],
        lastSeenMs: NOW_MS - 1,
        earlierChangesLimit: 3,
      }),
    )
    // The capped tail is short…
    expect(desk.earlierChanges.length).toBeLessThanOrEqual(3)
    // …but the fold's own first-of-a-kind list still has the actual first.
    expect(desk.firsts.find((f) => f.kind === 'first-worker')?.id).toBe('first-worker-ever')
  })
})

describe('deskNotes — what the team wrote down', () => {
  const row = (over: Partial<MemoryRow> = {}): MemoryRow => ({
    id: 'm1',
    labels: { kind: 'summary', name: 'review-2026-w37' },
    snippet: 'THE HONEST HEADLINE: nothing to review yet.',
    score: 0,
    created_by_worker: 'weekly-review',
    created_by_session: 'sess-4',
    created_at: NOW_MS,
    ...over,
  })

  it('titles by kind and name or worker, and says who wrote it', () => {
    const notes = deskNotes([
      row(),
      row({ id: 'm2', labels: { kind: 'rolling-summary', worker: 'copywriter' }, created_by_worker: 'scribe' }),
      row({ id: 'm3', labels: { kind: 'org-charter', name: 'x' }, created_by_worker: '' }),
      row({ id: 'm4', labels: {}, created_by_worker: '', created_by_session: '' }),
    ])
    expect(notes.map((n) => [n.title, n.writer])).toEqual([
      ['summary · review-2026-w37', 'weekly-review'],
      ['rolling-summary · copywriter', 'scribe'],
      ['org-charter · x', 'a chat session'],
      ['a note', 'you or the console'],
    ])
  })

  it('keeps only the newest few', () => {
    expect(deskNotes([row(), row({ id: 'm2' }), row({ id: 'm3' })], 2)).toHaveLength(2)
  })
})
