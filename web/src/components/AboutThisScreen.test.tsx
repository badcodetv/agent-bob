// @vitest-environment jsdom
// C2 — the disclosure itself. What matters here is the invariant the whole
// ticket rests on (no paragraph ⇒ no chrome at all), that dismissal is scoped
// to (surface, project) rather than global, and that opening it links
// somewhere real.

import React from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect, afterEach } from 'vitest'
import AboutThisScreen from './AboutThisScreen.js'
import { GuideProvider, type GuideParagraphMap } from '../guide/GuideProvider.js'

afterEach(() => {
  globalThis.localStorage?.clear()
})

const PARAGRAPHS: GuideParagraphMap = {
  desk: { slug: 'the-desk', text: 'The Desk is where you start every day.' },
  chat: { slug: 'talking-to-a-worker', text: 'A chat is not a job.' },
}

function renderAbout(
  surface: Parameters<typeof AboutThisScreen>[0]['surface'],
  projectId = 'acme',
  paragraphs: GuideParagraphMap = PARAGRAPHS,
  alwaysExpanded = false,
) {
  return render(
    <GuideProvider paragraphs={paragraphs}>
      <AboutThisScreen surface={surface} projectId={projectId} alwaysExpanded={alwaysExpanded} />
    </GuideProvider>,
  )
}

describe('no paragraph', () => {
  it('renders nothing at all — no chevron, no empty box', () => {
    const { container } = renderAbout('settings', 'acme', {})
    expect(container).toBeEmptyDOMElement()
  })

  it('renders nothing with no GuideProvider mounted either', () => {
    const { container } = render(<AboutThisScreen surface="desk" projectId="acme" />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('collapsed / open', () => {
  it('starts collapsed, showing only the toggle line', () => {
    renderAbout('desk')
    expect(screen.getByTestId('about-toggle-desk')).toHaveTextContent('About this screen')
    expect(screen.queryByText('The Desk is where you start every day.')).not.toBeInTheDocument()
  })

  it('opens on click, showing the paragraph and a guide link', async () => {
    const user = userEvent.setup()
    renderAbout('desk')
    await user.click(screen.getByTestId('about-toggle-desk'))
    expect(screen.getByText('The Desk is where you start every day.')).toBeInTheDocument()
    const link = screen.getByText('Read more in the guide →')
    expect(link.closest('a')).toHaveAttribute('href', '#/guide/the-desk')
  })
})

describe('dismissal', () => {
  it('is per surface, not global', async () => {
    const user = userEvent.setup()
    renderAbout('desk')
    await user.click(screen.getByTestId('about-toggle-desk'))
    await user.click(screen.getByTestId('about-dismiss-desk'))
    expect(screen.getByTestId('about-restore-desk')).toBeInTheDocument()

    // A sibling surface on the same project is unaffected.
    const { getByTestId: getChat } = renderAbout('chat')
    expect(getChat('about-toggle-chat')).toBeInTheDocument()
  })

  it('is per project, not global', async () => {
    const user = userEvent.setup()
    renderAbout('desk', 'acme')
    await user.click(screen.getByTestId('about-toggle-desk'))
    await user.click(screen.getByTestId('about-dismiss-desk'))

    const { getByTestId } = renderAbout('desk', 'other-project')
    expect(getByTestId('about-toggle-desk')).toBeInTheDocument()
  })

  it('survives a remount (sticky in localStorage)', async () => {
    const user = userEvent.setup()
    const { unmount } = renderAbout('desk')
    await user.click(screen.getByTestId('about-toggle-desk'))
    await user.click(screen.getByTestId('about-dismiss-desk'))
    unmount()

    renderAbout('desk')
    expect(screen.getByTestId('about-restore-desk')).toBeInTheDocument()
  })

  it('a restore brings the full disclosure back, collapsed', async () => {
    const user = userEvent.setup()
    renderAbout('desk')
    await user.click(screen.getByTestId('about-toggle-desk'))
    await user.click(screen.getByTestId('about-dismiss-desk'))
    await user.click(screen.getByTestId('about-restore-desk'))
    expect(screen.getByTestId('about-toggle-desk')).toBeInTheDocument()
    expect(screen.queryByText('The Desk is where you start every day.')).not.toBeInTheDocument()
  })
})

describe('alwaysExpanded (project-create)', () => {
  it('renders the paragraph with no chevron and no dismiss control', () => {
    renderAbout('project-create', '', { 'project-create': { slug: 'x', text: 'One sentence.' } }, true)
    expect(screen.getByTestId('about-project-create')).toHaveTextContent('One sentence.')
    expect(screen.queryByTestId('about-toggle-project-create')).not.toBeInTheDocument()
    expect(screen.queryByText('Dismiss')).not.toBeInTheDocument()
  })

  it('renders nothing when project-create has no paragraph', () => {
    const { container } = renderAbout('project-create', '', {}, true)
    expect(container).toBeEmptyDOMElement()
  })
})
