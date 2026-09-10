import { test, expect, request as apiRequest, type APIRequestContext } from '@playwright/test'
import {
  newProjectClient,
  poll,
  projectClient,
  ProjectClient,
  type ConfigEvent,
} from '../helpers/api'
import { configEvents } from '../helpers/configlog'
import {
  BOT_AUTHOR_EMAIL,
  BOT_AUTHOR_NAME,
  CLONE_ROOT,
  agentdLogsSince,
  agentdShellUsable,
  createBareRemote,
  humanPush,
  inAgentd,
  postWebhook,
  remoteCommits,
  remoteFile,
  remotePath,
  remoteTip,
  removeProjectRepos,
  withTrailers,
  WEBHOOK_SECRET_ENV,
  type GitProjectionStatus,
  type RemoteCommit,
} from '../helpers/gitprojection'

// Feature e2e for the git projection (G18, design/2026-09-09-git-projection.md).
//
// The claim the whole design rests on is "the database keeps the writes, git
// gets the publication", and it is made of four behaviours no unit test can
// prove together, because proving them together needs a real agentd, a real
// Postgres, a real clone on a real disk, and a real remote:
//
//   1. OUT — a configuration change becomes a commit, authored by the fixed bot
//      identity, carrying the config event's own seq/id/action/actor as
//      trailers, with the new prompt as the file's body.
//   2. IN  — a human's commit becomes config event N+1, with the commit message
//      as its rationale and an EMPTY actor, which is the encoding for "a person
//      did this, not a worker".
//   3. QUARANTINE — one unparseable file rejects the WHOLE push. Not just the
//      bad file: the whole push, good files included, watermark not advanced,
//      and the reason readable by an operator.
//   4. NO-OP — a config event that changes nothing rendered produces NO commit.
//      This is what terminates the import→render→import loop, so a spurious
//      commit here would not be cosmetic; it would be an infinite loop.
//
// # The remote is a local bare repository, and everything runs inside agentd
//
// No GitHub, no network, no credential. agentd shells out to git in its own
// container, so the "remote" is a bare repo in a temp directory *there*, and so
// is the human's clone. See helpers/gitprojection.ts for why.
//
// # The assertions rest on git and the config log, not on the status route
//
// Deliberate. `GET /agent/git-projection` is a *view* of the projection, and a
// test whose every wait went through it would go green on a stack whose route
// answers well and whose renderer does nothing. The repository is the
// publication and the config log is the record, so those are what is asserted;
// the status route is checked where it is itself the product surface (the
// operator's sight of a quarantine, and of a refusal to render).
//
// # The inbound door is driven by the WEBHOOK, not the poll
//
// Both are equally real triggers — TriggerImport is the same call from either —
// and the poll would need no secret. It is not used here because its cadence is
// AGENTKIT_GIT_WEBHOOK_POLL_INTERVAL, which the stack does not set, so it falls
// back to five minutes: two inbound scenarios would cost ten minutes of waiting.
// The webhook fires in milliseconds. Its secret is named by
// `git_webhook_secret_env`, pointed at a variable the stack-e2e overlay already
// defines (see WEBHOOK_SECRET_ENV), so this spec adds nothing to the shared rig.

/** Render is hook-driven and immediate; the push loop ticks every 30s. */
const RENDER_TIMEOUT = 120_000

/**
 * How long "nothing happened" is given to happen anyway.
 *
 * Proving a NEGATIVE (no commit) needs a bound, and this is it: the render loop
 * is woken by the post-commit hook, so a render that is going to happen has
 * happened long before this. Every negative assertion below is paired with a
 * POSITIVE one — a later change that does commit — so a stack whose projector
 * is simply dead cannot pass by staying quiet.
 */
const QUIET = 10_000

const BASE_URL = process.env.STACK_BASE_URL || 'http://localhost:8080'

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

