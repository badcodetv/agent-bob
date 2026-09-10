import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { projectClient, TEST_EMAIL, TEST_PASSWORD, uniqueProject } from './api'

// Browser fixtures for the example app (examples/web/src/App.tsx).
//
// The app is a state machine, not a router: loading → login → project picker →
// workspace, and inside the workspace a three-way view switch (chat / workers /
// settings). These helpers drive that machine so a spec can say "open the
// workers view of a fresh project" in one line.
//
// Selectors are the app's own `data-testid`s where it has them, and accessible
// roles/labels for the library components, which ship none (see e2e/README.md).

/**
 * View switch ids in App.tsx's ViewNav — all eight of them.
 *
 * This list used to name only the three views that existed before the operator
 * console; a spec that reached for `desk` or `chart` did not typecheck.
 */
export type View =
  | 'desk'
  | 'chat'
  | 'workers'
  | 'memory'
  | 'activity'
  | 'chart'
  | 'settings'

/**
 * Every view, in the order the nav renders them.
 *
 * Since K9 (doc 28 §3) the nav is PROGRESSIVE: this is the full set a mature
 * project reaches, not what any given project shows. A fresh project shows
 * `BASE_VIEWS` and earns the rest — so a test that walks this list must first
 * give the project something to reveal them with.
 */
export const ALL_VIEWS: readonly View[] = [
  'desk',
  'chat',
  'workers',
  'memory',
  'activity',
  'chart',
  'settings',
]

/**
 * What a project with nothing in it shows: four buttons, not eight.
 *
 * Memory arrives with the first memory, Activity with the first event, and the
 * Chart with the first SUBSCRIPTION — a fleet with nothing wired between its
 * workers has no shape worth drawing.
 */
export const BASE_VIEWS: readonly View[] = ['desk', 'chat', 'workers', 'settings']

/**
 * Logs in with the stack-e2e password account, clearing any auth state a
 * previous run left in localStorage — without this the login form never
 * appears on a re-run against a long-lived stack.
 */
export async function loginUI(page: Page): Promise<void> {
  await page.goto('/')
  await page.evaluate(() => localStorage.clear())
  await page.reload()
  await expect(page.getByTestId('login-email')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId('login-email').fill(TEST_EMAIL)
  await page.getByTestId('login-password').fill(TEST_PASSWORD)
  await page.getByTestId('login-submit').click()
  await expect(page.getByTestId('project-picker')).toBeVisible({ timeout: 30_000 })
}

/**
 * Logs in and opens a brand-new, EMPTY project, landing on the Desk.
 *
 * It does NOT go through the picker's create form any more, and that is the
 * point. Since the onboarding change, creating a project through the UI starts
 * an interview: it applies `onboarding@v1` and provisions a container for the
 * interviewer session. Thirteen specs that only wanted a namespace would each
 * burn a container and a host port, and `port-pool.stack.spec.ts` runs against
 * a deliberately narrowed pool.
 *
 * So this mints the project token through the API the shell itself uses
 * (`POST /auth/project-token`, the wildcard grant), writes it into the shell's
 * own localStorage state, and reloads. The project ends up genuinely empty —
 * no interviewer, no session, no container — which is what every caller of
 * this helper was asserting against before onboarding existed.
 *
 * Use `openOnboardingProject` when the interview is what you are testing.
 */
export async function openFreshProject(page: Page, prefix = 'e2e-ui'): Promise<string> {
  const project = uniqueProject(prefix)
  await loginUI(page)
  await page.evaluate(async (id) => {
    const KEY = 'agent-bob-auth'
    const state = JSON.parse(localStorage.getItem(KEY) ?? '{}') as {
      loginToken?: string
      projects?: { id: string; token: string }[]
      selectedProject?: string | null
    }
    const res = await fetch('/auth/project-token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ token: state.loginToken, project: id }),
    })
    if (!res.ok) throw new Error(`project-token for ${id}: HTTP ${res.status}`)
    const minted = (await res.json()) as { id: string; token: string }
    state.projects = [...(state.projects ?? []).filter((p) => p.id !== minted.id), minted]
    state.selectedProject = minted.id
    localStorage.setItem(KEY, JSON.stringify(state))
  }, project)
  await page.reload()
  await expect(page.getByTestId('session-sidebar')).toBeVisible({ timeout: 30_000 })
  openedProjects.push(project)
  return project
}

