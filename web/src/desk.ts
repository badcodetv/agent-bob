// The Desk fold — the three stacks of the operator's morning, computed
// read-time from data the product layer already keeps (operator-console design
// `docs/product/15-operator-console-design.md` §5.2).
//
// Three questions, in this order, every morning: *does anything want me? what
// changed? what broke?* — so `buildDesk` answers them as `{asks, changes,
// trouble}` and nothing else. The Desk stores nothing and decides nothing: it
// is a query over `event_deliveries`, `attention_requests`, `config_events`,
// `project_events` and the schedule rows, and every item's action is to open
// the thing it names.
//
// Pure: no React, no window, no fetch, no clock. `nowSeconds` and `lastSeenMs`
// are parameters precisely because a screen whose content depends on the wall
// clock is untestable — and this one is a table of ages.
//
// Two unit systems meet here, deliberately and awkwardly:
//   - deliveries, events, schedules and attention requests stamp unix SECONDS;
//   - config events stamp unix MILLISECONDS (J1).
// Hence `nowSeconds` *and* `lastSeenMs`. Do not "tidy" one into the other.
//
// The copy every field carries follows design §11: the operator's own
// vocabulary, no forward write called an undo, and a failure that has no reason
// column says so rather than showing a blank cell.

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
import { describeCron, type Schedule } from './schedules.js'
import type { MemoryRow } from './memories.js'

/** The routes the Asks and From-the-team stacks need (design B1; go/httpapi/attention.go). */
export const ATTENTION_ENDPOINTS = {
  /** GET — `?state=open|all`, `?limit=`; `{"attention_requests": [...]}`. */
  list: '/agent/attention-requests',
  /** POST — a person acknowledges one request ("Got it" / "Dismiss"). */
  resolve: (id: string) => `/agent/attention-requests/${encodeURIComponent(id)}/resolve`,
}

/**
 * `ask` — a question a person owes an answer to; its job parks at
 * `awaiting_human`. `notice` — act-then-notify: the worker already did the
 * thing and is saying so, nothing parks, and a person acknowledges it
 * (engine migration 051).
 */
export type AttentionKind = 'ask' | 'notice'

// ---------------------------------------------------------------------------
// The attention request (go/agentdb.AttentionRequest's JSON, verbatim)
// ---------------------------------------------------------------------------

/**
 * One `request_human_attention` call: the message a worker wrote for a person.
 *
 * This is where the *sentence* lives — the delivery row knows only that it is
 * parked. Without this list the Asks stack still renders, just without the
 * words, which is most of its value.
 */
export interface AttentionRequest {
  id: string
  project: string
  session_id: string
  worker: string
  message: string
  /** Optional because rows from an engine older than migration 051, and
   *  fixtures, carry none — absent means `ask`, which is what those were. */
  kind?: AttentionKind
  /** Permalink minted at request time, so a later base-URL change cannot
   *  rewrite history. */
  session_url: string
  /** Where the notification went: `webhook`, or `none` for log-only. */
  channel: string
  delivered: boolean
  /** Unix SECONDS; 0 = never expires. */
  expires_at: number
  created_at: number
  /** Unix SECONDS; 0 while still open. */
  answered_at: number
  timed_out_at: number
}

const num = (v: unknown, fallback = 0): number =>
  typeof v === 'number' && Number.isFinite(v) ? v : fallback
const str = (v: unknown, fallback = ''): string => (typeof v === 'string' ? v : fallback)
const bool = (v: unknown, fallback = false): boolean => (typeof v === 'boolean' ? v : fallback)

export function coerceAttentionRequest(raw: unknown): AttentionRequest {
  const r = raw && typeof raw === 'object' && !Array.isArray(raw) ? (raw as Record<string, unknown>) : {}
  return {
    id: str(r.id),
    project: str(r.project),
    session_id: str(r.session_id),
    worker: str(r.worker),
    message: str(r.message),
    kind: r.kind === 'notice' ? 'notice' : 'ask',
    session_url: str(r.session_url),
    channel: str(r.channel),
    delivered: bool(r.delivered),
    expires_at: num(r.expires_at),
    created_at: num(r.created_at),
    answered_at: num(r.answered_at),
    timed_out_at: num(r.timed_out_at),
  }
}

/** A notice tells; an ask asks. Anything unmarked is an ask. */
export function isAttentionNotice(r: Pick<AttentionRequest, 'kind'>): boolean {
  return r.kind === 'notice'
}

/** Open = nobody has answered it and the sweep has not timed it out. */
export function isAttentionRequestOpen(
  r: Pick<AttentionRequest, 'answered_at' | 'timed_out_at'>,
): boolean {
  return r.answered_at === 0 && r.timed_out_at === 0
}

