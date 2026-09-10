package gitproj

import (
	"fmt"
	"sort"
	"strings"
)

// notImportable lists the frontmatter keys that are rendered for a human to
// read but must NEVER come back in through the import door
// (design/2026-09-09-git-projection.md §D, and DI3 of that document).
//
// These five fields say *which repository* a project projects itself into,
// *which branch*, *which subfolder inside it*, *which environment variable
// holds the push credential*, and *which one holds the secret an inbound
// webhook delivery must be signed with*. Anyone with commit access to the
// mirror can edit a file in it — that is the entire point of the inbound
// door — so if these were importable, a commit could point a project's
// projection (and its push token) at a repository the committer controls.
// The projection would then dutifully publish that project's configuration
// there. They are therefore dropped from every Change, on every kind, and
// the drop is reported in Change.DroppedFields so the importer can tell the
// operator their edit had no effect rather than leaving them to wonder.
//
// Dropping rather than erroring is deliberate: a human editing the prose of
// settings.md around these lines is doing nothing wrong, and refusing their
// whole commit would be a hostile way to enforce a rule they cannot see.
var notImportable = map[string]bool{
	"git_remote":    true,
	"git_branch":    true,
	"git_subfolder": true,
	"git_token_env": true,
	// G20: the webhook HMAC secret's variable name. Dropped for the same
	// reason as the four above and one sharper one — a commit that could
	// rewrite it would decide which secret verifies inbound deliveries.
	"git_webhook_secret_env": true,
}

