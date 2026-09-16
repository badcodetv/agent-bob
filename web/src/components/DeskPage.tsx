// DeskPage — the landing view (decision K1; design §5.2).
//
// Read top to bottom: *does anything need me* (notes, asks, failures — drawn
// only when there are some), *who is on the team and what is each one for*
// (with a way to chat to any of them), *what just happened*, and *what wakes
// the team*. That order is the 2026-09-16 dashboard redesign (layout A), which
// replaced the "Written down" and "Changes" stacks: the Desk had become too
// busy to read at a glance, and both lists have their own pages (Memory,
// Activity). Everything is read-only except "Chat to …", which the host starts.
//
// The empty Desk is the FIRST-RUN state: a project with no workers is not shown
// "nothing to show", it is shown the two ways in: the org chart the topology
// flow builds, and chat.
//
// One exception to read-only: "Run a cycle now" (RunCycleControl), which fires
// every enabled schedule once after a confirmation that names who will run.
// It exists because a young project's clocks are daily and the Desk is where
// a human watches what a cycle does.

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Alert, Box, Button, Chip, Link, Paper, Stack, Typography } from '@mui/material'
import useDesk, { type UseDeskOptions } from '../useDesk.js'
import {
  DESK_ASKS_CAVEAT,
  type DeskAsk,
  type DeskNotice,
  type DeskFirstRecord,
  type DeskTrouble,
} from '../desk.js'
import type { ActivityRecord } from '../activity.js'
import { formatTimestamp } from '../events.js'
import { SpineGlyph, SpineRail, SpineRow } from '../spine.js'
import { newItemsSummary } from '../watermark.js'
import { highlightSx, highlightMarker, NEW_MARKER_LABEL } from '../feedhighlight.js'
import { buildWakeRules } from '../team.js'
import { ageEscalation, coarseAgeLabel } from '../useElapsedTicker.js'
import usePrefersReducedMotion from '../useReducedMotion.js'
import useStagedFeed from '../useStagedFeed.js'
import useMemories from '../useMemories.js'
import useFirsts from '../useFirsts.js'
import type { FirstToNarrate } from '../firsts.js'
import { buildGuideHash } from '../guide/guideRoute.js'
import { useGuideParagraph } from '../guide/GuideProvider.js'
import { NewItemsPill, PauseLiveUpdates } from './FeedLiveness.js'
import AboutThisScreen from './AboutThisScreen.js'
import BudgetPanel from './BudgetPanel.js'
import ClampedText from './ClampedText.js'
import RunCycleControl from './RunCycleControl.js'
import { RecentActivitySection, TeamSection, WakeSection } from './TeamDashboard.js'

export interface DeskPageProps extends UseDeskOptions {
  /**
   * Show the "Pause live updates" toggle. Default: only when the Desk is
   * actually polling (`refreshMs > 0`) — a switch that pauses nothing is a lie,
   * and WCAG 2.2.2's obligation only exists where content updates on its own.
   */
  showPauseToggle?: boolean
  /** Project id — the high-water mark's key, and the permalink builder's. */
  projectId: string
  /** Open a session thread (typically useSessionPermalink().openSession). */
  onOpenSession?: (sessionId: string) => void
  /** Take the operator to the topology flow — the first-run "org chart" door. */
  onStartFromTopology?: () => void
  /** Take the operator to chat — the other first-run door. */
  onOpenChat?: () => void
  /**
   * True while this project's onboarding interview is unresolved (design §3
   * G1 / PR1): an `onboard` session exists and no charter has been applied
   * yet. While true, the first-run panel offers "Finish setting up this
   * project" instead of the two ordinary doors — the interview is the
   * designed default, not one of several starting points, while it is still
   * running (topology seeds are hidden by the same rule).
   */
  inInterview?: boolean
  /** Opens the onboarding view. Renders the "Finish setting up this project"
   *  row only when this is given. */
  onOpenOnboarding?: () => void
  /** Heading. Pass '' for none. */
  title?: string
  /**
   * Reports how many asks this Desk is showing, whenever that changes.
   *
   * The shell's badge is that number (X7). Before W4 the shell fetched the two
   * lists again through `useAsksCount` while the Desk had them already; now the
   * Desk hands its count up and the shell stands the second reader down. One
   * fetch, and — still the point of X7 — one definition of "an ask".
   */
  onAsksCount?: (count: number) => void
  /**
   * Offer "Run a cycle now" (hurry the clock) once the project has workers.
   * Default true: a young project's schedules are daily, and the Desk is where
   * a human watches what a cycle does.
   */
  showRunCycle?: boolean
  /**
   * Start a new conversation with a worker. Renders a "Chat to <worker>"
   * button on each enabled worker's card only when given. A rejection's
   * message is shown on that card.
   */
  onChatWithWorker?: (worker: string) => Promise<void> | void
  /** Open a worker's own page. Renders "Full prompt" on each card when given. */
  onOpenWorker?: (worker: string) => void
  /** Take the human to Activity. Renders "All activity →" when given. */
  onOpenActivity?: () => void
}

