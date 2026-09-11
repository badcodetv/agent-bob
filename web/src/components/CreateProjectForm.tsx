// CreateProjectForm — the "new project" form, shared (design
// 2026-09-11-onboarding-and-the-guide.md §3 G6).
//
// Before this file there were two: examples/web's ProjectPicker.tsx (shown
// after login when an account maps to more than one project) and its
// Sidebar.tsx (the "+ New project…" dialog). They asked for the same two
// fields — a project id and a goal — but only the Sidebar's said what
// creating a project actually does. A person landing on the picker never
// saw that sentence. One form, used by both; the Sidebar's copy wins.
//
// Presentational: props in, callbacks out, no fetch of its own — `onCreate`
// is the caller's API call, so this stays a tier-2 component
// (`web/src/components/index.ts`).

import React, { useState } from 'react'
import { Alert, Box, Button, Stack, TextField, Typography } from '@mui/material'

export interface CreateProjectFormProps {
  /** Called with the trimmed project id and goal on submit. Throw/reject to show the error inline. */
  onCreate: (projectId: string, goal: string) => Promise<void>
  /** Called after a successful create (e.g. to close a dialog). Fields are cleared regardless. */
  onCreated?: () => void
  /** Renders a "Cancel" button beside "Create project" when given. */
  onCancel?: () => void
  /** Autofocus the project id field. The Sidebar's dialog wants this; a picker embedded in a page usually does not. */
  autoFocus?: boolean
}

export default function CreateProjectForm({ onCreate, onCreated, onCancel, autoFocus }: CreateProjectFormProps) {
  const [projectId, setProjectId] = useState('')
  const [goal, setGoal] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const canSubmit = !submitting && projectId.trim() !== '' && goal.trim() !== ''

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    setError(null)
    try {
      await onCreate(projectId.trim(), goal.trim())
      setProjectId('')
      setGoal('')
      onCreated?.()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Box component="form" onSubmit={(e) => void submit(e)} sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <Typography variant="body2" color="text.secondary">
        Creating a project starts an interview. It asks what the project is for and how you would
        know it is working, then writes that down for you to approve.
      </Typography>
      <TextField
        size="small"
        fullWidth
        autoFocus={autoFocus}
        required
        label="Project id"
        placeholder="apples-oranges"
        helperText="Kebab-case. This is the namespace everything in the project lives under."
        value={projectId}
        onChange={(e) => setProjectId(e.target.value)}
        slotProps={{ htmlInput: { 'data-testid': 'new-project-input' } }}
      />
      <TextField
        size="small"
        fullWidth
        multiline
        minRows={2}
        required
        label="What is this project for?"
        helperText="Your goal — the interview starts from this."
        placeholder="e.g. send a weekly newsletter that brings people back into the shop"
        value={goal}
        onChange={(e) => setGoal(e.target.value)}
        slotProps={{ htmlInput: { 'data-testid': 'new-project-goal' } }}
      />
      {error !== null && <Alert severity="error">{error}</Alert>}
      <Stack direction="row" spacing={1} justifyContent="flex-end">
        {onCancel && (
          <Button onClick={onCancel} disabled={submitting} sx={{ textTransform: 'none' }}>
            Cancel
          </Button>
        )}
        <Button
          type="submit"
          variant="contained"
          disabled={!canSubmit}
          data-testid="new-project-create"
          sx={{ textTransform: 'none' }}
        >
          {submitting ? 'Creating…' : 'Create project'}
        </Button>
      </Stack>
    </Box>
  )
}
