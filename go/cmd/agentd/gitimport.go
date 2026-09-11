package main

// The INBOUND door of the git projection (design/2026-09-09-git-projection.md
// §C). A human edits `bob/workers/copywriter.md` in the mirror repository,
// pushes, and that edit becomes config event N+1 — with the commit message as
// its rationale and an empty actor, which is already the log's encoding for a
// human/UI/API edit (agentdb.ConfigWrite).
//
// # The one rule (§A)
//
// The database is the only place where writes are put in order. Git is a door.
// This file therefore contains NO merge, NO conflict resolution and NO
// reconciliation algorithm: it calls exactly the store methods the HTTP API
// calls, and each becomes an ordinary config event. There is nothing to
// reconcile because there is only ever one serialisation point.
//
// # Four rules that are load-bearing, not stylistic
//
//  1. TREES, NEVER HISTORY. The importer diffs the tree at its durable
//     watermark against the tree at the remote tip. It never walks history to
//     work out *what happened in what order*, because git has no total order to
//     read — that is the first of the three structural kills the design records.
//     History is read for exactly two things, neither of them ordering: whose
//     commit it is, and what the commit message says.
//
//  2. TRAILERS ARE READ WITH GIT'S OWN PARSER (DI4, a security rule). Commits
//     carrying an `Bob-Seq:` trailer are the renderer's own and are skipped.
//     Rationales are model-written text, and git parses the LAST PARAGRAPH of a
//     message as trailers — so a rationale ending in `Bob-Seq: 999999` is a
//     forged trailer, not prose. `git log --format=%(trailers:…)` applies git's
//     real parser, which reads only that last paragraph, exactly as the renderer
//     assumes when it appends its own trailer block last. A `grep` of the
//     message for `Bob-Seq:` would re-open the hole completely — and would
//     pass every test written from our own commits, which is why there is a
//     test here built from a forged one.
//
//  3. PARSE EVERYTHING, THEN APPLY (quarantine). Every changed path is parsed,
//     type-checked and planned before a single store call. If ANY file fails,
//     the whole push is rejected: nothing is written and the watermark does not
//     move. A malformed file must not be able to half-apply a project's
//     configuration.
//
//  4. FIELD-MERGE, NEVER WHOLE-STRUCT (DI2). `UpsertWorker` and
//     `PutProjectSettings` are whole-object writes: a struct built out of
//     frontmatter alone silently wipes every field the projection does not
//     render. So the importer reads the current row and overlays ONLY the fields
//     gitproj.Change says the file actually changed — including a field the
//     human deleted, which arrives as a nil value and means "clear it" (DI7).
//
// What this file does NOT own: the webhook route and the poll (G12), the
// watermark column and the loop that calls this (G8), and the renderer (G4/G8).
// Import returns the SHA it reached and leaves persisting it to its caller,
// because a watermark that advanced on a quarantined push would swallow the
// human's edit forever.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

// gitImportStore is the narrow slice of *agentdb.Store the importer calls.
//
// Every method on it is one the console or the HTTP API already calls. That is
// the point: the import door has no privileged path of its own, so anything it
// writes is logged, guarded and ordered exactly like a console edit.
//
// Note what is absent. There is no DeleteSkill and no DeleteMemory: skills and
// memories are append-only (§14.1, §E), so deleting their file is reported as
// ignored rather than executed. There is no image write at all — an image is a
// content-addressed blob with a version, and nothing in frontmatter could
// reconstruct one.
type gitImportStore interface {
	// workers
	GetWorker(ctx context.Context, project, name string) (*agentdb.Worker, error)
	UpsertWorker(ctx context.Context, w *agentdb.Worker, cw agentdb.ConfigWrite) (*agentdb.Worker, error)
	SetWorkerPrompt(ctx context.Context, project, name, prompt string, cw agentdb.ConfigWrite) (*agentdb.Worker, string, error)
	DeleteWorker(ctx context.Context, project, name string, cw agentdb.ConfigWrite) error
	// project settings
	GetProjectSettings(ctx context.Context, project string) (*agentdb.ProjectSettings, error)
	PutProjectSettings(ctx context.Context, ps *agentdb.ProjectSettings, cw agentdb.ConfigWrite) (*agentdb.ProjectSettings, error)
	SetProjectPrompt(ctx context.Context, project, prompt string, cw agentdb.ConfigWrite) (*agentdb.ProjectSettings, string, error)
	// skills (append-only: a change is a new revision)
	GetProjectSkill(ctx context.Context, project, name string) (*agentdb.Skill, error)
	CreateSkill(ctx context.Context, sk *agentdb.Skill, cw agentdb.ConfigWrite) (*agentdb.Skill, error)
	// subscriptions and schedules. Listed rather than fetched by id because
	// neither Get has a not-found sentinel to distinguish "absent" from "the
	// database is down", and guessing wrong there turns an outage into a
	// create.
	ListSubscriptions(ctx context.Context, project string) ([]*agentdb.Subscription, error)
	CreateSubscription(ctx context.Context, sub *agentdb.Subscription, cw agentdb.ConfigWrite) (*agentdb.Subscription, error)
	UpdateSubscription(ctx context.Context, sub *agentdb.Subscription, cw agentdb.ConfigWrite) (*agentdb.Subscription, error)
	DeleteSubscription(ctx context.Context, project, id string, cw agentdb.ConfigWrite) error
	ListSchedules(ctx context.Context, project string) ([]*agentdb.Schedule, error)
	CreateSchedule(ctx context.Context, sch *agentdb.Schedule, cw agentdb.ConfigWrite) (*agentdb.Schedule, error)
	UpdateSchedule(ctx context.Context, sch *agentdb.Schedule, cw agentdb.ConfigWrite) (*agentdb.Schedule, error)
	DeleteSchedule(ctx context.Context, project, id string, cw agentdb.ConfigWrite) error
	// named-document memories (§E): an edit is a NEW memory carrying the same
	// name= label, never a mutation.
	NewestMemory(ctx context.Context, project, selector string) (*agentdb.Memory, error)
	CreateMemory(ctx context.Context, m *agentdb.Memory, embedding []float32) (*agentdb.Memory, bool, error)
}

