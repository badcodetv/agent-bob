// ConnectionsPanel — the project's connections, and the Connect Google button
// (design/2026-09-11-project-connections.md, addendum 2026-09-16, T25).
//
// Deliberately plain. A heading; per Google account one line ("Google —
// Connected as x@y" with Disconnect, or "Google — Not connected" with Connect
// Google, disabled with the server's reason under it when `can_connect` is
// false); under it the connections that use that account, each with a dot and
// the reason it cannot be used; then any env-var connections as read-only
// lines. A one-line banner when the page was opened by Google's redirect back.
//
// Every authority decision is the server's: `can_connect` gates the button
// (fail closed — see connections.ts), and a refused POST or DELETE shows the
// server's own sentence. Mounted in ProjectSettingsPage's top tier; router-free
// like everything else in this package.

import { useState } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  Stack,
  Typography,
} from '@mui/material'
import useConnections, { type UseConnectionsOptions } from '../useConnections.js'
import {
  describeConnectResult,
  googleAccountLabel,
  type AccountRow,
  type ConnectionRow,
  type ConnectResult,
} from '../connections.js'

export interface ConnectionsPanelProps extends Omit<UseConnectionsOptions, 'projectId'> {
  /** The project shown. Changing it reads the list again. */
  projectId: string
  /** What Google's redirect back said (`parseConnectResult(location.search)`),
   *  shown as a one-line banner. The host strips the query afterwards. */
  connectResult?: ConnectResult | null
  /** Heading text. Pass '' to render no heading. */
  title?: string
}

/** The confirmation a disconnect asks, once. */
export const DISCONNECT_CONFIRM_TEXT =
  'Disconnect Google? Workers lose Gmail, Drive, Docs and Sheets until someone connects again.'

export default function ConnectionsPanel({
  projectId,
  connectResult = null,
  title = 'Connections',
  ...options
}: ConnectionsPanelProps) {
  const c = useConnections({ ...options, projectId })
  const [confirming, setConfirming] = useState<string | null>(null)
  const [dismissed, setDismissed] = useState<ConnectResult | null>(null)

  if (c.unwired) return null

  const { state } = c
  const accountConnections = new Set(state.accounts.flatMap((a) => a.connections))
  const byName = new Map(state.connections.map((row) => [row.name, row]))
  const others = state.connections.filter((row) => row.account === undefined && !accountConnections.has(row.name))
  const showBanner = connectResult !== null && connectResult !== dismissed
  const empty = !c.loading && c.error === null && state.accounts.length === 0 && state.connections.length === 0

  return (
    <Box sx={{ p: 2, border: 1, borderColor: 'divider', borderRadius: 1 }} data-testid="connections-panel">
      {title !== '' && (
        <Typography variant="subtitle2" component="h2" sx={{ mb: 1 }}>
          {title}
        </Typography>
      )}

      <Stack spacing={1.5}>
        {showBanner && (
          <Alert severity={connectResult.ok ? 'success' : 'error'} onClose={() => setDismissed(connectResult)}>
            {describeConnectResult(connectResult)}
          </Alert>
        )}
        {c.error !== null && <Alert severity="error">{c.error}</Alert>}
        {c.actionError !== null && <Alert severity="error">{c.actionError}</Alert>}
        {c.lastDisconnect !== null && (
          <Alert severity={c.lastDisconnect.revoked ? 'info' : 'warning'}>
            {c.lastDisconnect.revoked
              ? `${googleAccountLabel(c.lastDisconnect.account)} disconnected.`
              : `${googleAccountLabel(c.lastDisconnect.account)} disconnected here, but Google did not confirm it removed Agent Bob's access. You can remove Agent Bob yourself in your Google account, under third-party connections.`}
          </Alert>
        )}

        {c.loading && state.accounts.length === 0 && state.connections.length === 0 && (
          <CircularProgress size={20} aria-label="Loading connections" />
        )}

        {empty && (
          <Typography variant="body2" color="text.secondary">
            This project has no connections.
          </Typography>
        )}

        {state.accounts.map((account) => (
          <AccountLine
            key={account.account}
            account={account}
            rows={account.connections.map(
              (name) => byName.get(name) ?? { name, description: '', available: false },
            )}
            canConnect={state.can_connect}
            disabledReason={state.connect_disabled_reason}
            busy={c.busy !== null}
            onConnect={() => void c.connect(account.account)}
            onDisconnect={() => setConfirming(account.account)}
          />
        ))}

        {others.length > 0 && (
          <Box data-testid="connection-other">
            {others.map((row) => (
              <ConnectionLine key={row.name} row={row} />
            ))}
          </Box>
        )}
      </Stack>

      <Dialog open={confirming !== null} onClose={() => setConfirming(null)}>
        <DialogContent>
          <DialogContentText>{DISCONNECT_CONFIRM_TEXT}</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirming(null)}>Cancel</Button>
          <Button
            color="error"
            onClick={() => {
              const account = confirming
              setConfirming(null)
              if (account !== null) void c.disconnect(account)
            }}
          >
            Disconnect
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

function AccountLine({
  account,
  rows,
  canConnect,
  disabledReason,
  busy,
  onConnect,
  onDisconnect,
}: {
  account: AccountRow
  rows: ConnectionRow[]
  canConnect: boolean
  disabledReason?: string
  busy: boolean
  onConnect: () => void
  onDisconnect: () => void
}) {
  const label = googleAccountLabel(account.account)
  const status = account.connected
    ? `${label} — Connected as ${account.account_email ?? 'an unknown account'}`
    : `${label} — Not connected`

  return (
    <Box data-testid={`connection-account-${account.account}`}>
      <Stack direction="row" spacing={2} alignItems="center" flexWrap="wrap" useFlexGap>
        <Typography variant="body2" sx={{ fontWeight: 500 }}>
          {status}
        </Typography>
        {account.connected ? (
          <Button size="small" variant="outlined" disabled={busy} onClick={onDisconnect}>
            Disconnect
          </Button>
        ) : (
          <Button size="small" variant="contained" disabled={!canConnect || busy} onClick={onConnect}>
            Connect Google
          </Button>
        )}
      </Stack>
      {account.connected && account.unavailable && (
        <Typography variant="caption" color="warning.main" component="p">
          {account.unavailable}
        </Typography>
      )}
      {!account.connected && !canConnect && disabledReason && (
        <Typography variant="caption" color="text.secondary" component="p">
          {disabledReason}
        </Typography>
      )}
      {rows.length > 0 && (
        <Box sx={{ mt: 0.5, pl: 2 }}>
          {rows.map((row) => (
            <ConnectionLine key={row.name} row={row} />
          ))}
        </Box>
      )}
    </Box>
  )
}

function ConnectionLine({ row }: { row: ConnectionRow }) {
  return (
    <Box sx={{ py: 0.25 }} title={row.available ? undefined : row.unavailable}>
      <Stack direction="row" spacing={1} alignItems="center">
        <Box
          component="span"
          aria-label={row.available ? 'available' : 'unavailable'}
          sx={{
            width: 8,
            height: 8,
            borderRadius: '50%',
            flexShrink: 0,
            bgcolor: row.available ? 'success.main' : 'text.disabled',
          }}
        />
        <Typography variant="body2" component="span">
          {row.name}
        </Typography>
        {row.description !== '' && (
          <Typography variant="body2" component="span" color="text.secondary">
            {row.description}
          </Typography>
        )}
      </Stack>
      {!row.available && row.unavailable && (
        <Typography variant="caption" color="text.secondary" component="p" sx={{ pl: 2 }}>
          {row.unavailable}
        </Typography>
      )}
    </Box>
  )
}
