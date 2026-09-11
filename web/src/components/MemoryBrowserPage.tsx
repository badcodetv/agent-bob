// MemoryBrowserPage — the memory surface of design §8, over the B2 read route,
// plus the one write control G8 adds (design/2026-09-11-onboarding-and-the-guide.md
// §3 G8, work plan C4): "Write a note".
//
// A SELECTOR BAR, not a search box: the query language is Kubernetes label
// selectors and this screen's job is to teach them. Clauses become chips, an
// invalid clause is named the way the engine's parser names it, and the "there
// is no OR" rule is written down rather than discovered.
//
// Browsing stays read-only, because memory is append-only (§7.1). The `name=`
// convention is folded — current value first, superseded values beneath — for
// the same reason: appending IS updating, and a flat list would show five
// values where the project has one. Writing follows the same rule: there is no
// edit and no delete here, only POST /agent/memories, which appends. "Publish
// a new version" on the label-registry row is that append pre-filled with the
// current value, not a PUT.
//
// The one thing a human writing a note here can rely on: this route stamps
// provenance EMPTY and refuses a body that supplies it (docs/20-datasets.md
// §9), so `memoryWriteBody` never has a place to put one — see its doc comment
// and `memories.test.ts` for the pinned guarantee.
//
// Router-free like its siblings: no URL is written, because the selector is not
// a location. A host that wants it in the address bar owns `selector`/`query`
// through the hook.

import { useMemo, useRef, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  Link,
  Paper,
  Stack,
  TextField,
  Typography,
} from '@mui/material'
import useMemories, { type UseMemoriesOptions } from '../useMemories.js'
import { useConfigApi, type ConfigApiOptions } from '../configApi.js'
import usePrefersReducedMotion from '../useReducedMotion.js'
import { highlightSx, highlightMarker, NEW_MARKER_LABEL } from '../feedhighlight.js'
import {
  buildMemorySelector,
  coerceMemory,
  foldNamedMemories,
  formatLabelLines,
  formatMemoryTimestamp,
  LABEL_REGISTRY_NAME,
  MEMORY_ENDPOINTS,
  MEMORY_SNIPPET_CHARS,
  memoryWriteBody,
  NO_OR_NOTE,
  parseLabelLines,
  parseMemorySelector,
  RRF_NOTE,
  SEMANTIC_OFF_NOTE,
  semanticLegLooksOff,
  type MemoryRow,
  type NamedMemory,
} from '../memories.js'

export interface MemoryBrowserPageProps extends UseMemoriesOptions {
  /** Open a session thread — typically useSessionPermalink().openSession. */
  onOpenSession?: (sessionId: string) => void
  /** Heading. Pass '' for none. */
  title?: string
  /** Override the write route (default MEMORY_ENDPOINTS.list — same path,
   *  POST). */
  createEndpoint?: string
}

/** Identifiers are mono, content is prose (§3.4). */
const MONO = { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }

