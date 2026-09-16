package main

// gitbackfill.go — G10: a project's whole history, one commit per config event.
// design/2026-09-09-git-projection.md §B ("Backfill"), §F.
//
// # Why this can exist at all
//
// Two properties of the config log, and nothing else, make a faithful git
// history possible:
//
//  1. 🔴 SEQ ORDER IS COMMIT ORDER. `config_events.Seq` is allocated INSIDE the
//     mutation transaction (agentdb/config_events.go), so it is a total order
//     over a project's configuration writes. Git has no total order of its own
//     — that is kill #1 of the design — so every ordering decision here is made
//     by reading seq, and never by asking git anything.
//
//  2. EVERY PAYLOAD IS THE FULL NEW STATE, never a diff (§15.2), and deletes
//     are tombstones. So replaying the log from seq 1 reconstructs the
//     configuration at every instant without touching the projection tables.
//
// Put together: fold to seq N, render, commit with seq N's own trailers, and
// repeat. The result is a repository whose `git log` IS the config log.
//
// # The walk
//
//	events ascending by seq
//	   │
//	   ├─ fold the event into the running state            (always — even below
//	   │                                                    the watermark)
//	   ├─ seq <= watermark ?  ──yes──▶  next event         (already published)
//	   │
//	   ├─ RenderTree(state as of THIS seq, documents)
//	   ├─ WriteTree ─── nothing changed? ──▶ no commit, but the watermark still
//	   │                                     advances (a no-op mutation, e.g. a
//	   │                                     write of a field that does not
//	   │                                     render, must not stall the walk)
//	   ├─ Commit with THIS event's subject, rationale and trailers
//	   └─ MarkRendered(seq, sha)                           (durable, per commit)
//
// # Four rules that are load-bearing
//
//  1. 🔴 AN UNRENDERABLE STATE STOPS THE BACKFILL. §D's refusal is not a
//     skippable event: if seq N cannot render and we carried on to N+1, every
//     later commit would be built on a state the project never passed through,
//     and the history would be a plausible fiction. The reason is recorded on
//     the state row (NoteFailure), everything already committed stays exactly
//     where it is, and the walk resumes at the failing seq once a human has
//     fixed the field. A fold error — a payload that cannot be keyed or decoded
//     — stops it for the same reason.
//
//  2. RESUMABLE AND IDEMPOTENT. The watermark is written after every commit, so
//     a re-run starts from it and produces no duplicate. A crash between
//     WriteTree and Commit is safe because WriteTree compares the INDEX against
//     HEAD: the re-run re-stages the same bytes and still sees a change. A
//     crash between Commit and MarkRendered is safe because the re-run
//     re-renders the same tree, finds nothing changed, and simply advances the
//     watermark.
//
//  3. IT DOES NOT PUSH. G9's loop owns the network leg entirely. Backfill is a
//     local, one-shot job; the commits it makes are published by the same push
//     loop that publishes every other commit, fast-forward only.
//
//  4. PROVENANCE STILL COMES OUT OF THE LOG (DI4). The subject, body and
//     trailers are built by gitprojection.go's helpers from the ConfigEvent
//     being replayed — the same code path the live renderer uses, so a
//     backfilled commit and a live one are indistinguishable in shape. There is
//     deliberately no second trailer builder in this file.
//
// # Memory documents: rendered as they are NOW, and why
//
// The fold replays CONFIGURATION. It cannot replay memories: they are not
// config events, they have no seq, and §E renders only the newest memory per
// `name=` label. Two honest options existed — render every historical commit
// with today's documents, or with none — and this file takes TODAY'S:
//
//   - The final tree must equal what the live renderer produces, because the
//     watermark we leave behind asserts exactly that ("the working tree
//     reflects seq N"). Backfilling with no documents would leave that
//     assertion false, and the documents would then stay missing from the
//     repository until the next unrelated config mutation happened to trigger a
//     live render.
//   - The content is real. What is anachronistic is only WHEN a document
//     appears in the history, and it appears once, at the first backfilled
//     commit, and never changes again — so no fabricated evolution of a
//     document is written into the log.
//
// Nothing here invents historical memory state. The repository is a
// current-state export whose configuration history is exact and whose document
// history is deliberately flat; the ordered, stamped, searchable memory archive
// stays in the database, which is what §E already decided.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

