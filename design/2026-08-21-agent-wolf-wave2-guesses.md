# Agent Wolf wave 2 — the guesses log

> **What this is.** Every place a wave-2 implementer had to decide something
> `design/2026-08-20-agent-wolf.md` revision 4 did not specify. It is the primary output of
> the wave: it measures whether the plan is precise enough to run the remaining tickets
> unattended. Recorded 2026-08-21, from the structured reports of the five implementers.

Tickets executed: **O2, O11, W2, W3, W6** — all five verified PASS by an independent
adversarial verifier that re-ran each ticket's own Validation commands.
**48 guesses** were recorded. The ones with cross-ticket consequences were promoted to the
plan's Discovered Issues Log as **R57–R70**; this file is the complete list, including the
small ones.

| Ticket | Guesses |
| --- | --- |
| O11 | 5 |
| O2 | 12 |
| W2 | 3 |
| W3 | 23 |
| W6 | 5 |


---

## O2

### Where the unique-index conflict re-read happens

- **The plan is silent on:** The plan says a unique violation must surface as a conflict "carrying the re-read current version", but not that the re-read is impossible inside the failed transaction.
- **The executor chose:** An unexported sentinel errDatasetVersionTaken returned from inside the gorm transaction; the transaction unwinds, then currentDatasetVersion re-reads on a fresh connection and the exported ErrDatasetVersionConflict is built outside. Documented in a comment.
- **Why it matters:** Postgres aborts a transaction on a constraint violation (25P02), so an executor who re-read inside the tx would get an opaque "current transaction is aborted" error instead of a conflict, and the concurrency criterion would fail intermittently — only when the backstop path is actually taken, which is exactly the rare case.

### CAS mechanism: high-water read + unique index vs. an advisory lock

- **The plan is silent on:** The plan requires the version choice and the CAS check to be in one transaction and names the unique index as "the backstop", but does not forbid pg_advisory_xact_lock or SELECT … FOR UPDATE.
- **The executor chose:** SELECT COALESCE(MAX(version),0) inside the transaction, no locking, with the unique index as the only serialiser.
- **Why it matters:** An executor choosing an advisory lock would make the backstop path unreachable, so O3's tests (and any later ticket asserting the conflict shape under contention) would exercise different code. It also decides whether a lock-namespace convention exists that later dataset work must respect.

### Whether the caller's *Dataset is mutated with the generated defaults

- **The plan is silent on:** "The store fills three fields when, and only when, the caller left them zero" does not say whether it fills them on the caller's struct (as CreateMemory does) or on a copy.
- **The executor chose:** A local copy (row := *d); the caller's struct is never touched, and the stored row is read back and returned.
- **Why it matters:** A caller that retries after a conflict gets a fresh id rather than silently reusing the one a failed attempt generated. If O6a assumes its input struct comes back stamped with the id and created_at, it will read zeros — it must use the returned value.

### GetDatasetVersion with version < 1

- **The plan is silent on:** The plan pins ifVersion < 0 as an argument error but says nothing about the version argument of GetDatasetVersion.
- **The executor chose:** version < 1 is an argument error ("dataset version must be 1 or greater"), not ErrDatasetNotFound.
- **Why it matters:** O5's HTTP route maps a version query parameter straight into this call. An argument error should become 400; if O5 expects ErrDatasetNotFound for ?version=0 it will emit 400 where it planned 404.

### Whether the read methods validate the name

- **The plan is silent on:** The name-validation criterion is stated for the write path only.
- **The executor chose:** CurrentDataset and GetDatasetVersion both call ValidateDatasetName (and reject an empty project) before the dialect guard, so an illegal name is an argument error rather than a not-found.
- **Why it matters:** O5/W2 will see 400 rather than 404 for a syntactically impossible name. If a later ticket expects a not-found for arbitrary junk in the {name} path segment, this diverges.

### Exact error message wording and the fmt.Errorf prefix

