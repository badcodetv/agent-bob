package main

// The seam between the memory store and the outbound git door, for named
// documents only (design/2026-09-09-git-projection.md §E, ticket G14).
//
// gitproj.RenderTree already knows how to render ProjectState.Documents, and
// gitimport.go already knows how to turn an edited orange/memory/<name>.md back
// into a NEW memory carrying the same name= label. This file is the piece
// between them: it reads "the newest non-retracted memory per distinct name=
// label value" out of the store, in a deterministic order, ready to be assigned
// straight to ProjectState.Documents.
//
// # What renders, and what deliberately does not (§E, confirmed — do not re-open)
//
// ONLY memories carrying a `name=` label render. That label is the substrate's
// existing "current value of X" convention (docs/product/03-memory.md §7.1,
// NewestMemory, memory_current): the message board, the label registry, the
// goal — the documents a human wants to read and diff.
//
// Every other memory — summaries, lessons, verdicts, retractions,
// prompt-revisions — stays in the database. They are written once and never
// edited, so a diff of one shows nothing, and they are the high-volume path
// where git's costs land. The accepted consequence is stated in §E: a project
// bootstrapped from a folder arrives with its configuration and its named
// documents and NO memory log. The repo is a current-state export, not an
// archive; the archive stays in the database where it is ordered, stamped and
// searchable.
//
// There is deliberately no option here to render the whole log. Adding one is a
// decision for the design doc, not a flag.
//
// # Why the query is MemorySearchQuery.LatestPer and not SQL of our own
//
// LatestPer is documented as "the set-valued form of NewestMemory": it reduces
// the candidate set to the newest row per distinct value of one label key
// BEFORE ranking, and — the part that is easy to get wrong by hand — it also
// excludes rows that do not carry the key at all, because labels->>'name' is
// NULL for those and DISTINCT ON would otherwise group every keyless row
// together and let exactly one arbitrary one survive.
//
// Retraction is likewise not ours to reimplement: it is part of the same hard
// filter, applied unless IncludeRetracted is set, which this loader never sets.
// A withdrawn memory is not the current value of anything, so it must not win
// its name's slot in the repo.
//
// # Why the winners are then re-read one by one
//
// SearchMemories returns a 500-byte SNIPPET, not the content. A document
// rendered from a snippet would be a file that silently stops mid-sentence —
// and then gets read back by the importer as the document's new value. So each
// winner is fetched in full by id with GetMemory, which is the same reason
// NewestMemory exists rather than being SearchMemories with limit 1.

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

// gitDocumentPageSize is the page size used to walk the named documents.
//
// It is agentdb's own maxMemorySearchLimit, which is unexported. Keep the two in
// sync: if the store ever caps a page BELOW this number, a full page would never
// be recognised as full and a project with many named documents would silently
// render only the first page. That is why the pager below also refuses to
// return a truncated set quietly.
const gitDocumentPageSize = 100

// gitDocumentStore is the narrow slice of *agentdb.Store this loader needs. Both
// methods are ordinary read paths; nothing here writes.
type gitDocumentStore interface {
	SearchMemories(ctx context.Context, q *agentdb.MemorySearchQuery) ([]*agentdb.MemorySearchResult, error)
	GetMemory(ctx context.Context, project, id string) (*agentdb.Memory, error)
}

var _ gitDocumentStore = (*agentdb.Store)(nil)

// gitDocumentSkip is one named memory that could not become a file, with the
// reason, so the projection worker can surface it (G16) instead of it vanishing.
type gitDocumentSkip struct {
	// Name is the `name=` label value, verbatim — it may be anything a label
	// value may be, which is precisely why it might not be a path segment.
	Name string
	// ID is the memory that would have rendered.
	ID string
	// Reason says why it did not, in words an operator can act on.
	Reason string
}

// gitDocuments is the loader's answer: what renders, and what was left out.
type gitDocuments struct {
	// Documents are the newest non-retracted memory per name=, in FULL, sorted
	// by name. Assign straight to gitproj.ProjectState.Documents.
	Documents []*agentdb.Memory
	// Skipped names a memory that is a valid memory but not a valid file, in
	// the same sorted order. Never silently dropped, and never fatal.
	Skipped []gitDocumentSkip
}

