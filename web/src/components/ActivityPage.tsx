// ActivityPage — the whole project on one rail, in time order
// (`docs/product/28-console-ia-design.md` §1, work plan 29 B1).
//
// This replaces the Events page's Events / Jobs / Changelog tabs, which split
// one story across three screens: a prompt rewrite and the job it changed lived
// on two of them, so the reader had to hold a timestamp in their head to join
// them. On one rail they are adjacent rows.
//
// Two rules this page must not break, both from the design:
//
//   1. **The chips are lenses, not tabs.** They subset the rail IN PLACE — the
//      rail, the watermark and the scroll position all survive a chip change.
//      A chip that re-mounted the list would be a tab wearing a chip's clothes,
//      and the merge would be a rename. Filtering happens inside the fold (so
//      gap markers stay correct for what is on screen), which is why changing
//      lens re-folds rather than re-fetches.
//   2. **The glyph set is closed.** Every row's tick comes from the fold, which
//      draws only from `spine.tsx`'s five. Nothing here invents a sixth.
//
// PR0 (design/2026-09-11-onboarding-and-the-guide.md §6): this is also the
// page's changes lens's only route to "Revert to this version" — the console's
// own copy (CharterPanel, RunArchitectControl, docs/18 §9a) promises the
// button on every changelog entry, but `ChangelogView`, which owns it, is no
// longer mounted anywhere in the shipped shell. Rather than fork a second copy
// of the button, its dialog and its confirm wording, `RevertControl` is
// imported from `ChangelogView.tsx` and mounted on the `changes` rows below —
// one control, two rails.

import { useState } from 'react'
import { Alert, Box, Chip, Link, Stack, Typography } from '@mui/material'
import useActivity, { type UseActivityOptions } from '../useActivity.js'
import {
  ACTIVITY_LENSES,
  oldestShownMs,
  type ActivityLens,
  type ActivityRecord,
} from '../activity.js'
import { SpineGap, SpineRail, SpineRow } from '../spine.js'
import { formatConfigTimestamp, type RevertBlock } from '../configLog.js'
import { RevertControl } from './ChangelogView.js'
import { newItemsSummary, waterlineLabel } from '../watermark.js'
import { highlightSx, highlightMarker, NEW_MARKER_LABEL, type HighlightTone } from '../feedhighlight.js'
import usePrefersReducedMotion from '../useReducedMotion.js'
import useStagedFeed from '../useStagedFeed.js'
import { FeedWaterline, NewItemsPill, PauseLiveUpdates } from './FeedLiveness.js'
import AboutThisScreen from './AboutThisScreen.js'
import ClampedText from './ClampedText.js'

export interface ActivityPageProps extends UseActivityOptions {
  projectId: string
  /** Open a session thread (typically useSessionPermalink().openSession). */
  onOpenSession?: (sessionId: string) => void
  /** Show the pause toggle. Default: only while actually polling. */
  showPauseToggle?: boolean
  /** Heading. Pass '' for none. */
  title?: string
}

/** Identifiers are mono, content is prose (§3.4). */
const MONO = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }

/** How far "Show earlier" reaches back each time it is pressed. */
export const ACTIVITY_WINDOW_STEP_MS = 24 * 60 * 60 * 1000

/** What each chip is called, and what it means. Copy lives in one place. */
export const ACTIVITY_LENS_LABELS: Record<ActivityLens, string> = {
  all: 'all',
  events: 'events',
  jobs: 'jobs',
  changes: 'changes',
}

/** The empty line for each lens — an empty screen names what would fill it. */
const EMPTY_COPY: Record<ActivityLens, string> = {
  all: 'Nothing has happened in this project yet.',
  events: 'No events have arrived.',
  jobs: 'No jobs have run.',
  changes: 'No configuration changes recorded.',
}

/** Authorship decides the tint, exactly as it decides the glyph (§3.2). */
function toneFor(record: ActivityRecord): HighlightTone {
  if (record.kind === 'ask') return 'ask'
  if (record.glyph === 'failure') return 'failure'
  return record.glyph === 'human' ? 'human' : 'agent'
}

