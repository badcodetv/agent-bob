// Agent chat UI component.
// Copied + factored from frontend/src/components/agent/AgentChat.tsx.
// Factoring changes:
//   - Removed InlinePlatinumTable, InlineDashboard, ArtifactViewer (Platinum render plugins).
//   - Removed AGENT_MODELS constant; model list is a prop.
//   - Removed PreparingToolCard; inline equivalent.
//   - ThinkingBlock extracted to its own component (ThinkingBlock.tsx).
//   - Plugin slots for table/chart/dashboard rendering via RenderPlugin seam.
//   - API base URL and auth header as props.
//   - No dependency on AccountContext, router5, or Platinum app state.
// See ../../docs/09-frontend-components.md and ../../docs/90-provenance-map.md.

import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Box, Button, Typography, Alert, Switch, Chip, Divider, useMediaQuery } from '@mui/material'
import { alpha } from '@mui/material/styles'
import type { ActivityStatus, AgentMessage, ArtifactInfo, AskUserQuestionInfo, CreatedDashboardInfo, RenderedTableInfo, RenderedChartInfo, TodoItem } from '../types.js'
import { getToolCategory, getToolIcon } from '../tool-formatters.js'
import AgentMarkdown from './AgentMarkdown.js'
import ToolCallGroup, { tryParseImageOutput, isImageToolCall } from './ToolCallGroup.js'
import InlineArtifactPreview from './InlineArtifactPreview.js'
import AskUserCard from './AskUserCard.js'
import ArtifactPanel from './ArtifactPanel.js'
import ThinkingBlock from './ThinkingBlock.js'
import ChatInputToolbar from './ChatInputToolbar.js'
import AboutThisScreen from './AboutThisScreen.js'
import useFileAttachments from '../hooks/useFileAttachments.js'
import useVoiceDictation from '../hooks/useVoiceDictation.js'
import type { RenderPlugin, AgentSSEEvent } from '../plugins.js'
import type { AgentSSEEvent as CoreSSEEvent } from '../types.js'
import { useAgentChatContextOptional } from '../AgentChatProvider.js'
import { parseOnboardingSeed } from '../charter.js'

/**
 * Fold plugin events into per-plugin, per-toolCallId state maps.
 *
 * Pure — replay-safe: same input events always produce the same output.
 *
 * Returns Map<pluginIndex, Map<toolCallId, TState>>.
 * Keyed by plugin index so two plugins sharing an event type never collide.
 *
 * Accepts CoreSSEEvent (data: unknown from types.ts) so hosts can pass the
 * pluginEvents array returned directly from useAgentSession without a cast.
 * The data fields are accessed via an explicit cast inside.
 */
export function foldPluginEvents(
  plugins: RenderPlugin[],
  pluginEvents: CoreSSEEvent[],
): Map<number, Map<string, unknown>> {
  const byPlugin = new Map<number, Map<string, unknown>>()
  for (let i = 0; i < plugins.length; i++) {
    const plugin = plugins[i]
    const typeSet = new Set(plugin.eventTypes)
    const byTool = new Map<string, unknown>()
    for (const ev of pluginEvents) {
      if (!typeSet.has(ev.type)) continue
      // Cast data to Record so we can extract the toolCallId key. Plugin.reduce()
      // receives the event typed as plugins.AgentSSEEvent (data: Record<string,unknown>)
      // — the cast is safe because any event processed here went through the SSE parser
      // which always produces an object for data.
      const d = ev.data as Record<string, unknown>
      const key =
        (d?.toolCallId as string | undefined) ||
        (d?.tool_use_id as string | undefined) ||
        (d?.messageId as string | undefined) ||
        ''
      const pluginEv: AgentSSEEvent = { type: ev.type, data: d, timestamp: ev.timestamp }
      const prev = byTool.has(key) ? byTool.get(key) : plugin.init()
      byTool.set(key, plugin.reduce(prev, pluginEv))
    }
    byPlugin.set(i, byTool)
  }
  return byPlugin
}

interface SubagentEventInfo {
  event: 'start' | 'stop'
  agentId: string
  agentType?: string
  result?: string
  timestamp: string
}

interface AgentChatProps {
  // All props are optional so <AgentChat/> can be used inside <AgentChatProvider>
  // with zero props. Each value falls back to the context when omitted.
  messages?: AgentMessage[]
  isStreaming?: boolean
  error?: string | null
  activityStatus?: ActivityStatus | null
  onSendMessage?: (content: string, model?: string, attachmentIds?: string[]) => void
  onCancel?: () => void
  artifacts?: ArtifactInfo[]
  todos?: TodoItem[]
  renderedTables?: Map<string, RenderedTableInfo>
  renderedCharts?: Map<string, RenderedChartInfo>
  askedQuestions?: Map<string, AskUserQuestionInfo>
  createdDashboards?: Map<string, CreatedDashboardInfo>
  onAnswerQuestion?: (toolCallId: string, value: string) => void
  sessionId?: string
  toolInputBuffer?: string
  subagentEvents?: SubagentEventInfo[]
  selectedModel?: string
  onModelChange?: (model: string) => void
  /** Model list for the model selector. */
  models?: { id: string; label: string }[]
  lastHeartbeat?: number
  stuckStatus?: 'ok' | 'possibly_stuck' | 'likely_stuck'
  onNudge?: () => void
  readOnly?: boolean
  /** Render plugins (e.g. Platinum's Carbon table/chart widgets). */
  plugins?: RenderPlugin[]
  /**
   * Raw plugin events accumulated by useAgentSession (live + restored).
   * Folded per-plugin per-toolCallId by AgentChat via foldPluginEvents.
   * Pass useAgentSession's `pluginEvents` return value here directly.
   */
  pluginEvents?: CoreSSEEvent[]
  /** API base URL for artifact downloads. */
  apiBaseUrl?: string
  /** Auth header for artifact downloads and voice transcription. */
  authHeader?: string
  /** Transcription endpoint. Default: apiBaseUrl + /transcribe */
  transcribeEndpoint?: string
  /** Optional upload endpoint factory for file attachments. */
  uploadEndpoint?: (sessionId: string) => string
  /** Optional callback when user clicks "add artifact" (e.g. pin to dashboard). */
  onPinToDashboard?: (sessionId: string) => void
  /** Optional callback to open the full artifact viewer. */
  onOpenArtifactViewer?: (artifact: ArtifactInfo | null) => void
  forkedMessageCount?: number
  /**
   * Name of the worker this session is chatting with, if any (spec §6.4).
   * Absent/empty for a plain base-agent chat. Drives the empty-state copy and
   * suggestions (design 2026-09-11-onboarding-and-the-guide.md §3 G3): a
   * worker chat gets sentences and suggestions about that worker; a base
   * chat gets ones about the project.
   */
  workerName?: string
  /** Scopes the "About this screen" disclosure's dismissal (C2). */
  projectId?: string
}

