// ChangelogView — the config log rendered chronologically (§15.10).
//
// "The organisation's changelog, written by construction rather than by
// discipline." So it is rendered as a changelog: newest first, each entry a
// headline (what changed), a commit message (the rationale), the actor, and —
// for prompt rewrites — a read-time diff against the previous state of the same
// key. Every entry deep-links to the session that decided it, which is the
// question the log exists to answer.
//
// The diff is computed here, in the browser, from full-state payloads. §15.2 is
// explicit that this is where diffs belong: the log stores whole states so that
// folding is last-writer-wins with no merge algebra, and the changelog is the
// only place a diff is ever wanted.
//
// The route is mounted: `GET /agent/config-events` (go/httpapi/config_events.go),
// and this view reads it with no host wiring. A host that serves the log from
// somewhere else can still pass `fetchConfigEvents` (see configLog.ts for the
// exact contract) — an override, not a stopgap. If a deployment answers 404/501
// this view says plainly that the log is written but not served there, rather
// than rendering an empty history that reads as "nothing has changed".

import type React from 'react'
import { useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Link,
  MenuItem,
  Paper,
  Stack,
  TextField,
  Tooltip,
  Typography,
  type Theme,
} from '@mui/material'
import { alpha } from '@mui/material/styles'
import useConfigLog, { type UseConfigLogOptions } from '../useConfigLog.js'
import { useConfigApi } from '../configApi.js'
import { SpineRail, SpineRow, consoleTokenColor } from '../spine.js'
import { usePrefersReducedMotion } from '../useReducedMotion.js'
import {
  configRevertPath,
  formatConfigTimestamp,
  revertBlocks,
  type ChangelogEntry,
  type DiffLine,
  type RevertBlock,
} from '../configLog.js'

export interface ChangelogViewProps extends Omit<UseConfigLogOptions, 'query'> {
  /** Heading. Pass '' for none. */
  title?: string
  /** Called when an entry's actor session is clicked; falls back to a link. */
  onOpenSession?: (sessionId: string) => void
  /** Hide the action/actor filter row (a host with its own filters). */
  hideFilters?: boolean
  /**
   * Offer "Revert to this version" on each entry. Default true.
   *
   * A host that mounts this view read-only — an audit screen, an embed — turns
   * it off. The route is guarded server-side regardless; this only decides
   * whether the button is drawn.
   */
  allowRevert?: boolean
}

/** Filter presets: the whole vocabulary, plus the prefix groups §15.9 allows. */
const ACTION_FILTERS: { value: string; label: string }[] = [
  { value: '', label: 'Every change' },
  { value: 'worker_*', label: 'Workers' },
  { value: 'worker_prompt_write', label: 'Prompt rewrites (workers)' },
  { value: 'project_*', label: 'Project prompt & settings' },
  { value: 'subscription_*', label: 'Subscriptions' },
  { value: 'schedule_*', label: 'Schedules' },
  { value: 'image_create', label: 'Images published' },
  { value: 'skill_create', label: 'Skills published' },
  { value: 'topology_apply', label: 'Topologies applied' },
]

