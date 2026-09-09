package httpapi

// gitprojectionstatus.go — GET /agent/git-projection, the one read behind the
// console's git panel (design/2026-09-09-git-projection.md, ticket G16).
//
// The projection is a background loop. Everything it does that a human needs to
// know about, it does silently: it renders on a config-log hook, pushes on a
// timer, and imports what a human pushes back. Every failure mode below is one
// nobody would otherwise notice, which is the entire reason this route exists:
//
//   - **off** — an empty git_remote. A project that does not project must read
//     as a deliberate state, never as a broken one.
//   - **unrenderable** — a credential-bearing field holds a literal, so §D
//     refuses to write ANY tree. The repo silently stops moving. The response
//     names the field and never the value: gitproj.UnrenderableError carries no
//     value by construction (DI8), and this route does not go looking for one.
//   - **diverged** — gitproj.ErrNotFastForward. The one failure that never
//     resolves on its own: commits accumulate locally forever and only a human
//     reconciling the remote clears it.
//   - **push failing** — anything else that stops the push while rendering
//     keeps working. Commits pile up locally and the repo goes quietly stale;
//     agents notice nothing at all.
//   - **quarantined** — a human's push was rejected wholesale because a file
//     did not parse, so NOTHING in it was applied.
//   - **ignored** — edits a human made in git that had no effect (DI10): images
//     are not importable, and deleting a skill or a memory file removes nothing
//     because both are append-only. Silence here is the worst outcome on the
//     list — a human who edits those, sees nothing happen and is told nothing
//     concludes the whole system is broken.
//
// Tenancy: the project is ALWAYS Identity.Customer, never a path or query
// value, exactly as project_settings.go does it. There is no by-name form and
// no project parameter, so there is nothing for one project's token to point at
// another project with.
//
// 🔴 Two seams, on purpose. The *settings* half (is it on, which repo, which
// branch) comes from ProjectSettings, which Config auto-fills from AgentDB, so
// this route answers usefully on any Postgres deployment. The *state* half (the
// watermark, the SHAs, the last failure) lives in `git_projection_state`, a
// table cmd/agentd creates at boot with no agentdb accessor at all — so it
// arrives through GitProjection, which nothing auto-fills. Left nil the route
// still answers, with state_available=false and health "unknown". That is a
// deliberately honest partial answer: "projection is configured, I cannot see
// whether it is working" is a true and useful sentence, and a 501 that hid the
// repo link would not be.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/binocarlos/badcode-agent-orange/agentdb"
	"github.com/binocarlos/badcode-agent-orange/gitproj"
)

// GitProjectionEndpoint is the default route path.
const GitProjectionEndpoint = "GET /agent/git-projection"

// Health values. They are a closed set, and the browser branches on them, so
// they are named here once and mirrored in web/src/gitProjection.ts.
const (
	// GitProjectionOff — no remote configured. Deliberate, not broken.
	GitProjectionOff = "off"
	// GitProjectionUnknown — a remote is configured but no state store is
	// wired, so nothing can be said about whether it is working.
	GitProjectionUnknown = "unknown"
	// GitProjectionOK — the last render and the last push both succeeded.
	GitProjectionOK = "ok"
	// GitProjectionUnrenderable — a credential-bearing field holds a literal.
	GitProjectionUnrenderable = "unrenderable"
	// GitProjectionDiverged — the remote is not an ancestor of local HEAD.
	GitProjectionDiverged = "diverged"
	// GitProjectionPushFailing — rendering works, publishing does not.
	GitProjectionPushFailing = "push_failing"
	// GitProjectionQuarantined — an inbound push was rejected wholesale.
	GitProjectionQuarantined = "quarantined"
	// GitProjectionFailing — a failure that is none of the above.
	GitProjectionFailing = "failing"
)

// GitProjectionNote is one file a run had something to say about: a quarantine
// reason, or an edit that had no effect. `Path` is repo-relative.
type GitProjectionNote struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	At     int64  `json:"at,omitempty"`
}

