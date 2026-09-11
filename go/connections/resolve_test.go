package connections

import "testing"

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	specs := map[string]map[string]Spec{
		"wolf": {
			"github": validBearer(),
			"gmail":  validGoogle(), // unavailable: none of its env vars are set below
		},
	}
	env := map[string]string{"WOLF_GITHUB_PAT": "tok"}
	reg, err := NewRegistry(specs, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func TestServers_BasicEntry(t *testing.T) {
	reg := testRegistry(t)
	out := Servers(reg, "wolf", []string{"github"}, "https://agentd.example.com")
	entry, ok := out["github"]
	if !ok {
		t.Fatalf("want a github entry, got %+v", out)
	}
	if entry.URL != "https://agentd.example.com/connect/github/" {
		t.Fatalf("URL = %q", entry.URL)
	}
	if entry.Headers["Authorization"] != "${SESSION_TOKEN}" {
		t.Fatalf("Headers = %+v", entry.Headers)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestServers_TrimsTrailingSlash(t *testing.T) {
	reg := testRegistry(t)
	out := Servers(reg, "wolf", []string{"github"}, "https://agentd.example.com/")
	entry := out["github"]
	if entry.URL != "https://agentd.example.com/connect/github/" {
		t.Fatalf("URL = %q, want no doubled slash", entry.URL)
	}
}

func TestServers_ExpandsWildcard(t *testing.T) {
	reg := testRegistry(t)
	out := Servers(reg, "wolf", []string{"*"}, "https://agentd.example.com")
	// gmail's env vars are unset (see testRegistry) so only github should appear.
	if _, ok := out["github"]; !ok {
		t.Fatalf("want github present, got %+v", out)
	}
	if _, ok := out["gmail"]; ok {
		t.Fatalf("gmail is unavailable and must be dropped, got %+v", out)
	}
}

func TestServers_DropsUnknownAndUnavailable(t *testing.T) {
	reg := testRegistry(t)
	out := Servers(reg, "wolf", []string{"github", "gmail", "does-not-exist"}, "https://agentd.example.com")
	if len(out) != 1 {
		t.Fatalf("want only github, got %+v", out)
	}
	if _, ok := out["github"]; !ok {
		t.Fatalf("want github present, got %+v", out)
	}
}

func TestServers_EmptyGrantsEmptyOutput(t *testing.T) {
	reg := testRegistry(t)
	out := Servers(reg, "wolf", nil, "https://agentd.example.com")
	if len(out) != 0 {
		t.Fatalf("want no entries, got %+v", out)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestServers_NilRegistry(t *testing.T) {
	var reg *Registry
	out := Servers(reg, "wolf", []string{"*", "github"}, "https://agentd.example.com")
	if len(out) != 0 {
		t.Fatalf("want no entries against a nil registry, got %+v", out)
	}
}
