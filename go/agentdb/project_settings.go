package agentdb

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Spec defaults for the numeric project settings (docs/product/01-session-config.md §5).
const (
	// DefaultMaxConcurrentJobs is the router/scheduler concurrency cap (§8.4).
	DefaultMaxConcurrentJobs = 4
	// DefaultBriefingMaxBytes caps each injected briefing section (§7.4).
	DefaultBriefingMaxBytes = 2048
	// DefaultSnapshotTTLDays is the snapshot reaper horizon; 0 means never (§5).
	DefaultSnapshotTTLDays = 30
)

// ErrInvalidProjectSettings marks a caller mistake (empty project, negative
// budget) as opposed to a store failure, so HTTP handlers can answer 400.
var ErrInvalidProjectSettings = errors.New("invalid project settings")

// gitTokenEnvPattern mirrors the shell's own rule for a plausible environment
// variable name — the same regexp as cmd/agentd's envVarName (googleauth.go),
// which validates api_key_env/github_token_env in the project map. agentdb
// cannot import cmd/agentd (the dependency runs the other way), so this is a
// deliberate duplicate of that one-line rule rather than a new invention;
// keep the two in sync if either changes.
var gitTokenEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// gitSubfolderPattern is the path-safety rule from
// design/2026-09-09-git-projection.md §D: every rendered/imported path
// segment is validated against this pattern, with a fixed prefix and fixed
// depth. GitSubfolder is exactly one such segment — no slashes, no `..`, no
// leading dot — because a name that escapes it could write into
// `.github/workflows/*.yml` in the project's own repo, where it would run
// with that repo's secrets on the next push.
var gitSubfolderPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// DefaultGitBranch and DefaultGitSubfolder are the values GitBranch and
// GitSubfolder take when left empty (design/2026-09-09-git-projection.md).
const (
	DefaultGitBranch    = "main"
	DefaultGitSubfolder = "orange"
)

