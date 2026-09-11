# Project connections — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless
> dependencies say otherwise. Only the orchestrator changes ticket Status;
> workers may only append to Notes and the Discovered Issues Log. A ticket's
> checkbox is checked only after its Validation commands have been re-run by
> the orchestrator and pass. Do not expand scope; log surprises in the
> Discovered Issues Log instead.

Status: approved (2026-09-11)
Relates: `docs/19-embedding.md` hazard H6 (the unauthenticated `/agent-proxy/`, closed by T13);
`design/2026-09-09-git-projection.md` (the secrets allowlist this work must satisfy);
`design/2026-09-11-ovh-compose-hosting.md` (the deployment this must run on).

## Context

Agent Bob workers need to reach external services — GitHub and Google (Gmail, Docs) first — through
MCP tools. The Claude Agent SDK already speaks MCP over HTTP, and Bob already delivers MCP servers to
a session: agentd builds a map, sends it to the container in the body of `POST /sessions`
(`go/runner.go:696-713`), and the sandbox hands it to `query({mcpServers})`
(`sandbox/src/harness/claude-agent-sdk.ts:551-586`). What is missing is **authentication without
leaking the credential**.

Today the only way to give a session a credentialed MCP server is `mcp_config`
(`agentdb.MCPServerConfig`, `go/agentdb/sessions.go:46-57`) on project settings or a worker, with
`${VAR}` header values resolved **inside the container** from env vars copied in by the global
`AGENTKIT_MCP_ENV` allowlist (`go/cmd/agentd/mcpenv.go`). So the real token sits in every
container's environment, for every project, readable by `Bash`. Session containers also have
unrestricted internet egress (`go/execenv/docker/dind.go:112-113, 640-657`), so anything in a
container can leave it.

The operator is one person (Kai). There is deliberately **no UI and no OAuth loop**: tokens are
obtained by hand following a doc, placed in the environment, and named by a config file. That file
already exists — the project map (`AGENTKIT_PROJECT_MAP` / `AGENTKIT_PROJECT_MAP_FILE`, parsed in
`go/cmd/agentd/googleauth.go:91-175`), which already names env vars rather than holding secrets
(`api_key_env`, `github_token_env`).

The intended outcome: Kai adds a `connections` block to a project in the project map and a token to
the environment; the architect sees the connection and grants it to the workers that need it; those
workers get `mcp__github__*` (etc.) tools whose traffic flows through agentd, which swaps in the real
credential. The token never enters a container, the database, or git.

Two facts about upstreams, checked 2026-09-11:
- **GitHub** hosts an MCP server at `https://api.githubcopilot.com/mcp/` that accepts
  `Authorization: Bearer <personal access token>`.
- **Google** hosts per-product MCP servers — Gmail `https://gmailmcp.googleapis.com/mcp/v1`
  (scopes `gmail.readonly`, `gmail.compose`; **drafts only, cannot send**), Docs
  `https://docsmcp.googleapis.com/mcp/v1` (scopes `drive.readonly`, `drive.file`,
  `documents.readonly`, `documents`), Drive `https://drivemcp.googleapis.com/mcp/v1` — taking an
  OAuth 2.0 access token (≤1 hour). They are in the **Google Workspace Developer Preview Program**,
  and each needs its API *and* its `*mcp.googleapis.com` service enabled in the GCP project. Whether
  they accept consumer `@gmail.com` accounts is **unconfirmed**; T14's doc says so and T15's manual
  check settles it.

## Architecture

```
 local-config/connections.env    local-config/project-map.json          Postgres
 WOLF_GITHUB_PAT=github_pat_… ◄── wolf.connections.github.auth.token_env   workers.connections
 GOOGLE_CLIENT_SECRET=…       ◄── wolf.connections.gmail.auth.*_env        architect:  ["*"]
 KAI_GOOGLE_REFRESH=…               (names + URLs; never secrets)          researcher: ["github"]
          │                                 │                                     │
          └────────── read once at boot ────┴──► agentd ◄──── read on every request ─┘
                                                   │
   session container                               │  /connect/{name}/{rest}
 ┌──────────────────────────────┐ ${SESSION_TOKEN} │  1. verify session JWT → project, session
 │ Claude Agent SDK             │ ───────────────► │  2. session row → worker → worker.connections
 │ mcp__github__* tools         │                  │  3. drop inbound credentials, add the real one
 │ url = <self>/connect/github/ │ ◄─────────────── │  4. stream request + response (no buffering)
 └──────────────────────────────┘   no secret      ▼
                          api.githubcopilot.com/mcp/  ·  gmailmcp/docsmcp.googleapis.com/mcp/v1
```

### Decisions

1. **The project map defines what a project *has*.** Each project in the object form gains
   `connections: {name → {description, url, auth}}`. `auth.type` is `bearer` (a static token:
   `token_env`) or `google_oauth` (`client_id_env`, `client_secret_env`, `refresh_token_env`; agentd
   exchanges the refresh token for an access token and caches it until shortly before expiry). The
   map names env vars only. Read at boot; editing it needs an agentd restart — acceptable for one
   operator.
2. **The database records who *gets* what.** `workers.connections` is a JSONB list of connection
   names, `"*"` meaning every connection the project has. It is a normal worker field: config-logged
   (via `UpsertWorker`'s existing `WithConfigEvent`), revertable, rendered by the git projection
   (names are not secrets). Grants live in the DB — not the file — because the architect writes
   them.
3. **The trust rule: authority is passed down, never taken.** Workers may change anything that is
   revertable. Reach into the outside world (an email drafted, code pushed) cannot be reverted, so a
   worker may **grant only connections it holds itself**; any worker may **remove** a connection
   from another (that only reduces reach). The architect is created holding `"*"`, so it can grant
   anything. There is no architect-specific code — it is one rule for every worker, consistent with
   "roles are workers composed from primitives". An MCP caller with no worker (a human chat session
   reaching `/mcp`) holds nothing and so can grant nothing through tools.
   - **You cannot steer someone stronger.** Granting is not the only route to reach: rewriting the
     architect's prompt ("grant researcher github") would get the architect to do it. So a worker
     may change another worker — prompt, fields, anything, via `worker_update`,
     `worker_prompt_write` or any other `worker_*` mutation — only if it holds **every** connection
     the target holds (`"*"` target → caller must hold `"*"`). Rewriting the **project** prompt
     (`project_prompt_write`), which every worker reads, requires holding `"*"`. A worker with no
     connections can still improve other workers with none, so the §8.7 acceptance loop is
     unaffected. Steering through memories and events remains possible and is documented, not
     blocked.
   - **Only a logged-in person grants over HTTP.** A change to `connections` through
     `PUT /agent/workers/{name}` requires a console login. A project API key (an embedding app such
     as Wolf) or an embed/dataset-scoped token is refused with 403 for that field; its other worker
     edits still work.
   - **Git import never grants.** `connections` renders to git but is `NotImportable`, like the
     `git_*` settings (`go/gitproj/allowlist.go:238-249`): anyone with push access to the mirror
     would otherwise be able to grant `"*"`.
   - **A stolen session token dies with the session.** `sessionTokenAuth` honours an *expired* JWT
     while the session row exists (`go/cmd/agentd/mcpserver.go:530-535`), and rows outlive idle
     archival indefinitely — so a token read with `Bash` and sent out over open egress would be a
     lasting credential. `/connect/` and `/agent-proxy/` honour an expired token only for a session
     that is **live** (not archived, completed, errored or deleted). `/mcp` keeps today's rule (out
     of scope; logged).
