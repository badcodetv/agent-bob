package main

// gitwebhookwiring.go — the host half of the INBOUND door
// (design/2026-09-09-git-projection.md §C, ticket G20). httpapi owns the route
// itself (httpapi/gitwebhook.go, G12): the signature check, the body bound, the
// quiet 2xx for events we do not care about, and the poll loop's shape. What it
// deliberately does NOT own — because httpapi cannot import cmd/agentd without
// a cycle, and because httpapi reads no environment variables — is everything
// in this file:
//
//   - WHICH PROJECT a delivery belongs to, and WHICH SECRET verifies it
//     (ResolveGitWebhookProject).
//   - What "look now" actually does (TriggerImport).
//   - Where the poll interval comes from (AGENTKIT_GIT_WEBHOOK_POLL_INTERVAL).
//
// # The payload routes, and nothing else (§C)
//
// GitHub's JSON body is attacker-influenced input even when the signature
// verifies: a repository's own collaborators can push anything, and the body
// arrives before anything has been verified at all. So the two fields read from
// it — the repository's full name and clone URL — are used for ONE thing:
// choosing which project's secret to check the signature against. They never
// reach the import. What gets imported is the tree diff between this project's
// durable watermark and the REMOTE TIP, read out of the repository itself.
//
// # No oracle
//
// A repository no project claims resolves to nothing and the route answers 404
// having done no work. In particular we never fall back to "try every project's
// secret", which would turn one leaked webhook secret into a test for every
// other project's. An AMBIGUOUS repository — two projects naming the same
// remote — resolves to nothing for the same reason: picking one would decide,
// silently, whose secret is authoritative for a repository they share.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/httpapi"
)

// gitWebhookPollIntervalVar names the poll fallback's cadence. Webhook delivery
// is best-effort — GitHub does not retry a failed delivery, pushes coalesce,
// and ordering is not guaranteed — so a project whose delivery never arrives
// must still converge. Parsed with parseGCDuration (gc.go), so it takes a Go
// duration, refuses a bare number, and accepts "off" to disable the sweep.
const gitWebhookPollIntervalVar = "AGENTKIT_GIT_WEBHOOK_POLL_INTERVAL"

// gitWebhookStore is the slice of *agentdb.Store the wiring reads: the routing
// table (which project claims which repository, and which env var holds its
// secret) and the poll's project list.
type gitWebhookStore interface {
	ListGitProjectionTargets(ctx context.Context) ([]agentdb.GitProjectionTarget, error)
	ListProjectsWithGitRemote(ctx context.Context) ([]string, error)
}

// gitWebhookWiring implements httpapi's three seams —
// GitWebhookProjectResolver, GitWebhookImporter and GitWebhookProjectLister —
// against this process's store and projector.
type gitWebhookWiring struct {
	store    gitWebhookStore
	proj     *gitProjector
	importer *gitImporter
	getenv   func(string) string
	logf     func(string, ...any)
	// trigger is what TriggerImport actually does. It is a field, not a
	// method call, so a test can mount the real route — signature check and
	// all — without a clone, a projector or a remote behind it. Production
	// always sets it to runImport.
	trigger func(ctx context.Context, project string)
}

var (
	_ httpapi.GitWebhookProjectResolver = (*gitWebhookWiring)(nil)
	_ httpapi.GitWebhookImporter        = (*gitWebhookWiring)(nil)
	_ httpapi.GitWebhookProjectLister   = (*gitWebhookWiring)(nil)
)

// gitWebhookFullStore is what main.go holds: the routing reads above plus the
// writes the importer performs. *agentdb.Store satisfies it.
type gitWebhookFullStore interface {
	gitWebhookStore
	gitImportStore
}