// loadGitDocuments returns the newest non-retracted memory per distinct `name=`
// label value for one project.
//
// # Ordering is total, on purpose
//
// gitproj.RenderTree is a pure function whose output must be byte-identical for
// the same state — that property is what makes the import→render loop terminate
// and what lets Repo.WriteTree decide "nothing to commit". A loader that handed
// it rows in whatever order the database returned them would break that
// guarantee from OUTSIDE the pure function, where no test of RenderTree could
// see it. So the result is sorted by name, which is total because a name is a
// map key here and therefore unique.
//
// # A memory name is not necessarily a file name
//
// A `name=` label is a LABEL VALUE: the charset is [A-Za-z0-9] plus `-`, `_` and
// `.` (agentdb.ValidateLabelValue). A path segment is narrower —
// ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ (gitproj.ValidateName) — and deliberately so:
// it is a security invariant, not tidiness, because a name that escapes its
// folder could write .github/workflows/*.yml into the project's own repository
// (§D). So `Message.Board` is a perfectly legal memory and cannot be a file.
//
// DECISION: such a memory is SKIPPED and REPORTED, and the rest of the project
// renders. The two alternatives were both rejected —
//
//   - failing the render would let one badly-named memory, written by any
//     worker holding memory_create, stop a whole project from projecting; that
//     is a denial of service with no operator remedy short of a database edit;
//   - sanitising the name into something legal would publish a file whose name
//     is not the document's name, and the importer would then read it back as a
//     DIFFERENT document — an edit to `message-board.md` becoming a new memory
//     named `message-board` while `Message.Board` sat unchanged beside it.
//
// Skipping is loud (it is in the result, for the console) and reversible (write
// the document under a renderable name and it appears).
func loadGitDocuments(ctx context.Context, store gitDocumentStore, project string) (gitDocuments, error) {
	if store == nil {
		return gitDocuments{}, fmt.Errorf("gitproj documents: no store")
	}
	if project == "" {
		// The project is the hard namespace (P5). It is never inferred.
		return gitDocuments{}, fmt.Errorf("gitproj documents: project is required")
	}

	winners, err := newestPerName(ctx, store, project)
	if err != nil {
		return gitDocuments{}, err
	}

	names := make([]string, 0, len(winners))
	for name := range winners {
		names = append(names, name)
	}
	sort.Strings(names)

	out := gitDocuments{}
	for _, name := range names {
		id := winners[name]
		if err := gitproj.ValidateName(name); err != nil {
			out.Skipped = append(out.Skipped, gitDocumentSkip{
				Name: name, ID: id,
				Reason: fmt.Sprintf("the memory's name= label is not a valid file name, so it cannot be rendered: %v", err),
			})
			continue
		}
		mem, err := store.GetMemory(ctx, project, id)
		if errors.Is(err, agentdb.ErrMemoryNotFound) {
			// Memories are never deleted, so this is not an expected state; it
			// is reachable only if the row went away between the two reads. A
			// skip with a reason is the right answer either way: the next
			// render re-reads everything.
			out.Skipped = append(out.Skipped, gitDocumentSkip{
				Name: name, ID: id,
				Reason: "the memory was not found when read back in full",
			})
			continue
		}
		if err != nil {
			return gitDocuments{}, fmt.Errorf("gitproj documents: read memory %s (name=%s): %w", id, name, err)
		}
		out.Documents = append(out.Documents, mem)
	}
	return out, nil
}

// newestPerName walks the project's named documents and returns name → the id
// of the newest non-retracted memory carrying it.
//
// It pages because SearchMemories caps a page at 100 rows and has no cursor. The
// paging key is Until (an inclusive upper bound on created_at): every name that
// did NOT appear in a page has, by construction, a winner older than or equal to
// that page's oldest row, so lowering the bound to it can only reveal names not
// yet seen. A name already seen may come back with an OLDER row on a later page
// — the reduction is re-evaluated against the narrowed set — so the first
// sighting of a name is kept and later ones are ignored.
func newestPerName(ctx context.Context, store gitDocumentStore, project string) (map[string]string, error) {
	winners := map[string]string{}
	until := int64(0)
	for {
		page, err := store.SearchMemories(ctx, &agentdb.MemorySearchQuery{
			Project: project,
			// The whole query is this one line: newest row per distinct value
			// of the name label, keyless rows excluded (§E's boundary), and —
			// by leaving IncludeRetracted false — withdrawn rows excluded from
			// the reduction itself rather than filtered out afterwards, so a
			// retracted memory cannot win a name's slot.
			LatestPer: agentdb.MemoryNameLabel,
			Limit:     gitDocumentPageSize,
			Until:     until,
		})
		if err != nil {
			return nil, fmt.Errorf("gitproj documents: search: %w", err)
		}

		oldest := int64(0)
		for _, r := range page {
			if oldest == 0 || r.CreatedAt < oldest {
				oldest = r.CreatedAt
			}
			name := r.Labels[agentdb.MemoryNameLabel]
			if name == "" {
				// LatestPer already excludes keyless rows; this is the belt to
				// its braces, because rendering a nameless memory would mean
				// publishing part of the raw log (§E).
				continue
			}
			if _, seen := winners[name]; seen {
				continue
			}
			winners[name] = r.ID
		}

		if len(page) < gitDocumentPageSize {
			return winners, nil
		}
		if oldest <= 0 || (until != 0 && oldest == until) {
			// A full page whose oldest row is exactly the bound we asked for
			// means the bound cannot be lowered, and this API has no cursor —
			// so continuing would loop on the same page forever and stopping
			// would silently publish a partial project. Both are worse than
			// saying so. Reaching this needs 100 distinct named documents whose
			// newest versions share one millisecond.
			return nil, fmt.Errorf(
				"gitproj documents: cannot page past created_at=%d: %d named documents share that timestamp, which is more than one page",
				oldest, len(page))
		}
		until = oldest
	}
}
