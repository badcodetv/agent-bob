// @vitest-environment jsdom
// The session list's rows, as a person reads them. A project's onboarding
// interview used to read "Untitled · Unknown · agent": no title (its first
// message is instructions, not something to title), a container state nobody
// had polled, and a workflow id that is "agent" on every session.

import React from 'react'
import { render, screen, within } from '@testing-library/react'
import { describe, it, expect } from 'vitest'
import ChatHistoryDrawer, { getSessionAgentLabel, getSessionTitle } from './ChatHistoryDrawer.js'
import type { AgentSessionListItem } from '../types.js'

function row(over: Partial<AgentSessionListItem>): AgentSessionListItem {
  return {
    id: 's1',
    created_at: Date.now() / 1000,
    updated_at: Date.now() / 1000,
    user_email: 'test@example.com',
    customer: 'acme',
    job: '',
    workflow_id: 'agent',
    persona: '',
    status: 'active',
    current_node: '',
    title: '',
    artifact_count: 0,
    ...over,
  }
}

describe('getSessionTitle', () => {
  it('prefers the title, then names the interview, then any name, then Untitled', () => {
    expect(getSessionTitle(row({ title: 'Plan the launch', name: 'onboard' }))).toBe('Plan the launch')
    expect(getSessionTitle(row({ name: 'onboard' }))).toBe('Onboarding interview')
    expect(getSessionTitle(row({ name: 'support-bot' }))).toBe('support-bot')
    expect(getSessionTitle(row({}))).toBe('Untitled')
  })
})

describe('getSessionAgentLabel', () => {
  it('names the worker or persona, and hides the default workflow id', () => {
    expect(getSessionAgentLabel(row({ worker: 'architect', persona: 'architect' }))).toBe('architect')
    expect(getSessionAgentLabel(row({ persona: 'interviewer' }))).toBe('interviewer')
    expect(getSessionAgentLabel(row({}))).toBe('')
    expect(getSessionAgentLabel(row({ workflow_id: 'triage' }))).toBe('triage')
  })
})

describe('ChatHistoryDrawer rows', () => {
  it('shows the interview as a titled row without "Unknown" or "agent"', () => {
    render(
      <ChatHistoryDrawer
        open
        onClose={() => {}}
        onSelectSession={() => {}}
        sessions={[row({ name: 'onboard', persona: 'interviewer' })]}
      />,
    )
    const r = screen.getByTestId('session-row')
    expect(within(r).getByText('Onboarding interview')).toBeTruthy()
    expect(within(r).getByText('interviewer')).toBeTruthy()
    expect(within(r).queryByText('Unknown')).toBeNull()
    expect(within(r).queryByText('agent')).toBeNull()
  })

  it('still shows a container state it knows', () => {
    render(
      <ChatHistoryDrawer open onClose={() => {}} onSelectSession={() => {}} sessions={[row({ container_state: 'running' })]} />,
    )
    expect(within(screen.getByTestId('session-row')).getByText('Running')).toBeTruthy()
  })
})
