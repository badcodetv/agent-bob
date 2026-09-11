// Command agentd — standalone agentkit host: Runner + httpapi + /health + /agent-proxy.
//
// agentd is the pre-built host for the standalone stack (docker-compose). Use it
// when you want a running agent API without writing a host. It uses the reference
// adapters (sqlitestore, devclaims) and a real DinD execution environment; the
// blob and image-registry backends are selected from env (filesystem +
// blob-archive by default, or GCS + Artifact Registry — see backends.go).
//
// # Quick start
//
//  1. Build the sandbox image and load it into DinD:
//
//     docker build -t agentkit-sandbox:dev agent-library/sandbox
//     docker save agentkit-sandbox:dev | docker -H tcp://localhost:2375 load
//
//  2. Run agentd (mock model proxy built-in when ANTHROPIC_API_KEY is unset):
//
//     DOCKER_HOST=tcp://localhost:2375 \
//     AGENTKIT_IMAGE=agentkit-sandbox:dev \
//     go run ./cmd/agentd
//
// The server listens on :8099 by default (ADDR env to override).
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentkit "github.com/badcodetv/agent-bob"
	"github.com/badcodetv/agent-bob/agentdb"
	"github.com/badcodetv/agent-bob/artifacts"
	dockerdind "github.com/badcodetv/agent-bob/execenv/docker"
	"github.com/badcodetv/agent-bob/extension"
	"github.com/badcodetv/agent-bob/extension/blobartifacts"
	"github.com/badcodetv/agent-bob/extension/dbartifacts"
	"github.com/badcodetv/agent-bob/extension/devclaims"
	"github.com/badcodetv/agent-bob/extension/embedding"
	"github.com/badcodetv/agent-bob/extension/sqlitestore"
	"github.com/badcodetv/agent-bob/fleet"
	"github.com/badcodetv/agent-bob/httpapi"
)

