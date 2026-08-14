// WorkerHistory — one worker's runs and rewrites on one rail
// (`docs/product/28-console-ia-design.md` §2.3, work plan 29 B2).
//
// This replaces the worker page's separate `Jobs` and `Lineage` tabs. They are
// one story told in time — *the prompt changed at 05:40, and the job that ran
// after came out different* — and two tabs is exactly what hides it.
//
// Everything the two tabs did survives, moved onto rows: the per-version diff,
// the "changes since your last review" banner, the mark-viewed toggle, the
// version chips, and folding a version back onto the Configuration tab to
// restore it. What is new is only the adjacency.
//
// The watermark is its own. `WorkerLineage` read the DESK's mark
// (`useFeedWatermark('desk', …)`), which meant clearing the Desk silently
// cleared this banner too — a borrowing the work plan named as a trap to break
// rather than preserve.

import { useMemo } from 'react'
import {
  Alert,
  Box,
  Chip,
  CircularProgress,
  Link,
  Paper,
  Stack,
  Typography,
} from '@mui/material'
import useConfigLog from '../useConfigLog.js'
import useEventsOverview from '../useEvents.js'
import { useWorkerJobs } from '../useWorkers.js'
import type { ConfigApiOptions } from '../configApi.js'
import { buildWorkerHistory, type WorkerHistoryRecord } from '../workerHistory.js'
import { SpineGap, SpineRail, SpineRow } from '../spine.js'
import { buildSessionPath } from '../permalink.js'
import { formatConfigTimestamp } from '../configLog.js'
import { useFeedWatermark, waterlineLabel } from '../watermark.js'
import {
  cumulativeHeading,
  cumulativeLineageDiff,
  lineageHeadEventId,
  useViewedVersions,
  type CumulativeLineageDiff,
} from '../lineageWaterline.js'
import { DiffBlock } from './ChangelogView.js'
import { FeedWaterline } from './FeedLiveness.js'

/** The surface this page's "since you last looked" mark is stored under. */
export const WORKER_HISTORY_SURFACE = 'worker-history'

/**
 * What the page needs to fold the Configuration tab to a past version.
 *
 * Structurally a superset of `WorkerLineage`'s `LineageVersion` — same four
 * fields, same names — so `WorkerPromptVersion` and the restore path keep
 * working unchanged while the Lineage tab is retired around them.
 */
export interface HistoryVersion {
  /** The config event that wrote this prompt. */
  eventId: string
  /** 1-based version number, oldest = v1. */
  version: number
  /** The prompt as it was. */
  prompt: string
  /** Unix milliseconds. */
  at: number
  /** The reason given for this rewrite; '' when none was. */
  rationale: string
  /** The worker that wrote it; '' when a human did. */
  actorWorker: string
}

export interface WorkerHistoryProps extends ConfigApiOptions {
  workerName: string
  projectId?: string
  onOpenSession?: (sessionId: string) => void
  /** Fold the Configuration tab to this version. */
  onSelectVersion?: (version: HistoryVersion) => void
  /** The version currently folded to, so its row can mark itself. */
  selectedEventId?: string | null
  /** Override the watermark (tests, and a host that keeps its own). */
  watermarkMs?: number
  /** Clock in unix ms, injectable so ages are testable. */
  nowMs?: number
}

const MONO = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }

