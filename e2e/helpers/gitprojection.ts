import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { createHmac } from 'node:crypto'
import type { APIRequestContext } from '@playwright/test'

const exec = promisify(execFile)

// Helpers for the git-projection stack e2e (G18,
// design/2026-09-09-git-projection.md).
//
// # Why every git command runs INSIDE the agentd container
//
// agentd shells out to `git` in its own filesystem: it clones each project's
// remote into AGENTKIT_GIT_CLONE_ROOT (default /data/git-projection, which is
// the agentd-data volume) and pushes from there. So a "remote" the projection
// can actually reach has to be a path agentd can open — not a path on the host,
// which agentd has never heard of. Everything below therefore runs through
// `docker compose exec agentd sh -c …`: the bare repository, the human's clone,
// the human's commit and push, and the reads that assert what landed.
//
// That is also why the remote is a **local bare repository** rather than a real
// GitHub repo. Nothing here touches the network, no credential is needed, and a
// failing test cannot push anything to anywhere real.
//
// # File contents travel as base64
//
// The scripts below are handed to `sh -c` as ONE argv element (execFile does not
// invoke a shell of its own), so the only quoting that matters is the quoting
// inside the script string. Rather than reason about that for markdown bodies
// containing quotes, newlines and YAML, every file write is
// `printf %s <base64> | base64 -d > file`.

const COMPOSE_PROJECT = process.env.STACK_COMPOSE_PROJECT || 'agent-bob-stack-e2e'

/**
 * The environment variable agentd reads this project's webhook HMAC secret
 * from, and its value.
 *
 * ⚠️ These deliberately default to the project API key the stack-e2e overlay
 * already defines (docker-compose.stack-e2e.yml: `APPLES_API_KEY`). The webhook
 * needs *some* variable that exists in agentd's environment and whose value the
 * test knows, and the alternative was adding one to the shared overlay — which
 * this ticket does not own. `git_webhook_secret_env` only ever names a
 * variable, so pointing it at an existing one is a rig choice, not a product
 * behaviour: agentd cannot tell the difference.
 *
 * If a dedicated variable is added later, set STACK_GIT_WEBHOOK_SECRET_ENV and
 * STACK_GIT_WEBHOOK_SECRET and nothing else here changes.
 */
export const WEBHOOK_SECRET_ENV = process.env.STACK_GIT_WEBHOOK_SECRET_ENV || 'APPLES_API_KEY'
export const WEBHOOK_SECRET =
  process.env.STACK_GIT_WEBHOOK_SECRET || 'stack-e2e-apples-api-key-0123456789'

/** Where this suite's bare "remotes" live, inside the agentd container. */
export const REMOTE_ROOT = '/tmp/g18-remotes'
/** Where this suite's human clones live, inside the agentd container. */
export const WORK_ROOT = '/tmp/g18-work'
/** agentd's own clone root (AGENTKIT_GIT_CLONE_ROOT's default). */
export const CLONE_ROOT = '/data/git-projection'

/** The fixed identity gitproj.Repo commits under (go/gitproj/repo.go:43-44). */
export const BOT_AUTHOR_NAME = 'Agent Bob'
export const BOT_AUTHOR_EMAIL = 'bob@agentbob.local'

/** The identity the "human" side of these tests commits under. */
export const HUMAN_NAME = 'A Human'
export const HUMAN_EMAIL = 'human@example.test'

// git flags used on EVERY invocation in here, for the same reasons agentd uses
// them (go/cmd/agentd/gitprojection.go runGit): no hooks, no credential prompt,
// no locale surprises, and no "dubious ownership" refusal on a directory owned
// by a different uid inside the container.
const GIT = `git -c safe.directory='*' -c core.hooksPath=/dev/null -c gc.auto=0`
const GIT_AS_HUMAN = `${GIT} -c user.name='${HUMAN_NAME}' -c user.email='${HUMAN_EMAIL}'`

/** Runs a shell script inside the stack's agentd container. */
export async function inAgentd(script: string): Promise<string> {
  try {
    const { stdout } = await exec(
      'docker',
      ['compose', '-p', COMPOSE_PROJECT, 'exec', '-T', 'agentd', 'sh', '-c', script],
      { maxBuffer: 16 * 1024 * 1024 },
    )
    return stdout
  } catch (e) {
    const err = e as { stdout?: string; stderr?: string; message?: string }
    throw new Error(
      `agentd shell failed: ${err.message}\n--- script ---\n${script}\n--- stderr ---\n${err.stderr ?? ''}\n--- stdout ---\n${err.stdout ?? ''}`,
    )
  }
}

/** True when `git` and a shell are usable inside agentd — lets a spec skip cleanly. */
export async function agentdShellUsable(): Promise<boolean> {
  try {
    await inAgentd('git --version')
    return true
  } catch {
    return false
  }
}