// GitProjectionStore is the seam onto that state.
//
// *agentdb.Store satisfies it — the `git_projection_state` table and its
// accessors live there (migration 048) — so Config auto-fills it from AgentDB
// like every other metadata seam, and the route works on any Postgres
// deployment with no host wiring at all. Nil is still supported and does NOT
// 501: see the file header.
type GitProjectionStore interface {
	GetGitProjectionState(ctx context.Context, project string) (*agentdb.GitProjectionState, error)
}

// GitProjectionNotesStore is an OPTIONAL extension of GitProjectionStore, found
// by type assertion. A store that implements it reports the inbound half.
//
// 🔴 Nothing implements it today, and that is the honest state of the system,
// not an oversight in this route. gitImportResult carries `Failures` and
// `Ignored` in memory for exactly one run and no caller persists them, so an
// operator whose push was quarantined — or who deleted a skill file and watched
// nothing happen (DI10) — still learns it from nowhere. Closing that needs a
// table and a write on the import path, which belongs with the importer, not
// here. The contract carries the shape so the browser does not have to change
// when it lands.
type GitProjectionNotesStore interface {
	GitProjectionNotes(ctx context.Context, project string) (quarantine, ignored []GitProjectionNote, err error)
}

// gitProjectionStatusResp is the wire shape.
type gitProjectionStatusResp struct {
	Project string `json:"project"`
	// Enabled is false exactly when git_remote is empty.
	Enabled bool `json:"enabled"`
	// Remote is git_remote with any embedded userinfo removed. The settings
	// route already returns it verbatim, so this is not a containment boundary
	// — it is so a console that renders this field cannot paint a credential
	// onto a screen somebody screenshots.
	Remote string `json:"remote"`
	// Branch and Subfolder are EFFECTIVE values: the engine stores them empty
	// and applies its defaults at read time (gitSubfolderOf / gitBranchOf), so
	// showing the raw column would tell an operator the wrong thing.
	Branch    string `json:"branch"`
	Subfolder string `json:"subfolder"`
	// TokenEnv is the NAME of the environment variable holding the push token.
	// Never a token.
	TokenEnv string `json:"token_env"`
	// BrowseURL is a clickable https link, or "" when the remote is not a shape
	// we can turn into one.
	BrowseURL string `json:"browse_url"`

	// StateAvailable is false when no state store is wired.
	StateAvailable bool `json:"state_available"`

	LastRenderedSeq int64  `json:"last_rendered_seq"`
	LastRenderedSHA string `json:"last_rendered_sha"`
	LastPushedSHA   string `json:"last_pushed_sha"`
	LastImportedSHA string `json:"last_imported_sha"`
	UpdatedAt       int64  `json:"updated_at"`

	Health      string `json:"health"`
	LastError   string `json:"last_error"`
	LastErrorAt int64  `json:"last_error_at"`
	// UnrenderableField is the dotted field path the refusal named, e.g.
	// "ProjectSettings.AttentionChannel.url". Present only for health
	// "unrenderable", and never accompanied by the offending value.
	UnrenderableField string `json:"unrenderable_field,omitempty"`
	// PushBehind is true when something is rendered locally that the remote has
	// not got. It is the "quietly stale" signal, and it is independent of
	// health: a push that has merely not run yet sets it with no error at all.
	PushBehind bool `json:"push_behind"`

	Quarantine []GitProjectionNote `json:"quarantine"`
	Ignored    []GitProjectionNote `json:"ignored"`
}

