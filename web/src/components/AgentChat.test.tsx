// @vitest-environment jsdom
import React from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import { test, expect, vi } from 'vitest'
import { AgentChatProvider } from '../AgentChatProvider.js'
import AgentChat from './AgentChat.js'
import type { AgentMessage } from '../types.js'

test('AgentChat renders from provider context', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat onSendMessage={() => {}} />
    </AgentChatProvider>
  )
  expect(screen.getByPlaceholderText(/type a message/i)).toBeInTheDocument()
})

// The provider's sendMessage returns silently with no current session, so an
// enabled composer there typed into nothing (2026-09-13: the Chat view opened
// from the Desk, and the onboarding rail before it resumed its session).
test('AgentChat disables the composer and says why when the provider has no session', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat />
    </AgentChatProvider>
  )
  const input = screen.getByTestId('chat-input')
  expect(input).toBeDisabled()
  expect(input).toHaveAttribute('placeholder', expect.stringMatching(/no session is open/i))
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
})

// ---------------------------------------------------------------------------
// messages render
// ---------------------------------------------------------------------------

// docs/19-embedding.md §3a: an application's instructions arrive as a user
// message; marked, they are one collapsed line and the person's words stay theirs.
test('AgentChat shows a marked context block as one collapsed line, then the words after it', () => {
  const messages: AgentMessage[] = [
    {
      id: 'u1',
      role: 'user',
      content: '<agent-context summary="Opened from the hypothesis page">\nLabel every candidate memory name: x\n</agent-context>\nIs gold a hedge?',
      timestamp: '2024-01-01T00:00:00Z',
    },
  ]
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={messages} />
    </AgentChatProvider>
  )
  const line = screen.getByTestId('agent-context')
  expect(line).toHaveTextContent('Context sent to the agent: Opened from the hypothesis page')
  expect(screen.queryByText(/Label every candidate memory/)).toBeNull()
  expect(screen.getByText('Is gold a hedge?')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Show' }))
  expect(screen.getByTestId('agent-context-body')).toHaveTextContent('Label every candidate memory name: x')
})

test('AgentChat renders user and assistant messages', () => {
  const messages: AgentMessage[] = [
    { id: 'm1', role: 'user', content: 'Hello agent!', timestamp: '2024-01-01T00:00:00Z' },
    { id: 'm2', role: 'assistant', content: 'Hello! How can I help?', timestamp: '2024-01-01T00:00:01Z' },
  ]
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={messages} />
    </AgentChatProvider>
  )
  expect(screen.getByText('Hello agent!')).toBeInTheDocument()
  expect(screen.getByText('Hello! How can I help?')).toBeInTheDocument()
})

test('AgentChat renders multiple messages in order', () => {
  const messages: AgentMessage[] = [
    { id: 'm1', role: 'user', content: 'First message', timestamp: '2024-01-01T00:00:00Z' },
    { id: 'm2', role: 'assistant', content: 'Second message', timestamp: '2024-01-01T00:00:01Z' },
    { id: 'm3', role: 'user', content: 'Third message', timestamp: '2024-01-01T00:00:02Z' },
  ]
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={messages} />
    </AgentChatProvider>
  )
  expect(screen.getByText('First message')).toBeInTheDocument()
  expect(screen.getByText('Third message')).toBeInTheDocument()
})

test('AgentChat assigns data-role to message boxes', () => {
  const messages: AgentMessage[] = [
    { id: 'u1', role: 'user', content: 'My question', timestamp: '2024-01-01T00:00:00Z' },
    { id: 'a1', role: 'assistant', content: 'My answer', timestamp: '2024-01-01T00:00:01Z' },
  ]
  const { container } = render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={messages} />
    </AgentChatProvider>
  )
  expect(container.querySelector('[data-role="user"]')).not.toBeNull()
  expect(container.querySelector('[data-role="assistant"]')).not.toBeNull()
})

// ---------------------------------------------------------------------------
// error state displays
// ---------------------------------------------------------------------------

