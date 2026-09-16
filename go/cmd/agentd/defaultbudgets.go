package main

// defaultbudgets.go — the two boot-time knobs behind onboarding-work-plan
// §1.3: the daily token budgets a BRAND NEW project starts with.
//
//	AGENTKIT_DEFAULT_DAILY_TOKENS_SOFT
//	AGENTKIT_DEFAULT_DAILY_TOKENS_HARD
//
// Both are int64 token counts. Unset or "0" means today's behaviour — the
// budget is off, exactly as agentdb.DefaultProjectSettings has always
// returned before this existed. They apply ONLY to a project that has never
// had a settings row written: PutProjectSettings/GetProjectSettings never
// consult these values once a row exists, so a project created before this
// was set, or after it changes, keeps whatever it already has.
//
// agentdb must not read the environment itself (CLAUDE.md's liftability
// invariant — the engine imports nothing host-specific), so resolution
// happens here, once, at boot, and the result is handed to agentdb through
// its one setter (agentdb.SetDefaultBudgets) rather than a package-global env
// read inside agentdb. A malformed value is a boot error naming the
// variable, the same shape as parseDatasetMaxBytes/parseGCDuration: silently
// falling back to "off" would mean an operator who set a budget and typo'd it
// never finds out their new projects are unbraked.
//
// Resolution is pure and unit-tested, like portrange.go / gc.go / backends.go.

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultDailyTokensSoftVar = "AGENTKIT_DEFAULT_DAILY_TOKENS_SOFT"
	defaultDailyTokensHardVar = "AGENTKIT_DEFAULT_DAILY_TOKENS_HARD"
)

// resolveDefaultBudgets reads both env vars and returns the (soft, hard)
// pair to hand to agentdb.SetDefaultBudgets.
func resolveDefaultBudgets(env func(string) string) (soft, hard int64, err error) {
	soft, err = parseDefaultBudgetVar(defaultDailyTokensSoftVar, env(defaultDailyTokensSoftVar))
	if err != nil {
		return 0, 0, err
	}
	hard, err = parseDefaultBudgetVar(defaultDailyTokensHardVar, env(defaultDailyTokensHardVar))
	if err != nil {
		return 0, 0, err
	}
	return soft, hard, nil
}

// parseDefaultBudgetVar parses one of the two env vars above: empty means 0
// (off); otherwise a non-negative whole number of tokens.
func parseDefaultBudgetVar(name, raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a whole number of tokens", name, raw)
	}
	if n < 0 {
		return 0, fmt.Errorf("%s: %q must not be negative (0, or unset, means off)", name, raw)
	}
	return n, nil
}