4. **agentd proxies and swaps the credential.** New route `/connect/{name}/…` on the root mux, beside
   `/mcp`, authenticated by the same session-token verifier (`sessionTokenAuth.authenticate`,
   `go/cmd/agentd/mcpserver.go:465-546`), with the live-session rule above. The worker is
   `Session.Worker` from the session row — never a header, never `Persona`. The grant is checked on
   **every request**, so revoking a grant takes effect mid-session. A disabled or deleted worker
   holds nothing. The container's MCP entry is shaped exactly like the core
   server's (`coreMCPServers`, `mcpserver.go:576-584`): `{url: <AGENTKIT_SELF_URL>/connect/<name>/,
   headers: {Authorization: "${SESSION_TOKEN}"}}`. **The sandbox does not change.**
5. **Merge order** of a session's MCP map: project `mcp_config` → worker `mcp_config` →
   **connections** → core. Connections are operator-defined so they outrank workers' own entries;
   core stays non-overridable.
6. **Plain chat sessions (no worker) get no connections.** To give a chat tools, create it as a
   worker (`worker` on `POST /agent/session`, `go/httpapi/session.go:44`). Connections are keyed on
   `Session.Worker` **only**: a session created with `persona` alone gets none (the proxy would 403
   it anyway), and a session whose persona and worker differ gets the *worker's* grants.
7. **`core` and `ui` are reserved connection names**, refused at boot: `core` is the core MCP server
   (`mcpserver.go:74`); `ui` is the sandbox's in-image server, which a session server of the same
   name would silently replace (`claude-agent-sdk.ts:562`).
8. **Compose delivery.** Compose passes agentd's env var by var (`docker-compose.yml:146-185`), which
   would force a compose edit per token. Instead a gitignored `local-config/` folder is mounted
   read-only into agentd at `/etc/agent-bob/`, holding `project-map.json`, and
   `local-config/connections.env` is loaded as an optional `env_file`. Kai adds arbitrary var names
   without touching compose. Two traps, handled in T12/T14: a key listed in agentd's `environment:`
   block (e.g. `GOOGLE_CLIENT_ID: ${GOOGLE_CLIENT_ID:-}`) **overrides** `env_file`, so
   `connections.env` must not define those names — reuse them from `.env`; and an inline
   `AGENTKIT_PROJECT_MAP` silently wins over `_FILE` (`googleauth.go:243-247`), so agentd now logs
   a loud warning when both are set. `env_file` `required: false` needs Docker Compose ≥ 2.24
   (2.29.7 locally; check the OVH box).

### Rejected alternatives

- **Token in the container via `AGENTKIT_MCP_ENV`** (today's path): every container, every project,
  readable by Bash, exfiltratable over open egress.
- **A browser OAuth "connect" loop / secrets in the DB**: real work for one user; revisit if Bob
  becomes a product. Secrets stay in the environment.
- **"Only the architect may grant"**: would make the architect an engine role with its own code path;
  the delegation rule gives the same safety with no role.
- **Hosting a third-party Gmail MCP server beside agentd**: unnecessary now Google hosts one.

## File Structure

**Create**
- `go/connections/doc.go` — package doc: what a connection is, the trust rule.
- `go/connections/spec.go` — `Spec`/`Auth` types, validation, `Registry` built from specs + env.
- `go/connections/grant.go` — `Holds`, `CanGrant`, `ExpandGrants`.
- `go/connections/resolve.go` — grants → `agentdb.MCPServers` pointing at `/connect/`.
- `go/connections/token.go` — `TokenSource`: static bearer; Google refresh → access, cached.
- `go/connections/proxy.go` — the `/connect/` `http.Handler`.
- `go/connections/*_test.go` — table tests for each file above.
- `go/cmd/agentd/connections.go` — agentd wiring: registry from the project map, grant lookup,
  mount helper `newConnectHandler`, the `ConnectionServers` closure.
- `go/cmd/agentd/connections_test.go` — wiring tests + T15's offline end-to-end test.
- `docs/22-connections.md` — operator guide incl. manual token steps.
- `local-config/README.md` — what goes in the folder (the folder's other contents are gitignored).
- `local-config/project-map.example.json` — a worked example with GitHub + Gmail + Docs.

**Modify**
- `go/cmd/agentd/googleauth.go` — `projectConfig.Connections`, validation.
- `go/agentdb/workers.go` — `ConnectionList` type, `Worker.Connections`, validation.
- `go/agentdb/migrations.go` — migration `050_worker_connections`.
- `go/compose.go` — `ComposeJobInput.Connections`, merged in `composeMCP`.
- `go/cmd/agentd/dispatch.go` — compute and pass connections into `ComposeJob`.
- `go/cmd/agentd/sessioncontext.go` — merge connections for chat sessions run as a worker.
- `go/httpapi/workers.go`, `go/httpapi/httpapi.go` — `connections` on PUT; name check.
- `go/cmd/agentd/mcp_management.go` — `worker_create`/`worker_update`/`worker_list` +
  `connection_list`.
- `go/charter/resolve.go` — architect gets `["*"]`.
- `go/orgprompts/` (the architect prompt file) — one paragraph on connections.
- `go/gitproj/allowlist.go`, `go/cmd/agentd/gitimport.go` — render + import `connections`.
- `go/cmd/agentd/modelproxy.go`, `go/cmd/agentd/main.go` — mount `/connect/`; guard `/agent-proxy/`;
  boot order (project map loaded before the session-context provider).
- `go/cmd/agentd/mcpserver.go` — factor `verifyToken(ctx, raw, requireLive)` out of `authenticate`.
- `go/cmd/agentd/auth.go` — mark API-key principals (`Identity.APIKey`).
- `docker-compose.yml`, `.gitignore`, `.env.example` — `local-config/` mount + env file.
- `docs/19-embedding.md` (H6), `docs/18-workers-memory-events.md` (worker fields), `CLAUDE.md`
  (docs table row) — documentation.

## Interfaces

### Project map (object form) — new per-project key

```json
{
  "users": { "kai@example.com": ["wolf"] },
  "projects": {
    "wolf": {
      "api_key_env": "WOLF_API_KEY",
      "connections": {
        "github": {
          "description": "badcodetv repos, issues and pull requests",
          "url": "https://api.githubcopilot.com/mcp/",
          "auth": { "type": "bearer", "token_env": "WOLF_GITHUB_PAT" }
        },
        "gmail": {
          "description": "Kai's inbox — read, search, label, draft (cannot send)",
          "url": "https://gmailmcp.googleapis.com/mcp/v1",
          "auth": { "type": "google_oauth",
                    "client_id_env": "GOOGLE_CLIENT_ID",
                    "client_secret_env": "GOOGLE_CLIENT_SECRET",
                    "refresh_token_env": "KAI_GOOGLE_REFRESH" }
        }
      }
    }
  }
}
```

### `go/connections`

```go
package connections

const Wildcard = "*"
var ReservedNames = []string{"core", "ui"}

type AuthType string
const (
    AuthBearer      AuthType = "bearer"
    AuthGoogleOAuth AuthType = "google_oauth"
)

type Auth struct {
    Type            AuthType `json:"type"`
    TokenEnv        string   `json:"token_env,omitempty"`         // bearer
    ClientIDEnv     string   `json:"client_id_env,omitempty"`     // google_oauth
    ClientSecretEnv string   `json:"client_secret_env,omitempty"` // google_oauth
    RefreshTokenEnv string   `json:"refresh_token_env,omitempty"` // google_oauth
    // tokenURL overrides Google's token endpoint. NOT settable from the project
    // map (an edit there must not be able to send the client secret and refresh
    // token elsewhere); set only by the unexported test constructor.
    tokenURL        string
}

type Spec struct {
    Description string `json:"description"`
    URL         string `json:"url"`
    Auth        Auth   `json:"auth"`
}

// ValidateName: agentdb.ValidateConnectionName plus not in ReservedNames.
func ValidateName(name string) error
// Validate: URL absolute; https, or http only for localhost/127.0.0.1 (local fakes);
// auth fields present for the type and absent for the other type; env names match
// ^[A-Za-z_][A-Za-z0-9_]*$; unknown auth type refused.
func (s Spec) Validate() error

type Info struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Available   bool   `json:"available"`
    // Unavailable says why, naming the env var(s) — names are not secrets.
    Unavailable string `json:"unavailable,omitempty"`
}

type Connection struct {
    Project, Name string
    Spec          Spec
    Token         TokenSource // nil when unavailable
    Unavailable   string
}

type Registry struct{ /* project → name → *Connection */ }

// NewRegistry validates every spec (error → boot fails) and resolves env vars.
// A missing/empty env var does NOT fail: the connection is Unavailable and logf
// is called once naming the project, connection and variable.
func NewRegistry(specs map[string]map[string]Spec, getenv func(string) string,
    logf func(string, ...any)) (*Registry, error)
func (r *Registry) Get(project, name string) (*Connection, bool)
func (r *Registry) List(project string) []Info        // sorted by name; nil registry → nil
func (r *Registry) Names(project string) []string     // sorted

// grant.go
func Holds(held []string, name string) bool           // "*" or exact match
// ExpandGrants: "*" → every name the project has; unknown names dropped; sorted, deduped.
func ExpandGrants(r *Registry, project string, grants []string) []string
// CanGrant enforces the trust rule for a transition before → after made by a
// caller holding `held`: every entry in after that is not in before must be held
// by the caller ("*" in after-not-before requires the caller to hold "*").
// Removals are always allowed. The error names the first offending connection.
func CanGrant(held, before, after []string) error

// resolve.go — one MCP entry per expanded, AVAILABLE grant:
//   {URL: selfURL + "/connect/" + name + "/", Headers: {"Authorization": "${SESSION_TOKEN}"}}
// Unavailable grants are skipped and logged by the caller.
func Servers(r *Registry, project string, grants []string, selfURL string) agentdb.MCPServers

// token.go
type TokenSource interface{ Token(ctx context.Context) (string, error) } // the bare token
func StaticToken(tok string) TokenSource
// GoogleRefresh uses golang.org/x/oauth2 (already in go.mod) with a ReuseTokenSource;
// concurrent callers share one refresh. tokenURL "" → https://oauth2.googleapis.com/token.
func GoogleRefresh(clientID, clientSecret, refreshToken, tokenURL string) TokenSource
var ErrCredentialRevoked = errors.New("connections: upstream refused the refresh token (invalid_grant)")

// proxy.go
type Caller struct{ Project, SessionID, Worker string }
type ProxyConfig struct {
    Registry     *Registry
    Authenticate func(*http.Request) (Caller, error)
    Grants       func(ctx context.Context, project, worker string) ([]string, error)
    Transport    http.RoundTripper // nil → http.DefaultTransport
    Logf         func(string, ...any)
}
// NewProxy serves paths of the form /connect/{name}/{rest...}.
func NewProxy(cfg ProxyConfig) http.Handler
```

### Proxy behaviour (normative)

| Condition | Status | Body (JSON `{"error": "..."}`) |
|---|---|---|
| no/invalid session token | 401 | "session token rejected" |
| expired token for a session that is not live (archived, completed, errored, deleted) | 401 | "session token expired" |
| session has no worker | 403 | "this session is not running as a worker; connections are granted to workers" |
| worker deleted or disabled | 403 | "worker X is disabled or gone, and holds no connections" |
| worker does not hold `name` | 403 | "worker X does not hold connection NAME (connection_list shows what exists)" |
| grant lookup fails (store error) | 503 | "could not read worker X's connections; retry" |
| `{rest}` contains a `.`/`..` segment or an encoded `/` (`%2F`) | 400 | "invalid path" |
| `name` not in this project's registry | 404 | "no connection NAME in this project" |
| connection unavailable | 503 | the `Unavailable` reason (names the env var) |
| token source fails (`ErrCredentialRevoked`) | 502 | "Google refused the refresh token for NAME — get a new one: docs/22-connections.md §Google" |
| upstream answers 401/403 | 502 | "upstream rejected the credential for NAME"; `WWW-Authenticate` stripped |
| otherwise | upstream's | streamed as-is |

- Upstream URL: `spec.URL` is parsed **once** at registry build; each request joins `{rest}` with
  `url.JoinPath` (empty rest → `spec.URL` exactly) and keeps the inbound query string. Use
  `ProxyRequest.SetURL` so the outbound `Host` is the upstream's, never agentd's. Methods pass
  through (Streamable HTTP uses POST, GET and DELETE).
- **Redirects:** the proxy never follows them; a 3xx whose `Location` points at a different host
  than `spec.URL` is turned into a 502 ("upstream redirected off-host"), so the credential can
  never be replayed elsewhere.
- **Request headers: allowlist**, not denylist — `Accept`, `Content-Type`, `Mcp-Session-Id`,
  `Mcp-Protocol-Version`, `Last-Event-ID`. Everything else (including `Authorization`, `Cookie`,
  `X-Api-Key`, `X-Session-Id`) is dropped; the proxy then sets `Authorization: Bearer <token>`.
- **Response headers:** passed through except `WWW-Authenticate` and `Set-Cookie`.
- Streaming: `httputil.ReverseProxy` with `FlushInterval: -1`; no body buffering either way.
- Every request logs one line: project, session, worker, connection, method, upstream status,
  duration. Never a header value.

### agentdb

```go
// ConnectionList is NULL-preserving JSONB, exactly like SelectorList
// (go/agentdb/workers.go:31-60): nil ↔ SQL NULL, [] ↔ '[]'.
type ConnectionList []string

