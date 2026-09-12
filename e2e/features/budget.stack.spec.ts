import { test, expect } from '@playwright/test'
import { cleanupOpenedProjects, gotoView, openFreshProject } from '../helpers/ui'
import { newProjectClient, type ProjectClient } from '../helpers/api'

// Browser e2e for A5 — the budget panel
// (design/2026-09-11-onboarding-work-plan.md §1.1-1.4, A5).
//
// Two claims:
//
//  1. The test login is a WILDCARD login (§1.1: `writeLoginResponse` mints
//     `operator: true` on every per-project token it hands out), so against
//     the real stack it is always an operator — the panel shows a working
//     limits form, and editing the hard limit through it round-trips through
//     a real reload.
//  2. A non-operator gets the numbers and the fixed sentence, never the
//     form. The stack has no non-operator login to drive this with (§1.1:
//     only a non-wildcard Google account is ever minted a token without the
//     claim, and there is no such account in the e2e environment), so this
//     half mocks `GET /agent/whoami` in the browser — the one place doing so
//     is honest, because the claim under test is precisely "the gate reads
//     this route", not "a real non-operator token can be minted here".
test.describe('the budget panel (A5)', () => {
  let client: ProjectClient

  test.afterEach(async ({ request }) => {
    await cleanupOpenedProjects(request)
    if (client) await client.cleanup()
  })

  test('an operator sees the env default, edits the hard limit, and it survives a reload', async ({
    page,
    request,
  }) => {
    const project = await openFreshProject(page, 'e2e-budget')
    client = await newProjectClient(request, project)

    // A fresh project's budget is the env default applied at
    // DefaultProjectSettings — 0/0 ("off") unless the stack sets
    // AGENTKIT_DEFAULT_DAILY_TOKENS_HARD/SOFT (§1.3). Read it from the API
    // rather than hardcoding a number, so the spec holds either way.
    const before = await client.getSettings()

    await gotoView(page, 'settings')
    const panel = page.getByTestId('budget-panel')
    await expect(panel).toBeVisible({ timeout: 30_000 })

    const limitsForm = page.getByTestId('budget-limits-form')
    await expect(limitsForm, 'the test login is a wildcard login — always the operator').toBeVisible()

    const hardField = page.getByLabel('Hard limit')
    await expect(hardField).toHaveValue(String(before.daily_tokens_hard))

    const nextHard = before.daily_tokens_hard + 5000
    await hardField.fill(String(nextHard))
    await page.getByLabel('Why?').fill('e2e: raising the hard limit')
    await page.getByRole('button', { name: /save limits/i }).click()
    await expect(page.getByText(/no unsaved changes/i)).toBeVisible({ timeout: 15_000 })

    // Round-tripped on the server, not just in the form's own state.
    await expect
      .poll(async () => (await client.getSettings()).daily_tokens_hard)
      .toBe(nextHard)

    // …and a reload reads the new value back from the route, not from
    // anything the browser cached.
    await page.reload()
    await gotoView(page, 'settings')
    await expect(page.getByTestId('budget-panel')).toBeVisible({ timeout: 30_000 })
    await expect(page.getByLabel('Hard limit')).toHaveValue(String(nextHard))
  })

  test('a non-operator sees the numbers and the fixed sentence, never the form', async ({ page, request }) => {
    // No non-operator login exists in this environment (§1.1) — the ONE
    // route this spec mocks, and only to prove the client-side gate really
    // is wired to whoami rather than to some other guess.
    await page.route('**/agent/whoami', async (route) => {
      const response = await route.fetch()
      const body = (await response.json()) as Record<string, unknown>
      await route.fulfill({ response, json: { ...body, operator: false } })
    })

    const project = await openFreshProject(page, 'e2e-budget-noop')
    client = await newProjectClient(request, project)

    await gotoView(page, 'settings')
    await expect(page.getByTestId('budget-panel')).toBeVisible({ timeout: 30_000 })

    await expect(page.getByText(/only the operator can change this/i)).toBeVisible()
    await expect(page.getByTestId('budget-limits-form')).toHaveCount(0)
    // The numbers are still there — the gate hides the FORM, not the panel.
    await expect(page.getByText(/soft limit:/i)).toBeVisible()
  })
})
