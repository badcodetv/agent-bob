package agentdb

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// newGitProjectionTestStore is a sqlite-backed Store with the projection state
// table and the settings table. The production migrations only run on Postgres
// (see runMigrations), so unit tests build the same schema with AutoMigrate —
// which is exactly why TestGitProjectionStateSchemaMatchesTheMigration below
// exists: it is what stops the two shapes drifting apart.
func newGitProjectionTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gitprojection_test.sqlite")
	return openGitProjectionTestStore(t, dbPath), dbPath
}

func openGitProjectionTestStore(t *testing.T, dbPath string) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&GitProjectionState{}, &ProjectSettings{}, &ConfigEvent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return &Store{gdb: db}
}

// TestGitProjectionStateStartsEmpty: a project that has never rendered has no
// row, and the zero-valued row is the right answer rather than an error.
func TestGitProjectionStateStartsEmpty(t *testing.T) {
	s, _ := newGitProjectionTestStore(t)
	got, err := s.GetGitProjectionState(context.Background(), "wolf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Project != "wolf" || got.LastRenderedSeq != 0 || got.LastImportedSHA != "" || got.LeaseOwner != "" {
		t.Fatalf("an unrendered project should read as empty, got %+v", got)
	}
	if _, err := s.GetGitProjectionState(context.Background(), ""); err == nil {
		t.Fatal("an empty project must be refused")
	}
}

// TestGitProjectionWatermarkSurvivesRestart is §F's whole point: the watermark
// is DURABLE and lives in the database, not in the working tree, so a lost
// process (or a lost disk) costs a re-render and not a divergence.
func TestGitProjectionWatermarkSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	s, dbPath := newGitProjectionTestStore(t)

	if err := s.MarkGitProjectionRendered(ctx, "wolf", 42, "aaaa111"); err != nil {
		t.Fatalf("mark rendered: %v", err)
	}
	if err := s.MarkGitProjectionPushed(ctx, "wolf", "aaaa111"); err != nil {
		t.Fatalf("mark pushed: %v", err)
	}
	if err := s.MarkGitProjectionImported(ctx, "wolf", "bbbb222"); err != nil {
		t.Fatalf("mark imported: %v", err)
	}
	if _, err := s.AcquireGitProjectionLease(ctx, "wolf", "host-a/1", time.Now().Add(time.Minute).Unix()); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// A new process opens the same database.
	restarted := openGitProjectionTestStore(t, dbPath)
	got, err := restarted.GetGitProjectionState(ctx, "wolf")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.LastRenderedSeq != 42 || got.LastRenderedSHA != "aaaa111" {
		t.Errorf("render watermark did not survive: %+v", got)
	}
	if got.LastPushedSHA != "aaaa111" {
		t.Errorf("push watermark did not survive: %+v", got)
	}
	if got.LastImportedSHA != "bbbb222" {
		t.Errorf("import watermark did not survive: %+v", got)
	}
	if got.LeaseOwner != "host-a/1" {
		t.Errorf("the lease did not survive: %+v", got)
	}

	// And the restarted process can take its own lease once the old one lapses,
	// which is what stops a crash from parking a project forever.
	if _, err := restarted.AcquireGitProjectionLease(ctx, "wolf", "host-a/1", time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	ok, err := restarted.AcquireGitProjectionLease(ctx, "wolf", "host-b/9", time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatalf("acquire after expiry: %v", err)
	}
	if !ok {
		t.Fatal("an EXPIRED lease must be takeable, or a crashed process parks the project forever")
	}
}

// TestGitProjectionLeaseIsReentrantForTheSameOwner pins the one property the
// render and push loops depend on: they are two goroutines in ONE process, both
// take the lease around their work, and must not deadlock each other. A second
// process is still excluded.
func TestGitProjectionLeaseIsReentrantForTheSameOwner(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionTestStore(t)
	until := time.Now().Add(5 * time.Minute).Unix()

	for i, who := range []string{"host-a/1", "host-a/1", "host-a/1"} {
		ok, err := s.AcquireGitProjectionLease(ctx, "wolf", who, until)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		if !ok {
			t.Fatalf("take %d by the SAME owner was refused — the render and push loops would starve each other", i)
		}
	}

	ok, err := s.AcquireGitProjectionLease(ctx, "wolf", "host-b/9", until)
	if err != nil {
		t.Fatalf("acquire by another owner: %v", err)
	}
	if ok {
		t.Fatal("a live lease held by another process was stolen — §F says one writer per clone")
	}

	// Another owner's release is a no-op: it must not hand the clone over.
	if err := s.ReleaseGitProjectionLease(ctx, "wolf", "host-b/9"); err != nil {
		t.Fatalf("release by a non-holder: %v", err)
	}
	rec, err := s.GetGitProjectionState(ctx, "wolf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.LeaseOwner != "host-a/1" {
		t.Fatalf("a non-holder's release took the lease away: %+v", rec)
	}

	if err := s.ReleaseGitProjectionLease(ctx, "wolf", "host-a/1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	ok, err = s.AcquireGitProjectionLease(ctx, "wolf", "host-b/9", until)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if !ok {
		t.Fatal("a released lease must be takeable by anyone")
	}

	if _, err := s.AcquireGitProjectionLease(ctx, "wolf", "", until); err == nil {
		t.Fatal("a lease with no owner must be refused: it would be indistinguishable from 'free'")
	}
}

