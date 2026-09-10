# 19 — Embedding Agent Bob in another application

This is the document you read if you are building an application that uses Agent Bob as its
agent runtime and does **not** want to read any Go. It covers the three credentials, the config
ops has to set, the routes your backend calls, the iframe you drop into your page, and the three
patterns for keeping state between runs.

The driving use case is **Agent Wolf** — a product with its own UI, its own vocabulary
("hypotheses", not "sessions") and its own user allowlist, which stores none of the agent state
itself. Orange owns prompts, sessions, schedules, memories and artifacts; Wolf owns the page.

> **Read [§ Known hazards](#known-hazards) before you ship.** Several of them are live, deliberate
> and shipping-as-documented — in particular, an embed token carries more authority than its name
> suggests.

**Preconditions, both hard:**

- **Postgres only.** Session names live in migration `035_session_names`
  (`go/agentdb/migrations.go:743`) and session-mode schedules in `036_schedule_target_session`
  (`:771`). The sqlite fallback store has no `name` column, so every named-session route answers
  **501** there (`go/httpapi/session.go:75`, `go/httpapi/sessions_byname.go:104`,
  `go/cmd/agentd/embedtoken.go:111`). `DATABASE_URL` must be set.
- **Not available in dev-open mode.** The whole flow needs a project credential, and configuring
  a project API key is exactly what turns dev-open off (`devOpen`, `go/cmd/agentd/auth.go:118`). A zero-config
  demo stack cannot exercise any of this.

---

## 1. The three credentials

| Credential | Lifetime | Where it lives | Sent as | Grants |
| --- | --- | --- | --- | --- |
| **Project API key** | Long-lived; rotated by editing an env var and restarting | `agentd`'s environment, named by the project map. **Server-side only** | `X-API-Key: <raw>` | Full API access to one project |
| **Embed token** | 900s by default, clamped to `[60, 3600]` | The browser, in memory, arriving in a URL **fragment** | `Authorization: Bearer <jwt>` | One session on session-by-id routes — **but read hazard H1 below** |
| **Console JWT** | 12h | `localStorage` on the Orange origin (unchanged) | `Authorization: Bearer <jwt>` | Full API access to **one** project |
| **Dataset download token** — *minted by agentd, never held by you* | 300s by default, clamped to `[60, 900]` | Inside a session container, in a URL agentd handed the agent | `?token=<jwt>` **in the query string** | One dataset's bytes on one route — **and read hazard H14 below** |

**The three in the title are the three you hold and mint.** The fourth row is agentd's own and is
listed because the same middleware accepts it: it is **minted by agentd, never held by you**, it
exists so an agent inside a container can `curl` one dataset without being handed the project's API
key ([`20-datasets.md`](20-datasets.md) § 7), and — uniquely in this tree — **it travels in a URL**,
which is why it has a hazard of its own (**H14**). A fifth class is also not yours: the per-session
container token, covered in § 5.1 and **H12**.

Login mints **one JWT per project** the user's email maps to — `mintProjectTokens` issues a token
per project id, each carrying a single `customer` claim (`go/cmd/agentd/googleauth.go:354-367`).
The browser holds the set and picks one; no single console JWT spans two projects.

**One middleware accepts all four** (`apiAuthMiddleware`, `go/cmd/agentd/auth.go:117`). It tries
`X-API-Key` **first**; a bad key is a `401` and never falls through to the bearer path or to dev-open
(`auth.go:120-125`) — a silent downgrade on a typo is how a project ends up authenticated as someone
else. The `?token=` leg sits **after** the key branch and **before** dev-open, and is gated to one
request shape (`datasetDownloadPath`, `auth.go:71-90`; the leg at `:152-166`). An API key's principal
email is the synthetic `api-key:<project>` (`apiKeyEmail`, `auth.go:246`); this matters for
`GET /agent/sessions` (see the hazards).

*(Cite the symbol, not the line: the three citations in this paragraph all drifted once when ~55
lines were inserted at the top of `auth.go`, and pointed at unrelated constants for a while.)*

The console JWT is minted with a 12h TTL by `/auth/google` and `/auth/password`
(`loginIssuer`, `go/cmd/agentd/main.go:546`). Nothing in this document changes it.

---

## 2. Ops config: the project map

There is deliberately **no `api_keys` table and no key-management UI**. A key is ops config, like
the map that names it. The map is read from `AGENTKIT_PROJECT_MAP` (inline JSON, wins) or
`AGENTKIT_PROJECT_MAP_FILE` (path to a mounted file) — `go/cmd/agentd/googleauth.go:221-235`.

The historic flat form is still accepted unchanged:

```jsonc
{ "kai@badcode.dev": ["wolf", "demo"] }
```

The new **object form** adds per-project config:

```jsonc
{
  "users": {
    "kai@badcode.dev": ["wolf", "demo"]
  },
  "projects": {
    "wolf": {
      "api_key_env": "WOLF_API_KEY",
      "allowed_origins": ["https://wolf.badcode.dev"]
    }
  }
}
```

Then, in `agentd`'s environment:

```sh
WOLF_API_KEY=$(openssl rand -base64 24)
```

> **On the standalone compose stack this is not enough.** `docker-compose.yml:102-103` forwards only
> `AGENTKIT_PROJECT_MAP` and `AGENTKIT_PROJECT_MAP_FILE` into the `agentd` service — **not** arbitrary
> per-project key variables. Putting `WOLF_API_KEY` in `.env` therefore leaves the project keyless,
> and `agentd` says so once at boot (`project "wolf": WOLF_API_KEY is unset or empty`). Add the
> variable to the `agentd` service's `environment:` block, or mount a map file and name a variable
> compose already forwards. In a real deployment (Cloud Run, k8s) the environment is yours and this
> does not arise.

**Rules, all enforced at boot:**

- The two forms are told apart by the **shape of the values**, not by key names — an email key
  can legitimately be `users@…`. Legacy values are arrays, object-form values are objects; a file
  mixing both is an error, not a guess (`googleauth.go:73-102`).
- Unknown top-level keys in the object form are an error (`googleauth.go:117`).
- Project ids must be kebab-case, ≤64 chars (`googleauth.go:134`).
- `api_key_env` must be a plausible env var name, `^[A-Za-z_][A-Za-z0-9_]*$` (`googleauth.go:61,137`).
- Each `allowed_origins` entry must be `scheme://host[:port]` with **no path, query, fragment,
  userinfo or trailing slash**; `https` except loopback, which may be `http`. `"*"` is refused
  (`googleauth.go:187-208`).
- A **key shorter than 24 characters is a boot error**, and so are two projects resolving to the
  same key value (`go/cmd/agentd/apikey.go:36,85-92`). Key values are trimmed, so a trailing
  newline from an `env_file` is harmless (`apikey.go:79`).
- A project whose `api_key_env` is unset or empty simply has **no key**; that is logged once and
  never fatal (`apikey.go:80-84`).
- A project listed under `projects` that no user is a member of is **legal** — that is exactly
  the API-key-only integration shape.
- Changing `allowed_origins` requires an `agentd` restart. The map has no reload path.

`allowed_origins` drives **`Content-Security-Policy: frame-ancestors`**, not CORS. There is no CORS
anywhere in Go, by design: every hop is arranged so the browser never makes a cross-origin request
to `agentd` (the embed page is served by Orange, so its calls are same-origin; your backend →
Orange is server-to-server; artifact bytes are proxied by your backend to your own origin).

---

## 3. Create a named session

```http
POST /agent/session
X-API-Key: $WOLF_API_KEY
Content-Type: application/json

{ "name": "hypothesis-a" }
```

`name` is optional and joins the existing create body (`go/httpapi/session.go:17-31`). Everything
else about the route is unchanged.

- **kebab-case, `^[a-z0-9]+(-[a-z0-9]+)*$`, ≤64 chars** (`go/agentdb/sessions.go:163,166`).
- **Unique per project.** The same name in two projects is fine.
- **Immutable.** There is no rename route and no rename store method — `Session.Name` is tagged
  `<-:create`, so no UPDATE this store emits carries the column. A name you hand to a third party
  is a promise.
- Statuses: `409` name taken, `400` malformed, `403` credential carries no project, `501` no name
  store (sqlite). All of them are decided **before** anything is provisioned, so a refused name
  leaves no row and no container (`session.go:63-90`).

Response is the usual `{id, status, workflowId}`.

**Look it up again by the name you chose:**

```http
GET /agent/sessions/by-name/hypothesis-a
X-API-Key: $WOLF_API_KEY
```

```json
{
  "id": "…", "name": "hypothesis-a", "customer": "wolf",
  "status": "running",
  "created_at": 1754400000, "updated_at": 1754400600
}
```

`job`, `persona`, `title` and `create_error` carry `omitempty`
(`go/httpapi/sessions_byname.go:58-60,66`), so a plain chat session's body is exactly the six keys
above — do not code against their presence.

The body deliberately **omits `composed_prompt`** — this is the one session route an embed token
is meant to reach, and a composed prompt is the project's system prompt plus its memory briefings
(`go/httpapi/sessions_byname.go:48-69`). `403` when the credential carries no project; `404` for
absent, malformed **and** other-project alike, so the route is not a membership oracle
(`sessions_byname.go:107-129`).

---

## 4. Attach a session-mode schedule

A schedule targets **either** a worker (each firing dispatches a fresh job in a fresh container)
**or** a session by name (each firing sends `input` to that existing session as its next message).
Never both, never neither — the store enforces the XOR (`go/agentdb/schedules.go:207-220`).

```http
POST /agent/schedules
X-API-Key: $WOLF_API_KEY

{ "target_session": "hypothesis-a", "cron": "0 7 * * *", "input": "Check overnight moves and update your read." }
```

Body fields: `worker`, `target_session`, `cron`, `input`, `enabled` (pointer — absent means
default true), `rationale` (`go/httpapi/schedules.go:49-60`). `201` on create, echoing the stored
row.

Behaviour of a session-mode firing:

- The name is resolved **before** the firing is claimed. `ErrSessionNotFound` **disables** the
  schedule (mirroring the worker-not-found rule); any other read error is logged and retried next
  minute.
- **Archived session?** It is restored first — files, workspace and conversation history all come
  back — inside the ordinary `SendMessage` path. No extra call is needed.
- **Turn already in flight?** The firing is **skipped and recorded, never queued** — a stale "good
  morning" delivered at 11am is worse than one not delivered at all. A busy session is *not*
  counted as a failure (`go/cmd/agentd/scheduler.go:449-455`). If the status **cannot be read**,
  the firing is skipped *and* counted, so a session that can never be inspected eventually retires
  the schedule rather than skipping forever in silence (`scheduler.go:440-448`).
- Cron is a standard **5-field** expression evaluated in the stack's local time (`TZ` on `agentd`,
  default UTC). Nicknames like `@daily` are refused — write `0 0 * * *`.