/** GET /agent/git-projection for this project. */
async function status(client: ProjectClient): Promise<GitProjectionStatus> {
  const resp = await client.raw('GET', '/agent/git-projection')
  if (!resp.ok()) {
    throw new Error(`GET /agent/git-projection → ${resp.status()}: ${await resp.text()}`)
  }
  return (await resp.json()) as GitProjectionStatus
}

/**
 * The settings row as JSON, including the git columns.
 *
 * `ProjectClient.getSettings()` is typed against helpers/api.ts's
 * ProjectSettings, which predates those columns; going through `raw` keeps them
 * rather than editing a helper every other spec depends on.
 */
async function readSettings(client: ProjectClient): Promise<Record<string, unknown>> {
  const resp = await client.raw('GET', '/agent/project-settings')
  if (!resp.ok()) throw new Error(`GET settings → ${resp.status()}: ${await resp.text()}`)
  return (await resp.json()) as Record<string, unknown>
}

/** PUT is whole-object, so every write here is read-modify-write. */
async function writeSettings(
  client: ProjectClient,
  patch: Record<string, unknown>,
): Promise<Record<string, unknown>> {
  const body = { ...(await readSettings(client)), ...patch }
  delete body.updated_at
  const resp = await client.raw('PUT', '/agent/project-settings', body)
  if (!resp.ok()) throw new Error(`PUT settings → ${resp.status()}: ${await resp.text()}`)
  return (await resp.json()) as Record<string, unknown>
}

/** The newest config event. */
async function newestEvent(client: ProjectClient): Promise<ConfigEvent> {
  const rows = await configEvents(client)
  if (rows.length === 0) throw new Error(`${client.project} has no config events`)
  return rows[0]
}

/** Rewrites one worker's prompt, leaving every other field as stored. */
async function setPrompt(client: ProjectClient, name: string, prompt: string): Promise<ConfigEvent> {
  const stored = await client.getWorker(name)
  await client.putWorker(name, {
    description: stored.description,
    system_prompt: prompt,
    mcp_config: stored.mcp_config,
    image: stored.image,
    max_instances: stored.max_instances,
    enabled: stored.enabled,
    frozen: stored.frozen,
  })
  return newestEvent(client)
}

/** Splits a projected markdown file into its frontmatter block and its body. */
function splitFile(content: string): { frontmatter: string; body: string } {
  if (!content.startsWith('---\n')) return { frontmatter: '', body: content }
  const rest = content.slice(4)
  const end = rest.indexOf('\n---\n')
  if (end < 0) return { frontmatter: '', body: content }
  return { frontmatter: rest.slice(0, end), body: rest.slice(end + 5).replace(/^\n/, '') }
}

/** Rebuilds a projected file with a new body, leaving the frontmatter alone. */
function withBody(content: string, body: string): string {
  const { frontmatter } = splitFile(content)
  return `---\n${frontmatter}\n---\n\n${body.replace(/\n*$/, '')}\n`
}

/** Waits for a commit carrying this exact Bob-Seq trailer. */
function waitForCommitOfSeq(project: string, seq: number): Promise<RemoteCommit | undefined> {
  return poll(
    async () => {
      const commits = await withTrailers(project, await remoteCommits(project))
      return commits.find((c) => c.trailers['Bob-Seq'] === String(seq))
    },
    (c) => c !== undefined,
    RENDER_TIMEOUT,
    `a commit carrying Bob-Seq: ${seq}`,
  )
}

/** Every Bob-Seq trailer currently published on the branch. */
async function publishedSeqs(project: string): Promise<string[]> {
  const commits = await withTrailers(project, await remoteCommits(project))
  return commits.map((c) => c.trailers['Bob-Seq']).filter(Boolean)
}

/**
 * Points a fresh project at a fresh bare repository and waits until the first
 * render has been pushed. Returns the project id.
 *
 * Everything the tests need must exist BEFORE the remote is set, so the first
 * render already carries it: setting `git_remote` is itself the config event
 * that makes the project start projecting.
 */