export default function ChangelogView({
  title = 'Changelog',
  onOpenSession,
  hideFilters = false,
  allowRevert = true,
  ...options
}: ChangelogViewProps) {
  const [action, setAction] = useState('')
  const [actorWorker, setActorWorker] = useState('')
  const log = useConfigLog({ ...options, query: { action, actorWorker } })
  // Which entries cannot be put back, and why — computed once for the page
  // rather than per card, because the answer for one entry depends on the
  // others (only the newest change to a thing can be reverted).
  const blocks = revertBlocks(log.entries)
  // The "it worked" notice lives HERE, not on the card that was clicked. A
  // successful revert reloads the list, which remounts every card — so a
  // notice held inside one would vanish at exactly the moment it was earned.
  const [justReverted, setJustReverted] = useState(false)

  return (
    <Box sx={{ p: 3, maxWidth: 960 }}>
      {title !== '' && (
        <Typography variant="h6" sx={{ mb: 0.5 }}>
          {title}
        </Typography>
      )}
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mb: 2 }}>
        Every management mutation, newest first. Rationales are the commit messages; prompt
        rewrites are diffed against the previous version of the same prompt.
      </Typography>

      {justReverted && (
        <Alert severity="success" sx={{ mb: 2 }} data-testid="revert-done" onClose={() => setJustReverted(false)}>
          Reverted. The change that put it back is the newest entry below — the entry you reverted
          stays exactly where it is, because nothing here is ever erased.
        </Alert>
      )}

      {!log.available && (
        <Alert severity="info" sx={{ mb: 2 }}>
          The config log is being written, but this deployment does not serve it yet:{' '}
          <code>GET /agent/config-events</code> answered as unmounted here. The route ships
          mounted in <code>agentd</code>; a host that serves the log from somewhere else can
          supply a <code>fetchConfigEvents</code> function instead.
        </Alert>
      )}
      {log.available && log.error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {log.error}
        </Alert>
      )}

      {!hideFilters && (
        <Stack direction="row" spacing={2} sx={{ mb: 2 }}>
          <TextField
            select
            size="small"
            label="Show"
            value={action}
            onChange={(e) => setAction(e.target.value)}
            sx={{ minWidth: 240 }}
          >
            {ACTION_FILTERS.map((f) => (
              <MenuItem key={f.value || 'all'} value={f.value}>
                {f.label}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            size="small"
            label="Made by worker"
            placeholder="any"
            value={actorWorker}
            onChange={(e) => setActorWorker(e.target.value)}
            helperText="Blank includes human and API edits, which log no actor."
          />
        </Stack>
      )}

      {log.loading ? (
        <Box sx={{ p: 3, display: 'flex', justifyContent: 'center' }}>
          <CircularProgress size={24} aria-label="Loading the changelog" />
        </Box>
      ) : log.entries.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          {log.available ? 'No configuration changes recorded.' : 'Nothing to show.'}
        </Typography>
      ) : (
        // The changelog is the spine's third lens (design §2: Desk = today,
        // lineage = one worker, changelog = everything). It carries the same
        // rail and the same closed glyph set, so "a mark means a worker decided
        // it, unmarked means a human did" is true on the screen where history
        // actually lives — not only on the two that had it first.
        <SpineRail component="ol" role="log" aria-label="Configuration changes, newest first">
          {log.entries.map((entry) => (
            <ChangelogEntryCard
              key={entry.id}
              entry={entry}
              onOpenSession={onOpenSession}
              revert={
                allowRevert
                  ? {
                      block: blocks.get(entry.id) ?? null,
                      onReverted: () => {
                        setJustReverted(true)
                        void log.reload()
                      },
                    }
                  : null
              }
              apiOptions={options}
            />
          ))}
        </SpineRail>
      )}
    </Box>
  )
}

