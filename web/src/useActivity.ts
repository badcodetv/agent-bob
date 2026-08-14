// useActivity — everything the Activity rail reads, in one hook
// (`docs/product/28-console-ia-design.md` §1).
//
// Deliberately `useDesk`'s twin: the same five read hooks, the same one clock,
// the same one high-water mark, folded by a different pure function. The Desk
// digests ("what wants me, what changed, what broke, since I last looked") and
// this orders ("everything, oldest to newest"), but they are queries over the
// same five tables and there is no reason for two plumbing layers.
//
// The one real difference is that the LENS is an input to the fold rather than
// a filter applied to its output: gap markers are only correct for the list
// actually on screen, because a four-hour hole between two jobs is not a hole
// at all if a config change sits in the middle of it.
//
// Its watermark is its own. The Desk's answers "what have I not seen?", and
// this one answers the same question about a different surface — sharing a mark
// would make reading one clear the other, which is how the operator loses the
// only record of where they were.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ConfigApiOptions } from './configApi.js'
import { buildActivity, type ActivityLens, type ActivityRecord } from './activity.js'
import useAttentionRequests from './useAttentionRequests.js'
import useConfigLog from './useConfigLog.js'
import useEventsOverview from './useEvents.js'
import useSchedules from './useSchedules.js'
import { deliveriesAsRequests } from './useDesk.js'
import { deliveryDurationSeconds } from './events.js'
import { readWatermark, useFeedWatermark, watermarkKey, writeWatermark } from './watermark.js'
import useElapsedTicker, { tickIntervalForRows } from './useElapsedTicker.js'

/** The surface name the Activity mark is stored under — not the Desk's. */
export const ACTIVITY_SURFACE = 'activity'

/** `localStorage` key for the "since you last looked" mark, per project. */
export function activityLastSeenKey(projectId: string): string {
  return watermarkKey(ACTIVITY_SURFACE, projectId)
}

/** Read the mark. Unreadable storage and rubbish values both mean 0. */
export function readActivityLastSeen(projectId: string): number {
  return readWatermark(ACTIVITY_SURFACE, projectId)
}

/** Write the mark. A storage that refuses is not an error the operator needs. */
export function writeActivityLastSeen(projectId: string, ms: number): void {
  writeWatermark(ACTIVITY_SURFACE, projectId, ms)
}

export interface UseActivityOptions extends ConfigApiOptions {
  projectId?: string
  /** Rows per underlying list. Passed straight to useEventsOverview. */
  limit?: number
  /** Clock in unix seconds. Injectable so ages are testable. */
  nowSeconds?: number
  /** Override the high-water mark instead of reading `localStorage`. */
  lastSeenMs?: number
  /**
   * Poll every N ms. 0 (the default) never polls: this is a library, and a
   * component that starts fetching because it was mounted is a surprise.
   */
  refreshMs?: number
  /** WCAG 2.2.2: paused stops the poll AND the elapsed ticker. */
  paused?: boolean
  /** Which chip is active. Default `all`. */
  lens?: ActivityLens
  /**
   * Floor of the visible time window, unix MILLISECONDS. 0 shows everything
   * fetched. "Show earlier" lowers this rather than raising a row count, because
   * merging four time-ordered sources by row count has no defined oldest row.
   */
  windowStartMs?: number
}

export interface ActivityApi {
  records: ActivityRecord[]
  loading: boolean
  error: string | null
  /**
   * False when the attention list did not load — the asks are rebuilt from the
   * parked jobs and carry no message, exactly as the Desk degrades.
   */
  asksHaveMessages: boolean
  /** False only when the route is not mounted on this deployment. */
  asksRouteAvailable: boolean
  /** Unix MILLISECONDS; 0 means "never looked". */
  lastSeenMs: number
  markSeen: () => void
  reload: () => Promise<void>
  /** The shared clock, unix MILLISECONDS. */
  nowMs: number
}

export default function useActivity(options: UseActivityOptions = {}): ActivityApi {
  const {
    projectId = '',
    limit,
    nowSeconds,
    lastSeenMs: lastSeenOverride,
    refreshMs = 0,
    paused = false,
    lens = 'all',
    windowStartMs = 0,
  } = options

  const overview = useEventsOverview({ ...options, limit, nowSeconds })
  const attention = useAttentionRequests(options)
  const log = useConfigLog({ ...options, projectId })
  const schedules = useSchedules(options)

  const watermark = useFeedWatermark(ACTIVITY_SURFACE, projectId, lastSeenOverride)

  // One ticker for the surface, at the cadence its own rows ask for. A rail
  // with nothing running holds no timer at all.
  const [tickMs, setTickMs] = useState(0)
  const nowMs = useElapsedTicker({
    intervalMs: tickMs,
    paused,
    nowMs: nowSeconds === undefined ? undefined : nowSeconds * 1000,
  })
  const tickNowSeconds = nowSeconds ?? Math.floor(nowMs / 1000)
  const wantedTick = useMemo(
    () =>
      tickIntervalForRows(
        overview.deliveries.map((d) => ({
          status: d.status,
          elapsedSeconds: deliveryDurationSeconds(d, tickNowSeconds) ?? 0,
        })),
      ),
    [overview.deliveries, tickNowSeconds],
  )
  useEffect(() => setTickMs(wantedTick), [wantedTick])

  // The poll goes through one `reload` so the four lists still agree on one
  // moment — four independently-refreshing lists would render a job whose event
  // came from one fetch and whose subscription came from another.
  const reloadRef = useRef<() => Promise<void>>(async () => {})
  const reload = useCallback(async () => {
    await Promise.all([overview.reload(), attention.reload(), log.reload(), schedules.reload()])
  }, [attention, log, overview, schedules])
  reloadRef.current = reload

  useEffect(() => {
    if (refreshMs <= 0 || paused) return
    const id = setInterval(() => void reloadRef.current(), refreshMs)
    return () => clearInterval(id)
  }, [paused, refreshMs])

  const records = useMemo(
    () =>
      buildActivity({
        events: overview.events,
        deliveries: overview.deliveries,
        subscriptions: overview.subscriptions,
        configEvents: log.events,
        // Off "did this load succeed", not "is the route mounted": a mounted
        // route that failed leaves an empty list, and the fallback exists for
        // exactly the case where we do not have the real one.
        attentionRequests: attention.ok
          ? attention.requests
          : deliveriesAsRequests(overview.deliveries),
        schedules: schedules.schedules,
        nowMs: tickNowSeconds * 1000,
        projectId,
        lens,
        windowStartMs,
      }),
    [
      attention.ok,
      attention.requests,
      lens,
      log.events,
      overview.deliveries,
      overview.events,
      overview.subscriptions,
      projectId,
      schedules.schedules,
      tickNowSeconds,
      windowStartMs,
    ],
  )

  return {
    records,
    loading: overview.loading || attention.loading || log.loading || schedules.loading,
    error:
      overview.error ??
      (attention.available ? attention.error : null) ??
      (log.available ? log.error : null) ??
      schedules.error,
    asksHaveMessages: attention.ok,
    asksRouteAvailable: attention.available,
    lastSeenMs: watermark.markMs,
    markSeen: watermark.mark,
    reload,
    nowMs: tickNowSeconds * 1000,
  }
}