async function projectedProject(
  request: APIRequestContext,
  prefix: string,
  workers: Record<string, string>,
  // Called the moment the project exists, BEFORE the wait below. A project that
  // is already projecting when the first push times out still has to be torn
  // down: leaving `git_remote` set on a shared stack leaves agentd failing to
  // fetch a directory nobody will ever recreate, on every poll and every boot.
  born?: (project: string) => void,
): Promise<string> {
  const client = await newProjectClient(request, prefix)
  born?.(client.project)
  await createBareRemote(client.project)
  const seedTip = await remoteTip(client.project)
  for (const [name, prompt] of Object.entries(workers)) {
    await client.putWorker(name, {
      description: `${name} worker`,
      system_prompt: prompt,
      enabled: true,
    })
  }
  await writeSettings(client, {
    git_remote: remotePath(client.project),
    git_branch: 'main',
    git_subfolder: 'orange',
    git_webhook_secret_env: WEBHOOK_SECRET_ENV,
  })

  // Ground truth: the remote actually moved. The render is hook-driven and
  // immediate; the push loop ticks on the projector's interval (30s), so this
  // wait is dominated by the push.
  await poll(
    () => remoteTip(client.project),
    (tip) => tip !== '' && tip !== seedTip,
    RENDER_TIMEOUT,
    `${client.project} to render and push its first commit`,
  )
  return client.project
}

/** Stops a project projecting and removes its repositories from the container. */
async function unproject(project: string | undefined): Promise<void> {
  if (!project) return
  const ctx = await apiRequest.newContext({ baseURL: BASE_URL })
  try {
    const client = await projectClient(ctx, project)
    // Clearing git_remote FIRST matters: the five-minute poll fallback would
    // otherwise keep trying to import from a directory this teardown deleted,
    // on a stack other specs are sharing.
    await writeSettings(client, {
      git_remote: '',
      git_branch: '',
      git_subfolder: '',
      git_webhook_secret_env: '',
    })
  } catch {
    // A teardown that cannot reach the API must still remove the directories.
  } finally {
    await ctx.dispose()
  }
  await removeProjectRepos(project)
}