// Worker gains (after Briefing). No omitempty: the config-event payload must
// carry null vs [] so a revert restores exactly what was there.
Connections ConnectionList `json:"connections" gorm:"type:jsonb"`

// ValidateConnectionName: ^[A-Za-z0-9][A-Za-z0-9_-]*$, ≤ 64 bytes
// (the MCP server-name rule, go/agentdb/sessions.go:64).
func ValidateConnectionName(name string) error
// validateWorker additionally: every entry is "*" or a valid name; no duplicates.
```

Migration `050_worker_connections`:
`ALTER TABLE workers ADD COLUMN IF NOT EXISTS connections JSONB;` (nullable, no default).

### Compose / HTTP / tools

- `agentkit.ComposeJobInput.Connections agentdb.MCPServers` — already-resolved entries; merged in
  `composeMCP` (`go/compose.go:501-521`) after worker, before core.
- `httpapi.Config.ConnectionNames func(project string) []string` — nil → no existence check.
- `PUT /agent/workers/{name}` body: `connections` — absent/`null` keeps, `[]` clears, otherwise
  replaces; every entry `"*"` or a name in `ConnectionNames(project)`, else 400. A body that would
  **change** connections is refused 403 unless the caller is a console login: `httpapi.Identity`
  gains `APIKey bool` (set by agentd's `identityFromRequest`, `go/cmd/agentd/auth.go:248-257`, for
  API-key principals — today recognisable only by the synthetic `api-key:<project>` email), and
  the route also refuses when `SessionScope` or `DatasetScope` is non-empty. Sending the stored
  value unchanged is not a change and is allowed.
- Core tools:
  - `worker_create` — new optional `connections: string[]`; checked with
    `CanGrant(callerHeld, nil, requested)` and existence.
  - `worker_update` — `connections` joins `workerUpdatableFields` (`mcp_management.go:1008`);
    `CanGrant(callerHeld, current, next)` and existence for added names. Frozen targets are
    already refused.
  - `worker_list` — `workerRecord` gains `connections: string[]` (never null).
  - `connection_list` (new, no arguments) — `[{name, description, available, unavailable?,
    held}]`, `held` computed from the caller's worker.
  - **Steering rule** (every `worker_*` mutation — `worker_update`, `worker_prompt_write`, and any
    other tool that writes a worker row; `worker_create` of a new name is covered by `CanGrant`):
    refused unless `connections.Covers(callerHeld, targetHeld)` — the caller holds every
    connection the target holds, `"*"` target needing `"*"`. `project_prompt_write` requires the
    caller to hold `"*"`. Refusal text: "worker X can reach connections you do not hold (…); only
    a worker holding all of them may change it".
- `connections.Covers(held, target []string) bool` joins `grant.go` (T2).
- Charter: `charter.Resolve` (`go/charter/resolve.go:84-100`) sets
  `Connections: agentdb.ConnectionList{"*"}` on the architect.

### Compose

```yaml
# docker-compose.yml, service agentd
env_file:
  - path: ./local-config/connections.env
    required: false
volumes:
  - ./local-config:/etc/agent-bob:ro
```
and `.env.example` documents `AGENTKIT_PROJECT_MAP_FILE=/etc/agent-bob/project-map.json`.

## Out of Scope

- Any UI for connections or grants (the worker editor in `web/` is untouched; the PUT route keeps an
  omitted `connections` field, so the current UI cannot wipe grants).
- An OAuth "connect" loop, storing secrets in the database, token rotation automation.
- Credentials for Bash/CLI tools (`gh`, `git push` from inside a container).
- Per-tool filtering at the proxy (e.g. GitHub read-only); a scoped token does this for now.
- Restricting container egress; removing or migrating `mcp_config` / `AGENTKIT_MCP_ENV`.
- Fixing `worker_list` echoing `mcp_config` literals, and the sandbox not expanding `${VAR}` in an MCP
  `url` — both pre-existing; log in Discovered Issues if touched, do not fix here.
- Updating prompts of architects that already exist in databases (prompts are data; Kai decides).
- Changing `/mcp`'s acceptance of expired tokens for non-live sessions (the same hazard as Decision
  3's last bullet, for the core tools) — log it; a separate change.
- Blocking indirect steering through memories, briefings or events.
- Forwarding static, non-secret upstream headers from the spec (e.g. GitHub's read-only header).

## Tickets

### T1: `connections` package — spec, validation, registry   [Status: done | Model: sonnet]
- **Scope:** Create `go/connections/doc.go` and `spec.go` implementing `Wildcard`, `ReservedNames`,
  `AuthType`, `Auth`, `Spec`, `ValidateName`, `Spec.Validate`, `Info`, `Connection`, `Registry`,
  `NewRegistry`, `Get`, `List`, `Names` exactly as in Interfaces. `NewRegistry` builds a
  `TokenSource` per available connection using T3's constructors — to keep T1 independent, define
  the `TokenSource` interface and `StaticToken` here in `token.go`, and have `NewRegistry` take the
  Google constructor through an unexported package variable that T3 fills in (default: returns an
  unavailable connection with reason "google_oauth not built yet"). `Auth.tokenURL` is unexported
  and unmarshalled from nothing; a `json:"token_url"` key in the map is an unknown key and ignored.
  `NewRegistry` parses each `spec.URL` once and keeps the `*url.URL` on `Connection`. Also add
  `agentdb.ValidateConnectionName` in `go/agentdb/workers.go` (T1 needs it; T6 reuses it).
- **Files:** create `go/connections/{doc,spec,token}.go`, `go/connections/spec_test.go`; modify
  `go/agentdb/workers.go` (+ test in `go/agentdb/workers_test.go` or the existing worker test file).
- **Acceptance criteria:** table tests cover: valid bearer and google specs; reserved names; bad
  names; http URL refused except localhost/127.0.0.1; missing/extra auth fields per type; bad env
  var names; unknown auth type; a missing env var → `Available:false` with a reason naming the var
  and exactly one `logf` call; `List`/`Names` sorted; nil registry safe.
- **TDD:** yes
- **Validation:** `cd go && go test ./connections/... ./agentdb/... -count=1` → PASS; `go vet ./...`
  clean.
- **Depends on:** —
- [x] done
- Notes: Implemented `go/connections/doc.go`, `spec.go`, `token.go` (TokenSource/StaticToken +
  the `googleTokenSource` package-var seam for T3) and added `agentdb.ValidateConnectionName` in
  `go/agentdb/workers.go` (borrows the existing `mcpServerNamePattern` shape, 64-byte cap), with a
  table test in `workers_test.go`. `go test ./connections/... ./agentdb/... -count=1` PASS;
  `go vet ./...` clean.

### T2: Grant rule and resolution   [Status: done | Model: sonnet]
- **Scope:** `go/connections/grant.go` (`Holds`, `ExpandGrants`, `CanGrant`, `Covers`) and
  `resolve.go` (`Servers`) per Interfaces. `Covers(held, target)` is true when every entry of
  `target` is held (`"*"` in target requires `"*"` in held); an empty target is always covered.
- **Files:** create `go/connections/{grant,resolve}.go`, `go/connections/{grant,resolve}_test.go`.
- **Acceptance criteria:** `CanGrant` table covers: adding a held name OK; adding an unheld name
  refused naming it; adding `"*"` refused unless caller holds `"*"`; caller holding `"*"` may add
  anything; removals always OK (even by a caller holding nothing); unchanged list OK; nil vs empty
  equivalent. `Covers` table: empty target; subset; superset refused; `"*"` target vs `"*"` and
  vs a full explicit list (refused — `"*"` also covers connections added later).
  `Servers` emits `<selfURL>/connect/<name>/` with `Authorization: ${SESSION_TOKEN}`,
  trims a trailing slash on selfURL, expands `"*"`, drops unknown and unavailable names, and its
  output passes `agentdb.MCPServers.Validate()`.
- **TDD:** yes
- **Validation:** `cd go && go test ./connections/... -count=1` → PASS.
- **Depends on:** T1
- [x] done
- Notes: Implemented `go/connections/grant.go` (`Holds`, `ExpandGrants`, `CanGrant`, `Covers`) and
  `resolve.go` (`Servers`). `go test ./connections/... -count=1` PASS (all T1+T2 cases green).

### T3: Google refresh-token source   [Status: done | Model: sonnet]
- **Scope:** `GoogleRefresh` and `ErrCredentialRevoked` in `go/connections/token.go`, using
  `golang.org/x/oauth2` (`oauth2.Config{Endpoint: {TokenURL}}` + `ReuseTokenSource`); wire it into
  `NewRegistry` (replace T1's placeholder). An `invalid_grant` response maps to
  `ErrCredentialRevoked` (use `errors.As` on `*oauth2.RetrieveError`, checking `ErrorCode`).
- **Files:** modify `go/connections/token.go`, `go/connections/spec.go`; create
  `go/connections/token_test.go`.
- **Acceptance criteria:** against an `httptest` token endpoint: first call exchanges the refresh
  token (asserts `grant_type=refresh_token`, client id/secret sent); a second call before expiry
  makes no request; after expiry it refreshes; 20 concurrent callers cause one request;
  `invalid_grant` → `errors.Is(err, ErrCredentialRevoked)`; the refresh token never appears in an
  error string.
- **TDD:** yes
- **Validation:** `cd go && go test ./connections/... -count=1 -race` → PASS.
- **Depends on:** T1
- [x] done
- Notes: Implemented `GoogleRefresh`/`ErrCredentialRevoked` in `token.go` on top of
  `oauth2.Config.TokenSource` (its `reuseTokenSource` already holds one mutex across the whole
  refresh, so N concurrent callers naturally collapse to one upstream request — no extra
  singleflight needed). Wired into `NewRegistry` by replacing T1's placeholder `googleTokenSource`
  var. `go test ./connections/... -count=1 -race` PASS (2.6s once the race-instrumented build is
  warm; see Discovered Issues Log for how long a cold `-race` build took on this box, which is
  process/environment noise, not a code issue).

### T4: The `/connect/` proxy handler   [Status: done | Model: opus]
- **Scope:** `go/connections/proxy.go`: `Caller`, `ProxyConfig`, `NewProxy`, implementing the
  normative table, URL, redirect and header rules above with `httputil.ReverseProxy` (`Rewrite`
  hook using `SetURL`, `FlushInterval: -1`, `ModifyResponse` for 401/403 → 502, off-host 3xx →
  502 and header stripping, `ErrorHandler` for transport errors → 502). `Grants` returns
  `(grants []string, err error)`; the wiring (T12) returns an empty list for a disabled or deleted
  worker and a sentinel `connections.ErrWorkerGone` the proxy maps to the 403 row. Live-session
  checking belongs to `Authenticate` (T12), not to this package.
- **Files:** create `go/connections/proxy.go`, `go/connections/proxy_test.go`.
- **Acceptance criteria:** tests with an `httptest` upstream prove: the upstream receives
  `Authorization: Bearer <real>` and never the session token, `Cookie`, `X-Api-Key` or
  `X-Session-Id`; `Mcp-Session-Id` passes both ways; an SSE response's first event reaches the
  client before the upstream finishes (flush test with a blocking upstream); GET and DELETE pass;
  query string and `{rest}` path preserved; every status row in the table produced by a dedicated
  case; `WWW-Authenticate` and `Set-Cookie` absent from responses; the grant is re-read on each
  request (revoking between two requests flips 200 → 403); `/connect/github/../../x`,
  `/connect/github/a%2Fb` → 400; the upstream sees its own `Host`; a 302 to another host → 502 and
  the other host is never contacted; a 302 to the same host is passed back unchanged.
- **TDD:** yes
- **Validation:** `cd go && go test ./connections/... -count=1 -race` → PASS.
- **Depends on:** T1, T2, T3
- [x] done
- Notes: Implemented `go/connections/proxy.go` (`Caller`, `ProxyConfig`, `NewProxy`,
  `ErrWorkerGone`, plus **`ErrSessionExpired`** — see Discovered Issues) on a per-request
  `httputil.ReverseProxy` (`Rewrite` + `SetURL`, `FlushInterval: -1`, `ModifyResponse` for
  401/403 and off-origin 3xx → 502 and header stripping, `ErrorHandler` → 502). `proxy_test.go`
  has one dedicated case per status-table row (18 cases incl. 4 path-traversal shapes, each also
  asserting whether the upstream was contacted) plus credential swap/header allowlist, own
  `Host`, path+query joining (empty rest = spec URL exactly), POST/GET/DELETE, response headers,
  SSE flush with a blocking upstream through a real `httptest` front server, grant re-read
  (200 → 403), off-host / same-host / same-host-absolute redirects, transport error, and the log
  line (one per request, no header value). Test file failed to compile first (TDD), then
  `go test ./connections/... -count=1 -race` PASS; `go build ./... && go vet ./...` clean.
  For T12: `Authenticate` must wrap `connections.ErrSessionExpired` for the "session token
  expired" 401 row (any other error → "session token rejected"); `Grants` returns
  `([]string{}, connections.ErrWorkerGone)` for a disabled/deleted worker.
  Merge step: the final builder (commit 421793d) re-verified the proxy under `-race` five times
  (`-run Proxy -count=5`), mutation-tested three real breaks (same-origin check, inbound header
  pass-through, `%2F` refusal) and kept the files unchanged; its notes match the above.

### T5: Project map `connections` key   [Status: done | Model: sonnet]
- **Scope:** add `Connections map[string]connections.Spec `json:"connections"`` to `projectConfig`
  (`go/cmd/agentd/googleauth.go:40-67`); in `parseProjectSettingsObjectForm` (`:132-175`) validate
  each name with `connections.ValidateName` and each spec with `Spec.Validate`, errors prefixed
  `project map: project "<id>": connections: "<name>": …`. Add
  `connectionSpecsOf(*projectSettings) map[string]map[string]connections.Spec` beside
  `projectConfigsOf`. The flat legacy form yields no connections.
- **Files:** modify `go/cmd/agentd/googleauth.go`, `go/cmd/agentd/googleauth_test.go`.
- **Acceptance criteria:** a map with valid connections parses; each invalid case from T1 surfaces
  as a parse error naming project and connection; existing project-map tests unchanged and green.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/ -run 'ProjectMap|ProjectSettings' -count=1` → PASS.