// ---------------------------------------------------------------------------
// The closed glyph set (design §3.6)
// ---------------------------------------------------------------------------

/** Spine glyphs, as the Desk names them. Rendered by spine.tsx. */
export const DESK_GLYPHS = ['agent', 'human', 'attention', 'failure', 'freeze'] as const
export type DeskGlyph = (typeof DESK_GLYPHS)[number]

// ---------------------------------------------------------------------------
// Stack 1 — asks
// ---------------------------------------------------------------------------

/**
 * How an ask leaves the stack, stated once and rendered by the page. A reply in
 * the thread closes the request and settles the parked job (engine
 * 2026-09-13); the stack is still computed from the *request*, so an older
 * engine that leaves the row parked shows the same thing.
 */
export const DESK_ASKS_CAVEAT =
  'An ask leaves this stack when you reply in its thread, dismiss it here, or it times out.'

/** One thing waiting for a person. */
export interface DeskAsk {
  /** Stable key: the delivery this ask parks. */
  id: string
  deliveryId: string
  requestId: string
  /** The worker that asked; '' when neither the request nor the subscription
   *  names one. */
  worker: string
  sessionId: string
  /** The permalink the request minted; '' when it carries none. */
  sessionUrl: string
  /** What the worker actually wrote, verbatim. */
  message: string
  /** Always `awaiting_human` — the vocabulary stays the vocabulary (§11.6). */
  status: string
  /** How long it has been waiting, in seconds. The clock genuinely runs. */
  waitingSeconds: number
  /** `2h 40m`. */
  waitingLabel: string
  /** Seconds until the request expires; null when it never expires. */
  expiresInSeconds: number | null
  /** `expires in 5h`, `expired`, or '' when it never expires. */
  expiresLabel: string
  /** `email-answerer · awaiting_human · 2h 40m`. */
  headline: string
  glyph: DeskGlyph
  /** Unix SECONDS — attention requests stamp seconds, not milliseconds (J1). */
  createdAt: number
  /** Always `'first-ask'`: every ask is a candidate, and `firsts.ts` picks
   *  whichever is chronologically earliest. See `DeskFirstRecord`. */
  firstKind: DeskFirstKind
}

// ---------------------------------------------------------------------------
// Stack 1b — notices ("from the team")
// ---------------------------------------------------------------------------

/**
 * One open notice: a worker telling a person what it did. There is no question
 * in it, so it is never shown as "nobody has answered these" — it is shown as a
 * note, and a person acknowledges it.
 */
export interface DeskNotice {
  /** The request id — also what "Got it" resolves. */
  id: string
  requestId: string
  worker: string
  sessionId: string
  sessionUrl: string
  message: string
  /** `note from architect`. */
  headline: string
  /** Unix SECONDS. */
  createdAt: number
  /** How long ago it was written, in seconds. */
  ageSeconds: number
}

// ---------------------------------------------------------------------------
// Stack 2 — changes
// ---------------------------------------------------------------------------

/** One configuration change made since the operator last looked. */
export interface DeskChange {
  id: string
  /** The full changelog entry, diff machinery and all (§15.10). */
  entry: ChangelogEntry
  /** `email-reviewer`, or `you` for a human/API edit. */
  actor: string
  /** True when a worker made it — the ember mark (§3.2). */
  byAgent: boolean
  /** `rewrote`, `retuned`, `published`… — the short verb §11.6 uses. */
  verb: string
  /** `email-answerer`, `schedule daily-brief`, `project settings`. */
  subject: string
  /** `email-reviewer rewrote email-answerer`. */
  sentence: string
  /** The rationale, or the design's honest `(no reason given)`. */
  reason: string
  /** True when the rationale is blank — §8's patchy rationales, said plainly. */
  noReason: boolean
  /** `+4 −1 lines`, or '' when the change carried no prompt diff. */
  diffLabel: string
  glyph: DeskGlyph
  /** Unix MILLISECONDS. */
  createdAt: number
  /**
   * Which "first of a kind" (design §3 G4, `firsts.ts`) this record would
   * narrate as, or `null` when it is not one of the seven — most changes
   * aren't. Set on every matching record, not only the project's actual
   * first; `firsts.ts` is the one that decides which is earliest.
   */
  firstKind: DeskFirstKind | null
}

// ---------------------------------------------------------------------------
// "Firsts" — the Desk narrating the first of a kind (design §3 G4)
// ---------------------------------------------------------------------------

/**
 * The seven kinds G4 narrates. Closed on purpose: `firsts.ts` writes one
 * sentence per kind, once, and a kind that is not in this list can never
 * narrate — there is no "8th first" a stray action string could smuggle in.
 */
