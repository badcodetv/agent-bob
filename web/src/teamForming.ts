// teamForming — the pure half of "Your team is forming" and "Run a cycle now".
//
// The moment after Approve is the moment a new project is most watchable and,
// until this existed, least watched: the architect started (or did not), and
// the screen said nothing more. What happens next is visible in three reads
// the console already makes elsewhere — the worker list, the delivery (job)
// list, and the event spine — so this module folds those three into one
// answer: where is the architect, and who has it hired so far.
//
// No React here: it is pure so the tier line holds and so every rule below is
// pinned by a plain test.

import { DEFAULT_ARCHITECT_NAME } from './charter.js'
import type { EventDelivery, ProjectEvent } from './events.js'
import { collectEnvelopes } from './jobprogress.js'
import { describeCron, type Schedule } from './schedules.js'
import { getToolDisplayName, stripMcpPrefix } from './tool-formatters.js'
import type { Worker } from './workers.js'

/** The onboarding worker (go/topology's OnboardingWorker). Never "the team". */
export const INTERVIEWER_WORKER_NAME = 'interviewer'

/**
 * Where the architect's first run is.
 *
 * - `starting`  no job for it exists yet. The router picks the event up within
 *               seconds; the container takes longer.
 * - `designing` its job is queued or running.
 * - `ready`     its job finished, or parked to ask the human something — both
 *               mean the architect has said what it built.
 * - `failed`    its job failed. The reason is on the delivery.
 */
export type TeamFormingPhase = 'starting' | 'designing' | 'ready' | 'failed'

/** A worker's live state, as cheaply as the delivery list can say it. */
export type MemberStatus = 'running' | 'queued' | 'waiting-on-you' | 'finished' | 'failed' | 'idle'

export interface TeamMember {
  name: string
  /** The first non-empty line of the description, else of the prompt. */
  line: string
  enabled: boolean
  status: MemberStatus
  /** Newest job's start (or creation) time, unix seconds; 0 when it never ran. */
  lastRunAt: number
}

export interface TeamFormingState {
  phase: TeamFormingPhase
  /** The architect's newest job, when there is one. */
  architectDelivery: EventDelivery | null
  architect: TeamMember | null
  /** Everyone else, oldest hire first — the order they appeared in. */
  members: TeamMember[]
}

