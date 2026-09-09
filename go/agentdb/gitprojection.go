package agentdb

// gitprojection.go — the durable state of the git projection (design/
// 2026-09-09-git-projection.md §F, ticket G22): one row per project holding
// how far the projection has rendered, pushed and imported, why it last
// failed, and which process currently owns the clone.
//
// # Why this lives here and not in cmd/agentd
//
// G8 created this table with `AutoMigrate` at boot because its file budget was
// three files in cmd/agentd. Nothing else in this product creates schema at
// boot: every table comes from a numbered migration in migrations.go. A table
// that appears by side effect is invisible to anyone reading the migration
// list, cannot be reviewed as a schema change, and can differ between a fresh
// database and an upgraded one — which is exactly the class of difference that
// only shows up in production. So the schema is migration `048_git_projection_state`
// and the accessors are ordinary *Store methods, like every other table.
//
// # Runtime state, not configuration
//
// These rows are the projection worker's own bookkeeping — the same class as
// the session lease in leases.go. They write NO config event (§15.3 rule 3) and
// are not under the config-write guard: a watermark advancing is not a
// configuration change a human should have to read in a changelog.
//
// # The lease
//
// AcquireGitProjectionLease is a compare-and-swap, and it is deliberately
// RE-ENTRANT for the same owner: the render loop and the push loop are two
// goroutines in ONE agentd process and both take the lease around their work.
// If the second take failed, the two loops would starve each other on every
// pass. What the lease excludes is a SECOND PROCESS holding the same clone —
// one writer per working tree (§F). leases.go's session lease is a different
// thing entirely (it is keyed by session and reaped by the router), which is
// why this one exists rather than being folded into it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// The kinds of failure a projection run can record in
// GitProjectionState.LastErrorKind. A closed set, deliberately small: it exists
// to let a console say the right word, not to describe git.
//
// 🔴 The KIND is the authority on what a failure was, not the message. The
// message is a human sentence that may be reworded at any time; the kind is
// decided by errors.Is against gitproj's own sentinels, at the point the error
// is raised.
const (
	// GitProjectionErrorNone is the zero value: nothing is wrong.
	GitProjectionErrorNone = ""
	// GitProjectionErrorUnrenderable — a credential-bearing field holds a
	// literal, so §D refused to write any tree at all (gitproj.ErrUnrenderable).
	GitProjectionErrorUnrenderable = "unrenderable"
	// GitProjectionErrorNotFastForward — the remote is not an ancestor of local
	// HEAD (gitproj.ErrNotFastForward). The one failure that never resolves on
	// its own.
	GitProjectionErrorNotFastForward = "not_fast_forward"
	// GitProjectionErrorQuarantined — an inbound push was rejected wholesale
	// because a file did not parse. Nothing was applied; the per-file reasons
	// are the quarantine notes (gitprojectionnotes.go).
	GitProjectionErrorQuarantined = "quarantined"
	// GitProjectionErrorNeedsAdoption — the remote's subfolder already holds an
	// exported project that THIS project has never imported, so rendering would
	// have written an empty configuration over somebody's export and deleted
	// their files (G27, DI21). Nothing was rendered and nothing was deleted.
	//
	// 🔴 It is a STATE, not a breakage: the projection is waiting for a human
	// to say what the folder is. `POST /agent/git-bootstrap` adopts it; after
	// that the watermark is set and this can never fire again for the project.
	GitProjectionErrorNeedsAdoption = "needs_adoption"
	// GitProjectionErrorOther — anything else: an unreachable host, a bad
	// credential, a disk that filled.
	GitProjectionErrorOther = "other"
)