test.describe('git projection: out, in, quarantine, no-op', () => {
  test.describe.configure({ mode: 'serial' })

  // One project carries all four scenarios, in order, because they are one
  // story: publish it, prove a no-op stays silent, let a human edit it, then
  // let a human break it. The quarantine goes last because it deliberately
  // leaves the inbound watermark parked.
  let project: string | undefined
  let usable: boolean | undefined
  let client: ProjectClient

  const EDITOR_PROMPT = 'You edit copy. Keep it short.'
  const SCRIBE_PROMPT = 'You take notes.'

  test.beforeEach(async ({ request }) => {
    if (usable === undefined) usable = await agentdShellUsable()
    test.skip(!usable, "no shell or git inside this stack's agentd container")
    if (!project) {
      project = await projectedProject(
        request,
        'e2e-gitproj',
        { editor: EDITOR_PROMPT, scribe: SCRIBE_PROMPT },
        (p) => {
          project = p
        },
      )
    }
    // A client bound to THIS test's request context. The project is shared; the
    // context is not — Playwright disposes it when the test ends.
    client = await projectClient(request, project)
  })

  test.afterAll(async () => {
    await unproject(project)
  })

  test('the first render publishes the whole project under orange/', async () => {
    const tip = await remoteTip(project!)
    expect(tip).not.toBe('')

    expect(
      await remoteFile(project!, 'orange/settings.md', tip),
      'orange/settings.md must exist at the tip',
    ).not.toBeNull()

    const editor = await remoteFile(project!, 'orange/workers/editor.md', tip)
    expect(editor).not.toBeNull()
    expect(splitFile(editor!).body.trim()).toBe(EDITOR_PROMPT)

    // The projection's own commits are authored by the fixed bot identity, not
    // by whoever happens to have git configured in the container.
    const head = (await withTrailers(project!, await remoteCommits(project!)))[0]
    expect(head.authorName).toBe(BOT_AUTHOR_NAME)
    expect(head.authorEmail).toBe(BOT_AUTHOR_EMAIL)
    expect(head.trailers['Bob-Project']).toBe(project)
  })

  test('OUT: a prompt change becomes a commit carrying the config event verbatim', async () => {
    const before = await remoteTip(project!)
    const NEW_PROMPT = 'You edit copy. Keep it short, and never use the word "leverage".'

    // The config event is the SOURCE of the trailers, so it is what they are
    // compared against — not a hardcoded expectation of what the action is
    // called this month.
    const ev = await setPrompt(client, 'editor', NEW_PROMPT)
    const commit = (await waitForCommitOfSeq(project!, ev.seq))!

    expect(commit.sha).not.toBe(before)
    expect(commit.authorName).toBe(BOT_AUTHOR_NAME)
    expect(commit.authorEmail).toBe(BOT_AUTHOR_EMAIL)
    expect(commit.trailers['Bob-Project']).toBe(project)
    expect(commit.trailers['Bob-Seq']).toBe(String(ev.seq))
    expect(commit.trailers['Bob-Event']).toBe(ev.id)
    expect(commit.trailers['Bob-Action']).toBe(ev.action)

    // Provenance is COPIED OUT OF THE LOG (rule 2 / DI4), never invented. A
    // console write has an empty actor, and an empty actor renders as NO actor
    // trailer — the same encoding the importer uses inbound. Asserting against
    // the event rather than a constant keeps this true for an MCP write too.
    if (ev.actor_worker === '') {
      expect(commit.trailers['Bob-Actor-Worker']).toBeUndefined()
    } else {
      expect(commit.trailers['Bob-Actor-Worker']).toBe(ev.actor_worker)
    }
    if (ev.actor_session === '') {
      expect(commit.trailers['Bob-Actor-Session']).toBeUndefined()
    } else {
      expect(commit.trailers['Bob-Actor-Session']).toBe(ev.actor_session)
    }

    // And the file at that commit really says the new prompt.
    const file = await remoteFile(project!, 'orange/workers/editor.md', commit.sha)
    expect(file).not.toBeNull()
    expect(splitFile(file!).body.trim()).toBe(NEW_PROMPT)
    expect(splitFile(file!).frontmatter).toContain('name: editor')
  })

  test('NO-OP: a config event that changes nothing rendered produces no commit', async () => {
    const tipBefore = await remoteTip(project!)

    // A byte-identical whole-object PUT. It is still a mutation: it allocates a
    // seq and appends to the config log (PutProjectSettings has no same-value
    // short circuit). What it must NOT do is produce a commit.
    await writeSettings(client, {})
    const noop = await newestEvent(client)
    expect(noop.action).toBe('project_settings_put')

    await sleep(QUIET)
    expect(
      await remoteTip(project!),
      'a config event that changed nothing rendered must not move the branch',
    ).toBe(tipBefore)

    // The positive half: the projector was awake the whole time, and a REAL
    // change straight afterwards does commit. Without this, a dead projector
    // would pass the assertion above.
    const real = await setPrompt(client, 'scribe', 'You take notes. Timestamp every one.')
    const commit = (await waitForCommitOfSeq(project!, real.seq))!
    expect(commit.sha).not.toBe(tipBefore)

    // …and the no-op's seq is on no commit at all, then or now.
    expect(await publishedSeqs(project!)).not.toContain(String(noop.seq))
  })

  test('IN: a human commit lands as event N+1, rationale = commit message, actor empty', async ({
    request,
  }) => {
    const tip = await remoteTip(project!)
    const rendered = await remoteFile(project!, 'orange/workers/editor.md', tip)
    expect(rendered).not.toBeNull()

    const HUMAN_PROMPT = 'You edit copy. Prefer plain words, and cut every second adjective.'
    const MESSAGE = 'Tighten the editor prompt\n\nThe old one said nothing about adjectives.'

    const seqBefore = (await newestEvent(client)).seq

    await humanPush(project!, MESSAGE, {
      'orange/workers/editor.md': withBody(rendered!, HUMAN_PROMPT),
    })

    const delivery = await postWebhook(request, project!)
    expect(
      delivery.status,
      `the webhook must accept a signed push delivery (body: ${delivery.body})`,
    ).toBe(202)

    const ev = (await poll(
      async () => (await configEvents(client)).find((e) => e.seq > seqBefore),
      (e) => e !== undefined,
      RENDER_TIMEOUT,
      `an imported config event after seq ${seqBefore}`,
    ))!

    // N+1: the human's edit is the very next entry in the same log the
    // architect reads, not a parallel history.
    expect(ev.seq).toBe(seqBefore + 1)

    // The commit message IS the rationale — the whole reason a human's reason
    // for a change survives the trip.
    expect(ev.rationale).toContain('Tighten the editor prompt')
    expect(ev.rationale).toContain('The old one said nothing about adjectives.')

    // An EMPTY actor is the encoding for "a person did this". Provenance is
    // server-stamped; a commit cannot claim to be a worker.
    expect(ev.actor_worker).toBe('')
    expect(ev.actor_session).toBe('')

    // A body-only edit takes the dedicated prompt-write path, so the log says
    // what actually happened rather than "somebody replaced the whole worker".
    expect(ev.action).toBe('worker_prompt_write')

    // And the database really changed — through the same store method the API
    // calls, so the row, the log and the repo agree.
    expect((await client.getWorker('editor')).system_prompt.trim()).toBe(HUMAN_PROMPT)
  })

  test('QUARANTINE: one unparseable file rejects the whole push, good file included', async ({
    request,
  }) => {
    const tip = await remoteTip(project!)
    const editorFile = (await remoteFile(project!, 'orange/workers/editor.md', tip))!
    const scribeFile = (await remoteFile(project!, 'orange/workers/scribe.md', tip))!
    expect(editorFile).toBeTruthy()
    expect(scribeFile).toBeTruthy()

    const editorBefore = await client.getWorker('editor')
    const scribeBefore = await client.getWorker('scribe')
    const eventsBefore = await configEvents(client)
    const importedBefore = (await status(client)).last_imported_sha
    const since = new Date(Date.now() - 5_000).toISOString()

    // One good change and one file whose frontmatter is not YAML, in ONE push.
    // That is the only shape that can prove the rule: rejecting the bad file
    // alone would be indistinguishable from a per-file importer.
    const GOOD = 'You edit copy. This change must NOT be applied.'
    const broken = '---\nname: scribe\nenabled: [unclosed\n---\n\nAlso must not be applied.\n'

    const pushed = await humanPush(project!, 'a good edit and a broken one', {
      'orange/workers/editor.md': withBody(editorFile, GOOD),
      'orange/workers/scribe.md': broken,
    })

    const delivery = await postWebhook(request, project!)
    expect(delivery.status).toBe(202)

    // The failure is VISIBLE — not a silent drop. agentd says so in its own
    // words, naming the file…
    const logs = await poll(
      () => agentdLogsSince(since),
      (l) => l.includes('quarantined') && l.includes(project!),
      RENDER_TIMEOUT,
      'agentd to report the quarantine',
    )
    expect(logs).toContain('orange/workers/scribe.md')
    expect(logs).toContain('nothing was applied')

    // …and an operator reading the console sees it too: the quarantine is
    // PERSISTED (G23) as a list of paths with reasons, not as a single
    // last_error string, so that is what is asserted. A push rejected wholesale
    // that an operator can only learn about from a container's stdout is not
    // visible in any useful sense.
    const after = await poll(
      () => status(client),
      (s) => s.health === 'quarantined' && s.quarantine.length > 0,
      RENDER_TIMEOUT,
      'the projection status to report a quarantine',
    )
    expect(after.quarantine.map((q) => q.path)).toContain('orange/workers/scribe.md')
    expect(after.quarantine.map((q) => q.reason).join('\n')).toContain('malformed frontmatter')

    // NOTHING was applied. Not the broken file, and not the good one beside it.
    expect((await client.getWorker('editor')).system_prompt).toBe(editorBefore.system_prompt)
    expect((await client.getWorker('scribe')).system_prompt).toBe(scribeBefore.system_prompt)
    expect(await configEvents(client)).toHaveLength(eventsBefore.length)

    // And the watermark did not move past the rejected commit, so fixing the
    // file and pushing again re-offers the SAME change rather than losing it.
    const importedAfter = (await status(client)).last_imported_sha
    expect(importedAfter).toBe(importedBefore)
    expect(importedAfter).not.toBe(pushed)
  })
})

