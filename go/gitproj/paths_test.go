package gitproj

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	long64 := strings.Repeat("a", 64)
	ok63 := strings.Repeat("a", 63)

	tests := []struct {
		name    string
		in      string
		wantErr string // substring; "" = must pass
	}{
		{name: "simple", in: "copywriter"},
		{name: "with digits", in: "worker-2"},
		{name: "internal dashes", in: "a-b-c"},
		{name: "single char", in: "a"},
		{name: "63 chars is the edge", in: ok63},
		{name: "empty", in: "", wantErr: "must not be empty"},
		{name: "too long", in: long64, wantErr: "exceeds max length"},
		{name: "uppercase", in: "Copywriter", wantErr: "invalid name"},
		{name: "leading dash", in: "-copywriter", wantErr: "invalid name"},
		{name: "trailing dash", in: "copywriter-", wantErr: "invalid name"},
		{name: "contains slash", in: "a/b", wantErr: "invalid name"},
		{name: "contains dot", in: "a.b", wantErr: "invalid name"},
		{name: "dotdot", in: "..", wantErr: "invalid name"},
		{name: "single dot", in: ".", wantErr: "invalid name"},
		{name: "space", in: "a b", wantErr: "invalid name"},
		{name: "underscore", in: "a_b", wantErr: "invalid name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestPathConstructors(t *testing.T) {
	tests := []struct {
		name string
		fn   func(subfolder, name string) (string, error)
		n    string
		want string
	}{
		{"worker", WorkerPath, "copywriter", "bob/workers/copywriter.md"},
		{"skill", SkillPath, "email-triage", "bob/skills/email-triage.md"},
		{"subscription", SubscriptionPath, "sub-123", "bob/subscriptions/sub-123.md"},
		{"schedule", SchedulePath, "sched-9", "bob/schedules/sched-9.md"},
		{"image", ImagePath, "core-v2", "bob/images/core-v2.md"},
		{"memory", MemoryPath, "message-board", "bob/memory/message-board.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn("", tc.n)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	if got := SettingsPath(""); got != "bob/settings.md" {
		t.Fatalf("SettingsPath: got %q", got)
	}

	custom, err := WorkerPath("proj-root", "copywriter")
	if err != nil || custom != "proj-root/workers/copywriter.md" {
		t.Fatalf("custom subfolder: got %q, err %v", custom, err)
	}

	if _, err := WorkerPath("", "Bad Name"); err == nil {
		t.Fatalf("want error for invalid name, got nil")
	}
}

func TestParsePathRoundTrip(t *testing.T) {
	tests := []struct {
		path     string
		wantKind Kind
		wantName string
	}{
		{"bob/settings.md", KindSettings, ""},
		{"bob/workers/copywriter.md", KindWorker, "copywriter"},
		{"bob/skills/email-triage.md", KindSkill, "email-triage"},
		{"bob/subscriptions/sub-123.md", KindSubscription, "sub-123"},
		{"bob/schedules/sched-9.md", KindSchedule, "sched-9"},
		{"bob/images/core-v2.md", KindImage, "core-v2"},
		{"bob/memory/message-board.md", KindMemory, "message-board"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			kind, name, err := ParsePath("", tc.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if kind != tc.wantKind || name != tc.wantName {
				t.Fatalf("got (%q, %q), want (%q, %q)", kind, name, tc.wantKind, tc.wantName)
			}
		})
	}
}

func TestParsePathCustomSubfolder(t *testing.T) {
	kind, name, err := ParsePath("proj-root", "proj-root/workers/copywriter.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if kind != KindWorker || name != "copywriter" {
		t.Fatalf("got (%q, %q)", kind, name)
	}
}

func TestParsePathRejections(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"dotdot traversal", "bob/workers/../../../etc/passwd"},
		{"dotdot segment", "bob/../secrets.md"},
		{"single dot segment", "bob/./workers/x.md"},
		{"absolute path", "/bob/workers/copywriter.md"},
		{"outside subfolder", ".github/workflows/x.yml"},
		{"uppercase name", "bob/workers/Copywriter.md"},
		{"empty segment", "bob//x.md"},
		{"trailing slash", "bob/workers/"},
		{"name with slash smuggled via extra segment", "bob/workers/a/b.md"},
		{"unknown directory", "bob/nope/x.md"},
		{"missing extension", "bob/workers/copywriter"},
		{"empty path", ""},
		{"name too long", "bob/workers/" + strings.Repeat("a", 64) + ".md"},
		{"name with leading dash", "bob/workers/-bad.md"},
		{"name with trailing dash", "bob/workers/bad-.md"},
		{"not under subfolder at all", "elsewhere/settings.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParsePath("", tc.path)
			if err == nil {
				t.Fatalf("want error for path %q, got nil", tc.path)
			}
		})
	}
}

func TestParsePathNeverEscapesSubfolder(t *testing.T) {
	// However it's spelled, anything ParsePath accepts must, when rejoined,
	// stay under the subfolder. This is the property that matters: §D of
	// the design doc calls an unvalidated name a path-traversal primitive
	// that could write into e.g. .github/workflows in the project's own
	// repo.
	candidates := []string{
		"bob/workers/copywriter.md",
		"bob/workers/../workers/copywriter.md",
		"bob/workers/%2e%2e/x.md",
		"bob\\workers\\copywriter.md",
	}
	for _, path := range candidates {
		kind, name, err := ParsePath("", path)
		if err != nil {
			continue // rejected outright is fine
		}
		rejoined, rerr := WorkerPath("", name)
		if kind == KindWorker {
			if rerr != nil {
				t.Fatalf("path %q parsed to unrejoinable name %q: %v", path, name, rerr)
			}
			if !strings.HasPrefix(rejoined, "bob/") {
				t.Fatalf("path %q parsed to name %q which rejoins outside bob/: %q", path, name, rejoined)
			}
		}
	}
}
