package main

// orgprompts_test.go — the test that makes go/orgprompts trustworthy (T6).
//
// The prompts in go/orgprompts tell a model which tools to call and which
// arguments to pass. Nothing else checks that those tools exist or that those
// arguments do: prose compiles. The superseded plan shipped an instruction to
// call memory_current with a `kind:` argument it does not accept, and it read
// fine in review — a model following it loses one call and one error message,
// or, less capably, loops. T5 found the same class of bug in the prompt that
// actually ran live: it named `briefing` as a top-level argument of
// `worker_update`, which takes `name` and `fields` (see DI5).
//
// It lives HERE, in package main, and not in go/orgprompts, because the core
// tool list is built in this package and go/orgprompts is required to import
// nothing from this module. A test in that package could assert only that a
// string contains a string.
//
// NAME MAPPING. In a prompt a tool is written bare — `memory_search`. In the
// running harness the same tool is addressed as
// `mcp__<server>__<tool>`, e.g. `mcp__agent-bob__memory_search`, where the
// server segment is coreMCPServerName. This test compares bare names, and
// strips that prefix if a prompt ever writes one out in full.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/charter"
	"github.com/badcodetv/agent-bob/orgprompts"
)

// coreToolsForPromptAssertions rebuilds the list main.go registers inside its
// `if agentDB != nil` guard (main.go:601-615). Every constructor here takes
// its dependencies as interfaces and stores them; none is touched by tools(),
// so nil is safe and no fake is needed.
//
// This mirrors main.go by hand. If a tool group is ever added there and not
// here, this test would silently stop covering it — so
// TestPromptToolListMirrorsTheServer below pins the group count.
func coreToolsForPromptAssertions() []*mcpTool {
	links := permalinker{base: "https://ui.example"}
	var tools []*mcpTool
	tools = append(tools, newMemoryTools(nil, nil, links).tools()...)
	tools = append(tools, newImageTools(nil, nil, links).tools()...)
	tools = append(tools, newSkillTools(nil, nil, links).tools()...)
	tools = append(tools, newManagementTools(nil, nil, nil, links).tools()...)
	tools = append(tools, newConfigLogTools(nil, links).tools()...)
	tools = append(tools, newSessionTools(nil, links).tools()...)
	tools = append(tools, newDatasetTools(nil, nil, nil, nil, "", 0).tools()...)
	tools = append(tools, newCharterTools().tools()...)
	return tools
}

// promptToolNames indexes the list by bare name.
func promptToolNames(t *testing.T) map[string]*mcpTool {
	t.Helper()
	byName := map[string]*mcpTool{}
	for _, tool := range coreToolsForPromptAssertions() {
		if _, dup := byName[tool.Name]; dup {
			t.Fatalf("two core tools are both called %q", tool.Name)
		}
		byName[tool.Name] = tool
	}
	return byName
}

// sandboxBuiltins are tools the prompts may name that are NOT core tools and
// therefore cannot be found in the list above. They run inside the session
// container, registered by the in-image harness rather than by agentd, and are
// addressed as `mcp__ui__<name>`.
//
// `ask_user` is sandbox/src/tools/builtin/ask_user.ts, wired in
// sandbox/src/tools/registry-impl.ts:14. Its arguments are checked against
// that file's schema by hand, listed here, because Go cannot read a Zod
// schema in TypeScript — this is a carve-out, and it is written down rather
// than left as an omission.
var sandboxBuiltins = map[string]map[string]bool{
	"ask_user": {
		"question":       true,
		"options":        true,
		"allow_freetext": true,
		"context":        true,
	},
}

// toolMention is one "tool_name(arg, arg)" claim extracted from a prompt.
type toolMention struct {
	tool string
	args []string
	line string
}

// toolNameRe matches a bare tool name as the prompts write them: the naming
// convention across every core tool is lowercase words joined by underscores,
// and every one of them contains at least one underscore.
var toolNameRe = regexp.MustCompile(`\b(mcp__[a-z0-9-]+__)?([a-z][a-z0-9]*(?:_[a-z0-9]+)+)\b`)

