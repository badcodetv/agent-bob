// BudgetPanel — today's spend against the operator's budget, and the small
// form that changes it (design/2026-09-11-onboarding-work-plan.md A5, §1.1-1.4).
//
// Mounted twice: collapsed-when-under-budget at the top of the Desk, and
// expanded in Settings' top tier ("You may want to change these" — G5). Both
// mounts are the same component; `collapsible` is the only difference.
//
// Three rules the ticket calls out as easy to get wrong, so they are repeated
// here next to the code that has to honour them:
//
//  1. The limits-editing form renders ONLY when `whoami.operator` is true.
//     That check is on the server's own answer to `GET /agent/whoami`
//     (`useWhoami`) — never a client-side guess about who is logged in.
//  2. `cost_known: false` renders "cost not reported", never `$0.00`
//     (`formatCost` in `../usage.js` is where that rule actually lives).
//  3. The hairline bar carries no colour unless `today` is at or over the
//     soft tier (`steel`), and turns `rose` only once a hard stop is
//     genuinely in force — never as a generic "getting close" alarm.

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  LinearProgress,
  Link,
  Stack,
  TextField,
  Typography,
  type Theme,
} from '@mui/material'
import useUsage, { type UseUsageOptions } from '../usage.js'
import useWhoami from '../whoami.js'
import useProjectSettings, { type ProjectSettingsApi } from '../useProjectSettings.js'
import { consoleTokenColor } from '../spine.js'
import { budgetFraction, budgetTier, credentialModeSentence, formatSpend, formatTokens } from '../usage.js'

export interface BudgetPanelProps extends UseUsageOptions {
  /** Heading text. Pass '' to render no heading. */
  title?: string
  /**
   * Collapse to one line while `today` is under the soft tier. The Desk
   * mounts this true (design §1: "above the three stacks, collapsed to one
   * line when under budget"); Settings mounts it false — Settings is the
   * budget's home screen and should never hide it.
   *
   * A tier of 'soft' or 'stopped' always shows the full panel regardless of
   * this prop: the moment there is something to look at is the one moment a
   * collapsed line must not hide it.
   */
  collapsible?: boolean
  /**
   * Share the caller's own `useProjectSettings` instance instead of creating
   * one. `ProjectSettingsPage` mounts this panel INSIDE a page that already
   * holds a settings draft and its own whole-object PUT (the route has no
   * patch semantics — `useProjectSettings.ts`'s own doc comment). A second,
   * independent instance would read the row at its own mount time and PUT
   * its own stale copy back on ANY save from the main form, silently
   * reverting whatever this panel had just written — a spending brake that
   * lies about whether it took is worse than not having one. Pass it there;
   * leave it unset on the Desk, where this panel is the only settings
   * consumer on the page and its own instance is correct.
   */
  settings?: ProjectSettingsApi
}

export default function BudgetPanel({ settings, ...rest }: BudgetPanelProps) {
  if (settings) {
    // A parent supplied the instance, so the parent also owns the reason and
    // the save button. See `ownsSave` on BudgetLimitsForm for why that is not
    // just tidiness.
    return <BudgetPanelBody {...rest} settings={settings} ownsSave={false} />
  }
  return <BudgetPanelOwnSettings {...rest} />
}

/** The Desk's shape: no settings instance was handed in, so this is the only
 *  consumer of one on the page and owning it is correct. */
function BudgetPanelOwnSettings(props: Omit<BudgetPanelProps, 'settings'>) {
  const settings = useProjectSettings(props)
  return <BudgetPanelBody {...props} settings={settings} ownsSave />
}

type BudgetPanelBodyProps = Omit<BudgetPanelProps, 'settings'> & {
  settings: ProjectSettingsApi
  ownsSave: boolean
}

