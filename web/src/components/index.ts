// TIER 2 of the tiered-reuse split — `@agentkit/chat-ui/components`.
//
// The presentational components: props in, callbacks out, styled by the HOST's
// ThemeProvider. That last property is the whole argument for sharing them —
// an iframe can never be themed by its embedder
// (design/2026-08-24-agent-wolf-ui.md § 1).
//
// WHAT IS DELIBERATELY NOT HERE. The eight components that carry Bob's API
// contract and its fetch behaviour stay tier 3, reachable only from the root
// entry point and normally consumed through the embed page instead:
//
//   AgentChat · AgentSessionList · ArtifactViewer · ArtifactPreviewDialog
//   InlineArtifactPreview · WorkersPage · WorkerChatPanel · ProjectSettingsPage
//
// They are the only tier where a Bob change forces every client app to
// move. Adding one of them to this file collapses the tier line, so don't.
//
// Everything here is ALSO exported from the root entry point.
export {
  FeedWaterline,
  NewItemsPill,
  PauseLiveUpdates,
  useAtHead,
  useDefaultPaused,
  PILL_DEBOUNCE_MS,
} from './FeedLiveness.js'

export type {
  FeedWaterlineProps,
  NewItemsPillProps,
  PauseLiveUpdatesProps,
} from './FeedLiveness.js'

export { default as ActivityPage } from './ActivityPage.js'

export type { ActivityPageProps } from './ActivityPage.js'

export { ACTIVITY_LENS_LABELS, ACTIVITY_WINDOW_STEP_MS } from './ActivityPage.js'

export { default as WorkerHistory, WORKER_HISTORY_SURFACE } from './WorkerHistory.js'

export type { WorkerHistoryProps, HistoryVersion } from './WorkerHistory.js'

export { default as WorkerTriggers, WokenBy } from './WorkerTriggers.js'

export type { WorkerTriggersProps } from './WorkerTriggers.js'

export { default as DeskPage } from './DeskPage.js'

export type { DeskPageProps } from './DeskPage.js'

export {
  default as OrgChartPage,
  stateLine,
  HALTED_CLOCK_SENTENCE,
  WIRE_EVENT_TYPE,
  wireFilter,
  wireSentence,
  cutWireTitle,
  toggleTitle,
  toggleSentence,
} from './OrgChartPage.js'

export type { OrgChartPageProps, WireProposal } from './OrgChartPage.js'

export { default as MemoryBrowserPage } from './MemoryBrowserPage.js'

export type { MemoryBrowserPageProps } from './MemoryBrowserPage.js'

export { default as BriefingPreview } from './BriefingPreview.js'

export type { BriefingPreviewProps } from './BriefingPreview.js'

export { default as ChatHistoryDrawer, DRAWER_WIDTH, STORAGE_KEY } from './ChatHistoryDrawer.js'

export { default as AgentMarkdown } from './AgentMarkdown.js'

export { default as ArtifactPanel } from './ArtifactPanel.js'

export { default as AskUserCard } from './AskUserCard.js'

export { default as ChatInputToolbar } from './ChatInputToolbar.js'

export { default as CodeCreatedBlock } from './CodeCreatedBlock.js'

export { default as RecordingOverlay } from './RecordingOverlay.js'

export { default as ScriptExecutionBlock } from './ScriptExecutionBlock.js'

export { default as ThinkingBlock } from './ThinkingBlock.js'

export {
  default as ToolCallGroup,
  tryParseImageOutput,
  isImageToolCall,
  isImageReadToolCall,
  isScreenshotToolCall,
} from './ToolCallGroup.js'

export { default as JsonObjectEditor } from './JsonObjectEditor.js'

export type { JsonObjectEditorProps } from './JsonObjectEditor.js'

export { default as WorkerLineage } from './WorkerLineage.js'

export type { WorkerLineageProps, LineageVersion } from './WorkerLineage.js'

export { default as BeforeAfterView } from './BeforeAfterView.js'