- **Depends on:** T1
- [x] done
- Notes: No live Postgres involved in T5 (unchanged from original ticket) — the
  AGENTKIT_TEST_POSTGRES_URL setup in the task instructions was precautionary and not needed here,
  since none of T5's validation commands touch agentdb. Committed as c5d5ead on conn/T5, on top of
  the prior c760646.
  Merge step: merged cleanly (no conflicts) onto the T4 merge; `go build`/`go vet` green, the T5
  validation command PASS, and `go test ./cmd/agentd/... ./connections/...` PASS both without and
  with `AGENTKIT_TEST_POSTGRES_URL` (the `bob-conn-pg` container did not exist at merge time and
  was recreated from `pgvector/pgvector:pg16` on 127.0.0.1:55439).

### T6: `Worker.Connections` column   [Status: done | Model: sonnet]
- **Scope:** `agentdb.ConnectionList` (NULL-preserving Scan/Value, copy `SelectorList`'s pattern at
  `go/agentdb/workers.go:31-60`), `Worker.Connections` (tag `json:"connections"`, no omitempty),
  validation in `validateWorker` (`:152`), migration `050_worker_connections` appended after
  `049_git_projection_notes` (`go/agentdb/migrations.go:1181`). **Two hand-maintained spots must
  learn the field**, or it is silently dropped: `UpsertWorker` copies columns by hand
  (`go/agentdb/workers.go:251-259`) — add `Connections`; and `workerConfigEqual` (`:176-190`)
  must compare it, so a connections-only change is not treated as "no change" and a
  connections change plus an enable flip is not logged as a plain `worker_enable`.
- **Files:** modify `go/agentdb/workers.go`, `go/agentdb/migrations.go`; tests in the existing
  worker/migration test files.
- **Acceptance criteria:** validation refuses empty, malformed and duplicate entries and accepts
  `"*"`; live-Postgres round trip preserves nil vs `[]` vs `["github","*"]`; updating ONLY
  connections on an existing worker persists and writes exactly one config event (not zero);
  changing connections and `enabled` together is logged as an update, not `worker_enable`; a
  worker upsert that
  changes only connections writes a config event whose payload contains `connections`, and
  reverting that event (`POST /agent/config-events/{id}/revert` path, or the store method it calls)
  restores the previous list; `TestMutationsAreLogged` still passes.
- **TDD:** yes
- **Validation:** `cd go && go test ./agentdb/... -count=1` → PASS; with a throwaway database,
  `AGENTKIT_TEST_POSTGRES_URL=<url> go test ./agentdb/... -run 'Worker|Migration|ConfigEvent'
  -count=1` → PASS (not skipped).