test.describe('git projection: a literal secret makes a project unrenderable', () => {
  test.describe.configure({ mode: 'serial' })

  let project: string | undefined
  let usable: boolean | undefined
  let client: ProjectClient

  // A Slack incoming-webhook URL is a bearer token: anybody holding it can post
  // as that app. §D's rule is REFUSAL, not redaction — the whole tree stops
  // rendering, and the reason names the field without ever quoting the value.
  const SECRET = 'g18neverpublishthisvalue'
  const SECRET_URL = `https://hooks.slack.com/services/T00G18/B00G18/${SECRET}`

  test.beforeEach(async ({ request }) => {
    if (usable === undefined) usable = await agentdShellUsable()
    test.skip(!usable, "no shell or git inside this stack's agentd container")
    if (!project) {
      project = await projectedProject(
        request,
        'e2e-gitsecret',
        { editor: 'You edit copy.' },
        (p) => {
          project = p
        },
      )
    }
    client = await projectClient(request, project)
  })

  test.afterAll(async () => {
    await unproject(project)
  })

  test('the whole tree stops rendering, and the secret reaches no repo, disk or log', async () => {
    const tipBefore = await remoteTip(project!)
    const since = new Date(Date.now() - 5_000).toISOString()

    const refused = await writeSettings(client, {
      attention_channel: { kind: 'webhook', url: SECRET_URL },
    })
    const refusedSeq = (await newestEvent(client)).seq
    expect(refused.attention_channel).toBeTruthy()

    // A SECOND, entirely innocent change. Refusal is of the whole tree, so this
    // one must not publish either — and asserting it is what tells a stalled
    // projector apart from a refusing one, since a refusing projector keeps
    // rendering every OTHER project.
    const innocent = await setPrompt(client, 'editor', 'You edit copy. Use short sentences.')

    await sleep(QUIET)
    expect(await remoteTip(project!)).toBe(tipBefore)
    expect(await publishedSeqs(project!)).not.toContain(String(refusedSeq))
    expect(await publishedSeqs(project!)).not.toContain(String(innocent.seq))

    // The secret is in no published file…
    const settingsFile = await remoteFile(project!, 'orange/settings.md', tipBefore)
    expect(settingsFile ?? '').not.toContain(SECRET)

    // …in nothing agentd printed. This is the check that would catch a
    // well-meaning "log what we refused, and what it was" change.
    expect(await agentdLogsSince(since)).not.toContain(SECRET)

    // …and not on the volume either. A refusal that had already written the
    // file and only then declined to commit would still have leaked it onto
    // disk, where a snapshot or a backup would pick it up.
    const onDisk = await inAgentd(
      `grep -rl ${SECRET} ${CLONE_ROOT}/${project} 2>/dev/null || true`,
    )
    expect(onDisk.trim()).toBe('')
  })

  test('the console is told WHICH field refused, and never the value', async () => {
    const unrenderable = await poll(
      () => status(client),
      (s) => s.health === 'unrenderable',
      RENDER_TIMEOUT,
      'the projection status to report an unrenderable project',
    )
    expect(unrenderable.unrenderable_field ?? '').toContain('AttentionChannel')
    expect(JSON.stringify(unrenderable)).not.toContain(SECRET)
  })
})