/** Identifiers are mono, content is prose (§3.4). */
const MONO = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }

/** Off-screen but announced — the coarse label beside an `aria-hidden` age. */
const VISUALLY_HIDDEN = {
  position: 'absolute',
  width: 1,
  height: 1,
  overflow: 'hidden',
  clip: 'rect(0 0 0 0)',
  whiteSpace: 'nowrap',
} as const

export default function DeskPage({
  projectId,
  onOpenSession,
  onStartFromTopology,
  onOpenChat,
  inInterview,
  onOpenOnboarding,
  title = 'Desk',
  showPauseToggle,
  onAsksCount,
  showRunCycle = true,
  onChatWithWorker,
  onOpenWorker,
  onOpenActivity,
  ...deskOptions
}: DeskPageProps) {
  const reduced = usePrefersReducedMotion()
  // A reduced-motion operator lands on a paused surface: the setting is about
  // motion, but the operator asking for it is asking not to be chased.
  const [paused, setPaused] = useState(reduced)
  const {
    desk,
    loading,
    error,
    asksHaveMessages,
    asksRouteAvailable,
    workerCount,
    workers,
    schedules,
    subscriptions,
    activity,
    nowMs,
    resolveAttention,
  } = useDesk({
      ...deskOptions,
      projectId,
      paused: deskOptions.paused ?? paused,
    })

  const askCount = desk.asks.length
  useEffect(() => {
    onAsksCount?.(askCount)
  }, [askCount, onAsksCount])

  // "First memory" (design §3 G4) is the one kind `desk.ts`'s fold cannot tag
  // on its own: a memory write is not a config event, so it never appears in
  // `configEvents` at all (`docs/product/17-product-spec.md` §7 — memory is
  // append-only content, not a configuration mutation). A cheap, separate
  // read stands in for it; the newest row's own timestamp is a stand-in for
  // "the project's first memory ever" rather than the true earliest one —
  // exactly right for a NEW project (there is only one row to be newest),
  // approximate for a project already old when this feature first ran on it.
  // It follows the Desk's own poll, so a project's first memory is narrated
  // without a reload.
  const memories = useMemories({ ...deskOptions, limit: 1 })
  const reloadMemories = memories.reload
  const memoryPollPaused = deskOptions.paused ?? paused
  useEffect(() => {
    const every = deskOptions.refreshMs ?? 0
    if (every <= 0 || memoryPollPaused) return
    const timer = setInterval(() => void reloadMemories(), every)
    return () => clearInterval(timer)
  }, [deskOptions.refreshMs, memoryPollPaused, reloadMemories])
  const memoryFirsts: DeskFirstRecord[] = useMemo(() => {
    const newest = memories.memories[0]
    return newest ? [{ kind: 'first-memory' as const, createdAtMs: newest.created_at, id: `memory:${newest.id}` }] : []
  }, [memories.memories])
  const firstRecords = useMemo(
    () => [...desk.firsts, ...memoryFirsts],
    [desk.firsts, memoryFirsts],
  )
  const { toNarrate } = useFirsts({ projectId, records: firstRecords })
  const narrationByRecordId = useMemo(
    () => new Map(toNarrate.map((f) => [f.recordId, f] as const)),
    [toNarrate],
  )
  const memoryNarration = toNarrate.find((f) => f.kind === 'first-memory')
  // Whether a guide page exists to link to at all (§3 G7's "no GuideProvider
  // mounted" degradation) — reusing the Desk's own About-screen signal (C2)
  // rather than inventing a second "is the guide here" mechanism: if this
  // very page's own disclosure paragraph resolved, a `GuideProvider` is
  // mounted and pages exist to link to.
  const guideAvailable = useGuideParagraph('desk') !== undefined

  const polling = (deskOptions.refreshMs ?? 0) > 0
  const showPause = showPauseToggle ?? polling

  // Arrivals stage rather than insert: new asks go behind a pill. Trouble does
  // not, because a failure appearing quietly is the one thing this screen must
  // never do.
  const asksFeed = useStagedFeed(desk.asks, (a) => a.id, { paused })

  const rules = useMemo(() => buildWakeRules(schedules, subscriptions), [schedules, subscriptions])
  // Firsts narrated on a change (first worker, first rewrite, …) now ride the
  // "What happened" row for that change. Activity prefixes the config event's
  // id with `change:`; the Desk's fold does not.
  const firstNarrationFor = useCallback(
    (record: ActivityRecord) =>
      record.kind === 'change' ? narrationByRecordId.get(record.id.replace(/^change:/, '')) : undefined,
    [narrationByRecordId],
  )

  // `error === null` is load-bearing, not defensive: a failed worker list stays
  // the initial `[]`, and without this gate an established project's Desk is
  // replaced wholesale by "start from a topology" (RD28). The banner above says
  // what went wrong; the panel would say something confident and false.
  const firstRun = !loading && error === null && workerCount === 0
  // A project mid-interview already has one worker — the interviewer itself
  // (`go/topology/onboarding.go`'s `renderOnboarding`), so `firstRun` above is
  // false for the WHOLE interview and never fires on its own here. The panel
  // still has to show while `inInterview` is true, gated by the same failed-
  // load protection `firstRun` uses (RD28): a broken fetch must never read as
  // "still in interview".
  const showFirstRunPanel = firstRun || (inInterview === true && !loading && error === null)
  const needsYou = desk.notices.length + desk.asks.length + desk.trouble.length > 0

  return (
    <Box sx={{ p: 3, maxWidth: 1240 }}>
      <Stack direction="row" alignItems="center" justifyContent="space-between" spacing={2} sx={{ mb: 1 }}>
        {title !== '' && <Typography variant="h5">{title}</Typography>}
        <Stack direction="row" spacing={2} alignItems="center">
          {showPause && <PauseLiveUpdates paused={paused} onChange={setPaused} />}
          {showRunCycle && !showFirstRunPanel && workerCount > 0 && (
            <Box data-testid="desk-run-cycle">
              <RunCycleControl apiBaseUrl={deskOptions.apiBaseUrl} getAuthToken={deskOptions.getAuthToken} />
            </Box>
          )}
        </Stack>
      </Stack>

      <AboutThisScreen surface="desk" projectId={projectId} />

      {memoryNarration && (
        <FirstNarrationLine first={memoryNarration} guideAvailable={guideAvailable} sx={{ mb: 2 }} />
      )}

      {error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {showFirstRunPanel ? (
        <FirstRun
          onStartFromTopology={onStartFromTopology}
          onOpenChat={onOpenChat}
          inInterview={inInterview}
          onOpenOnboarding={onOpenOnboarding}
        />
      ) : (
        <Stack spacing={5} sx={{ mt: 2 }}>
          {needsYou && (
            <Stack spacing={4} data-testid="desk-needs-you">
              {desk.notices.length > 0 && (
                <Section
                  label="From the team"
                  count={desk.notices.length}
                  caption="what they did — nothing to answer"
                  empty=""
                >
                  {desk.notices.map((notice) => (
                    <NoticeRow
                      key={notice.id}
                      notice={notice}
                      onOpenSession={onOpenSession}
                      onAcknowledge={() => resolveAttention(notice.requestId)}
                    />
                  ))}
                </Section>
              )}

              {desk.asks.length > 0 && (
                <Section
                  label="Waiting on you"
                  count={desk.asks.length}
                  caption="nobody has answered these"
                  empty=""
                >
                  {!asksHaveMessages && (
                    <Alert severity="info" sx={{ mb: 2 }}>
                      {asksRouteAvailable ? (
                        <>
                          <code>GET /agent/attention-requests</code> did not answer, so these asks are
                          rebuilt from the parked jobs and show without the sentence the worker wrote.
                          Open the thread to read it.
                        </>
                      ) : (
                        <>
                          This deployment does not serve <code>GET /agent/attention-requests</code>, so
                          these asks show without the sentence the worker wrote. Open the thread to read
                          it.
                        </>
                      )}
                    </Alert>
                  )}
                  <NewItemsPill
                    count={asksFeed.stagedCount}
                    summary={newItemsSummary(asksFeed.stagedCount, 'ask')}
                    onShow={asksFeed.flush}
                  />
                  {asksFeed.visible.map((ask) => (
                    <AskRow
                      key={ask.id}
                      ask={ask}
                      onOpenSession={onOpenSession}
                      arrived={asksFeed.arrivals.has(ask.id)}
                      reduced={reduced}
                      firstNarration={narrationByRecordId.get(ask.id)}
                      guideAvailable={guideAvailable}
                      // A stand-in ask rebuilt from a parked delivery has no request
                      // to resolve, so it offers no Dismiss.
                      onDismiss={asksHaveMessages ? () => resolveAttention(ask.requestId) : undefined}
                    />
                  ))}
                  <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
                    {DESK_ASKS_CAVEAT}
                  </Typography>
                </Section>
              )}

              {desk.trouble.length > 0 && (
                <Section label="Trouble" count={desk.trouble.length} caption="" empty="">
                  {desk.trouble.map((item) => (
                    <TroubleRow key={item.id} item={item} onOpenSession={onOpenSession} />
                  ))}
                </Section>
              )}
            </Stack>
          )}

          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: { xs: '1fr', md: 'minmax(0, 1fr) 360px' },
              gap: 5,
              alignItems: 'start',
            }}
          >
            <TeamSection
              workers={workers}
              rules={rules}
              activity={activity}
              nowMs={nowMs}
              onChatWithWorker={onChatWithWorker}
              onOpenWorker={onOpenWorker}
            />
            <RecentActivitySection
              activity={activity}
              nowMs={nowMs}
              onOpenSession={onOpenSession}
              onOpenActivity={onOpenActivity}
              firstNarrationFor={firstNarrationFor}
              renderFirstNarration={(first) => (
                <FirstNarrationLine first={first} guideAvailable={guideAvailable} />
              )}
            />
          </Box>

          <WakeSection rules={rules} onOpenWorker={onOpenWorker} />
        </Stack>
      )}

      <Box sx={{ mt: 5, border: 1, borderColor: 'divider', borderRadius: 1 }}>
        <BudgetPanel
          title="Budget"
          collapsible
          apiBaseUrl={deskOptions.apiBaseUrl}
          getAuthToken={deskOptions.getAuthToken}
        />
      </Box>
    </Box>
  )
}