export const DESK_FIRST_KINDS = [
  'first-worker',
  'first-memory',
  'first-schedule',
  'first-subscription',
  'first-rewrite',
  'first-ask',
  'first-revert',
] as const
export type DeskFirstKind = (typeof DESK_FIRST_KINDS)[number]

/**
 * One candidate for `firsts.ts` to consider: a record of a given kind, and
 * when it happened.
 *
 * `createdAtMs` is ALWAYS milliseconds, regardless of what the underlying
 * table stamps — this is the J1 hazard made harmless at the one seam that
 * has to compare timestamps of different kinds against each other to find
 * "the earliest". Asks are the case that matters: `attention_requests` (and
 * therefore `DeskAsk.createdAt`) stamp unix SECONDS, so it is multiplied by
 * 1000 here. Do not remove that multiplication because a record "looks like"
 * it is already in milliseconds — check the source table.
 */
export interface DeskFirstRecord {
  kind: DeskFirstKind
  createdAtMs: number
  /** The id of the row that should carry the sentence (a `DeskChange.id`, a
   *  `DeskAsk.id`, or a synthetic `memory:<created_at>` id). */
  id: string
}

/**
 * A config action's kind, or `null` when it is not one of the seven.
 *
 * Reverts are not their own action in the engine — §D's design reuses the
 * ordinary mutation verbs and the compensating write's rationale is the only
 * place a revert says what it is. The engine defaults that rationale to
 * `revert of <action> (seq <n>, event <id>)` when the caller supplies none
 * (`go/agentdb/config_revert.go`); the console's own revert control requires
 * a human-typed reason, so this match is necessarily a heuristic; a revert
 * whose rationale does not start this way narrates under its underlying
 * action's kind instead (e.g. a reverted worker rewrite still narrates as
 * `first-rewrite`), never as nothing.
 */
const REVERT_RATIONALE_RE = /^revert of /i

export function looksLikeRevertRationale(rationale: string): boolean {
  return REVERT_RATIONALE_RE.test(rationale.trim())
}

export function deskFirstKindForChange(
  entry: Pick<ChangelogEntry, 'action' | 'rationale'>,
): DeskFirstKind | null {
  if (looksLikeRevertRationale(entry.rationale)) return 'first-revert'
  switch (entry.action) {
    case 'worker_create':
      return 'first-worker'
    case 'subscription_create':
      return 'first-subscription'
    case 'schedule_create':
      return 'first-schedule'
    case 'worker_prompt_write':
    case 'project_prompt_write':
      return 'first-rewrite'
    default:
      return null
  }
}

/** The short verb §11.6 wants in a sentence, per config action. */
export function deskChangeVerb(action: string): string {
  switch (action) {
    case 'worker_create':
      return 'hired'
    case 'worker_update':
      return 'updated'
    case 'worker_enable':
      return 'enabled'
    case 'worker_disable':
      return 'disabled'
    case 'worker_freeze':
      return 'froze'
    case 'worker_unfreeze':
      return 'unfroze'
    case 'worker_delete':
      return 'retired'
    case 'worker_prompt_write':
    case 'project_prompt_write':
      return 'rewrote'
    case 'project_settings_put':
      return 'changed'
    case 'subscription_create':
    case 'schedule_create':
      return 'created'
    case 'subscription_update':
    case 'schedule_update':
      return 'retuned'
    case 'subscription_delete':
    case 'schedule_delete':
      return 'deleted'
    case 'image_create':
    case 'skill_create':
      return 'published'
    case 'topology_apply':
      return 'applied'
    default:
      return action
  }
}

/** What a change acted on, named as the operator controls it (§11.1). */
export function deskChangeSubject(entry: ChangelogEntry): string {
  const { kind, name } = entry.entity
  switch (kind) {
    case 'worker':
      return name
    case 'project-prompt':
      return 'the project prompt'
    case 'project-settings':
      return 'project settings'
    // A subscription or schedule is keyed by a uuid, which says nothing to a
    // person. The payload is the row's full state, so name what it wakes and
    // when — "numbers-clerk's schedule (At 18:00, on Thursday.)" — and keep the
    // id only when the payload has nothing better.
    case 'subscription': {
      const p = entry.event.payload ?? {}
      const worker = typeof p.worker === 'string' ? p.worker : ''
      const type = typeof p.event_type === 'string' ? p.event_type : ''
      if (worker !== '') return type === '' ? `${worker}'s subscription` : `${worker}'s subscription to ${type}`
      return name === '' ? 'a subscription' : `subscription ${name}`
    }
    case 'schedule': {
      const p = entry.event.payload ?? {}
      const worker = typeof p.worker === 'string' ? p.worker : ''
      const when = typeof p.cron === 'string' ? describeCron(p.cron) : null
      if (worker !== '') return when === null ? `${worker}'s schedule` : `${worker}'s schedule (${when.replace(/\.$/, '')})`
      return name === '' ? 'a schedule' : `schedule ${name}`
    }
    case 'image':
    case 'skill':
      return name
    case 'topology':
      return name === '' ? 'a topology' : `topology ${name}`
    default:
      return name === '' ? entry.action : name
  }
}

