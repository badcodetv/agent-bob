// The worker-history fold — one worker's runs and rewrites on the same rail
// (`docs/product/28-console-ia-design.md` §2.3).
//
// Today the worker page keeps these on two tabs: `Jobs` (what it did) and
// `Lineage` (how it changed). They are one story told in time, and separating
// them hides the product's own thesis — design §7.2 calls before/after "the
// product's thesis on one screen", and that thesis is a sentence about time:
// *the prompt changed at 05:40, and the job that ran after came out different.*
//
// `BeforeAfterView` exists precisely because the two tabs make that invisible:
// it is a bespoke component built to manually re-join two records that a
// time-ordered rail puts next to each other for nothing. Folding the tabs
// together is what retires it.
//
// This is the third altitude of the same rail — Desk digests everything,
// `activity.ts` orders the whole project, and this orders one worker — so it
// deliberately reuses `activity.ts`'s record shape and its `toMs` boundary
// rather than growing a parallel vocabulary. Two normalisers is how the J1 bug
// gets reintroduced by a file that never read the warning.
//
// Pure, like its two siblings: no React, no window, no fetch, no clock.

import {
  ACTIVITY_HOME,
  ACTIVITY_GAP_THRESHOLD_MS,
  toMs,
  type ActivityRecord,
} from './activity.js'
import { buildChangelog, workerLineage, type ConfigEvent } from './configLog.js'
import {
  deliveryDurationSeconds,
  formatDuration,
  type EventDelivery,
  type ProjectEvent,
  type Subscription,
} from './events.js'

/**
 * A row on one worker's rail: an activity record, plus the version machinery a
 * prompt rewrite carries.
 *
 * The extra three fields are null/false on everything that is not a prompt
 * version, so the shared rail component can render this list without knowing
 * which altitude it is drawing.
 */
export interface WorkerHistoryRecord extends ActivityRecord {
  /** 1-based version, oldest prompt-carrying change = v1. Null when no prompt. */
  version: number | null
  /** The full prompt this change wrote, for the restore path. Null when none. */
  prompt: string | null
  /**
   * True when this version's text is byte-identical to the one before it.
   * Churn, not a change — and worth drawing quietly rather than hiding, because
   * a worker that keeps "rewriting" a prompt to itself is a finding.
   */
  duplicate: boolean
}

export interface BuildWorkerHistoryInput {
  /** The worker this rail belongs to. */
  workerName: string
  /**
   * Deliveries to consider. The caller supplies whatever it fetched; this fold
   * filters to the worker itself rather than trusting the query, so a page that
   * over-fetches cannot put another worker's job on this rail.
   */
  deliveries: EventDelivery[]
  /** For naming the event that woke each job; optional. */
  events?: ProjectEvent[]
  /** Fallback worker lookup for delivery rows written before migration 024. */
  subscriptions?: Subscription[]
  /** The whole project's config log; filtered to this worker here. */
  configEvents: ConfigEvent[]
  /** The clock, in unix MILLISECONDS. */
  nowMs: number
  projectId?: string
  /** Floor of the visible window, in unix MILLISECONDS. 0 shows everything. */
  windowStartMs?: number
}

export interface WorkerHistory {
  records: WorkerHistoryRecord[]
  /** Prompt-carrying changes — the number of versions, v1…vN. */
  versions: number
  /** Versions after the first: what an operator means by "rewrites". */
  rewrites: number
  /** Jobs on the rail, after filtering. */
  jobs: number
}

/**
 * One worker's runs and rewrites, newest first.
 *
 * The counts come from `workerLineage`, not from counting rows here: doc 21's
 * X5 was a header reading "2 rewrites · 3 distinct" because two places counted
 * the same thing differently, and the fix was one counter. This fold consumes
 * that counter rather than becoming a second one.
 */
export function buildWorkerHistory(input: BuildWorkerHistoryInput): WorkerHistory {
  const { workerName, configEvents, projectId = '', windowStartMs = 0 } = input

  const lineage = workerLineage(buildChangelog(configEvents, { projectId }), workerName)

  const changeRecords = lineage.entries.map((row): WorkerHistoryRecord => {
    const { entry } = row
    const actor = row.byWorker ? entry.actorWorker : 'you'
    const noReason = entry.rationale.trim() === ''
    const versionLabel = row.version === null ? '' : `v${row.version}`
    const diffLabel = entry.diff ? `+${entry.diff.added} −${entry.diff.removed} lines` : ''
    return {
      id: `change:${entry.id}`,
      kind: 'change',
      lens: ACTIVITY_HOME.change,
      glyph: row.byWorker ? 'agent' : 'human',
      // Config events are the millisecond ones. This is J1's whole blast radius.
      atMs: toMs(entry.createdAt, 'ms'),
      headline: `${actor} ${entry.action === 'worker_prompt_write' ? 'rewrote' : 'changed'} ${workerName}`,
      detail: noReason ? '(no reason given)' : entry.rationale,
      detailIsQuote: !noReason,
      meta: [versionLabel, diffLabel, row.duplicate ? 'no change to the text' : '']
        .filter((part) => part !== '')
        .join(' · '),
      worker: workerName,
      sessionId: entry.actorSession,
      eventType: '',
      gapBeforeLabel: '',
      entry,
      version: row.version,
      prompt: row.prompt,
      duplicate: row.duplicate,
    }
  })

  const jobRecords = buildJobRecords(input)

  const records = [...changeRecords, ...jobRecords]
    .filter((r) => windowStartMs <= 0 || r.atMs >= windowStartMs)
    .sort((a, b) => b.atMs - a.atMs || a.id.localeCompare(b.id))

  return {
    records: markGaps(records),
    versions: lineage.versions,
    rewrites: lineage.rewrites,
    jobs: jobRecords.length,
  }
}