// NotImportableFields returns the frontmatter keys Parse always drops, in
// sorted order, for callers that want to explain the rule (the importer's
// quarantine/notice text, docs, tests).
func NotImportableFields() []string {
	out := make([]string, 0, len(notImportable))
	for k := range notImportable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Change is one file's worth of inbound edit, typed and validated, with the
// crucial property that Fields names ONLY what actually changed.
//
// That property is the reason this type is not simply "the file, unmarshalled
// into a struct". `PUT /agent/workers/{name}` silently wipes any field the
// caller omits (DI2 of design/2026-09-08-memory-coordinated-organisation.md,
// found live: it erased an architect's briefing with a 200 and a read-back
// that echoed the wipe). An importer that builds a whole Worker out of a
// file's frontmatter and hands it to the store would strip every field the
// projection does not render — on every human commit, silently, forever. So
// the answer this type gives is "what did this file change?", which is a
// question that can only be answered against the previous version of the
// file. See ParseAgainst.
type Change struct {
	// Kind and Name identify the entity, taken from the path by ParsePath —
	// never from the file's contents. Name is empty for KindSettings.
	Kind Kind
	Name string

	// Path is the repo-relative path the change came from, carried so the
	// importer can name the file in an error or a quarantine notice without
	// reconstructing it.
	Path string

	// BodyOnly is true when the markdown body changed and no frontmatter
	// field did. It is the signal that the importer must take the dedicated
	// prompt-write path (SetWorkerPrompt / SetProjectPrompt), which is the
	// only path that records a worker_prompt_write action with a rationale.
	// A whole-object write there would misreport the change in the config
	// log.
	BodyOnly bool

	// BodyChanged is true whenever the body differs from the previous
	// version, whether or not frontmatter also changed. BodyOnly is exactly
	// BodyChanged && len(Fields) == 0; both are kept because a simultaneous
	// body-and-frontmatter edit must report both halves, and a caller
	// reading only BodyOnly would silently drop the prompt half of it.
	BodyChanged bool

	// Body is the markdown body as the file now holds it. Meaningful only
	// when BodyChanged is true.
	Body string

	// Fields holds ONLY the frontmatter fields whose value differs from the
	// previous version. A key present with a nil value means "the human
	// removed this, clear it". A field that did not change is absent — that
	// absence is the DI2 guarantee, and it is what makes it safe for the
	// importer to read the current row and overlay this map on it.
	Fields map[string]interface{}

	// DroppedFields names the not-importable keys (NotImportableFields) the
	// file carried, sorted. They are never in Fields. Reported whether or
	// not their value changed, so the importer can say "these lines are
	// informational and were ignored".
	DroppedFields []string

	// Deleted is true when the file is gone from the tree. Kind, Name and
	// Path are set; nothing else is.
	Deleted bool
}

// HasChange reports whether this Change asks the importer to do anything at
// all. A file that was reformatted — keys reordered, quoting or indentation
// changed — parses to a Change for which this is false, and the importer
// should write nothing: a no-op write would still allocate a config event
// seq, produce a commit, and so append a meaningless entry to the log a
// human reads.
func (c Change) HasChange() bool {
	return c.Deleted || c.BodyChanged || len(c.Fields) > 0
}

// Parse reads a file that has no previous version: the create case, and the
// case a bootstrap import (G15) is in for every file it sees.
//
// 🔴 Prefer ParseAgainst wherever a previous version exists. Parse cannot
// tell which fields the file changed, because there is nothing to compare
// against, so it reports every non-empty field the file carries. That is
// correct for a create — there is no stored row whose fields could be wiped
// — and it is exactly the DI2 hazard for an update.
//
// content == nil means the file is gone; see ParseDelete.
func Parse(subfolder, path string, content []byte) (Change, error) {
	return ParseAgainst(subfolder, path, nil, content)
}

// ParseDelete is the explicit form of "this file is gone", and the one the
// importer should use. ParseAgainst also treats a nil new-content as a
// deletion, but nil-versus-empty is a distinction that survives badly
// through an io.Reader or an exec'd `git show`: os.ReadFile returns a
// non-nil zero-length slice for an empty file, and an empty prompt is a
// legal (if unwise) prompt, not a deletion. Naming the intent removes the
// question.
func ParseDelete(subfolder, path string) (Change, error) {
	kind, name, err := ParsePath(subfolder, path)
	if err != nil {
		return Change{}, err
	}
	return Change{Kind: kind, Name: name, Path: path, Deleted: true}, nil
}

// ParseAgainst turns one repo-relative path plus the old and new bytes at
// that path into a typed Change carrying only the difference between them.
// It is the inbound door and the inverse of Render: pure, touching no store,
// no database, no git and no network.
//
// old is the content this package last rendered (or last imported) at that
// path, and may be nil for a create. next is the content now in the tree, and
// nil means the file was deleted.
//
// An empty subfolder means DefaultSubfolder, matching ParsePath and the
// read-time default rule of DI3 — an empty subfolder that reached path
// construction would otherwise address the repository root, next to
// `.github/`.
//
// Errors name the file. Where the failure is inside the YAML block the
// underlying error carries a line number relative to that block (its first
// line is the line after the opening `---` fence).
func ParseAgainst(subfolder, path string, old, next []byte) (Change, error) {
	kind, name, err := ParsePath(subfolder, path)
	if err != nil {
		// ParsePath's validation is a security invariant, not a
		// convenience: a path it rejects is an error here, never a guess
		// at what the committer meant.
		return Change{}, err
	}

	if next == nil {
		return Change{Kind: kind, Name: name, Path: path, Deleted: true}, nil
	}

	newValues, newBody, err := ParseFrontmatter(next)
	if err != nil {
		return Change{}, fmt.Errorf("gitproj: %s: %w", path, err)
	}
	if err := checkIdentity(kind, name, newValues); err != nil {
		return Change{}, fmt.Errorf("gitproj: %s: %w", path, err)
	}

	var oldValues map[string]interface{}
	var oldBody string
	if old != nil {
		oldValues, oldBody, err = ParseFrontmatter(old)
		if err != nil {
			// The previous version is normally our own render, so this
			// means the tree is corrupt rather than the commit is bad.
			// Either way the importer must not guess: treating an
			// unreadable previous version as "no previous version" would
			// turn an update into a create, which is the DI2 wipe.
			return Change{}, fmt.Errorf("gitproj: %s: previous version: %w", path, err)
		}
	}

	ch := Change{Kind: kind, Name: name, Path: path}

	dropped := make([]string, 0, len(notImportable))
	for k := range newValues {
		if notImportable[k] {
			dropped = append(dropped, k)
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		ch.DroppedFields = dropped
	}

	fields := map[string]interface{}{}
	for _, k := range unionKeys(oldValues, newValues) {
		if notImportable[k] {
			continue
		}
		if sameValue(oldValues[k], newValues[k]) {
			continue
		}
		// newValues[k] is nil when the key was removed from the file. That
		// is a deliberate edit — a line deleted in a diff — and means
		// "clear it", which is why Fields distinguishes a key present with
		// a nil value from a key that is absent.
		fields[k] = newValues[k]
	}
	if len(fields) > 0 {
		ch.Fields = fields
	}

	if old == nil {
		// Nothing to compare the body against. A non-empty body on a create
		// is a prompt to write; an empty one is not.
		ch.BodyChanged = strings.TrimSpace(newBody) != ""
	} else {
		ch.BodyChanged = newBody != oldBody
	}
	ch.Body = newBody
	ch.BodyOnly = ch.BodyChanged && len(ch.Fields) == 0

	return ch, nil
}

// checkIdentity refuses a file whose frontmatter names an entity other than
// the one its path names. The path is authoritative — it is what ParsePath
// validated and what every store method keys on — so a disagreement is
// either a copied file someone forgot to edit or an attempted rename, and
// both deserve an error rather than a silent write to whichever of the two
// names happened to win.
func checkIdentity(kind Kind, name string, values map[string]interface{}) error {
	if kind == KindSettings || name == "" {
		return nil
	}
	for _, key := range []string{"name", "id"} {
		v, ok := values[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if s != name {
			return fmt.Errorf("frontmatter %s %q does not match the filename %q", key, s, name)
		}
	}
	return nil
}

// unionKeys returns every key in either map, sorted, so that field
// comparison walks in a fixed order and an error mentioning "the first
// offending field" is reproducible.
func unionKeys(a, b map[string]interface{}) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]interface{}{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// sameValue reports whether two frontmatter values mean the same thing.
//
// It is deliberately more tolerant than reflect.DeepEqual, because the two
// sides are not symmetrical: one was written by Render and the other by a
// human in an editor. Quoting a value (`enabled: "true"`), spelling a
// number as a string, reordering keys inside a nested map, or blanking a
// value to nothing rather than to "" are all things a person plausibly does
// while changing nothing. Reporting those as changes would append a
// meaningless entry to the config log a human reads, and — worse — would
// make the import→render loop of §C emit a commit for a no-op.
//
// So: containers compare structurally (order-independently for maps,
// order-dependently for sequences, since a list's order is data), empty and
// absent are the same thing, and scalars compare by their textual form,
// which makes the comparison indifferent to the YAML type a value happened
// to parse as. The value carried into Change.Fields is always the parsed
// new one, so this tolerance affects only the decision "did it change",
// never what gets written.
func sameValue(a, b interface{}) bool {
	if isEmptyValue(a) && isEmptyValue(b) {
		return true
	}

	am, aIsMap := a.(map[string]interface{})
	bm, bIsMap := b.(map[string]interface{})
	if aIsMap || bIsMap {
		if !aIsMap || !bIsMap || len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, ok := bm[k]
			if !ok || !sameValue(av, bv) {
				return false
			}
		}
		return true
	}

	as, aIsSeq := asSlice(a)
	bs, bIsSeq := asSlice(b)
	if aIsSeq || bIsSeq {
		if !aIsSeq || !bIsSeq || len(as) != len(bs) {
			return false
		}
		for i := range as {
			if !sameValue(as[i], bs[i]) {
				return false
			}
		}
		return true
	}

	return fmt.Sprint(a) == fmt.Sprint(b)
}

// isEmptyValue treats nil, the empty string and empty containers as the same
// thing: "this field carries nothing". A key removed from a file arrives
// here as nil and must not read as a change from an already-empty value.
func isEmptyValue(v interface{}) bool {
	switch val := v.(type) {
	case nil:
		return true
	case string:
		return val == ""
	case map[string]interface{}:
		return len(val) == 0
	case []string:
		return len(val) == 0
	case []interface{}:
		return len(val) == 0
	default:
		return false
	}
}

// asSlice normalises the two sequence shapes ParseFrontmatter can produce —
// []string for an all-strings list, []interface{} otherwise — into one form
// so that a list does not compare unequal to itself merely because one side
// happened to contain a non-string element.
func asSlice(v interface{}) ([]interface{}, bool) {
	switch val := v.(type) {
	case []string:
		out := make([]interface{}, len(val))
		for i, s := range val {
			out[i] = s
		}
		return out, true
	case []interface{}:
		return val, true
	default:
		return nil, false
	}
}

// StorageBody is a markdown body as it should be STORED: without the trailing
// newlines a file carries. The renderer gives every non-empty body exactly one
// (normalizeBody), and editors add or drop them freely, so a trailing newline
// is never part of a prompt, a skill or a memory's content.
func StorageBody(body string) string { return strings.TrimRight(body, "\n") }

// BodyEqual reports whether a body read from a FILE and a body read from the
// DATABASE say the same thing. It is the only correct comparison across the two
// doors, and every importer same-value check must use it.
//
// A raw != is always true for a rendered file whose stored value has no
// trailing newline — which is every value the console and API write. So each of
// our own rendered files looked like a human edit whenever it fell inside an
// import's diff range, which happens when the push loop lags under load. That
// defeated the same-value suppression built for exactly that case, and stamped
// the rewrite with a stranger's commit message: the changelog recorded a person
// rewriting a worker they never touched (git-projection DI23). Parse compares
// file against file and stays raw; this is for file against store.
func BodyEqual(a, b string) bool { return StorageBody(a) == StorageBody(b) }
