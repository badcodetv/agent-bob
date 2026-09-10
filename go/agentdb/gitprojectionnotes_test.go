package agentdb

// Tests for the per-file notes of an import run (G23) and for the error KIND on
// the state row beside them. The shape of the fixture matches
// gitprojection_test.go's: sqlite + AutoMigrate for the unit tests, with a
// schema check pinning that against the migration production actually runs.

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func newGitProjectionNotesTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gitprojectionnotes_test.sqlite")
	return openGitProjectionNotesTestStore(t, dbPath), dbPath
}

func openGitProjectionNotesTestStore(t *testing.T, dbPath string) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&GitProjectionState{}, &GitProjectionNote{}, &ProjectSettings{}, &ConfigEvent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return &Store{gdb: db}
}

// TestGitProjectionQuarantineSurvivesRestart is the whole point of the table.
// The importer's failure list used to live in memory for one run; an operator
// whose push was rejected wholesale learned WHICH FILE from nowhere. It has to
// still be there after the process that recorded it is gone.
func TestGitProjectionQuarantineSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	s, dbPath := newGitProjectionNotesTestStore(t)

	err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteQuarantine, []GitProjectionNote{
		{Path: "orange/workers/copywriter.md", Reason: "frontmatter: cron is not a valid expression", NotedAt: 1000},
	})
	if err != nil {
		t.Fatalf("put quarantine: %v", err)
	}
	if err := s.NoteGitProjectionFailure(ctx, "wolf", GitProjectionErrorQuarantined, "inbound commit quarantined"); err != nil {
		t.Fatalf("note failure: %v", err)
	}

	// A new process opens the same database.
	restarted := openGitProjectionNotesTestStore(t, dbPath)
	quarantine, ignored, err := restarted.GetGitProjectionNotes(ctx, "wolf")
	if err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if len(quarantine) != 1 {
		t.Fatalf("want 1 quarantine note after a restart, got %d", len(quarantine))
	}
	if quarantine[0].Path != "orange/workers/copywriter.md" {
		t.Errorf("the note must name the FILE, got %q", quarantine[0].Path)
	}
	if !strings.Contains(quarantine[0].Reason, "cron is not a valid expression") {
		t.Errorf("the note must carry the reason, got %q", quarantine[0].Reason)
	}
	if quarantine[0].NotedAt != 1000 {
		t.Errorf("noted_at must survive, got %d", quarantine[0].NotedAt)
	}
	if len(ignored) != 0 {
		t.Errorf("nothing was ignored; got %d", len(ignored))
	}

	st, err := restarted.GetGitProjectionState(ctx, "wolf")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if st.LastErrorKind != GitProjectionErrorQuarantined {
		t.Errorf("the KIND must survive too, got %q", st.LastErrorKind)
	}
}

// TestGitProjectionIgnoredNotesSurvive: the quieter half, and the one DI10 calls
// the worst outcome on the list — a human deletes a skill file, nothing happens,
// and nothing tells them why.
func TestGitProjectionIgnoredNotesSurvive(t *testing.T) {
	ctx := context.Background()
	s, dbPath := newGitProjectionNotesTestStore(t)

	err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteIgnored, []GitProjectionNote{
		{Path: "orange/skills/research.md", Reason: "skills are append-only; deleting the file removes nothing"},
		{Path: "orange/images/base.md", Reason: "images are not importable"},
	})
	if err != nil {
		t.Fatalf("put ignored: %v", err)
	}

	quarantine, ignored, err := openGitProjectionNotesTestStore(t, dbPath).GetGitProjectionNotes(ctx, "wolf")
	if err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if len(ignored) != 2 {
		t.Fatalf("want 2 ignored notes, got %d", len(ignored))
	}
	if len(quarantine) != 0 {
		t.Fatalf("ignored notes must not appear as a quarantine")
	}
	var paths []string
	for _, n := range ignored {
		paths = append(paths, n.Path)
	}
	sort.Strings(paths)
	if paths[0] != "orange/images/base.md" || paths[1] != "orange/skills/research.md" {
		t.Errorf("both files must be named, got %v", paths)
	}
}

