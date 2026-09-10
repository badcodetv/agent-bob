// Git projection — the browser-side mirror of `GET /agent/git-projection`
// (design/2026-09-09-git-projection.md ticket G16; engine:
// go/httpapi/gitprojectionstatus.go).
//
// Pure: no React, no window, no fetch. The panel is
// components/GitProjectionPanel.tsx.
//
// What this module is FOR. The projection is a background loop that publishes a
// project's configuration to a git repository. Every way it can stop working is
// silent: agents carry on, the console carries on, and the repository quietly
// stops matching reality. So this file's real content is not the types — it is
// `describeGitProjection`, which turns one health word into a sentence that
// says what actually happened and what a human has to do about it. Vagueness
// here ("something went wrong") is the failure, not a stylistic lapse.
//
// The classification itself lives in the ENGINE and is not repeated here. The
// state row stores an error string with no code, so the engine matches its own
// sentinel text and pins that with a test; a second, drifting copy of that
// matching in the browser would be a way for the console to disagree with the
// server about whether a project is broken.

/** Default endpoint for the projection-status read. */
export const GIT_PROJECTION_ENDPOINT = '/agent/git-projection'

/** The closed set of health words the server emits (Go: gitprojectionstatus.go). */
export const GIT_PROJECTION_HEALTHS = [
  'off',
  'unknown',
  'ok',
  'unrenderable',
  'diverged',
  'push_failing',
  'quarantined',
  'failing',
] as const

export type GitProjectionHealth = (typeof GIT_PROJECTION_HEALTHS)[number]

/** How loudly the panel should say it. `off` is its own thing on purpose: a
 *  project that does not publish is a decision, and painting it as a warning
 *  would train operators to ignore the one row that matters. */
export type GitProjectionSeverity = 'off' | 'ok' | 'info' | 'warning' | 'error'

/** One file a run had something to say about. */
export interface GitProjectionNote {
  /** Repo-relative path. */
  path: string
  reason: string
  at: number
}

/** The `GET /agent/git-projection` body. */
export interface GitProjectionStatus {
  project: string
  /** False exactly when no repository is configured. */
  enabled: boolean
  /** The repository, with any embedded credential already stripped server-side. */
  remote: string
  /** Effective values — the server applies its defaults before answering. */
  branch: string
  subfolder: string
  /** The NAME of the environment variable holding the push token. Never a token. */
  token_env: string
  /** A clickable https link, or '' when the remote is not a shape we can turn
   *  into one. */
  browse_url: string
  /** False when the deployment cannot report projection state at all. */
  state_available: boolean
  /** The config-log sequence the working tree reflects. */
  last_rendered_seq: number
  last_rendered_sha: string
  last_pushed_sha: string
  last_imported_sha: string
  updated_at: number
  health: GitProjectionHealth
  /** The engine's own words for the last failure. Empty when the last render
   *  and the last push both succeeded. */
  last_error: string
  last_error_at: number
  /** The dotted field path a render refusal named, e.g.
   *  `ProjectSettings.AttentionChannel.url`. Never accompanied by the value —
   *  the engine's error does not carry one. */
  unrenderable_field: string
  /** Something is committed locally that the remote has not got. */
  push_behind: boolean
  /** Pushes rejected wholesale because a file did not parse. */
  quarantine: GitProjectionNote[]
  /** Edits a human made in git that had no effect. */
  ignored: GitProjectionNote[]
}

// ---------------------------------------------------------------------------
// Coercion — fill anything the server omitted so the panel never branches on
// undefined, the same posture as coerceCharter and coerceWorker.
// ---------------------------------------------------------------------------

const str = (v: unknown, fallback = ''): string => (typeof v === 'string' ? v : fallback)
const bool = (v: unknown): boolean => v === true
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
const record = (v: unknown): Record<string, unknown> | null =>
  v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null

/** True for one of the health words the server actually emits. */
export function isGitProjectionHealth(v: unknown): v is GitProjectionHealth {
  return typeof v === 'string' && (GIT_PROJECTION_HEALTHS as readonly string[]).includes(v)
}

export function coerceGitProjectionNote(raw: unknown): GitProjectionNote {
  const r = record(raw) ?? {}
  return { path: str(r.path), reason: str(r.reason), at: num(r.at) }
}

function notes(raw: unknown): GitProjectionNote[] {
  return Array.isArray(raw) ? raw.map(coerceGitProjectionNote) : []
}

export function coerceGitProjectionStatus(raw: unknown): GitProjectionStatus {
  const r = record(raw) ?? {}
  const enabled = bool(r.enabled)
  // An unrecognised health word means the server is newer than this build. It
  // is reported as `unknown` rather than guessed at: telling an operator a
  // project is fine because we could not read the word is the one answer that
  // must never be produced.
  const health: GitProjectionHealth = isGitProjectionHealth(r.health)
    ? r.health
    : enabled
      ? 'unknown'
      : 'off'
  return {
    project: str(r.project),
    enabled,
    remote: str(r.remote),
    branch: str(r.branch),
    subfolder: str(r.subfolder),
    token_env: str(r.token_env),
    browse_url: str(r.browse_url),
    state_available: bool(r.state_available),
    last_rendered_seq: num(r.last_rendered_seq),
    last_rendered_sha: str(r.last_rendered_sha),
    last_pushed_sha: str(r.last_pushed_sha),
    last_imported_sha: str(r.last_imported_sha),
    updated_at: num(r.updated_at),
    health,
    last_error: str(r.last_error),
    last_error_at: num(r.last_error_at),
    unrenderable_field: str(r.unrenderable_field),
    push_behind: bool(r.push_behind),
    quarantine: notes(r.quarantine),
    ignored: notes(r.ignored),
  }
}

