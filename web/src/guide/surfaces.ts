// The closed set of console surfaces the guide can address (design
// `2026-09-11-onboarding-and-the-guide.md` §3 G2/G7, work plan §1.6).
//
// Every "About this screen" disclosure (G2) and every generated guide page's
// front-matter `surfaces:` list is validated against this set — the same
// trick `navReveal.ts` uses for `NavEntry`: adding a surface id anywhere else
// in the codebase without adding it HERE is a type error, not a silently
// inert string.
//
// Pure: no React, no window. Consumed by the front-matter parser (this
// package) and by `AboutThisScreen`/`GuideProvider` (ticket C2, not yet
// built) and by the build-time guide generator in `examples/web`.

export const GUIDE_SURFACES = [
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
] as const

export type SurfaceId = (typeof GUIDE_SURFACES)[number]

/** Runtime guard for a string read out of markdown front matter or JSON. */
export function isSurfaceId(value: string): value is SurfaceId {
  return (GUIDE_SURFACES as readonly string[]).includes(value)
}
