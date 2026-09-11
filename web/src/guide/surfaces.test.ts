import { describe, expect, it } from 'vitest'
import { GUIDE_SURFACES, isSurfaceId } from './surfaces.js'

describe('GUIDE_SURFACES', () => {
  it('is exactly the closed set from work plan §1.6', () => {
    expect(GUIDE_SURFACES).toEqual([
      'desk',
      'chat',
      'workers',
      'worker-configuration',
      'worker-triggers',
      'worker-history',
      'memory',
      'activity',
      'chart',
      'settings',
      'onboarding',
      'project-create',
    ])
  })

  it('has no duplicates', () => {
    expect(new Set(GUIDE_SURFACES).size).toBe(GUIDE_SURFACES.length)
  })
})

describe('isSurfaceId', () => {
  it.each(GUIDE_SURFACES)('accepts %s', (id) => {
    expect(isSurfaceId(id)).toBe(true)
  })

  it.each(['worker-config', 'Workers', '', 'chart ', 'automation'])('rejects %s', (id) => {
    expect(isSurfaceId(id)).toBe(false)
  })
})