var _ gitImportStore = (*agentdb.Store)(nil)

// gitImporter applies inbound commits. It holds no state: the watermark is the
// caller's (it lives in the database, §F), and the repository is passed in.
type gitImporter struct {
	store gitImportStore
}

func newGitImporter(store gitImportStore) *gitImporter { return &gitImporter{store: store} }

// gitImportInput names one import run.
type gitImportInput struct {
	// Project is the hard namespace (P5) — required, never inferred from the
	// repository or from a file's frontmatter.
	Project string
	// Repo is the project's clone. The importer only reads from it.
	Repo *gitproj.Repo
	// Subfolder is the path prefix Bob owns. Empty means
	// gitproj.DefaultSubfolder, per DI3's read-time default rule: an empty
	// subfolder reaching path construction would address the repository root.
	Subfolder string
	// FromSHA is the durable watermark: the commit this project last imported.
	// Empty means "import everything at ToSHA" — the bootstrap case (G15).
	FromSHA string
	// ToSHA is the commit to import up to. Empty means "whatever the remote
	// tip is", which Import resolves by fetching.
	ToSHA string
}

// gitImportResult is the whole outcome of one run, in a shape a console can
// render (G16) without re-deriving anything.
type gitImportResult struct {
	Project string
	FromSHA string
	ToSHA   string

	// Watermark is the SHA the caller should persist. It equals ToSHA on
	// success and FromSHA when the push was quarantined — a quarantined push
	// must be retried, not skipped, or the human's edit is lost silently.
	Watermark string

	// Quarantined is true when at least one changed file failed to parse or
	// validate. NOTHING was written in that case (rule 3 above).
	Quarantined bool
	// Failures explains the quarantine, one entry per bad file.
	Failures []gitImportFailure

	// Applied names the store writes performed, in order.
	Applied []gitImportWrite
	// Ignored names changed paths that were understood and deliberately not
	// applied — an image file, a deleted skill, a not-importable git field.
	// They are reported rather than dropped so an operator is told their edit
	// had no effect instead of being left to wonder.
	Ignored []gitImportNotice
	// OursSkipped counts commits in the range that carried our own trailers.
	OursSkipped int
	// HumanCommits counts the commits whose messages became the rationale.
	HumanCommits int
}

type gitImportFailure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type gitImportWrite struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
}

type gitImportNotice struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Import fetches, resolves the remote tip and imports everything between the
// watermark and it.
//
// The tip is the REMOTE's, not the local clone's: the inbound door exists to
// pick up what a human pushed. Fast-forwarding the local branch onto it is the
// projection worker's business (G8, §F boot reconciliation), not this file's.
func (im *gitImporter) Import(ctx context.Context, in gitImportInput) (*gitImportResult, error) {
	if in.Repo == nil {
		return nil, errors.New("gitimport: a repository is required")
	}
	to := in.ToSHA
	if to == "" {
		if err := in.Repo.Fetch(ctx); err != nil {
			return nil, err
		}
		sha, err := in.Repo.RemoteSHA(ctx)
		if err != nil {
			return nil, err
		}
		if sha == "" {
			// The branch does not exist on the remote yet: there is nothing to
			// import and nothing has gone wrong.
			return &gitImportResult{Project: in.Project, FromSHA: in.FromSHA, Watermark: in.FromSHA}, nil
		}
		to = sha
	}
	in.ToSHA = to
	return im.ImportRange(ctx, in)
}

