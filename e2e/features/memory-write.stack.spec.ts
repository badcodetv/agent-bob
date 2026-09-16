import { test, expect } from '@playwright/test'
import { cleanupOpenedProjects, gotoView, openFreshProject } from '../helpers/ui'
import { projectClient, type ProjectClient } from '../helpers/api'

// Browser e2e for "Write a note" (design/2026-09-11-onboarding-and-the-guide.md
// §3 G8, work plan C4): the first write control the Memory page has ever had.
//
// Two things only a stack run can prove, that memories.test.ts and
// MemoryBrowserPage.test.tsx (mocked fetch) cannot:
//
//   * the body the browser actually sends survives the wire into a real
//     Postgres row with EMPTY provenance — the trust rule docs/20-datasets.md
//     §9 describes (`created_by_worker == "" && created_by_session == ""`
//     means "the application's own word", never a container's);
//   * "Publish a new version" against the real `name=` convention: the row it
//     writes becomes the NEWEST one, so `GET /agent/memories/current?name=…`
//     — the route an embedder actually reads the rulebook through — returns
//     the new content, not the old.
//
// DISCOVERED ISSUE (see this ticket's report): the Memory nav entry is
// revealed only once a project already has a memory (K9 / examples/web
// App.tsx:296-298), so a project with none has no UI path to its own first
// "Write a note" click — the very afterr-approval gap G8 exists to close.
// This spec sidesteps that by seeding one memory over the API first, the same
// way a worker's first `memory_create` would; it does not exercise the
// zero-memory case, which has no UI path to test yet.

test.describe('memory: write a note (G8)', () => {
  test.describe.configure({ mode: 'serial' })
  test.setTimeout(120_000)

  test.afterEach(async ({ request }) => {
    await cleanupOpenedProjects(request)
  })

  test('a note written through the console lands with empty provenance', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-memwrite')
    const client: ProjectClient = await projectClient(request, project)

    // Seed one memory so the Memory nav entry reveals (K9) — see the note
    // above the describe block.
    const seedResp = await client.raw('POST', '/agent/memories', {
      labels: { kind: 'seed' },
      content: 'A memory that already existed.',
    })
    expect(seedResp.ok()).toBe(true)

    await expect(page.getByTestId('nav-memory')).toBeVisible({ timeout: 30_000 })
    await gotoView(page, 'memory')

    await expect(page.getByTestId('write-a-note')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('write-a-note').click()

    const NOTE_TEXT = 'Quote the order number in every reply, not the customer name.'
    await page.getByTestId('note-content').fill(NOTE_TEXT)
    await page.getByTestId('note-labels').fill('kind=e2e-write-note')
    await page.getByTestId('note-why').fill('a human noticed a pattern worth remembering')
    await page.getByRole('button', { name: 'Write it' }).click()

    // The list refreshes and shows what was just written (C4's acceptance:
    // the row appears — the highlight itself is a CSS/localStorage-free
    // affordance already unit-tested, not re-proved here).
    await expect(page.getByText(NOTE_TEXT)).toBeVisible({ timeout: 15_000 })

    // The trust rule, against the real database: find the row by its unique
    // label and check both provenance fields are empty, not merely absent
    // from the response the browser happened to render.
    const listResp = await client.raw(
      'GET',
      '/agent/memories?selector=' + encodeURIComponent('kind=e2e-write-note'),
    )
    expect(listResp.ok()).toBe(true)
    const { memories } = (await listResp.json()) as { memories: Array<Record<string, unknown>> }
    expect(memories).toHaveLength(1)
    // The LIST route returns `agentdb.MemorySearchResult` — which carries
    // `snippet`, NOT `content` (go/agentdb/memories.go:180-197). This spec
    // asserted `content` and so compared undefined against the note until D2
    // ran it. Full content has its own route, and asserting through it proves
    // the note really landed rather than trusting a search snippet.
    const id = memories[0]!.id as string
    expect(memories[0]!.created_by_worker).toBe('')
    expect(memories[0]!.created_by_session).toBe('')

    const fullResp = await client.raw('GET', `/agent/memories/${id}`)
    expect(fullResp.ok()).toBe(true)
    const full = (await fullResp.json()) as Record<string, unknown>
    expect(full.content).toBe(NOTE_TEXT)
    expect(full.created_by_worker).toBe('')
    expect(full.created_by_session).toBe('')
  })

  test('publishing a new registry version becomes the current one', async ({ page, request }) => {
    const project = await openFreshProject(page, 'e2e-memregistry')
    const client: ProjectClient = await projectClient(request, project)

    const OLD_RULES = 'kind: lesson\nworker: email-answerer'
    const seedResp = await client.raw('POST', '/agent/memories', {
      labels: { name: 'label-registry' },
      content: OLD_RULES,
    })
    expect(seedResp.ok()).toBe(true)

    await expect(page.getByTestId('nav-memory')).toBeVisible({ timeout: 30_000 })
    await gotoView(page, 'memory')

    await expect(page.getByText('name=label-registry')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('publish-registry-version').click()

    // Pre-fill proves the "same row's labels and content" half of the
    // acceptance criteria, against a REAL stored row (not a mocked fetch).
    await expect(page.getByTestId('note-content')).toHaveValue(OLD_RULES)
    await expect(page.getByTestId('note-labels')).toHaveValue('name=label-registry')

    const NEW_RULES = 'kind: lesson\nworker: email-answerer\nkind: fact'
    await page.getByTestId('note-content').fill(NEW_RULES)
    await page.getByTestId('note-why').fill('adding a fact rule the architect asked for')
    await page.getByRole('button', { name: 'Publish it' }).click()

    await expect(page.getByText(NEW_RULES)).toBeVisible({ timeout: 15_000 })

    // memory_current semantics: the NEWEST row with this name is now current,
    // over the same route an embedder reads the rulebook through.
    //
    // Polled, not read once. The visibility assertion above cannot stand as
    // proof that the row landed: `NEW_RULES` is also the text sitting in the
    // form's own textarea, so it matches whether or not the POST has
    // completed. D2 caught that as a flake — this test failed reading the
    // seed's content back, and passed in isolation, because the race is with
    // the publish request, not with the database. Polling the route asserts
    // the same fact without depending on which won. See DI27.
    await expect
      .poll(
        async () => {
          const resp = await client.raw('GET', '/agent/memories/current?name=label-registry')
          if (!resp.ok()) return null
          return ((await resp.json()) as Record<string, unknown>).content
        },
        { timeout: 15_000 },
      )
      .toBe(NEW_RULES)

    const currentResp = await client.raw('GET', '/agent/memories/current?name=label-registry')
    expect(currentResp.ok()).toBe(true)
    const current = (await currentResp.json()) as Record<string, unknown>
    expect(current.created_by_worker).toBe('')
    expect(current.created_by_session).toBe('')
  })
})
