package agentdb

import (
	"fmt"
	"sync"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store is a concrete Postgres-backed store for agent data. It manages its own
// connection pool and runs migrations on startup.
//
// Config-log invariant (§15.4): every method that mutates *project
// configuration* — workers, project settings and prompts, subscriptions,
// schedules, images, skills — writes its projection row and its `config_events`
// record in one transaction, via WithConfigEvent. See the adoption recipe at
// the top of config_events.go; TestMutationsAreLogged enforces it.
type Store struct {
	gdb *gorm.DB

	// memVecKnown/memVecOK cache whether the pgvector column on `memories`
	// exists (migration 022 adds it only where the extension is available).
	// A *failed* probe caches nothing: this was a sync.Once latched on
	// `err == nil && n > 0`, so one transient query error pinned the whole
	// process to keyword-only search and blamed an absent column (RD3).
	// "The query failed" and "the column is absent" are different answers and
	// only the second one is a deployment fact.
	memVecMu    sync.Mutex
	memVecKnown bool
	memVecOK    bool

	// configHook is J3's post-commit seam: WithConfigEvent calls it with the
	// committed record once the transaction has landed, and the host turns that
	// into the routable `config.changed` event (§15.4). Guarded because it is
	// installed at boot while other goroutines may already be mutating.
	// See SetConfigEventHook in config_events.go.
	hookMu     sync.RWMutex
	configHook ConfigEventHook
}

// Open connects to Postgres, runs migrations, and returns a ready Store.
func Open(postgresURL string) (*Store, error) {
	gdb, err := gorm.Open(postgres.Open(postgresURL), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("agentdb: connect: %w", err)
	}
	if err := runMigrations(gdb); err != nil {
		return nil, fmt.Errorf("agentdb: migrations: %w", err)
	}
	return &Store{gdb: gdb}, nil
}

// MustOpen is like Open but panics on error.
func MustOpen(postgresURL string) *Store {
	s, err := Open(postgresURL)
	if err != nil {
		panic(fmt.Sprintf("agentdb.MustOpen: %v", err))
	}
	return s
}

// NewStore wraps an already-open *gorm.DB. It runs NO migrations: the caller
// owns the schema. Open is what production uses.
//
// This exists so packages OUTSIDE agentdb can exercise store-backed code
// against a throwaway sqlite database instead of requiring a live Postgres —
// extension/dbartifacts' restart test is the first such caller, and proving
// that artifact metadata survives a restart is worth less if the proof only
// runs on the machines that have a database.
func NewStore(gdb *gorm.DB) *Store { return &Store{gdb: gdb} }

// Close releases the connection pool this Store owns.
//
// It exists because Open builds a pool and, until this method, nothing could
// ever hand it back: a process that opened several Stores held every pool for
// its own lifetime. Production opens one Store and keeps it, so that cost was
// invisible there — but a TEST BINARY opens one per test, and the agentdb
// package alone accumulated enough idle connections to exhaust Postgres's
// default max_connections of 100, failing later tests at connect time with
// "sorry, too many clients already". The failure landed on whichever test ran
// last, never on the one that leaked, which is why it read as a flake.
//
// Do NOT call this on a Store built with NewStore: there the caller owns the
// *gorm.DB and closing it out from under them is not this type's business.
func (s *Store) Close() error {
	sqlDB, err := s.gdb.DB()
	if err != nil {
		return fmt.Errorf("agentdb: close: %w", err)
	}
	return sqlDB.Close()
}

// DB returns the underlying *gorm.DB for advanced queries.
func (s *Store) DB() *gorm.DB { return s.gdb }