- Firings missed while `agentd` was down are **skipped, not replayed**.
- A failed send counts toward the existing five-consecutive-failure disable streak
  (`scheduler.go:481`) — a `SendMessage` error means the turn could not be *delivered*; what the
  model then does comes back as events, not as an error. A successful send clears the streak.

The `schedule_create` MCP tool also accepts `target_session`. `schedule_update` deliberately does
**not** — a mode switch through a partial-field update is the ambiguous case — while the HTTP PUT
does accept it (`go/httpapi/schedules.go:198-199`). The two management surfaces are asymmetric
here on purpose.

### What waking a session does and does NOT refresh

**This is the thing people get backwards.** Read it twice.

When a schedule (or your backend) sends a message to an existing session, and the session had been
archived, it is restored first: **files, workspace and conversation history all come back**
(`go/runner.go:1425-1440`). Then:

| | Chat session (created via `POST /agent/session`, empty `composed_prompt`) | Dispatched job session (created by the router/scheduler through `ComposeJob`) |
| --- | --- | --- |
| **System prompt** | **Re-resolved from live config on EVERY turn.** Edits to the project prompt or to the worker prompt it names via `persona` **do** reach it at the next message | **Frozen.** The `composed_prompt` written at dispatch is that session's prompt verbatim, for life |
| **MCP tool set** | **Never refreshes** | **Never refreshes** |
| **Briefing** | **Never has one** | Built once at composition; not rebuilt on resume |

The prompt rule is one function with two cases: a non-empty `composed_prompt` on the row wins
verbatim, otherwise the host's session-context provider resolves the prompt per turn
(`go/runner.go:1914-1944`, called at `go/runner.go:885-888`). `composed_prompt` is written only by
`persistComposition`, which fires only when a create request carries a `Worker` — i.e. only for
dispatched jobs (`go/runner.go:536-549`).

