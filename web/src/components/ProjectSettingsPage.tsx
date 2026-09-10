// ProjectSettingsPage — the whole of `project_settings` on one screen
// (spec docs/product/01-session-config.md §5; work-plan B3).
//
// Base image, the project system prompt, the two JSON editors, and the five
// numeric budget/cap settings. Save is whole-object (the route has no patch
// semantics) and is blocked while any JSON editor is unparsable — the error
// appears under the offending box, not in a toast that has scrolled away.
//
// The numeric fields render their "0 means…" sentence live, from
// PROJECT_SETTING_NUMERICS, because the difference between "0 = off" and
// "0 = use the default" is the one thing about this screen a human can get
// expensively wrong.
//
// Router-free by construction: it renders where the host puts it and owns no
// URL. Mount it inside <AgentChatProvider> to inherit apiBaseUrl + auth, or
// pass them as props to use it standalone.

import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Divider,
  FormHelperText,
  IconButton,
  Stack,
  TextField,
  Typography,
} from '@mui/material'
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline'
import { useCallback, useRef, useState } from 'react'
import useProjectSettings, { type UseProjectSettingsOptions } from '../useProjectSettings.js'
import { looksUnwired, useConfigApi } from '../configApi.js'
import {
  coerceGitProjectionStatus,
  GIT_PROJECTION_ENDPOINT,
  type GitProjectionStatus,
} from '../gitProjection.js'
import GitProjectionPanel from './GitProjectionPanel.js'
import {
  describeNumericSetting,
  PROJECT_SETTING_NUMERICS,
  validateProjectBriefing,
  type NumericSettingSpec,
  type ProjectSettings,
} from '../projectSettings.js'
import JsonObjectEditor from './JsonObjectEditor.js'

export interface ProjectSettingsPageProps extends UseProjectSettingsOptions {
  /** Heading text. Pass '' to render no heading (host supplies its own). */
  title?: string
}

