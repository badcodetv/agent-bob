// The tier line, enforced.
//
// `pure.ts` and `components/index.ts` are the two subpath entry points of the
// published package. Their VALUE is what they leave out — a barrel that quietly
// grows an import of `AgentChat` still typechecks, still passes every component
// test, and silently costs every `/pure` consumer the whole chat client
// (R125, finding 2: 37-45s of module resolution for one test file).
//
// So these tests walk the real relative-import graph of each entry point and
// assert what is NOT in it. A static walk, not a dynamic import: the question
// is what a bundler or `tsc` would pull, which is a property of the source, and
// a dynamic import would need a DOM and would answer a different question.

// Node's builtins are referenced HERE rather than added to tsconfig's `types`:
// this is a browser component library, and making `process` and `fs` ambient
// across all of src/ would hide a real mistake in a component.
/// <reference types="node" />
import { describe, expect, it } from 'vitest'
import { readFileSync, existsSync } from 'node:fs'
import { dirname, resolve, relative, basename } from 'node:path'
import { fileURLToPath } from 'node:url'

import * as pure from './pure.js'

const SRC = dirname(fileURLToPath(import.meta.url))

/** Resolve one relative specifier the way the source writes them (always `.js`). */
function resolveRelative(fromFile: string, spec: string): string | null {
  if (!spec.startsWith('.')) return null
  const base = resolve(dirname(fromFile), spec).replace(/\.js$/, '')
  for (const ext of ['.ts', '.tsx', '.json', '/index.ts', '/index.tsx']) {
    if (existsSync(base + ext)) return base + ext
  }
  return existsSync(base) ? base : null
}

interface Graph {
  /** Every source file reachable from the entry, entry included. */
  files: string[]
  /** Every bare (package) specifier any of them imports. */
  packages: string[]
}

function importGraph(entry: string): Graph {
  const seen = new Set<string>()
  const packages = new Set<string>()
  const stack = [entry]
  while (stack.length > 0) {
    const file = stack.pop() as string
    if (seen.has(file)) continue
    seen.add(file)
    const src = readFileSync(file, 'utf8')
    // `from '…'` covers static import/export-from; `import('…')` covers dynamic.
    const re = /(?:from\s+|import\s*\(\s*)['"]([^'"]+)['"]/g
    let m: RegExpExecArray | null
    while ((m = re.exec(src)) !== null) {
      const spec = m[1] as string
      if (spec.startsWith('.')) {
        const r = resolveRelative(file, spec)
        // An unresolvable relative import is itself a bug worth failing on.
        expect(r, `${relative(SRC, file)} imports '${spec}', which resolves to nothing`).not.toBeNull()
        stack.push(r as string)
      } else {
        packages.add(spec)
      }
    }
  }
  return { files: [...seen], packages: [...packages].sort() }
}

const moduleNames = (g: Graph) => g.files.map(f => basename(f).replace(/\.tsx?$/, ''))

describe('@agentkit/chat-ui/pure — tier 1', () => {
  const graph = importGraph(resolve(SRC, 'pure.ts'))

  it('does not pull AgentChat', () => {
    expect(moduleNames(graph)).not.toContain('AgentChat')
  })

  it('pulls no component at all', () => {
    const inComponents = graph.files.filter(f => f.includes(`${'/'}components${'/'}`))
    expect(inComponents.map(f => relative(SRC, f))).toEqual([])
  })

  it('imports no React, MUI or emotion — anywhere in its graph', () => {
    // The criterion is "no React import anywhere in its graph". Assert on the
    // whole package list rather than a denylist, so a new UI dependency cannot
    // sneak in under a name nobody thought to forbid.
    expect(graph.packages).toEqual([])
  })

  it('actually exports the tier-1 surface', () => {
    // The graph walk above is static; this proves the module also loads and
    // that the six modules it re-exports are really reachable through it.
    expect(typeof pure.agentEventReducer).toBe('function')
    expect(typeof pure.replayEvents).toBe('function')
    expect(typeof pure.buildArtifactTree).toBe('function')
    expect(typeof pure.filterArtifactsByType).toBe('function')
    expect(typeof pure.parseSessionPermalink).toBe('function')
    expect(pure.SESSION_PERMALINK_FORMAT).toBe('/p/:projectId/s/:sessionId')
  })
})

describe('@agentkit/chat-ui/components — tier 2', () => {
  const entry = resolve(SRC, 'components/index.ts')
  const source = readFileSync(entry, 'utf8')

  // The eight components that touch context or fetch(). They carry Bob's API
  // contract as well as its look, so they stay behind the embed page.
  const TIER_3 = [
    'AgentChat',
    'AgentSessionList',
    'ArtifactViewer',
    'ArtifactPreviewDialog',
    'InlineArtifactPreview',
    'WorkersPage',
    'WorkerChatPanel',
    'ProjectSettingsPage',
  ]

  it.each(TIER_3)('does not re-export %s', name => {
    expect(source).not.toMatch(new RegExp(`from '\\./${name}\\.js'`))
  })

  it('does export the presentational components the tier exists for', () => {
    for (const name of ['ArtifactPanel', 'ArtifactGrid', 'AgentMarkdown', 'DeliveryStatusChip']) {
      expect(source).toMatch(new RegExp(`from '\\./${name}\\.js'`))
    }
  })

  it('is a subset of the root entry point, not a second surface', () => {
    // Every specifier the tier-2 barrel exports from must also be exported from
    // index.ts, so `/components` can never drift into exporting something the
    // package's own public surface does not.
    const root = readFileSync(resolve(SRC, 'index.ts'), 'utf8')
    const specs = [...source.matchAll(/from '\.\/([A-Za-z]+)\.js'/g)].map(m => m[1] as string)
    expect(specs.length).toBeGreaterThan(20)
    for (const s of new Set(specs)) {
      expect(root, `index.ts does not export ./components/${s}.js`).toContain(`from './components/${s}.js'`)
    }
  })
})
