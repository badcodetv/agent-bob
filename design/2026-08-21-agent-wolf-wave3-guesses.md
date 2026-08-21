# Agent Wolf wave 3 — the guesses log

> **What this is.** Every place a wave-3 implementer had to decide something
> `design/2026-08-20-agent-wolf.md` did not specify. Companion to
> `design/2026-08-21-agent-wolf-wave2-guesses.md`. Recorded 2026-08-21.

Executed: **O3, O4, W4, W7** from the dependency graph, plus two catch-up items —
**W1b** (closing W1's three escalated failures) and **W6b** (recording the real FRED
fixtures, unblocked by the owner supplying a key). All six passed adversarial
verification with **zero fix rounds**.

**60 guesses** recorded. The ones with cross-ticket consequences were promoted to the
plan's Discovered Issues Log as **R71–R83**.

| Ticket | Guesses |
| --- | --- |
| O3 | 5 |
| O4 | 5 |
| W1b | 5 |
| W4 | 17 |
| W6b | 4 |
| W7 | 24 |


---

## O3

### Reaper failure semantics: per-name halt vs whole-sweep halt

- **The plan is silent on:** The ticket says a Delete error "stops work on that name" and "returns a non-nil error naming the blob path", but the signature returns a single (int, error) and the plan never says explicitly whether OTHER names still get reaped in the same call.
- **The executor chose:** Continue reaping other (project, name) groups after one name's blob-delete failure; halt only that one name's remaining excess versions; remember the FIRST error and return it alongside the total successfully-deleted count once every name has been attempted. A row-DELETE SQL failure (as opposed to a blob-Delete failure) is treated as more severe and aborts the whole sweep immediately, returning what was deleted so far.
- **Why it matters:** The alternative reading (stop the entire sweep on the first blob failure) is simpler and defensible from the same sentence, but would mean one flaky blob backend call for a single stale version blocks GC for every other dataset name in the deployment — which seems like exactly the failure mode a background janitor should avoid. If O8 (which calls this) expects whole-sweep-abort semantics instead, this is the one place its retry/backoff logic needs to match my choice.

### Reap candidate processing order

- **The plan is silent on:** The plan does not say what order the reaper visits candidates in, only that it must never touch the highest keepPerName versions.
- **The executor chose:** ORDER BY project ASC, name ASC, version ASC — oldest-excess-version first within each name, names in a deterministic project/name order.
- **Why it matters:** This determinism is what makes the failure test (aaa fails, bbb still gets reaped) reproducible; a caller relying on any other order (e.g. newest-excess-first) would see different partial-completion behavior on a failure.

### ListOrphanBlobPaths' "known" query scope

- **The plan is silent on:** The plan says the known set is "present in the datasets table for any project" but doesn't say whether the query should itself filter by DatasetBlobPrefix before comparing.
- **The executor chose:** SELECT DISTINCT blob_path FROM datasets with no WHERE clause at all — every blob_path ever stored, prefix or not — then intersect against the lister's (already prefix-scoped) keys in Go.
- **Why it matters:** Functionally equivalent to filtering in SQL since every legitimately-written row's blob_path already carries the prefix, but slightly more defensive: if a row somehow got written with a non-prefixed blob_path, this still recognizes it as "known" and refuses to call it an orphan, rather than a WHERE clause silently excluding it from the known set and making it look orphaned by construction.

### Order of the nil-deleter/nil-lister check vs the dialect guard

