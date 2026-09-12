import { test, expect, type Page } from '@playwright/test'
import { BASE_VIEWS, cleanupOpenedProjects, gotoView, openFreshProject } from '../helpers/ui'
import { projectClient, type ProjectClient } from '../helpers/api'
import { configEvents, waitForConfigAction } from '../helpers/configlog'

// Browser e2e for the operator console (docs 15/16/21, and the IA change in
// 28/29): the reveal curve, the Activity rail, the worker's Triggers tab, the
// Desk, the org chart, and the two write gestures that only exist on the canvas.
//
// **UNRUN AT AUTHORING TIME (2026-08-14).** Rewritten for the new IA by the D1
// executor, who found a compose stack already running and serving a pre-change
// web image. Rebuilding it (`docker compose up -d --build web`) would have
// changed what someone else was looking at, so it was left alone — the same
// call, and the same note, as the F1 executor left on the emit test below.
// **Someone with the stack to themselves must run this.** It typechecks; that
// is all that is currently proved.
//
// Everything here was previously proved only by unit tests and by a human
// looking at screenshots. The live pass (doc 21) drove READS; what had never
// run under a real pointer is the pair of chart write flows — wiring two
// workers together and freezing one — both of which refuse to proceed without
// a reason. That refusal is the console's whole thesis (K2), so it is the part
// most worth pinning: a regression that made the reason optional would leave
// every screen looking correct and quietly gut the changelog.
//
// Conventions borrowed from product-ui.stack.spec.ts: run-scoped projects, and
// an afterEach that deletes sessions (each holds a container and a host port).

// Worker names are deliberately NOT substrings of one another — `mentionsWorkerName`
// and several assertions match on names, and `book-keeper` matching inside
// `keeper` has bitten this codebase before (doc 16, OC3).
const WRITER = 'cx-scribe'
const CRITIC = 'cx-judge'
const THIRD = 'cx-herald'

const CRITERION = 'every blurb opens with a headline line'
const SEED = 'You write product blurbs for the fruit catalogue.'

/** The chart canvas — every plate, wire and label lives inside it. */
function canvas(page: Page) {
  return page.getByTestId('org-chart-canvas')
}

/**
 * Opens a plate's action menu through the keyboard path.
 *
 * The plate is focusable and answers Enter precisely so this is testable
 * without synthesising a drag; the pointer path opens the same menu (§K3).
 */
async function openNodeMenu(page: Page, worker: string): Promise<void> {
  const plate = page.getByTestId(`node-${worker}`)
  await expect(plate).toBeVisible({ timeout: 15_000 })
  await plate.focus()
  await plate.press('Enter')
  await expect(page.getByRole('menu', { name: `${worker} actions` })).toBeVisible({ timeout: 10_000 })
}

/** Applies actor-critic@v1 through the onboarding flow, as a human would. */
async function applyActorCriticViaUI(page: Page): Promise<void> {
  await gotoView(page, 'workers')
  await page.getByRole('button', { name: 'Start from a topology' }).first().click()
  await page.getByRole('button', { name: 'Choose actor-critic' }).click()

  await page.getByLabel(/Name for the actor/).fill(WRITER)
  await page.getByLabel(/What should the actor do/).fill(SEED)
  await page.getByLabel(/Name for the critic/).fill(CRITIC)
  await page.getByLabel(/What does good mean/).fill(CRITERION)

  await page.getByRole('button', { name: 'Preview' }).click()
  const apply = page.getByRole('button', { name: 'Apply topology' })
  await expect(apply).toBeEnabled({ timeout: 30_000 })
  await apply.click()
  // The done step is the happens-after signal that the apply committed.
  await expect(page.getByRole('button', { name: 'View workers' })).toBeVisible({ timeout: 30_000 })
}

