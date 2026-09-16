// T25: the pure half of the Connections panel — the GET /agent/connections
// body, the ?connect= result the callback redirects with, and the words a
// human reads for each.

import { describe, expect, it } from 'vitest'
import {
  CONNECT_ERROR_REASONS,
  CONNECTIONS_ENDPOINT,
  coerceConnections,
  connectEndpoint,
  describeConnectError,
  describeConnectResult,
  disconnectEndpoint,
  googleAccountLabel,
  isSafeAuthorizeUrl,
  parseConnectResult,
  readApiErrorMessage,
} from './connections.js'

describe('coerceConnections', () => {
  it('reads the full contract', () => {
    const s = coerceConnections({
      connections: [
        { name: 'gmail', description: 'ENC Gmail', account: 'google', available: false, unavailable: 'not connected' },
        { name: 'linear', description: 'Linear', available: true },
      ],
      accounts: [
        {
          account: 'google',
          provider: 'google',
          connected: true,
          account_email: 'x@y.example',
          connected_by: 'r@y.example',
          connected_at: 1700000000000,
          unavailable: 'key changed',
          connections: ['gmail'],
        },
      ],
      can_connect: true,
      connect_disabled_reason: '',
    })
    expect(s.can_connect).toBe(true)
    expect(s.connections).toEqual([
      { name: 'gmail', description: 'ENC Gmail', account: 'google', available: false, unavailable: 'not connected' },
      { name: 'linear', description: 'Linear', available: true },
    ])
    expect(s.accounts).toEqual([
      {
        account: 'google',
        provider: 'google',
        connected: true,
        account_email: 'x@y.example',
        connected_by: 'r@y.example',
        connected_at: 1700000000000,
        unavailable: 'key changed',
        connections: ['gmail'],
      },
    ])
    expect(s.connect_disabled_reason).toBeUndefined()
  })

  it('fails closed: can_connect only on literal true', () => {
    for (const v of ['true', 1, {}, [], null, undefined, 'yes']) {
      expect(coerceConnections({ can_connect: v }).can_connect).toBe(false)
    }
    expect(coerceConnections(null).can_connect).toBe(false)
    expect(coerceConnections('rubbish')).toEqual({ connections: [], accounts: [], can_connect: false })
  })

  it('fails closed on connected and available too', () => {
    const s = coerceConnections({
      connections: [{ name: 'a', available: 'true' }],
      accounts: [{ account: 'google', connected: 'true' }],
    })
    expect(s.connections[0]!.available).toBe(false)
    expect(s.accounts[0]!.connected).toBe(false)
    expect(s.accounts[0]!.connections).toEqual([])
  })

  it('drops rows without a name or account, and non-string connection names', () => {
    const s = coerceConnections({
      connections: [{ description: 'no name' }, 'x', { name: '' }, { name: 'ok' }],
      accounts: [{ provider: 'google' }, { account: 'google', connections: ['gmail', 3, null] }],
    })
    expect(s.connections.map((c) => c.name)).toEqual(['ok'])
    expect(s.accounts.map((a) => a.account)).toEqual(['google'])
    expect(s.accounts[0]!.connections).toEqual(['gmail'])
  })

  it('keeps the disabled reason when set', () => {
    expect(coerceConnections({ can_connect: false, connect_disabled_reason: 'dev-open' }).connect_disabled_reason).toBe(
      'dev-open',
    )
  })
})

describe('endpoints', () => {
  it('builds the connect and disconnect paths with the account escaped', () => {
    expect(CONNECTIONS_ENDPOINT).toBe('/agent/connections')
    expect(connectEndpoint('google')).toBe('/agent/connections/google/connect')
    expect(disconnectEndpoint('google')).toBe('/agent/connections/google')
    expect(connectEndpoint('a/b')).toBe('/agent/connections/a%2Fb/connect')
  })
})

