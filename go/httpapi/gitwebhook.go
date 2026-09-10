package httpapi

// The INBOUND door's front step (design/2026-09-09-git-projection.md §C,
// ticket G12): the HTTP route GitHub calls, and a background poll that gives
// every project's importer a periodic nudge even when no delivery ever
// arrives. This file owns exactly that — it does NOT own the importer
// (gitimport.go, package main, G11) or the projection worker that actually
// fetches and applies commits (G8). Those sit on the other side of the
// GitWebhookImporter seam below, because cmd/agentd imports this package and
// a reverse import would cycle.
//
// # Auth model — outside the JWT middleware, self-authenticating
//
// GitHub cannot hold a console JWT or a project API key, so this route
// cannot live behind apiAuthMiddleware the way every route on Mux() does. It
// is mounted the same way cmd/agentd/main.go mounts the core MCP server
// (mcpserver.go) — directly on the host's root mux, never through
// h.Mux()/apiMux — and authenticates itself: GitHub signs every delivery
// with HMAC-SHA256 over the raw request body, keyed by a secret configured
// per project (the X-Hub-Signature-256 header). Verification uses
// hmac.Equal, never a string/[]byte == compare: a timing-variable comparison
// on a signature is a real vulnerability, not a style nit.
//
// # The webhook is a HINT, not a transaction (§C)
//
// GitHub's payload names a repository, a ref and a list of commits. NONE of
// that carries any authority over what gets imported — the actual importer
// diffs the TREE between its durable watermark and the remote's real tip,
// read straight from the repository, never from this payload (a webhook
// body is attacker-influenced input). This file uses the payload for
// exactly one thing — routing the delivery to a project, via Resolver — and
// forgets everything else about it the instant TriggerImport is called with
// nothing but a project name.
//
// # Async, not synchronous
//
// GitHub times a delivery out and retries non-2xx responses; holding the
// connection open for a clone-and-import is how you get duplicate
// deliveries. The handler verifies, hands off to Importer.TriggerImport on a
// detached goroutine, and answers immediately.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	// gitHubSignatureHeader carries "sha256=<hex hmac>" (GitHub's current
	// signature scheme; the legacy X-Hub-Signature/sha1 header is ignored).
	gitHubSignatureHeader = "X-Hub-Signature-256"
	// gitHubEventHeader names the GitHub event kind ("push", "ping", ...).
	gitHubEventHeader = "X-GitHub-Event"

	// DefaultGitWebhookMaxBodyBytes bounds one delivery. The route reads only
	// the payload's repository identity (rule above) — it never needs
	// GitHub's full commit list — so the bound is deliberately far below
	// GitHub's own ~25MB delivery ceiling. An unauthenticated route that
	// buffers an unbounded body before doing anything else is a
	// denial-of-service.
	DefaultGitWebhookMaxBodyBytes = 1 << 20 // 1 MiB

	// DefaultGitWebhookPollInterval is the poll fallback's cadence when the
	// host does not override it. Webhook delivery is best-effort — GitHub
	// does not auto-retry a failed delivery, pushes coalesce, and order is
	// not guaranteed (design §C) — so a project whose webhook never arrives
	// must still converge.
	//
	// House style (see cmd/agentd/gc.go's AGENTKIT_SESSION_IDLE_TIMEOUT /
	// AGENTKIT_SNAPSHOT_REAP_INTERVAL): the HOST reads an env var — suggested
	// AGENTKIT_GIT_WEBHOOK_POLL_INTERVAL, parsed with time.ParseDuration —
	// and passes the result as GitWebhookPollConfig.Interval. This package
	// does not read environment variables itself; httpapi never has (grep
	// confirms it — only *_test.go files touch os.Getenv, and only for
	// AGENTKIT_TEST_POSTGRES_URL).
	DefaultGitWebhookPollInterval = 5 * time.Minute
)

// GitWebhookProjectResolver identifies which project a delivery is for and
// hands back the secret to verify it with.
//
// repoFullName and cloneURL come straight off the UNVERIFIED JSON payload —
// used here for ROUTING ONLY, never as authority over what is imported (see
// the file doc's "hint, not a transaction"). A real implementation matches
// them against agentdb.ProjectSettings.GitRemote and reads the secret from
// the environment variable a settings/project-map field NAMES — never a
// literal secret stored in the row — the same api_key_env / github_token_env
// shape as cmd/agentd/googleauth.go's projectConfig.
//
// ok=false means no project claims this repository. The caller refuses
// cleanly (404) and does no work — in particular, it never falls back to
// "verify against every project's secret", which would turn one leaked
// webhook secret into an oracle for every other project's.
type GitWebhookProjectResolver interface {
	ResolveGitWebhookProject(ctx context.Context, repoFullName, cloneURL string) (project string, secret []byte, ok bool)
}

// GitWebhookImporter is the seam to the real importer (gitimport.go, and the
// projection worker that owns the durable watermark — both package main).
// httpapi cannot import cmd/agentd — main.go imports httpapi, so the reverse
// would cycle — so whoever wires main.go supplies this.
//
// TriggerImport is a HINT ("look now"), never itself the import: it must
// return quickly (the webhook handler calls it from a detached goroutine,
// but the poll loop below calls it inline, in sequence, for every project it
// sweeps). Implementations are expected to be cheap when nothing has moved —
// a fetch and a tip comparison, not a full import — since both the webhook
// and the poll fallback call it on exactly the same footing and the poll
// fires on every configured project every interval.
type GitWebhookImporter interface {
	TriggerImport(ctx context.Context, project string)
}