The **tool set** is fixed when the container is provisioned: the create request's MCP config is
persisted onto the row (`go/runner.go:370,504-517`) and re-supplied verbatim on restore
(`go/runner.go:1433-1436`). Adding a tool to a project does not reach a session that already
exists.

**Briefings** are a composition-time concept only: `BuildBriefingSections` (`go/compose.go:191`) has
exactly one caller, the dispatcher (`go/cmd/agentd/dispatch.go:309`). A chat session has never had
one and waking it does not create one.

**Therefore: the way fresh state reaches a long-lived session is the memory tools at message
time.** Write what you learned into a memory, and tell the session — in the schedule's `input` —
to read it with `memory_current`. Do not try to deliver state by editing a prompt and expecting a
resumed session to have absorbed it.

---

## 5. Mint an embed token and frame the session

### 5.1 Mint (server-to-server)

```http
POST /agent/embed-token
X-API-Key: $WOLF_API_KEY

{ "session": "hypothesis-a", "ttl_seconds": 900 }
```

```json
{ "token": "eyJ…", "expires_at": 1754400900 }
```

- **API-key auth only.** A console JWT gets `403`, and an embed token cannot mint another
  (`go/cmd/agentd/embedtoken.go:102-105`). A browser must never be able to mint its own.
- `session` is a **name**, never a uuid.
- `ttl_seconds` is **clamped, never rejected**: absent or `0` → 900, `<60` → 60, `>3600` → 3600
  (`embedtoken.go:48-52,75-87`).
- `expires_at` is **unix seconds**, read back off the token that was just signed, so your idea of
  when to re-mint and the server's idea of when to refuse agree exactly.
- `404` for an unknown or foreign name, whatever the reason. `403` if the credential carries no
  project. `501` if session names or `AGENTKIT_JWT_SECRET` are not configured.

The token carries a `scope: "session:<id>"` claim. Its `sid` claim is deliberately **empty**, and
that is still load-bearing even though the reason has changed since it was written.

- **It used to be the whole defence.** `agentd` once signed each container's own session token with
  the *same* secret, and the core MCP server authenticates its caller by exactly the `sid` claim —
  so an embed token carrying a `sid` would have been a working credential for the project's memory
  and worker-prompt tools, handed to a browser inside a third-party page.
- **The secrets are now separate** (`go/cmd/agentd/sessionsecret.go`; `main.go:152-153`; **H12**),
  and `/mcp` verifies with the *session* key (`main.go:601`), so an embed token — signed with the
  API key — cannot authenticate there whatever its `sid` says. ⚠️ **With one exception**: a
  deployment that sets `AGENTKIT_SESSION_JWT_SECRET` to the **same value** as `AGENTKIT_JWT_SECRET`
  collapses the two classes again. agentd prints a `WARNING` at boot when it detects exactly that.
- **The empty `sid` is now the second of two locks, not the only one.** The API middleware also
  rejects any token carrying a non-empty `sid` with **401** (`go/cmd/agentd/auth.go`), so an embed
  token that grew one would simply stop working on the routes it exists for.

Confinement rides on the `scope` claim either way — it is the claim built for the question "what
may this token touch" — and **scope is enforced only on session-by-id routes** (**H1**). *(The
comment at `go/cmd/agentd/embedtoken.go:158-172` still describes the pre-split world and is stale;
the code around it is correct.)*

### 5.2 Frame it

```html
<iframe
  src="https://orange.badcode.dev/embed/session/hypothesis-a#token=eyJ…"
  style="width:100%;height:640px;border:0"
  title="hypothesis-a"></iframe>
```

The token is in the **fragment**, which browsers never send to a server: it appears in no access
log and in no `Referer`. The page reads `location.hash`, `history.replaceState`s it away before
first paint, holds the token in memory, and never writes it to `localStorage` or `sessionStorage`
(`examples/web/src/EmbedSession.tsx:37-47`). It then resolves the name to an id with
`GET /agent/sessions/by-name/{name}` using only that token, and mounts the chat — no sidebar, no
project picker, no login gate. On `401`/`404` it renders a plain "session unavailable" card rather
than redirecting to login (a login redirect inside someone else's iframe is both useless and a
phishing surface).

The URL form is `/embed/session/{name}`. nginx serves it from `embed.html`, a second Vite entry
point beside the console (`deploy/web.nginx.conf:19-35`).

### 5.3 Framing control

`agentd` computes the `Content-Security-Policy: frame-ancestors` value at boot from the project
map and nginx copies it onto the static page via an `internal` `auth_request` to
`GET /embed/csp` (`deploy/web.nginx.conf:26-28,41-46`, `go/cmd/agentd/embedcsp.go:44,83-99`).

- **The value is the UNION of every configured project's origins**, deduped and sorted. The request
  identifies no project and cannot be made to: the URL carries no project segment, the only
  credential is in the fragment, and resolving name → project would require a cross-tenant name
  lookup that deliberately does not exist (`embedcsp.go:14-32`).
- **No origins configured → `frame-ancestors 'none'`, emitted rather than omitted.** An absent CSP
  means "frame me anywhere". On the default stack (`.env.example` sets no `AGENTKIT_PROJECT_MAP`)
  the embed page is therefore **not framable at all** — a real `<iframe>` test needs a project map
  with origins.
- It is logged at boot as `[agentd] embed framing: …`, so a misconfiguration is visible without a
  browser (`go/cmd/agentd/main.go:463`).

---

## 6. Fetch artifacts (the proxy pattern)

Your backend fetches bytes with the API key and re-serves them from **your** origin. That is the
whole reason there is no CORS and no signed-URL machinery **on the browser-facing paths**. (One
signed-URL mechanism does exist, and it is not for browsers: the dataset download token, minted for
an agent inside a container — [`20-datasets.md`](20-datasets.md) § 7.)

```
GET /agent/sessions/by-name/hypothesis-a/artifacts                    → metadata list
GET /agent/sessions/by-name/hypothesis-a/artifacts/file?path=summary.md → raw bytes
GET /agent/artifacts/{id}/download                                    → raw bytes
```

Route patterns at `go/httpapi/httpapi.go:359-360,365`, registered at `:473-475`; implemented in
`go/httpapi/artifacts_download.go`.

Artifacts dedup on `(session_id, file_path)`, so **a path is a stable logical handle**: `summary.md`
upserts rather than accumulating, and "the current summary for `hypothesis-a`" is genuinely
addressable. Leading slashes are normalised — `summary.md` and `/summary.md` resolve to the same
artifact (`artifacts_download.go:146-156`).