const (
	// gitBackfillPageSize is how many config events are read per query. The log
	// is paged newest-first (ListConfigEvents has no ascending cursor), so the
	// whole history is collected and then reversed.
	gitBackfillPageSize = 500

	// gitBackfillMaxEvents bounds how much history one backfill will hold in
	// memory. It is a real limit, stated rather than hidden: a project past it
	// refuses to backfill with a message saying so, instead of being quietly
	// truncated into a history that starts in the middle.
	gitBackfillMaxEvents = 50000
)

// ── entry points ────────────────────────────────────────────────────────────

// BackfillPending backfills every project that projects and whose published
// history is behind its config log.
//
// It is a BOOT-TIME, one-shot job and must run BEFORE the render loop's
// Reconcile: Reconcile's answer to "this project is behind" is one commit
// carrying the current state, which is the right answer for a project that has
// been rendering all along and the wrong one for a project that has never
// rendered. When backfill has run first, Reconcile finds the watermark caught
// up and enqueues nothing.
//
// One project's failure never stops another's: the reason is recorded on that
// project's state row and the walk moves on, in a deterministic project order.
func (p *gitProjector) BackfillPending(ctx context.Context) error {
	projects, err := p.cfg.State.ProjectsWithRemote(ctx)
	if err != nil {
		return err
	}
	for _, project := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		behind, err := p.backfillNeeded(ctx, project)
		if err != nil {
			p.cfg.Logf("[agentd] git backfill: %s: %v", project, err)
			continue
		}
		if !behind {
			continue
		}
		if err := p.BackfillProject(ctx, project); err != nil {
			p.cfg.Logf("[agentd] git backfill: %s: %v", project, err)
		}
	}
	return nil
}

// backfillNeeded reports whether the published history is behind the log.
//
// "Behind" is a seq comparison and never a git question (§F). A project whose
// watermark already names the newest seq has nothing to replay.
func (p *gitProjector) backfillNeeded(ctx context.Context, project string) (bool, error) {
	rec, err := p.cfg.State.Get(ctx, project)
	if err != nil {
		return false, err
	}
	newest, err := p.cfg.Store.ListConfigEvents(ctx, agentdb.ConfigEventQuery{Project: project, Limit: 1})
	if err != nil {
		return false, fmt.Errorf("read config log: %w", err)
	}
	if len(newest) == 0 {
		return false, nil
	}
	return rec.LastRenderedSeq < newest[0].Seq, nil
}

