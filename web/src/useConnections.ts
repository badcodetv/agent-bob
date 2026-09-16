// useConnections — the data hook behind the Connections panel (T25).
//
// One read (`GET /agent/connections`) and two writes: `connect(account)` POSTs
// to start a Google sign-in and then sends the whole browser to Google, and
// `disconnect(account)` DELETEs and reads the list again.
//
// connect() goes through `useConfigApi` on purpose. The POST's response sets
// the `bob_connect` cookie that the callback checks against the OAuth state;
// the request must be the browser's own same-origin fetch for that cookie to
// be stored, and the navigation must follow in the SAME browser, or the
// callback answers `other_browser`.
//
// A 404/501 on the read is "this deployment has no connections routes" (they
// are mounted only on Postgres), reported as `unwired`, not as an error.

import { useCallback, useEffect, useRef, useState } from 'react'
import { looksUnwired, useConfigApi, type ConfigApiOptions } from './configApi.js'
import {
  coerceConnections,
  connectEndpoint,
  CONNECTIONS_ENDPOINT,
  disconnectEndpoint,
  isSafeAuthorizeUrl,
  readApiErrorMessage,
  type ConnectionsState,
} from './connections.js'

export interface UseConnectionsOptions extends ConfigApiOptions {
  /** Override the base endpoint (default `/agent/connections`). */
  endpoint?: string
  /** The project the list belongs to. Changing it reads the list again; the
   *  project itself travels in the bearer token, not in the URL. */
  projectId?: string
}

export interface DisconnectOutcome {
  account: string
  /** Whether Google confirmed the revoke. The stored connection is removed
   *  either way (A12). */
  revoked: boolean
}

export interface ConnectionsApi {
  state: ConnectionsState
  /** True until the first read settles. */
  loading: boolean
  /** A read failure in the server's own words. */
  error: string | null
  /** The routes are not mounted on this deployment (404/501). */
  unwired: boolean
  reload: () => Promise<void>
  /** Start connecting `account`: POST, then `window.location.assign` the
   *  returned Google URL. Resolves false (and sets `actionError`) when the
   *  POST fails; on success the page is navigating away. */
  connect: (account: string) => Promise<boolean>
  /** Disconnect `account`, then read the list again. Null on failure. */
  disconnect: (account: string) => Promise<DisconnectOutcome | null>
  /** The account a connect/disconnect is in flight for, else null. */
  busy: string | null
  /** The last connect/disconnect failure, in the server's own words. */
  actionError: string | null
  /** The last successful disconnect, for a one-line notice. */
  lastDisconnect: DisconnectOutcome | null
}

function emptyState(): ConnectionsState {
  return { connections: [], accounts: [], can_connect: false }
}

function messageOf(err: unknown, fallback: string): string {
  return err instanceof Error && err.message !== '' ? readApiErrorMessage(err.message) : fallback
}

export default function useConnections(options: UseConnectionsOptions = {}): ConnectionsApi {
  const { endpoint = CONNECTIONS_ENDPOINT, projectId = '' } = options
  const { request } = useConfigApi(options)

  const [state, setState] = useState<ConnectionsState>(emptyState)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [unwired, setUnwired] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [lastDisconnect, setLastDisconnect] = useState<DisconnectOutcome | null>(null)

  // A read for a previous project must not land on top of this one's.
  const seq = useRef(0)

  // connect() leaves `busy` set as the page navigates to Google. If the user
  // presses Back and the browser restores this page from its back/forward
  // cache, nothing re-runs, so clear it or every button stays disabled.
  useEffect(() => {
    const onPageShow = (e: PageTransitionEvent) => {
      if (e.persisted) setBusy(null)
    }
    window.addEventListener('pageshow', onPageShow)
    return () => window.removeEventListener('pageshow', onPageShow)
  }, [])

  const reload = useCallback(async () => {
    const mine = ++seq.current
    setLoading(true)
    try {
      const next = coerceConnections(await request<unknown>(endpoint))
      if (mine !== seq.current) return
      setState(next)
      setError(null)
      setUnwired(false)
    } catch (err) {
      if (mine !== seq.current) return
      if (looksUnwired(err)) {
        setUnwired(true)
        setError(null)
      } else {
        setError(messageOf(err, 'failed to load connections'))
      }
    } finally {
      if (mine === seq.current) setLoading(false)
    }
  }, [endpoint, request])

  const connect = useCallback(
    async (account: string) => {
      setBusy(account)
      setActionError(null)
      setLastDisconnect(null)
      try {
        const resp = await request<{ authorize_url?: unknown } | undefined>(connectEndpoint(account, endpoint), {
          method: 'POST',
        })
        const url = resp?.authorize_url
        if (!isSafeAuthorizeUrl(url)) {
          setActionError('Agent Bob did not return a Google sign-in link. Try again.')
          setBusy(null)
          return false
        }
        // Read at call time, not captured: the page is leaving.
        window.location.assign(url)
        return true
      } catch (err) {
        setActionError(messageOf(err, 'could not start connecting Google'))
        setBusy(null)
        return false
      }
    },
    [endpoint, request],
  )

  const disconnect = useCallback(
    async (account: string) => {
      setBusy(account)
      setActionError(null)
      setLastDisconnect(null)
      try {
        const resp = await request<{ revoked?: unknown } | undefined>(disconnectEndpoint(account, endpoint), {
          method: 'DELETE',
        })
        const outcome = { account, revoked: resp?.revoked === true }
        setLastDisconnect(outcome)
        await reload()
        return outcome
      } catch (err) {
        setActionError(messageOf(err, 'could not disconnect Google'))
        return null
      } finally {
        setBusy(null)
      }
    },
    [endpoint, request, reload],
  )

  // Render-phase guard, keyed on the project — this package's convention (see
  // useUsage): a host that inlines getAuthToken changes `request` every render,
  // which a `[request]` effect would turn into a GET loop.
  const loadedFor = useRef<string | null>(null)
  if (loadedFor.current !== projectId) {
    loadedFor.current = projectId
    void reload()
  }

  return { state, loading, error, unwired, reload, connect, disconnect, busy, actionError, lastDisconnect }
}