function b64(s: string): string {
  return Buffer.from(s, 'utf8').toString('base64')
}

/** The path of the bare repository this project projects into. */
export function remotePath(project: string): string {
  return `${REMOTE_ROOT}/${project}.git`
}

/** The path of the human's clone of that repository. */
export function workPath(project: string): string {
  return `${WORK_ROOT}/${project}`
}

/**
 * Creates the bare repository a project renders into, with `main` as its
 * initial branch and one empty root commit so `main` actually resolves.
 *
 * The empty root commit matters: agentd's clone of an EMPTY bare repo has no
 * branch to fast-forward, and the first render then has to create one. Seeding
 * a root commit is what a real GitHub repository looks like anyway ("initialise
 * with a README"), so this is the realistic shape, not a convenience.
 */
export async function createBareRemote(project: string): Promise<string> {
  const remote = remotePath(project)
  const seed = `${WORK_ROOT}/${project}-seed`
  await inAgentd(
    [
      `set -e`,
      `mkdir -p ${REMOTE_ROOT} ${WORK_ROOT}`,
      `rm -rf ${remote} ${seed}`,
      `${GIT} init --bare --initial-branch=main ${remote} >/dev/null`,
      `${GIT} init --initial-branch=main ${seed} >/dev/null`,
      `printf %s ${b64('# projected by Agent Bob\n')} | base64 -d > ${seed}/README.md`,
      `cd ${seed}`,
      `${GIT_AS_HUMAN} add README.md`,
      `${GIT_AS_HUMAN} commit -q -m 'initial commit'`,
      `${GIT_AS_HUMAN} push -q ${remote} main`,
      `cd /`,
      `rm -rf ${seed}`,
    ].join(' && '),
  )
  return remote
}

/** Removes everything this suite created for a project, inside the container. */
export async function removeProjectRepos(project: string): Promise<void> {
  await inAgentd(
    `rm -rf ${remotePath(project)} ${workPath(project)} ${CLONE_ROOT}/${project} || true`,
  ).catch(() => {})
}

/** One commit as this suite reads it out of the bare repository. */
export interface RemoteCommit {
  sha: string
  authorName: string
  authorEmail: string
  subject: string
  /** The full commit message, subject and body. */
  message: string
  /** Trailers as git's own parser sees them (`git interpret-trailers --parse`). */
  trailers: Record<string, string>
}

// A record separator no commit message will contain.
const REC = '@@G18REC@@'
const FIELD = '@@G18FIELD@@'

/**
 * Reads a branch's commits, newest first, out of the bare repository.
 *
 * Trailers are parsed with `git interpret-trailers --parse`, i.e. with git's own
 * parser rather than a regex over the message — which is the whole point of
 * DI4's defence: a rationale that *contains* the text "Bob-Seq: 9" must not
 * be seen as a trailer, because gitproj.Commit always emits the real trailer
 * block as the final paragraph.
 */
export async function remoteCommits(project: string, branch = 'main'): Promise<RemoteCommit[]> {
  const remote = remotePath(project)
  const out = await inAgentd(
    `${GIT} -C ${remote} log ${branch} --format='%H${FIELD}%an${FIELD}%ae${FIELD}%s${FIELD}%B${REC}' || true`,
  )
  return out
    .split(REC)
    .map((chunk) => chunk.replace(/^\n+/, ''))
    .filter((chunk) => chunk.trim() !== '')
    .map((chunk) => {
      const [sha, authorName, authorEmail, subject, message] = chunk.split(FIELD)
      return {
        sha: sha.trim(),
        authorName,
        authorEmail,
        subject,
        message,
        trailers: {} as Record<string, string>,
      }
    })
}

/** Fills in `trailers` for the given commits, using git's own trailer parser. */
export async function withTrailers(project: string, commits: RemoteCommit[]): Promise<RemoteCommit[]> {
  const remote = remotePath(project)
  for (const c of commits) {
    const out = await inAgentd(
      `${GIT} -C ${remote} log -1 --format=%B ${c.sha} | git interpret-trailers --parse`,
    )
    for (const line of out.split('\n')) {
      const i = line.indexOf(':')
      if (i <= 0) continue
      c.trailers[line.slice(0, i).trim()] = line.slice(i + 1).trim()
    }
  }
  return commits
}

/** The tip SHA of a branch in the bare repository, or "" when it does not resolve. */
export async function remoteTip(project: string, branch = 'main'): Promise<string> {
  const out = await inAgentd(`${GIT} -C ${remotePath(project)} rev-parse ${branch} 2>/dev/null || true`)
  return out.trim()
}