/** One stack: its name, its count, the line that qualifies it, its rail. */
function Section({
  label,
  count,
  caption,
  empty,
  children,
  after,
}: {
  label: string
  count: number
  caption: string
  empty: string
  children?: ReactNode
  /** Rendered below the stack, inside the same region — the waterline and the
   *  already-read tail beneath it (doc 21 §4.2). */
  after?: ReactNode
}) {
  return (
    <Box component="section" aria-label={label}>
      <Stack direction="row" alignItems="baseline" spacing={1} sx={{ mb: 1.5 }}>
        <Typography variant="subtitle2" sx={{ textTransform: 'uppercase', letterSpacing: '0.08em' }}>
          {label}
        </Typography>
        <Typography variant="subtitle2" sx={MONO}>
          {count}
        </Typography>
        {caption !== '' && (
          <Typography variant="caption" color="text.secondary" sx={{ flex: 1, textAlign: 'right' }}>
            {caption}
          </Typography>
        )}
      </Stack>
      {count === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {empty}
        </Typography>
      ) : (
        // role="log", not role="feed" (§4.2): a chronological list that grows,
        // which is implicitly polite. `feed` would drag in a keyboard contract
        // (article navigation) the Desk does not implement.
        <SpineRail component="ol" role="log" aria-label={label}>
          {children}
        </SpineRail>
      )}
      {after}
    </Box>
  )
}

