package httpapi

// gitbootstraproute.go — POST /agent/git-bootstrap, the entry point "project as
// code" never had (design/2026-09-09-git-projection.md §C, ticket G26, DI20).
//
// cmd/agentd/gitbootstrap.go has been built, tested and proven by a
// render→bootstrap→fold-equal round trip since G15, and until this file
// **nothing anywhere called it**: no route, no CLI, no console action. The
// payoff of the whole projection branch — point a project at a folder someone
// exported and get a working project out of it — existed only in tests.
//
// # What this file owns, and what it deliberately does not
//
// It owns the DOOR: the tenancy rule, the refusal shapes, the wire types. It
// owns none of the work. The bootstrap needs a clone, a lease, a projector and
// a store, all of which live in package main — and main imports this package,
// so the reverse would cycle. So the work sits behind the GitBootstrapper seam
// below, exactly as GitWebhookImporter does for the inbound door, and
// cmd/agentd/gitbootstrapwiring.go implements it.
//
// # Tenancy: the project is the `customer` claim, and nothing else
//
// There is no path parameter, no query parameter and no body field naming a
// project — the same rule GET /agent/git-projection follows. A request body is
// not read at all, so there is nothing in one for a caller to point somewhere
// else. A session-scoped embed token (Identity.SessionScope) gets a 404: a
// credential minted for one session inside somebody else's page has no business
// recreating that project's entire configuration from a folder.
//
// # 🔴 Bootstrap creates a project FROM a folder. It never merges one INTO a
// live project.
//
// A project that already has workers, skills, subscriptions or schedules is
// refused with 409, naming what was found. This is not conservatism about
// overwriting: the two operations are different in kind. Bootstrapping an empty
// project is recoverable — you delete it and try again. Merging a foreign
// folder into a live project rewrites prompts an architect wrote, wakes
// subscriptions nobody reviewed, and cannot be undone in one act, because every
// write lands as its own config event. An operator who meant the first and got
// the second would never get back. So the door refuses, and says what to do
// instead: push the folder to the project's own remote and let the ordinary
// import apply it, file by file, with a changelog entry each.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// GitBootstrapEndpoint is the default route path.
const GitBootstrapEndpoint = "POST /agent/git-bootstrap"

// GitBootstrapWrite is one store write a bootstrap performed, in the order it
// ran. It mirrors cmd/agentd's gitImportWrite so a console can render a
// bootstrap and an ordinary import with one component.
type GitBootstrapWrite struct {
	// Path is repo-relative, as the operator sees it in their editor.
	Path string `json:"path"`
	// Kind is the projection kind: worker, skill, settings, subscription,
	// schedule, document.
	Kind string `json:"kind"`
	// Name is the entity's name (or id, for subscriptions and schedules).
	Name string `json:"name"`
	// Action is "create", "update", "prompt" or "delete".
	Action string `json:"action"`
}

// GitBootstrapReport is the whole outcome of one run.
//
// 🔴 Ignored is the field this route exists to surface as much as Applied is.
// An operator who bootstraps a folder of twelve files and ends up with ten
// things has to be told why without reading agentd's logs — images are not
// importable, a deleted skill or memory file removes nothing (both are
// append-only), and the git-configuration fields are never taken from a folder
// (§D: a folder that could set them would redirect this project's projection,
// and its push credential, at a repository the folder's author controls).
type GitBootstrapReport struct {
	Project string `json:"project"`
	// SHA is the commit the folder was read at.
	SHA string `json:"sha"`
	// Watermark is what the project's last-imported SHA was set to. It equals
	// SHA on success and is EMPTY on a quarantine, so a rejected bootstrap is
	// retried in full rather than leaving a project that believes it already
	// imported the folder it refused.
	Watermark string `json:"watermark"`
	// Files counts the projection files found under the subfolder, including
	// ones that produced no write.
	Files int `json:"files"`

	Applied []GitBootstrapWrite `json:"applied"`
	Ignored []GitProjectionNote `json:"ignored"`

	// Quarantined is true when at least one file failed to parse or validate.
	// NOTHING was written in that case — a half-bootstrapped project is worse
	// than a failed one, because it looks configured — and the route answers
	// 422, not 200.
	Quarantined bool                `json:"quarantined"`
	Failures    []GitProjectionNote `json:"failures"`
}