function BudgetPanelBody({
  title = 'Budget',
  collapsible = false,
  settings,
  ownsSave,
  ...options
}: BudgetPanelBodyProps) {
  const { usage, loading: usageLoading, error: usageError, reload: reloadUsage } = useUsage(options)
  const { whoami, loading: whoamiLoading } = useWhoami(options)

  // Refresh the numbers once a save genuinely completes: `settings.saving`
  // flips true→false with no error. Watched rather than fired from the
  // button's own click handler so it works identically whether `settings`
  // is this component's own instance or the page's shared one (a save
  // triggered from OUTSIDE this panel — e.g. the main "Save settings"
  // button on `ProjectSettingsPage`'s Advanced tier — still moves the
  // numbers on screen). `settings.save()` never rejects (it catches its own
  // errors into `settings.error`), so this is the only reliable success
  // signal.
  const wasSaving = useRef(false)
  useEffect(() => {
    if (wasSaving.current && !settings.saving && settings.error === null) {
      void reloadUsage()
    }
    wasSaving.current = settings.saving
  }, [settings.saving, settings.error, reloadUsage])

  const [manuallyExpanded, setManuallyExpanded] = useState(false)

  const todayTokens = usage.today.input_tokens + usage.today.output_tokens
  const tier = budgetTier(todayTokens, usage.budget.daily_tokens_soft, usage.budget.daily_tokens_hard)
  const expanded = !collapsible || manuallyExpanded || tier !== 'ok'

  const loading = usageLoading || whoamiLoading

  if (loading) {
    return (
      <Box sx={{ p: 2, display: 'flex', alignItems: 'center', gap: 1 }}>
        <CircularProgress size={16} aria-label="Loading budget" />
        <Typography variant="body2" color="text.secondary">
          Loading budget…
        </Typography>
      </Box>
    )
  }

  if (!expanded) {
    return (
      <Box sx={{ p: 1.5 }} data-testid="budget-panel-collapsed">
        <Stack direction="row" spacing={1.5} alignItems="baseline">
          <Typography variant="body2">
            Today: {formatSpend(todayTokens, usage.today.cost_usd, usage.cost_known, usage.credential_mode)}
          </Typography>
          <Typography variant="caption" color="text.secondary">
            under budget
          </Typography>
          <Link component="button" type="button" variant="caption" onClick={() => setManuallyExpanded(true)}>
            Details
          </Link>
        </Stack>
      </Box>
    )
  }

  const sentence = credentialModeSentence(usage.credential_mode)

  return (
    <Box sx={{ p: 2 }} data-testid="budget-panel">
      <Stack direction="row" justifyContent="space-between" alignItems="baseline" sx={{ mb: 1 }}>
        {title !== '' && <Typography variant="subtitle2">{title}</Typography>}
        {collapsible && manuallyExpanded && tier === 'ok' && (
          <Link component="button" type="button" variant="caption" onClick={() => setManuallyExpanded(false)}>
            Hide details
          </Link>
        )}
      </Stack>

      {usageError !== null && (
        <Alert severity="error" sx={{ mb: 1.5 }}>
          {usageError}
        </Alert>
      )}

      <BudgetBar tier={tier} todayTokens={todayTokens} hardLimit={usage.budget.daily_tokens_hard} />

      <Stack spacing={0.5} sx={{ mt: 1.5 }}>
        <Typography variant="body2">
          Today: {formatSpend(todayTokens, usage.today.cost_usd, usage.cost_known, usage.credential_mode)}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          Last 7 days:{' '}
          {formatSpend(usage.last_7d.input_tokens + usage.last_7d.output_tokens, usage.last_7d.cost_usd, usage.cost_known, usage.credential_mode)}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          Last 30 days:{' '}
          {formatSpend(usage.last_30d.input_tokens + usage.last_30d.output_tokens, usage.last_30d.cost_usd, usage.cost_known, usage.credential_mode)}
        </Typography>
        {sentence !== '' && (
          <Typography variant="caption" color="text.secondary">
            {sentence}
          </Typography>
        )}
      </Stack>

      <Box sx={{ mt: 2 }}>
        {whoami.operator ? (
          settings.loading ? (
            <CircularProgress size={16} aria-label="Loading limits" />
          ) : (
            <BudgetLimitsForm settings={settings} ownsSave={ownsSave} />
          )
        ) : (
          <Stack spacing={0.5}>
            <Typography variant="body2">
              Soft limit: {usage.budget.daily_tokens_soft > 0 ? `${formatTokens(usage.budget.daily_tokens_soft)} tokens/day` : 'off'}
              {' · '}
              Hard limit: {usage.budget.daily_tokens_hard > 0 ? `${formatTokens(usage.budget.daily_tokens_hard)} tokens/day` : 'off'}
            </Typography>
            <Typography variant="caption" color="text.secondary">
              Only the operator can change this.
            </Typography>
          </Stack>
        )}
      </Box>
    </Box>
  )
}

