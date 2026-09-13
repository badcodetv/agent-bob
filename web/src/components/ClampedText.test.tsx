// @vitest-environment jsdom
// Model-written markdown: a stripped preview while collapsed, the rendered
// markdown once the reader asks for all of it.

import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, it, expect } from 'vitest'
import ClampedText from './ClampedText.js'

const LONG = `**Written:** one memory, \`kind=rolling-summary\`.\n${'More detail about the handover. '.repeat(30)}`

describe('ClampedText', () => {
  it('shows a collapsed markdown preview without the marks', () => {
    render(<ClampedText text={LONG} markdown />)
    const preview = screen.getByText(/Written: one memory, kind=rolling-summary\./)
    expect(preview.textContent).not.toMatch(/\*\*|`/)
    expect(screen.queryByTestId('clamped-markdown')).toBeNull()
  })

  it('renders the whole text as markdown on Show all', async () => {
    const { container } = render(<ClampedText text={LONG} markdown />)
    await userEvent.click(screen.getByRole('button', { name: 'Show all' }))
    expect(screen.getByTestId('clamped-markdown')).toBeInTheDocument()
    expect(container.querySelector('strong')?.textContent).toBe('Written:')
    expect(container.querySelector('code')?.textContent).toBe('kind=rolling-summary')
  })

  it('renders a short markdown text as markdown straight away', () => {
    const { container } = render(<ClampedText text="Made **one** change." markdown />)
    expect(container.querySelector('strong')?.textContent).toBe('one')
    expect(screen.queryByRole('button', { name: 'Show all' })).toBeNull()
  })

  it('leaves plain text alone when not marked as markdown', () => {
    render(<ClampedText text="Keep **these** marks." />)
    expect(screen.getByText('Keep **these** marks.')).toBeInTheDocument()
  })
})