function AskRow({
  ask,
  onOpenSession,
  arrived = false,
  reduced = false,
  firstNarration,
  guideAvailable = false,
  onDismiss,
}: {
  ask: DeskAsk
  onOpenSession?: (id: string) => void
  arrived?: boolean
  reduced?: boolean
  /** design §3 G4: set only on the project's first ask, once, ever. */
  firstNarration?: FirstToNarrate
  guideAvailable?: boolean
  /** Close the ask without replying — for one dealt with some other way. */
  onDismiss?: () => Promise<void>
}) {
  // §4.2: an ask's age ticks, and escalates — the number is an SLA on the
  // operator, not a progress bar. The escalation is a word AND a colour on top
  // of the number, never instead of it.
  const escalation = ageEscalation(ask.waitingSeconds)
  const ageColor =
    escalation === 'fault' ? 'error.main' : escalation === 'amber' ? 'warning.main' : undefined
  return (
    <SpineRow
      glyph="attention"
      component="li"
      glyphLabel="waiting for you"
      sx={highlightSx({ active: arrived, tone: 'ask', reduced })}
    >
      <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap" useFlexGap>
        {/* The headline carries the ticking age, so it is hidden from screen
            readers; the coarse label beside it says the same thing without
            announcing every second (§4.2). */}
        <Typography variant="body2" sx={MONO} aria-hidden>
          {ask.headline}
        </Typography>
        <Box sx={VISUALLY_HIDDEN}>
          {`${ask.worker === '' ? 'a worker' : ask.worker} · ${ask.status} · waiting ${coarseAgeLabel(ask.waitingSeconds)}`}
        </Box>
        {ageColor !== undefined && (
          <Typography variant="caption" sx={{ color: ageColor }}>
            {escalation === 'fault' ? 'waiting over 4h' : 'waiting over 1h'}
          </Typography>
        )}
        {highlightMarker({ active: arrived, tone: 'ask', reduced }) && (
          <Chip size="small" variant="outlined" label={NEW_MARKER_LABEL} />
        )}
        {ask.expiresLabel !== '' && (
          <Typography variant="caption" color="text.secondary">
            {ask.expiresLabel}
          </Typography>
        )}
      </Stack>
      {ask.message !== '' && <ClampedText text={ask.message} markdown sx={{ mt: 0.5 }} />}
      <Stack direction="row" spacing={2} alignItems="baseline" sx={{ mt: 0.5 }}>
        <ThreadLink
          sessionId={ask.sessionId}
          url={ask.sessionUrl}
          onOpenSession={onOpenSession}
          label="open thread to answer"
        />
        {onDismiss && <ResolveControl label="Dismiss" variant="link" onResolve={onDismiss} />}
      </Stack>
      {firstNarration && <FirstNarrationLine first={firstNarration} guideAvailable={guideAvailable} />}
    </SpineRow>
  )
}