export default function WorkerHistory({
  workerName,
  projectId = '',
  onOpenSession,
  onSelectVersion,
  selectedEventId = null,
  watermarkMs,
  nowMs,
  ...apiOptions
}: WorkerHistoryProps) {
  // Scoped server-side where the route supports it; the fold filters again by
  // worker regardless, so an over-fetching host cannot put another worker's
  // change on this rail.
  const log = useConfigLog({
    ...apiOptions,
    projectId,
    query: { entity: `worker:${workerName}` },
  })
  // The runs come from the session route because it filters IN THE DATABASE —
  // this worker's whole history up to the page size. The delivery list is a
  // recent window over the WHOLE project, so using it as the source would have
  // quietly shortened every worker's history; it is enrichment only.
  const runs = useWorkerJobs(workerName, apiOptions)
  const overview = useEventsOverview(apiOptions)

  const clockMs = nowMs ?? Date.now()

  const history = useMemo(
    () =>
      buildWorkerHistory({
        workerName,
        jobs: runs.jobs,
        deliveries: overview.deliveries,
        events: overview.events,
        subscriptions: overview.subscriptions,
        configEvents: log.events,
        nowMs: clockMs,
        projectId,
      }),
    [
      clockMs,
      log.events,
      overview.deliveries,
      overview.events,
      overview.subscriptions,
      projectId,
      runs.jobs,
      workerName,
    ],
  )

  // Its own surface, not the Desk's.
  const watermark = useFeedWatermark(WORKER_HISTORY_SURFACE, projectId, watermarkMs)
  const cumulative = cumulativeLineageDiff(history.lineage, watermark.markMs)
  const headEventId = lineageHeadEventId(history.lineage)
  const viewed = useViewedVersions(projectId, workerName, headEventId)

  const loading = log.loading || overview.loading || runs.loading
  // The lineage's own summary verbatim — it is the string doc 21's X5 fixed
  // ("2 rewrites · 3 distinct" was arithmetic nonsense to read) and it is
  // pinned by test. The run count is appended, not folded in.
  const summary = `${history.lineage.summary} · ${history.jobs} ${history.jobs === 1 ? 'run' : 'runs'}`

  return (
    <Box sx={{ p: 3, maxWidth: 880 }}>
      <Stack direction="row" spacing={1} alignItems="baseline" sx={{ mb: 0.5 }}>
        <Typography variant="subtitle1" sx={MONO}>
          {workerName}
        </Typography>
        <Typography variant="caption" color="text.secondary" data-testid="history-summary">
          {summary}
        </Typography>
        <Box sx={{ flex: 1 }} />
        {history.records.length > 0 && (
          <Link component="button" type="button" variant="caption" onClick={watermark.mark}>
            Mark these as seen
          </Link>
        )}
      </Stack>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 2 }}>
        What this worker has done and how it has changed, newest first, on one line of time — so a
        rewrite sits between the run before it and the run after it.
      </Typography>

      {!log.available && (
        <Alert severity="info" sx={{ mb: 2 }}>
          This deployment does not serve <code>GET /agent/config-events</code>, so the rewrites
          cannot be shown here. The log is still being written.
        </Alert>
      )}
      {log.available && log.error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {log.error}
        </Alert>
      )}
      {/* Said rather than hidden: a full page means this worker's older runs
          are missing, and a short list that looks authoritative is worse. */}
      {runs.truncated && (
        <Alert severity="info" sx={{ mb: 2 }}>
          Showing this worker's most recent runs only — older ones are not listed.
        </Alert>
      )}

      {loading ? (
        <Box sx={{ p: 3, display: 'flex', justifyContent: 'center' }}>
          <CircularProgress size={24} aria-label="Loading this worker's history" />
        </Box>
      ) : history.records.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          This worker has not run and has not been changed since the log started.
        </Typography>
      ) : (
        <>
          {cumulative && <CumulativeDiffBanner cumulative={cumulative} />}
          <SpineRail component="ol" role="log" aria-label="history" data-testid="history-rail">
            {history.records.map((record) => (
              <HistoryRow
                key={record.id}
                record={record}
                projectId={projectId}
                selected={selectedEventId === record.entry?.id}
                onOpenSession={onOpenSession}
                onSelectVersion={onSelectVersion}
                viewed={record.version !== null && viewed.isViewed(record.entry?.id ?? '')}
                onToggleViewed={
                  record.version !== null && record.entry
                    ? () => viewed.toggle(record.entry!.id)
                    : undefined
                }
              />
            ))}
          </SpineRail>
          {watermark.markMs > 0 && (
            <FeedWaterline label={waterlineLabel(watermark.markMs, clockMs)} />
          )}
        </>
      )}
    </Box>
  )
}

/**
 * "Changes since your last review" — shown only when more than one rewrite
 * landed after the mark. With zero or one, the per-row diffs below already are
 * the right view and this would repeat one of them under a grander heading.
 */
