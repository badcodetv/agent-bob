// GitProjectionPanel — what the git projection is doing, on the project
// settings page (design/2026-09-09-git-projection.md, ticket G16).
//
// The projection publishes a project's configuration to a git repository in the
// background. Its failure modes are all silent — agents keep working, the
// console keeps working, and the repository quietly stops matching reality — so
// this panel exists to make the silence audible. Three things shape it:
//
//  1. **Off is a state, not a fault.** A project with no repository renders in
//     the same calm grey as everything else on the page. Painting it as a
//     warning would teach an operator to scroll past the one row that matters.
//  2. **A failure gets a sentence, not a chip.** "push_failing" means nothing
//     to a human at 9am. What they need is: commits are piling up locally, git
//     is going stale, and here is the credential to check. The prose lives in
//     gitProjection.ts, which is where it is tested.
//  3. **Nothing here ever shows a secret.** The unrenderable case names the
//     FIELD and never the value; the engine's error does not carry one, and
//     this component does not go looking for it in `attention_channel` either.
//     GitProjectionPanel.test.tsx asserts that.
//
// Presentational and store-free: it takes a status and renders it. The fetch
// belongs to whoever mounts it (ProjectSettingsPage).

import {
  Alert,
  AlertTitle,
  Box,
  Button,
  Chip,
  CircularProgress,
  Divider,
  Link,
  List,
  ListItem,
  ListItemText,
  Paper,
  Stack,
  Typography,
} from '@mui/material'
import {
  describeGitProjection,
  shortSha,
  GIT_PROJECTION_IGNORED_EXPLANATION,
  type GitProjectionNote,
  type GitProjectionSeverity,
  type GitProjectionStatus,
} from '../gitProjection.js'

export interface GitProjectionPanelProps {
  /** The status as the server reported it, or null while it is unknown. */
  status: GitProjectionStatus | null
  loading?: boolean
  /** A load failure, in the server's own words. */
  error?: string | null
  /** Offered as a "Check again" button when supplied. */
  onRefresh?: () => void
}

/** MUI severity for the four states that get an Alert. `off` and `ok` do not. */
const ALERT_SEVERITY: Record<GitProjectionSeverity, 'success' | 'info' | 'warning' | 'error'> = {
  off: 'info',
  ok: 'success',
  info: 'info',
  warning: 'warning',
  error: 'error',
}

export default function GitProjectionPanel({
  status,
  loading = false,
  error = null,
  onRefresh,
}: GitProjectionPanelProps) {
  return (
    <Box data-testid="git-projection-panel">
      <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
        Published to git
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        Bob renders this project&rsquo;s configuration into a repository as markdown, and imports
        what a human pushes back. Git is the <strong>published record</strong>, never the write
        path: the database stays authoritative, so nothing here can be lost to a merge.
      </Typography>

      {loading && status === null && (
        <Box sx={{ py: 2, display: 'flex', justifyContent: 'center' }}>
          <CircularProgress size={20} aria-label="Loading git projection status" />
        </Box>
      )}

      {error !== null && (
        <Alert severity="warning" sx={{ mb: 2 }}>
          <AlertTitle>Could not read the projection&rsquo;s status</AlertTitle>
          {error}
        </Alert>
      )}

      {status !== null && <Body status={status} onRefresh={onRefresh} loading={loading} />}
    </Box>
  )
}

