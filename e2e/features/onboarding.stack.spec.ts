import { test, expect } from '@playwright/test'
import { cleanupOpenedProjects, loginUI } from '../helpers/ui'
import { projectClient, uniqueProject, type ProjectClient } from '../helpers/api'

// Browser e2e for onboarding: a project is created with a goal, an interviewer
// runs in a real container and writes a charter through the core tools, a human
// approves it, and an architect exists with its daily schedule ON (T25 — a
// charter that produced a switched-off architect would be a charter that did
// nothing until somebody found the schedule editor). The assertions below
// already said ON; this line had not caught up.
//
// ── What the mock model can and cannot do here ────────────────────────────
//
// The charter is found by the label `name=<the interview's session id>`, and a
// real interviewer learns that id from its first message. The MOCK cannot:
// `modelproxy`'s script is fixed JSON with no templating, so it cannot write a
// label whose value is decided at run time. Pinning the session id instead does
// not work either — session ids are globally unique and rows are SOFT-deleted,
// so a pinned id is permanently taken after the first run.
//
// So the spec splits the two halves and proves each properly:
//
//   * THE TOOL PATH is proved by the scripted interviewer, running in a real
//     container, calling `charter_validate` and then `memory_create`. Its
//     deposit lands under a fixed label of its own. The script only reaches
//     `memory_create` if `charter_validate` returned first, so the deposit
//     existing proves both calls crossed the MCP boundary.
//
//   * THE SCREEN is proved against a charter deposited under the interview's
//     REAL session id, written through `POST /agent/memories` — the route that
//     exists for an application to write on its own authority.
//
// What is therefore NOT proved: that a model reads its session id out of the
// seed and labels the deposit with it. That is discovery, and doc 25 §5 is
// explicit that mock mode proves transmission and never discovery. It was
// observed against the real model in T1.

const NEEDS_SCRIPT =
  'needs the onboarding mock script: ./e2e/run-stack-e2e.sh test mock --mock-script e2e/mock-scripts/onboarding.json'

/** The label the SCRIPT writes — see the note above. */
const SCRIPTED_DEPOSIT_NAME = 'e2e-onboard'
const GOAL = 'Sell more books to people who came in once and never came back.'

/** The charter the SPEC deposits, under the real session id. */
const DEPOSIT = `Charter v1: a weekly bookshop newsletter, judged on it going out and the list growing.
{
  "goal": "Send one email newsletter every week to the shop's mailing list.",
  "measure": "In a month: four newsletters have gone out and the list is bigger than the 430 it is today.",
  "label_rules": "kind=draft - a newsletter written but not sent; name=<issue-slug>. kind=fact - subscriber-count lives here.",
  "rationale": "The shop's problem is repeat visits, not new customers."
}`

