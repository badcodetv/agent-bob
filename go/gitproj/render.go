package gitproj

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
)

// ─────────────────────────────────────────────────────────────────────────────
// Render — the outbound door (design/2026-09-09-git-projection.md §B).
//
// Render turns a project's configuration into the exact bytes that live in the
// git clone. It is a PURE function: no git, no filesystem, no database, no
// clock, no randomness. The same ProjectState always produces byte-identical
// output, in whatever order the caller happened to collect the rows.
//
// That is not tidiness, it is the property two other parts of the design rest
// on:
//
//   - repo.WriteTree decides whether a commit is needed by byte-comparing this
//     output against what is already in the tree. A renderer that emitted map
//     iteration order would produce a spurious commit every time anything at
//     all changed in the project, and a diff no human could read.
//   - §C's import→render loop terminates BECAUSE render is a pure function of
//     store state: the re-render after an import either equals the imported
//     tree (no commit) or differs from it by normalisation (exactly one commit,
//     after which it equals). A renderer with any nondeterminism in it can
//     oscillate forever, committing on every pass.
//
// So map iteration order is never allowed to reach the output. Frontmatter keys
// are sorted by RenderFrontmatter at every nesting level (G1, and DI2 of the
// design doc explains why that sort is ours and not the YAML library's), and
// everything this file does between the store rows and that call is either a
// sorted walk or a write into a map whose own order is irrelevant.
//
// ── What decides whether a field is published ────────────────────────────────
//
// allowlist.go, and nothing else, for all six rendered structs — ProjectSettings,
// Worker, Skill, Subscription, Schedule and CustomImage (G19 brought the last
// three under the same reflection guard that used to protect only the first
// three): this file asks RenderableFields which Go fields may be published,
// pulls exactly those off the struct by reflection, and hands them to
// CheckRenderable before writing anything. A credential-bearing field holding
// anything other than a whole-value ${VAR} reference makes the whole call fail
// with an *UnrenderableError and NO tree at all — §D is refusal, not
// redaction, and a half-written tree would publish most of a project while
// claiming to have refused.
// ─────────────────────────────────────────────────────────────────────────────

// ProjectState is everything Render publishes about one project. It is the
// caller's job (the projection worker, G8) to read it out of the store; Render
// itself never touches a store.
type ProjectState struct {
	// Settings is the project settings row. Nil means the project has never
	// had settings written, and settings.md is simply not rendered — that is a
	// real state (GetProjectSettings answers with defaults for a project that
	// has never been written), not an error.
	Settings *agentdb.ProjectSettings
	// Workers, Skills, Subscriptions, Schedules and Images are the project's
	// configuration rows, in any order.
	//
	// Skills and Images may contain several rows per name — both tables are
	// append-only with a revision/version ordinal — and only the newest per
	// name renders, since the repo holds current state and git holds the
	// history (§E's reasoning, applied to configuration).
	Workers       []*agentdb.Worker
	Skills        []*agentdb.Skill
	Subscriptions []*agentdb.Subscription
	Schedules     []*agentdb.Schedule
	Images        []*agentdb.CustomImage
	// Documents are named-document memories — the newest memory per `name=`
	// label (§E). The raw memory log does not render: it is written once and
	// never edited, so a diff shows nothing, and it is the high-volume path
	// where git's costs land. A memory here that carries no `name=` label is a
	// caller mistake and is an error, not a guess.
	Documents []*agentdb.Memory
}