// BackfillProject replays one project's config log into commits, resuming from
// the durable watermark.
//
// It is safe to call on a project that is fully up to date (it does nothing),
// on one that has never rendered (it writes the whole history), and on one
// whose previous backfill was interrupted (it continues from where that one
// got to, without duplicating a commit).
func (p *gitProjector) BackfillProject(ctx context.Context, project string) error {
	ps, err := p.cfg.Store.GetProjectSettings(ctx, project)
	if err != nil {
		return fmt.Errorf("read project settings: %w", err)
	}
	if ps == nil || strings.TrimSpace(ps.GitRemote) == "" {
		return nil // projection is off for this project
	}

	ok, err := p.cfg.State.AcquireLease(ctx, project, p.cfg.Owner, time.Now().Add(p.cfg.LeaseTTL).Unix())
	if err != nil {
		return err
	}
	if !ok {
		// §F: one writer per clone. Another agentd is working this project;
		// backfilling underneath it would interleave two histories.
		p.cfg.Logf("[agentd] git backfill: %s is held by another writer; skipping", project)
		return nil
	}
	defer func() { _ = p.cfg.State.ReleaseLease(context.WithoutCancel(ctx), project, p.cfg.Owner) }()

	events, err := p.backfillEvents(ctx, project)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	rec, err := p.cfg.State.Get(ctx, project)
	if err != nil {
		return err
	}
	if rec.LastRenderedSeq >= events[len(events)-1].Seq {
		return nil // already published to the tip of the log
	}

	repo, err := p.ensureRepo(ctx, project, ps)
	if err != nil {
		return err
	}
	sub := gitSubfolderOf(ps)

	// The documents as they stand NOW — see the file header for why this is the
	// honest choice and what it costs. Read ONCE: re-reading per event would
	// make the walk depend on the clock, and a document written mid-backfill
	// would appear as a spurious commit attributed to an unrelated config event.
	docs, err := p.loadDocuments(ctx, project)
	if err != nil {
		return fmt.Errorf("read documents: %w", err)
	}

	fold := newGitFold(project)
	committed, skipped := 0, 0
	for _, ev := range events {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fold.Apply(ev); err != nil {
			// A payload the fold cannot key or decode is a corrupt record, and
			// carrying on past it would build every later commit on a state the
			// project never had (rule 1).
			return p.backfillStop(ctx, project, ev, err)
		}
		if ev.Seq <= rec.LastRenderedSeq {
			continue // already published by an earlier run
		}

		tree, err := gitproj.RenderTree(fold.State(docs), sub)
		if err != nil {
			return p.backfillStop(ctx, project, ev, err)
		}
		changed, err := repo.WriteTree(ctx, tree, sub)
		if err != nil {
			return fmt.Errorf("write tree at seq %d: %w", ev.Seq, err)
		}

		sha := ""
		if changed {
			sha, err = repo.Commit(ctx, gitCommitSubject(ev), gitCommitBody(ev, 1), commitTrailers(ev))
			if err != nil {
				return fmt.Errorf("commit seq %d: %w", ev.Seq, err)
			}
			committed++
		} else {
			// A mutation that changed nothing RENDERABLE — a worker's
			// `updated_at`, an image the allowlist does not publish, a re-put of
			// identical settings. No commit, and the walk still advances: the
			// watermark must reach the tip or the next run replays this event
			// forever.
			sha, err = repo.HeadSHA(ctx)
			if err != nil && !errors.Is(err, gitproj.ErrNoCommits) {
				return fmt.Errorf("read head at seq %d: %w", ev.Seq, err)
			}
			skipped++
		}
		if err := p.cfg.State.MarkRendered(ctx, project, ev.Seq, sha); err != nil {
			// The commit landed and the watermark did not. Reported, not hidden:
			// the next run re-renders this seq, finds no change, and advances.
			return fmt.Errorf("seq %d committed %s but the watermark did not advance: %w", ev.Seq, short(sha), err)
		}
	}
	p.cfg.Logf("[agentd] git backfill: %s replayed %d config event(s) into %d commit(s) (%d changed nothing renderable), now at seq %d",
		project, len(events), committed, skipped, events[len(events)-1].Seq)
	return nil
}

// backfillStop ends a backfill at one event, recording why.
//
// Everything already committed stays exactly as it is — those commits are
// faithful, and deleting them would destroy the only local copy of what the
// database decided. The watermark is left naming the last event that DID
// publish, so a re-run after the field is fixed continues from the failure.
func (p *gitProjector) backfillStop(ctx context.Context, project string, ev *agentdb.ConfigEvent, cause error) error {
	reason := fmt.Sprintf("git backfill stopped at seq %d (%s): %v", ev.Seq, ev.Action, cause)
	p.cfg.Logf("[agentd] git backfill: %s: %s — earlier commits are intact; the walk resumes here once this is fixed",
		project, reason)
	if err := p.cfg.State.NoteFailure(ctx, project, reason); err != nil {
		p.cfg.Logf("[agentd] git backfill: %s: recording the failure itself failed: %v", project, err)
	}
	// The cause is wrapped, not flattened, so a caller can still ask
	// errors.Is(err, gitproj.ErrUnrenderable) and know not to retry on a timer.
	return fmt.Errorf("git backfill stopped at seq %d (%s): %w", ev.Seq, ev.Action, cause)
}

