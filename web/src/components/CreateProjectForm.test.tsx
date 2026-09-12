// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import CreateProjectForm from './CreateProjectForm.js'

describe('CreateProjectForm (G6)', () => {
  it('shows the sentence about what creating a project does', () => {
    render(<CreateProjectForm onCreate={vi.fn()} />)
    expect(
      screen.getByText(/Creating a project starts an interview/),
    ).toBeInTheDocument()
  })

  it('keeps Create project disabled until both fields are filled', async () => {
    const user = userEvent.setup()
    render(<CreateProjectForm onCreate={vi.fn()} />)
    const button = screen.getByTestId('new-project-create')
    expect(button).toBeDisabled()

    await user.type(screen.getByTestId('new-project-input'), 'apples-oranges')
    expect(button).toBeDisabled()

    await user.type(screen.getByTestId('new-project-goal'), 'sell more apples')
    expect(button).toBeEnabled()
  })

  it('calls onCreate with the trimmed id and goal, then clears the fields', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockResolvedValue(undefined)
    const onCreated = vi.fn()
    render(<CreateProjectForm onCreate={onCreate} onCreated={onCreated} />)

    await user.type(screen.getByTestId('new-project-input'), '  apples-oranges  ')
    await user.type(screen.getByTestId('new-project-goal'), '  sell more apples  ')
    await user.click(screen.getByTestId('new-project-create'))

    await waitFor(() => expect(onCreate).toHaveBeenCalledWith('apples-oranges', 'sell more apples'))
    await waitFor(() => expect(onCreated).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByTestId('new-project-input')).toHaveValue(''))
  })

  it('shows the error inline and keeps the fields when onCreate rejects', async () => {
    const user = userEvent.setup()
    const onCreate = vi.fn().mockRejectedValue(new Error('project id already taken'))
    render(<CreateProjectForm onCreate={onCreate} />)

    await user.type(screen.getByTestId('new-project-input'), 'apples-oranges')
    await user.type(screen.getByTestId('new-project-goal'), 'sell more apples')
    await user.click(screen.getByTestId('new-project-create'))

    expect(await screen.findByText('project id already taken')).toBeInTheDocument()
    expect(screen.getByTestId('new-project-input')).toHaveValue('apples-oranges')
  })

  it('renders a Cancel button only when onCancel is given', () => {
    const { rerender } = render(<CreateProjectForm onCreate={vi.fn()} />)
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()

    rerender(<CreateProjectForm onCreate={vi.fn()} onCancel={vi.fn()} />)
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  })
})
