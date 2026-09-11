import { describe, expect, it } from 'vitest'
import { parseGuidePage, GUIDE_PART_LABELS } from './frontMatter.js'

const VALID = `---
title: A worker's instructions
slug: a-workers-instructions
part: 1            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 3
surfaces: [workers, worker-configuration]   # console surfaces whose About line uses this page
---

Every worker's behaviour comes from one prompt, changed by a human or another
worker.

## In the console

Workers → Configuration.

## What this will not do

It will not run two versions at once.
`

describe('parseGuidePage', () => {
  it('parses the §1.5 example whole', () => {
    const page = parseGuidePage(VALID, 'a-workers-instructions.md')
    expect(page.title).toBe("A worker's instructions")
    expect(page.slug).toBe('a-workers-instructions')
    expect(page.part).toBe(1)
    expect(page.order).toBe(3)
    expect(page.surfaces).toEqual(['workers', 'worker-configuration'])
    expect(page.firstParagraph).toBe(
      "Every worker's behaviour comes from one prompt, changed by a human or another worker.",
    )
    expect(page.body.startsWith("Every worker's behaviour")).toBe(true)
    expect(page.body).toContain('What this will not do')
  })

  it('accepts a nested slug (for-operators/inviting-someone)', () => {
    const raw = VALID.replace('slug: a-workers-instructions', 'slug: for-operators/inviting-someone')
    const page = parseGuidePage(raw, 'inviting-someone.md')
    expect(page.slug).toBe('for-operators/inviting-someone')
  })

  it('defaults surfaces to [] when the field is omitted', () => {
    const raw = VALID.split('\n').filter((l) => !l.startsWith('surfaces:')).join('\n')
    const page = parseGuidePage(raw, 'glossary.md')
    expect(page.surfaces).toEqual([])
  })

  it('covers every documented part number', () => {
    expect(Object.keys(GUIDE_PART_LABELS).map(Number).sort((a, b) => a - b)).toEqual([0, 1, 2, 3, 4, 9])
  })

  it('rejects a page missing the opening delimiter', () => {
    expect(() => parseGuidePage('title: x\n---\nbody', 'bad.md')).toThrow(/does not start with/)
  })

  it('rejects a page whose front matter is never closed', () => {
    expect(() => parseGuidePage('---\ntitle: x\nno close', 'bad.md')).toThrow(/never closed/)
  })

  it('rejects a missing title', () => {
    const raw = VALID.replace(/title:.*\n/, '')
    expect(() => parseGuidePage(raw, 'bad.md')).toThrow(/missing "title"/)
  })

  it('rejects a slug that is not lower-kebab', () => {
    const raw = VALID.replace('slug: a-workers-instructions', 'slug: A Workers Instructions')
    expect(() => parseGuidePage(raw, 'bad.md')).toThrow(/not lower-kebab/)
  })

  it('rejects a part number outside §4.2', () => {
    const raw = VALID.replace(/part: 1.*\n/, 'part: 7\n')
    expect(() => parseGuidePage(raw, 'bad.md')).toThrow(/part 7 is not one of/)
  })

  it('rejects a non-integer order', () => {
    const raw = VALID.replace('order: 3', 'order: three')
    expect(() => parseGuidePage(raw, 'bad.md')).toThrow(/"order" must be a whole number/)
  })

  it('rejects an unknown surface id', () => {
    const raw = VALID.replace('[workers, worker-configuration]', '[workers, automation]')
    expect(() => parseGuidePage(raw, 'bad.md')).toThrow(/"automation" is not a known surface/)
  })

  it('rejects a page with no first paragraph', () => {
    const raw = VALID.slice(0, VALID.indexOf('surfaces:')) +
      'surfaces: [workers, worker-configuration]\n---\n\n   \n'
    expect(() => parseGuidePage(raw, 'bad.md')).toThrow(/has no first paragraph/)
  })
})