function CumulativeDiffBanner({ cumulative }: { cumulative: CumulativeLineageDiff }) {
  return (
    <Paper variant="outlined" data-testid="history-cumulative" sx={{ p: 2, mb: 2 }}>
      <Typography variant="subtitle2" sx={{ mb: 1 }}>
        {cumulativeHeading(cumulative)}
      </Typography>
      {/* Open by default: an operator who has been away is here to read exactly
          this, and a diff they have to click for is a diff they will not read. */}
      <DiffBlock
        lines={cumulative.diff.lines}
        added={cumulative.diff.added}
        removed={cumulative.diff.removed}
        defaultOpen
      />
    </Paper>
  )
}

function HistoryRow({
  record,
  projectId,
  selected,
  onOpenSession,
  onSelectVersion,
  viewed,
  onToggleViewed,
}: {
  record: WorkerHistoryRecord
  projectId: string
  selected: boolean
  onOpenSession?: (sessionId: string) => void
  onSelectVersion?: (version: HistoryVersion) => void
  viewed: boolean
  onToggleViewed?: () => void
}) {
  const entry = record.entry
  const isVersion = record.version !== null && entry !== null
  return (
    <>
      {record.gapBeforeLabel !== '' && <SpineGap label={record.gapBeforeLabel} component="li" />}
      <SpineRow
        glyph={record.glyph}
        component="li"
        data-testid={`history-row-${record.kind}`}
        sx={selected ? { outline: '1px dashed', outlineOffset: 4 } : undefined}
      >
        <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap" useFlexGap>
          {record.atMs > 0 && (
            <Typography variant="caption" color="text.secondary" sx={MONO}>
              {formatConfigTimestamp(record.atMs)}
            </Typography>
          )}
          <Typography variant="body2">{record.headline}</Typography>
          {record.meta !== '' && (
            <Typography variant="caption" color="text.secondary" sx={MONO}>
              {record.meta}
            </Typography>
          )}
          {/* The version number lives here and nowhere else on the row. It
              renders whether or not folding is wired up — a version without a
              number is not identifiable — and only becomes clickable when the
              host gave us somewhere to fold it to. */}
          {isVersion && (
            <Chip
              size="small"
              variant={selected ? 'filled' : 'outlined'}
              label={`v${record.version}`}
              data-testid={`history-version-${record.version}`}
              onClick={
                onSelectVersion === undefined
                  ? undefined
                  : () =>
                      onSelectVersion({
                        eventId: entry!.id,
                        version: record.version!,
                        prompt: record.prompt ?? '',
                        at: entry!.createdAt,
                        rationale: entry!.rationale,
                        actorWorker: entry!.actorWorker,
                      })
              }
            />
          )}
          {onToggleViewed !== undefined && (
            <Link component="button" type="button" variant="caption" onClick={onToggleViewed}>
              {viewed ? '✓ viewed' : 'mark viewed'}
            </Link>
          )}
        </Stack>
        {record.detail !== '' && (
          <Typography
            variant="body2"
            color={record.detailIsQuote ? 'text.primary' : 'text.secondary'}
            sx={{ mt: 0.5, whiteSpace: 'pre-wrap' }}
          >
            {record.detail}
          </Typography>
        )}
        {entry?.diff && (
          <Box sx={{ mt: 1 }}>
            <DiffBlock
              lines={entry.diff.lines}
              added={entry.diff.added}
              removed={entry.diff.removed}
              collapsible
            />
          </Box>
        )}
        {/* A real anchor, always: a job is shareable at its canonical permalink
            (F3), so the row must be copyable and middle-clickable even when the
            host also wants the click. The handler intercepts rather than
            replaces — dropping the href was how the old Jobs tab's shareability
            nearly went missing in this merge. */}
        {record.sessionId !== '' && (
          <Box sx={{ mt: 0.5 }}>
            <Link
              variant="caption"
              href={buildSessionPath(projectId, record.sessionId)}
              onClick={
                onOpenSession === undefined
                  ? undefined
                  : (e) => {
                      e.preventDefault()
                      onOpenSession(record.sessionId)
                    }
              }
            >
              {record.title || (record.kind === 'ask' ? 'open thread' : 'open the session')}
            </Link>
          </Box>
        )}
      </SpineRow>
    </>
  )
}
