// WorkersPage — the whole worker surface of spec §6.5 on one screen: the list,
// the editor, the job history, and the chat, with the selected worker in the
// address bar.
//
// Router-free, F3's way: selection is one query parameter written through the
// History API, so a host that already has a router can pass `selected` +
// `onSelect` and this component never touches the URL. Nothing here imports a
// router, and nothing here assumes one exists.

import { useCallback, useEffect, useState } from 'react'
import { Alert, Box, Button, Divider, Paper, Stack, Tab, Tabs, Typography } from '@mui/material'
import useWorkers from '../useWorkers.js'
import useImages from '../useImages.js'
import type { ConfigApiOptions } from '../configApi.js'
import { buildWorkerSearch, newWorkerDraft, workerFromSearch, type WorkerDraft } from '../workers.js'
import WorkerList from './WorkerList.js'
import WorkerEditor from './WorkerEditor.js'
import WorkerHistory, { type HistoryVersion } from './WorkerHistory.js'
import WorkerTriggers, { WokenBy } from './WorkerTriggers.js'
import WorkerPromptVersion, { restoreRationale } from './WorkerPromptVersion.js'
import WorkerChatPanel from './WorkerChatPanel.js'
import TopologyOnboarding from './TopologyOnboarding.js'
import BriefingPreview from './BriefingPreview.js'

/** Sentinel for "the create-a-worker form is open". Not a legal worker name
 *  (names are kebab-case), so it can never collide with a real selection. */
const NEW_WORKER = '#new'

/** Sentinel for "the start-from-a-topology flow is open" (T3). Same trick. */
const FROM_TOPOLOGY = '#topology'

export interface WorkersPageProps extends ConfigApiOptions {
  /** Project id — scopes permalinks and the chat's session. */
  projectId: string
  /**
   * Controlled selection. Pass it (with onSelect) when the host owns routing;
   * doing so also disables this component's own URL writing.
   */
  selected?: string | null
  onSelect?: (name: string | null) => void
  /** Write `?worker=` into the URL. Ignored when `selected` is controlled. */
  syncUrl?: boolean
  /**
   * Known image names for the editor's image picker. Optional: left unset the
   * page loads the project's catalogue itself (`GET /agent/images`, B4). Pass
   * it when the host already has the list, or to override what is offered.
   */
  imageOptions?: string[]
  /** The project's base image, named in the picker's helper text. */
  projectBaseImage?: string
  /** Called when a job row is clicked — typically useSessionPermalink().openSession. */
  onOpenSession?: (sessionId: string) => void
  /** Render the "Chat" tab. Requires an <AgentChatProvider> ancestor. */
  enableChat?: boolean
  /**
   * Which tab to open on. Applied once, on mount and whenever it CHANGES, so a
   * deep link (the chart's clock → this worker's triggers) lands where it meant
   * to without pinning the human there afterwards.
   */
  initialTab?: TabKey
}

/**
 * The four facets of one worker: what is it, what makes it run, what has it
 * done and how has it changed, and let me talk to it.
 *
 * `jobs` and `lineage` were separate tabs until doc 28 §2.3 — "what it did" and
 * "how it changed" are one story told in time, and two tabs is what hid it.
 */
type TabKey = 'config' | 'triggers' | 'history' | 'chat'

