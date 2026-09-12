// WorkerEditor — the worker page of spec docs/product/02-workers.md §6.5:
// description, prompt editor, MCP JSON editor, image picker (§13),
// max_instances, briefing-selector list (§7.4) and the enabled toggle.
//
// The editor holds its own draft and re-seeds it when a different worker is
// selected. That keeps the parent free of form state and makes "Discard" a
// one-line reset, at the cost of one rule the parent must respect: change the
// `worker.name` (or `isNew`) to load a different row, never mutate the object
// in place and expect the form to follow.
//
// What it does NOT do: validate label-selector grammar (the server owns that
// parser — one source of truth) or check that an image name exists: a registry
// reference is a legal value (§13.3), so `imageOptions` suggests and never
// constrains.

import { useMemo, useRef, useState } from 'react'
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Chip,
  Divider,
  FormControlLabel,
  FormHelperText,
  IconButton,
  Stack,
  Switch,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/Delete'
import LockIcon from '@mui/icons-material/Lock'
import JsonObjectEditor from './JsonObjectEditor.js'
import { formatJsonObject, parseJsonObject } from '../projectSettings.js'
import {
  describeImageRef,
  FROZEN_SENTENCE,
  newWorkerDraft,
  validateWorker,
  type Worker,
  type WorkerDraft,
} from '../workers.js'

export interface WorkerEditorProps {
  /** The worker being edited. Omit (with isNew) to create one. */
  worker?: Worker | null
  /** True when creating: the name field becomes editable. */
  isNew?: boolean
  /** Save handler — the parent owns the API call (useWorkers().save). The
   *  second argument is the operator's one-line reason (design B3 / K2). */
  onSave: (draft: WorkerDraft, rationale: string) => void | Promise<unknown>
  /** Delete handler. Omitted ⇒ no delete button (e.g. while creating). */
  onDelete?: (name: string, rationale: string) => void | Promise<unknown>
  /** Save/delete failure from the parent, shown at the top of the form. */
  error?: string | null
  saving?: boolean
  /**
   * Known image names for the picker's dropdown, typically the project's
   * catalogue (`GET /agent/images`, B4 — WorkersPage loads it). Optional: with
   * no options the picker is a plain free-text field, which is exactly what
   * §13's one-text-field grammar needs, and the field accepts arbitrary refs
   * either way.
   */
  imageOptions?: string[]
  /** The project's base image, named in the picker's helper text when unset. */
  projectBaseImage?: string
  /**
   * Pre-filled one-line reason. Used by the lineage's "Restore this version"
   * (design §7.1), which is an ordinary forward save carrying a reason that
   * names the config event it restores. Re-seeded like the rest of the draft,
   * so change the parent's `key` to load a different pre-fill.
   */
  initialRationale?: string
}

