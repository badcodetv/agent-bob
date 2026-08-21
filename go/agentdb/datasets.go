package agentdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// The dataset atom (design/2026-08-20-agent-wolf.md, "The dataset atom").
//
// A dataset is a project-scoped, named, versioned, labeled BLOB. It exists so
// that a shared numeric time series (a price history, a macro series) never
// has to cross the model's context window: bytes travel by Exec+cat on write
// and by a scoped download URL on read (see O2/O4/O6b), and only metadata —
// name, version, size, row_count, sha256 — ever appears in a tool result or a
// transcript.
//
// Every write creates a new immutable version; "current" is the highest
// version for (project, name). This is the same append-only posture as
// memories (this file's sibling, memories.go) and the §13 image catalogue,
// but versioned rather than latest-wins: a reader can ask for "current" or
// pin an exact version, and nothing already written is ever mutated or
// deleted except by the reaper (O3), which is storage GC, not curation.
//
// This file (O1) declares the schema and the type only. Store methods
// (CreateDatasetVersion, CurrentDataset, GetDatasetVersion, ListDatasets,
// ListDatasetVersions, ReapDatasetVersions, ListOrphanBlobPaths) land in O2
// and O3 — see migration 045_datasets in migrations.go for the table.
// ---------------------------------------------------------------------------

// Dataset is one immutable version of a named, project-scoped blob.
type Dataset struct {
	ID      string `json:"id" gorm:"primaryKey;type:text"`
	Project string `json:"project" gorm:"type:text;not null"`
	Name    string `json:"name" gorm:"type:text;not null"`
	Version int    `json:"version" gorm:"not null"`

	// Labels is the same LabelSet type memories use (labels.go): the shared
	// validator, selector parser and jsonb translator apply unchanged. No
	// new label code for datasets.
	Labels LabelSet `json:"labels" gorm:"type:jsonb"`

	BlobPath    string `json:"blob_path" gorm:"type:text;not null"`
	SizeBytes   int64  `json:"size_bytes" gorm:"not null"`
	RowCount    int    `json:"row_count" gorm:"not null"`
	SHA256      string `json:"sha256" gorm:"type:text;not null"`
	ContentType string `json:"content_type" gorm:"type:text;not null"`

	CreatedByWorker  string `json:"created_by_worker" gorm:"type:text;not null"`
	CreatedBySession string `json:"created_by_session" gorm:"type:text;not null"`

	// CreatedAt is unix MILLISECONDS — the same unit as Memory.CreatedAt
	// (memories.go), and deliberately NOT the unix-seconds convention the
	// agent_* tables use. The two units do not unify; encode the unit in
	// every type that carries one.
	CreatedAt int64 `json:"created_at" gorm:"not null"`
}

func (Dataset) TableName() string { return "datasets" }

// ErrDatasetNotFound is returned when a dataset name (or a specific version
// of one) does not exist in the caller's project — including when it exists
// in a different project (no existence leak across projects), matching
// ErrMemoryNotFound's posture in memories.go.
var ErrDatasetNotFound = errors.New("agentdb: dataset not found")

// ErrDatasetVersionConflict is returned by CreateDatasetVersion (O2) when the
// caller's if_version does not match the dataset's current version. It is a
// struct rather than a sentinel because the caller needs the version it
// should have passed — to re-read and retry, exactly as a model tool-calling
// dataset_put needs Current back to decide its next move.
type ErrDatasetVersionConflict struct {
	// Current is the dataset's actual current version at the time of the
	// conflict. 0 means the name does not exist yet in this project.
	Current int
}

func (e ErrDatasetVersionConflict) Error() string {
	return fmt.Sprintf("agentdb: dataset version conflict: current version is %d", e.Current)
}

// ValidateDatasetName checks a dataset name against the same label-value
// charset as memory and label values (labels.go's ValidateLabelValue) — no
// second regex. Unlike a label value, the empty string is not a legal
// dataset name.
//
// Store methods added in O2 call this before any write; O1 exposes it now so
// the validation rule lives in exactly one place from the start.
func ValidateDatasetName(name string) error {
	if name == "" {
		return fmt.Errorf("dataset name must not be empty")
	}
	if err := ValidateLabelValue(name); err != nil {
		return fmt.Errorf("invalid dataset name: %w", err)
	}
	return nil
}

// ErrDatasetRequiresPostgres is returned when the dataset store is used against
// a non-Postgres dialect. The write path needs jsonb labels and, more
// importantly, the unique index on (project, name, version) that makes
// compare-and-swap correct under concurrency — neither of which the sqlite
// fallback provides.
//
// It is a DATASET sentinel and deliberately not ErrMemoryRequiresPostgres: a
// 501 raised on a dataset route should not blame memories. O3 and O5 reuse this
// one rather than declaring a second.
var ErrDatasetRequiresPostgres = errors.New("agentdb: datasets require Postgres (jsonb labels + the unique index compare-and-swap depends on)")