- **The plan is silent on:** No message text is pinned for any rejection or for the new sentinel.
- **The executor chose:** All argument errors are "agentdb: dataset <thing> …" (e.g. "agentdb: dataset if_version must not be negative, got -1"); ErrDatasetRequiresPostgres reads "agentdb: datasets require Postgres (jsonb labels + the unique index compare-and-swap depends on)"; ErrDatasetVersionConflict.Error() is O1's, unchanged.
- **Why it matters:** O5 and O6b will surface these strings to HTTP clients and to the model in tool results. Any ticket asserting on message text (or W-side tests matching on it) is written against a wording nobody pinned.

### Signal for "if_version" in the argument error

- **The plan is silent on:** The plan names the Go parameter ifVersion; the wire field (O5/O6b) is presumably if_version.
- **The executor chose:** The error text uses the snake_case wire spelling if_version, since the message is destined for an HTTP/tool caller rather than a Go one.
- **Why it matters:** A caller mapping errors by substring gets if_version, not ifVersion.

### Number of racing goroutines and how the backstop is proven

- **The plan is silent on:** "Two concurrent writers" is the stated minimum; the plan does not say how to guarantee the unique-index path is actually taken, and a naive goroutine race usually is not close enough to trigger it.
- **The executor chose:** 16 writers over two rounds (ifVersion 0 and 1), plus a separate deterministic test that seeds an uncommitted row at version 1 so the store passes its own CAS check and blocks on the index. I verified with a temporary println (removed before the validation runs and the commit) that the backstop branch fires in both tests.
- **Why it matters:** Without the deterministic test, an implementation whose unique-violation branch is wrong — e.g. re-reading inside the aborted transaction — passes the goroutine test on most machines. Any executor who wrote only the goroutine test would ship the bug.

### Test-file split of the eleven rejection rows and the per-file helpers

- **The plan is silent on:** The plan pins the dialect split and the test-name prefixes, not the helper shapes or which store fixture the unit tests use.
- **The executor chose:** datasets_test.go reuses artifacts_test.go's newTestStore (sqlite, Artifact-only AutoMigrate — no dataset table is needed because nothing reaches SQL) with a validDataset() builder and a testDatasetBlobPath constant; datasets_live_test.go adds newLiveDatasetProject(t,s) (a uuid project id plus a DELETE-by-project cleanup) and liveDataset(project,name).
- **Why it matters:** O3 modifies both files and will reuse or shadow these helpers. In particular datasets rows hang off no FK, so nothing cascades — O3 must keep the explicit cleanup or the throwaway accumulates rows that break ListDatasets assertions.

### Eight live tests rather than the five the Validation demands

- **The plan is silent on:** The Validation requires at least five --- PASS but does not enumerate which behaviours get their own top-level test.
- **The executor chose:** Eight top-level TestDatasetLivePG_ tests, one per criterion cluster (create+defaults+jsonb, monotonic chain, conflicts, goroutine race, project binding, verbatim caller fields, no config event, unique-index backstop). Subtests were avoided at the top level because `grep '^--- '` only matches unindented lines, so subtests would not count toward the five.
- **Why it matters:** An executor using subtests inside one parent would print one --- PASS and appear to fail the Validation's five-PASS rule.

### Naming of the unexported dialect guard

- **The plan is silent on:** The plan names the exported sentinel but not the guard method.
- **The executor chose:** (*Store).requireDatasetPostgres, alongside memories.go's requirePostgres.
- **Why it matters:** O3 and O5 add dataset methods that must call the same guard; a second guard with a different name would be easy to add by accident.

### Column list and insert style

- **The plan is silent on:** Nothing pins how the row is written (gorm Create vs raw SQL).
- **The executor chose:** A package-level const datasetColumns and a raw INSERT with a ?::jsonb cast for labels, mirroring CreateMemory, rather than gorm's Create — LabelSet's Valuer would work, but the explicit cast matches the house pattern and keeps the projection in one place.
- **Why it matters:** O3's list/version queries should reuse datasetColumns (or gorm's model scan) rather than re-listing columns; if it hand-lists them the two can drift when a later migration adds a column.