// ---------------------------------------------------------------------------
// Stack 3 — trouble
// ---------------------------------------------------------------------------

/** The three failure shapes the docs warn about, each with its own sentence. */
export type DeskTroubleKind = 'failed-deliveries' | 'schedule-halted' | 'freeze-refusal'

export interface DeskTrouble {
  id: string
  kind: DeskTroubleKind
  glyph: DeskGlyph
  /** `3 deliveries failed · worker invoice-parser`. */
  headline: string
  /** The honest second line: what is recorded, and what is not. */
  detail: string
  /** The worker this is about; '' when the group has no worker. */
  worker: string
  count: number
  /** Unix SECONDS of the oldest member of the group; 0 when unknown. */
  sinceSeconds: number
  /** The most recent session involved, for an "open last job" link; '' if none. */
  sessionId: string
}

/**
 * Said once per failed-delivery group when the group has nothing to say.
 * The column exists since engine migration 037 (RD20); a row can still be blank
 * — it failed before that migration, or the failure path recorded nothing — and
 * a fabricated reason would be worse than this sentence.
 */
export const DESK_NO_DELIVERY_REASON =
  'No reason is recorded on a delivery row for this group. ' +
  'The agentd log and the last job are where the reason is.'

/** Why a refusal is on this screen at all (playbook C8). */
export const DESK_FREEZE_REFUSAL_NOTE =
  'A frozen worker is an instrument. An agent trying to edit the thing that scores it is a ' +
  'signal worth reading, not an error to clear.'

/**
 * How many consecutive failed starts disable a schedule — the engine's
 * `agentdb.ScheduleMaxProvisionFailures`. Mirrored, not imported: this package
 * never depends on `go/`.
 */
export const SCHEDULE_MAX_PROVISION_FAILURES = 5

// ---------------------------------------------------------------------------
// The fold
// ---------------------------------------------------------------------------

export interface BuildDeskInput {
  deliveries: EventDelivery[]
  events: ProjectEvent[]
  subscriptions: Subscription[]
  configEvents: ConfigEvent[]
  attentionRequests: AttentionRequest[]
  /**
   * Schedule rows, read for their `provision_failures` streak. Optional
   * because a host without the schedules route still gets the other two
   * stacks; the halted-schedule line simply never appears.
   */
  schedules?: Schedule[]
  /**
   * Memory rows, read only for `created_at` (design §3 G4). Optional because a
   * host that has not wired the Memory read route into the Desk still gets
   * the other six firsts; `first-memory` simply never fires. Unix
   * MILLISECONDS — the `memories` table stamps milliseconds, like config
   * events (`go/agentdb/memories.go`'s own comment on the column).
   */
  memories?: { created_at: number }[]
  /** The clock, in unix seconds. */
  nowSeconds: number
  /**
   * High-water mark for the Changes stack, in unix MILLISECONDS. 0 means "the
   * operator has never looked", which shows everything fetched — a first visit
   * is not an empty screen.
   */
  lastSeenMs: number
  /** Used to build actor-session permalinks, as buildChangelog does. */
  projectId?: string
  /**
   * How many already-seen changes to keep BELOW the waterline. Default 10.
   *
   * §3's critique of this screen: "since you last looked" was a filter, not a
   * visible line — the operator could see what was new but never what it was
   * new relative to. The window is still the window (`changes` is unchanged);
   * these are the few rows underneath it that give the divider something to
   * divide.
   */
  earlierChangesLimit?: number
}

export interface Desk {
  asks: DeskAsk[]
  /** Open notices, newest first — reports that want reading, not answering. */
  notices: DeskNotice[]
  changes: DeskChange[]
  /**
   * Changes at or before the mark, newest first, capped. Empty when the
   * operator has never looked — a waterline at the top of the list, with
   * everything below it, says nothing.
   */
  earlierChanges: DeskChange[]
  trouble: DeskTrouble[]
  /**
   * Every "first of a kind" candidate the fold found (design §3 G4),
   * UNWINDOWED — built from the whole changelog and every open ask, not just
   * `changes`/`earlierChanges`'s capped tail, so a project older than the
   * `earlierChangesLimit` window still gets its firsts right. `firsts.ts`
   * reduces this list against a seen-set to decide what narrates.
   */
  firsts: DeskFirstRecord[]
}

