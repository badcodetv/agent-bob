# 20 — Datasets, and the memory append route

> **Who this is for.** An application embedding Agent Orange that needs (a) somewhere to keep a
> shared numeric time series that agents read and rewrite, and (b) a way to write state of its own
> that an agent cannot forge. Those are the two things Orange could not do before
> `design/2026-08-20-agent-wolf.md`, and this document is what shipped for them.
>
> Read [`19-embedding.md`](19-embedding.md) first for the credential model. Its § 1 tables the three
> an embedding application holds — **project API key**, **embed token**, **console JWT** — and § 5.1
> and hazard **H12** cover the fourth class, the per-container **session token**, which an embedder
> never holds. The **dataset download token** below is a fifth, minted by agentd for a container and
> accepted on one route (§ 7).
>
> **Postgres-only.** Every route, tool and sweep here needs `DATABASE_URL`. On the sqlite fallback
> the store returns `ErrDatasetRequiresPostgres` and the HTTP routes answer **501**
> (`go/agentdb/datasets.go`, `go/httpapi/datasets.go`).

---

## 1. The atom

A **dataset** is a project-scoped, named, versioned, labelled blob (`go/agentdb/datasets.go`, table
`datasets`, migration `045_datasets`).

| Property | Rule |
| --- | --- |
| **Project-scoped** | The project is the credential's `customer` claim, in code, always. There is no project parameter on any route or tool, and another project's dataset is indistinguishable from one that does not exist |
| **Named** | `name` uses the label-value charset — `[A-Za-z0-9]` plus `-`, `_`, `.`, at most 63 characters — and, unlike a label value, may not be empty (`ValidateDatasetName`) |
| **Versioned** | Every write creates a **new immutable version**, numbered from 1. Nothing is ever mutated in place |
| **"Current"** | The **highest version** for `(project, name)`. Not "the newest by timestamp" — the version column is the authority. `CurrentDataset` reads it as `Order("version DESC").First()` (`go/agentdb/datasets.go:324-327`); the CAS path reads the same high-water mark as `COALESCE(MAX(version), 0)` (`:227-230`) |
| **Labelled** | The same `LabelSet` memories use, the same validator, the same Kubernetes-style selector parser. Labels are **per version**, so a superseded version's labels never decide whether the current one matches a selector |
| **Provenance** | `created_by_worker` / `created_by_session`, stamped from the writing session's token — see § 9 |

