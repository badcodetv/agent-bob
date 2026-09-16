import { describe, expect, it } from 'vitest'
import { buildGuideHash, parseGuideHash, GUIDE_HASH_PREFIX } from './guideRoute.js'

describe('buildGuideHash', () => {
  it('builds the index hash for null', () => {
    expect(buildGuideHash(null)).toBe('#/guide')
  })

  it('builds a slug hash', () => {
    expect(buildGuideHash('the-architect')).toBe('#/guide/the-architect')
  })

  it('encodes a nested slug', () => {
    expect(buildGuideHash('for-operators/inviting-someone')).toBe(
      '#/guide/for-operators%2Finviting-someone',
    )
  })
})

describe('parseGuideHash', () => {
  it('parses the bare index', () => {
    expect(parseGuideHash('#/guide')).toBe('')
  })

  it('parses a slug', () => {
    expect(parseGuideHash('#/guide/the-architect')).toBe('the-architect')
  })

  it('decodes a nested slug', () => {
    expect(parseGuideHash('#/guide/for-operators%2Finviting-someone')).toBe(
      'for-operators/inviting-someone',
    )
  })

  it('parses out of a full href', () => {
    expect(parseGuideHash('https://bob.example.com/index.html#/guide/the-desk')).toBe('the-desk')
  })

  it.each(['', '#/desk', '#/guide-not-really', '#/guideish/x', 'no hash at all'])(
    'returns null for %s',
    (input) => {
      expect(parseGuideHash(input)).toBeNull()
    },
  )

  it('round-trips build → parse for every slug shape', () => {
    for (const slug of [null, 'the-desk', 'for-operators/inviting-someone']) {
      expect(parseGuideHash(buildGuideHash(slug))).toBe(slug ?? '')
    }
  })

  it('is anchored on the documented prefix constant', () => {
    expect(GUIDE_HASH_PREFIX).toBe('#/guide')
  })
})