**Only `?path` is read.** A session id in the query string is ignored on purpose: tenancy rides
entirely on the name having been resolved first (`artifacts_download.go:41-49,117-118`).

Response headers on a successful byte read (`artifacts_download.go:198-217`):

| Header | Value |
| --- | --- |
| `Content-Type` | the artifact's `MimeType`, or `application/octet-stream` when empty |
| `X-Content-Type-Options` | `nosniff` |
| `Content-Disposition` | `attachment; filename="<basename of FilePath>"` |

`attachment` is a security decision, not a UX one: an agent can write an artifact containing HTML,
and rendering it inline on Orange's origin would be scripting with the console's session in reach.
There is no `Content-Length` — `FileSize` is metadata written by a different call than the bytes,
and a stale value would truncate the response.

**Status mapping when there are no bytes** (`artifacts_download.go:237-259`), checked in this
order:

| Condition | Status | Meaning |
| --- | --- | --- |
| `status = lost` | **410** | the container was destroyed before extraction; no retry will help |
| `status = extraction_failed` | **409** | there are no bytes to serve |
| `IsDir` | **409** | it is a directory; list the session's artifacts instead |
| `status = live` | **202** | registered but not yet extracted — **come back later** |
| otherwise | **410** | extracted, but the blob is gone |

A missing or foreign artifact is **404 with the body `not found`** — byte-identical to the
ownership refusal, so an embed token cannot distinguish "exists but belongs to a sibling session"
from "no such id" (`artifacts_download.go:181,186`). The one exception is the by-name **file**
route's own lookup miss: when no row matches the `?path`, it answers `404` with the body
`artifact not found` (`artifacts_download.go:137`) — tenancy is already settled there by resolving
the name, so the distinguishable message leaks nothing. `501` if the host has no by-path artifact
index (the sqlite fallback).

---

## 7. Read memory over HTTP — and the one write

Four routes (`go/httpapi/memories.go`), three reads and one append:

| Route | Returns |
| --- | --- |
| `GET /agent/memories?selector=&query=&limit=` | `{"memories": [...]}` — search results carrying a **`snippet` cut at 500 bytes** and no `content` field at all |
| `GET /agent/memories/{id}` | one memory **in full** |
| `GET /agent/memories/current?name=<n>` | the **newest** memory labelled `name=<n>`, in full |
| `POST /agent/memories` | **201** and the appended row — the trust anchor, below |

The last two are what T18 added, and they are what makes "render the current state from memory"
possible: before them, full content was reachable only from inside a container through the
`memory_get` / `memory_current` MCP tools.

Full-read body shape (`memories.go:166-173`):

```json
{
  "id": "…",
  "labels": {"name": "hypothesis-a-state", "kind": "research-state"},
  "content": "…the whole thing, untruncated…",
  "created_by_worker": "reviewer-a",
  "created_by_session": "…",
  "created_at": 1754400000000
}
```

`created_at` is **unix milliseconds** here (`go/agentdb/memories.go:127` writes
`time.Now().UnixMilli()`) — unlike `session_list`'s timestamps, which are seconds. The units differ
across the product surface, so read the field's own note rather than assuming.

- Project scope always comes from the credential, never from a parameter.
- `current` **requires** `name`; a bare `name=` would match every row with no name label and hand
  back an arbitrary memory (`memories.go:236-241`). The name must be a legal label value, which is
  also what stops a comma or `=` smuggling a second term into the selector (`memories.go:246-249`).
- Unknown name or id → **404 `memory not found`**, one string for absent, malformed and
  other-project alike. A store outage is **500**, not 404.
- `403` when the credential names no project; `501` when the memory store is not Postgres.

### The append route — `POST /agent/memories`

This is how an embedding application holds state of its own. Until it shipped, the only write
surface was `memory_create` on the core MCP server, authenticated by a **session token an embedder
does not hold** — so an application embedding Orange could hold no authoritative state at all.

```http
POST /agent/memories
X-API-Key: $YOUR_PROJECT_KEY
Content-Type: application/json

{ "labels": {"kind":"hypothesis","name":"1a2b3c4d","status":"live"},
  "content": "…", "embed": false }
```

- **Auth: a project API key or a console JWT, and nothing else.** An **embed token is 403** (`403`
  on `Identity.SessionScope`); a **session token is 401** before this handler runs, rejected by
  agentd's middleware on its non-empty `sid` (see **H12**). A credential that reaches a container
  must never be able to mint state the application will read back as its own.
- **201 Created**, body = the stored row read back from the database: `id`, `labels`, `content`,
  both provenance fields, `created_at` in unix **milliseconds**.
- **Provenance is server-stamped EMPTY** and the body cannot influence it. A body carrying
  `created_by_worker` or `created_by_session` is **rejected 400** — including `""` and `null` —
  rather than ignored: silently dropping them would leave the caller believing it had attributed
  the memory. **Empty provenance is what marks a row as the application's own word**; anything
  written from inside a container carries a worker name or a session id. (Necessary, not
  sufficient — `POST /agent/topologies/apply` also writes provenance-free memory seeds, so pin the
  label vocabulary you trust as well.)
- **Project comes from the credential.** There is no project field.
- Labels go through the existing validator; a bad label is **400** with the validator's own message.
  Blank content is **400**.
- **`embed` defaults to `true`.** Content over **24576 bytes** with `embed: true` is **400** naming
  the `embed: false` remedy; the hard ceiling for any memory is **1048576 bytes** and is checked
  first, so a caller 2MB over is not told to retry and then refused again.
- **501** when the memory store is not Postgres — or when the database has no column to hold an
  embedding.
- **No config event is written.** A memory is data, not configuration.

Still append-only: no `PUT` and no `DELETE` anywhere on these routes. "Changing" a memory is
appending a newer one.

### Seeing what was withdrawn — `?include_retracted=1`

`retracts=<id>` is an ordinary label, and the SQL that hides retracted rows correlates on the
**retracted row's id and project only — it never checks who wrote the retraction**. So anything
holding the core MCP tools can withdraw your application's own authoritative row from every search
path, and with the filter always on that erasure is indistinguishable from the row never having
existed. A `latest_per=` query then quietly hands back the *older* row beneath it.

`GET /agent/memories?selector=…&latest_per=…&include_retracted=1` is the audit view:

