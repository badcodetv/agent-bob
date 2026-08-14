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
import {
  buildChangelog,
  workerLineage,
  type ConfigEvent,
  type WorkerLineage as PromptLineage,
} from './configLog.js'
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
  /**
   * The session's own title, for the permalink's text. '' on a change row, and
   * on a run that never got one.
   */
  title: string
}

/**
 * One run, as `GET /agent/sessions?worker=` returns it.
 *
 * Structural rather than the full `AgentSessionListItem`, so the fold stays a
 * pure function over the few fields it actually reads.
 */
export interface WorkerJobRow {
  id: string
  created_at: number
  title?: string
  status?: string
  /** Absent on plain sessions; only ever used to exclude a foreign row. */
  worker?: string
}

export interface BuildWorkerHistoryInput {
  /** The worker this rail belongs to. */
  workerName: string
  /**
   * This worker's runs, from `GET /agent/sessions?worker=`.
   *
   * That route filters IN THE DATABASE, which is why it is the source rather
   * than the delivery list: deliveries come back as a recent window over the
   * whole project, so on a busy project a given worker's runs may not appear in
   * it at all. Using deliveries as the source would have quietly shortened
   * every worker's history — the coverage the old Jobs tab had, lost.
   */
  jobs: WorkerJobRow[]
  /**
   * Deliveries, used only to ENRICH a run with what a session row does not
   * carry: how long it took, and why it failed. Joined on session id, so a run
   * with no matching delivery still appears — just without the extra.
   */
  deliveries?: EventDelivery[]
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

/**
 * The fold's result. Named `…Result` because `WorkerHistory` is the component
 * that renders it, and one public API cannot export both under one name.
 */
export interface WorkerHistoryResult {
  records: WorkerHistoryRecord[]
  /** Prompt-carrying changes — the number of versions, v1…vN. */
  versions: number
  /** Versions after the first: what an operator means by "rewrites". */
  rewrites: number
  /** Jobs on the rail, after filtering. */
  jobs: number
  /**
   * The prompt lineage this fold consumed, passed through rather than recomputed.
   *
   * The "changes since your last review" banner is a function of the lineage and
   * the watermark, and it survives the Jobs/Lineage merge — so the component
   * needs the same object the counts came from. Building the changelog twice to
   * get it would be the seam where two counters start to disagree, which is
   * exactly the defect doc 21 filed as X5.
   */
  lineage: PromptLineage
}

/**
 * One worker's runs and rewrites, newest first.
 *
 * The counts come from `workerLineage`, not from counting rows here: doc 21's
 * X5 was a header reading "2 rewrites · 3 distinct" because two places counted
 * the same thing differently, and the fix was one counter. This fold consumes
 * that counter rather than becoming a second one.
 */
export function buildWorkerHistory(input: BuildWorkerHistoryInput): WorkerHistoryResult {
  const { workerName, configEvents, projectId = '', windowStartMs = 0 } = input

  const lineage = workerLineage(buildChangelog(configEvents, { projectId }), workerName)

  const changeRecords = lineage.entries.map((row): WorkerHistoryRecord => {
    const { entry } = row
    const actor = row.byWorker ? entry.actorWorker : 'you'
    const noReason = entry.rationale.trim() === ''
    // The version number is deliberately NOT in `meta`: the row renders it as a
    // chip, and printing it twice twenty pixels apart is doc 21's X4 defect
    // (a duplicated label) reappearing on a different surface.
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
      meta: [diffLabel, row.duplicate ? 'no change to the text' : '']
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
      title: '',
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
    lineage,
  }
}

/**
 * This worker's runs: one row per session, enriched from its delivery.
 *
 * A session is the run; the delivery — where there is one — knows what woke it,
 * how long it took and why it failed. A run started by hand or by a schedule
 * has no matching delivery and still belongs on the rail, so the join is a
 * left join in spirit: absent enrichment is quiet, never a dropped row.
 */
function buildJobRecords(input: BuildWorkerHistoryInput): WorkerHistoryRecord[] {
  const { workerName, jobs, deliveries = [], events = [], subscriptions = [], nowMs } = input
  const nowSeconds = Math.floor(nowMs / 1000)
  const eventById = new Map(events.map((e) => [e.id, e]))
  const workerBySubscription = new Map(subscriptions.map((s) => [s.id, s.worker]))
  const deliveryBySession = new Map(
    deliveries
      .filter((d) => d.session_id !== '')
      .map((d) => [d.session_id, d] as const),
  )

  const out: WorkerHistoryRecord[] = []
  for (const job of jobs) {
    // The route filters by worker already; this only rejects a row that
    // explicitly names a DIFFERENT one, so an over-fetching caller cannot put
    // another worker's run here and a row that names nobody is still ours.
    if (job.worker !== undefined && job.worker !== '' && job.worker !== workerName) continue

    const delivery = deliveryBySession.get(job.id)
    const event = delivery ? eventById.get(delivery.event_id) : undefined
    const eventType = event?.type ?? ''
    const status = delivery?.status ?? job.status ?? ''
    const failed = status === 'failed'
    const parked = status === 'awaiting_human'
    const ran = delivery ? deliveryDurationSeconds(delivery, nowSeconds) : null
    const reason = (delivery?.failure_reason ?? '').trim()

    // The subscription join is the pre-024 fallback for naming the worker; kept
    // so an enriched row still reads correctly on an old delivery.
    const named =
      delivery?.worker || workerBySubscription.get(delivery?.subscription_id ?? '') || workerName

    out.push({
      id: `job:${job.id}`,
      kind: parked ? 'ask' : 'job',
      lens: ACTIVITY_HOME.job,
      glyph: parked ? 'attention' : failed ? 'failure' : 'agent',
      atMs: toMs(job.created_at, 'seconds'),
      // The title is NOT folded into the headline: the row renders it as the
      // permalink's text, and printing it in both places is the duplicated
      // label again.
      headline: parked
        ? `${named} is waiting for you`
        : eventType === ''
          ? `${named} ran a job`
          : `${eventType} woke ${named}`,
      detail: failed
        ? reason === ''
          ? 'No reason is recorded on this delivery. The agentd log and the job are where the reason is.'
          : reason
        : '',
      detailIsQuote: false,
      meta: ran === null ? status : `ran ${formatDuration(ran)}`,
      worker: workerName,
      sessionId: job.id,
      eventType,
      gapBeforeLabel: '',
      entry: null,
      version: null,
      prompt: null,
      duplicate: false,
      title: job.title ?? '',
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
