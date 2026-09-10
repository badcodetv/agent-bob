package main

// G15 — BOOTSTRAP A PROJECT FROM A FOLDER
// (design/2026-09-09-git-projection.md §C, ticket G15).
//
// Point a project at a repository and a subfolder, and its whole configuration
// is created from the files. This is the "project as code" payoff: an operator
// clones a folder someone exported, or hand-writes one, and gets a working
// project without touching the console.
//
// # It is the SAME DOOR as an ordinary import, not a parallel code path
//
// Everything below delegates to gitimport.go: gitImporter.parsePath does the
// reading and the gitproj parsing, gitImporter.plan does the field-merge and
// produces the store calls. This file adds exactly three things an incremental
// import does not need, and nothing else:
//
//  1. THE RANGE. There is no watermark to diff from, so the "changed paths" are
//     every path in the tree at one commit (gitproj.Repo.ChangedPaths with an
//     empty fromSHA is documented as the bootstrap case) and every file is read
//     with no previous version, which gitproj.ParseAgainst treats as a create.
//
//  2. WHOSE COMMITS THEY ARE DOES NOT MATTER. ImportRange skips commits
//     carrying the renderer's trailers, because their content is already in the
//     database by construction. In a bootstrap that reasoning is inverted: the
//     folder is very often project A's *rendered* output, so every commit in it
//     is "ours" and an ordinary import would correctly conclude there is
//     nothing to do. A bootstrap reads the TREE and ignores authorship
//     entirely — which is safe for the same reason the rest of the design is:
//     the files are parsed and applied through the store's front door, so a
//     forged trailer buys a writer nothing here (it is not consulted).
//
//  3. ORDER. A tree listing is alphabetical, which would create subscriptions
//     and schedules before the workers they wake. Nothing in the store enforces
//     that ordering, but a config log a human reads should not describe a
//     project being wired up backwards, so the files are applied
//     settings → workers → skills → subscriptions → schedules → documents.
//
// # What a bootstrap does NOT restore
//
// 🔴 THE MEMORY LOG. Per §E of the design, confirmed on 2026-09-09 and marked
// do-not-re-open: only *named* memories — the newest memory per `name=` label,
// the message-board / label-registry / goal documents a human reads — are
// rendered as files. The append-only log of summaries, lessons, verdicts,
// retractions and prompt-revisions is not in the repository at all. So a
// bootstrapped project arrives with its whole configuration and its named
// documents, and with NO MEMORY LOG. It starts with a brain-state, not a past.
//
// That is the intended trade, not a gap to be closed later: the repo is a
// current-state export and the archive stays in the database where it is
// ordered, stamped and searchable. Do not add history reconstruction here.
//
// Three smaller things also do not come back, and are reported in Ignored
// rather than dropped silently, so an operator is told what their folder did
// not carry (G11's findings):
//
//   - IMAGES. images/<name>.md is informational; the image itself is a
//     content-addressed blob with an allocated version and nothing in
//     frontmatter could reconstruct one.
//   - THE FOUR GIT-CONFIGURATION FIELDS. See below — this is a security rule.
//   - SERVER-OWNED FIELDS. A skill's revision, and the created_by_worker /
//     created_by_session provenance on skills and memories, are stamped by the
//     store. A bootstrap is a human/API act, so provenance is stamped empty and
//     a fresh skill starts at revision 1. That is the trust rule an embedding
//     application depends on (docs/20-datasets.md §9) working as designed.
//
// A bootstrap also never DELETES anything. There is no previous tree to diff
// against, so a file's absence carries no instruction: configuration the folder
// does not mention is left exactly as it was. (Skills and memories are
// append-only in any case — a missing file removes nothing there even during an
// ordinary import.)
//
// # 🔴 The git-configuration fields are not importable, and this is where it
// matters most
//
// git_remote, git_branch, git_subfolder and git_token_env say WHICH repository
// a project publishes to and WHICH environment variable holds the credential to
// push there. A folder that could set them would redirect a project's
// projection — and its push token — at a repository the folder's author
// controls. gitproj.ParseAgainst already strips them into Change.DroppedFields
// and gitimport.go's planSettings restores the stored values on top; this file
// adds a third check that refuses outright, because bootstrap is precisely the
// moment a human points the system at a folder they did not write.
//
// # Ownership
//
// As with the importer, the watermark is the CALLER's to persist (§F: it lives
// in the database, and G8 owns the column). Bootstrap returns the SHA it
// reached; persisting it is what makes the next ordinary import a no-op instead
// of a re-import of everything.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/gitproj"
)