// TestGitProjectionNotesAreCapped: a repo pushed with a thousand bad files must
// not become a thousand rows. Newest kept, oldest dropped.
func TestGitProjectionNotesAreCapped(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionNotesTestStore(t)

	over := GitProjectionNotesCap + 10
	notes := make([]GitProjectionNote, 0, over)
	for i := 0; i < over; i++ {
		notes = append(notes, GitProjectionNote{
			Path:    "orange/workers/w" + itoaPad(i) + ".md",
			Reason:  "did not parse",
			NotedAt: int64(1000 + i), // ascending: the last entry is the newest
		})
	}
	if err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteQuarantine, notes); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, _, err := s.GetGitProjectionNotes(ctx, "wolf")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != GitProjectionNotesCap {
		t.Fatalf("want the cap (%d) rows, got %d", GitProjectionNotesCap, len(got))
	}
	// Newest first, and the oldest ten are gone.
	if got[0].NotedAt != int64(1000+over-1) {
		t.Errorf("newest must be kept and returned first, got at=%d", got[0].NotedAt)
	}
	oldestKept := int64(1000 + over - GitProjectionNotesCap)
	for _, n := range got {
		if n.NotedAt < oldestKept {
			t.Fatalf("an entry older than the cap survived: at=%d, oldest kept should be %d", n.NotedAt, oldestKept)
		}
	}
}

// TestGitProjectionNotesAreReplacedWholesale is "a clean import clears the
// previous quarantine". A stale red banner after a successful push teaches an
// operator to ignore the banner, which is the one thing it must never do.
func TestGitProjectionNotesAreReplacedWholesale(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionNotesTestStore(t)

	if err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteQuarantine, []GitProjectionNote{
		{Path: "orange/workers/copywriter.md", Reason: "did not parse"},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteIgnored, []GitProjectionNote{
		{Path: "orange/images/base.md", Reason: "images are not importable"},
	}); err != nil {
		t.Fatalf("put ignored: %v", err)
	}

	// The human fixes the file and pushes again: the next run has no failures.
	if err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteQuarantine, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}

	quarantine, ignored, err := s.GetGitProjectionNotes(ctx, "wolf")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(quarantine) != 0 {
		t.Fatalf("a clean import must clear the quarantine, got %d", len(quarantine))
	}
	if len(ignored) != 1 {
		t.Fatalf("clearing one kind must not touch the other, got %d ignored", len(ignored))
	}
}

