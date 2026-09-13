// DeskPage — the landing view (decision K1; design §5.2).
//
// Three stacks, in the order the morning is actually read: *does anything want
// me? what changed? what broke?* Everything on the page is read-only and every
// item's action is to open the thing it names — this is a query, not an
// approval queue, and it holds no state of its own beyond one `localStorage`
// high-water mark for "since you last looked".
//
// The empty Desk is the FIRST-RUN state: a project with no workers is not shown
// "nothing to show", it is shown the two ways in: the org chart the topology
// flow builds, and chat.
//
// One exception to read-only: "Run a cycle now" (RunCycleControl), which fires
// every enabled schedule once after a confirmation that names who will run.
// It exists because a young project's clocks are daily and the Desk is where
// a human watches what a cycle does.

import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Alert, Box, Button, Chip, Link, Paper, Stack, Typography } from '@mui/material'
import useDesk, { type UseDeskOptions } from '../useDesk.js'
import {
  DESK_ASKS_CAVEAT,
  type DeskAsk,
  type DeskNotice,
  deskNotes,
  type DeskChange,
  type DeskFirstRecord,
  type DeskNote,
  type DeskTrouble,
} from '../desk.js'
import { formatTimestamp } from '../events.js'
import { formatConfigTimestamp } from '../configLog.js'
import { SpineGlyph, SpineRail, SpineRow } from '../spine.js'
import { newItemsSummary, waterlineLabel } from '../watermark.js'
import {
  highlightSx,
  highlightMarker,
  NEW_MARKER_LABEL,
  type HighlightTone,
} from '../feedhighlight.js'
import { ageEscalation, coarseAgeLabel } from '../useElapsedTicker.js'
import usePrefersReducedMotion from '../useReducedMotion.js'
import useStagedFeed from '../useStagedFeed.js'
import useMemories from '../useMemories.js'
import { formatMemoryTimestamp } from '../memories.js'
import useFirsts from '../useFirsts.js'
import type { FirstToNarrate } from '../firsts.js'
import { buildGuideHash } from '../guide/guideRoute.js'
import { useGuideParagraph } from '../guide/GuideProvider.js'
import { FeedWaterline, NewItemsPill, PauseLiveUpdates } from './FeedLiveness.js'
import AboutThisScreen from './AboutThisScreen.js'
import BudgetPanel from './BudgetPanel.js'
import ClampedText from './ClampedText.js'
import RunCycleControl from './RunCycleControl.js'

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
  /** Take the human to the memory browser. Renders "See everything written
   *  down" under the Written down stack only when given. */
  onOpenMemory?: () => void
}

