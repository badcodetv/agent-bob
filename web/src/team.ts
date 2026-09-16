// The Desk's team view — who is on the project, what each one is for, and what
// wakes them (design: the 2026-09-16 "team dashboard" mockup, layout A).
//
// Everything here is a pure fold over rows the Desk already fetches: workers,
// schedules, subscriptions, and the Activity records. It adds no request.
//
// The vocabulary rule is the console's own (design §11): a clock is "Every day
// at 07:30", a subscription is "When scout finishes". The raw event type and
// filter still ride along as the detail line, because a sentence that hides the
// thing it summarises cannot be checked against it.

import type { ActivityRecord } from './activity.js'
import type { Subscription } from './events.js'
import { describeCron, type Schedule } from './schedules.js'
import { agoShort } from './timefmt.js'
import type { Worker } from './workers.js'

/** How long the role line may run before it is cut. */
export const ROLE_LINE_MAX_CHARS = 220

/** How many Activity records the Desk's "What happened" column shows. */
export const RECENT_ACTIVITY_LIMIT = 10

/**
 * One line saying what a worker is for.
 *
 * The worker's `description` when it has one — that field exists for exactly
 * this. Otherwise the first sentence of its prompt, skipping markdown headings,
 * because a worker written by another worker often has no description at all
 * and "no summary" would hide the worker's purpose behind a click.
 *
 * Note the description is a claim, not a proof: a worker that rewrites another
 * worker's prompt need not update its description, so the two can drift.
 */