test('AgentChat displays error alert when error prop is set', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat error="Connection failed" />
    </AgentChatProvider>
  )
  expect(screen.getByText('Connection failed')).toBeInTheDocument()
  // MUI Alert renders with role="alert" when severity is set
  expect(screen.getByRole('alert')).toBeInTheDocument()
})

test('AgentChat does not show error alert when error is null', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat error={null} />
    </AgentChatProvider>
  )
  expect(screen.queryByRole('alert')).toBeNull()
})

test('AgentChat shows error alongside messages', () => {
  const messages: AgentMessage[] = [
    { id: 'm1', role: 'user', content: 'A message', timestamp: '2024-01-01T00:00:00Z' },
  ]
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={messages} error="Something went wrong" />
    </AgentChatProvider>
  )
  expect(screen.getByText('A message')).toBeInTheDocument()
  expect(screen.getByText('Something went wrong')).toBeInTheDocument()
})

// ---------------------------------------------------------------------------
// empty session placeholder
// ---------------------------------------------------------------------------

test('AgentChat shows input placeholder when no messages', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={[]} onSendMessage={() => {}} />
    </AgentChatProvider>
  )
  expect(screen.getByPlaceholderText(/type a message/i)).toBeInTheDocument()
})

test('AgentChat renders no message bubbles when messages is empty', () => {
  const { container } = render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={[]} />
    </AgentChatProvider>
  )
  // No data-role elements should be present when there are no messages
  expect(container.querySelector('[data-role]')).toBeNull()
})

test('AgentChat shows Send button in empty state', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={[]} />
    </AgentChatProvider>
  )
  expect(screen.getByRole('button', { name: /send/i })).toBeInTheDocument()
})

// ---------------------------------------------------------------------------
// G3 — chat empty state (design 2026-09-11-onboarding-and-the-guide.md §3)
// ---------------------------------------------------------------------------

test('AgentChat empty state (base chat) names the base agent and shows three suggestions', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={[]} />
    </AgentChatProvider>
  )
  expect(screen.getByText('This is a chat with the base agent.')).toBeInTheDocument()
  expect(screen.getByText(/gets none of the project.s briefing/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'What is in this project’s memory?' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Which workers exist and what wakes them?' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Write a memory that records …' })).toBeInTheDocument()
})

test('AgentChat empty state (worker chat) names the worker and shows worker suggestions', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={[]} workerName="email-answerer" />
    </AgentChatProvider>
  )
  expect(screen.getByText('This is a chat with email-answerer.')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Show me your instructions.' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'What did you do last time you ran?' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'What would you do if I sent you: …' })).toBeInTheDocument()
})

test('clicking an empty-state suggestion fills the composer without sending it', () => {
  const onSendMessage = vi.fn()
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={[]} onSendMessage={onSendMessage} />
    </AgentChatProvider>
  )
  fireEvent.click(screen.getByRole('button', { name: 'Which workers exist and what wakes them?' }))
  expect(screen.getByPlaceholderText(/type a message/i)).toHaveValue('Which workers exist and what wakes them?')
  expect(onSendMessage).not.toHaveBeenCalled()
})

test('the empty state disappears once a message has been sent', () => {
  const messages: AgentMessage[] = [
    { id: 'm1', role: 'user', content: 'hi', timestamp: '2024-01-01T00:00:00Z' },
  ]
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat messages={messages} />
    </AgentChatProvider>
  )
  expect(screen.queryByTestId('chat-empty-state')).toBeNull()
})

// ---------------------------------------------------------------------------
// stuck detection reaches a pixel (item B5)
//
// B1 left the stuck detector armed when a stream ends without query_complete
// and the status probe cannot confirm the turn finished. On that path
// isStreaming is false — it must be, or the composer is disabled forever
// (item P2) — so the two isStreaming-gated banners can never render it.
// ---------------------------------------------------------------------------

