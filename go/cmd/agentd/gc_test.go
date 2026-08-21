package main

import (
	"strings"
	"testing"
	"time"
)

// The session-GC knobs (gc.go). Two facts are worth pinning above all: the
// DEFAULTS, because they are what every host that configures nothing gets, and
// the REJECTIONS, because a garbage collector that silently does not run is the
// exact failure this file exists to end.

func TestResolveGCConfig(t *testing.T) {
	cases := []struct {
		name       string
		env        map[string]string
		wantIdle   time.Duration
		wantReap   time.Duration
		wantDSReap time.Duration
		wantDSKeep int
	}{
		{
			name:       "unset → 30m archive, 6h sweep, 6h dataset reap, keep 30",
			env:        nil,
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			// This is how compose delivers an unset variable: `${VAR:-}`.
			name: "empty (compose's ${VAR:-}) → the same defaults",
			env: map[string]string{
				sessionIdleTimeoutVar:   "",
				snapshotReapIntervalVar: "",
				datasetReapIntervalVar:  "",
				datasetKeepVersionsVar:  "",
			},
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "explicit durations",
			env:        map[string]string{sessionIdleTimeoutVar: "5m", snapshotReapIntervalVar: "12h"},
			wantIdle:   5 * time.Minute,
			wantReap:   12 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "surrounding space trimmed",
			env:        map[string]string{sessionIdleTimeoutVar: "  90m  "},
			wantIdle:   90 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "compound duration",
			env:        map[string]string{sessionIdleTimeoutVar: "1h30m"},
			wantIdle:   90 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "off disables archiving only",
			env:        map[string]string{sessionIdleTimeoutVar: "off"},
			wantIdle:   0,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "OFF is case-insensitive",
			env:        map[string]string{sessionIdleTimeoutVar: "OFF", snapshotReapIntervalVar: "Never"},
			wantIdle:   0,
			wantReap:   0,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "0 means off (it is what the Policy field itself uses)",
			env:        map[string]string{sessionIdleTimeoutVar: "0", snapshotReapIntervalVar: "0"},
			wantIdle:   0,
			wantReap:   0,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "the floor itself is accepted",
			env:        map[string]string{sessionIdleTimeoutVar: "1m", snapshotReapIntervalVar: "1m"},
			wantIdle:   time.Minute,
			wantReap:   time.Minute,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "the cap itself is accepted",
			env:        map[string]string{sessionIdleTimeoutVar: "720h", snapshotReapIntervalVar: "720h"},
			wantIdle:   720 * time.Hour,
			wantReap:   720 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "dataset reap interval: explicit duration, disabled words, floor and cap all reuse parseGCDuration",
			env:        map[string]string{datasetReapIntervalVar: "12h"},
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 12 * time.Hour,
			wantDSKeep: 30,
		},
		{
			name:       "dataset reap interval: off disables only the dataset reaper",
			env:        map[string]string{datasetReapIntervalVar: "off"},
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 0,
			wantDSKeep: 30,
		},
		{
			name:       "dataset keep-versions: explicit integer",
			env:        map[string]string{datasetKeepVersionsVar: "7"},
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 7,
		},
		{
			name:       "dataset keep-versions: 0 means keep EVERYTHING, not disabled — the opposite of the duration knobs' 0",
			env:        map[string]string{datasetKeepVersionsVar: "0"},
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 0,
		},
		{
			name:       "dataset keep-versions: the maximum itself is accepted",
			env:        map[string]string{datasetKeepVersionsVar: "10000"},
			wantIdle:   30 * time.Minute,
			wantReap:   6 * time.Hour,
			wantDSReap: 6 * time.Hour,
			wantDSKeep: 10000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveGCConfig(envMap(tc.env))
			if err != nil {
				t.Fatalf("resolveGCConfig: %v", err)
			}
			if got.idleTimeout != tc.wantIdle {
				t.Errorf("idleTimeout = %v, want %v", got.idleTimeout, tc.wantIdle)
			}
			if got.reapInterval != tc.wantReap {
				t.Errorf("reapInterval = %v, want %v", got.reapInterval, tc.wantReap)
			}
			if got.datasetReapInterval != tc.wantDSReap {
				t.Errorf("datasetReapInterval = %v, want %v", got.datasetReapInterval, tc.wantDSReap)
			}
			if got.datasetKeepVersions != tc.wantDSKeep {
				t.Errorf("datasetKeepVersions = %v, want %v", got.datasetKeepVersions, tc.wantDSKeep)
			}
		})
	}
}