/** This worker's jobs — the same occurrence collapse `activity.ts` performs. */
function buildJobRecords(input: BuildWorkerHistoryInput): WorkerHistoryRecord[] {
  const { workerName, deliveries, events = [], subscriptions = [], nowMs } = input
  const nowSeconds = Math.floor(nowMs / 1000)
  const eventById = new Map(events.map((e) => [e.id, e]))
  const workerBySubscription = new Map(subscriptions.map((s) => [s.id, s.worker]))

  const out: WorkerHistoryRecord[] = []
  for (const delivery of deliveries) {
    // Read the row (migration 024); fall back to the subscription join only for
    // older rows, which carry ''. Filtering here rather than trusting the query
    // keeps another worker's job off this rail even if the caller over-fetches.
    const worker =
      delivery.worker || workerBySubscription.get(delivery.subscription_id) || ''
    if (worker !== workerName) continue

    const event = eventById.get(delivery.event_id)
    const eventType = event?.type ?? ''
    const failed = delivery.status === 'failed'
    const parked = delivery.status === 'awaiting_human'
    const ran = deliveryDurationSeconds(delivery, nowSeconds)
    const reason = (delivery.failure_reason ?? '').trim()

    out.push({
      id: `job:${delivery.id}`,
      kind: parked ? 'ask' : 'job',
      lens: ACTIVITY_HOME.job,
      glyph: parked ? 'attention' : failed ? 'failure' : 'agent',
      atMs: toMs(
        delivery.created_at || delivery.started_at || event?.occurred_at || event?.created_at,
        'seconds',
      ),
      headline: parked
        ? `${workerName} is waiting for you`
        : eventType === ''
          ? `${workerName} ran a job`
          : `${eventType} woke ${workerName}`,
      detail: failed
        ? reason === ''
          ? 'No reason is recorded on this delivery. The agentd log and the job are where the reason is.'
          : reason
        : '',
      detailIsQuote: false,
      meta: ran === null ? delivery.status : `ran ${formatDuration(ran)}`,
      worker: workerName,
      sessionId: delivery.session_id,
      eventType,
      gapBeforeLabel: '',
      entry: null,
      version: null,
      prompt: null,
      duplicate: false,
    })
  }
  return out
}

/**
 * Label quiet stretches, same rule as the project rail: only between two rows
 * that both know when they happened, because captioning a gap above a row
 * stamped 0 would be a lie about the data rather than a fact about the night.
 */
function markGaps(sorted: WorkerHistoryRecord[]): WorkerHistoryRecord[] {
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
 * The runs immediately before and after a prompt version — the before/after
 * comparison, read off the rail instead of re-fetched.
 *
 * This is what `BeforeAfterView` does by hand today. On an ordered rail it is
 * an index walk, which is the argument for the merge: the comparison stops
 * being a feature and becomes a property of the list.
 *
 * Returns nulls where the rail has nothing — a version with no run after it yet
 * is the normal case for a rewrite that just happened, and the caller should
 * say "no run yet" rather than pretend.
 */
export function runsAround(
  records: WorkerHistoryRecord[],
  changeId: string,
): { before: WorkerHistoryRecord | null; after: WorkerHistoryRecord | null } {
  const index = records.findIndex((r) => r.id === changeId)
  if (index === -1) return { before: null, after: null }
  // Records are newest first, so "after in time" is *earlier* in the array.
  let after: WorkerHistoryRecord | null = null
  for (let i = index - 1; i >= 0; i -= 1) {
    if (records[i]!.kind !== 'change') {
      after = records[i]!
      break
    }
  }
  let before: WorkerHistoryRecord | null = null
  for (let i = index + 1; i < records.length; i += 1) {
    if (records[i]!.kind !== 'change') {
      before = records[i]!
      break
    }
  }
  return { before, after }
}