test('AgentChat warns when a turn ended unconfirmed (detector armed, not streaming)', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat isStreaming={false} stuckStatus="likely_stuck" onSendMessage={() => {}} />
    </AgentChatProvider>
  )
  expect(screen.getByTestId('unconfirmed-end-banner')).toBeInTheDocument()
  expect(screen.getByText(/never confirmed/i)).toBeInTheDocument()
  // The composer must stay usable — this is the whole reason isStreaming is
  // false on this path, and re-disabling it would reintroduce P2.
  expect(screen.getByPlaceholderText(/type a message/i)).not.toBeDisabled()
})

test('AgentChat warns on possibly_stuck after an unconfirmed end too', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat isStreaming={false} stuckStatus="possibly_stuck" />
    </AgentChatProvider>
  )
  expect(screen.getByTestId('unconfirmed-end-banner')).toBeInTheDocument()
})

test('AgentChat offers Stop and Resume on an unconfirmed end', () => {
  const onCancel = vi.fn()
  const onNudge = vi.fn()
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat isStreaming={false} stuckStatus="likely_stuck" onCancel={onCancel} onNudge={onNudge} />
    </AgentChatProvider>
  )
  fireEvent.click(screen.getByRole('button', { name: /resume/i }))
  expect(onNudge).toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: /^stop$/i }))
  expect(onCancel).toHaveBeenCalled()
})

test('AgentChat shows no unconfirmed-end banner when the detector is disarmed', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat isStreaming={false} stuckStatus="ok" />
    </AgentChatProvider>
  )
  expect(screen.queryByTestId('unconfirmed-end-banner')).toBeNull()
})

test('AgentChat shows no unconfirmed-end banner when stuckStatus is absent', () => {
  // Guards the undefined case: `stuckStatus !== "ok"` would render the banner
  // on every prop-only mount that never wires the detector.
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat isStreaming={false} />
    </AgentChatProvider>
  )
  expect(screen.queryByTestId('unconfirmed-end-banner')).toBeNull()
})

test('AgentChat shows the streaming stuck banner, not the unconfirmed-end one, while streaming', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat isStreaming={true} stuckStatus="likely_stuck" />
    </AgentChatProvider>
  )
  expect(screen.getByText(/appears to be stuck/i)).toBeInTheDocument()
  expect(screen.queryByTestId('unconfirmed-end-banner')).toBeNull()
})

test('AgentChat hides the unconfirmed-end banner in readOnly replay', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat readOnly={true} isStreaming={false} stuckStatus="likely_stuck" />
    </AgentChatProvider>
  )
  expect(screen.queryByTestId('unconfirmed-end-banner')).toBeNull()
})

// ---------------------------------------------------------------------------
// readOnly mode
// ---------------------------------------------------------------------------

test('AgentChat hides input when readOnly is true', () => {
  render(
    <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
      <AgentChat readOnly={true} />
    </AgentChatProvider>
  )
  expect(screen.queryByPlaceholderText(/type a message/i)).toBeNull()
  expect(screen.queryByRole('button', { name: /send/i })).toBeNull()
})

// ---------------------------------------------------------------------------
// narrow host: the artifacts panel is an overlay, not a 320px column (2026-09-12)
// ---------------------------------------------------------------------------

test('AgentChat below 900px hides the artifacts column behind a button and opens it as an overlay', () => {
  const original = window.matchMedia
  window.matchMedia = ((query: string) => ({
    matches: query.includes('max-width:899.95px'),
    media: query, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
  try {
    render(
      <AgentChatProvider config={{ apiBaseUrl: '', models: [{ id: 'm', label: 'M' }] }}>
        <AgentChat artifacts={[{ filePath: 'report.html', fileName: 'report.html', label: 'report', artifactType: 'report', source: 'registered', status: 'extracted', id: 'a1' }]} />
      </AgentChatProvider>
    )
    expect(screen.queryByTestId('artifact-panel')).toBeNull()
    fireEvent.click(screen.getByTestId('artifact-panel-open'))
    expect(screen.getByTestId('artifact-panel')).toHaveAttribute('data-overlay', 'true')
    fireEvent.click(screen.getByTestId('artifact-panel-close'))
    expect(screen.queryByTestId('artifact-panel')).toBeNull()
  } finally {
    window.matchMedia = original
  }
})