- Retracted rows come back, each carrying **`retracted_by`** — **every** retraction against it,
  newest first, each with its own `memory_id`, `created_by_worker`, `created_by_session` and
  `created_at`. All of them, not just the newest: a reader honours a withdrawal only if *some*
  retraction of the row has empty provenance, and with only the newest an attacker can stack its own
  retraction on top of a legitimate one and resurrect state you took back.
- Rows nothing retracts omit the field entirely, so an ordinary search is byte-identical to what it
  was before.
- **Auth: key or JWT only. `403` for a session-scoped (embed) credential** — handing the audit view
  to an actor with a container's reach tells it exactly which erasure was noticed.
- Any value other than exactly `1` (including present-but-empty) is **400**.

`GET /agent/memories/{id}` is deliberately **not** retraction-filtered: asking for an id is an
explicit request for that row, and reading what was withdrawn is the point of not deleting anything.

---

## 8. The two-bot pattern

Agent Bob offers **three** ways to carry state between runs, and an application-layer builder
picks. Do not collapse them.

| Strategy | Mechanism | Suits |
| --- | --- | --- |
| **Memory** | Labelled, append-only rows; `memory_current(name)` returns the newest match; injected per-job via a worker's `briefing` selectors | Product-layer workers, where every scheduled tick spawns a fresh session and container by design |
| **Session snapshot** | The archive loop snapshots an idle session and releases its container; the next message restores the filesystem *and* rehydrates conversation history | Long-lived workspaces where **files are the state** |
| **Dataset** | A project-scoped, named, **versioned blob** written under compare-and-swap by the `dataset_put` tool and read over four HTTP routes; bytes never pass through a tool result. [`20-datasets.md`](20-datasets.md) | A numeric series many fresh containers rewrite — where the *bytes* are the state and putting them in a memory would cost tokens daily |

The recommended shape for a user-facing, continuously-researched thing is **two atoms plus project
memory as the system of record**:

| Atom | Kind | Role |
| --- | --- | --- |
| `hypothesis-a` | Long-lived **named session**, user-facing | The conversation surface your iframe shows. Reads current state from memory **at message time**; holds no authoritative state of its own |
| `reviewer-a` | **Worker + daily schedule**, fresh session per tick | Researches the world, writes the updated state as a memory, and where warranted rewrites `hypothesis-a`'s prompt |

Why this and not "one hypothesis = one session forever": a session resumed daily for a year
accumulates transcript, and a *dispatched* session's composed prompt is frozen at creation. The
split gives you a clean researcher and a durable conversation.

**Everything this needs already exists. Do not rebuild any of it:**

- **Memory is a genuine shared bus.** Project-scoped, append-only, no per-worker permissions and
  no origin check: any worker writes, any other worker's briefing reads. **No cross-container
  file-mutation tool is needed, and none should be added** — it would duplicate memory with worse
  provenance and no history.
- **History of a name is already queryable.** `memory_search` with selector `name=<x>` and no
  query returns every version newest-first; `memory_current` takes only the newest.
- **One agent rewriting another's prompt is built and guarded.**
  `worker_prompt_write(name, system_prompt, rationale)` — rationale mandatory, superseded prompt
  auto-stored as a `kind=prompt-revision` memory, frozen workers refuse.
- **Session→memory archiving is a policy, not a feature.** `worker.finished` carries the full
  verbatim transcript, and `kind=rolling-summary,worker=<subject>` is the *default* briefing
  selector — so wiring an archivist worker is a prompt and a subscription, not a migration. None
  is wired in this repo.

### How your page reads state

Two options; both work, and which you use is a product choice.

1. **Memory** — `GET /agent/memories/current?name=hypothesis-a-state` with the API key, and render
   `content`. Have `reviewer-a` write each update as a memory labelled
   `name=hypothesis-a-state,kind=research-state`. This is the route for prose of any length.
2. **Artifacts** — have the session write `summary.md` into its workspace and fetch
   `GET /agent/sessions/by-name/hypothesis-a/artifacts/file?path=summary.md`. This is the route
   for files, CSVs and images.

Do **not** try to render state from `GET /agent/memories` alone: those results carry a 500-byte
`snippet` and no full content.

### Making the long-lived session able to read memory

A session created through `POST /agent/session` now launches with the **core MCP tools** merged
over the project's and the worker's, core winning any name collision
(`go/httpapi/session.go:177,244-256`, wired at `go/cmd/agentd/main.go:313-316,323`). That is what
lets `hypothesis-a` call `memory_current` when a scheduled message tells it to.

⚠️ **There is no backfill.** A session created before this shipped restores forever with its old,
empty MCP set — the config is fixed when the container is provisioned and re-supplied from the
persisted row on restore. **A long-lived session that needs core tools must be created fresh.**

### What a chat session may do with the core tools

**Everything a worker may do.** A session created over HTTP has no `worker` — that column is
written only for dispatched jobs — but it is not thereby a second-class caller. Both kinds reach
the same core MCP server, authenticated the same way, with the same tools.

The only difference is **how the change is attributed**, and it is a record, not a restriction:

| Caller | `config_events.actor_worker` | Rendered in the changelog as |
| --- | --- | --- |
| a dispatched job | the worker's name | that worker's decision |
| a chat session (a human driving it) | *empty*, plus the session id | a human edit — §15.2's meaning of an empty actor |
| a session whose row cannot be read | — | **refused**; the actor would be a guess |

That last row is the invariant (RD4, `go/cmd/agentd/mcpserver.go`): attribution is never guessed.
An empty actor from an *identified* session is a fact — this session has no worker — and is written
as the human edit it is. An empty actor from a session nobody could read might be concealing a
worker, so nothing is written at all.

Two consequences worth knowing:

- **You can bootstrap a project by talking to it.** `worker_create`, `schedule_create`,
  `subscription_create`, `project_prompt_write`, `image_create` and `skill_create` all work from a
  chat session, so a project with no workers can gain its first one conversationally rather than
  only through the console.
- **A chat session's config edits are more traceable than the console's**, not less: the console's
  human edit records no session, while this one records the conversation that produced it.

There is deliberately **no per-project capability layer** yet — no way to say "humans in this
project may not delete schedules". That is a real feature and a separate one; it is not implied by
attribution, and it was not smuggled in as a side effect of it.

### `session_list`: provenance for agents