export default function AgentChat(props: AgentChatProps) {
  // Read optional context — null when used outside a Provider (legacy prop-only usage).
  const ctx = useAgentChatContextOptional()

  // Resolve each value: explicit prop takes precedence, then context, then empty default.
  const messages        = props.messages        ?? ctx?.messages        ?? []
  const isStreaming     = props.isStreaming      ?? ctx?.isStreaming     ?? false
  const error           = props.error           ?? ctx?.error           ?? null
  const activityStatus  = props.activityStatus  ?? ctx?.activityStatus  ?? null
  const onSendMessage   = props.onSendMessage   ?? ctx?.sendMessage     ?? (() => {})
  const onCancel        = props.onCancel        ?? ctx?.cancelSession   ?? (() => {})
  const artifacts       = props.artifacts       ?? ctx?.artifacts       ?? []
  const todos           = props.todos           ?? ctx?.todos           ?? []
  const renderedTables  = props.renderedTables  ?? ctx?.renderedTables  ?? new Map()
  const renderedCharts  = props.renderedCharts  ?? ctx?.renderedCharts  ?? new Map()
  const askedQuestions  = props.askedQuestions  ?? ctx?.askedQuestions  ?? new Map()
  const createdDashboards = props.createdDashboards ?? ctx?.createdDashboards ?? new Map()
  const onAnswerQuestion = props.onAnswerQuestion ?? ctx?.markQuestionAnswered ?? (() => {})
  const sessionId       = props.sessionId       ?? ctx?.session?.id    ?? ''
  const toolInputBuffer = props.toolInputBuffer ?? ctx?.toolInputBuffer ?? ''
  const subagentEvents  = props.subagentEvents  ?? (ctx?.subagentEvents as SubagentEventInfo[] | undefined)
  const selectedModel   = props.selectedModel   ?? ctx?.selectedModel  ?? ''
  const onModelChange   = props.onModelChange   ?? ctx?.setSelectedModel ?? (() => {})
  const models          = props.models          ?? ctx?.config.models   ?? []
  const stuckStatus     = props.stuckStatus     ?? ctx?.stuckStatus
  const onNudge         = props.onNudge         ?? ctx?.nudgeAgent
  const readOnly        = props.readOnly
  // Driven by the provider, and the provider has no session: there is nothing
  // to send TO. `sendMessage` returns silently in that state, so an enabled
  // composer here typed into a void — the Chat view opened from the Desk with
  // no session selected did exactly that. Disabled, and it says why.
  const noSession       = !props.onSendMessage && ctx !== null && !ctx.session
  const plugins         = props.plugins         ?? ctx?.config.plugins ?? []
  const pluginEvents    = props.pluginEvents    ?? ctx?.pluginEvents   ?? []
  const apiBaseUrl      = props.apiBaseUrl      ?? ctx?.config.apiBaseUrl ?? ''
  const authHeader      = props.authHeader
  // The embed page (and the console) hand AgentChatProvider a TOKEN getter, not
  // a header — and this component used to read `props.authHeader` only, so
  // every artifact preview inside the embed went out with no credential and
  // failed with HTTP 401 (found on the box, 2026-09-12). Resolved at fetch time
  // because a host's getter may refresh.
  const getAuthToken    = ctx?.config.getAuthToken
  const getAuthHeader   = useCallback(async (): Promise<string | undefined> => {
    if (authHeader) return authHeader
    if (!getAuthToken) return undefined
    const token = await getAuthToken()
    return token ? `Bearer ${token}` : undefined
  }, [authHeader, getAuthToken])
  const transcribeEndpoint = props.transcribeEndpoint
  const uploadEndpoint  = props.uploadEndpoint
  const onPinToDashboard = props.onPinToDashboard
  const onOpenArtifactViewer = props.onOpenArtifactViewer
  const forkedMessageCount = props.forkedMessageCount
  const workerName = props.workerName
  const projectId = props.projectId ?? ''

  const [input, setInput] = useState('')
  // Below 900px — which inside an iframe is the IFRAME's width — the artifacts
  // panel stops being a fixed 320px column and becomes an overlay behind a
  // button. In a ~550px embed rail the column left the conversation ~230px
  // (reported from Agent Wolf on the box, 2026-09-12).
  const narrow = useMediaQuery('(max-width:899.95px)')
  const [artifactsOpen, setArtifactsOpen] = useState(false)
  const [, setViewerArtifact] = useState<ArtifactInfo | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const scrollContainerRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const prevMsgCountRef = useRef(0)
  const prevMsgLenRef = useRef(messages.length)
  const prevStreamingRef = useRef(isStreaming)

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isStreaming && !readOnly) {
        e.preventDefault()
        onCancel()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isStreaming, readOnly, onCancel])

  const fileAttachments = useFileAttachments({
    uploadEndpoint,
    authHeader,
  })

  const handleTranscription = useCallback((text: string) => {
    setInput(prev => prev ? prev + ' ' + text : text)
  }, [])

  const resolvedTranscribeEndpoint = transcribeEndpoint || `${apiBaseUrl}/transcribe`

  const voiceDictation = useVoiceDictation({
    onTranscription: handleTranscription,
    transcribeEndpoint: resolvedTranscribeEndpoint,
    authHeader,
  })

  const handleToggleRecording = useCallback(() => {
    if (voiceDictation.isRecording) {
      voiceDictation.stopRecording()
    } else {
      voiceDictation.startRecording()
    }
  }, [voiceDictation.isRecording, voiceDictation.stopRecording, voiceDictation.startRecording])

  const handleSubmit = useCallback(async (e: React.FormEvent) => {
    e.preventDefault()
    if (!input.trim() || isStreaming || noSession) return
    let attachmentIds: string[] | undefined
    if (fileAttachments.attachments.length > 0) {
      attachmentIds = await fileAttachments.uploadAll(sessionId)
      fileAttachments.clear()
    }
    onSendMessage(input.trim(), selectedModel, attachmentIds)
    setInput('')
  }, [input, isStreaming, noSession, onSendMessage, selectedModel, fileAttachments.attachments.length, fileAttachments.uploadAll, fileAttachments.clear, sessionId])

  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit(e as unknown as React.FormEvent)
    }
  }, [handleSubmit])

  // Fold plugin events into per-plugin, per-toolCallId state.
  // Pure — replay-safe. Keyed by plugin index so two plugins sharing an
  // event type do not collide. Depends on plugins identity + pluginEvents array.
  const byPlugin = useMemo(
    () => foldPluginEvents(plugins, pluginEvents),
    [plugins, pluginEvents],
  )

  // Build two maps of artifacts per message
  const { autoArtifacts, toolArtifacts } = useMemo(() => {
    const autoMap = new Map<string, ArtifactInfo[]>()
    const toolMap = new Map<string, ArtifactInfo[]>()
    const claimedPaths = new Set<string>()

    for (let i = messages.length - 1; i >= 0; i--) {
      const message = messages[i]
      const toolPaths: string[] = []
      const autoPaths: string[] = []

      if (message.toolCalls) {
        for (const tc of message.toolCalls) {
          if (tc.name === 'register_artifact' || tc.name === 'mcp__ui__register_artifact' || tc.name === 'mcp__data__register_artifact') {
            const filePath = tc.input?.file_path as string | undefined
            if (filePath && !claimedPaths.has(filePath)) toolPaths.push(filePath)
          }
        }
      }

      if (message.autoArtifactPaths) {
        for (const p of message.autoArtifactPaths) {
          if (!claimedPaths.has(p)) autoPaths.push(p)
        }
      }

      for (const p of [...toolPaths, ...autoPaths]) claimedPaths.add(p)

      if (toolPaths.length > 0) {
        const matched = artifacts.filter(a => toolPaths.some(p => a.filePath === p || a.filePath.endsWith(p)))
        if (matched.length > 0) toolMap.set(message.id, matched)
      }
      if (autoPaths.length > 0) {
        const matched = artifacts.filter(a =>
          a.artifactType !== 'webapp' &&
          autoPaths.some(p => a.filePath === p || a.filePath.endsWith(p))
        )
        if (matched.length > 0) autoMap.set(message.id, matched)
      }
    }
    return { autoArtifacts: autoMap, toolArtifacts: toolMap }
  }, [messages, artifacts])

  // Build maps of tool call IDs to rendered tables/charts
  const toolCallTables = useMemo(() => {
    const map = new Map<string, RenderedTableInfo[]>()
    for (const table of renderedTables.values()) {
      if (table.toolCallId) {
        const existing = map.get(table.toolCallId) || []
        existing.push(table)
        map.set(table.toolCallId, existing)
      }
    }
    return map
  }, [renderedTables])

  const toolCallCharts = useMemo(() => {
    const map = new Map<string, RenderedChartInfo[]>()
    for (const chart of renderedCharts.values()) {
      if (chart.toolCallId) {
        const existing = map.get(chart.toolCallId) || []
        existing.push(chart)
        map.set(chart.toolCallId, existing)
      }
    }
    return map
  }, [renderedCharts])

  const toolCallQuestions = useMemo(() => {
    const map = new Map<string, AskUserQuestionInfo>()
    for (const q of askedQuestions.values()) {
      if (q.toolCallId) map.set(q.toolCallId, q)
    }
    return map
  }, [askedQuestions])

  const toolCallDashboards = useMemo(() => {
    const map = new Map<string, CreatedDashboardInfo>()
    for (const dash of createdDashboards.values()) {
      if (dash.toolCallId) map.set(dash.toolCallId, dash)
    }
    return map
  }, [createdDashboards])

  const displayMessages = messages

  // G3 (design 2026-09-11-onboarding-and-the-guide.md §3): an empty
  // transcript is the most-used surface in the product and used to teach
  // nothing — zero rows and a bare composer. `isWorkerChat` distinguishes
  // the two fixed suggestion sets; both are plain text, chosen per context,
  // never sent automatically — clicking one only fills the composer.
  const isWorkerChat = !!workerName
  const emptyStateSuggestions = isWorkerChat
    ? [
        'Show me your instructions.',
        'What did you do last time you ran?',
        'What would you do if I sent you: …',
      ]
    : [
        'What is in this project’s memory?',
        'Which workers exist and what wakes them?',
        'Write a memory that records …',
      ]

  if (messages.length !== prevMsgLenRef.current) {
    prevMsgLenRef.current = messages.length
  }

  if (prevStreamingRef.current && !isStreaming) {
    textareaRef.current?.focus()
  }
  prevStreamingRef.current = isStreaming

  if (displayMessages.length > prevMsgCountRef.current) {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }
  prevMsgCountRef.current = displayMessages.length

  if (isStreaming && scrollContainerRef.current) {
    const el = scrollContainerRef.current
    const isNearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 150
    if (isNearBottom) {
      el.scrollTop = el.scrollHeight
    }
  }

  // Helper: render plugin slots for a tool call ID.
  // Only renders when the plugin has accumulated state for this specific toolCallId
  // (i.e. at least one plugin event arrived with that toolCallId). This is correct:
  // if no event arrived for a toolCallId, there is nothing to render.
  const renderPluginSlots = (toolCallId: string): React.ReactNode => {
    if (plugins.length === 0) return null
    const slots: React.ReactNode[] = []
    for (let i = 0; i < plugins.length; i++) {
      const plugin = plugins[i]
      const state = byPlugin.get(i)?.get(toolCallId)
      if (state !== undefined) {
        const node = plugin.render({ state, toolCallId, sessionId })
        if (node) slots.push(<React.Fragment key={i}>{node}</React.Fragment>)
      }
    }
    return slots.length > 0 ? <>{slots}</> : null
  }

  // Determine model toggle elements
  const hasModelToggle = models.length >= 2

  const handleOpenPreview = useCallback((a: ArtifactInfo) => {
    setViewerArtifact(a)
    if (onOpenArtifactViewer) onOpenArtifactViewer(a)
  }, [onOpenArtifactViewer])

  return (
    // 🔴 `minWidth: 0` on the ROOT ROW too, not just on the chat column inside
    // it. Measured, in a real browser, inside the embed iframe at a 610px
    // rail: this element computed `min-width: auto` → min-content 628px and
    // held itself 18px wider than its 610px parent, so the panel scrolled
    // sideways and Send sat off the edge. The column below already carried
    // `minWidth: 0`, which is why this looked fixed and was not — a
    // shrinkable child inside an unshrinkable parent shrinks nothing.
    //
    // Neither `minHeight: 0` nor `minWidth: 0` is cosmetic here: they are the
    // two halves of the same flexbox rule, and only the height half was ever
    // written down. This component is embedded in a narrow rail by design
    // (docs/19-embedding.md), so it must survive any width.
    <Box sx={{ display: 'flex', flex: 1, minWidth: 0, minHeight: 0, position: 'relative' }}>
      {/* Chat area */}
      <Box sx={{ display: 'flex', flexDirection: 'column', flex: 1, minWidth: 0, minHeight: 0 }}>
        <AboutThisScreen surface="chat" projectId={projectId} sx={{ mx: 2, mt: 2, mb: 0 }} />
        {/* Messages */}
        <Box ref={scrollContainerRef} sx={{ flex: 1, overflow: 'auto', p: 2, position: 'relative' }}>
          {displayMessages.length === 0 && (
            <Box data-testid="chat-empty-state" sx={{ p: 2, display: 'flex', flexDirection: 'column', gap: 1.5 }}>
              <Typography variant="body2" color="text.secondary">
                {isWorkerChat
                  ? `This is a chat with ${workerName}.`
                  : 'This is a chat with the base agent.'}
              </Typography>
              <Typography variant="body2" color="text.secondary">
                A chat is not a job: it gets none of the project&rsquo;s briefing, and nothing it
                says is remembered unless a worker writes a memory.
              </Typography>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, alignItems: 'flex-start', mt: 1 }}>
                {emptyStateSuggestions.map((suggestion) => (
                  <Button
                    key={suggestion}
                    variant="outlined"
                    size="small"
                    onClick={() => {
                      setInput(suggestion)
                      textareaRef.current?.focus()
                    }}
                    sx={{ textTransform: 'none', justifyContent: 'flex-start' }}
                  >
                    {suggestion}
                  </Button>
                ))}
              </Box>
            </Box>
          )}
          {displayMessages.map((message, index) => {
            const hasContent = message.role === 'user' || message.content.trim()
            const hasThinking = !!message.thinking
            const hasTools = message.toolCalls && message.toolCalls.length > 0
            const hasArtifacts = autoArtifacts.has(message.id) || toolArtifacts.has(message.id)
            if (!hasContent && !hasThinking && !hasTools && !hasArtifacts) return null
            const showForkDivider = forkedMessageCount != null && forkedMessageCount > 0 && index === forkedMessageCount - 1
            // An onboarding interview's first message is instructions for the
            // interviewer plus the goal the person typed. Show them their goal,
            // not the instructions (charter.ts, parseOnboardingSeed).
            const seed = message.role === 'user' ? parseOnboardingSeed(message.content) : null
            if (seed !== null) {
              return (
                <Box
                  key={message.id}
                  data-role="user"
                  data-testid="onboarding-seed"
                  sx={{
                    mb: 2,
                    alignSelf: 'center',
                    mx: 'auto',
                    maxWidth: '90%',
                    px: 1.5,
                    py: 1,
                    border: '1px solid',
                    borderColor: 'divider',
                    backgroundColor: 'action.hover',
                    fontSize: '0.8125rem',
                    lineHeight: 1.6,
                    color: 'text.secondary',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-word',
                  }}
                >
                  {seed.goal === '' ? (
                    'You started this project without a goal, so the interview begins by asking what it is for.'
                  ) : (
                    <>
                      <Box component="span" sx={{ fontWeight: 600, color: 'text.primary' }}>
                        You set the goal:
                      </Box>{' '}
                      {seed.goal}
                    </>
                  )}
                </Box>
              )
            }
            return (
              <React.Fragment key={message.id}>
                <Box
                  data-role={message.role}
                  sx={{
                    mb: 2,
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: message.role === 'user' ? 'flex-end' : 'flex-start',
                  }}
                >
                  {/* Thinking block (extracted component) */}
                  {message.thinking && (
                    <ThinkingBlock
                      content={message.thinking}
                      isActivelyThinking={
                        isStreaming
                        && activityStatus?.category === 'thinking'
                        && index === displayMessages.length - 1
                      }
                    />
                  )}
                  {/* Hide empty assistant bubbles during streaming */}
                  {hasContent && (
                    <Box
                      sx={{
                        maxWidth: '80%',
                        p: '12px 16px',
                        borderRadius: 0,
                        backgroundColor: message.role === 'user' ? 'primary.main' : 'background.default',
                        color: message.role === 'user' ? 'white' : 'text.primary',
                        borderLeft: message.role === 'assistant' ? '3px solid #00B2FF' : 'none',
                        wordBreak: 'break-word',
                        fontSize: '0.8125rem',
                        lineHeight: 1.6,
                        ...(message.role === 'user' ? { whiteSpace: 'pre-wrap' } : {}),
                      }}
                    >
                      {message.role === 'assistant' ? (
                        <AgentMarkdown content={message.content} />
                      ) : (
                        message.content
                      )}
                    </Box>
                  )}
                  {/* Auto-registered artifact previews */}
                  {autoArtifacts.get(message.id)?.map((artifact, i) => (
                    <Box key={`auto-artifact-${message.id}-${i}`} sx={{ maxWidth: artifact.artifactType === 'webapp' ? '100%' : '80%', ...(artifact.artifactType === 'webapp' ? { alignSelf: 'stretch' } : {}), mt: 1 }}>
                      <InlineArtifactPreview
                        artifact={artifact}
                        sessionId={sessionId}
                        onOpenPreview={handleOpenPreview}
                        apiBaseUrl={apiBaseUrl}
                        authHeader={authHeader}
                        getAuthHeader={getAuthHeader}
                      />
                    </Box>
                  ))}
                  {/* Tool calls */}
                  {hasTools && (
                    <Box sx={{ maxWidth: '80%', mt: hasContent ? 1 : 0 }}>
                      <ToolCallGroup toolCalls={message.toolCalls!} />
                      {/* Inline tables/charts from renderedTables/Charts — dispatched through plugins when registered */}
                      {message.toolCalls!.map(tc => (
                        <React.Fragment key={tc.id}>
                          {/* Render plugin slots (Platinum table/chart/dashboard widgets register here) */}
                          {renderPluginSlots(tc.id)}
                          {/* Fallback: show raw table/chart state for hosts without plugins.
                              When plugins are registered (e.g. Platinum's Carbon widgets) the host
                              owns rendering via renderPluginSlots above, so suppress these debug
                              cards — otherwise a stray "📈 Chart: X" leaks in next to the widget. */}
                          {plugins.length === 0 && (toolCallTables.get(tc.id) || []).map(table => (
                            <Box key={table.id} sx={{ mt: 1, p: 1.5, border: '1px solid', borderColor: 'divider', borderRadius: 1, backgroundColor: 'action.hover', fontSize: 12, color: 'text.secondary' }}>
                              📊 Table: {table.title || table.id}
                            </Box>
                          ))}
                          {plugins.length === 0 && (toolCallCharts.get(tc.id) || []).map(chart => (
                            <Box key={chart.id} sx={{ mt: 1, p: 1.5, border: '1px solid', borderColor: 'divider', borderRadius: 1, backgroundColor: 'action.hover', fontSize: 12, color: 'text.secondary' }}>
                              📈 Chart: {chart.title || chart.id}
                            </Box>
                          ))}
                          {toolCallQuestions.has(tc.id) && (
                            <AskUserCard
                              question={toolCallQuestions.get(tc.id)!}
                              onAnswer={(value) => {
                                onAnswerQuestion(tc.id, value)
                                onSendMessage(value)
                              }}
                              disabled={isStreaming || toolCallQuestions.get(tc.id)!.answered}
                            />
                          )}
                          {plugins.length === 0 && toolCallDashboards.has(tc.id) && (
                            <Box sx={{ mt: 1, p: 1.5, border: '1px solid', borderColor: 'divider', borderRadius: 1, backgroundColor: 'action.hover', fontSize: 12, color: 'text.secondary' }}>
                              🗂️ Dashboard created
                            </Box>
                          )}
                        </React.Fragment>
                      ))}
                    </Box>
                  )}
                  {/* View image tool results */}
                  {hasTools && message.toolCalls!.filter(tc => isImageToolCall(tc) && tc.output && tryParseImageOutput(tc.output!)).map(tc => {
                    const imgData = tryParseImageOutput(tc.output!)!
                    const filePath = (tc.input?.file_path as string) || 'image.png'
                    const fileName = filePath.split('/').pop() || 'Image'
                    const dataUrl = `data:${imgData.mimeType};base64,${imgData.base64}`
                    const syntheticArtifact: ArtifactInfo = {
                      filePath,
                      fileName,
                      mimeType: imgData.mimeType,
                      label: fileName,
                      artifactType: 'image',
                      source: 'auto',
                      status: 'live',
                      downloadUrl: dataUrl,
                    }
                    return (
                      <Box key={`view-image-${tc.id}`} sx={{ maxWidth: '80%', mt: 1 }}>
                        <InlineArtifactPreview
                          artifact={syntheticArtifact}
                          sessionId={sessionId}
                          dataUrl={dataUrl}
                          onOpenPreview={handleOpenPreview}
                          apiBaseUrl={apiBaseUrl}
                          authHeader={authHeader}
                          getAuthHeader={getAuthHeader}
                        />
                      </Box>
                    )
                  })}
                  {/* Inline artifact previews from register_artifact tool calls */}
                  {toolArtifacts.get(message.id)?.map((artifact, i) => (
                    <Box key={`tool-artifact-${message.id}-${i}`} sx={{ maxWidth: artifact.artifactType === 'webapp' ? '100%' : '80%', ...(artifact.artifactType === 'webapp' ? { alignSelf: 'stretch' } : {}), mt: 1 }}>
                      <InlineArtifactPreview
                        artifact={artifact}
                        sessionId={sessionId}
                        onOpenPreview={handleOpenPreview}
                        apiBaseUrl={apiBaseUrl}
                        authHeader={authHeader}
                        getAuthHeader={getAuthHeader}
                      />
                    </Box>
                  ))}
                </Box>
                {showForkDivider && (
                  <Divider sx={{ my: 2, borderStyle: 'dashed', borderColor: 'divider' }}>
                    <Chip
                      label="Forked from published app — new messages below"
                      size="small"
                      sx={{ fontSize: 11, color: 'text.secondary', backgroundColor: 'action.hover', fontWeight: 500 }}
                    />
                  </Divider>
                )}
              </React.Fragment>
            )
          })}

          {error && (
            <Alert severity="error" sx={{ mt: 1, fontSize: 13 }}>
              {error}
            </Alert>
          )}

          <div ref={messagesEndRef} />
        </Box>

        {/* Preparing tool card (file write preview) */}
        {!readOnly && isStreaming && activityStatus?.category === 'preparing_tool' && activityStatus.toolName && toolInputBuffer && (
          <Box sx={{ px: 2, py: 1, borderTop: '1px solid', borderColor: 'divider', backgroundColor: 'action.hover', display: 'flex', alignItems: 'center', gap: 1 }}>
            <Box sx={{ width: 14, height: 14, border: '2px solid', borderColor: 'divider', borderTopColor: 'text.secondary', borderRadius: '50%', animation: 'spin 0.8s linear infinite', '@keyframes spin': { '100%': { transform: 'rotate(360deg)' } }, flexShrink: 0 }} />
            <Typography sx={{ fontSize: 12, color: 'text.secondary', fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {activityStatus.toolName}: {toolInputBuffer.slice(-120)}
            </Typography>
          </Box>
        )}

        {/* System status */}
        {!readOnly && !isStreaming && activityStatus?.category === 'system' && (
          <Box sx={{ px: 2, py: '6px', display: 'flex', alignItems: 'center', gap: 1, fontSize: 13, color: 'text.secondary', borderTop: '1px solid', borderColor: 'divider' }}>
            <Box sx={{ width: 16, height: 16, border: '2px solid', borderColor: 'divider', borderTopColor: 'text.secondary', borderRadius: '50%', animation: 'spin 0.8s linear infinite', '@keyframes spin': { '100%': { transform: 'rotate(360deg)' } }, flexShrink: 0 }} />
            <span style={{ fontWeight: 500 }}>{activityStatus.label}</span>
          </Box>
        )}

        {/* Activity status line */}
        {!readOnly && isStreaming && activityStatus && (
          <Box sx={{ px: 2, py: '6px', display: 'flex', alignItems: 'center', gap: 1, fontSize: 13, color: 'text.secondary', borderTop: '1px solid', borderColor: 'divider' }}>
            <Box sx={{ width: 16, height: 16, border: '2px solid', borderColor: 'divider', borderTopColor: 'text.secondary', borderRadius: '50%', animation: 'spin 0.8s linear infinite', '@keyframes spin': { '100%': { transform: 'rotate(360deg)' } }, flexShrink: 0 }} />
            {activityStatus.toolName && (
              <span>{getToolIcon(getToolCategory(activityStatus.toolName, activityStatus.toolInput))}</span>
            )}
            <span style={{ fontWeight: 500 }}>{activityStatus.label}</span>
            {activityStatus.detail && (
              <span style={{ color: 'text.disabled' }}>{activityStatus.detail}</span>
            )}
            {activityStatus.elapsedSeconds != null && activityStatus.elapsedSeconds > 0 && (
              <span style={{ color: 'text.disabled' }}>{activityStatus.elapsedSeconds}s</span>
            )}
            {activityStatus.category === 'preparing_tool' && activityStatus.toolInputPreview && !toolInputBuffer && (
              <span style={{ color: 'text.disabled', fontFamily: 'monospace', fontSize: 11, maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'inline-block' }}>{activityStatus.toolInputPreview}</span>
            )}
          </Box>
        )}

        {/* Active sub-agent indicators */}
        {!readOnly && isStreaming && subagentEvents && (() => {
          const stoppedIds = new Set(subagentEvents.filter(e => e.event === 'stop').map(e => e.agentId))
          const active = subagentEvents.filter(e => e.event === 'start' && !stoppedIds.has(e.agentId))
          if (active.length === 0) return null
          return (
            <Box sx={{ px: 2, py: '4px', display: 'flex', gap: 1, flexWrap: 'wrap' }}>
              {active.map(a => (
                <Chip key={a.agentId} size="small" label={`${a.agentType || 'Sub-agent'} running...`} sx={{ fontSize: 11, height: 22, backgroundColor: (t) => alpha(t.palette.primary.main, 0.12), color: 'primary.main', fontWeight: 500 }} />
              ))}
            </Box>
          )
        })()}

        {/* Possibly stuck banner */}
        {!readOnly && isStreaming && stuckStatus === 'possibly_stuck' && (
          <Box sx={{ px: 2, py: 1, display: 'flex', alignItems: 'center', gap: 1.5, backgroundColor: (t) => alpha(t.palette.warning.main, 0.12), borderTop: '1px solid', borderColor: (t) => alpha(t.palette.warning.main, 0.4) }}>
            <Typography sx={{ fontSize: 13, flex: 1, color: 'warning.main' }}>
              The agent has been quiet for a while. It may be working on something complex.
            </Typography>
            <Button variant="outlined" color="info" size="small" onClick={onCancel} sx={{ textTransform: 'none', fontSize: 12, whiteSpace: 'nowrap' }}>
              Stop
            </Button>
          </Box>
        )}

        {/* Stuck detection banner */}
        {!readOnly && isStreaming && stuckStatus === 'likely_stuck' && (
          <Box sx={{ px: 2, py: 1, display: 'flex', alignItems: 'center', gap: 1.5, backgroundColor: (t) => alpha(t.palette.error.main, 0.12), borderTop: '1px solid', borderColor: (t) => alpha(t.palette.error.main, 0.4) }}>
            <Typography sx={{ fontSize: 13, flex: 1, color: 'error.main' }}>
              The agent appears to be stuck. No activity for over 2 minutes.
            </Typography>
            {onNudge && (
              <Button variant="contained" color="info" size="small" onClick={onNudge} sx={{ textTransform: 'none', fontSize: 12, whiteSpace: 'nowrap' }}>
                Send Nudge
              </Button>
            )}
            <Button variant="outlined" color="info" size="small" onClick={onCancel} sx={{ textTransform: 'none', fontSize: 12, whiteSpace: 'nowrap' }}>
              Stop
            </Button>
          </Box>
        )}

        {/* Unconfirmed-end banner.
         *
         * The two banners above are gated on isStreaming and are correct for the
         * live case: heartbeats stop while a stream is attached, so the detector
         * escalates and the operator is told the agent has gone quiet.
         *
         * They cannot fire on the case they matter most for. When a stream ends
         * without `query_complete` and the status probe cannot confirm the turn
         * finished, useAgentSession sets isStreaming=false (it must — holding it
         * true disables the composer forever) but deliberately leaves the stuck
         * detector ARMED (doc 22 RD26 / item B1). That combination —
         * !isStreaming with a non-'ok' stuckStatus — is reachable by no other
         * path: every other stop calls stopStuckDetection(), which resets to
         * 'ok'. So it is a precise signal for "we lost this turn and never
         * learned how it ended", and it is what this banner renders.
         *
         * It deliberately does NOT repeat the "Connection lost" error alert
         * above, which states what already happened. This states what may still
         * be happening — the agent can still be running in its container,
         * producing output that will never reach this transcript — and offers
         * the two actions that resolve it. Both clear the banner, because both
         * call stopStuckDetection(). The composer stays enabled throughout
         * (item P2's defect must not come back in another costume). */}
        {!readOnly && !isStreaming && (stuckStatus === 'possibly_stuck' || stuckStatus === 'likely_stuck') && (
          <Box data-testid="unconfirmed-end-banner" sx={{ px: 2, py: 1, display: 'flex', alignItems: 'center', gap: 1.5, backgroundColor: (t) => alpha(t.palette.warning.main, 0.12), borderTop: '1px solid', borderColor: (t) => alpha(t.palette.warning.main, 0.4) }}>
            <Typography sx={{ fontSize: 13, flex: 1, color: 'warning.main' }}>
              This turn&apos;s end was never confirmed. The agent may still be running — anything it
              produced since the connection dropped will not appear here.
            </Typography>
            {onNudge && (
              <Button variant="contained" color="info" size="small" onClick={onNudge} sx={{ textTransform: 'none', fontSize: 12, whiteSpace: 'nowrap' }}>
                Resume
              </Button>
            )}
            <Button variant="outlined" color="info" size="small" onClick={onCancel} sx={{ textTransform: 'none', fontSize: 12, whiteSpace: 'nowrap' }}>
              Stop
            </Button>
          </Box>
        )}

        {/* Input */}
        {!readOnly && (
          <Box
            component="form"
            onSubmit={handleSubmit}
            onDragOver={(e: React.DragEvent) => { e.preventDefault(); e.stopPropagation() }}
            onDrop={(e: React.DragEvent) => {
              e.preventDefault()
              e.stopPropagation()
              if (e.dataTransfer.files.length > 0) {
                fileAttachments.addFiles(e.dataTransfer.files)
              }
            }}
            sx={{ p: '12px 16px', borderTop: '1px solid', borderColor: 'divider', display: 'flex', flexDirection: 'column', gap: 1 }}
          >
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center' }}>
              {/* Model toggle (only shown when 2 models provided) */}
              {hasModelToggle && (
                <Box sx={{ display: 'flex', alignItems: 'center', flexShrink: 0 }}>
                  <Typography sx={{ fontSize: 13, fontWeight: selectedModel === models[0].id ? 600 : 400, color: selectedModel === models[0].id ? 'text.primary' : 'text.disabled' }}>
                    {models[0].label}
                  </Typography>
                  <Switch
                    size="small"
                    checked={selectedModel === models[1].id}
                    onChange={(_e, checked) => onModelChange(checked ? models[1].id : models[0].id)}
                    disabled={isStreaming}
                    sx={{ mx: 0.5 }}
                  />
                  <Typography sx={{ fontSize: 13, fontWeight: selectedModel === models[1].id ? 600 : 400, color: selectedModel === models[1].id ? 'text.primary' : 'text.disabled' }}>
                    {models[1].label}
                  </Typography>
                </Box>
              )}
              <ChatInputToolbar
                onFilesSelected={files => fileAttachments.addFiles(files)}
                attachments={fileAttachments.attachments}
                onRemoveAttachment={fileAttachments.removeFile}
                isRecording={voiceDictation.isRecording}
                isTranscribing={voiceDictation.isTranscribing}
                onToggleRecording={handleToggleRecording}
                onStopRecording={voiceDictation.stopRecording}
                onCancelRecording={voiceDictation.cancelRecording}
                stream={voiceDictation.stream}
                error={voiceDictation.error}
                disabled={isStreaming}
                devices={voiceDictation.devices}
                selectedDeviceId={voiceDictation.selectedDeviceId}
                onSelectDevice={voiceDictation.selectDevice}
              />
            </Box>
            <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-end' }}>
              <Box
                ref={textareaRef}
                component="textarea"
                value={input}
                onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                onPaste={(e: React.ClipboardEvent) => fileAttachments.handlePaste(e.nativeEvent)}
                placeholder={noSession ? 'No session is open — start a new session or pick one to chat.' : 'Type a message...'}
                rows={1}
                disabled={isStreaming || noSession}
                data-testid="chat-input"
                sx={{
                  flex: 1,
                  // 🔴 `minWidth: 0` OR THE COMPOSER CANNOT NARROW AT ALL.
                  //
                  // A flex item defaults to `min-width: auto`, which is its
                  // MIN-CONTENT width — and a bare `<textarea>`'s min-content
                  // is its `cols` default, about 522px at this font size. So
                  // `flex: 1` was a lie: the textarea could grow but never
                  // shrink, and the whole composer had a hard floor of ~628px.
                  //
                  // Measured in a real browser inside the embed iframe at a
                  // 459px rail: document scrollWidth 628 vs clientWidth 459.
                  // The overflow pushed Send off the right edge and put a
                  // horizontal scrollbar under a chat panel, which is how this
                  // was reported. It is invisible in the full-width console
                  // because 628px always fitted there.
                  minWidth: 0,
                  p: '8px 12px',
                  border: '1px solid', borderColor: 'divider',
                  borderRadius: '8px',
                  resize: 'none',
                  fontSize: 14,
                  fontFamily: 'inherit',
                  outline: 'none',
                  '&:focus': { borderColor: 'primary.main' },
                }}
              />
              {isStreaming ? (
                <Button type="button" aria-label="Stop" onClick={onCancel} variant="contained" color="info" sx={{ borderRadius: '8px', textTransform: 'none' }}>
                  Stop
                </Button>
              ) : (
                <Button type="submit" aria-label="Send" disabled={!input.trim() || noSession} variant="contained" color="info" sx={{ borderRadius: '8px', textTransform: 'none' }}>
                  Send
                </Button>
              )}
            </Box>
          </Box>
        )}
      </Box>

      {/* Artifact Panel — a column when there is room, an overlay when narrow */}
      {!readOnly && narrow && !artifactsOpen && (artifacts.length > 0 || (todos?.length ?? 0) > 0) && (
        <Chip
          data-testid="artifact-panel-open"
          label={artifacts.length > 0 ? `Artifacts (${artifacts.length})` : `Tasks (${todos?.length ?? 0})`}
          onClick={() => setArtifactsOpen(true)}
          size="small"
          sx={{ position: 'absolute', top: 8, right: 16, zIndex: 1, boxShadow: 2, bgcolor: 'background.paper' }}
        />
      )}
      {!readOnly && (!narrow || artifactsOpen) && (
        <ArtifactPanel
          overlay={narrow}
          onClose={() => setArtifactsOpen(false)}
          artifacts={artifacts}
          todos={todos}
          sessionId={sessionId}
          onPinToDashboard={onPinToDashboard}
          onArtifactClick={(a) => {
            setViewerArtifact(a)
            if (onOpenArtifactViewer) onOpenArtifactViewer(a)
          }}
          onViewAll={() => {
            if (onOpenArtifactViewer) onOpenArtifactViewer(null)
          }}
          apiBaseUrl={apiBaseUrl}
          authHeader={authHeader}
        />
      )}
    </Box>
  )
}