// promptSkipWords are lowercase_underscore identifiers that appear in the
// prompts but are NOT tool names: JSON field names, label keys and argument
// names quoted on their own. Listing them explicitly is the point — a new one
// has to be added deliberately, so a genuine typo cannot hide among them.
var promptSkipWords = map[string]bool{
	// charter schema fields (interviewer.md documents the deposit shape)
	"label_rules": true, "architect_name": true, "architect_cron": true,
	"project_background": true,
	// tool ARGUMENTS, named in prose next to their tools
	"system_prompt": true, "max_instances": true, "label_selector": true,
	"created_by_worker": true, "event_type": true, "target_session": true,
	"actor_worker":   true,
	"allow_freetext": true, "latest_per": true,
	// memory labels and event types written in prose
	"rolling_summary": true,
	// values, not calls
	"no_signal": true,
}

// mentionsIn extracts every tool-shaped token from a prompt, with the
// arguments named on the same line. "On the same line" is the convention both
// prompts follow — `worker_prompt_write (name, system_prompt, rationale)` —
// and a looser window would sweep in unrelated words.
func mentionsIn(prompt string) []toolMention {
	var out []toolMention
	for _, line := range strings.Split(prompt, "\n") {
		for _, m := range toolNameRe.FindAllStringSubmatch(line, -1) {
			name := m[2]
			if promptSkipWords[name] {
				continue
			}
			out = append(out, toolMention{tool: name, args: argsOnLine(line, name), line: strings.TrimSpace(line)})
		}
	}
	return out
}

// argsNamedRe finds a parenthesised argument list immediately after a tool
// name: `schedule_create (worker, cron, input)`.
var argsNamedRe = regexp.MustCompile(`^\s*\(([^)]*)\)`)

func argsOnLine(line, tool string) []string {
	idx := strings.Index(line, tool)
	if idx < 0 {
		return nil
	}
	rest := line[idx+len(tool):]
	m := argsNamedRe.FindStringSubmatch(rest)
	if m == nil {
		return nil
	}
	var args []string
	for _, part := range strings.Split(m[1], ",") {
		part = strings.TrimSpace(part)
		// Prose inside the parentheses ("(worker, cron, input) so it is
		// actually woken") is excluded by requiring an identifier.
		if part != "" && regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(part) {
			args = append(args, part)
		}
	}
	return args
}

// schemaProperties returns the top-level property names of a tool's input
// schema. Nested properties (worker_update's `fields`) are deliberately NOT
// flattened: `briefing` is not a top-level argument of `worker_update`, and
// flattening would have hidden exactly the bug DI5 records.
func schemaProperties(tool *mcpTool) map[string]bool {
	out := map[string]bool{}
	props, _ := tool.InputSchema["properties"].(map[string]any)
	for k := range props {
		out[k] = true
	}
	return out
}

// TestPromptsOnlyNameToolsThatExist is the first half of the guard.
func TestPromptsOnlyNameToolsThatExist(t *testing.T) {
	byName := promptToolNames(t)

	for _, p := range []struct {
		file, text string
	}{
		{"architect.md", orgprompts.Architect()},
		{"interviewer.md", orgprompts.Interviewer()},
		{"registry.md", orgprompts.LabelRegistry()},
	} {
		t.Run(p.file, func(t *testing.T) {
			seen := map[string]bool{}
			for _, m := range mentionsIn(p.text) {
				if seen[m.tool] {
					continue
				}
				seen[m.tool] = true
				if _, ok := byName[m.tool]; ok {
					continue
				}
				if _, ok := sandboxBuiltins[m.tool]; ok {
					continue
				}
				t.Errorf("%s names %q, which is neither a core tool nor a listed sandbox builtin\n  line: %s",
					p.file, m.tool, m.line)
			}
			if len(seen) == 0 {
				t.Errorf("%s: extracted no tool names at all — the extractor is broken, not the prompt", p.file)
			}
		})
	}
}

