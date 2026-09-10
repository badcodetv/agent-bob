#!/usr/bin/env bash
#
# verify-package.sh — prove @agentkit/chat-ui is a package, not a folder.
#
# This is the R125 probe turned into a repeatable check. `npm run typecheck` and
# `npm test` both pass against a package that NOBODY CAN INSTALL: they compile
# source in place, with web/node_modules on the resolution path and a Vite alias
# standing in for the package name. Every failure mode that matters to a
# consumer lives outside that: a runtime import filed under devDependencies, an
# `exports` map that does not point at the emitted files, a second copy of React,
# a second emotion cache.
#
# So: build → pack → install the TARBALL into a throwaway app that has its own
# react/MUI/emotion and shares nothing with this repo → render a component →
# assert three things.
#
#   (a) the artifact filenames actually reach the DOM
#   (b) the render does not throw — two React copies raise an invalid-hook-call,
#       which is exactly what a mis-filed peer dependency produces
#   (c) a `live` status dot computes to the CONSUMER's success.main
#
# (c) is the whole reason the package exists. Bob's components are themed by
# the HOST's ThemeProvider, which is the one thing an iframe can never do
# (design/2026-08-24-agent-wolf-ui.md § 1). It is also the assertion a refactor
# will silently break: swap `success.main` for a literal hex and every other
# check here still passes.
#
# The throwaway app is created under `mktemp -d`, deliberately OUTSIDE this repo,
# so Node's resolver cannot walk up into web/node_modules and quietly satisfy an
# import the tarball failed to declare. Set KEEP_APP=1 to leave it on disk.
#
# Needs network (one `npm install` in the throwaway app). Exits non-zero on any
# failure. Run from anywhere: `web/scripts/verify-package.sh`.

set -euo pipefail

WEB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$WEB_DIR"

# The consumer's success.main. Deliberately NOT any colour in Bob's own
# palette, so a dot that resolves to Bob's default green fails loudly.
CONSUMER_SUCCESS_HEX="#00E5A0"
CONSUMER_SUCCESS_RGB="rgb(0, 229, 160)"

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

say "1/5  build"
npm run build

say "2/5  pack"
# --silent so the only thing on stdout is the tarball name.
TARBALL_NAME="$(npm pack --silent)"
TARBALL="$WEB_DIR/$TARBALL_NAME"
[ -f "$TARBALL" ] || { echo "npm pack produced no tarball"; exit 1; }
ls -l "$TARBALL"

# The tarball must carry the three entry points the `exports` map promises, and
# must NOT carry tests. Checked on the tarball rather than on dist/ because the
# tarball is what a consumer receives.
# Listed once into a variable: `tar … | grep -q` makes tar die of SIGPIPE, and
# under `set -o pipefail` that reads as a missing file.
LISTING="$(tar -tzf "$TARBALL")"
for want in package/dist/index.js package/dist/index.d.ts \
            package/dist/pure.js package/dist/pure.d.ts \
            package/dist/components/index.js package/dist/components/index.d.ts; do
  grep -qx "$want" <<<"$LISTING" || { echo "tarball is missing $want"; exit 1; }
done
if grep -E '\.test\.|__fixtures__|test-setup' <<<"$LISTING"; then
  echo "tarball ships test files"; exit 1
fi
echo "tarball: $(wc -l <<<"$LISTING") files, entry points present, no tests"

say "3/5  install the tarball into a throwaway app"
APP="$(mktemp -d "${TMPDIR:-/tmp}/chat-ui-verify.XXXXXX")"
cleanup() {
  if [ "${KEEP_APP:-0}" = "1" ]; then echo "throwaway app kept at $APP"; else rm -rf "$APP"; fi
}
trap cleanup EXIT
echo "throwaway app: $APP"

# Its own react/react-dom/@mui/emotion — nothing here is shared with web/.
# Versions match examples/web (React 18.3.1, MUI 6) because that is the pairing
# the visual language is built against.
cat > "$APP/package.json" <<JSON
{
  "name": "chat-ui-consumer",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "dependencies": {
    "@agentkit/chat-ui": "file:$TARBALL",
    "@emotion/react": "^11.14.0",
    "@emotion/styled": "^11.14.0",
    "@mui/material": "^6.1.7",
    "react": "18.3.1",
    "react-dom": "18.3.1"
  },
  "comment": "The test harness is NOT listed here. See the two-step install below."
}
JSON

# server.deps.inline is NOT optional and NOT ours to avoid: without it every
# import dies with
#   Directory import '.../@mui/material/utils' is not supported resolving ES
#   modules imported from .../@mui/icons-material/esm/utils/createSvgIcon.js
# which points at MUI's own ESM build, not at this package (R125, finding 1).
# Every consumer needs the same two lines — W28 carries them into Wolf.
cat > "$APP/vitest.config.ts" <<'JSON'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    environment: 'jsdom',
    include: ['*.test.tsx'],
    server: { deps: { inline: [/@mui/, /@agentkit/] } },
  },
})
JSON

