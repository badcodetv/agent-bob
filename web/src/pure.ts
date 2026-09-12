// TIER 1 of the tiered-reuse split — `@agentkit/chat-ui/pure`.
//
// Types and pure logic: the wire shapes, the single event reducer, the replay
// helpers, the artifact tree/filters and the canonical session permalink.
// NOTHING in this module's import graph touches React, MUI or emotion, and
// `pure.test.ts` walks the graph and fails if that ever stops being true.
//
// Why a subpath and not just the barrel: importing anything from the root
// `index.ts` pulls the whole component tree, including `AgentChat` — 37-45s of
// module resolution for one test file, and a tier line nothing enforces
// (design/2026-08-20-agent-wolf.md, R125, finding 2). A consumer that only
// wants `ArtifactInfo` should not be resolving a chat client to get it.
//
// Everything here is ALSO exported from the root entry point; this is a
// narrower door onto the same rooms, not a second copy.

export * from './types.js'
export * from './agentEventReducer.js'
export * from './replayEvents.js'
export * from './artifactTree.js'
export * from './artifactFilters.js'
export * from './permalink.js'
export * from './charter.js'
export * from './guide/surfaces.js'
export * from './guide/frontMatter.js'
export * from './guide/guideRoute.js'
