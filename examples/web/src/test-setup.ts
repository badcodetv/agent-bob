// Mirrors `web/src/test-setup.ts`. Kept as a separate copy rather than shared:
// `web/` is a published package and this app is its consumer, so importing the
// library's test harness across the package boundary would be exactly the
// source-folder coupling O12 removed.
import { expect, afterEach } from 'vitest'
import * as matchers from '@testing-library/jest-dom/matchers'
expect.extend(matchers)

// Unmount React trees between tests so repeated render() calls in one file do
// not leak DOM. Only meaningful under jsdom — guarded so node-env .ts tests
// skip it.
if (typeof document !== 'undefined') {
  afterEach(async () => {
    const { cleanup } = await import('@testing-library/react')
    cleanup()
  })
}