/**
 * Logs in and creates a project through the picker, goal and all, landing on
 * the ONBOARDING screen with a real interview session starting behind it.
 *
 * This is the slow path on purpose: it provisions a container. Use it only in
 * a spec whose subject is onboarding itself.
 */
export async function openOnboardingProject(
  page: Page,
  prefix: string,
  goal: string,
): Promise<string> {
  const project = uniqueProject(prefix)
  await loginUI(page)
  await page.getByTestId('new-project-input').fill(project)
  // By label — see the note in stack.spec.ts: the goal box is multiline, and
  // its test id does not land on the control the human types into.
  await page.getByLabel('What is this project for?').fill(goal)
  await page.getByTestId('new-project-create').click()
  await expect(page.getByTestId('onboarding-rail')).toBeVisible({ timeout: 30_000 })
  openedProjects.push(project)
  return project
}

/** Projects a browser test created, so their sessions can be released. */
const openedProjects: string[] = []

/**
 * Deletes every session in every project a browser test opened.
 *
 * Sessions started through the UI belong to no ProjectClient, so nothing else
 * knows about them — and each one holds a running container until deleted. Call
 * this from an afterEach in any spec that uses `openFreshProject`.
 */
export async function cleanupOpenedProjects(request: APIRequestContext): Promise<void> {
  for (const project of openedProjects.splice(0)) {
    const client = await projectClient(request, project).catch(() => null)
    if (client) await client.cleanup()
  }
}

/** Switches the workspace view and waits for the button to read as selected. */
export async function gotoView(page: Page, view: View): Promise<void> {
  await page.getByTestId(`nav-${view}`).click()
}

/** The project the switcher currently shows — proof of which project is active. */
export async function selectedProject(page: Page): Promise<string> {
  return page.getByTestId('project-switcher').inputValue()
}

/**
 * The chat composer — scoped by its placeholder, never by tag.
 *
 * `page.locator('textarea')` used to be safe when chat was the whole app. The
 * shell now has eight views and the composer is only mounted in one of them
 * (`view === 'chat'` in App.tsx), while other views mount textareas of their
 * own (worker prompts, memory content). A bare tag locator therefore either
 * finds nothing or finds the wrong box, and "not found" reads exactly like
 * "still disabled" in the failure message.
 */
export function composer(page: Page) {
  return page.getByPlaceholder('Type a message...')
}

/**
 * Starts a session and shows it.
 *
 * Clicking "New session" creates the session but does NOT change the view, and
 * the landing view is the Desk (decision K1) — so the composer is not mounted
 * until something switches to chat. Every spec that talks to a new session
 * needs both halves; doing only the first is how this suite spent weeks
 * timing out on an element that was never going to exist.
 */
export async function newSessionInChat(page: Page): Promise<void> {
  await page.getByTestId('new-session').click()
  await gotoView(page, 'chat')
}

/**
 * Sends a prompt and waits until the assistant's reply has stopped growing.
 *
 * The settling matters: a turn is only fully persisted once it ends, and a
 * reload taken mid-stream leaves the chat blank until the stream resolves (see
 * e2e/README.md). Any test that reloads or navigates after talking must wait
 * here first, or it is racing the model rather than testing the feature.
 */
export async function sendAndSettle(page: Page, prompt: string, timeoutMs = 120_000): Promise<string> {
  const textarea = composer(page)
  await expect(textarea).toBeEnabled({ timeout: timeoutMs })
  await textarea.fill(prompt)
  await page.locator('button[aria-label="Send"]').click()

  const reply = page.locator('[data-role="assistant"]').last()
  await expect(reply).toBeVisible({ timeout: timeoutMs })

  let last = ''
  let stableSince = Date.now()
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const text = (await reply.textContent()) ?? ''
    if (text !== last) {
      last = text
      stableSince = Date.now()
    } else if (text.length > 0 && Date.now() - stableSince > 3_000) {
      return last
    }
    await page.waitForTimeout(100)
  }
  throw new Error(`assistant reply never settled within ${timeoutMs}ms; last text: ${last.slice(0, 200)}`)
}