Row shape on the wire (`datasetResp`, `go/httpapi/datasets.go`; the ten fields below `id` are
byte-identical in name to the `dataset_list` tool's output):

```json
{ "id": "ds-…", "name": "1a2b3c4d-drone-suppliers-basket", "version": 7,
  "labels": {"hypothesis": "1a2b3c4d", "metric": "drone-suppliers-basket"},
  "size_bytes": 40213, "row_count": 512, "sha256": "…", "content_type": "text/csv",
  "created_by_worker": "", "created_by_session": "sess-…", "created_at": 1789000000123 }
```

`created_at` is unix **milliseconds** — the `memories` table's unit, not the `agent_*` tables'
seconds. `blob_path` and `project` are **never serialised**: the first is an internal storage key
(handing it out invites a caller to build its own blob addresses), the second is already the
caller's own credential.

Why a new atom rather than a memory or an artifact: a memory's content crosses the model's context
on every read and write, and an artifact is deduped on `session_id + file_path`
([`06-artifacts.md`](06-artifacts.md)) while a scheduled worker gets a **fresh container every
tick**. Neither gives a series that many sessions can share and rewrite.

---

## 2. Compare-and-swap, mandatory on every write

`dataset_put` takes `if_version` and it is **not optional**.

- `if_version` must equal the dataset's **current** version.
- `if_version = 0` means **"this dataset must not exist yet"**.
- A mismatch writes nothing and returns an error **naming the real current version**, so the model
  can re-read that version, merge, and retry:

  > `nothing was written: "prices" is at version 7, not 5 — someone else wrote while you were
  > working. Re-read it with dataset_get(name: "prices"), merge your work into that version, and
  > retry with if_version: 7`

  and, for the create case: `"prices" does not exist, but if_version was 3. Pass if_version: 0 to
  create it`.

The authority is the **store**, not the tool's pre-read: `CreateDatasetVersion` re-reads the
high-water mark inside a transaction and inserts `if_version + 1`, and the unique index on
`(project, name, version)` decides the race if two writers pass that read together. A caller whose
write loses keeps the struct it passed, unstamped, and retries with a fresh id.

Without CAS, two sessions that both read v5 and both write would silently lose a day of data.

---

## 3. The two byte paths

**Bytes never travel through a tool result, in either direction.** Two transports, neither of which
is the model's context:

| Direction | Mechanism | Why this one |
| --- | --- | --- |
| **Write** | `dataset_put(name, path, if_version)` names a file under `/workspace`; agentd pulls it out of the running container with `Exec` + `cat`, hashes it and stores the blob (`go/cmd/agentd/datasetpull.go`) | It is exactly what `onArtifactRegistered` already does, and `Exec` is the only file seam on the execution-environment interface. Nothing new had to be invented on the container side |
| **Read** | `dataset_get(name)` returns a **short-lived, single-dataset** `download_url`; the agent `curl`s it to a file | Containers already reach agentd on `AGENTKIT_SELF_URL`. No push-into-container plumbing, and no second credential in the image |

The pull pipeline is deliberate about failure. `test -f` probes existence; `wc -c` probes size
**before** anything is read, because the exec buffers the whole of stdout in agentd's memory and
agentd serves every session; `cat` pulls the exact bytes (binary-safe, never re-encoded or
line-split); the cap is re-checked against the bytes actually pulled, so a file that grows between
the probe and the read is refused and its blob rolled back. A `cat` on a missing file exits 1 with
empty stdout, so **every exec checks the exit code** — copying the artifact path's `err != nil`-only
handling would silently store a 0-row dataset.

Paths are validated before any exec: relative to `/workspace` only (an absolute path is a rejection,
not a synonym), no `..` escape, no NUL or newline, no `.git` segment. Symlinks are explicitly not
resolved — a path string cannot decide that — so `test -f` following the link is the only identity
check a file gets.

**Rejected alternatives**, with the reason each was rejected:

- **Cross-session artifact hand-off.** Works today with zero engine changes, but caps out on
  unbounded transcript growth in a session resumed daily for a year, and enforces a single writer.
- **A blob store in the embedding application.** Keeps machinery out of Orange, but makes the
  embedder a stateful service and produces nothing reusable for the next one.
- **MCP tools that return bytes.** Defeats the purpose: an MCP result *is* model context.
- **A cumulative-snapshot memory.** The whole series would cross the context window daily, and a
  model retyping it can mangle it silently.

---

## 4. The canonical CSV, and `row_count`

Every writer and reader agrees on this shape:

```
timestamp,value
2026-08-19T00:00:00Z,141.22
2026-08-20T00:00:00Z,143.90
```

Header **exactly** `timestamp,value`. Timestamps RFC3339 in **UTC**, **ascending**, **LF** line
endings, **one metric per dataset**, no trailing blank line. Nothing enforces this and the failure
is silent: a `t,value` header makes a reader parse zero observations out of a legitimately written
dataset, and no error surfaces anywhere. The `dataset_put` tool description states the format to the
model verbatim.

**`row_count` is `max(0, N-1)`**, where N counts `\n`-terminated runs plus a final unterminated run
when the last byte is not `\n` (`countCSVRows`, `go/cmd/agentd/datasetpull.go:219`). So `"h\na\nb"`
and `"h\na\nb\n"` both give 2, header-only gives 0, empty gives 0 — **never negative**: the column is
`NOT NULL` and the shrink guard compares against it. CRLF still counts; the `\r` stays in the bytes.

`row_count` is **counted server-side from the pulled bytes and never supplied by the caller**, and it
is only counted for `text/csv` — parameters are allowed, so `text/csv; charset=utf-8` still counts
(`isCSVContentType`, `go/cmd/agentd/datasetpull.go:205-212`). `content_type` defaults to `text/csv`;
anything else stores `row_count = 0`, which also switches the shrink guard off for that dataset.

---

## 5. The shrink guard

`replace-whole` has a failure mode CAS cannot catch, because it is the *same writer*: a provider
outage returning a truncated history replaces a year of data with a stub, at a perfectly valid next
version.

So `dataset_put` **refuses a replacement whose `row_count` is below 50% of the current version's**
unless the caller passes `allow_shrink: true` (`shrinkRefusal`,
`go/cmd/agentd/mcp_datasets.go:664`):

- The comparison is `2*new >= current` — integer arithmetic, so **exactly half is allowed** and there
  is no rounding to argue about.
- It applies **only** when a current version exists and its `row_count` is above zero. A first write
  has no predecessor, and a current version of 0 rows has nothing to shrink from.
- On refusal **nothing is written** and the freshly pulled blob is deleted.

The refusal names both counts and says what it suspects:

> `nothing was written: "prices" would go from 900 rows to 12, which is less than half. That is what
> a half-finished fetch looks like. Check the source, or pass allow_shrink: true if the series really
> did shrink`

`allow_shrink` is a caller assertion and nothing verifies it, so an embedder that cares should say in
the writing worker's prompt what a legitimate shrink looks like and require it to be filed as a
research note.

---

## 6. The MCP tools

Three tools on the existing `core` MCP server (`go/cmd/agentd/mcp_datasets.go`, registered in
`main.go` alongside the memory, worker and image tools). They reach the model as
`mcp__core__dataset_*`, and they exist **only when `DATABASE_URL` is set** — the core server is not
mounted at all on the sqlite fallback.

| Tool | Contract |
| --- | --- |
| `dataset_list(selector?, limit?)` | One entry per dataset, at its **current** version. **Metadata only, never bytes.** Selector is the memory-search grammar, comma-ANDed. `limit` defaults to **20**, capped at **100** — and when the cap bites the result *says so* rather than truncating silently |
| `dataset_get(name, version?)` | Metadata plus a **`download_url`** and `expires_at` (unix seconds). Omit `version` for the current one. The description instructs the model: `curl -sS -o file "<download_url>"`, then read the file with its own tools; do not print the contents, and do not echo the URL |
| `dataset_put(name, path, if_version, labels?, content_type?, allow_shrink?)` | Pulls `path` (relative to `/workspace`) out of the calling session's own container and writes a new version under CAS. Returns the row **read back from the database** — `name, version, size_bytes, row_count, sha256` — never the arguments it was given |

Two properties of `dataset_put` worth stating separately:

- **It writes only from the calling session's own workspace.** The session id comes from the token,
  never from an argument.
- **An unidentifiable caller gets no write at all** — never a write with empty provenance. Empty
  provenance is how an embedding application recognises state *it* wrote (§ 9), so a row written from
  inside a container must never be able to look like one.

The `download_url` is built on `AGENTKIT_SELF_URL` (the DinD bridge address a nested container can
reach) and never on `AGENTKIT_PUBLIC_BASE_URL` (the browser-facing address, unreachable from inside a
container). Its token is minted for **one `(project, name)` pair and no version**, with a **300s**
default TTL clamped to `[60s, 900s]` (`go/cmd/agentd/datasettoken.go`) — so a URL minted while a name
is at v3 still resolves to that name's requested version after a tick takes it to v4.

---

## 7. The HTTP routes

Four **read** routes (`go/httpapi/datasets.go`, registered in `go/httpapi/httpapi.go`). There is no
HTTP write and no HTTP delete: bytes are pulled out of a container's workspace, so a write has no
sensible request body, and removal is the reaper's (§ 8).

| Route | Auth | Returns |
| --- | --- | --- |
| `GET /agent/datasets?selector=&limit=` | key or JWT | `{"datasets": […]}` — one row per name, at its current version |
| `GET /agent/datasets/{name}` | key or JWT | the current version's metadata, as a **bare object** (no envelope) |
| `GET /agent/datasets/{name}/versions?limit=` | key or JWT | `{"versions": […]}`, **newest first** |
| `GET /agent/datasets/{name}/download?version=&token=` | key, JWT, **or a matching scoped dataset token in `?token=`** | the raw bytes |

`limit` on the two list routes defaults to **20** and is capped at **100** in the store (the same
house numbers memory search uses).

**Status vocabulary**, copied from the artifact download route:

| Status | When |
| --- | --- |
| **404** `dataset not found` | absent, malformed name, another project's, `?version=` non-numeric or `< 1`, or a dataset token presented against a different name — **one sentence for all of them**, because a dataset name is chosen by whoever wrote it and is guessable in a way a uuid is not, so any distinguishable answer is a project-membership oracle |
| **410** | the row exists and its **blob is gone** — a well-formed question whose answer is "this existed and no longer does", which an operator needs to see as different from 404 |
| **400** | a malformed selector, reported with the parser's own message (list route only) |
| **403** | the credential names no project — the question cannot be asked at all |
| **501** | no dataset store wired, or a store that is not Postgres. On the download route alone, also: no blob store wired (metadata still answers) |
| **500** | a store failure. Deliberately **not** 404 — a database refusing connections is not a missing row |

Byte responses set `Content-Type` from the row (`application/octet-stream` if the row has none),
`X-Content-Type-Options: nosniff`, and `Content-Disposition: attachment` — attachment for a security
reason, not a UX one: a dataset holds whatever an agent wrote, HTML included, and rendering it inline
on the API's origin would be scripting with the console's session in reach. No `Content-Length` is
set: `size_bytes` is written by a different call than the bytes, and a stale value truncates the
response.

**The `?token=` leg is the one place in the tree where a credential arrives in a URL**, and it is
accepted on exactly one request shape: `GET /agent/datasets/<exactly one segment>/download`
(`datasetDownloadPath`, `go/cmd/agentd/auth.go`). A query parameter rather than a header or a
fragment because the caller is a model's `curl` inside a container: curl cannot send a fragment, and a
header would make the model compose one. Verification failure falls through to the ordinary 401 with
no explanation, and the token's *name* is compared to the path in `httpapi` — so a mismatch is 404
rather than a 401 that confirms the name exists.

---

## 8. Retention, the orphan sweep, and the blob prefix

Dataset bytes live in the blob store under one constant:

```go
// go/agentdb/datasets.go:370
const DatasetBlobPrefix = "_datasets/bytes/"     // then a uuid4 per WRITE ATTEMPT
```

> 🔴 **This constant is load-bearing for data safety, and this is the paragraph to read before
> touching any sweep.** `agentd` runs **ONE global `BlobStore`**, shared with `_artifacts/bytes/`
> **and with session snapshot bytes**. An enumeration under an empty or wrong prefix would therefore
> return **every artifact and every snapshot blob in the deployment** — and a sweep that deletes what
> it enumerates would delete them. There is no second bucket and no separate namespace protecting
> you.
>
> Both sides of the seam re-assert the prefix immediately before every `Delete` —
> `agentdb.ReapDatasetVersions`, `agentdb.ListOrphanBlobPaths` **and** `cmd/agentd`'s reaper loop —
> and that redundancy is deliberate: either side alone being wrong would be enough to lose the
> deployment's artifacts. A row whose `blob_path` lacks the prefix is **skipped, never deleted**.

The key is a fresh uuid4 **per write attempt**, never derived from the dataset name or version, so
two concurrent pulls of the same dataset cannot collide.

**Version reaping.** `ReapDatasetVersions(keepPerName, blobs)` keeps the newest `keepPerName` versions
of each `(project, name)` and deletes the rest — **blob first, then row**, so a failure leaves an
orphaned blob (recoverable, § below) rather than a row pointing at nothing. `keepPerName <= 0` is a
no-op on any backend: **0 means keep everything**. The two delete failures are handled differently and
the asymmetry is deliberate: a **blob** delete failure stops further reaping of that same name, lets
other names continue, and is returned once every name has been attempted, so the next pass retries the
remainder; a **row** delete failure — the blob is already gone and the row is not — is a graver,
systemic failure and **halts the whole sweep immediately** (`go/agentdb/datasets.go:563-571`). The
count returned is exactly what was deleted either way.

**The orphan sweep** finds blob keys under the prefix that **no row references**. It is genuinely
two-pass, and the safety lives in the caller, not the store: a blob key carries no timestamp, so
`agentdb` can only ever say which paths are unreferenced *right now* — deleting on the strength of one
pass would eat a `dataset_put` that has written its bytes but not yet committed its row. So
`go/cmd/agentd/datasetreaper.go` deletes a path only after seeing it orphaned on **two separate sweeps
at least one hour apart** (`datasetOrphanMinAge`). A path that stops being reported is *forgotten*, not
deleted — there is no "orphaned once" debt that outlives the write which cleared it.

Both run on one ticker inside agentd, started only when `DATABASE_URL` is set.

> ⚠️ **The reaper deletes evidence.** With the defaults, the oldest surviving version of a daily
> series is about 30 days old. If a decision was made from a dataset — a verdict, a trip, an alert —
> the embedding application must **snapshot the numbers it decided on into a memory or an artifact at
> the moment it decides**. The dataset is working storage; it is not the permanent record. A provider
> restatement can also make a past condition silently stop being true, with nothing left to show it
> ever was.

---

## 9. Trust: what "empty provenance" means, and the memory append route

This is the mechanism an embedder holding authoritative state is expected to use. It is the reason
`POST /agent/memories` exists.

Orange's memory is a genuine shared bus: project-scoped, append-only, **no per-worker permissions and
no origin check**, with labels chosen entirely by the caller. So an embedder cannot derive
authoritative state from "the newest memory with these labels" — a worker, a chat session, or a
prompt-injected web page read during research could write exactly that row.

**The rule:**

> **Provenance is stamped by the server from the credential's class and cannot be supplied by the
> caller.** A memory with `created_by_worker == ""` **and** `created_by_session == ""` was written by
> the application over HTTP. Anything written from inside a container carries a worker name or a
> session id, and is therefore **advisory**.

### `POST /agent/memories`

```http
POST /agent/memories
X-API-Key: $YOUR_PROJECT_KEY
Content-Type: application/json

{ "labels": {"kind":"hypothesis","name":"1a2b3c4d","status":"live"},
  "content": "…", "embed": false }
```

| Fact | Detail |
| --- | --- |
| **Auth** | A **project API key or a console JWT only**. An **embed token is 403** (it is API-class but is minted for a browser inside a third-party page, so it reaches exactly where a container's output reaches). A **session token never arrives at all**: it is signed with a different key and the middleware rejects any token carrying a non-empty `sid` with **401** before routing |
| **Success** | **201 Created**, body = the stored row read back from the database (`id`, `labels`, `content`, both provenance fields, `created_at` in unix **milliseconds**) |
| **Provenance** | Stamped **empty**, always. A body carrying `created_by_worker` or `created_by_session` is **rejected 400** — including `""` and `null` — rather than ignored: silently dropping them would leave the caller believing it had attributed the memory |
| **Project** | From the credential. There is no project field |
| **Labels** | The existing validator; a bad label is **400** with the validator's own message |
| **`embed`** | Defaults to **true**. Content over **24576 bytes** with `embed: true` is **400** naming the `embed: false` remedy; the hard ceiling for any memory is **1048576 bytes**, checked first so a caller 2MB over is not told to retry and then refused again |
| **Blank content** | **400** |
| **Off Postgres** | **501** — as is an embedding this database has no column to hold |
| **Config log** | **No config event is written.** A memory is data, not configuration; every append appearing in the changelog would bury a project's actual configuration history within a week |

Still append-only: no `PUT`, no `DELETE`, anywhere on this surface. "Changing" a memory is appending
a newer one.

### The two hazards an embedder must handle

🔴 **1. `ApplyTopology` is a second producer of empty provenance.** `POST /agent/topologies/apply` is
reachable by any API-class credential — **including an embed token**, because
`ApplyTopologyHandler` calls `identify` and applies **no `SessionScope` gate**
(`go/httpapi/topologies.go:230-234`), unlike `POST /agent/memories` and `?include_retracted=1`, which
both refuse one explicitly. That is the credential an embedder hands to a browser inside a
third-party page. Its memory seeds are written with no provenance
(`go/agentdb/topology_apply.go`). Today no shipped topology can seed an arbitrary label set — the one
that seeds memory uses fixed `{kind: registry, name: label-registry}` and the request body cannot
supply its own — so there is no live forgery path. But **empty provenance is necessary, not
sufficient**: a reader that depends on it must also pin the label vocabulary it trusts, and ideally
anchor identity in something that cannot be created from inside a container at all (a **named
session** is the usual choice: creating one needs an API key or a console JWT).

🔴 **2. A retraction is not provenance-checked.** `retracts=<id>` is an ordinary label, and the SQL
that hides retracted rows (`notRetractedSQL`) correlates on the **retracted row's id and project
only — it never checks who wrote the retraction**. It is applied to both `NewestMemory` and
`SearchMemories`. So anything holding the core MCP tools can withdraw the application's own
authoritative row from every search path, and with the filter always on that erasure is
indistinguishable from the row never having existed. Worse, a `latest_per=` query then hands back the
*older* trusted row underneath, which passes the trust rule and renders as authoritative with no
warning.

**That is what `?include_retracted=1` exists for:**

| | |
| --- | --- |
| **Route** | `GET /agent/memories?selector=…&latest_per=…&include_retracted=1` |
| **Effect** | The retraction filter is omitted, and every returned row carries **`retracted_by`**: **all** retractions against it, newest first, each with its own `memory_id`, `created_by_worker`, `created_by_session` and `created_at`. Omitted entirely for a row nothing retracts, so an ordinary search is byte-identical to what it was before |
| **Auth** | Project API key or console JWT. **403** for a session-scoped (embed) credential — handing the audit view to an actor with a container's reach tells it precisely which erasure was noticed |
| **400** | Any value other than exactly `1`, including a present-but-empty parameter |

**All** retractions and not merely the newest, deliberately: a reader decides whether to honour a
withdrawal by asking whether *any* retraction of the row has empty provenance. With only the newest,
an attacker appends its own retraction on top of a legitimate one, the reader discards that single
untrusted retractor, and state the application properly took back is resurrected.

Note also that `GET /agent/memories/{id}` is **deliberately not** retraction-filtered: fetching a
specific id is an explicit request for that row, and reading what was withdrawn — and the retraction
that withdrew it — is the point of not deleting anything.

---

## 10. Bytes and the model's context — the guarantee, stated honestly

**The plan never *requires* dataset bytes to cross the model's context window.** Writes go out by
`Exec` + `cat`; reads come back as a URL the agent `curl`s to a file; tool results carry metadata
only. That is a real and useful property: a 200KB series can be rewritten daily without costing a
token, and without a model retyping — and silently mangling — the numbers.

It is **not** the same as "bytes can never appear there", and an embedder should not design as if it
were. Two caveats, both real:

1. 🔴 **The bytes stay out of context; the CREDENTIAL does not.** `dataset_get`'s `download_url`
   carries a **bearer token**, and it is returned **as a tool result**. It therefore enters the model
   context, the **persisted transcript**, the **SSE event stream**, and **every subscriber of
   `worker.finished`**. Anything that can read the conversation can fetch that dataset. **Assume it
   reaches every one of those.** The mitigations are the whole of the defence and they are bounds,
   not barriers: a **300s** default TTL (ceiling 900s) and a scope pinned to **one
   `(project, name)` pair**. The tool description tells the model not to echo the URL, which is a
   prompt and not an enforcement. One further bound, worth knowing and not worth relying on: the
   **rendered transcript** truncates each tool result to **200 characters**
   (`maxToolOutputChars`, `go/runner.go:2060`, applied in `toolOutcome`), and a `dataset_get` result
   spends most of that budget on metadata before reaching the URL — so a whole JWT rarely survives
   into a `worker.finished` subscriber's text. The persisted events and the live SSE stream are
   **not** truncated, and a 200-character cut is a rendering detail, not a security boundary.
2. **Nothing stops a model reading a downloaded file into its own context.** `head prices.csv`, a
   `df.head()`, a `cat` in a bash tool — all are ordinary agent actions and all put rows in the
   transcript. The tool description says not to, for the same reason and with the same force.

If an embedder needs a hard guarantee that a series never appears in a transcript, it must not hand
the series to a model at all — compute over it outside the agent and give the agent the conclusion.

---

## 11. Environment variables

Exactly three variables belong to datasets. All are read by `agentd` at boot; a bad value is a **boot
error naming the variable**, never a silent fallback.

| Variable | Default | Effect |
| --- | --- | --- |
| `AGENTKIT_DATASET_MAX_BYTES` | **`67108864` (64 MiB)** | The per-write cap on a pulled workspace file. A **plain integer count of bytes** — not a `64MiB`-style size string; the unit is in the name. Anything that is not a positive integer stops the boot. It is bounded because the exec that pulls the file buffers the whole of it in agentd's memory, so an unbounded pull is a memory DoS from inside a container |
| `AGENTKIT_DATASET_REAP_INTERVAL` | **`6h`** | How often the version reaper and orphan sweep run. A Go duration; `0` (or `off`, `none`, `never`, `disabled`) means **never sweep**. Bounded to `[1m, 30d]` — below the floor it promises a precision the loop does not have, above the ceiling it is a dropped unit rather than an intention. Needs `DATABASE_URL`; without one the loop is not started |
| `AGENTKIT_DATASET_KEEP_VERSIONS` | **`30`** | How many versions of each `(project, name)` the reaper keeps — roughly 30 daily ticks. An **integer count, not a duration**. Maximum `10000`. ⚠️ **`0` here means "keep every version" (never reap), the OPPOSITE of what `0` means for the interval above** — the two knobs read the same digit differently, so read this row twice before setting either to zero |

The Wolf-specific variables (`WOLF_API_KEY`, `WOLF_MCP_TOKEN`, `AGENTKIT_MCP_ENV`) are **out of scope
here** — they belong to that product's own `.env.example` and to
[`18-workers-memory-events.md`](18-workers-memory-events.md) § 11, and documenting them twice is how
two descriptions drift apart.

---

## 12. Known limits

- **Postgres only**, everywhere: the jsonb labels and the unique index CAS depends on. On sqlite the
  routes are 501 and the tools are not mounted.
- **No HTTP write and no HTTP delete for datasets.** An application outside a container can *read*
  every dataset but cannot create one; writes come from inside a session. If an embedder needs to seed
  a dataset, it must do so through a session.
- **`allow_shrink` is unverified.** It is the caller's assertion that the shrink is real.
- **Nothing enforces the canonical CSV.** A wrong header is accepted, stored, and read as zero rows by
  whatever parses it later.
- **Reaping is not archiving.** Versions past `AGENTKIT_DATASET_KEEP_VERSIONS` are gone, bytes and
  row alike — see the warning in § 8.

---

## See also

- [`19-embedding.md`](19-embedding.md) — the integration guide: credentials, named sessions, embed
  tokens, artifact and memory reads, and the hazard list.
- [`18-workers-memory-events.md`](18-workers-memory-events.md) — the product layer from an operator's
  seat, including the core-tools table these three tools joined.
- [`06-artifacts.md`](06-artifacts.md) — artifacts, the *other* byte plane, and why it is not this
  one.
- `design/2026-08-20-agent-wolf.md` — the design these were built from: § "The dataset atom",
  § "The trust model", and the ticket Notes recording what changed during implementation.