export default function ProjectSettingsPage({
  title = 'Project settings',
  ...options
}: ProjectSettingsPageProps) {
  // Re-read the projection after a save: turning a repository on (or off) is a
  // settings edit, and a panel still saying "not published" underneath the
  // field that just enabled it is the kind of small lie that makes an operator
  // distrust the whole screen. The indirection through a ref is so the settings
  // load still goes first — this page's contract is that it GETs its own row
  // before anything else.
  const refreshProjection = useRef<() => void>(() => {})
  const s = useProjectSettings({
    ...options,
    onSaved: (saved) => {
      refreshProjection.current()
      options.onSaved?.(saved)
    },
  })
  const projection = useGitProjectionStatus(options)
  refreshProjection.current = projection.refresh

  if (s.loading) {
    return (
      <Box sx={{ p: 3, display: 'flex', justifyContent: 'center' }}>
        <CircularProgress size={24} aria-label="Loading project settings" />
      </Box>
    )
  }

  return (
    <Box sx={{ p: 3, maxWidth: 880 }}>
      {title !== '' && (
        <Typography variant="h6" sx={{ mb: 2 }}>
          {title}
        </Typography>
      )}

      {s.error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {s.error}
        </Alert>
      )}

      <Stack spacing={3}>
        <Box>
          <TextField
            label="Base image"
            fullWidth
            value={s.draft.base_image}
            onChange={(e) => s.update({ base_image: e.target.value })}
            placeholder="(unset — the global default image)"
          />
          <FormHelperText>
            Default launch image for every session in this project. A worker&rsquo;s own image
            overrides it; leaving it empty falls back to the global default.
          </FormHelperText>
        </Box>

        <Box>
          <TextField
            label="Project system prompt"
            multiline
            minRows={8}
            maxRows={30}
            fullWidth
            value={s.draft.system_prompt}
            onChange={(e) => s.update({ system_prompt: e.target.value })}
          />
          <FormHelperText>
            Prepended to every worker&rsquo;s prompt, after the engine&rsquo;s core preamble
            and before the worker&rsquo;s own prompt.
          </FormHelperText>
        </Box>

        <JsonObjectEditor
          id="project-mcp-config"
          label="MCP servers (project-wide)"
          value={s.mcpText}
          onChange={s.setMcpText}
          error={s.mcpError}
          helperText={
            'map of name → server config, granted to every worker in the project. ' +
            'Secrets belong in ${VAR} references, never in this file.'
          }
        />

        <JsonObjectEditor
          id="project-attention-channel"
          label="Attention channel"
          value={s.attentionText}
          onChange={s.setAttentionText}
          error={s.attentionError}
          rows={4}
          helperText={
            'Where request_human_attention notifications go, e.g. ' +
            '{"kind":"webhook","url":"https://..."}. Unset: the tool still succeeds and only logs.'
          }
        />

        <Divider />

        <GitProjectionFields draft={s.draft} onChange={s.update} />

        <Divider />

        <ProjectBriefing
          entries={s.draft.briefing}
          onChange={(briefing) => s.update({ briefing })}
        />

        {/* A deployment with no projection route mounted gets no panel at
            all, rather than an error about a feature it does not have. */}
        {!projection.unwired && (
          <>
            <Divider />
            <GitProjectionPanel
              status={projection.status}
              loading={projection.loading}
              error={projection.error}
              onRefresh={projection.refresh}
            />
          </>
        )}

        <Divider />

        <Box>
          <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
            Budgets and caps
          </Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
            Both token budgets exempt interactive chat: a blown budget never locks you out of
            talking to your workers.
          </Typography>
          <Stack spacing={2.5}>
            {PROJECT_SETTING_NUMERICS.map((spec) => (
              <NumericSetting
                key={spec.key}
                spec={spec}
                value={s.draft[spec.key]}
                error={s.fieldErrors[spec.key] ?? null}
                onChange={(v) => s.update({ [spec.key]: v })}
              />
            ))}
          </Stack>
        </Box>

        <Box>
          <TextField
            label="Why?"
            fullWidth
            size="small"
            value={s.rationale}
            placeholder="raising the concurrency cap; the morning queue was backing up"
            onChange={(e) => s.setRationale(e.target.value)}
            inputProps={{ 'aria-label': 'Why?' }}
          />
          <FormHelperText>
            {s.rationale.trim() === ''
              ? 'Required. One line, stored with the change in the config log — the changelog reads it next to who made it.'
              : 'Stored with the change in the config log, and shown in the changelog next to who made it.'}
          </FormHelperText>
        </Box>

        <Stack direction="row" spacing={2} alignItems="center">
          <Button variant="contained" disabled={!s.canSave || !s.dirty} onClick={() => void s.save()}>
            {s.saving ? 'Saving…' : 'Save settings'}
          </Button>
          <Button disabled={s.saving || !s.dirty} onClick={() => void s.reload()}>
            Discard changes
          </Button>
          {!s.dirty && (
            <Typography variant="caption" color="text.secondary">
              No unsaved changes
            </Typography>
          )}
        </Stack>
      </Stack>
    </Box>
  )
}

/**
 * The git projection's status, read once on mount and again after a save.
 *
 * A load failure here is deliberately NOT fatal to the page: this is a status
 * panel beside a form, and a deployment that has not wired the route (501)
 * must not stop anybody editing their project settings. The error is shown
 * inside the panel, in the server's own words, and everything else keeps
 * working.
 */
function useGitProjectionStatus(options: UseProjectSettingsOptions) {
  const { request } = useConfigApi(options)
  const [status, setStatus] = useState<GitProjectionStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [unwired, setUnwired] = useState(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      setStatus(coerceGitProjectionStatus(await request<unknown>(GIT_PROJECTION_ENDPOINT)))
      setError(null)
      setUnwired(false)
    } catch (err) {
      // A 404/501 is not a failure to report: it is a deployment without the
      // route, and the whole panel disappears. Anything else is a real error
      // and is shown in the server's own words.
      if (looksUnwired(err)) {
        setUnwired(true)
        setError(null)
      } else {
        setError(err instanceof Error ? err.message : 'failed to read the git projection status')
      }
    } finally {
      setLoading(false)
    }
  }, [request])

  // Render-phase ref-guard rather than useEffect — the convention this package
  // already follows in useProjectSettings, and for the same reason: a host that
  // inlines `getAuthToken` changes `request`'s identity every render, which a
  // `[request]` effect would turn into an unbounded GET loop.
  const didLoad = useRef(false)
  if (!didLoad.current) {
    didLoad.current = true
    void refresh()
  }

  return { status, loading, error, unwired, refresh: () => void refresh() }
}

/** One numeric setting: the input plus the sentence that applies to the value
 *  currently typed — so "0" always explains itself. */