function Body({
  status,
  loading,
  onRefresh,
}: {
  status: GitProjectionStatus
  loading: boolean
  onRefresh?: () => void
}) {
  const summary = describeGitProjection(status)

  return (
    <Stack spacing={2}>
      <Alert
        severity={ALERT_SEVERITY[summary.severity]}
        icon={summary.severity === 'off' ? false : undefined}
        data-testid="git-projection-health"
        data-health={status.health}
        action={
          onRefresh !== undefined ? (
            <Button size="small" disabled={loading} onClick={onRefresh}>
              Check again
            </Button>
          ) : undefined
        }
      >
        <AlertTitle>{summary.headline}</AlertTitle>
        <Typography variant="body2" component="div">
          {summary.detail}
        </Typography>
        {summary.action !== '' && (
          <Typography variant="body2" component="div" sx={{ mt: 1, fontWeight: 600 }}>
            {summary.action}
          </Typography>
        )}
      </Alert>

      {status.enabled && <Where status={status} />}
      {status.enabled && status.state_available && <Watermarks status={status} />}

      {status.quarantine.length > 0 && (
        <NoteList
          testId="git-projection-quarantine"
          title="Rejected on import"
          explanation={
            'Each of these files failed to parse, and a push containing one is refused in full — ' +
            'nothing in it was applied.'
          }
          notes={status.quarantine}
        />
      )}

      {status.ignored.length > 0 && (
        <NoteList
          testId="git-projection-ignored"
          title="Edits that had no effect"
          explanation={GIT_PROJECTION_IGNORED_EXPLANATION}
          notes={status.ignored}
        />
      )}
    </Stack>
  )
}

/** Where it publishes: the repo link, the branch, and the folder Bob owns. */
function Where({ status }: { status: GitProjectionStatus }) {
  return (
    <Box data-testid="git-projection-where">
      <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: 1, alignItems: 'center' }}>
        {status.browse_url !== '' ? (
          <Link href={status.browse_url} target="_blank" rel="noopener noreferrer" variant="body2">
            {status.remote}
          </Link>
        ) : (
          <Typography variant="body2" component="span">
            {status.remote}
          </Typography>
        )}
        <Chip size="small" variant="outlined" label={`branch ${status.branch}`} />
        <Chip size="small" variant="outlined" label={`${status.subfolder}/`} />
        {/* The NAME of the variable holding the push token. Never the token —
            and the engine refuses to render a literal anywhere it could be
            one, which is the `unrenderable` state above. */}
        {status.token_env !== '' && (
          <Chip size="small" variant="outlined" label={`token from $${status.token_env}`} />
        )}
      </Stack>
    </Box>
  )
}

/** Rendered / pushed / imported, as three plain facts. `seq` is the config-log
 *  sequence, which is the projection's only authority on order — git's dates
 *  are not consulted anywhere, and are not shown here either. */
function Watermarks({ status }: { status: GitProjectionStatus }) {
  const rows: Array<[string, string]> = [
    [
      'Last rendered',
      status.last_rendered_sha === ''
        ? 'nothing rendered yet'
        : `configuration change #${status.last_rendered_seq}, as ${shortSha(status.last_rendered_sha)}`,
    ],
    [
      'Last pushed',
      status.last_pushed_sha === '' ? 'never pushed' : shortSha(status.last_pushed_sha),
    ],
    [
      'Last imported from git',
      status.last_imported_sha === ''
        ? 'nothing imported yet'
        : shortSha(status.last_imported_sha),
    ],
  ]

  return (
    <Box data-testid="git-projection-watermarks">
      <Divider sx={{ mb: 1.5 }} />
      <Stack spacing={0.5}>
        {rows.map(([label, value]) => (
          <Stack key={label} direction="row" spacing={1}>
            <Typography variant="caption" color="text.secondary" sx={{ minWidth: 170 }}>
              {label}
            </Typography>
            <Typography variant="caption" sx={{ fontFamily: 'monospace' }}>
              {value}
            </Typography>
          </Stack>
        ))}
      </Stack>
    </Box>
  )
}

function NoteList({
  testId,
  title,
  explanation,
  notes,
}: {
  testId: string
  title: string
  explanation: string
  notes: GitProjectionNote[]
}) {
  return (
    <Paper variant="outlined" sx={{ p: 2 }} data-testid={testId}>
      <Typography variant="subtitle2">{title}</Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
        {explanation}
      </Typography>
      <List dense disablePadding>
        {notes.map((note, i) => (
          <ListItem key={`${note.path}-${i}`} disableGutters>
            <ListItemText
              primary={note.path}
              secondary={note.reason}
              primaryTypographyProps={{ sx: { fontFamily: 'monospace' }, variant: 'body2' }}
              secondaryTypographyProps={{ variant: 'caption' }}
            />
          </ListItem>
        ))}
      </List>
    </Paper>
  )
}