/**
 * A worker telling a person what it did (a `notice`). Not a question, so it
 * says who it is from, shows the words, and offers "Got it" — which is the
 * whole of acknowledging it.
 */
function NoticeRow({
  notice,
  onOpenSession,
  onAcknowledge,
}: {
  notice: DeskNotice
  onOpenSession?: (id: string) => void
  onAcknowledge: () => Promise<void>
}) {
  return (
    <SpineRow glyph="agent" component="li" glyphLabel="a note from the team" data-testid="desk-notice">
      <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap" useFlexGap>
        <Typography variant="body2" sx={MONO}>
          {notice.headline}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {coarseAgeLabel(notice.ageSeconds)} ago
        </Typography>
      </Stack>
      {notice.message !== '' && <ClampedText text={notice.message} markdown sx={{ mt: 0.5 }} />}
      <Stack direction="row" spacing={2} alignItems="center" sx={{ mt: 0.75 }}>
        <ResolveControl label="Got it" variant="button" onResolve={onAcknowledge} />
        <ThreadLink sessionId={notice.sessionId} url={notice.sessionUrl} onOpenSession={onOpenSession} />
      </Stack>
    </SpineRow>
  )
}

/** One acknowledge action: disabled while it posts, and says so if it fails. */
function ResolveControl({
  label,
  variant,
  onResolve,
}: {
  label: string
  variant: 'button' | 'link'
  onResolve: () => Promise<void>
}) {
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const run = () => {
    setBusy(true)
    setFailure(null)
    onResolve()
      .catch((err: unknown) => setFailure(err instanceof Error ? err.message : 'could not save that'))
      .finally(() => setBusy(false))
  }
  return (
    <>
      {variant === 'button' ? (
        <Button size="small" variant="outlined" onClick={run} disabled={busy}>
          {label}
        </Button>
      ) : (
        <Link component="button" type="button" variant="caption" onClick={run} disabled={busy}>
          {label}
        </Link>
      )}
      {failure !== null && (
        <Typography variant="caption" color="error.main" role="alert">
          {failure}
        </Typography>
      )}
    </>
  )
}

/**
 * One sentence (design §3 G4) plus a link to the guide, or just the sentence
 * when no guide is mounted (§3 G7's degradation). Not a checklist, not a
 * badge — one line, once, on the row (or, for `first-memory`, which has no
 * row of its own on the Desk today, on its own).
 */
function FirstNarrationLine({
  first,
  guideAvailable,
  sx,
}: {
  first: FirstToNarrate
  guideAvailable: boolean
  sx?: object
}) {
  return (
    <Typography
      variant="body2"
      sx={{ mt: 0.5, fontWeight: 600, ...sx }}
      data-testid={`first-${first.kind}`}
    >
      {first.narration.sentence}
      {guideAvailable && (
        <>
          {' '}
          <Link href={buildGuideHash(first.narration.slug)} variant="caption">
            Read more in the guide →
          </Link>
        </>
      )}
    </Typography>
  )
}