// newGitWebhookWiring returns nil when the webhook cannot work — no product
// layer, or projection is off (no database, git missing, unusable clone root).
// A nil wiring mounts no route: an inbound door that can verify a signature but
// has nothing to hand the delivery to would answer 202 and do nothing, which is
// worse than a 404.
func newGitWebhookWiring(store gitWebhookFullStore, proj *gitProjector, getenv func(string) string, logf func(string, ...any)) *gitWebhookWiring {
	if store == nil || proj == nil {
		return nil
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	w := &gitWebhookWiring{
		store:    store,
		proj:     proj,
		importer: newGitImporter(store),
		getenv:   getenv,
		logf:     logf,
	}
	w.trigger = w.runImport
	return w
}

// mount registers the route on the ROOT mux, outside apiAuthMiddleware, the way
// main.go mounts the core MCP server. GitHub holds no console JWT and no
// project API key; this route authenticates itself, per delivery, by verifying
// an HMAC-SHA256 signature over the raw body against the project's secret.
//
// The pattern is registered WITHOUT httpapi's "POST " method prefix on purpose:
// with the method attached, a GET to the same path would fall through to "/"
// and be answered by the authenticated mux with a 401, hiding the handler's own
// 405. The handler already refuses every method but POST.
func (w *gitWebhookWiring) mount(root *http.ServeMux, pollCtx context.Context) error {
	pattern := gitWebhookPathOnly(httpapi.DefaultEndpoints.GitWebhook)
	root.Handle(pattern, httpapi.NewGitWebhookHandler(httpapi.GitWebhookConfig{
		Resolver: w,
		Importer: w,
	}))

	interval, err := parseGCDuration(gitWebhookPollIntervalVar, w.getenv(gitWebhookPollIntervalVar), httpapi.DefaultGitWebhookPollInterval)
	if err != nil {
		return err
	}
	if interval > 0 {
		go httpapi.RunGitWebhookPoll(pollCtx, httpapi.GitWebhookPollConfig{
			Lister:   w,
			Importer: w,
			Interval: interval,
			Logf:     w.logf,
		})
		w.logf("[agentd] git webhook: POST %s (unauthenticated; each delivery is HMAC-verified), poll every %s", pattern, interval)
	} else {
		w.logf("[agentd] git webhook: POST %s (unauthenticated; each delivery is HMAC-verified), poll DISABLED", pattern)
	}
	return nil
}

// gitWebhookPathOnly strips the leading method from an Endpoints pattern.
func gitWebhookPathOnly(pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return pattern[i+1:]
	}
	return pattern
}

// ProjectsWithGitRemote is the poll fallback's list: every project that
// projects at all.
// ProjectsWithGitRemote satisfies httpapi.GitWebhookProjectLister; the store
// spells it ListProjectsWithGitRemote (its read-verb prefix is load-bearing —
// see the method's own comment).
func (w *gitWebhookWiring) ProjectsWithGitRemote(ctx context.Context) ([]string, error) {
	return w.store.ListProjectsWithGitRemote(ctx)
}

// ResolveGitWebhookProject matches the delivery's repository identity against
// ProjectSettings.GitRemote and returns the project plus the secret to verify
// with. See the file doc: routing only, no oracle, no ambiguity.
func (w *gitWebhookWiring) ResolveGitWebhookProject(ctx context.Context, repoFullName, cloneURL string) (string, []byte, bool) {
	targets, err := w.store.ListGitProjectionTargets(ctx)
	if err != nil {
		w.logf("[agentd] git webhook: listing projection targets: %v", err)
		return "", nil, false
	}

	wantHost, wantPath := gitRepoIdentity(cloneURL)
	_, wantName := gitRepoIdentity(repoFullName)

	var exact, byName []agentdb.GitProjectionTarget
	for _, t := range targets {
		host, path := gitRepoIdentity(t.GitRemote)
		if path == "" {
			continue
		}
		switch {
		case wantPath != "" && path == wantPath && (wantHost == "" || host == "" || host == wantHost):
			exact = append(exact, t)
		case wantName != "" && path == wantName:
			// The payload gave us "owner/repo" with no host. Weaker, and only
			// consulted when the clone URL matched nothing, because two hosts
			// can carry the same owner/repo pair.
			byName = append(byName, t)
		}
	}

	match := exact
	if len(match) == 0 {
		match = byName
	}
	switch len(match) {
	case 0:
		return "", nil, false
	case 1:
	default:
		names := make([]string, 0, len(match))
		for _, t := range match {
			names = append(names, t.Project)
		}
		w.logf("[agentd] git webhook: %q is claimed by more than one project (%s) — refusing the delivery; give them distinct git_remote values",
			repoFullName, strings.Join(names, ", "))
		return "", nil, false
	}

	t := match[0]
	envName := strings.TrimSpace(t.GitWebhookSecretEnv)
	if envName == "" {
		w.logf("[agentd] git webhook: project %s has no git_webhook_secret_env — nothing can verify a delivery for it", t.Project)
		return "", nil, false
	}
	secret := w.getenv(envName)
	if secret == "" {
		w.logf("[agentd] git webhook: project %s names %s as its webhook secret but that variable is empty in agentd's environment", t.Project, envName)
		return "", nil, false
	}
	return t.Project, []byte(secret), true
}

