import { test, expect } from '@playwright/test'
import { cleanupOpenedProjects, gotoView, openFreshProject } from '../helpers/ui'
import { projectClient } from '../helpers/api'
import { waitForConfigEvents } from '../helpers/configlog'

// Browser e2e for PR0 (design/2026-09-11-onboarding-and-the-guide.md §6): the
// "Revert to this version" control is real, but until this ticket it lived
// only inside `ChangelogView`, which nothing in the shipped shell mounts —
// `ActivityPage` replaced it, and its `changes` rows carried an "open the
// session" link and nothing else. The console's own copy promised otherwise
// (`CharterPanel.tsx`, `RunArchitectControl.tsx`: "every change can be
// reverted from the changelog"), so this is the test that makes that sentence
// true again, against the real running stack rather than a fixture fold.
//
// Two claims:
//
//  1. Reverting the newest change to a worker from the Activity rail writes a
//     NEW (compensating) config event and actually puts the field back —
//     proved against the server, not the DOM (nothing here is erased).
//  2. Only the newest change to a thing can be put back: the console disables
//     the button on an older entry to the same worker with a readable reason,
//     and the route itself refuses the same request with 409 + that reason —
//     the button is a courtesy, the route is the gate (configLog.ts's own
//     words for the split).

test.describe('revert from the Activity rail (PR0)', () => {
  test.afterEach(async ({ request }) => {
    await cleanupOpenedProjects(request)
  })

  test('revert puts a worker field back, and a superseded entry is refused', async ({
    page,
    request,
  }) => {
    const project = await openFreshProject(page, 'e2e-revert')
    const api = await projectClient(request, project)

    // Two changes to the SAME worker: the create, then an edit. Both are
    // config events keyed to the same entity, which is what makes the second
    // one block the first from being reverted.
    await api.putWorker('email-answerer', {
      description: 'answers inbound email',
      system_prompt: 'You answer customer email.',
      rationale: 'e2e: hiring the email answerer',
    })
    await api.putWorker('email-answerer', {
      description: 'answers inbound email, briefly',
      rationale: 'e2e: shorten the description',
    })

    // The Activity nav item is progressive (K9): it is earned by the first
    // project EVENT, not by a config event, so without this the rail itself
    // never appears and `gotoView` has nothing to click.
    await api.postEvent({ type: 'e2e.revert-setup', text: 'reveal the activity rail' })

    const [update, create] = await waitForConfigEvents(api, 2)
    expect(update.action).toBe('worker_update')
    expect(create.action).toBe('worker_create')

    // Reload so the shell's progressive nav re-counts against what the API
    // calls above just wrote, rather than waiting out the 10s reveal poll.
    await page.reload()
    await expect(page.getByTestId('session-sidebar')).toBeVisible({ timeout: 30_000 })
    await gotoView(page, 'activity')
    await expect(page.getByTestId('activity-rail')).toBeVisible({ timeout: 15_000 })
    // Narrow to config changes: the lens is the one that carries the control,
    // and the assertion below cares about ROWS, not about the event/job noise
    // the setup above also produced.
    await page.getByTestId('activity-lens-changes').click()

    // ── the older entry is blocked before anything is reverted ──────────────
    const blockedOlder = page.getByTestId(`revert-blocked-${create.id}`)
    await expect(blockedOlder).toBeVisible({ timeout: 15_000 })
    await expect(blockedOlder).toContainText('Something changed')
    // A blocked control has no working button behind it — assert the ENABLED
    // one exists on the newest entry instead of trying (and failing) to click
    // a disabled one, which proves nothing beyond MUI's own semantics.
    await expect(page.getByTestId(`revert-${update.id}`)).toBeEnabled()

    // ── revert the newest change ─────────────────────────────────────────────
    await page.getByTestId(`revert-${update.id}`).click()
    await expect(page.getByText('Revert to this version?')).toBeVisible()
    await page.getByLabel('Why?').fill('e2e: the shorter description read worse')
    await page.getByTestId('revert-confirm').click()
    await expect(page.getByTestId('revert-done')).toBeVisible({ timeout: 15_000 })

    // The claim is about the SERVER: a third, compensating config event
    // exists, and the field is genuinely back — not merely that the dialog
    // closed and a success alert appeared, which a UI that reverted nothing
    // could still show.
    const afterRevert = await waitForConfigEvents(api, 3)
    expect(afterRevert[0]!.action).toBe('worker_update')
    expect(afterRevert[0]!.rationale).toContain('the shorter description read worse')
    expect((await api.getWorker('email-answerer')).description).toBe('answers inbound email')

    // ── the route itself refuses a revert of a now-doubly-superseded entry ──
    // (the console's disabled button is a courtesy — configLog.ts calls the
    // server's own check "the gate" — so the refusal is proved directly
    // against the route the button would have called.)
    const refused = await api.raw('POST', `/agent/config-events/${create.id}/revert`, {
      rationale: 'e2e: trying to go back further than the console allows',
    })
    expect(refused.status()).toBe(409)
    expect(await refused.text()).toContain('email-answerer')
  })
})