---

## O11

### Timestamp base for the fixed live tests

- **The plan is silent on:** The plan says nothing about how live memory tests should seed `created_at`; the criteria only say 'newest first' and 'the older row beneath it'.
- **The executor chose:** A fixed literal `base := int64(1_700_000_000_000)` with `base+1000` / `base+2000` steps, copying the sibling pattern at agentdb/memories_live_test.go:161, rather than the `time.Now().UnixMilli()` base the finding suggested.
- **Why it matters:** A fixed base avoids adding a `time` import to memories_retraction_live_test.go and matches what every other live memory test in the package already does. If a future ticket adds a max-age or recency-window default to SearchMemories, a 2023-era fixed base would silently exclude these rows while a `time.Now()` base would not — that is the one way the choice could diverge.

### Which retraction is 'newer' in the B5 test

- **The plan is silent on:** The criterion says 'one with empty provenance, one from a session' and 'newest first', but does not say which of the two must be the newer.
- **The executor chose:** The application's own empty-provenance retraction is older (base+1000); the attacker's session-provenanced one is newer (base+2000), so `RetractedBy[0]` is the attacker's and `RetractedBy[1]` is the application's — preserving the assertion order the previous round already wrote.
- **Why it matters:** This is the direction that models the actual attack the criterion describes (attacker appends on top of the legitimate withdrawal). Reversing it would still satisfy the literal words but would not exercise the resurrection scenario, and index-based assertions in any later test built on this one would flip.

### Project-name uniqueness scheme in the httpapi live test

- **The plan is silent on:** The plan does not specify how httpapi live tests should isolate their Postgres rows; `newLiveProject` is an agentdb-package helper and is not exported.
- **The executor chose:** Inlined `"memaudit-" + uuid.New().String()` in the test rather than exporting/duplicating a `newLiveProject` helper into httpapi.
- **Why it matters:** If a later ticket adds more httpapi live-Postgres memory tests, whoever writes them will either repeat this inline idiom or introduce a shared httpapi test helper. Introducing the helper now would have been scope expansion; the cost is one duplicated line if a third such test appears.

### Whether the target/seed row also needs an explicit timestamp

- **The plan is silent on:** Nothing in the ticket addresses timestamps at all.
- **The executor chose:** Gave the retraction TARGET row an explicit `CreatedAt: base` too, even though it is not in any ordering contest, so every row in the fixture is deterministic rather than half-deterministic.
- **Why it matters:** Purely defensive. If a future assertion in that test starts caring about the target's position in a multi-row result, it will already be pinned instead of quietly reintroducing the same class of flake.

### Finding 3 asked for a plan-document edit I am forbidden to make

- **The plan is silent on:** The fix instructions say 'do not touch the plan document'; the finding's requiredFix says to add a Validation line to the O11 ticket.
- **The executor chose:** Left design/2026-08-20-agent-wolf.md untouched, ran `go test ./cmd/agentd/... -run 'TestMemoryTools|TestSessionTokenIsRejected' -count=1` myself (exit 0, reported in validation), and raised it as a discovered issue for the plan owner.
- **Why it matters:** The 401 criterion remains ungated by the ticket's own Validation list. Any future executor re-running O11's commands verbatim will not exercise TestSessionTokenIsRejectedByProjectRoutes, so a regression in the `sid` lock would pass all listed commands — the exact trap the plan's own Validation rule warns about.


---

## W2

### Non-empty provenance fixture values

- **The plan is silent on:** The plan does not specify what non-empty values a test fixture for created_by_worker/created_by_session should use.
- **The executor chose:** Reused naming conventions already present elsewhere in the same test file ("researcher-1a2b3c4d", "interviewer-9f3a7c21") for worker slugs, and short session ids like "sess-cur-1" for session ids.
- **Why it matters:** Cosmetic only — any non-empty distinguishing string satisfies the mutation-proof requirement; a different executor's choice of literal string would not diverge behaviourally.

