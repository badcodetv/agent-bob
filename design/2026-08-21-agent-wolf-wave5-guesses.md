# Agent Wolf — wave 5 guess log

Every place a wave-5 implementer had to decide something `design/2026-08-20-agent-wolf.md`
did not decide for it. Collected from the implementers' own structured returns. Recorded 2026-08-21.

Wave 5 = O6b and W8 (phase A) and W15 (phase B, serialised behind W8 on `api/src/hypothesis/store.ts`).

**Phase A total: 73 guesses** — O6b 25, W8 48. W15 appends below when it lands.

## O6b — 25 guesses

### O6b.1 dataset_list's top-level envelope beyond the pinned {"datasets":[…]}

- **Plan said:** `dataset_list` → `{"datasets":[{…ten fields…}]}`; nothing about count/limit/note keys
- **Assumed:** Added `count` always, and `limit` + `note` only when something needs saying — following skill_list's precedent (mcp_skills.go returns skills/count/truncated/note). The `datasets` array itself is byte-exact to the pin.
- **Reversibility:** trivial

### O6b.2 How the limit cap is 'stated in the result'

- **Plan said:** "limit defaults to 20, max 100, and the cap is stated in the result rather than truncating silently" — no field name, no wording
- **Assumed:** A human-readable `note` string naming 100, emitted when the caller asked for more than 100. I also added a second note for the exactly-full-page case (len(results)==limit), which the plan did not ask for, because silence there reads as 'that is all of them'.
- **Reversibility:** trivial

### O6b.3 A negative `limit` argument

- **Plan said:** nothing
- **Assumed:** limit <= 0 is the 20 default, and is NOT reported as capped.
- **Reversibility:** trivial

### O6b.4 Whether the download URL carries ?version= when the caller omitted version