/**
 * The whole Desk, in one pure fold.
 *
 * Asks are `awaiting_human` deliveries joined to *open* attention requests, by
 * session: a delivery whose request has been answered or timed out drops out of
 * the stack, which is the read-time answer to the parked-row wart. We do not
 * fix the row; we stop showing a stale row as if it were live.
 *
 * Changes are the changelog windowed to "since you last looked". The window is
 * applied *after* `buildChangelog`, never before: a diff is computed against
 * the previous state of the same key, and that previous state is usually older
 * than the window. Filtering first would silently produce a first-ever-version
 * entry for every prompt rewrite.
 *
 * Trouble collects the failure shapes with the sentences the docs already
 * wrote — a failed delivery has no reason column, a halted schedule has
 * `last_provision_error` precisely so a human can recover, and a freeze refusal
 * is a signal rather than a fault.
 */
export function buildDesk(input: BuildDeskInput): Desk {
  const { fresh, earlier, all } = buildDeskChangeStacks(input)
  const asks = buildDeskAsks(input)
  return {
    asks,
    notices: buildDeskNotices(input),
    changes: fresh,
    earlierChanges: earlier,
    trouble: buildDeskTrouble(input),
    firsts: buildDeskFirstRecords(all, asks, input.memories ?? []),
  }
}

/**
 * The fold's own tagged records, collapsed into the one flat list `firsts.ts`
 * reduces (design §3 G4). Every unit here is normalised to milliseconds
 * (`DeskFirstRecord.createdAtMs`) — see its doc comment for why that matters.
 *
 * `asks` here is `buildDeskAsks`'s output — OPEN asks only, joined to a live
 * attention request. That is deliberate, not an oversight: an ask that has
 * already been answered has no row left on the Desk to carry the sentence,
 * so `first-ask` can only ever narrate on a still-open one. The practical
 * consequence is that "first ask" means "the earliest ask that is STILL
 * open" rather than "the literal first ask this project ever asked" — the
 * same thing for a project narrating this in real time as records land, but
 * an approximation if this feature is switched on for the first time on a
 * project whose actual first ask has already been answered and closed.
 */
function buildDeskFirstRecords(
  changes: DeskChange[],
  asks: DeskAsk[],
  memories: { created_at: number }[],
): DeskFirstRecord[] {
  const out: DeskFirstRecord[] = []
  for (const change of changes) {
    if (change.firstKind) out.push({ kind: change.firstKind, createdAtMs: change.createdAt, id: change.id })
  }
  for (const ask of asks) {
    out.push({ kind: ask.firstKind, createdAtMs: ask.createdAt * 1000, id: ask.id })
  }
  for (const memory of memories) {
    out.push({ kind: 'first-memory', createdAtMs: memory.created_at, id: `memory:${memory.created_at}` })
  }
  return out
}

/**
 * Newest open request per session — a worker can ask twice in one session, and
 * the live question is the last one it asked.
 *
 * Exported because the shell's Desk badge counts asks, and an ask is this join,
 * not "an open attention request" (doc 21, X7): a request whose delivery has
 * moved on is not on the Desk, and a badge that counts one thing while the
 * stack lists another is a number an operator learns to distrust.
 */
export function openRequestsBySession(
  attentionRequests: AttentionRequest[],
): Map<string, AttentionRequest> {
  const openBySession = new Map<string, AttentionRequest>()
  for (const request of attentionRequests) {
    // A notice is not an ask: it parks no job and nobody owes it an answer.
    if (!isAttentionRequestOpen(request) || isAttentionNotice(request) || request.session_id === '') continue
    const held = openBySession.get(request.session_id)
    if (
      !held ||
      request.created_at > held.created_at ||
      (request.created_at === held.created_at && request.id > held.id)
    ) {
      openBySession.set(request.session_id, request)
    }
  }
  return openBySession
}

/**
 * How many asks the Desk would show, without building the rest of the fold.
 *
 * The same predicate `buildDeskAsks` applies, and deliberately the same
 * function: two implementations of "is this an ask" is exactly how the badge
 * and the stack came to disagree in the first place.
 */
export function countAsks(
  deliveries: EventDelivery[],
  attentionRequests: AttentionRequest[],
): number {
  return askPairs(deliveries, openRequestsBySession(attentionRequests)).length
}

/**
 * Which deliveries carry a live question, each with the request it waits on.
 *
 * A parked (`awaiting_human`) delivery with an open request, as always. And one
 * more shape the first real-model walk found (2026-09-13): a person replies in a
 * worker's thread, the reply settles the job to `ok`, the worker carries on in
 * the same session and asks again — handing over the draft it was asked for.
 * That second request is open and newer than the job's end, so it is a live
 * question; without this it showed on Activity and nowhere on the Desk.
 *
 * Still not on the Desk: a request older than the job's end (a job that failed
 * or moved on after asking — the stale row X7 is about). One delivery per
 * session, the newest, so a session is never listed twice.
 */