// TestGitProjectionImportWatermarkNeverClears: MarkGitProjectionImported is
// called with the value the IMPORTER returned, which on a quarantined push is
// the old watermark — and an empty one must never erase a real one.
func TestGitProjectionImportWatermarkNeverClears(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionTestStore(t)

	if err := s.MarkGitProjectionImported(ctx, "wolf", "cccc333"); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if err := s.MarkGitProjectionImported(ctx, "wolf", ""); err != nil {
		t.Fatalf("mark empty: %v", err)
	}
	rec, err := s.GetGitProjectionState(ctx, "wolf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.LastImportedSHA != "cccc333" {
		t.Fatalf("an empty watermark erased a real one: %+v", rec)
	}

	// A failure is recorded without disturbing the watermark, and a successful
	// render clears it again.
	if err := s.NoteGitProjectionFailure(ctx, "wolf", GitProjectionErrorOther, strings.Repeat("x", 5000)); err != nil {
		t.Fatalf("note failure: %v", err)
	}
	rec, _ = s.GetGitProjectionState(ctx, "wolf")
	if len(rec.LastError) > 2100 {
		t.Fatalf("a failure reason was stored untruncated (%d bytes)", len(rec.LastError))
	}
	if rec.LastErrorAt == 0 || rec.LastImportedSHA != "cccc333" {
		t.Fatalf("recording a failure disturbed the watermark: %+v", rec)
	}
	if err := s.MarkGitProjectionRendered(ctx, "wolf", 1, "dddd444"); err != nil {
		t.Fatalf("mark rendered: %v", err)
	}
	rec, _ = s.GetGitProjectionState(ctx, "wolf")
	if rec.LastError != "" || rec.LastErrorAt != 0 {
		t.Fatalf("a successful render did not clear the failure: %+v", rec)
	}
}

