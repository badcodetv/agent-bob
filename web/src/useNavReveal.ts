// useNavReveal — the plumbing behind progressive navigation
// (`docs/product/28-console-ia-design.md` §3, decision K9).
//
// `navReveal.ts` decides; this fetches the counts it decides from and keeps the
// sticky set. The split is the usual one in this package: the rule is a pure
// function with a table test, and the hook is the part that touches the
// network and `localStorage`.
//
// Stickiness lives here rather than in the rule because it is storage, and the
// rule must stay answerable from its arguments alone.

import { useCallback, useEffect, useRef, useState } from 'react'
import type { ConfigApiOptions } from './configApi.js'
import useWorkers from './useWorkers.js'
import useMemories from './useMemories.js'
import useSubscriptions from './useSubscriptions.js'
import useEventsOverview from './useEvents.js'
import { everythingRevealed, revealedNav, type NavEntry } from './navReveal.js'

/**
 * How often the nav re-counts what the project contains, in ms.
 *
 * DI12: it used to count once, on mount, and never again — every hook feeding
 * it is a one-shot ref-guarded fetch, and they are separate instances from the
 * ones the pages hold, so a worker hired on the Workers page could not reach
 * the nav's copy. The Chart tab therefore did not appear when you gave a
 * project its second worker; it appeared the next time you reloaded the page.
 *
 * That is not a tuning problem, it is the difference between the feature
 * working and not: `appeared` + `navRevealSentence` exist so the shell can name
 * a newly revealed entry IN THE CONFIRMATION OF THE ACTION THAT CAUSED IT
 * (design 28 §3.2). On the next page load there is no such action left, so that
 * sentence could never once have been shown.
 *
 * Ten seconds because a reveal is an "oh, and now you can also…" rather than
 * something being waited on — cheaper than the 4s the charter panel polls at,
 * and slow enough that four list requests a tick is not a cost worth thinking
 * about. It also stops entirely once there is nothing left to reveal.
 */
export const NAV_REVEAL_POLL_MS = 10_000

/** `localStorage` key for a project's revealed set. */
export function navRevealKey(projectId: string): string {
  return `agentkit.nav.revealed.${projectId}`
}

/** Read the sticky set. Unreadable storage and rubbish both mean "none yet". */
export function readRevealed(projectId: string): NavEntry[] {
  try {
    const raw = window.localStorage.getItem(navRevealKey(projectId))
    if (raw === null) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((e): e is NavEntry => typeof e === 'string')
  } catch {
    return []
  }
}

/** Write it. A storage that refuses is not an error the operator needs. */
export function writeRevealed(projectId: string, entries: NavEntry[]): void {
  try {
    window.localStorage.setItem(navRevealKey(projectId), JSON.stringify(entries))
  } catch {
    /* private mode, quota, SSR — the nav simply re-reveals next time */
  }
}

export interface UseNavRevealOptions extends ConfigApiOptions {
  /** Project id — the sticky set's key. */
  projectId: string
  /**
   * Re-count interval in ms. 0 or negative switches polling off, which is what
   * a test wants when it is driving the counts itself.
   */
  pollMs?: number
}

export interface NavRevealApi {
  /** What to draw, in canonical order. */
  visible: NavEntry[]
  /**
   * Entries revealed since this hook mounted, newest last.
   *
   * The shell names them in the confirmation of the action that caused the
   * reveal (design §3.2) — words rather than a pulsing badge, because colour
   * in this console is never spent on chrome.
   */
  appeared: NavEntry[]
  /** Clear the appeared list once the shell has said its piece. */
  acknowledge: () => void
  /** True while any underlying count is still loading. */
  loading: boolean
}

export default function useNavReveal(options: UseNavRevealOptions): NavRevealApi {
  const { projectId, pollMs = NAV_REVEAL_POLL_MS } = options

  const workers = useWorkers(options)
  const memories = useMemories(options)
  const subscriptions = useSubscriptions(options)
  const events = useEventsOverview(options)

  // Render-phase re-read on a project change — the pattern `useFeedWatermark`
  // uses, so switching project cannot render one frame of another project's nav.
  const [held, setHeld] = useState(() => ({
    key: projectId,
    sticky: typeof window === 'undefined' ? [] : readRevealed(projectId),
    appeared: [] as NavEntry[],
  }))
  if (held.key !== projectId) {
    setHeld({
      key: projectId,
      sticky: typeof window === 'undefined' ? [] : readRevealed(projectId),
      appeared: [],
    })
  }

  const loading = workers.loading || memories.loading || subscriptions.loading || events.loading

  const result = revealedNav(
    {
      workers: workers.workers.length,
      memories: memories.memories.length,
      events: events.events.length,
      subscriptions: subscriptions.subscriptions.length,
    },
    held.sticky,
  )

  // Persist whatever the rule just unlocked. Guarded on length so a render that
  // reveals nothing writes nothing — the common case by far, and a storage
  // write on every render would be a real cost for no reason.
  //
  // Deliberately NOT gated on `loading`: a count that has not arrived is zero,
  // and zero reveals nothing, so an in-flight fetch can only ever fail to
  // reveal — never falsely reveal, and never un-reveal.
  const unlocked = result.appeared
  const unlockedKey = unlocked.join(',')
  useEffect(() => {
    if (unlocked.length === 0) return
    setHeld((prev) => {
      if (prev.key !== projectId) return prev
      const sticky = [...new Set([...prev.sticky, ...unlocked])]
      writeRevealed(projectId, sticky)
      return { key: projectId, sticky, appeared: [...prev.appeared, ...unlocked] }
    })
    // `unlockedKey` rather than the array: a fresh array every render would
    // re-run this effect forever.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, unlockedKey])

  // Re-count on a timer until every conditional entry has been revealed, then
  // stop for good (DI12).
  //
  // The four reloads live in a ref rather than in the effect's dependencies.
  // They are `useCallback`s whose own dependencies change as their data does,
  // so depending on them directly would tear down and rebuild the interval on
  // render after render — and an interval that is rebuilt more often than its
  // period never fires at all, which is the same bug wearing a different hat.
  const reloads = useRef({
    workers: workers.reload,
    memories: memories.reload,
    subscriptions: subscriptions.reload,
    events: events.reload,
  })
  reloads.current = {
    workers: workers.reload,
    memories: memories.reload,
    subscriptions: subscriptions.reload,
    events: events.reload,
  }

  const settled = everythingRevealed(result.sticky)
  useEffect(() => {
    if (pollMs <= 0 || settled) return
    const id = setInterval(() => {
      // Failures are the hooks' own business — each keeps its last good list
      // and reports its own error. A rejected reload here must not take the
      // interval down with it.
      void reloads.current.workers().catch(() => {})
      void reloads.current.memories().catch(() => {})
      void reloads.current.subscriptions().catch(() => {})
      void reloads.current.events().catch(() => {})
    }, pollMs)
    return () => clearInterval(id)
  }, [pollMs, settled, projectId])

  const acknowledge = useCallback(() => {
    setHeld((prev) => (prev.appeared.length === 0 ? prev : { ...prev, appeared: [] }))
  }, [])

  return {
    visible: result.visible,
    appeared: held.appeared,
    acknowledge,
    loading,
  }
}
