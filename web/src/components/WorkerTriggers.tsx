// WorkerTriggers — what makes THIS worker run
// (`docs/product/28-console-ia-design.md` §2, work plan 29 B3).
//
// `AutomationPage`'s own header says the question a human arrives with is
// *"what makes this worker run?"* — a per-worker question that was answered on
// a project-wide page beside Workers rather than on the worker itself. Every
// subscription targets exactly one worker, so every row has exactly one home,
// and this is it.
//
// The two editors move across INTACT. This component is a list column and a
// scope filter; it does not reimplement subscription or schedule editing, and
// it must not grow its own idea of what those objects are.

import { useCallback, useState } from 'react'
import { Alert, Box, Button, Divider, List, ListItemButton, Stack, Typography } from '@mui/material'
import useSubscriptions from '../useSubscriptions.js'
import useSchedules from '../useSchedules.js'
import useEventsOverview from '../useEvents.js'
import type { ConfigApiOptions } from '../configApi.js'
import { describeSubscriptionTarget, type SubscriptionDraft } from '../subscriptions.js'
import { describeCron, type ScheduleDraft } from '../schedules.js'
import SubscriptionEditor from './SubscriptionEditor.js'
import ScheduleEditor from './ScheduleEditor.js'

/** Sentinel for "the create form is open" — not a legal id. */
const NEW_ROW = '#new'

export interface WorkerTriggersProps extends ConfigApiOptions {
  /** The worker every row here belongs to. */
  workerName: string
  /** Known worker names for the editors' pickers. */
  workerOptions?: string[]
  /** Preselect a row — the chart's clock deep link lands here. */
  selectedId?: string | null
}

export default function WorkerTriggers({
  workerName,
  workerOptions,
  selectedId = null,
  ...apiOptions
}: WorkerTriggersProps) {
  const subs = useSubscriptions(apiOptions)
  const scheds = useSchedules(apiOptions)
  const events = useEventsOverview({ ...apiOptions, limit: 50 })
  const [selected, setSelected] = useState<string | null>(selectedId)
  const [creating, setCreating] = useState<'subscription' | 'schedule' | null>(null)
  const [saving, setSaving] = useState(false)

  // Scope is the whole point of this surface: a row that belongs to another
  // worker is not this worker's trigger, whatever the project-wide list holds.
  const mySubs = subs.subscriptions.filter((s) => s.worker === workerName)
  const mySchedules = scheds.schedules.filter((s) => s.worker === workerName)

  const currentSub = mySubs.find((s) => s.id === selected) ?? null
  const currentSched = mySchedules.find((s) => s.id === selected) ?? null

  const saveSubscription = useCallback(
    async (draft: SubscriptionDraft, rationale: string) => {
      setSaving(true)
      const stored = await subs.save({ ...draft, worker: workerName }, rationale)
      setSaving(false)
      if (stored) {
        setSelected(stored.id)
        setCreating(null)
      }
    },
    [subs, workerName],
  )

  const saveSchedule = useCallback(
    async (draft: ScheduleDraft, rationale: string) => {
      setSaving(true)
      const stored = await scheds.save({ ...draft, worker: workerName }, rationale)
      setSaving(false)
      if (stored) {
        setSelected(stored.id)
        setCreating(null)
      }
    },
    [scheds, workerName],
  )

  const removeSubscription = useCallback(
    async (id: string, rationale: string) => {
      setSaving(true)
      const ok = await subs.remove(id, rationale)
      setSaving(false)
      if (ok) setSelected(null)
    },
    [subs],
  )

  const removeSchedule = useCallback(
    async (id: string, rationale: string) => {
      setSaving(true)
      const ok = await scheds.remove(id, rationale)
      setSaving(false)
      if (ok) setSelected(null)
    },
    [scheds],
  )

  const error = subs.error ?? scheds.error

  return (
    <Stack direction="row" sx={{ minHeight: 0 }}>
      <Box
        sx={{ width: 280, flexShrink: 0, borderRight: 1, borderColor: 'divider', overflowY: 'auto' }}
      >
        <Box sx={{ p: 2 }}>
          <Typography variant="overline" color="text.secondary">
            On an event
          </Typography>
          <List dense disablePadding data-testid="worker-subscriptions">
            {mySubs.map((sub) => (
              <ListItemButton
                key={sub.id}
                selected={selected === sub.id && creating === null}
                onClick={() => {
                  setSelected(sub.id)
                  setCreating(null)
                }}
              >
                <Typography variant="body2" sx={{ fontFamily: 'monospace', fontSize: 12 }}>
                  {sub.event_type}
                </Typography>
              </ListItemButton>
            ))}
          </List>
          {mySubs.length === 0 && (
            <Typography variant="caption" color="text.secondary">
              No event wakes this worker.
            </Typography>
          )}
          <Button
            size="small"
            sx={{ mt: 1, textTransform: 'none' }}
            data-testid="new-subscription"
            onClick={() => {
              setCreating('subscription')
              setSelected(NEW_ROW)
            }}
          >
            New subscription
          </Button>

          <Divider sx={{ my: 2 }} />

          <Typography variant="overline" color="text.secondary">
            On a clock
          </Typography>
          <List dense disablePadding data-testid="worker-schedules">
            {mySchedules.map((sched) => (
              <ListItemButton
                key={sched.id}
                selected={selected === sched.id && creating === null}
                onClick={() => {
                  setSelected(sched.id)
                  setCreating(null)
                }}
              >
                <Typography variant="body2" sx={{ fontSize: 12 }}>
                  {describeCron(sched.cron)}
                </Typography>
              </ListItemButton>
            ))}
          </List>
          {mySchedules.length === 0 && (
            <Typography variant="caption" color="text.secondary">
              No clock wakes this worker.
            </Typography>
          )}
          <Button
            size="small"
            sx={{ mt: 1, textTransform: 'none' }}
            data-testid="new-schedule"
            onClick={() => {
              setCreating('schedule')
              setSelected(NEW_ROW)
            }}
          >
            New schedule
          </Button>
        </Box>
      </Box>

      <Box sx={{ flex: 1, minWidth: 0, overflowY: 'auto', p: 2 }}>
        {error !== null && (
          <Alert severity="error" sx={{ mb: 2 }}>
            {error}
          </Alert>
        )}
        {creating === 'subscription' || currentSub ? (
          <SubscriptionEditor
            subscription={creating === 'subscription' ? null : currentSub}
            isNew={creating === 'subscription'}
            onSave={saveSubscription}
            onDelete={creating === 'subscription' ? undefined : removeSubscription}
            saving={saving}
            workerOptions={workerOptions}
            recentEvents={events.events}
          />
        ) : creating === 'schedule' || currentSched ? (
          <ScheduleEditor
            schedule={creating === 'schedule' ? null : currentSched}
            isNew={creating === 'schedule'}
            onSave={saveSchedule}
            onDelete={creating === 'schedule' ? undefined : removeSchedule}
            saving={saving}
            workerOptions={workerOptions}
          />
        ) : (
          // The definition sentence, so an empty pane teaches rather than shrugs.
          <Typography variant="body2" color="text.secondary">
            A subscription says: when an event of this type arrives, start a job for this worker. A
            schedule says: at these times, tell this worker to do this.
          </Typography>
        )}
      </Box>
    </Stack>
  )
}