### Where to add the upstreamBody assertion for the generic error-construction site

- **The plan is silent on:** The requiredFix said 'at least one row per error-construction site' without naming which row.
- **The executor chose:** Picked the 404 row (the first one in the status table) for the generic path, since it was the simplest single-line addition with no other side effects.
- **Why it matters:** Any status row backed by the generic defaultErrorFor branch (404/410/409/403/400/422/500/502/503/504/418) would have worked equally; picking 404 does not narrow future coverage since defaultErrorFor is shared code.

### Dataset/worker/delivery createdByWorker fixtures left as empty string

- **The plan is silent on:** Finding 1's problem statement mentions createdByWorker is unproven 'for datasets, workers and deliveries' too, but its requiredFix explicitly lists only memory-read fixtures (listMemories, getMemory, getCurrentMemory, retraction rows) and the failing criterion in criteriaNotPassing is scoped to memory reads only ('Every memory read returns provenance...').
- **The executor chose:** Left the dataset fixture at client.test.ts:324 (created_by_worker: "") unchanged, since WorkerRecord and DeliveryRecord have no createdByWorker/createdBySession fields at all (confirmed by reading types.ts), and the dataset gap was not in requiredFix or in a failing criterion.
- **Why it matters:** If a future adversarial pass re-runs the same 'rename created_by_worker in all four mappers' mutation, mapDatasetMetadata (client.ts:379) would still pass undetected via the dataset fixture at line 324 keeping created_by_worker: "". I verified this round's specific required mutation (rename in client.ts globally) is now caught by the memory-side fixtures alone, since a global rename hits mapMemorySearchRow/mapMemoryRecord/mapMemoryRetraction too — but a mapDatasetMetadata-only rename would not be caught. This is a narrower residual gap than what was reported failing, so I left it out of scope for this fix round; a later ticket touching datasets should be aware.


---

## W3

### Explicit `null` counts as ABSENT for every "present iff" rule (V21, V22, V23, V24) and for every optional field

- **The plan is silent on:** § "The condition object" prints the optional fields as explicit nulls (`"ratio_metric": null`, `"ratio_lookback_days": null`, `"reference_days": null`) and W3 mandates transcribing that block verbatim into the fixture, but V22/V23/V24 are stated as "present iff …" and nothing says whether `null` is presence or absence.
- **The executor chose:** `null` is absence. Every optional field is `.nullish()`, and the `present()` helper (spec.ts:252-255) treats `undefined` and `null` alike. Normalisation drops the key entirely from the returned `Spec`, so the output never contains `null`.
- **Why it matters:** Under the opposite reading the plan's own printed condition object fails V21/V22/V23/V24 and W3 cannot pass its own fixture criterion. Downstream: W9/W12/W13 may emit specs with explicit nulls (an LLM copying the printed shape will), and W4/W10/W14 read `Condition` objects where absent fields are missing keys, not nulls — a `null` check downstream would be dead code and an `in` check would be wrong.

### zod major and strictness idiom

- **The plan is silent on:** § "Pinned technology choices" pins "zod" with no major version; package.json declares ^4.0.0.
- **The executor chose:** Resolved version is 4.4.3. Used the zod 4 idiom `z.strictObject({...})` (not `z.object().strict()`), `z.enum(TUPLE as const)`, and `z.core.$ZodIssue` as the issue type in `issuesToErrors`.
- **Why it matters:** Later tickets validating route bodies (W8, W9, W10, W11, W21) must use the same major and the same strictness idiom; a zod-3 style `.strict()` chain or `z.ZodIssue` type import will not compile against 4.4.3.

### Cross-field rules implemented OUTSIDE zod rather than as a superRefine

