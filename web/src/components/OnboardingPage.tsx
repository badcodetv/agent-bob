// OnboardingPage — the first screen of a new project (T16; design §A).
//
// Two halves, side by side: the interview on the left, and the charter it
// deposits on the right, appearing when there is one to show.
//
// Three things here are deliberate and easy to undo by accident.
//
// 1. The rail is bound to the onboarding session EXPLICITLY. `AgentChat` takes
//    all-optional props and falls back to `AgentChatProvider`'s *current*
//    session for each one it is not given, so a bare <AgentChat/> here would
//    render whichever session the shell happened to have selected.
//
// 2. The waiting state NAMES ITS CAUSE. Session creation provisions a
//    container, which is slow by construction; a screen that just sits there
//    reads as a bug and gets clicked again, which is how you end up with two
//    interviews.
//
// 3. "Run the architect now" is an EVENT, never a chat with the architect. A
//    chat receives no briefing at all, so an architect talked to rather than
//    woken has never seen the label registry (B1/C5).

import {
  Alert,
  Box,
  CircularProgress,
  Paper,
  Stack,
  Typography,
} from '@mui/material'
import AgentChat from './AgentChat.js'
import CharterPanel from './CharterPanel.js'
import RunArchitectControl from './RunArchitectControl.js'
import AboutThisScreen from './AboutThisScreen.js'
import useCharter from '../useCharter.js'
import type { ConfigApiOptions } from '../configApi.js'

export interface OnboardingPageProps extends ConfigApiOptions {
  /**
   * The interview session's id. Empty while it is still being created — which
   * is the waiting state, not an error.
   */
  sessionId: string
  /**
   * A session-creation failure, in the server's own words. Surfaced verbatim:
   * "host port pool is exhausted" tells an operator exactly what to do, and
   * "could not start the interview" tells them nothing.
   */
  sessionError?: string | null
  /** Poll interval for the charter read; forwarded to useCharter. */
  refreshMs?: number
  /** Scopes the "About this screen" disclosure's dismissal (C2). The project
   *  being onboarded — not yet in `apiOptions`, since this screen predates a
   *  usable project token for most of its life. */
  projectId?: string
}

/** The waiting rail: a spinner and, more importantly, a reason. */
function StartingUp() {
  return (
    <Stack spacing={2} alignItems="center" justifyContent="center" sx={{ height: '100%', p: 4 }}>
      <CircularProgress />
      <Typography variant="body2" color="text.secondary" align="center" data-testid="onboarding-waiting">
        Starting a container for the interview. This can take a minute the first time — nothing is
        stuck, and you do not need to reload.
      </Typography>
    </Stack>
  )
}

export default function OnboardingPage({
  sessionId,
  sessionError = null,
  refreshMs,
  projectId = '',
  ...apiOptions
}: OnboardingPageProps) {
  const charter = useCharter({ ...apiOptions, session: sessionId, refreshMs })
  const current = charter.charter

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: { xs: 'column', md: 'row' },
        gap: 2,
        // Full height of a flex column, never a pixel height: the rail has to
        // grow with the window or the conversation scrolls inside a stub.
        height: '100%',
        minHeight: 0,
        p: 2,
      }}
    >
      <Paper
        variant="outlined"
        sx={{
          // clamp, so the rail stays readable on a laptop and does not eat a
          // wide screen.
          width: { xs: '100%', md: 'clamp(360px, 32vw, 620px)' },
          flexShrink: 0,
          display: 'flex',
          flexDirection: 'column',
          minHeight: 0,
          overflow: 'hidden',
        }}
        data-testid="onboarding-rail"
      >
        {sessionError !== null && sessionError !== '' ? (
          <Alert severity="error" sx={{ m: 2 }} data-testid="onboarding-session-error">
            {sessionError}
          </Alert>
        ) : sessionId === '' ? (
          <StartingUp />
        ) : (
          // Explicitly bound. See note 1 at the top of this file.
          <AgentChat sessionId={sessionId} {...apiOptions} />
        )}
      </Paper>

      <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, overflowY: 'auto' }}>
        <Stack spacing={2}>
          <Box>
            <Typography variant="h6" component="h1">
              Setting up this project
            </Typography>
            <Typography variant="body2" color="text.secondary">
              Answer the questions on the left. When there is enough to go on, a charter appears
              here — what this project is for, how you would know it is working, and what gets
              written down. Nothing exists until you approve it.
            </Typography>
          </Box>

          <AboutThisScreen surface="onboarding" projectId={projectId} />

          {charter.error !== null && (
            <Alert severity="error" data-testid="onboarding-charter-error">
              {charter.error}
            </Alert>
          )}

          {current === null ? (
            <Alert severity="info" data-testid="onboarding-no-charter">
              No charter yet. It appears here as soon as the interview has written one — you do not
              need to reload.
            </Alert>
          ) : (
            <CharterPanel
              charter={current}
              onApprove={charter.apply}
              applying={charter.applying}
              applyError={charter.applyError}
              applyIssues={charter.applyIssues}
              applied={charter.applied}
            />
          )}

          {charter.applied && (
            <Paper variant="outlined" sx={{ p: 2 }} data-testid="onboarding-next">
              <Typography variant="subtitle2" gutterBottom>
                What happens next
              </Typography>
              <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                The architect exists but has not run yet. It will run on its own schedule from now
                on — run it once now and watch what it does, so the first time it changes something
                is not while you are looking the other way.
              </Typography>
              <RunArchitectControl
                {...apiOptions}
                primary
                architectName={current?.charter?.architect_name ?? ''}
              />
            </Paper>
          )}
        </Stack>
      </Box>
    </Box>
  )
}
