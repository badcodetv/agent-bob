// The guide's on-disk contract (work plan §1.5): front matter + skeleton.
//
//     ---
//     title: A worker's instructions
//     slug: a-workers-instructions
//     part: 1            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
//     order: 3
//     surfaces: [workers, worker-configuration]   # console surfaces whose About line uses this page
//     ---
//
// This module owns exactly the parse of that block plus the split into "the
// first paragraph" (what G2's About disclosure shows, §4.1 items 1-2) and
// "the body" (the whole page, §4.1 items 1-7). It does not render markdown —
// that stays a build-time concern in `examples/web/scripts/build-guide.mjs`,
// which imports this parser from the built package so the parse rules and the
// `SurfaceId` set can never drift between the writer's contract and the
// build step that reads it.
//
// Pure: no React, no filesystem. A hand-rolled parser rather than a YAML
// dependency — the shape above is narrow and ours; a general YAML parser
// would accept far more than this contract intends and hide a typo instead
// of failing on it.

import { GUIDE_SURFACES, isSurfaceId, type SurfaceId } from './surfaces.js'

/** What the console calls each `part` number (design §4.2). */
export const GUIDE_PART_LABELS: Record<number, string> = {
  0: 'Start here',
  1: 'The six things you can touch',
  2: 'Reading what happened',
  3: 'The architect and the loop',
  4: 'Field notes',
  9: 'Reference',
}

/** The only slug shape the contract allows: lower-kebab, optionally one `/` per nesting level (`for-operators/inviting-someone`). */
const SLUG_PATTERN = /^[a-z0-9]+(?:[-/][a-z0-9]+)*$/

export interface GuideFrontMatter {
  title: string
  slug: string
  part: number
  order: number
  surfaces: SurfaceId[]
}

export interface ParsedGuidePage extends GuideFrontMatter {
  /** Items 1-2 of the skeleton: two to four sentences, no links, stands alone. */
  firstParagraph: string
  /** The whole markdown source after the closing `---`, item 1 through 7. */
  body: string
}

const DELIMITER = /^---\s*$/

/**
 * Parse one guide page's raw markdown source. `sourceLabel` (typically the
 * file path) is folded into every error so a build failure names the page
 * that caused it.
 */
export function parseGuidePage(raw: string, sourceLabel = '<guide page>'): ParsedGuidePage {
  const lines = raw.split(/\r?\n/)
  if (!DELIMITER.test(lines[0] ?? '')) {
    throw new Error(`${sourceLabel}: does not start with a "---" front-matter delimiter`)
  }
  const closeIdx = lines.findIndex((line, i) => i > 0 && DELIMITER.test(line))
  if (closeIdx === -1) {
    throw new Error(`${sourceLabel}: front matter is never closed with a "---" line`)
  }

  const rawFields = parseFields(lines.slice(1, closeIdx), sourceLabel)
  const frontMatter = validateFields(rawFields, sourceLabel)

  const bodyLines = lines.slice(closeIdx + 1)
  // Drop leading blank lines only — everything else is the page, verbatim.
  let bodyStart = 0
  while (bodyStart < bodyLines.length && bodyLines[bodyStart]!.trim() === '') bodyStart++
  const body = bodyLines.slice(bodyStart).join('\n').trimEnd()

  const firstParagraph = extractFirstParagraph(body)
  if (firstParagraph === '') {
    throw new Error(`${sourceLabel}: has no first paragraph (the About text, §4.1 items 1-2) after the front matter`)
  }

  return { ...frontMatter, firstParagraph, body }
}

/** The first paragraph, soft-wrapped lines joined into one line of prose. */
function extractFirstParagraph(body: string): string {
  const blankAt = body.search(/\n[ \t]*\n/)
  const raw = blankAt === -1 ? body : body.slice(0, blankAt)
  return raw.split(/\s+/).filter(Boolean).join(' ').trim()
}

type RawFields = Record<string, string>

function parseFields(lines: string[], sourceLabel: string): RawFields {
  const fields: RawFields = {}
  for (const rawLine of lines) {
    const line = rawLine.trim()
    if (line === '' || line.startsWith('#')) continue
    const colonIdx = line.indexOf(':')
    if (colonIdx === -1) {
      throw new Error(`${sourceLabel}: front-matter line is not "key: value": ${JSON.stringify(rawLine)}`)
    }
    const key = line.slice(0, colonIdx).trim()
    // Strip a trailing "  # comment" — a hash preceded by whitespace. The
    // contract's values never contain a literal "#", so this is unambiguous.
    const rest = line.slice(colonIdx + 1).replace(/\s#.*$/, '')
    fields[key] = rest.trim()
  }
  return fields
}

function validateFields(fields: RawFields, sourceLabel: string): GuideFrontMatter {
  const title = fields.title
  if (!title) throw new Error(`${sourceLabel}: front matter is missing "title"`)

  const slug = fields.slug
  if (!slug) throw new Error(`${sourceLabel}: front matter is missing "slug"`)
  if (!SLUG_PATTERN.test(slug)) {
    throw new Error(`${sourceLabel}: slug ${JSON.stringify(slug)} is not lower-kebab (optionally one "/" per nesting level)`)
  }

  const part = parseIntField(fields.part, 'part', sourceLabel)
  if (!(part in GUIDE_PART_LABELS)) {
    throw new Error(
      `${sourceLabel}: part ${part} is not one of ${Object.keys(GUIDE_PART_LABELS).join(', ')} (§4.2)`,
    )
  }

  const order = parseIntField(fields.order, 'order', sourceLabel)

  const surfaces = parseSurfaces(fields.surfaces, sourceLabel)

  return { title, slug, part, order, surfaces }
}

function parseIntField(value: string | undefined, name: string, sourceLabel: string): number {
  if (value === undefined || value === '') {
    throw new Error(`${sourceLabel}: front matter is missing "${name}"`)
  }
  if (!/^-?\d+$/.test(value)) {
    throw new Error(`${sourceLabel}: "${name}" must be a whole number, got ${JSON.stringify(value)}`)
  }
  return parseInt(value, 10)
}

function parseSurfaces(value: string | undefined, sourceLabel: string): SurfaceId[] {
  if (value === undefined || value === '') return []
  const trimmed = value.trim()
  if (!trimmed.startsWith('[') || !trimmed.endsWith(']')) {
    throw new Error(`${sourceLabel}: "surfaces" must be a bracketed list, e.g. [workers, worker-configuration]`)
  }
  const inner = trimmed.slice(1, -1).trim()
  if (inner === '') return []
  const ids = inner.split(',').map((s) => s.trim()).filter(Boolean)
  for (const id of ids) {
    if (!isSurfaceId(id)) {
      throw new Error(
        `${sourceLabel}: "${id}" is not a known surface (§1.6). Known: ${GUIDE_SURFACES.join(', ')}`,
      )
    }
  }
  return ids as SurfaceId[]
}
