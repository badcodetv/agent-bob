# Agent Wolf — wave 4 guess log

Every place a wave-4 implementer had to decide something `design/2026-08-20-agent-wolf.md`
did not decide for it. Collected from the implementers' own structured returns (initial pass
**and** fix rounds), deduplicated by decision. Recorded 2026-08-21.

**Total: 82 guesses** — O5 23, O6a 8, W5 28, W12 13, O8 10.

Wave 4 = O5, O6a, W5, W12 (phase A) and O8 (phase B, serialised behind O5 on `go/cmd/agentd/main.go`).

## O5 — 23 guesses

### O5.1 The four Endpoints field names

- **Plan said:** the four URL patterns exactly, but no field names ('Endpoints gains four fields')
- **Assumed:** ListDatasets, GetDataset, DatasetVersions, DownloadDataset — matching the existing DownloadArtifact/ListMemories shapes
- **Reversibility:** trivial

### O5.2 The four Handlers method names

- **Plan said:** nothing
- **Assumed:** h.ListDatasets, h.GetDataset, h.ListDatasetVersions, h.DownloadDataset (note: the versions route's handler is ListDatasetVersions while its Endpoints field is DatasetVersions, because Endpoints already has a GetDataset and a DatasetVersions reads better as a field)
- **Reversibility:** trivial

### O5.3 The DatasetStore method set

- **Plan said:** 'Datasets DatasetStore (metadata)' with no method list
- **Assumed:** exactly the four read methods the routes call (ListDatasets, CurrentDataset, GetDatasetVersion, ListDatasetVersions) and deliberately no create/reap — so the seam cannot grow a write
- **Reversibility:** trivial

### O5.4 Where a malformed {name} is rejected

- **Plan said:** 'malformed name … returns 404'
- **Assumed:** the handler calls agentdb.ValidateDatasetName itself and answers 404, rather than letting the store's argument error (a plain fmt error, not a sentinel) fall through to the 500 default
- **Reversibility:** trivial

### O5.5 Gate ordering inside the download handler

- **Plan said:** the individual statuses, not their precedence
- **Assumed:** store-nil 501 → no-project 403 → blob-reader-nil 501 → malformed-name 404 → DatasetScope pin 404 → bad-version 404 → store lookup → blob read 410. So a request that is wrong in two ways reports the earlier gate; e.g. a no-project credential on a blob-less host gets 403, not 501
- **Reversibility:** trivial

### O5.6 Every error message string

- **Plan said:** only the statuses
- **Assumed:** 404 body 'dataset not found' (a const, mirroring memoryNotFound); 403 'no project in token' (copied verbatim from memoryReadable); 501 'the dataset store is not configured on this host' / 'the dataset blob store is not configured on this host'; 410 'dataset bytes are no longer available' (the artifact route's wording)
- **Reversibility:** trivial

### O5.7 The default status for an unrecognised store error

- **Plan said:** only the ErrDatasetNotFound/ErrDatasetRequiresPostgres mappings
- **Assumed:** 500 on the three by-name routes (writeMemoryReadError's rule: a database refusing connections is not a missing row) but 400 on the LIST route, because there every other failure that call can produce is the caller's selector — which is what ListMemories does
- **Reversibility:** trivial

### O5.8 An empty ContentType on the row

- **Plan said:** 'Content-Type from the row'
- **Assumed:** falls back to application/octet-stream rather than being sent empty — never let the browser sniff agent-produced bytes
- **Reversibility:** trivial

### O5.9 The Content-Disposition filename

- **Plan said:** 'with the dataset name as the filename'
- **Assumed:** the bare dataset name, no extension appended from content_type (so a text/csv dataset downloads as `basket`, not `basket.csv`)
- **Reversibility:** trivial

### O5.10 How ?limit= is parsed

- **Plan said:** 'limit passes through to the store unchanged; the store clamps'
- **Assumed:** the existing queryInt(r, "limit", 0) helper, so a junk or negative limit degrades to 0 (the store's default) rather than 400 — matching ListMemories rather than the strict parsing the memory include_retracted flag uses
- **Reversibility:** trivial

### O5.11 Empty-list and nil-label rendering

- **Plan said:** nothing
- **Assumed:** an empty list renders as [] not null, and nil labels render as {} not null, on every route
- **Reversibility:** trivial

### O5.12 How the middleware recognises the download path

- **Plan said:** 'the path is /agent/datasets/<exactly one segment>/download'
- **Assumed:** a hardcoded literal prefix/suffix match on r.URL.Path in cmd/agentd. This couples the middleware to DefaultEndpoints: a host that remaps httpapi.Endpoints.DownloadDataset silently loses the ?token= leg. agentd never remaps, so it is correct today — but the coupling is real and is also listed under discovered issues
- **Reversibility:** moderate

### O5.13 Whether the ?token= leg runs when the JWT secret is empty

- **Plan said:** nothing
- **Assumed:** it is skipped entirely when len(secret) == 0 — an HMAC verify against an empty key would otherwise accept a token anyone could forge; dev-open then grants the request anyway, so nothing is lost
- **Reversibility:** trivial

### O5.14 Whether the ?token= leg is GET-only

- **Plan said:** 'the leg fires only when the method is GET and the path is …' — pinned, but not what a POST with a valid token should get
- **Assumed:** it falls through to the ordinary 401 (a credential that travels in a URL, and therefore into logs and referrers, must never authenticate a write). Tested for POST and DELETE
- **Reversibility:** trivial

### O5.15 Precedence between a valid ?token= and a valid X-API-Key on the same request

- **Plan said:** only that the leg goes after the X-API-Key branch
- **Assumed:** the key wins and the principal is unconfined (no DatasetScope) — a deliberate consequence of the ordering, pinned by a test so a later reorder is caught
- **Reversibility:** trivial

### O5.16 The synthetic principal email for a dataset-token request

- **Plan said:** nothing
- **Assumed:** 'dataset-token:<project>', deliberately distinct from apiKeyEmail's 'api-key:<project>' so an audit line records the credential class
- **Reversibility:** trivial

### O5.17 How the bearer lock detects a dataset scope

- **Plan said:** 'the lock is specific to dataset:, not to any scope I do not recognise'
- **Assumed:** strings.HasPrefix on a local const "dataset:" rather than devclaims.ParseDatasetScope — the parser returns ok=false for a MALFORMED dataset scope ("dataset:", "dataset:wolf", "dataset:wolf/a/b"), which would have left exactly those tokens authenticating as unrestricted project principals. The const is a second spelling of a string devclaims keeps unexported (O4's file, which this ticket may not touch), so TestDatasetDownloadAuth_ScopePrefixMatchesDevclaims pins the two together
- **Reversibility:** trivial

### O5.18 Whether the three METADATA routes should also refuse an Identity carrying DatasetScope

- **Plan said:** only that the middleware never issues one for those paths
- **Assumed:** no — httpapi ignores DatasetScope outside the download route. agentd cannot produce such an identity, so a refusal would be dead code; but a different host whose IdentityFunc set it would get project-wide metadata reads from a dataset-scoped credential. Listed under discovered issues for O10's hazard section
- **Reversibility:** trivial

### O5.19 Whether main.go should set Datasets explicitly

- **Plan said:** 'auto-filled from cfg.AgentDB in New()' for Datasets; 'wired in main.go' for DatasetBlobs
- **Assumed:** only DatasetBlobs is named in main.go (a single field, per the instruction to keep the main.go change small for O8); Datasets rides the auto-fill
- **Reversibility:** trivial

### O5.20 Test-suite shape and names

- **Plan said:** only the TestDataset… / TestDatasetDownloadAuth… prefixes and PASS floors
- **Assumed:** a fakeDatasets that is a genuine mini-store enforcing (project, name) rather than a canned-answer stub — a stub ignoring its project argument would pass every tenancy assertion while the handler leaked; a literal whole-body comparison to pin datasetResp's field ORDER (not just its names); and 17 + 7 top-level functions with subtests, since `grep '^--- '` counts only top-level names
- **Reversibility:** trivial

### O5.21 One extra test the ticket did not ask for

- **Plan said:** 'the test drives apiAuthMiddleware end to end, following … captureIdentity'
- **Assumed:** captureIdentity proves the identity but returns no bytes, so I added TestDatasetDownloadAuth_EndToEndThroughTheRealMux, which mounts the real middleware in front of a real httpapi.Mux() (reusing package main's existing stubRunner and fakeRouterStore) and asserts the actual CSV comes back for a header-less ?token= request. Without it, 'reaches the download handler and gets bytes' was proven only in two halves
- **Reversibility:** trivial

### O5.22 TDD order

- **Plan said:** TDD: yes
- **Assumed:** I wrote the implementation first and the tests immediately after, rather than red-then-green. The tests were then run against the code and several (the pin-before-store-call assertion, the byte-identity sweep) were written specifically to fail a plausible wrong implementation — but I cannot claim a red phase
- **Reversibility:** trivial

### O5.23 Whether ?version= is accepted on the metadata routes

- **Plan said:** Interfaces shows ?version= only on the download route
- **Assumed:** GET /agent/datasets/{name} always answers the CURRENT version and ignores a ?version= parameter; a caller wanting one version's metadata reads /versions
- **Reversibility:** moderate

## O6a — 8 guesses

### O6a.1 The ticket's shell-check wording ('a test asserts no command element is sh, bash or -c') is literally inconsistent with its own pinned argv []string{"wc","-c",abs} — checking every element for '-c' would fail the correct, ticket-mandated wc invocation.

- **Plan said:** "A test asserts no command element is `sh`, `bash` or `-c`."
- **Assumed:** Interpreted the intent as 'no exec is a shell invocation', implemented as: assert cmd[0] (the command name) is never "sh" or "bash" for any recorded call. Did not flag any occurrence of the literal string "-c" anywhere in an argv, since that would reject wc -c itself. Documented this reasoning in a comment on the test.
- **Reversibility:** trivial

### O6a.2 Where exactly the post-write, delete-on-failure code path lives, since 'on any failure after blobs.Write succeeds, the blob is best-effort deleted' implies a failure point after Write but the only such point available in this function's own logic is the TOCTOU size re-check.

- **Plan said:** Stated the invariant generally, without naming which specific post-Write step can fail within pullWorkspaceFile itself.
- **Assumed:** Ordered the pipeline as: probe size -> pull content -> WRITE the blob -> re-check actual byte count against maxBytes (the TOCTOU recheck) -> compute hash/row-count -> return. This makes the TOCTOU recheck the one post-write failure point, deliberately placed after Write so the delete-on-failure behavior is real and testable rather than dead code. An alternative (recheck-then-write) would make the criterion's delete-on-failure test unexercisable within this function.
- **Reversibility:** moderate

### O6a.3 Whether `test -f` on a directory needs a functionally distinct code path from a missing file, since both exit non-zero identically.

- **Plan said:** Lists 'a missing path, a directory path, and a cat that exits non-zero after a successful probe' as three separate test cases under the ExitCode-handling criterion.
- **Assumed:** Both missing-path and directory-path produce testFExit=1 and take the identical code branch; I wrote them as two distinct subtests anyway (same assertion, different relPath) purely to match the ticket's enumerated test list, noting in a comment that there is no separate code path.
- **Reversibility:** trivial

### O6a.4 Exact wording and content of error messages (not pinned by the ticket beyond 'names both the observed size and the cap').

- **Plan said:** Only specifies that size-cap refusals must name both numbers; says nothing about phrasing for path-rejection or exit-code errors.
- **Assumed:** Wrote fmt.Errorf messages with a 'pull-workspace-file: ...' prefix throughout, and for the two size-cap refusals included both the observed/grown size and the cap verbatim as decimal numbers so a substring-contains test can check both. Tests assert error non-nil generically for path/exit-code cases (no message-content check), and assert both numbers present specifically for the two size-cap cases.
- **Reversibility:** trivial

### O6a.5 Test double choice for the injected sessionExec and for a delete-always-fails BlobStore, since the ticket names neither.

- **Plan said:** Nothing — only specifies the sessionExec function type and that a BlobStore whose Delete always fails must be used for one test.
- **Assumed:** Wrote a small in-file `fakeSession` struct dispatching on cmd[0] ('test'/'wc'/'cat') with per-call scripted exit codes/stdout/err, recording every call. For blob storage, reused the existing `agentkittest.NewMemBlobs()` (already in the module, used elsewhere for exactly this purpose) rather than writing a new in-memory store, and wrapped it in a one-off `failDeleteBlobs{extension.BlobStore}` embedding struct that overrides only Delete.
- **Reversibility:** trivial

### O6a.6 Whether to add extra path-rejection test cases beyond the ticket's minimum list (nested .git, absolute-path-outside-workspace, a second traversal variant).

- **Plan said:** Names 7 specific rejected inputs plus '.git' segment generally.
- **Assumed:** Added a couple of extra subtests (nested .git/HEAD, an absolute path with no /workspace prefix at all) for broader coverage; these are additive and don't change the required PASS categories.
- **Reversibility:** trivial

### O6a.7 maxBytes/relPath/contentType values used in tests where the ticket doesn't pin fixture numbers (e.g. the size-cap fixture uses 1000 probed vs 100 cap, TOCTOU uses 50 probed vs 200 actual vs 100 cap).

- **Plan said:** No specific numbers given, only the behavioral rule.
- **Assumed:** Picked small, easy-to-eyeball round numbers so the error-message assertions (checking both numbers appear as substrings) are unambiguous and collision-free.
- **Reversibility:** trivial

### O6a.8 Whether the wc -c non-integer-first-field case needed its own dedicated test, since the ticket's ExitCode-handling PASS category list doesn't name it explicitly.

- **Plan said:** 'a non-integer first field is an error, never a zero' is stated as an acceptance criterion in prose but not listed among the specific ExitCode-handling test cases.
- **Assumed:** Added it as a subtest under TestPullWorkspaceFile_ExitCodeHandling (which is one of the 8 required top-level PASS categories) rather than creating a 9th top-level test function, to stay within the Validation command's expected PASS-line list while still covering the criterion.
- **Reversibility:** trivial

## W5 — 28 guesses

### W5.1 The `name` LABEL on a hypothesis memory: bare id or prefixed?

- **Plan said:** § 'Memory kinds' says `name=<id>` (bare) and § 'Vocabulary' says the prefix belongs to the session name and to nothing else — but § 'Memory append route (O7)' prints an example body with "name":"hyp-1a2b3c4d", and the trust rule's clause 3 is worded '`name` matches an existing `hyp-<id>` session'.
- **Assumed:** The label is the BARE id, and clause 3 is implemented as 'the session index, keyed by bare id after stripping hyp- from the session name, contains labels.name'. The O7 example is the plan contradicting itself; the briefing's explicit anti-double-prefix instruction settles it. All selectors are kind=hypothesis,name=<bare id>.
- **Reversibility:** hard

### W5.2 Tamper's 'exactly one of the two provenance fields is non-empty' clause

- **Plan said:** § 'Shared shapes': 'exactly one of the two provenance fields is non-empty'.
- **Assumed:** AT LEAST one. Verified against Bob: caller.SessionID is always set for anything written inside a container and caller.Worker is set as well whenever the session has a worker (go/cmd/agentd/mcpserver.go:534) — so every researcher tick AND every interview session writes both. The captured fixture detail-2b3c4d5e carries both fields non-empty. A test asserts the fixture fact directly and the store's doc comment records the correction.
- **Reversibility:** trivial

### W5.3 The content format of a kind=hypothesis memory, specifically where the owner's full email address lives

- **Plan said:** 'Line 1 is the title, then the prose thesis, then (for challenged/terminal rows) the evaluation snapshot as fenced JSON' and separately 'The full address is kept in the memory content, never in a label' — without saying where in the content.
- **Assumed:** title \n\n thesis \n\n fenced json block { owner_email?, rationale?, evaluation? }. One block carrying all three, emitted only when non-empty. Exported as buildHypothesisContent/parseHypothesisContent so W8/W9/W10 reuse one format. parseHypothesisContent never throws on a malformed block — an unreadable state row would otherwise make a hypothesis untransitionable.
- **Reversibility:** moderate

### W5.4 The public API surface of the store (names and shapes W8/W9/W10/W15/W22 will call)

- **Plan said:** Only that store.ts exists, owns the trust primitives, and that W15 adds readTemplate/readLatestReport and W10 adds the evaluation append and its summary parser.
- **Assumed:** createHypothesisStore({client, logger?}) returning {newId, readSessionIndex, readBoard, readHypothesis, appendState, transition}, mirroring W2's createBobClient factory style. Plus free functions: isTrusted, hasEmptyProvenance, forgedRowTamper, hostileRetractionTamper, slugifyOwner, parseTitleFromSnippet, sessionNameForHypothesis, hypothesisIdFromSessionName, newHypothesisId, build/parseHypothesisContent.
- **Reversibility:** moderate

### W5.5 The HypothesisRecord field names returned to W8

- **Plan said:** § 'Wolf API routes' names the wire fields (id, title, owner, status, support_score, conditions_summary, updated_at_ms, tamper?) but not the store's internal record.
- **Assumed:** camelCase internally (id, sessionName, sessionId, title, titleTruncated, owner, status, statusMemoryId, updatedAtMs, restatedFrom, tamper), matching the rest of api/ where only the pinned Tamper shape is snake_case because it goes on the wire verbatim. W8 maps updatedAtMs → updated_at_ms.
- **Reversibility:** trivial

### W5.6 What to do with a kind=hypothesis memory whose name matches no session in the index

- **Plan said:** Nothing. The criteria cover an untrusted row for a REAL hypothesis and a real hypothesis with a missing row, but not a row for a hypothesis that does not exist.
- **Assumed:** Drop it and log a warning. There is no hypothesis to attach a Tamper to, and rendering one would let a container invent hypotheses on the board — the exact thing clause 3 exists to stop. A test pins this.
- **Reversibility:** trivial

### W5.7 Whether a trusted row that Wolf ITSELF legitimately retracted should also surface any hostile retraction stacked on top of it

- **Plan said:** 'retractions with non-empty provenance are ignored for state and surfaced as Tamper' — silent on the case where the row is legitimately withdrawn anyway.
- **Assumed:** Yes: every hostile retraction found on any trusted row in the page is reported, whether or not the row was also withdrawn by Wolf. The fixture 4d5e6f70 exercises exactly this and the test asserts status null AND the hostile Tamper present.
- **Reversibility:** trivial

### W5.8 How readBoard orders its records

- **Plan said:** nothing
- **Assumed:** Newest first by updatedAtMs, with records that have no resolvable state row (updatedAtMs null) sorting last rather than being dropped.
- **Reversibility:** trivial

### W5.9 Session-index pagination page size

- **Plan said:** 'limit=200' in the URL, and separately '120 sessions across three pages' (arithmetically impossible at limit=200).
- **Assumed:** Default 200 (SESSION_PAGE_SIZE), overridable per call via ReadOptions.sessionPageSize. The 120-session test passes 50 to get three pages; the three-page fixture walk passes 2.
- **Reversibility:** trivial

### W5.10 Whether transition should be callable on a hypothesis with no trusted state row

- **Plan said:** Nothing — only that transitions are between two states.
- **Assumed:** It throws conflict ('has no trusted state row to move from'). Creating the first draft row is appendState, which W8 owns.
- **Reversibility:** trivial

### W5.11 How the title/thesis are carried forward into the row a transition appends

- **Plan said:** Nothing. The board read only has snippets, so this was unspecified.
- **Assumed:** Inside the critical section, after resolving the trusted state row, do a full-content GET /agent/memories/{id} and re-emit its parsed title/thesis/owner_email with the new status. This is also why I captured a memory-by-id fixture.
- **Reversibility:** moderate

### W5.12 Whether trusted appends should set embed:false

- **Plan said:** Nothing about embedding for hypothesis rows (only that O7 defaults embed to true and 400s content >24KB with embed:true).
- **Assumed:** embed:false. Hypothesis state rows are looked up by label, never by semantic search, and an embedded row with a large evaluation snapshot risks the 24KB ceiling for no benefit.
- **Reversibility:** trivial

### W5.13 How to make TRUSTED_KINDS actually 'frozen'

- **Plan said:** 'an exported frozen set'
- **Assumed:** Object.freeze on a Set does not stop .add in JS, so I shadowed add/delete/clear with own properties that throw TypeError and then froze the object. A test asserts the throw and that membership is unchanged. TRUSTED_KIND_LIST is a plain frozen array.
- **Reversibility:** trivial

### W5.14 Which error kind sessionNameForHypothesis / appendState use for a bad id

- **Plan said:** nothing
- **Assumed:** invalid (caller error) per the shared taxonomy, with details {id}.
- **Reversibility:** trivial

### W5.15 The test-harness shape for store.test.ts

- **Plan said:** Nothing beyond the pinned choice of undici MockAgent.
- **Assumed:** A persistent catch-all MockAgent interceptor per method, driving the REAL createBobClient, recording every request (method/path/body) and routing by pathname+query to raw captured fixture text. This is what makes 'exactly one memory request', the URL assertions and the 'no hyp-hyp- anywhere' assertion possible against real client behaviour rather than a hand-written fake.
- **Reversibility:** trivial

### W5.16 Fixture scenario design — which ids, which attacks, which owners

- **Plan said:** Only 'Bob response bodies captured verbatim from a running O11 build'.
- **Assumed:** Four hypotheses: 1a2b3c4d (clean), 2b3c4d5e (forged newer row by researcher-2b3c4d5e/sess-b31f0c9a), 3c4d5e6f (hostile retraction by sess-77c1e2d5), 4d5e6f70 (Wolf's own retraction with a hostile one stacked on top — the B5 resurrection case). Plus three snippet-boundary rows and six sessions including one non-hypothesis (settings-chat) and one researcher-worker session.
- **Reversibility:** moderate

### W5.17 How to capture provenance-bearing rows, given no HTTP route can write them

- **Plan said:** Nothing — it assumes such bodies are capturable.
- **Assumed:** Seed them with agentdb.Store.CreateMemory (the exact call mcp_memory.go:322-328 makes, with the same CreatedByWorker/CreatedBySession fields) from a scratch Go program using a replace directive, then READ them back over real HTTP from the running agentd. The response bodies are therefore genuinely Bob's; only the write of those two fields bypassed HTTP, because a session token from inside a container is the only credential that can set them. Documented prominently in the fixtures README.
- **Reversibility:** moderate

### W5.18 How to run 'a running O11 build' without docker compose (house rule 9 forbids compose up/down)

- **Plan said:** The plan's stack instructions are `docker compose up --build`; the briefing pointed at README-stack.md's mock-mode invocation.
- **Assumed:** Build and run the same agentd binary directly on the host against a fresh throwaway pgvector instance (own name/port, so no collision with the three sibling worktrees' instances), with DOCKER_HOST pointed at the host daemon. I first checked that NO container carried the agentkit.managed=true label, so agentd's boot-time Recover (which force-removes stopped managed containers) could not touch anything belonging to another agent. Mock mode confirmed by the boot log line.
- **Reversibility:** trivial

### W5.19 Whether to create real sessions (and therefore real containers) for the session-index fixture

- **Plan said:** Nothing; the criterion only describes the request shape.
- **Assumed:** Yes — six sessions via POST /agent/session, which really provisioned six mock-model sandbox containers, then all six deleted via DELETE /agent/session/{id} (never docker rm). Verified zero sandbox containers remained. This made the three-page walk, the worker= filter and the hyp-* filter all real rather than invented.
- **Reversibility:** trivial

### W5.20 The 120-session pagination test's data

- **Plan said:** 'A test asserts 120 sessions across three pages all appear in the index.'
- **Assumed:** Synthetic: 120 rows cloned from a CAPTURED row with only id and name varied. Creating 120 real sessions is not something a unit test may do. The test comment says so explicitly, and the field shape is still Bob's.
- **Reversibility:** trivial

### W5.21 Whether to keep the fixture bodies raw or pretty-printed

- **Plan said:** 'captured verbatim … not hand-shaped'
- **Assumed:** Raw, byte-for-byte, compact with the trailing newline Bob emits (W6b's FRED fixtures were re-serialised for readability; I judged 'verbatim' to outrank diff readability here, and the README says so).
- **Reversibility:** trivial

### W5.22 Test file/name organisation

- **Plan said:** 'Test names are prefixed lifecycle_ and store_.'
- **Assumed:** The prefix goes on the individual test titles (and the describe blocks), so the verbose reporter prints it on every line — which is what the Validation greps for.
- **Reversibility:** trivial

### W5.23 Whether to follow W5's literal board-URL criterion or § "The trust model"'s restated rule, which contradict each other

- **Plan said:** The board criterion pins `GET /agent/memories?selector=kind%3Dhypothesis&latest_per=name&limit=100` with 'a follow-up only for ids whose newest row is untrusted'; § "The trust model" says state is resolved from trusted memories read with retractions VISIBLE. Nothing says which wins.
- **Assumed:** The trust model wins. readBoard adds `&include_retracted=1` to the pinned URL, keeping every other parameter and the one-request fast path. The plan needs amending on this point — the board criterion as written mandates the hole.
- **Reversibility:** trivial

### W5.24 When the board's fast path may settle a hypothesis without a follow-up, now that retractions are visible on it

- **Plan said:** 'a per-name follow-up ONLY for ids whose newest row is untrusted' — it does not say what to do when the newest row is trusted but RETRACTED, because with the criterion's unflagged URL that case cannot arise.
- **Assumed:** Settle whenever the newest row is trusted and Wolf has not withdrawn it — a hostile retraction of it raises Tamper and costs nothing extra (a container cannot withdraw Wolf's word, so there is nothing older to look for). Follow up only when the row is untrusted, retracted BY WOLF, or absent. This makes the tampered fixture set cost 2 follow-ups where the previous implementation paid 3.
- **Reversibility:** trivial

### W5.25 Whether a fix round may capture NEW fixtures, and against what

- **Plan said:** W5 requires the include_retracted fixtures be 'captured verbatim from a running O11 build'; it says nothing about later rounds or about which build a second capture should use.
- **Assumed:** Re-capture from the SAME commit the first capture names (af0e0cb, extracted with `git archive` so no repo state was disturbed), with the mock model, a fresh database in the existing throwaway Postgres, and no sessions created. I also seeded the scenario myself (two Wolf rows plus a researcher-written retraction) rather than reusing the first capture's data, since no existing fixture had an older row beneath a hostilely-retracted one.
- **Reversibility:** moderate

### W5.26 The label shape of a retraction WOLF itself writes (needed to capture the fall-through case)

- **Plan said:** Nothing. The plan defines `retracts=<id>` as the attacker's move and says Wolf's own retractions are 'honoured normally', but never gives the kind or name labels Wolf would use.
- **Assumed:** `{kind: "retraction", name: <id>, retracts: <memory id>}` for the captured fixture only — deliberately NOT `kind=hypothesis`, so Wolf's own retraction never shows up as a state row on the board. No code in store.ts writes retractions yet; if a later ticket adds that write it should agree with this shape or change the fixture.
- **Reversibility:** moderate

### W5.27 Which body the test stub returns for a board read that omits include_retracted

- **Plan said:** Nothing — the plan has no stub design.
- **Assumed:** The stub answers the flagged and unflagged board reads from DIFFERENT config fields, the way Bob does, and the unflagged one defaults to the empty page. That makes every board test in the file fail if the flag is ever dropped, rather than only the resurrection ones.
- **Reversibility:** trivial

### W5.28 Whether the pre-existing 'exactly one provenance field is non-empty' deviation could be closed in this round

- **Plan said:** § "Shared shapes" pins 'exactly one of the two provenance fields is non-empty'.
- **Assumed:** It cannot be closed by a worker: real Bob sets BOTH for any session that has a worker (recorded twice now — detail-2b3c4d5e, and my own capture's researcher-1a2b3c4d/sess-c0ffee11), so 'exactly one' is unsatisfiable, and correcting the plan is the orchestrator's act, not mine (house rule 1). Left relaxed to 'at least one', documented in the file header, in a recorded-fact test and in the commit body.
- **Reversibility:** moderate

## W12 — 13 guesses

### W12.1 Where the bootstrap script gets BOB_BASE_URL and WOLF_API_KEY (the client baseUrl/apiKey pair) since W2's client takes them as constructor args and my Files line forbids adding them to config.ts/.env.example (those belong to W8/W9's not-yet-landed additions)

- **Plan said:** nothing explicit; the ticket says the read-merge-write helper/worker-create/schedule-create must 'come from W2' but is silent on where the client's own credentials come from for a standalone bootstrap script run before W8 lands
- **Assumed:** runBootstrapFromEnv() reads process.env.WOLF_API_KEY (required, misconfigured if absent) and process.env.BOB_BASE_URL (optional, default http://localhost:8099 matching the documented topology constant) directly, bypassing api/src/config.ts's WolfConfig entirely for these two — undocumented in .env.example since that file's W12 allowance is only the two named variables
- **Reversibility:** trivial

### W12.2 How to check worker-already-correct state for idempotency, since W2's 22-route client list has putWorker/deleteWorker but no GET /agent/workers/{name} or list-workers route, and the ticket's idempotency criterion requires zero PUT calls on a second run

- **Plan said:** 'the project-settings read-merge-write helper, worker create and schedule create must come from W2 — do not hand-roll a second HTTP client here'; silent on how worker reads (which W2 doesn't expose) should work
- **Assumed:** wrote one narrowly-scoped raw fetch (readWorker in bootstrap-project.ts) hitting Bob's real GET /agent/workers/{name} route directly (confirmed to exist server-side at go/httpapi/workers.go:77, just not wrapped by W2's client), used only for this one read, not a second general-purpose client
- **Reversibility:** moderate

### W12.3 Schedule idempotency match key: match by worker name alone ('critic'), ignoring whether the existing schedule's cron equals the currently-configured WOLF_CRITIC_CRON

- **Plan said:** nothing — the ticket only says the schedule is created idempotently, not how a cron *change* across runs should be handled (no updateSchedule route exists in W2's client either)
- **Assumed:** if any schedule with worker==='critic' exists, skip creation entirely (never delete+recreate to pick up a changed cron) — a deliberate simplification; an operator changing WOLF_CRITIC_CRON after first bootstrap must delete the old schedule by hand
- **Reversibility:** moderate

### W12.4 installations/wolf/Dockerfile installs python3-pip (and uses --break-system-packages) as the mechanism to get pandas/numpy/duckdb onto a PEP-668 (Debian bookworm) base, in addition to the three named packages plus python3

- **Plan said:** 'adds python3, pandas, numpy and duckdb and nothing else'
- **Assumed:** python3-pip is a necessary installer, not a fourth unrelated package, so it doesn't violate 'nothing else' — pandas/numpy/duckdb are pip packages, not apt packages, on this base image
- **Reversibility:** trivial

### W12.5 WOLF_CRITIC_CRON validation strictness: I implemented only a shape check (exactly 5 whitespace-separated fields, no '@' nickname), not per-field range/grammar validation

- **Plan said:** 'Never a nickname: go/agentdb/schedules.go:827 refuses @weekly' — no other cron-validation detail specified
- **Assumed:** a full cron grammar validator is out of scope; Bob's own schedule store is the authority on field-range validity and will reject a truly malformed cron at POST /agent/schedules time — config.ts's job is only to catch the nickname/field-count class of error before that HTTP round-trip
- **Reversibility:** trivial

### W12.6 Worker PUT bodies for interviewer/critic set only systemPrompt+enabled+rationale, leaving description/image/maxInstances/briefing/mcpConfig unset (Bob defaults apply: image inherits the project's base_image, maxInstances=1, frozen=false)

- **Plan said:** 'worker interviewer, prompt = prompts/interviewer.md verbatim, enabled' / same for critic — no mention of the other PutWorkerParams fields
- **Assumed:** leaving them unset is correct: per-worker mcp_config is unnecessary since the wolf MCP server is registered at project-settings level and compose.go merges project+worker mcp_config at job-composition time; per-worker image override is unnecessary since project base_image is already set to WOLF_BASE_IMAGE
- **Reversibility:** trivial

### W12.7 The 'rationale' string content on every write (project-settings PUT, worker PUTs, schedule POST) — not pinned by the ticket

- **Plan said:** nothing specific beyond 'PUT is a whole-object replace' and (for critic.md's own future writes, not the bootstrap's) 'requires a rationale on every worker_prompt_write'
- **Assumed:** wrote a short descriptive rationale naming the ticket/design doc for traceability in Bob's config-event log; exact wording is mine
- **Reversibility:** trivial

### W12.8 scripts/bootstrap-project.ts's shebang/run mechanism (npx tsx) and that it deliberately isn't wired into package.json's own scripts (no 'yarn bootstrap' added)

- **Plan said:** 'a thin scripts/bootstrap-project.ts that only imports and runs the former' — no run-mechanism specified, and package.json isn't on this ticket's Files line
- **Assumed:** documented `npx tsx scripts/bootstrap-project.ts` as the invocation in a header comment rather than adding a package.json script, since package.json isn't in my Files list and I didn't want to expand scope
- **Reversibility:** trivial

### W12.9 Whether the marker line '<!-- WOLF:METHOD-BODY -->' may appear inside prose elsewhere in researcher-method.md as a quoted reference

- **Plan said:** not explicit, but the acceptance criteria's prompt-contract test only asserts the marker occurs 'in the preamble and in critic.md', implying its absence from method.md is expected though not literally graded as a negative assertion in the ticket text
- **Assumed:** added a defensive negative test ('the method body contains no marker line of its own') and fixed a first draft that had accidentally quoted the marker inside researcher-method.md's own prose (caught by that test failing) — treated any occurrence in method.md as a bug
- **Reversibility:** trivial

### W12.10 How to execute the loader script's DinD happy path (the ticket's Validation #2/#3) when the Bob compose stack is down and house rule 9 forbids `docker compose` up/down

- **Plan said:** "with agent-bob's stack up: ./scripts/load-image-into-dind.sh" — the plan assumes the stack is running and says nothing about what to do when it is not
- **Assumed:** That a throwaway DinD daemon started with a plain `docker run -d --rm --privileged --name w12-fix-dind docker:27-dind` (the exact image compose uses, same server version 27.5.1), seeded with the host's REAL agentkit-sandbox:dev via docker save|load, and driven through the script's own BOB_DIND_CONTAINER variable, is a faithful stand-in for agent-bob-dind-1. It exercises everything the criterion names except the literal default container NAME. Nothing was fabricated: the sandbox base is a real image built from this repo's sandbox/, core and wolf were really built inside the DinD daemon, and the import really ran.
- **Reversibility:** trivial

### W12.11 Whether to add a test assertion beyond the seven literals the ticket enumerates

- **Plan said:** The prompt-contract criterion lists exactly seven literals to assert; it does not forbid more
- **Assumed:** That pinning `<!-- WOLF:METHOD-BODY -->` as occurring exactly ONCE AS A WHOLE LINE in the preamble is in-scope hardening of W12's own contract, since the preamble also mentions the marker in prose at line 6 and a substring-based splitter in W9 would cut the document at the wrong place. Added one test (24 not 23) plus a comment saying why the split must be line-anchored.
- **Reversibility:** trivial

### W12.12 How much of the attention_channel rationale to write, and what to cite

- **Plan said:** "A comment in the bootstrap says so, so a later reader does not 'fix' it by inventing a URL" — it does not say how long the comment should be or what it should cite
- **Assumed:** A comment at the write site naming the engine file and line range (go/cmd/agentd/attention.go:57-103), the fact that Wolf's API mounts no inbound webhook route, and W10's polling of GET /agent/attention-requests as the replacement surface. I verified the engine claim by reading attention.go:50-105 myself rather than trusting the verifier's citation, and reworded a first draft that referenced `api/src/routes/` after finding that directory does not exist on this branch.
- **Reversibility:** trivial

### W12.13 Number of commits on the branch

- **Plan said:** House rule 5 says one commit unless the work genuinely splits; the fix-round brief says "add commits on top"
- **Assumed:** The fix-round brief wins: the branch now carries two commits (889a961 the original, a9c79e6 the fix round). Squash if the orchestrator prefers one.
- **Reversibility:** trivial


## O8 — 10 guesses

### O8.1 Where the two new duration-knob boot-log test cases should live

- **Plan said:** "new boot-log cases extend TestGCConfigBootLines"
- **Assumed:** Added the dataset-reap boot-line assertions as additional statements inside the existing TestGCConfigBootLines function body (not a sibling test function), so a -run filter on that exact name still exercises them.
- **Reversibility:** trivial

### O8.2 The exact wording and format of describeDatasetReap's active-state message

- **Plan said:** "names both variables in describeReapInterval's style" -- but describeReapInterval's own active line does NOT name its variable, only describeIdleTimeout does implicitly via context
- **Assumed:** Made the active dataset-reap line explicitly print both AGENTKIT_DATASET_REAP_INTERVAL and AGENTKIT_DATASET_KEEP_VERSIONS by name (not just their values), since the criterion says "names both variables" and an operator reading the log benefits from it -- my first draft omitted the names and I corrected it after my own test caught it.
- **Reversibility:** trivial

### O8.3 Relative path depth from go/cmd/agentd/ to the repo-root .env.example in TestProjectMapExample

- **Plan said:** the ticket's literal text says the test reads the line "out of ../../.env.example" (two ../ levels)
- **Assumed:** That literal path is a plan defect -- go/cmd/agentd/ is three directory levels below the repo root (agentd -> cmd -> go -> root), so I used "../../../.env.example" (filepath.Join("..","..","..",".env.example")), matching the depth convention already used by cmd/hypolabgen, cmd/triagelabgen and cmd/gauntletgen's own test fixtures. Verified go/.env.example does not exist and .../O8/.env.example does, and the test only passes with three levels. Reporting as a discovered issue below since I cannot edit the plan.
- **Reversibility:** trivial

### O8.4 How to distinguish the two AGENTKIT_PROJECT_MAP example lines .env.example now contains (the pre-existing legacy flat form and O8's new object form) when writing a test that must read "the" line

- **Plan said:** nothing -- the ticket assumes there is one line to read, but the pre-existing legacy example ("kaiyadavenport@gmail.com":["*"]) was already in the file and I judged it out of scope to remove or replace (a different ticket/section owns it)
- **Assumed:** Kept both lines and made TestProjectMapExample select the one containing the substring \"wolf\" (unique to the object-form worked example), also asserting exactly one such line exists so a future edit that duplicates or drops it fails loudly rather than silently reading the wrong one.
- **Reversibility:** moderate

### O8.5 Whether to also update the pre-existing generic AGENTKIT_MCP_ENV=/GMAIL_API_KEY=/NOTION_AUTH= placeholder block further down .env.example to reference WOLF_MCP_TOKEN

- **Plan said:** "sets AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN in its worked example" without saying exactly where
- **Assumed:** Left the pre-existing generic MCP-credentials placeholder block untouched (it is a template for arbitrary future credentials, not Wolf-specific) and instead added the AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN worked line inside the new Wolf-credentials section next to WOLF_MCP_TOKEN's own comment, where it reads as a concrete instruction rather than a generic template.
- **Reversibility:** trivial

### O8.6 Exact narrow interface shape for the reaper's store and blob dependencies (for testability with fakes)

- **Plan said:** nothing -- only named the two agentdb.Store methods and 'a fake extension.BlobStore and a fake store' for the tests
- **Assumed:** Declared datasetReapStore (ReapDatasetVersions + ListOrphanBlobPaths) and datasetReapBlobs (Delete + List) as package-local narrow interfaces in datasetreaper.go, mirroring the existing agentdb.DatasetBlobDeleter/DatasetBlobLister split-interface convention, rather than depending on *agentdb.Store or extension.BlobStore concretely.
- **Reversibility:** trivial

### O8.7 How to make the two-pass >=1h orphan safety unit-testable without a real 1-hour wait

- **Plan said:** nothing about test mechanics for timing
- **Assumed:** Added an injectable `now func() time.Time` field on datasetReaper (defaulting to time.Now, overridden with a fakeClock in tests) rather than real sleeps or a wall-clock-dependent test.
- **Reversibility:** trivial

### O8.8 What happens to a tracked orphan-sighting entry when the belt-and-braces prefix check fails on its second sighting

- **Plan said:** nothing -- the prefix guard is described only for the version reaper's candidate loop (skip, don't delete, don't count)
- **Assumed:** For the orphan sweep specifically, a path that fails the prefix check on its second-sighting pass is dropped from the tracking map entirely (never retried), on the reasoning that a foreign-prefix path reported by ListOrphanBlobPaths is either a lister bug or a genuinely foreign key this reaper has no business remembering across passes.
- **Reversibility:** moderate

### O8.9 Log line phrasing for individual delete-failure lines vs. the one pinned per-pass summary line

- **Plan said:** pins only the per-pass summary line's exact shape
- **Assumed:** Added an additional per-failure log line ('[agentd] datasets: delete orphan blob %q: %v') beyond the pinned summary, so an operator can see which path failed, not just the count.
- **Reversibility:** trivial

### O8.10 Whether the dataset reaper's boot-availability log line should sit beside the idle/reap lines (before Runner construction) or later, beside the loop's actual start

- **Plan said:** nothing about ordering, only that the loop itself starts 'after the store is built and cancelled on shutdown'
- **Assumed:** Split it: the descriptive boot line logs early (grouped with describeIdleTimeout/describeReapInterval, before Runner construction, since gc is resolved there and agentDB's nilness is already known) while the actual goroutine start happens later, inside the `if agentDB != nil` block beside router/scheduler/attention -- because gc.datasetReapInterval and gc.datasetKeepVersions are available immediately but `blobs` and the dispatcher/router setup context are only ready in that later block.
- **Reversibility:** trivial

