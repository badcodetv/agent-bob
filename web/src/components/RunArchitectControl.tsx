// RunArchitectControl — "Run the architect now".
//
// It posts an EVENT (`architect.run`), and that is not a stylistic choice.
// Briefings are built only inside ComposeJob on the dispatch path, so opening
// a chat with the architect would silently deprive it of the label registry —
// no error, no empty section, just an architect designing an organisation
// without knowing the project's vocabulary (design §C, Decision C5). The one
// way to wake it correctly is through the subscription the charter created.
//
// It is NOT EmitEventControl. That control's words are about tracing a wire
// on the org chart ("Emit this event", "Emit a real event?", a paragraph about
// which envelope fields core stamps) and its confirmation quotes a count of
// matching subscriptions — a list this screen has no reason to load, and a
// count that would be a guess if it did. Same shape, different sentence.
//
// It still confirms first, for EmitEventControl's reason: this spends real
// tokens and cannot be undone.

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
import { coerceProjectEvent, EVENT_ENDPOINTS, type ProjectEvent } from '../events.js'

/** The event type the charter's subscription listens for (go/charter). */
export const ARCHITECT_RUN_EVENT = 'architect.run'

export interface RunArchitectControlProps extends ConfigApiOptions {
  /** The architect's name, for the words. Defaults to "the architect". */
  architectName?: string
  /** Override the events endpoint. */
  eventsEndpoint?: string
  /** Told what was written, so a host can send the human to the job. */
  onEmitted?: (event: ProjectEvent) => void
  /** Rendered as the primary action rather than the secondary one. True on
   *  the onboarding screen, where running it is the obvious next step; false
   *  on the workers page, where it is one control among many. */
  primary?: boolean
  disabled?: boolean
}

export default function RunArchitectControl({
  architectName = '',
  eventsEndpoint = EVENT_ENDPOINTS.events,
  onEmitted,
  primary = false,
  disabled = false,
  ...apiOptions
}: RunArchitectControlProps) {
  const { request } = useConfigApi(apiOptions)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [emitted, setEmitted] = useState<ProjectEvent | null>(null)

  const who = architectName.trim() === '' ? 'the architect' : architectName.trim()

  const run = async () => {
    setRunning(true)
    setError(null)
    try {
      const created = await request<unknown>(eventsEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: ARCHITECT_RUN_EVENT,
          text: 'Run the architect now (asked for from the console).',
        }),
      })
      const stored = coerceProjectEvent(created)
      setEmitted(stored)
      setConfirmOpen(false)
      onEmitted?.(stored)
    } catch (err) {
      // The server's own words. "host port pool is exhausted" is a sentence a
      // human can act on; "HTTP 500" is not.
      setError(err instanceof Error ? err.message : 'failed to run the architect')
    } finally {
      setRunning(false)
    }
  }

  return (
    <>
      <Button
        variant={primary ? 'contained' : 'outlined'}
        size={primary ? 'medium' : 'small'}
        disabled={disabled}
        data-testid="run-architect"
        onClick={() => {
          setError(null)
          setConfirmOpen(true)
        }}
      >
        Run the architect now
      </Button>

      {emitted !== null && (
        <Alert severity="success" sx={{ mt: 1 }} data-testid="run-architect-emitted">
          {who} is starting. It will read the goal and the label registry, decide what workers this
          project needs, and tell you what it did.
        </Alert>
      )}

      {error !== null && emitted === null && !confirmOpen && (
        <Alert severity="error" sx={{ mt: 1 }} data-testid="run-architect-error">
          {error}
        </Alert>
      )}

      <Dialog open={confirmOpen} onClose={() => (running ? undefined : setConfirmOpen(false))}>
        <DialogTitle>Run {who} now?</DialogTitle>
        <DialogContent>
          <DialogContentText component="div">
            <p>
              This starts a real job. {who} will look at what this project has, decide what should
              change, and <strong>make those changes itself</strong> — creating workers, wiring
              them to their triggers, and rewriting prompts.
            </p>
            <p>
              It will tell you what it did when it finishes, and every change can be reverted from
              the changelog.
            </p>
          </DialogContentText>
          {error !== null && (
            <Alert severity="error" sx={{ mt: 1 }}>
              {error}
            </Alert>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirmOpen(false)} disabled={running}>
            Cancel
          </Button>
          <Button onClick={() => void run()} disabled={running} variant="contained">
            {running ? 'Starting…' : 'Run it'}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}