// gitRepoIdentity reduces a repository reference to (host, owner/repo),
// lowercased, so the several spellings of the same repository compare equal:
//
//	https://github.com/badcode/wolf.git   → github.com, badcode/wolf
//	https://x-token@github.com/badcode/wolf → github.com, badcode/wolf
//	git@github.com:badcode/wolf.git       → github.com, badcode/wolf
//	badcode/wolf                          → "",         badcode/wolf
//
// Anything it cannot make sense of comes back with an empty path, which
// matches nothing — the safe direction for a routing function whose only
// failure mode that matters is matching the WRONG project.
func gitRepoIdentity(raw string) (host, path string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return "", ""
		}
		host, path = u.Hostname(), u.Path
	case strings.Contains(s, "@") && strings.Contains(s, ":"):
		// scp-like: [user@]host:path
		at := strings.LastIndex(s, "@")
		rest := s[at+1:]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			return "", ""
		}
		host, path = rest[:colon], rest[colon+1:]
	default:
		path = s
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	return strings.ToLower(host), strings.ToLower(strings.Trim(path, "/"))
}

// TriggerImport is the "look now" hint — from a verified delivery, or from the
// poll's periodic sweep. Both call it on exactly the same footing, and neither
// tells it anything about what changed: it reads the repository.
//
// It is cheap when nothing has moved (a fetch and a tip comparison), which is
// what makes it safe to fire on every project every poll interval.
func (w *gitWebhookWiring) TriggerImport(ctx context.Context, project string) {
	if w.trigger == nil {
		return
	}
	w.trigger(ctx, project)
}

func (w *gitWebhookWiring) runImport(ctx context.Context, project string) {
	if err := w.importProject(ctx, project); err != nil {
		w.logf("[agentd] git import: %s: %v", project, err)
		_ = w.proj.cfg.State.NoteFailureKind(ctx, project, gitProjectionErrorKind(err), err.Error())
	}
}

