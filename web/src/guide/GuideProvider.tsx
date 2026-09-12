// GuideProvider — how the guide's build-time content reaches `AboutThisScreen`
// without `web/` ever importing a generated file itself (work plan §1.5/C2,
// design/2026-09-11-onboarding-and-the-guide.md §3 G2).
//
// `examples/web/scripts/build-guide.mjs` (ticket C1) reads `docs/guide/*.md`
// at build time and emits, among other things,
// `examples/web/src/guide/pages.generated.ts` — the whole page list, each
// entry carrying `slug`, `firstParagraph` and which `surfaces` (§1.6) it is
// the About text for. Both generated files are gitignored: `web/` (a
// published package, O12) must never depend on them existing, so this module
// takes the finished `surface -> paragraph` map as a plain prop instead of
// reading anything off disk or off a bare object keyed by slug. The shell
// builds that map (surface is the lookup key `AboutThisScreen` actually has)
// and injects it here, once, at the root.
//
// The default — no `GuideProvider` mounted at all — is an EMPTY map, which is
// exactly what `useGuideParagraph` needs to make `AboutThisScreen` render
// nothing: a consumer with no guide still gets a working component library.

import { createContext, useContext, type ReactNode } from 'react'
import type { SurfaceId } from './surfaces.js'

export interface GuideParagraph {
  /** The page's slug — builds the "Read more in the guide →" link (`#/guide/<slug>`). */
  slug: string
  /** The first paragraph (§1.5): two to four sentences, no links, stands alone. */
  text: string
}

/** Not every surface has a page yet — and `project-create`'s one-sentence
 *  entry may have no `slug` worth linking, so the mapping stays partial. */
export type GuideParagraphMap = Partial<Record<SurfaceId, GuideParagraph>>

const EMPTY_MAP: GuideParagraphMap = {}

const GuideContext = createContext<GuideParagraphMap>(EMPTY_MAP)

export interface GuideProviderProps {
  paragraphs: GuideParagraphMap
  children?: ReactNode
}

/** Injects the guide's build-time content into the component tree. Mount it
 *  once, at the shell's root, above every view that uses `AboutThisScreen`. */
export function GuideProvider({ paragraphs, children }: GuideProviderProps) {
  return <GuideContext.Provider value={paragraphs}>{children}</GuideContext.Provider>
}

/** The paragraph for one surface, or `undefined` when there is none — no
 *  page written for it yet, or no `GuideProvider` mounted at all (a consumer
 *  with no guide). `AboutThisScreen` renders nothing in either case. */
export function useGuideParagraph(surface: SurfaceId): GuideParagraph | undefined {
  return useContext(GuideContext)[surface]
}
