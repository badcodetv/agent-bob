// whoami — the browser's mirror of `GET /agent/whoami`
// (design/2026-09-11-onboarding-work-plan.md §1.1).
//
// The whole point of this route is that "can this human change the budget?"
// is answered by the SERVER, from the JWT claim it minted, never guessed in
// the browser from who is logged in or what project they are looking at. A5's
// BudgetPanel renders its limits-editing form only when `operator` here is
// true — nothing else may gate it, or a non-operator could edit their own
// budget by editing local state.
//
// Pure fetch, no formatting: `usage.ts` is where the numbers get shaped for
// display, this file only answers who is asking.

import { useCallback, useRef, useState } from 'react'
import { useConfigApi, type ConfigApiOptions } from './configApi.js'

/** Default endpoint for the whoami route. */
export const WHOAMI_ENDPOINT = '/agent/whoami'

/** `GET /agent/whoami`'s whole response (go/httpapi/whoami.go). */
export interface Whoami {
  email: string
  project: string
  operator: boolean
}

/** A safe default for "we don't know yet" — never operator, so a caller that
 *  forgets to check `loading` still fails closed on the budget-edit gate. */
export function defaultWhoami(): Whoami {
  return { email: '', project: '', operator: false }
}

/** Tolerant coercion, the same posture every other reader in this package
 *  takes with a server response: an unexpected shape becomes the safe
 *  default rather than a thrown error, and `operator` in particular MUST NOT
 *  become `true` by accident — only a literal boolean `true` counts. */
export function coerceWhoami(raw: unknown): Whoami {
  const r = (raw ?? {}) as Record<string, unknown>
  return {
    email: typeof r.email === 'string' ? r.email : '',
    project: typeof r.project === 'string' ? r.project : '',
    operator: r.operator === true,
  }
}

export interface UseWhoamiOptions extends ConfigApiOptions {
  /** Override the endpoint (default `/agent/whoami`). */
  endpoint?: string
}

export interface WhoamiApi {
  whoami: Whoami
  loading: boolean
  /** Load failure, as the server phrased it. `whoami` stays the safe default
   *  (non-operator) while this is set — a failed read must never be read as
   *  permission. */
  error: string | null
  reload: () => Promise<void>
}

export default function useWhoami(options: UseWhoamiOptions = {}): WhoamiApi {
  const { endpoint = WHOAMI_ENDPOINT } = options
  const { request } = useConfigApi(options)

  const [whoami, setWhoami] = useState<Whoami>(() => defaultWhoami())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      setWhoami(coerceWhoami(await request<unknown>(endpoint)))
      setError(null)
    } catch (err) {
      setWhoami(defaultWhoami())
      setError(err instanceof Error ? err.message : 'failed to load who you are')
    } finally {
      setLoading(false)
    }
  }, [endpoint, request])

  // Ref-guard rather than useEffect — see the note in useProjectSettings.ts:
  // a host that inlines `getAuthToken` changes `request`'s identity every
  // render, which a `[request]` effect would turn into an unbounded GET loop.
  const didLoad = useRef(false)
  if (!didLoad.current) {
    didLoad.current = true
    void reload()
  }

  return { whoami, loading, error, reload }
}