// GitProjectionState is one project's projection state.
//
// The struct is the source of truth for the COLUMN NAMES too: migration 048
// spells the same columns by hand, and TestGitProjectionStateSchemaMatchesTheMigration
// fails if the two ever drift — that is what makes "a fresh database and a
// migrated one end up with the same schema" a checked claim rather than a hope.
type GitProjectionState struct {
	Project string `json:"project" gorm:"primaryKey;type:varchar(255)"`
	// LastRenderedSeq is the config-log sequence the working tree reflects.
	// Seq is the authority on order (§F); nothing here ever asks git.
	LastRenderedSeq int64  `json:"last_rendered_seq" gorm:"not null;default:0"`
	LastRenderedSHA string `json:"last_rendered_sha" gorm:"type:text;not null;default:''"`
	LastPushedSHA   string `json:"last_pushed_sha" gorm:"type:text;not null;default:''"`
	// LastImportedSHA is the INBOUND watermark: the commit the importer last
	// applied. The importer itself deliberately persists nothing ("a watermark
	// that advanced on a quarantined push would swallow the human's edit
	// forever"), so it hands back the SHA the caller should store and
	// MarkGitProjectionImported is where it lands.
	LastImportedSHA string `json:"last_imported_sha" gorm:"type:text;not null;default:''"`
	// LastError is why the last attempt did not publish — an unrenderable
	// field, a diverged remote, an unreachable host.
	LastError   string `json:"last_error" gorm:"type:text;not null;default:''"`
	LastErrorAt int64  `json:"last_error_at" gorm:"not null;default:0"`
	// LastErrorKind is WHICH KIND of failure that was, one of the
	// GitProjectionError* constants, decided at the point the error was raised
	// and its Go type was still in hand. It exists because the alternative —
	// which is what shipped first — is a consumer re-deriving the kind by
	// string-matching gitproj's own error text, so that rewording a sentence
	// silently reclassifies a production failure. Empty means "no failure", and
	// is what MarkGitProjectionRendered and MarkGitProjectionPushed write back.
	LastErrorKind string `json:"last_error_kind" gorm:"type:varchar(32);not null;default:''"`
	// LeaseOwner / LeaseExpiresAt are the single-writer guarantee. The
	// deployment already says replicas: 1; this is what makes that a guarantee
	// rather than a convention (§F).
	LeaseOwner     string `json:"lease_owner" gorm:"type:text;not null;default:''"`
	LeaseExpiresAt int64  `json:"lease_expires_at" gorm:"not null;default:0"`
	UpdatedAt      int64  `json:"updated_at" gorm:"not null;default:0"`
}

func (GitProjectionState) TableName() string { return "git_projection_state" }

// GitProjectionTarget is one project that has git projection configured: the
// repository it renders to, and the NAME of the environment variable holding
// the webhook secret its inbound deliveries are signed with. Never a secret
// value — see ProjectSettings.GitWebhookSecretEnv.
type GitProjectionTarget struct {
	Project             string
	GitRemote           string
	GitWebhookSecretEnv string
}

// GetGitProjectionState reads one project's row.
//
// A project that has never rendered has no row, and that is not an error: the
// zero-valued row IS the correct answer ("nothing rendered, nothing pushed,
// nothing imported, no lease"). Returning a not-found sentinel would make
// every caller write the same three lines.
func (s *Store) GetGitProjectionState(ctx context.Context, project string) (*GitProjectionState, error) {
	if strings.TrimSpace(project) == "" {
		return nil, errors.New("agentdb: git projection state: project is required")
	}
	var rec GitProjectionState
	err := s.gdb.WithContext(ctx).Where("project = ?", project).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &GitProjectionState{Project: project}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("agentdb: read git projection state for %s: %w", project, err)
	}
	return &rec, nil
}

// ensureGitProjectionRow creates the row if it is missing, without disturbing
// an existing one. Two processes racing here both end up with the row they
// wanted.
func (s *Store) ensureGitProjectionRow(ctx context.Context, project string) error {
	rec := GitProjectionState{Project: project, UpdatedAt: time.Now().Unix()}
	err := s.gdb.WithContext(ctx).
		Where("project = ?", project).
		FirstOrCreate(&rec, GitProjectionState{Project: project}).Error
	if err != nil {
		return fmt.Errorf("agentdb: create git projection state for %s: %w", project, err)
	}
	return nil
}