func main() {
	ctx := context.Background()

	// ── Data directory ───────────────────────────────────────────────────────────
	dataDir := envOr("AGENTKIT_DATA", "./.agentkit-data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("agentkit-server: mkdir %s: %v", dataDir, err)
	}

	// ── Session store ────────────────────────────────────────────────────────────
	// DATABASE_URL set → Postgres (agentdb.Store, self-migrating): one store for
	// the Runner AND the rich httpapi read paths (session listing, message
	// search, replayable query events). Unset → the legacy local SQLite store.
	var store agentkit.RunnerStore
	var agentDB *agentdb.Store
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		pg, err := agentdb.Open(dbURL)
		// Not a bare must(): gorm's own text names neither the `postgres`
		// service nor the once-initialised `pg-data` volume, which is what has
		// actually gone wrong nearly every time (dbconnect.go).
		must(databaseConnectError(dbURL, err))
		agentDB = pg
		store = pg
		log.Printf("[agentd] store=postgres")
	} else {
		dbPath := filepath.Join(dataDir, "sessions.db")
		s, err := sqlitestore.Open(dbPath)
		must(err)
		store = s
		log.Printf("[agentd] store=sqlite %s", dbPath)
	}

	// ── Default project budgets (onboarding-work-plan §1.3) ──────────────────────
	// A brand-new project (no project_settings row yet) starts braked at these
	// daily token budgets; an existing project is untouched. agentd is the only
	// caller of agentdb.SetDefaultBudgets — see defaultbudgets.go for why the
	// env read lives here and not in agentdb.
	defaultTokensSoft, defaultTokensHard, err := resolveDefaultBudgets(os.Getenv)
	must(err)
	must(agentdb.SetDefaultBudgets(defaultTokensSoft, defaultTokensHard))
	log.Printf("[agentd] default project budgets: daily_tokens_soft=%d daily_tokens_hard=%d (0=off)",
		defaultTokensSoft, defaultTokensHard)

	// ── Blob backend (shared by registry + artifact store) ───────────────────────
	// fs (default) or gcs — see backends.go. One BlobStore serves the artifact
	// bytes and (for the blob-archive registry) snapshot tarballs.
	blobCfg, err := resolveBlobConfig(os.Getenv, dataDir)
	must(err)

	// ── Embedding provider (memory semantic leg, §7.5) ───────────────────────────
	// AGENTKIT_EMBEDDING_BACKEND: none (default) | mock | openai. A nil provider
	// is a supported deployment, not a failure — memories store a NULL embedding
	// and search degrades to keyword+recency with the same result shape
	// (§7.6.5). A typo in the variable IS a failure, so it is a boot error
	// rather than a silent fall back to "none".
	embedder, err := embedding.NewFromEnv(os.Getenv)
	must(err)
	switch e := embedder.(type) {
	case nil:
		log.Printf("[agentd] embeddings=none — memory search is keyword+recency")
	case *embedding.OpenAI:
		// Name the MODEL, not just the backend. Vectors from two models are not
		// comparable and memories are embedded once, at create — so the model a
		// deployment ran with is a fact about its stored rows, and the boot log
		// is where someone will look for it later.
		log.Printf("[agentd] embeddings=openai model=%s dim=%d — memory search is hybrid (keyword + semantic)",
			e.Model(), embedding.Dim)
	default:
		log.Printf("[agentd] embeddings=%s", envOr("AGENTKIT_EMBEDDING_BACKEND", "none"))
	}
	// …and whether the database can actually hold a vector. Loud here, at boot,
	// rather than at the first search that silently came back keyword-only.
	if agentDB != nil {
		reportMemoryVectorColumn(ctx, agentDB.MemoryVectorColumn, embedder != nil, log.Printf)
	}
	blobs, closeBlobs, err := newBlobs(ctx, blobCfg)
	must(err)
	defer closeBlobs() //nolint:errcheck

	// ── Artifact store ───────────────────────────────────────────────────────────
	// Bytes always go to the BlobStore. The METADATA index is durable only on
	// Postgres (extension/dbartifacts → `agent_artifacts`): restart agentd and
	// the rows are still there.
	//
	// On the sqlite fallback there is no such table, so the index stays the
	// in-process map (extension/blobartifacts) it has always been — artifacts
	// written by this process are listable while it lives and are lost, bytes
	// orphaned in the bucket, when it exits. That is the pre-existing behaviour,
	// kept deliberately rather than failing to boot, but it is a real data-loss
	// mode: it is logged loudly here because nothing later in the run will
	// mention it. Compose always sets DATABASE_URL.
	var artStore artifacts.ArtifactStore
	if agentDB != nil {
		artStore = dbartifacts.New(agentDB, blobs)
		log.Printf("[agentd] artifacts=postgres+blob (metadata survives restart)")
	} else {
		artStore = blobartifacts.New(blobs)
		log.Printf("[agentd] artifacts=in-process index — NOT durable: " +
			"artifact metadata is lost on restart and its bytes orphaned. Set DATABASE_URL.")
	}

	// ── Claims issuer ────────────────────────────────────────────────────────────
	// Two secrets, and now genuinely two VALUES: jwtSecret verifies API callers
	// and may be empty (dev-open, so the demo UI works with no configuration),
	// while sessionSecret is what the Runner MINTS per-session tokens with, is
	// never empty, and is derived from — never equal to — jwtSecret. Both were
	// the same string in every real deployment until 2026-08-06, which made a
	// container's SESSION_TOKEN a valid project credential on every protected
	// route (doc 22 RD30). See sessionsecret.go for the derivation, the
	// AGENTKIT_SESSION_JWT_SECRET override and the migration path.
	//
	// The core MCP server verifies against sessionSecret — a session's memories
	// must not be reachable just because the API is open.
	jwtSecret := []byte(os.Getenv("AGENTKIT_JWT_SECRET")) // empty → dev-open
	sessionSecret, sessionSecretExplicit := resolveSessionSecret(os.Getenv)
	log.Printf("%s", sessionSecretNotice(jwtSecret, sessionSecret, sessionSecretExplicit))
	claims := devclaims.New(sessionSecret)

	// ── Public base URL (session permalinks) ─────────────────────────────────────
	// Where a human clicking a session link lands — the web UI's externally
	// reachable origin, NOT AGENTKIT_SELF_URL (that is a DinD bridge IP only
	// containers can reach). Everything that stamps provenance mints its
	// session_url from this. See permalink.go.
	permalinks, err := resolvePublicBaseURL(os.Getenv)
	must(err)
	log.Printf("[agentd] permalinks=%s/p/<project>/s/<session>", permalinks.BaseURL())

	// ── Docker host (shared by DinD + blobarchive) ───────────────────────────────
	dockerHost := envOr("DOCKER_HOST", "tcp://localhost:2375")

	// ── Image registry (blob-archive default, or ociregistry → Artifact Registry) ─
	regCfg, err := resolveRegistryConfig(os.Getenv)
	must(err)
	registry, err := newRegistry(ctx, dockerHost, blobs, regCfg)
	must(err)
	log.Printf("[agentd] blobs=%s registry=%s", blobCfg.backend, regCfg.backend)

	// ── Session port pool ────────────────────────────────────────────────────────
	// One live session leases one host port until it is deleted, so the size of
	// this range IS the concurrent-session ceiling for the host. Default
	// 30001-30100 (what agentd hardcoded before the seam existed); a nonsense
	// range is a boot error rather than a pool that fails every session. A test
	// stack sets a pool of three to reach the exhaustion path in seconds. See
	// portrange.go.
	pool, err := resolvePortRange(os.Getenv)
	must(err)
	log.Printf("[agentd] session port pool=%s (%d concurrent sessions max on this host; one live session holds one port until it is deleted or reclaimed for idleness)",
		pool, pool.size())

	// ── DinD execution environment ───────────────────────────────────────────────
	dindEnv, err := dockerdind.NewDinD(dockerdind.DinDConfig{
		DockerHost:     dockerHost,
		PortRangeStart: pool.start,
		PortRangeEnd:   pool.end,
		GatewayIP:      "172.17.0.1",
	})
	must(err)

	// ── Fleet (one-worker in-memory) ─────────────────────────────────────────────
	f := fleet.NewMemory(store, &fleet.MemFleetOptions{TrustedWorkload: true})
	err = f.Register(context.Background(), &fleet.Worker{
		ID:   "w1",
		Env:  dindEnv,
		Caps: dindEnv.Capabilities(),
	})
	must(err)

	// ── Session env (model-provider config the in-image agent requires) ──────────
	// selfURL is how a session container (nested in DinD) reaches agentd. With
	// agentd sharing DinD's network namespace, that is the bridge gateway IP.
	// Model auth by key presence: CLAUDE_CODE_OAUTH_TOKEN → subscription mode,
	// sessions talk to api.anthropic.com directly (the token wins when both are
	// set, so an attended dev stack bills the subscription and production blanks
	// the token — Anthropic's terms restrict subscription OAuth to attended use);
	// only ANTHROPIC_API_KEY → proxy path; neither → proxy path serving the mock.
	selfURL := envOr("AGENTKIT_SELF_URL", "http://172.17.0.1:8099")
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	oauthToken := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	subscriptionMode := oauthToken != ""
	sessionEnv := sandboxSessionEnv(selfURL)
	if subscriptionMode {
		sessionEnv = subscriptionSessionEnv(selfURL, oauthToken)
		log.Printf("[agentd] subscription mode (CLAUDE_CODE_OAUTH_TOKEN) → sessions call api.anthropic.com directly")
	}
	if apiKey != "" && oauthToken != "" {
		log.Printf("[agentd] both ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN set — the OAuth token wins (subscription mode; blank it to bill the API key)")
	}

	// ── MCP credential env (AGENTKIT_MCP_ENV allowlist) ──────────────────────────
	// Operator-designated variables travel agentd → session container, where the
	// sandbox resolves the ${VAR} references in MCP config (§4.4). Allowlist
	// only: agentd's own secrets (JWT, model keys) are never forwarded — and
	// naming one is a boot error. See mcpenv.go.
	mcpEnv, missingMCPEnv, err := resolveMCPEnv(os.Getenv)
	must(err)
	sessionEnv = applyMCPEnv(sessionEnv, mcpEnv)
	if len(mcpEnv) > 0 {
		log.Printf("[agentd] forwarding MCP credential env into sessions: %s", strings.Join(mcpEnvNames(mcpEnv), ","))
	}
	if len(missingMCPEnv) > 0 {
		log.Printf("[agentd] WARNING: %s names unset variable(s) %s — MCP servers referencing them will fail at spawn",
			mcpEnvVar, strings.Join(missingMCPEnv, ","))
	}

	// ── Session context (project settings + workers) ─────────────────────────────
	// The §5 defaults chain: worker beats project beats global, for base image,
	// system prompt and MCP config. Needs the product-layer tables, so it is
	// wired only on the Postgres store. See sessioncontext.go.
	var sessionCtx extension.SessionContextProvider
	baseImage := envOr("AGENTKIT_IMAGE", "agentkit-example:dev")
	if agentDB != nil {
		sessionCtx = newSessionContextProvider(agentDB, baseImage)
	}
	logSessionContextWiring(sessionCtx != nil)

	// ── Image pointer resolution (§13.3, I4) ─────────────────────────────────────
	// ONE resolver, handed to both the Runner (Deps.Images — the launch chain)
	// and the dispatcher (Images — composition step 1), so a worker job and a
	// chat with the same worker cannot launch from different environments. Like
	// everything else product-layer it needs Postgres; without it a worker's
	// `image` column is unreadable anyway, because the catalogue lives there.
	// Declared as the interface, never as the concrete type: a typed-nil
	// *catalogueImageResolver in an interface field is non-nil, and the whole
	// point of nil here is "this host has no catalogue" (see agentkit.Deps).
	var imageResolver agentkit.ImageResolver
	if agentDB != nil {
		imageResolver = newCatalogueImageResolver(agentDB, registry, log.Printf)
	}

	// ── Runner ───────────────────────────────────────────────────────────────────
	// The §8.2 internal emitters (worker.finished / worker.failed) append to the
	// event spine, which only the Postgres store has. Left nil on the sqlite
	// fallback: no event tables, so no events — assigning the typed-nil *Store
	// would hand the Runner a non-nil interface over a nil pointer.
	var workerEvents agentkit.WorkerEventStore
	if agentDB != nil {
		workerEvents = agentDB
	}

	// ── Session garbage collection ───────────────────────────────────────────────
	// Both of the Runner's background loops, off by default until this was wired:
	// idle containers were never reclaimed (so the host port pool only drained)
	// and expired snapshots were never retired. See gc.go for the numbers and
	// why. A nonsense duration is a boot error, not a loop that quietly never
	// runs.
	gc, err := resolveGCConfig(os.Getenv)
	must(err)
	// The B4 constraint, stated where it is relied on: the reaper tombstones
	// `agent_custom_images` — a config-guarded table — deliberately OUTSIDE the
	// config-event seam, because storage GC is not a configuration decision an
	// agent made. That is legal here precisely because agentd never arms
	// agentdb.InstallConfigEventGuard on this store (only tests do). Wire it only
	// on Postgres: the catalogue it sweeps has no sqlite equivalent. Declared as
	// the interface so the sqlite fallback hands the Runner a genuine nil, not a
	// non-nil interface wrapping a nil *Store.
	var snapshotCatalog agentkit.SnapshotCatalog
	if agentDB != nil {
		snapshotCatalog = agentDB
	}
	log.Printf("[agentd] %s", gc.describeIdleTimeout())
	log.Printf("[agentd] %s", gc.describeReapInterval(snapshotCatalog != nil))
	// The dataset version reaper + orphan blob sweep (O8) are product-layer
	// and Postgres-only, exactly like the snapshot catalogue above — hence the
	// same agentDB != nil wiredness. The loop itself is started further down,
	// beside the router/scheduler/attention sweep, once agentDB's non-nilness
	// is already being branched on for those.
	log.Printf("[agentd] %s", gc.describeDatasetReap(agentDB != nil))

	// The per-write dataset byte cap (O6b). Read ONCE here rather than from
	// inside the dataset_put handler — a tool must never read the environment —
	// and a nonsense value is a boot error naming the variable rather than a
	// silent fallback to the default an operator did not choose. Parsed
	// unconditionally, even on the sqlite fallback where the tools are never
	// mounted, so a typo is reported on the host that has it and not only on the
	// one that happens to run Postgres.
	datasetMaxBytes, err := parseDatasetMaxBytes(os.Getenv(datasetMaxBytesVar))
	must(err)

	runner, err := agentkit.NewRunner(agentkit.Deps{
		Fleet:          f,
		Registry:       registry,
		Store:          store,
		Artifacts:      artStore,
		Claims:         claims,
		SessionContext: sessionCtx,
		WorkerEvents:   workerEvents,
		// The §13 pointer at the front of the launch chain (§13.5, §13.6): a
		// session whose resolved context carries a worker image resolves it
		// here, and a failure fails the launch rather than substituting the
		// base image.
		Images: imageResolver,
		// The §13.7 / B4 image catalogue the snapshot TTL reaper sweeps.
		Snapshots: snapshotCatalog,
		Policy: agentkit.Policy{
			BaseImage: baseImage,
			AgentPort: 3010,
			// Reclaim the container (and its host port) of a session nobody has
			// spoken to for this long. NOT a delete: the row survives and the
			// next message restores it from its snapshot.
			ArchiveTimeout:             gc.idleTimeout,
			SnapshotReapInterval:       gc.reapInterval,
			SessionEnv:                 sessionEnv,
			DisableModelAPIKeyOverride: subscriptionMode,
		},
	})
	must(err)
	must(runner.Start(context.Background()))
	defer runner.Close() //nolint:errcheck

	// ── HTTP API ─────────────────────────────────────────────────────────────────
	// The core tool server a session created over HTTP is told about — the same
	// value the dispatcher hands every composed job below, so a chat session and
	// a worker job reach memory_*, worker_*, image_* through one identical
	// config. Set ONLY when the server is actually mounted (the `agentDB != nil`
	// blocks further down): on the SQLite fallback the /mcp endpoint does not
	// exist, and pointing a container at it would trade "no core tools" for "core
	// tools that 404".
	var coreMCP agentdb.MCPServers
	if agentDB != nil {
		coreMCP = coreMCPServers(selfURL)
	}
	// The bootstrap seam (G26): create a project's whole configuration from the
	// folder in its repository. It is built EMPTY here and armed further down,
	// because the projector it needs does not exist until the product-layer
	// block — and boot order is not negotiable there (the config hook must be
	// installed before anything can serve a request). Never armed on the sqlite
	// fallback, where the route answers 501 rather than pretending.
	gitBootstrap := newGitBootstrapWiring(log.Printf)

	api, err := httpapi.New(httpapi.Config{
		Runner:    runner,
		Store:     store,
		Artifacts: artStore,
		Identity:  identityFromRequest,
		AgentDB:   agentDB, // nil on the SQLite fallback → legacy read paths
		CoreMCP:   coreMCP,
		// GET /agent/memories borrows the same embedder the memory tools use,
		// on the same READ-path terms: EmbedOrDegrade swallows a provider
		// outage, so one query loses its semantic leg rather than its answer.
		MemoryEmbedder: func(ctx context.Context, text string) []float32 {
			return embedding.EmbedOrDegrade(ctx, embedder, text)
		},
		// The dataset BYTE plane (O5). Config.Datasets fills itself from
		// AgentDB like every other metadata store; this one cannot, because
		// agentdb can never reach a blob store (extension imports agentdb) and
		// httpapi must not import extension. So the process-wide BlobStore —
		// the same one artifacts and snapshots use — is handed over here, as
		// the single-method reader httpapi declares.
		DatasetBlobs: blobs,
		// The console's git-projection status read (G16/G23). httpapi would
		// auto-fill this seam from AgentDB, and that would work — but it would
		// only ever answer the STATE half. The per-file quarantine and ignored
		// lists are an optional second interface agentdb cannot implement
		// (its methods would have to return an httpapi type, and httpapi
		// already imports agentdb), so the adapter that implements both is
		// handed over here. Nil store → nil seam, and the route answers
		// state_available=false exactly as it does on the sqlite fallback.
		GitProjection: newGitProjectionStatusSource(agentDB),
		// POST /agent/git-bootstrap (G26). Not auto-filled from AgentDB — a
		// bootstrap needs a clone, a lease and a projector — and empty until
		// the product-layer block arms it.
		GitBootstrap: gitBootstrap,
	})
	must(err)

	// API mux (authenticated) + an outer root mux for unauthenticated routes.
	apiMux := api.Mux()

	// ── Router + scheduler + human attention (product layer) ─────────────────────
	// All three need the product-layer tables, so all three are wired only on the
	// Postgres store; on the SQLite fallback events are never routed, schedules
	// never fire and the attention route is not mounted (404). See router.go /
	// scheduler.go / attention.go, and dispatch.go for the ONE gate the router
	// and the scheduler share — capacity is decided in exactly one place.
	//
	// attention is declared out here because it is shared by the HTTP route and
	// E4's `request_human_attention` MCP tool: the §9 mechanics are implemented
	// once, in attention.go, and the tool is a thin adapter onto this service.
	var attention *attentionService

	// gitWebhook is the inbound half of the git projection (G20). It is built
	// inside the product-layer block below, alongside the projector it hands
	// deliveries to, and mounted on the ROOT mux further down — outside
	// apiAuthMiddleware, because GitHub cannot hold a console JWT. Nil means
	// there is nothing to mount.
	var gitWebhook *gitWebhookWiring

	// The project map is loaded HERE, before the product-layer block, because
	// the git projection needs its `github_token_env` half as the fallback for
	// a project whose settings row names no push credential (DI3). Everything
	// else it feeds — API keys, framing origins, login — is wired further down
	// from the same value.
	//
	// It is held behind projectMapHolder rather than read once into a plain
	// *projectSettings: when AGENTKIT_PROJECT_MAP_FILE is set, the map reloads
	// on SIGHUP and on a timer (below), and every reader — resolve/allProjects
	// for login, the API-key index, and the git-token-env fallback — goes
	// through the holder so a reload reaches all of them without a restart
	// (A6). An inline AGENTKIT_PROJECT_MAP has no file to watch, so it behaves
	// exactly as before.
	projectMapHolder, err := newProjectSettingsHolder(os.Getenv)
	must(err)

	if agentDB != nil {
		// The `config.changed` emitter (§15.4, §15.8 — J3). Installed FIRST and
		// before anything can serve a request, because it is a post-commit hook
		// on the config-log seam: a configuration mutation that lands before the
		// hook exists is a change nobody is told about. Its sweep repairs
		// emissions lost to a crash between commit and append. See
		// configchanged.go.
		configChanges := newConfigChangeEmitter(agentDB, log.Printf)
		go configChanges.Run(ctx)

		// The git projection (G8/G9/G10, design/2026-09-09-git-projection.md).
		// installGitProjection replays the config log into one commit per event
		// for any project that has never rendered (the backfill) BEFORE the
		// render loop's boot reconciliation, which would otherwise lump that
		// history into a single commit, irrecoverably. The
		// store takes ONE post-commit hook, so the two consumers are fanned out
		// here rather than each installing its own. The projection's half does
		// nothing but mark a project dirty — no git, no IO — because this hook
		// runs synchronously on the mutating goroutine, which for a worker
		// rewriting a prompt is a model's blocking tool call. See
		// gitprojection.go rule 1.
		// gitTokenEnvFromProjectMap takes a *projectSettings snapshot and
		// returns a lookup closure; wrapping the call like this — rather than
		// calling it once with projectMapHolder.Get() — means the closure
		// below re-fetches the current settings on every invocation, so a
		// reloaded map's github_token_env reaches the git projection too (A6).
		gitHook, gitProj := installGitProjection(ctx, agentDB,
			func(project string) string {
				return gitTokenEnvFromProjectMap(projectMapHolder.Get())(project)
			}, log.Printf)
		gitWebhook = newGitWebhookWiring(agentDB, gitProj, os.Getenv, log.Printf)
		// The bootstrap door can now do its work: it takes the same lease and
		// work-lock as the render loop and the webhook import, off the same
		// projector (G26).
		gitBootstrap.enable(agentDB, gitProj)
		agentDB.SetConfigEventHook(fanOutConfigEvents(log.Printf, configChanges.Hook(), gitHook))

		gate := newDispatcher(dispatcherConfig{
			Store: agentDB,
			// The lease is what the router's reaper claims a dead job by (§8.4
			// step 4), so the starter must be able to take and release it.
			Starter: newRunnerSessionStarter(runner, store).withLeases(agentDB),
			// The core tool servers every job is told about (§6.2 step 3). This is
			// what makes memory_search & co. reachable from inside a worker job;
			// E4/I2 add their tools to the same server, so this line does not grow.
			// The same value httpapi.Config.CoreMCP got above — one spelling, so a
			// chat session and a job can never be told about different endpoints.
			CoreMCP:      coreMCP,
			DefaultImage: baseImage,
			// The briefing read seam (§6.2 step 2.4, §7.4) — the rolling summary
			// and each of the worker's own selectors.
			Memories: agentDB,
			Budget: newTokenBudget(tokenBudgetConfig{
				Store:  agentDB,
				Notify: softBudgetNotifier(os.Getenv, log.Printf),
			}),
			// Composition step 1 (§6.2, §13.5): `worker.image > project
			// base_image > global`. The SAME resolver the Runner holds, so the
			// composed image and a launch-time resolution cannot disagree.
			Images: imageResolver,
		})

		rt := newRouter(routerConfig{
			Store:      agentDB,
			Dispatcher: gate,
			Reaper:     newLeaseReaper(agentDB),
		})
		go rt.Run(ctx)

		// Sessions is the Runner, narrowed to SendMessage+Status
		// (sessionMessenger): a session-mode schedule wakes an EXISTING named
		// session instead of dispatching a job, and that is the whole extra
		// capability it gets. Deliberately not the whole *Runner — see the seam.
		// AGENTKIT_SCHEDULE_MAX_PROVISION_FAILURES: TEST RIGS ONLY. Proving the
		// §8.6 retirement streak costs one cron MINUTE per firing, so the
		// product default of five costs five minutes in a single e2e test.
		// Unset or unparseable → the default; a deployment should never set it.
		maxFail := 0
		if v := strings.TrimSpace(os.Getenv("AGENTKIT_SCHEDULE_MAX_PROVISION_FAILURES")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				log.Fatalf("[agentd] AGENTKIT_SCHEDULE_MAX_PROVISION_FAILURES=%q is not a positive integer", v)
			}
			maxFail = n
			log.Printf("[agentd] WARNING: schedule retirement streak overridden to %d (default %d) — test rigs only",
				n, agentdb.ScheduleMaxProvisionFailures)
		}
		sched := newScheduler(schedulerConfig{
			Store: agentDB, Dispatcher: gate, Sessions: runner, MaxProvisionFailures: maxFail,
		})
		go sched.Run(ctx)

		attention = newAttentionService(agentDB, permalinks)
		apiMux.HandleFunc("POST /agent/attention", attentionHandler(attention))
		go newAttentionSweeper(agentDB).Run(ctx)

		// The dataset version reaper + orphan blob sweep (O8, datasetreaper.go).
		// Same blobs value httpapi.Config.DatasetBlobs was handed above (one
		// process-wide BlobStore), and the same ctx every other product-layer
		// loop here uses.
		go newDatasetReaper(agentDB, blobs, gc.datasetKeepVersions, log.Printf).Run(ctx, gc.datasetReapInterval)

		log.Printf("[agentd] router + scheduler + attention sweep + dataset reaper running (zone=%s)", time.Local)
	} else {
		log.Printf("[agentd] no DATABASE_URL — event routing, schedules and request_human_attention are unavailable")
	}

	// ── Login modes ──────────────────────────────────────────────────────────────
	// google (GOOGLE_CLIENT_ID) and/or password (AGENTKIT_TEST_LOGIN) mint real
	// project-scoped JWTs, so both require a verifying secret and a project map.
	// Neither set = dev-open mode with the legacy /dev/token issuer.
	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	testLogin := os.Getenv("AGENTKIT_TEST_LOGIN")
	loginEnabled := googleClientID != "" || testLogin != ""

	root := http.NewServeMux()
	root.HandleFunc("/health", healthHandler)
	root.HandleFunc("GET /auth/config", authConfigHandler(googleClientID, testLogin != "", credentialMode(apiKey, oauthToken)))

	// POST /auth/verify-google — Google ID token → {email}, for an application
	// that embeds Agent Bob and runs its own allowlist. Registered whenever
	// GOOGLE_CLIENT_ID is set, and on the AUTHENTICATED mux, not beside
	// /auth/google on root: it verifies identities for a project's backend, which
	// holds a project API key, and an unauthenticated verification oracle would
	// answer anyone. It is mounted here rather than inside the login block below
	// because it needs no JWT secret, no project users and no login mode — only
	// a client id to check the token's audience against.
	registerVerifyGoogle(apiMux, googleClientID)

	// POST /agent/embed-token — an application's backend names one of ITS
	// sessions and gets a short-lived JWT confined to that session, which it
	// drops into an iframe URL fragment. API-key auth only, enforced inside the
	// handler: the middleware also accepts bearer tokens, and a browser holding
	// one must never be able to mint a fresh credential for a sibling session.
	//
	// Signed with jwtSecret — the secret apiAuthMiddleware verifies with, a few
	// lines below — because anything else mints tokens the API answers 401 to.
	// Session names are Postgres-only, so the lookup seam is left nil on the
	// sqlite fallback and the route answers 501 there. Declared as the interface
	// so that nil is a genuine nil and not an interface wrapping a nil *Store.
	var sessionNames embedSessionLookup
	if agentDB != nil {
		sessionNames = agentDB
	}
	registerEmbedToken(apiMux, jwtSecret, sessionNames)

	// The project map was loaded once, above the product-layer block (the git
	// projection needs it): its "projects" half carries per-project ops config
	// (API key env var names, framing origins, the git push token's variable
	// name) that a login-less, embed-only deployment still needs. It stays
	// optional — the zero-config demo mounts no map at all.
	//
	// Resolving env → key values is a boot-time act: a short key or one value
	// granting two projects fails the process rather than degrading quietly.
	// Held behind projectKeysHolder so a file-map reload (below) can recompute
	// it without apiAuthMiddleware ever holding a stale index (A6).
	apiKeys, err := newProjectKeysHolder(projectConfigsOf(projectMapHolder.Get()), os.Getenv, log.Printf)
	must(err)
	if s := projectMapHolder.Get(); s != nil {
		log.Printf("[agentd] project map: %d mapped account(s), %d configured project(s)",
			len(s.users), len(s.projects))
	}

	// AGENTKIT_PROJECT_MAP_RELOAD: how often the file map (if any) is re-read
	// on a timer, on top of SIGHUP always triggering a reload. 0 disables the
	// timer; unset defaults to 60s. Meaningless for an inline map or no map at
	// all — projectMapHolder.watch is a no-op in both cases (A6).
	reloadInterval := 60 * time.Second
	if v := strings.TrimSpace(os.Getenv("AGENTKIT_PROJECT_MAP_RELOAD")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatalf("[agentd] AGENTKIT_PROJECT_MAP_RELOAD=%q is not a valid duration", v)
		}
		reloadInterval = d
	}
	// api_key_env and allowed_origins live in the same file's "projects"
	// section as the users half, so every successful map reload also
	// recomputes the API-key index from the fresh config.
	go projectMapHolder.watch(ctx, reloadInterval, func() {
		apiKeys.reload(projectConfigsOf(projectMapHolder.Get()), os.Getenv, log.Printf)
	}, log.Printf)

	// GET /embed/csp — the frame-ancestors value nginx copies onto the embed
	// page it serves statically (deploy/web.nginx.conf). Unauthenticated and one
	// answer for every project; embedcsp.go explains both. This value is a
	// boot-time snapshot, not re-read on reload — see Discovered Issue A6-DI1.
	registerEmbedCSP(root, apiKeys.allowedOrigins())
	log.Printf("[agentd] embed framing: %s", frameAncestors(apiKeys.allowedOrigins()))

	if loginEnabled {
		if len(jwtSecret) == 0 {
			log.Fatal("[agentd] login modes require AGENTKIT_JWT_SECRET (dev-open auth would ignore the minted tokens)")
		}
		if projectMapHolder.Get() == nil {
			log.Fatal("[agentd] login modes require a project map: set AGENTKIT_PROJECT_MAP or AGENTKIT_PROJECT_MAP_FILE")
		}
		// projectMapHolder itself is passed as the userDirectory, not a
		// snapshot of its users half, so a reload reaches these handlers
		// without re-registering them (A6).
		loginIssuer := devclaims.NewWithTTL(jwtSecret, 12*time.Hour)
		if googleClientID != "" {
			root.HandleFunc("POST /auth/google", authGoogleHandler(
				&googleVerifier{clientID: googleClientID}, projectMapHolder, loginIssuer))
			log.Printf("[agentd] google login enabled (%d mapped account(s))", len(projectMapHolder.users()))
		}
		if testLogin != "" {
			email, password, err := parseTestLogin(testLogin)
			must(err)
			root.HandleFunc("POST /auth/password", authPasswordHandler(email, password, projectMapHolder, loginIssuer))
			log.Printf("[agentd] WARNING: password test login enabled for %s — all projects granted; test/dev only", email)
		}
		// Wildcard-login exchange: mints tokens for new project IDs.
		root.HandleFunc("POST /auth/project-token", authProjectTokenHandler(jwtSecret, loginIssuer))
	} else {
		// /dev/token (DEV ONLY): issues a short-lived JWT for the bundled UI. Not
		// registered when a login mode is on — it would mint valid demo tokens
		// signed with the real secret.
		//
		// It mints with the API secret, not the session secret: this token is
		// an API credential the browser sends as a bearer, and since the two
		// classes stopped sharing a key (sessionsecret.go) a session-class
		// token no longer verifies here. jwtSecret may be empty, which is
		// exactly the dev-open case where the middleware verifies nothing.
		devIssuer := devclaims.New(jwtSecret)
		root.HandleFunc("/dev/token", func(w http.ResponseWriter, r *http.Request) {
			scope := extension.ContextScope{
				UserEmail: "demo@example.com",
				Customer:  "demo",
				Job:       "demo-job",
			}
			tok, err := devIssuer.Issue(r.Context(), scope, "")
			if err != nil {
				http.Error(w, "token generation failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": tok})
		})
	}

	root.Handle("/agent-proxy/", http.StripPrefix("/agent-proxy", newModelProxyHandler()))

	// ── Core MCP tools (memory, images, skills, management) ──────────────────────
	// One http MCP server, mounted outside the API auth middleware because it
	// authenticates differently: the caller is a session container bearing its
	// per-session token, and the project scope comes from that token's claims.
	// See mcpserver.go. Needs the product-layer tables, so — like the session
	// context provider — it is wired only on the Postgres store.
	//
	// The image and skill tools additionally take the Runner: image_create
	// snapshots the calling session's container (§13.4) and skill_install
	// installs into it (§14.2), both identified from the token, never from a
	// tool argument.
	if agentDB != nil {
		mcpSrv := newMCPServer(coreMCPServerName, newSessionTokenAuth(sessionSecret, agentDB).authenticate)
		mcpSrv.register(newMemoryTools(agentDB, embedder, permalinks).tools()...)
		mcpSrv.register(newImageTools(agentDB, runner, permalinks).tools()...)
		mcpSrv.register(newSkillTools(agentDB, runner, permalinks).tools()...)
		mcpSrv.register(newManagementTools(agentDB, embedder, attention, permalinks).tools()...)
		mcpSrv.register(newConfigLogTools(agentDB, permalinks).tools()...)
		mcpSrv.register(newSessionTools(agentDB, permalinks).tools()...)
		// charter_validate takes no store — validation is pure — but it is
		// registered here with the rest, because a project with no product
		// layer has nothing to apply a charter to, and a tool that appeared
		// on only some stacks would be worse than one that never appears.
		mcpSrv.register(newCharterTools().tools()...)
		// The dataset tools (O6b). They take the Runner for its exec seam
		// (dataset_put pulls the named file out of the CALLING session's
		// container), the process-wide BlobStore that httpapi.Config.DatasetBlobs
		// and the reaper also hold, the API-class secret their download tokens
		// are signed with, and selfURL — how a nested container reaches agentd,
		// never the public base URL, which is unreachable from in there.
		mcpSrv.register(newDatasetTools(agentDB, runner, blobs, jwtSecret, selfURL, datasetMaxBytes).tools()...)
		root.Handle(coreMCPPath, mcpSrv)
		// Some MCP clients normalise the endpoint with a trailing slash; both
		// spellings must reach the same server or the tools simply vanish.
		root.Handle(coreMCPPath+"/", mcpSrv)
		log.Printf("[agentd] core mcp: %s%s tools=%s", selfURL, coreMCPPath,
			strings.Join(sortedStrings(mcpSrv.toolNames()), ","))
	} else {
		log.Printf("[agentd] core mcp DISABLED (no DATABASE_URL): memory requires Postgres")
	}

	// ── The git projection's inbound door ────────────────────────────────────────
	// POST /agent/git/webhook, mounted on the ROOT mux for the same reason the
	// core MCP server is: it authenticates differently. GitHub holds no console
	// JWT and no project API key; every delivery carries an HMAC-SHA256
	// signature over its raw body, verified against the secret named by that
	// project's git_webhook_secret_env. The payload chooses which project's
	// secret is checked and NOTHING else — what gets imported is the tree diff
	// against the remote tip, read from the repository itself. See
	// gitwebhookwiring.go and httpapi/gitwebhook.go.
	if gitWebhook != nil {
		must(gitWebhook.mount(root, ctx))
	} else {
		log.Printf("[agentd] git webhook DISABLED: the git projection is not running")
	}

	// Everything else goes through auth: a project API key, or a bearer JWT.
	root.Handle("/", apiAuthMiddleware(jwtSecret, apiKeys, apiMux))

	// ── Serve ────────────────────────────────────────────────────────────────────
	addr := envOr("ADDR", ":8099")
	log.Printf("[agentd] listening on %s  image=%s  docker=%s", addr, baseImage, dockerHost)
	must(http.ListenAndServe(addr, root))
}

// healthHandler is the unauthenticated liveness probe used by the compose
// healthcheck and the e2e harness.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
