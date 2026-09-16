// The Desk's team view (the 2026-09-16 dashboard mockup, layout A): who is on
// the project and a way to talk to each of them, the last few things that
// happened, and everything that wakes the team.
//
// Presentational only. The rows come from `useDesk`, which already fetches
// every list these draw — this file adds no request of its own, and the one
// write it can start ("Chat to …") is the host's, through `onChatWithWorker`.

import { useState, type ReactNode } from 'react'
import { Box, Button, CircularProgress, Link, Stack, Typography } from '@mui/material'
import ChatIcon from '@mui/icons-material/ChatBubbleOutline'
import ScheduleIcon from '@mui/icons-material/Schedule'
import BoltIcon from '@mui/icons-material/BoltOutlined'
import type { ActivityRecord } from '../activity.js'
import type { FirstToNarrate } from '../firsts.js'
import {
  RECENT_ACTIVITY_LIMIT,
  wakeRulesFor,
  workerRoleLine,
  workerStatus,
  type WakeRule,
  type WakeRules,
  type WorkerStatusTone,
} from '../team.js'
import { agoShort } from '../timefmt.js'
import type { Worker } from '../workers.js'
import { SpineRail, SpineRow } from '../spine.js'

const MONO = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }

/** Authorship is a colour (§3.2): the console theme's named palette entries. */
const TONE_COLOR: Record<WorkerStatusTone, string> = {
  agent: 'steel.main',
  attention: 'rose.main',
  failure: 'fault.main',
  idle: 'text.disabled',
  off: 'text.disabled',
}

/** A section heading: the label, its count, and one qualifying line. */
function Heading({ label, count, caption, action }: { label: string; count?: number; caption?: string; action?: ReactNode }) {
  return (
    <Stack direction="row" alignItems="baseline" spacing={1} sx={{ mb: 1.5 }}>
      <Typography variant="subtitle2" component="h2" sx={{ textTransform: 'uppercase', letterSpacing: '0.08em' }}>
        {label}
      </Typography>
      {count !== undefined && (
        <Typography variant="subtitle2" sx={MONO} color="text.secondary">
          {count}
        </Typography>
      )}
      <Box sx={{ flex: 1 }} />
      {caption && (
        <Typography variant="caption" color="text.secondary">
          {caption}
        </Typography>
      )}
      {action}
    </Stack>
  )
}

// ---------------------------------------------------------------------------
// The team
// ---------------------------------------------------------------------------

export interface TeamSectionProps {
  workers: Worker[]
  rules: WakeRules
  /** Newest first — `useDesk().activity`. */
  activity: ActivityRecord[]
  nowMs: number
  /** Start a new conversation with this worker. Rejecting shows its message on the card. */
  onChatWithWorker?: (worker: string) => Promise<void> | void
  /** Open the worker's own page (its full prompt, triggers, history). */
  onOpenWorker?: (worker: string) => void
}

export function TeamSection({ workers, rules, activity, nowMs, onChatWithWorker, onOpenWorker }: TeamSectionProps) {
  // Switched-off workers last: they are part of the team's history, not its work.
  const ordered = [...workers].sort((a, b) => Number(b.enabled) - Number(a.enabled) || a.name.localeCompare(b.name))
  return (
    <Box component="section" aria-label="The team" data-testid="desk-team">
      <Heading label="The team" count={workers.length} caption="who does what — chat to any of them" />
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', lg: 'repeat(2, minmax(0, 1fr))' }, gap: 1.75 }}>
        {ordered.map((w) => (
          <WorkerCard
            key={w.name}
            worker={w}
            rules={wakeRulesFor(w.name, rules)}
            status={workerStatus(w, activity, nowMs)}
            onChat={onChatWithWorker}
            onOpen={onOpenWorker}
          />
        ))}
      </Box>
    </Box>
  )
}