test.describe('onboarding', () => {
  let client: ProjectClient
  let project: string

  test.afterEach(async ({ request }) => {
    await cleanupOpenedProjects(request)
    if (client) await client.cleanup()
  })

  test('an interview deposits a charter, a human approves it, and the architect exists', async ({
    page,
    request,
  }) => {
    test.skip(!process.env.STACK_MOCK_SCRIPT, NEEDS_SCRIPT)
    test.setTimeout(300_000)

    project = uniqueProject('e2e-onb')
    client = await projectClient(request, project)

    // Into the shell, on the project we can address from the API side. The
    // "unfinished onboarding" note is what the shell itself persists, so this
    // is the same entry point a reload takes.
    await loginUI(page)
    await page.evaluate(
      async ({ id, goal }) => {
        const AUTH = 'agent-orange-auth'
        const state = JSON.parse(localStorage.getItem(AUTH) ?? '{}') as {
          loginToken?: string
          projects?: { id: string; token: string }[]
          selectedProject?: string | null
        }
        const res = await fetch('/auth/project-token', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ token: state.loginToken, project: id }),
        })
        if (!res.ok) throw new Error(`project-token: HTTP ${res.status}`)
        const minted = (await res.json()) as { id: string; token: string }
        state.projects = [...(state.projects ?? []).filter((p) => p.id !== minted.id), minted]
        state.selectedProject = minted.id
        localStorage.setItem(AUTH, JSON.stringify(state))
        localStorage.setItem(
          'agentkit.onboarding.pending',
          JSON.stringify({ project: minted.id, goal }),
        )
      },
      { id: project, goal: GOAL },
    )
    await page.reload()

    // The waiting copy names its cause while the container starts.
    const waiting = page.getByTestId('onboarding-waiting')
    if (await waiting.isVisible().catch(() => false)) {
      await expect(waiting).toContainText(/container/i)
    }

    // The shell applied onboarding@v1 and started the interview.
    await expect(page.getByTestId('onboarding-rail')).toBeVisible({ timeout: 120_000 })
    await expect(page.getByTestId('onboarding-no-charter')).toBeVisible({ timeout: 120_000 })
    expect((await client.listWorkers()).map((w) => w.name)).toEqual(['interviewer'])

    // Wait for the interview session to exist and settle, then read its id.
    const session = await waitForOnboardSession(client)
    expect(session.worker ?? '', 'the interview must carry no worker identity').toBe('')
    expect(session.persona).toBe('interviewer')

    // ── half one: the tool path, from inside the container ──────────────────
    //
    // The shell's seed already ran a turn. The scripted interviewer answers it
    // by calling charter_validate and then memory_create, so a deposit under
    // the script's own label proves both calls crossed the MCP boundary.
    await expect
      .poll(async () => (await depositsNamed(client, SCRIPTED_DEPOSIT_NAME)).length, {
        timeout: 180_000,
        message: 'the scripted interviewer never deposited through memory_create',
      })
      .toBeGreaterThan(0)

    // ── half two: the screen ────────────────────────────────────────────────
    const deposited = await client.raw('POST', '/agent/memories', {
      content: DEPOSIT,
      labels: { kind: 'org-charter', name: session.id },
    })
    expect(deposited.ok(), await deposited.text()).toBe(true)

    await expect(page.getByTestId('charter-panel')).toBeVisible({ timeout: 60_000 })
    await expect(page.getByText(/four newsletters have gone out/i)).toBeVisible()
    // The rules are the thing being agreed, so they are shown whole.
    await expect(page.getByText(/kind=fact - subscriber-count lives here/i)).toBeVisible()
    // And the panel says, in words, that approving starts a loop which will
    // change the project on a clock without asking.
    await expect(page.getByText(/runs on this schedule from now on/i)).toBeVisible()

    await page.getByTestId('charter-approve').click()
    await expect(page.getByTestId('charter-applied')).toBeVisible({ timeout: 60_000 })

    // ── what approving actually did ─────────────────────────────────────────
    const workers = await client.listWorkers()
    expect(workers.map((w) => w.name).sort(), 'the architect, and no roster').toEqual([
      'architect',
      'interviewer',
    ])

    const interviewer = workers.find((w) => w.name === 'interviewer')!
    expect(interviewer.enabled, 'the interview is over (A8)').toBe(false)
    expect(interviewer.system_prompt, 'disabling must not wipe the prompt').not.toBe('')

    const schedules = await client.listSchedules()
    expect(schedules).toHaveLength(1)
    expect(schedules[0].worker).toBe('architect')
    expect(schedules[0].enabled, 'approving a charter starts the daily loop (T25)').toBe(true)

    const subs = await client.listSubscriptions()
    expect(subs.map((s) => s.event_type)).toContain('architect.run')

    const settings = await client.getSettings()
    expect(settings.briefing, 'the project-wide briefing is what delivers the registry').toContain(
      'name=label-registry',
    )
    expect(settings.system_prompt).toContain('Send one email newsletter every week')

    expect(await labelled(client, 'kind=registry'), 'the label registry seed').not.toHaveLength(0)
    expect(await labelled(client, 'kind=project-goal'), 'the goal seed').not.toHaveLength(0)

    // The button that wakes it — an event, never a chat.
    await expect(page.getByTestId('run-architect')).toBeVisible()
  })
})

interface OnboardSession {
  id: string
  status?: string
  worker?: string
  persona?: string
}

/** The `onboard` session, once it has left `creating`. */
async function waitForOnboardSession(client: ProjectClient): Promise<OnboardSession> {
  let row: OnboardSession | null = null
  await expect
    .poll(
      async () => {
        const res = await client.raw('GET', '/agent/sessions/by-name/onboard')
        if (!res.ok()) return 'absent'
        row = (await res.json()) as OnboardSession
        return row.status ?? 'unknown'
      },
      {
        timeout: 180_000,
        message: 'the interview session never appeared, or never left `creating`',
      },
    )
    // BOTH waiting states have to be excluded, not just one.
    //
    // This read `.not.toBe('creating')`, and `'absent'` is not `'creating'` —
    // so the poll was satisfied by the very first request, made before the
    // shell had even asked for the session. It then fell through to the null
    // check below and failed the whole spec in 3.2s with a bare "no onboard
    // session", which reads like the engine refusing to start an interview
    // rather than like a test that did not wait. A poll whose success
    // condition is "not the one bad value" accepts every other bad value.
    .not.toMatch(/^(absent|creating)$/)
  if (row === null) throw new Error('no onboard session')
  return row
}

async function labelled(
  client: ProjectClient,
  selector: string,
): Promise<{ labels?: Record<string, string> }[]> {
  const res = await client.raw(
    'GET',
    `/agent/memories?label_selector=${encodeURIComponent(selector)}`,
  )
  if (!res.ok()) return []
  const body = (await res.json()) as { memories?: { labels?: Record<string, string> }[] }
  return body.memories ?? []
}

async function depositsNamed(client: ProjectClient, name: string) {
  return labelled(client, `kind=org-charter,name=${name}`)
}
