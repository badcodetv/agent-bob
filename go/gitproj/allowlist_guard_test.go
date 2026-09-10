package gitproj

import (
	"reflect"
	"sort"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
)

// guardedStructs binds each allowlist table to the real struct it describes.
//
// Adding a struct to the projection means adding it here AND giving every one
// of its fields a decision in allowlist.go. Nothing else in the package needs
// to change for the guard to start protecting it.
var guardedStructs = map[string]any{
	StructProjectSettings: agentdb.ProjectSettings{},
	StructWorker:          agentdb.Worker{},
	StructSkill:           agentdb.Skill{},
	StructSubscription:    agentdb.Subscription{},
	StructSchedule:        agentdb.Schedule{},
	StructCustomImage:     agentdb.CustomImage{},
}

// TestAllowlistCoversEveryField is the point of the allowlist ticket, and it is
// load-bearing in the same way TestMutationsAreLogged is in agentdb: it makes
// an omission a build failure instead of a leak.
//
// The failure mode it exists to stop is entirely ordinary. Somebody adds a
// column — `slack_bot_token`, `webhook_secret`, an `api_key` on a worker —
// three months from now, in a ticket that has nothing to do with git. The
// renderer, driven by reflection over the struct, picks it up. It is published
// to a repository with collaborators and permanent history on the next config
// change, and nobody notices until it is far too late to un-publish. So: a new
// field must be a DECISION, recorded in gitproj/allowlist.go, or the build
// stops.
func TestAllowlistCoversEveryField(t *testing.T) {
	for _, structName := range sortedKeys(guardedStructs) {
		structName := structName
		t.Run(structName, func(t *testing.T) {
			rules, ok := Rules(structName)
			if !ok {
				t.Fatalf("gitproj/allowlist.go has no rule table for %s, but guardedStructs says it is rendered.\n"+
					"Add a table for it in allowlist.go and register it in the allowlist map.", structName)
			}

			declared := make(map[string]bool, len(rules))
			for _, r := range rules {
				if declared[r.Field] {
					t.Errorf("%s: field %q has more than one rule in gitproj/allowlist.go. "+
						"Exactly one decision per field — delete the duplicate.", structName, r.Field)
				}
				declared[r.Field] = true
			}

			actual := exportedFields(guardedStructs[structName])

			// 1. Deny by default. A field on the struct that the allowlist does
			//    not name is the leak this test exists to prevent.
			var undecided []string
			for _, f := range actual {
				if !declared[f] {
					undecided = append(undecided, f)
				}
			}
			if len(undecided) > 0 {
				sort.Strings(undecided)
				t.Errorf("these fields of agentdb.%s have NO entry in the git-projection allowlist: %v\n\n"+
					"Nothing may be rendered into a git repository without a recorded decision, because a repo\n"+
					"has collaborators, forks and permanent history and the database does not.\n\n"+
					"Fix: open go/gitproj/allowlist.go, find the %s table, and add one Rule per field above with\n"+
					"  Decision: Render            — safe to publish as-is, or\n"+
					"  Decision: RenderEnvRefOnly  — may hold a credential; publishable ONLY as a whole-value\n"+
					"                                ${VAR} reference (add Leaves if it is a jsonb blob and only\n"+
					"                                some positions inside it are credential-bearing), or\n"+
					"  Decision: Never             — not rendered at all,\n"+
					"and a Reason saying why. If you are unsure, the answer is Never: publishing a secret cannot\n"+
					"be undone, and not publishing a field can.",
					structName, undecided, structName)
			}

			// 2. The table cannot outlive the struct. A rule naming a field that
			//    no longer exists means a rename happened and one side moved;
			//    the stale entry would silently stop protecting the renamed
			//    field while looking like it still did.
			present := make(map[string]bool, len(actual))
			for _, f := range actual {
				present[f] = true
			}
			var stale []string
			for _, r := range rules {
				if !present[r.Field] {
					stale = append(stale, r.Field)
				}
			}
			if len(stale) > 0 {
				sort.Strings(stale)
				t.Errorf("the git-projection allowlist names fields that agentdb.%s no longer has: %v\n"+
					"Most likely they were renamed. Update the %s table in go/gitproj/allowlist.go to the new\n"+
					"names — a stale entry protects nothing while looking like it does.",
					structName, stale, structName)
			}
		})
	}
}

