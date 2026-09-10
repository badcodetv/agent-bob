// The sandbox's config defaults.
//
// DEFAULT_MODEL is the reason this file exists. It is the model EVERY
// session runs on: nothing else in the stack sets one unless a session
// explicitly asks (`SessionConfig.Model`), and agentd's SessionEnv is a
// fixed map, so a `DEFAULT_MODEL` on the HOST never reaches a session
// container. That made it a single unpinned string literal deciding both
// the cost and the quality of every agent turn — and it sat at
// claude-opus-4-5 for a year with nothing recording that as a choice.
import { describe, it, expect } from 'vitest'
import { ConfigSchema } from './config.js'

/** Parses with only the required fields, so the defaults are what is under test. */
function defaults(overrides: Record<string, string | undefined> = {}) {
  const parsed = ConfigSchema.safeParse({ ...overrides })
  if (!parsed.success) throw new Error(`config did not parse: ${parsed.error.message}`)
  return parsed.data
}

describe('sandbox config defaults', () => {
  it('runs sessions on claude-opus-5', () => {
    // A deliberate, dated decision (2026-09-07), not an accident of history.
    // If you are changing this, you are changing what every session costs
    // and how well it reasons — say so in the commit message.
    expect(defaults().DEFAULT_MODEL).toBe('claude-opus-5')
  })

  it('is NOT still on claude-opus-4-5', () => {
    // The specific regression: the value this replaced. Kept as its own case
    // so a revert reads as a revert rather than as a generic mismatch.
    expect(defaults().DEFAULT_MODEL).not.toBe('claude-opus-4-5')
  })

  it('lets a session override the model via DEFAULT_MODEL', () => {
    // How `SessionConfig.Model` reaches the harness: the Runner writes
    // DEFAULT_MODEL into the container env (go/runner.go:2750) and this
    // schema reads it. If the override stopped working, every session would
    // silently run on the default and nothing would report it.
    expect(defaults({ DEFAULT_MODEL: 'claude-haiku-4-5-20251001' }).DEFAULT_MODEL).toBe(
      'claude-haiku-4-5-20251001',
    )
  })

  it('keeps the other agent defaults it ships with', () => {
    const c = defaults()
    expect(c.DEFAULT_MAX_TURNS).toBe(100)
    expect(c.DEFAULT_THINKING_BUDGET_TOKENS).toBe(10000)
  })
})