function ChangelogEntryCard({
  entry,
  onOpenSession,
  revert,
  apiOptions,
}: {
  entry: ChangelogEntry
  onOpenSession?: (sessionId: string) => void
  /** Null when the host mounted the view read-only. */
  revert: { block: RevertBlock | null; onReverted: () => void } | null
  apiOptions: Record<string, unknown>
}) {
  const [showPayload, setShowPayload] = useState(false)
  // Authorship is the glyph, exactly as on the Desk: the config log already
  // distinguishes the two for free (a worker's write names itself; a human's
  // logs no actor), so the mark costs nothing and answers "who decided this?"
  // before any text is read.
  const byWorker = entry.actorWorker !== ''
  return (
    <SpineRow
      component="li"
      glyph={byWorker ? 'agent' : 'human'}
      glyphLabel={byWorker ? `changed by ${entry.actorWorker}` : 'changed by a human'}
    >
    <Paper variant="outlined" sx={{ p: 2 }}>
      <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap>
        <Typography variant="subtitle2">{entry.title}</Typography>
        <Chip size="small" variant="outlined" label={entry.action} sx={{ fontFamily: 'monospace' }} />
      </Stack>

      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
        {formatConfigTimestamp(entry.createdAt)}
        {' · '}
        {entry.actorWorker ? `by ${entry.actorWorker}` : 'by a human (UI or API)'}
        {entry.sessionPath && (
          <>
            {' · '}
            {onOpenSession && entry.actorSession ? (
              <Link
                component="button"
                type="button"
                variant="caption"
                onClick={() => onOpenSession(entry.actorSession)}
              >
                the session that decided it
              </Link>
            ) : (
              <Link href={entry.sessionPath} variant="caption">
                the session that decided it
              </Link>
            )}
          </>
        )}
      </Typography>

      {entry.rationale !== '' && (
        <Box
          sx={{
            mt: 1.5,
            pl: 1.5,
            borderLeft: 3,
            borderColor: 'divider',
            whiteSpace: 'pre-wrap',
          }}
        >
          <Typography variant="body2">{entry.rationale}</Typography>
        </Box>
      )}

      {entry.diff && (
        <Box sx={{ mt: 1.5 }}>
          <DiffBlock
            lines={entry.diff.lines}
            collapsible
            added={entry.diff.added}
            removed={entry.diff.removed}
            summaryNote="against the previous version"
          />
        </Box>
      )}

      {revert !== null && (
        <RevertControl entry={entry} block={revert.block} onReverted={revert.onReverted} {...apiOptions} />
      )}

      <Box sx={{ mt: 1.5 }}>
        <Link
          component="button"
          type="button"
          variant="caption"
          onClick={() => setShowPayload((v) => !v)}
        >
          {showPayload ? 'Hide full state' : 'Show full state'}
        </Link>
        {showPayload && (
          <Paper
            variant="outlined"
            sx={{
              mt: 1,
              p: 1.5,
              maxHeight: 320,
              overflow: 'auto',
              whiteSpace: 'pre-wrap',
              fontFamily: 'monospace',
              fontSize: 12,
            }}
          >
            {JSON.stringify(entry.event.payload, null, 2)}
          </Paper>
        )}
      </Box>
    </Paper>
    </SpineRow>
  )
}

/**
 * "Revert to this version" — the whole of the human's control over a
 * self-revising organisation (design §C6: there is no mechanical brake on the
 * architect; revert IS the control).
 *
 * Exported so `ActivityPage` — the shell's actual changelog surface, since
 * `ChangelogView` is no longer mounted there — can put the same control on its
 * `changes` rows rather than growing a second copy (design
 * 2026-09-11-onboarding-and-the-guide.md §6 PR0).
 *
 * Three things about it are deliberate.
 *
 * NEVER THE WORD "UNDO". Nothing here is undone: reverting writes a NEW change
 * that puts the old state back, and both changes stay in this log forever. A
 * button labelled "undo" would promise the history could be edited, which is
 * the one thing this log exists to make impossible.
 *
 * THE CONFIRMATION SAYS WHAT WILL CHANGE, by name — not "are you sure?". The
 * reader has been scrolling a list of similar-looking entries, and the cost of
 * clicking the wrong one is a live worker's prompt.
 *
 * THE REASON IS REQUIRED, like every other write in this console (K2). A
 * changelog whose entries say "(no reason given)" is a changelog nobody reads.
 */