// gitBootstrapper creates a project's configuration from a folder. It holds no
// state of its own: everything it does is the importer's, run over a whole tree.
type gitBootstrapper struct {
	im *gitImporter
}

func newGitBootstrapper(store gitImportStore) *gitBootstrapper {
	return &gitBootstrapper{im: newGitImporter(store)}
}

// gitBootstrapInput names one bootstrap run.
type gitBootstrapInput struct {
	// Project is the hard namespace (P5) — required, and never inferred from
	// the repository or from a file's frontmatter. The folder says what the
	// configuration is; the caller says whose it is.
	Project string
	// Repo is the clone to read. Bootstrap only ever reads from it.
	Repo *gitproj.Repo
	// Subfolder is the path prefix Bob owns. Empty means
	// gitproj.DefaultSubfolder, per DI3's read-time default rule: an empty
	// subfolder reaching path construction would address the repository root.
	Subfolder string
	// SHA is the commit to bootstrap from. Empty means "the remote tip", which
	// Bootstrap resolves by fetching — the ordinary case, since the point is to
	// pick up what somebody pushed.
	SHA string
}

// gitBootstrapResult is the whole outcome of one run, in the same shape as
// gitImportResult so a console (G16) can render either without special-casing.
type gitBootstrapResult struct {
	Project string
	// SHA is the commit the folder was read at.
	SHA string
	// Watermark is what the caller should persist as the project's
	// last-imported SHA. It equals SHA on success and is EMPTY on a
	// quarantine, so a rejected bootstrap is retried in full rather than
	// leaving a project that believes it has already imported the folder it
	// refused.
	Watermark string

	// Quarantined is true when at least one file failed to parse or validate.
	// NOTHING was written in that case: a half-bootstrapped project is worse
	// than a failed one, because it looks configured.
	Quarantined bool
	// Failures explains the quarantine, one entry per bad file.
	Failures []gitImportFailure

	// Applied names the store writes performed, in the order they ran.
	Applied []gitImportWrite
	// Ignored names files that were understood and deliberately not applied —
	// an image, a not-importable git field, a file the store had nothing to do
	// with. Reported rather than dropped so an operator can see what their
	// folder did not carry.
	Ignored []gitImportNotice
	// Files counts the projection files found under the subfolder, including
	// ones that produced no write.
	Files int
}

// ErrGitBootstrapNoCommits means the branch has no commit to read a folder
// from. It is a distinct error because "the remote is empty" and "the folder is
// empty" are different operator mistakes with different fixes.
var ErrGitBootstrapNoCommits = errors.New("gitbootstrap: the branch has no commits to bootstrap from")

