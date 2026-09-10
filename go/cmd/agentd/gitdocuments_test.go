package main

// Tests for G14 — the seam between the memory store and the two git doors.
//
// Four of these exist because the code would be wrong without them, not because
// it looked untested:
//
//   - TestGitDocumentLogMemoriesDoNotRender is the §E boundary. Without it the
//     repository quietly fills with every summary, lesson, verdict and
//     prompt-revision the project has ever written — the high-volume, never-
//     edited path git is worst at — and nothing else would notice.
//   - TestGitDocumentRetractedMemoryDoesNotWinItsName is the retraction
//     predicate. A withdrawn memory that still renders is a document a human
//     reads as current after the project took it back.
//   - TestGitDocumentUnrenderableNameIsSkipped is the denial-of-service. Any
//     worker holding memory_create can write name=Message.Board; if that made
//     the project unrenderable, one memory would stop the whole projection.
//   - TestGitDocumentEndToEndEditAppendsANewMemory is immutability. An imported
//     edit must APPEND; a mutation would destroy the stamp on the old version
//     and the searchable history §E keeps in the database.
//
// Everything here runs offline against an in-memory store that reimplements the
// bits of SearchMemories this loader depends on, plus (where marked) the same
// scenarios against a real Postgres, which is the only thing that can prove the
// jsonb LatestPer reduction and the retraction NOT EXISTS actually behave that
// way. The live cases skip without AGENTKIT_TEST_POSTGRES_URL.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
	"github.com/google/uuid"
)

const gitDocProject = "wolf"

// ── the fake store ──────────────────────────────────────────────────────────

// fakeGitDocumentStore reimplements the narrow slice of SearchMemories the
// loader uses: the project filter, the retraction NOT EXISTS, the LatestPer
// reduction (including its exclusion of rows without the key), newest-first
// ordering with id as the tiebreak, the limit cap, and the 500-byte snippet.
//
// The snippet matters: a loader that rendered search results directly instead of
// re-reading each winner in full would pass every other test here and publish
// documents truncated at 500 bytes.
type fakeGitDocumentStore struct {
	memories []agentdb.Memory
	queries  []agentdb.MemorySearchQuery
	// searchLimitCap mirrors agentdb's maxMemorySearchLimit.
	searchLimitCap int
}

func newFakeGitDocumentStore(mems ...agentdb.Memory) *fakeGitDocumentStore {
	return &fakeGitDocumentStore{memories: mems, searchLimitCap: 100}
}

func (f *fakeGitDocumentStore) SearchMemories(_ context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error) {
	f.queries = append(f.queries, *q)

	retracted := map[string]bool{}
	for _, m := range f.memories {
		if id := m.Labels[agentdb.RetractionLabel]; id != "" {
			retracted[m.Project+"\x00"+id] = true
		}
	}

	var kept []agentdb.Memory
	for _, m := range f.memories {
		if m.Project != q.Project {
			continue
		}
		if !q.IncludeRetracted && retracted[m.Project+"\x00"+m.ID] {
			continue
		}
		if q.Until > 0 && m.CreatedAt > q.Until {
			continue
		}
		if q.LatestPer != "" {
			if _, ok := m.Labels[q.LatestPer]; !ok {
				continue
			}
		}
		kept = append(kept, m)
	}

	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].CreatedAt != kept[j].CreatedAt {
			return kept[i].CreatedAt > kept[j].CreatedAt
		}
		return kept[i].ID > kept[j].ID
	})

	if q.LatestPer != "" {
		seen := map[string]bool{}
		var reduced []agentdb.Memory
		for _, m := range kept {
			v := m.Labels[q.LatestPer]
			if seen[v] {
				continue
			}
			seen[v] = true
			reduced = append(reduced, m)
		}
		kept = reduced
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > f.searchLimitCap {
		limit = f.searchLimitCap
	}
	if len(kept) > limit {
		kept = kept[:limit]
	}

	out := make([]*agentdb.MemorySearchResult, 0, len(kept))
	for _, m := range kept {
		snippet := m.Content
		if len(snippet) > 500 {
			snippet = snippet[:500]
		}
		out = append(out, &agentdb.MemorySearchResult{
			ID: m.ID, Labels: m.Labels, Snippet: snippet,
			CreatedByWorker: m.CreatedByWorker, CreatedBySession: m.CreatedBySession,
			CreatedAt: m.CreatedAt,
		})
	}
	return out, nil
}

