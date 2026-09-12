// The guide's deep link (design §3 G7): `#/guide/<slug>`.
//
// A hash route, not a path route, and deliberately not `permalink.ts`'s
// `/p/<project>/s/<session>` scheme: the shell has no router (App.tsx picks a
// view off a state machine, not a URL), the session permalink already owns
// the pathname, and a hash route needs no server-side rewrite rule to survive
// a reload — nginx just serves index.html for `/` and the fragment is never
// sent to the server at all. `parseGuideHash` accepts either a bare
// `location.hash` or a full href/URL so the shell can read
// `window.location.hash` directly and a test can pass a whole string.
//
// Pure: no React, no window access — the hook that binds this to view state
// lives in the shell (`examples/web/src/App.tsx`), same split as
// `permalink.ts` / `useSessionPermalink.ts`.

export const GUIDE_HASH_PREFIX = '#/guide'

/** `null` slug → the guide's index (no page selected yet). */
export function buildGuideHash(slug: string | null): string {
  return slug ? `${GUIDE_HASH_PREFIX}/${encodeURIComponent(slug)}` : GUIDE_HASH_PREFIX
}

/**
 * Parse a guide route out of a hash or href.
 *
 * Returns `''` for the bare index (`#/guide`), the decoded slug for
 * `#/guide/<slug>`, or `null` when the input names no guide route at all
 * (including the empty string, and any other page's hash).
 */
export function parseGuideHash(input: string): string | null {
  if (!input) return null
  const hashIdx = input.indexOf('#')
  const hash = hashIdx === -1 ? input : input.slice(hashIdx)
  if (hash !== GUIDE_HASH_PREFIX && !hash.startsWith(`${GUIDE_HASH_PREFIX}/`)) return null
  const rest = hash.slice(GUIDE_HASH_PREFIX.length).replace(/^\//, '')
  if (rest === '') return ''
  try {
    return decodeURIComponent(rest)
  } catch {
    // Malformed percent-escape — treat the raw segment as the slug rather
    // than throwing out of a render path (mirrors permalink.ts's safeDecode).
    return rest
  }
}
