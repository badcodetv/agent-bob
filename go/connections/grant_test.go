package connections

import (
	"strings"
	"testing"
)

func TestHolds(t *testing.T) {
	tests := []struct {
		name string
		held []string
		want string
		ok   bool
	}{
		{"exact", []string{"github", "gmail"}, "github", true},
		{"wildcard", []string{"*"}, "anything", true},
		{"miss", []string{"github"}, "gmail", false},
		{"empty", nil, "github", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Holds(tc.held, tc.want); got != tc.ok {
				t.Fatalf("Holds(%v, %q) = %v, want %v", tc.held, tc.want, got, tc.ok)
			}
		})
	}
}

func TestCanGrant(t *testing.T) {
	tests := []struct {
		name    string
		held    []string
		before  []string
		after   []string
		wantErr bool
	}{
		{"add held name ok", []string{"github"}, nil, []string{"github"}, false},
		{"add unheld name refused", []string{"github"}, nil, []string{"gmail"}, true},
		{"add wildcard refused without wildcard", []string{"github"}, nil, []string{"*"}, true},
		{"wildcard holder adds anything", []string{"*"}, nil, []string{"github", "gmail", "*"}, false},
		{"removal always ok, no holdings", nil, []string{"github"}, nil, false},
		{"removal always ok, some holdings", []string{"gmail"}, []string{"github", "gmail"}, []string{"gmail"}, false},
		{"unchanged list ok", []string{"github"}, []string{"github"}, []string{"github"}, false},
		{"unchanged list ok even with no holdings", nil, []string{"github"}, []string{"github"}, false},
		{"nil vs empty equivalent (no additions)", []string{}, nil, []string{}, false},
		{"nil vs empty equivalent (addition refused)", []string{}, nil, []string{"github"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CanGrant(tc.held, tc.before, tc.after)
			if tc.wantErr && err == nil {
				t.Fatalf("CanGrant(%v, %v, %v): want error, got nil", tc.held, tc.before, tc.after)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("CanGrant(%v, %v, %v): want nil, got %v", tc.held, tc.before, tc.after, err)
			}
		})
	}
}

func TestCanGrant_NamesTheOffendingConnection(t *testing.T) {
	err := CanGrant([]string{"github"}, nil, []string{"github", "gmail"})
	if err == nil {
		t.Fatalf("want error")
	}
	if got := err.Error(); !strings.Contains(got, "gmail") {
		t.Fatalf("error should name the offending connection %q, got %q", "gmail", got)
	}
}

func TestCovers(t *testing.T) {
	tests := []struct {
		name   string
		held   []string
		target []string
		want   bool
	}{
		{"empty target always covered", nil, nil, true},
		{"empty target even with no holdings", []string{}, []string{}, true},
		{"subset covered", []string{"github", "gmail"}, []string{"github"}, true},
		{"superset refused", []string{"github"}, []string{"github", "gmail"}, false},
		{"exact match covered", []string{"github"}, []string{"github"}, true},
		{"wildcard target needs wildcard held", []string{"github", "gmail"}, []string{"*"}, false},
		{"wildcard target with wildcard held ok", []string{"*"}, []string{"*"}, true},
		{"wildcard target vs full explicit held refused", []string{"github", "gmail"}, []string{"*"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Covers(tc.held, tc.target); got != tc.want {
				t.Fatalf("Covers(%v, %v) = %v, want %v", tc.held, tc.target, got, tc.want)
			}
		})
	}
}