export default function ActivityPage({
  projectId,
  onOpenSession,
  showPauseToggle,
  title = 'Activity',
  ...activityOptions
}: ActivityPageProps) {
  const [lens, setLens] = useState<ActivityLens>('all')
  const [windowStartMs, setWindowStartMs] = useState(0)
  const [paused, setPaused] = useState(false)
  const reduced = usePrefersReducedMotion()
  // Lives here, not on the row that was clicked, for the same reason
  // ChangelogView keeps it at the page level: a successful revert reloads the
  // rail, which remounts every row, so a notice held inside one would vanish
  // at exactly the moment it was earned.
  const [justReverted, setJustReverted] = useState(false)

  const {
    records,
    loading,
    error,
    asksHaveMessages,
    asksRouteAvailable,
    lastSeenMs,
    markSeen,
    nowMs,
    revertBlocks,
    reload,
  } = useActivity({
    ...activityOptions,
    projectId,
    lens,
    windowStartMs,
    paused: activityOptions.paused ?? paused,
  })

  const polling = (activityOptions.refreshMs ?? 0) > 0
  const showPause = showPauseToggle ?? polling

  // Arrivals stage behind a pill rather than inserting themselves under the
  // reader's eye. Keyed by record id, which is stable across refetches.
  const feed = useStagedFeed(records, (r) => r.id, { paused })

  const hasAsks = records.some((r) => r.kind === 'ask')
  const oldest = oldestShownMs(records)

  return (
    <Box sx={{ p: 3, maxWidth: 900 }}>
      <Stack direction="row" alignItems="baseline" justifyContent="space-between" sx={{ mb: 2 }}>
        {title !== '' && <Typography variant="h6">{title}</Typography>}
        <Stack direction="row" spacing={2} alignItems="center">
          {showPause && <PauseLiveUpdates paused={paused} onChange={setPaused} />}
          {records.length > 0 && (
            <Link component="button" type="button" variant="caption" onClick={markSeen}>
              Mark these as seen
            </Link>
          )}
        </Stack>
      </Stack>

      <AboutThisScreen surface="activity" projectId={projectId} />

      {/* Lenses. `role="group"` and not a tablist: these do not swap panels,
          they narrow one list, and announcing them as tabs would promise a
          navigation that does not happen. */}
      <Stack
        direction="row"
        spacing={0.75}
        sx={{ mb: 2, flexWrap: 'wrap' }}
        role="group"
        aria-label="filter this list"
      >
        {ACTIVITY_LENSES.map((option) => (
          <Chip
            key={option}
            size="small"
            label={ACTIVITY_LENS_LABELS[option]}
            variant={option === lens ? 'filled' : 'outlined'}
            aria-pressed={option === lens}
            data-testid={`activity-lens-${option}`}
            onClick={() => setLens(option)}
          />
        ))}
      </Stack>

      {justReverted && (
        <Alert
          severity="success"
          sx={{ mb: 2 }}
          data-testid="revert-done"
          onClose={() => setJustReverted(false)}
        >
          Reverted. The change that put it back is the newest entry below — the entry you
          reverted stays exactly where it is, because nothing here is ever erased.
        </Alert>
      )}

      {error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {hasAsks && !asksHaveMessages && (
        <Alert severity="info" sx={{ mb: 2 }}>
          {asksRouteAvailable ? (
            <>
              <code>GET /agent/attention-requests</code> did not answer, so the waiting rows show
              without the sentence the worker wrote. Open the thread to read it.
            </>
          ) : (
            <>
              This deployment does not serve <code>GET /agent/attention-requests</code>, so the
              waiting rows show without the sentence the worker wrote. Open the thread to read it.
            </>
          )}
        </Alert>
      )}

      <NewItemsPill
        count={feed.stagedCount}
        summary={newItemsSummary(feed.stagedCount, 'record')}
        onShow={feed.flush}
      />

      {records.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {loading ? 'Loading…' : EMPTY_COPY[lens]}
        </Typography>
      ) : (
        <>
          {/* role="log" and not "feed" (doc 21 §4.2): chronological and
              implicitly polite, without the article-navigation keyboard
              contract `feed` would promise. */}
          <SpineRail component="ol" role="log" aria-label="activity" data-testid="activity-rail">
            {feed.visible.map((record) => (
              <ActivityRow
                key={record.id}
                record={record}
                onOpenSession={onOpenSession}
                arrived={feed.arrivals.has(record.id)}
                reduced={reduced}
                revert={
                  record.kind === 'change' && record.entry !== null
                    ? {
                        block: revertBlocks.get(record.entry.id) ?? null,
                        onReverted: () => {
                          setJustReverted(true)
                          void reload()
                        },
                      }
                    : null
                }
                apiOptions={activityOptions}
              />
            ))}
          </SpineRail>
          {lastSeenMs > 0 && <FeedWaterline label={waterlineLabel(lastSeenMs, nowMs)} />}
          <Box sx={{ mt: 2 }}>
            {/* Paging by time, never by row count: four time-ordered sources
                merged by `LIMIT n` have no defined oldest row. */}
            <Link
              component="button"
              type="button"
              variant="caption"
              data-testid="activity-earlier"
              onClick={() =>
                setWindowStartMs((current) => {
                  const from = current > 0 ? current : oldest || nowMs
                  return Math.max(0, from - ACTIVITY_WINDOW_STEP_MS)
                })
              }
            >
              Show earlier
            </Link>
          </Box>
        </>
      )}
    </Box>
  )
}

function ActivityRow({
  record,
  onOpenSession,
  arrived = false,
  reduced = false,
  revert = null,
  apiOptions = {},
}: {
  record: ActivityRecord
  onOpenSession?: (sessionId: string) => void
  arrived?: boolean
  reduced?: boolean
  /** Non-null only for `kind === 'change'` rows — never on an event or job. */
  revert?: { block: RevertBlock | null; onReverted: () => void } | null
  apiOptions?: Record<string, unknown>
}) {
  const tone = toneFor(record)
  return (
    <>
      {/* The quiet stretch is drawn, not compressed away: a quiet night should
          read as a quiet night (§3.6). */}
      {record.gapBeforeLabel !== '' && <SpineGap label={record.gapBeforeLabel} component="li" />}
      <SpineRow
        glyph={record.glyph}
        component="li"
        sx={highlightSx({ active: arrived, tone, reduced })}
        data-testid={`activity-row-${record.kind}`}
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
          {highlightMarker({ active: arrived, tone, reduced }) && (
            <Chip size="small" variant="outlined" label={NEW_MARKER_LABEL} />
          )}
        </Stack>
        {record.detail !== '' && (
          <ClampedText
            text={record.detail}
            color={record.detailIsQuote ? 'text.primary' : 'text.secondary'}
            sx={{ mt: 0.5 }}
          />
        )}
        {record.sessionId !== '' && onOpenSession !== undefined && (
          <Box sx={{ mt: 0.5 }}>
            <Link
              component="button"
              type="button"
              variant="caption"
              onClick={() => onOpenSession(record.sessionId)}
            >
              {record.kind === 'ask' ? 'open thread' : 'open the session'}
            </Link>
          </Box>
        )}
        {revert !== null && record.entry !== null && (
          <RevertControl
            entry={record.entry}
            block={revert.block}
            onReverted={revert.onReverted}
            {...apiOptions}
          />
        )}
      </SpineRow>
    </>
  )
}