// GitWebhookProjectLister names the projects the poll fallback should nudge —
// every project with git projection configured (a non-empty GitRemote).
type GitWebhookProjectLister interface {
	ProjectsWithGitRemote(ctx context.Context) ([]string, error)
}

// GitWebhookConfig wires NewGitWebhookHandler.
type GitWebhookConfig struct {
	// Resolver and Importer are required; NewGitWebhookHandler panics without
	// them — a webhook route that cannot identify a project or cannot tell
	// anyone to look is not a route, it's a stub.
	Resolver GitWebhookProjectResolver
	Importer GitWebhookImporter
	// MaxBodyBytes overrides DefaultGitWebhookMaxBodyBytes when positive.
	MaxBodyBytes int64
}

func (cfg GitWebhookConfig) maxBody() int64 {
	if cfg.MaxBodyBytes > 0 {
		return cfg.MaxBodyBytes
	}
	return DefaultGitWebhookMaxBodyBytes
}

// NewGitWebhookHandler builds the route. It is a bare http.Handler,
// deliberately not a method on Handlers and not registered by Mux(): see
// Endpoints.GitWebhook's comment for why (it must be mounted outside
// apiAuthMiddleware, on the host's root mux, the same way
// cmd/agentd/main.go mounts the core MCP server).
func NewGitWebhookHandler(cfg GitWebhookConfig) http.Handler {
	if cfg.Resolver == nil || cfg.Importer == nil {
		panic("httpapi: NewGitWebhookHandler requires a Resolver and an Importer")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// A missing signature is refused outright, before the body is even
		// looked at — this is a header check, not "work".
		sigHeader := strings.TrimSpace(r.Header.Get(gitHubSignatureHeader))
		if sigHeader == "" {
			w.Header().Set("WWW-Authenticate", "hmac")
			http.Error(w, "missing "+gitHubSignatureHeader, http.StatusUnauthorized)
			return
		}

		// THE SIZE LIMIT COMES BEFORE THE BODY IS READ OR HASHED. An
		// unauthenticated route that buffers an unbounded body first is a
		// denial-of-service — this must not move below the signature check
		// above (which never touches the body) nor below the JSON parse and
		// HMAC compute that follow.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, cfg.maxBody()))
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}

		// Routing only (see GitWebhookProjectResolver's doc): a decode
		// failure just means the fields below stay zero and Resolver reports
		// ok=false, not a crash.
		var payload struct {
			Repository struct {
				FullName string `json:"full_name"`
				CloneURL string `json:"clone_url"`
			} `json:"repository"`
		}
		_ = json.Unmarshal(body, &payload)

		project, secret, ok := cfg.Resolver.ResolveGitWebhookProject(r.Context(),
			payload.Repository.FullName, payload.Repository.CloneURL)
		if !ok {
			http.Error(w, "no project is configured for this repository", http.StatusNotFound)
			return
		}

		if !verifyGitHubSignature(secret, body, sigHeader) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		// Unexpected event types (ping, star, ...) are accepted quietly:
		// GitHub retries on anything but 2xx, and a route that answers
		// non-2xx to events it simply doesn't care about gets hammered.
		// Reached only for a request whose signature just verified, so this
		// costs nothing security-wise.
		if r.Header.Get(gitHubEventHeader) != "push" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Hand off and answer immediately (see file doc: async, not
		// synchronous). context.Background() is deliberate: r.Context() is
		// cancelled the instant ServeHTTP returns, and the import must
		// outlive this request.
		go cfg.Importer.TriggerImport(context.Background(), project)

		w.WriteHeader(http.StatusAccepted)
	})
}

// verifyGitHubSignature checks "sha256=<hex>" against
// HMAC-SHA256(secret, body), via hmac.Equal — never a string or []byte ==
// compare, which leaks how many leading bytes matched through timing.
func verifyGitHubSignature(secret, body []byte, header string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)
	return hmac.Equal(got, want)
}

// GitWebhookPollConfig configures the poll fallback (design §C: "webhook
// delivery is best-effort ... a project whose webhook never arrives must
// still converge").
type GitWebhookPollConfig struct {
	// Lister and Importer are required; RunGitWebhookPoll panics without them.
	Lister   GitWebhookProjectLister
	Importer GitWebhookImporter
	// Interval between sweeps. <=0 uses DefaultGitWebhookPollInterval.
	Interval time.Duration
	// Logf reports a listing failure; defaults to log.Printf. A sweep that
	// cannot list projects skips this tick rather than stopping the loop —
	// the next webhook, or the next tick, tries again.
	Logf func(format string, args ...any)
}

// RunGitWebhookPoll runs the fallback until ctx is done. It is cheap by
// construction: every tick it does nothing but call TriggerImport once per
// project — the exact same hint the webhook sends — and leaves the actual
// "fetch and compare the tip" work, the part that must stay cheap when
// nothing moved, to whatever implements GitWebhookImporter (the projection
// worker, G8). Callers typically run this in its own goroutine.
func RunGitWebhookPoll(ctx context.Context, cfg GitWebhookPollConfig) {
	if cfg.Lister == nil || cfg.Importer == nil {
		panic("httpapi: RunGitWebhookPoll requires a Lister and an Importer")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultGitWebhookPollInterval
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			gitWebhookPollOnce(ctx, cfg, logf)
		}
	}
}

func gitWebhookPollOnce(ctx context.Context, cfg GitWebhookPollConfig, logf func(string, ...any)) {
	projects, err := cfg.Lister.ProjectsWithGitRemote(ctx)
	if err != nil {
		logf("[git-webhook] poll: listing projects with git remotes: %v", err)
		return
	}
	for _, p := range projects {
		cfg.Importer.TriggerImport(ctx, p)
	}
}
