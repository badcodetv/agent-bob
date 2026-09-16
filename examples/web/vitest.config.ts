// The app shell's test runner. It did not have one until 2026-09-12, and that
// absence is the root cause of DI29 in
// `design/2026-09-11-onboarding-work-plan.md`: the worst defect of the
// onboarding wave lived in `src/onboarding.ts`'s `useInterviewState`, where a
// one-line latch stopped the poll at mount and permanently blinded the console
// to an interview starting. Nothing offline could see it — `web/`'s 1711 tests
// cover `web/src`, and A2's own 59 tests fed `inInterview` in as a prop instead
// of computing it — so it took a real browser (the stack e2e) to find.
//
// Mirrors `web/vitest.config.ts`: node environment by default, jsdom opted into
// per file with a `// @vitest-environment jsdom` first line, and the same
// `src/test-setup.ts` shape.
//
// It MERGES the Vite config rather than restating it, and that is load-bearing.
// `src/onboarding.ts` imports `@agentkit/chat-ui`, which resolves through the
// three aliases in `vite.config.ts` to `web/dist` — the EMITTED package, the
// same surface the build and the typecheck resolve (O12). So `yarn test` here
// needs `npm ci && npm run build` in `web/` first, exactly as `yarn typecheck`
// and `yarn build` do, and fails the same way without it ("Cannot find module
// '@agentkit/chat-ui'"). Restating the aliases would let them drift; merging
// cannot.
//
// ONE DIVERGENCE TO KNOW ABOUT. vitest 4 declares a peer dependency on vite
// >=6 and this app builds on vite 5, so yarn installs vitest its OWN nested
// vite (node_modules/vitest/node_modules/vite). Tests therefore transform
// through a different vite major from the one `yarn build` uses, which is where
// the `configLoader: 'native'` and rolldown warnings on every run come from.
// Accepted rather than resolved: vitest 4 matches `web/`'s pinned version, and
// the alternative — moving the shell the stack SERVES to vite 7 — is a
// production change, not a test-tooling one. Revisit if a test ever passes here
// and fails in the browser.
import { defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config.ts'

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
      setupFiles: ['./src/test-setup.ts'],
    },
  }),
)
