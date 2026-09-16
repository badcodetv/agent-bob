// firsts.test.ts — the pure "first of a kind" reducer (design §3 G4).

import { describe, it, expect } from 'vitest'
import { FIRST_KINDS, FIRST_NARRATIONS, firstsToNarrate } from './firsts.js'
import { DESK_FIRST_KINDS, type DeskFirstKind, type DeskFirstRecord } from './desk.js'

const record = (over: Partial<DeskFirstRecord> & Pick<DeskFirstRecord, 'kind'>): DeskFirstRecord => ({
  createdAtMs: 1_000,
  id: 'r1',
  ...over,
})

describe('the closed kind list', () => {
  it('has exactly the seven kinds design §3 G4 names, and every one has a sentence + slug', () => {
    expect(FIRST_KINDS).toEqual(DESK_FIRST_KINDS)
    expect(FIRST_KINDS).toHaveLength(7)
    for (const kind of FIRST_KINDS) {
      const n = FIRST_NARRATIONS[kind]
      expect(n.sentence.length).toBeGreaterThan(0)
      expect(n.slug.length).toBeGreaterThan(0)
      // The sentence names what it is, not a bare "first" — every one starts
      // "This is the project's first …", per the worked example in the design.
      expect(n.sentence.startsWith("This is the project's first")).toBe(true)
    }
  })

  it('never shows a checklist count or "N of 7" anywhere in the copy', () => {
    for (const kind of FIRST_KINDS) {
      expect(FIRST_NARRATIONS[kind].sentence).not.toMatch(/\d\s*(of|\/)\s*7/i)
    }
  })
})

describe('firstsToNarrate', () => {
  it('narrates one record of each of the seven kinds when none has been seen', () => {
    const records: DeskFirstRecord[] = DESK_FIRST_KINDS.map((kind, i) =>
      record({ kind, id: `${kind}-1`, createdAtMs: 1000 + i }),
    )
    const out = firstsToNarrate(records, new Set())
    expect(out).toHaveLength(7)
    expect(new Set(out.map((f) => f.kind))).toEqual(new Set(DESK_FIRST_KINDS))
    for (const f of out) {
      expect(f.narration).toBe(FIRST_NARRATIONS[f.kind])
    }
  })

  it('narrates nothing when the project has no records at all', () => {
    expect(firstsToNarrate([], new Set())).toEqual([])
  })

  it('a kind never narrates twice: once seen, it drops out even with fresh records', () => {
    const records = [record({ kind: 'first-worker', id: 'w1' })]
    const first = firstsToNarrate(records, new Set())
    expect(first.map((f) => f.kind)).toEqual(['first-worker'])

    const seen = new Set<DeskFirstKind>(['first-worker'])
    const second = firstsToNarrate(records, seen)
    expect(second).toEqual([])
  })

  it('picks the chronologically earliest record of a kind, not merely one of them', () => {
    const records: DeskFirstRecord[] = [
      record({ kind: 'first-worker', id: 'later', createdAtMs: 5000 }),
      record({ kind: 'first-worker', id: 'earlier', createdAtMs: 1000 }),
      record({ kind: 'first-worker', id: 'middle', createdAtMs: 3000 }),
    ]
    const out = firstsToNarrate(records, new Set())
    expect(out).toEqual([
      { kind: 'first-worker', recordId: 'earlier', narration: FIRST_NARRATIONS['first-worker'] },
    ])
  })

  it('breaks a same-millisecond tie by id, deterministically', () => {
    const records: DeskFirstRecord[] = [
      record({ kind: 'first-ask', id: 'b', createdAtMs: 1000 }),
      record({ kind: 'first-ask', id: 'a', createdAtMs: 1000 }),
    ]
    expect(firstsToNarrate(records, new Set())[0]?.recordId).toBe('a')
  })

  it('deleting the worker does not reset the seen-set: an empty record list plus a seen kind still narrates nothing', () => {
    // The record that originally earned "first-worker" is gone (the worker
    // was deleted, so no worker_create survives in the changelog fold this
    // render), but the kind is already in `seen` from a previous visit.
    const seen = new Set<DeskFirstKind>(['first-worker'])
    expect(firstsToNarrate([], seen)).toEqual([])
    // Even if a SECOND worker is hired later, producing a fresh
    // `first-worker`-tagged record, the kind still does not re-narrate —
    // "first" was already spent.
    const recordsAfterRehire = [record({ kind: 'first-worker', id: 'new-worker', createdAtMs: 9000 })]
    expect(firstsToNarrate(recordsAfterRehire, seen)).toEqual([])
  })

  it('narrates independently per kind — one seen kind does not block the others', () => {
    const records: DeskFirstRecord[] = [
      record({ kind: 'first-worker', id: 'w1' }),
      record({ kind: 'first-memory', id: 'm1' }),
    ]
    const out = firstsToNarrate(records, new Set(['first-worker']))
    expect(out.map((f) => f.kind)).toEqual(['first-memory'])
  })
})
