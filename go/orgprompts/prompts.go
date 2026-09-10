// Package orgprompts holds the three pieces of prose the onboarding-and-
// architect design ships as data: the interviewer's system prompt, the
// architect's system prompt, and the seed label registry.
//
// It imports NOTHING from this module, on purpose. go/charter must import
// go/topology (Resolve returns a *topology.Bundle), so if the interviewer's
// prompt lived in go/charter then go/topology/onboarding.go reaching for it
// would close an import cycle. A leaf package holding only embedded bytes
// can be imported from either side.
//
// The prompts are .md files rather than Go string literals so they can be
// read and reviewed as prose. Nothing renders the markdown; the extension
// only tells an editor how to syntax-highlight it.
//
// The assertions that keep these prompts honest — that every tool they name
// exists, and that every argument they name alongside a tool is in that
// tool's input schema — deliberately do NOT live here. The core tool list is
// built in package main under go/cmd/agentd, which a test in this package
// could not import without breaking the rule above. See
// go/cmd/agentd/orgprompts_test.go (ticket T6).
package orgprompts

import _ "embed"

//go:embed interviewer.md
var interviewer string

//go:embed architect.md
var architect string

//go:embed registry.md
var labelRegistry string

// Interviewer is the system prompt for the onboarding interview: it drives a
// conversation towards a charter — a goal, a measure and a first set of
// labelling rules — and deposits it as an org-charter memory. It does not
// design a roster; that is the architect's job.
func Interviewer() string { return interviewer }

// Architect is the system prompt for the architect worker: the bootstrap
// branch that builds a first roster when the project is empty, and the
// standing reconcile loop (what did I change, did it help, is there new
// evidence, reconcile, tell a human) it runs on every later wake.
func Architect() string { return architect }

// LabelRegistry is the seed content for the project's label-registry memory:
// how labels work here, the starting vocabulary, and the two labels the
// engine itself depends on. A charter's own label rules are appended to it —
// this is the frame, not the whole note.
func LabelRegistry() string { return labelRegistry }