The new core MCP tool `session_list(worker?, limit?)` lets an agent enumerate a worker's recent
runs — id, name, status, `created_at`/`updated_at` (unix **seconds**), `session_url`,
`artifact_count`, `message_count`, `attention_requested`, `create_error`. Default limit 10, max 50.

**No transcripts, ever.** "What did I conclude last time" is a memory question.

It lists **job** sessions only: the `worker` column is written only for dispatched jobs, and a chat
session carries its worker name in `persona` instead. So `session_list` called with no arguments
**from a chat session correctly returns an empty list** plus a note saying so. From your long-lived
session, name the worker explicitly: `session_list(worker: "reviewer-a")`.

---

## 9. Identity: `POST /auth/verify-google`

If your app already uses Google Sign-In, Orange will verify an ID token for you rather than making
you duplicate the verification logic and the client id.

```http
POST /auth/verify-google
X-API-Key: $WOLF_API_KEY

{ "credential": "<google id token>" }
```

```json
{ "email": "someone@example.com", "email_verified": true }
```

- **API-key auth only** — `403` otherwise. It is a token-verification oracle and answers only a
  project's backend (`go/cmd/agentd/googleauth.go:444-449`).
- Registered on the **authenticated** mux, and only when `GOOGLE_CLIENT_ID` is set; otherwise the
  route **404s** (`googleauth.go:493-498`, `go/cmd/agentd/main.go:425`). This is the one thing the
  original design prose got wrong: putting it beside `/auth/google` on the root mux would have
  shipped an unauthenticated verification oracle.
- Every failure — bad signature, wrong audience, unverified address — is one `401
  invalid credential`.
- It **mints nothing, grants nothing, and creates no user row.** Orange is not becoming an identity
  provider: your app owns its allowlist.

---

## 10. Route summary

Everything an embedding backend touches, in one table.

| Method + path | Auth | Notes |
| --- | --- | --- |
| `POST /agent/session` | key or JWT | optional `name`; 409/400/403/501 |
| `GET /agent/sessions/by-name/{name}` | key, JWT, **or a matching embed token** | omits `composed_prompt` |
| `GET /agent/sessions/by-name/{name}/artifacts` | key, JWT, **or a matching embed token** | metadata list |
| `GET /agent/sessions/by-name/{name}/artifacts/file?path=…` | key, JWT, **or a matching embed token** | raw bytes |
| `GET /agent/artifacts/{id}/download` | key, JWT, or matching embed token | raw bytes |
| `POST /agent/schedules` | key or JWT | `target_session` XOR `worker` |
| `GET /agent/memories?selector=&query=&limit=` | key or JWT | **snippets only** |
| `GET /agent/memories/{id}` | key or JWT | full content |
| `GET /agent/memories/current?name=<n>` | key or JWT | full content, newest |
| `POST /agent/memories` | **key or JWT only** | 201; provenance stamped empty, a body supplying it is 400; embed token 403, session token 401 |
| `GET /agent/datasets?selector=&limit=` | key or JWT | `{"datasets":[…]}`, one row per name at its current version |
| `GET /agent/datasets/{name}` | key or JWT | current version's metadata, **bare object** |
| `GET /agent/datasets/{name}/versions?limit=` | key or JWT | `{"versions":[…]}`, newest first |
| `GET /agent/datasets/{name}/download?version=&token=` | key, JWT, **or a matching scoped dataset token in `?token=`** | raw bytes; 410 when the row is there and the blob is gone |
| `POST /agent/embed-token` | **key only** | `{session, ttl_seconds?}` → `{token, expires_at}` |
| `POST /auth/verify-google` | **key only** | needs `GOOGLE_CLIENT_ID`, else 404 |
| `GET /embed/session/{name}#token=…` | fragment token | static page served by nginx |
| `GET /embed/csp` | none (nginx-internal) | 204 + the `frame-ancestors` header |

`agentd` registers `/auth/verify-google`, `/agent/embed-token` and `/embed/csp` **directly on its
muxes**, outside `httpapi`'s route table — so `httpapi.Handlers.Mux()` is no longer a complete
picture of the surface.

### Environment variables

| Variable | Effect |
| --- | --- |
| `DATABASE_URL` | Postgres. **Required** — every route above is Postgres-only |
| `AGENTKIT_PROJECT_MAP` / `AGENTKIT_PROJECT_MAP_FILE` | The project map (inline JSON wins over the file) |
| *(the var named by `api_key_env`)* | e.g. `WOLF_API_KEY` — the raw key value. ≥24 chars |
| `AGENTKIT_JWT_SECRET` | Signs and verifies console JWTs **and** embed tokens. Without it, embed-token minting answers 501 |
| `GOOGLE_CLIENT_ID` | Enables `/auth/google` and `/auth/verify-google` |
| `AGENTKIT_PUBLIC_BASE_URL` | Externally reachable base for permalinks (`<base>/p/<project>/s/<session>`) |
| `AGENTKIT_SESSION_IDLE_TIMEOUT` | Default `30m` — idle sessions are snapshotted and their container released; the next message restores them |
| `AGENTKIT_PORT_RANGE_START` / `_END` | The concurrent-session ceiling per host (default 100) |
| `AGENTKIT_DATASET_MAX_BYTES` | Per-write cap on a dataset pulled out of a workspace. Default `67108864` (**64 MiB**), given as a plain integer count of bytes |
| `AGENTKIT_DATASET_REAP_INTERVAL` | How often the dataset version reaper and orphan blob sweep run. Default `6h`; **`0` (or `off`) means never sweep**. Bounded to `[1m, 30d]` |
| `AGENTKIT_DATASET_KEEP_VERSIONS` | Versions kept per `(project, name)`. Default `30`; an integer **count**, max `10000`. ⚠️ `0` here means **keep everything**, the opposite of `0` on the interval above |

Datasets, in full: [`20-datasets.md`](20-datasets.md).

---

## Known hazards

> **Before you connect an outward-facing tool**, read the human-valve pattern in
> [`workflows.md`](workflows.md): an approval gate that lives inside your MCP server is the
> only control in this area that does not depend on the model choosing to obey a prompt.

Each one is real, each one is named with `path:line`, and each was a deliberate decision to ship
documented rather than to fix in this plan. Read all of them.

### H1 — ⚠️ HIGH: an embed token carries **project** authority, not session authority