func (w *gitWebhookWiring) importProject(ctx context.Context, project string) error {
	ps, err := w.proj.cfg.Store.GetProjectSettings(ctx, project)
	if err != nil {
		return fmt.Errorf("read project settings: %w", err)
	}
	if ps == nil || strings.TrimSpace(ps.GitRemote) == "" {
		return nil // projection is off for this project — nothing to import
	}

	// The same lease as the render and push loops, for the same reason: one
	// writer per clone (§F). Re-entrant for this owner, so an import can run
	// while this process's own render loop holds it; another process holding
	// it means the work is theirs, and skipping is correct rather than an
	// error.
	deadline := time.Now().Add(w.proj.cfg.LeaseTTL).Unix()
	ok, err := w.proj.cfg.State.AcquireLease(ctx, project, w.proj.cfg.Owner, deadline)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer func() { _ = w.proj.cfg.State.ReleaseLease(context.WithoutCancel(ctx), project, w.proj.cfg.Owner) }()

	// One uninterrupted sequence per clone, exactly as RenderProject does: a
	// fetch and a render must not interleave in one working tree.
	work := w.proj.workLock(project)
	work.Lock()
	defer work.Unlock()

	repo, err := w.proj.ensureRepo(ctx, project, ps)
	if err != nil {
		return err
	}

	state, err := w.proj.cfg.State.Get(ctx, project)
	if err != nil {
		return err
	}
	from := state.LastImportedSHA
	if from == "" {
		// Never imported. Start from our own last published commit rather than
		// from the beginning of history: everything up to it is the renderer's
		// own output, which ImportRange would skip anyway (its Orange-Seq
		// check), and starting there keeps the first import proportional to
		// what a human actually pushed. With no published commit either, "" is
		// correct — it means "the whole tree at the tip", which is the
		// bootstrap case.
		from = state.LastPushedSHA
	}

	res, err := w.importer.Import(ctx, gitImportInput{
		Project:   project,
		Repo:      repo,
		Subfolder: gitSubfolderOf(ps),
		FromSHA:   from,
	})
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}

	if res.Quarantined {
		// NOTHING was written, and the watermark deliberately did not move: a
		// quarantined push must be retried once the human fixes the file, not
		// skipped. Recorded so the console can show why — per file (G23), not
		// only as one summary sentence, because "your push was rejected" with
		// no file and no reason is not something anybody can act on.
		reasons := make([]string, 0, len(res.Failures))
		for _, f := range res.Failures {
			reasons = append(reasons, f.Path+": "+f.Reason)
		}
		w.logf("[agentd] git import: %s quarantined %s — nothing was applied: %s",
			project, shortSHA(res.ToSHA), strings.Join(reasons, "; "))
		w.putNotes(ctx, project, agentdb.GitProjectionNoteQuarantine, gitImportQuarantineNotes(res.Failures))
		return w.proj.cfg.State.NoteFailureKind(ctx, project,
			agentdb.GitProjectionErrorQuarantined,
			"inbound commit quarantined: "+strings.Join(reasons, "; "))
	}

	// A clean run. Both lists are written EVEN WHEN EMPTY: an empty quarantine
	// is how the previous one is cleared, and a red banner that outlives the
	// push that fixed it teaches an operator to ignore the banner.
	// The notes are gone; the row's own error must go too, or the console
	// keeps reporting a file this run has just fixed. Narrow on purpose: it
	// clears a quarantine and cannot touch a push failure (see the store).
	w.putNotes(ctx, project, agentdb.GitProjectionNoteQuarantine, nil)
	if err := w.proj.cfg.State.ClearQuarantine(ctx, project); err != nil {
		w.logf("gitimport: clear quarantine for %s: %v", project, err)
	}

	w.putNotes(ctx, project, agentdb.GitProjectionNoteIgnored, gitImportIgnoredNotes(res.Ignored))

	if err := w.proj.cfg.State.MarkImported(ctx, project, res.Watermark); err != nil {
		return fmt.Errorf("advance the import watermark: %w", err)
	}
	if len(res.Applied) > 0 || len(res.Ignored) > 0 {
		w.logf("[agentd] git import: %s applied %d change(s) up to %s (%d ignored, %d of ours skipped)",
			project, len(res.Applied), shortSHA(res.Watermark), len(res.Ignored), res.OursSkipped)
	}
	return nil
}

// putNotes records one kind of note, best effort.
//
// 🔴 Deliberately not fatal. These are diagnostics ABOUT a run; failing the run
// because we could not write a note about it would turn a display problem into
// a projection outage, and on the quarantine path it would also lose the
// summary that NoteFailure is about to write. It is logged, because a store
// that cannot take a note is a real symptom of something else.
func (w *gitWebhookWiring) putNotes(ctx context.Context, project, kind string, notes []agentdb.GitProjectionNote) {
	if err := w.proj.cfg.State.PutNotes(ctx, project, kind, notes); err != nil {
		w.logf("[agentd] git import: %s: record %s notes: %v", project, kind, err)
	}
}

// gitImportQuarantineNotes and gitImportIgnoredNotes convert one run's in-memory
// lists into rows. The timestamp is stamped once for the whole run, so every
// note from one push shares an age and the newest-kept cap orders by run rather
// than by however long each file took to parse.
func gitImportQuarantineNotes(failures []gitImportFailure) []agentdb.GitProjectionNote {
	if len(failures) == 0 {
		return nil
	}
	at := time.Now().Unix()
	out := make([]agentdb.GitProjectionNote, 0, len(failures))
	for _, f := range failures {
		out = append(out, agentdb.GitProjectionNote{Path: f.Path, Reason: f.Reason, NotedAt: at})
	}
	return out
}

func gitImportIgnoredNotes(notices []gitImportNotice) []agentdb.GitProjectionNote {
	if len(notices) == 0 {
		return nil
	}
	at := time.Now().Unix()
	out := make([]agentdb.GitProjectionNote, 0, len(notices))
	for _, n := range notices {
		out = append(out, agentdb.GitProjectionNote{Path: n.Path, Reason: n.Reason, NotedAt: at})
	}
	return out
}