export function workerRoleLine(worker: Pick<Worker, 'description' | 'system_prompt'>, maxChars = ROLE_LINE_MAX_CHARS): string {
  const description = collapse(worker.description)
  if (description !== '') return clamp(description, maxChars)
  const paragraph = worker.system_prompt
    .split(/\n\s*\n/)
    .map((p) =>
      p
        .split('\n')
        .filter((line) => !/^\s*(#|---|```)/.test(line))
        .join(' '),
    )
    .map(collapse)
    .find((p) => p !== '')
  if (paragraph === undefined) return ''
  const sentence = paragraph.match(/^.+?[.!?](?=\s|$)/)
  return clamp(sentence ? sentence[0] : paragraph, maxChars)
}

// ---------------------------------------------------------------------------
// What wakes the team
// ---------------------------------------------------------------------------

export interface WakeRule {
  /** The schedule or subscription id. */
  id: string
  kind: 'clock' | 'event'
  /** The worker it wakes; '' for a session-mode schedule. */
  worker: string
  /** For a session-mode schedule, the session NAME it messages; '' otherwise. */
  targetSession: string
  /** `Every day at 07:30`, `When scout finishes`. */
  sentence: string
  /** The exact thing the sentence summarises: `0 7 * * *`, `worker.finished · worker=scout`. */
  detail: string
  enabled: boolean
}

/**
 * A clock in words: `Every day at 07:30`, `On weekdays at 09:00`.
 *
 * `describeCron` answers `At 07:30, every day.` — right under an input field,
 * where the time is the thing being typed, but backwards in a list read by
 * *when*, so the scope moves to the front here. An expression it cannot parse
 * is shown as itself rather than hidden.
 */
export function describeClock(cron: string): string {
  const described = describeCron(cron)
  if (described === null) return cron.trim()
  const text = described.replace(/\.$/, '')
  const split = text.match(/^(At [^,]+), (every day|on weekdays|at weekends|on [A-Za-z, ]+|from [A-Za-z ]+)$/)
  if (split === null) return text
  const scope = split[2]
  return `${scope.charAt(0).toUpperCase()}${scope.slice(1)} ${split[1].charAt(0).toLowerCase()}${split[1].slice(1)}`
}

/** A subscription in words, plus the raw type and filter it summarises. */
export function describeHandler(sub: Pick<Subscription, 'event_type' | 'filter' | 'max_firings_per_hour'>): {
  sentence: string
  detail: string
} {
  const type = sub.event_type.trim()
  const filter = sub.filter ?? {}
  const filterWorker = typeof filter.worker === 'string' ? filter.worker : ''

  let sentence: string
  if (type === 'worker.finished') {
    sentence = filterWorker !== '' ? `When ${filterWorker} finishes` : 'When any worker finishes'
  } else if (type === '*') {
    sentence = 'When anything happens'
  } else if (type.endsWith('.*')) {
    sentence = `When any ${type.slice(0, -2)} event happens`
  } else {
    sentence = `When ${type} happens`
  }

  const parts = [type]
  const pairs = Object.entries(filter)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
  if (pairs.length > 0) parts.push(pairs.join(' '))
  if (sub.max_firings_per_hour > 0) parts.push(`max ${sub.max_firings_per_hour}/hour`)
  return { sentence, detail: parts.join(' · ') }
}

export interface WakeRules {
  clocks: WakeRule[]
  handlers: WakeRule[]
}

/** Every schedule and subscription, as sentences, in a stable order. */
export function buildWakeRules(schedules: Schedule[], subscriptions: Subscription[]): WakeRules {
  const clocks = schedules.map<WakeRule>((s) => ({
    id: s.id,
    kind: 'clock',
    worker: s.worker,
    targetSession: s.worker === '' ? (s.target_session ?? '') : '',
    sentence: describeClock(s.cron),
    detail: s.cron.trim(),
    enabled: s.enabled,
  }))
  const handlers = subscriptions.map<WakeRule>((sub) => {
    const { sentence, detail } = describeHandler(sub)
    return {
      id: sub.id,
      kind: 'event',
      worker: sub.worker,
      targetSession: '',
      sentence,
      detail,
      enabled: sub.enabled,
    }
  })
  const byWorker = (a: WakeRule, b: WakeRule) =>
    a.worker.localeCompare(b.worker) || a.sentence.localeCompare(b.sentence) || a.id.localeCompare(b.id)
  return { clocks: clocks.sort(byWorker), handlers: handlers.sort(byWorker) }
}

/** The rules that wake one worker, clocks first. */
export function wakeRulesFor(worker: string, rules: WakeRules): WakeRule[] {
  return [...rules.clocks, ...rules.handlers].filter((r) => r.worker === worker)
}

// ---------------------------------------------------------------------------
// Worker status
// ---------------------------------------------------------------------------

export type WorkerStatusTone = 'agent' | 'attention' | 'failure' | 'idle' | 'off'

export interface WorkerStatus {
  tone: WorkerStatusTone
  /** `waiting on you`, `last run failed · 2h ago`, `active 12m ago`, `not run yet`. */
  label: string
}

/**
 * A worker's state in three words, from the newest Activity record naming it.
 *
 * `records` must be newest first — `buildActivity`'s order — so the first match
 * is the latest thing that worker did.
 */
export function workerStatus(
  worker: Pick<Worker, 'name' | 'enabled'>,
  records: ActivityRecord[],
  nowMs: number,
): WorkerStatus {
  if (!worker.enabled) return { tone: 'off', label: 'switched off' }
  const latest = records.find((r) => r.worker === worker.name)
  if (latest === undefined) return { tone: 'idle', label: 'not run yet' }
  const ago = agoShort(latest.atMs, nowMs)
  const when = ago === 'now' ? 'just now' : `${ago} ago`
  if (latest.glyph === 'attention') return { tone: 'attention', label: 'waiting on you' }
  if (latest.glyph === 'failure') return { tone: 'failure', label: `last run failed · ${when}` }
  return { tone: 'agent', label: `active ${when}` }
}

// ---------------------------------------------------------------------------

function collapse(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

function clamp(text: string, maxChars: number): string {
  if (text.length <= maxChars) return text
  const cut = text.slice(0, maxChars)
  const lastSpace = cut.lastIndexOf(' ')
  return `${(lastSpace > maxChars * 0.6 ? cut.slice(0, lastSpace) : cut).replace(/[\s,;:.]+$/, '')}…`
}