// AcquireGitProjectionLease takes (or extends) the projection lease on one
// project until `until` (unix seconds), reporting whether this owner now holds
// it.
//
// It succeeds when the row holds NO lease, holds an EXPIRED one, or already
// holds THIS owner's — see the file comment: re-entrancy for the same owner is
// deliberate, because the render and push loops are two goroutines in one
// process and must not deadlock each other over a row.
func (s *Store) AcquireGitProjectionLease(ctx context.Context, project, owner string, until int64) (bool, error) {
	if strings.TrimSpace(project) == "" {
		return false, errors.New("agentdb: git projection lease: project is required")
	}
	if strings.TrimSpace(owner) == "" {
		return false, errors.New("agentdb: git projection lease: an owner is required")
	}
	if err := s.ensureGitProjectionRow(ctx, project); err != nil {
		return false, err
	}
	now := time.Now().Unix()
	res := s.gdb.WithContext(ctx).Model(&GitProjectionState{}).
		Where("project = ? AND (lease_owner = ? OR lease_owner = ? OR lease_expires_at < ?)",
			project, "", owner, now).
		Updates(map[string]any{
			"lease_owner":      owner,
			"lease_expires_at": until,
			"updated_at":       now,
		})
	if res.Error != nil {
		return false, fmt.Errorf("agentdb: acquire git projection lease for %s: %w", project, res.Error)
	}
	return res.RowsAffected > 0, nil
}

// ReleaseGitProjectionLease drops the lease if this owner is the one holding
// it. Releasing a lease somebody else holds is a no-op, not an error: the
// caller's own work is over either way, and stealing the row back would break
// the one-writer rule the lease exists to keep.
func (s *Store) ReleaseGitProjectionLease(ctx context.Context, project, owner string) error {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(owner) == "" {
		return errors.New("agentdb: git projection lease: project and owner are required")
	}
	err := s.gdb.WithContext(ctx).Model(&GitProjectionState{}).
		Where("project = ? AND lease_owner = ?", project, owner).
		Updates(map[string]any{"lease_owner": "", "lease_expires_at": 0}).Error
	if err != nil {
		return fmt.Errorf("agentdb: release git projection lease for %s: %w", project, err)
	}
	return nil
}

// MarkGitProjectionRendered records that the working tree now reflects config
// sequence seq, committed as sha. A successful render clears the last error:
// whatever was wrong is no longer wrong.
func (s *Store) MarkGitProjectionRendered(ctx context.Context, project string, seq int64, sha string) error {
	return s.updateGitProjectionRow(ctx, project, "record render", map[string]any{
		"last_rendered_seq": seq,
		"last_rendered_sha": sha,
		"last_error":        "",
		"last_error_at":     0,
		"last_error_kind":   GitProjectionErrorNone,
	})
}

// MarkGitProjectionPushed records that sha is published on the remote.
func (s *Store) MarkGitProjectionPushed(ctx context.Context, project, sha string) error {
	return s.updateGitProjectionRow(ctx, project, "record push", map[string]any{
		"last_pushed_sha": sha,
		"last_error":      "",
		"last_error_at":   0,
		"last_error_kind": GitProjectionErrorNone,
	})
}

// MarkGitProjectionImported advances the INBOUND watermark to sha — the commit
// the importer has applied. It must only ever be called with the watermark the
// importer returned: on a quarantined push that is the OLD value, because a
// watermark that advanced past a rejected commit would swallow the human's
// edit forever.
//
// Unlike the two above it does NOT clear last_error: an import can succeed
// while the outbound half is still failing to publish, and clearing the reason
// would hide it.
func (s *Store) MarkGitProjectionImported(ctx context.Context, project, sha string) error {
	if strings.TrimSpace(sha) == "" {
		// Nothing was imported (an empty remote, or a quarantine that left the
		// watermark where it was). Writing "" would erase a real watermark.
		return nil
	}
	return s.updateGitProjectionRow(ctx, project, "record import", map[string]any{
		"last_imported_sha": sha,
	})
}

// ClearGitProjectionQuarantine clears the stored failure ONLY when it is a
// quarantine — the inbound kind. It is what a clean import or a clean bootstrap
// calls once it has succeeded.
//
// It is deliberately narrower than "clear the error". MarkGitProjectionImported
// clears nothing at all, for the good reason stated above it: an import can
// succeed while the OUTBOUND half is still failing to publish, and blanking the
// row there would hide a real push failure behind an unrelated success. But the
// opposite error is just as bad and was the live behaviour: a quarantine that
// outlived the push which fixed it left the console reading `failing`, with the
// sentence describing a file the operator had already corrected. G23 persisted
// the notes precisely so a stale red banner would not teach an operator to
// ignore banners; leaving the row's own error behind defeated that.
//
// The kind is matched in the WHERE clause rather than read-then-written, so a
// push failure recorded between the read and the write cannot be erased by it.
func (s *Store) ClearGitProjectionQuarantine(ctx context.Context, project string) error {
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("agentdb: git projection clear quarantine: project is required")
	}
	err := s.gdb.WithContext(ctx).Model(&GitProjectionState{}).
		Where("project = ? AND last_error_kind = ?", project, GitProjectionErrorQuarantined).
		Updates(map[string]any{
			"last_error":      "",
			"last_error_at":   0,
			"last_error_kind": GitProjectionErrorNone,
			"updated_at":      time.Now().Unix(),
		}).Error
	if err != nil {
		return fmt.Errorf("agentdb: git projection clear quarantine for %s: %w", project, err)
	}
	return nil
}