// RenderTree renders a project's configuration to repo-relative path → file
// bytes, keyed exactly the way Repo.WriteTree wants them (each path inside
// subfolder, and any file under subfolder that is not in the map is deleted).
//
// 🔴 NAME. The design doc's Interfaces sketch calls this `Render`. It cannot be
// called that: allowlist.go (G2) already declares `Render` as a Decision
// constant in this same package, so the two names collide and one of them has
// to move. Renaming a shipped constant was not this ticket's to do, so the
// function is `RenderTree` — which reads better beside Repo.WriteTree anyway.
// If the const is later renamed (DecisionRender), this can go back to Render.
//
// subfolder is the single path segment Orange owns inside the repository. Empty
// falls back to the project's own ProjectSettings.GitSubfolder, and then to
// agentdb.DefaultGitSubfolder — DI3's read-time defaulting rule, applied here
// because the column is deliberately stored empty rather than defaulted on
// write. A subfolder that is still empty after all of that, or that is not a
// single safe path segment, is REFUSED: rendering at the repository root would
// write over the project's own files and sit next to `.github/`, where a
// crafted name becomes a workflow that runs with that repo's secrets. That is a
// security failure, so it errors rather than guessing.
//
// On any error the returned map is nil. Callers get all-or-nothing: a refusal
// must not be able to half-publish a project.
func RenderTree(st ProjectState, subfolder string) (map[string][]byte, error) {
	settingsSubfolder := ""
	if st.Settings != nil {
		settingsSubfolder = st.Settings.GitSubfolder
	}
	sub, err := resolveSubfolder(subfolder, settingsSubfolder, agentdb.DefaultGitSubfolder)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]byte)
	add := func(path string, content []byte) error {
		if _, dup := out[path]; dup {
			// Two entities claiming one path means one of them would be
			// invisible in the repo, and WHICH one would depend on slice
			// order. Refusing is the only answer that is not a silent,
			// order-dependent data loss.
			return fmt.Errorf("gitproj: render: two entities render to the same path %q", path)
		}
		out[path] = content
		return nil
	}

	if err := add(join(sub, "README.md"), []byte(readme())); err != nil {
		return nil, err
	}

	if st.Settings != nil {
		// A copy, so applying DI3's read-time defaults for display cannot
		// mutate the caller's row. GitSubfolder renders as the subfolder
		// actually in use, not the (possibly empty) stored one, so the file
		// says where it really is.
		s := *st.Settings
		if s.GitBranch == "" {
			s.GitBranch = agentdb.DefaultGitBranch
		}
		s.GitSubfolder = sub

		values, body, err := renderAllowlisted(StructProjectSettings, &s, "SystemPrompt")
		if err != nil {
			return nil, err
		}
		content, err := RenderFrontmatter(values, body)
		if err != nil {
			return nil, err
		}
		if err := add(SettingsPath(sub), content); err != nil {
			return nil, err
		}
	}

	for _, w := range st.Workers {
		if w == nil {
			return nil, fmt.Errorf("gitproj: render: nil worker in ProjectState.Workers")
		}
		path, err := WorkerPath(sub, w.Name)
		if err != nil {
			return nil, err
		}
		values, body, err := renderAllowlisted(StructWorker, w, "SystemPrompt")
		if err != nil {
			return nil, err
		}
		content, err := RenderFrontmatter(values, body)
		if err != nil {
			return nil, err
		}
		if err := add(path, content); err != nil {
			return nil, err
		}
	}

	skills, err := newestSkills(st.Skills)
	if err != nil {
		return nil, err
	}
	for _, sk := range skills {
		path, err := SkillPath(sub, sk.Name)
		if err != nil {
			return nil, err
		}
		values, body, err := renderAllowlisted(StructSkill, sk, "Markdown")
		if err != nil {
			return nil, err
		}
		content, err := RenderFrontmatter(values, body)
		if err != nil {
			return nil, err
		}
		if err := add(path, content); err != nil {
			return nil, err
		}
	}

	for _, sub2 := range st.Subscriptions {
		if sub2 == nil {
			return nil, fmt.Errorf("gitproj: render: nil subscription in ProjectState.Subscriptions")
		}
		path, err := SubscriptionPath(sub, sub2.ID)
		if err != nil {
			return nil, err
		}
		values, _, err := renderAllowlisted(StructSubscription, sub2, "")
		if err != nil {
			return nil, err
		}
		content, err := RenderFrontmatter(values, "")
		if err != nil {
			return nil, err
		}
		if err := add(path, content); err != nil {
			return nil, err
		}
	}

	for _, sch := range st.Schedules {
		if sch == nil {
			return nil, fmt.Errorf("gitproj: render: nil schedule in ProjectState.Schedules")
		}
		path, err := SchedulePath(sub, sch.ID)
		if err != nil {
			return nil, err
		}
		values, _, err := renderAllowlisted(StructSchedule, sch, "")
		if err != nil {
			return nil, err
		}
		content, err := RenderFrontmatter(values, "")
		if err != nil {
			return nil, err
		}
		if err := add(path, content); err != nil {
			return nil, err
		}
	}

	images, err := newestImages(st.Images)
	if err != nil {
		return nil, err
	}
	for _, img := range images {
		path, err := ImagePath(sub, img.Name)
		if err != nil {
			return nil, err
		}
		values, _, err := renderAllowlisted(StructCustomImage, img, "")
		if err != nil {
			return nil, err
		}
		content, err := RenderFrontmatter(values, "")
		if err != nil {
			return nil, err
		}
		if err := add(path, content); err != nil {
			return nil, err
		}
	}

	docs, err := newestDocuments(st.Documents)
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		name := d.Labels[agentdb.MemoryNameLabel]
		path, err := MemoryPath(sub, name)
		if err != nil {
			return nil, err
		}
		labels, err := frontmatterValue(map[string]string(d.Labels))
		if err != nil {
			return nil, fmt.Errorf("gitproj: render: memory %q labels: %w", name, err)
		}
		values := map[string]interface{}{}
		if !omitValue(labels) {
			values["labels"] = labels
		}
		content, err := RenderFrontmatter(values, normalizeBody(d.Content))
		if err != nil {
			return nil, err
		}
		if err := add(path, content); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// resolveSubfolder applies DI3's read-time defaulting and then validates the
