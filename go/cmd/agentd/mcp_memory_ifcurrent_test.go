package main

import (
	"strings"
	"testing"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/extension/embedding"
)

// ---------------------------------------------------------------------------
// memory_create's if_current argument (G13).
//
// The store does the compare-and-swap; the tool's job is narrower and entirely
// about the model on the other end. Three things have to hold:
//
//  1. absent if_current is today's behaviour, byte for byte — the ordinary
//     append path, not a compare-and-swap against nothing;
//  2. present if_current reaches the store unaltered;
//  3. the loser's error is an INSTRUCTION. A model that has just lost a race is
//     one sentence away from either recovering or overwriting somebody's work,
//     and that sentence is written here.
// ---------------------------------------------------------------------------

// TestMemoryCreateWithoutIfCurrentTakesThePlainPath: the promise to every
// existing caller and every existing prompt. Saying nothing must not opt into
// anything.
func TestMemoryCreateWithoutIfCurrentTakesThePlainPath(t *testing.T) {
	store := newFakeMemoryStore()
	tools := testMemoryTools(store, embedding.NewMock())

	if _, err := callTool(t, tools, "memory_create", testCaller(), map[string]any{
		"content": "The board is quiet.",
		"labels":  map[string]string{"name": "message-board"},
	}); err != nil {
		t.Fatalf("memory_create: %v", err)
	}
	if len(store.ifCurrent) != 0 {
		t.Fatalf("the compare-and-swap path was taken with no if_current: %v", store.ifCurrent)
	}
	if len(store.created) != 1 {
		t.Fatalf("created %d memories, want 1", len(store.created))
	}
}

// TestMemoryCreateIfCurrentReachesTheStore: the id the model passed is what the
// store compares against — not trimmed away, not silently dropped.
func TestMemoryCreateIfCurrentReachesTheStore(t *testing.T) {
	store := newFakeMemoryStore()
	tools := testMemoryTools(store, embedding.NewMock())

	if _, err := callTool(t, tools, "memory_create", testCaller(), map[string]any{
		"content":    "The board now says the newsletter is in review.",
		"labels":     map[string]string{"name": "message-board"},
		"if_current": "  mem-7  ",
	}); err != nil {
		t.Fatalf("memory_create: %v", err)
	}
	if len(store.ifCurrent) != 1 || store.ifCurrent[0] != "mem-7" {
		t.Fatalf("store saw if_current = %v, want [mem-7]", store.ifCurrent)
	}
	// Provenance still comes from the caller, not the arguments — the swap path
	// must not be a second, laxer way in.
	if len(store.created) != 1 || store.created[0].CreatedByWorker != testCaller().Worker {
		t.Fatalf("provenance must come from the caller: %#v", store.created)
	}
}

// TestMemoryCreateIfCurrentNeedsANameLabel: refused here, before the embedding
// provider is called, so a doomed create costs nothing — and refused with the
// argument that fixes it, because "invalid" would leave the model to guess.
func TestMemoryCreateIfCurrentNeedsANameLabel(t *testing.T) {
	store := newFakeMemoryStore()
	embedder := &failingEmbedder{}
	tools := testMemoryTools(store, embedder)

	_, err := callTool(t, tools, "memory_create", testCaller(), map[string]any{
		"content":    "no name here",
		"labels":     map[string]string{"kind": "lesson"},
		"if_current": "mem-7",
	})
	if err == nil {
		t.Fatal("want an error: if_current without a name label must not become an append")
	}
	for _, want := range []string{"nothing was written", "name"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %q", err, want)
		}
	}
	if len(store.created) != 0 || len(store.ifCurrent) != 0 {
		t.Fatal("nothing may reach the store")
	}
	if embedder.calls != 0 {
		t.Fatalf("the embedding provider was called %d times for a create that could never succeed", embedder.calls)
	}
}

// TestMemoryCreateIfCurrentConflictIsAnInstruction is the one that matters. The
// message is read by a model deciding what to do next, so it must say the write
// did NOT happen, name the memory that won, and say how to recover. A
// diagnostic ("version conflict") leaves it to guess, and the likeliest guess is
// to write again without if_current — which is the original bug, on purpose.
func TestMemoryCreateIfCurrentConflictIsAnInstruction(t *testing.T) {
	store := newFakeMemoryStore()
	store.casConflict = &agentdb.ErrMemoryNotCurrent{
		Name: "message-board", IfCurrent: "mem-1", Current: "mem-9",
	}
	tools := testMemoryTools(store, embedding.NewMock())

	_, err := callTool(t, tools, "memory_create", testCaller(), map[string]any{
		"content":    "my rewrite",
		"labels":     map[string]string{"name": "message-board"},
		"if_current": "mem-1",
	})
	if err == nil {
		t.Fatal("a lost compare-and-swap must be an error, not a quiet success")
	}
	msg := err.Error()
	for _, want := range []string{
		"nothing was written",   // it did not happen
		"mem-9",                 // who won
		"message-board",         // what was contested
		"memory_get",            // how to re-read the winner
		"if_current: \"mem-9\"", // and what to pass on the retry
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict message %q must contain %q", msg, want)
		}
	}
}

// TestMemoryCreateIfCurrentConflictWhenNothingIsCurrent: the other conflict
// shape. Telling the model to memory_get an id that no longer holds the name
// would send it in a circle, so this branch points at memory_current instead
// and says what found:false means.
func TestMemoryCreateIfCurrentConflictWhenNothingIsCurrent(t *testing.T) {
	store := newFakeMemoryStore()
	store.casConflict = &agentdb.ErrMemoryNotCurrent{Name: "message-board", IfCurrent: "mem-1"}
	tools := testMemoryTools(store, embedding.NewMock())

	_, err := callTool(t, tools, "memory_create", testCaller(), map[string]any{
		"content":    "my rewrite",
		"labels":     map[string]string{"name": "message-board"},
		"if_current": "mem-1",
	})
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"nothing was written", "memory_current", "message-board"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q must contain %q", err, want)
		}
	}
}

// TestMemoryCreateSchemaDeclaresIfCurrent: the schema and the description are
// the only things that tell a model this exists. A store-side feature no prompt
// mentions is a feature nothing will ever use.
func TestMemoryCreateSchemaDeclaresIfCurrent(t *testing.T) {
	tools := testMemoryTools(newFakeMemoryStore(), nil)

	var create *mcpTool
	for _, tool := range tools.tools() {
		if tool.Name == "memory_create" {
			create = tool
		}
	}
	if create == nil {
		t.Fatal("memory_create is missing")
	}
	props, _ := create.InputSchema["properties"].(map[string]any)
	arg, ok := props["if_current"].(map[string]any)
	if !ok {
		t.Fatalf("memory_create has no if_current argument: %#v", props)
	}
	if arg["type"] != "string" {
		t.Fatalf("if_current type = %v, want string", arg["type"])
	}
	// Optional: an argument that became required would break every existing
	// caller and every prompt that writes an ordinary memory.
	req, _ := create.InputSchema["required"].([]string)
	for _, r := range req {
		if r == "if_current" {
			t.Fatal("if_current must stay optional — an ordinary append passes nothing")
		}
	}
	if !strings.Contains(create.Description, "if_current") {
		t.Fatal("the description must tell the model when to reach for if_current")
	}
}