- **The plan is silent on:** The ticket doesn't explicitly say whether the nil-blob-seam check must run before or after requireDatasetPostgres, only that the keepPerName<=0 no-op must be checked first ('before even the dialect guard').
- **The executor chose:** For ReapDatasetVersions: keepPerName<=0 first, THEN nil-deleter check, THEN dialect guard. For ListOrphanBlobPaths: nil-lister check, THEN dialect guard (no keepPerName-equivalent exists there).
- **Why it matters:** This lets 'nil deleter/lister is refused' be proven on the sqlite unit store (consistent with O2's 'argument validation before dialect check' rule), rather than only being provable live. If a later ticket assumes the opposite order, the error path (ErrDatasetRequiresPostgres vs the nil-seam error) would differ for a sqlite/dev-open caller.

### clampDatasetListLimit as a shared helper vs inlining the clamp twice

- **The plan is silent on:** The ticket says to reuse the constants, not whether to factor a helper.
- **The executor chose:** One small package-level clampDatasetListLimit(limit int) int wrapping defaultMemorySearchLimit/maxMemorySearchLimit, called from both ListDatasets and ListDatasetVersions.
- **Why it matters:** Purely a style choice; noting it only because it's a new small identifier a later ticket editing this file should know exists rather than re-deriving the clamp inline a third time.


---

## O4

### Test naming rule for devclaims tests

- **The plan is silent on:** The ticket says every test added under devclaims is named 'TestDatasetScope…', but the natural names for two of the negative-parse tests read better as TestParseDatasetScope… / TestParseSessionScope… (matching the existing TestParseSessionScopeRejectsNonSessionScopes convention already in the file)
- **The executor chose:** Renamed those two tests to start with the literal prefix TestDatasetScope (TestDatasetScopeNeverParsesAsSessionScope, TestDatasetScopeValueNeverParsesAsSessionScope) so the Validation's '-run TestDatasetScope' substring filter actually matches them, rather than following the file's existing TestParseSessionScope* naming convention
- **Why it matters:** The Validation filter is a substring match anchored at the start of the compared string in effect (go test -run matches anywhere in the name, but the ticket explicitly pins the prefix). Had I kept the more idiomatic TestParseDatasetScopeRejectsX names, '-run TestDatasetScope' would not match them and they'd silently not run under the filtered validation command, even though they'd still run in the whole-package validation — an easy way for a reviewer re-running just the filtered command to undercount coverage.

### mintDatasetToken's ContextScope.Job and UserEmail fields

- **The plan is silent on:** The ticket doesn't specify what Job/UserEmail values a dataset token's claims should carry beyond 'customer must equal project' and 'sid must be empty'
- **The executor chose:** Set Job: "dataset-download" and left UserEmail empty, since mintDatasetToken has no caller identity to attribute — the eventual O6b caller (an MCP tool handler) may have a principal to pass in, but this ticket's helper signature takes only (secret, project, name, ttlSeconds), matching the ticket's stated verifier signature `verifyDatasetToken(secret, raw) (project, name, err)` for symmetry
- **Why it matters:** If O6b (dataset MCP tools, not yet built) expects mintDatasetToken to accept a caller email or a different Job string, this is a mismatch it will need to either work around or ask for a signature change. I did not invent a UserEmail parameter since none of the acceptance criteria mention identity attribution for this token — only project+name scoping and TTL — but flagging it because embedtoken.go's mint path always threads through the API-key principal's email.

### mintDatasetToken function signature and placement of ttlSeconds as the last positional int (not a struct/request body)

- **The plan is silent on:** The ticket describes 'the mint helper' and 'verifyDatasetToken(secret []byte, raw string) (project, name string, err error)' precisely for the verifier, but never states the mint helper's exact signature
- **The executor chose:** func mintDatasetToken(secret []byte, project, name string, ttlSeconds int) (token string, exp int64, err error) — plain positional args mirroring embedTokenRequest's ttl_seconds field name and clampEmbedTTL's int parameter, since this ticket builds no HTTP route (no JSON body to decode) and the ticket says 'the mint helper' will be called by O6b's MCP tool code, not by an HTTP handler in this ticket
- **Why it matters:** O6b, which actually calls this to mint tokens from an MCP tool handler, will need to match this exact signature. If O6b's author expected a struct-based request or different parameter order, this is the seam most likely to need a small adjustment — but the shape follows the embedtoken.go precedent closely enough that I judged it a safe default rather than a coin-flip.

### verifyDatasetToken error messages leaking distinguishability

- **The plan is silent on:** The ticket says errors must be 'distinctly enough for O5 to answer 404 rather than leak a reason' but doesn't say whether the error TEXT itself should differ per failure mode or just the returned err being non-nil with any message
- **The executor chose:** Gave each of the 7 failure modes its own distinct Go error message (for debugging/logging purposes) but documented in a comment that O5 must not pass err.Error() through to a client response — the distinctness is for server-side logs, not for what a caller sees
- **Why it matters:** If O5's author reads 'distinctly enough' as license to echo the message text back to callers, that would leak which check failed (e.g. distinguishing an expired token from a wrong-secret token) to an attacker probing the download route — which is the opposite of the security posture the ticket's own wording implies ("O5 to answer 404" — one answer, not seven). I made this a code comment rather than raising it as a discovered issue because it's about how O5 should USE this function, and O5's own ticket text already says route-level 404 behavior is O5's responsibility.

### TestDatasetTokenVerifyRejectsNonHS256 construction

- **The plan is silent on:** The ticket says to test 'a token signed with a non-HS256 method' but doesn't specify which alternate algorithm to construct the test token with
- **The executor chose:** Used jwt.SigningMethodNone with jwt.UnsafeAllowNoneSignatureType (the 'alg: none' attack vector), since it requires no second real keypair to set up and directly exercises the jwt.WithValidMethods([]string{"HS256"}) guard in verifyDatasetToken
- **Why it matters:** This is the classic JWT alg-confusion attack surface, so it's a meaningfully adversarial choice rather than an arbitrary one — but a reviewer might have expected RS256/ES256 with a real second key instead. I judged 'none' as strictly more valuable to cover since it needs no external key material and is the higher-severity historical vulnerability class.


---

## W4

### Reason has SIX members, not five

- **The plan is silent on:** W4's criteria list five (stale_series, non_positive_reference, insufficient_coverage, no_observations, no_ratio_pair) and say "Do not add a sixth"; R67/the orchestrator brief lists a different five (condition_tripped, insufficient_coverage, non_positive_reference, stale_data, no_ratio_pair). Neither list is a superset of the other.
- **The executor chose:** The union, with R67's spelling: condition_tripped | insufficient_coverage | non_positive_reference | stale_data | no_observations | no_ratio_pair. `stale_series` does not appear anywhere. Dropping no_observations would break two explicit W4 criteria; dropping condition_tripped would leave a tripped condition with no reason, which § "Every condition result carries a reason" says W14 renders.
- **Why it matters:** W14 imports this type and switches on it. If the plan is amended back to five, W14's exhaustive switch either loses a case or fails to compile.

### A holding condition's reason is null

- **The plan is silent on:** The plan gives reasons for tripped and for every indeterminate branch, never for `holding`.
- **The executor chose:** reason: null when state is "holding"; reason: "condition_tripped" when tripped.
- **Why it matters:** W14 must render an empty cell rather than a stale reason for the ordinary healthy case.

### The trailing_n_days mean is computed over post-go-live observations only

- **The plan is silent on:** § Condition semantics says the mean is over [t - reference_days, t) without saying whether pre-go-live points may contribute; the criterion pairs it with "only observations with tMs >= liveAtMs are considered".
- **The executor chose:** The trailing mean uses only observations with tMs >= liveAtMs, the same array every other statistic reads.
- **Why it matters:** With history available before go-live, the alternative reading changes the reference — and therefore the statistic — at the first `reference_days` worth of observations. It is a silent numeric divergence, not a crash.

### An empty trailing window is a SKIP, with reason insufficient_coverage

- **The plan is silent on:** The first observation after go-live always has an empty [t - N, t) window, and the plan defines no outcome for a reference that cannot be computed. The enumerated skip reasons cover only R <= 0 and a missing ratio partner.
- **The executor chose:** Skip that observation, counted under `insufficient_coverage`, which is already in the vocabulary and already means "not enough data here". So a trailing condition's day-0 observation is skipped (visible in evaluate_grid_4/7/10 as a leading null).
- **Why it matters:** The alternative — computing a reference from a single point, or treating it as non_positive_reference — would either invent data or mislabel the cause on W14's table.

### Skip-exhaustion tie-break extended to three reasons

- **The plan is silent on:** The tie rule names two skip kinds only.
- **The executor chose:** Counts per reason, highest count wins, ties resolve in the fixed order non_positive_reference > no_ratio_pair > insufficient_coverage. The plan's stated two-way rule is a strict special case.
- **Why it matters:** Determinism: without a total order, a 1-1-1 split would depend on Map iteration order.

### Skip exhaustion is judged over ALL post-go-live observations, not over the window

- **The plan is silent on:** "If every observation of a condition is skipped" — ambiguous between the condition's whole series and the sustained window.
- **The executor chose:** Whole series. If some observations were computable but none of the ones inside the window were, the result is indeterminate/insufficient_coverage rather than the skip reason (see evaluate_grid_11's last observation).
- **Why it matters:** It decides which reason W14 shows for a ratio_to condition whose partner series simply stopped: 'insufficient coverage' rather than 'no ratio pair'. Both are honest; only one is implemented. Every mandated fixture is built so the two readings agree, so a later amendment would not change any pinned number.

### The ratio_to partner point is searched over the partner's WHOLE series, including points before go-live

- **The plan is silent on:** § Condition semantics constrains the partner only by "at or before t, within ratio_lookback_days".
- **The executor chose:** No go-live filter on the partner series. evaluate_grid_11 depends on it: the day-0..day-20 observations resolve to a monthly point five days BEFORE go-live.
- **Why it matters:** With the opposite choice, every mixed-frequency ratio condition is no_ratio_pair for the first month of a hypothesis's life — exactly the failure R26/A13 exist to prevent.

### observations_in_window when sustained_days == 0

- **The plan is silent on:** The window is pinned as the degenerate [nowMs, nowMs], which literally contains no observation at all.
- **The executor chose:** 1 when the most recent observation exists and was not skipped (the decision rests on it), 0 when it was skipped. Not a literal count inside [nowMs, nowMs], which would always be 0.
- **Why it matters:** W14 prints this number next to a tripped condition; a literal 0 would read as 'tripped on no data'.

### Freshness boundary unified to strictly-older

- **The plan is silent on:** The condition rule says "newer than staleness_days before nowMs" and the metric rule says "older than staleness_days before nowMs" — the two disagree at the exact boundary.
- **The executor chose:** Stale iff nowMs - t_last > staleness_days * 86_400_000 in BOTH places; exactly staleness_days old is fresh. Pinned by a fixture at the boundary and at boundary+1ms.
- **Why it matters:** A daily FRED series evaluated by a cron at the same wall-clock time each day sits exactly on that boundary, so the choice is load-bearing, not academic.

### `value` is reported even when the state is indeterminate

- **The plan is silent on:** The criteria define `value` positionally without saying whether an indeterminate condition should blank it.
- **The executor chose:** Always the statistic at the most recent non-skipped observation in the window (overall, for sustained_days == 0); null only when there is none. So an insufficient_coverage result can still carry a number, and a stale one still shows the last statistic.
- **Why it matters:** W14's table would otherwise be blank in exactly the cases a human is trying to diagnose.

### No self-ratio guard

- **The plan is silent on:** W3 deliberately allows a ratio_to condition to name its own metric in ratio_metric and left any guard to W4.
- **The executor chose:** No guard. v / v is 1 at every observation (and the v == 0 case is already skipped by the v_other == 0 rule), so the evaluator is total and needs none. A self-ratio condition therefore trips or holds against the constant 1 rather than erroring.
- **Why it matters:** It is a silent spec mistake rather than a rejection; if the owner wants it rejected, the rule belongs in W3's V23, not here.

### Non-finite observations are treated as absent

- **The plan is silent on:** The plan says the caller parses CSV into Point[]; it does not say what a NaN value or timestamp means.
- **The executor chose:** Observations with a non-finite tMs or v are filtered out before anything else, so an all-NaN series reads as no_observations.
- **Why it matters:** A NaN reaching `value` serialises to null, which would break the JSON round-trip deep-equal that makes an evaluation the permanent record after the dataset reaper runs.

### Series are trusted to be ascending; the module does not sort

- **The plan is silent on:** "ascending by tMs" is stated as part of the contract but nothing enforces it.
- **The executor chose:** No defensive sort — documented in the module doc comment instead. peak_since_live and value_at_live read the order directly.
- **Why it matters:** A sort would mask a caller bug in W10/W11; not sorting means an unsorted series produces wrong numbers rather than an error.

### support_score is clamped to [-1, 1] and -0 is normalised to 0

- **The plan is silent on:** It states the bound as a consequence of the weights summing to 1, not as something to enforce.
- **The executor chose:** Math.min(1, Math.max(-1, sum)), then map -0 to 0. No rounding — the board's 2-decimal display is W10's formatting job.
- **Why it matters:** Floating-point addition of decimal weights can land a whisker outside the bound, and JSON.stringify(-0) is "0", which would break the round-trip deep-equal.

### UnixMs and MS_PER_DAY exported from this module; the two spec defaults duplicated as local constants

- **The plan is silent on:** § Shared shapes declares UnixMs but pins no home for it, and the import-list criterion forbids importing anything but types from spec.ts — so DEFAULT_FLAT_BAND_PCT / DEFAULT_STALENESS_DAYS (which are values) cannot be imported.
- **The executor chose:** Export `UnixMs` and `MS_PER_DAY` here; redeclare the two defaults as private constants with a comment naming spec.ts as the source of truth. They are only reachable via a hand-built spec, since validateSpec always fills both.
- **Why it matters:** It is a deliberate two-place constant. If W3's defaults ever change, this file must change with them; the alternative was violating the import-list criterion.

### A regex literal in the test had to be assembled from strings

- **The plan is silent on:** Nothing warns that W1's tools/import-boundary scans test files textually.
- **The executor chose:** The import-list assertion checks a prefix and extracts the specifier separately, because writing the expected statement as one regex literal made the boundary checker read `"./spec.js"` as a real bare import of a package called "." and fail api/src/import-boundary.test.ts.
- **Why it matters:** Any later ticket asserting on import statements as literals will hit the same false positive.

### How the grid fixtures observe a per-observation statistic

- **The plan is silent on:** The criteria demand hand-computed numbers for the whole statistic x reference grid, but `evaluate` exposes only one `value` per condition.
- **The executor chose:** Each grid fixture calls evaluate once per observation with sustained_days: 1 and nowMs set to that observation's timestamp, so the window holds exactly that observation and `value` IS its statistic — the whole series of statistics is then asserted as an array of literals, through the public API only.
- **Why it matters:** It keeps the grid graded on the public contract without exporting internals; it also means the exclusive window start is load-bearing for the fixtures.


---

## W7

### WOLF_MCP_TOKEN's length and charset (R49 says pinned nowhere)

- **The plan is silent on:** Only that the variable exists and authenticates /mcp.
- **The executor chose:** 32–128 characters of [A-Za-z0-9_-] (URL-safe base64), validated in config.ts with a `misconfigured` error that names the variable and never echoes the value; generation command documented as `openssl rand -base64 32 | tr '+/' '-_' | tr -d '='`.
- **Why it matters:** A short token defeats the constant-time compare, and a value containing a space, quote or `$` breaks silently somewhere along X1's shell export → Docker → Orange `${VAR}` interpolation chain — failing inside a container at first tool call. W12 and X1 must generate tokens that satisfy this pattern.

### Whether a missing WOLF_MCP_TOKEN is fatal at boot

- **The plan is silent on:** Nothing states what wolf-api does when the MCP token is unset.
- **The executor chose:** Fatal. config.ts allows empty (so loadConfig stays usable), but `createWolfMcp` throws `misconfigured` naming WOLF_MCP_TOKEN, and createApp calls it — so wolf-api refuses to boot rather than serve market-data tools unauthenticated. index.ts prints one line, not a stack.
- **Why it matters:** Changes the operator experience: `cp .env.example .env && docker compose up` now fails until a token is generated (README and .env.example say so). The alternative — mount /mcp unauthenticated when unconfigured — is the silent no-op this codebase keeps getting bitten by.

### WOLF_SERIES_TOKEN_SECRET's requiredness and default

- **The plan is silent on:** Named only as "to be wired by whichever ticket owns config.ts".
- **The executor chose:** Optional, minimum 32 characters; when unset a fresh random 32-byte secret is generated per boot and `seriesTokenSecretSource: "env" | "generated"` is logged (never the value).
- **Why it matters:** No committed weak default, and X1's run.sh does not have to learn a new variable. Cost: download URLs minted before a restart stop verifying after it (bounded by the 300s TTL), and two wolf-api processes would not honour each other's URLs.

### The duration variable's name and range

- **The plan is silent on:** The ticket writes `WOLF_SERIES_URL_TTL` (no _SECONDS suffix) and gives no valid range.
- **The executor chose:** `WOLF_SERIES_URL_TTL_SECONDS`, integer, 1–3600, default 300 — same correction W6's Notes made for the cache TTL, since the § "Parallelism and file ownership" house rule wins over ticket prose.
- **Why it matters:** R49 lists the suffixed name; this is now the pin. The 3600 cap enforces the ticket's own "keep the TTL short" security posture rather than trusting the operator.

### WOLF_MARKETDATA_CACHE_TTL_SECONDS lower bound

- **The plan is silent on:** Only "default 3600".
- **The executor chose:** Any integer ≥ 0; 0 means every resolve recomputes (cache.ts's `now - stored >= ttlMs` is always true).
- **Why it matters:** Gives an operator a documented way to disable caching for debugging, but a `0` reaching config by accident silently doubles upstream traffic — which is why the empty-string trap below mattered.

### FRED_API_KEY requiredness and when the FRED client is constructed

- **The plan is silent on:** W6 pins "misconfigured at construction"; nothing says when W7 constructs the client.
- **The executor chose:** Optional in config (empty passes through). The FRED connector is built LAZILY on first FRED use, so a keyless stack boots and Stooq keeps working; the first FRED tool call answers with `misconfigured` naming FRED_API_KEY.
- **Why it matters:** R55 recorded that no FRED key was available; eager construction would make wolf-api unbootable in exactly that environment, taking X1 down with it. The trade is that a missing key is discovered at first use rather than at boot.

### series_fetch's `unit` for FRED

- **The plan is silent on:** The shape requires a `unit`, but W6's connector exposes no per-series metadata fetch (only search returns `units`).
- **The executor chose:** `unit: string | null` — "USD" for Stooq, `null` for FRED, with the tool description pointing the model at series_search's `unit`. I did not add a metadata call to W6's connector (out of scope).
- **Why it matters:** Anything downstream that reads `series_fetch().unit` for a FRED series gets null. If W10/W12 need it, W6's connector needs a `/fred/series` lookup, which is a W6 change.

### series_search with `source` omitted when one provider is misconfigured

- **The plan is silent on:** Says only that omitting source searches both.
- **The executor chose:** Query fred then stooq, concatenate in that order, and PROPAGATE a failing leg (so a keyless FRED makes an unfiltered search fail with a misconfigured error) rather than silently returning the working half. The description tells the model to retry with an explicit source.
- **Why it matters:** Loud beats degraded — a silent half-answer reads as "FRED has nothing on that query". But it does mean a keyless deployment must pass `source: "stooq"`, which W12's researcher prompt should know.

### Download token wire format

- **The plan is silent on:** Only "HMAC-SHA256 over (source, id, from, to, exp)".
- **The executor chose:** `base64url(canonical JSON payload).base64url(HMAC)` — a mini-JWT — with the payload written in a fixed key order and absent optionals omitted entirely (never `null`), matching the plan's null-is-absence convention.
- **Why it matters:** Any other component that wants to mint or inspect one of these URLs must use signSeriesToken/verifySeriesToken; the format is not a standard JWT and has no `alg` field.

### How the "token minted for (fred,DGS10) used to fetch (stooq,avav.us)" case is even expressible

- **The plan is silent on:** The route is "authenticated solely by the token", which leaves nothing to compare the token against.
- **The executor chose:** The route accepts OPTIONAL `source`/`id`/`from`/`to` query parameters; the token stays authoritative for what is served, but any parameter present must equal the token's scope or the request is 403.
- **Why it matters:** Without this the mis-scoping criterion cannot be tested at all. It also means a caller can safely echo the tuple back into the URL for readability, and a mismatch fails closed.

### Rejection bodies and statuses

- **The plan is silent on:** Says 403 with byte-identical bodies for the download route; says nothing about /mcp's rejection status or either body's content.
- **The executor chose:** Download: 403 with exactly `{"kind":"forbidden","message":"invalid or expired token"}` sent directly (never through the shared error handler, whose per-error messages would differ). MCP: HTTP 401 with `{"jsonrpc":"2.0","error":{"code":-32001,"message":"unauthorized"},"id":null}`.
- **Why it matters:** X1 and any MCP client will see 401 (not 403) for a bad token, and the JSON-RPC error code -32001 is my pick, not a standard one.

### Exact Content-Type of the CSV response

- **The plan is silent on:** Says "Content-Type: text/csv"; Express appends `; charset=utf-8` to any string body.
- **The executor chose:** Send a Buffer so the header is literally `text/csv`, and add `Cache-Control: no-store` (the URL carries a credential).
- **Why it matters:** A grader asserting equality with "text/csv" passes; clients that want a charset must assume UTF-8.

### MCP transport mode and non-POST methods

- **The plan is silent on:** Pins only "@modelcontextprotocol/sdk, HTTP transport".
- **The executor chose:** Streamable HTTP, stateless (`sessionIdGenerator: undefined`) with `enableJsonResponse: true` and a fresh McpServer per request; GET/DELETE /mcp answer 405 with an Allow header.
- **Why it matters:** Wolf's tools are pure request/response and Orange's client speaks Streamable HTTP; but wolf-web's nginx /mcp location carries SSE directives that will now never be exercised, and any client expecting a session id will not get one.

### McpServer version string

- **The plan is silent on:** Nothing.
- **The executor chose:** "0.1.0" (api/package.json is 0.0.0).
- **Why it matters:** Appears in initialize's serverInfo; harmless but arbitrary, and nothing keeps it in step with package.json.

### createApp's signature

- **The plan is silent on:** Says only "the one-line mount into api/src/app.ts".
- **The executor chose:** `createApp(logger, config)` with config REQUIRED (not an optional that skips mounting); index.ts and app.test.ts updated, and boot-time WolfErrors routed through one `fatal()` helper.
- **Why it matters:** W8, W11 and W21 also mount routers into app.ts and inherit this signature. An optional config would have made /mcp silently absent whenever a caller forgot it.

### Router mount style

- **The plan is silent on:** Names the exports `mcpRouter` and `seriesDownloadRouter` but not their mount paths.
- **The executor chose:** Each router registers its FULL path internally (`/mcp`, `/series/download`), so mounting is `app.use(router)` with no prefix and the path lives in exactly one place (shared with the URL minter).
- **Why it matters:** A later ticket that mounts them under a prefix would break the minted URLs, which embed SERIES_DOWNLOAD_PATH.

### Where the connectors+cache seam lives

- **The plan is silent on:** Lists exactly five files for this ticket, and none of them is an obvious home for the shared resolver.
- **The executor chose:** `createMarketDataAccess` + the `MarketDataAccess` interface live in `tools.ts` (imported type-only by seriesdownload.ts, so no runtime cycle) rather than in a sixth file, and the cache stores the NORMALISED CSV string keyed by (source,id,from,to).
- **Why it matters:** Keeps the ticket's file list honest, and caching post-normalisation is what makes a download's bytes byte-identical to the ones series_fetch counted.

### Tool error body shape

- **The plan is silent on:** Says kinds propagate unchanged, not how they are serialised.
- **The executor chose:** `isError: true` with a JSON text block `{"error":{"kind","message","retryable"}}`; `retryable` is true only for `unavailable`, and `internal`'s message is the fixed string "internal error".
- **Why it matters:** W12's prompt and W10's poller will parse this shape; nothing else pins it.

### Empty-string environment variables

- **The plan is silent on:** Nothing — but docker-compose forwards optional variables as `${VAR:-}`.
- **The executor chose:** A `present()` helper in config.ts treats "" as absent for the numeric variables (z.coerce.number() turns "" into 0), with a test.
- **Why it matters:** Without it, an operator who left WOLF_MARKETDATA_CACHE_TTL_SECONDS out of .env would get a 0-second cache (never caches, doubles upstream traffic) and a 0-second URL TTL would refuse to boot. Every later ticket adding an optional numeric variable faces this.

### docker-compose.yml passthrough

- **The plan is silent on:** Wolf's compose file is in no ownership table, and W7's Files list does not mention it.
- **The executor chose:** Added the five `environment:` entries for the variables I documented.
- **Why it matters:** Compose injects nothing from .env that an `environment:` entry does not name — without these lines every variable I added would be documented but unreachable inside the container. It is also a collision risk if another ticket edits that file concurrently.

### README quickstart

- **The plan is silent on:** Nothing.
- **The executor chose:** Added the WOLF_MCP_TOKEN generation line to the two-step quickstart plus a pointer to .env.example.
- **Why it matters:** The quickstart as written would now fail at boot with no explanation of why.

### Constant-time comparison of unequal-length strings

- **The plan is silent on:** Says "compared in constant time".
- **The executor chose:** HMAC both sides with a fixed key and `timingSafeEqual` the 32-byte digests, rather than `timingSafeEqual` on the raw buffers (which throws on a length mismatch — and catching that throw is itself a length oracle).
- **Why it matters:** Makes the "wrong token of a different length" case genuinely constant-time rather than an early return.

### Editing test files owned by W1

- **The plan is silent on:** The ownership table lists config.ts/.env.example/app.ts but not their sibling test files.
- **The executor chose:** Extended api/src/config.test.ts and api/src/app.test.ts in place (new describe blocks plus a `testConfig()` helper for the changed createApp signature); did not touch W1's existing assertions except the createApp call site.
- **Why it matters:** W8 branches from here and will rebase over these edits.

### The ticket's own ownership-diff Validation

- **The plan is silent on:** The ticket forbids touching config.ts/app.ts/.env.example; the orchestrator's briefing assigns them to me.
- **The executor chose:** Followed the briefing, ran the diff against W6-market-data-providers (R68), and report the three expected paths plus an empty `web/`.
- **Why it matters:** That Validation command now fails on its literal wording for a ticket that was told to do the opposite; the plan needs amending before it is re-run as a gate.


---

## W1b

### Where to put new F1/F2/F3 regression tests, given tools/import-boundary has no test runner wired

- **The plan is silent on:** The plan/briefing doesn't say where to place new unit tests for the shared checker; tools/import-boundary/package.json has no vitest devDependency and no test script, and the root package.json's `test` script only runs the api and web workspaces.
- **The executor chose:** Added a new file api/src/import-boundary.fixtures.test.ts (auto-picked-up by api/vitest.config.ts's `src/**/*.test.ts` include) rather than adding a vitest devDependency to tools/import-boundary, which would have violated the 'do not add dependencies unless the ticket names one' rule.
- **Why it matters:** If a future ticket gives tools/import-boundary its own test runner, these fixture tests could be moved to sit beside the module they test rather than in api/. Flagging so the choice isn't mistaken for a permanent home.

### Self-scanning trap in the new test file's own prose

- **The plan is silent on:** The plan's W1 criterion warns generally: 'The suite must not fail against its own comments: use a placeholder token in prose rather than a literal quoted specifier' — but doesn't call out how sharp this edge is once backticks join the quote class.
- **The executor chose:** My first draft of api/src/import-boundary.fixtures.test.ts tripped exactly this trap: a doc comment containing the literal text "import"/"require" (in quotes) was matched by the checker's own regex as an absolute specifier "/" (import immediately preceded a quote character), and a comment containing require(`...`) was matched as a relative specifier "...". Caught by running the real api/src/import-boundary.test.ts suite after adding the file (it failed with a false absoluteViolations entry on my own new file); rewrote the prose to remove literal quote/backtick adjacency to the words import/export/require.
- **Why it matters:** Live, easy-to-hit trap for anyone adding a new test file under web/src or api/src that discusses import syntax in comments — worth surfacing since it bit me even while deliberately trying to avoid it. Not proposing a plan change since the rule is already documented; just confirming how easy it is to violate in practice, especially now that backticks are a quote character too.

### F2 fix: order of declaration-check vs. resolution

- **The plan is silent on:** The escalation says 'the criterion requires DECLARATION, not mere resolvability' but doesn't specify whether resolution should still run for declared-but-resolvable packages (to catch a declared package whose node_modules entry has been symlinked outside the repo).
- **The executor chose:** Kept the containment check for declared packages that DO resolve (via resolvePackageDir + realpath), only skipping resolution when the package isn't installed at all. So a declared package is still checked for an out-of-repo node_modules realpath escape, in addition to the new unconditional declaration check.
- **Why it matters:** Preserves the round-3 escape-hole fix (an empty/absolute specifier falling into node_modules) alongside the new F2 fix, rather than trading one protection for the other. Seemed like the safer reading of 'allowed only if declared' (declared is necessary, not automatically sufficient once something resolves to outside the repo), but the criterion doesn't state this explicitly.

### F3 scope: which specifier forms get backtick support

- **The plan is silent on:** The escalation's F3 description names only require(...) with backticks as the concrete failing case; the criterion's 'Specifier forms caught' list doesn't say whether backtick-quoting should apply uniformly to import/export/dynamic-import too.
- **The executor chose:** Extended the quote character class to backtick uniformly across all three IMPORT_SPECIFIER_PATTERNS (static import/export, dynamic import(), require()), not just require().
- **Why it matters:** A narrower fix (require() only) would have left a backtick-quoted import(...) or import x from `...` still unmatched — the same class of bug under a different keyword. Chose the uniform fix as the more defensible reading of 'template-literal specifiers are unmatched' rather than 'require's template-literal specifiers are unmatched', but this is a real interpretive choice worth flagging in case a stricter, require()-only reading was intended.

### F3's "contains interpolation" filter

- **The plan is silent on:** Neither the criterion nor the escalation says what should happen to a backtick specifier that DOES contain interpolation (e.g. a computed template) once backticks join the quote class — only that computed/non-literal dynamic specifiers are explicitly out of scope and not to be fixed.
- **The executor chose:** Added a filter in allImportSpecifiers dropping any captured specifier containing the literal two-character substring that opens a template expression, so an interpolated backtick literal is captured by the now-broader regex but then discarded before being treated as a real specifier — net behavior for that case is unchanged from before this ticket (still unmatched/unflagged).
- **Why it matters:** Without this filter, a computed backtick specifier would be treated as a literal path (its raw, unevaluated source text), which could produce a false negative or a spurious violation — either way, mis-handling a case the plan explicitly says is out of scope. Self-imposed guard to keep F3 from silently widening into the deliberately-excluded computed-specifier gap; flagging in case reviewers want this made more visible (e.g. a dedicated comment/test) rather than a quiet filter.


---

## W6b

### Which FRED series/date range to record for the '.' sentinel

- **The plan is silent on:** The ticket says "choose a series that actually contains the '.' missing-value sentinel — DGS10 has them on market holidays, so a range spanning one is the reliable choice" but does not name an exact date range.
- **The executor chose:** DGS10, observation_start=2024-12-20&observation_end=2025-01-03 — verified by direct query to contain exactly two '.' rows (2024-12-25 Christmas, 2025-01-01 New Year's Day) among 11 total observations, a small enough fixture to commit and read easily while covering the boundary case twice.
- **Why it matters:** A narrower or differently-placed range might have caught zero or only one sentinel, or produced an unwieldy fixture; this range gives two independent instances of the omission to pin against.

### Whether to add a `limit` param to the FRED search fixture

- **The plan is silent on:** The ticket and W6's README stub showed the search command as bare `search_text=treasury` with no `limit`.
- **The executor chose:** Added `&limit=5` to the recording command only (the client code itself still sends only `search_text`, unchanged). The unbounded query matches 7306 series, which would make an unreasonably large fixture to commit and diff.
- **Why it matters:** Keeps the committed fixture small (5 items, ~6KB) while still exercising the real field shape for every criterion the test needs (id/title/units/frequency/observation_start/observation_end). Documented explicitly in the README so a future reader isn't confused about why the recorded command differs slightly from the ticket's original stub.

### How to load the JSON fixtures inside fred.test.ts given NodeNext + resolveJsonModule constraints

- **The plan is silent on:** Not addressed by the ticket or by any prior-art fixture-loading pattern in the repo (no existing test imports a .json file; the only JSON-consuming precedent, stooq-tickers, deliberately moved to a compiled .ts module specifically to avoid readFileSync-against-a-relative-path-in-dist problems).
- **The executor chose:** readFileSync + JSON.parse with a path built via fileURLToPath(import.meta.url), following the exact pattern already used in import-boundary.test.ts (dirname(fileURLToPath(import.meta.url))). Did not use a native JSON import (`import x from "./f.json"` / `with { type: "json" }`) to avoid NodeNext import-assertion churn in a test-only file.
- **Why it matters:** Test files never ship into dist/ (only src/*.ts gets built and the Docker image copies only dist/), so the ENOENT-in-production trap that forced stooq-tickers off JSON doesn't apply here — but I still chose the readFileSync approach for consistency with the one existing precedent in the codebase rather than introducing a new JSON-import mechanism for a single test file.

### Whether the ownership-diff validation command (`git diff --name-only main -- ...`) is meaningful as literally written

- **The plan is silent on:** The ticket's Validation section copies W6's literal command comparing against `main`, but this repo's actual `main` branch predates the whole agent-wolf plan (unrelated pre-existing scaffold commits: "moved files around", "new docs", etc.) and simply lacks api/src/config.ts, api/src/app.ts and .env.example entirely — so the diff can never print nothing for any ticket built on W1 onward.
- **The executor chose:** Ran the diff against my actual base branch (W6-market-data-providers) instead, which is the meaningful comparison for "did THIS ticket touch ownership-restricted files", and reported both the literal-main result and the corrected one in evidence/validation. This mirrors W6's own Notes, which already flagged the same discrepancy ("the ownership diff prints nothing against the corrected base").
- **Why it matters:** A worker who only ran the literal command as written would see three filenames printed and might wrongly conclude they'd violated file ownership, or (worse) might not notice at all since the instruction says 'prints nothing' without saying what to do if it doesn't. This is the same plan defect W6 already surfaced — I'm not logging it again as a new issue since it's already known, just confirming it recurs for this ticket too.