- **The plan is silent on:** The ticket says schemas are built with zod and are strict, and separately that all errors come back at once. It does not say how the cross-field rules (V13, V17, V21–V24, V27, uniqueness) are attached.
- **The executor chose:** A separate `semanticErrors(input)` pass over the RAW input, always run, concatenated after the zod issues. Verified empirically that zod 4.4.3 does NOT run a `superRefine` when the base parse produced any issue.
- **Why it matters:** With a superRefine, a spec with one bad `weight` type would report only that and hide the six cross-field problems, breaking the all-errors-at-once criterion. Any later ticket that adds a rule must add it to the right pass or it will silently stop firing.

### JSON path format and the root path

- **The plan is silent on:** The ticket gives two example paths (`metrics[1].weight`, `invalidation[0].ratio_lookback_days`) but no formatting rule and no path for a root-level failure.
- **The executor chose:** `formatSpecPath` (spec.ts:205-213): dots between object keys, `[n]` for array indices, no leading dot, and the empty string `""` for the root (e.g. `validateSpec(42)` → `{path: "", message: "Invalid input: expected object, received number"}`).
- **Why it matters:** W13 renders these paths next to form fields and W9 returns them in a 422 body; if W13 assumes JSON-Pointer (`/metrics/1/weight`) or a root path of `"$"`, nothing matches.

### One error per unknown key rather than one per object

- **The plan is silent on:** V7 says "unknown keys are rejected … each error naming its JSON path". zod folds every surplus key of one object into a single `unrecognized_keys` issue whose path is the CONTAINER.
- **The executor chose:** Fan the issue out into one `SpecError` per key, path `container.key` (spec.ts:217-228), with the message `unrecognized key "x"; the spec is strict at every level (V7)`.
- **Why it matters:** Two surplus keys on one metric produce two errors, not one, so any downstream count-of-errors assertion differs; and W13 can point at the individual offending field.

### Error paths for the two rules that are not about a single field (V13, V27)

- **The plan is silent on:** Neither rule's error path is specified anywhere.
- **The executor chose:** V13 (weights do not sum to 1.0) → path `metrics`. V27 (heavy metric no condition names) → path `metrics[<i>]`, i.e. the offending metric itself rather than `invalidation`.
- **Why it matters:** A verifier or a UI grouping errors by path will file these differently; if W13 groups blocking reasons per metric row, `metrics` (V13) has no row to attach to and must be rendered spec-level.

### V27 is skipped entirely when there are no conditions

- **The plan is silent on:** V6 (at least one condition) and V27 (heavy metric must be named) both fire on `invalidation: []`; the plan does not say whether V27 should also report every heavy metric in that case.
- **The executor chose:** Gate V27 on `conditions.length > 0` (spec.ts:429), so an empty `invalidation` yields exactly one error (V6) rather than V6 plus one per metric.
- **Why it matters:** Changes the error count and the noise level for the commonest half-finished spec — the shape W13 renders during the interview before any condition exists.

### V13 is only evaluated when EVERY weight is a finite number

- **The plan is silent on:** Silent on what the weight sum should do when one weight is a string or missing.
- **The executor chose:** Skip V13 unless `weights.length === metrics.length` (spec.ts:333-343); the bad weight is reported once by zod and V13 does not pile on a nonsense sum.
- **Why it matters:** Affects the error count for malformed input and prevents a misleading "they sum to 0.4" message next to a type error.

### `sustained_days` is required, with no default

- **The plan is silent on:** V25 gives the range `[0, 365]` but never says whether the field is optional or what it defaults to; V3 and V4 explicitly name defaults, V25 does not.
- **The executor chose:** Required (spec.ts:186). A condition omitting it is rejected at `invalidation[i].sustained_days`.
- **Why it matters:** W9/W12 must emit it on every condition, and W4's sustained-window arithmetic can rely on it always being a number rather than defaulting to 0.

### `method.constituents` and `method.source_series` are allowed keys