// ImportRange is the whole flow, with both ends of the range given. Import is a
// thin wrapper that resolves the far end from the remote; tests drive this.
func (im *gitImporter) ImportRange(ctx context.Context, in gitImportInput) (*gitImportResult, error) {
	if strings.TrimSpace(in.Project) == "" {
		return nil, errors.New("gitimport: project is required (P5: the namespace is never inferred)")
	}
	if in.Repo == nil {
		return nil, errors.New("gitimport: a repository is required")
	}
	if in.ToSHA == "" {
		return nil, errors.New("gitimport: a target commit is required")
	}
	sub := in.Subfolder
	if sub == "" {
		sub = gitproj.DefaultSubfolder
	}

	res := &gitImportResult{
		Project:   in.Project,
		FromSHA:   in.FromSHA,
		ToSHA:     in.ToSHA,
		Watermark: in.FromSHA,
	}
	if in.FromSHA == in.ToSHA {
		res.Watermark = in.ToSHA
		return res, nil
	}

	// (a) Whose commits are these? Read with git's own trailer parser.
	commits, err := gitCommitsInRange(ctx, in.Repo.Path(), in.FromSHA, in.ToSHA)
	if err != nil {
		return nil, err
	}
	var human []gitCommitInfo
	for _, c := range commits {
		if c.Ours {
			res.OursSkipped++
			continue
		}
		human = append(human, c)
	}
	res.HumanCommits = len(human)
	if len(human) == 0 {
		// Every commit in the range is the renderer's own. Its content is
		// already in the database by construction, so there is nothing to
		// import — the watermark just advances past it (§C). This, plus the
		// fact that Render is pure, is the entire reason the loop terminates.
		res.Watermark = in.ToSHA
		return res, nil
	}
	rationale := gitImportRationale(human)

	// (b) Which paths changed? A TREE diff, never a history walk.
	paths, err := in.Repo.ChangedPaths(ctx, in.FromSHA, in.ToSHA)
	if err != nil {
		return nil, err
	}

	// (c) Parse and plan EVERYTHING before writing anything.
	cw := agentdb.ConfigWrite{Rationale: rationale} // empty actor = a human edit
	var plan []func(context.Context) error
	for _, p := range paths {
		if !strings.HasPrefix(p, sub+"/") {
			continue // outside the projection; not ours to read
		}
		if p == gitImportGeneratedReadme(sub) {
			// 🔴 The renderer writes a generated README.md at the root of the
			// subfolder (gitproj/render.go), and ParsePath rejects it because
			// it is not an entity shape — correctly, and with a test pinning
			// the rejection. Without this skip, the FIRST render would write a
			// file that quarantines every subsequent push, forever, since the
			// renderer keeps rewriting it. It is generated prose: there is
			// nothing in it to import, so it is skipped silently rather than
			// reported as ignored.
			continue
		}
		ch, err := im.parsePath(ctx, in.Repo, sub, p, in.FromSHA, in.ToSHA)
		if err != nil {
			res.Failures = append(res.Failures, gitImportFailure{Path: p, Reason: err.Error()})
			continue
		}
		if ch == nil {
			continue // identical on both sides of the range
		}
		if len(ch.DroppedFields) > 0 {
			res.Ignored = append(res.Ignored, gitImportNotice{
				Path: p,
				Reason: fmt.Sprintf("git configuration is not importable; ignored: %s",
					strings.Join(ch.DroppedFields, ", ")),
			})
		}
		if !ch.HasChange() {
			continue // reformatted only: writing would append a meaningless log entry
		}
		steps, notices, err := im.plan(ctx, in.Project, *ch, cw)
		if err != nil {
			res.Failures = append(res.Failures, gitImportFailure{Path: p, Reason: err.Error()})
			continue
		}
		res.Ignored = append(res.Ignored, notices...)
		plan = append(plan, steps...)
		if len(steps) > 0 {
			res.Applied = append(res.Applied, gitImportWrite{
				Path: ch.Path, Kind: string(ch.Kind), Name: ch.Name,
				Action: gitImportAction(*ch, len(steps)),
			})
		}
	}

	if len(res.Failures) > 0 {
		// Rule 3. Not one write, and the watermark stays where it was so the
		// same push is retried once the file is fixed.
		res.Quarantined = true
		res.Applied = nil
		return res, nil
	}

	// (d) Apply, through the same front door the API uses.
	for _, step := range plan {
		if err := step(ctx); err != nil {
			// A store failure part-way through is NOT a quarantine: the writes
			// already made are real, logged config events and pretending
			// otherwise would be a lie. The watermark stays put, so the next
			// run re-derives the remainder from the tree — and the writes that
			// did land are then no-ops, suppressed by the same-value check in
			// plan().
			return res, fmt.Errorf("gitimport: applying %s: %w", in.Project, err)
		}
	}

	res.Watermark = in.ToSHA
	return res, nil
}

// gitImportAction labels what one file's worth of change did, for the console.
// A file that changed both frontmatter and body produces two config events —
// an ordinary update and a prompt write — because only the prompt-write path
// records `worker_prompt_write` with its rationale (§15.5).
func gitImportAction(ch gitproj.Change, steps int) string {
	switch {
	case ch.Deleted:
		return "delete"
	case ch.BodyOnly:
		return "prompt_write"
	case ch.BodyChanged && steps > 1:
		return "update+prompt_write"
	default:
		return "update"
	}
}