// result as a single safe path segment.
//
// The default is a parameter rather than a constant read inside so that the
// "empty after defaulting is refused" branch is reachable from a test. It is
// not a theoretical branch: it is the one that stands between an empty column
// and a render at the repository root.
func resolveSubfolder(explicit, fromSettings, def string) (string, error) {
	sub := explicit
	if sub == "" {
		sub = fromSettings
	}
	if sub == "" {
		sub = def
	}
	if sub == "" {
		return "", fmt.Errorf("gitproj: render: git subfolder is empty after defaulting; refusing to render at the repository root, where the projection would overwrite the project's own files and sit beside .github/")
	}
	if err := ValidateName(sub); err != nil {
		return "", fmt.Errorf("gitproj: render: invalid git subfolder: %w", err)
	}
	return sub, nil
}

// readme is the generated README.md at the root of the subfolder. It is a
// function rather than a constant only so the not-importable list stays in sync
// with parse.go instead of being retyped here; the output is fixed, so it does
// not disturb determinism.
func readme() string {
	var b strings.Builder
	b.WriteString("# Agent Orange\n")
	b.WriteString("\n")
	b.WriteString("This folder is written by Agent Orange. It is the published record of one\n")
	b.WriteString("project's configuration: its prompts, workers, skills, triggers, images and\n")
	b.WriteString("named documents, as the running system holds them.\n")
	b.WriteString("\n")
	b.WriteString("**Edits to these files are applied back into the system.** Change a file and\n")
	b.WriteString("push it, and the change is read back and written through the same door the\n")
	b.WriteString("console and the API use, with your commit message as its recorded reason. The\n")
	b.WriteString("database stays the place where writes are put in order; this folder is the\n")
	b.WriteString("door out and the door in, and neither door writes anything the system did not\n")
	b.WriteString("serialise. If a file cannot be read, the whole push is rejected and nothing is\n")
	b.WriteString("applied.\n")
	b.WriteString("\n")
	b.WriteString("**Four fields are informational only.** These are ignored on import and can\n")
	b.WriteString("only be changed in the console:\n")
	b.WriteString("\n")
	for _, f := range NotImportableFields() {
		b.WriteString("- `" + f + "`\n")
	}
	b.WriteString("\n")
	b.WriteString("They say which repository, branch and folder this project publishes to, and\n")
	b.WriteString("which environment variable holds the push credential. If a commit could change\n")
	b.WriteString("them, anyone who can push here could point the projection — and its token — at\n")
	b.WriteString("a repository they control.\n")
	b.WriteString("\n")
	b.WriteString("Do not hand-write files here that Agent Orange did not write: anything outside\n")
	b.WriteString("the layout above is not read, and this file itself is regenerated.\n")
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// The two field-selection paths.
// ─────────────────────────────────────────────────────────────────────────────

// renderAllowlisted builds the frontmatter values and the markdown body for one
// row of an allowlist-covered struct.
//
// The fields are chosen by allowlist.go — RenderableFields names them, in
// sorted order — pulled off the row by reflection under those Go names, and
// checked by CheckRenderable BEFORE anything is written. bodyField names the
// one allowlisted field that renders as the markdown body instead of a
// frontmatter key (a worker's prompt, a project's prompt, a skill's document);
// it is still checked, it just does not appear above the fence.
func renderAllowlisted(structName string, row any, bodyField string) (map[string]interface{}, string, error) {
	rv, err := structValue(structName, row)
	if err != nil {
		return nil, "", err
	}

	byGoField := make(map[string]any)
	for _, field := range RenderableFields(structName) {
		fv := rv.FieldByName(field)
		if !fv.IsValid() {
			// allowlist_guard_test.go makes this unreachable in a green
			// tree; it is here because "the allowlist and the struct have
			// drifted" must never degrade into publishing something else.
			return nil, "", fmt.Errorf("gitproj: render: the allowlist names %s.%s but the struct has no such field", structName, field)
		}
		value, err := frontmatterValue(fv.Interface())
		if err != nil {
			return nil, "", fmt.Errorf("gitproj: render: %s.%s: %w", structName, field, err)
		}
		if omitValue(value) {
			continue
		}
		byGoField[field] = value
	}

	if err := CheckRenderable(structName, byGoField); err != nil {
		return nil, "", err
	}

	values := make(map[string]interface{}, len(byGoField))
	body := ""
	for field, value := range byGoField {
		if field == bodyField {
			s, ok := value.(string)
			if !ok {
				return nil, "", fmt.Errorf("gitproj: render: %s.%s renders as the body but is a %T, not a string", structName, field, value)
			}
			body = s
			continue
		}
		rule, ok := RuleFor(structName, field)
		if !ok || rule.Key == "" {
			return nil, "", fmt.Errorf("gitproj: render: the allowlist gives %s.%s no frontmatter key", structName, field)
		}
		values[rule.Key] = value
	}
	return values, normalizeBody(body), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Value conversion.
// ─────────────────────────────────────────────────────────────────────────────

func structValue(structName string, row any) (reflect.Value, error) {
	rv := reflect.ValueOf(row)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return reflect.Value{}, fmt.Errorf("gitproj: render: nil %s", structName)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("gitproj: render: %s is a %s, not a struct", structName, rv.Kind())
	}
	return rv, nil
}

// frontmatterValue converts a stored value into the narrow set of types
// RenderFrontmatter accepts (string, bool, int, []string, and nested
// map[string]interface{}).
//
// It works by reflect.Kind rather than a type switch because every interesting
// value here is a NAMED type: agentdb.JSONMap is `map[string]any`,
// agentdb.LabelSet is `map[string]string`, agentdb.SelectorList is `[]string`,
// and none of them match a `case map[string]any` / `case []string`. A missed
// case would not be a compile error — it would be a field quietly failing to
// render, which is the same class of silent gap allowlist.go's asStringMap
// comment warns about.
//
// Everything it cannot represent is an ERROR, never a best guess: a JSON null
// and a non-integer number both hit this path, and both would otherwise become
// a dropped key or a reinterpreted value in a file that is read back and
// written into the database.
func frontmatterValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, nil
	}
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return nil, nil
		}
		return frontmatterValue(rv.Elem().Interface())
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := rv.Int()
		if n > math.MaxInt32 || n < math.MinInt32 {
			// Not a representability problem on 64-bit Go, but a token budget
			// of 10^12 in a config file is a bug worth surfacing rather than
			// publishing.
			return nil, fmt.Errorf("integer %d is out of the range this projection renders", n)
		}
		return int(n), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := rv.Uint()
		if n > math.MaxInt32 {
			return nil, fmt.Errorf("integer %d is out of the range this projection renders", n)
		}
		return int(n), nil
	case reflect.Float32, reflect.Float64:
		// jsonb numbers arrive as float64. An integral one is the ordinary
		// case and renders as an integer; anything else cannot be written by
		// the frontmatter writer, and rounding it would change what a reader
		// (and the importer) sees.
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f > math.MaxInt32 || f < math.MinInt32 {
			return nil, fmt.Errorf("number %v cannot be rendered: this frontmatter format holds only integers", f)
		}
		return int(f), nil
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil, nil
		}
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return nil, fmt.Errorf("byte slices are not renderable")
		}
		out := make([]string, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			item, err := frontmatterValue(rv.Index(i).Interface())
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("item %d: this frontmatter format holds only lists of strings, got %T", i, rv.Index(i).Interface())
			}
			out = append(out, s)
		}
		return out, nil
	case reflect.Map:
		if rv.IsNil() {
			return nil, nil
		}
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("maps with %s keys are not renderable", rv.Type().Key().Kind())
		}
		out := make(map[string]interface{}, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			item, err := frontmatterValue(iter.Value().Interface())
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", key, err)
			}
			if item == nil {
				return nil, fmt.Errorf("key %q holds a null, which this frontmatter format cannot express; store the key with a value or remove it", key)
			}
			out[key] = item
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}