// Bootstrap creates a project's configuration from every file under the
// subfolder at one commit.
//
// It is all-or-nothing on parsing: every file is read, parsed and planned
// before the first store call, and a single bad file rejects the whole
// bootstrap with nothing written. Once the writes begin they are ordinary
// config events, so a store failure part-way through leaves the events that did
// land — they are real and the log says so. The watermark is not advanced in
// that case, so re-running re-derives the remainder and the writes that already
// landed become no-ops, suppressed by the same-value checks in the importer's
// plan step.
func (b *gitBootstrapper) Bootstrap(ctx context.Context, in gitBootstrapInput) (*gitBootstrapResult, error) {
	if strings.TrimSpace(in.Project) == "" {
		return nil, errors.New("gitbootstrap: project is required (P5: the namespace is never inferred)")
	}
	if in.Repo == nil {
		return nil, errors.New("gitbootstrap: a repository is required")
	}
	sub := in.Subfolder
	if sub == "" {
		sub = gitproj.DefaultSubfolder
	}

	sha := in.SHA
	if sha == "" {
		if err := in.Repo.Fetch(ctx); err != nil {
			return nil, err
		}
		remote, err := in.Repo.RemoteSHA(ctx)
		if err != nil {
			return nil, err
		}
		if remote == "" {
			return nil, fmt.Errorf("%w: %s on %s", ErrGitBootstrapNoCommits, in.Repo.Branch(), in.Repo.Remote())
		}
		sha = remote
	}

	res := &gitBootstrapResult{Project: in.Project, SHA: sha}

	// (a) Every path in the tree. An empty fromSHA is ChangedPaths' documented
	// bootstrap case: it lists the whole tree rather than diffing.
	all, err := in.Repo.ChangedPaths(ctx, "", sha)
	if err != nil {
		return nil, err
	}

	// (b) Keep only what is ours, and put it in dependency order. ParsePath is
	// the same validator the importer uses, called here only to learn the kind
	// so the files can be sorted; ParseAgainst validates the path again, which
	// is free (it is pure) and keeps this file from being the one that decides
	// what a valid path is.
	type entry struct {
		path string
		kind gitproj.Kind
	}
	var files []entry
	for _, p := range all {
		if !strings.HasPrefix(p, sub+"/") {
			// Somebody else's files. A projection subfolder usually lives in a
			// repository with a life of its own, so this is the normal case and
			// is not reported — an "ignored" entry per source file would bury
			// the ones that matter.
			continue
		}
		if p == gitImportGeneratedReadme(sub) {
			// 🔴 DI9 item 2. The renderer writes a generated README.md at the
			// root of the subfolder and gitproj.ParsePath rejects it, correctly,
			// because it is not an entity shape. Any folder produced by a render
			// contains it — which is to say every folder a bootstrap is pointed
			// at — so failing to skip it here would quarantine every bootstrap
			// of an exported project. It is generated prose with nothing in it
			// to import, so it is skipped silently rather than reported.
			continue
		}
		kind, _, err := gitproj.ParsePath(sub, p)
		if err != nil {
			res.Failures = append(res.Failures, gitImportFailure{Path: p, Reason: err.Error()})
			continue
		}
		files = append(files, entry{path: p, kind: kind})
	}
	res.Files = len(files)
	sort.SliceStable(files, func(i, j int) bool {
		ri, rj := gitBootstrapOrder(files[i].kind), gitBootstrapOrder(files[j].kind)
		if ri != rj {
			return ri < rj
		}
		return files[i].path < files[j].path
	})

	// (c) Parse and plan EVERYTHING before writing anything.
	cw := agentdb.ConfigWrite{Rationale: gitBootstrapRationale(in.Project, in.Repo, sub, sha)}
	var plan []func(context.Context) error
	for _, f := range files {
		// An empty fromSHA gives ParseAgainst a nil previous version, which is
		// the create case: every field in the file is a change, which is exactly
		// right when there is nothing to merge onto. Where a project already has
		// a row of that name the importer's plan step merges onto it, so
		// bootstrapping over a populated project behaves as an ordinary import
		// rather than as a wipe.
		ch, err := b.im.parsePath(ctx, in.Repo, sub, f.path, "", sha)
		if err != nil {
			res.Failures = append(res.Failures, gitImportFailure{Path: f.path, Reason: err.Error()})
			continue
		}
		if ch == nil || !ch.HasChange() {
			continue
		}
		if len(ch.DroppedFields) > 0 {
			res.Ignored = append(res.Ignored, gitImportNotice{
				Path: f.path,
				Reason: fmt.Sprintf("git configuration is not importable; ignored: %s",
					strings.Join(ch.DroppedFields, ", ")),
			})
		}
		// 🔴 Defence in depth on the most dangerous field set, at the most
		// dangerous moment. gitproj already strips these into DroppedFields and
		// planSettings restores the stored values over the top; this refuses
		// outright if either of those ever stops holding, because the failure
		// mode is a project silently publishing to somebody else's repository
		// with somebody else's credential.
		if err := gitBootstrapCheckNotImportable(*ch); err != nil {
			res.Failures = append(res.Failures, gitImportFailure{Path: f.path, Reason: err.Error()})
			continue
		}
		steps, notices, err := b.im.plan(ctx, in.Project, *ch, cw)
		if err != nil {
			res.Failures = append(res.Failures, gitImportFailure{Path: f.path, Reason: err.Error()})
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
		// Not one write, and no watermark: the whole folder is retried once the
		// bad file is fixed. A project that looks configured but is missing half
		// its workers is the outcome this branch exists to prevent.
		res.Quarantined = true
		res.Applied = nil
		res.Watermark = ""
		return res, nil
	}

	// (d) Apply, through the same store methods the console and the HTTP API
	// call. Nothing here has a privileged path of its own.
	for _, step := range plan {
		if err := step(ctx); err != nil {
			return res, fmt.Errorf("gitbootstrap: applying %s: %w", in.Project, err)
		}
	}

	// The watermark is the whole reason the NEXT import is a diff of nothing
	// rather than a re-import of everything. The caller persists it.
	res.Watermark = sha
	return res, nil
}

// gitBootstrapOrder ranks the projection kinds into the order a project should
// be wired up in: what everything reads first, then the workers, then the
// things that wake them, then the documents they read.
//
// Nothing in the store enforces this — a subscription naming an absent worker
// is a valid row, and the router simply finds nothing to wake — so this is not
// a correctness fix. It is so that the config log a human reads afterwards
// describes a project being built, not assembled backwards.
func gitBootstrapOrder(k gitproj.Kind) int {
	switch k {
	case gitproj.KindSettings:
		return 0
	case gitproj.KindWorker:
		return 1
	case gitproj.KindSkill:
		return 2
	case gitproj.KindImage:
		return 3 // ignored by the importer, but kept in a stable place
	case gitproj.KindSubscription:
		return 4
	case gitproj.KindSchedule:
		return 5
	case gitproj.KindMemory:
		return 6
	default:
		return 7
	}
}

// gitBootstrapCheckNotImportable refuses a change carrying any of the four
// git-configuration fields. See the file comment: this is the third and last
// line of a defence that has two earlier ones, kept because commit access must
// never become the power to redirect a project's projection at another
// repository and another credential (§D, DI3).
func gitBootstrapCheckNotImportable(ch gitproj.Change) error {
	for _, key := range gitproj.NotImportableFields() {
		if _, present := ch.Fields[key]; present {
			return fmt.Errorf("refusing to bootstrap %q: git configuration is not importable and must not reach a store write", key)
		}
	}
	return nil
}

// gitBootstrapRationale is the reason recorded against every config event a
// bootstrap writes. It names the source, because a year later the only question
// anyone asks of a row like this is "where did this come from" — and it says
// plainly what did not come with it, so nobody goes looking in the config log
// for a memory log that was never exported.
func gitBootstrapRationale(project string, repo *gitproj.Repo, subfolder, sha string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bootstrapped project %q from %s/ at commit %s", project, subfolder, sha)
	if remote := repo.Remote(); remote != "" {
		fmt.Fprintf(&b, " in %s", remote)
	}
	b.WriteString(".\n\n")
	b.WriteString("Every worker, skill, subscription, schedule, setting and named document " +
		"in this project was created from the files in that folder. The append-only memory " +
		"log is not part of a git export and was not restored: this project has its " +
		"configuration and its named documents, and no history.")
	return b.String()
}