- **Plan said:** the URL format `…/download?version=<v>&token=<t>`, but not what <v> is when dataset_get was called without a version
- **Assumed:** Always emit the RESOLVED version (the row's), so the URL and the returned `version` field agree and a URL minted now still fetches the same bytes after a tick. The alternative — omitting the parameter so the download follows 'current' — would make the URL and the metadata able to disagree.
- **Reversibility:** trivial

### O6b.5 Query-string construction

- **Plan said:** the literal `?version=<v>&token=<t>`
- **Assumed:** Built by hand rather than url.Values.Encode(), which sorts keys and would emit token before version. Name goes through url.PathEscape, token through url.QueryEscape (both are no-ops for legal values; defensive only).
- **Reversibility:** trivial

### O6b.6 Whether the CAS conflict is pre-checked before the (expensive) container pull

- **Plan said:** "On conflict the blob O6a just wrote is best-effort deleted … A test asserts the delete was attempted"
- **Assumed:** No pre-check. Pull first, then let the store's CAS be the only authority, so the conflict always arrives after the blob exists and the delete-on-conflict path is the real one rather than a race-only path. Cost: a wasted pull on every conflict.
- **Reversibility:** moderate

### O6b.7 When the current version is read for the shrink guard

- **Plan said:** the guard's rule, not the read's placement
- **Assumed:** Read CurrentDataset AFTER the pull and immediately before the CAS, so the row_count compared is as fresh as possible. A CurrentDataset error that is not ErrDatasetNotFound aborts the write and deletes the blob.
- **Reversibility:** trivial

### O6b.8 Blob cleanup on failures other than the CAS conflict

- **Plan said:** only the conflict case
- **Assumed:** Best-effort delete on EVERY post-write failure: a shrink refusal, an unexpected CurrentDataset error, and any non-conflict store error. Strictly safer; O3's orphan sweep remains the backstop.
- **Reversibility:** trivial

### O6b.9 Whether 'below 50%' includes exactly 50%

- **Plan said:** "row_count below 50% of the current version's"
- **Assumed:** Exactly half is ALLOWED: the test is `2*new < current` (integer, no float rounding to argue about). Pinned by a subtest named 'exactly half is allowed'.
- **Reversibility:** trivial

### O6b.10 The download token's TTL

- **Plan said:** nothing about TTL at the tool level; O4 clamps to [60,900] with a 300s default
- **Assumed:** Pass 0 → O4's 300s default, and expose no TTL argument to the model.
- **Reversibility:** trivial

### O6b.11 AGENTKIT_DATASET_MAX_BYTES grammar

- **Plan said:** "default 64MiB … an unparseable value is a boot error naming the variable"
- **Assumed:** A plain integer COUNT OF BYTES only — no '64MiB'/'64M' suffix grammar. Empty/whitespace is the default; 0 and negatives are errors (an operator who wrote 0 meaning 'unlimited' gets told, rather than silently disabling every write).
- **Reversibility:** moderate

### O6b.12 Where in main.go the variable is parsed

- **Plan said:** "parsed once at boot in main.go"
- **Assumed:** Unconditionally, beside the GC config resolution (main.go:~306) rather than inside the `if agentDB != nil` block — so a typo fails boot even on the sqlite fallback where the tools are never mounted.
- **Reversibility:** trivial

### O6b.13 Names of the narrow interfaces and the tool struct

- **Plan said:** "a narrow interface declared in mcp_datasets.go that agentkit.Runner satisfies, exactly as sessionSnapshotter / sessionLocator do"
- **Assumed:** `datasetCatalog` (the four store methods) and `sessionExecer` (ExecInSession alone, with `var _ sessionExecer = (agentkit.Runner)(nil)`), plus `datasetTools`/`newDatasetTools` mirroring imageTools/skillTools.
- **Reversibility:** trivial

### O6b.14 How the tools are made testable without Docker

- **Plan said:** nothing — O6a injected its exec seam, but nothing said how O6b would inject the pull
- **Assumed:** A `pull` function field on datasetTools defaulting to pullWorkspaceFile, so one test can assert exactly what maxBytes/contentType/relPath reached the pipeline. Every other test drives the real pullWorkspaceFile over O6a's own fakeSession.
- **Reversibility:** trivial

### O6b.15 ExecInSession's error when workerEnvFor fails (as opposed to a nil instance)

- **Plan said:** "error `exec-in-session: session %q has no running instance` when either is absent"
- **Assumed:** Same pinned sentence for both, with the workerEnvFor cause wrapped on the end (`… has no running instance: %w`), so the pinned substring holds in both cases without discarding the cause.
- **Reversibility:** trivial

### O6b.16 What the two out-of-package stubRunners return from ExecInSession

- **Plan said:** only that they must gain the method
- **Assumed:** `&execenv.ExecResult{}, nil` (an inert success) rather than nil or an error — neither package's code path calls it, and a nil result would panic anything that later did.
- **Reversibility:** trivial

### O6b.17 Whether dataset_list / dataset_get also refuse an unidentified caller

- **Plan said:** the refusal is stated for the WRITE only
- **Assumed:** Reads are allowed for an unidentified caller: the project scope still comes from the verified token, and provenance is irrelevant to a read.
- **Reversibility:** moderate

### O6b.18 dataset_put's behaviour when no session runtime is wired (d.sessions == nil)

- **Plan said:** nothing
- **Assumed:** Refuse with "dataset_put is not available on this deployment: no session runtime is wired", copying skill_install's precedent.
- **Reversibility:** trivial

### O6b.19 Where content_type defaults and where labels are validated

- **Plan said:** the Interfaces default of text/csv; the store also validates
- **Assumed:** Default applied in the tool (so the value the pull pipeline sees for row-counting and the value stored are the same), and labels validated in the tool as well as the store so the model gets the validator's message rather than a wrapped DB error — the mcp_skills.go precedent.
- **Reversibility:** trivial

### O6b.20 The exact wording of every model-facing error (not-found, bad version, conflict, shrink refusal, unidentified caller)

- **Plan said:** what each must NAME, not how
- **Assumed:** Wrote them as instructions to a model: each names the remedy (dataset_list, dataset_get with the current version, if_version: N, allow_shrink: true) and every refusal opens with "nothing was written".
- **Reversibility:** trivial

### O6b.21 How to gate 'registration is one line inside the if agentDB != nil block'

- **Plan said:** the criterion, no test shape
- **Assumed:** A test that reads main.go's source, locates the span between `mcpSrv := newMCPServer(` and the boot-log line, and asserts the register call is inside it and appears exactly once. Source-scanning is unusual here but it is the only way to gate a placement criterion; TestProjectMapExample (O8) set the precedent of a test reading a file rather than trusting it.
- **Reversibility:** trivial

### O6b.22 How to gate 'the boot log's tools= list names all three' without booting agentd

- **Plan said:** the criterion
- **Assumed:** Register the tools onto a real mcpServer and assert `strings.Join(sortedStrings(srv.toolNames()), ",")` — the exact expression main.go logs — equals "dataset_get,dataset_list,dataset_put".
- **Reversibility:** trivial

### O6b.23 How to hold up my half of O5's byte-identity claim

- **Plan said:** "Field names are byte-identical … pin your side literally too"
- **Assumed:** Copied O5's literal pinned body into my test as a const (`o5PinnedMetadataBody`) and compared key sets AND values field by field, excluding O5's `id`. This proves the two documented shapes agree today; it does NOT track a future change on O5's side (my copy would go stale silently).
- **Reversibility:** moderate

### O6b.24 Test file/test naming and fake design

- **Plan said:** the `TestDatasetTools…` and `TestExecInSession…` prefixes
- **Assumed:** 24 cases under TestDatasetTools*, reusing O6a's `fakeSession` and mcp_images_test.go's `invokeTool`, plus a new `invokeToolRaw` (exact JSON bytes, for the pinned body) and a `recordingBlobs` wrapper around agentkittest.MemBlobs so deletes can be asserted as ATTEMPTED rather than inferred.
- **Reversibility:** trivial

### O6b.25 dataset_list's argument is named `selector`, while every other core MCP tool calls the same thing `label_selector`

- **Plan said:** § Interfaces pins `dataset_list(selector?: string, limit?: number)`
- **Assumed:** Followed the plan and used `selector`. Worth knowing that memory_search and skill_list both use `label_selector`, so a model that has learned one name will get a hard error from decodeArgs (unknown field) on the other — which is at least loud rather than silent.
- **Reversibility:** moderate

## W8 — 48 guesses

### W8.1 WHERE the boot-time presence checks for WOLF_SESSION_SECRET / WOLF_ALLOWED_EMAILS / WOLF_API_KEY live

- **Plan said:** 'parsed in api/src/config.ts', and the secret is 'required at boot (misconfigured naming it if absent or shorter than 32 characters)'; 'Unset or empty is a boot-time misconfigured failure naming the variable'
- **Assumed:** Shape validation in loadConfig (secret length, allowlist token form, URL form), but PRESENCE enforced in createApp via a new exported assertSessionConfigured(config). Reason: loadConfig() is also what scripts/bootstrap-project.ts (W12) runs through, and that tool signs nobody in — making loadConfig throw would make the bootstrap unrunnable without a session secret it never reads. This follows W7's identical split for WOLF_MCP_TOKEN. createApp is on every boot path and index.ts fatals with one line naming the variable.
- **Reversibility:** trivial

### W8.2 Whether to implement POST /api/auth/dev-login at all

- **Plan said:** W8's acceptance criteria do not mention it; the route table lists it and owner decision B6 assigns it to 'W8; X1's criteria'
- **Assumed:** Implemented it (mounted only when WOLF_TEST_LOGIN is set). Without it X1 has no offline sign-in and the plan names no other owner.
- **Reversibility:** trivial

### W8.3 dev-login's request body

- **Plan said:** 'POST /api/auth/dev-login { email } → session cookie' in the route table, but WOLF_TEST_LOGIN="email:password" in X1
- **Assumed:** { email, password }, both required, compared constant-time against the parsed pair. A route that accepts any allowlisted email with no secret is a sign-in bypass. X1's run.sh knows the password because it sets the variable — but X1's spec must send it.
- **Reversibility:** trivial

### W8.4 What 'refusing to boot alongside production settings' (B6) means concretely

- **Plan said:** Only that phrase
- **Assumed:** loadConfig throws misconfigured when WOLF_TEST_LOGIN is set and NODE_ENV === 'production'. ⚠️ .env.example ships NODE_ENV=production, so X1's run.sh must set NODE_ENV=development or test for the Wolf stack.
- **Reversibility:** trivial

### W8.5 Whether the allowlist also applies to dev-login

- **Plan said:** nothing
- **Assumed:** It does — the test login skips Google, never the allowlist.
- **Reversibility:** trivial

### W8.6 The cookie's value format and whether expiry is checked server-side

- **Plan said:** only '12h maxAge'
- **Assumed:** JSON {email, iat_ms} inside the signed value, and requireSignedIn rejects a cookie older than 12h itself. maxAge is browser-enforced only and the signature never expires, so a client replaying a stale cookie would otherwise stay signed in forever.
- **Reversibility:** moderate

### W8.7 Cookie Path and clearing behaviour

- **Plan said:** nothing
- **Assumed:** path:'/'; requireSignedIn does NOT clear an expired cookie (it is a bare middleware with no config, and mismatched flags make clearCookie a no-op anyway). clearSessionCookie is exported for a future logout route.
- **Reversibility:** trivial

### W8.8 How strict WOLF_ALLOWED_EMAILS entries must be

- **Plan said:** 'full Google addresses'
- **Assumed:** A token must match /^[^\s@,]+@[^\s@,]+\.[^\s@,]+$/ or boot fails; a bare domain ('@badcode.dev') and '*' are refused rather than silently dropped or read as a domain rule.
- **Reversibility:** trivial

### W8.9 What to do with a 200 carrying email_verified:false

- **Plan said:** nothing (Orange only ever returns true today)
- **Assumed:** 403 forbidden, no cookie.
- **Reversibility:** trivial

### W8.10 How to map Orange's 403 on /auth/verify-google ('project api key required')

- **Plan said:** only 200/401/404 are enumerated
- **Assumed:** misconfigured naming WOLF_API_KEY — it is Wolf's own credential being refused, and reporting it as forbidden would blame the person signing in.
- **Reversibility:** trivial

### W8.11 The success body of POST /api/auth/google and dev-login

- **Plan said:** '{ credential } → session cookie'
- **Assumed:** 200 with { email } (the lowercased address), so the UI can render who is signed in without a separate route.
- **Reversibility:** trivial

### W8.12 Detecting Orange's 401 on verify-google

- **Plan said:** '401 → forbidden'
- **Assumed:** Checked err.status === 401 rather than err.kind: the W2 client's classifyStatus has no 401 case, so a 401 arrives as kind 'internal' with status 401 preserved. (Reported as a discovered issue.)
- **Reversibility:** trivial

### W8.13 POST /api/hypotheses request body beyond title

- **Plan said:** '{ title } → { id }'
- **Assumed:** title required (trimmed, 1–500 chars) plus an OPTIONAL thesis string (≤20k) written into the memory below line 1. An empty/whitespace title is 400 invalid and creates nothing.
- **Reversibility:** trivial

### W8.14 The create-poll bounds and whether they are env-configurable

- **Plan said:** 'polls … until the status leaves creating, bounded; on timeout, unavailable'
- **Assumed:** 500ms interval / 30s timeout as router-factory options (injectable for tests), NOT new env variables — the criteria name no variable and every duration variable would need three more places (R81).
- **Reversibility:** trivial

### W8.15 What counts as 'leaving creating'

- **Plan said:** 'until the status leaves creating'
- **Assumed:** status 'error' → unavailable 503 with create_error verbatim; ANY other non-'creating' status (Orange writes 'running') → success.
- **Reversibility:** trivial

### W8.16 The HTTP status for the port-pool refusal arriving on the POST (not the poll)

- **Plan said:** 'unavailable, HTTP 503' is stated for the polled path; the direct 403 is only required to keep its message
- **Assumed:** Restated the client's kind-unavailable-with-status-403 error as HTTP 503 with the message passed through untouched, so both paths answer identically and a retryable kind is not served behind a non-retryable status. My first test failed on exactly this and I chose to normalise rather than assert 403.
- **Reversibility:** trivial

### W8.17 The error payload shape for the two 409s

- **Plan said:** '409 session name already taken → conflict; 409 worker … is disabled → conflict'
- **Assumed:** One conflict error naming the session, carrying Orange's body in details.upstream and upstreamBody (they need different fixes, so the upstream text is preserved rather than branched on).
- **Reversibility:** trivial

### W8.18 The details payload of the 404 no-worker error

- **Plan said:** 'misconfigured naming W12's bootstrap'
- **Assumed:** Message names scripts/bootstrap-project.ts (W12) and details is {worker:'interviewer', fix:'scripts/bootstrap-project.ts'} — WolfError.misconfigured's details.variable is for env variables and 'interviewer' is not one.
- **Reversibility:** trivial

### W8.19 Extra field on the board row

- **Plan said:** 'returns, per row: id, title, owner, status, support_score, conditions_summary, updated_at_ms, tamper?'
- **Assumed:** Also title_truncated (W5 deliberately reports when the 500-char snippet cut line 1, and dropping that signal would let the UI claim a truncated title is complete). The shape is documented as open for extension because W22 adds headline.
- **Reversibility:** trivial

### W8.20 The shape of conditions_summary

- **Plan said:** the field name only
- **Assumed:** { tripped, holding, indeterminate, evaluated_at_ms } (the summary line's four non-score fields, with the RFC3339 'evaluated' converted to unix ms per the unit-in-the-name rule), or null when there is no readable evaluation.
- **Reversibility:** moderate

### W8.21 How an UNTRUSTED or retracted kind=evaluation row is treated

- **Plan said:** nothing — the trust model covers state rows, and `evaluation` is in TRUSTED_KINDS
- **Assumed:** A forged evaluation row sets no score and is logged at warn, NOT surfaced as tamper (the board's tamper array stays W5's state-row array; adding to it would make the board report tamper the detail page does not). A row Wolf itself retracted is skipped; a hostile retraction changes nothing.
- **Reversibility:** moderate

### W8.22 Whether the kind=evaluation board read carries include_retracted=1

- **Plan said:** the two board URLs are printed WITHOUT it (the state one gained it only via R90)
- **Assumed:** Yes — the same resurrection reasoning applies: without the flag a hostile retraction of the newest evaluation row hands back the older one and silently rolls the displayed score back.
- **Reversibility:** trivial

### W8.23 Parser edge rules for the summary line

- **Plan said:** 'all five keys are required, unrecognised key=value tokens are ignored'
- **Assumed:** first occurrence of a key wins; tripped/holding/indeterminate must be non-negative safe integers; score must be a finite number; evaluated must be Date.parse-able; anything else yields null and never throws.
- **Reversibility:** trivial

### W8.24 The signature of the new store read

- **Plan said:** nothing (it only pins where the parser lives)
- **Assumed:** readEvaluationSummaries(sessions: SessionLookup): Promise<Map<id, EvaluationSummary>> — the route passes the board's own id Set, so the trust rule's session clause is enforced without a second session-index read.
- **Reversibility:** moderate

### W8.25 Casing and field set of the detail response's `hypothesis` object

- **Plan said:** '{ hypothesis, spec, … }' with no field list
- **Assumed:** snake_case on the wire (matching the board's pinned updated_at_ms/support_score): {id, session_name, session_id, title, title_truncated, owner, status, status_memory_id, updated_at_ms, restated_from, tamper?}.
- **Reversibility:** moderate

### W8.26 The values of spec_source

- **Plan said:** 'spec_source says which of the two it is'
- **Assumed:** the memory kinds themselves — 'hypothesis-spec' | 'hypothesis-spec-candidate' | null.
- **Reversibility:** trivial

### W8.27 How to get the spec JSON out of a memory's content

- **Plan said:** a candidate is 'Line 1 is a summary; then the proposed spec JSON'; a locked spec is 'The spec JSON'
- **Assumed:** One tolerant extractor: a ```json fence if present, else the slice from the first { to the last }. Handles both shapes and a fenced one; unparseable ⇒ spec null with spec_validation.valid false, never a 500.
- **Reversibility:** trivial

### W8.28 spec_validation when there is no spec at all

- **Plan said:** 'comes from W3's validator run over whichever spec was returned'
- **Assumed:** { valid:false, errors:[{path:'$', message:'no spec has been proposed yet …'}] } so W13's Go Live button always has a blocking reason to list.
- **Reversibility:** trivial

### W8.29 What the detail's `evaluation` field contains

- **Plan said:** the field name only
- **Assumed:** The parsed EvaluationResult snapshot from the newest TRUSTED kind=evaluation memory's full content (null when absent/unreadable) — not the summary line, which the board already carries.
- **Reversibility:** moderate

### W8.30 What the detail's `verdict` field contains

- **Plan said:** the field name only
- **Assumed:** { id, status (the label), content (a FULL-content read, because the rationale and deciding user live below the snippet), created_at_ms } from the newest trusted kind=verdict row, or null.
- **Reversibility:** moderate

### W8.31 Shape and volume of notes/amendments

- **Plan said:** 'returned as untrusted evidence and each row carries its writing worker or session'
- **Assumed:** Snippet rows (no full-content read): {id, snippet, status, created_at_ms, created_by_worker, created_by_session}, limit 50, newest first, and WITHOUT include_retracted — a retracted note stays hidden, which is the normal 'withdrawn' behaviour for untrusted evidence.
- **Reversibility:** moderate

### W8.32 Whether the trusted-kind detail reads apply the retraction rule

- **Plan said:** the rule is stated for kind=hypothesis only
- **Assumed:** spec / evaluation / verdict reads carry include_retracted=1 and skip a row only when a retraction with EMPTY provenance exists; a hostile retraction is ignored. Consistent with § 'Retraction'.
- **Reversibility:** moderate

### W8.33 The SessionLookup used for the detail's trust checks

- **Plan said:** nothing
- **Assumed:** new Set([id]) — every row read is selected by name=<id> and store.readHypothesis has already proved that id has a hyp-<id> session, so clause 3 holds; this avoids a second session-index walk.
- **Reversibility:** trivial

### W8.34 Which attention requests to list

- **Plan said:** 'read from GET /agent/attention-requests'
- **Assumed:** state:'open' (Orange's own default) — an answered or timed-out ask is not actionable on a detail page.
- **Reversibility:** trivial

### W8.35 How to find the schedule id for atoms

- **Plan said:** 'the schedule id (null before go-live)'
- **Assumed:** GET /agent/schedules and match worker === researcher-<id>; a failure of that call (or of the attention-request call) degrades to null/[] with a warn rather than failing the whole detail page.
- **Reversibility:** trivial

### W8.36 Which spec the dataset names come from when only a candidate exists

- **Plan said:** 'the <id>-<slug> dataset names from the locked spec'
- **Assumed:** [] unless spec_source is the LOCKED kind; names derived from raw metrics[].slug strings, not from the validated object, so a locked-but-now-invalid spec still names its datasets.
- **Reversibility:** trivial

### W8.37 How narrowly to mount requireSignedIn on the hypotheses router

- **Plan said:** 'mounted PER ROUTER, never globally'
- **Assumed:** router.use('/api/hypotheses', requireSignedIn) rather than router.use(requireSignedIn), so a request the router does not serve passes through (a 404 stays a 404) and a later ticket mounting a public route after it is not silently 401ed.
- **Reversibility:** trivial

### W8.38 Test mechanics for route tests

- **Plan said:** 'HTTP mocking in tests: undici MockAgent'
- **Assumed:** MockAgent for Orange plus mockAgent.enableNetConnect(host => host.startsWith('127.0.0.1')) so the app under test can be a real express server on a random port (W5's store tests never needed this because they made no local requests).
- **Reversibility:** trivial

### W8.39 Provenance of the new test bodies

- **Plan said:** house rule 10 forbids presenting a hand-written file as a recorded one
- **Assumed:** Reused W5's CAPTURED fixtures for the tamper pass-through test; every evaluation/spec/verdict/note body I built is hand-written and is labelled 'Synthetic' in the test file header, with the reason stated (no kind=evaluation row exists in any capture because nothing writes one until W10). No new file was added to __fixtures__.
- **Reversibility:** trivial

### W8.40 Editing api/src/app.test.ts, which is not on my Files line

- **Plan said:** Files lists app.ts but not app.test.ts
- **Assumed:** Necessary: its testConfig() helper builds a config that createApp now refuses (no session secret/allowlist/API key). I added the three variables to the helper and to the one test that asserts the WOLF_MCP_TOKEN failure, and added no new tests there — the R79 tests went into auth/session.test.ts instead, so the ticket's own 4-file Validation actually covers them.
- **Reversibility:** trivial

### W8.41 Editing README.md, which is not on my Files line or any ownership row

- **Plan said:** nothing
- **Assumed:** Added four lines to the quickstart: it previously said WOLF_MCP_TOKEN was the only required variable, and after this ticket `docker compose up` fails at boot on three more. Leaving a documented quickstart that cannot boot seemed worse than the small out-of-list edit.
- **Reversibility:** trivial

### W8.42 ORANGE_BASE_URL validation

- **Plan said:** only that it is documented and typed
- **Assumed:** Must match ^https?://\S+$ or boot fails naming it; an empty value from compose is treated as unset (R80's present() helper) and takes the http://localhost:8099 default — the same default W12's bootstrap hardcodes.
- **Reversibility:** trivial

### W8.43 WOLF_TEST_LOGIN parsing

- **Plan said:** only the literal "email:password"
- **Assumed:** Split on the FIRST colon (an address cannot contain one, a password may); both halves must be non-empty; the email half is lowercased.
- **Reversibility:** trivial

### W8.44 No session-introspection or logout route

- **Plan said:** the route table has neither
- **Assumed:** Did not add GET /api/auth/me or POST /api/auth/logout. W13 will need some way to know whether it is signed in (today: call the board and read a 401).
- **Reversibility:** trivial

### W8.45 Router factory option names/shapes

- **Plan said:** nothing
- **Assumed:** createAuthRouter({client, config, logger, now?}) and createHypothesesRouter({store, client, logger, sessionPollIntervalMs?, sessionPollTimeoutMs?, sleep?}). W9 extends the second file and will inherit these.
- **Reversibility:** trivial

### W8.46 Accepting yarn.lock churn beyond my two new packages

- **Plan said:** nothing
- **Assumed:** `yarn workspace agent-wolf-api add cookie-parser @types/cookie-parser` also normalised pre-existing lockfile drift (react/react-dom/react-is keys moved from ^18.3.1 to the exact 18.3.1 web/package.json pins, and the vite resolutions key was rewritten). Resolved versions are unchanged and web's dependency-hygiene test still passes, so I kept the generated lockfile rather than hand-editing it.
- **Reversibility:** moderate

### W8.47 cookie-parser version range

- **Plan said:** 'cookie-parser' only
- **Assumed:** ^1.4.7 plus @types/cookie-parser ^1.4.10 (current releases; cookie-parser 1.4.x is the only line).
- **Reversibility:** trivial

### W8.48 Board ordering

- **Plan said:** nothing
- **Assumed:** Left W5's ordering untouched (newest updated_at_ms first, a hypothesis with no resolvable state row sorting last rather than vanishing).
- **Reversibility:** trivial

