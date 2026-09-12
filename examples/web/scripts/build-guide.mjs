#!/usr/bin/env node
// Builds the guide (design 2026-09-11-onboarding-and-the-guide.md §3 G7,
// work plan §1.5, ticket C1).
//
// Reads every `docs/guide/**/*.md`, parses its front matter with the SAME
// parser the console will validate against (`@agentkit/chat-ui`'s
// `parseGuidePage`, imported from web/'s built `pure` entry point so there is
// exactly one implementation of the contract — not a second one re-typed
// here), renders each page's markdown to static HTML at build time (so the
// browser bundle ships no markdown parser at all), and writes:
//
//   - examples/web/src/guide/pages.generated.ts     (§1.5: the whole page,
//     read by GuidePage.tsx)
//   - web/src/guide/firstParagraphs.generated.json   (§1.5: just the About
//     text per slug, for ticket C2's GuideProvider)
//
// Both are gitignored — this script is what "commits" them, every build.
//
// `docs/guide/` does not exist yet at the time this ticket was written
// (other agents are writing it in parallel, per the work plan's Stream B).
// Rather than fail, an absent or empty directory produces an EMPTY guide:
// GuidePage.tsx is responsible for rendering "The guide has not been written
// yet." in that case (§ Scope, C1's acceptance criteria).
//
// Run by `yarn build`, `yarn dev` and `yarn typecheck` (prefixed in
// package.json) so the generated files always exist before Vite or tsc looks
// for them.

import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { readdirSync, readFileSync, statSync, mkdirSync, writeFileSync, existsSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

// examples/web/scripts -> examples/web -> examples -> <repo root>
const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = join(SCRIPT_DIR, '..', '..', '..')
const EXAMPLES_WEB = join(SCRIPT_DIR, '..')
// Overridable for testing this script itself against throwaway fixtures
// without ever writing into the real docs/guide/ (owned by Stream B).
const GUIDE_DIR = process.env.BUILD_GUIDE_SOURCE_DIR ?? join(REPO_ROOT, 'docs', 'guide')

const PAGES_OUT = join(EXAMPLES_WEB, 'src', 'guide', 'pages.generated.ts')
const FIRST_PARAGRAPHS_OUT = join(REPO_ROOT, 'web', 'src', 'guide', 'firstParagraphs.generated.json')

async function main() {
  if (!existsSync(GUIDE_DIR)) {
    console.log(`[build-guide] ${relative(REPO_ROOT, GUIDE_DIR)} does not exist yet — writing an empty guide.`)
    writeOutputs([])
    return
  }

  // web/ must be built before examples/web (deploy/web.Dockerfile and the
  // CI job both do this; the parser contract lives in its dist, not its src,
  // so this app depends on nothing but a published door of the package it
  // already consumes for everything else).
  const { parseGuidePage } = await import(join(REPO_ROOT, 'web', 'dist', 'pure.js'))

  const files = listMarkdownFiles(GUIDE_DIR).filter((f) => f.toLowerCase() !== join(GUIDE_DIR, 'README.md').toLowerCase())

  if (files.length === 0) {
    console.log(`[build-guide] ${relative(REPO_ROOT, GUIDE_DIR)} has no pages yet — writing an empty guide.`)
    writeOutputs([])
    return
  }

  const pages = []
  const seenSlugs = new Map()
  for (const file of files) {
    const label = relative(REPO_ROOT, file)
    const raw = readFileSync(file, 'utf8')
    const parsed = parseGuidePage(raw, label)
    if (seenSlugs.has(parsed.slug)) {
      throw new Error(`[build-guide] slug "${parsed.slug}" is claimed by both ${seenSlugs.get(parsed.slug)} and ${label}`)
    }
    seenSlugs.set(parsed.slug, label)
    pages.push({
      slug: parsed.slug,
      title: parsed.title,
      part: parsed.part,
      order: parsed.order,
      surfaces: parsed.surfaces,
      firstParagraph: parsed.firstParagraph,
      html: renderMarkdown(parsed.body),
    })
  }

  pages.sort((a, b) => a.part - b.part || a.order - b.order || a.slug.localeCompare(b.slug))
  writeOutputs(pages)
  console.log(`[build-guide] wrote ${pages.length} page(s).`)
}

/** Recursively collect every `.md` file under `dir` (the guide nests `for-operators/`). */
function listMarkdownFiles(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    const stat = statSync(full)
    if (stat.isDirectory()) out.push(...listMarkdownFiles(full))
    else if (entry.toLowerCase().endsWith('.md')) out.push(full)
  }
  return out
}

function renderMarkdown(markdown) {
  return renderToStaticMarkup(createElement(ReactMarkdown, { remarkPlugins: [remarkGfm] }, markdown))
}

function writeOutputs(pages) {
  mkdirSync(dirname(PAGES_OUT), { recursive: true })
  mkdirSync(dirname(FIRST_PARAGRAPHS_OUT), { recursive: true })

  const body = pages
    .map((p) => `  {
    slug: ${JSON.stringify(p.slug)},
    title: ${JSON.stringify(p.title)},
    part: ${JSON.stringify(p.part)},
    order: ${JSON.stringify(p.order)},
    surfaces: ${JSON.stringify(p.surfaces)},
    firstParagraph: ${JSON.stringify(p.firstParagraph)},
    html: ${JSON.stringify(p.html)},
  }`)
    .join(',\n')

  writeFileSync(
    PAGES_OUT,
    `// AUTO-GENERATED by examples/web/scripts/build-guide.mjs from docs/guide/. Do not edit — it
// is gitignored and rebuilt by \`yarn build\` / \`yarn dev\` / \`yarn typecheck\`.
import type { SurfaceId } from "@agentkit/chat-ui/pure";

export interface GuidePage {
  slug: string;
  title: string;
  part: number;
  order: number;
  surfaces: SurfaceId[];
  /** Two to four sentences, no links — what the console's "About this screen" shows. */
  firstParagraph: string;
  /** The whole page, rendered to static HTML at build time. */
  html: string;
}

export const GUIDE_PAGES: GuidePage[] = [
${body}
];
`,
  )

  const firstParagraphs = Object.fromEntries(pages.map((p) => [p.slug, p.firstParagraph]))
  writeFileSync(FIRST_PARAGRAPHS_OUT, `${JSON.stringify(firstParagraphs, null, 2)}\n`)
}

main().catch((err) => {
  console.error(err instanceof Error ? err.message : err)
  process.exit(1)
})
