// RunCycleControl — "Run a cycle now". Hurry the clock.
//
// A new project's schedules are daily, and the first hour is when a human most
// wants to watch the organisation work. This fires every ENABLED schedule once,
// now, through POST /agent/schedules/{id}/run — the scheduler's own firing, so
// each job is composed, briefed, capacity-gated and budgeted exactly as its
// 09:00 run would be. Workers woken by events (a summary-writer on
// `worker.finished`, say) follow on their own as those jobs finish; nothing
// here fakes an event to reach them.
//
// It confirms first, naming who will run, for RunArchitectControl's reason:
// this spends real tokens and cannot be undone. The schedule list is read at
// the moment of the click, not held from page load — the architect may have
// hired someone thirty seconds ago.

import { useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Typography,
} from '@mui/material'
import { useConfigApi, type ConfigApiOptions } from '../configApi.js'
import { coerceSchedule, describeCron, SCHEDULE_ENDPOINTS, type Schedule } from '../schedules.js'
import {
  coerceScheduleRunResult,
  describeRunOutcome,
  planCycle,
  scheduleRunEndpoint,
  scheduleTarget,
  type CycleLine,
} from '../teamForming.js'

export interface RunCycleControlProps extends ConfigApiOptions {
  /** Rendered as the primary action. */
  primary?: boolean
  disabled?: boolean
  /** Told what the cycle started, so a host can refresh what it shows. */
  onRan?: (lines: CycleLine[]) => void
}

export default function RunCycleControl({
  primary = false,
  disabled = false,
  onRan,
  ...apiOptions
}: RunCycleControlProps) {
  const { request } = useConfigApi(apiOptions)
  const [planning, setPlanning] = useState(false)
  const [plan, setPlan] = useState<Schedule[] | null>(null)
  const [running, setRunning] = useState(false)
  const [lines, setLines] = useState<CycleLine[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const open = async () => {
    setPlanning(true)
    setError(null)
    setLines(null)
    try {
      const data = await request<{ schedules?: unknown[] } | null>(SCHEDULE_ENDPOINTS.list)
      const raw = Array.isArray(data?.schedules) ? data!.schedules! : []
      setPlan(planCycle(raw.map((s) => coerceSchedule(s))))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to read the schedules')
    } finally {
      setPlanning(false)
    }
  }

  const run = async (schedules: Schedule[]) => {
    setRunning(true)
    const settled = await Promise.allSettled(
      schedules.map((s) => request<unknown>(scheduleRunEndpoint(s.id), { method: 'POST' })),
    )
    const out = settled.map((r, i) => {
      const target = scheduleTarget(schedules[i])
      if (r.status === 'rejected') {
        const reason = r.reason instanceof Error ? r.reason.message : 'did not start'
        return describeRunOutcome(target, 'error', reason)
      }
      const res = coerceScheduleRunResult(r.value)
      return describeRunOutcome(target, res.outcome, res.reason)
    })
    setRunning(false)
    setPlan(null)
    setLines(out)
    onRan?.(out)
  }

  // The same worker can be on several clocks (an end-of-week ask and a
  // send-day ask, say); the confirmation names each by when it would have run,
  // or it lists one worker twice and reads like a bug.
  const who = plan?.map((s) => {
    const when = describeCron(s.cron)
    return when === null ? scheduleTarget(s) : `${scheduleTarget(s)} — ${when.charAt(0).toLowerCase()}${when.slice(1).replace(/\.$/, '')}`
  }) ?? []

  return (
    <Box>
      <Button
        variant={primary ? 'contained' : 'outlined'}
        size={primary ? 'medium' : 'small'}
        disabled={disabled || planning || running}
        onClick={() => void open()}
        data-testid="run-cycle"
      >
        Run a cycle now
      </Button>

      {lines !== null && (
        <Alert
          severity={lines.every((l) => l.ok) ? 'success' : 'warning'}
          sx={{ mt: 1 }}
          onClose={() => setLines(null)}
          data-testid="run-cycle-result"
        >
          <Typography variant="body2" sx={{ mb: 0.5 }}>
            {lines.length === 1 ? 'One schedule fired:' : `${lines.length} schedules fired:`}
          </Typography>
          <Box component="ul" sx={{ m: 0, pl: 2.5 }}>
            {lines.map((l, i) => (
              <li key={`${l.target}-${i}`}>
                <Typography variant="body2">{l.text}</Typography>
              </li>
            ))}
          </Box>
        </Alert>
      )}

      {error !== null && plan === null && (
        <Alert severity="error" sx={{ mt: 1 }} data-testid="run-cycle-error">
          {error}
        </Alert>
      )}

      <Dialog open={plan !== null} onClose={() => (running ? undefined : setPlan(null))}>
        <DialogTitle>Run a cycle now?</DialogTitle>
        <DialogContent>
          {plan !== null && plan.length === 0 ? (
            <DialogContentText data-testid="run-cycle-nothing">
              Nothing in this project is on a clock yet, so there is nothing to run. Workers that
              wake on events will run when those events arrive.
            </DialogContentText>
          ) : (
            <DialogContentText component="div">
              <p>
                This does what the clock would do later, now: every enabled schedule fires once, and
                each starts a real job.
              </p>
              <Box component="ul" sx={{ pl: 2.5 }} data-testid="run-cycle-plan">
                {who.map((w, i) => (
                  <li key={`${w}-${i}`}>{w}</li>
                ))}
              </Box>
              <p>
                Workers that wake on events — someone finishing, say — follow on their own. A worker
                that is already busy queues rather than running twice.
              </p>
            </DialogContentText>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPlan(null)} disabled={running}>
            {plan !== null && plan.length === 0 ? 'Close' : 'Cancel'}
          </Button>
          {plan !== null && plan.length > 0 && (
            <Button variant="contained" onClick={() => void run(plan)} disabled={running}>
              {running ? 'Starting…' : 'Run them'}
            </Button>
          )}
        </DialogActions>
      </Dialog>
    </Box>
  )
}