// TestGitProjectionTargets covers both listing shapes the webhook depends on:
// only projects with a remote are listed, in a deterministic order, and the
// targets carry the secret's variable NAME.
func TestGitProjectionTargets(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionTestStore(t)

	put := func(project, remote, secretEnv string) {
		t.Helper()
		if _, err := s.PutProjectSettings(ctx, &ProjectSettings{
			Project: project, GitRemote: remote, GitWebhookSecretEnv: secretEnv,
		}, ConfigWrite{}); err != nil {
			t.Fatalf("put %s: %v", project, err)
		}
	}
	put("wolf", "https://github.com/badcode/wolf", "WOLF_WEBHOOK_SECRET")
	put("acme", "https://github.com/badcode/acme.git", "ACME_WEBHOOK_SECRET")
	put("quiet", "", "")

	names, err := s.ListProjectsWithGitRemote(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if want := []string{"acme", "wolf"}; !sameStringSlice(names, want) {
		t.Fatalf("ListProjectsWithGitRemote = %v, want %v (a project with no remote does not project)", names, want)
	}

	targets, err := s.ListGitProjectionTargets(ctx)
	if err != nil {
		t.Fatalf("targets: %v", err)
	}
	if len(targets) != 2 || targets[0].Project != "acme" || targets[1].Project != "wolf" {
		t.Fatalf("targets = %+v, want acme then wolf", targets)
	}
	if targets[1].GitWebhookSecretEnv != "WOLF_WEBHOOK_SECRET" || targets[1].GitRemote != "https://github.com/badcode/wolf" {
		t.Fatalf("target did not carry the routing fields: %+v", targets[1])
	}
}

// TestGitWebhookSecretEnvIsAName is the rule the whole field rests on: it holds
// the NAME of an environment variable, never a secret. A value that is not a
// plausible name is nearly always someone pasting the secret itself in.
func TestGitWebhookSecretEnvIsAName(t *testing.T) {
	ctx := context.Background()
	s, _ := newGitProjectionTestStore(t)

	// The rule is the shell's own: what a variable may be NAMED. It catches the
	// shapes a pasted secret usually has (punctuation, spaces, a leading digit)
	// — not a secret that happens to look like an identifier, which no
	// validator could tell apart from a name.
	for _, bad := range []string{
		"WOLF WEBHOOK",        // a space
		"9WOLF",               // leading digit
		"WOLF-WEBHOOK-SECRET", // hyphens are not shell-legal
		"sha256=deadbeef",     // a pasted signature
		"ghp_abc/def",         // a pasted token with punctuation
	} {
		_, err := s.PutProjectSettings(ctx, &ProjectSettings{
			Project: "wolf", GitWebhookSecretEnv: bad,
		}, ConfigWrite{})
		if !errors.Is(err, ErrInvalidProjectSettings) {
			t.Errorf("git_webhook_secret_env %q was accepted (err=%v); it must be a variable NAME", bad, err)
		}
	}

	got, err := s.PutProjectSettings(ctx, &ProjectSettings{
		Project: "wolf", GitWebhookSecretEnv: "WOLF_WEBHOOK_SECRET",
	}, ConfigWrite{})
	if err != nil {
		t.Fatalf("a valid variable name was refused: %v", err)
	}
	if got.GitWebhookSecretEnv != "WOLF_WEBHOOK_SECRET" {
		t.Fatalf("the field did not round-trip: %+v", got)
	}
}

// TestGitProjectionStateSchemaMatchesTheMigration is the "a fresh database and
// a migrated one end up with the same schema" check, and it is why this table
// no longer appears by AutoMigrate at boot (G22).
//
// Unit tests build the table with AutoMigrate (Postgres migrations do not run
// on sqlite), production builds it with migration 048. If those two ever
// disagree about a column name, every test here would keep passing while
// production wrote to a column that does not exist. So the migration's column
// list is compared against the names GORM derives from the struct.
func TestGitProjectionStateSchemaMatchesTheMigration(t *testing.T) {
	sch, err := schema.Parse(&GitProjectionState{}, &sync.Map{}, schema.NamingStrategy{})
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

	// 048 creates the table; 049 adds last_error_kind to it (G23). A struct
	// field must be findable in the migrations a production database actually
	// ran, whichever migration put it there.
	got := columnsOfCreateTable(t, migrationSQL(t, "048_git_projection_state"), "git_projection_state")
	got = append(got, columnsAddedByAlter(t, migrationSQL(t, "049_git_projection_notes"), "git_projection_state")...)
	sort.Strings(got)

	if !sameStringSlice(got, want) {
		t.Fatalf("migrations 048+049 and agentdb.GitProjectionState disagree about columns.\n"+
			"migration: %v\nstruct:    %v\n"+
			"A fresh database (migration) and a test database (AutoMigrate) must end up with the same schema.", got, want)
	}

	if !strings.Contains(migrationSQL(t, "048_git_projection_state"), "git_webhook_secret_env") {
		t.Error("migration 048 must also add project_settings.git_webhook_secret_env (G20)")
	}
	if sch.LookUpField("Project") == nil || sch.PrioritizedPrimaryField == nil ||
		sch.PrioritizedPrimaryField.DBName != "project" {
		t.Error("the projection state is keyed by project — one row per project")
	}
}

func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	for _, m := range agentMigrations {
		if m.Name == name {
			return m.SQL
		}
	}
	t.Fatalf("migration %q not found", name)
	return ""
}

// columnsOfCreateTable pulls the column names out of a CREATE TABLE statement.
// Deliberately dumb: this is a check on a hand-written migration, so it reads
// the migration the way a human does.
// columnsAddedByAlter finds `ALTER TABLE <table> ADD COLUMN IF NOT EXISTS <name>`
// in a migration. A column added later is as real as one in the CREATE, and the
// schema check has to see both or it starts failing honest work.
func columnsAddedByAlter(t *testing.T, sql, table string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?is)ALTER TABLE ` + table + ` ADD COLUMN IF NOT EXISTS (\w+)`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(sql, -1) {
		out = append(out, m[1])
	}
	return out
}

func columnsOfCreateTable(t *testing.T, sql, table string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?is)CREATE TABLE IF NOT EXISTS ` + table + `\s*\((.*?)\n\s*\);`)
	m := re.FindStringSubmatch(sql)
	if m == nil {
		t.Fatalf("no CREATE TABLE for %s in the migration", table)
	}
	var out []string
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		out = append(out, strings.TrimSuffix(fields[0], ","))
	}
	return out
}

// sameStringSlice is a local helper (memories_live_test.go has its own
// equalStrings, and this file must not depend on a live-Postgres file).
func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