// TestGitProjectionNotesAreProjectScoped: P5's hard namespace. The project is
// stamped by the store, never taken from the caller's struct.
func TestGitProjectionNotesAreProjectScoped(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionNotesTestStore(t)

	if err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteQuarantine, []GitProjectionNote{
		{Project: "someone-else", Path: "orange/workers/a.md", Reason: "bad"},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if q, _, _ := s.GetGitProjectionNotes(ctx, "someone-else"); len(q) != 0 {
		t.Fatal("a note must not be filable against another project")
	}
	if q, _, _ := s.GetGitProjectionNotes(ctx, "wolf"); len(q) != 1 {
		t.Fatal("the note belongs to the project it was written for")
	}

	if err := s.PutGitProjectionNotes(ctx, "", GitProjectionNoteQuarantine, nil); err == nil {
		t.Error("an empty project must be refused")
	}
	if err := s.PutGitProjectionNotes(ctx, "wolf", "something-else", nil); err == nil {
		t.Error("an unknown kind must be refused")
	}
	if _, _, err := s.GetGitProjectionNotes(ctx, ""); err == nil {
		t.Error("an empty project must be refused on read too")
	}
}

// TestGitProjectionErrorKindIsStoredAndCleared: the kind is written where the
// error's TYPE was known, and a success clears it — a row that still named a
// failure after a good render would be read as a live one.
func TestGitProjectionErrorKindIsStoredAndCleared(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionNotesTestStore(t)

	cases := []struct{ in, want string }{
		{GitProjectionErrorUnrenderable, GitProjectionErrorUnrenderable},
		{GitProjectionErrorNotFastForward, GitProjectionErrorNotFastForward},
		{GitProjectionErrorQuarantined, GitProjectionErrorQuarantined},
		{GitProjectionErrorOther, GitProjectionErrorOther},
		// Anything unrecognised — including empty — lands on "other" rather
		// than being written through for every reader to defend against.
		{"", GitProjectionErrorOther},
		{"nonsense", GitProjectionErrorOther},
	}
	for _, c := range cases {
		if err := s.NoteGitProjectionFailure(ctx, "wolf", c.in, "boom"); err != nil {
			t.Fatalf("note %q: %v", c.in, err)
		}
		st, err := s.GetGitProjectionState(ctx, "wolf")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if st.LastErrorKind != c.want {
			t.Errorf("kind %q stored as %q, want %q", c.in, st.LastErrorKind, c.want)
		}
		if st.LastError != "boom" || st.LastErrorAt == 0 {
			t.Errorf("the message and its timestamp must be stored too, got %+v", st)
		}
	}

	if err := s.MarkGitProjectionRendered(ctx, "wolf", 7, "aaa111"); err != nil {
		t.Fatalf("mark rendered: %v", err)
	}
	st, _ := s.GetGitProjectionState(ctx, "wolf")
	if st.LastErrorKind != GitProjectionErrorNone || st.LastError != "" {
		t.Errorf("a successful render clears the failure AND its kind, got %+v", st)
	}

	if err := s.NoteGitProjectionFailure(ctx, "wolf", GitProjectionErrorNotFastForward, "diverged"); err != nil {
		t.Fatalf("note: %v", err)
	}
	if err := s.MarkGitProjectionPushed(ctx, "wolf", "aaa111"); err != nil {
		t.Fatalf("mark pushed: %v", err)
	}
	st, _ = s.GetGitProjectionState(ctx, "wolf")
	if st.LastErrorKind != GitProjectionErrorNone || st.LastError != "" {
		t.Errorf("a successful push clears the failure AND its kind, got %+v", st)
	}
}

// TestGitProjectionNoteReasonIsTruncated: a git error page must not become a row
// nobody can display, exactly as the state row's last_error is bounded.
func TestGitProjectionNoteReasonIsTruncated(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionNotesTestStore(t)
	huge := strings.Repeat("x", 9000)
	if err := s.PutGitProjectionNotes(ctx, "wolf", GitProjectionNoteQuarantine, []GitProjectionNote{
		{Path: "orange/workers/a.md", Reason: huge, NotedAt: time.Now().Unix()},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	q, _, _ := s.GetGitProjectionNotes(ctx, "wolf")
	if len(q) != 1 || len(q[0].Reason) >= len(huge) {
		t.Fatalf("the reason must be truncated, got %d bytes", len(q[0].Reason))
	}
}

// TestGitProjectionNotesSchemaMatchesTheMigration — the same check the state row
// has, for the same reason: unit tests build this table with AutoMigrate and
// production builds it with migration 049, and a disagreement about a column
// name would leave every test here green while production wrote nowhere.
func TestGitProjectionNotesSchemaMatchesTheMigration(t *testing.T) {
	sch, err := schema.Parse(&GitProjectionNote{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	var want []string
	for _, f := range sch.Fields {
		if f.DBName != "" {
			want = append(want, f.DBName)
		}
	}
	sort.Strings(want)

	got := columnsOfCreateTable(t, migrationSQL(t, "049_git_projection_notes"), "git_projection_notes")
	sort.Strings(got)

	if !sameStringSlice(got, want) {
		t.Fatalf("migration 049 and agentdb.GitProjectionNote disagree about columns.\n"+
			"migration: %v\nstruct:    %v", got, want)
	}
}

func itoaPad(i int) string {
	s := ""
	for _, d := range []int{100, 10, 1} {
		s += string(rune('0' + (i/d)%10))
	}
	return s
}

// TestClearGitProjectionQuarantineIsNarrow is the fix for the stale-banner bug
// G26 found: a quarantine that outlived the push which fixed it left the console
// reading `failing`, describing a file the operator had already corrected. G23
// persisted the notes so a stale banner would not teach an operator to ignore
// banners; leaving the row's own error behind defeated exactly that.
//
// The narrowness is the point and is the half most likely to be "simplified"
// later: a clean INBOUND run must not erase an OUTBOUND failure. An import can
// succeed while the push half is still broken, and blanking the row there would
// hide a real problem behind an unrelated success.
func TestClearGitProjectionQuarantineIsNarrow(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionNotesTestStore(t)

	for _, tc := range []struct {
		name      string
		kind      string
		wantClear bool
	}{
		{"a quarantine is cleared", GitProjectionErrorQuarantined, true},
		{"a push failure is NOT cleared", GitProjectionErrorNotFastForward, false},
		{"an unrenderable field is NOT cleared", GitProjectionErrorUnrenderable, false},
		{"an unclassified failure is NOT cleared", GitProjectionErrorOther, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := "clearnarrow-" + tc.kind
			if err := s.NoteGitProjectionFailure(ctx, project, tc.kind, "the reason"); err != nil {
				t.Fatalf("note failure: %v", err)
			}
			if err := s.ClearGitProjectionQuarantine(ctx, project); err != nil {
				t.Fatalf("clear: %v", err)
			}
			got, err := s.GetGitProjectionState(ctx, project)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if tc.wantClear {
				if got.LastError != "" || got.LastErrorKind != GitProjectionErrorNone {
					t.Fatalf("quarantine survived a clean run: kind=%q error=%q", got.LastErrorKind, got.LastError)
				}
				return
			}
			if got.LastError != "the reason" || got.LastErrorKind != tc.kind {
				t.Fatalf("clearing a quarantine erased a %s failure: kind=%q error=%q",
					tc.kind, got.LastErrorKind, got.LastError)
			}
		})
	}
}
