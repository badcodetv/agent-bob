import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    environment: 'node',
    // 20s, not vitest's 5s default. This suite is ~1,700 tests and many drive
    // `@testing-library/user-event`, which types a character at a time against
    // real timers; on a loaded machine 2-6 of them cross 5s and report as
    // FAILURES rather than as timeouts. Measured on untouched main: the whole
    // suite gave 6 failed / 1542 passed, the same three files re-run alone gave
    // 71 passed / 0 failed, and the whole suite at this timeout gave
    // 1549/1549. So the default was making a green suite unreadable — an agent
    // cannot tell a real regression from contention — which is worse than a
    // slow test. See DI8 in design/2026-09-11-onboarding-work-plan.md.
    //
    // It is a CEILING, not a wait: a passing test still finishes in its own
    // time, so the suite's duration is unchanged. Only a genuinely hung test
    // now takes 20s to say so.
    testTimeout: 20_000,
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    setupFiles: ['./src/test-setup.ts'],
  },
})
