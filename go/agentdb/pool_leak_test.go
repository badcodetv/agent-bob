package agentdb

// Guards against the connection-pool leak that made the suite fail at connect
// with "sorry, too many clients already" (SQLSTATE 53300).
//
// The mechanism: Open builds a pool, production opens ONE Store and keeps it,
// so nothing ever needed to hand a pool back — Store had no Close at all. A
// test binary, though, opens one Store per test. Each held ~2 idle connections
// for the life of the binary, and the agentdb package alone drifted past
// Postgres's default max_connections of 100. The failure then landed on
// whichever test happened to open next, never on one that had leaked, so it
// read as a flake and was re-run rather than fixed.
//
// Two tests below, because there are two ways to regress it: Close could stop
// working, or a new test could simply not call it.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestStoreCloseReleasesThePool proves Close actually returns connections to
// the server, rather than merely returning nil.
func TestStoreCloseReleasesThePool(t *testing.T) {
	url := os.Getenv("AGENTKIT_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("AGENTKIT_TEST_POSTGRES_URL not set — skipping live Postgres test")
	}
	probe := openLivePG(t) // its own pool, closed by the helper's cleanup

	// Pin the PROBE's own pool to exactly one connection. Without this the
	// probe grows a second connection partway through — it is being queried
	// throughout — and the counts below drift by one for a reason that has
	// nothing to do with what is under test.
	probeDB, err := probe.DB().DB()
	if err != nil {
		t.Fatalf("probe pool: %v", err)
	}
	probeDB.SetMaxOpenConns(1)
	probeDB.SetMaxIdleConns(1)

	count := func() int64 {
		var n int64
		if err := probe.DB().Raw(
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()`,
		).Scan(&n).Error; err != nil {
			t.Fatalf("count connections: %v", err)
		}
		return n
	}

	before := count()

	const opens = 8
	stores := make([]*Store, 0, opens)
	for range opens {
		s, err := Open(url)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		// Touch it, so the pool holds a real connection rather than a lazy one.
		if _, err := s.GetProjectSettings(context.Background(), "pool-leak-probe"); err != nil && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("warm the pool: %v", err)
		}
		stores = append(stores, s)
	}
	if held := count(); held <= before {
		t.Fatalf("expected %d open stores to hold connections: before=%d, held=%d", opens, before, held)
	}

	for i, s := range stores {
		if err := s.Close(); err != nil {
			t.Fatalf("close store %d: %v", i, err)
		}
	}
	// pg_stat_activity can lag a client-side disconnect by a few milliseconds,
	// so poll rather than sampling once — a sleep long enough to be reliable
	// would be longer than this whole test.
	var after int64
	deadline := time.Now().Add(5 * time.Second)
	for {
		if after = count(); after <= before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Close did not release the pool: before=%d, still open after 5s=%d", before, after)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// openQualifiedRe matches a test-side `x, err := agentdb.Open(` — the shape
// that takes ownership of a pool, seen from another package.
var openQualifiedRe = regexp.MustCompile(`\w+, err :?= agentdb\.Open\(`)

// openBareRe matches the same call UNQUALIFIED, which only means agentdb.Open
// inside package agentdb itself. It must never be applied to another package:
// `Open` is an ordinary constructor name and other packages have their own
// (gitproj.Open returns a git working clone, and holds no pool). Matching it
// everywhere reported those as leaks, which is a false positive that would
// recur for every package added to this module.
var openBareRe = regexp.MustCompile(`\w+, err :?= Open\(`)

// poolCloseExemptTestFiles are files where the Open above cannot leak, with the
// reason. Keep this list SHORT: an entry is a promise, not a silencer.
var poolCloseExemptTestFiles = map[string]string{
	// Opens a deliberately malformed DSN and asserts the error — the call
	// never returns a Store, so there is no pool to release.
	"agentdb/store_test.go": "asserts Open fails on a bad DSN; no pool is created",
}

// TestEveryTestThatOpensAStoreClosesIt walks the module's test files and fails
// on one that takes a pool without handing it back. File granularity, not
// call-site: it is coarse enough to stay readable and still catches the class.
func TestEveryTestThatOpensAStoreClosesIt(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(body)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// A bare `Open(` is only agentdb's own inside package agentdb; anywhere
		// else it belongs to that package and is none of this test's business.
		inAgentdb := filepath.Dir(rel) == "agentdb"
		if !openQualifiedRe.MatchString(src) && !(inAgentdb && openBareRe.MatchString(src)) {
			return nil
		}
		if _, exempt := poolCloseExemptTestFiles[rel]; exempt {
			return nil
		}
		if !strings.Contains(src, ".Close()") {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these test files open an agentdb Store but never close it — each leaks a "+
			"connection pool for the life of the test binary, and the suite exhausts Postgres's "+
			"connection limit some tests later, blaming an innocent test:\n  %s\n"+
			"Fix: t.Cleanup(func() { _ = store.Close() }) right after the open, so it runs last.",
			strings.Join(offenders, "\n  "))
	}
}