function WorkerCard({
  worker,
  rules,
  status,
  onChat,
  onOpen,
}: {
  worker: Worker
  rules: WakeRule[]
  status: ReturnType<typeof workerStatus>
  onChat?: (worker: string) => Promise<void> | void
  onOpen?: (worker: string) => void
}) {
  const [starting, setStarting] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const role = workerRoleLine(worker)

  const chat = async () => {
    if (!onChat) return
    setStarting(true)
    setFailure(null)
    try {
      await onChat(worker.name)
    } catch (err) {
      setFailure(err instanceof Error ? err.message : `Could not start a chat with ${worker.name}.`)
    } finally {
      setStarting(false)
    }
  }

  return (
    <Box
      component="article"
      aria-label={worker.name}
      data-testid={`desk-worker-${worker.name}`}
      sx={{
        border: 1,
        borderColor: 'divider',
        borderRadius: 1,
        p: 2.25,
        display: 'flex',
        flexDirection: 'column',
        gap: 1.25,
        opacity: worker.enabled ? 1 : 0.64,
      }}
    >
      <Stack direction="row" alignItems="baseline" justifyContent="space-between" spacing={1}>
        {/* The name never wraps; a long status gives way to it instead. */}
        <Typography sx={{ ...MONO, fontSize: 16, fontWeight: 500, whiteSpace: 'nowrap', flexShrink: 0 }}>
          {worker.name}
        </Typography>
        <Stack direction="row" alignItems="center" spacing={0.75} sx={{ color: TONE_COLOR[status.tone], minWidth: 0 }}>
          <Box aria-hidden sx={{ width: 7, height: 7, borderRadius: '50%', bgcolor: 'currentColor', flex: 'none' }} />
          <Typography variant="caption" sx={{ ...MONO, color: status.tone === 'agent' || status.tone === 'idle' ? 'text.secondary' : 'inherit' }} noWrap>
            {status.label}
          </Typography>
        </Stack>
      </Stack>

      <Typography variant="body1" color={role === '' ? 'text.secondary' : 'text.primary'}>
        {role === '' ? 'No description or prompt yet.' : role}
      </Typography>

      <Stack spacing={0.5}>
        {rules.length === 0 ? (
          <Typography variant="caption" color="text.secondary" sx={MONO}>
            only wakes when you chat or another worker asks
          </Typography>
        ) : (
          rules.map((r) => <RuleChip key={`${r.kind}:${r.id}`} rule={r} />)
        )}
      </Stack>

      <Stack direction="row" alignItems="center" spacing={1.5} sx={{ mt: 'auto', pt: 0.25 }}>
        {onChat && worker.enabled && (
          <Button
            size="small"
            variant="contained"
            disableElevation
            startIcon={starting ? <CircularProgress size={14} color="inherit" /> : <ChatIcon fontSize="small" />}
            disabled={starting}
            onClick={() => void chat()}
          >
            {starting ? 'Starting…' : `Chat to ${worker.name}`}
          </Button>
        )}
        {onOpen && (
          <Link component="button" type="button" variant="body2" onClick={() => onOpen(worker.name)}>
            Full prompt
          </Link>
        )}
      </Stack>
      {failure !== null && (
        <Typography variant="caption" color="error.main" role="alert">
          {failure}
        </Typography>
      )}
    </Box>
  )
}

function RuleChip({ rule }: { rule: WakeRule }) {
  const Icon = rule.kind === 'clock' ? ScheduleIcon : BoltIcon
  return (
    <Stack direction="row" alignItems="center" spacing={0.75} sx={{ color: 'text.secondary' }} title={rule.detail}>
      <Icon sx={{ fontSize: 14 }} aria-hidden />
      <Typography variant="caption" sx={{ ...MONO, textDecoration: rule.enabled ? 'none' : 'line-through' }}>
        {rule.sentence.charAt(0).toLowerCase() + rule.sentence.slice(1)}
        {rule.enabled ? '' : ' (off)'}
      </Typography>
    </Stack>
  )
}

// ---------------------------------------------------------------------------
// What happened
// ---------------------------------------------------------------------------

export interface RecentActivitySectionProps {
  /** Newest first — `useDesk().activity`. Only the first few are drawn. */
  activity: ActivityRecord[]
  nowMs: number
  onOpenSession?: (sessionId: string) => void
  onOpenActivity?: () => void
  /** A "first time this happened" sentence per record id, drawn under its row. */
  firstNarrationFor?: (record: ActivityRecord) => FirstToNarrate | undefined
  renderFirstNarration?: (first: FirstToNarrate) => ReactNode
  limit?: number
}

