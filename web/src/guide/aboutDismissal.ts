// aboutDismissal — "About this screen" dismissal, sticky per surface per
// project (work plan §2, ticket C2).
//
// One `localStorage` entry per project, `agentkit.about.dismissed.<projectId>`
// (the exact key the ticket names), holding the JSON array of surface ids the
// operator has dismissed on THAT project. Not one key per surface: a project
// is deleted or a browser profile is wiped far more often than a single
// surface is un-dismissed, and one key per project is one thing to find and
// clear.
//
// Same shape as `watermark.ts`'s `readWatermark`/`writeWatermark`: unreadable
// or malformed storage degrades to "nothing dismissed" rather than throwing,
// because a broken localStorage is not a reason to hide every About line, and
// a storage write that is refused (private mode, quota) is not an error the
// operator needs to see — the disclosure just reappears next visit.
//
// Pure: no React. `AboutThisScreen` is the only caller.

import type { SurfaceId } from './surfaces.js'

/** `agentkit.about.dismissed.<projectId>` */
export function aboutDismissalKey(projectId: string): string {
  return `agentkit.about.dismissed.${projectId}`
}

function readDismissed(projectId: string): SurfaceId[] {
  try {
    const raw = globalThis.localStorage?.getItem(aboutDismissalKey(projectId))
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((v): v is SurfaceId => typeof v === 'string')
  } catch {
    return []
  }
}

function writeDismissed(projectId: string, surfaces: SurfaceId[]): void {
  try {
    globalThis.localStorage?.setItem(aboutDismissalKey(projectId), JSON.stringify(surfaces))
  } catch {
    /* private mode, quota, no storage — the disclosure just reappears next visit. */
  }
}

/** Whether this surface's About line has been dismissed on this project. */
export function isAboutDismissed(projectId: string, surface: SurfaceId): boolean {
  return readDismissed(projectId).includes(surface)
}

/** Dismiss this surface's About line on this project. Idempotent. */
export function dismissAbout(projectId: string, surface: SurfaceId): void {
  const current = readDismissed(projectId)
  if (current.includes(surface)) return
  writeDismissed(projectId, [...current, surface])
}

/** Bring a dismissed About line back on this project. Idempotent. */
export function restoreAbout(projectId: string, surface: SurfaceId): void {
  const current = readDismissed(projectId)
  if (!current.includes(surface)) return
  writeDismissed(
    projectId,
    current.filter((s) => s !== surface),
  )
}