function askPairs(
  deliveries: EventDelivery[],
  openBySession: Map<string, AttentionRequest>,
): { delivery: EventDelivery; request: AttentionRequest; askedAgain: boolean }[] {
  const out: { delivery: EventDelivery; request: AttentionRequest; askedAgain: boolean }[] = []
  const settledBySession = new Map<string, EventDelivery>()
  const parkedSessions = new Set<string>()
  for (const delivery of deliveries) {
    const request = openBySession.get(delivery.session_id)
    if (!request) continue
    if (delivery.status === 'awaiting_human') {
      parkedSessions.add(delivery.session_id)
      out.push({ delivery, request, askedAgain: false })
      continue
    }
    if (delivery.status !== 'ok' || delivery.ended_at <= 0 || request.created_at < delivery.ended_at) continue
    const held = settledBySession.get(delivery.session_id)
    if (!held || delivery.ended_at > held.ended_at) settledBySession.set(delivery.session_id, delivery)
  }
  for (const [sessionId, delivery] of settledBySession) {
    if (parkedSessions.has(sessionId)) continue
    out.push({ delivery, request: openBySession.get(sessionId)!, askedAgain: true })
  }
  return out
}

function buildDeskAsks(input: BuildDeskInput): DeskAsk[] {
  const { deliveries, subscriptions, attentionRequests, nowSeconds } = input

  const openBySession = openRequestsBySession(attentionRequests)

  const workerBySubscription = new Map(subscriptions.map((s) => [s.id, s.worker]))

  const asks: DeskAsk[] = []
  // No open request ⇒ answered, timed out, or never asked. Either way that row
  // is not a live question, so it is not on the Desk.
  for (const { delivery, request, askedAgain } of askPairs(deliveries, openBySession)) {
    const askedFor = Math.max(0, nowSeconds - (request.created_at || nowSeconds))
    const waitingSeconds = askedAgain ? askedFor : (deliveryDurationSeconds(delivery, nowSeconds) ?? askedFor)
    // The job row says `ok` once a reply settled it; the question is still waiting.
    const status = askedAgain ? 'awaiting_human' : delivery.status
    const worker =
      request.worker || delivery.worker || workerBySubscription.get(delivery.subscription_id) || ''
    const expiresInSeconds = request.expires_at > 0 ? request.expires_at - nowSeconds : null

    asks.push({
      id: delivery.id,
      deliveryId: delivery.id,
      requestId: request.id,
      worker,
      sessionId: delivery.session_id,
      sessionUrl: request.session_url,
      message: request.message,
      status,
      waitingSeconds,
      waitingLabel: formatDuration(waitingSeconds),
      expiresInSeconds,
      expiresLabel:
        expiresInSeconds === null
          ? ''
          : expiresInSeconds <= 0
            ? 'expired'
            : `expires in ${formatDuration(expiresInSeconds)}`,
      headline: `${worker === '' ? 'a worker' : worker} · ${status} · ${formatDuration(waitingSeconds)}`,
      glyph: 'attention',
      createdAt: request.created_at,
      firstKind: 'first-ask',
    })
  }

  // Newest first, like every other list in this UI; ties by id so the order is
  // deterministic under a second-resolution clock.
  return asks.sort((a, b) => b.createdAt - a.createdAt || a.id.localeCompare(b.id))
}

/** Open notices, newest first. Not joined to deliveries: a notice parks none. */
function buildDeskNotices(input: BuildDeskInput): DeskNotice[] {
  const { attentionRequests, nowSeconds } = input
  return attentionRequests
    .filter((r) => isAttentionNotice(r) && isAttentionRequestOpen(r))
    .map((r) => ({
      id: r.id,
      requestId: r.id,
      worker: r.worker,
      sessionId: r.session_id,
      sessionUrl: r.session_url,
      message: r.message,
      headline: `note from ${r.worker === '' ? 'a worker' : r.worker}`,
      createdAt: r.created_at,
      ageSeconds: Math.max(0, nowSeconds - (r.created_at || nowSeconds)),
    }))
    .sort((a, b) => b.createdAt - a.createdAt || a.id.localeCompare(b.id))
}

/** The default depth of the "earlier" tail under the waterline. */
export const DESK_EARLIER_CHANGES_LIMIT = 10

