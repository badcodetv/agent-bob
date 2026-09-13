package orgprompts

import (
	"strings"
	"testing"
)

// TestAccessorsAreNonEmpty is the floor: a //go:embed of a file that is
// present but empty compiles and returns "", so "it builds" proves nothing.
func TestAccessorsAreNonEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"Interviewer", Interviewer()},
		{"Architect", Architect()},
		{"LabelRegistry", LabelRegistry()},
	} {
		if strings.TrimSpace(tc.got) == "" {
			t.Errorf("%s() is empty", tc.name)
		}
	}
}

// TestArchitectBootstrapPrecedesTheEvidenceGate pins Decision C0: without a
// bootstrap branch ahead of the evidence gate, a freshly-onboarded project's
// architect finds no summary or lesson memories on run one, changes nothing,
// and never creates the worker whose job is to write them — so the gate can
// never open. Ordering is the whole point, hence an index comparison rather
// than two contains checks.
func TestArchitectBootstrapPrecedesTheEvidenceGate(t *testing.T) {
	p := Architect()

	bootstrap := strings.Index(p, "STEP 0 — BOOTSTRAP")
	if bootstrap < 0 {
		t.Fatal("architect.md has no STEP 0 bootstrap branch")
	}
	gate := strings.Index(p, "STEP 3 — IS THERE ANYTHING NEW?")
	if gate < 0 {
		t.Fatal("architect.md has no STEP 3 evidence gate")
	}
	if bootstrap > gate {
		t.Errorf("bootstrap branch appears at %d, after the evidence gate at %d", bootstrap, gate)
	}

	// The branch is only load-bearing if it says to skip the gate and to
	// create the memory-writing worker; a bootstrap that falls through into
	// step 3 stalls exactly as if it were absent.
	for _, want := range []string{
		"SKIP steps 1-3",
		"worker.finished",
		"worker_create",
	} {
		if !strings.Contains(p[bootstrap:gate], want) {
			t.Errorf("bootstrap branch does not mention %q", want)
		}
	}
}

// TestArchitectCarriesTheProbeRevisions pins the three changes T1's live probe
// asked for (docs/product/runs/2026-09-08-architect-probe/README.md, "Changes
// to fold into T5") plus the DI3 line. Each one is a hole a weaker model would
// fall into, and prose is easy to lose in a later edit.
func TestArchitectCarriesTheProbeRevisions(t *testing.T) {
	p := Architect()
	for name, want := range map[string]string{
		"elapsed time in the evidence gate": "no evidence about it can exist yet",
		"per-worker subscription filter":    "INCLUDING THE SUBSCRIBER'S OWN",
		"instrument check":                  "FIX THE INSTRUMENT",
		"DI3 rolling-summary label":         `"rolling-summary"`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("architect.md is missing the %s: no %q", name, want)
		}
	}
}

// TestInterviewerRefusesToDesignARoster pins Decision A1 from the interview
// side. The failure this guards is not a crash: an interviewer that proposes
// workers produces a charter a human approves, and then the architect finds a
// roster it did not design and cannot account for.
func TestInterviewerRefusesToDesignARoster(t *testing.T) {
	p := Interviewer()
	for name, want := range map[string]string{
		"the not-a-team statement": "You are NOT designing a team",
		"who does design it":       "architect decides what workers should exist",
		"the deposit label":        `"kind": "org-charter"`,
		"validate before deposit":  "charter_validate",
		"the whole-charter rule":   "ALWAYS DEPOSIT THE WHOLE CHARTER",
		"nothing is live yet":      "changes NOTHING",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("interviewer.md is missing %s: no %q", name, want)
		}
	}
}

// TestInterviewerHasExactlyOneWorkedExample pins the extraction convention
// T6's parse-and-validate assertion depends on: the worked example is the one
// fenced block tagged `charter`. If a second one is ever added, T6 would
// silently check whichever came first.
func TestInterviewerHasExactlyOneWorkedExample(t *testing.T) {
	if n := strings.Count(Interviewer(), "\n```charter\n"); n != 1 {
		t.Errorf("want exactly one ```charter fenced example in interviewer.md, got %d", n)
	}
}

// TestLabelRegistryExplainsTheThreeMechanics keeps the seed note covering the
// three label behaviours the engine actually implements — newest-wins names,
// retraction, and the one automatic briefing section — rather than only a
// vocabulary list, which is the part a project is expected to replace.
func TestLabelRegistryExplainsTheThreeMechanics(t *testing.T) {
	p := LabelRegistry()
	for name, want := range map[string]string{
		"name= newest-wins":     "the newest memory carrying that",
		"retracts=":             "retracts=<memory-id>",
		"the automatic section": "kind=rolling-summary, worker=<name>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("registry.md does not explain %s: no %q", name, want)
		}
	}
}

// TestArchitectReportsAsANotice pins the first-run fix for parked architect
// runs: STEP 5 is act-then-notify, so it must say notice — without it every
// run that changed anything parked at awaiting_human forever and sat on the
// Desk as an unanswered question. STEP 6 needs a decision, so it must not.
func TestArchitectReportsAsANotice(t *testing.T) {
	p := Architect()
	step5 := strings.Index(p, "STEP 5 — IF YOU CHANGED ANYTHING, SAY SO.")
	step6 := strings.Index(p, "STEP 6 — IF YOU ARE BLIND, SAY THAT TOO.")
	rules := strings.Index(p, "RULES THAT DO NOT BEND")
	if step5 < 0 || step6 < step5 || rules < step6 {
		t.Fatal("architect.md has lost STEP 5, STEP 6 or the rules that follow them")
	}
	if !strings.Contains(p[step5:step6], "notice set to true") {
		t.Error("STEP 5 must send its report as a notice")
	}
	if !strings.Contains(p[step6:rules], "Leave notice off") {
		t.Error("STEP 6 must ask, not notify")
	}
}

// TestInterviewerKeepsQuestionsShort pins the round-2 first-use fix: the
// labelling question used to arrive as a twenty-line paragraph. The
// interviewer proposes labels rather than asking a person to invent them.
func TestInterviewerKeepsQuestionsShort(t *testing.T) {
	p := Interviewer()
	for name, want := range map[string]string{
		"the length rule":       "at most two short sentences",
		"options when a choice": "Offer options whenever the answer can",
		"labels are proposed":   "PROPOSE them",
		"no follow-up on a yes": "do not ask a follow-up",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("interviewer.md is missing %s: no %q", name, want)
		}
	}
	if strings.Contains(p, "explain it before you ask") {
		t.Error("interviewer.md still tells the interviewer to lecture before the labelling question")
	}
}