// omitValue reports whether a value is "not set" and its key should therefore
// be left out of the file entirely rather than written as `url: ""`.
//
// Strings and containers omit when empty. Numbers and booleans do NOT: their
// zero is a documented, meaningful value on every struct rendered here —
// `daily_tokens_soft: 0` is "no budget", `snapshot_ttl_days: 0` is "never
// reap", `max_firings_per_hour: 0` is "unlimited", `enabled: false` is switched
// off — and omitting them would publish a file that reads as "unset, take the
// default", which is a different configuration.
func omitValue(v any) bool {
	switch val := v.(type) {
	case nil:
		return true
	case string:
		return val == ""
	case []string:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	default:
		return false
	}
}

// normalizeBody gives a non-empty body exactly one trailing newline, so that
// files end the way every other text file in a repo does and a later diff is
// not "\ No newline at end of file".
//
// It is idempotent, which matters more than it looks: a human who pushes a body
// without a trailing newline causes exactly ONE normalisation commit (§C's
// "differs by normalisation, after which it equals"), never an oscillation.
func normalizeBody(body string) string {
	if body == "" {
		return ""
	}
	if strings.HasSuffix(body, "\n") {
		return body
	}
	return body + "\n"
}

// ─────────────────────────────────────────────────────────────────────────────
// Newest-per-name reduction.
//
// Skills and images are append-only with an ordinal, and memories are
// append-only full stop, but each renders to one file per NAME. So the rows are
// reduced to the newest per name before any path is built. The comparison is
// (ordinal, created_at, id) so that it is total: two rows recorded in the same
// second with the same ordinal still order deterministically, and the output
// does not depend on the order the caller collected them in.
// ─────────────────────────────────────────────────────────────────────────────