- **The plan is silent on:** They appear in the printed Spec JSON worked example but in no rule; V7 makes `method` strict, so a schema built only from V1–V27 would reject the plan's own example.
- **The executor chose:** Both allowed on `Method`, typed `string[]`, optional/nullable, carried through normalisation unchanged. Neither is required and neither is otherwise validated.
- **Why it matters:** W12's interviewer prompt and W9's writer must know these two keys survive; if a later ticket rebuilds `Method` from the rule list alone, every derived metric the interviewer produces in the plan's own shape becomes invalid.

### Exported name `HypothesisSpec` in addition to `Spec`

- **The plan is silent on:** W3 says `Spec` is exported; W4's acceptance criteria write `evaluate(spec: HypothesisSpec, …)`. The plan never reconciles the two names.
- **The executor chose:** `Spec` is the real interface; `export type HypothesisSpec = Spec` is an alias (spec.ts:121).
- **Why it matters:** Without the alias W4 either redeclares the type (which W3 forbids) or renames its own signature away from what its criteria state. If W4's executor instead defines its own `HypothesisSpec`, two structurally-similar-but-separate types diverge the moment a spec field is added.

### The `WolfError` seam: an exported `specValidationError(errors, message?)` helper

- **The plan is silent on:** The ticket says the module "imports WolfError/WolfErrorKind from api/src/errors.ts for the invalid kind" but `validateSpec` is explicitly non-throwing, so it never says what the import is actually FOR.
- **The executor chose:** Added `specValidationError(errors: SpecError[], message = "hypothesis spec is not valid"): WolfError` returning kind `invalid`, status 400, `details: { errors }` (spec.ts:549-554), and typed the kind constant as `WolfErrorKind`.
- **Why it matters:** W9's 422 and W8's `spec_validation` need a canonical wrapper; if they each build their own, the `details` shape (`{errors}` vs a bare array) diverges between the two responses the UI reads.

### Error ordering and de-duplication

- **The plan is silent on:** Nothing about order, sorting, or whether two errors may share a path.
- **The executor chose:** zod issues first in zod's own order, then semantic errors in metric-then-condition-then-V27 order. No sorting, no de-duplication — one field can produce two errors (e.g. an empty slug fails both `min(1)` and the charset regex).
- **Why it matters:** Any downstream test asserting `errors.length` exactly, or `errors[0]`, depends on this. W13 rendering per-path may show two messages on one field.

### A non-`derived` metric carrying `method` is NOT rejected

- **The plan is silent on:** V15 says a derived metric must have `method` and no `series_id`; it never says a fred/stooq metric must not carry `method`.
- **The executor chose:** Left permitted, deliberately — inventing the mirror rule would be an ungraded 28th rule.
- **Why it matters:** A spec with `source: "fred"` plus a stray `method` validates. If a later ticket assumes `method` implies derived, it will mis-dispatch that metric to the derived path.

### V20 "finite" is delegated to `z.number()`

- **The plan is silent on:** Does not say whether `NaN`/`Infinity` need an explicit check.
- **The executor chose:** Verified `z.number()` in zod 4.4.3 already rejects both; no `.finite()` added. Test asserts NaN, Infinity and a numeric string are all rejected.
- **Why it matters:** If zod's behaviour changes, or if a later ticket copies this schema style for another numeric field, the finiteness guarantee is implicit rather than written down.

### The fixture's second invalidation condition

- **The plan is silent on:** The plan only says it must name `petro-settlement-share` and satisfy V16–V26; every other field is the executor's choice.
- **The executor chose:** `{ id: "inv-2", metric: "petro-settlement-share", stat: "change_pct", reference: "value_at_live", op: "gt", threshold: 5, sustained_days: 20, meaning: "the settlement share is rising, which the thesis says it should not" }` — and it deliberately OMITS the optional keys entirely (rather than nulling them) so the fixture exercises both the null form and the absent form.
- **Why it matters:** Any later ticket using this fixture as its input (W4, W10, W11, W14 all plausibly will) inherits these exact numbers; a `change_pct` with `value_at_live` reference and a 20-day sustained window is what their evaluator fixtures will see.