// GetGitProjectionStatus answers GET /agent/git-projection for the calling
// project.
func (h *Handlers) GetGitProjectionStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identify(w, r)
	if !ok {
		return
	}
	if h.cfg.ProjectSettings == nil {
		http.Error(w, "project settings not configured", http.StatusNotImplemented)
		return
	}
	if id.Customer == "" {
		http.Error(w, "project scope required", http.StatusBadRequest)
		return
	}
	// Identity.SessionScope is an embed token: a credential minted for one
	// session inside somebody else's page. It has no business reading how the
	// project publishes itself, or which environment variable holds its push
	// token. Same refusal shape the other project-wide reads use.
	if id.SessionScope != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	ps, err := h.cfg.ProjectSettings.GetProjectSettings(r.Context(), id.Customer)
	if err != nil {
		writeProjectSettingsError(w, err)
		return
	}

	resp := gitProjectionStatusResp{
		Project:    id.Customer,
		Quarantine: []GitProjectionNote{},
		Ignored:    []GitProjectionNote{},
		Health:     GitProjectionOff,
	}
	if ps != nil {
		resp.Remote = redactGitRemote(ps.GitRemote)
		resp.Enabled = strings.TrimSpace(ps.GitRemote) != ""
		resp.Branch = effectiveGitBranch(ps)
		resp.Subfolder = effectiveGitSubfolder(ps)
		resp.TokenEnv = ps.GitTokenEnv
	}
	if !resp.Enabled {
		// Off: no state is read at all. A project with no remote has no
		// projection to be healthy or unhealthy about, and reading a row for it
		// would only invite a console to render a stale watermark next to the
		// word "off".
		writeJSON(w, resp)
		return
	}
	resp.BrowseURL = gitBrowseURL(resp.Remote, resp.Branch, resp.Subfolder)

	if h.cfg.GitProjection == nil {
		resp.Health = GitProjectionUnknown
		writeJSON(w, resp)
		return
	}
	st, err := h.cfg.GitProjection.GetGitProjectionState(r.Context(), id.Customer)
	if errors.Is(err, ErrGitProjectionUnavailable) {
		resp.Health = GitProjectionUnknown
		writeJSON(w, resp)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if st == nil {
		// A project that has never rendered has no row. That is "configured,
		// nothing has happened yet" — a zero state, not an error.
		st = &agentdb.GitProjectionState{}
	}
	resp.StateAvailable = true
	resp.LastRenderedSeq = st.LastRenderedSeq
	resp.LastRenderedSHA = st.LastRenderedSHA
	resp.LastPushedSHA = st.LastPushedSHA
	resp.LastImportedSHA = st.LastImportedSHA
	resp.UpdatedAt = st.UpdatedAt
	resp.LastError = st.LastError
	resp.LastErrorAt = st.LastErrorAt
	// The lease is single-writer plumbing, not an operator fact, and is
	// deliberately not published: an operator cannot act on it and a lease
	// holder's identity is a host detail.
	if notes, ok := h.cfg.GitProjection.(GitProjectionNotesStore); ok {
		quarantine, ignored, err := notes.GitProjectionNotes(r.Context(), id.Customer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(quarantine) > 0 {
			resp.Quarantine = quarantine
		}
		if len(ignored) > 0 {
			resp.Ignored = ignored
		}
	}
	resp.PushBehind = st.LastRenderedSHA != "" && st.LastPushedSHA != st.LastRenderedSHA
	resp.Health = classifyGitProjection(st, resp.Quarantine, resp.PushBehind)
	if resp.Health == GitProjectionUnrenderable {
		resp.UnrenderableField = unrenderableField(st.LastError)
	}

	writeJSON(w, resp)
}

// classifyGitProjection turns the durable state into the one word the console
// leads with.
//
// 🔴 It classifies by matching the engine's own sentinel TEXT, because the
// state row stores `err.Error()` and nothing else — there is no code column. So
// the two sentinels are read out of gitproj rather than retyped, and
// TestGitProjectionStatusSentinelsMatchEngine fails if either changes wording.
// The right long-term fix is for the state row to carry a kind alongside the
// message; that belongs with the table's move into agentdb.
func classifyGitProjection(st *agentdb.GitProjectionState, quarantine []GitProjectionNote, pushBehind bool) string {
	if msg := strings.TrimSpace(st.LastError); msg != "" {
		switch {
		case strings.Contains(msg, gitUnrenderableMarker):
			return GitProjectionUnrenderable
		case strings.Contains(msg, gitproj.ErrNotFastForward.Error()):
			return GitProjectionDiverged
		case pushBehind:
			return GitProjectionPushFailing
		default:
			return GitProjectionFailing
		}
	}
	if len(quarantine) > 0 {
		return GitProjectionQuarantined
	}
	return GitProjectionOK
}

// gitUnrenderableMarker is the fixed half of UnrenderableError.Error()
// ("gitproj: %s.%s cannot be rendered: %s"). The wrapped sentinel's own text
// ("gitproj: field cannot be rendered") never appears in the formatted string,
// so matching on it would classify every refusal as an unknown failure.
const gitUnrenderableMarker = " cannot be rendered: "

// unrenderableFieldRe pulls the dotted path out of that message. A miss returns
// "" and the console falls back to the full sentence, which already names the
// field — this only lets it be shown as a field rather than as prose.
var unrenderableFieldRe = regexp.MustCompile(`gitproj: (\S+) cannot be rendered: `)

func unrenderableField(msg string) string {
	m := unrenderableFieldRe.FindStringSubmatch(msg)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

func effectiveGitBranch(ps *agentdb.ProjectSettings) string {
	if ps != nil && strings.TrimSpace(ps.GitBranch) != "" {
		return ps.GitBranch
	}
	return agentdb.DefaultGitBranch
}

func effectiveGitSubfolder(ps *agentdb.ProjectSettings) string {
	if ps != nil && strings.TrimSpace(ps.GitSubfolder) != "" {
		return ps.GitSubfolder
	}
	return agentdb.DefaultGitSubfolder
}

// redactGitRemote strips any `user:password@` from an https remote. The stored
// shape is supposed to be credential-free — the push token comes from
// git_token_env — but "supposed to" is not a guarantee about a text column a
// human types into, and this is the one field this route puts on a screen.
func redactGitRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	u, err := url.Parse(remote)
	if err != nil || u.User == nil {
		return remote
	}
	u.User = nil
	return u.String()
}

// gitBrowseURL turns a remote into a link a human can click.
//
// Deep links are GitHub-shaped ONLY (`/tree/<branch>/<subfolder>`), and github
// is the only forge this design targets — gitwebhook.go verifies a GitHub
// signature. Every other host gets the repository root, because guessing a
// forge's path grammar and landing an operator on a 404 while they are trying
// to diagnose a silent failure is worse than one extra click.
func gitBrowseURL(remote, branch, subfolder string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}

	var host, path string
	switch {
	case strings.HasPrefix(remote, "http://"), strings.HasPrefix(remote, "https://"):
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return ""
		}
		host, path = u.Host, u.Path
	case strings.HasPrefix(remote, "ssh://"):
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return ""
		}
		host, path = u.Hostname(), u.Path
	default:
		// scp-style: git@github.com:org/repo.git
		at := strings.LastIndex(remote, "@")
		colon := strings.Index(remote[at+1:], ":")
		if colon < 0 {
			return ""
		}
		host = remote[at+1 : at+1+colon]
		path = remote[at+1+colon+1:]
	}

	host = strings.TrimSuffix(host, "/")
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return ""
	}
	base := "https://" + host + "/" + path
	if strings.EqualFold(host, "github.com") && branch != "" && subfolder != "" {
		return base + "/tree/" + url.PathEscape(branch) + "/" + url.PathEscape(subfolder)
	}
	return base
}

// ErrGitProjectionUnavailable is what a host's GitProjectionStore returns when
// the state table is not there at all — a deployment where the projector never
// started. It is answered the same way a nil seam is (state_available=false,
// health "unknown") rather than as a 500, because "I cannot see the state" is a
// different sentence from "the database is broken" and an operator staring at a
// silent repo needs to be told which one it is.
var ErrGitProjectionUnavailable = errors.New("httpapi: git projection state is not available")