// ---------------------------------------------------------------------------
// Prose
// ---------------------------------------------------------------------------

/** A git SHA, shortened the way git itself shortens it. */
export function shortSha(sha: string): string {
  return sha.length > 7 ? sha.slice(0, 7) : sha
}

/** What the panel says about one status: a headline, the explanation, and the
 *  thing to do next (empty when there is nothing to do). */
export interface GitProjectionSummary {
  severity: GitProjectionSeverity
  headline: string
  detail: string
  /** What a human should do. Empty when nothing is required of them. */
  action: string
}

export function describeGitProjection(status: GitProjectionStatus): GitProjectionSummary {
  const branch = status.branch === '' ? 'the default branch' : status.branch

  switch (status.health) {
    case 'off':
      return {
        severity: 'off',
        headline: 'Not published to git',
        detail:
          'No repository is set for this project, so nothing is rendered and nothing is pushed. ' +
          'This is a setting, not a fault.',
        action: '',
      }

    case 'unknown':
      return {
        severity: 'warning',
        headline: 'Cannot tell whether publishing is working',
        detail:
          `A repository is configured, but this deployment is not reporting projection state. ` +
          'Nothing here says whether the repository is up to date, so treat what is in git as possibly stale.',
        action: 'Check that agentd is running the projector for this project.',
      }

    case 'unrenderable':
      return {
        severity: 'error',
        headline: 'Nothing is being published: a field holds a literal secret',
        detail:
          (status.unrenderable_field !== ''
            ? `\`${status.unrenderable_field}\` holds a value instead of a \${VAR} reference. `
            : 'A credential-bearing field holds a value instead of a ${VAR} reference. ') +
          'The whole tree is refused rather than published with that field redacted, so the repository ' +
          'has stopped moving entirely while the rest of the project carries on changing. ' +
          'The value itself is never recorded here or in the repository.',
        action:
          'Move the value into an environment variable of agentd and set the field to ${VAR}. ' +
          'Publishing resumes on the next configuration change.',
      }

    case 'diverged':
      return {
        severity: 'error',
        headline: 'The remote has diverged — this will not resolve on its own',
        detail:
          `Someone rewrote history on ${branch}, so a fast-forward push is refused. ` +
          'Bob will never force, rebase or merge to get past this. The local commits are intact ' +
          'and will publish the moment the remote is reconciled — but until then every change made ' +
          'in the console stays invisible in git.',
        action: 'A human has to reconcile the remote branch by hand.',
      }

    case 'push_failing':
      return {
        severity: 'error',
        headline: 'Pushing is failing — the repository is going stale',
        detail:
          'Configuration is still being rendered and committed locally, and agents carry on working, ' +
          `so nothing looks wrong anywhere else. But nothing has reached ${branch} since the last ` +
          'successful push, and the commits are piling up in agentd\'s clone.' +
          (status.last_error !== '' ? ` The engine said: ${status.last_error}` : ''),
        action:
          status.token_env !== ''
            ? `Check that the remote is reachable and that ${status.token_env} still holds a valid push token.`
            : 'Check that the remote is reachable and that a push credential is configured.',
      }

    case 'quarantined': {
      const n = status.quarantine.length
      return {
        severity: 'error',
        headline: `A push from git was rejected in full (${n} ${n === 1 ? 'file' : 'files'})`,
        detail:
          'A file in that push did not parse, so NOTHING in it was applied — not the broken file, ' +
          'and not the good ones beside it. Whoever pushed it will see their change simply never arrive.',
        action: 'Fix the files listed below and push again.',
      }
    }

    case 'failing':
      return {
        severity: 'error',
        headline: 'Publishing to git is failing',
        detail:
          status.last_error !== ''
            ? `The engine said: ${status.last_error}`
            : 'The last attempt did not publish, and no reason was recorded.',
        action: 'Check agentd\'s logs for this project.',
      }

    case 'ok':
    default:
      if (status.push_behind) {
        // No error, but the remote is behind: the push loop is on a timer, so
        // this is normally a few seconds old rather than a fault.
        return {
          severity: 'info',
          headline: 'Publishing — one commit not yet pushed',
          detail:
            `Rendered up to configuration change #${status.last_rendered_seq} and committed locally; ` +
            `the push to ${branch} has not run yet. The push loop is on a timer, so this clears itself.`,
          action: '',
        }
      }
      return {
        severity: 'ok',
        headline: 'Published to git',
        detail:
          status.last_rendered_sha === ''
            ? 'A repository is configured and healthy; nothing has been rendered yet.'
            : `Rendered up to configuration change #${status.last_rendered_seq} and pushed to ${branch} ` +
              `as ${shortSha(status.last_pushed_sha)}.`,
        action: '',
      }
  }
}

/** The sentence above the "ignored" list. It is separate from the health
 *  summary because ignored edits are not a fault of the projection at all —
 *  they are things the design cannot do, and the operator who made one is
 *  otherwise left staring at a change that had no effect. */
export const GIT_PROJECTION_IGNORED_EXPLANATION =
  'These edits were pushed to git and had no effect. Images cannot be imported, and deleting a ' +
  'skill or a memory file removes nothing, because both are append-only. Nothing is wrong — but ' +
  'the change you made is not in the project.'
