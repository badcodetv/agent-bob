// useTeamForming — the poll behind "Your team is forming".
//
// Three reads, taken together on one tick so the list and the statuses agree on
// one moment: the workers, the recent jobs, the recent events. It keeps the last
// good answer when a poll fails — a hiccup mid-watch should not blank a list of
// workers that certainly still exist — and reports the failure beside it.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useConfigApi, type ConfigApiOptions } from './configApi.js'
import { coerceDelivery, coerceProjectEvent, EVENT_ENDPOINTS, type EventDelivery, type ProjectEvent } from './events.js'
import { coerceWorker, WORKER_ENDPOINTS, type Worker } from './workers.js'
import { recentActivity, summariseTeamForming, type TeamFormingState } from './teamForming.js'

export interface UseTeamFormingOptions extends ConfigApiOptions {
  /** The architect's name; '' means the default. */
  architectName?: string
  /** Ignore architect jobs created before this (unix seconds). */
  sinceSeconds?: number
  /** Poll interval in ms. 0 or less loads once. Default 4000. */
  refreshMs?: number
  /** How many jobs and events to read per poll. Default 100 and 20. */
  deliveryLimit?: number
  eventLimit?: number
}

export interface TeamFormingApi extends TeamFormingState {
  /** Newest non-config events, for the "what just happened" strip. */
  activity: ProjectEvent[]
  /** True until the first poll settles. */
  loading: boolean
  /** The latest poll's failure, in the server's words; null once one succeeds. */
  error: string | null
  reload: () => Promise<void>
}

export default function useTeamForming(options: UseTeamFormingOptions = {}): TeamFormingApi {
  const {
    architectName = '',
    sinceSeconds = 0,
    refreshMs = 4000,
    deliveryLimit = 100,
    eventLimit = 20,
  } = options
  const { request } = useConfigApi(options)

  const [workers, setWorkers] = useState<Worker[]>([])
  const [deliveries, setDeliveries] = useState<EventDelivery[]>([])
  const [events, setEvents] = useState<ProjectEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const [w, d, e] = await Promise.all([
        request<{ workers?: unknown[] } | null>(WORKER_ENDPOINTS.list),
        request<{ deliveries?: unknown[] } | null>(`${EVENT_ENDPOINTS.deliveries}?limit=${deliveryLimit}`),
        request<{ events?: unknown[] } | null>(`${EVENT_ENDPOINTS.events}?limit=${eventLimit}`),
      ])
      setWorkers((Array.isArray(w?.workers) ? w!.workers! : []).map((x) => coerceWorker(x)))
      setDeliveries((Array.isArray(d?.deliveries) ? d!.deliveries! : []).map(coerceDelivery))
      setEvents((Array.isArray(e?.events) ? e!.events! : []).map(coerceProjectEvent))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to read the team')
    } finally {
      setLoading(false)
    }
  }, [deliveryLimit, eventLimit, request])

  // Ref-guarded first load and an interval over a ref — the useCharter shape,
  // so the timer is not torn down every time `request` changes identity.
  const reloadRef = useRef(reload)
  reloadRef.current = reload
  const didLoad = useRef(false)
  if (!didLoad.current) {
    didLoad.current = true
    void reload()
  }
  useEffect(() => {
    if (refreshMs <= 0) return
    const id = setInterval(() => void reloadRef.current(), refreshMs)
    return () => clearInterval(id)
  }, [refreshMs])

  const state = useMemo(
    () => summariseTeamForming({ workers, deliveries, architectName, sinceSeconds }),
    [architectName, deliveries, sinceSeconds, workers],
  )
  const activity = useMemo(() => recentActivity(events), [events])

  return { ...state, activity, loading, error, reload }
}
