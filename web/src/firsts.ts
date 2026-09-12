// firsts.ts — the Desk narrating "the first of a kind" (design §3 G4,
// `design/2026-09-11-onboarding-and-the-guide.md`).
//
// Pure: given `desk.ts`'s folded `DeskFirstRecord[]` and the set of kinds a
// project has ALREADY narrated, decide which kind(s) should narrate on this
// render, and which record earned it. Nothing here touches `localStorage` or
// React — that split is `useFirsts.ts`, the same way `desk.ts`/`useDesk.ts`
// and `navReveal.ts`/`useNavReveal.ts` split it.
//
// The seven sentences (one per kind, one link each) are written ONCE, here —
// never in JSX — per the ticket's own instruction, sourced from design §3 G4
// and its table §4.4:
//   - `first-worker` is G4's own worked example, verbatim.
//   - `first-schedule` / `first-subscription` reuse `WorkerTriggers.tsx`'s own
//     sentences, quoted in the design's §1 ("Guidance today").
//   - `first-rewrite`, `first-ask` and `first-revert` reuse the guide table's
//     own trap/summary lines for `the-architect`, `when-a-worker-asks-you`
//     and `the-rail-and-the-changelog` respectively.
//   - `first-memory` reuses `the memory` page's own line ("Memories are
//     references, not rules").
// The design names a full sentence for exactly one kind (`first-worker`); the
// other six are not spelled out verbatim anywhere in the design doc, so these
// are built from the closest existing verbatim phrase for that kind rather
// than invented prose — see the C5 report for the line-by-line sourcing.
//
// A kind never narrates twice per project (the seen-set only grows), and
// deleting the record that earned it does not un-narrate it — the seen-set
// remembers the KIND, never the id, precisely so a deleted worker cannot
// bring "first worker" back.
//
// Not a checklist: some projects will never see all seven, and there is no
// "3 of 7" anywhere. `FIRST_KINDS`'s order is display order only.

import type { DeskFirstKind, DeskFirstRecord } from './desk.js'

export const FIRST_KINDS: readonly DeskFirstKind[] = [
  'first-worker',
  'first-memory',
  'first-schedule',
  'first-subscription',
  'first-rewrite',
  'first-ask',
  'first-revert',
]

/** One kind's sentence and the guide page it points at. */
export interface FirstNarration {
  sentence: string
  /** Guide slug — render `#/guide/<slug>` (`guideRoute.ts`'s `buildGuideHash`)
   *  when a guide is mounted, and just the sentence when it is not. */
  slug: string
}

/** The seven sentences, written once. See the file banner for sourcing. */
export const FIRST_NARRATIONS: Record<DeskFirstKind, FirstNarration> = {
  'first-worker': {
    sentence:
      "This is the project's first worker. A worker is a set of instructions and a trigger — read how to change one.",
    slug: 'a-workers-instructions',
  },
  'first-memory': {
    sentence:
      "This is the project's first memory. Memories are references, not rules — read what the project remembers.",
    slug: 'memory',
  },
  'first-schedule': {
    sentence:
      "This is the project's first schedule. A schedule says: at these times, tell this worker to do this.",
    slug: 'clocks-and-wake-ups',
  },
  'first-subscription': {
    sentence:
      "This is the project's first subscription. A subscription says: when an event of this type arrives, start a job for this worker.",
    slug: 'clocks-and-wake-ups',
  },
  'first-rewrite': {
    sentence:
      "This is the project's first rewrite. A quiet architect is the alarm — read about its loop.",
    slug: 'the-architect',
  },
  'first-ask': {
    sentence:
      "This is the project's first ask. The worker has stopped and is waiting — reply in its thread and it continues.",
    slug: 'when-a-worker-asks-you',
  },
  'first-revert': {
    sentence:
      "This is the project's first revert. Revert is a forward write: nothing is erased, both changes stay.",
    slug: 'the-rail-and-the-changelog',
  },
}

/** One kind due to narrate, and which record's row should carry it. */
export interface FirstToNarrate {
  kind: DeskFirstKind
  /** The chronologically earliest record of this kind — its row gets the
   *  sentence. Matches a `DeskChange.id`, a `DeskAsk.id`, or a synthetic
   *  `memory:<created_at>` id for `first-memory`, which has no row of its
   *  own on the Desk today. */
  recordId: string
  narration: FirstNarration
}

/**
 * Which kinds should narrate on this render, and on which record.
 *
 * A kind narrates iff the project's folded records actually contain one of
 * that kind AND the kind is not already in `seen`. Ties for "earliest" break
 * on id so the result is deterministic under a same-millisecond clock (the
 * same tie-break `desk.ts` uses everywhere else).
 */
export function firstsToNarrate(
  records: readonly DeskFirstRecord[],
  seen: ReadonlySet<DeskFirstKind>,
): FirstToNarrate[] {
  const earliest = new Map<DeskFirstKind, DeskFirstRecord>()
  for (const record of records) {
    const held = earliest.get(record.kind)
    if (
      !held ||
      record.createdAtMs < held.createdAtMs ||
      (record.createdAtMs === held.createdAtMs && record.id < held.id)
    ) {
      earliest.set(record.kind, record)
    }
  }

  const out: FirstToNarrate[] = []
  for (const kind of FIRST_KINDS) {
    if (seen.has(kind)) continue
    const record = earliest.get(kind)
    if (!record) continue
    out.push({ kind, recordId: record.id, narration: FIRST_NARRATIONS[kind] })
  }
  return out
}