### The fixture omits `staleness_days`

- **The plan is silent on:** The printed Spec JSON has no `staleness_days`; the plan says no spec-level value may be altered but adding a defaulted field is not obviously an alteration.
- **The executor chose:** Left it out of the JSON. `validateSpec` therefore returns `staleness_days: 5` from the V4 default, and the returned `Spec` always carries the field.
- **Why it matters:** A later ticket diffing the raw fixture against `validateSpec`'s output will find one extra key; and `Spec.staleness_days` is non-optional in the type, so W4's staleness check can rely on it always being present.

### Fixture loading in the test

- **The plan is silent on:** Does not say how the test reads the fixture.
- **The executor chose:** `readFileSync(new URL("./__fixtures__/worked-spec.json", import.meta.url), "utf8")` rather than a JSON import — api/tsconfig.json uses `moduleResolution: NodeNext`, where a JSON import needs an import attribute, and reading the raw text is also what makes the "is real JSON rather than jsonc" assertion meaningful.
- **Why it matters:** A later ticket importing the fixture as a module will hit the NodeNext import-attribute requirement; the `__fixtures__` directory is also outside vitest's `src/**/*.test.ts` include, so it is never collected as a test.

### Each V-case bundles boundary and sibling assertions

- **The plan is silent on:** "One accepting case and one rejecting case for each rule" fixes the case COUNT but not how much each case asserts.
- **The executor chose:** Exactly 54 `it` blocks, but several assert multiple variants inside (e.g. `V22 rejects` covers missing, surplus, too-small and too-large `reference_days`; `V8 rejects` covers 51 chars, illegal charset and a duplicate).
- **Why it matters:** The two grep counters stay at 54 and V1–V27 exactly, which is what the Validation block grades; a verifier expecting one assertion per case will see more.

### Extra exports beyond the four the ticket names

- **The plan is silent on:** The ticket names `Spec`, `Metric`, `Method`, `Condition` as the exports later tickets import.
- **The executor chose:** Also exported `MetricSource`, `Direction`, `Stat`, `Op`, `Reference`, the const tuples `METRIC_SOURCES`/`DIRECTIONS`/`STATS`/`OPS`/`REFERENCES`/`REFERENCE_FREE_STATS`, the constants `MAX_SLUG_LENGTH`, `LABEL_VALUE_PATTERN`, `CONDITION_ID_PATTERN`, `WEIGHT_SUM`, `WEIGHT_SUM_TOLERANCE`, `HEAVY_METRIC_WEIGHT`, `DEFAULT_FLAT_BAND_PCT`, `DEFAULT_STALENESS_DAYS`, plus `SpecError`, `ValidateSpecResult`, `formatSpecPath` and `specValidationError`.
- **Why it matters:** W4 needs `Stat`/`Op`/`Reference` as types and the enum tuples for exhaustive switches; W12's interviewer prompt and W13's UI need the same charset/length constants. Without these exports each would re-declare them and drift.

### Metric `slug` also gets an explicit `min(1)`

- **The plan is silent on:** V8 names the charset and the 50-character ceiling but no floor.
- **The executor chose:** `.min(1)` in addition to the regex, so an empty slug produces a length message as well as a charset one.
- **Why it matters:** An empty slug yields two errors at `metrics[i].slug`, not one.

### A `ratio_to` condition may name the same metric in `metric` and `ratio_metric`

- **The plan is silent on:** V23 only requires `ratio_metric` to name a metric slug in this spec.
- **The executor chose:** Self-reference permitted; not rejected.
- **Why it matters:** W4's `ratio_to` evaluator can be handed a condition whose ratio is a series against itself (always 1.0); if that is meant to be impossible, the guard has to live in W4.

### `series_id: ""` on a fred/stooq metric is rejected at `metrics[i].series_id`