cat > "$APP/consumer.test.tsx" <<TSX
// Written by web/scripts/verify-package.sh. Not part of any repo.
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { createTheme, ThemeProvider } from '@mui/material/styles'

// The two subpath entry points, imported by PACKAGE NAME — the whole point.
import { ArtifactPanel } from '@agentkit/chat-ui/components'
import { buildArtifactTree, type ArtifactInfo } from '@agentkit/chat-ui/pure'

// The consumer's own theme. Its success.main is a colour Bob does not use.
const theme = createTheme({ palette: { success: { main: '$CONSUMER_SUCCESS_HEX' } } })

const artifacts: ArtifactInfo[] = [
  { id: 'a1', fileName: 'scoreboard.csv', filePath: 'out/scoreboard.csv', label: 'scoreboard', artifactType: 'csv', source: 'auto', status: 'live' },
  { id: 'a2', fileName: 'gone.txt', filePath: 'out/gone.txt', label: 'gone', artifactType: 'file', source: 'auto', status: 'lost' },
]

describe('@agentkit/chat-ui installed from a tarball', () => {
  it('renders ArtifactPanel, and the live dot takes the CONSUMER theme colour', () => {
    // (b) two React copies raise "Invalid hook call" here rather than failing an
    // assertion, so the render itself is the check.
    render(
      <ThemeProvider theme={theme}>
        <ArtifactPanel artifacts={artifacts} sessionId="s1" />
      </ThemeProvider>,
    )

    // (a) the filenames reach the DOM.
    expect(screen.getByText('scoreboard.csv')).toBeTruthy()
    expect(screen.getByText('gone.txt')).toBeTruthy()

    // (c) the live entry's status dot resolves 'success.main' through the
    // consumer's theme. The dot is the first child of the entry row.
    const entries = screen.getAllByTestId('artifact-entry')
    const live = entries.find(e => e.getAttribute('data-lost') === null)
    if (!live) throw new Error('no live artifact entry rendered')
    const dot = live.firstElementChild
    if (!dot) throw new Error('live entry has no status dot')
    const colour = getComputedStyle(dot).backgroundColor
    // To a FILE, not to console.log: vitest's reporter swallows test-side
    // console output, and the script must be able to print the real value even
    // when the assertion below passes.
    writeFileSync(join(process.cwd(), 'dot-colour.txt'), colour)
    expect(colour).toBe('$CONSUMER_SUCCESS_RGB')

    // A control: the lost entry must NOT be the success colour, or the
    // assertion above would pass against a component that colours everything.
    const lost = entries.find(e => e.getAttribute('data-lost') === 'true')
    if (!lost || !lost.firstElementChild) throw new Error('no lost artifact entry rendered')
    expect(getComputedStyle(lost.firstElementChild).backgroundColor).not.toBe('$CONSUMER_SUCCESS_RGB')
  })

  it('tier 1 is usable on its own', () => {
    const tree = buildArtifactTree(artifacts)
    expect(tree).toBeTruthy()
  })
})
TSX

# TWO STEPS, and the split is load-bearing.
#
# Step one installs ONLY the subject and its runtime company — the tarball,
# react, react-dom, MUI, emotion — with npm's peer resolution fully strict.
# That is the install whose result this script asserts on: a runtime import
# mis-filed under devDependencies, or react moved from peerDependencies to
# dependencies, shows up here as a second nested copy and nowhere else.
#
# Step two adds the test harness (vitest, jsdom, testing-library) with
# --legacy-peer-deps, because npm 10.9.2's arborist CRASHES resolving vitest 4's
# own optional-peer graph:
#
#   TypeError: Cannot read properties of null (reading 'edgesOut')
#     at #loadPeerSet (@npmcli/arborist/lib/arborist/build-ideal-tree.js:1289)
#
# That is an upstream npm bug, not a fact about this package: a package.json
# containing nothing but `"vitest": "4.1.8"`, in an empty directory, reproduces
# it (verified 2026-09-09). web/ itself is immune only because `npm ci` installs
# from a lockfile and never builds the peer set. Confining the flag to step two
# keeps the strictness where the assertion lives; the shape check in 3b then
# runs against the FINAL tree, so if step two disturbed the subject's
# resolution, that check still catches it.
#
# Revisit when npm ships the arborist fix: drop step two's flag and re-run.
say "3a/5  the subject, with peer resolution strict"
( cd "$APP" && npm install --no-audit --no-fund >/dev/null 2>&1 ) \
  || { echo "npm install failed in the throwaway app"; ( cd "$APP" && npm install --no-audit --no-fund ); exit 1; }