/** The first non-empty line, trimmed of markdown heading marks, capped. */
export function firstLine(text: string, max = 140): string {
  for (const raw of text.split('\n')) {
    const line = raw.replace(/^#+\s*/, '').trim()
    if (line !== '') return line.length > max ? `${line.slice(0, max - 1).trimEnd()}…` : line
  }
  return ''
}

/** What a delivery's status means for the worker it belongs to. */
export function memberStatusOf(delivery: EventDelivery | undefined): MemberStatus {
  if (!delivery) return 'idle'
  switch (delivery.status) {
    case 'running':
      return 'running'
    case 'pending':
    case 'rate_limited':
      return 'queued'
    case 'awaiting_human':
      return 'waiting-on-you'
    case 'ok':
      return 'finished'
    case 'failed':
      return 'failed'
    default:
      return 'idle'
  }
}

/** Plain words for a member status, for the list. */
export function describeMemberStatus(status: MemberStatus): string {
  switch (status) {
    case 'running':
      return 'working now'
    case 'queued':
      return 'queued'
    case 'waiting-on-you':
      return 'waiting for you'
    case 'finished':
      return 'finished its last job'
    case 'failed':
      return 'its last job failed'
    case 'idle':
      return 'has not run yet'
  }
}

/** Newest delivery per worker. Deliveries arrive newest-first, but this does
 *  not rely on it. */
export function newestDeliveryByWorker(deliveries: EventDelivery[]): Map<string, EventDelivery> {
  const out = new Map<string, EventDelivery>()
  for (const d of deliveries) {
    if (d.worker === '') continue
    const seen = out.get(d.worker)
    if (!seen || deliveryTime(d) > deliveryTime(seen)) out.set(d.worker, d)
  }
  return out
}

function deliveryTime(d: EventDelivery): number {
  return d.started_at > 0 ? d.started_at : d.created_at
}

function toMember(w: Worker, newest: Map<string, EventDelivery>): TeamMember {
  const d = newest.get(w.name)
  return {
    name: w.name,
    line: firstLine(w.description) || firstLine(w.system_prompt),
    enabled: w.enabled,
    status: memberStatusOf(d),
    lastRunAt: d ? deliveryTime(d) : 0,
  }
}

/**
 * Fold the three reads into one state.
 *
 * `sinceSeconds`, when given, ignores architect jobs created before it — so a
 * project whose architect ran last week does not read as "ready" the instant
 * a fresh approval's run is still starting. The onboarding screen passes the
 * charter's approval time.
 */
export function summariseTeamForming(input: {
  workers: Worker[]
  deliveries: EventDelivery[]
  architectName?: string
  sinceSeconds?: number
}): TeamFormingState {
  const architectName = (input.architectName ?? '').trim() || DEFAULT_ARCHITECT_NAME
  const since = input.sinceSeconds ?? 0
  const newest = newestDeliveryByWorker(input.deliveries.filter((d) => d.created_at >= since || d.worker !== architectName))

  const architectWorker = input.workers.find((w) => w.name === architectName) ?? null
  const architectDelivery = newest.get(architectName) ?? null

  let phase: TeamFormingPhase
  switch (memberStatusOf(architectDelivery ?? undefined)) {
    case 'idle':
      phase = 'starting'
      break
    case 'running':
    case 'queued':
      phase = 'designing'
      break
    case 'finished':
    case 'waiting-on-you':
      phase = 'ready'
      break
    case 'failed':
      phase = 'failed'
      break
  }

  const members = input.workers
    .filter((w) => w.name !== architectName && w.name !== INTERVIEWER_WORKER_NAME)
    .slice()
    .sort((a, b) => a.created_at - b.created_at || a.name.localeCompare(b.name))
    .map((w) => toMember(w, newest))

  return {
    phase,
    architectDelivery,
    architect: architectWorker ? toMember(architectWorker, newest) : null,
    members,
  }
}

/** Newest events first, capped — the "what just happened" strip. Config
 *  churn is left out: the changelog is where that is read. */
export function recentActivity(events: ProjectEvent[], limit = 6): ProjectEvent[] {
  return events
    .filter((e) => e.type !== 'config.changed')
    .slice()
    .sort((a, b) => (b.occurred_at || b.created_at) - (a.occurred_at || a.created_at))
    .slice(0, limit)
}

// ---------------------------------------------------------------------------
// The architect's first run, step by step
// ---------------------------------------------------------------------------

/** One line of "what the architect is doing". */
export interface FormingStep {
  /** Stable within one run: the tool call id, or a fixed key for the phases. */
  key: string
  text: string
  /** True for the newest step while the run is still going. */
  current: boolean
}

const str = (v: unknown): string => (typeof v === 'string' ? v.trim() : '')

/**
 * One tool call in words a person who has never seen the tool list can read.
 * '' means "not worth a line" — a validation call, or a tool with nothing to say.
 */
export function describeFormingTool(toolName: string, input: Record<string, unknown>): string {
  const tool = stripMcpPrefix(toolName)
  const name = str(input.name)
  const worker = str(input.worker)
  switch (tool) {
    case 'worker_list':
      return 'Looking at who is on the team'
    case 'worker_get':
      return name === '' ? 'Reading a worker' : `Reading ${name}`
    case 'worker_create':
      return name === '' ? 'Creating a worker' : `Creating ${name}`
    case 'worker_update':
      return name === '' ? 'Adjusting a worker' : `Adjusting ${name}`
    case 'worker_prompt_write':
      return name === '' ? "Writing a worker's instructions" : `Writing ${name}'s instructions`
    case 'worker_delete':
      return name === '' ? 'Removing a worker' : `Removing ${name}`
    case 'schedule_create': {
      const when = describeCron(str(input.cron))
      const whenText = when === null ? '' : ` — ${when.replace(/\.$/, '').toLowerCase()}`
      return worker === '' ? `Setting a schedule${whenText}` : `Putting ${worker} on a schedule${whenText}`
    }
    case 'schedule_update':
    case 'schedule_delete':
      return 'Adjusting a schedule'
    case 'subscription_create': {
      const type = str(input.event_type)
      if (worker === '') return 'Wiring a trigger'
      return type === '' ? `Wiring ${worker} to wake on an event` : `Wiring ${worker} to wake on ${type}`
    }
    case 'subscription_update':
    case 'subscription_delete':
      return 'Adjusting a trigger'
    case 'schedule_list':
    case 'subscription_list':
      return 'Checking what wakes whom'
    case 'memory_current':
    case 'memory_search':
    case 'memory_get':
      return 'Reading what the project has written down'
    case 'memory_create': {
      const labels = input.labels && typeof input.labels === 'object' ? (input.labels as Record<string, unknown>) : {}
      const kind = str(labels.kind)
      return kind === '' ? 'Writing a note down' : `Writing a note down: ${kind.replace(/-/g, ' ')}`
    }
    case 'config_history':
      return 'Checking what changed last time'
    case 'project_prompt_read':
      return 'Reading the project background'
    case 'project_prompt_write':
      return 'Updating the project background'
    case 'request_human_attention':
      return 'Writing you a note about what it did'
    case 'charter_validate':
      return ''
    default:
      return getToolDisplayName(toolName, input)
  }
}

/**
 * The architect's run as a short list of steps, newest last, capped to the
 * last `limit`.
 *
 * Read from the run's `query-events` (flushed every couple of seconds while it
 * runs), so it is what the architect has actually DONE — never a guess at what
 * it will do next. Before its job exists the one step is the container
 * starting; once the job is running but has called nothing yet, it is reading
 * the goal. Repeated identical lines collapse into one: "Reading what the
 * project has written down" three times says nothing the first did not.
 */
export function summariseFormingSteps(input: {
  phase: TeamFormingPhase
  /** The architect's run's `GET /agent/session/{id}/query-events` response, or null. */
  events: unknown
  limit?: number
}): FormingStep[] {
  const { phase, events, limit = 5 } = input
  const going = phase === 'starting' || phase === 'designing'
  const steps: Omit<FormingStep, 'current'>[] = [{ key: 'starting', text: 'Starting the architect' }]
  if (phase !== 'starting') {
    steps.push({ key: 'reading', text: 'Reading your goal and the charter' })
    let n = 0
    for (const env of collectEnvelopes(events)) {
      if (env.type !== 'tool_use_start') continue
      const toolName = str(env.data.toolName)
      const toolInput =
        env.data.input && typeof env.data.input === 'object' ? (env.data.input as Record<string, unknown>) : {}
      const text = toolName === '' ? '' : describeFormingTool(toolName, toolInput)
      n += 1
      if (text === '' || steps[steps.length - 1]!.text === text) continue
      steps.push({ key: str(env.data.toolCallId) || `step-${n}`, text })
    }
  }
  return steps.slice(-Math.max(1, limit)).map((step, i, all) => ({
    ...step,
    current: going && i === all.length - 1,
  }))
}

// ---------------------------------------------------------------------------
// Run a cycle now
// ---------------------------------------------------------------------------

/** POST /agent/schedules/{id}/run — agentd's schedulerun.go. */
export const scheduleRunEndpoint = (id: string): string =>
  `/agent/schedules/${encodeURIComponent(id)}/run`

/** What the route answers. `outcome` is one of the engine's four words. */
export interface ScheduleRunResult {
  schedule_id: string
  worker: string
  target_session: string
  outcome: string
  reason: string
  event_id: string
}

export function coerceScheduleRunResult(raw: unknown): ScheduleRunResult {
  const r = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
  const s = (v: unknown) => (typeof v === 'string' ? v : '')
  return {
    schedule_id: s(r.schedule_id),
    worker: s(r.worker),
    target_session: s(r.target_session),
    outcome: s(r.outcome),
    reason: s(r.reason),
    event_id: s(r.event_id),
  }
}

/** Who a schedule wakes, in words: a worker's name, or the named session. */
export function scheduleTarget(s: Pick<Schedule, 'worker' | 'target_session'>): string {
  if (s.worker !== '') return s.worker
  if (s.target_session) return `session ${s.target_session}`
  return 'an unknown target'
}

/**
 * The schedules one cycle fires: every ENABLED schedule, in a stable order.
 * A disabled schedule was switched off by someone — possibly the scheduler,
 * with a reason in the changelog — and a cycle must not quietly overrule that.
 */
export function planCycle(schedules: Schedule[]): Schedule[] {
  return schedules
    .filter((s) => s.enabled)
    .slice()
    .sort((a, b) => scheduleTarget(a).localeCompare(scheduleTarget(b)) || a.id.localeCompare(b.id))
}

/** One line of the confirmation a cycle leaves behind. */
export interface CycleLine {
  target: string
  ok: boolean
  text: string
}

/** Plain words for one schedule's run outcome. */
export function describeRunOutcome(target: string, outcome: string, reason = ''): CycleLine {
  switch (outcome) {
    case 'requested':
      return { target, ok: true, text: `${target} — started` }
    case 'already_fired':
      return { target, ok: true, text: `${target} — already ran this minute` }
    case 'busy':
      return { target, ok: false, text: `${target} — busy, nothing sent${reason ? ` (${reason})` : ''}` }
    case 'target_missing':
      return { target, ok: false, text: `${target} — gone; its schedule was switched off` }
    default:
      return { target, ok: false, text: `${target} — ${reason || outcome || 'did not start'}` }
  }
}