function NumericSetting({
  spec,
  value,
  error,
  onChange,
}: {
  spec: NumericSettingSpec
  value: number
  error: string | null
  onChange: (value: number) => void
}) {
  return (
    <Box>
      <TextField
        label={spec.label}
        type="number"
        size="small"
        value={String(value)}
        error={error !== null}
        onChange={(e) => {
          // An emptied box means zero, not NaN: the human is mid-edit and the
          // draft must stay a number or every downstream check misreports.
          const raw = e.target.value.trim()
          onChange(raw === '' ? 0 : Number(raw))
        }}
        inputProps={{ min: 0, 'aria-label': spec.label }}
        InputProps={{
          endAdornment: (
            <Typography variant="caption" color="text.secondary" sx={{ ml: 1, whiteSpace: 'nowrap' }}>
              {spec.unit}
            </Typography>
          ),
        }}
        sx={{ width: 280 }}
      />
      <FormHelperText error={error !== null}>
        {error !== null ? error : describeNumericSetting(spec, value)}
      </FormHelperText>
    </Box>
  )
}

/**
 * The project-wide briefing (design B1): a list of label selectors handed to
 * every JOB in the project, on top of whatever the worker lists for itself.
 *
 * Two sentences of copy here are load-bearing, and both describe a blast
 * radius rather than a mechanism:
 *
 *   * every job in the project gets these — this is the one field on the
 *     screen that changes what every worker is told, and a human adding a
 *     selector here is editing every prompt at once;
 *   * a CHAT gets none of them. Briefings are built inside ComposeJob on the
 *     dispatch path only, so talking to a worker in the console gives it none
 *     of this. Nothing anywhere else says so, and the difference is invisible
 *     — the chat simply behaves as though the rulebook did not exist.
 */
function ProjectBriefing({
  entries,
  onChange,
}: {
  entries: string[]
  onChange: (entries: string[]) => void
}) {
  const errors = validateProjectBriefing(entries)

  return (
    <Box data-testid="project-briefing">
      <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
        Project-wide briefing
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        Label selectors handed to <strong>every job in this project</strong>, on top of whatever
        each worker asks for itself. The newest memory matching each selector is injected as a
        briefing section — so this is how one note, edited in one place, reaches every worker.{' '}
        <strong>Chat sessions do not receive these.</strong> Briefings are built when a job is
        dispatched, so a worker you talk to in the console is never handed them.
      </Typography>

      <Stack spacing={1}>
        {entries.map((entry, i) => (
          <Stack key={i} direction="row" spacing={1} alignItems="flex-start">
            <TextField
              fullWidth
              size="small"
              value={entry}
              placeholder="name=label-registry"
              error={errors[i] !== undefined}
              helperText={errors[i]}
              onChange={(e) => {
                const next = [...entries]
                next[i] = e.target.value
                onChange(next)
              }}
              inputProps={{ 'aria-label': `Briefing selector ${i + 1}` }}
            />
            <IconButton
              size="small"
              aria-label={`Remove briefing selector ${i + 1}`}
              onClick={() => onChange(entries.filter((_, j) => j !== i))}
            >
              <DeleteOutlineIcon fontSize="small" />
            </IconButton>
          </Stack>
        ))}
      </Stack>

      <Button size="small" sx={{ mt: 1 }} onClick={() => onChange([...entries, ''])}>
        Add a selector
      </Button>
      {entries.length === 0 && (
        <FormHelperText>
          None set. Onboarding adds <code>name=label-registry</code> here, which is what puts the
          project&rsquo;s label rulebook in front of every worker.
        </FormHelperText>
      )}
    </Box>
  )
}

// G24: editors for the four git-projection fields (design
// 2026-09-09-git-projection.md §D). GitProjectionPanel above this section is
// read-only status; these are the only way to point a project at a repository
// at all.
//
// `git_token_env` and `git_subfolder` get their own regexes, duplicated from
// go/agentdb/project_settings.go's gitTokenEnvPattern/gitSubfolderPattern
// rather than imported (this is a browser bundle, the engine is Go) — kept in
// sync by eye the same way projectSettings.ts already says it keeps the
// numeric defaults in sync. The point is to catch the mistake as it is typed,
// not to replace the server's own check.
const GIT_TOKEN_ENV_PATTERN = /^[A-Za-z_][A-Za-z0-9_]*$/
const GIT_SUBFOLDER_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/