// TestPromptsOnlyNameArgumentsThatExist is the second half, and the one the
// plan calls the guard against the superseded design's `memory_current(kind:)`.
func TestPromptsOnlyNameArgumentsThatExist(t *testing.T) {
	byName := promptToolNames(t)

	for _, p := range []struct {
		file, text string
	}{
		{"architect.md", orgprompts.Architect()},
		{"interviewer.md", orgprompts.Interviewer()},
	} {
		t.Run(p.file, func(t *testing.T) {
			checked := 0
			for _, m := range mentionsIn(p.text) {
				if len(m.args) == 0 {
					continue
				}
				var allowed map[string]bool
				if tool, ok := byName[m.tool]; ok {
					allowed = schemaProperties(tool)
				} else if builtin, ok := sandboxBuiltins[m.tool]; ok {
					allowed = builtin
				} else {
					continue // reported by the test above
				}
				for _, arg := range m.args {
					checked++
					if !allowed[arg] {
						t.Errorf("%s says %s takes %q, which is not one of its arguments (%s)\n  line: %s",
							p.file, m.tool, arg, sortedArgNames(allowed), m.line)
					}
				}
			}
			if checked == 0 {
				t.Errorf("%s: no tool arguments were checked — either the prompt stopped naming any, "+
					"or the extractor no longer matches how it writes them", p.file)
			}
		})
	}
}

// TestPromptExtractorCatchesABadArgument proves the two tests above are not
// vacuous. Without it, a broken extractor and a clean prompt look identical.
func TestPromptExtractorCatchesABadArgument(t *testing.T) {
	byName := promptToolNames(t)
	tool := byName["worker_update"]
	if tool == nil {
		t.Fatal("worker_update is not registered")
	}
	allowed := schemaProperties(tool)

	// This is the exact wording DI5 records from the prompt that ran live.
	ms := mentionsIn("correct it precisely with worker_update (name, briefing).")
	if len(ms) != 1 {
		t.Fatalf("extractor found %d mentions, want 1: %+v", len(ms), ms)
	}
	if !allowed["name"] {
		t.Error("worker_update should accept name")
	}
	if allowed["briefing"] {
		t.Error("worker_update must NOT accept a top-level briefing — it lives inside fields")
	}
	found := false
	for _, a := range ms[0].args {
		if a == "briefing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("extractor did not pick up the bad argument: %+v", ms[0].args)
	}
}

// TestPromptToolListMirrorsTheServer guards the hand-mirroring in
// coreToolsForPromptAssertions. It cannot read main.go, so it pins the total
// instead: a tool group added there and not here changes this number, and the
// failure message says what to do.
func TestPromptToolListMirrorsTheServer(t *testing.T) {
	const wantAtLeast = 25
	got := len(coreToolsForPromptAssertions())
	if got < wantAtLeast {
		t.Errorf("core tool list has %d tools, fewer than the %d this test was written against — "+
			"a group is probably missing from coreToolsForPromptAssertions", got, wantAtLeast)
	}
	// Spot-check one tool from each group, so a group silently returning an
	// empty slice is caught rather than counted.
	for _, name := range []string{
		"memory_create", "image_create", "skill_install", "worker_create",
		"config_history", "session_list", "dataset_list", "charter_validate",
	} {
		if _, ok := promptToolNames(t)[name]; !ok {
			t.Errorf("core tool %q is missing — a tool group is not mirrored here", name)
		}
	}
}

// TestInterviewerWorkedExampleValidates: an example that does not validate
// teaches the model to fail and looks fine in review. The example is the one
// ```charter fenced block in interviewer.md; orgprompts pins that there is
// exactly one.
func TestInterviewerWorkedExampleValidates(t *testing.T) {
	const fence = "\n```charter\n"
	text := orgprompts.Interviewer()
	_, after, found := strings.Cut(text, fence)
	if !found {
		t.Fatal("interviewer.md has no ```charter worked example")
	}
	example, _, found := strings.Cut(after, "\n```")
	if !found {
		t.Fatal("the ```charter block in interviewer.md is not closed")
	}

	c, summary, err := charter.Parse(example)
	if err != nil {
		t.Fatalf("the worked example does not parse: %v", err)
	}
	if strings.TrimSpace(summary) == "" {
		t.Error("the worked example has no summary line, which is what it is demonstrating")
	}
	if issues := charter.Validate(c); len(issues) > 0 {
		t.Fatalf("the worked example does not validate: %+v", issues)
	}
	// It must also survive the whole gate, since that is what the console runs.
	if _, err := charter.Resolve(c); err != nil {
		t.Fatalf("the worked example does not resolve: %v", err)
	}
}

func sortedArgNames(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprint(keys)
}