export default function MemoryBrowserPage({
  onOpenSession,
  title = 'Memory',
  createEndpoint = MEMORY_ENDPOINTS.list,
  ...options
}: MemoryBrowserPageProps) {
  const { memories, selector, query, search, loading, selectorError, error, available, reload } =
    useMemories(options)
  const reduced = usePrefersReducedMotion()

  // The bar is a draft until Search: fetching per keystroke would run a
  // half-typed selector against the route and paint an error for every prefix.
  const [selectorDraft, setSelectorDraft] = useState(selector)
  const [queryDraft, setQueryDraft] = useState(query)

  const parsed = useMemo(() => parseMemorySelector(selectorDraft), [selectorDraft])
  const folded = useMemo(() => foldNamedMemories(memories), [memories])
  const semanticOff = useMemo(() => semanticLegLooksOff(memories, query), [memories, query])

  // The id of the row this screen itself just wrote, so it can be highlighted
  // with the existing feed-highlight mechanism after the reload brings it
  // back. Cleared whenever the human runs a different search — the highlight
  // is about "what I just added to what I'm looking at", not a permanent mark.
  const [justWrittenId, setJustWrittenId] = useState<string | null>(null)

  const runSearch = () => {
    setJustWrittenId(null)
    void search(selectorDraft.trim(), queryDraft.trim())
  }

  const dropClause = (index: number) => {
    const next = buildMemorySelector(parsed.requirements.filter((_r, i) => i !== index))
    setSelectorDraft(next)
    setJustWrittenId(null)
    void search(next, queryDraft.trim())
  }

  // ── Write a note (G8) ─────────────────────────────────────────────────────
  const [writeOpen, setWriteOpen] = useState(false)
  const [writeTitle, setWriteTitle] = useState('Write a note')
  const [writeSubmitLabel, setWriteSubmitLabel] = useState('Write it')
  const [writeInitialLabels, setWriteInitialLabels] = useState('')
  const [writeInitialContent, setWriteInitialContent] = useState('')

  const openWriteNote = () => {
    setWriteTitle('Write a note')
    setWriteSubmitLabel('Write it')
    setWriteInitialLabels('')
    setWriteInitialContent('')
    setWriteOpen(true)
  }

  const openPublishRegistry = (group: NamedMemory) => {
    setWriteTitle('Publish a new version')
    setWriteSubmitLabel('Publish it')
    setWriteInitialLabels(formatLabelLines(group.current.labels))
    setWriteInitialContent(group.current.snippet)
    setWriteOpen(true)
  }

  const handleWritten = async (row: MemoryRow) => {
    setWriteOpen(false)
    await reload()
    setJustWrittenId(row.id)
  }

  return (
    <Box sx={{ p: 3, maxWidth: 900 }}>
      {title !== '' && (
        <Typography variant="h6" sx={{ mb: 2 }}>
          {title}
        </Typography>
      )}

      <Stack direction="row" spacing={1} sx={{ mb: 1 }} alignItems="flex-start">
        <TextField
          size="small"
          fullWidth
          label="Selector"
          placeholder="kind=rolling-summary, worker=email-answerer"
          value={selectorDraft}
          onChange={(e) => setSelectorDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') runSearch()
          }}
          inputProps={{ style: MONO, 'data-testid': 'memory-selector' }}
          error={parsed.error !== null}
          helperText={parsed.error ?? NO_OR_NOTE}
        />
        <TextField
          size="small"
          label="Text"
          placeholder="what was said"
          value={queryDraft}
          onChange={(e) => setQueryDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') runSearch()
          }}
          inputProps={{ 'data-testid': 'memory-query' }}
          sx={{ width: 240 }}
        />
        <Button variant="contained" size="small" sx={{ mt: 0.5 }} onClick={runSearch}>
          Search
        </Button>
        <Button
          variant="outlined"
          size="small"
          sx={{ mt: 0.5 }}
          onClick={openWriteNote}
          data-testid="write-a-note"
        >
          Write a note
        </Button>
      </Stack>

      <WriteNoteDialog
        open={writeOpen}
        title={writeTitle}
        submitLabel={writeSubmitLabel}
        initialLabels={writeInitialLabels}
        initialContent={writeInitialContent}
        createEndpoint={createEndpoint}
        onClose={() => setWriteOpen(false)}
        onWritten={(row) => void handleWritten(row)}
        apiOptions={options}
      />

      {parsed.requirements.length > 0 && (
        <Stack
          direction="row"
          spacing={0.5}
          sx={{ mb: 1, flexWrap: 'wrap', gap: 0.5 }}
          data-testid="selector-chips"
        >
          {parsed.requirements.map((req, i) => (
            <Chip
              key={`${req.key}-${req.op}-${i}`}
              size="small"
              label={buildMemorySelector([req])}
              onDelete={() => dropClause(i)}
              sx={MONO}
            />
          ))}
        </Stack>
      )}

      {selectorError !== null && (
        <Alert severity="warning" sx={{ mb: 2 }} data-testid="memory-selector-error">
          {selectorError}
        </Alert>
      )}

      {!available && (
        <Alert severity="info" sx={{ mb: 2 }}>
          Memory is not available on this host — the browse route is not mounted, or the project
          runs without Postgres. Workers can still write memories only where the store exists.
        </Alert>
      )}
      {available && error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      {query !== '' && (
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
          {RRF_NOTE}
        </Typography>
      )}
      {query !== '' && semanticOff && (
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
          {SEMANTIC_OFF_NOTE}
        </Typography>
      )}

      {loading && (
        <Typography variant="body2" color="text.secondary">
          Loading…
        </Typography>
      )}

      {/* `error === null` is the same gate as the Desk's and the worker list's
          (RD27/RD28): `useMemories` empties the list on failure, so without it
          a failed search claims the project has never remembered anything. */}
      {!loading && available && error === null && selectorError === null && memories.length === 0 && (
        <Typography variant="body2" color="text.secondary">
          {selector === '' && query === ''
            ? 'Nothing has been remembered in this project yet. Memories are written by workers through their tools — write the first one yourself with Write a note, above.'
            : 'No memory matches. There is no OR in a selector: try one clause at a time.'}
        </Typography>
      )}

      {folded.named.map((group) => {
        const arrived = group.current.id === justWrittenId
        return (
          <Paper
            key={group.name}
            variant="outlined"
            sx={[{ p: 2, mb: 1 }, highlightSx({ active: arrived, tone: 'human', reduced })]}
          >
            <Stack direction="row" spacing={1} alignItems="center" sx={{ mb: 0.5 }}>
              <Typography variant="subtitle2" sx={MONO}>
                name={group.name}
              </Typography>
              {highlightMarker({ active: arrived, tone: 'human', reduced }) && (
                <Chip size="small" variant="outlined" label={NEW_MARKER_LABEL} />
              )}
              {group.name === LABEL_REGISTRY_NAME && (
                <Button
                  size="small"
                  onClick={() => openPublishRegistry(group)}
                  data-testid="publish-registry-version"
                >
                  Publish a new version
                </Button>
              )}
            </Stack>
            <MemoryBody row={group.current} onOpenSession={onOpenSession} />
            {group.superseded.length > 0 && (
              <Box sx={{ mt: 1 }}>
                <Divider sx={{ mb: 1 }} />
                <Typography variant="caption" color="text.secondary">
                  {group.superseded.length} superseded{' '}
                  {group.superseded.length === 1 ? 'value' : 'values'} — appending is how this value
                  was updated; nothing was deleted.
                </Typography>
                {group.superseded.map((row) => (
                  <Box key={row.id} sx={{ opacity: 0.6, mt: 1 }}>
                    <MemoryBody row={row} onOpenSession={onOpenSession} />
                  </Box>
                ))}
              </Box>
            )}
          </Paper>
        )
      })}

      {folded.rest.map((row) => {
        const arrived = row.id === justWrittenId
        return (
          <Paper
            key={row.id}
            variant="outlined"
            sx={[{ p: 2, mb: 1 }, highlightSx({ active: arrived, tone: 'human', reduced })]}
            data-testid="memory-row"
          >
            <Stack direction="row" spacing={0.5} sx={{ flexWrap: 'wrap', gap: 0.5, mb: 1 }} alignItems="center">
              {Object.entries(row.labels)
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([k, v]) => (
                  <Chip key={k} size="small" variant="outlined" label={`${k}=${v}`} sx={MONO} />
                ))}
              {highlightMarker({ active: arrived, tone: 'human', reduced }) && (
                <Chip size="small" variant="outlined" label={NEW_MARKER_LABEL} />
              )}
            </Stack>
            <MemoryBody row={row} onOpenSession={onOpenSession} />
          </Paper>
        )
      })}
    </Box>
  )
}

