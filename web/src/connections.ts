// Connections — the browser-side mirror of `GET /agent/connections` and the
// Connect Google flow (design/2026-09-11-project-connections.md, addendum
// 2026-09-16, ticket T25; engine: go/cmd/agentd/googleconnect.go).
//
// Pure: no React, no window, no fetch. The hook is useConnections.ts and the
// panel is components/ConnectionsPanel.tsx.
//
// Two rules live here rather than in the panel:
//
//  * Fail closed. `can_connect` is true only when the server literally said
//    `true`; a missing, stringy or malformed field is "you may not". The same
//    goes for `connected` and `available` — a console that shows a Google
//    account as connected when it is not would send someone off to debug a
//    worker instead of pressing a button.
//  * The callback's reason codes are a closed set and the human reads a
//    sentence, never the code. An unknown code gets a generic sentence and is
//    never echoed: it arrived in a URL, which anyone can type.

/** Default endpoint for the connections read. */
export const CONNECTIONS_ENDPOINT = '/agent/connections'

/** One declared connection (any auth type). `account` is set only on a
 *  `google_account` connection. */
export interface ConnectionRow {
  name: string
  description: string
  account?: string
  available: boolean
  unavailable?: string
}

/** One Google account the project declares, connected or not. */
export interface AccountRow {
  account: string
  provider: 'google'
  connected: boolean
  account_email?: string
  connected_by?: string
  /** Unix milliseconds. */
  connected_at?: number
  /** Set only when connected but unusable (key changed, Connect Google off). */
  unavailable?: string
  /** The connection names that use this account. */
  connections: string[]
}