The credential table says an embed token grants one session. The **mechanism** —
`Identity.SessionScope` checked inside `ownsSession` (`go/httpapi/lifecycle.go:211-218`, helper at
`:245-247`) — is reachable only from routes that take a session id. An embed token still carries
`customer: <project>` (`go/cmd/agentd/auth.go:104-114,123-130`), so for its life it can also call
every project-wide route: `PUT /agent/workers/{name}`, `PUT /agent/project-settings`,
`POST /agent/events`, the schedule CRUD, `GET /agent/memories` and both full-content memory reads
(none of those handlers takes a session id, so `ownsSession` never runs).

⚠️ **Two routes now refuse a scoped credential directly, without `ownsSession`** — they check
`Identity.SessionScope` themselves and answer **403** (`go/httpapi/memories.go`): the memory append
`POST /agent/memories`, and `GET /agent/memories?…&include_retracted=1`. So an embed token **cannot
mint trusted state and cannot read the audit view**, which is the pair this hazard would otherwise
hand it. That is a per-route patch on the two places it mattered most, **not** the project-wide rule
below — everything else in the paragraph above is unchanged.

That credential is handed to a browser inside a third-party page. **This is a real privilege
escalation bounded only by the token's TTL** — which is why the TTL ceiling is one hour
(`go/cmd/agentd/embedtoken.go:51`). Closing it means a new project-wide rule ("a scoped identity
may touch only session routes"), which is a design decision, not an implementation detail.

**Do not mint an embed token for a third party you would not trust with the project**, and prefer
the 900s default over the ceiling.

### H2 — the CSP is bound to the `/embed/` **path prefix**, not to the document

`curl -D- http://localhost:8080/embed.html` returns 200 with **no** `Content-Security-Policy`
header: that URI falls through to `location /` (`deploy/web.nginx.conf:19,49-51`). Framing
protection therefore depends on the framer using the `/embed/session/{name}` form. Direct
`/embed.html` renders nothing useful without a name in the path, so the exposure is small — but it
is not zero.

Related nginx traps, both fixed here and worth knowing anywhere else:
`try_files $uri /file.html;` **discards `add_header` from its own block** (the URI fallback is an
internal redirect that re-matches `location /`), which is why the line reads
`try_files $uri /embed.html =404;` (`deploy/web.nginx.conf:30-34`); and nginx **silently skips
`add_header` when the value is empty**, which fails OPEN — hence the `map` that turns an empty
upstream value into `'none'` (`deploy/web.nginx.conf:1-8`).

Also: `auth_request` couples a static asset's availability to `agentd`. If `agentd` is down, the
embed page 500s rather than rendering the "session unavailable" card.

### H3 — the artifact iframes are `sandbox="allow-scripts allow-same-origin"`

`web/src/components/ArtifactViewer.tsx:609` and
`web/src/components/InlineArtifactPreview.tsx:315`. That pair of tokens **cancels the sandbox**:
agent-authored content runs scripts with the console's own origin, within reach of the console JWT
in `localStorage`. Pre-existing, and more dangerous once third parties are pointing browsers at
this deployment. Not fixed by this plan.

(`ArtifactViewer.tsx:622` uses the correct `sandbox="allow-scripts"` for the other case.)

### H4 — the `/webapp/{session}/{token}/{path}` serving route does not exist

Nothing in `go/` serves it. The raw JWT was removed from the URL the console constructs
(`web/src/components/ArtifactViewer.tsx:595`, `InlineArtifactPreview.tsx:189`), but the route
itself was never built. Webapp artifacts are broken in the deployed stack and remain so.

### H5 — the `workspace/files` route does not exist either

`/agent/session/{id}/workspace/files/{path}` is built at **nine** call sites across three
components — `ArtifactViewer.tsx:168,209,602`, `InlineArtifactPreview.tsx:54,90,113,193`,
`ArtifactPreviewDialog.tsx:104,157` — and `grep -rn 'workspace/files' go/` finds only a comment
describing an *in-image* endpoint. This is larger than H4 and equally untouched. A tenth call site,
`ArtifactPreviewDialog.tsx:113`, calls `GET /agent/artifacts/{id}/preview-url`, which also does not
exist (it degrades gracefully to download, so it is cosmetic).

A note on inline images, because it looks like a tenth hazard and is not:
`getArtifactImageUrl` (`InlineArtifactPreview.tsx:50-55`) builds a bare
`/agent/artifacts/{id}/download` URL, but that URL is never handed to an `<img src>`. It is
`fetch`ed with an `Authorization` header (`InlineArtifactPreview.tsx:256-259`) into an object URL,
and the `<img>` renders *that* (`:265,:273`). Browsers do not attach bearer tokens to subresource
loads, so the blob-URL indirection is what makes authenticated images work at all — do not
"simplify" it away.

### H6 — the unauthenticated `/agent-proxy/` carries the real Anthropic key

`go/cmd/agentd/main.go:507` mounts it on the **root** mux, outside `apiAuthMiddleware` (which is
registered on `"/"` at `:539` and therefore never sees the more specific prefix).
`go/cmd/agentd/modelproxy.go:39-58` shows it forwards to `api.anthropic.com` when
`ANTHROPIC_API_KEY` is set, and `go/modelproxy/modelproxy.go:146` stamps that key onto the upstream
request — while `modelproxy.go:30`'s own doc comment says "Mount under your auth middleware", which
`agentd` does not do. It is there because session containers reach `agentd` through
`AGENTKIT_SELF_URL` and authenticate with a per-session token the proxy does not check. Anyone who
can reach `agentd`'s port directly can spend the key. Pre-existing; more dangerous once the
deployment is a shared singleton. **Do not expose `agentd`'s port beyond the container network.**

Narrowed on 2026-08-13 by the credential-precedence inversion: `CLAUDE_CODE_OAUTH_TOKEN` now
outranks `ANTHROPIC_API_KEY`, and in subscription mode sessions call `api.anthropic.com` directly,
so nothing routes through the proxy. `newModelProxyHandler` therefore serves the **mock** whenever
the OAuth token is set — a stack with both credentials no longer mounts an unauthenticated relay
onto a real billing key that nothing uses. The hazard stands unchanged for API-key (proxy) mode.

### H7 — sessions created **before** T15 never gain core tools

MCP config is fixed at provisioning (`go/runner.go:370,504-517`) and re-supplied from the persisted
`agent_sessions.mcp_servers` column on restore (`go/runner.go:1433-1436`). There is no backfill and
no route that rewrites a session's MCP config. A pre-existing long-lived session that needs
`memory_current` **must be recreated**.

### H8 — an API key sees an **empty** `GET /agent/sessions`

`ListSessions` filters on the principal's email unless `?user_email=*` is passed
(`go/httpapi/history.go:111-115`). An API key's email is the synthetic `api-key:<project>`
(`go/cmd/agentd/auth.go:120`), which matches no session row — so the list comes back `[]` rather
than the project's sessions. Not a bug; the documented lookup path is
`GET /agent/sessions/by-name/{name}`. But if you want the list:

```http
GET /agent/sessions?user_email=*
X-API-Key: $WOLF_API_KEY
```

### H9 — the whole flow is unavailable in dev-open mode, and Postgres-only

`POST /agent/embed-token` demands an API key, and a configured API key is exactly what turns
dev-open off (`go/cmd/agentd/auth.go:63`). Named creates and by-name lookup also need a project
credential. And session names exist only in Postgres. **Do not try this against a zero-config
demo stack.**

### H10 — the console cannot create or edit a session-mode schedule

`web/src/schedules.ts:321` hard-requires a non-empty `worker` client-side, so a session schedule
renders with a blank worker column and no way to set the target. **Session schedules are
API/MCP-only** for now.

### H11 — a session literally named `session` breaks the embed page's path parsing

`sessionNameFromPath` takes `segments.lastIndexOf("session")`
(`examples/web/src/EmbedSession.tsx:57`), so `/embed/session/session` resolves to the empty string.
`session` is legal kebab-case. Cosmetic; just do not use that name.

### H12 — ✅ CLOSED: the two credential classes are now separately signed, and `sid` is locked out

**This hazard used to read "the session-token secret and the API secret are the same value", and
that no longer describes the code.** It was true when written: agentd signed a container's
per-session token with `AGENTKIT_JWT_SECRET`, the same value the API middleware verifies bearer
tokens with, so a token a prompt-injected model could read out of its own container was a
structurally valid **full-project** credential on every route the middleware protects.

Two independent defences now stand, either of which alone closes it:

1. **Different signing keys** (`go/cmd/agentd/sessionsecret.go`; `main.go:152-153`). The session
   class gets its own key, **derived** from the API secret by HMAC-SHA256 under a fixed, versioned
   label. Derivation rather than a new mandatory variable was deliberate: no deployment has to
   change anything to get the fix, the derived key is a one-way function of the API secret, and
   rolling `AGENTKIT_JWT_SECRET` rolls both together. A deployment wanting to roll them
   independently sets `AGENTKIT_SESSION_JWT_SECRET`; setting it to the *same* value as
   `AGENTKIT_JWT_SECRET` re-opens the defect, and agentd says so loudly at boot.
2. **The `sid` lock** (`go/cmd/agentd/auth.go`, which marks it *"doc 22, RD30"* — that is the Go
   comment's own reference to a design log, **not** a `docs/22-*.md`; no such file exists). The API
   middleware independently
   rejects **any** token carrying a non-empty `sid` claim with **401**, before routing — belt and
   braces for a future issuer that stamps `sid` by accident.

Consequences for an embedder, both of which the rest of this document depends on: a session token
cannot reach `POST /agent/memories` at all (it is 401, not 403), and **empty provenance therefore
means "written by something holding project authority"** rather than "written by anything with a
token". Note that an **embed token is still API-class** — same secret, empty `sid` — and is confined
only by its `scope` claim (**H1**), so it is refused explicitly by the routes that mint or read
trusted state rather than by its signature.

*Upgrading a live deployment:* restart, no config change and no data migration. In-flight session
tokens minted by the previous binary stop verifying at the core MCP server; they carry a one-hour
TTL and are re-minted on every provision, restore and rehydrate, so the window is bounded by that
TTL and touches only the core tools of containers adopted across the restart.

### H13 — `GET /agent/memories/current` reserves the name `current` in the id path space

Harmless today because ids are uuids, but `CreateMemory` accepts a caller-supplied id, so a memory
created with the literal id `current` would be unreachable by id. Nothing in-repo supplies one.

### H14 — ⚠️ a dataset download token travels **in the query string**, where the rest of the tree never puts a credential

Every other credential here rides in a header (`X-API-Key`, `Authorization`) or in a URL **fragment**
that browsers never transmit (the embed token, § 5.2). The **dataset download token** is the one
exception in the whole tree: it is a bearer JWT in `?token=` on
`GET /agent/datasets/{name}/download` (`go/cmd/agentd/auth.go:152-166`). The reason is concrete — the
caller is a model's `curl` inside a container, curl cannot send a fragment, and a header would make
the model compose one from a URL it was handed — but the consequences follow the query string
wherever it goes:

- **Anything that logs a URL logs the credential.** `agentd` itself writes no access log, but nginx
  proxies `/agent/` (`deploy/web.nginx.conf:54-62`) and its default `access_log` format includes the
  query string, so a download fetched through the web origin lands in a log file with the token in
  it. So would any sidecar, load balancer, APM tracer or proxy an operator adds.
- **It reaches the model, and therefore the transcript.** The URL is returned by `dataset_get` as a
  tool result — see [`20-datasets.md`](20-datasets.md) § 10 for the full reach and the two caveats.
- **`Referer` is not a risk here** (nothing renders these URLs in a page), but it becomes one the
  moment anybody puts a download URL into HTML.

**The bound is the whole mitigation, and it is a bound rather than a barrier:** a **300s** default
TTL (ceiling 900s) and a scope pinned to one `(project, name)` pair — so a leaked token buys one
dataset's bytes for a few minutes, and cannot be replayed against another dataset or another project.
**Do not extend the TTL, do not log these URLs, and do not re-serve one to a browser** — proxy the
bytes from your own backend the way § 6 does for artifacts.

---

## See also

- `docs/18-workers-memory-events.md` — the product layer from an operator's seat: workers, memory,
  triggers, the core tools, the config log.
- `docs/20-datasets.md` — the dataset atom (the fourth credential class, the four read routes, the
  reaper) and the provenance trust rule behind `POST /agent/memories` in § 7.
- `docs/06-artifacts.md` — the artifact contract and the status state machine behind § 6.
- `docs/14-host-adapters.md` — the tenancy contract and the store seams.
- `design/2026-08-06-embeddable-agent-bob.md` — the plan this was built from, including the
  Discovered Issues Log every hazard above is drawn from.
