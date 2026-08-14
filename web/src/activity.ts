// The Activity fold — the whole project on one rail, in time order
// (`docs/product/28-console-ia-design.md` §1).
//
// The console's signature is one hairline with ticks hung off it, and its glyph
// set is keyed to AUTHORSHIP, not to record type: there is no "event" glyph and
// no "job" glyph, because the design never intended the rail to care which
// table a record came from. That is what makes one rail able to carry the whole
// project, and it is why this module exists: the Events page currently splits
// the same story across five tabs, so a prompt rewrite and the job it changed
// are on two screens and the reader has to hold a timestamp in their head.
//
// Pure, exactly as `desk.ts` is pure: no React, no window, no fetch, no clock.
// `nowMs` is a parameter because a screen whose content depends on the wall
// clock is untestable, and this one is a list of moments.
//
// ── The unit is the OCCURRENCE, not the record ──────────────────────────────
//
// One thing happening writes three rows in the database: an event arrives, a
// delivery is created, a job runs. Interleaving those naively gives three rows
// for one occurrence, which is worse than the tabs this replaces. So:
//
//   - a delivery IS the occurrence of work, and carries its event as headline
//     and its session as outcome — three records, one row;
//   - an event that woke nobody has no delivery to hide behind, so it gets its
//     own row;
//   - fan-out is honest: one event waking three workers is three rows, because
//     those are three jobs that can end three different ways. Collapsing them
//     would mean one row with three statuses, which says less than three rows.
//
// A record only earns its own row when it has no parent occurrence: a human's
// config change, an open attention request, a schedule that failed to start.
//
// ── Two traps that would double-list ────────────────────────────────────────
//
//   1. An `awaiting_human` delivery and the attention request parking it are
//      the SAME occurrence — the delivery is the job, the request is the words.
//      They fold to one row, exactly as the Desk's Asks stack joins them.
//   2. `worker.freeze_refused` is a project event, so it would appear both as a
//      refusal row and as a generic event row. It is excluded from the generic
//      pass.
//
// ── J1: the unit mismatch, and why there is one normaliser ──────────────────
//
// `desk.ts` records it and this module depends on it absolutely: deliveries,
// events, schedules and attention requests stamp unix SECONDS, while config
// events stamp unix MILLISECONDS. A rail SORTS BY TIME. A fold that misses this
// puts every configuration change roughly fifty thousand years into the future,
// silently, at the top of the list, looking entirely plausible.
//
// So every timestamp entering this module goes through `toMs`, once, at the
// boundary — and the tests feed it one record of every kind in the same table.

import {
  buildChangelog,
  type ChangelogEntry,
  type ConfigEvent,
} from './configLog.js'
import {
  deliveryDurationSeconds,
  formatDuration,
  type EventDelivery,
  type ProjectEvent,
  type Subscription,
} from './events.js'
import {
  deskChangeSubject,
  deskChangeVerb,
  frozenTargetFromText,
  openRequestsBySession,
  SCHEDULE_MAX_PROVISION_FAILURES,
  type AttentionRequest,
  type DeskGlyph,
} from './desk.js'
import type { Schedule } from './schedules.js'

// ---------------------------------------------------------------------------
// Time
// ---------------------------------------------------------------------------

/**
 * The one place a timestamp changes unit (J1). Everything on the rail is held
 * in unix MILLISECONDS, because that is the finer of the two resolutions and
 * converting the other way would quantise config events onto second boundaries
 * and shuffle same-second ties.
 *
 * Exported so the tests can aim at it directly, and so the worker-history fold
 * reuses it rather than growing a second copy.
 */
export function toMs(value: number | undefined | null, unit: 'seconds' | 'ms'): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) return 0
  return unit === 'ms' ? value : value * 1000
}

/** A quiet stretch worth marking rather than compressing away (design §3.6). */
export const ACTIVITY_GAP_THRESHOLD_MS = 4 * 60 * 60 * 1000

// ---------------------------------------------------------------------------
// The record
// ---------------------------------------------------------------------------