export function RecentActivitySection({
  activity,
  nowMs,
  onOpenSession,
  onOpenActivity,
  firstNarrationFor,
  renderFirstNarration,
  limit = RECENT_ACTIVITY_LIMIT,
}: RecentActivitySectionProps) {
  const shown = activity.slice(0, limit)
  return (
    <Box component="section" aria-label="What happened" data-testid="desk-recent">
      <Heading
        label="What happened"
        action={
          onOpenActivity ? (
            <Link component="button" type="button" variant="caption" onClick={onOpenActivity}>
              All activity →
            </Link>
          ) : undefined
        }
      />
      {shown.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          Nothing has happened yet.
        </Typography>
      ) : (
        <SpineRail component="ol" role="log" aria-label="What happened">
          {shown.map((r) => {
            const first = firstNarrationFor?.(r)
            const ago = agoShort(r.atMs, nowMs)
            return (
              <SpineRow key={r.id} glyph={r.glyph} component="li" data-testid={`desk-recent-${r.kind}`}>
                <Typography variant="body2">{r.headline}</Typography>
                <Stack direction="row" spacing={1.5} alignItems="baseline">
                  <Typography variant="caption" color="text.secondary" sx={MONO}>
                    {[r.eventType, ago === '' ? '' : ago === 'now' ? 'just now' : `${ago} ago`].filter(Boolean).join(' · ')}
                  </Typography>
                  {r.sessionId !== '' && onOpenSession && (
                    <Link component="button" type="button" variant="caption" onClick={() => onOpenSession(r.sessionId)}>
                      open
                    </Link>
                  )}
                </Stack>
                {first && renderFirstNarration?.(first)}
              </SpineRow>
            )
          })}
        </SpineRail>
      )}
    </Box>
  )
}

// ---------------------------------------------------------------------------
// What wakes the team
// ---------------------------------------------------------------------------

export function WakeSection({ rules, onOpenWorker }: { rules: WakeRules; onOpenWorker?: (worker: string) => void }) {
  return (
    <Box component="section" aria-label="What wakes the team" data-testid="desk-wake">
      <Heading label="What wakes the team" caption="nothing runs unless one of these fires, or someone chats" />
      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: 'repeat(2, minmax(0, 1fr))' }, gap: 5 }}>
        <RuleList
          title="On a clock"
          icon={<ScheduleIcon sx={{ fontSize: 16 }} aria-hidden />}
          rules={rules.clocks}
          empty="No schedules."
          testId="desk-clocks"
          onOpenWorker={onOpenWorker}
        />
        <RuleList
          title="When something happens"
          icon={<BoltIcon sx={{ fontSize: 16 }} aria-hidden />}
          rules={rules.handlers}
          empty="No event handlers."
          testId="desk-handlers"
          onOpenWorker={onOpenWorker}
        />
      </Box>
    </Box>
  )
}

function RuleList({
  title,
  icon,
  rules,
  empty,
  testId,
  onOpenWorker,
}: {
  title: string
  icon: ReactNode
  rules: WakeRule[]
  empty: string
  testId: string
  onOpenWorker?: (worker: string) => void
}) {
  return (
    <Box data-testid={testId}>
      <Stack direction="row" spacing={1} alignItems="center" sx={{ pb: 1, borderBottom: 1, borderColor: 'divider' }}>
        {icon}
        <Typography variant="body2" sx={{ fontWeight: 600 }}>
          {title}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {rules.length}
        </Typography>
      </Stack>
      {rules.length === 0 ? (
        <Typography variant="body2" color="text.secondary" sx={{ py: 1.25 }}>
          {empty}
        </Typography>
      ) : (
        <Box component="ul" sx={{ listStyle: 'none', m: 0, p: 0 }}>
          {rules.map((r) => (
            <Box
              component="li"
              key={r.id}
              sx={{
                display: 'grid',
                gridTemplateColumns: 'minmax(0, 1.6fr) minmax(0, 1fr) 40px',
                gap: 1.5,
                py: 1.25,
                borderBottom: 1,
                borderColor: 'divider',
                alignItems: 'center',
                opacity: r.enabled ? 1 : 0.64,
              }}
            >
              <Stack spacing={0.25} sx={{ minWidth: 0 }}>
                <Typography variant="body2">{r.sentence}</Typography>
                <Typography variant="caption" color="text.secondary" sx={{ ...MONO, overflowWrap: 'anywhere' }}>
                  {r.detail}
                </Typography>
              </Stack>
              <Stack direction="row" spacing={0.75} alignItems="baseline" sx={{ minWidth: 0 }}>
                <Typography variant="caption" color="text.disabled">
                  {r.worker !== '' ? 'wakes' : 'messages'}
                </Typography>
                {r.worker !== '' && onOpenWorker ? (
                  <Link component="button" type="button" variant="body2" sx={MONO} onClick={() => onOpenWorker(r.worker)}>
                    {r.worker}
                  </Link>
                ) : (
                  <Typography variant="body2" sx={MONO} noWrap>
                    {r.worker !== '' ? r.worker : r.targetSession}
                  </Typography>
                )}
              </Stack>
              <Typography variant="caption" sx={{ ...MONO, color: r.enabled ? 'steel.main' : 'text.disabled' }}>
                {r.enabled ? 'on' : 'off'}
              </Typography>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}
