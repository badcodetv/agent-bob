// EmitEventControl — "Emit this event", and the confirmation it must ask for.
//
// Extracted from `EventReplayPanel` so the org chart's propagation panel can
// mount the same flow rather than grow a second one (design 28 §4.2, work plan
// 29 C1). One dialog, one wire format, one set of words.
//
// The wart this control exists to handle honestly: the chart's rule is that
// *every gesture is a proposal, never a mutation* — and emitting is the one
// real mutation on that surface, and an irreversible one. It wakes real workers
// and spends real tokens. So it is never the primary action, it always asks
// first, and the verb holds through the whole flow: *Emit this event* → *Emit a
// real event?* → *Emitted*.

import { useState } from 'react'
import {
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
} from '@mui/material'
import { useConfigApi, type ConfigApiOptions } from '../configApi.js'
import {
  coerceProjectEvent,
  EVENT_ENDPOINTS,
  type MatchableEvent,
  type ProjectEvent,
  type Subscription,
} from '../events.js'

export interface EmitEventControlProps extends ConfigApiOptions {
  /**
   * The parsed event, or null when the draft does not parse. `text` is required
   * because it is half of what actually goes on the wire.
   */
  event: (MatchableEvent & { text: string }) | null
  /** Subscriptions, so the confirmation can say how many would fire. */
  subscriptions: Subscription[]
  /** Override the events endpoint. */
  eventsEndpoint?: string
  /** Told what was written, so a host can refresh its lists. */
  onEmitted?: (event: ProjectEvent) => void
  /** How many subscriptions currently match — the confirmation quotes it. */
  matchedCount: number
}

export default function EmitEventControl({
  event,
  subscriptions,
  eventsEndpoint = EVENT_ENDPOINTS.events,
  onEmitted,
  matchedCount,
  ...apiOptions
}: EmitEventControlProps) {
  const { request } = useConfigApi(apiOptions)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [emitting, setEmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [emitted, setEmitted] = useState<ProjectEvent | null>(null)

  const emit = async () => {
    if (event === null) return
    setEmitting(true)
    setError(null)
    try {
      // Only {type, text} goes on the wire: core stamps the envelope and
      // ignores any envelope in the body (httpapi/events.go, ingestEventBody).
      // Sending the drafted envelope would imply it was honoured.
      const created = await request<unknown>(eventsEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: event.type, text: event.text }),
      })
      const stored = coerceProjectEvent(created)
      setEmitted(stored)
      setConfirmOpen(false)
      onEmitted?.(stored)
    } catch (err) {
      // The server's own text, not "HTTP 400" — these routes answer in
      // sentences a human can act on (configApi's convention).
      setError(err instanceof Error ? err.message : 'failed to emit the event')
    } finally {
      setEmitting(false)
    }
  }

  return (
    <>
      {/* Secondary by construction: tracing is what this panel is for, and the
          one thing here that cannot be undone must not be the button a hand
          falls on. */}
      <Button
        size="small"
        variant="outlined"
        color="warning"
        disabled={event === null}
        data-testid="emit-event"
        onClick={() => {
          setError(null)
          setConfirmOpen(true)
        }}
      >
        Emit this event
      </Button>

      {emitted !== null && (
        <Alert severity="success" sx={{ mt: 1 }}>
          Emitted “{emitted.type}” — event id <code>{emitted.id}</code>.{' '}
          {matchedCount === 0
            ? 'No subscription matched, so nothing started. The event is recorded.'
            : 'Watch the wires: the workers it matched are starting.'}
        </Alert>
      )}

      <Dialog open={confirmOpen} onClose={() => (emitting ? undefined : setConfirmOpen(false))}>
        <DialogTitle>Emit a real event?</DialogTitle>
        <DialogContent>
          <DialogContentText component="div">
            <p>
              This is not a dry run. It writes a <strong>real</strong> event of type{' '}
              <code>{event?.type ?? ''}</code> into this project, and any subscription that matches
              will <strong>wake its worker and start a job</strong>. Right now{' '}
              {matchedCount === 0
                ? 'no subscription matches, so nothing would start — the event is still recorded.'
                : `${matchedCount} of ${subscriptions.length} subscriptions match.`}
            </p>
            <p>
              Only <code>type</code> and <code>text</code> are sent. Core stamps the envelope
              (<code>source: external</code>, <code>depth: 0</code>) and ignores the one drafted
              above, so envelope filters will be tested against core&apos;s values, not yours.
            </p>
          </DialogContentText>
          {error !== null && (
            <Alert severity="error" sx={{ mt: 1 }}>
              {error}
            </Alert>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmOpen(false)} disabled={emitting}>
            Cancel
          </Button>
          <Button onClick={() => void emit()} disabled={emitting} variant="contained" color="warning">
            {emitting ? 'Emitting…' : 'Emit it'}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}
