package main

import (
	"strings"
	"testing"
)

func TestParseDefaultBudgetVar(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{name: "unset means off", raw: "", want: 0},
		{name: "explicit zero means off", raw: "0", want: 0},
		{name: "a positive count", raw: "50000", want: 50000},
		{name: "whitespace is trimmed", raw: "  1234  ", want: 1234},
		{name: "negative is refused", raw: "-1", wantErr: true},
		{name: "not a number is refused", raw: "lots", wantErr: true},
		{name: "a duration string is refused", raw: "30m", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDefaultBudgetVar("AGENTKIT_DEFAULT_DAILY_TOKENS_SOFT", tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseDefaultBudgetVar(%q) = %d, nil; want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDefaultBudgetVar(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseDefaultBudgetVar(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResolveDefaultBudgets(t *testing.T) {
	t.Run("both unset means off/off", func(t *testing.T) {
		soft, hard, err := resolveDefaultBudgets(envFrom(nil))
		if err != nil {
			t.Fatalf("resolveDefaultBudgets: %v", err)
		}
		if soft != 0 || hard != 0 {
			t.Fatalf("soft=%d hard=%d, want 0/0", soft, hard)
		}
	})

	t.Run("both set", func(t *testing.T) {
		soft, hard, err := resolveDefaultBudgets(envFrom(map[string]string{
			defaultDailyTokensSoftVar: "50000",
			defaultDailyTokensHardVar: "100000",
		}))
		if err != nil {
			t.Fatalf("resolveDefaultBudgets: %v", err)
		}
		if soft != 50000 || hard != 100000 {
			t.Fatalf("soft=%d hard=%d, want 50000/100000", soft, hard)
		}
	})

	t.Run("a bad hard value is a boot error naming the variable", func(t *testing.T) {
		_, _, err := resolveDefaultBudgets(envFrom(map[string]string{
			defaultDailyTokensSoftVar: "1000",
			defaultDailyTokensHardVar: "not-a-number",
		}))
		if err == nil {
			t.Fatalf("want an error")
		}
		if got := err.Error(); !strings.Contains(got, defaultDailyTokensHardVar) {
			t.Fatalf("error %q does not name %s", got, defaultDailyTokensHardVar)
		}
	})
}