// TestAllowlistRulesAreWellFormed pins the invariants the tables must hold, so
// a half-filled entry cannot pass the coverage check above by existing.
func TestAllowlistRulesAreWellFormed(t *testing.T) {
	for _, structName := range sortedKeys(guardedStructs) {
		rules, _ := Rules(structName)
		for _, r := range rules {
			if r.Reason == "" {
				t.Errorf("%s.%s has no Reason. Every decision records why, so the next engineer inherits the\n"+
					"reasoning and not just the verdict.", structName, r.Field)
			}
			switch r.Decision {
			case Never:
				if r.Key != "" {
					t.Errorf("%s.%s is Never but declares frontmatter key %q. A field that is not rendered has\n"+
						"no key; the non-empty key suggests somebody meant Render.", structName, r.Field, r.Key)
				}
				if len(r.Leaves) > 0 {
					t.Errorf("%s.%s is Never but declares leaf selectors, which are only meaningful for\n"+
						"RenderEnvRefOnly.", structName, r.Field)
				}
			case Render, RenderEnvRefOnly:
				if r.Key == "" {
					t.Errorf("%s.%s renders but has no frontmatter Key. G4 builds the file from Key; an empty\n"+
						"one would drop the field silently.", structName, r.Field)
				}
				if len(r.Leaves) > 0 && r.Decision != RenderEnvRefOnly {
					t.Errorf("%s.%s declares leaf selectors but is not RenderEnvRefOnly.", structName, r.Field)
				}
			default:
				t.Errorf("%s.%s has unknown Decision %q.", structName, r.Field, r.Decision)
			}
		}
	}
}

// TestNotImportableFieldsArePinned freezes §D's list of fields the git importer
// (G11) must never write. It is a separate test from the coverage guard because
// it protects a different thing: not "is a secret published" but "can a commit
// redirect a project's projection at a repo the committer controls".
//
// If this list needs to change, that is a security decision about the importer,
// not a tidy-up.
func TestNotImportableFieldsArePinned(t *testing.T) {
	// GitWebhookSecretEnv joined the set in G20 (the webhook's HMAC secret had
	// nowhere to live, DI11). That was exactly the deliberate security decision
	// this test asks for: a commit that could rewrite it would choose which
	// secret makes an inbound delivery verify.
	want := []string{"GitBranch", "GitRemote", "GitSubfolder", "GitTokenEnv", "GitWebhookSecretEnv"}

	var got []string
	for _, structName := range sortedKeys(guardedStructs) {
		rules, _ := Rules(structName)
		for _, r := range rules {
			if r.NotImportable {
				got = append(got, r.Field)
			}
		}
	}
	sort.Strings(got)

	if !reflect.DeepEqual(want, got) {
		t.Fatalf("the set of not-importable fields changed.\nwant %v\ngot  %v\n"+
			"§D of design/2026-09-09-git-projection.md: the four git-projection settings render as\n"+
			"informational but are ignored by the importer, because anyone with commit access to the\n"+
			"mirror could otherwise point a project's projection — and its push credential — at a\n"+
			"repository they control. Changing this set is a security decision.", want, got)
	}
}

// exportedFields returns the exported field names of a struct, flattening
// embedded structs the way encoding/json and GORM both do, so an embedded
// struct cannot smuggle a column past the guard.
func exportedFields(v any) []string {
	t := reflect.TypeOf(v)
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, exportedFields(reflect.New(f.Type).Elem().Interface())...)
			continue
		}
		out = append(out, f.Name)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
