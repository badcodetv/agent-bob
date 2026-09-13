// useCharter — the data hook behind the onboarding screen (T14).
//
// One read that polls, and one write that happens once.
//
// It POLLS because the charter arrives from OUTSIDE the browser: the
// interviewer deposits it as a memory from inside its container, mid-
// conversation, with nothing in the chat stream saying it has done so
// (Decision A6 — there is no end signal, no "finish" tool and no terminal
// state; the agent simply re-deposits the complete charter whenever it
// revises, and the newest one wins). So the screen asks again, periodically,
// and the panel appears when there is something to show.
//
// A 404 is the EMPTY STATE, not an error: "the interview has not deposited a
// charter yet" is the normal condition for most of an interview's life, and
// rendering it as a failure would put a red box on a screen where nothing is
// wrong.

import { useCallback, useEffect, useRef, useState } from 'react'
import { configApiStatus, useConfigApi, type ConfigApiOptions } from './configApi.js'
import {
  CHARTER_ENDPOINTS,
  coerceCharterCurrent,
  coerceCharterIssues,
  type CharterCurrent,
  type CharterIssue,
} from './charter.js'
import { coerceTopologyApplyResult, type TopologyApplyResult } from './topologies.js'

export interface UseCharterOptions extends ConfigApiOptions {
  /** The onboarding session whose charter this is. Without one the hook does
   *  not fetch: there is no "the project's charter", only this interview's. */
  session?: string
  /** Override the read endpoint (default `/agent/charter/current`). */
  currentEndpoint?: string
  /** Override the apply endpoint. */
  applyEndpoint?: string
  /** Poll interval in ms. 0 or less polls once and stops. Default 4000. */
  refreshMs?: number
}

export interface CharterApi {
  /** The deposited charter and its verdict, or null while none exists. */
  charter: CharterCurrent | null
  /** True until the first fetch settles — distinct from "there is none". */
  loading: boolean
  /** A read failure in the server's own words. A 404 is NOT one of these. */
  error: string | null
  reload: () => Promise<void>
  /** POST /agent/charter/apply. Returns the read-back result, or null on
   *  failure: a 422's issues land in `applyIssues`, and anything else — a 409
   *  naming the worker that already exists, above all — lands in `applyError`
   *  verbatim. */
  apply: (rationale?: string) => Promise<TopologyApplyResult | null>
  applying: boolean
  applyError: string | null
  applyIssues: CharterIssue[]
  /** True when the charter has been approved — either because THIS screen saw
   *  its own apply succeed, or because the server says so (`CharterCurrent`'s
   *  `applied`, added for DI10). It used to be the first of those alone,
   *  because the route reported nothing; the consequence was that a reload
   *  mid-onboarding forgot the approval had happened and offered "Approve"
   *  again on an already-approved charter. It is what the "Run the architect
   *  now" control keys off, and what stops the poll. */
  applied: boolean
  /** What THIS screen's apply said about the architect's first run, which the
   *  server now starts on approval: the `architect.run` event id, or the
   *  sentence explaining why it was not started. Null until this screen
   *  applies (a reload knows only `applied`, and the team view reads the jobs
   *  themselves). */
  architectRun: { eventId: string; error: string } | null
}