/** What a row on the rail is. The glyph says who; this says what. */
export type ActivityKind =
  | 'job'
  | 'event'
  | 'change'
  | 'ask'
  | 'freeze-refusal'
  | 'schedule-halted'

/** The filter chips (design 28 §1.3). They subset the rail; they never swap it. */
export const ACTIVITY_LENSES = ['all', 'events', 'jobs', 'changes'] as const
export type ActivityLens = (typeof ACTIVITY_LENSES)[number]

/** The lens a kind answers to. `all` is not a home, it is the absence of one. */
export type ActivityHome = Exclude<ActivityLens, 'all'>

/**
 * Which chip shows which kind.
 *
 * "jobs" is work — that ran, that is parked, or that failed to start. "events"
 * is what arrived on the spine. "changes" is configuration. A schedule halted
 * by the five-strike rule is filed under jobs because the fact it reports is
 * that work is not running, which is what someone filtering for jobs wants.
 */
export const ACTIVITY_HOME: Record<ActivityKind, ActivityHome> = {
  job: 'jobs',
  ask: 'jobs',
  'schedule-halted': 'jobs',
  event: 'events',
  'freeze-refusal': 'events',
  change: 'changes',
}

export interface ActivityRecord {
  /** Stable across refetches: the underlying row's id, prefixed by kind. */
  id: string
  kind: ActivityKind
  lens: ActivityHome
  glyph: DeskGlyph
  /** Unix MILLISECONDS, always — see `toMs`. */
  atMs: number
  /** The sentence, in the operator's vocabulary (design §11). */
  headline: string
  /** The second line: what the worker wrote, or why something failed. '' if none. */
  detail: string
  /** True when `detail` is something a person or a model wrote, rather than ours. */
  detailIsQuote: boolean
  /** `ran 41s`, `+4 −1 lines`, `waiting 2h 40m`. '' when there is nothing to say. */
  meta: string
  /** The worker this is about; '' when none is named. */
  worker: string
  /** For an "open the session" link; '' when there is nothing to open. */
  sessionId: string
  /** The event type, for the rows that have one; '' otherwise. */
  eventType: string
  /**
   * `3h 50m of nothing` when a quiet stretch precedes this row, '' otherwise.
   * Computed against the row above it IN THE FILTERED LIST — a gap is a fact
   * about what you are looking at, so filtering recomputes it.
   */
  gapBeforeLabel: string
  /**
   * The changelog entry behind a `change` row, diff machinery and all, so the
   * component can render the diff without rebuilding the changelog.
   */
  entry: ChangelogEntry | null
}

// ---------------------------------------------------------------------------
// The fold
// ---------------------------------------------------------------------------

export interface BuildActivityInput {
  events: ProjectEvent[]
  deliveries: EventDelivery[]
  configEvents: ConfigEvent[]
  attentionRequests: AttentionRequest[]
  subscriptions?: Subscription[]
  schedules?: Schedule[]
  /** The clock, in unix MILLISECONDS. */
  nowMs: number
  /** Used to build actor-session permalinks, as `buildChangelog` does. */
  projectId?: string
  /**
   * Which chip is active. Filtering happens INSIDE the fold so that gap markers
   * are correct for what is actually on screen: a four-hour hole between two
   * jobs is not a hole at all if a config change sits between them.
   */
  lens?: ActivityLens
  /**
   * The floor of the visible time window, in unix MILLISECONDS. Records older
   * than this are dropped. 0 shows everything fetched.
   *
   * Paging a merged rail by row count is wrong at its bottom edge — `LIMIT 50`
   * from each of four sources, merged, has no defined oldest row. So "Show
   * earlier" lowers this instead (design 28 §1.4).
   */
  windowStartMs?: number
}

/**
 * The whole rail, in one pure fold, newest first.
 *
 * Ties break on id so the order is deterministic under a second-resolution
 * clock — half these sources cannot distinguish two things in the same second,
 * and a list that reshuffles itself between refetches is a list nobody trusts.
 */