// GitBootstrapper is the seam onto the real bootstrap (cmd/agentd:
// gitbootstrap.go does the work, gitbootstrapwiring.go supplies the clone, the
// lease and the store). httpapi cannot import cmd/agentd — main.go imports this
// package — so whoever wires main.go supplies this.
//
// The implementation is expected to take the SAME per-project lease and
// work-lock the render loop and the webhook import take: a bootstrap that ran
// beside a render of the same clone would be two writers in one working tree.
//
// It returns:
//   - *GitBootstrapReport with Quarantined=true when the folder did not parse
//     (an outcome, not an error: nothing was written and the operator needs the
//     per-file reasons);
//   - *GitBootstrapConflictError when the project already has configuration;
//   - ErrGitBootstrapNotConfigured when the project has no git_remote;
//   - ErrGitBootstrapBusy when another writer holds the project's lease;
//   - ErrGitBootstrapUnavailable when this deployment has no projector at all.
type GitBootstrapper interface {
	BootstrapProject(ctx context.Context, project string) (*GitBootstrapReport, error)
}

var (
	// ErrGitBootstrapUnavailable — this deployment cannot bootstrap: no
	// product layer, or the git projection never started. Answered 501, the
	// same way every other unwired seam is.
	ErrGitBootstrapUnavailable = errors.New("httpapi: git bootstrap is not available on this deployment")
	// ErrGitBootstrapNotConfigured — the project has no git_remote, so there is
	// no folder to bootstrap from. A 400 naming the settings field, never a
	// 500: this is an operator step that has not been done yet.
	ErrGitBootstrapNotConfigured = errors.New("httpapi: this project has no git_remote configured, so there is no folder to bootstrap from — set the repository on the project's settings first")
	// ErrGitBootstrapBusy — another process (or this one's render loop) holds
	// the project's projection lease. Retryable, and said so: 409 with a
	// sentence that tells the operator to try again rather than to change
	// anything.
	ErrGitBootstrapBusy = errors.New("httpapi: another writer is working on this project's repository right now — try again in a moment")
)

// GitBootstrapConflictError is the refusal at the centre of this route: the
// project already has configuration, so this would be a merge, not a bootstrap.
// Found names what was there, in an operator's words ("3 workers", "1
// schedule"), because "already configured" with nothing under it is not
// something anybody can act on.
type GitBootstrapConflictError struct {
	Found []string
}

func (e *GitBootstrapConflictError) Error() string {
	what := "existing configuration"
	if len(e.Found) > 0 {
		what = strings.Join(e.Found, ", ")
	}
	return fmt.Sprintf("this project already has configuration (%s). "+
		"Bootstrap CREATES a project from a folder and will not merge a folder into a live one — "+
		"that would rewrite prompts, wake subscriptions nobody reviewed, and could not be undone in one act. "+
		"To apply this folder to this project instead, push it to the project's own git remote and let the "+
		"ordinary import apply it file by file, with a changelog entry for each. "+
		"To bootstrap, point an EMPTY project at the repository.", what)
}

// GitBootstrap answers POST /agent/git-bootstrap for the calling project.
func (h *Handlers) GitBootstrap(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.GitBootstrap == nil {
		http.Error(w, ErrGitBootstrapUnavailable.Error(), http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "project scope required", http.StatusBadRequest)
		return
	}
	// An embed token is minted for one session inside somebody else's page. It
	// may not recreate a project's entire configuration from a folder. Same
	// refusal shape as the other project-wide routes: 404, never 403, which
	// would confirm the project exists.
	if id.SessionScope != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// The request body is never read. Every input this route has comes from the
	// claim and from the project's own settings row, so there is nothing in a
	// body to point at another project, another repository or another commit.
	res, err := h.cfg.GitBootstrap.BootstrapProject(r.Context(), id.Customer)
	if err != nil {
		writeGitBootstrapError(w, err)
		return
	}
	if res == nil {
		http.Error(w, "git bootstrap returned no result", http.StatusInternalServerError)
		return
	}
	if res.Applied == nil {
		res.Applied = []GitBootstrapWrite{}
	}
	if res.Ignored == nil {
		res.Ignored = []GitProjectionNote{}
	}
	if res.Failures == nil {
		res.Failures = []GitProjectionNote{}
	}

	status := http.StatusOK
	if res.Quarantined {
		// 422, with the full report as the body. The request was understood and
		// nothing was applied, so a 200 would tell an operator running curl
		// that their folder had been imported when it had not. The body is the
		// same shape either way, because the per-file reasons are the whole
		// point of answering at all.
		status = http.StatusUnprocessableEntity
	}
	writeJSONStatus(w, status, res)
}

func writeGitBootstrapError(w http.ResponseWriter, err error) {
	var conflict *GitBootstrapConflictError
	switch {
	case errors.As(err, &conflict):
		http.Error(w, conflict.Error(), http.StatusConflict)
	case errors.Is(err, ErrGitBootstrapBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrGitBootstrapNotConfigured):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrGitBootstrapUnavailable):
		http.Error(w, err.Error(), http.StatusNotImplemented)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// writeJSONStatus is writeJSON with a status code. The header must be written
// before the body, and after it nothing can change the status — which is why
// the encode error is swallowed here exactly as writeJSON swallows it.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