// Every rejection stops the process at boot. A host that starts with a broken
// GC setting looks healthy and reclaims nothing, which is indistinguishable
// from the bug this change fixes.
func TestResolveGCConfig_Rejections(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantWord string // a phrase the operator needs to see
	}{
		{"bare number has no unit", map[string]string{sessionIdleTimeoutVar: "30"}, "not a duration"},
		{"words are not durations", map[string]string{sessionIdleTimeoutVar: "half an hour"}, "not a duration"},
		{"negative", map[string]string{sessionIdleTimeoutVar: "-5m"}, "negative"},
		{"below the floor", map[string]string{sessionIdleTimeoutVar: "30s"}, "minimum"},
		{"a millisecond", map[string]string{sessionIdleTimeoutVar: "5ms"}, "minimum"},
		{"beyond the cap", map[string]string{sessionIdleTimeoutVar: "1000h"}, "maximum"},
		{"reap: bare number", map[string]string{snapshotReapIntervalVar: "6"}, "not a duration"},
		{"reap: below the floor", map[string]string{snapshotReapIntervalVar: "1s"}, "minimum"},
		{"reap: beyond the cap", map[string]string{snapshotReapIntervalVar: "8760h"}, "maximum"},
		{"reap: negative", map[string]string{snapshotReapIntervalVar: "-1h"}, "negative"},
		{"dataset reap interval: bare number", map[string]string{datasetReapIntervalVar: "6"}, "not a duration"},
		{"dataset reap interval: below the floor", map[string]string{datasetReapIntervalVar: "30s"}, "minimum"},
		{"dataset reap interval: beyond the cap", map[string]string{datasetReapIntervalVar: "1000h"}, "maximum"},
		{"dataset reap interval: negative", map[string]string{datasetReapIntervalVar: "-5m"}, "negative"},
		{"dataset keep-versions: not an integer — a duration string is rejected here too", map[string]string{datasetKeepVersionsVar: "6h"}, "not an integer"},
		{"dataset keep-versions: words", map[string]string{datasetKeepVersionsVar: "thirty"}, "not an integer"},
		{"dataset keep-versions: negative", map[string]string{datasetKeepVersionsVar: "-1"}, "negative"},
		{"dataset keep-versions: beyond the maximum", map[string]string{datasetKeepVersionsVar: "10001"}, "maximum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveGCConfig(envMap(tc.env))
			if err == nil {
				t.Fatalf("expected a boot error for %v", tc.env)
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantWord)
			}
			// Whatever went wrong, the operator must be told which variable and
			// how to turn the loop off deliberately.
			if !strings.Contains(err.Error(), "AGENTKIT_") {
				t.Errorf("error %q does not name the variable", err.Error())
			}
		})
	}
}

// The boot log is the only place an operator sees what their host will do. It
// must say that archiving is not deletion — otherwise "sessions are being
// reclaimed" reads as data loss.
func TestGCConfigBootLines(t *testing.T) {
	on := gcConfig{idleTimeout: 30 * time.Minute, reapInterval: 6 * time.Hour}
	if got := on.describeIdleTimeout(); !strings.Contains(got, "30m") || !strings.Contains(got, "resumes") {
		t.Errorf("idle line does not state the timeout and that the session survives: %q", got)
	}
	if got := on.describeReapInterval(true); !strings.Contains(got, "6h") {
		t.Errorf("reap line does not state the interval: %q", got)
	}
	off := gcConfig{}
	if got := off.describeIdleTimeout(); !strings.Contains(got, "DISABLED") || !strings.Contains(got, "host port") {
		t.Errorf("disabled idle line must say what it costs: %q", got)
	}
	if got := off.describeReapInterval(true); !strings.Contains(got, "DISABLED") {
		t.Errorf("disabled reap line: %q", got)
	}
	// No Postgres → no catalogue to sweep, and the reason must be the missing
	// database rather than the (perfectly valid) interval.
	if got := on.describeReapInterval(false); !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("unwired reap line must blame the missing database: %q", got)
	}

	// The dataset reaper's boot line (O8) names both variables — the interval
	// AND the keep-versions count — because a reap that keeps the wrong number
	// of versions is as invisible to an operator as one that never runs at all.
	dsOn := gcConfig{datasetReapInterval: 6 * time.Hour, datasetKeepVersions: 30}
	if got := dsOn.describeDatasetReap(true); !strings.Contains(got, "6h") {
		t.Errorf("dataset reap line does not state the interval: %q", got)
	} else if !strings.Contains(got, "30") {
		t.Errorf("dataset reap line does not state the keep-versions count: %q", got)
	} else if !strings.Contains(got, datasetReapIntervalVar) || !strings.Contains(got, datasetKeepVersionsVar) {
		t.Errorf("dataset reap line does not name both variables: %q", got)
	}

	dsOff := gcConfig{datasetReapInterval: 0, datasetKeepVersions: 30}
	if got := dsOff.describeDatasetReap(true); !strings.Contains(got, "DISABLED") {
		t.Errorf("disabled dataset reap line: %q", got)
	}

	// keep-versions=0 means "keep everything" and must NOT read as disabled —
	// the exact inversion the ticket calls out.
	dsKeepAll := gcConfig{datasetReapInterval: 6 * time.Hour, datasetKeepVersions: 0}
	if got := dsKeepAll.describeDatasetReap(true); strings.Contains(got, "DISABLED") {
		t.Errorf("keep-versions=0 must not read as disabled: %q", got)
	} else if !strings.Contains(strings.ToLower(got), "every") {
		t.Errorf("keep-versions=0 line must say it keeps everything: %q", got)
	}

	// No Postgres → no catalogue, exactly like the snapshot reaper.
	if got := dsOn.describeDatasetReap(false); !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("unwired dataset reap line must blame the missing database: %q", got)
	}
}