export function buildActivity(input: BuildActivityInput): ActivityRecord[] {
  const work = buildWorkRecords(input)
  const records = [
    ...work,
    ...buildUnparkedAskRecords(input, work),
    ...buildEventRecords(input),
    ...buildChangeRecords(input),
    ...buildScheduleRecords(input),
  ]

  const { lens = 'all', windowStartMs = 0 } = input
  const visible = records
    .filter((r) => (lens === 'all' ? true : r.lens === lens))
    .filter((r) => windowStartMs <= 0 || r.atMs >= windowStartMs)
    .sort((a, b) => b.atMs - a.atMs || a.id.localeCompare(b.id))

  return markGaps(visible)
}

/**
 * Label the quiet stretches. Mutates nothing the caller handed us — the records
 * are ours, built above.
 *
 * A gap is only meaningful between two records that both have a real timestamp:
 * a row stamped 0 is a row whose time we do not know, and inventing a
 * "55 years of nothing" caption above it would be a lie about the data.
 */
function markGaps(sorted: ActivityRecord[]): ActivityRecord[] {
  for (let i = 1; i < sorted.length; i += 1) {
    const newer = sorted[i - 1]!
    const older = sorted[i]!
    if (newer.atMs <= 0 || older.atMs <= 0) continue
    const delta = newer.atMs - older.atMs
    if (delta >= ACTIVITY_GAP_THRESHOLD_MS) {
      newer.gapBeforeLabel = `${formatDuration(Math.round(delta / 1000))} of nothing`
    }
  }
  return sorted
}

/**
 * Work: one row per delivery, joined to the event that caused it and to the
 * attention request parking it.
 *
 * This is the three-records-one-row collapse. The event is the headline because
 * the event is what happened; the delivery is the preposition; the job is the
 * outcome.
 */
function buildWorkRecords(input: BuildActivityInput): ActivityRecord[] {
  const { deliveries, events, attentionRequests, subscriptions = [], nowMs } = input
  const nowSeconds = Math.floor(nowMs / 1000)

  const eventById = new Map(events.map((e) => [e.id, e]))
  const workerBySubscription = new Map(subscriptions.map((s) => [s.id, s.worker]))
  const openBySession = openRequestsBySession(attentionRequests)

  return deliveries.map((delivery): ActivityRecord => {
    const event = eventById.get(delivery.event_id)
    // The row names its own worker since migration 024; the subscription join
    // is the fallback for older rows, and it goes blank when the subscription
    // has since been deleted.
    const worker =
      delivery.worker || workerBySubscription.get(delivery.subscription_id) || ''
    const eventType = event?.type ?? ''
    const request = openBySession.get(delivery.session_id)
    const parked = delivery.status === 'awaiting_human' && request !== undefined

    const atMs = toMs(
      delivery.created_at || delivery.started_at || event?.occurred_at || event?.created_at,
      'seconds',
    )

    const subject = worker === '' ? 'a worker' : worker
    const headline =
      eventType === ''
        ? `${subject} ran a job`
        : `${eventType} woke ${subject}`

    if (parked) {
      // The delivery is the job; the request is the words it is waiting on.
      // One occurrence, so one row — and the row leaves the rail's attention
      // state behind the moment the request is answered, exactly as the Desk's
      // Asks stack does. We do not rewrite the parked delivery row; we stop
      // drawing it as a live question.
      const waitingSeconds =
        deliveryDurationSeconds(delivery, nowSeconds) ??
        Math.max(0, nowSeconds - (request!.created_at || nowSeconds))
      return {
        id: `ask:${delivery.id}`,
        kind: 'ask',
        lens: ACTIVITY_HOME.ask,
        glyph: 'attention',
        atMs,
        headline: `${subject} is waiting for you`,
        detail: request!.message,
        detailIsQuote: true,
        meta: `waiting ${formatDuration(waitingSeconds)}`,
        worker,
        sessionId: delivery.session_id,
        eventType,
        gapBeforeLabel: '',
        entry: null,
      }
    }

    const failed = delivery.status === 'failed'
    const reason = (delivery.failure_reason ?? '').trim()
    const ran = deliveryDurationSeconds(delivery, nowSeconds)

    return {
      id: `job:${delivery.id}`,
      kind: 'job',
      lens: ACTIVITY_HOME.job,
      glyph: failed ? 'failure' : 'agent',
      atMs,
      headline: failed ? `${headline} — and it failed` : headline,
      // A failed delivery has no reason column on older rows, and RD20's column
      // can still be blank. Say so rather than rendering an empty line.
      detail: failed
        ? reason === ''
          ? 'No reason is recorded on this delivery. The agentd log and the job are where the reason is.'
          : reason
        : '',
      detailIsQuote: false,
      meta: ran === null ? delivery.status : `ran ${formatDuration(ran)}`,
      worker,
      sessionId: delivery.session_id,
      eventType,
      gapBeforeLabel: '',
      entry: null,
    }
  })
}

