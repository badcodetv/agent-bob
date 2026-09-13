import { describe, expect, it } from 'vitest'
import { clampText, firstLine, stripMarkdown } from './clamp.js'

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

describe('stripMarkdown', () => {
  it.each([
    ['bold and code', '- **`kind=rolling-summary, worker=copywriter`** (2b8f93) — its first', '• kind=rolling-summary, worker=copywriter (2b8f93) — its first'],
    ['emphasis between words', 'flagged as *not* a draft', 'flagged as not a draft'],
    ['underscores inside a word stay', 'a snake_case_name and 2*3*4', 'a snake_case_name and 2*3*4'],
    ['headings and links', '## Result\nsee [the thread](https://x/y)', 'Result\nsee the thread'],
    ['fences and quotes', '```json\n{"a":1}\n```\n> quoted', '{"a":1}\nquoted'],
    ['plain text is untouched', 'Nothing to strip here.', 'Nothing to strip here.'],
  ])('%s', (_name, input, want) => {
    expect(stripMarkdown(input)).toBe(want)
  })
})