// backfillEvents reads a project's whole config log, ascending by seq.
//
// ListConfigEvents answers newest-first and pages with BeforeSeq, so the walk
// collects descending pages and reverses them. The cursor is checked for
// progress on every page: a store that ignored BeforeSeq would otherwise spin
// forever on the same page.
func (p *gitProjector) backfillEvents(ctx context.Context, project string) ([]*agentdb.ConfigEvent, error) {
	var all []*agentdb.ConfigEvent
	seen := map[int64]bool{}
	before := int64(0)
	for {
		page, err := p.cfg.Store.ListConfigEvents(ctx, agentdb.ConfigEventQuery{
			Project:   project,
			Limit:     gitBackfillPageSize,
			BeforeSeq: before,
		})
		if err != nil {
			return nil, fmt.Errorf("read config log: %w", err)
		}
		if len(page) == 0 {
			break
		}
		lowest := page[0].Seq
		for _, ev := range page {
			if ev == nil || seen[ev.Seq] {
				continue
			}
			seen[ev.Seq] = true
			all = append(all, ev)
			if ev.Seq < lowest {
				lowest = ev.Seq
			}
		}
		if len(all) > gitBackfillMaxEvents {
			return nil, fmt.Errorf(
				"project %q has more than %d config events; refusing to backfill rather than publish a history that starts in the middle",
				project, gitBackfillMaxEvents)
		}
		if before != 0 && lowest >= before {
			return nil, fmt.Errorf("config log paging made no progress at seq %d (the BeforeSeq cursor is not being applied)", before)
		}
		if len(page) < gitBackfillPageSize || lowest <= 1 {
			break
		}
		before = lowest
	}
	// Seq is the authority (§F): sort by it and by nothing else.
	sort.Slice(all, func(i, j int) bool { return all[i].Seq < all[j].Seq })
	return all, nil
}

// ── the fold ────────────────────────────────────────────────────────────────

// gitFold is the configuration as of the last event applied — agentdb's §15.6
// fold, kept incrementally and materialised as typed rows rather than payload
// maps, because RenderTree takes rows.
//
// 🔴 It does NOT call agentdb.FoldTo, for two reasons that are recorded in
// FoldTo's own doc comment as known divergences:
//
//   - FoldTo folds to a WALL-CLOCK instant and this walk needs a SEQ, which is
//     the only total order (two events can share a millisecond).
//   - FoldTo keys `project_prompt_write` as a second entity from the row it
//     lives in, so after a mixed sequence its project-settings entity and its
//     project-prompt entity disagree about `system_prompt` and a reader has to
//     know which to trust. A renderer cannot: settings.md has one body. Here
//     the prompt is applied ONTO the settings row, which is what the projection
//     table actually holds.
//
// Everything else — the entity keys, the tombstone rule, the closed action
// vocabulary — is agentdb's, via EntityRefFor and IsDeleteAction. There is no
// second copy of the fold's key rules in this file.
type gitFold struct {
	project  string
	settings *agentdb.ProjectSettings
	workers  map[string]*agentdb.Worker
	skills   map[string]*agentdb.Skill
	subs     map[string]*agentdb.Subscription
	scheds   map[string]*agentdb.Schedule
	images   map[string]*agentdb.CustomImage // keyed name:version — every version is its own entity
}

func newGitFold(project string) *gitFold {
	return &gitFold{
		project: project,
		workers: map[string]*agentdb.Worker{},
		skills:  map[string]*agentdb.Skill{},
		subs:    map[string]*agentdb.Subscription{},
		scheds:  map[string]*agentdb.Schedule{},
		images:  map[string]*agentdb.CustomImage{},
	}
}