/** The hairline bar: no colour under the soft tier, `steel` over it, `rose`
 *  once a hard stop is in force. Hidden entirely with no hard limit set —
 *  there is nothing to measure a bar against. */
function BudgetBar({
  tier,
  todayTokens,
  hardLimit,
}: {
  tier: 'ok' | 'soft' | 'stopped'
  todayTokens: number
  hardLimit: number
}) {
  if (hardLimit <= 0) {
    return (
      <Typography variant="caption" color="text.secondary">
        No hard limit set — today has no ceiling.
      </Typography>
    )
  }
  const fraction = budgetFraction(todayTokens, hardLimit)
  return (
    <LinearProgress
      variant="determinate"
      value={fraction * 100}
      aria-label="Today's tokens against the hard limit"
      sx={{
        height: 4,
        borderRadius: 1,
        bgcolor: (theme: Theme) => theme.palette.action.hover,
        '& .MuiLinearProgress-bar': {
          bgcolor: (theme: Theme) => {
            if (tier === 'stopped') return consoleTokenColor(theme, 'rose')
            if (tier === 'soft') return consoleTokenColor(theme, 'steel')
            return theme.palette.text.disabled
          },
        },
      }}
    />
  )
}

/** The operator-only limits form: two numbers plus the reason every settings
 *  edit on this console carries. Takes its `settings` instance from the
 *  caller (own or shared — see `BudgetPanel`'s doc comment on the `settings`
 *  prop) rather than a second PUT wrapper; the route is whole-object, so
 *  editing just these two fields and saving still echoes back the rest of
 *  the row unchanged. `BudgetPanelBody` watches `settings.saving` to reload
 *  the numbers above once a save actually completes — this component only
 *  triggers the save. */
function BudgetLimitsForm({
  settings,
  ownsSave,
}: {
  settings: ProjectSettingsApi
  ownsSave: boolean
}) {
  const soft = settings.draft.daily_tokens_soft
  const hard = settings.draft.daily_tokens_hard

  const onChangeSoft = useCallback(
    (v: number) => settings.update({ daily_tokens_soft: v }),
    [settings],
  )
  const onChangeHard = useCallback(
    (v: number) => settings.update({ daily_tokens_hard: v }),
    [settings],
  )

  const softError = settings.fieldErrors.daily_tokens_soft ?? null
  const hardError = settings.fieldErrors.daily_tokens_hard ?? null

  return (
    <Stack spacing={1.5} data-testid="budget-limits-form">
      {settings.error !== null && <Alert severity="error">{settings.error}</Alert>}
      <Stack direction="row" spacing={2}>
        <TextField
          label="Soft limit"
          type="number"
          size="small"
          value={String(soft)}
          error={softError !== null}
          helperText={softError ?? 'tokens/day, 0 = off'}
          onChange={(e) => {
            const raw = e.target.value.trim()
            onChangeSoft(raw === '' ? 0 : Number(raw))
          }}
          inputProps={{ min: 0, 'aria-label': 'Soft limit' }}
        />
        <TextField
          label="Hard limit"
          type="number"
          size="small"
          value={String(hard)}
          error={hardError !== null}
          helperText={hardError ?? 'tokens/day, 0 = off'}
          onChange={(e) => {
            const raw = e.target.value.trim()
            onChangeHard(raw === '' ? 0 : Number(raw))
          }}
          inputProps={{ min: 0, 'aria-label': 'Hard limit' }}
        />
      </Stack>
      {ownsSave ? (
        <>
          <TextField
            label="Why?"
            size="small"
            value={settings.rationale}
            placeholder="raising the daily hard limit for the launch"
            onChange={(e) => settings.setRationale(e.target.value)}
            inputProps={{ 'aria-label': 'Why?' }}
          />
          <Stack direction="row" spacing={2} alignItems="center">
            <Button
              variant="contained"
              size="small"
              disabled={!settings.canSave || !settings.dirty}
              onClick={() => void settings.save()}
            >
              {settings.saving ? 'Saving…' : 'Save limits'}
            </Button>
            {!settings.dirty && (
              <Typography variant="caption" color="text.secondary">
                No unsaved changes
              </Typography>
            )}
          </Stack>
        </>
      ) : (
        <Typography variant="caption" color="text.secondary">
          Saving these is the page's own “Why?” and Save settings, below — the
          limits above are part of this page's one draft.
        </Typography>
      )}
    </Stack>
  )
}
