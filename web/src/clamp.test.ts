import { describe, expect, it } from 'vitest'
import { clampText, firstLine } from './clamp.js'

describe('clampText', () => {
  it('leaves short text alone', () => {
    expect(clampText('one\ntwo', 6, 480)).toEqual({ preview: 'one\ntwo', clamped: false })
  })

  it('keeps the first lines and marks the cut', () => {
    const r = clampText('a\nb\nc\nd', 2, 480)
    expect(r).toEqual({ preview: 'a\nb…', clamped: true })
  })

  it('cuts long single lines at a word', () => {
    const r = clampText('alpha beta gamma delta epsilon', 6, 20)
    expect(r.clamped).toBe(true)
    expect(r.preview).toBe('alpha beta gamma…')
  })

  it('does not count trailing blank lines as more', () => {
    expect(clampText('a\nb\n\n\n', 2, 480).clamped).toBe(false)
  })

  it('never ends a preview on a blank line', () => {
    expect(clampText('a\n\nb', 2, 480).preview).toBe('a…')
  })
})

describe('firstLine', () => {
  it('skips leading blanks and cuts long lines', () => {
    expect(firstLine('\n\n  THE HEADLINE  \nrest')).toBe('THE HEADLINE')
    expect(firstLine('x'.repeat(10), 4)).toBe('xxxx…')
  })
})