// Apply folds one config event in. Events must arrive in ascending seq order —
// the fold is last-writer-wins, so it only runs forwards.
func (f *gitFold) Apply(ev *agentdb.ConfigEvent) error {
	if ev == nil {
		return errors.New("nil config event")
	}
	ref, err := agentdb.EntityRefFor(ev)
	if err != nil {
		return err
	}
	if agentdb.IsDeleteAction(ev.Action) {
		switch ref.Kind {
		case agentdb.EntityWorker:
			delete(f.workers, ref.Key)
		case agentdb.EntitySubscription:
			delete(f.subs, ref.Key)
		case agentdb.EntitySchedule:
			delete(f.scheds, ref.Key)
		}
		// EntityConnection's `connection_disconnect` lands here too and is
		// ignored: nothing was ever folded for it, so there is nothing to remove
		// (see the EntityConnection case below).
		return nil
	}

	switch ref.Kind {
	case agentdb.EntityWorker:
		w := &agentdb.Worker{}
		if err := gitFoldDecode(ev, w); err != nil {
			return err
		}
		if w.Name == "" {
			w.Name = ref.Key
		}
		f.workers[ref.Key] = w
	case agentdb.EntitySkill:
		sk := &agentdb.Skill{}
		if err := gitFoldDecode(ev, sk); err != nil {
			return err
		}
		if sk.Name == "" {
			sk.Name = ref.Key
		}
		f.skills[ref.Key] = sk
	case agentdb.EntitySubscription:
		s := &agentdb.Subscription{}
		if err := gitFoldDecode(ev, s); err != nil {
			return err
		}
		if s.ID == "" {
			s.ID = ref.Key
		}
		f.subs[ref.Key] = s
	case agentdb.EntitySchedule:
		s := &agentdb.Schedule{}
		if err := gitFoldDecode(ev, s); err != nil {
			return err
		}
		if s.ID == "" {
			s.ID = ref.Key
		}
		f.scheds[ref.Key] = s
	case agentdb.EntityImage:
		img := &agentdb.CustomImage{}
		if err := gitFoldDecode(ev, img); err != nil {
			return err
		}
		f.images[ref.Key] = img
	case agentdb.EntityProjectSettings:
		ps := &agentdb.ProjectSettings{}
		if err := gitFoldDecode(ev, ps); err != nil {
			return err
		}
		if ps.Project == "" {
			ps.Project = f.project
		}
		f.settings = ps
	case agentdb.EntityProjectPrompt:
		// The payload is {project, system_prompt} — one COLUMN of the settings
		// row, not the row (SetProjectPrompt). Applying it onto the row is what
		// keeps settings.md's body and its frontmatter describing one object.
		prompt, _ := ev.PayloadString("system_prompt")
		if f.settings == nil {
			f.settings = &agentdb.ProjectSettings{Project: f.project}
		}
		f.settings.SystemPrompt = prompt
	case agentdb.EntityTopology:
		// `topology_apply` records a DECISION, not a projection row: nothing in
		// the repository represents it. Skipped deliberately — silently dropping
		// it would be indistinguishable from forgetting it, so it is named here.
	case agentdb.EntityConnection:
		// `connection_connect` records that a Google account was connected
		// (design/2026-09-11-project-connections.md, addendum A10: not
		// rendered, decided explicitly). The credential itself is never in the
		// log, and the payload's account email must never reach git — anything
		// rendered is in history permanently. There is no ProjectState field for
		// it, so the tree is unchanged and the renderer commits nothing.
	}
	return nil
}

// State materialises the fold as the renderer's input.
//
// The slices are sorted by key. RenderTree is order-independent by design, so
// this is not what makes it deterministic — it is so that a failure inside the
// renderer names the same entity on every run.
func (f *gitFold) State(docs []*agentdb.Memory) gitproj.ProjectState {
	st := gitproj.ProjectState{Documents: docs}
	if f.settings != nil {
		s := *f.settings
		st.Settings = &s
	}
	for _, name := range gitFoldSortedKeys(f.workers) {
		st.Workers = append(st.Workers, f.workers[name])
	}
	for _, name := range gitFoldSortedKeys(f.skills) {
		st.Skills = append(st.Skills, f.skills[name])
	}
	for _, id := range gitFoldSortedKeys(f.subs) {
		st.Subscriptions = append(st.Subscriptions, f.subs[id])
	}
	for _, id := range gitFoldSortedKeys(f.scheds) {
		st.Schedules = append(st.Schedules, f.scheds[id])
	}
	for _, key := range gitFoldSortedKeys(f.images) {
		st.Images = append(st.Images, f.images[key])
	}
	return st
}

func gitFoldSortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gitFoldDecode turns a full-state payload back into its row.
//
// The payload was produced by json.Marshal of exactly this struct
// (agentdb.configPayload), so the json tags are the schema on both sides. A
// payload that will not decode is a corrupt record and says so rather than
// producing a half-populated row that would render as a deletion of every field
// it lost.
func gitFoldDecode(ev *agentdb.ConfigEvent, dst any) error {
	if ev.Payload == nil {
		return fmt.Errorf("config event %s (%s) has no payload — §15.2 requires the full new state", ev.ID, ev.Action)
	}
	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("config event %s (%s): re-encode payload: %w", ev.ID, ev.Action, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("config event %s (%s): payload does not decode into %T: %w", ev.ID, ev.Action, dst, err)
	}
	return nil
}