export function RevertControl({
  entry,
  block,
  onReverted,
  ...apiOptions
}: {
  entry: ChangelogEntry
  block: RevertBlock | null
  onReverted: () => void
} & Record<string, unknown>) {
  const { request } = useConfigApi(apiOptions)
  const [open, setOpen] = useState(false)
  const [rationale, setRationale] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const what = entry.entity.name === '' ? entry.entity.kind : entry.entity.name

  const submit = async () => {
    if (rationale.trim() === '') return
    setBusy(true)
    setError(null)
    try {
      await request<unknown>(configRevertPath(entry.id), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ rationale: rationale.trim() }),
      })
      setOpen(false)
      onReverted()
    } catch (err) {
      // The server's own sentence. Its refusal names the entries standing in
      // the way, and a paraphrase would drop exactly the part that tells the
      // human what to do next.
      setError(err instanceof Error ? err.message : 'the revert failed')
    } finally {
      setBusy(false)
    }
  }

  const button = (
    <span>
      <Button
        size="small"
        variant="outlined"
        disabled={block !== null}
        data-testid={`revert-${entry.id}`}
        onClick={() => {
          setError(null)
          setRationale('')
          setOpen(true)
        }}
      >
        Revert to this version
      </Button>
    </span>
  )

  return (
    <Box sx={{ mt: 1.5 }}>
      {block === null ? (
        button
      ) : (
        // The reason rides on the disabled control rather than replacing it:
        // "why can I not do this" is the question a greyed-out button asks,
        // and it should be answerable without guessing.
        <Tooltip title={block.reason}>
          <Box component="span" data-testid={`revert-blocked-${entry.id}`}>
            {button}
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
              {block.reason}
            </Typography>
          </Box>
        </Tooltip>
      )}

      {error !== null && (
        <Alert severity="error" sx={{ mt: 1 }} data-testid="revert-error">
          {error}
        </Alert>
      )}

      <Dialog open={open} onClose={() => (busy ? undefined : setOpen(false))} fullWidth maxWidth="sm">
        <DialogTitle>Revert to this version?</DialogTitle>
        <DialogContent>
          <DialogContentText component="div">
            <p>
              This puts <strong>{what}</strong> back to the state it was in{' '}
              <strong>after {entry.title.toLowerCase()}</strong>, made{' '}
              {formatConfigTimestamp(entry.createdAt)}
              {entry.actorWorker !== '' ? ` by ${entry.actorWorker}` : ' by a human'}.
            </p>
            <p>
              Nothing is erased. Reverting writes a <strong>new change</strong> that puts the old
              state back, and both stay in this log — so you can revert the revert.
            </p>
          </DialogContentText>
          <TextField
            fullWidth
            size="small"
            required
            autoFocus
            sx={{ mt: 1 }}
            label="Why?"
            placeholder="the rewrite made the replies worse"
            value={rationale}
            onChange={(e) => setRationale(e.target.value)}
            inputProps={{ 'aria-label': 'Why?' }}
            helperText="Required. Stored with the change, and shown in this list next to who made it."
          />
          {error !== null && (
            <Alert severity="error" sx={{ mt: 1 }}>
              {error}
            </Alert>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setOpen(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="contained"
            onClick={() => void submit()}
            disabled={busy || rationale.trim() === ''}
            data-testid="revert-confirm"
          >
            {busy ? 'Reverting…' : 'Revert it'}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}

/** A diff line's tint: a wash of the named colour, never the raw colour.
 *  The band is the *secondary* cue — the +/− gutter glyph carries the meaning
 *  on its own, so the diff survives greyscale, a still screenshot and a
 *  colour-blind reader (§4.1 rule 4).
 *
 *  The ember/fault values come from `spine.tsx`'s exported token table; this
 *  file used to carry its own copy of them. */
function diffTint(theme: Theme, kind: 'add' | 'del'): { bg: string; fg: string } {
  const dark = theme.palette.mode === 'dark'
  const base = consoleTokenColor(theme, kind === 'add' ? 'ember' : 'fault')
  return { bg: alpha(base, dark ? 0.22 : 0.12), fg: base }
}

/** The first changed line, shortened — the summary's "what kind of change". */
function firstHunk(lines: DiffLine[]): string {
  const line = lines.find((l) => l.type !== 'ctx')
  if (!line) return ''
  const glyph = line.type === 'add' ? '+' : '-'
  const text = line.text.trim()
  return glyph + (text.length > 56 ? `${text.slice(0, 55)}…` : text)
}

export interface DiffBlockProps {
  lines: DiffLine[]
  /** Collapse behind a `+n −m` summary (every feed does; §4.2 disclosure). */
  collapsible?: boolean
  /** Counts for the summary line. Derived from `lines` when omitted. */
  added?: number
  removed?: number
  /** Trailing words on the summary after the counts. */
  summaryNote?: string
  /** Start open (a diff the operator navigated to, not one in a feed). */
  defaultOpen?: boolean
}

/** The diff, rendered the way a diff is read: +/− gutters, monospaced.
 *  Exported because the worker lineage (design §7.1) renders the same diff.
 *
 *  In a feed it is `collapsible`: a `<details>`/`<summary>` — which buys the
 *  whole keyboard and screen-reader contract for free — whose body opens on
 *  the `grid-template-rows: 0fr → 1fr` trick (the child MUST carry
 *  `min-height: 0`, or it never collapses). 200ms; snap under reduced motion. */
export function DiffBlock({
  lines,
  collapsible = false,
  added,
  removed,
  summaryNote = '',
  defaultOpen = false,
}: DiffBlockProps) {
  const reduced = usePrefersReducedMotion()
  const [open, setOpen] = useState(defaultOpen)

  const body = (
    <Paper
      variant="outlined"
      aria-label="Prompt diff"
      sx={{ mt: 0.5, maxHeight: 360, overflow: 'auto', fontFamily: 'monospace', fontSize: 12 }}
    >
      {lines.map((line, i) => (
        <Box
          key={i}
          sx={(theme: Theme) => {
            if (line.type === 'ctx') return { px: 1, whiteSpace: 'pre-wrap', opacity: 0.7 }
            const tint = diffTint(theme, line.type)
            return { px: 1, whiteSpace: 'pre-wrap', bgcolor: tint.bg, color: tint.fg }
          }}
        >
          {line.type === 'add' ? '+' : line.type === 'del' ? '-' : ' '}
          {line.text}
        </Box>
      ))}
    </Paper>
  )

  if (!collapsible) return body

  const plus = added ?? lines.filter((l) => l.type === 'add').length
  const minus = removed ?? lines.filter((l) => l.type === 'del').length
  const hunk = firstHunk(lines)

  return (
    <Box component="details" open={open} sx={{ mt: 0.5 }}>
      <Box
        component="summary"
        onClick={(e: React.MouseEvent) => {
          // Controlled, so the body can animate instead of the browser
          // snapping the element open.
          e.preventDefault()
          setOpen((v) => !v)
        }}
        sx={{
          cursor: 'pointer',
          listStyle: 'none',
          fontSize: 12,
          color: 'text.secondary',
          '&::-webkit-details-marker': { display: 'none' },
        }}
      >
        {/* One text node on purpose: testing-library reads direct text
            children, and this line is asserted verbatim. */}
        {`${open ? '▾' : '▸'} +${plus} −${minus}${summaryNote !== '' ? ` ${summaryNote}` : ''}${
          hunk !== '' ? ` · ${hunk}` : ''
        }`}
      </Box>
      <Box
        sx={{
          display: 'grid',
          gridTemplateRows: open ? '1fr' : '0fr',
          transition: reduced ? 'none' : 'grid-template-rows 200ms ease',
        }}
      >
        {/* min-height:0 is the half of the 0fr→1fr trick everyone forgets. */}
        <Box sx={{ overflow: 'hidden', minHeight: 0 }}>{body}</Box>
      </Box>
    </Box>
  )
}

/** Re-exported for a host building its own filter control. */
export { ACTION_FILTERS }
