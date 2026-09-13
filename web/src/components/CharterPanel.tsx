// CharterPanel — the charter, and the one approval (T15; design §A).
//
// This is the whole of the human gate. Decision A4: the agent proposes, a
// human approves, once — and the gate is for COMPREHENSION, not correctness.
// Nobody is being asked to audit a schema here; they are being asked whether
// this is what their project is for. So the panel renders the four
// substantive parts in the words the interview agreed, and the labelling
// rules IN FULL rather than truncated, because those are the thing actually
// being agreed and a "…" over them would defeat the point of asking.
//
// Router-free and store-free: it takes a charter and an apply function.

import { useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Chip,
  Divider,
  List,
  ListItem,
  ListItemText,
  Paper,
  Stack,
  Typography,
} from '@mui/material'
import {
  describeCharterCadence,
  type CharterCurrent,
  type CharterIssue,
} from '../charter.js'
import type { TopologyApplyResult } from '../topologies.js'

export interface CharterPanelProps {
  /** What the interview deposited, as the server read it back. */
  charter: CharterCurrent
  /** Approve. Resolves to the applied result, or null on failure. */
  onApprove: (rationale?: string) => Promise<TopologyApplyResult | null>
  applying?: boolean
  /** A refusal in the server's own words — a 409 naming the worker that
   *  already exists, above all. Rendered verbatim. */
  applyError?: string | null
  /** A 422's issues, when the charter changed between render and click. */
  applyIssues?: CharterIssue[]
  /** True once it has been approved, so the panel stops offering to again. */
  applied?: boolean
}

/** One labelled block of the charter's prose. */
function Part({ label, value }: { label: string; value: string }) {
  if (value.trim() === '') return null
  return (
    <Box>
      <Typography variant="overline" color="text.secondary" component="div">
        {label}
      </Typography>
      {/* pre-wrap, not a paragraph: the labelling rules are written as lines
          and a model's line breaks are part of what is being agreed. */}
      <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
        {value}
      </Typography>
    </Box>
  )
}

export default function CharterPanel({
  charter,
  onApprove,
  applying = false,
  applyError = null,
  applyIssues = [],
  applied = false,
}: CharterPanelProps) {
  const [busy, setBusy] = useState(false)
  const c = charter.charter
  const issues = applyIssues.length > 0 ? applyIssues : charter.errors
  const effects = charter.summary_of_effects

  const approve = async () => {
    setBusy(true)
    try {
      await onApprove()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Paper variant="outlined" sx={{ p: 2 }} data-testid="charter-panel">
      <Stack spacing={2}>
        <Box>
          <Typography variant="subtitle1" component="h2">
            The charter
          </Typography>
          {charter.summary !== '' && (
            <Typography variant="body2" color="text.secondary">
              {charter.summary}
            </Typography>
          )}
        </Box>

        {c !== null && (
          <Stack spacing={2}>
            <Part label="What this project is for" value={c.goal} />
            <Part label="How we would know it is working" value={c.measure} />
            <Part label="What gets written down, and how it is filed" value={c.label_rules} />
            <Part label="Background every worker carries" value={c.project_background} />
          </Stack>
        )}

        {c !== null && (
          <Box>
            <Typography variant="overline" color="text.secondary" component="div">
              The architect
            </Typography>
            <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: 1 }}>
              <Chip size="small" label={c.architect_name} />
              <Chip size="small" variant="outlined" label={describeCharterCadence(c.architect_cron)} />
              {/* What approving actually starts. This is the one line on the
                  screen that says a loop will change the project on a clock,
                  by itself — and the difference between a human expecting
                  that and being surprised by it. */}
              {effects !== null && (
                <Chip
                  size="small"
                  variant="outlined"
                  color={effects.schedule_enabled ? 'warning' : 'default'}
                  label={
                    effects.schedule_enabled
                      ? 'runs on this schedule from now on'
                      : 'scheduled runs start switched off'
                  }
                />
              )}
            </Stack>
            <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>
              Approving this creates one worker, the architect, and nothing else. The architect is
              an AI worker whose job is to decide what other workers this project needs, and to
              create them itself.
              {effects?.schedule_enabled === true
                ? ' From then on it reviews the project on the schedule above and makes changes without asking. Every change it makes can be reverted from the changelog, and the schedule itself can be switched off on the architect\u2019s Triggers tab.'
                : ' You can run it whenever you like.'}
            </Typography>
          </Box>
        )}

        {c !== null && c.rationale.trim() !== '' && (
          <Box>
            <Divider sx={{ mb: 1 }} />
            <Typography variant="overline" color="text.secondary" component="div">
              Why, in the interviewer's words
            </Typography>
            <Typography variant="body2" color="text.secondary" sx={{ whiteSpace: 'pre-wrap' }}>
              {c.rationale}
            </Typography>
          </Box>
        )}

        {issues.length > 0 && (
          <Alert severity="warning" data-testid="charter-issues">
            <Typography variant="body2" sx={{ mb: 1 }}>
              This charter is not finished yet, so it cannot be approved. The interview needs to
              fix:
            </Typography>
            <List dense disablePadding>
              {issues.map((issue, i) => (
                <ListItem key={`${issue.path}-${i}`} disableGutters sx={{ py: 0 }}>
                  <ListItemText
                    primary={issue.message === '' ? issue.path : issue.message}
                    secondary={issue.message === '' ? undefined : issue.path}
                  />
                </ListItem>
              ))}
            </List>
          </Alert>
        )}

        {applyError !== null && applyError !== '' && (
          <Alert severity="error" data-testid="charter-apply-error">
            {applyError}
          </Alert>
        )}

        {applied ? (
          <Alert severity="success" data-testid="charter-applied">
            Approved. The architect has been created.
          </Alert>
        ) : (
          charter.valid && (
            <Box>
              <Button
                variant="contained"
                onClick={() => void approve()}
                disabled={applying || busy}
                data-testid="charter-approve"
              >
                {applying || busy ? 'Approving…' : 'Approve'}
              </Button>
            </Box>
          )
        )}
      </Stack>
    </Paper>
  )
}
