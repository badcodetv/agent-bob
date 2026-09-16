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
import {
  recentActivity,
  summariseFormingSteps,
  summariseTeamForming,
  type FormingStep,
  type TeamFormingState,
} from './teamForming.js'

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
  /** The architect's run in steps, newest last (summariseFormingSteps). */
  steps: FormingStep[]
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
  // The architect run's own query-events, re-read while it runs so the panel
  // can say what it is doing rather than only that it is busy.
  const [runEvents, setRunEvents] = useState<unknown>(null)
  const runEventsFor = useRef<{ sessionId: string; settled: boolean }>({ sessionId: '', settled: false })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const [w, d, e] = await Promise.all([
        request<{ workers?: unknown[] } | null>(WORKER_ENDPOINTS.list),
        request<{ deliveries?: unknown[] } | null>(`${EVENT_ENDPOINTS.deliveries}?limit=${deliveryLimit}`),
        request<{ events?: unknown[] } | null>(`${EVENT_ENDPOINTS.events}?limit=${eventLimit}`),
      ])
      const nextWorkers = (Array.isArray(w?.workers) ? w!.workers! : []).map((x) => coerceWorker(x))
      const nextDeliveries = (Array.isArray(d?.deliveries) ? d!.deliveries! : []).map(coerceDelivery)
      setWorkers(nextWorkers)
      setDeliveries(nextDeliveries)
      setEvents((Array.isArray(e?.events) ? e!.events! : []).map(coerceProjectEvent))
      setError(null)

      // Read the run's steps while it is going, and once more when it settles
      // so the last step lands; a settled run is never read again.
      const run = summariseTeamForming({
        workers: nextWorkers,
        deliveries: nextDeliveries,
        architectName,
        sinceSeconds,
      }).architectDelivery
      const sessionId = run?.session_id ?? ''
      const settled = run !== null && run.status !== 'running' && run.status !== 'pending'
      const held = runEventsFor.current
      if (sessionId !== '' && !(held.sessionId === sessionId && held.settled)) {
        try {
          const raw = await request<unknown>(EVENT_ENDPOINTS.queryEvents(sessionId))
          runEventsFor.current = { sessionId, settled }
          setRunEvents(raw ?? null)
        } catch {
          // Steps are a nicety beside the team list; a failed read keeps the
          // last steps rather than blanking them or failing the panel.
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to read the team')
    } finally {
      setLoading(false)
    }
  }, [architectName, deliveryLimit, eventLimit, request, sinceSeconds])

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
  const steps = useMemo(
    () => summariseFormingSteps({ phase: state.phase, events: runEvents }),
    [runEvents, state.phase],
  )

  return { ...state, activity, steps, loading, error, reload }
}