// parsePath reads both sides of one path out of git and hands them to gitproj.
// A nil Change means the path is identical at both ends of the range (which
// happens when a commit changed it and a later one changed it back).
func (im *gitImporter) parsePath(ctx context.Context, repo *gitproj.Repo, sub, path, fromSHA, toSHA string) (*gitproj.Change, error) {
	var old []byte
	if fromSHA != "" {
		b, err := gitFileAt(ctx, repo.Path(), fromSHA, path)
		if err != nil {
			return nil, err
		}
		old = b
	}
	next, err := gitFileAt(ctx, repo.Path(), toSHA, path)
	if err != nil {
		return nil, err
	}
	if old == nil && next == nil {
		return nil, nil
	}
	var ch gitproj.Change
	if next == nil {
		ch, err = gitproj.ParseDelete(sub, path)
	} else {
		ch, err = gitproj.ParseAgainst(sub, path, old, next)
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

// ── planning ────────────────────────────────────────────────────────────────
//
// Each plan* function reads the CURRENT row, overlays only what the file
// changed, and returns closures. It returns an error rather than writing when
// the file is unusable, which is what makes the all-or-nothing guarantee
// possible: everything that can fail has failed before the first store call.

func (im *gitImporter) plan(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	switch ch.Kind {
	case gitproj.KindWorker:
		return im.planWorker(ctx, project, ch, cw)
	case gitproj.KindSettings:
		return im.planSettings(ctx, project, ch, cw)
	case gitproj.KindSkill:
		return im.planSkill(ctx, project, ch, cw)
	case gitproj.KindSubscription:
		return im.planSubscription(ctx, project, ch, cw)
	case gitproj.KindSchedule:
		return im.planSchedule(ctx, project, ch, cw)
	case gitproj.KindMemory:
		return im.planMemory(ctx, project, ch, cw)
	case gitproj.KindImage:
		// An image is a content-addressed blob with an allocated version; its
		// file is informational (§B "the blob is not rendered") and there is
		// nothing in frontmatter that could reconstruct one.
		return nil, []gitImportNotice{{Path: ch.Path, Reason: "images are not importable: the image blob is not rendered"}}, nil
	default:
		return nil, nil, fmt.Errorf("unknown projection kind %q", ch.Kind)
	}
}

func (im *gitImporter) planWorker(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	current, err := im.store.GetWorker(ctx, project, ch.Name)
	switch {
	case err == nil:
	case errors.Is(err, agentdb.ErrWorkerNotFound):
		current = nil
	default:
		return nil, nil, err
	}

	if ch.Deleted {
		if current == nil {
			return nil, []gitImportNotice{{Path: ch.Path, Reason: "worker already absent"}}, nil
		}
		name := ch.Name
		return []func(context.Context) error{func(ctx context.Context) error {
			return im.store.DeleteWorker(ctx, project, name, cw)
		}}, nil, nil
	}

	// THE DI2 DEFENCE. next starts as a copy of the stored row — never as a
	// struct built from frontmatter — so every field the projection does not
	// render survives a human's commit untouched.
	next := agentdb.Worker{Project: project, Name: ch.Name, Enabled: true, MaxInstances: agentdb.DefaultMaxInstances}
	if current != nil {
		next = *current
	}
	if err := applyWorkerFields(&next, ch.Fields); err != nil {
		return nil, nil, err
	}
	// Belt and braces on top of gitproj's DroppedFields, matching
	// planSettings's git_remote/git_branch restore: whatever happened above,
	// connections keeps the value the console/architect granted. Commit
	// access must never become the power to grant a worker "*" (Decision 3
	// of design/2026-09-11-project-connections.md, T11).
	if current != nil {
		next.Connections = current.Connections
	} else {
		next.Connections = nil
	}

	body := gitproj.StorageBody(ch.Body)
	promptChanged := ch.BodyChanged && strings.TrimSpace(body) != "" && (current == nil || !gitproj.BodyEqual(body, current.SystemPrompt))
	if ch.BodyChanged && strings.TrimSpace(body) == "" {
		// SetWorkerPrompt refuses a blank prompt (§9), and emptying a file is
		// far more likely to be an accident than an instruction to un-brief a
		// worker. Say so instead of failing the whole push.
		return nil, []gitImportNotice{{Path: ch.Path, Reason: "empty system prompt ignored: a worker's prompt may not be blank"}}, nil
	}

	var steps []func(context.Context) error
	if current == nil {
		// A create. There is no stored row to preserve, so the file is the
		// whole truth — the one case where building from frontmatter is right.
		created := next
		created.SystemPrompt = body
		if strings.TrimSpace(created.SystemPrompt) == "" {
			return nil, nil, fmt.Errorf("worker %q: system_prompt (the markdown body) is required", ch.Name)
		}
		steps = append(steps, func(ctx context.Context) error {
			_, err := im.store.UpsertWorker(ctx, &created, cw)
			return err
		})
		return steps, nil, nil
	}

	// Fields first, prompt second, and the field write carries the OLD prompt:
	// a whole-object save must never be the thing that rewrites a prompt, since
	// only SetWorkerPrompt records worker_prompt_write with its rationale
	// (§15.5) and only it returns the superseded text.
	fieldsWrite := next
	fieldsWrite.SystemPrompt = current.SystemPrompt
	if !sameWorker(*current, fieldsWrite) {
		w := fieldsWrite
		steps = append(steps, func(ctx context.Context) error {
			_, err := im.store.UpsertWorker(ctx, &w, cw)
			return err
		})
	}
	if promptChanged {
		name, prompt := ch.Name, body
		steps = append(steps, func(ctx context.Context) error {
			_, _, err := im.store.SetWorkerPrompt(ctx, project, name, prompt, cw)
			return err
		})
	}
	return steps, nil, nil
}

// sameWorker compares two rows ignoring the timestamps the store maintains, so
// that a file whose frontmatter says exactly what the database already says
// produces no write. Without it a range containing one of our own commits
// alongside a human's would re-write every field that commit rendered, appending
// meaningless entries to the log a human reads.
func sameWorker(a, b agentdb.Worker) bool {
	a.CreatedAt, a.UpdatedAt = 0, 0
	b.CreatedAt, b.UpdatedAt = 0, 0
	return reflect.DeepEqual(a, b)
}

func applyWorkerFields(w *agentdb.Worker, fields map[string]interface{}) error {
	for _, k := range gitFieldKeys(fields) {
		v := fields[k]
		var err error
		switch k {
		case "description":
			w.Description, err = fmString(k, v)
		case "image":
			w.Image, err = fmString(k, v)
		case "enabled":
			w.Enabled, err = fmBool(k, v)
		case "frozen":
			w.Frozen, err = fmBool(k, v)
		case "max_instances":
			var n int
			n, err = fmInt(k, v)
			if err == nil {
				if n == 0 {
					n = agentdb.DefaultMaxInstances
				}
				w.MaxInstances = n
			}
		case "briefing":
			var list []string
			list, err = fmStrings(k, v)
			w.Briefing = agentdb.SelectorList(list)
		case "mcp_config":
			var m agentdb.JSONMap
			m, err = fmMap(k, v)
			w.MCPConfig = m
		case "system_prompt":
			// The prompt is the markdown body, not a frontmatter key. A file
			// carrying both would have two answers; the body wins and this is
			// reported rather than silently preferred.
			return fmt.Errorf("system_prompt belongs in the markdown body, not in frontmatter")
		case "project", "name", "created_at", "updated_at":
			// Identity and stamps: the path is authoritative and the store owns
			// the clock.
		default:
			// Unknown keys are IGNORED, not refused, for the same reason
			// gitproj drops the not-importable ones rather than erroring: a
			// human editing prose around a key they invented is doing nothing
			// wrong, and a renderer that grows a field must not brick every
			// push until this file catches up.
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (im *gitImporter) planSettings(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	if ch.Deleted {
		return nil, []gitImportNotice{{Path: ch.Path, Reason: "settings cannot be deleted; the file will be restored on the next render"}}, nil
	}
	current, err := im.store.GetProjectSettings(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	next := *current
	if err := applySettingsFields(&next, ch.Fields); err != nil {
		return nil, nil, err
	}
	// Belt and braces on top of gitproj's DroppedFields: whatever happened
	// above, the four git-configuration columns keep the values the console
	// wrote. Commit access must never become the power to redirect a
	// project's projection (§D, DI3).
	next.GitRemote, next.GitBranch = current.GitRemote, current.GitBranch
	next.GitSubfolder, next.GitTokenEnv = current.GitSubfolder, current.GitTokenEnv

	var steps []func(context.Context) error
	fieldsWrite := next
	fieldsWrite.SystemPrompt = current.SystemPrompt
	if !sameSettings(*current, fieldsWrite) {
		ps := fieldsWrite
		steps = append(steps, func(ctx context.Context) error {
			_, err := im.store.PutProjectSettings(ctx, &ps, cw)
			return err
		})
	}
	if ch.BodyChanged && !gitproj.BodyEqual(ch.Body, current.SystemPrompt) {
		if strings.TrimSpace(ch.Body) == "" {
			return steps, []gitImportNotice{{Path: ch.Path, Reason: "empty project prompt ignored: the prompt may not be blank"}}, nil
		}
		prompt := gitproj.StorageBody(ch.Body)
		steps = append(steps, func(ctx context.Context) error {
			_, _, err := im.store.SetProjectPrompt(ctx, project, prompt, cw)
			return err
		})
	}
	return steps, nil, nil
}

func sameSettings(a, b agentdb.ProjectSettings) bool {
	a.UpdatedAt, b.UpdatedAt = 0, 0
	return reflect.DeepEqual(a, b)
}

func applySettingsFields(ps *agentdb.ProjectSettings, fields map[string]interface{}) error {
	for _, k := range gitFieldKeys(fields) {
		v := fields[k]
		var err error
		switch k {
		case "base_image":
			ps.BaseImage, err = fmString(k, v)
		case "max_concurrent_jobs":
			ps.MaxConcurrentJobs, err = fmInt(k, v)
		case "briefing_max_bytes":
			ps.BriefingMaxBytes, err = fmInt(k, v)
		case "snapshot_ttl_days":
			ps.SnapshotTTLDays, err = fmInt(k, v)
		case "daily_tokens_soft":
			ps.DailyTokensSoft, err = fmInt64(k, v)
		case "daily_tokens_hard":
			ps.DailyTokensHard, err = fmInt64(k, v)
		case "briefing":
			var list []string
			list, err = fmStrings(k, v)
			ps.Briefing = agentdb.SelectorList(list)
		case "mcp_config":
			var m agentdb.JSONMap
			m, err = fmMap(k, v)
			ps.MCPConfig = m
		case "attention_channel":
			var m agentdb.JSONMap
			m, err = fmMap(k, v)
			ps.AttentionChannel = m
		case "system_prompt":
			return fmt.Errorf("system_prompt belongs in the markdown body, not in frontmatter")
		case "project", "updated_at":
		default:
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (im *gitImporter) planSkill(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	if ch.Deleted {
		return nil, []gitImportNotice{{Path: ch.Path, Reason: "skills are append-only (§14.1): deleting the file removes nothing"}}, nil
	}
	current, err := im.store.GetProjectSkill(ctx, project, ch.Name)
	switch {
	case err == nil:
	case errors.Is(err, agentdb.ErrSkillNotFound):
		current = nil
	default:
		return nil, nil, err
	}

	next := agentdb.Skill{Customer: project, Name: ch.Name}
	if current != nil {
		next = agentdb.Skill{
			Customer:    project,
			Name:        ch.Name,
			Description: current.Description,
			Labels:      current.Labels,
			Markdown:    current.Markdown,
			InstallSh:   current.InstallSh,
			Visibility:  current.Visibility,
		}
	}
	for _, k := range gitFieldKeys(ch.Fields) {
		v := ch.Fields[k]
		switch k {
		case "description":
			s, err := fmString(k, v)
			if err != nil {
				return nil, nil, err
			}
			next.Description = s
		case "labels":
			l, err := fmLabels(k, v)
			if err != nil {
				return nil, nil, err
			}
			next.Labels = l
		case "install_sh":
			s, err := fmString(k, v)
			if err != nil {
				return nil, nil, err
			}
			next.InstallSh = s
		case "visibility":
			s, err := fmString(k, v)
			if err != nil {
				return nil, nil, err
			}
			next.Visibility = s
		case "requires_build":
			b, err := fmBool(k, v)
			if err != nil {
				return nil, nil, err
			}
			next.RequiresBuild = b
		}
	}
	if ch.BodyChanged {
		next.Markdown = gitproj.StorageBody(ch.Body)
	}
	if strings.TrimSpace(next.Markdown) == "" {
		return nil, nil, fmt.Errorf("skill %q: the markdown body is required", ch.Name)
	}
	if current != nil && next.Description == current.Description && next.InstallSh == current.InstallSh &&
		gitproj.BodyEqual(next.Markdown, current.Markdown) && next.Visibility == current.Visibility &&
		next.RequiresBuild == current.RequiresBuild &&
		reflect.DeepEqual(map[string]string(next.Labels), map[string]string(current.Labels)) {
		return nil, nil, nil
	}
	sk := next
	return []func(context.Context) error{func(ctx context.Context) error {
		_, err := im.store.CreateSkill(ctx, &sk, cw)
		return err
	}}, nil, nil
}

func (im *gitImporter) planSubscription(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	subs, err := im.store.ListSubscriptions(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	var current *agentdb.Subscription
	for _, s := range subs {
		if s.ID == ch.Name {
			current = s
			break
		}
	}

	if ch.Deleted {
		if current == nil {
			return nil, []gitImportNotice{{Path: ch.Path, Reason: "subscription already absent"}}, nil
		}
		id := ch.Name
		return []func(context.Context) error{func(ctx context.Context) error {
			return im.store.DeleteSubscription(ctx, project, id, cw)
		}}, nil, nil
	}

	next := agentdb.Subscription{ID: ch.Name, Project: project, Enabled: true}
	if current != nil {
		next = *current
	}
	for _, k := range gitFieldKeys(ch.Fields) {
		v := ch.Fields[k]
		var err error
		switch k {
		case "event_type":
			next.EventType, err = fmString(k, v)
		case "worker":
			next.Worker, err = fmString(k, v)
		case "enabled":
			next.Enabled, err = fmBool(k, v)
		case "max_firings_per_hour":
			next.MaxFiringsPerHour, err = fmInt(k, v)
		case "filter":
			var m agentdb.JSONMap
			m, err = fmMap(k, v)
			next.Filter = m
		}
		if err != nil {
			return nil, nil, err
		}
	}
	var notices []gitImportNotice
	if ch.BodyChanged && strings.TrimSpace(ch.Body) != "" {
		notices = append(notices, gitImportNotice{Path: ch.Path, Reason: "subscriptions are frontmatter only; the body was ignored"})
	}

	sub := next
	if current == nil {
		return []func(context.Context) error{func(ctx context.Context) error {
			_, err := im.store.CreateSubscription(ctx, &sub, cw)
			return err
		}}, notices, nil
	}
	a, b := *current, sub
	a.CreatedAt, a.UpdatedAt, b.CreatedAt, b.UpdatedAt = 0, 0, 0, 0
	if reflect.DeepEqual(a, b) {
		return nil, notices, nil
	}
	return []func(context.Context) error{func(ctx context.Context) error {
		_, err := im.store.UpdateSubscription(ctx, &sub, cw)
		return err
	}}, notices, nil
}

func (im *gitImporter) planSchedule(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	scheds, err := im.store.ListSchedules(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	var current *agentdb.Schedule
	for _, s := range scheds {
		if s.ID == ch.Name {
			current = s
			break
		}
	}

	if ch.Deleted {
		if current == nil {
			return nil, []gitImportNotice{{Path: ch.Path, Reason: "schedule already absent"}}, nil
		}
		id := ch.Name
		return []func(context.Context) error{func(ctx context.Context) error {
			return im.store.DeleteSchedule(ctx, project, id, cw)
		}}, nil, nil
	}

	next := agentdb.Schedule{ID: ch.Name, Project: project, Enabled: true}
	if current != nil {
		next = *current
	}
	for _, k := range gitFieldKeys(ch.Fields) {
		v := ch.Fields[k]
		var err error
		switch k {
		case "worker":
			next.Worker, err = fmString(k, v)
		case "target_session":
			next.TargetSession, err = fmString(k, v)
		case "cron":
			next.Cron, err = fmString(k, v)
		case "input":
			next.Input, err = fmString(k, v)
		case "enabled":
			next.Enabled, err = fmBool(k, v)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	var notices []gitImportNotice
	if ch.BodyChanged && strings.TrimSpace(ch.Body) != "" {
		notices = append(notices, gitImportNotice{Path: ch.Path, Reason: "schedules are frontmatter only; the body was ignored"})
	}

	sch := next
	if current == nil {
		return []func(context.Context) error{func(ctx context.Context) error {
			_, err := im.store.CreateSchedule(ctx, &sch, cw)
			return err
		}}, notices, nil
	}
	a, b := *current, sch
	// Runtime state, not configuration: never compared and never written back
	// from a file.
	a.CreatedAt, a.UpdatedAt, b.CreatedAt, b.UpdatedAt = 0, 0, 0, 0
	if reflect.DeepEqual(a, b) {
		return nil, notices, nil
	}
	return []func(context.Context) error{func(ctx context.Context) error {
		_, err := im.store.UpdateSchedule(ctx, &sch, cw)
		return err
	}}, notices, nil
}

// planMemory implements §E: a human editing bob/memory/<name>.md produces a
// NEW memory carrying the same name= label, exactly as an agent would. Nothing
// is mutated, so every version keeps its stamp and history stays searchable.
func (im *gitImporter) planMemory(ctx context.Context, project string, ch gitproj.Change, cw agentdb.ConfigWrite) ([]func(context.Context) error, []gitImportNotice, error) {
	if ch.Deleted {
		return nil, []gitImportNotice{{Path: ch.Path, Reason: "memories are immutable (§E): deleting the file retracts nothing"}}, nil
	}
	current, err := im.store.NewestMemory(ctx, project, "name="+ch.Name)
	switch {
	case err == nil:
	case errors.Is(err, agentdb.ErrMemoryNotFound):
		current = nil
	default:
		return nil, nil, err
	}

	labels := agentdb.LabelSet{}
	if current != nil {
		for k, v := range current.Labels {
			labels[k] = v
		}
	}
	for _, k := range gitFieldKeys(ch.Fields) {
		if k != "labels" {
			continue
		}
		l, err := fmLabels(k, ch.Fields[k])
		if err != nil {
			return nil, nil, err
		}
		labels = l
		if labels == nil {
			labels = agentdb.LabelSet{}
		}
	}
	// The name= label is what makes this file that document. It is taken from
	// the path, which ParsePath validated, never from frontmatter.
	labels["name"] = ch.Name

	content := gitproj.StorageBody(ch.Body)
	if !ch.BodyChanged && current != nil {
		content = current.Content
	}
	if strings.TrimSpace(content) == "" {
		return nil, nil, fmt.Errorf("memory %q: the markdown body is required", ch.Name)
	}
	if current != nil && gitproj.BodyEqual(content, current.Content) && reflect.DeepEqual(map[string]string(labels), map[string]string(current.Labels)) {
		return nil, nil, nil
	}

	// Provenance is stamped EMPTY, deliberately: a git commit is a human/API
	// act, and claiming a worker wrote it would break the trust rule an
	// embedding application depends on (docs/20-datasets.md §9).
	mem := &agentdb.Memory{Project: project, Labels: labels, Content: content}
	return []func(context.Context) error{func(ctx context.Context) error {
		_, _, err := im.store.CreateMemory(ctx, mem, nil)
		return err
	}}, nil, nil
}

// ── frontmatter coercion ────────────────────────────────────────────────────
//
// A frontmatter value arrives as whatever YAML made of it. These are tolerant
// about FORM (a quoted "true" is a bool, a quoted "4" is an int) and strict
// about MEANING: a value that cannot be read as the field's type is an error,
// which quarantines the push rather than writing a guess. A nil value means the
// human deleted the key, which per DI7 means "clear this field".

func fmString(key string, v interface{}) (string, error) {
	switch val := v.(type) {
	case nil:
		return "", nil
	case string:
		return val, nil
	case bool, int, int64, float64:
		return fmt.Sprint(val), nil
	default:
		return "", fmt.Errorf("%s: expected a string, got %T", key, v)
	}
}

func fmBool(key string, v interface{}) (bool, error) {
	switch val := v.(type) {
	case nil:
		return false, nil
	case bool:
		return val, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(val))
		if err != nil {
			switch strings.ToLower(strings.TrimSpace(val)) {
			case "yes", "on":
				return true, nil
			case "no", "off", "":
				return false, nil
			}
			return false, fmt.Errorf("%s: %q is not a true/false value", key, val)
		}
		return b, nil
	default:
		return false, fmt.Errorf("%s: expected true or false, got %T", key, v)
	}
}

func fmInt(key string, v interface{}) (int, error) {
	n, err := fmInt64(key, v)
	return int(n), err
}

func fmInt64(key string, v interface{}) (int64, error) {
	switch val := v.(type) {
	case nil:
		return 0, nil
	case int:
		return int64(val), nil
	case int64:
		return val, nil
	case float64:
		if val != float64(int64(val)) {
			return 0, fmt.Errorf("%s: expected a whole number, got %v", key, val)
		}
		return int64(val), nil
	case string:
		s := strings.TrimSpace(val)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %q is not a whole number", key, val)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s: expected a whole number, got %T", key, v)
	}
}

// fmStrings returns nil for a removed or emptied key, which is what clears a
// SelectorList to SQL NULL — the state that means "no selectors configured", as
// distinct from an explicitly empty list.
func fmStrings(key string, v interface{}) ([]string, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case []string:
		if len(val) == 0 {
			return nil, nil
		}
		return append([]string(nil), val...), nil
	case string:
		// A human writing one selector without a list dash means a list of one.
		if strings.TrimSpace(val) == "" {
			return nil, nil
		}
		return []string{val}, nil
	case []interface{}:
		out := make([]string, 0, len(val))
		for i, e := range val {
			s, err := fmString(fmt.Sprintf("%s[%d]", key, i), e)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: expected a list of strings, got %T", key, v)
	}
}

func fmMap(key string, v interface{}) (agentdb.JSONMap, error) {
	switch val := v.(type) {
	case nil:
		return agentdb.JSONMap{}, nil
	case map[string]interface{}:
		out := agentdb.JSONMap{}
		for k, e := range val {
			out[k] = e
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: expected a mapping, got %T", key, v)
	}
}

func fmLabels(key string, v interface{}) (agentdb.LabelSet, error) {
	m, err := fmMap(key, v)
	if err != nil {
		return nil, err
	}
	out := agentdb.LabelSet{}
	for k, e := range m {
		s, err := fmString(key+"."+k, e)
		if err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, nil
}

// gitImportGeneratedReadme is the one path inside the subfolder that the
// renderer writes and the parser refuses. Kept here rather than imported
// because gitproj exports no constant for it; if that file is ever renamed,
// this and gitproj/render.go move together.
func gitImportGeneratedReadme(subfolder string) string {
	return subfolder + "/README.md"
}

func gitFieldKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ── git reads ───────────────────────────────────────────────────────────────
//
// gitproj.Repo owns writing; these are the two reads the importer needs that it
// does not expose. They shell out with the same hardening Repo uses: no hooks,
// no credential prompt, no locale surprises, and safe.directory set so a clone
// owned by another uid inside a container is still readable.

type gitCommitInfo struct {
	SHA         string
	Subject     string
	Body        string
	AuthorEmail string
	// Ours is true when this commit was written by the renderer. See
	// gitCommitsInRange for why it is not a grep.
	Ours bool
}

const (
	gitFieldSep  = "\x1f"
	gitRecordSep = "\x1e"
)

// gitCommitsInRange lists the commits in (fromSHA, toSHA], oldest first, and
// classifies each as ours or a human's.
//
// 🔴 DI4. Ownership is decided by GIT'S OWN TRAILER PARSER, via
// `%(trailers:key=…)`, which reads only the last paragraph of the message —
// never by searching the message text. The renderer always emits its trailer
// block as the final paragraph precisely so that an `Bob-Seq:` line inside a
// model-written rationale lands in an earlier one, where git ignores it. A grep
// would find that forged line, skip the commit, and hand a worker the power to
// mint commits the importer refuses to read.
//
// Ownership additionally requires the fixed projection author identity and the
// Bob-Event trailer. That is not a security boundary — a pusher can set any
// author — but it errs in the safe direction: the failure mode of being too
// strict is that a commit gets imported, and the failure mode of being too loose
// is that a human's edit is silently discarded.
func gitCommitsInRange(ctx context.Context, repoPath, fromSHA, toSHA string) ([]gitCommitInfo, error) {
	rang := toSHA
	if fromSHA != "" {
		rang = fromSHA + ".." + toSHA
	}
	format := strings.Join([]string{
		"%H",
		"%ae",
		"%(trailers:key=Bob-Seq,valueonly,only)",
		"%(trailers:key=Bob-Event,valueonly,only)",
		"%s",
		"%b",
	}, gitFieldSep) + gitRecordSep

	out, stderr, err := gitRead(ctx, repoPath, "log", "--reverse", "--format="+format, rang)
	if err != nil {
		return nil, fmt.Errorf("gitimport: log %s: %w: %s", rang, err, strings.TrimSpace(stderr))
	}

	var commits []gitCommitInfo
	for _, rec := range strings.Split(out, gitRecordSep) {
		rec = strings.TrimLeft(rec, "\n")
		if strings.TrimSpace(rec) == "" {
			continue
		}
		f := strings.Split(rec, gitFieldSep)
		if len(f) < 6 {
			return nil, fmt.Errorf("gitimport: unparseable git log record in %s", rang)
		}
		c := gitCommitInfo{
			SHA:         strings.TrimSpace(f[0]),
			AuthorEmail: strings.TrimSpace(f[1]),
			Subject:     f[4],
			Body:        f[5],
		}
		seq := strings.TrimSpace(f[2])
		event := strings.TrimSpace(f[3])
		c.Ours = seq != "" && event != "" && c.AuthorEmail == gitproj.AuthorEmail
		commits = append(commits, c)
	}
	return commits, nil
}

// gitFileAt returns the bytes at path in commit sha, or nil when the path does
// not exist there. nil is the shape gitproj.ParseAgainst reads as "absent".
func gitFileAt(ctx context.Context, repoPath, sha, path string) ([]byte, error) {
	spec := sha + ":" + path
	if _, _, err := gitRead(ctx, repoPath, "rev-parse", "--verify", "--quiet", spec); err != nil {
		return nil, nil // not present in that tree
	}
	out, stderr, err := gitRead(ctx, repoPath, "cat-file", "blob", spec)
	if err != nil {
		return nil, fmt.Errorf("gitimport: read %s: %w: %s", spec, err, strings.TrimSpace(stderr))
	}
	return []byte(out), nil
}

func gitRead(ctx context.Context, repoPath string, args ...string) (string, string, error) {
	full := append([]string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "gc.auto=0",
		"-c", "safe.directory=" + repoPath,
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = repoPath
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// gitImportRationale turns the human commits in a push into the rationale the
// config log records. It is the commit message, verbatim — the whole reason
// this door exists is that "why" is written by a person in a place people
// already look.
func gitImportRationale(commits []gitCommitInfo) string {
	const maxRationale = 8000
	var parts []string
	for _, c := range commits {
		msg := strings.TrimSpace(c.Subject)
		if body := strings.TrimSpace(c.Body); body != "" {
			msg = strings.TrimSpace(msg + "\n\n" + body)
		}
		if msg == "" {
			// git permits an empty message; the log requires a rationale on a
			// prompt write, so name the commit rather than write nothing.
			msg = "imported from git commit " + shortSHA(c.SHA)
		}
		parts = append(parts, msg)
	}
	out := strings.Join(parts, "\n\n---\n\n")
	if len(out) > maxRationale {
		out = out[:maxRationale] + "\n\n[truncated]"
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