/** A file's contents at a ref in the bare repository, or null when absent. */
export async function remoteFile(
  project: string,
  path: string,
  ref = 'main',
): Promise<string | null> {
  const marker = '@@G18MISSING@@'
  const out = await inAgentd(
    `${GIT} -C ${remotePath(project)} show ${ref}:${path} 2>/dev/null || printf %s ${marker}`,
  )
  return out === marker ? null : out
}

/** Every path under a ref, one per line. */
export async function remotePaths(project: string, ref = 'main'): Promise<string[]> {
  const out = await inAgentd(`${GIT} -C ${remotePath(project)} ls-tree -r --name-only ${ref} || true`)
  return out.split('\n').map((s) => s.trim()).filter(Boolean)
}

/**
 * Clones the project's remote, applies `files`, commits with `message` under the
 * human identity, and pushes — the inbound door's other side.
 *
 * A file whose content is `null` is deleted. Returns the pushed SHA.
 */
export async function humanPush(
  project: string,
  message: string,
  files: Record<string, string | null>,
  branch = 'main',
): Promise<string> {
  const remote = remotePath(project)
  const work = workPath(project)
  const steps = [
    `set -e`,
    `mkdir -p ${WORK_ROOT}`,
    `rm -rf ${work}`,
    `${GIT} clone -q --branch ${branch} ${remote} ${work}`,
    `cd ${work}`,
  ]
  for (const [path, content] of Object.entries(files)) {
    if (content === null) {
      steps.push(`${GIT} rm -q -f ${path}`)
    } else {
      steps.push(`mkdir -p "$(dirname ${path})"`)
      steps.push(`printf %s ${b64(content)} | base64 -d > ${path}`)
      steps.push(`${GIT} add ${path}`)
    }
  }
  steps.push(`${GIT_AS_HUMAN} commit -q -F - <<'G18MSG'\n${message}\nG18MSG`)
  steps.push(`${GIT_AS_HUMAN} push -q origin ${branch}`)
  steps.push(`${GIT} rev-parse HEAD`)
  const out = await inAgentd(steps.join('\n'))
  return out.trim().split('\n').pop()!.trim()
}

/** Reads a file out of the human's clone (after `humanPush` left it behind). */
export async function workFile(project: string, path: string): Promise<string | null> {
  const marker = '@@G18MISSING@@'
  const out = await inAgentd(`cat ${workPath(project)}/${path} 2>/dev/null || printf %s ${marker}`)
  return out === marker ? null : out
}

/**
 * Posts a GitHub-shaped push delivery to agentd's inbound door, signed with the
 * project's webhook secret.
 *
 * The body is sent as an exact string, not as an object: the signature is over
 * the raw bytes, so anything that might re-serialise them differently (a JSON
 * body Playwright encodes itself) would produce a signature agentd cannot
 * verify. The payload carries only what the route uses — repository identity,
 * for routing — because that is all it is allowed to use.
 */
export async function postWebhook(
  request: APIRequestContext,
  project: string,
  opts: { secret?: string; event?: string } = {},
): Promise<{ status: number; body: string }> {
  const remote = remotePath(project)
  const body = JSON.stringify({
    repository: { full_name: `local/${project}`, clone_url: remote },
  })
  const secret = opts.secret ?? WEBHOOK_SECRET
  const sig = 'sha256=' + createHmac('sha256', secret).update(body).digest('hex')
  const resp = await request.post('/agent/git/webhook', {
    data: body,
    headers: {
      'content-type': 'application/json',
      'X-Hub-Signature-256': sig,
      'X-GitHub-Event': opts.event ?? 'push',
    },
  })
  return { status: resp.status(), body: await resp.text() }
}

/** The wire shape of GET /agent/git-projection (go/httpapi/gitprojectionstatus.go). */
export interface GitProjectionStatus {
  project: string
  enabled: boolean
  remote: string
  branch: string
  subfolder: string
  token_env: string
  browse_url: string
  state_available: boolean
  last_rendered_seq: number
  last_rendered_sha: string
  last_pushed_sha: string
  last_imported_sha: string
  updated_at: number
  health: string
  last_error: string
  last_error_at: number
  unrenderable_field?: string
  push_behind: boolean
  quarantine: Array<{ path: string; reason: string; at: number }>
  ignored: Array<{ path: string; reason: string; at: number }>
}

/** agentd's log since a moment, for "the secret never appears anywhere" checks. */
export async function agentdLogsSince(since: string): Promise<string> {
  const { stdout, stderr } = await exec(
    'docker',
    ['compose', '-p', COMPOSE_PROJECT, 'logs', '--no-color', '--since', since, 'agentd'],
    { maxBuffer: 32 * 1024 * 1024 },
  )
  return stdout + stderr
}