/** One memory's content and its provenance: which worker wrote it, when, and
 *  one click to the thread it was written in (design §8). */
function MemoryBody({
  row,
  onOpenSession,
}: {
  row: MemoryRow
  onOpenSession?: (sessionId: string) => void
}) {
  return (
    <>
      <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
        {row.snippet}
      </Typography>
      <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>
        {row.created_by_worker !== '' ? (
          <Box component="span" sx={MONO}>
            {row.created_by_worker}
          </Box>
        ) : (
          'unattributed'
        )}
        {row.created_at > 0 ? ` · ${formatMemoryTimestamp(row.created_at)}` : ''}
        {row.created_by_session !== '' && onOpenSession ? (
          <>
            {' · '}
            <Link
              component="button"
              variant="caption"
              onClick={() => onOpenSession(row.created_by_session)}
            >
              open the session
            </Link>
          </>
        ) : null}
      </Typography>
    </>
  )
}

// ---------------------------------------------------------------------------
// Write a note (design §3 G8, work plan C4)
// ---------------------------------------------------------------------------

/**
 * The one write on this surface: POST /agent/memories, behind a small form.
 * Used both for "Write a note" (blank) and "Publish a new version" on the
 * label-registry row (pre-filled with its current labels and content) — one
 * dialog, one wire format, exactly the reasoning `EmitEventControl` already
 * established for this package's other single-purpose write.
 *
 * The body is `memoryWriteBody(labels, content)` — labels and content, and
 * nothing else. There is no field here this component could put a provenance
 * value into even if it tried; see that function's doc comment and
 * `memories.test.ts` for the pinned guarantee the ticket asks for.
 *
 * "Why?" is required by this form but is never sent: `POST /agent/memories`
 * takes no rationale, and per docs/20-datasets.md §9 an append writes no
 * config event at all (a memory is data, not configuration — every append
 * appearing in the changelog would bury the project's real configuration
 * history within a week). Requiring it here is a deliberate pause before an
 * append to a shared, permission-less bus, not a field with a home on the
 * wire — see this ticket's Notes for the call and why it was not invented one.
 */