// requireDatasetPostgres guards the dialect, modelled on memories.go's
// requirePostgres. Every dataset method calls it AFTER validating its
// arguments — see CreateDatasetVersion.
func (s *Store) requireDatasetPostgres() error {
	if s == nil || s.gdb == nil || s.gdb.Dialector == nil || s.gdb.Dialector.Name() != "postgres" {
		return ErrDatasetRequiresPostgres
	}
	return nil
}

// datasetColumns is the full projection, in migration order. Named once so the
// three read paths and the insert cannot drift apart.
const datasetColumns = "id, project, name, version, labels, blob_path, size_bytes, row_count, " +
	"sha256, content_type, created_by_worker, created_by_session, created_at"

// errDatasetVersionTaken is the internal signal that the unique index refused
// the insert. It never leaves this file: the caller sees an
// ErrDatasetVersionConflict carrying a freshly re-read current version.
//
// It has to be a separate value because the re-read CANNOT happen inside the
// failed transaction — Postgres aborts a transaction on a constraint violation
// and every subsequent statement in it fails with 25P02. So the transaction
// unwinds first, and the version is re-read on a fresh connection afterwards.
var errDatasetVersionTaken = errors.New("agentdb: dataset version already taken")

// CreateDatasetVersion appends a new immutable version of (project, name) under
// compare-and-swap: the write lands only if the caller's ifVersion equals the
// dataset's current version, where 0 means "no such name in this project yet".
// The row it writes carries version ifVersion+1, so versions start at 1 and
// have no gaps.
//
// A mismatch is ErrDatasetVersionConflict{Current: n} — a VALUE, because O1
// gave Error() a value receiver — carrying the version the caller should have
// passed, so a model calling dataset_put can re-read and retry rather than
// guess. ifVersion < 0 is an argument error and never a conflict: a caller that
// passed -1 has a bug, and telling it "the current version is 3" would invite
// it to retry the same nonsense.
//
// Concurrency. The high-water mark and the CAS check are read INSIDE the
// transaction that inserts, but that alone is not enough: under Read Committed
// two transactions can both read version n before either commits, and both
// compute n+1. The unique index on (project, name, version) is the backstop
// that makes exactly one of them land, and its violation is translated here
// into the same conflict any other loser would see — never a raw driver error,
// never a bare "duplicate key" string reaching a tool result.
//
// The store never parses the bytes: row_count is stored exactly as supplied
// (agentd counts it — O6a), and a negative one is rejected because the column
// is NOT NULL DEFAULT 0 and O6b's 50% shrink guard divides by it.
//
// No config event is written and none is expected: a dataset is data, not
// configuration (§15.3).
func (s *Store) CreateDatasetVersion(ctx context.Context, d *Dataset, ifVersion int) (*Dataset, error) {
	// Argument validation runs BEFORE the dialect check, unlike CreateMemory
	// (memories.go:142). "This dataset name is illegal" is a more useful answer
	// than "this deployment is not Postgres", and checking arguments first is
	// what makes every rejection below provable without a live instance.
	if d == nil {
		return nil, fmt.Errorf("agentdb: dataset is required")
	}
	if d.Project == "" {
		return nil, fmt.Errorf("agentdb: dataset project is required")
	}
	if err := ValidateDatasetName(d.Name); err != nil {
		return nil, fmt.Errorf("agentdb: %w", err)
	}
	if err := ValidateLabels(d.Labels); err != nil {
		return nil, fmt.Errorf("agentdb: dataset labels: %w", err)
	}
	if d.SHA256 == "" {
		return nil, fmt.Errorf("agentdb: dataset sha256 is required")
	}
	if d.BlobPath == "" {
		return nil, fmt.Errorf("agentdb: dataset blob path is required")
	}
	if d.SizeBytes < 0 {
		return nil, fmt.Errorf("agentdb: dataset size bytes must not be negative, got %d", d.SizeBytes)
	}
	if d.RowCount < 0 {
		return nil, fmt.Errorf("agentdb: dataset row count must not be negative, got %d", d.RowCount)
	}
	if ifVersion < 0 {
		return nil, fmt.Errorf("agentdb: dataset if_version must not be negative, got %d", ifVersion)
	}
	if err := s.requireDatasetPostgres(); err != nil {
		return nil, err
	}

	// Work on a copy: a caller whose write loses the race keeps the struct it
	// passed, unstamped, and its retry gets a fresh id rather than reusing the
	// one this attempt generated.
	row := *d
	if row.ID == "" {
		row.ID = "ds-" + uuid.New().String()
	}
	if row.CreatedAt == 0 {
		// Milliseconds, like memories (and unlike the agent_* tables' seconds).
		row.CreatedAt = time.Now().UnixMilli()
	}
	if row.ContentType == "" {
		row.ContentType = "text/csv"
	}
	labelsJSON, err := json.Marshal(nonNilLabels(row.Labels))
	if err != nil {
		return nil, fmt.Errorf("agentdb: encode dataset labels: %w", err)
	}

	var stored *Dataset
	txErr := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current int
		if err := tx.Raw(
			"SELECT COALESCE(MAX(version), 0) FROM datasets WHERE project = ? AND name = ?",
			row.Project, row.Name,
		).Scan(&current).Error; err != nil {
			return fmt.Errorf("agentdb: read dataset version high-water mark: %w", err)
		}
		if current != ifVersion {
			return ErrDatasetVersionConflict{Current: current}
		}
		row.Version = ifVersion + 1

		err := tx.Exec(
			"INSERT INTO datasets ("+datasetColumns+") VALUES (?, ?, ?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?, ?)",
			row.ID, row.Project, row.Name, row.Version, string(labelsJSON), row.BlobPath,
			row.SizeBytes, row.RowCount, row.SHA256, row.ContentType,
			row.CreatedByWorker, row.CreatedBySession, row.CreatedAt,
		).Error
		if err != nil {
			if isUniqueViolation(err) {
				// Either another writer took this version between our read and
				// our insert, or the caller supplied an id that already exists.
				// Both unwind the transaction; the conflict is built outside.
				return errDatasetVersionTaken
			}
			return fmt.Errorf("agentdb: create dataset version: %w", err)
		}

		// Return what the database holds, not the caller's struct.
		got, err := getDatasetVersionTx(tx, row.Project, row.Name, row.Version)
		if err != nil {
			return err
		}
		stored = got
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, errDatasetVersionTaken) {
			// Re-read on a fresh connection: the aborted transaction can answer
			// nothing, and the caller needs the version it should have passed.
			current, err := s.currentDatasetVersion(ctx, d.Project, d.Name)
			if err != nil {
				return nil, err
			}
			return nil, ErrDatasetVersionConflict{Current: current}
		}
		return nil, txErr
	}
	return stored, nil
}