function buildDeskChangeStacks(input: BuildDeskInput): {
  fresh: DeskChange[]
  earlier: DeskChange[]
  /** The full, unwindowed list — `buildDeskFirstRecords` needs every record,
   *  not just what the waterline currently shows. */
  all: DeskChange[]
} {
  const { lastSeenMs, earlierChangesLimit = DESK_EARLIER_CHANGES_LIMIT } = input
  const all = buildDeskChanges(input)
  const fresh = all.filter((change) => change.createdAt > lastSeenMs)
  // Never looked ⇒ everything is "new", so there is nothing for a line to
  // separate and no tail to show. Same rule `waterlineIndex` applies.
  if (lastSeenMs <= 0) return { fresh: all, earlier: [], all }
  const earlier = all.filter((change) => change.createdAt <= lastSeenMs)
  return {
    fresh,
    earlier: earlierChangesLimit <= 0 ? [] : earlier.slice(0, earlierChangesLimit),
    all,
  }
}

function buildDeskChanges(input: BuildDeskInput): DeskChange[] {
  const { configEvents, projectId = '' } = input
  return buildChangelog(configEvents, { projectId })
    .map((entry): DeskChange => {
      const byAgent = entry.actorWorker !== ''
      const actor = byAgent ? entry.actorWorker : 'you'
      const verb = deskChangeVerb(entry.action)
      const subject = deskChangeSubject(entry)
      const noReason = entry.rationale.trim() === ''
      return {
        id: entry.id,
        entry,
        actor,
        byAgent,
        verb,
        subject,
        sentence: `${actor} ${verb} ${subject}`.trimEnd(),
        // Not a scold: the screen telling the truth about §8's patchy
        // rationales, which is the argument for asking for one.
        reason: noReason ? '(no reason given)' : entry.rationale,
        noReason,
        diffLabel: entry.diff ? `+${entry.diff.added} −${entry.diff.removed} lines` : '',
        glyph: byAgent ? 'agent' : 'human',
        createdAt: entry.createdAt,
        firstKind: deskFirstKindForChange(entry),
      }
    })
}

function buildDeskTrouble(input: BuildDeskInput): DeskTrouble[] {
  const { deliveries, events, subscriptions, schedules = [] } = input
  const out: DeskTrouble[] = []

  // ── failed deliveries, grouped by worker ─────────────────────────────────
  const workerBySubscription = new Map(subscriptions.map((s) => [s.id, s.worker]))
  const failures = new Map<
    string,
    { count: number; since: number; sessionId: string; last: number; reason: string }
  >()
  for (const delivery of deliveries) {
    if (delivery.status !== 'failed') continue
    // The delivery row names its own worker (migration 024); the subscription
    // join is the fallback for pre-024 rows, and it is the leg that goes blank
    // when the subscription has since been deleted.
    const worker = delivery.worker || workerBySubscription.get(delivery.subscription_id) || ''
    const at = delivery.created_at || delivery.started_at || 0
    const reason = (delivery.failure_reason ?? '').trim()
    const held = failures.get(worker)
    if (!held) {
      failures.set(worker, {
        count: 1,
        since: at,
        sessionId: delivery.session_id,
        last: at,
        reason,
      })
      continue
    }
    held.count += 1
    if (at !== 0 && (held.since === 0 || at < held.since)) held.since = at
    if (at >= held.last) {
      held.last = at
      if (delivery.session_id !== '') held.sessionId = delivery.session_id
      // The freshest reason wins — a group is read to answer "what is wrong
      // NOW" — but a blank never overwrites one that says something.
      if (reason !== '') held.reason = reason
    } else if (held.reason === '') {
      held.reason = reason
    }
  }
  for (const [worker, group] of [...failures.entries()].sort(
    (a, b) => b[1].count - a[1].count || a[0].localeCompare(b[0]),
  )) {
    out.push({
      id: `failed:${worker}`,
      kind: 'failed-deliveries',
      glyph: 'failure',
      headline:
        worker === ''
          ? `${group.count} ${group.count === 1 ? 'delivery' : 'deliveries'} failed · the subscription that started them is gone`
          : `${group.count} ${group.count === 1 ? 'delivery' : 'deliveries'} failed · worker ${worker}`,
      // The engine's own words when it has any (RD20); the honest fallback when
      // it has none. The count is in the headline, so this line is only ever
      // "why".
      detail: group.reason === '' ? DESK_NO_DELIVERY_REASON : group.reason,
      worker,
      count: group.count,
      sinceSeconds: group.since,
      sessionId: group.sessionId,
    })
  }

  // ── schedules halted by the five-strike rule ─────────────────────────────
  for (const schedule of [...schedules]
    .filter((s) => (s.provision_failures ?? 0) >= SCHEDULE_MAX_PROVISION_FAILURES)
    .sort((a, b) => a.worker.localeCompare(b.worker) || a.id.localeCompare(b.id))) {
    const failuresCount = schedule.provision_failures ?? 0
    const reason = (schedule.last_provision_error ?? '').trim()
    out.push({
      id: `schedule:${schedule.id}`,
      kind: 'schedule-halted',
      glyph: 'failure',
      headline: `schedule ${schedule.worker} (${schedule.cron}) disabled after ${failuresCount} failed starts`,
      detail:
        reason === ''
          ? 'No reason is recorded on the schedule row — last_provision_error is empty.'
          : `last reason: ${reason}`,
      worker: schedule.worker,
      count: failuresCount,
      sinceSeconds: schedule.updated_at,
      sessionId: '',
    })
  }

  // ── freeze refusals ──────────────────────────────────────────────────────
  const refusals = new Map<
    string,
    { target: string; actor: string; count: number; since: number; sessionId: string; last: number }
  >()
  for (const event of events) {
    if (event.type !== 'worker.freeze_refused') continue
    const target = frozenTargetFromText(event.text)
    const actor = event.envelope.worker
    // The key's separator must be a character no worker name can contain. It
    // was a raw NUL, which made this whole file grep as binary; U+001F holds
    // the same guarantee and does not.
    const key = `${target}\u001f${actor}`
    const at = event.occurred_at || event.created_at || 0
    const held = refusals.get(key)
    if (!held) {
      refusals.set(key, {
        target,
        actor,
        count: 1,
        since: at,
        sessionId: event.envelope.session_id,
        last: at,
      })
      continue
    }
    held.count += 1
    if (at !== 0 && (held.since === 0 || at < held.since)) held.since = at
    if (at >= held.last) {
      held.last = at
      if (event.envelope.session_id !== '') held.sessionId = event.envelope.session_id
    }
  }
  for (const group of [...refusals.values()].sort(
    (a, b) => b.count - a.count || a.target.localeCompare(b.target) || a.actor.localeCompare(b.actor),
  )) {
    const target = group.target === '' ? 'a frozen worker' : group.target
    const from = group.actor === '' ? '' : ` from ${group.actor}`
    out.push({
      id: `freeze:${group.target}:${group.actor}`,
      kind: 'freeze-refusal',
      glyph: 'freeze',
      headline: `${target} refused ${group.count} ${group.count === 1 ? 'rewrite' : 'rewrites'}${from}`,
      detail: DESK_FREEZE_REFUSAL_NOTE,
      worker: group.target,
      count: group.count,
      sinceSeconds: group.since,
      sessionId: group.sessionId,
    })
  }

  return out
}