/**
 * Open attention requests that no delivery is parking.
 *
 * The Desk's Asks stack is deliveries joined to requests, so it can only ever
 * show a worker that was woken by an event. But `request_human_attention` is a
 * core MCP tool, and a worker in an ordinary interactive session — a chat, or a
 * session created over HTTP by an embedding application — calls it with no
 * subscription and no delivery anywhere in sight. On a rail that claims to be
 * everything the project did, that question would simply not exist.
 *
 * So the request stands as its own occurrence when nothing else is carrying it.
 * `buildWorkRecords` has already emitted a row for every request it could pair
 * with a parked delivery; this adds the remainder, and the pairing is checked
 * against those rows rather than recomputed, so the two can never disagree.
 */
function buildUnparkedAskRecords(
  input: BuildActivityInput,
  work: ActivityRecord[],
): ActivityRecord[] {
  const { attentionRequests, nowMs } = input
  const nowSeconds = Math.floor(nowMs / 1000)
  const parkedSessions = new Set(
    work.filter((r) => r.kind === 'ask').map((r) => r.sessionId),
  )

  const out: ActivityRecord[] = []
  for (const request of openRequestsBySession(attentionRequests).values()) {
    if (parkedSessions.has(request.session_id)) continue
    const worker = request.worker
    const waitingSeconds = Math.max(0, nowSeconds - (request.created_at || nowSeconds))
    out.push({
      id: `ask:request:${request.id}`,
      kind: 'ask',
      lens: ACTIVITY_HOME.ask,
      glyph: 'attention',
      atMs: toMs(request.created_at, 'seconds'),
      headline: `${worker === '' ? 'a worker' : worker} is waiting for you`,
      detail: request.message,
      detailIsQuote: true,
      meta: `waiting ${formatDuration(waitingSeconds)}`,
      worker,
      sessionId: request.session_id,
      eventType: '',
      gapBeforeLabel: '',
      entry: null,
    })
  }
  return out
}

/**
 * Events with no delivery of their own, plus freeze refusals.
 *
 * An event that woke somebody is already on the rail as that job, so listing it
 * again would be the three-rows-per-occurrence bug this fold exists to avoid.
 */
function buildEventRecords(input: BuildActivityInput): ActivityRecord[] {
  const { events, deliveries } = input
  const deliveredEventIds = new Set(deliveries.map((d) => d.event_id))
  const out: ActivityRecord[] = []

  for (const event of events) {
    const atMs = toMs(event.occurred_at || event.created_at, 'seconds')

    // A refusal is a signal, not a fault (playbook C8): an agent trying to edit
    // the thing that scores it is the reward-hacking hypothesis in its most
    // literal form. It gets its own glyph and never doubles as a plain event.
    if (event.type === 'worker.freeze_refused') {
      const target = frozenTargetFromText(event.text)
      const actor = event.envelope.worker
      out.push({
        id: `freeze:${event.id}`,
        kind: 'freeze-refusal',
        lens: ACTIVITY_HOME['freeze-refusal'],
        glyph: 'freeze',
        atMs,
        headline: `${target === '' ? 'a frozen worker' : target} refused a rewrite${actor === '' ? '' : ` from ${actor}`}`,
        detail:
          'A frozen worker is an instrument. An agent trying to edit the thing that scores it is a signal worth reading, not an error to clear.',
        detailIsQuote: false,
        meta: '',
        worker: target,
        sessionId: event.envelope.session_id,
        eventType: event.type,
        gapBeforeLabel: '',
        entry: null,
      })
      continue
    }

    if (deliveredEventIds.has(event.id)) continue

    // Nothing woke. That is a fact worth showing — a subscription that should
    // have matched and did not is invisible everywhere else in the console.
    const emitter = event.envelope.worker
    out.push({
      id: `event:${event.id}`,
      kind: 'event',
      lens: ACTIVITY_HOME.event,
      glyph: emitter === '' ? 'human' : 'agent',
      atMs,
      headline: `${event.type} woke nobody`,
      detail: event.text,
      detailIsQuote: true,
      meta: emitter === '' ? '' : `from ${emitter}`,
      worker: emitter,
      sessionId: event.envelope.session_id,
      eventType: event.type,
      gapBeforeLabel: '',
      entry: null,
    })
  }

  return out
}