/** The `GET /agent/connections` body. */
export interface ConnectionsState {
  connections: ConnectionRow[]
  accounts: AccountRow[]
  can_connect: boolean
  connect_disabled_reason?: string
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function asRecord(v: unknown): Record<string, unknown> | null {
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

function coerceConnectionRow(raw: unknown): ConnectionRow | null {
  const r = asRecord(raw)
  if (!r || str(r.name) === '') return null
  const row: ConnectionRow = {
    name: str(r.name),
    description: str(r.description),
    available: r.available === true,
  }
  if (str(r.account) !== '') row.account = str(r.account)
  if (str(r.unavailable) !== '') row.unavailable = str(r.unavailable)
  return row
}

function coerceAccountRow(raw: unknown): AccountRow | null {
  const r = asRecord(raw)
  if (!r || str(r.account) === '') return null
  const row: AccountRow = {
    account: str(r.account),
    provider: 'google',
    connected: r.connected === true,
    connections: Array.isArray(r.connections)
      ? r.connections.filter((c): c is string => typeof c === 'string' && c !== '')
      : [],
  }
  if (str(r.account_email) !== '') row.account_email = str(r.account_email)
  if (str(r.connected_by) !== '') row.connected_by = str(r.connected_by)
  if (typeof r.connected_at === 'number' && Number.isFinite(r.connected_at) && r.connected_at > 0) {
    row.connected_at = r.connected_at
  }
  if (str(r.unavailable) !== '') row.unavailable = str(r.unavailable)
  return row
}

/** Tolerant coercion of the list body. Fails closed: `can_connect` only on a
 *  literal `true`. Rows without a name (or an account) are dropped. */
export function coerceConnections(raw: unknown): ConnectionsState {
  const r = asRecord(raw) ?? {}
  const state: ConnectionsState = {
    connections: (Array.isArray(r.connections) ? r.connections : [])
      .map(coerceConnectionRow)
      .filter((c): c is ConnectionRow => c !== null),
    accounts: (Array.isArray(r.accounts) ? r.accounts : [])
      .map(coerceAccountRow)
      .filter((a): a is AccountRow => a !== null),
    can_connect: r.can_connect === true,
  }
  if (str(r.connect_disabled_reason) !== '') state.connect_disabled_reason = str(r.connect_disabled_reason)
  return state
}

/** `POST` here to start connecting `account`. */
export function connectEndpoint(account: string, base: string = CONNECTIONS_ENDPOINT): string {
  return `${base}/${encodeURIComponent(account)}/connect`
}

/** `DELETE` here to disconnect `account`. */
export function disconnectEndpoint(account: string, base: string = CONNECTIONS_ENDPOINT): string {
  return `${base}/${encodeURIComponent(account)}`
}

/** What the callback said, read off `?connect=<account>&result=…`. */
export type ConnectResult =
  | { account: string; ok: true }
  | { account: string; ok: false; reason: string; missing?: string[] }

/** The callback's closed set of reason codes (go: googleconnect.go). */
export const CONNECT_ERROR_REASONS = [
  'cancelled',
  'expired',
  'other_browser',
  'not_allowed',
  'no_refresh_token',
  'missing_scopes',
  'exchange_failed',
  'store_failed',
] as const

export type ConnectErrorReason = (typeof CONNECT_ERROR_REASONS)[number]

/**
 * Read the result the Google callback redirects back with:
 * `?connect=<account>&result=connected`, or `&result=error&reason=<code>`
 * (plus `&missing=a,b` for `missing_scopes`). Accepts the search string with
 * or without its leading `?`. Anything that is not one of those two shapes is
 * `null` — a settings URL without a result is the normal case.
 */
export function parseConnectResult(search: string): ConnectResult | null {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const account = (params.get('connect') ?? '').trim()
  if (account === '') return null
  const result = params.get('result')
  if (result === 'connected') return { account, ok: true }
  if (result !== 'error') return null
  const out: ConnectResult = { account, ok: false, reason: (params.get('reason') ?? '').trim() }
  const missing = (params.get('missing') ?? '')
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '')
  if (missing.length > 0) out.missing = missing
  return out
}

const CONNECT_ERROR_TEXT: Record<ConnectErrorReason, string> = {
  cancelled: 'Google sign-in was cancelled, so nothing was connected. Press Connect Google to try again.',
  expired:
    'Connecting Google took too long, or Agent Bob restarted part way through. Press Connect Google to start again.',
  other_browser:
    'Google sent you back to a different browser from the one that started. Press Connect Google again and finish in the same browser.',
  not_allowed:
    'Your login is not allowed to connect Google for this project. Ask whoever runs Agent Bob to make you an operator of this project.',
  no_refresh_token: 'Google did not give Agent Bob lasting access, so nothing was connected. Press Connect Google again.',
  missing_scopes:
    'Some permissions were unticked on the Google screen, so nothing was connected. Press Connect Google again and leave every box ticked.',
  exchange_failed:
    'Google did not accept the sign-in, so nothing was connected. Press Connect Google again; if it keeps happening, ask whoever runs Agent Bob.',
  store_failed:
    'Google approved, but Agent Bob could not save the connection. Press Connect Google again; if it keeps happening, ask whoever runs Agent Bob.',
}

const CONNECT_ERROR_FALLBACK = 'Connecting Google did not work. Press Connect Google to try again.'

function isConnectErrorReason(reason: string): reason is ConnectErrorReason {
  return (CONNECT_ERROR_REASONS as readonly string[]).includes(reason)
}

/** Plain English for one callback reason code. `missing` (the short
 *  permission names from `&missing=`) is appended for `missing_scopes`. An
 *  unknown code gets a generic sentence and is never echoed. */
export function describeConnectError(reason: string, missing?: string[]): string {
  if (!isConnectErrorReason(reason)) return CONNECT_ERROR_FALLBACK
  const text = CONNECT_ERROR_TEXT[reason]
  if (reason === 'missing_scopes' && missing && missing.length > 0) {
    return `${text} Missing: ${missing.join(', ')}.`
  }
  return text
}

/** "Google" for the default account, "Google (name)" for any other. */
export function googleAccountLabel(account: string): string {
  return account === 'google' ? 'Google' : `Google (${account})`
}

/** The one-line banner text for a callback result. */
export function describeConnectResult(result: ConnectResult): string {
  if (result.ok) {
    return `${googleAccountLabel(result.account)} is connected. Workers can use it from their next run.`
  }
  return describeConnectError(result.reason, result.missing)
}

/** The human part of an error body: agentd answers these routes with
 *  `{"error": "..."}`, and `useConfigApi` hands the raw body over as the
 *  message. Anything that is not that shape passes through unchanged. */
export function readApiErrorMessage(message: string): string {
  const trimmed = message.trim()
  if (!trimmed.startsWith('{')) return message
  try {
    const parsed = JSON.parse(trimmed) as unknown
    const r = asRecord(parsed)
    if (r && typeof r.error === 'string' && r.error !== '') return r.error
  } catch {
    /* not JSON after all */
  }
  return message
}

/** Only an http(s) URL is ever followed: a `javascript:` authorize_url from a
 *  confused or hostile server must not become a navigation. */
export function isSafeAuthorizeUrl(url: unknown): url is string {
  return typeof url === 'string' && /^https?:\/\//i.test(url)
}