describe('parseConnectResult', () => {
  it('reads a success', () => {
    expect(parseConnectResult('?connect=google&result=connected')).toEqual({ account: 'google', ok: true })
    expect(parseConnectResult('connect=google&result=connected')).toEqual({ account: 'google', ok: true })
  })

  it('reads an error with its reason', () => {
    expect(parseConnectResult('?connect=google&result=error&reason=cancelled')).toEqual({
      account: 'google',
      ok: false,
      reason: 'cancelled',
    })
  })

  it('reads missing scopes as a list', () => {
    expect(
      parseConnectResult('?connect=google&result=error&reason=missing_scopes&missing=gmail.compose,drive'),
    ).toEqual({ account: 'google', ok: false, reason: 'missing_scopes', missing: ['gmail.compose', 'drive'] })
  })

  it('an error with no reason keeps an empty reason', () => {
    expect(parseConnectResult('?connect=google&result=error')).toEqual({ account: 'google', ok: false, reason: '' })
  })

  it('returns null for anything else', () => {
    expect(parseConnectResult('')).toBeNull()
    expect(parseConnectResult('?worker=x')).toBeNull()
    expect(parseConnectResult('?connect=&result=connected')).toBeNull()
    expect(parseConnectResult('?connect=google')).toBeNull()
    expect(parseConnectResult('?connect=google&result=maybe')).toBeNull()
  })
})

describe('describeConnectError', () => {
  it('has plain English for every reason code', () => {
    expect([...CONNECT_ERROR_REASONS].sort()).toEqual(
      [
        'cancelled',
        'expired',
        'other_browser',
        'not_allowed',
        'no_refresh_token',
        'missing_scopes',
        'exchange_failed',
        'store_failed',
      ].sort(),
    )
    const texts = CONNECT_ERROR_REASONS.map((r) => describeConnectError(r))
    for (const t of texts) {
      expect(t.length).toBeGreaterThan(20)
      expect(t).not.toMatch(/oauth|scope|token|_/i)
    }
    expect(new Set(texts).size).toBe(texts.length)
  })

  it('falls back for an unknown reason without echoing it', () => {
    const t = describeConnectError('<script>')
    expect(t).not.toContain('<script>')
    expect(t).toMatch(/Connect Google/)
  })

  it('names the missing permissions when given them', () => {
    expect(describeConnectError('missing_scopes', ['gmail.compose'])).toContain('gmail.compose')
  })
})

describe('describeConnectResult', () => {
  it('describes a success and an error', () => {
    expect(describeConnectResult({ account: 'google', ok: true })).toMatch(/connected/i)
    expect(describeConnectResult({ account: 'google', ok: false, reason: 'expired' })).toBe(
      describeConnectError('expired'),
    )
    expect(
      describeConnectResult({ account: 'google', ok: false, reason: 'missing_scopes', missing: ['drive'] }),
    ).toContain('drive')
  })
})

describe('helpers', () => {
  it('labels the default account as Google and others with their name', () => {
    expect(googleAccountLabel('google')).toBe('Google')
    expect(googleAccountLabel('marketing')).toBe('Google (marketing)')
  })

  it('reads {"error": "..."} bodies and passes other text through', () => {
    expect(readApiErrorMessage('{"error":"not an operator"}')).toBe('not an operator')
    expect(readApiErrorMessage('plain words')).toBe('plain words')
    expect(readApiErrorMessage('{"other":1}')).toBe('{"other":1}')
  })

  it('only follows http(s) authorize URLs', () => {
    expect(isSafeAuthorizeUrl('https://accounts.google.com/o/oauth2/v2/auth?x=1')).toBe(true)
    expect(isSafeAuthorizeUrl('http://localhost:9999/auth')).toBe(true)
    expect(isSafeAuthorizeUrl('javascript:alert(1)')).toBe(false)
    expect(isSafeAuthorizeUrl('')).toBe(false)
    expect(isSafeAuthorizeUrl(42)).toBe(false)
  })
})