say "3b/5  the test harness (see the arborist note above)"
( cd "$APP" && npm install --no-audit --no-fund --legacy-peer-deps --save-dev \
    "@testing-library/jest-dom@^6.9.1" "@testing-library/react@^16.3.2" \
    "jsdom@^29.1.1" "vitest@^4.0.18" \
    "@testing-library/dom@^10.4.1" "@types/react@^18.3.12" >/dev/null 2>&1 ) \
  || { echo "installing the test harness failed"; \
       ( cd "$APP" && npm install --no-audit --no-fund --legacy-peer-deps --save-dev \
           "@testing-library/jest-dom@^6.9.1" "@testing-library/react@^16.3.2" \
           "jsdom@^29.1.1" "vitest@^4.0.18" \
           "@testing-library/dom@^10.4.1" "@types/react@^18.3.12" ); exit 1; }
# @testing-library/dom and @types/react are the harness's OWN peers, named here
# because --legacy-peer-deps does not auto-install peers. Left out, they show up
# as "missing" and the tree is genuinely incomplete rather than merely loose.

# One React, one react-dom, one @mui/material, one emotion cache in the whole
# consumer tree. `npm ls` exits non-zero on an unmet peer dependency, which is
# the other half of the same question — so its exit code is checked, not
# discarded.
#
# It is asked about the SUBJECT and its runtime company by name rather than
# about the whole tree (`npm ls --all`), because the loosely-installed harness
# leaves resolvable-but-untidy edges that say nothing about this package: vite
# wants yaml@2 while emotion's build-time babel plugin pulls yaml@1 to the top,
# and npm reports that as ELSPROBLEMS for the entire tree. Naming the packages
# keeps the question pointed at the thing under test.
say "3c/5  dependency shape in the consumer"
( cd "$APP" && npm ls @agentkit/chat-ui react react-dom @mui/material @emotion/react >/dev/null 2>&1 ) \
  || { echo "npm ls reports an unmet or invalid dependency for the subject:"; \
       ( cd "$APP" && npm ls @agentkit/chat-ui react react-dom @mui/material @emotion/react ); exit 1; }

( cd "$APP" && npm ls --all --parseable 2>/dev/null | grep -E 'node_modules/(react|react-dom|@mui/material|@emotion/react)$' | sort -u ) \
  > "$APP/.resolved.txt" || true
cat "$APP/.resolved.txt"
for pkg in react react-dom @mui/material @emotion/react; do
  n=$(grep -c "node_modules/${pkg}\$" "$APP/.resolved.txt" || true)
  [ "$n" = "1" ] || { echo "expected exactly one copy of $pkg in the consumer, found $n"; exit 1; }
done

# The shipped tier-1 entry point, checked as SHIPPED rather than as source:
# walk dist/pure.js's own import graph inside the installed package and assert
# it names no package at all. A barrel that grows a React import breaks the tier
# line for every consumer and nothing else here would notice.
say "4/5  tier line, checked on the installed files"
node - "$APP/node_modules/@agentkit/chat-ui/dist/pure.js" <<'NODE'
import { readFileSync, existsSync } from 'node:fs'
import { dirname, resolve, basename } from 'node:path'
const entry = process.argv[2]
const seen = new Set(), pkgs = new Set()
const stack = [entry]
while (stack.length) {
  const f = stack.pop()
  if (seen.has(f)) continue
  seen.add(f)
  const re = /(?:from\s+|import\s*\(\s*)['"]([^'"]+)['"]/g
  let m
  const src = readFileSync(f, 'utf8')
  while ((m = re.exec(src))) {
    const s = m[1]
    if (!s.startsWith('.')) { pkgs.add(s); continue }
    const p = resolve(dirname(f), s)
    if (!existsSync(p)) { console.error('unresolved import', s, 'from', f); process.exit(1) }
    stack.push(p)
  }
}
const bad = [...seen].map(f => basename(f)).filter(n => n === 'AgentChat.js')
if (bad.length || pkgs.size) {
  console.error('dist/pure.js is not pure — packages:', [...pkgs], 'components:', bad)
  process.exit(1)
}
console.log(`dist/pure.js: ${seen.size} modules, 0 package imports, no AgentChat`)
NODE

say "5/5  render the component"
OUT="$APP/.vitest.out"
if ! ( cd "$APP" && npx vitest run ) > "$OUT" 2>&1; then
  cat "$OUT"
  echo
  echo "FAIL: the throwaway consumer could not render @agentkit/chat-ui"
  exit 1
fi
cat "$OUT"

[ -f "$APP/dot-colour.txt" ] || { echo "the consumer test recorded no dot colour"; exit 1; }
DOT="$(cat "$APP/dot-colour.txt")"
[ -n "$DOT" ] || { echo "the consumer test recorded an empty dot colour"; exit 1; }
[ "$DOT" = "$CONSUMER_SUCCESS_RGB" ] || { echo "dot colour was $DOT, expected $CONSUMER_SUCCESS_RGB"; exit 1; }

printf '\n\033[1;32mPASS\033[0m  %s installed into a throwaway app; ArtifactPanel rendered; live status dot computed to %s (the CONSUMER theme success.main, %s)\n' \
  "$TARBALL_NAME" "$DOT" "$CONSUMER_SUCCESS_HEX"