func (f *fakeGitDocumentStore) GetMemory(_ context.Context, project, id string) (*agentdb.Memory, error) {
	for _, m := range f.memories {
		if m.Project == project && m.ID == id {
			c := m
			return &c, nil
		}
	}
	return nil, agentdb.ErrMemoryNotFound
}

var _ gitDocumentStore = (*fakeGitDocumentStore)(nil)

// doc builds one named-document memory.
func doc(id, name string, createdAt int64, content string, extra ...string) agentdb.Memory {
	labels := agentdb.LabelSet{agentdb.MemoryNameLabel: name}
	for i := 0; i+1 < len(extra); i += 2 {
		labels[extra[i]] = extra[i+1]
	}
	return agentdb.Memory{ID: id, Project: gitDocProject, Labels: labels, Content: content, CreatedAt: createdAt}
}

// logMem builds a memory of the kind §E keeps OUT of the repo: no name= label.
func logMem(id string, createdAt int64, labels agentdb.LabelSet, content string) agentdb.Memory {
	return agentdb.Memory{ID: id, Project: gitDocProject, Labels: labels, Content: content, CreatedAt: createdAt}
}

// loadDocs runs the loader and fails the test on error.
func loadDocs(t *testing.T, store gitDocumentStore) gitDocuments {
	t.Helper()
	got, err := loadGitDocuments(context.Background(), store, gitDocProject)
	if err != nil {
		t.Fatalf("loadGitDocuments: %v", err)
	}
	return got
}

// renderDocs runs loader → RenderTree, the whole outbound path for documents.
func renderDocs(t *testing.T, store gitDocumentStore) (map[string][]byte, gitDocuments) {
	t.Helper()
	docs := loadDocs(t, store)
	tree, err := gitproj.RenderTree(gitproj.ProjectState{Documents: docs.Documents}, "")
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	return tree, docs
}