/** Configuration changes, straight off the changelog the Desk already builds. */
function buildChangeRecords(input: BuildActivityInput): ActivityRecord[] {
  const { configEvents, projectId = '' } = input
  return buildChangelog(configEvents, { projectId }).map((entry): ActivityRecord => {
    const byAgent = entry.actorWorker !== ''
    const actor = byAgent ? entry.actorWorker : 'you'
    const noReason = entry.rationale.trim() === ''
    return {
      id: `change:${entry.id}`,
      kind: 'change',
      lens: ACTIVITY_HOME.change,
      glyph: byAgent ? 'agent' : 'human',
      // Config events are the ones stamped in milliseconds. This is J1's whole
      // blast radius, and it is one argument.
      atMs: toMs(entry.createdAt, 'ms'),
      headline: `${actor} ${deskChangeVerb(entry.action)} ${deskChangeSubject(entry)}`.trimEnd(),
      // Not a scold: the screen telling the truth about the product's patchy
      // rationales, which is the argument for asking for one.
      detail: noReason ? '(no reason given)' : entry.rationale,
      detailIsQuote: !noReason,
      meta: entry.diff ? `+${entry.diff.added} −${entry.diff.removed} lines` : '',
      worker: entry.actorWorker,
      sessionId: entry.actorSession,
      eventType: '',
      gapBeforeLabel: '',
      entry,
    }
  })
}

/** Schedules stopped by the five-strike rule. */
function buildScheduleRecords(input: BuildActivityInput): ActivityRecord[] {
  const { schedules = [] } = input
  return schedules
    .filter((s) => (s.provision_failures ?? 0) >= SCHEDULE_MAX_PROVISION_FAILURES)
    .map((schedule): ActivityRecord => {
      const reason = (schedule.last_provision_error ?? '').trim()
      const count = schedule.provision_failures ?? 0
      return {
        id: `schedule:${schedule.id}`,
        kind: 'schedule-halted',
        lens: ACTIVITY_HOME['schedule-halted'],
        glyph: 'failure',
        atMs: toMs(schedule.updated_at, 'seconds'),
        headline: `schedule ${schedule.worker} (${schedule.cron}) disabled after ${count} failed starts`,
        // `last_provision_error` exists precisely so a human can recover.
        detail:
          reason === ''
            ? 'No reason is recorded on the schedule row — last_provision_error is empty.'
            : `last reason: ${reason}`,
        detailIsQuote: false,
        meta: '',
        worker: schedule.worker,
        sessionId: '',
        eventType: '',
        gapBeforeLabel: '',
        entry: null,
      }
    })
}

/**
 * The oldest moment the rail is currently showing, for the "Show earlier"
 * control: paging lowers the window floor rather than raising a row count.
 * 0 when the rail is empty.
 */
export function oldestShownMs(records: ActivityRecord[]): number {
  let oldest = 0
  for (const record of records) {
    if (record.atMs <= 0) continue
    if (oldest === 0 || record.atMs < oldest) oldest = record.atMs
  }
  return oldest
}
