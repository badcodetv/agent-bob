package agentdb

// gitprojectionnotes.go — what one import run had to SAY about individual files
// (design/2026-09-09-git-projection.md, ticket G23).
//
// # Why this table exists
//
// The importer already produced both of these lists — `gitImportResult.Failures`
// and `.Ignored` — and then dropped them on the floor when the run ended. The
// state row next door holds one `last_error` sentence for the whole project, so
// two things an operator badly needs were unavailable on any real deployment:
//
//   - **quarantine** — a human's push was rejected WHOLESALE because one file
//     did not parse, so nothing in it was applied. "Your push was rejected" with
//     no file and no reason is not an answer anybody can act on.
//   - **ignored** — an edit that was understood and deliberately not applied: an
//     image file (nothing in frontmatter could reconstruct a blob), a deleted
//     skill or memory file (both are append-only, §E). This one is worse than
//     the quarantine, because NOTHING at all is reported: the human edits a
//     file, pushes, watches nothing happen, and reasonably concludes the whole
//     projection is broken.
//
// # Runtime state, not configuration
//
// Same class as the watermarks and the lease in gitprojection.go: §15.3 rule 3.
// A note is an observation a background loop wrote about its own last pass. It
// writes NO config event — see the reason recorded against
// PutGitProjectionNotes in config_events.go.
//
// # Bounded on purpose
//
// This is an OPERATOR'S VIEW of the most recent run, not an audit log. A repo
// pushed with a thousand malformed files must not become a thousand rows nobody
// reads, so a kind is capped at GitProjectionNotesCap entries, newest kept.
// The audit record of what actually changed is elsewhere and is complete: every
// write an import performs is an ordinary config event.
//
// # Replacement, not accumulation
//
// PutGitProjectionNotes REPLACES one kind's notes wholesale, which is what makes
// "a clean import clears the previous quarantine" true. A stale red banner after
// a push that succeeded is its own bug — it teaches an operator to ignore the
// banner, which is the one thing it must never do.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// The two kinds of note. Closed set; a third would need a console that knows
// what to do with it.
const (
	// GitProjectionNoteQuarantine — a file that failed to parse or validate,
	// and therefore stopped the whole push from being applied.
	GitProjectionNoteQuarantine = "quarantine"
	// GitProjectionNoteIgnored — a change that was read, understood and
	// deliberately not applied.
	GitProjectionNoteIgnored = "ignored"
)

// GitProjectionNotesCap is how many notes of one kind one project keeps.
//
// 50 is chosen to be larger than any push a human writes by hand and far
// smaller than a machine-generated disaster. A run that produces more is telling
// the operator one thing ("this push is wholesale wrong"), and the fifty-first
// line does not add to it.
const GitProjectionNotesCap = 50

// GitProjectionNote is one file a run had something to say about.
//
// The struct is the source of truth for the COLUMN NAMES too, exactly as
// GitProjectionState is: migration 049 spells them by hand and
// TestGitProjectionNotesSchemaMatchesTheMigration fails if the two drift.
type GitProjectionNote struct {
	ID      int64  `json:"-" gorm:"primaryKey;autoIncrement"`
	Project string `json:"-" gorm:"type:varchar(255);not null;index:idx_git_projection_notes_lookup,priority:1"`
	// Kind is GitProjectionNoteQuarantine or GitProjectionNoteIgnored.
	Kind string `json:"-" gorm:"type:varchar(32);not null;index:idx_git_projection_notes_lookup,priority:2"`
	// Path is repo-relative, as the human sees it in their editor.
	Path string `json:"path" gorm:"type:text;not null"`
	// Reason is the sentence to show them. Truncated on write.
	Reason string `json:"reason" gorm:"type:text;not null"`
	// NotedAt is when the run that produced it happened (unix seconds).
	NotedAt int64 `json:"at" gorm:"not null;default:0"`
}

func (GitProjectionNote) TableName() string { return "git_projection_notes" }

// PutGitProjectionNotes replaces one project's notes of one kind with `notes`.
//
// Passing an empty slice is meaningful and is the clearing operation: a clean
// import calls this with no quarantine entries, and the previous quarantine
// stops being reported. Over the cap, the NEWEST entries are kept.
//
// Delete and insert happen in one transaction, so a reader never sees a project
// with its old notes deleted and its new ones not yet written.
func (s *Store) PutGitProjectionNotes(ctx context.Context, project, kind string, notes []GitProjectionNote) error {
	if strings.TrimSpace(project) == "" {
		return errors.New("agentdb: git projection notes: project is required")
	}
	kind = strings.TrimSpace(kind)
	if kind != GitProjectionNoteQuarantine && kind != GitProjectionNoteIgnored {
		return fmt.Errorf("agentdb: git projection notes: unknown kind %q", kind)
	}

	keep := capGitProjectionNotes(project, kind, notes)

	err := s.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project = ? AND kind = ?", project, kind).
			Delete(&GitProjectionNote{}).Error; err != nil {
			return err
		}
		if len(keep) == 0 {
			return nil
		}
		return tx.Create(&keep).Error
	})
	if err != nil {
		return fmt.Errorf("agentdb: write git projection %s notes for %s: %w", kind, project, err)
	}
	return nil
}

// capGitProjectionNotes normalises a run's notes into the rows to store: the
// project and kind are stamped here (never taken from the caller's struct, so a
// note cannot be filed against another project), blank paths and reasons are
// tolerated rather than refused — a note is diagnostics, and losing the whole
// list because one entry was odd would be the wrong trade — and the newest
// GitProjectionNotesCap survive.
func capGitProjectionNotes(project, kind string, notes []GitProjectionNote) []GitProjectionNote {
	if len(notes) == 0 {
		return nil
	}
	now := time.Now().Unix()
	out := make([]GitProjectionNote, 0, len(notes))
	for _, n := range notes {
		at := n.NotedAt
		if at == 0 {
			at = now
		}
		out = append(out, GitProjectionNote{
			Project: project,
			Kind:    kind,
			Path:    truncateGitProjectionReason(strings.TrimSpace(n.Path)),
			Reason:  truncateGitProjectionReason(n.Reason),
			NotedAt: at,
		})
	}
	if len(out) <= GitProjectionNotesCap {
		return out
	}
	// Newest kept. SliceStable so that entries sharing a timestamp — which is
	// every entry from one run — keep the order the importer produced them in,
	// which is the order of the files in the tree.
	sort.SliceStable(out, func(i, j int) bool { return out[i].NotedAt > out[j].NotedAt })
	return out[:GitProjectionNotesCap]
}

// GetGitProjectionNotes reads both lists for one project, newest first.
//
// A project with nothing to say returns two empty slices and no error: silence
// is the normal case and is not a not-found.
func (s *Store) GetGitProjectionNotes(ctx context.Context, project string) (quarantine, ignored []GitProjectionNote, err error) {
	if strings.TrimSpace(project) == "" {
		return nil, nil, errors.New("agentdb: git projection notes: project is required")
	}
	var rows []GitProjectionNote
	if err := s.gdb.WithContext(ctx).
		Where("project = ?", project).
		Order("noted_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("agentdb: read git projection notes for %s: %w", project, err)
	}
	for _, r := range rows {
		switch r.Kind {
		case GitProjectionNoteQuarantine:
			quarantine = append(quarantine, r)
		case GitProjectionNoteIgnored:
			ignored = append(ignored, r)
		}
	}
	return quarantine, ignored, nil
}