// newer reports whether a is the newer of two rows sharing a name.
func newer(ordA int, tsA int64, idA string, ordB int, tsB int64, idB string) bool {
	if ordA != ordB {
		return ordA > ordB
	}
	if tsA != tsB {
		return tsA > tsB
	}
	return idA > idB
}

func newestSkills(in []*agentdb.Skill) ([]*agentdb.Skill, error) {
	best := make(map[string]*agentdb.Skill, len(in))
	for _, s := range in {
		if s == nil {
			return nil, fmt.Errorf("gitproj: render: nil skill in ProjectState.Skills")
		}
		cur, ok := best[s.Name]
		if !ok || newer(s.Revision, s.CreatedAt, s.ID, cur.Revision, cur.CreatedAt, cur.ID) {
			best[s.Name] = s
		}
	}
	out := make([]*agentdb.Skill, 0, len(best))
	for _, name := range sortedMapKeys(best) {
		out = append(out, best[name])
	}
	return out, nil
}

func newestImages(in []*agentdb.CustomImage) ([]*agentdb.CustomImage, error) {
	best := make(map[string]*agentdb.CustomImage, len(in))
	for _, img := range in {
		if img == nil {
			return nil, fmt.Errorf("gitproj: render: nil image in ProjectState.Images")
		}
		cur, ok := best[img.Name]
		if !ok || newer(img.Version, img.CreatedAt, img.ID, cur.Version, cur.CreatedAt, cur.ID) {
			best[img.Name] = img
		}
	}
	out := make([]*agentdb.CustomImage, 0, len(best))
	for _, name := range sortedMapKeys(best) {
		out = append(out, best[name])
	}
	return out, nil
}

// newestDocuments reduces named-document memories to the newest per `name=`
// label.
//
// A memory with no `name=` label is an ERROR rather than a skip. §E renders
// named documents and nothing else, and the caller's own query
// (MemorySearchQuery.LatestPer = "name") excludes rows without the key — so a
// nameless memory arriving here means the caller handed Render the raw memory
// log, and silently rendering part of it would publish a project whose repo
// claims to be its current state while omitting rows for a reason nobody can
// see.
func newestDocuments(in []*agentdb.Memory) ([]*agentdb.Memory, error) {
	best := make(map[string]*agentdb.Memory, len(in))
	for _, m := range in {
		if m == nil {
			return nil, fmt.Errorf("gitproj: render: nil memory in ProjectState.Documents")
		}
		name := m.Labels[agentdb.MemoryNameLabel]
		if name == "" {
			return nil, fmt.Errorf("gitproj: render: memory %s carries no %s= label; only named documents render, and the caller must select them (newest per name)", m.ID, agentdb.MemoryNameLabel)
		}
		cur, ok := best[name]
		if !ok || newer(0, m.CreatedAt, m.ID, 0, cur.CreatedAt, cur.ID) {
			best[name] = m
		}
	}
	out := make([]*agentdb.Memory, 0, len(best))
	for _, name := range sortedMapKeys(best) {
		out = append(out, best[name])
	}
	return out, nil
}

// sortedMapKeys returns a map's keys in sorted order. Every iteration in this
// file that reaches the output goes through it: Go randomises map iteration,
// and a single unsorted walk would be enough to make Render nondeterministic.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