/**
 * The frozen worker a refusal was aimed at, read out of the event text.
 *
 * The engine writes `Refused <tool> against frozen worker "fee-scorer".` and
 * puts the *attempting* worker in the envelope, so the target exists only in
 * the sentence. Parsing prose is not lovely; the alternative is not naming the
 * instrument that was defended, which is the whole point of the line. A text
 * this does not recognise yields '' and the item reads "a frozen worker".
 */
export function frozenTargetFromText(text: string): string {
  const m = /frozen worker "([^"]+)"/.exec(text) ?? /frozen worker “([^”]+)”/.exec(text)
  return m ? m[1]! : ''
}

// ---------------------------------------------------------------------------
// Written down — what the team concluded
// ---------------------------------------------------------------------------

/** One memory as the Desk shows it: what it is, who wrote it, its opening. */
export interface DeskNote {
  id: string
  /** `summary · review-2026-w37`, `rolling-summary · copywriter`. */
  title: string
  /** The worker that wrote it, or a plain phrase when no worker did. */
  writer: string
  /** Unix milliseconds (memories stamp ms). */
  createdAtMs: number
  /** The route's snippet — at most MEMORY_SNIPPET_CHARS; the Desk clamps it. */
  excerpt: string
  sessionId: string
}

/**
 * The newest memories, as the Desk's "Written down" stack.
 *
 * Memory is where a worker's conclusions land, and it is not a configuration
 * change, so before this the Desk — the page a first-time user is sent to
 * after the team forms — showed the asks and the config log and not one thing
 * the team had concluded (real-model walk, 2026-09-13).
 */
export function deskNotes(rows: MemoryRow[], limit = 5): DeskNote[] {
  return rows.slice(0, Math.max(0, limit)).map((m) => {
    const kind = m.labels.kind ?? ''
    const which = m.labels.name ?? m.labels.worker ?? ''
    const title = [kind, which].filter((part) => part !== '').join(' · ') || 'a note'
    return {
      id: m.id,
      title,
      writer:
        m.created_by_worker !== ''
          ? m.created_by_worker
          : m.created_by_session !== ''
            ? 'a chat session'
            : 'you or the console',
      createdAtMs: m.created_at,
      excerpt: m.snippet,
      sessionId: m.created_by_session,
    }
  })
}