function GitProjectionFields({
  draft,
  onChange,
}: {
  draft: ProjectSettings
  onChange: (patch: Partial<ProjectSettings>) => void
}) {
  const remoteSet = draft.git_remote.trim() !== ''
  const tokenEnvError =
    draft.git_token_env.trim() !== '' && !GIT_TOKEN_ENV_PATTERN.test(draft.git_token_env)
      ? 'not a valid environment variable name (letters, digits, underscore; cannot start with a digit)'
      : null
  const webhookSecretEnvError =
    draft.git_webhook_secret_env.trim() !== '' &&
    !GIT_TOKEN_ENV_PATTERN.test(draft.git_webhook_secret_env)
      ? 'not a valid environment variable name (letters, digits, underscore; cannot start with a digit)'
      : null
  const subfolderError =
    draft.git_subfolder.trim() !== '' && !GIT_SUBFOLDER_PATTERN.test(draft.git_subfolder)
      ? 'must be a single path segment: lowercase letters, digits and hyphens only — no slashes, no ".."'
      : null

  return (
    <Box data-testid="git-projection-fields">
      <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
        Git repository
      </Typography>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        Where this project&rsquo;s configuration renders to as markdown. These four fields are{' '}
        <strong>never imported</strong> — commit access to the repository cannot redirect a
        project&rsquo;s own projection or its credential.
      </Typography>

      <Stack spacing={2.5}>
        <Box>
          <TextField
            label="Repository"
            fullWidth
            size="small"
            value={draft.git_remote}
            placeholder="(empty — git projection is off)"
            onChange={(e) => onChange({ git_remote: e.target.value })}
          />
          <FormHelperText>
            {remoteSet
              ? 'Configuration renders to this repository on every change.'
              : 'Empty on purpose: git projection is off. Setting a repository URL here turns it on.'}
          </FormHelperText>
        </Box>

        <Box>
          <TextField
            label="Branch"
            fullWidth
            size="small"
            value={draft.git_branch}
            placeholder="main"
            onChange={(e) => onChange({ git_branch: e.target.value })}
          />
          <FormHelperText>
            Optional. Left empty, the engine renders to its own default branch (shown as the
            placeholder above) rather than one stored here.
          </FormHelperText>
        </Box>

        <Box>
          <TextField
            label="Subfolder"
            fullWidth
            size="small"
            value={draft.git_subfolder}
            placeholder="bob"
            error={subfolderError !== null}
            onChange={(e) => onChange({ git_subfolder: e.target.value })}
          />
          <FormHelperText error={subfolderError !== null}>
            {subfolderError ??
              'Optional. A single path segment inside the repository that Bob owns (default shown as the placeholder). No slashes, no "..", nothing that could land outside it.'}
          </FormHelperText>
        </Box>

        <Box>
          <TextField
            label="Push token — environment variable name"
            fullWidth
            size="small"
            value={draft.git_token_env}
            placeholder="e.g. GIT_PUSH_TOKEN"
            error={tokenEnvError !== null}
            onChange={(e) => onChange({ git_token_env: e.target.value })}
          />
          <FormHelperText error={tokenEnvError !== null}>
            {tokenEnvError ??
              'The NAME of an environment variable already set on agentd, never the token itself. ' +
                'agentd reads the push token from that variable at render time — do not paste a ' +
                'credential into this field, it would be written to the database and then refused ' +
                'at render rather than published.'}
          </FormHelperText>
        </Box>

        <Box>
          <TextField
            label="Webhook secret — environment variable name"
            fullWidth
            size="small"
            value={draft.git_webhook_secret_env}
            placeholder="e.g. GIT_WEBHOOK_SECRET"
            error={webhookSecretEnvError !== null}
            onChange={(e) => onChange({ git_webhook_secret_env: e.target.value })}
          />
          <FormHelperText error={webhookSecretEnvError !== null}>
            {webhookSecretEnvError ??
              'Verifies that an inbound push notification really came from GitHub — without it, ' +
                'inbound changes are not accepted. The NAME of an environment variable already set ' +
                'on agentd, never the secret itself. Do not paste a credential into this field: it ' +
                'would be written to the database and published nowhere useful, leaving the webhook ' +
                'unverified while looking configured. Optional — empty means inbound webhooks are ' +
                'not configured.'}
          </FormHelperText>
        </Box>
      </Stack>
    </Box>
  )
}