// currentDatasetVersion reads the high-water mark for (project, name), 0 when
// the name has no row in that project.
func (s *Store) currentDatasetVersion(ctx context.Context, project, name string) (int, error) {
	var current int
	if err := s.gdb.WithContext(ctx).Raw(
		"SELECT COALESCE(MAX(version), 0) FROM datasets WHERE project = ? AND name = ?",
		project, name,
	).Scan(&current).Error; err != nil {
		return 0, fmt.Errorf("agentdb: read dataset version high-water mark: %w", err)
	}
	return current, nil
}

// getDatasetVersionTx is the shared read, on whatever handle the caller has.
// The project binds first and in code, always — never as a filter a caller may
// forget to add.
func getDatasetVersionTx(tx *gorm.DB, project, name string, version int) (*Dataset, error) {
	var ds Dataset
	err := tx.Model(&Dataset{}).
		Where("project = ? AND name = ? AND version = ?", project, name, version).
		First(&ds).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDatasetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agentdb: get dataset version: %w", err)
	}
	return &ds, nil
}

// CurrentDataset returns the highest version of (project, name).
//
// A name that exists only in another project answers ErrDatasetNotFound — the
// same answer as absent, so this is not an existence oracle across the tenancy
// boundary (the posture go/httpapi/memories.go's memoryNotFound states for
// memories).
func (s *Store) CurrentDataset(ctx context.Context, project, name string) (*Dataset, error) {
	if project == "" {
		return nil, fmt.Errorf("agentdb: dataset project is required")
	}
	if err := ValidateDatasetName(name); err != nil {
		return nil, fmt.Errorf("agentdb: %w", err)
	}
	if err := s.requireDatasetPostgres(); err != nil {
		return nil, err
	}
	var ds Dataset
	err := s.gdb.WithContext(ctx).Model(&Dataset{}).
		Where("project = ? AND name = ?", project, name).
		Order("version DESC").
		First(&ds).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDatasetNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agentdb: current dataset: %w", err)
	}
	return &ds, nil
}

// GetDatasetVersion returns one pinned version of (project, name). Versions
// start at 1, so a version below 1 is an argument error rather than a lookup
// that can never succeed. Cross-project reads are ErrDatasetNotFound, exactly
// as in CurrentDataset.
func (s *Store) GetDatasetVersion(ctx context.Context, project, name string, version int) (*Dataset, error) {
	if project == "" {
		return nil, fmt.Errorf("agentdb: dataset project is required")
	}
	if err := ValidateDatasetName(name); err != nil {
		return nil, fmt.Errorf("agentdb: %w", err)
	}
	if version < 1 {
		return nil, fmt.Errorf("agentdb: dataset version must be 1 or greater, got %d", version)
	}
	if err := s.requireDatasetPostgres(); err != nil {
		return nil, err
	}
	return getDatasetVersionTx(s.gdb.WithContext(ctx), project, name, version)
}