test.describe('operator console', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(300_000)

  test.afterEach(async ({ request }) => {
    await cleanupOpenedProjects(request)
  })

  // ── (a) every view renders ─────────────────────────────────────────────────
  //
  // Cheap, and it is the test that would have caught the nav squash and any
  // page that throws on an empty project. A view that crashes renders nothing
  // and the nav button stays unselected, so "is the button now contained" is
  // a real assertion about the page having mounted.
  test('a fresh project shows FOUR views, and the Desk shows its first run', async ({ page }) => {
    await openFreshProject(page, 'e2e-cx-nav')

    // The Desk is the landing view (App.tsx defaults to it) and an empty
    // project must get the first-run panel, never a bare "nothing to show".
    await expect(page.getByText('This project has no workers yet')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByRole('button', { name: 'Start from an org chart' })).toBeVisible()

    const pageErrors: string[] = []
    page.on('pageerror', (e) => pageErrors.push(String(e)))

    // K9: an empty project shows FOUR buttons, not eight. This is the assertion
    // the whole IA change exists for — the complaint was "so many pages I get
    // lost", and a fresh project meeting seven of them was the cause.
    for (const view of BASE_VIEWS) {
      await gotoView(page, view)
      await expect(page.getByTestId(`nav-${view}`)).toBeVisible()
      await expect(page.getByTestId('session-sidebar')).toBeVisible()
    }

    // And the earned ones are genuinely absent, not merely disabled.
    for (const hidden of ['memory', 'activity', 'chart']) {
      await expect(page.getByTestId(`nav-${hidden}`)).toHaveCount(0)
    }

    expect(pageErrors, 'no view may throw while rendering an empty project').toEqual([])
  })

  // ── the reveal curve: entries arrive when the project earns them ───────────
  test('a second worker reveals the Chart, and an event reveals Activity', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-reveal')
    const api = await projectClient(request, project)

    await expect(page.getByTestId('nav-chart')).toHaveCount(0)
    await expect(page.getByTestId('nav-activity')).toHaveCount(0)

    // ── the first worker reveals ACTIVITY, and this is not what it looks like
    //
    // Activity is revealed by the project's first EVENT, and hiring a worker
    // produces one: every configuration mutation emits `config.changed` as a
    // project event (§15.8), so the first `PUT /agent/workers/{name}` is also
    // the first thing that ever happened in this project. Verified against the
    // running stack: a fresh project goes from 0 events to 1 on that call.
    //
    // This assertion used to sit at the END of the test, after an explicitly
    // posted event, and could never have held there — Activity was already
    // sticky two reloads earlier, so `appeared` was empty and the notice was
    // never drawn. The file's own header says it was UNRUN at authoring time;
    // this is what that cost.
    await api.putWorker(WRITER, { system_prompt: SEED, description: 'writes blurbs' })
    await page.reload()
    await expect(page.getByTestId('nav-workers')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('nav-activity')).toBeVisible({ timeout: 30_000 })
    // Announced in words rather than by a badge (design 28 §3.2).
    await expect(page.getByTestId('nav-reveal-notice')).toContainText(
      'lists everything this project does',
    )

    // One worker is not a SHAPE, though — there is nothing to wire it to.
    await expect(page.getByTestId('nav-chart')).toHaveCount(0)

    // Two workers is the first moment there is a PAIR to connect, which is the
    // first moment the canvas has a job. Gating this on "a subscription exists"
    // would make the gesture that creates the first subscription unreachable.
    await api.putWorker(THIRD, { system_prompt: 'You announce finished work.', description: 'announces' })
    await page.reload()
    await expect(page.getByTestId('nav-chart')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('nav-reveal-notice')).toContainText(
      'draws which worker wakes which',
    )

    // And an ordinary posted event reveals nothing new — Activity is sticky,
    // so there is no second announcement for it.
    await api.postEvent({ type: `${WRITER}.task`, text: 'Write a blurb about the new apples.' })
    await page.reload()
    await expect(page.getByTestId('nav-activity')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('nav-reveal-notice')).toHaveCount(0)
  })

  // ── Activity: one rail carrying more than one kind of record ──────────────
  test('the Activity rail interleaves a config change and a job in time order', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-activity')
    const api = await projectClient(request, project)

    await api.putWorker(WRITER, { system_prompt: SEED, description: 'writes blurbs' })
    await api.postEvent({ type: `${WRITER}.task`, text: 'Write a blurb about the new apples.' })
    await page.reload()

    await gotoView(page, 'activity')
    const rail = page.getByTestId('activity-rail')
    await expect(rail).toBeVisible({ timeout: 30_000 })

    // The worker's creation is a config change; the event is an event. Both are
    // on ONE rail — that is the merge, and the thing five tabs used to hide.
    await expect(rail).toContainText(WRITER)
    await expect(rail).toContainText(`${WRITER}.task`)

    // The chips subset the rail IN PLACE. If a chip re-mounted the list this
    // would be a rename rather than a merge, so the rail must survive the click.
    await page.getByTestId('activity-lens-changes').click()
    await expect(page.getByTestId('activity-rail')).toBeVisible()
    await expect(page.getByTestId('activity-rail')).not.toContainText(`${WRITER}.task`)
    await page.getByTestId('activity-lens-all').click()
    await expect(page.getByTestId('activity-rail')).toContainText(`${WRITER}.task`)
  })

  // ── Triggers: what wakes a worker is edited on the worker ─────────────────
  test('a schedule is created from the worker’s Triggers tab, with a reason', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-triggers')
    const api = await projectClient(request, project)
    await api.putWorker(WRITER, { system_prompt: SEED, description: 'writes blurbs' })

    await gotoView(page, 'workers')
    await page.getByText(WRITER, { exact: true }).first().click()

    // Configuration answers "what makes this run?" in zero clicks, before the
    // tab is even opened (doc 28 §2.2).
    await expect(page.getByTestId('woken-by')).toContainText('Nothing wakes this worker yet', {
      timeout: 30_000,
    })

    await page.getByTestId('edit-triggers').click()
    await page.getByTestId('new-schedule').click()

    // The editor's own labels (ScheduleEditor.tsx): "Cron", "Instruction",
    // "Why?" (renamed from "Rationale" by C3 — the repo's reason field is one
    // word everywhere now). This block previously asked for
    // /what should .* do|input/i and /^Why\??$/, and neither ever matched
    // anything on that form — "Instruction" does not contain "input", and the
    // reason field was then called Rationale. Another consequence of the
    // UNRUN note at the top of this file; see the plan's DI8.
    // FIXED (plan DI9). This line used to type the worker's name in by hand,
    // with a note explaining why it had to: the editor did not prefill the
    // worker even when opened from that worker's own Triggers tab.
    // `WorkerTriggers` passed `schedule={null}` while holding `workerName` and
    // never handed it over, and `validateSchedule` keeps the save button
    // disabled without one — so the form asked which worker it was for while
    // displaying the answer two inches away. Both editors now take a
    // `defaultWorker`, and the hand-fill has become the assertion that they do.
    await expect(page.getByLabel(/^Worker$/).first()).toHaveValue(WRITER)
    await page.getByLabel(/cron/i).first().fill('0 9 * * 1-5')
    await page.getByLabel(/^Instruction$/).first().fill('Write the morning blurb.')
    await page.getByLabel(/^Why\?$/).first().fill('the catalogue goes out at nine')
    // "Create schedule" for a new one, "Save schedule" for an existing one —
    // /save/i matched neither on this form. The third assertion in this file
    // that could never have passed (DI8).
    await page.getByRole('button', { name: /create schedule|save schedule/i }).first().click()

    // The engine, not the screen, is the proof — but the save is a round trip,
    // so poll rather than reading once. The previous version asked the API in
    // the same tick as the click and could only ever have passed by luck.
    await expect
      .poll(
        async () => (await api.listSchedules()).some((sc) => sc.worker === WRITER),
        { timeout: 30_000, message: 'the schedule never reached the engine' },
      )
      .toBe(true)
  })

  // ── (b) + (c) the topology flow, the chart it draws, and traffic on a wire ──
  test('applying actor-critic@v1 draws the chart, records why, and counts traffic', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-top')
    const api: ProjectClient = await projectClient(request, project)

    await applyActorCriticViaUI(page)

    // ── the chart: two plates, two wires ────────────────────────────────────
    await gotoView(page, 'chart')
    await expect(canvas(page)).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId(`node-${WRITER}`)).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId(`node-${CRITIC}`)).toBeVisible()

    // Scoped to the canvas on purpose: the wire-proposal DIALOG also carries a
    // testid starting `wire-`, so an unscoped ^wire- count is a trap.
    const wires = canvas(page).locator('[data-testid^="wire-"]')
    await expect(wires).toHaveCount(2, { timeout: 30_000 })

    // ── the Desk: a seeded org must record its reasons ──────────────────────
    //
    // This is doc 21's live finding turned into a regression: a topology that
    // applied the zero ConfigWrite gave a brand-new operator five
    // "(no reason given)" rows on the one screen that exists to keep the
    // system honest. ApplyTopology now defaults the rationale.
    await gotoView(page, 'desk')
    await expect(page.getByText('CHANGES', { exact: false }).first()).toBeVisible({ timeout: 30_000 })
    await expect(page.getByText('seeded from actor-critic@v1').first()).toBeVisible({ timeout: 30_000 })
    await expect(page.getByText('(no reason given)')).toHaveCount(0)

    // The log agrees with the screen.
    const applied = await waitForConfigAction(api, 'topology_apply')
    expect(applied.rationale).toBe('seeded from actor-critic@v1')

    // ── (c) real traffic puts a count on the wire ───────────────────────────
    //
    // Posted through the API helper rather than the UI: this asserts that the
    // CHART reflects the backend, so the event must arrive by a path the chart
    // has nothing to do with.
    const event = await api.postEvent({ type: `${WRITER}.task`, text: 'Write a blurb about the new apples.' })
    const delivered = await api.waitForDeliveries(
      (rows) => rows.some((d) => d.status === 'ok'),
      { event_id: event.id, timeoutMs: 240_000 },
    )
    expect(delivered.some((d) => d.status === 'ok')).toBe(true)

    // The chart polls, so this is a poll-until-visible, not a sleep-then-read.
    await gotoView(page, 'chart')
    await expect(canvas(page).locator('[data-testid^="traffic-"]').first()).toContainText('↳ ×1', {
      timeout: 60_000,
    })
  })

  // ── (b2) the first-run path, entirely from the browser (F1 / RD17) ─────────
  //
  // Test (b) posts its event through the API helper on purpose — it is asserting
  // that the CHART reflects the backend. This one asserts the opposite thing:
  // that a human who has just applied an event-driven topology can make
  // something happen WITHOUT leaving the console. Until F1 there was no POST to
  // /agent/events anywhere in the browser at all, so this journey — apply a
  // topology, wake it, watch the job finish — simply could not be walked, which
  // is readiness bar #4 and doc 22's blocker.
  //
  // UNRUN AT AUTHORING TIME: written by the F1 executor, who was barred from the
  // compose stack (another agent held it). The orchestrator must run it.
  test('emitting an event from the console wakes the topology just applied', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-emit')
    const api: ProjectClient = await projectClient(request, project)

    await applyActorCriticViaUI(page)

    // Emitting moved to the chart's propagation panel (doc 28 §4.2): the same
    // question — "what would this wake?" — answered on the shape instead of as
    // a list beside a canvas already drawing those subscriptions.
    await gotoView(page, 'chart')
    const editor = page.getByLabel('…or paste an event')
    await expect(editor).toBeVisible({ timeout: 30_000 })
    await editor.fill(
      JSON.stringify({ type: `${WRITER}.task`, text: 'Write a blurb about the new apples.' }, null, 2),
    )

    // Trace is the primary action, and it is a dry run: it draws the path on
    // the canvas and writes nothing.
    await page.getByRole('button', { name: 'Trace this event' }).click()
    await expect(page.getByTestId('depth-ruler')).toBeVisible({ timeout: 30_000 })
    expect(
      await api.listEvents({ type: `${WRITER}.task` }),
      'tracing must never write an event',
    ).toHaveLength(0)

    // Nothing is written until the confirm is accepted — the footgun guard.
    await page.getByTestId('emit-event').click()
    const confirm = page.getByRole('dialog')
    await expect(confirm).toBeVisible({ timeout: 10_000 })
    await expect(confirm).toContainText('wake its worker and start a job')
    expect(
      await api.listEvents({ type: `${WRITER}.task` }),
      'the confirm dialog must not have posted anything yet',
    ).toHaveLength(0)

    await confirm.getByRole('button', { name: 'Emit it' }).click()

    // The event exists, and the id the panel shows is the row the server made.
    const [event] = await api.waitForEvents((rows) => rows.length > 0, {
      type: `${WRITER}.task`,
      timeoutMs: 30_000,
    })
    await expect(page.getByText(event!.id)).toBeVisible({ timeout: 30_000 })

    // …and the delivery it produced runs to completion. This is the whole
    // first-run path: topology → event → job → ok, browser-driven end to end.
    const delivered = await api.waitForDeliveries(
      (rows) => rows.some((d) => d.status === 'ok'),
      { event_id: event!.id, timeoutMs: 240_000 },
    )
    expect(delivered.some((d) => d.status === 'ok')).toBe(true)
  })

  // ── (d) wiring two workers together, under a real pointer ──────────────────
  test('wiring two workers from the chart demands a why, and the changelog keeps it', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-wire')
    const api = await projectClient(request, project)

    // Two unrelated workers, so the wire this test draws is the only one.
    await api.putWorker(WRITER, { system_prompt: SEED, description: 'writes blurbs' })
    await api.putWorker(THIRD, { system_prompt: 'You announce finished work.', description: 'announces' })

    await gotoView(page, 'chart')
    await expect(page.getByTestId(`node-${WRITER}`)).toBeVisible({ timeout: 30_000 })

    await openNodeMenu(page, WRITER)
    await page.getByRole('menuitem', { name: `Wire ${WRITER} to another worker…` }).click()

    const dialog = page.getByTestId('wire-proposal')
    await expect(dialog).toBeVisible({ timeout: 10_000 })

    // The filter names the source — the caption says so, and it is what stops
    // the unfiltered worker.finished self-edge cycle (doc 16, OC1/OC4).
    await expect(dialog.getByTestId('proposal-filter')).toContainText(WRITER)

    // The reason is mandatory: with the target chosen and the why empty, the
    // commit button must still refuse.
    await dialog.getByLabel('Wake which worker?').click()
    await page.getByRole('option', { name: THIRD }).click()
    const wireIt = dialog.getByRole('button', { name: 'Wire it up' })
    await expect(wireIt, 'a wire with no reason must not be committable (K2)').toBeDisabled()

    const why = 'the herald should announce whatever the scribe finishes'
    await dialog.getByLabel('Why?').fill(why)
    await expect(wireIt).toBeEnabled()
    await wireIt.click()
    await expect(dialog).toBeHidden({ timeout: 30_000 })

    // The subscription exists, filtered to the source…
    const subs = await api.listSubscriptions()
    const wired = subs.find((s) => s.worker === THIRD)
    expect(wired, `a subscription waking ${THIRD} must exist`).toBeTruthy()
    expect(wired!.event_type).toBe('worker.finished')
    expect(wired!.filter).toMatchObject({ worker: WRITER })

    // …and the reason survived into the config log, which is the point.
    const created = await waitForConfigAction(api, 'subscription_create')
    expect(created.rationale).toBe(why)

    // The chart now draws it.
    await expect(canvas(page).locator('[data-testid^="wire-"]')).toHaveCount(1, { timeout: 30_000 })
  })

  // ── (e) freezing a worker from the chart ───────────────────────────────────
  test('freezing a worker from the chart records worker_freeze with its reason', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-cx-froz')
    const api = await projectClient(request, project)

    await api.putWorker(CRITIC, { system_prompt: 'You score answers.', description: 'the judge' })
    // TWO workers, and the second one is load-bearing rather than scenery.
    // Chart is revealed by `subscriptions > 0 || workers >= 2` — one worker
    // with nothing wired to it has no shape worth drawing — so this test with
    // a single worker was waiting for a tab that could not appear, and burned
    // its full 300s doing it. It had never actually run to find out: it sits
    // after the topology test in a serial describe, and that one failed first
    // for a different reason (DI12), so this was reported "did not run" on
    // every previous attempt. The same UNRUN note at the top of this file
    // (DI8) covers it.
    await api.putWorker(WRITER, { system_prompt: SEED, description: 'the scribe' })

    await gotoView(page, 'chart')
    await openNodeMenu(page, CRITIC)
    await page.getByRole('menuitem', { name: `Freeze ${CRITIC}` }).click()

    const dialog = page.getByTestId('worker-toggle-dialog')
    await expect(dialog).toBeVisible({ timeout: 10_000 })

    // K2 again: the freeze toggle asks for a reason in the same words as the
    // wire cut, and refuses without one (doc 16, OC4).
    const confirm = dialog.getByRole('button', { name: `Freeze ${CRITIC}` })
    await expect(confirm, 'a freeze with no reason must not be committable').toBeDisabled()

    const why = 'measurement instrument for the fee experiment'
    await dialog.getByLabel('Why?').fill(why)
    await expect(confirm).toBeEnabled()
    await confirm.click()
    await expect(dialog).toBeHidden({ timeout: 30_000 })

    // The store agrees…
    await expect
      .poll(async () => (await api.getWorker(CRITIC)).frozen, { timeout: 30_000 })
      .toBe(true)

    // …the log names the act, not a generic update…
    const frozen = await waitForConfigAction(api, 'worker_freeze')
    expect(frozen.rationale).toBe(why)
    const actions = (await configEvents(api)).map((e) => e.action)
    expect(actions, 'a freeze must never be logged as a bare worker_update').toContain('worker_freeze')

    // …and the plate says so, with the lock the design reserves for it.
    await expect(
      page.getByLabel('frozen — only a human may change it').first(),
    ).toBeVisible({ timeout: 30_000 })
  })

  // ── C2: "About this screen" on the Desk ────────────────────────────────────
  //
  // Only the Desk is exercised live here (the ticket's own scope): the eleven
  // mount points share one component and one provider, so the thing worth
  // proving against a real browser is the wiring — the build's generated
  // paragraphs actually reaching `AboutThisScreen` through `GuideProvider`,
  // the guide link actually resolving, and dismissal actually surviving a
  // reload — not eleven near-identical repeats of the same three facts.
  // `AboutThisScreen.test.tsx` (`web/src/components/`) covers the component's
  // own logic (no-paragraph ⇒ nothing rendered, per-surface/per-project
  // dismissal) at the unit level, against a mocked provider.
  test('the Desk\'s About line opens, links to a guide page that renders, and stays dismissed after reload', async ({ page }) => {
    await openFreshProject(page, 'e2e-cx-about')

    // Collapsed: the toggle line, nothing more.
    const toggle = page.getByTestId('about-toggle-desk')
    await expect(toggle).toBeVisible({ timeout: 30_000 })
    await expect(toggle).toContainText('About this screen')
    await expect(page.getByText('The Desk is where you start every visit.')).toHaveCount(0)

    // Opens on click, showing the guide's own first paragraph (docs/guide/the-desk.md)
    // — proof the generated paragraph actually reached the component through
    // build-guide.mjs → pages.generated.ts → App.tsx's GuideProvider, not a
    // placeholder string.
    await toggle.click()
    await expect(page.getByText('The Desk is where you start every visit.')).toBeVisible()

    // "Read more in the guide →" is a real link to a page that renders.
    const guideLink = page.getByRole('link', { name: 'Read more in the guide →' })
    await expect(guideLink).toHaveAttribute('href', '#/guide/the-desk')
    await guideLink.click()
    await expect(page.getByRole('heading', { name: 'The Desk' })).toBeVisible({ timeout: 15_000 })

    // Back to the Desk, dismiss the line, and reload: dismissal is sticky
    // per (surface, project) in localStorage (aboutDismissal.ts), so the
    // control that remains after a reload must be the small "bring it back"
    // one, never the full disclosure.
    await gotoView(page, 'desk')
    await page.getByTestId('about-toggle-desk').click()
    await page.getByTestId('about-dismiss-desk').click()
    await expect(page.getByTestId('about-restore-desk')).toBeVisible()

    await page.reload()
    await expect(page.getByTestId('nav-desk')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByTestId('about-restore-desk')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('about-toggle-desk')).toHaveCount(0)

    // And restoring it brings the full disclosure back, collapsed.
    await page.getByTestId('about-restore-desk').click()
    await expect(page.getByTestId('about-toggle-desk')).toBeVisible()
  })
})
