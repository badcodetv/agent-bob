// TeamFormingPanel — "Your team is forming", shown the moment a charter is
// approved.
//
// Approving a charter starts the architect's first run (go/httpapi/charter.go).
// Before this panel existed the screen then said "the architect exists" and
// stopped, at exactly the moment the project became worth watching. Now it
// watches: the architect's own job state, each worker as the architect hires
// it, what those workers are doing, and the events going past — and when the
// architect has said what it built, one obvious next step.
//
// Everything is read from the same routes the rest of the console uses
// (useTeamForming), so what this panel says and what the Desk says cannot
// disagree for longer than one poll.

import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Paper,
  Stack,
  Typography,
} from '@mui/material'
import type { ConfigApiOptions } from '../configApi.js'
import useTeamForming from '../useTeamForming.js'
import { describeMemberStatus, type TeamFormingPhase, type TeamMember } from '../teamForming.js'
import { agoShort } from '../timefmt.js'
import RunArchitectControl from './RunArchitectControl.js'
import RunCycleControl from './RunCycleControl.js'

export interface TeamFormingPanelProps extends ConfigApiOptions {
  /** The charter's architect name; '' means the default. */
  architectName?: string
  /** When the charter was approved, unix ms; 0 when not known. Older
   *  architect jobs are ignored, so a stale run never reads as this one. */
  approvedAtMs?: number
  /** The apply's own sentence for why the first run was NOT started, when it
   *  was not. Shows the manual "Run the architect now" fallback. */
  architectRunError?: string | null
  /** Take the human to the Desk. The "team is ready" button renders only when
   *  this is given. */
  onOpenDesk?: () => void
  /** Poll interval; forwarded to useTeamForming. */
  refreshMs?: number
  /** Fixed "now" for tests, unix ms. */
  nowMs?: number
}

const MONO = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }

function heading(phase: TeamFormingPhase, who: string): { title: string; body: string } {
  switch (phase) {
    case 'starting':
      return {
        title: 'Your team is forming',
        body: `${who} is starting. It reads the goal and the label registry, then decides which workers this project needs and creates them. The first container can take a minute or two — nothing is stuck, and you do not need to reload.`,
      }
    case 'designing':
      return {
        title: `${capitalise(who)} is designing your team`,
        body: 'Workers appear below as they are created, with what each one is for. It will tell you what it built, and why, when it finishes.',
      }
    case 'ready':
      return {
        title: 'Your team is ready',
        body: `${capitalise(who)} has finished its first pass and said what it built — the Desk shows its note and everything the team does from here. The team runs on its own schedule; to see a day's work now, run a cycle.`,
      }
    case 'failed':
      return {
        title: `${capitalise(who)}'s first run failed`,
        body: 'Nothing about the charter is lost. Read the reason below, then run it again.',
      }
  }
}