/**
 * The read-only `Woken by` summary for the Configuration tab.
 *
 * The arrival question — *"what makes this worker run?"* — answered in ZERO
 * clicks on the screen that defines the worker, with editing one click away.
 * Read-only summary plus "open the thing it names" is the Desk's own rule, and
 * the reason this is not a second place to edit a trigger.
 */
export function WokenBy({
  workerName,
  onEditTriggers,
  ...apiOptions
}: ConfigApiOptions & { workerName: string; onEditTriggers?: () => void }) {
  const subs = useSubscriptions(apiOptions)
  const scheds = useSchedules(apiOptions)

  const mySubs = subs.subscriptions.filter((s) => s.worker === workerName)
  const mySchedules = scheds.schedules.filter((s) => s.worker === workerName)
  const nothing = mySubs.length === 0 && mySchedules.length === 0

  return (
    <Box sx={{ mt: 2 }} data-testid="woken-by">
      <Typography variant="overline" color="text.secondary">
        Woken by
      </Typography>
      {nothing ? (
        <Typography variant="body2" color="text.secondary">
          Nothing wakes this worker yet — it runs only when you start it by hand.
        </Typography>
      ) : (
        <Stack spacing={0.5} sx={{ mt: 0.5 }}>
          {mySubs.map((sub) => (
            <Typography key={sub.id} variant="body2" sx={{ fontFamily: 'monospace', fontSize: 13 }}>
              {describeSubscriptionTarget(sub)}
            </Typography>
          ))}
          {mySchedules.map((sched) => (
            <Typography key={sched.id} variant="body2">
              {describeCron(sched.cron)}
            </Typography>
          ))}
        </Stack>
      )}
      {onEditTriggers !== undefined && (
        <Button
          size="small"
          sx={{ mt: 0.5, textTransform: 'none' }}
          data-testid="edit-triggers"
          onClick={onEditTriggers}
        >
          Edit triggers
        </Button>
      )}
    </Box>
  )
}