// NoteGitProjectionFailure records what kind of failure stopped the last
// attempt, and why. The reason is truncated: a git error page must not become a
// row nobody can display.
//
// `kind` is one of the GitProjectionError* constants and is the field a
// consumer should branch on; an unrecognised kind is stored as
// GitProjectionErrorOther rather than being written through, so nothing
// downstream has to defend against a spelling.
func (s *Store) NoteGitProjectionFailure(ctx context.Context, project, kind, reason string) error {
	return s.updateGitProjectionRow(ctx, project, "record failure", map[string]any{
		"last_error":      truncateGitProjectionReason(reason),
		"last_error_at":   time.Now().Unix(),
		"last_error_kind": normalizeGitProjectionErrorKind(kind),
	})
}

// normalizeGitProjectionErrorKind maps anything unknown onto "other". A failure
// whose kind we cannot name is still a failure, and storing an unvetted string
// would push the defending onto every reader.
func normalizeGitProjectionErrorKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case GitProjectionErrorUnrenderable:
		return GitProjectionErrorUnrenderable
	case GitProjectionErrorNotFastForward:
		return GitProjectionErrorNotFastForward
	case GitProjectionErrorQuarantined:
		return GitProjectionErrorQuarantined
	case GitProjectionErrorNeedsAdoption:
		return GitProjectionErrorNeedsAdoption
	default:
		return GitProjectionErrorOther
	}
}

func (s *Store) updateGitProjectionRow(ctx context.Context, project, what string, fields map[string]any) error {
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("agentdb: git projection %s: project is required", what)
	}
	if err := s.ensureGitProjectionRow(ctx, project); err != nil {
		return err
	}
	fields["updated_at"] = time.Now().Unix()
	err := s.gdb.WithContext(ctx).Model(&GitProjectionState{}).
		Where("project = ?", project).
		Updates(fields).Error
	if err != nil {
		return fmt.Errorf("agentdb: git projection %s for %s: %w", what, project, err)
	}
	return nil
}

// ListProjectsWithGitRemote lists every project whose settings row names a
// repository, in a deterministic order. A project with an empty git_remote does
// not project (§B) and is not listed.
//
// It is the candidate list for boot reconciliation AND the poll fallback's
// Lister (httpapi.GitWebhookProjectLister, whose method is named
// ProjectsWithGitRemote — cmd/agentd's wiring adapts the name). The "List"
// prefix is not cosmetic: config_events_test.go's classifier reads a method
// naming a configuration entity WITHOUT a read verb as a mutation, and it is
// right to.
func (s *Store) ListProjectsWithGitRemote(ctx context.Context) ([]string, error) {
	var out []string
	err := s.gdb.WithContext(ctx).Model(&ProjectSettings{}).
		Where("git_remote <> ''").
		Order("project ASC").
		Pluck("project", &out).Error
	if err != nil {
		return nil, fmt.Errorf("agentdb: list projected projects: %w", err)
	}
	return out, nil
}

// ListGitProjectionTargets is ProjectsWithGitRemote plus the two fields the
// webhook resolver needs to route a delivery: the remote to match the payload's
// repository against, and the env var name holding that project's webhook
// secret. Deterministic order, for the same reason.
func (s *Store) ListGitProjectionTargets(ctx context.Context) ([]GitProjectionTarget, error) {
	var rows []ProjectSettings
	err := s.gdb.WithContext(ctx).
		Where("git_remote <> ''").
		Order("project ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("agentdb: list git projection targets: %w", err)
	}
	out := make([]GitProjectionTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, GitProjectionTarget{
			Project:             r.Project,
			GitRemote:           r.GitRemote,
			GitWebhookSecretEnv: r.GitWebhookSecretEnv,
		})
	}
	return out, nil
}

func truncateGitProjectionReason(s string) string {
	const max = 2000
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
