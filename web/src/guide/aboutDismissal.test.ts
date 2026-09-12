// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import {
  aboutDismissalKey,
  dismissAbout,
  isAboutDismissed,
  restoreAbout,
} from './aboutDismissal.js'

afterEach(() => {
  globalThis.localStorage?.clear()
})

describe('the key', () => {
  it('is the exact key the ticket names', () => {
    expect(aboutDismissalKey('acme')).toBe('agentkit.about.dismissed.acme')
  })

  it('is scoped per project', () => {
    expect(aboutDismissalKey('acme')).not.toBe(aboutDismissalKey('other'))
  })
})

describe('isAboutDismissed', () => {
  it('is false for a surface never dismissed', () => {
    expect(isAboutDismissed('acme', 'desk')).toBe(false)
  })

  it('is false when storage holds rubbish', () => {
    globalThis.localStorage.setItem(aboutDismissalKey('acme'), 'not json')
    expect(isAboutDismissed('acme', 'desk')).toBe(false)
  })

  it('is false when storage holds a non-array', () => {
    globalThis.localStorage.setItem(aboutDismissalKey('acme'), JSON.stringify({ desk: true }))
    expect(isAboutDismissed('acme', 'desk')).toBe(false)
  })
})

describe('dismissAbout / restoreAbout', () => {
  it('dismisses one surface without touching another', () => {
    dismissAbout('acme', 'desk')
    expect(isAboutDismissed('acme', 'desk')).toBe(true)
    expect(isAboutDismissed('acme', 'chat')).toBe(false)
  })

  it('is per project, not global', () => {
    dismissAbout('acme', 'desk')
    expect(isAboutDismissed('other', 'desk')).toBe(false)
  })

  it('is idempotent to dismiss twice', () => {
    dismissAbout('acme', 'desk')
    dismissAbout('acme', 'desk')
    expect(JSON.parse(globalThis.localStorage.getItem(aboutDismissalKey('acme')) ?? '[]')).toEqual([
      'desk',
    ])
  })

  it('restores a dismissed surface', () => {
    dismissAbout('acme', 'desk')
    restoreAbout('acme', 'desk')
    expect(isAboutDismissed('acme', 'desk')).toBe(false)
  })

  it('restoring an already-shown surface is a no-op', () => {
    restoreAbout('acme', 'desk')
    expect(isAboutDismissed('acme', 'desk')).toBe(false)
  })

  it('keeps other dismissed surfaces when one is restored', () => {
    dismissAbout('acme', 'desk')
    dismissAbout('acme', 'chat')
    restoreAbout('acme', 'desk')
    expect(isAboutDismissed('acme', 'desk')).toBe(false)
    expect(isAboutDismissed('acme', 'chat')).toBe(true)
  })
})
