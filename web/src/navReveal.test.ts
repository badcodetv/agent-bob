// A3: progressive navigation — which nav entries a project has earned
// (docs/product/28-console-ia-design.md §3, decision K9).

import { describe, it, expect } from 'vitest'
import {
  NAV_ALWAYS,
  NAV_ENTRIES,
  NAV_LABELS,
  navRevealSentence,
  revealedNav,
  type NavCounts,
  type NavEntry,
} from './navReveal.js'

const counts = (over: Partial<NavCounts> = {}): NavCounts => ({
  workers: 0,
  memories: 0,
  events: 0,
  subscriptions: 0,
  ...over,
})

describe('day one', () => {
  it('shows four entries for an empty project', () => {
    const { visible } = revealedNav(counts())
    expect(visible).toEqual(['desk', 'chat', 'workers', 'settings'])
  })

  it('keeps the Desk even though it is empty — its first-run panel is the onboarding', () => {
    expect(revealedNav(counts()).visible).toContain('desk')
  })

  it('reveals nothing on the strength of ONE worker', () => {
    // One worker has produced no events, no memories, and has nothing to be
    // wired to, so there is still nothing behind the other three buttons.
    expect(revealedNav(counts({ workers: 1 })).visible).toEqual([
      'desk',
      'chat',
      'workers',
      'settings',
    ])
  })
})

describe('the reveal rules', () => {
  it('reveals Memory on the first memory', () => {
    expect(revealedNav(counts({ memories: 1 })).visible).toContain('memory')
  })

  it('reveals Activity on the first event', () => {
    expect(revealedNav(counts({ events: 1 })).visible).toContain('activity')
  })

  it('reveals the Chart on the first subscription', () => {
    expect(revealedNav(counts({ subscriptions: 1 })).visible).toContain('chart')
  })

  it('also reveals the Chart at two workers — or the first wire is unreachable', () => {
    // The canvas is where a wire is DRAWN. Gating it on "a subscription exists"
    // alone would mean the gesture designed to create the first subscription
    // only appears after something else already created one.
    expect(revealedNav(counts({ workers: 2 })).visible).toContain('chart')
  })

  it('does not reveal the Chart for a schedule', () => {
    // A clock is a per-worker fact; the Triggers tab renders it better than a
    // dial on a canvas, and one worker on a clock is still not a shape.
    expect(revealedNav(counts({ workers: 1, subscriptions: 0 })).visible).not.toContain('chart')
  })

  it('a working single-worker project shows six', () => {
    const { visible } = revealedNav(counts({ workers: 1, memories: 4, events: 9 }))
    expect(visible).toEqual(['desk', 'chat', 'workers', 'memory', 'activity', 'settings'])
  })

  it('a wired fleet shows all seven', () => {
    const { visible } = revealedNav(
      counts({ workers: 2, memories: 4, events: 9, subscriptions: 1 }),
    )
    expect(visible).toEqual([...NAV_ENTRIES])
  })
})

describe('stickiness', () => {
  it('keeps an entry once revealed, even when the count returns to zero', () => {
    const first = revealedNav(counts({ memories: 3 }))
    expect(first.sticky).toEqual(['memory'])

    // Every memory superseded, every worker retired — the history still exists,
    // so the surface that reads it stays.
    const later = revealedNav(counts(), first.sticky)
    expect(later.visible).toContain('memory')
    expect(later.sticky).toEqual(['memory'])
  })

  it('deleting the last worker un-reveals nothing', () => {
    const wired = revealedNav(counts({ workers: 1, events: 5, subscriptions: 1 }))
    const emptied = revealedNav(counts(), wired.sticky)
    expect(emptied.visible).toEqual(wired.visible)
  })

  it('accepts an unknown or stale sticky entry without breaking the order', () => {
    const { visible } = revealedNav(counts(), ['chart' as NavEntry])
    expect(visible).toEqual(['desk', 'chat', 'workers', 'chart', 'settings'])
  })
})

describe('appeared — what the confirmation gets to name', () => {
  it('is empty when nothing changed', () => {
    const first = revealedNav(counts({ events: 1 }))
    expect(first.appeared).toEqual(['activity'])
    expect(revealedNav(counts({ events: 2 }), first.sticky).appeared).toEqual([])
  })

  it('names every entry unlocked in the same evaluation, in canonical order', () => {
    const { appeared } = revealedNav(counts({ memories: 1, events: 1, subscriptions: 1 }))
    expect(appeared).toEqual(['memory', 'activity', 'chart'])
  })

  it('has a sentence for each, written for the operator rather than the schema', () => {
    for (const entry of NAV_ENTRIES) {
      const sentence = navRevealSentence(entry)
      expect(sentence).toContain(NAV_LABELS[entry])
      expect(sentence.endsWith('.')).toBe(true)
    }
    expect(navRevealSentence('chart')).toContain('which worker wakes which')
  })

  // The entries sit in the menu at the top of the sidebar, and the notice sits
  // below that menu. "Now in the sidebar" sent people looking for a new row
  // in the session list.
  it('points at the menu, not at "the sidebar"', () => {
    for (const entry of NAV_ENTRIES) {
      expect(navRevealSentence(entry)).toContain('in the menu above')
      expect(navRevealSentence(entry)).not.toMatch(/sidebar/)
    }
  })
})

describe('order is fixed', () => {
  it('never reorders, whatever order the sticky set arrives in', () => {
    const scrambled = revealedNav(counts(), ['chart', 'memory', 'activity'] as NavEntry[])
    expect(scrambled.visible).toEqual([...NAV_ENTRIES])
    expect(scrambled.sticky).toEqual(['memory', 'activity', 'chart'])
  })

  it('every always-on entry is a real entry', () => {
    for (const entry of NAV_ALWAYS) expect(NAV_ENTRIES).toContain(entry)
  })

  it('every entry has a label', () => {
    for (const entry of NAV_ENTRIES) expect(NAV_LABELS[entry]).toBeTruthy()
  })
})