export type { BeforeAfterViewProps } from './BeforeAfterView.js'

export { default as WorkerPromptVersion, restoreRationale } from './WorkerPromptVersion.js'

export type { WorkerPromptVersionProps } from './WorkerPromptVersion.js'

export { default as WorkerList } from './WorkerList.js'

export type { WorkerListProps } from './WorkerList.js'

export { default as WorkerEditor } from './WorkerEditor.js'

export type { WorkerEditorProps } from './WorkerEditor.js'

export { default as CreateProjectForm } from './CreateProjectForm.js'

export type { CreateProjectFormProps } from './CreateProjectForm.js'

export { default as WorkerJobHistory } from './WorkerJobHistory.js'

export type { WorkerJobHistoryProps } from './WorkerJobHistory.js'

export { default as TopologyOnboarding } from './TopologyOnboarding.js'

export type { TopologyOnboardingProps } from './TopologyOnboarding.js'

export { default as EventsPage, EVENTS_SURFACE } from './EventsPage.js'

export type { EventsPageProps } from './EventsPage.js'

export { default as EventList } from './EventList.js'

export type { EventListProps } from './EventList.js'

export { default as EventDetail } from './EventDetail.js'

export type { EventDetailProps } from './EventDetail.js'

export { default as EventJobHistory, statusChipColor } from './EventJobHistory.js'

export type { EventJobHistoryProps } from './EventJobHistory.js'

export { default as DeliveryStatusChip, attentionColor } from './DeliveryStatusChip.js'

export type { DeliveryStatusChipProps } from './DeliveryStatusChip.js'

export {
  default as CredentialModeBadge,
  CREDENTIAL_MODES,
  isCredentialMode,
} from './CredentialModeBadge.js'

export type {
  CredentialModeBadgeProps,
  CredentialMode,
} from './CredentialModeBadge.js'

export { default as EventReplayPanel } from './EventReplayPanel.js'

export { default as EmitEventControl } from './EmitEventControl.js'

export type { EmitEventControlProps } from './EmitEventControl.js'

export type { EventReplayPanelProps } from './EventReplayPanel.js'

export { default as ChangelogView, ACTION_FILTERS, DiffBlock } from './ChangelogView.js'

export type { ChangelogViewProps, DiffBlockProps } from './ChangelogView.js'

export {
  default as BenchReportView,
  TIER_A_BANNER,
  DEDUPE_NOTE,
  SPREAD_ALARM,
} from './BenchReportView.js'

export type { BenchReportViewProps } from './BenchReportView.js'

export { default as AutomationPage } from './AutomationPage.js'

export type { AutomationPageProps, AutomationTab } from './AutomationPage.js'

export { default as SubscriptionEditor } from './SubscriptionEditor.js'

export type { SubscriptionEditorProps } from './SubscriptionEditor.js'

export { default as ScheduleEditor } from './ScheduleEditor.js'

export type { ScheduleEditorProps } from './ScheduleEditor.js'

export { default as NlAssistField } from './NlAssistField.js'

export type { NlAssistFieldProps } from './NlAssistField.js'

export { default as ArtifactCodePreview } from './ArtifactCodePreview.js'

export { default as ArtifactCsvPreview } from './ArtifactCsvPreview.js'

export { default as ArtifactLightbox } from './ArtifactLightbox.js'

export { default as ArtifactGrid } from './ArtifactGrid.js'

export { default as ArtifactTreeView } from './ArtifactTreeView.js'

export { default as CharterPanel } from './CharterPanel.js'

export type { CharterPanelProps } from './CharterPanel.js'

export { default as RunArchitectControl, ARCHITECT_RUN_EVENT } from './RunArchitectControl.js'

export type { RunArchitectControlProps } from './RunArchitectControl.js'

export { default as OnboardingPage } from './OnboardingPage.js'

export type { OnboardingPageProps } from './OnboardingPage.js'

export { default as BudgetPanel } from './BudgetPanel.js'

export type { BudgetPanelProps } from './BudgetPanel.js'
