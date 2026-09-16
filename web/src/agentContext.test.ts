import { describe, expect, it } from 'vitest'
import { formatAgentContext, parseAgentContext } from './agentContext.js'

describe('parseAgentContext', () => {
  it('splits the context from the words after it', () => {
    const msg = '<agent-context summary="Opened from the hypothesis page">\nLabel every candidate memory.\n</agent-context>\nIs gold a hedge?'
    expect(parseAgentContext(msg)).toEqual({
      summary: 'Opened from the hypothesis page',
      context: 'Label every candidate memory.',
      rest: 'Is gold a hedge?',
    })
  })

  it('allows no summary and no following words', () => {
    expect(parseAgentContext('<agent-context>\nx\n</agent-context>')).toEqual({ summary: '', context: 'x', rest: '' })
  })

  it('ignores the marker anywhere but the start, and an unclosed one', () => {
    expect(parseAgentContext('hello <agent-context>x</agent-context>')).toBeNull()
    expect(parseAgentContext('<agent-context>\nnever closed')).toBeNull()
    expect(parseAgentContext('an ordinary message')).toBeNull()
  })

  it('round-trips through formatAgentContext, quotes included', () => {
    const msg = formatAgentContext('Do the thing.', 'From "the board"', 'hi')
    expect(parseAgentContext(msg)).toEqual({ summary: 'From "the board"', context: 'Do the thing.', rest: 'hi' })
  })
})