// ProjectSettings is the per-project configuration row (§5): one row per
// project (the customer string), created lazily on first write. Projects
// themselves stay "a name that exists once something carries it", so reads of
// an unwritten project return the defaults rather than an error.
//
// MCPConfig holds map[string]MCPServerConfig as raw JSON — values are only ever
// ${VAR} references, never secrets (§4.4), so persisting/displaying it is safe.
// Deliberately no gorm `default:` tags on the numeric columns: gorm treats a
// zero value on a defaulted column as "unset" and substitutes the default,
// which would make 0 (= off / never) unwritable. The column DEFAULTs live in
// migration 020 for rows inserted outside this store; normalize() owns the
// in-Go defaulting.
type ProjectSettings struct {
	Project           string  `json:"project" gorm:"primaryKey;type:varchar(255)"`
	BaseImage         string  `json:"base_image" gorm:"type:text"`
	SystemPrompt      string  `json:"system_prompt" gorm:"type:text"`
	MCPConfig         JSONMap `json:"mcp_config" gorm:"type:jsonb"`
	AttentionChannel  JSONMap `json:"attention_channel" gorm:"type:jsonb"`
	MaxConcurrentJobs int     `json:"max_concurrent_jobs"`
	DailyTokensSoft   int64   `json:"daily_tokens_soft"` // 0 = off
	DailyTokensHard   int64   `json:"daily_tokens_hard"` // 0 = off
	BriefingMaxBytes  int     `json:"briefing_max_bytes"`
	SnapshotTTLDays   int     `json:"snapshot_ttl_days"` // 0 = never reap
	// Briefing is the project-wide briefing selector list (design B1,
	// 2026-09-08-memory-coordinated-organisation.md): unioned into every
	// worker's own `Briefing` inside BuildBriefingSections, after the
	// built-in default selector and before the worker's own entries, so a
	// worker with none of its own still receives it. Same NULL-preserving
	// SelectorList as Worker.Briefing, and the same "entry must parse as a
	// label selector" rule normalize() applies to it.
	Briefing SelectorList `json:"briefing,omitempty" gorm:"type:jsonb"`

	// The four fields below are git projection config
	// (design/2026-09-09-git-projection.md, G7): which repo a project's
	// configuration renders to, and which environment variable names the push
	// token. NONE OF THEM ARE IMPORTABLE (§D): the git importer this design
	// also builds must never write these columns. If it could, anyone with
	// commit access to the rendered repo could redirect a project's
	// projection at a repository — and a credential — they control. They
	// change only through PutProjectSettings, i.e. the console/API, never
	// through anything reading the repo back.

	// GitRemote is the GitHub repo URL this project renders its configuration
	// to. Empty means projection is off for this project. NOT IMPORTABLE —
	// see the block comment above.
	GitRemote string `json:"git_remote" gorm:"type:text"`
	// GitBranch is the branch to render to. Empty means DefaultGitBranch
	// ("main") at render time — this column is not defaulted on write, the
	// same way BaseImage and SystemPrompt aren't. NOT IMPORTABLE.
	GitBranch string `json:"git_branch" gorm:"type:text"`
	// GitSubfolder is the single path segment Orange owns inside the repo.
	// Empty means DefaultGitSubfolder ("orange") at render time. Must be one
	// valid path segment — no slashes, no `..`, no leading dot — see
	// gitSubfolderPattern. NOT IMPORTABLE.
	GitSubfolder string `json:"git_subfolder" gorm:"type:text"`
	// GitTokenEnv names the environment variable holding the push token for
	// GitRemote. The token value itself is never stored here, only the
	// variable's name (same pattern as api_key_env in the project map,
	// cmd/agentd/googleauth.go) — see the overlap note on
	// github_token_env there. NOT IMPORTABLE.
	GitTokenEnv string `json:"git_token_env" gorm:"type:text"`

	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`
}

func (ProjectSettings) TableName() string { return "project_settings" }

// DefaultProjectSettings returns the settings a project has before anything has
// ever been written for it. Not persisted — GetProjectSettings hands this back
// for an unknown project (lazy creation happens on the first write).
func DefaultProjectSettings(project string) *ProjectSettings {
	return &ProjectSettings{
		Project:           project,
		MCPConfig:         JSONMap{},
		AttentionChannel:  JSONMap{},
		MaxConcurrentJobs: DefaultMaxConcurrentJobs,
		BriefingMaxBytes:  DefaultBriefingMaxBytes,
		SnapshotTTLDays:   DefaultSnapshotTTLDays,
	}
}

// normalize validates a whole-object write and fills the "unset" numerics.
//
// Zero is a *meaningful* value for the two settings the spec says so about —
// daily_tokens_soft/hard (0 = off) and snapshot_ttl_days (0 = never) — so those
// are kept as written. For max_concurrent_jobs and briefing_max_bytes zero has
// no useful meaning (it would deadlock the router / delete every briefing), so
// it is read as "unset" and the spec default applies.
func (ps *ProjectSettings) normalize() error {
	if ps.Project == "" {
		return fmt.Errorf("%w: project is required", ErrInvalidProjectSettings)
	}
	for _, f := range []struct {
		name string
		v    int64
	}{
		{"max_concurrent_jobs", int64(ps.MaxConcurrentJobs)},
		{"daily_tokens_soft", ps.DailyTokensSoft},
		{"daily_tokens_hard", ps.DailyTokensHard},
		{"briefing_max_bytes", int64(ps.BriefingMaxBytes)},
		{"snapshot_ttl_days", int64(ps.SnapshotTTLDays)},
	} {
		if f.v < 0 {
			return fmt.Errorf("%w: %s must not be negative (got %d)", ErrInvalidProjectSettings, f.name, f.v)
		}
	}
	for i, sel := range ps.Briefing {
		trimmed := strings.TrimSpace(sel)
		if trimmed == "" {
			return fmt.Errorf("%w: briefing selector %d is empty", ErrInvalidProjectSettings, i)
		}
		if _, err := ParseLabelSelector(trimmed); err != nil {
			return fmt.Errorf("%w: briefing selector %d (%q) does not parse: %v", ErrInvalidProjectSettings, i, sel, err)
		}
	}
	if ps.GitTokenEnv != "" && !gitTokenEnvPattern.MatchString(ps.GitTokenEnv) {
		return fmt.Errorf("%w: git_token_env %q is not a valid environment variable name", ErrInvalidProjectSettings, ps.GitTokenEnv)
	}
	if ps.GitSubfolder != "" && !gitSubfolderPattern.MatchString(ps.GitSubfolder) {
		// gitSubfolderPattern already rules out slashes, "..", and a leading
		// dot (it only accepts lowercase letters, digits and internal
		// hyphens) — the message spells those out because they're the
		// concrete ways a subfolder could escape.
		return fmt.Errorf("%w: git_subfolder %q must be a single path segment (lowercase letters, digits, hyphens; no slashes, no \"..\", no leading dot)", ErrInvalidProjectSettings, ps.GitSubfolder)
	}
	if ps.MaxConcurrentJobs == 0 {
		ps.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if ps.BriefingMaxBytes == 0 {
		ps.BriefingMaxBytes = DefaultBriefingMaxBytes
	}
	if ps.MCPConfig == nil {
		ps.MCPConfig = JSONMap{}
	}
	if ps.AttentionChannel == nil {
		ps.AttentionChannel = JSONMap{}
	}
	return nil
}

// GetProjectSettings returns the settings row for project, or the defaults when
// the project has never been written. Project scoping is enforced here and ONLY
// here: the caller passes the project it is authorized for and can reach no other.
func (s *Store) GetProjectSettings(ctx context.Context, project string) (*ProjectSettings, error) {
	if project == "" {
		return nil, fmt.Errorf("%w: project is required", ErrInvalidProjectSettings)
	}
	var ps ProjectSettings
	err := s.gdb.WithContext(ctx).Where("project = ?", project).First(&ps).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DefaultProjectSettings(project), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get project settings: %w", err)
	}
	return &ps, nil
}

// PutProjectSettings writes the whole settings object for ps.Project (no patch
// semantics — §5: "PUT is whole-object"), creating the row on first write, and
// returns the stored state read back from the row that was written.
//
// The write appends a `project_settings_put` config event in the same
// transaction (§15.3/§15.4). It is one action whether the row was created or
// replaced: §5's settings row has no create/update distinction — it is created
// lazily and always written whole. cw carries the who/why; a human/API edit
// passes the zero value.
func (s *Store) PutProjectSettings(ctx context.Context, ps *ProjectSettings, cw ConfigWrite) (*ProjectSettings, error) {
	if ps == nil {
		return nil, fmt.Errorf("%w: settings are required", ErrInvalidProjectSettings)
	}
	next := *ps
	if err := next.normalize(); err != nil {
		return nil, err
	}

	var existing ProjectSettings
	err := s.gdb.WithContext(ctx).Where("project = ?", next.Project).First(&existing).Error
	switch {
	case err == nil:
		// Whole-object replace: every field is written, zero values included.
		existing.BaseImage = next.BaseImage
		existing.SystemPrompt = next.SystemPrompt
		existing.MCPConfig = next.MCPConfig
		existing.AttentionChannel = next.AttentionChannel
		existing.MaxConcurrentJobs = next.MaxConcurrentJobs
		existing.DailyTokensSoft = next.DailyTokensSoft
		existing.DailyTokensHard = next.DailyTokensHard
		existing.BriefingMaxBytes = next.BriefingMaxBytes
		existing.SnapshotTTLDays = next.SnapshotTTLDays
		existing.Briefing = next.Briefing
		existing.GitRemote = next.GitRemote
		existing.GitBranch = next.GitBranch
		existing.GitSubfolder = next.GitSubfolder
		existing.GitTokenEnv = next.GitTokenEnv
		if _, err := s.WithConfigEvent(ctx, ConfigChange{
			Project: existing.Project,
			Action:  ActionProjectSettingsPut,
			Payload: &existing,
			Write:   cw,
		}, func(tx *gorm.DB) error {
			return tx.Save(&existing).Error
		}); err != nil {
			return nil, fmt.Errorf("failed to update project settings: %w", err)
		}
		return &existing, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		if _, err := s.WithConfigEvent(ctx, ConfigChange{
			Project: next.Project,
			Action:  ActionProjectSettingsPut,
			Payload: &next,
			Write:   cw,
		}, func(tx *gorm.DB) error {
			return tx.Create(&next).Error
		}); err != nil {
			return nil, fmt.Errorf("failed to create project settings: %w", err)
		}
		return &next, nil
	default:
		return nil, fmt.Errorf("failed to read project settings: %w", err)
	}
}

// SetProjectPrompt replaces the project-level system prompt wholesale (P4) and
// appends a `project_prompt_write` config event. It is the ONLY path that writes
// that action.
//
// It is a NARROW write rather than a read-modify-write through
// PutProjectSettings for the same reason SetWorkerPrompt is: PutProjectSettings
// is whole-object (§5), so rewriting the prompt through it would silently
// rewrite the budgets and the attention channel with whatever the caller last
// read — and a prompt rewrite is precisely the mutation that must change nothing
// else. It also lets the two actions stay distinct in the log: `project_prompt_write`
// carries a rationale (§15.5) and folds as its own entity (§15.6).
//
// The payload is `{project, system_prompt}` — NOT the whole settings row. That
// shape is pinned in three places already (the fold's EntityProjectPrompt
// singleton, its tests, and the changelog UI, which accepts `system_prompt`
// first — the F1 finding of 2026-07-25), and it is what §15.3 asks for: "the new
// project system prompt".
//
// The superseded prompt is returned alongside the stored row for the automatic
// `kind=prompt-revision` memory (§9).
func (s *Store) SetProjectPrompt(ctx context.Context, project, prompt string, cw ConfigWrite) (*ProjectSettings, string, error) {
	if strings.TrimSpace(project) == "" {
		return nil, "", fmt.Errorf("%w: project is required", ErrInvalidProjectSettings)
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, "", fmt.Errorf("%w: system_prompt must not be blank (§9 validates a non-empty prompt)", ErrInvalidProjectSettings)
	}

	change := ConfigChange{
		Project: project,
		Action:  ActionProjectPromptWrite,
		Payload: JSONMap{"project": project, "system_prompt": prompt},
		Write:   cw,
	}

	var existing ProjectSettings
	err := s.gdb.WithContext(ctx).Where("project = ?", project).First(&existing).Error
	switch {
	case err == nil:
		previous := existing.SystemPrompt
		now := time.Now().Unix()
		if _, err := s.WithConfigEvent(ctx, change, func(tx *gorm.DB) error {
			return tx.Model(&ProjectSettings{}).
				Where("project = ?", project).
				Updates(map[string]any{"system_prompt": prompt, "updated_at": now}).Error
		}); err != nil {
			return nil, "", fmt.Errorf("failed to write project prompt: %w", err)
		}
		stored, err := s.GetProjectSettings(ctx, project)
		if err != nil {
			return nil, previous, err
		}
		return stored, previous, nil

	case errors.Is(err, gorm.ErrRecordNotFound):
		// The settings row is created lazily on first write (§5), so the first
		// project_prompt_write for a project creates it — with the spec defaults,
		// not with zeroes, because a row of zeroes would read as "concurrency 0".
		next := DefaultProjectSettings(project)
		next.SystemPrompt = prompt
		if _, err := s.WithConfigEvent(ctx, change, func(tx *gorm.DB) error {
			return tx.Create(next).Error
		}); err != nil {
			return nil, "", fmt.Errorf("failed to create project settings: %w", err)
		}
		stored, err := s.GetProjectSettings(ctx, project)
		if err != nil {
			return nil, "", err
		}
		// Nothing was superseded: this project had never had a prompt.
		return stored, "", nil

	default:
		return nil, "", fmt.Errorf("failed to read project settings: %w", err)
	}
}