function TroubleRow({
  item,
  onOpenSession,
}: {
  item: DeskTrouble
  onOpenSession?: (id: string) => void
}) {
  return (
    <SpineRow
      glyph={item.glyph}
      component="li"
      glyphLabel={item.kind === 'freeze-refusal' ? 'a frozen worker refused a rewrite' : 'a failure'}
    >
      <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap" useFlexGap>
        <Typography variant="body2" sx={MONO}>
          {item.headline}
        </Typography>
        {item.sinceSeconds > 0 && (
          <Typography variant="caption" color="text.secondary">
            since {formatTimestamp(item.sinceSeconds)}
          </Typography>
        )}
      </Stack>
      <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
        {item.detail}
      </Typography>
      {item.sessionId !== '' && (
        <Box sx={{ mt: 0.5 }}>
          <ThreadLink
            sessionId={item.sessionId}
            url=""
            onOpenSession={onOpenSession}
            label="open the last job"
          />
        </Box>
      )}
    </SpineRow>
  )
}

/** Open a thread: the host's handler when it has one, else the permalink. */
function ThreadLink({
  sessionId,
  url,
  onOpenSession,
  label = 'open thread',
}: {
  sessionId: string
  url: string
  onOpenSession?: (id: string) => void
  label?: string
}) {
  if (sessionId === '' && url === '') return null
  if (onOpenSession && sessionId !== '') {
    return (
      <Link
        component="button"
        type="button"
        variant="caption"
        onClick={() => onOpenSession(sessionId)}
      >
        {label}
      </Link>
    )
  }
  if (url === '') return null
  return (
    <Link href={url} variant="caption">
      {label}
    </Link>
  )
}

/**
 * The first-run Desk (K1): an empty project is shown what this system is made
 * of and the two doors into it, never a bare "nothing to show".
 */
function FirstRun({
  onStartFromTopology,
  onOpenChat,
  inInterview,
  onOpenOnboarding,
}: {
  onStartFromTopology?: () => void
  onOpenChat?: () => void
  inInterview?: boolean
  onOpenOnboarding?: () => void
}) {
  return (
    <Paper variant="outlined" sx={{ p: 3, maxWidth: 620 }}>
      <Typography variant="subtitle1" sx={{ mb: 0.5 }}>
        {inInterview ? 'This project is being set up' : 'This project has no workers yet'}
      </Typography>
      {inInterview ? (
        <>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            An interview is setting this project up. Answer its questions, then approve the
            charter it writes: a short statement of what the project is for. Approving creates the
            architect, a worker that designs the rest of the team.
          </Typography>
          <Stack direction="row" spacing={1} sx={{ mb: 2 }}>
            <Button
              size="small"
              variant="contained"
              onClick={onOpenOnboarding}
              disabled={!onOpenOnboarding}
              data-testid="finish-onboarding"
            >
              Finish setting up this project
            </Button>
          </Stack>
        </>
      ) : (
        <>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            Once there is a team, the Desk shows each worker and what it is for, with a button to chat
            to it, what just happened, and what wakes them. Start from an org chart, which hires a set of
            workers and wires them to each other in one step, or just talk to the agent.
          </Typography>
          <Stack direction="row" spacing={1} sx={{ mb: 2 }}>
            <Button size="small" variant="contained" onClick={onStartFromTopology} disabled={!onStartFromTopology}>
              Start from an org chart
            </Button>
            <Button size="small" onClick={onOpenChat} disabled={!onOpenChat}>
              Open chat
            </Button>
          </Stack>
        </>
      )}
      <Stack direction="row" spacing={2}>
        <Legend glyph="agent" text="a worker did it" />
        <Legend glyph="human" text="you did it" />
        <Legend glyph="attention" text="waiting for you" />
        <Legend glyph="failure" text="a failure" />
      </Stack>
    </Paper>
  )
}

function Legend({
  glyph,
  text,
}: {
  glyph: 'agent' | 'human' | 'attention' | 'failure'
  text: string
}) {
  return (
    <Stack direction="row" spacing={0.5} alignItems="center">
      <SpineGlyph glyph={glyph} label="" />
      <Typography variant="caption" color="text.secondary">
        {text}
      </Typography>
    </Stack>
  )
}
