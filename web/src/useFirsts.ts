// useFirsts — the storage half of `firsts.ts` (design §3 G4).
//
// `firsts.ts` decides which kind(s) should narrate, given the folded records
// and a seen-set; this hook is only the plumbing — reading and writing that
// seen-set in `localStorage`, per project, the same split `navReveal.ts` /
// `useNavReveal.ts` and `desk.ts` / `useDesk.ts` already use.
//
// The seen-set is sticky and ONE-WAY: once a kind is in it, nothing removes
// it. In particular, deleting the worker that earned `first-worker` does not
// reset the set — the narration is about the PROJECT having reached a
// milestone once, not about a worker existing right now, and a console that
// re-showed "this is the project's first worker" every time someone rehired
// after a delete would be lying about what "first" means.

import { useEffect, useState } from 'react'
import { DESK_FIRST_KINDS, type DeskFirstKind, type DeskFirstRecord } from './desk.js'
import { firstsToNarrate, type FirstToNarrate } from './firsts.js'

/** `agentkit.firsts.<project>` — beside the nav's `agentkit.nav.revealed.<project>`. */
export function firstsSeenKey(projectId: string): string {
  return `agentkit.firsts.${projectId}`
}

const KNOWN_KINDS = new Set<string>(DESK_FIRST_KINDS)

/** Read the seen-set. Unreadable storage, rubbish JSON, and an unknown kind
 *  (a future version's) all just drop out rather than throwing. */
export function readFirstsSeen(projectId: string): DeskFirstKind[] {
  try {
    const raw = globalThis.localStorage?.getItem(firstsSeenKey(projectId))
    if (raw === null || raw === undefined) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((k): k is DeskFirstKind => typeof k === 'string' && KNOWN_KINDS.has(k))
  } catch {
    return []
  }
}

/** Write it. A storage that refuses is not an error the operator needs — the
 *  narration just repeats next visit, same degradation as every other mark
 *  in this package. */
export function writeFirstsSeen(projectId: string, kinds: DeskFirstKind[]): void {
  try {
    globalThis.localStorage?.setItem(firstsSeenKey(projectId), JSON.stringify(kinds))
  } catch {
    /* private mode, quota, SSR */
  }
}

export interface UseFirstsOptions {
  /** Project id — the seen-set's key. */
  projectId: string
  /** `desk.ts`'s folded candidates (`Desk.firsts`). */
  records: readonly DeskFirstRecord[]
}

export interface UseFirstsApi {
  /** Which kind(s) should narrate right now, and on which record. Empty most
   *  renders — most projects have already seen most of their sevens. */
  toNarrate: FirstToNarrate[]
}

export default function useFirsts({ projectId, records }: UseFirstsOptions): UseFirstsApi {
  // Render-phase re-read on a project change, the same pattern
  // `useFeedWatermark`/`useNavReveal` use, so switching project cannot render
  // one frame of another project's seen-set.
  //
  // `shown` is what THIS MOUNT has already decided to narrate, held apart
  // from `seenAtMount` (what storage said when this component first ran).
  // The two cannot be collapsed into one set: as soon as a kind is marked
  // seen in storage, `firstsToNarrate(records, seen)` would stop returning
  // it — and the data this hook is fed keeps arriving asynchronously (a
  // fetch resolving, a poll tick), so a NEW render happens moments after the
  // one that first narrated it. Without `shown` holding it, the sentence
  // would be marked seen and un-render itself before a person had a real
  // chance to read it. Once a kind lands in `shown` it stays there for the
  // rest of this mount, regardless of what `seenAtMount` grows to contain.
  const initial = () => ({
    key: projectId,
    seenAtMount: typeof window === 'undefined' ? [] : readFirstsSeen(projectId),
    shown: [] as FirstToNarrate[],
  })
  const [held, setHeld] = useState(initial)
  if (held.key !== projectId) {
    setHeld(initial())
  }

  const shownKinds = held.shown.map((f) => f.kind)
  const fresh = firstsToNarrate(records, new Set([...held.seenAtMount, ...shownKinds]))
  const freshKey = fresh.map((f) => f.kind).join(',')

  // Adds this render's newly-eligible kind(s) to `shown` (so they keep
  // narrating for the rest of this mount) and persists them to storage (so a
  // FUTURE mount does not narrate them again). Runs after the render that
  // first surfaced them has committed — the "rendered" `firsts.ts`/G4 talks
  // about is this commit, not the write.
  useEffect(() => {
    if (fresh.length === 0) return
    setHeld((prev) => {
      if (prev.key !== projectId) return prev
      const seenAtMount = [...new Set([...prev.seenAtMount, ...fresh.map((f) => f.kind)])]
      writeFirstsSeen(projectId, seenAtMount)
      return { key: projectId, seenAtMount, shown: [...prev.shown, ...fresh] }
    })
    // `freshKey` rather than `fresh`: a fresh array every render would re-run
    // this effect forever.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, freshKey])

  return { toNarrate: held.shown }
}

// Re-exported so a test that only imports this module can still drive the
// pure reducer directly, without a localStorage round-trip.
export { firstsToNarrate }