export default function useCharter(options: UseCharterOptions = {}): CharterApi {
  const {
    session = '',
    currentEndpoint = CHARTER_ENDPOINTS.current,
    applyEndpoint = CHARTER_ENDPOINTS.apply,
    refreshMs = 4000,
  } = options
  const { request } = useConfigApi(options)

  const [charter, setCharter] = useState<CharterCurrent | null>(null)
  const [loading, setLoading] = useState(session !== '')
  const [error, setError] = useState<string | null>(null)
  const [applying, setApplying] = useState(false)
  const [applyError, setApplyError] = useState<string | null>(null)
  const [applyIssues, setApplyIssues] = useState<CharterIssue[]>([])
  // What THIS screen has seen. The exported `applied` below is this OR the
  // server's own answer, so a reload no longer forgets an approval.
  const [sawApply, setSawApply] = useState(false)
  const [architectRun, setArchitectRun] = useState<CharterApi['architectRun']>(null)

  // The exported answer: this screen's own apply, or the server's record of
  // one. `charter.applied` is the field DI10 added to GET /agent/charter/current
  // — before it existed this hook could only ever report the first half.
  const applied = sawApply || charter?.applied === true

  const reload = useCallback(async () => {
    if (session === '') {
      setCharter(null)
      setLoading(false)
      return
    }
    setError(null)
    try {
      const raw = await request<unknown>(`${currentEndpoint}?session=${encodeURIComponent(session)}`)
      setCharter(coerceCharterCurrent(raw))
    } catch (err) {
      if (configApiStatus(err) === 404) {
        // Nothing deposited yet. Not an error — and the previously-seen
        // charter is deliberately left in place rather than cleared: a 404
        // after a charter existed would mean the deposit was retracted, which
        // nothing does, so it is far likelier to be a transient miss.
        return
      }
      setError(err instanceof Error ? err.message : 'failed to read the charter')
    } finally {
      setLoading(false)
    }
  }, [currentEndpoint, request, session])

  // Ref-guard for the first load, then an interval — the same shape
  // useActivity uses, and for the same reason: the fetch must not be tied to
  // the identity of a callback that changes on every render.
  const reloadRef = useRef<() => Promise<void>>(async () => {})
  reloadRef.current = reload

  const didLoad = useRef('')
  if (didLoad.current !== session) {
    didLoad.current = session
    void reload()
  }

  useEffect(() => {
    if (session === '' || refreshMs <= 0 || applied) return
    const id = setInterval(() => void reloadRef.current(), refreshMs)
    return () => clearInterval(id)
    // `applied` here is the combined answer, so the server confirming an
    // approval this screen did not perform also stops the poll — which is what
    // a second tab open on the same project needs.
  }, [applied, refreshMs, session])

  const apply = useCallback(
    async (rationale?: string): Promise<TopologyApplyResult | null> => {
      if (session === '') return null
      setApplying(true)
      setApplyError(null)
      setApplyIssues([])
      try {
        const raw = await request<unknown>(applyEndpoint, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            session,
            // The memory the human is looking at, not "the newest" — the
            // interview may have revised the charter between the render and
            // the click, and approving something nobody read is the one thing
            // this gate exists to prevent.
            memory_id: charter?.memory_id ?? '',
            rationale: rationale ?? '',
          }),
        })
        setSawApply(true)
        const r = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {}
        setArchitectRun({
          eventId: typeof r.architect_run_event_id === 'string' ? r.architect_run_event_id : '',
          error: typeof r.architect_run_error === 'string' ? r.architect_run_error : '',
        })
        return coerceTopologyApplyResult(raw)
      } catch (err) {
        if (configApiStatus(err) === 422 && err instanceof Error) {
          const issues = parseIssues(err.message)
          if (issues.length > 0) {
            setApplyIssues(issues)
            return null
          }
        }
        setApplyError(err instanceof Error ? err.message : 'failed to apply the charter')
        return null
      } finally {
        setApplying(false)
      }
    },
    [applyEndpoint, charter?.memory_id, request, session],
  )

  return {
    charter,
    loading,
    error,
    reload,
    apply,
    applying,
    applyError,
    applyIssues,
    applied,
    architectRun,
  }
}

/**
 * A 422 body is JSON, but it reaches us as the error's message text, because
 * `ConfigApi.request` deliberately makes the server's BODY the message. Parse
 * it back when it is the issue list; fall back to showing the text otherwise,
 * which is what the caller does when this returns nothing.
 */
function parseIssues(body: string): CharterIssue[] {
  try {
    return coerceCharterIssues(JSON.parse(body))
  } catch {
    return []
  }
}