- **Depends on:** T1
- [x] done
- Notes: Did not re-run web/ tests or a full-repo `go test ./...`: the fix touched only go/agentdb
  and go/gitproj, web was untouched by this change, and a full-repo `go test ./...` includes
  systemtest/e2e suites that spin up Docker-in-Docker — out of scope for these targeted fixes and
  unnecessary alongside other agents' containers on this host. I started it once, judged it
  unneeded against the literal instruction ('re-run every Validation command of T6 plus go
  build/go vet'), and stopped it before completion rather than let it run. Committed as 3eda355 +
  a770bfa (verifier fixes: live-PG round-trip test, gitproj allowlist entry) on conn/T6.
  Merge step: merged cleanly (no conflicts) onto the T13 merge; `go build`/`go vet` green;
  `go test ./agentdb/... -count=1` PASS; `go test ./gitproj/...` PASS; web `typecheck` + full
  `vitest run` PASS (1545 tests); `go test ./httpapi/... ./cmd/agentd/ -run 'Git|Worker|...'`
  PASS. Live Postgres (`bob_mt6`): `TestLivePG_WorkerConnectionsRoundTrip` and every other
  Worker/Migration/ConfigEvent case PASS; the only failure is the pre-existing
  `TestLivePG_QueryEventsMixedPreAndPostMigrationRows`, which also fails on the pre-merge base
  (4aae41c) on a fresh database — see the (T6) log entry.

### T7: HTTP worker PUT carries `connections`   [Status: done | Model: sonnet]
- **Scope:** `workerBody.Connections agentdb.ConnectionList` (nil keep, `[]` clear) in
  `go/httpapi/workers.go:44-62`, kept from `prev` in the keep block (`:151-160`) and applied like
  `Briefing` (`:176-178`); `httpapi.Config.ConnectionNames` (`go/httpapi/httpapi.go:45`); unknown
  names → 400 naming the name. A logged-in person is not subject to `CanGrant`, but only a
  logged-in person may **change** connections: add `Identity.APIKey bool`
  (`go/httpapi/httpapi.go:18-38`), set it in `identityFromRequest`
  (`go/cmd/agentd/auth.go:248-257`) for API-key principals (carry a flag on `principal` from the
  API-key middleware rather than string-matching the `api-key:` email if the middleware allows),
  and refuse a connections change with 403 when `APIKey`, `SessionScope` or `DatasetScope` is set.
- **Files:** modify `go/httpapi/workers.go`, `go/httpapi/httpapi.go`, `go/cmd/agentd/auth.go`, the
  httpapi workers test file, the agentd auth test file.
- **Acceptance criteria:** omitted field keeps stored grants (prompt-only save does not clear them);
  `null` keeps; `[]` clears; unknown name → 400; `"*"` accepted; nil `ConnectionNames` skips the
  existence check; an API-key caller changing connections → 403 while the same caller changing
  `description` → 200; an API-key caller re-sending the stored list unchanged → 200; an agentd
  test proves an API-key request yields `Identity.APIKey == true` and a console JWT `false`.
- **TDD:** yes
- **Validation:** `cd go && go test ./httpapi/... -count=1` → PASS.
- **Depends on:** T6
- [x] done
- Notes: Implemented the design's ordering: connections are validated + gated BEFORE any other field is applied to the in-progress `worker` struct, so a refusal (400 unknown name, or 403 unauthorized change) never partially writes other fields either — the read-then-mutate flow returns immediately, matching the existing "store read fails -> nothing written" discipline in this handler. `contains` and `connectionsEqual` were added as small local helpers in httpapi/workers.go rather than importing go/connections (would need agentdb.Wildcard-style access this package doesn't need) or agentdb's unexported `connectionWildcard`/`jsonValueEqual` (unexported) — this duplicates the "*" literal a third time (agentdb, connections, now httpapi), consistent with agentdb's own comment on why it duplicates rather than imports connections (import-cycle avoidance; httpapi's reason is narrower — just avoiding an unnecessary new dependency for one constant and one equality helper). connectionsEqual/order-sensitivity mirrors agentdb.jsonValueEqual's json.Marshal-based comparison exactly, so "unchanged" means byte-for-byte same order, same as the config-log's own equality notion. Did not add a `connection_list`-style richness (no Info/available/held) to ConnectionNames — the ticket only asked for existence-check names, and T9's `connection_list` tool owns richer introspection. Did not touch mcp_management.go / worker_create / worker_update — those are T9's scope (core tools under CanGrant), and this ticket's `CanGrant` rule intentionally does NOT apply to the human HTTP path per decision 3 ("A worker may change anything that is revertable... only a logged-in person grants over HTTP" — no CanGrant check, just the APIKey/Scope gate). (Merge step: agentd does not yet set `httpapi.Config.ConnectionNames`, so the existence check is inert in the running binary until the wiring ticket sets it.)

### T8: Connections reach the session's MCP map   [Status: pending | Model: sonnet]
- **Scope:** add `ComposeJobInput.Connections` and merge it in `composeMCP` (`go/compose.go:501-521`)
  between worker and core, updating the doc comment's order. In `go/cmd/agentd/dispatch.go`, add
  `dispatcherConfig.ConnectionServers func(project string, grants []string) agentdb.MCPServers`
  (nil → none) and pass its result at `:323-335`. In `go/cmd/agentd/sessioncontext.go`, give the
  provider the same func and merge its result after the worker layer (`:170-176`). **Key it on
  the session's Worker identity, not `scope.Persona`** (Decision 6): the proxy authorises against
  `Session.Worker`, so an entry keyed on Persona would 403 forever. If `extension.ContextScope`
  does not carry the worker, add a field for it and set it where httpapi builds the scope from the
  `worker` body field (`go/httpapi/session.go:185-192`). Persona-only → no connections; persona
  X + worker Y → Y's grants.
- **Files:** modify `go/compose.go`, `go/compose_test.go`, `go/cmd/agentd/dispatch.go`,
  `go/cmd/agentd/sessioncontext.go`, `go/cmd/agentd/sessioncontext_test.go`, and a dispatch test.
- **Acceptance criteria:** compose test pins the order (a connection named like a worker
  `mcp_config` entry wins; core wins over a connection named `core` — defensive, even though T1
  forbids it); a dispatched job for a worker holding `["github"]` gets a `github` entry pointing at
  `/connect/github/`; a chat session with no worker gets none; a persona-only session gets none; a
  session with persona X and worker Y gets Y's grants; existing compose pins (including the
  byte-for-byte core preamble) unchanged.
- **TDD:** yes
- **Validation:** `cd go && go test . ./cmd/agentd/ -count=1` → PASS.
- **Depends on:** T2, T6
- [ ] done
- Notes:

### T9: Core tools — grants under the trust rule   [Status: done | Model: opus]
- **Scope:** in `go/cmd/agentd/mcp_management.go`: give `managementTools` a
  `connections connectionCatalog` dependency (`interface{ List(project string) []connections.Info;
  Names(project string) []string }`, nil-safe) via `newManagementTools` (`:142`); `worker_create`
  schema + handler (`:502-538`, `:932`) accept `connections`; add `"connections"` to
  `workerUpdatableFields` (`:1008`) with a `case` in `workerUpdate` (`:1068-1101`); `workerRecord`
  and `toWorkerRecord` (`:165-205`) gain `connections`; register a new `connection_list` tool.
  Caller's holdings: `caller.Worker == ""` → none; else the caller's worker row's `Connections`.
  Refusal errors explain the rule in one sentence ("a worker can only grant connections it holds
  itself; you hold [..]"). **Also enforce the steering rule** (Interfaces → Compose / HTTP / tools):
  before any `worker_*` mutation of an existing worker (`worker_update`, `worker_prompt_write` at
  `:1151`, and any other tool in this file or others that writes a worker row — grep for
  `UpsertWorker` callers under `go/cmd/agentd/mcp_*.go`), refuse unless
  `connections.Covers(callerHeld, target.Connections)`; `project_prompt_write` (`:1240`) requires
  the caller to hold `"*"`. Check after the frozen check so error precedence is: not found →
  frozen → steering. Update the tool descriptions so a model reading them learns both rules.
- **Files:** modify `go/cmd/agentd/mcp_management.go`, `go/cmd/agentd/main.go` (constructor call),
  `go/cmd/agentd/orgprompts_test.go` (it also calls `newManagementTools`, `:50`),
  `go/cmd/agentd/mcp_management_test.go`.
- **Acceptance criteria:** tests: a `"*"` holder grants anything; a `["gmail"]` holder can grant
  gmail, cannot grant github or `"*"`, can remove github from another worker; a no-worker caller can
  grant nothing but can remove; unknown connection name refused; frozen target still refused;
  `connection_list` marks `held` correctly and never includes any env value; `worker_list` shows
  `connections: []` for none. Steering: a `[]` worker can rewrite the prompt of a `[]` worker
  (acceptance loop intact) but not of a `["github"]` worker or the `"*"` architect; a `["github"]`
  worker can update a `["github"]` worker; only a `"*"` holder may call `project_prompt_write`;
  the existing §8.7 acceptance-loop tests still pass.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/ -run 'Management|Worker|Connection' -count=1` →
  PASS.
- **Depends on:** T2, T6
- [x] done
- Notes: Committed as 2aac200 on conn/T9 (worktree created fresh from feat/project-connections at df091c4). The catalog passed from main.go is nil until T12 builds the Registry: until then connection_list is empty and only "*" can be granted through tools. Decisions the ticket did not spell out: (a) a caller whose own worker is DISABLED or deleted holds nothing, the same as the proxy, not only a caller with no worker; (b) a store error reading the caller's own row refuses with "retry" instead of being read as holding nothing; (c) the steering rule exempts only an update whose single field is connections and which adds nothing ("adds nothing" = connections.CanGrant(nil, before, after) == nil, i.e. the literal-list comparison CanGrant already uses, so narrowing "*" to ["github"] counts as a grant of github, not a removal); a removal bundled with another field (even enabled:false) is a change and goes through the steering check; (d) the existence check applies only to names being ADDED, so a stale name already on a worker (removed from the project map) does not block its other edits; (e) project_prompt_write's "*" check runs after the blank-prompt and rationale checks, so a missing rationale is still reported first; (f) connection_list is placed right after worker_update in the tool order, and returns {connections, count}. Tests were renamed/named so the validation regex 'Management|Worker|Connection' covers all of them (TestWorkerSteeringRule, TestWorkerSteeringRuleErrorPrecedence, TestConnectionProjectPromptWriteRequiresTheWildcard, TestConnectionGrantsUnderTheTrustRule, TestConnectionList, TestConnectionToolsWithoutACatalog, TestWorkerListShowsConnections, TestConnectionRulesAreInTheToolDescriptions, TestConnectionGrantFailsClosedWhenTheCallerCannotBeRead, TestConnectionRemovalFromAFrozenWorkerIsStillRefused). Three existing tests were changed deliberately (caller given "*" for project_prompt_write) and TestManagementToolsSurface now lists connection_list. Live Postgres run of the whole cmd/agentd package on bob_t9: PASS, nothing skipped. (Merge step: merged cleanly on top of T7 with no conflicts; build, vet, the T9 validation command, cmd/agentd + connections + orgprompts + charter, and a live-Postgres run of cmd/agentd on bob_mt9 all PASS.)

### T10: The architect holds everything and knows it   [Status: pending | Model: sonnet]
- **Scope:** `charter.Resolve` sets `Connections: agentdb.ConnectionList{"*"}` on the architect
  worker (`go/charter/resolve.go:84-100`); confirm `topology_apply` writes the field through (fix
  if it copies fields by hand). Add one short paragraph to the architect prompt in `go/orgprompts/`:
  `connection_list` shows the project's external tools; grant each worker only the connections its
  job needs via `worker_create`/`worker_update`; you can grant only what you hold.
- **Files:** modify `go/charter/resolve.go`, its test, the architect prompt file under
  `go/orgprompts/` and any test pinning its text.
- **Acceptance criteria:** approving a charter yields an architect row with `["*"]`; prompt tests
  updated deliberately, not deleted; `cmd/agentd/orgprompts_test.go` (which checks that every
  tool and argument a prompt names exists) passes.
- **TDD:** yes
- **Validation:** `cd go && go test ./charter/... ./orgprompts/... ./agentdb/... -count=1` → PASS;
  `go test ./cmd/agentd/ -run Prompt -count=1` → PASS.
- **Depends on:** T6, T9
- [ ] done
- Notes:

### T11: Git projection renders and imports grants   [Status: done | Model: sonnet]
- **Scope:** add `{Field: "Connections", Key: "connections", Decision: Render, NotImportable: true,
  Reason: "connection names — the credential lives in agentd's environment and is never a field.
  Rendered so a reader sees who can reach what; NOT importable, because anyone with push access to
  the mirror could otherwise grant '*' (Decision 3)"}` to `workerRules`
  (`go/gitproj/allowlist.go:260-280`), following the `git_*` settings' `NotImportable` precedent
  (`:238-249`). The importer (`go/cmd/agentd/gitimport.go:545-552`) must treat an edited
  `connections` key exactly as it treats other not-importable edits — ignored and recorded as a
  git-projection note — and never write the field.
- **Files:** modify `go/gitproj/allowlist.go`, `go/cmd/agentd/gitimport.go`, their tests.
- **Acceptance criteria:** `allowlist_guard_test.go` passes; a rendered worker file carries
  `connections: [github]`; a human commit changing `connections` leaves the DB unchanged and
  produces an ignored-edit note; render → import → render is a fixed point.
- **TDD:** yes
- **Validation:** `cd go && go test ./gitproj/... ./cmd/agentd/ -run 'Git|Import|Render|Allowlist'
  -count=1` → PASS.
- **Depends on:** T6
- [x] done
- Notes: T6's allowlist entry alone was not sufficient to satisfy this ticket's acceptance criteria. `Rule.NotImportable` in go/gitproj/allowlist.go is (and always was) purely descriptive/documentary — grepping the whole repo shows nothing outside test code ever reads `r.NotImportable`. The actual enforcement path the importer relies on is a second, independent, hand-maintained map (`notImportable` in go/gitproj/parse.go) keyed directly by frontmatter string key, which ParseAgainst consults to populate Change.DroppedFields / strip Change.Fields, and which gitimport.go's "ignored edit" notice and gitbootstrap.go's defence-in-depth refusal both read via `gitproj.NotImportableFields()`. T11 added "connections" to that second map — this is the change that actually makes a human commit editing `connections:` get dropped and reported, rather than silently accepted as a field change (it would still not have been *written* to the DB, since applyWorkerFields' switch has no "connections" case and falls to its default no-op branch, but it would not have produced the required "ignored-edit note", and Change.Fields would have carried a phantom entry the caller could act on incorrectly).

Went slightly outside the ticket's literal "Files" list (which named only allowlist.go and gitimport.go) by also touching gitproj/parse.go and its tests, and cmd/agentd/gitwebhookwiring_test.go — both were necessary: parse.go is where the actual drop-list lives, and gitwebhookwiring_test.go had an independent pinned copy of `NotImportableFields()`'s expected value that would otherwise regress. I also added a belt-and-braces restore of `next.Connections = current.Connections` in gitimport.go's planWorker, matching the existing precedent for GitRemote/GitBranch/GitSubfolder/GitTokenEnv in planSettings, so the "never write the field" guarantee holds even if some future refactor of applyWorkerFields' switch statement adds a "connections" case by accident.

Extended goldenState()'s "architect" worker fixture to carry `Connections: agentdb.ConnectionList{"github"}` and updated `goldenWorkerArchitect` accordingly, so the acceptance criterion "a rendered worker file carries connections: [github]" is proven inline by the existing golden-project test rather than only by a new standalone test.

One test (TestRenderTreeCreateParseSeesTheWholeFile) and two other pinned-list assertions (TestParseAgainstDropsGitFields's `wantDropped`, TestGitImportGitConfigFieldsAreIgnored's/TestGitWebhookSecretEnvIsNotImportable's `want` list) had baked in the assumption "NotImportableFields() as a whole == what settings.md alone can drop", which stopped being true the moment a second, worker-only not-importable key existed. These were deliberately narrowed/updated (not deleted or weakened) with comments explaining why, per the "never silently weaken a test" rule — each now asserts against an explicit settings-only or full list as appropriate, rather than assuming the global NotImportableFields() count corresponds to one entity kind.

Merge step: merged into feat/project-connections (after T9) with no conflicts; `go build ./...`, `go vet ./...`, the Validation command, and `go test ./gitproj/... ./cmd/agentd/...` (live Postgres, bob_mt11) all PASS on the merged tree.

### T12: Wire `/connect/` into agentd   [Status: pending | Model: sonnet]
- **Scope:** **Boot order first:** today the session-context provider is built at `main.go:250`,
  the runner at `:317` and `httpapi.New` at `:368`, but the project map is only loaded at `:431`.
  Move `loadProjectSettingsOptional` (it only reads env) and the Registry build **above `:247`** so
  every consumer can receive them; keep its later users working. When both `AGENTKIT_PROJECT_MAP`
  and `AGENTKIT_PROJECT_MAP_FILE` are set, log a loud warning that the inline map wins and the file
  is ignored (`googleauth.go:243-247`). Then `go/cmd/agentd/connections.go`: build the `Registry`
  from `connectionSpecsOf(projectCfg)`; a boot log line per project listing connection names and
  availability (never values); `newConnectHandler(reg, verify, store)` using T13's
  `verifyToken(ctx, raw, requireLive=true)` (so an expired token for a non-live session is 401),
  reading the worker from the session row, and `GetWorker(...)` → grants (disabled or deleted
  worker → `connections.ErrWorkerGone`; store error → error → 503); mount at `"/connect/"` on the
  root mux beside `/mcp` (`main.go:657-680`), **only when `agentDB != nil`**; pass
  `ConnectionServers` (closure over `connections.Servers(reg, project, grants, selfURL)`, logging
  skipped unavailable grants) to the dispatcher, session context provider and pass
  `ConnectionNames` to `httpapi.Config` and the catalog to `newManagementTools`. Compose/env:
  `docker-compose.yml` agentd gets the `env_file` + `local-config` mount from Interfaces;
  `.gitignore` ignores `local-config/*` except `README.md` and `project-map.example.json`;
  `.env.example` documents `AGENTKIT_PROJECT_MAP_FILE=/etc/agent-bob/project-map.json`.
- **Files:** create `go/cmd/agentd/connections.go`, `go/cmd/agentd/connections_test.go`,
  `local-config/README.md`, `local-config/project-map.example.json`; modify
  `go/cmd/agentd/main.go`, `docker-compose.yml`, `.gitignore`, `.env.example`.
- **Acceptance criteria:** handler test: valid session token for a worker holding the connection →
  forwarded; the adapter reads the worker from the session row, not from any header; an expired
  token for an archived session → 401, for a live session → forwarded; a disabled worker → 403;
  route absent when there is no database; the dual-map warning is logged; `docker compose config`
  succeeds with and without `local-config/connections.env` present.
- **TDD:** yes (the adapter); no (compose/env edits)
- **Validation:** `cd go && go build ./... && go vet ./... && go test ./cmd/agentd/ -count=1` → PASS;
  `cd .. && docker compose config >/dev/null` → exit 0 (run once with a `local-config/connections.env`
  present, once without).
- **Depends on:** T4, T5, T7, T8, T9, T13
- [ ] done
- Notes:

### T13: Guard `/agent-proxy/` with the session token   [Status: done | Model: sonnet]
- **Scope:** in real-key mode only (the `modelproxy.Handler` branch of `newModelProxyHandler`,
  `go/cmd/agentd/modelproxy.go:43-68`), wrap the handler so a request must carry a valid session
  JWT — read from `X-Api-Key` (what the SDK sends; the Runner puts the JWT there,
  `go/runner.go:2730-2752`), falling back to `Authorization`. Verify with the same logic as
  `sessionTokenAuth`: factor `verifyToken(ctx, raw string, requireLive bool) (mcpCaller, error)`
  out of `authenticate` at `mcpserver.go:465` rather than duplicating it. `authenticate` keeps
  calling it with `requireLive=false` (`/mcp` behaviour unchanged); the model proxy and T12's
  `/connect/` pass `true`, meaning an expired token is honoured only when the session row is live
  (not archived — `SnapshotState == "archived"` — and not in a terminal `Status`; list the status
  values found in `go/agentdb/types.go` in Notes). Refuse with 401 JSON otherwise. Mock and
  subscription branches stay as they are (no key is at stake). Pass the verifier into
  `newModelProxyHandler` from `main.go:643`. **Nil-store trap:** on the sqlite fallback
  `agentDB` is a nil `*agentdb.Store`; passing it as the `mcpSessionLookup` interface makes a
  non-nil interface whose `GetSession` panics (`go/agentdb/sessions.go:288`). Pass an untyped nil
  (or the Runner's store) when `agentDB == nil`, and without a store refuse expired tokens
  outright. Update `docs/19-embedding.md` H6 to say it is closed in
  API-key mode, with the date, and fix its stale line refs; fix the stale "token-protected (401)"
  comment at `go/execenv/docker/dind.go:640-657` only if it is now true.
- **Files:** modify `go/cmd/agentd/modelproxy.go`, `go/cmd/agentd/mcpserver.go`,
  `go/cmd/agentd/main.go`, `go/cmd/agentd/modelproxy_test.go`, `docs/19-embedding.md`.
- **Acceptance criteria:** tests: no token → 401 and the upstream is never contacted; a forged or
  wrong-secret JWT → 401; a valid session JWT in `X-Api-Key` → forwarded with the real key; an
  expired JWT for a live session → forwarded; for an archived session → 401; with no database
  (sqlite fallback) a valid token is forwarded and nothing panics; `/mcp`'s existing token tests
  unchanged; mock mode unaffected.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/ -run 'ModelProxy|SessionToken' -count=1` → PASS;
  mock stack still answers a chat turn (T15 covers this).
- **Depends on:** —
- [x] done
- Notes:
  This resubmission touched only docs/19-embedding.md and go/cmd/agentd/mcpserver.go (comment-only
  change in the latter, no logic touched). Recommend the orchestrator's merge step add a Discovered
  Issues Log entry to design/2026-09-11-project-connections.md along these lines: "T13: the archived
  half of the requireLive check (SnapshotState=='archived') has no live path today because the
  idle-archive sweep (go/cmd/agentd/gc.go) never writes SnapshotState — it only calls
  SetSnapshotHandle. Only the Status=='error' half of sessionIsLive currently protects an expired,
  replayed session token. Fixing the sweep to write SnapshotState='archived' would close this
  without any change to sessionIsLive or verifyToken." No such entry could be added by this run
  since design/2026-09-11-project-connections.md is explicitly out of scope (owned by a separate
  merge step). (Merge step: added as the (T13) Discovered Issues Log entry below.)

### T14: Operator guide `docs/22-connections.md`   [Status: pending | Model: sonnet]
- **Scope:** write the doc, in the house style of `docs/18`–`21`:
  1. What a connection is; the diagram; the trust rule in one paragraph.
  2. The `local-config/` folder: `project-map.json` + `connections.env`; restart to apply. The two
     compose traps from Decision 8: never define in `connections.env` a name that agentd's
     `environment:` block already lists (it will be blanked), and move an inline
     `AGENTKIT_PROJECT_MAP` into the file (inline wins). Compose ≥ 2.24.
  3. Config reference for `connections` (both auth types, reserved names, errors at boot).
  4. Granting: architect holds `"*"`; for projects chartered **before** this change, set it once:
     `curl -X PUT …/agent/workers/architect -d '{"connections":["*"],"rationale":"…"}'` (give the
     exact command with the auth header — it must be a console login token, since API keys cannot
     change connections); the trust rule, including "you cannot steer someone stronger" and the
     project-prompt rule; plain chats get nothing; git shows grants but never imports them.
  5. **Getting tokens by hand:**
     - *GitHub* — fine-grained personal access token: Settings → Developer settings → Fine-grained
       tokens; resource owner; repository selection; the minimum permissions for issues/PRs/contents
       read; expiry; paste into `connections.env`.
     - *Google* — in GCP project `webkit-servers`: enable Gmail API + `gmailmcp.googleapis.com`,
       Docs API + `docsmcp.googleapis.com` (and Drive's two if used); join the Workspace Developer
       Preview Program; OAuth consent screen: add the scopes, and set publishing status to **In
       production** (in *Testing*, refresh tokens expire after 7 days); on the existing Web OAuth
       client used for login (`GOOGLE_CLIENT_ID`) create a client secret and add
       `https://developers.google.com/oauthplayground` as an authorised redirect URI; in the OAuth
       Playground tick "Use your own OAuth credentials", select every scope for every Google
       connection at once, authorise, "Exchange authorization code for tokens", copy the refresh
       token into `connections.env`. State plainly: consumer `@gmail.com` support for the preview
       servers is unconfirmed; the Gmail server cannot send.
  6. Troubleshooting: each proxy status from the normative table and what to do.
  7. Security notes: what the container can still do (use a granted connection while its session is
     live — a stolen token dies with the session), unrestricted egress, steering through memories
     and events is not blocked, an embedded chat running as a worker can *use* (not grant) that
     worker's connections, `mcp_config` is the old leaky path.
  Also: add a `docs/22-connections.md` row to the docs table in `CLAUDE.md`; add `connections` to the
  worker field list in `docs/18-workers-memory-events.md`; add the embedded-chat note to
  `docs/19-embedding.md`'s hazards.
- **Files:** create `docs/22-connections.md`; modify `CLAUDE.md`,
  `docs/18-workers-memory-events.md`, `docs/19-embedding.md`.
- **Acceptance criteria:** every env var name, route, status code and field in the doc matches the
  code as merged (re-read T1–T13's final code, do not copy this plan blindly); no real token or
  email address in the doc.
- **TDD:** no
- **Validation:** `grep -n "connections" docs/22-connections.md CLAUDE.md
  docs/18-workers-memory-events.md` shows the new sections; `cd go && go test ./... -count=1` still
  PASS (doc-pinning tests, if any).
- **Depends on:** T12, T13
- [ ] done
- Notes:

### T15: End-to-end verification   [Status: pending | Model: opus]
- **Scope:**
  1. **Offline Go test** in `go/cmd/agentd/connections_test.go` (live-Postgres; skips without
     `AGENTKIT_TEST_POSTGRES_URL`): a fake Streamable-HTTP MCP upstream (`httptest`; answers
     `initialize` and `tools/list` over SSE, records headers); a project map with a `bearer`
     connection pointing at it; a worker holding it; a real session row and a session JWT minted as
     the Runner does; requests through the real `newConnectHandler` mounted on a mux. Assert:
     `tools/list` round-trips; upstream saw `Bearer <fake>` and never the JWT; revoke via
     `worker_update` from a second worker holding `"*"` → next call 403; the composed MCP map for a
     dispatched job contains the `/connect/` entry.
  2. **Full gates:** `cd go && go build ./... && go vet ./... && go test ./... -count=1`;
     `cd sandbox && npm ci && npm test && git checkout yarn.lock`;
     `cd web && npm ci && npm run typecheck && npm test`.
  3. **Mock stack smoke:** with a `local-config/project-map.json` holding one `bearer` connection
     (token var set in `local-config/connections.env` to a dummy) —
     `docker compose up -d --build`; agentd boot log lists the connection as available;
     `docker compose exec agentd wget -S -O /dev/null http://localhost:8099/connect/github/ 2>&1 |
     grep 'HTTP/'` → shows `401` (the agentd image is alpine with no curl,
     `deploy/agentd.Dockerfile:9-12`; wget exits non-zero on 401, which is expected);
     a chat turn in the UI still works in mock mode (proves T13 did not break `/agent-proxy/`).
     Tear down.
  4. **Manual live checklist for Kai** (appended to `docs/22-connections.md` §Verify, not run by the
     executor): real GitHub PAT → a worker holding `github` lists an issue; real Google refresh
     token → a worker holding `gmail` searches threads and creates a draft; record whether a
     consumer `@gmail.com` account works.
- **Files:** modify `go/cmd/agentd/connections_test.go`, `docs/22-connections.md`.
- **Acceptance criteria:** items 1–3 pass and their output is pasted into Notes; item 4 is written.
- **TDD:** no (verification)
- **Validation:** the commands in items 1–3.
- **Depends on:** T1–T14
- [ ] done
- Notes:

## Discovered Issues Log
(appended by executors during implementation)

- **T3, test-only.** `httptest`'s `ResponseWriter` does not set `Content-Type`, and
  `json.NewEncoder(w).Encode(...)` doesn't set it either; without it Go's sniffer calls a JSON body
  `text/plain`, and `golang.org/x/oauth2/internal.RetrieveToken` parses `text/plain` responses as a
  `x-www-form-urlencoded` query string, not JSON — so a fake Google token endpoint that forgets
  `w.Header().Set("Content-Type", "application/json")` silently returns a token with an empty
  `access_token` (`"oauth2: server response missing access_token"`) instead of a parse error. Every
  success handler in `token_test.go` sets it explicitly now. Worth a one-line callout in
  `docs/22-connections.md` if T15's fake upstream hits the same thing.
- **T3, process/environment note, not a code bug.** `go test ./connections/... -race` on this box
  took several minutes the first time (cold build of ~250 race-instrumented dependency packages
  under a loaded, shared machine — `uptime` showed load average ~3.75 on 32 cores with several other
  sessions' `go test` running concurrently); a second run with a warm build cache completed in
  ~2.6s. Mid-investigation a `SIGQUIT` goroutine dump was taken while it was merely slow (not
  deadlocked) and looked alarming out of context — worth knowing if a future ticket's `-race`
  validation seems to hang on first run here.
- **T3, real fix, not just test hygiene.** `TestGoogleRefresh_ExchangesOnFirstCall` originally
  synchronized the fake server's captured request form back to the test goroutine over an
  unbuffered read of a *buffered* channel it never actually needed — reworked to a mutex-guarded
  variable read only after `Token()` returns (the HTTP round trip already orders that in practice;
  the mutex is what gives the race detector a happens-before edge). Also: `oauth2`'s
  `AuthStyleAutoDetect` sends client_id/client_secret via HTTP Basic Auth on the *first* attempt
  and only falls back to body params if that gets a non-2xx — a fake upstream that always answers
  200 will only ever see the header form, so a test asserting the credentials arrived as form
  fields is asserting the wrong thing; assert against whichever place they actually showed up.
- **T4, interface addition.** The status table has two different 401 bodies ("rejected" vs
  "expired") but `ProxyConfig.Authenticate` returns a bare `error`, so the proxy could not tell
  them apart. Added sentinel `connections.ErrSessionExpired`: T12's `Authenticate` wraps it
  (`fmt.Errorf("…: %w", connections.ErrSessionExpired)`) when `verifyToken(…, requireLive=true)`
  refuses an expired token for a non-live session; every other error is "session token rejected".
- **T4, `ProxyRequest.SetURL` is not the join rule we want.** `SetURL` joins the *inbound* path
  onto the target's (`/mcp/v1` + `/connect/github/` → `/mcp/v1/connect/github/`), and with an empty
  outbound path its `singleJoiningSlash` would still append a `/` (`/mcp/v1` → `/mcp/v1/`), breaking
  "empty rest → spec URL exactly". The proxy calls `SetURL` (for scheme, host, query merge and
  `Out.Host = ""`) and then puts the path computed by `url.JoinPath(rest)` back into
  `Out.URL.Path`/`RawPath`. `TestProxy_PathAndQuery` pins it.
- **T4, rows/choices the table does not spell out.** (a) The 400 path check runs right after
  authentication, before the worker/grant checks (the table lists it lower; an authenticated
  caller learns nothing extra from the order). (b) A token source error that is *not*
  `ErrCredentialRevoked` → 502 "could not obtain a credential for NAME; retry"; a transport error
  → 502 "could not reach the upstream for NAME". Neither body carries the underlying error; the
  log line does (`note=`). (c) "Different host" for redirects is compared as an **origin**:
  scheme + lower-cased host + port with default ports made explicit, so an `https`→`http`
  redirect on the same host is also a 502 (the credential must not be replayed in clear).
  (d) `Accept-Encoding` is not on the allowlist, so Go's transport asks the upstream for gzip
  itself and decompresses transparently — harmless, and the SSE flush test passes through it.
  T14's troubleshooting table should include (b).
- **T4, the flush test cannot tell `FlushInterval: -1` from `0`.** `ReverseProxy` already flushes
  immediately when the upstream's `Content-Type` is `text/event-stream`, so the SSE test passes
  either way; `-1` is still set because it also covers responses a server streams without that
  content type. Not a gap in the code, a limit of what the test proves.
- **T4, for T12 — the root mux cleans paths first.** Go's `ServeMux` answers a request whose path
  contains `..`/`.` segments with a 301 to the cleaned path before any handler runs, so on the
  mounted route `/connect/github/../../x` becomes a redirect to `/x`, never reaching the proxy
  (and never reaching an upstream). The proxy's own 400 is what the handler tests prove, since
  they call it directly; T12's handler test should not expect a 400 through the mux for `..`.
  `%2F` and `%2e%2e` are *not* cleaned by the mux and do reach the proxy's 400.
- (T4) Minor, not acted on. An inbound `Connection: Upgrade`/`Upgrade` pair is re-added to the
  outbound request by `ReverseProxy` itself, after `Rewrite` has run, so a WebSocket-style upgrade
  could be passed to an upstream. The credential is still only the swapped Bearer token and MCP
  does not use upgrades, so this is harmless; blocking upgrades would need an explicit refusal in
  `ServeHTTP` if anyone wants it.
- (T4) Process: the T4 Notes and most T4 Discovered Issues entries were merged into this doc at
  730acee from an interrupted builder whose `proxy.go`/`proxy_test.go` were never committed; the
  code landed later as 421793d, reviewed and kept unchanged by a second builder.
- (T5) None beyond what the verifier already found — this was purely a test-strength fix, no new
  code-path bugs were discovered while adding the missing rows (all four new error cases already
  behave correctly per connections/spec.go).
- (T13) The idle-archive sweep (go/cmd/agentd/gc.go) snapshots a session and calls
  SetSnapshotHandle but never sets SnapshotState="archived"; SnapshotState is only ever read
  (go/agentdb/sessions.go:455,484, go/httpapi/lifecycle.go:133). This means the 'archived' half of
  sessionIsLive's live-session check can never actually trigger in production today — an expired
  session token for an idle-archived session is still honoured and forwarded with the real
  Anthropic key. Only the terminal Status=="error" half of the check currently provides
  protection. This was flagged by an earlier builder in the mcpserver.go sessionIsLive comment but
  the original docs/19-embedding.md H6 text stated the rule as fully working; this fix corrected
  the doc but did not fix the underlying sweep (out of T13's scope).
- (T6) T6 alone (before T11 lands) leaves go/gitproj's TestAllowlistCoversEveryField/Worker red because Worker gained a field with no allowlist entry. Fixed here by pulling forward exactly the Render/NotImportable(true) decision T11's own ticket text already specifies for Connections, and updating the paired pinned-set test (TestNotImportableFieldsArePinned) to include it — done deliberately, with rationale, per the 'never weaken a test silently' rule. T11 will still own the full render+import wiring (gitimport.go) and can build on this entry rather than re-adding it.
- (T6) TestLivePG_QueryEventsMixedPreAndPostMigrationRows (query_events_order_live_pg_test.go) did NOT fail in any live-Postgres run performed in this session, on a freshly created database (bob_t6) against the shared bob-conn-pg container. The verifier's finding #4 says it fails elsewhere ('backfill changed the replay order') and reproduces on a fresh bob_vt6_base database — a genuine pre-existing issue unrelated to T6's changes, just not one that reproduced here; still unresolved and worth someone checking for run-order/flakiness. (Merge step: it DID fail at merge time on fresh databases, both on the merged tree — got [new one old one old two] — and on the pre-merge base 4aae41c — got [old one new one old two]; the differing orders say the post-backfill tie-break is nondeterministic. Pre-existing, not caused by T6.)
- (T6) The design doc's Discovered Issues Log was intentionally left untouched per instructions (a separate merge step owns design/2026-09-11-project-connections.md) — everything that would normally go there is recorded only in this structured result.
- (T7) None found beyond what the ticket already specified. One judgment call worth flagging for the merge step: the existence check in PutWorker validates only names present in the body's `connections` (i.e. when body.Connections != nil), not the full stored+kept value — so a worker that already holds a name later removed from the project map (ConnectionNames no longer returns it) is not retroactively invalidated by an unrelated PUT that omits `connections` entirely. This matches the keep-on-absent philosophy of every other field on this route (an omitted field is never re-validated) and seems intended, but T14's operator guide or T9's connection_list tool may want to say explicitly that a stale/removed connection name can linger on a worker row until someone edits `connections` directly.
- (T7) The 403 gate compares only the FINAL requested list against the stored list (order-sensitive whole-list equality), not a set-difference against what specifically changed — a caller reordering the same names (e.g. ["gmail","github"] vs stored ["github","gmail"]) would trip the 403 for an API key/scoped caller even though no grant actually changed. This is analogous to agentdb's own workerConfigEqual/jsonValueEqual precedent (also order-sensitive) so I judged it consistent with the codebase's existing convention rather than a bug, but it's a minor UX rough edge if some future writer (e.g. the git importer or a UI) doesn't canonically sort connection lists before sending them.
- (T9) Behaviour change for existing deployments: project_prompt_write now requires the calling worker to hold "*". An architect chartered BEFORE this change has connections = NULL and so loses project_prompt_write, which go/orgprompts/architect.md tells it to use. T10 only fixes new charters; T14's one-off `PUT /agent/workers/architect {"connections":["*"]}` step is what restores it for existing projects. It is worth a callout in docs/22 and in the architect upgrade note.
- (T9) A human chat session reaching /mcp (no worker) can no longer call project_prompt_write, and can only change workers that hold no connections. That follows Decision 3 ("a caller with no worker holds nothing"), but it narrows the §8.8 "talk to Bob to set up your first worker" bootstrap: a person chatting can still create workers (without connections) and edit connection-less ones, but must use the console HTTP API to write the project prompt or grant connections. T14 should say so.
- (T9) Anyone may strip any worker's connections, including the architect's "*" (a pure removal is exempt from the steering rule by design). After that, only a console login can re-grant. This is the intended trust rule, not a bug, but it is an easy denial-of-reach any worker can do, and it deserves one line in docs/22's security notes.
- (T9) Because CanGrant compares literal lists, narrowing a worker from ["*"] to ["github"] counts as granting github, not as a removal, so a caller without github is refused and must remove to [] instead. This is conservative and consistent with T2's CanGrant, but a model may find it surprising.
- (T9) A disabled worker whose row holds connections is treated as holding nothing, so it cannot edit even itself if it holds connections (the steering check fails against its own list). Rare, and consistent with the proxy's 'disabled holds nothing'.
- (T9) Pre-existing, not touched: `gofmt -l go/cmd/agentd` flags go/cmd/agentd/gitbackfill_test.go as unformatted on the feature branch tip (df091c4).
- (T11) Rule.NotImportable in go/gitproj/allowlist.go is purely documentary today — no non-test code reads it. The real enforcement mechanism is the separate, hand-maintained `notImportable` map in go/gitproj/parse.go, keyed by bare frontmatter string (not by (struct, field)). This is a latent drift risk: a future NotImportable rule added to allowlist.go (for any of the six guarded structs) will pass TestAllowlistRulesAreWellFormed and TestNotImportableFieldsArePinned yet do nothing at the actual import door unless someone remembers to also add the matching key to parse.go's map. Worth a follow-up ticket to either (a) derive parse.go's notImportable set mechanically from allowlist.go's Rule.NotImportable flags across all six guardedStructs, or (b) add a cross-check test asserting the two sets agree, so the next NotImportable field added anywhere fails loudly instead of silently doing nothing.
- (T11) docs/21-git-projection.md documents the four/five ProjectSettings not-importable git_* fields in its field table but says nothing about Worker.Connections being not-importable on import. Not fixed here (docs weren't in this ticket's file list and design/2026-09-11-project-connections.md itself is explicitly off-limits to me), but a reader of docs/21 alone would not learn that editing a worker's connections in git has no effect.
- (T11) gitimport.go's generic 'ignored git configuration' notice text ('git configuration is not importable; ignored: %s', built in the shared loop at gitimport.go around line 304) now also fires for a dropped 'connections' key with the same wording ('git configuration is not importable; ignored: connections'), which reads oddly since connection grants are not git configuration. Left as-is because it's the one shared code path already used for all NotImportableFields()-dropped keys and rewording it was not in this ticket's scope; the TestGitImportConnectionsFieldIsIgnored test only asserts the notice mentions 'connections', not the exact sentence.