function capitalise(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

function MemberRow({ m, nowMs }: { m: TeamMember; nowMs: number }) {
  const busy = m.status === 'running' || m.status === 'queued'
  return (
    <Box component="li" sx={{ py: 1, borderTop: 1, borderColor: 'divider', listStyle: 'none' }} data-testid="team-member">
      <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap">
        <Typography variant="body2" sx={{ ...MONO, fontWeight: 600 }}>
          {m.name}
        </Typography>
        <Typography variant="caption" color={m.status === 'failed' ? 'error' : 'text.secondary'} data-testid="team-member-status">
          {busy && <CircularProgress size={10} sx={{ mr: 0.5, verticalAlign: 'middle' }} />}
          {m.enabled ? describeMemberStatus(m.status) : 'switched off'}
          {m.lastRunAt > 0 && ` · ${agoShort(m.lastRunAt * 1000, nowMs) === 'now' ? 'just now' : `${agoShort(m.lastRunAt * 1000, nowMs)} ago`}`}
        </Typography>
      </Stack>
      {m.line !== '' && (
        <Typography variant="body2" color="text.secondary">
          {m.line}
        </Typography>
      )}
    </Box>
  )
}

export default function TeamFormingPanel({
  architectName = '',
  approvedAtMs = 0,
  architectRunError = null,
  onOpenDesk,
  refreshMs,
  nowMs,
  ...apiOptions
}: TeamFormingPanelProps) {
  // A few seconds of slack: the approval's timestamp and the job's row come
  // from the same server, but not from the same statement.
  const sinceSeconds = approvedAtMs > 0 ? Math.floor(approvedAtMs / 1000) - 5 : 0
  const team = useTeamForming({ ...apiOptions, architectName, sinceSeconds, refreshMs })
  const now = nowMs ?? Date.now()
  const who = architectName.trim() === '' ? 'the architect' : architectName.trim()
  const notStarted = architectRunError !== null && architectRunError !== '' && team.architectDelivery === null
  const { title, body } = heading(team.phase, who)

  return (
    <Paper variant="outlined" sx={{ p: 2 }} data-testid="team-forming" data-phase={team.phase}>
      <Stack spacing={1.5}>
        <Stack direction="row" spacing={1} alignItems="center">
          {(team.phase === 'starting' || team.phase === 'designing') && !notStarted && (
            <CircularProgress size={16} aria-hidden />
          )}
          <Typography variant="subtitle1" component="h2" data-testid="team-forming-title">
            {notStarted ? 'The architect has not started' : title}
          </Typography>
        </Stack>

        {notStarted ? (
          <>
            <Alert severity="warning" data-testid="team-forming-not-started">
              {architectRunError}
            </Alert>
            <Box>
              <RunArchitectControl {...apiOptions} primary architectName={architectName} onEmitted={() => void team.reload()} />
            </Box>
          </>
        ) : (
          <Typography variant="body2" color="text.secondary">
            {body}
          </Typography>
        )}

        {team.phase === 'failed' && (
          <>
            <Alert severity="error" data-testid="team-forming-failed">
              {team.architectDelivery?.failure_reason || 'No reason was recorded. Open the Activity view for the job.'}
            </Alert>
            <Box>
              <RunArchitectControl {...apiOptions} primary architectName={architectName} onEmitted={() => void team.reload()} />
            </Box>
          </>
        )}

        {team.phase === 'ready' && (
          <Stack direction="row" spacing={2} alignItems="flex-start" flexWrap="wrap" useFlexGap>
            {onOpenDesk && (
              <Button variant="contained" onClick={onOpenDesk} data-testid="team-forming-open-desk">
                Your team is ready — go to the Desk
              </Button>
            )}
            <RunCycleControl {...apiOptions} onRan={() => void team.reload()} />
          </Stack>
        )}

        {team.error !== null && (
          <Alert severity="error" data-testid="team-forming-error">
            {team.error}
          </Alert>
        )}

        <Box>
          <Typography variant="overline" color="text.secondary">
            The team{team.members.length > 0 ? ` · ${team.members.length}` : ''}
          </Typography>
          {team.members.length === 0 ? (
            <Typography variant="body2" color="text.secondary" data-testid="team-forming-empty">
              {team.loading ? 'Reading the project…' : 'No workers yet. They appear here as they are created.'}
            </Typography>
          ) : (
            <Box component="ul" sx={{ m: 0, p: 0 }}>
              {team.members.map((m) => (
                <MemberRow key={m.name} m={m} nowMs={now} />
              ))}
            </Box>
          )}
        </Box>

        {team.activity.length > 0 && (
          <Box>
            <Typography variant="overline" color="text.secondary">
              Just happened
            </Typography>
            <Box component="ul" sx={{ m: 0, p: 0 }} data-testid="team-forming-activity">
              {team.activity.map((e) => (
                <Box component="li" key={e.id} sx={{ listStyle: 'none', py: 0.25 }}>
                  <Typography variant="caption" color="text.secondary">
                    <Box component="span" sx={MONO}>
                      {e.type}
                    </Box>
                    {e.envelope.worker !== '' && ` from ${e.envelope.worker}`}
                    {(e.occurred_at || e.created_at) > 0 &&
                      ` · ${agoShort((e.occurred_at || e.created_at) * 1000, now)}`}
                  </Typography>
                </Box>
              ))}
            </Box>
          </Box>
        )}
      </Stack>
    </Paper>
  )
}