func treePaths(tree map[string][]byte) []string {
	var out []string
	for p := range tree {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ── the round trip ──────────────────────────────────────────────────────────

func TestGitDocumentNewestVersionRendersInFull(t *testing.T) {
	// Deliberately longer than the 500-byte search snippet: a loader that
	// rendered the search result instead of re-reading the row would publish a
	// document that stops mid-sentence, and the importer would then read that
	// truncation back as the document's new value.
	body := "the board says: " + strings.Repeat("x", 900)

	store := newFakeGitDocumentStore(
		doc("m1", "message-board", 1000, "the first version", "kind", "board"),
		doc("m2", "message-board", 2000, body, "kind", "board"),
	)

	tree, docs := renderDocs(t, store)

	if len(docs.Documents) != 1 {
		t.Fatalf("want exactly one document, got %d", len(docs.Documents))
	}
	if docs.Documents[0].ID != "m2" {
		t.Fatalf("want the newer memory m2, got %s", docs.Documents[0].ID)
	}
	if len(docs.Skipped) != 0 {
		t.Fatalf("want nothing skipped, got %+v", docs.Skipped)
	}

	const path = "bob/memory/message-board.md"
	got, ok := tree[path]
	if !ok {
		t.Fatalf("want %s in the tree, got %v", path, treePaths(tree))
	}
	want := "---\nlabels:\n  kind: board\n  name: message-board\n---\n\n" + body + "\n"
	if string(got) != want {
		t.Fatalf("rendered file:\n%q\nwant:\n%q", got, want)
	}
}

// ── the retraction predicate ────────────────────────────────────────────────

func TestGitDocumentRetractedMemoryDoesNotWinItsName(t *testing.T) {
	store := newFakeGitDocumentStore(
		doc("m1", "message-board", 1000, "version one"),
		doc("m2", "message-board", 2000, "version two, later withdrawn"),
		// A retraction is an ordinary memory carrying retracts=<id>.
		logMem("m3", 3000, agentdb.LabelSet{agentdb.RetractionLabel: "m2"}, "that was wrong"),
	)

	docs := loadDocs(t, store)
	if len(docs.Documents) != 1 || docs.Documents[0].ID != "m1" {
		t.Fatalf("a withdrawn memory won its name's slot: %+v", docs.Documents)
	}

	// The loader must never lift the retraction filter, and must ask the store
	// for the reduction rather than doing it itself — the two properties the
	// live cases below then prove hold in real SQL.
	if len(store.queries) == 0 {
		t.Fatal("the loader issued no search")
	}
	for _, q := range store.queries {
		if q.IncludeRetracted {
			t.Fatal("the loader set IncludeRetracted: a withdrawn memory would render as current")
		}
		if q.LatestPer != agentdb.MemoryNameLabel {
			t.Fatalf("want LatestPer=%q, got %q", agentdb.MemoryNameLabel, q.LatestPer)
		}
		if q.Project != gitDocProject {
			t.Fatalf("want the project bound to %q, got %q", gitDocProject, q.Project)
		}
	}
}

func TestGitDocumentFullyRetractedNameDisappears(t *testing.T) {
	store := newFakeGitDocumentStore(
		doc("m1", "goal", 1000, "the only version"),
		logMem("m2", 2000, agentdb.LabelSet{agentdb.RetractionLabel: "m1"}, "withdrawn"),
	)
	tree, docs := renderDocs(t, store)
	if len(docs.Documents) != 0 {
		t.Fatalf("want no documents, got %+v", docs.Documents)
	}
	if _, ok := tree["bob/memory/goal.md"]; ok {
		t.Fatal("a name whose every version is retracted still rendered a file")
	}
}

// ── 🔴 the §E boundary: the log does not render ─────────────────────────────

func TestGitDocumentLogMemoriesDoNotRender(t *testing.T) {
	store := newFakeGitDocumentStore(
		logMem("s1", 1000, agentdb.LabelSet{"kind": "conversation-summary", "worker": "copywriter"}, "we discussed the launch"),
		logMem("s2", 1100, agentdb.LabelSet{"kind": "conversation-summary", "worker": "copywriter"}, "and then the pricing"),
		logMem("l1", 1200, agentdb.LabelSet{"kind": "lesson"}, "shorter subject lines won"),
		logMem("v1", 1300, agentdb.LabelSet{"kind": "verdict", "job": "j-7"}, "accepted"),
		logMem("p1", 1400, agentdb.LabelSet{"kind": "prompt-revision", "worker": "copywriter"}, "rewrote the brief"),
		logMem("r1", 1500, agentdb.LabelSet{agentdb.RetractionLabel: "l1"}, "not reproducible"),
		doc("d1", "label-registry", 1600, "kind, worker, thread"),
	)

	tree, docs := renderDocs(t, store)

	if len(docs.Documents) != 1 || docs.Documents[0].ID != "d1" {
		t.Fatalf("only the named document may load, got %+v", docs.Documents)
	}
	want := []string{"bob/README.md", "bob/memory/label-registry.md"}
	if got := treePaths(tree); !reflect.DeepEqual(got, want) {
		t.Fatalf("the raw memory log leaked into the repo.\ngot:  %v\nwant: %v", got, want)
	}
	for _, id := range []string{"s1", "s2", "l1", "v1", "p1", "r1"} {
		for path, content := range tree {
			if strings.Contains(string(content), id) {
				t.Fatalf("log memory %s appears in %s", id, path)
			}
		}
	}
}

// ── an unrenderable name is skipped, not fatal ──────────────────────────────

func TestGitDocumentUnrenderableNameIsSkipped(t *testing.T) {
	// Every one of these is a legal label value (agentdb.ValidateLabelValue
	// allows `.` and `_` and uppercase) and none is a legal path segment.
	store := newFakeGitDocumentStore(
		doc("m1", "message-board", 1000, "fine"),
		doc("m2", "Message.Board", 1100, "legal memory, illegal file"),
		doc("m3", "board_two", 1200, "underscore"),
		doc("m4", strings.Repeat("a", 64), 1300, "too long for a path segment"),
		doc("m5", "label-registry", 1400, "also fine"),
	)

	tree, docs := loadAndRenderWithConfig(t, store)

	var names []string
	for _, d := range docs.Documents {
		names = append(names, d.Labels[agentdb.MemoryNameLabel])
	}
	if want := []string{"label-registry", "message-board"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("documents: got %v, want %v", names, want)
	}

	var skipped []string
	for _, s := range docs.Skipped {
		skipped = append(skipped, s.Name)
		if s.ID == "" || s.Reason == "" {
			t.Fatalf("a skip must name the memory and say why: %+v", s)
		}
		if !strings.Contains(s.Reason, "name") {
			t.Fatalf("skip reason is not actionable: %q", s.Reason)
		}
	}
	if want := []string{"Message.Board", strings.Repeat("a", 64), "board_two"}; !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped: got %v, want %v", skipped, want)
	}

	// The rest of the project still projects — that is the whole point of
	// skipping rather than refusing.
	for _, want := range []string{
		"bob/README.md",
		"bob/settings.md",
		"bob/workers/copywriter.md",
		"bob/memory/label-registry.md",
		"bob/memory/message-board.md",
	} {
		if _, ok := tree[want]; !ok {
			t.Fatalf("want %s in the tree, got %v", want, treePaths(tree))
		}
	}
}

// loadAndRenderWithConfig renders the documents alongside real configuration, so
// "the rest of the project still renders" is an assertion about a whole tree and
// not about a tree containing only memories.
func loadAndRenderWithConfig(t *testing.T, store gitDocumentStore) (map[string][]byte, gitDocuments) {
	t.Helper()
	docs := loadDocs(t, store)
	tree, err := gitproj.RenderTree(gitproj.ProjectState{
		Settings:  agentdb.DefaultProjectSettings(gitDocProject),
		Workers:   []*agentdb.Worker{{Project: gitDocProject, Name: "copywriter", SystemPrompt: "write things"}},
		Documents: docs.Documents,
	}, "")
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	return tree, docs
}

// ── determinism ─────────────────────────────────────────────────────────────

func TestGitDocumentRenderIsDeterministicAcrossRowOrders(t *testing.T) {
	rows := []agentdb.Memory{
		doc("m1", "message-board", 1000, "board v1"),
		doc("m2", "message-board", 2000, "board v2"),
		doc("m3", "label-registry", 1500, "kind, worker"),
		doc("m4", "goal", 1500, "ship the thing", "kind", "charter"),
		// Same millisecond as m4, different name: the tiebreak must not be the
		// order the caller happened to collect the rows in.
		doc("m5", "brand-voice", 1500, "dry, precise"),
		logMem("x1", 3000, agentdb.LabelSet{"kind": "lesson"}, "ignored"),
	}

	var first map[string][]byte
	orders := [][]int{
		{0, 1, 2, 3, 4, 5},
		{5, 4, 3, 2, 1, 0},
		{2, 0, 5, 3, 1, 4},
		{3, 1, 4, 0, 2, 5},
	}
	for round := 0; round < 3; round++ {
		for oi, order := range orders {
			shuffled := make([]agentdb.Memory, 0, len(rows))
			for _, i := range order {
				shuffled = append(shuffled, rows[i])
			}
			tree, _ := renderDocs(t, newFakeGitDocumentStore(shuffled...))
			if first == nil {
				first = tree
				continue
			}
			if !reflect.DeepEqual(tree, first) {
				t.Fatalf("round %d order %d produced a different tree: got %v, want %v",
					round, oi, treePaths(tree), treePaths(first))
			}
		}
	}

	// And the loader's own slice order is sorted by name, which is what makes
	// the above true from outside the pure function.
	docs := loadDocs(t, newFakeGitDocumentStore(rows...))
	var names []string
	for _, d := range docs.Documents {
		names = append(names, d.Labels[agentdb.MemoryNameLabel])
	}
	if want := []string{"brand-voice", "goal", "label-registry", "message-board"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("loader order: got %v, want %v", names, want)
	}
}

// ── paging past the search limit ────────────────────────────────────────────

func TestGitDocumentPagesPastTheSearchLimit(t *testing.T) {
	// SearchMemories caps a page at 100 rows and has no cursor. A loader that
	// asked once would publish a project missing everything past the hundredth
	// document, silently.
	var rows []agentdb.Memory
	for i := 0; i < 150; i++ {
		rows = append(rows, doc(fmt.Sprintf("m%03d", i), fmt.Sprintf("doc-%03d", i), int64(1000+i), fmt.Sprintf("body %d", i)))
	}
	store := newFakeGitDocumentStore(rows...)

	docs := loadDocs(t, store)
	if len(docs.Documents) != 150 {
		t.Fatalf("want 150 documents, got %d (paging stopped early)", len(docs.Documents))
	}
	if len(store.queries) < 2 {
		t.Fatalf("want more than one page, got %d queries", len(store.queries))
	}
	seen := map[string]bool{}
	for _, d := range docs.Documents {
		name := d.Labels[agentdb.MemoryNameLabel]
		if seen[name] {
			t.Fatalf("document %s appeared twice", name)
		}
		seen[name] = true
	}
}

func TestGitDocumentRefusesToTruncateWhenItCannotPage(t *testing.T) {
	// A full page whose oldest row is exactly the bound already asked for means
	// the bound cannot be lowered. Publishing what we have would be a silent,
	// partial project; looping would never end. It says so instead.
	var rows []agentdb.Memory
	for i := 0; i < 150; i++ {
		rows = append(rows, doc(fmt.Sprintf("m%03d", i), fmt.Sprintf("doc-%03d", i), 1000, "same millisecond"))
	}
	_, err := loadGitDocuments(context.Background(), newFakeGitDocumentStore(rows...), gitDocProject)
	if err == nil {
		t.Fatal("want an error rather than a silently truncated project")
	}
	if !strings.Contains(err.Error(), "cannot page past") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestGitDocumentRejectsMissingProject(t *testing.T) {
	if _, err := loadGitDocuments(context.Background(), newFakeGitDocumentStore(), ""); err == nil {
		t.Fatal("want an error: the project is the hard namespace and is never inferred")
	}
}

// ── 🔴 end to end: an edited file APPENDS a memory ──────────────────────────

func TestGitDocumentEndToEndEditAppendsANewMemory(t *testing.T) {
	// Render a named document, put it in the repository the way the projection
	// worker would, edit the file the way a human would, push, import — and the
	// old memory must still be there, byte for byte, with a NEW one beside it.
	original := agentdb.Memory{
		ID: "mem-1", Project: gitImportProject,
		Labels:    agentdb.LabelSet{agentdb.MemoryNameLabel: "message-board", "kind": "board"},
		Content:   "the board, as the architect left it",
		CreatedAt: 1000,
	}

	docStore := newFakeGitDocumentStore(original)
	tree, docs := renderDocsForProject(t, docStore, gitImportProject)
	if len(docs.Documents) != 1 {
		t.Fatalf("want one document to render, got %d", len(docs.Documents))
	}
	const path = "bob/memory/message-board.md"
	rendered, ok := tree[path]
	if !ok {
		t.Fatalf("want %s, got %v", path, treePaths(tree))
	}

	g := newGitImportRepo(t)
	files := map[string]*string{}
	for p, content := range tree {
		files[p] = ptr(string(content))
	}
	ours := g.ours("memory_write: message-board", "the architect updated the board", 41, files)

	// A human edits the body — and only the body.
	edited := strings.Replace(string(rendered),
		"the board, as the architect left it",
		"the board, as a human corrected it", 1)
	if edited == string(rendered) {
		t.Fatal("the test's own edit did nothing")
	}
	human := g.human("correct the board\n\nthe architect had the date wrong", map[string]*string{path: ptr(edited)})
	g.push()

	importStore := newFakeGitImportStore()
	importStore.memories = append(importStore.memories, original)

	res := runImport(t, importStore, g, ours, human)
	if res.Quarantined {
		t.Fatalf("quarantined: %+v", res.Failures)
	}
	if got := importStore.methods(); !reflect.DeepEqual(got, []string{"CreateMemory"}) {
		t.Fatalf("want exactly one CreateMemory, got %v", got)
	}

	if len(importStore.memories) != 2 {
		t.Fatalf("want the old memory plus a new one, got %d", len(importStore.memories))
	}
	if !reflect.DeepEqual(importStore.memories[0], original) {
		t.Fatalf("the old memory was modified:\ngot:  %+v\nwant: %+v", importStore.memories[0], original)
	}
	appended := importStore.memories[1]
	if appended.ID == original.ID {
		t.Fatal("the edit reused the old memory's id — that is a mutation wearing an append's clothes")
	}
	if appended.Labels[agentdb.MemoryNameLabel] != "message-board" {
		t.Fatalf("the new memory lost its name= label: %v", appended.Labels)
	}
	if appended.Labels["kind"] != "board" {
		t.Fatalf("the new memory lost a label the file did not change: %v", appended.Labels)
	}
	if !strings.Contains(appended.Content, "as a human corrected it") {
		t.Fatalf("the new memory does not carry the edit: %q", appended.Content)
	}

	// The loader now sees the human's version as current, and re-rendering it
	// produces the file that was pushed — the import→render loop terminating,
	// seen from the document path.
	// The real store stamps created_at inside CreateMemory; the import fake
	// leaves it zero, so give the appended row the timestamp it would have had.
	// Without this the "newest" here would be the OLD memory, which is an
	// artefact of the fake and not of the loader.
	if appended.CreatedAt != 0 {
		t.Fatalf("the import fake now stamps created_at (%d) — drop the line below", appended.CreatedAt)
	}
	appended.CreatedAt = original.CreatedAt + 1

	after := newFakeGitDocumentStore(original, appended)
	afterTree, _ := renderDocsForProject(t, after, gitImportProject)
	if string(afterTree[path]) != edited {
		t.Fatalf("re-render differs from the imported file:\ngot:  %q\nwant: %q", afterTree[path], edited)
	}
	if _, err := os.Stat(filepath.Join(g.work, filepath.FromSlash(path))); err != nil {
		t.Fatalf("the rendered file is not where the importer read it: %v", err)
	}
}

func renderDocsForProject(t *testing.T, store gitDocumentStore, project string) (map[string][]byte, gitDocuments) {
	t.Helper()
	docs, err := loadGitDocuments(context.Background(), store, project)
	if err != nil {
		t.Fatalf("loadGitDocuments: %v", err)
	}
	tree, err := gitproj.RenderTree(gitproj.ProjectState{Documents: docs.Documents}, "")
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	return tree, docs
}

// ── live Postgres ───────────────────────────────────────────────────────────
//
// The fake above is my reading of LatestPer and the retraction clause. These
// prove that reading against the real SQL — the jsonb DISTINCT ON, its exclusion
// of rows without the key, and the correlated NOT EXISTS. Skipped without
// AGENTKIT_TEST_POSTGRES_URL:
//
//	AGENTKIT_TEST_POSTGRES_URL=postgres://... go test ./cmd/agentd/ -run GitDocument

func TestGitDocumentLiveNewestPerNameAndRetraction(t *testing.T) {
	store := openLiveStore(t)
	ctx := context.Background()
	project := "proj-" + uuid.New().String()
	t.Cleanup(func() { store.DB().Exec("DELETE FROM memories WHERE project = ?", project) })

	write := func(labels agentdb.LabelSet, content string, at int64) *agentdb.Memory {
		t.Helper()
		m, _, err := store.CreateMemory(ctx, &agentdb.Memory{
			Project: project, Labels: labels, Content: content, CreatedAt: at,
		}, nil)
		if err != nil {
			t.Fatalf("create memory: %v", err)
		}
		return m
	}

	body := "the board: " + strings.Repeat("y", 900)
	write(agentdb.LabelSet{agentdb.MemoryNameLabel: "message-board"}, "version one", 1000)
	newest := write(agentdb.LabelSet{agentdb.MemoryNameLabel: "message-board", "kind": "board"}, body, 2000)
	write(agentdb.LabelSet{agentdb.MemoryNameLabel: "label-registry"}, "kind, worker, thread", 1500)
	// A named document whose only version is then withdrawn.
	gone := write(agentdb.LabelSet{agentdb.MemoryNameLabel: "goal"}, "the old goal", 1600)
	write(agentdb.LabelSet{agentdb.RetractionLabel: gone.ID}, "superseded", 1700)
	// The log: none of this may render.
	write(agentdb.LabelSet{"kind": "conversation-summary"}, "we talked", 1800)
	write(agentdb.LabelSet{"kind": "lesson"}, "shorter is better", 1900)
	write(agentdb.LabelSet{"kind": "prompt-revision", "worker": "copywriter"}, "rewrote the brief", 2100)

	docs, err := loadGitDocuments(ctx, store, project)
	if err != nil {
		t.Fatalf("loadGitDocuments: %v", err)
	}
	var names []string
	for _, d := range docs.Documents {
		names = append(names, d.Labels[agentdb.MemoryNameLabel])
	}
	if want := []string{"label-registry", "message-board"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("live documents: got %v, want %v (the log must not render, and a retracted name must disappear)", names, want)
	}
	for _, d := range docs.Documents {
		if d.Labels[agentdb.MemoryNameLabel] != "message-board" {
			continue
		}
		if d.ID != newest.ID {
			t.Fatalf("the older version won: got %s, want %s", d.ID, newest.ID)
		}
		if d.Content != body {
			t.Fatalf("the document was rendered from a snippet, not the full row (%d bytes)", len(d.Content))
		}
	}

	tree, err := gitproj.RenderTree(gitproj.ProjectState{Documents: docs.Documents}, "")
	if err != nil {
		t.Fatalf("RenderTree: %v", err)
	}
	want := []string{"bob/README.md", "bob/memory/label-registry.md", "bob/memory/message-board.md"}
	if got := treePaths(tree); !reflect.DeepEqual(got, want) {
		t.Fatalf("live tree: got %v, want %v", got, want)
	}
}

func TestGitDocumentLiveUnrenderableNameIsSkipped(t *testing.T) {
	store := openLiveStore(t)
	ctx := context.Background()
	project := "proj-" + uuid.New().String()
	t.Cleanup(func() { store.DB().Exec("DELETE FROM memories WHERE project = ?", project) })

	for _, name := range []string{"message-board", "Message.Board"} {
		if _, _, err := store.CreateMemory(ctx, &agentdb.Memory{
			Project: project,
			Labels:  agentdb.LabelSet{agentdb.MemoryNameLabel: name},
			Content: "content for " + name,
		}, nil); err != nil {
			t.Fatalf("create memory %q: %v", name, err)
		}
	}

	docs, err := loadGitDocuments(ctx, store, project)
	if err != nil {
		t.Fatalf("one badly-named memory made the whole project unrenderable: %v", err)
	}
	if len(docs.Documents) != 1 || docs.Documents[0].Labels[agentdb.MemoryNameLabel] != "message-board" {
		t.Fatalf("documents: %+v", docs.Documents)
	}
	if len(docs.Skipped) != 1 || docs.Skipped[0].Name != "Message.Board" {
		t.Fatalf("skipped: %+v", docs.Skipped)
	}
}