function WriteNoteDialog({
  open,
  title,
  submitLabel,
  initialLabels,
  initialContent,
  createEndpoint,
  onClose,
  onWritten,
  apiOptions,
}: {
  open: boolean
  title: string
  submitLabel: string
  initialLabels: string
  initialContent: string
  createEndpoint: string
  onClose: () => void
  onWritten: (row: MemoryRow) => void
  apiOptions: ConfigApiOptions
}) {
  const { request } = useConfigApi(apiOptions)
  const [content, setContent] = useState(initialContent)
  const [labelsText, setLabelsText] = useState(initialLabels)
  const [why, setWhy] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Re-seed on every open, render-phase and keyed on identity — the same
  // pattern WorkerEditor uses to re-seed when the parent selects a different
  // worker. Keying on `open` (via the NUL sentinel while closed) rather than
  // only on the pre-fill values matters here: "Write a note" always pre-fills
  // with the same two empty strings, so a key that ignored `open` would leave
  // a half-typed note in place the next time the same button is clicked.
  const identity = open ? `${title} ${initialLabels} ${initialContent}` : ' closed'
  const seededFor = useRef<string | null>(null)
  if (seededFor.current !== identity) {
    seededFor.current = identity
    setContent(initialContent)
    setLabelsText(initialLabels)
    setWhy('')
    setError(null)
  }

  const parsedLabels = useMemo(() => parseLabelLines(labelsText), [labelsText])
  const canSubmit =
    !submitting && content.trim() !== '' && why.trim() !== '' && parsedLabels.error === null

  const submit = async () => {
    if (!canSubmit) return
    setSubmitting(true)
    setError(null)
    try {
      const stored = await request<unknown>(createEndpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(memoryWriteBody(parsedLabels.labels, content.trim())),
      })
      onWritten(coerceMemory(stored))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'failed to write the note')
    } finally {
      setSubmitting(false)
    }
  }

  // The pre-fill for "Publish a new version" comes from the search route,
  // which caps a snippet at MEMORY_SNIPPET_CHARS (§7.1) — so a registry longer
  // than that would be pre-filled short here, and publishing without noticing
  // would truncate the rulebook. Named rather than silently risked.
  const mayBeTruncated =
    initialContent.length >= MEMORY_SNIPPET_CHARS && initialContent === content

  return (
    <Dialog open={open} onClose={() => (submitting ? undefined : onClose())} fullWidth maxWidth="sm">
      <DialogTitle>{title}</DialogTitle>
      <DialogContent>
        {mayBeTruncated && (
          <Alert severity="warning" sx={{ mb: 2 }}>
            The current value is at least {MEMORY_SNIPPET_CHARS} characters, the most the browse
            route returns — this pre-fill may be cut short. Check it before publishing, or fetch
            the full memory first.
          </Alert>
        )}
        <TextField
          label="Content"
          fullWidth
          multiline
          minRows={4}
          sx={{ mb: 2, mt: 1 }}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          inputProps={{ 'data-testid': 'note-content' }}
        />
        <TextField
          label="Labels"
          placeholder={'kind=lesson\nworker=email-answerer'}
          fullWidth
          multiline
          minRows={2}
          sx={{ mb: 2 }}
          value={labelsText}
          onChange={(e) => setLabelsText(e.target.value)}
          inputProps={{ style: MONO, 'data-testid': 'note-labels' }}
          error={parsedLabels.error !== null}
          helperText={parsedLabels.error ?? 'One key=value per line.'}
        />
        <TextField
          label="Why?"
          placeholder="Why are you writing this?"
          fullWidth
          sx={{ mb: 1 }}
          value={why}
          onChange={(e) => setWhy(e.target.value)}
          inputProps={{ 'data-testid': 'note-why' }}
        />
        {error !== null && (
          <Alert severity="error" sx={{ mt: 1 }}>
            {error}
          </Alert>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={submitting}>
          Cancel
        </Button>
        <Button onClick={() => void submit()} disabled={!canSubmit} variant="contained">
          {submitting ? 'Writing…' : submitLabel}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