- **The plan is silent on:** V14 says "a non-`derived` metric has a non-empty `series_id`" but the schema field itself is optional (a derived metric must not have it), so emptiness is a semantic check, not a zod one.
- **The executor chose:** The V14 semantic check tests `typeof === "string" && length > 0`, so both absent and empty produce the same path and a message naming the source.
- **Why it matters:** Only one error appears for an empty series_id, at the semantic path, not a zod `min(1)` message — relevant to anything matching on message text.


---

## W6

### Default Stooq ticker table location

- **The plan is silent on:** The ticket names __fixtures__/stooq-tickers.json literally as the committed table's path; it does not anticipate that tsc's plain build never emits non-.ts files into dist/.
- **The executor chose:** Moved the table into api/src/marketdata/stooq-tickers.ts (a compiled TS module exporting DEFAULT_STOOQ_TICKERS), deleted the JSON file, and updated __fixtures__/README.md to explain why. Rejected the alternative (resolveJsonModule + explicit copy step in yarn build and the Dockerfile) as more fragile: NodeNext + JSON import assertions is a newer, less-tested TS/Node interaction, and a Dockerfile copy step is one more place to keep in sync.
- **Why it matters:** W7 constructs connectors inside the server factory and calls createStooqClient() with no injected tickers the first time series_search runs in a container — if a later ticket reverts to a JSON-file convention for some other data table without checking this precedent, it will hit the identical ENOENT.

### Default HTTP timeout value

- **The plan is silent on:** The ticket's error taxonomy criterion lists 'a TIMEOUT' as unavailable but never states a numeric deadline, and no ticket owns a WOLF_MARKETDATA_TIMEOUT_MS-style variable.
- **The executor chose:** 10_000ms (10s) default, exposed as a constructor option (timeoutMs) on both createFredClient and createStooqClient, read via an explicit option — never process.env, matching the ticket's existing pattern for ttlMs/apiKey.
- **Why it matters:** If W10's poller needs a shorter or longer bound (e.g. for a tighter tick interval), it can override timeoutMs per call/instance; if a later ticket instead expects this to come from an env var, it will need to wire it the same way FRED_API_KEY and the cache TTL are wired (a one-line pass-through in whichever ticket owns config.ts).

### Stooq empty-query threshold

- **The plan is silent on:** The ticket does not say what an empty or whitespace-only search query should do.
- **The executor chose:** Return { results: [] } for any needle of length 0 after trim() — no minimum-length requirement beyond non-empty.
- **Why it matters:** A stricter minimum (e.g. 2+ characters) would also be defensible and would change W7's series_search behavior for very short queries; I chose the minimal fix that closes the specific 'floods with everything' failure mode without guessing at an arbitrary length floor the ticket never specified.

### Placeholder Stooq ticker list contents

- **The plan is silent on:** prompts/interviewer.md (which the ticket says the table should cover) does not exist yet on this branch.
- **The executor chose:** Kept the same 20-symbol placeholder set from the original round (major US ETFs and large-cap equities, including avav.us) verbatim, just relocated to the new .ts module — did not add or remove any symbol.
- **Why it matters:** Unchanged from before this fix round; recorded again here since the finding touched the same file. A later ticket authoring prompts/interviewer.md should extend, not replace, this list if it names symbols not present here.

### FRED search()'s field-mapping and the '.' sentinel remain untested

- **The plan is silent on:** N/A — explicitly blocked per orchestrator correction #1 and the ticket's own 'do not hand-write a substitute' rule.
- **The executor chose:** Left fred.ts's search() and the '.' omission implemented against FRED's publicly documented shape only, exactly as the round-0 executor left them; added no synthetic test claiming to prove the real shape.
- **Why it matters:** Whoever unblocks this (has a real FRED_API_KEY) must run the two curl commands already recorded in __fixtures__/README.md and add the tests the README says are missing — this is not optional cleanup, it is the one remaining gap in a criterion this ticket cannot close without a credential.

