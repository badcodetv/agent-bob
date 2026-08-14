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

import { useCallback, useEffect, useState } from 'react'
import type { ConfigApiOptions } from './configApi.js'
import useWorkers from './useWorkers.js'
import useMemories from './useMemories.js'
import useSubscriptions from './useSubscriptions.js'
import useEventsOverview from './useEvents.js'
import { revealedNav, type NavEntry } from './navReveal.js'

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
  const { projectId } = options

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
