package agentdb

import (
	"errors"
	"fmt"
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