/** Identifiers are mono, content is prose (§3.4). */
/** How many of the newest memories the Written down stack shows. */
const DESK_NOTES_LIMIT = 5

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
  onOpenMemory,
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
    lastSeenMs,
    markSeen,
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
  // The same read feeds "Written down", so it asks for a few rows and follows
  // the Desk's own poll: a conclusion landing should not need a reload.
  const memories = useMemories({ ...deskOptions, limit: DESK_NOTES_LIMIT })
  const reloadMemories = memories.reload
  const memoryPollPaused = deskOptions.paused ?? paused
  useEffect(() => {
    const every = deskOptions.refreshMs ?? 0
    if (every <= 0 || memoryPollPaused) return
    const timer = setInterval(() => void reloadMemories(), every)
    return () => clearInterval(timer)
  }, [deskOptions.refreshMs, memoryPollPaused, reloadMemories])
  const notes = useMemo(() => deskNotes(memories.memories, DESK_NOTES_LIMIT), [memories.memories])
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

  // Arrivals stage rather than insert. Both growing stacks go behind a pill;
  // Trouble does not, because a failure appearing quietly is the one thing this
  // screen must never do.
  const changesFeed = useStagedFeed(desk.changes, (c) => c.id, { paused })
  const asksFeed = useStagedFeed(desk.asks, (a) => a.id, { paused })

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
  // Same gate, same reason: "the fleet ran and nobody needed you" is a claim
  // about the fleet, and three empty lists from three failed fetches are not
  // evidence for it.
  const nothingAtAll =
    !loading &&
    error === null &&
    desk.asks.length === 0 &&
    desk.notices.length === 0 &&
    desk.changes.length === 0 &&
    desk.trouble.length === 0

  return (
    <Box sx={{ p: 3, maxWidth: 900 }}>
      <Stack direction="row" alignItems="baseline" justifyContent="space-between" sx={{ mb: 2 }}>
        {title !== '' && <Typography variant="h6">{title}</Typography>}
        <Stack direction="row" spacing={2} alignItems="center">
          {showPause && <PauseLiveUpdates paused={paused} onChange={setPaused} />}
          {desk.changes.length > 0 && (
            <Link component="button" type="button" variant="caption" onClick={markSeen}>
              Mark these changes as seen
            </Link>
          )}
        </Stack>
      </Stack>

      <AboutThisScreen surface="desk" projectId={projectId} />

      {showRunCycle && !showFirstRunPanel && workerCount > 0 && (
        <Box sx={{ mb: 2 }} data-testid="desk-run-cycle">
          <RunCycleControl apiBaseUrl={deskOptions.apiBaseUrl} getAuthToken={deskOptions.getAuthToken} />
        </Box>
      )}

      {memoryNarration && (
        <FirstNarrationLine first={memoryNarration} guideAvailable={guideAvailable} sx={{ mb: 2 }} />
      )}

      {error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      <Box sx={{ mb: 3, border: 1, borderColor: 'divider', borderRadius: 1 }}>
        <BudgetPanel
          title="Budget"
          collapsible
          apiBaseUrl={deskOptions.apiBaseUrl}
          getAuthToken={deskOptions.getAuthToken}
        />
      </Box>

      {showFirstRunPanel ? (
        <FirstRun
          onStartFromTopology={onStartFromTopology}
          onOpenChat={onOpenChat}
          inInterview={inInterview}
          onOpenOnboarding={onOpenOnboarding}
        />
      ) : (
        <Stack spacing={4}>
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

          <Section
            label="Asks"
            count={desk.asks.length}
            caption="nobody has answered these"
            empty="Nothing is waiting on you."
          >
            {!asksHaveMessages && desk.asks.length > 0 && (
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
            {desk.asks.length > 0 && (
              <Typography variant="caption" color="text.secondary" sx={{ display: 'block' }}>
                {DESK_ASKS_CAVEAT}
              </Typography>
            )}
          </Section>

          <Section
            label="Written down"
            count={notes.length}
            caption="the newest things the team concluded"
            empty="Nothing has been written down yet. Workers write what they find and decide here as they finish."
            after={
              onOpenMemory && notes.length > 0 ? (
                <Link component="button" type="button" variant="caption" onClick={onOpenMemory} sx={{ mt: 1 }}>
                  See everything written down
                </Link>
              ) : undefined
            }
          >
            {notes.map((note) => (
              <NoteRow key={note.id} note={note} onOpenSession={onOpenSession} />
            ))}
          </Section>

          <Section
            label="Changes"
            count={desk.changes.length}
            after={
              desk.earlierChanges.length > 0 ? (
                // The waterline and the few changes the operator had already
                // read. Outside the stack's own count deliberately: "Nothing
                // has changed since you last looked" is still the honest line
                // when nothing has, and these sit under it as context.
                <>
                  <FeedWaterline label={waterlineLabel(lastSeenMs, nowMs)} />
                  <SpineRail component="ol">
                    {desk.earlierChanges.map((change) => (
                      <ChangeRow
                        key={change.id}
                        change={change}
                        onOpenSession={onOpenSession}
                        firstNarration={narrationByRecordId.get(change.id)}
                        guideAvailable={guideAvailable}
                      />
                    ))}
                  </SpineRail>
                </>
              ) : undefined
            }
            caption={lastSeenMs === 0 ? 'everything recorded so far' : 'since you last looked'}
            empty={
              lastSeenMs === 0
                ? 'No configuration changes recorded.'
                : 'Nothing has changed since you last looked.'
            }
          >
            <NewItemsPill
              count={changesFeed.stagedCount}
              summary={newItemsSummary(changesFeed.stagedCount, 'change')}
              onShow={changesFeed.flush}
            />
            {changesFeed.visible.map((change) => (
              <ChangeRow
                key={change.id}
                change={change}
                onOpenSession={onOpenSession}
                arrived={changesFeed.arrivals.has(change.id)}
                reduced={reduced}
                firstNarration={narrationByRecordId.get(change.id)}
                guideAvailable={guideAvailable}
              />
            ))}
          </Section>

          <Section
            label="Trouble"
            count={desk.trouble.length}
            caption=""
            empty="Nothing has failed."
          >
            {desk.trouble.map((item) => (
              <TroubleRow key={item.id} item={item} onOpenSession={onOpenSession} />
            ))}
          </Section>

          {nothingAtAll && (
            <Typography variant="body2" color="text.secondary">
              A quiet Desk means the fleet ran and nobody needed you — Activity has the jobs it
              ran, and each worker's Triggers tab has what will wake it next.
            </Typography>
          )}
        </Stack>
      )}
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

function NoteRow({
  note,
  onOpenSession,
}: {
  note: DeskNote
  onOpenSession?: (id: string) => void
}) {
  return (
    <SpineRow glyph="agent" component="li" glyphLabel="written down" data-testid="desk-note">
      <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap" useFlexGap>
        <Typography variant="body2" sx={MONO}>
          {note.title}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {note.writer} · {formatMemoryTimestamp(note.createdAtMs)}
        </Typography>
      </Stack>
      {note.excerpt !== '' && <ClampedText text={note.excerpt} markdown maxLines={4} maxChars={320} sx={{ mt: 0.5 }} />}
      {note.sessionId !== '' && (
        <Box sx={{ mt: 0.5 }}>
          <ThreadLink
            sessionId={note.sessionId}
            url=""
            onOpenSession={onOpenSession}
            label="open the session that wrote it"
          />
        </Box>
      )}
    </SpineRow>
  )
}

function ChangeRow({
  change,
  onOpenSession,
  arrived = false,
  reduced = false,
  firstNarration,
  guideAvailable = false,
}: {
  change: DeskChange
  onOpenSession?: (id: string) => void
  arrived?: boolean
  reduced?: boolean
  /** design §3 G4: set only on the project's first record of that kind. */
  firstNarration?: FirstToNarrate
  guideAvailable?: boolean
}) {
  // Authorship decides the tint, exactly as it decides the glyph (§3.2).
  const tone: HighlightTone = change.byAgent ? 'agent' : 'human'
  return (
    <SpineRow
      glyph={change.glyph}
      component="li"
      glyphLabel={change.byAgent ? 'a worker did this' : 'you did this'}
      sx={highlightSx({ active: arrived, tone, reduced })}
    >
      <Stack direction="row" spacing={1} alignItems="baseline" flexWrap="wrap" useFlexGap>
        <Typography variant="body2" sx={MONO}>
          {change.sentence}
        </Typography>
        <Typography variant="caption" color="text.secondary">
          {formatConfigTimestamp(change.createdAt)}
        </Typography>
        {change.diffLabel !== '' && (
          <Typography variant="caption" sx={MONO} color="text.secondary">
            {change.diffLabel}
          </Typography>
        )}
        {highlightMarker({ active: arrived, tone, reduced }) && (
          <Chip size="small" variant="outlined" label={NEW_MARKER_LABEL} />
        )}
      </Stack>
      <ClampedText
        text={change.reason}
        maxLines={4}
        maxChars={320}
        color={change.noReason ? 'text.disabled' : 'text.primary'}
        sx={{ mt: 0.5 }}
      />
      {change.entry.actorSession !== '' && (
        <Box sx={{ mt: 0.5 }}>
          <ThreadLink
            sessionId={change.entry.actorSession}
            url={change.entry.sessionPath ?? ''}
            onOpenSession={onOpenSession}
            label="open the session that decided it"
          />
        </Box>
      )}
      {firstNarration && <FirstNarrationLine first={firstNarration} guideAvailable={guideAvailable} />}
    </SpineRow>
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
            The Desk answers three questions every morning — what wants you, what changed, what broke.
            It stays quiet until something is running. Start from an org chart, which hires a set of
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
