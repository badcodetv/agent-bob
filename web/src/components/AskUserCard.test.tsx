// @vitest-environment jsdom
//
// AskUserCard — and the whole `ask_user` path, end to end, in one file.
//
// The interesting test here is the last one: it takes the bytes the sandbox
// builtin `ask_user` really emits, runs them through the REAL reducer, and
// renders the REAL card from whatever comes out. Every unit on that path was
// already tested in isolation and the path as a whole was not, which is how
// it went unnoticed that a question with no options could not be asked at
// all (sandbox/src/tools/builtin/ask_user.ts used to require two).
//
// The failure this guards is silent in both directions: a marker the reducer
// ignores produces no card and no error, and a card built from an `options`
// that is `undefined` throws inside React and blanks the chat panel rather
// than dropping one message.
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import React from 'react'
import AskUserCard from './AskUserCard.js'
import { agentEventReducer, initialAgentEventState } from '../agentEventReducer.js'
import { buildAskUserPayload } from '../../../sandbox/src/tools/builtin/ask_user.js'
import type { AskUserQuestionInfo } from '../types.js'

function question(overrides: Partial<AskUserQuestionInfo> = {}): AskUserQuestionInfo {
  return {
    question: 'Which metric decides this?',
    options: [
      { label: 'Gold', value: 'gold' },
      { label: 'Bitcoin', value: 'btc' },
    ],
    allowFreetext: false,
    context: '',
    toolCallId: 'tc-1',
    answered: false,
    ...overrides,
  }
}

const makeEvent = (type: string, data: unknown) =>
  ({ type, data, timestamp: '2026-09-07T00:00:00Z' }) as never

describe('AskUserCard', () => {
  it('renders the question and one button per option', () => {
    render(<AskUserCard question={question()} onAnswer={() => {}} disabled={false} />)
    expect(screen.getByText('Which metric decides this?')).toBeTruthy()
    expect(screen.getByRole('button', { name: /Gold/ })).toBeTruthy()
    expect(screen.getByRole('button', { name: /Bitcoin/ })).toBeTruthy()
  })

  it('answers with the option VALUE, not its label', () => {
    // The value is what gets sent as the user's next message, so a card that
    // sent labels would put button text into the transcript instead of the
    // answer the tool author intended.
    const onAnswer = vi.fn()
    render(<AskUserCard question={question()} onAnswer={onAnswer} disabled={false} />)
    fireEvent.click(screen.getByRole('button', { name: /Bitcoin/ }))
    expect(onAnswer).toHaveBeenCalledWith('btc')
  })

  it('shows the context line when there is one', () => {
    render(
      <AskUserCard
        question={question({ context: 'You said five years; I need a number of days.' })}
        onAnswer={() => {}}
        disabled={false}
      />,
    )
    expect(screen.getByText('You said five years; I need a number of days.')).toBeTruthy()
  })

  it('an OPEN question — no options — renders a usable text box', () => {
    // This is the shape that was impossible until `options` became optional.
    // A card with neither buttons nor a text box is one the user can neither
    // click nor type into.
    const onAnswer = vi.fn()
    render(
      <AskUserCard
        question={question({ question: 'What level would prove you wrong?', options: [], allowFreetext: true })}
        onAnswer={onAnswer}
        disabled={false}
      />,
    )
    const input = screen.getByPlaceholderText(/type your answer/i)
    expect(input).toBeTruthy()
    fireEvent.change(input, { target: { value: '3000' } })
    fireEvent.click(screen.getByRole('button', { name: /Submit/ }))
    expect(onAnswer).toHaveBeenCalledWith('3000')
  })

  it('does not crash with an empty options array', () => {
    const { container } = render(
      <AskUserCard question={question({ options: [] })} onAnswer={() => {}} disabled={true} />,
    )
    expect(container).toBeTruthy()
  })

  it('disables every option once answered, and shows what was chosen', () => {
    render(
      <AskUserCard
        question={question({ answered: true, selectedValue: 'gold' })}
        onAnswer={() => {}}
        disabled={false}
      />,
    )
    expect(screen.getByText(/Answered: gold/)).toBeTruthy()
    expect(screen.getByRole('button', { name: /Gold/ }).hasAttribute('disabled')).toBe(true)
  })
})

describe('ask_user end to end: the builtin tool → the reducer → the card', () => {
  /** Drives the real reducer over the events a real turn produces. */
  function cardStateFor(toolName: string, markerJson: string) {
    let state = initialAgentEventState()
    state = agentEventReducer(state, makeEvent('message_start', { role: 'assistant', messageId: 'm1' }))
    state = agentEventReducer(state, makeEvent('tool_use_start', { toolCallId: 'tc-1', toolName, input: {} }))
    state = agentEventReducer(
      state,
      makeEvent('tool_use_end', {
        toolCallId: 'tc-1',
        isError: false,
        output: JSON.stringify({ content: [{ type: 'text', text: markerJson }] }),
      }),
    )
    return state.askedQuestions.get('tc-1')
  }

  it('an OPTIONED question renders buttons', () => {
    const marker = buildAskUserPayload({
      question: 'Which horizon?',
      options: [
        { label: '1 year', value: '365 days' },
        { label: '5 years', value: '1825 days' },
      ],
    })
    const card = cardStateFor('mcp__ui__ask_user', JSON.stringify(marker))
    expect(card).toBeDefined()
    render(<AskUserCard question={card!} onAnswer={() => {}} disabled={false} />)
    expect(screen.getByRole('button', { name: /1 year/ })).toBeTruthy()
    expect(screen.getByRole('button', { name: /5 years/ })).toBeTruthy()
    // Options present and nothing said about freetext: no text box, which is
    // the pre-existing default for every caller that already passes options.
    expect(screen.queryByPlaceholderText(/type your answer/i)).toBeNull()
  })

  it('an OPEN question renders a text box — the case the schema used to forbid', () => {
    // The whole path: the real tool payload builder, the real reducer, the
    // real card. If `options` stopped being defaulted to `[]` anywhere along
    // here, this render throws instead of failing an assertion.
    const marker = buildAskUserPayload({ question: 'What level would prove you wrong?' })
    const card = cardStateFor('mcp__ui__ask_user', JSON.stringify(marker))
    expect(card).toBeDefined()
    expect(card!.options).toEqual([])
    expect(card!.allowFreetext).toBe(true)

    const onAnswer = vi.fn()
    render(<AskUserCard question={card!} onAnswer={onAnswer} disabled={false} />)
    expect(screen.getByText('What level would prove you wrong?')).toBeTruthy()
    const input = screen.getByPlaceholderText(/type your answer/i)
    fireEvent.change(input, { target: { value: 'gold below 3000' } })
    fireEvent.click(screen.getByRole('button', { name: /Submit/ }))
    expect(onAnswer).toHaveBeenCalledWith('gold below 3000')
  })

  it('LIMIT: the same marker under a tool name without "ask_user" renders NOTHING', () => {
    // The reducer's gate (`agentEventReducer.ts:243`). This is the assertion
    // that makes the tool's NAME a tested constraint rather than a comment —
    // rename the builtin and the card silently disappears.
    const marker = buildAskUserPayload({ question: 'Which horizon?' })
    expect(cardStateFor('mcp__ui__ask_user', JSON.stringify(marker))).toBeDefined()
    expect(cardStateFor('mcp__ui__request_input', JSON.stringify(marker))).toBeUndefined()
  })
})