export default function WorkerEditor({
  worker = null,
  isNew = false,
  onSave,
  onDelete,
  error = null,
  saving = false,
  imageOptions = [],
  projectBaseImage = '',
  initialRationale = '',
}: WorkerEditorProps) {
  const seed = () => (worker ? { ...worker } : newWorkerDraft())
  const [draft, setDraft] = useState<WorkerDraft>(seed)
  const [mcpText, setMcpText] = useState(() => formatJsonObject(seed().mcp_config))
  const [mcpError, setMcpError] = useState<string | null>(null)
  const [rationale, setRationale] = useState(initialRationale)
  // A pre-filled reason means the draft is already a proposed change (the
  // restore path hands us a prompt that differs from the live row), so the form
  // starts dirty and Save is live without a keystroke.
  const [dirty, setDirty] = useState(initialRationale !== '')

  // Re-seed when the parent selects a different worker. Render-phase, keyed on
  // identity rather than a useEffect, so the very first paint of the new
  // selection already shows the new row (an effect would flash the old one).
  const seededFor = useRef<string | null>(null)
  // The sentinels are a literal NUL prefix so that no real worker name can
  // ever equal them. Written as a \u0000 escape, not as a raw byte: a raw one
  // makes this file grep as binary and diff as "Binary files differ". The
  // runtime strings are unchanged.
  const identity = isNew ? '\u0000new' : (worker?.name ?? '\u0000none')
  if (seededFor.current !== identity) {
    seededFor.current = identity
    const next = seed()
    setDraft(next)
    setMcpText(formatJsonObject(next.mcp_config))
    setMcpError(null)
    setRationale(initialRationale)
    setDirty(initialRationale !== '')
  }

  const update = (patch: Partial<WorkerDraft>) => {
    setDraft((prev) => ({ ...prev, ...patch }))
    setDirty(true)
  }

  const fieldErrors = useMemo(() => validateWorker(draft), [draft])
  const canSave =
    !saving &&
    mcpError === null &&
    Object.keys(fieldErrors).length === 0 &&
    rationale.trim() !== '' &&
    dirty

  const selectors = draft.briefing ?? []

  const handleSave = () => {
    const mcp = parseJsonObject(mcpText)
    if (!mcp.ok) {
      setMcpError(mcp.error)
      return
    }
    void onSave({ ...draft, mcp_config: mcp.value }, rationale)
    setDirty(false)
  }

  return (
    <Box sx={{ p: 3, maxWidth: 880 }}>
      {error !== null && (
        <Alert severity="error" sx={{ mb: 2 }}>
          {error}
        </Alert>
      )}

      <Stack spacing={3}>
        <Stack direction="row" spacing={2} alignItems="flex-start">
          <Box sx={{ flex: 1 }}>
            <TextField
              label="Name"
              fullWidth
              value={draft.name}
              disabled={!isNew}
              error={fieldErrors.name !== undefined}
              onChange={(e) => update({ name: e.target.value })}
              inputProps={{ 'aria-label': 'Name' }}
              placeholder="email-answerer"
            />
            <FormHelperText error={fieldErrors.name !== undefined}>
              {fieldErrors.name ??
                (isNew
                  ? 'kebab-case, unique within the project.'
                  : 'Identity is fixed. To rename, create a new worker.')}
            </FormHelperText>
          </Box>
          <FormControlLabel
            sx={{ mt: 1 }}
            control={
              <Switch
                checked={draft.enabled}
                onChange={(e) => update({ enabled: e.target.checked })}
                inputProps={{ 'aria-label': 'Enabled' }}
              />
            }
            label={draft.enabled ? 'Enabled' : 'Disabled'}
          />
          {/* The freeze control is the HUMAN path (10-topology-library §3):
              this page rides the JWT-guarded HTTP API, so the toggle belongs
              here and nowhere a worker's tools can reach. */}
          <FormControlLabel
            sx={{ mt: 1 }}
            control={
              <Switch
                checked={draft.frozen}
                onChange={(e) => update({ frozen: e.target.checked })}
                inputProps={{ 'aria-label': 'Frozen' }}
              />
            }
            label={
              draft.frozen ? (
                <Chip size="small" color="info" variant="outlined" icon={<LockIcon />} label="Frozen" />
              ) : (
                'Not frozen'
              )
            }
          />
        </Stack>
        {!draft.enabled && (
          <Typography variant="caption" color="text.secondary" sx={{ mt: -2 }}>
            A disabled worker ignores its subscriptions. You can still chat with it.
          </Typography>
        )}
        {draft.frozen && (
          <Typography variant="caption" color="text.secondary" sx={{ mt: -2 }}>
            {FROZEN_SENTENCE} Their worker_update and worker_prompt_write calls are refused
            (and each refusal is recorded as a worker.freeze_refused event); only humans, on
            this page or through the API, can edit or unfreeze it. It still runs, and it can
            still be read.
          </Typography>
        )}

        <TextField
          label="Description"
          fullWidth
          value={draft.description}
          onChange={(e) => update({ description: e.target.value })}
          helperText="One line. Shown in the worker list, and to other workers as context."
          inputProps={{ 'aria-label': 'Description' }}
        />

        <Box>
          <TextField
            label="System prompt"
            multiline
            minRows={10}
            maxRows={40}
            fullWidth
            value={draft.system_prompt}
            onChange={(e) => update({ system_prompt: e.target.value })}
            inputProps={{ 'aria-label': 'System prompt' }}
          />
          <FormHelperText>
            Everything this worker believes. Appended after the core preamble and the project
            prompt. Edits address the next job — running sessions keep the prompt they were
            composed with.
          </FormHelperText>
        </Box>

        <JsonObjectEditor
          id="worker-mcp-config"
          label="MCP servers (worker)"
          value={mcpText}
          onChange={(text) => {
            setMcpText(text)
            setDirty(true)
            const parsed = parseJsonObject(text)
            setMcpError(parsed.ok ? null : parsed.error)
          }}
          error={mcpError}
          rows={8}
          helperText="Merged over the project's servers; this worker wins on name collisions."
        />

        <Divider />

        <Box>
          <Autocomplete
            freeSolo
            options={imageOptions}
            value={draft.image}
            inputValue={draft.image}
            onInputChange={(_e, value) => update({ image: value })}
            renderInput={(params) => (
              <TextField
                {...params}
                label="Image"
                placeholder="marketing-tools or marketing-tools:3"
                error={fieldErrors.image !== undefined}
                inputProps={{ ...params.inputProps, 'aria-label': 'Image' }}
              />
            )}
            sx={{ maxWidth: 420 }}
          />
          <FormHelperText error={fieldErrors.image !== undefined}>
            {fieldErrors.image ?? describeImageRef(draft.image, projectBaseImage)}
          </FormHelperText>
        </Box>

        <Box>
          <TextField
            label="Max instances"
            type="number"
            size="small"
            value={String(draft.max_instances)}
            error={fieldErrors.max_instances !== undefined}
            onChange={(e) => {
              const raw = e.target.value.trim()
              update({ max_instances: raw === '' ? 0 : Number(raw) })
            }}
            inputProps={{ min: 1, 'aria-label': 'Max instances' }}
            sx={{ width: 220 }}
          />
          <FormHelperText error={fieldErrors.max_instances !== undefined}>
            {fieldErrors.max_instances ??
              (draft.max_instances === 1
                ? 'One job at a time: a second trigger waits for the first to finish.'
                : `Up to ${draft.max_instances} jobs for this worker run at once.`)}
          </FormHelperText>
        </Box>

        <Box>
          <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
            Briefing selectors
          </Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
            Each selector contributes one headed section to this worker&rsquo;s prompt: the
            newest memory it matches, byte-capped by the project&rsquo;s briefing cap. This
            worker&rsquo;s rolling summary is always included and is not listed here.
          </Typography>
          <Stack spacing={1}>
            {selectors.map((selector, i) => (
              <Stack key={i} direction="row" spacing={1} alignItems="flex-start">
                <Box sx={{ flex: 1 }}>
                  <TextField
                    size="small"
                    fullWidth
                    value={selector}
                    error={fieldErrors[`briefing.${i}`] !== undefined}
                    onChange={(e) => {
                      const next = selectors.slice()
                      next[i] = e.target.value
                      update({ briefing: next })
                    }}
                    inputProps={{ 'aria-label': `Briefing selector ${i + 1}` }}
                    placeholder="kind=house-style"
                    sx={{ '& input': { fontFamily: 'monospace', fontSize: '0.8125rem' } }}
                  />
                  {fieldErrors[`briefing.${i}`] !== undefined && (
                    <FormHelperText error>{fieldErrors[`briefing.${i}`]}</FormHelperText>
                  )}
                </Box>
                <Tooltip title="Remove selector">
                  <IconButton
                    size="small"
                    color="info"
                    aria-label={`Remove briefing selector ${i + 1}`}
                    onClick={() => {
                      const next = selectors.filter((_, j) => j !== i)
                      // Emptying the list leaves [], NOT null. The two compose
                      // identically — a briefing of none and a briefing of []
                      // produce the same job — but since T27 they no longer
                      // TRAVEL identically: null on the wire means "leave the
                      // stored briefing alone" and [] means "clear it". So the
                      // draft has to keep the distinction the human just made.
                      // Collapsing to null here would turn "remove every
                      // selector, save" into a silent no-op.
                      update({ briefing: next })
                    }}
                  >
                    <DeleteIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              </Stack>
            ))}
            <Box>
              <Button
                size="small"
                startIcon={<AddIcon />}
                onClick={() => update({ briefing: [...selectors, ''] })}
              >
                Add selector
              </Button>
            </Box>
          </Stack>
        </Box>

        <Divider />

        <Box>
          <TextField
            label="Why?"
            fullWidth
            size="small"
            value={rationale}
            placeholder="customers complained the replies were long"
            onChange={(e) => setRationale(e.target.value)}
            inputProps={{ 'aria-label': 'Why?' }}
          />
          <FormHelperText>
            {rationale.trim() === ''
              ? 'Required. One line, stored with the change in the config log — the changelog reads it next to who made it.'
              : 'Stored with the change in the config log, and shown in the changelog next to who made it.'}
          </FormHelperText>
        </Box>

        <Stack direction="row" spacing={2} alignItems="center">
          <Button variant="contained" disabled={!canSave} onClick={handleSave}>
            {saving ? 'Saving…' : isNew ? 'Create worker' : 'Save worker'}
          </Button>
          {onDelete && !isNew && draft.name !== '' && (
            <Button color="error" disabled={saving} onClick={() => void onDelete(draft.name, rationale)}>
              Delete
            </Button>
          )}
          {!canSave && rationale.trim() === '' && (
            <Typography variant="caption" color="text.secondary">
              Saving needs a reason — it becomes this change's entry in the changelog.
            </Typography>
          )}
          {!dirty && !isNew && (
            <Typography variant="caption" color="text.secondary">
              No unsaved changes
            </Typography>
          )}
        </Stack>
      </Stack>
    </Box>
  )
}