export default function WorkersPage({
  projectId,
  selected: controlledSelected,
  onSelect,
  syncUrl = true,
  imageOptions,
  projectBaseImage,
  onOpenSession,
  enableChat = true,
  initialTab,
  ...apiOptions
}: WorkersPageProps) {
  const { workers, loading, error, loadError, save, remove, reload } = useWorkers(apiOptions)
  // The catalogue is a suggestion list, not a constraint (the field stays free
  // text), so a host that mounts no catalogue route simply gets no dropdown.
  const { imageOptions: catalogueOptions } = useImages(apiOptions)
  const images = imageOptions ?? catalogueOptions

  const controlled = controlledSelected !== undefined
  const hasWindow = typeof window !== 'undefined'
  const urlEnabled = !controlled && syncUrl && hasWindow

  const [internalSelected, setInternalSelected] = useState<string | null>(() =>
    urlEnabled ? workerFromSearch(window.location.search) : null,
  )
  const [tab, setTab] = useState<TabKey>(initialTab ?? 'config')
  // Follow a CHANGE of the requested tab, not its presence: render-phase and
  // keyed on the value, so a deep link switches the tab without an effect
  // painting the wrong one first — and without trapping the human on it.
  const [lastRequestedTab, setLastRequestedTab] = useState<TabKey | undefined>(initialTab)
  if (initialTab !== undefined && initialTab !== lastRequestedTab) {
    setLastRequestedTab(initialTab)
    setTab(initialTab)
  }
  const [saving, setSaving] = useState(false)
  // Fold-to-version (design §7.1): `folded` is history being read, `restoring`
  // is history being written forward. Never both, and both drop on selection
  // change — a version of one worker means nothing on another.
  const [folded, setFolded] = useState<HistoryVersion | null>(null)
  const [restoring, setRestoring] = useState<HistoryVersion | null>(null)

  const selected = controlled ? controlledSelected! : internalSelected

  const select = useCallback(
    (name: string | null) => {
      if (!controlled) setInternalSelected(name)
      setFolded(null)
      setRestoring(null)
      if (urlEnabled) {
        const search = buildWorkerSearch(window.location.search, name)
        window.history.pushState(null, '', window.location.pathname + search + window.location.hash)
      }
      onSelect?.(name)
    },
    [controlled, onSelect, urlEnabled],
  )

  // Back/forward moves the selection when we own the URL. Subscribing to a
  // browser event with a matching unsubscribe is exactly what useEffect is for
  // (unlike one-shot init, which this package does with a render-phase ref-guard).
  useEffect(() => {
    if (!urlEnabled) return
    const onPop = () => setInternalSelected(workerFromSearch(window.location.search))
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [urlEnabled])

  const isNew = selected === NEW_WORKER
  const isTopology = selected === FROM_TOPOLOGY
  const current =
    isNew || isTopology ? null : (workers.find((w) => w.name === selected) ?? null)
  // An empty project is where the topology flow earns its place (T3): offer it
  // prominently instead of a bare "no workers" shrug.
  // `loadError === null` matters: `useWorkers` leaves the initial `[]` when the
  // LIST fails, so without this gate an operator whose fetch failed is invited
  // to start a project they already have (RD28). The banner below carries why.
  // Gated on the load error rather than `error` so a failed *save* — which says
  // nothing about whether the list is real — does not swap the panel out.
  const emptyProject = !loading && loadError === null && workers.length === 0

  const handleSave = useCallback(
    async (draft: WorkerDraft, rationale: string) => {
      setSaving(true)
      const stored = await save(draft, rationale)
      setSaving(false)
      setRestoring(null)
      if (stored) select(stored.name)
    },
    [save, select],
  )

  const handleDelete = useCallback(
    async (name: string, rationale: string) => {
      setSaving(true)
      const ok = await remove(name, rationale)
      setSaving(false)
      if (ok) select(null)
    },
    [remove, select],
  )

  return (
    <Stack direction="row" sx={{ height: '100%', minHeight: 0 }}>
      <Box sx={{ width: 280, flexShrink: 0, borderRight: 1, borderColor: 'divider', overflowY: 'auto' }}>
        <WorkerList
          workers={workers}
          selected={selected}
          loading={loading}
          error={loadError}
          onSelect={select}
          onCreate={() => {
            select(NEW_WORKER)
            setTab('config')
          }}
        />
      </Box>

      <Box sx={{ flex: 1, minWidth: 0, overflowY: 'auto' }}>
        {error !== null && (
          <Alert severity="error" sx={{ m: 2 }}>
            {error}
          </Alert>
        )}

        {isTopology ? (
          <TopologyOnboarding
            onApplied={() => void reload()}
            onClose={() => select(null)}
            {...apiOptions}
          />
        ) : !isNew && current === null ? (
          <Box sx={{ p: 3 }}>
            {emptyProject ? (
              <Paper variant="outlined" sx={{ p: 3, maxWidth: 560 }}>
                <Typography variant="subtitle1" sx={{ mb: 0.5 }}>
                  This project has no workers yet
                </Typography>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                  Start from a topology — a pre-built org chart of workers, subscriptions and
                  schedules, applied in one step — or create a single worker by hand.
                </Typography>
                <Stack direction="row" spacing={1}>
                  <Button size="small" variant="contained" onClick={() => select(FROM_TOPOLOGY)}>
                    Start from a topology
                  </Button>
                  <Button
                    size="small"
                    onClick={() => {
                      select(NEW_WORKER)
                      setTab('config')
                    }}
                  >
                    Create a worker
                  </Button>
                </Stack>
              </Paper>
            ) : (
              <>
                <Typography variant="body2" color="text.secondary">
                  Select a worker, or create one.
                </Typography>
                {/* The flow stays reachable in a populated project: collisions
                    are the guard, and the preview shows them. */}
                <Button size="small" sx={{ mt: 1 }} onClick={() => select(FROM_TOPOLOGY)}>
                  Start from a topology
                </Button>
              </>
            )}
          </Box>
        ) : isNew ? (
          <WorkerEditor
            isNew
            worker={newWorkerDraft(projectId)}
            onSave={handleSave}
            saving={saving}
            imageOptions={images}
            projectBaseImage={projectBaseImage}
          />
        ) : (
          <>
            <Tabs value={tab} onChange={(_e, v: TabKey) => setTab(v)} sx={{ px: 2 }}>
              <Tab value="config" label="Configuration" />
              <Tab value="triggers" label="Triggers" />
              <Tab value="history" label="History" />
              {enableChat && <Tab value="chat" label="Chat" />}
            </Tabs>
            <Divider />
            {tab === 'config' &&
              current !== null &&
              (folded !== null ? (
                <WorkerPromptVersion
                  workerName={current.name}
                  version={folded}
                  onRestore={() => {
                    setRestoring(folded)
                    setFolded(null)
                  }}
                  onClose={() => setFolded(null)}
                />
              ) : (
                <WorkerEditor
                  // Remount so the draft re-seeds on the restored text: the
                  // editor keys its own re-seed on the worker's name, and a
                  // restore keeps the name.
                  key={restoring ? `restore-${restoring.eventId}` : 'live'}
                  worker={restoring ? { ...current, system_prompt: restoring.prompt } : current}
                  initialRationale={restoring ? restoreRationale(restoring) : ''}
                  onSave={handleSave}
                  onDelete={handleDelete}
                  saving={saving}
                  imageOptions={images}
                  projectBaseImage={projectBaseImage}
                />
              ))}
            {/* The briefing preview sits under the Configuration form because
                the briefing selectors are edited on it (design §8): what the
                worker would actually be told, right now, beside the field that
                decides it. */}
            {tab === 'config' && current !== null && folded === null && (
              <Box sx={{ px: 3, pb: 3 }}>
                {/* The arrival question, answered without a click (doc 28 §2.2).
                    Read-only here; the Triggers tab is where it is edited. */}
                <WokenBy
                  workerName={current.name}
                  onEditTriggers={() => setTab('triggers')}
                  {...apiOptions}
                />
                <BriefingPreview worker={current} {...apiOptions} />
              </Box>
            )}
            {tab === 'triggers' && current && (
              <WorkerTriggers
                workerName={current.name}
                workerOptions={workers.map((w) => w.name)}
                {...apiOptions}
              />
            )}
            {tab === 'history' && current && (
              <WorkerHistory
                workerName={current.name}
                projectId={projectId}
                onOpenSession={onOpenSession}
                selectedEventId={folded?.eventId ?? null}
                onSelectVersion={(version) => {
                  setFolded(version)
                  setRestoring(null)
                  setTab('config')
                }}
                {...apiOptions}
              />
            )}
            {tab === 'chat' && enableChat && current && (
              <WorkerChatPanel worker={current} projectId={projectId} />
            )}
          </>
        )}
      </Box>
    </Stack>
  )
}
