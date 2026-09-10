# The test-fake fidelity audit (W31)

**Date:** 2026-08-26 · **Subject:** `agent-wolf` at `a155c96` (`Merge W22 — board integration
and cross-hypothesis defence`), read-only · **Ticket:** W31 in
`design/2026-08-20-agent-wolf.md` · **Deliverable:** a measurement and a scoping recommendation.
**Nothing was fixed.** No file in `agent-wolf` was modified; every mutation ran against a
`git archive` export in a scratch directory.

> ⚠️ **`a155c96` stopped being `agent-wolf`'s HEAD during the fix round** — W23 merged and HEAD is
> now `574f87c`. **Every measurement in this document still describes today's code:**
> `git diff a155c96 HEAD -- api/` is **empty** (all nine changed files are under `web/`), so the
> audited subtree is byte-identical to HEAD and the `api` suite is still 36 files / 1696 tests.
> Checked rather than assumed, because a moving HEAD is exactly how an audit quietly stops being
> about the code that ships.

---

## 0. Headline

🔴 **Every count below is a FLOOR, not a total.** A first pass of this audit reported 24
undefended sends; a verifier found sixteen more at a finer caller granularity, and re-measuring
found **eighteen** more, including one neither pass had counted. The sweep is now exhaustive for
every parameter named here, but the method — enumerate callers, mutate each — can only ever
establish a lower bound.

| | |
| --- | --- |
| Hand-rolled fakes | **14** by the ticket's grep, matching its list exactly; **16** counting two more that fake at the `fetchImpl` seam (§2.16) |
| Production parameter *sends* proved undefended (**zero kills**) | **≥ 42** |
| — of those, **E1**: sent by production *and* exercised by at least one test | **40** |
| — of those, **E2**: production can send it, no test ever does (weaker evidence) | **2** (stooq `d1`, `d2`) |
| Of the 42, guarding a **security** property | **7** (six `include_retracted`, one outbound `api_key`) |
| Of the 42, guarding **evidence integrity** | **6** (four config-log `rationale` sends, stooq `s=`, `i=`) |
| Of the 42, guarding **correctness or cost** | **29** |
| Currently-passing assertions that are **decoration** — a test naming the parameter in its own title that cannot fail if the parameter is deleted | **2 tests / 2 assertions** (`marketdata/fred.test.ts:78`, `:94`) |
| Zero-kill parameters with **no test claiming to cover them** ("none today") | **40** |
| Fakes with at least one IGNORED production parameter that **no** request-shape assertion compensates for | **9 of 14** |
| The other five | `orange/client.test.ts` ignores every parameter *by design* and asserts the URL; `routes/auth.test.ts` and `routes/embed.test.ts` ignore the request body in their answer but capture and assert it; `bootstrap/bootstrap-project.test.ts` is sent no parameters at all; `testing/undici-mock.smoke.test.ts` exercises no production code |
| Fakes that carry a test of their **own** fidelity | **1 of 14** (`routes/report.test.ts`, 5 tests) |

**The shape of the finding is not what R190 predicted.** The dominant failure is not "a test
passes for the wrong reason". It is **"no test exists, and the permissive fake is why nobody
noticed the hole"** — 40 of the 42 undefended sends have no claimant. Two do, and both are in
`marketdata/fred.test.ts`.

🔴 **This document's own first pass demonstrated the failure mode it is about.** It reported the
four per-name `limit` sends in `hypothesis/store.ts` as ONE row, "defended, 1 kill" — and
splitting the row shows there are **five** callers, of which **four are entirely undefended**,
including `readTemplate` and `readLatestReport`, the two reads every renderer of a report goes
through (§2.5). Under-counting by aggregation is exactly what a permissive fake does to a test
suite, and it is what this audit did to itself. **Granularity is now one row per call site
throughout**, and the rule is stated as a method note in §1.2.

---

## 1. Scope and method

### 1.1 The fourteen, and the grep count

```
$ cd agent-wolf && grep -rln "MockAgent\|setGlobalDispatcher" api/src --include=*.test.ts | wc -l
14
```

The fourteen paths returned are **exactly** the fourteen the ticket names, with no extras and no
omissions:

`app` · `bootstrap/bootstrap-project` · `hypothesis/poller` · `hypothesis/provision` ·
`hypothesis/store` · `marketdata/fred` · `marketdata/stooq` · `orange/client` · `routes/auth` ·
`routes/embed` · `routes/hypotheses` · `routes/report` · `routes/series` ·
`testing/undici-mock.smoke`.

### 1.2 Parameters enumerated from the PRODUCTION side, by instrumentation not by reading

Reading a fake tells you what it parses, never what it is sent. So the production side was
**captured**, not inferred: a second scratch copy of the tree was instrumented with a three-line
probe at each of the three places `api/src` leaves the process —
`orange/client.ts`'s `fetch` call, `marketdata/fred.ts`'s and `marketdata/stooq.ts`'s — appending
`{method, url, body}` to a JSONL file. Each of the fourteen test files was then run alone with the
probe armed, and the captures aggregated into a per-file route/parameter inventory. **That
inventory, not a reading of the fakes, is what §2's "parameter" column enumerates**, and it is the
only way a parameter the fake never looks at can appear at all.

🔴 **Granularity rule, added in the fix round and applied throughout.** A parameter is measured
**once per production call site**, never once per parameter name. Aggregating call sites hides
exactly the case that matters: `limit: DETAIL_LIMIT` is sent from five places in
`hypothesis/store.ts`, **one** of which is defended, and a single aggregated row reports the whole
group as covered. Where a row below still aggregates — the client's own pass-through table in
§2.15 — it says so and the per-caller numbers are given beside it.

Two facts it produced that reading could not:

- `marketdata/stooq.test.ts` never sends `d1` or `d2` — production can, no test does. A parameter
  that is never sent is *unmeasurable*, which is a different state from *undefended*, and is
  labelled as such below.
- Three client parameters — `since`, `until` and the free-text `query` on `GET /agent/memories`,
  plus `token` on a dataset download and `GET /agent/datasets` entirely — are **never sent by any
  production caller**. They are dead surface on the client, not audit rows.

### 1.3 The mutation harness — all four rules

The harness is `w31mut.py` (scratch only). Its docstring states the four rules; here is how each is
implemented and what it caught.

1. **Decide on EXIT CODE, never by grepping output; "did not run" is a third outcome.**
   `subprocess.run(...)` decides pass/fail purely on `returncode`. Output is captured *whole* to a
   per-mutation file and is used for exactly one thing: if it contains no `Test Files` summary
   line, the run is classified **`DID_NOT_RUN`** regardless of exit code, never as "no kills".
   The runner is invoked as `node node_modules/vitest/vitest.mjs run`.
   **The R153 trap was reproduced deliberately as a harness self-check.** On this tree
   (vitest **4.1.11**), `--reporter=basic` still crashes at startup with exit 1 and prints no
   summary; the classifier tagged it `DID_NOT_RUN`. Log:
   `logs/HARNESS-selfcheck-didnotrun.log`.
   **A fourth outcome was added mid-audit and immediately earned its place: `TIMED_OUT`.** See
   §1.4.
   **Nothing is ever piped into `head`** (R168). The driver writes to files and reads the files.

2. **An OPENING control proving the harness can go red.** Two were run, both attributed:

   | Control | Mutation | Result |
   | --- | --- | --- |
   | `CONTROL-open-A` | delete `user_email: "*"` from `orange/client.ts:646` | **RED, exit 1, 78 kills** across `orange/client.test.ts` (2), `hypothesis/store.test.ts` (41), `hypothesis/poller.test.ts` (1), `routes/hypotheses.test.ts` (34) |
   | `CONTROL-open-B` | delete `query.include_retracted = 1` from `orange/client.ts:679` | **RED, exit 1, 39 kills** across `client.test.ts` (1), `store.test.ts` (27), `series.test.ts` (1), `hypotheses.test.ts` (6), `report.test.ts` (4) |

   **Attribution (R146/R153(3) — a red you have not attributed is not evidence).** Control A's
   kill set is the session-index consumers: everything downstream of `readSessionIndex` — the
   board, the detail route, the poller's enumeration. That is the right blast radius for a
   filter that decides whether the session list is empty.

   🔴 **The 78-vs-76 gap is measured, not guessed.** W22 recorded this mutation as killing **76**.
   A first pass of this document guessed "W22 measured pre-merge"; that guess was directionally
   right and specifically wrong, so it has been replaced by running the mutation on all four
   commits of the W22 chain:

   | commit | baseline tests | mutated | kills | `hypotheses.test.ts` | `store.test.ts` |
   | --- | ---: | --- | ---: | --- | --- |
   | `75414ab` W22 | 1683 | 1607 passed | **76** | 54 tests, 32 failed | 125 tests, 41 failed |
   | `6e5276f` fix round 1 | 1695 | 1617 passed | **78** | 60 tests, 34 failed | 130 tests, 41 failed |
   | `1b80f76` fix round 2 | 1696 | 1618 passed | **78** | 60 tests, 34 failed | 130 tests, 41 failed |
   | `a155c96` merge | 1696 | 1618 passed | **78** | 60 tests, 34 failed | 130 tests, 41 failed |

   **It is not the merge — the merge changes nothing. It is W22's own fix round 1**, which added
   twelve tests, six of them to `routes/hypotheses.test.ts`, **two of which this mutation also
   kills**. `store.test.ts` gained five tests over the same commit and its kill count did not
   move. W22's 76 was correct for the tree W22 measured; 78 is correct for `a155c96`.

3. **A CLOSING control proving the tree ended as it began.** Three parts, all passed:
   - `CONTROL-close` re-ran `CONTROL-open-A` as the last mutation of the last batch and
     reproduced **78 kills with the identical per-file breakdown** — the harness still goes red
     the same way at the end as at the beginning.
   - `diff -rq pristine/api/src tree/api/src` after the last reset: **identical**.
   - A final unmutated run of the working tree: **36 files / 1696 tests, exit 0**.

4. **A PER-MUTATION LANDING GATE.** After mutating and *before* running, the driver computes the
   set of files under `api/src` differing from pristine (a recursive content compare, not a
   one-file diff) and asserts it is **exactly** `[the one file intended]`. A mismatch aborts that
   mutation and resets. Two further apply-time guards sit in front of it: the replacement is an
   exact-string substitution whose occurrence count must equal the expected count (so an
   ambiguous or stale anchor is refused rather than applied `replace_all`), and an
   edit that produces an identical file is refused as a no-op.
   **Tally: 91 landing-gate evaluations, 91 passes, 0 failures.** Four mutations never reached
   the gate — the occurrence check rejected `P06` (a stale anchor) and `V14`, `V17`, `V19`
   (ambiguous anchors) first. See §1.5: that refusal is why an "invalid-mutation rate" measured
   under this harness is not comparable to one measured without it.

   🔴 **R170's stated limit was respected, not assumed away.** The gate proves an edit landed in
   the intended file; it says nothing about whether it was the edit meant. Every non-zero kill
   count in §2 was read against the mutation that produced it, and three results were re-examined
   because the number looked wrong for the change (`M15b`'s 162, `M01`'s 2, `CONTROL-open-A`'s 78
   against W22's 76). `M15b` survives that reading with a caveat stated in its row.

5. 🔴 **EVERY RUN MUST REPRODUCE THE BASELINE COLLECTED-TEST COUNT (1696) AND FILE COUNT (36).**
   *Added in the fix round; it is a fifth false-green direction and my first harness had it.*
   Rule 1 says "no summary line ⇒ did not run" — and **that does not catch a collection
   failure.** A syntax error in one module does not stop vitest printing a summary; it prints one
   that looks almost fine:

   ```
   exit 1     Test Files  14 failed | 22 passed (36)     Tests  1132 passed (1132)
   ```

   A summary line **is** present, so rule 1 passes it through, and a `Tests … failed` parse finds
   **zero failures while 564 tests never ran**. Reported as "0 kills" that is a manufactured
   finding, which is the one way this ticket can do harm.

   **The fix is to assert the collected total, not the summary's presence.** A file that fails to
   collect contributes *zero* tests, so `failed + passed == 1696` is the sharpest single
   tripwire; the file count is asserted too, and `COLLECTION_FAILURE` is a fifth outcome, never
   "no kills". **Self-check:** deliberately breaking `orange/client.ts` reproduces the summary
   above exactly and the harness now returns `COLLECTION_FAILURE`
   (`logs/HARNESS-selfcheck-collection.log`).

   🔴 **Retro-validated: no earlier result changes.** Every run in this audit that produced a
   summary line was re-checked against the new rule — **68 runs, 0 violations**; all report
   `36 passed (36)` and `1696`. The two runs without a summary line are the two already recorded
   as `TIMED_OUT`. Both controls were re-run under the stricter rule and reproduced 78 and 39
   kills with identical per-file breakdowns.

   *Provenance: this direction was found by W31's verifier, not by me. The five known directions
   are now: the runner (R153), the shell (R168), a concurrent peer (R170), the fixture (R178),
   and collection (this one).*

### 1.4 The fourth outcome, and the defect it exposed

`M15` — delete `query.offset = params.offset` from `orange/client.ts:649` — **did not terminate**.
`readSessionIndex` (`hypothesis/store.ts:1253-1275`) walks pages until it sees a short one; with
every request returning page zero, the walk never ends. The suite ran for over two minutes and
2.8 GB of RSS before it was killed by hand, at which point a 180-second timeout was added to the
harness and the outcome `TIMED_OUT` introduced — **explicitly not "zero kills"**, which is exactly
the misreading this ticket says would manufacture a false finding.

🔴 **That is a real production defect, found by the audit rather than by the audit's subject:
`readSessionIndex`'s paging walk has no iteration cap and no page budget.** An Orange that ignores
`offset` — a bug, a proxy that strips it, a future route change — hangs every board read forever
rather than failing. Reported, not fixed.

`M15` was replaced by `M15b`, which shifts the offset by one instead of deleting it: same question,
terminating program.

**Two siblings of the same missing guard, both confirmed by reading, neither fixed here.**
`store.ts:1243` is `opts?.sessionPageSize ?? SESSION_PAGE_SIZE` — `??` only catches `null` and
`undefined`, so a caller's **`0`** (or a negative) passes straight through; `:1271`'s
`page.length < limit` is then never true and `:1272`'s `offset += limit` never advances. And the
Orange side offers no backstop: `go/httpapi/history.go:107-110` parses `offset` with
`strconv.Atoi` and **silently keeps 0 on any parse error** (`limit` likewise falls back to 50), so
a malformed offset is never rejected — it is answered with page zero, forever.
**Latent, not live — high blast radius, cheap to close.** The orchestrator is cutting it as its
own ticket; it is recorded here and deliberately not fixed.

### 1.5 Invalid-mutation rate — and why it is not comparable to R182's

**92 distinct mutations** across seven batches (the control was additionally applied at three
earlier commits, for 96 applications in total). **5 were invalid on first attempt.**

🔴 **But the headline rate is an artefact of the harness, and must not be carried forward as
comparable to R182's 1-in-20.** The two are measuring different things, and the split says why:

| | count | rate | what it is |
| --- | ---: | ---: | --- |
| **Ran, and was invalid** | **1** | **1.1%** | `M15` — semantically valid, operationally invalid: it makes the suite non-terminating. Only this class is comparable to R182 |
| **Refused at apply time, never ran** | **4** | **4.3%** | `P06` (anchor assembled from non-adjacent line ranges, matched nothing), `V14`, `V17`, `V19` (anchors that matched **two** sites — `routes/report.ts:718-725` and `:975-982` are byte-identical eight-line blocks) |

The apply-time **occurrence check** — the replacement's match count must equal the expected count
— converts most would-be invalid mutations into *rejections* rather than *runs*. A harness with
that check will always report a low "invalid" rate, because the bad ones never get through to
produce a misleading number. W31's verifier reports **0 of 83** by the same mechanism, which is
the same artefact at the same strength.

**The transferable claim is therefore about the guard, not the rate:** an exact-string
substitution with a required occurrence count is worth more than care, because it catches the
stale anchor and the ambiguous anchor *before* either can be read as a kill count. R182 is right
that neither rate goes down by being careful; this says one of them goes down by being
**mechanically refused**.

## 2. Fake-by-fake

Legend. **IMPLEMENTED** = the fake's own body reads the parameter and its answer depends on it,
with a `file:line` in the fake. **IGNORED** = it does not. **Kills** = tests that fail across the
whole suite when the production line sending that parameter is deleted or negated; the per-file
breakdown says *where*. **request-shape** marks a kill that comes from asserting the URL rather
than the answer (R180(2)) — honest, but a test of intent, not of behaviour.

Three parameters are shared by many fakes because they are sent by one chokepoint
(`orange/client.ts`); their kill counts are therefore suite-wide and are given once, in §2.15.

### 2.1 `app.test.ts` — the declared answer-only stub

Fake body: `app.test.ts:199-236`. It carries an explicit exemption notice at `:155-162` saying it
"dispatches on path and on the selector's `kind=` term and ignores `limit`, `latest_per` and
`include_retracted`", delegating query semantics to `routes/hypotheses.test.ts`. That notice is
accurate about itself.

| Route | Parameter | Verdict | Evidence in the fake | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/sessions` | `user_email` | **IGNORED** | `:209-219` returns one fixed row; no `searchParams` read | (suite: 78, none here) | none today |
| | `worker` | **IGNORED** | same | (suite: 8, none here) | none today |
| | `limit` | **IGNORED** | same | (suite: 1, none here) | none today |
| | `offset` | **IGNORED** | same | (suite: 162, none here) | none today |
| `GET /agent/memories` | `selector` | **partial** — the `kind=` term only | `:204-207`, `:227-229` | 152 suite-wide, 3 here | — |
| | `limit` | **IGNORED** | `:227-229` | — | none today |
| | `include_retracted` | **IGNORED** | `:227-229` | — | none today |
| `GET /agent/memories/{id}` | — | n/a | `:220-226` | — | — |
| `GET /agent/attention-requests` | `state` | **UNROUTED** | falls through to `:208`'s `{memories: []}` | — | see note |
| `GET /agent/schedules` | — | **UNROUTED** | same | — | see note |

**Note on the two unrouted routes.** The probe shows `app.test.ts` really does issue
`GET /agent/attention-requests?state=open` and `GET /agent/schedules`. The fake answers both with
`200 {"memories":[]}`, the client rejects the shape as `invalid`, and `routes/hypotheses.ts:1131`
swallows it. So two of `app.test.ts`'s cases silently exercise an error path while appearing to
exercise the happy one. Not a parameter defect; a fidelity defect of the same family.

**Defect in the exemption notice.** `:158-161` says query semantics "are graded in
`routes/hypotheses.test.ts` against a stub that does honour all three". That stub honours
`include_retracted` **only on the `kind=report` branch** (`routes/hypotheses.test.ts:304`) and
ignores it on the `kind=hypothesis` board branch (`:308`), and ignores `limit` on every
`latest_per` branch. The delegation is to a fake that does not hold the guarantee being delegated.

### 2.2 `bootstrap/bootstrap-project.test.ts` — the clean one

Fake body: `bootstrap-project.test.ts:73-79`. Registers **exact-path** one-shot interceptors and
records `{method, path, body}`; assertions read the recorded body via `bodyOf` (`:81-85`).

The probe shows **every route it serves is called with an empty query string** —
`GET/PUT /agent/project-settings`, `GET/PUT /agent/workers/{name}`, `GET/POST /agent/schedules`.
**There is no ignored parameter here, because there is no parameter.**

Two properties worth carrying into the consolidation: undici matches the path *including* the
query string, so a stray or dropped parameter would cause a **miss** rather than a permissive
answer — strict in the useful direction; and the interceptors are **not** `.persist()`ed, so an
unexpected extra call fails rather than being served again.

### 2.3 `hypothesis/poller.test.ts`

Fake body: `poller.test.ts:268-398`.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/sessions` | `worker` | **IMPLEMENTED** | `:274`, `:277` | `P25` **3** (all here), behavioural | — |
| | `offset` | **IMPLEMENTED** | `:275`, `:286` | — | — |
| | `limit` | **IGNORED** | `:273-287` never reads it | (suite: 1, in `client.test.ts`) | none today |
| | `user_email` | **IGNORED** | same | 1 here — `poller.test.ts:554`, **request-shape** | the URL assertion is honest; nothing behavioural |
| | `limit: SESSION_PAGE` *(the sweep, `poller.ts:373`)* | **IGNORED** | `:273-287` | `V10` **0** | 🔴 **none today** |
| `GET /agent/memories` | `selector` | **IMPLEMENTED** | `:313-321` | — | — |
| | `latest_per` | **IMPLEMENTED**, value-checked `=== "name"` | `:324-332` | — | — |
| | `limit` | **IMPLEMENTED** | `:333`, `:336`; **defaults to 100 where Orange defaults to 20 and caps at 100** | `P33` **0** (`poller.ts:208`), `V30` **0** (`poller.ts:251`) | 🔴 **none today** — *both* of the poller's `ROW_LIMIT` sends can be deleted and nothing notices |
| | `include_retracted` | 🔴 **IGNORED** | `:312-338` never reads it | `P01` **0**, `P02` **0** | 🔴 **none today** — and see below |
| `GET /agent/memories/{id}` | — | n/a | `:304-310` | — | — |
| `POST /agent/memories` | `labels`, `content` | **IMPLEMENTED** | `:295-301` | — | — |
| | `embed` | **IGNORED** | `:295-301` | `M19` 1 here (**request-shape**); `P35` (value flip) **0** | presence asserted, value never |
| `GET /agent/datasets/{name}/download` | `version` | 🔴 **IGNORED** | `:350-352` returns `row.csv` regardless | `P20` **1** here — `poller_version_gate`, **request-shape** | R184's cited instance, **confirmed**: the pin is real and URL-shaped, never behavioural |
| `GET /agent/deliveries` | `limit` | **IGNORED** | `:371-396` | `V11` **0** at the caller (`poller.ts:381`); 1 suite-wide, in `client.test.ts` only | 🔴 **none today** |
| | `status` | IMPLEMENTED `:372-374` | — | *never sent* | — |

🔴 **The sharpest row in this file.** `hypothesis/poller.ts` carries `includeRetracted: true` on
**both** of its reads (`:209` the locked spec, `:252` the go-live timestamp) with the same
reasoning every other read in the codebase carries — and **deleting either changes nothing**.
`poller.test.ts` contains **no test whose name mentions retraction, hostility or tamper at all**,
so the CONSEQUENCE is honestly **"none today"**: this is missing coverage that a permissive fake
made invisible, not a lying test. The two are different defects and only the second is what R178
described.

### 2.4 `hypothesis/provision.test.ts`

Fake body: `provision.test.ts:234-...`.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/sessions` | `worker` | **IMPLEMENTED** | `:240`, `:242` | `P24` **4** (3 here + 1 in `hypotheses.test.ts`), behavioural | — |
| | `offset` | **IMPLEMENTED** | `:241`, `:251`, `:266` | — | — |
| | `limit`, `user_email` | **IGNORED** | `:239-267` | (suite counts only) | none today |
| `GET /agent/memories` | `selector` | **IMPLEMENTED** | `:288-296` | — | — |
| | `limit` | **IGNORED** | `:287-301` | `P41` **0** | 🔴 **none today** |
| | `limit` on sessions | **IGNORED** | `:239-267` | — (`provision.ts:661` sends none) | — |
| | `include_retracted` | 🔴 **IGNORED** | `:287-301` | `P15` **0** | 🔴 **none today** — no retraction test exists in this file |
| `POST /agent/memories` | `labels`, `content` | **IMPLEMENTED** | `:281-284` | — | — |
| | `embed` | **IGNORED** | `:281-284` | `V20`/`V21`/`V22` **0, 0, 0** (`provision.ts:725`, `:832`, `:939`) | 🔴 **none today** |
| `PUT /agent/workers/{n}` | `system_prompt` | **IMPLEMENTED** (echoed) | `:312`, `:319` | — | — |
| | every other body field | **IGNORED** | `:313-327` returns fixed values | — | none today |
| `DELETE /agent/workers/{n}` | `rationale` | **IGNORED** | `:335-337` | `V14b` **0** (`:642`), `V16` **0** (`:797`) | 🔴 **none today** |
| `DELETE /agent/schedules/{id}` | `rationale` | **IGNORED** | `:368-373` | `V13` **0** (`:620`), `V15` **0** (`:790`) | 🔴 **none today** |
| `GET /agent/deliveries` | `limit` | **IGNORED** | `:375-...` | `V12` **0** (`provision.ts:562`) | 🔴 **none today** |
| | `status` | IMPLEMENTED `:382-383` | — | *never sent* | see note |
| `GET /agent/sessions/by-name/{n}` | the name | **IGNORED** | `:269-274` answers one fixture for any name | — | none today |

**Defect in a comment.** `:379-381` reads: "Orange filters on `?status=` server-side, and so does
this stub: the drain asks for `pending` only". The probe says the drain sends
`GET /agent/deliveries?limit=…` and nothing else; `provision.ts:562-564` filters for `pending`
**in JavaScript** after the fact. The fake implements a parameter production does not send, and
documents a behaviour production does not have. This is exactly what W31's rule
*"never infer from a comment"* is defending against, and it is the only place in the codebase
where a fake's comment is false about the production side.

### 2.5 `hypothesis/store.test.ts` — Fake A of R190

Fake body: `store.test.ts:177-291`, plus the shared `applyListParams` at `:162-175`.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/sessions` | `user_email` | **IMPLEMENTED** | `:236` | 41 here | — |
| | `limit` | **IMPLEMENTED** | `:238`, `:242` | `M14` 0 here — see note | 🔴 see note |
| | `offset` | **IMPLEMENTED** | `:239`, `:242` | `M15b` 31 here | — |
| | `worker` | **IMPLEMENTED** | `:240-241` | 1 here (`:498`, request-shape) | — |
| `GET /agent/memories` *(`latest_per` branch)* | `selector` (`kind=` term) | **IMPLEMENTED** | `:256-259` | — | — |
| | `latest_per` | **presence only** (`!== null`) | `:260` | `P31` (value → `kind`) 1 here, **request-shape** `:570` | the *value* is asserted on the URL and nowhere else |
| | `include_retracted` | **IMPLEMENTED** | `:269`, `:273-276` | `P08` 6, `P09` 1, `P10` 9 — behavioural | — |
| | `limit` | 🔴 **IGNORED** | `:260-277` — no branch reads it | `M02` 1, `M03` **0**, `M04` 1 | 🔴 **R190. See §4.** |
| `GET /agent/memories` *(per-name branch)* | `include_retracted` | **IMPLEMENTED** | `:165-172` | `P07` 5, `P11` 1, `P12` 3, `P13` 2, `P14` 2 | — |
| | `limit` | **IMPLEMENTED** | `:173-174`, default `"50"` | 🔴 **split per call site — see below** | 🔴 four of five callers undefended |
| `POST /agent/memories` | `labels`, `content`, `embed` | 🔴 **IGNORED entirely** | `:215-228` answers `labels: {}` for any body, and the appended row never enters a later read | `M19` 1 here (request-shape) | none today |
| `GET /agent/memories/{id}` | — | n/a | `:244-249` | — | — |

🔴 **The per-name `limit`, split per call site — the correction that changes what the
consolidation must cover.** The row above was first reported as a single line reading
"IMPLEMENTED, 1 kill", i.e. **defended**. It is sent from **five** places in
`hypothesis/store.ts` and only **one** of them is defended — by a request-shape assertion, not by
behaviour:

| # | Call site | What it reads | Mutation | Kills |
| --- | --- | --- | --- | ---: |
| 1 | `store.ts:1361` `readDetailRows` | the detail page's per-hypothesis state rows | `V01` | **1** — `store.test.ts:622`, `expect(query.get("limit")).toBe("50")`, **request-shape** |
| 2 | `store.ts:1639` `readReportSummaries` anomaly follow-up | the per-name re-read after a cross-hypothesis anomaly | `V02` | 🔴 **0** |
| 3 | `store.ts:1661` `readTemplate` | 🔴 **the ONLY path by which any ticket obtains a report template** | `V03` | 🔴 **0** |
| 4 | `store.ts:1695` `readLatestReport` | 🔴 **the ONE read every renderer of a report goes through** — the detail payload's `report` block and W21's frame | `V04` | 🔴 **0** |
| 5 | `store.ts:1786` `readEvaluationRows` | the evaluation history the poller counts | `V05` | 🔴 **0** |

Rows 3 and 4 are the substantive ones: `store.ts:1168-1176` and `:1177-1181` name `readTemplate`
and `readLatestReport` as the sole paths to a template and to a report, and neither one's page cap
is observed anywhere. **Row 5 was counted by neither the first pass nor the verifier** — a fifth
caller, found only by enumerating call sites instead of parameter names, which is the whole point
of the granularity rule in §1.2.

Two further facts about even the one defended row: the fake's default is `"50"` (`:173`), the same
number `DETAIL_LIMIT` sends, so **deleting the parameter changes no answer** — the kill comes
entirely from the URL assertion; and `M07` (`DETAIL_LIMIT` → 3) kills the same single test,
confirming the *value* is asserted only on the URL.

🔴 **`SESSION_PAGE_SIZE` and the paging tests.** `M14` (client stops sending `limit` on
`/agent/sessions`) kills exactly one test, in `orange/client.test.ts` — **none** in this file,
including `store_session_index: a page exactly the size of the limit costs one extra request`
(`:537-549`) and `…: 120 sessions across three pages all appear in the index` (`:518-535`). The
mechanism is simpler than a first pass of this document claimed, and the correction matters
because it changes **who could fix it**. It is not that the fake's `200` default rescues the walk:
those two tests **pass `sessionPageSize` explicitly** (`:529` passes 50, `:546` passes 2), so they
never read `SESSION_PAGE_SIZE` at all. The board tests *do* reach the default — `readBoard` calls
`readSessionIndex(opts)` with `opts` usually undefined — but no fixture in the tree holds anywhere
near 200 sessions, so none of them can observe it either. `P32` (`SESSION_PAGE_SIZE` 200 → 3)
kills **0**, and `M14` (the parameter not sent at all) kills only `orange/client.test.ts`. So the
paging tests are real tests of the *walk* and prove nothing about the *page size* — and 🔴 **no
shared fake fixes that**: only a fixture with more than 200 sessions, or an assertion on the
constant, would.

### 2.6 `marketdata/fred.test.ts` — the matcher fake, and the audit's only true decoration

This fake is a different species: **thirteen** `intercept({path: <predicate or regex>})`
registrations (`:81`, `:97`, `:125`, `:169`, and nine more all on
`/\/fred\/series\/observations.*/`) whose responses are canned fixtures. Parameters are "checked" by the matcher, never by the answer.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `/fred/series/observations` | `api_key` | 🔴 **IGNORED in effect** | matched only at `:85`; see the inertness rule below | `M31` **0** | 🔴 **DECORATION**: `fred.test.ts:78-92`, whose title is *"hits the default host at /fred/series/observations with **api_key** and file_type=json"* |
| | `file_type` | 🔴 **IGNORED in effect** | matched only at `:86` | `M30` **0** | 🔴 **DECORATION**: same test, and `:94-108` for the search leg |
| | `series_id` | **IMPLEMENTED** (regex `:125`) | `:125` | `M34` **1** | — |
| | `observation_start` | **IGNORED** | no matcher, no response variation | `M32` **0** | none today |
| | `observation_end` | **IGNORED** | same | `M33` **0** | none today |
| `/fred/series/search` | `search_text` | **IMPLEMENTED** (regex `:169`) | `:169` | `M35` **1** | — |
| | `api_key`, `file_type` | 🔴 **IGNORED in effect** | `:101-102` | see above | 🔴 **DECORATION**: `:94-108` |

🔴 **The inertness rule, which generalises past FRED and is the single most transferable finding
in this audit.** A matcher-based fake defends a parameter **only when the test's expected outcome
differs from the outcome of "no interceptor matched"**. With `disableNetConnect()`, a miss throws;
`marketdata/fred.ts:100-104` catches any throw and raises `WolfError("unavailable")`. So for every
test that asserts `rejects.toMatchObject({ kind: "unavailable" })`, **matching and not matching
produce the identical observable result**, and the matcher predicate is inert however precise it
looks. The two tests at `:78` and `:94` reply `503` — which also maps to `unavailable`
(`fred.ts:109-111`) — and so cannot distinguish the two. `series_id` and `search_text` are
defended for the opposite reason: their tests expect a **200 with specific rows**, which a miss
cannot produce.

The same rule condemns **three** of `marketdata/stooq.test.ts`'s six matchers (§2.7) and is why
the consolidation cannot simply "keep the matcher fakes as they are". *(A first pass of this
document said four; the correct figure is three, and the error was in the unflattering direction —
it overstated the problem.)*

### 2.7 `marketdata/stooq.test.ts`

**Six** interceptors, every single one `path: /\/q\/d\/l\/.*/` — `:159`, `:169`, `:179`, `:189`,
`:199`, `:215` — a regex that matches any query string whatsoever.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `/q/d/l/` | `s` (the symbol) | 🔴 **IGNORED** | `:159` etc. match `.*`; no response depends on it | `M37` **0** | 🔴 **none today** |
| | `i=d` (daily interval) | 🔴 **IGNORED** | same | `M36` **0** | 🔴 **none today** |
| | `d1` (range start) | **IGNORED**, and **never sent** by any test | same | `M38` **0** | unmeasurable — no test passes a range |
| | `d2` (range end) | **IGNORED**, and **never sent** | same | `M39` **0** | unmeasurable |

**This is the least defended fake in the tree.** `marketdata/stooq.ts:138-143` builds the entire
outbound request from four parameters and **not one of them is observed by any test, anywhere**.
Deleting the line that names the symbol leaves 1696 tests green.

**Which of the six matchers are inert, by §2.6's rule** — the rule turns on the test's expected
outcome, so it has to be applied case by case rather than to the file:

| Interceptor | Test expects | vs. a miss (`unavailable`) | Verdict |
| --- | --- | --- | --- |
| `:159` reply 404 | `kind: "not_found"` | different | **discriminates** |
| `:169` reply 200 `"No data"` | `kind: "not_found"` | different | **discriminates** |
| `:179` reply 503 | `kind: "unavailable"` | **identical** | 🔴 **inert** |
| `:189` `replyWithError` | `kind: "unavailable"` | **identical** | 🔴 **inert** |
| `:199` reply 418 | `kind: "internal"`, and explicitly `not.toBe("unavailable")` | different | **discriminates** |
| `:215` reply + `.delay(200)` vs `timeoutMs: 10` | `kind: "unavailable"` | **identical** | 🔴 **inert** |

**Three inert, three discriminating** — and it changes nothing about the four parameters, because
all six matchers discriminate only on the *path prefix* `/q/d/l/`; the regex is `.*` from the query
string onward. The three that discriminate prove the request reached the right **route**, never
that it carried the right **series**.

### 2.8 `orange/client.test.ts` — the capture fake

Fake body: `client.test.ts:99-118`. It matches every path (`path: (p) => { captured.path = p;
return true; }`), captures the raw path, the body and the `X-API-Key` header, and answers a canned
response. **Every parameter is IGNORED by construction, and every parameter is available for a
request-shape assertion.** This is the one fake whose contract *is* the URL, and it should not be
"fixed" — it should be named.

It is also where most of the codebase's parameter coverage actually lives: `M17` (`state`),
`M18` (deliveries `limit`), `M20` and `M21` (`rationale` ×2) and `M14` (sessions `limit`) each
kill **exactly one test, and it is in this file**. Those five parameters have no consumer-side
defence of any kind.

One genuinely strong property, worth copying: `afterEach` asserts that **every** dispatched
request carried `X-API-Key` (`:71-80`), so the client's single authentication mechanism is gated
across all twenty-three routes rather than per test.

### 2.9 `routes/auth.test.ts`

Fake body: `auth.test.ts:82-100`, one exact-path interceptor for `POST /auth/verify-google`. No
query parameters exist on this route. The request body (`credential`) is **IGNORED in the answer**
but **captured** (`:90`) and asserted, as is the outbound API key (`:91`). No ignored-parameter
rows.

### 2.10 `routes/embed.test.ts`

Fake body: `embed.test.ts:99-111`, `POST /agent/embed-token`.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `POST /agent/embed-token` | body `session` | **IGNORED in the answer** | `:99-109` returns one fixed token for any body | `P23` (mint for the wrong session) **1** here, **request-shape** | the binding of token → session is asserted on the request only |
| | body `ttl_seconds` | *never sent* | — | — | `:240` asserts its **absence**, which is a request-shape assertion and is honest |

### 2.11 `routes/hypotheses.test.ts` — Fake B of R190

Fake body: `hypotheses.test.ts:234-321`, plus `applyListParams` at `:183-194`.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/sessions` | `user_email` | **IMPLEMENTED** | `:256` | 34 here | — |
| | `limit` | **IMPLEMENTED** | `:258`, `:260` | — | — |
| | `offset` | **IMPLEMENTED** | `:259`, `:260` | `M15b` 33 here | — |
| | `worker` | 🔴 **IGNORED** | `:252-261` never reads it | `P24` 1 here | — |
| `GET /agent/memories` *(`latest_per`)* | `latest_per` | **presence only** | `:296` | `P31` 1 here, **request-shape** `:507` | value asserted on the URL only |
| | `include_retracted` | **PARTIAL** — honoured on `kind=report` (`:304`), 🔴 **IGNORED on the `kind=hypothesis` board branch** (`:308`) | `:300-308` | `P08` 1 here | the board's flag is behaviourally covered in `store.test.ts`, not here |
| | `limit` | 🔴 **IGNORED** | `:296-309` | see §4 | 🔴 R190 |
| `GET /agent/memories` *(per-name)* | `include_retracted` | **IMPLEMENTED** | `:186-191` | `P16` **0**, `P17` **0**, `P18` **0** | 🔴 **none today** — the fake is faithful; **no fixture in this file ever seeds a retracted `hypothesis-spec`, `evaluation` or `verdict` row**, so `routes/hypotheses.ts:774,776,777` are undefended for a reason the fake is not to blame for |
| | `limit` | **IMPLEMENTED** | `:192-193`, default `"50"` = `DETAIL_ROW_LIMIT` | `P40` **0** | 🔴 **none today**, and defaults coincide again |
| `POST /agent/memories` | whole body | 🔴 **IGNORED** | `:262-274` | — | none today |
| `GET /agent/attention-requests` | `state` | 🔴 **IGNORED** | `:314-316` | `M17` 1 (`client.test.ts` only); `P34` (value → `closed`) **0** | 🔴 **none today** — nothing checks that the detail page asks for **open** requests |
| `GET /agent/sessions/by-name/{n}` | the name | **IGNORED** | `:244-251` scripted answers | — | none today |
| `GET /agent/schedules` | — | n/a | `:317-319` | — | — |

🔴 **P16/P17/P18 are the audit's most important distinction.** Three production security flags
are undefended, and the fake is *not* the reason. Reading only the fake would have called them
covered; running only the mutation would have called them decoration. Both are wrong: the fake
implements the parameter and **no fixture exercises it**. A shared fake fixes nothing here — three
tests do.

### 2.12 `routes/report.test.ts` — the repaired one

Fake body: `report.test.ts:333-458`. This is the fake W21 and W22 fixed, and the only one with
tests of its own (`:1599-1660`, five).

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/sessions` | `limit` | **IMPLEMENTED** | `:341`, `:343` | — | — |
| | `offset` | **IMPLEMENTED** | `:342`, `:343` | `M15b` 41 here | — |
| | `user_email` | 🔴 **IGNORED** | `:340-344` | 0 here | none today |
| | `worker` | 🔴 **IGNORED** | `:340-344` | 0 here | none today |
| `GET /agent/memories` | `selector` (all terms) | **IMPLEMENTED** | `:366-369`, `:389` | 36 here | — |
| | `limit` | **IMPLEMENTED**, with Orange's real default 20 and cap 100 | `:381`, `:392` | `M10` 2 here — the **fixture self-tests** `:1636`, `:1649` | — |
| | `include_retracted` | **IMPLEMENTED** | `:387`, `:390` | `P05` 1, `P06b` 1 — behavioural | — |
| | `latest_per` | **IGNORED**, and **never sent** through this fake | `:364-394` | — | unmeasurable here |
| `POST /agent/memories` | `labels`, `content` | **IMPLEMENTED**, and the row is stored and re-readable | `:346-361` | — | — |
| | `embed` | **IGNORED** | `:347` parses only `labels` and `content` | `V17b`/`V18`/`V19b` **0, 0, 0** (`report.ts:724`, `:856`, `:981`) | 🔴 **none today** |
| `GET /agent/datasets/{n}/download` | `version` | **IMPLEMENTED** | `:417-420` | `P22` **2** here, behavioural | — |
| | `token` | *never sent* | — | — | — |
| `GET /agent/datasets/{n}` | — | n/a | `:404-447` | — | — |
| `POST /agent/embed-token` | body `session` | **IGNORED** | `:450-455` | — | none today |

**Two defects in this file's own text, both worth carrying forward.**

1. `:1573-1575` says: *"for every filter parameter the client can send, assert the fake implements
   it rather than ignores it. The client sends two — `limit` and `include_retracted` — and both
   are below."* The probe says this fake is sent **seven** distinct parameters across five routes
   (`limit`, `offset`, `user_email`, `worker` on `/agent/sessions`; `selector`, `limit`,
   `include_retracted` on `/agent/memories`; `version` on the download; `session` in the
   embed-token body). "Two" is true of one route, and the sentence is scoped to the file. It is
   **R184's own lesson one level down**: a route-scoped claim wearing a file-scoped shape.
2. `:453` answers the embed-token mint with `expires_at_sec`. Orange's field — and
   `routes/embed.test.ts:106`, which says so explicitly — is `expires_at`. `numField`
   (`orange/client.ts:342-345`) returns `0` for a missing key, so in this file every minted token
   carries `expiresAtSec: 0`. **Two fakes for one route disagree about the response field name**,
   which is the "no shared floor" of R184 in a single line of evidence.

### 2.13 `routes/series.test.ts`

Fake body: `series.test.ts:130-175`.

| Route | Parameter | Verdict | Evidence | Kills | Consequence |
| --- | --- | --- | --- | --- | --- |
| `GET /agent/memories` | `selector` | 🔴 **IGNORED** | `:133-136` answers the spec row for any selector | 1 here (suite-wide `M12`) | — |
| | `limit` | 🔴 **IGNORED** | `:133-136` | `P39` **0** | 🔴 **none today** |
| | `include_retracted` | 🔴 **IGNORED** | `:133-136` | `P03` **1** here — `series.test.ts:491`, **request-shape** (`:496` asserts the URL) | the test's title claims *"a hostile retraction cannot hide it"*; it proves the flag was **asked for**, never that hiding fails. Label per R180(4). |
| `GET /agent/memories/{id}` | — | n/a | `:137-150` | — | — |
| `GET /agent/datasets/{n}/download` | `version` | 🔴 **IGNORED** | `:151-153` returns one body | `P21` **1** here, **request-shape** | same shape as the poller's pin |
| `GET /agent/datasets/{n}` | — | n/a | `:154-173` | — | — |

### 2.14 `testing/undici-mock.smoke.test.ts`

`:30-43`. A two-case smoke test of the mocking tool itself, against
`https://example.invalid/ping`. **No production code is exercised and no production parameter
passes through it**, which its own docstring states (`:9-12`). Zero rows; listed for completeness,
because the ticket's rule is that an omitted fake is the failure mode.

### 2.15 The shared chokepoint: suite-wide numbers for `orange/client.ts`

Every Orange parameter above is sent from one place, so its deletion is measurable once for the
whole suite. This is the summary table for those mutations.

| Mutation | Production line | Kills | Where |
| --- | --- | --- | --- |
| `CONTROL-open-A` / `-close` | `client.ts:646` `user_email: "*"` | **78** | client 2, store 41, poller 1, hypotheses 34 |
| `CONTROL-open-B` | `client.ts:679` `include_retracted` | **39** | client 1, store 27, series 1, hypotheses 6, report 4 |
| `M10` | `client.ts:673` memories `limit` | **6** | client 1, store 3 (all request-shape), report 2 (fixture self-tests) |
| `M11` | `client.ts:674` `latest_per` | **24** | client 2, store 16, hypotheses 6 |
| `M12` | `client.ts:671` `selector` (sanity) | **152** | all eight consumer files |
| `M13` | `client.ts:647` sessions `worker` | **8** | client 1, provision 3, store 1, poller 3 |
| `M14` | `client.ts:648` sessions `limit` | **1** | client only |
| `M15` | `client.ts:649` sessions `offset` (delete) | **TIMED_OUT** | invalid — see §1.4 |
| `M15b` | same, value shifted by 1 | **162** | six files — but see caveat |
| `M16` | `client.ts:745` `version` | **6** | client 1, poller 1, series 1, report 3 |
| `M17` | `client.ts:856` `state` | **1** | client only |
| `M18` | `client.ts:841` deliveries `limit` | **1** | client only |
| `M19` | `client.ts:658` `embed` | **2** | store 1, poller 1 (both request-shape) |
| `M20` | `client.ts:792` worker `rationale` | **1** | client only |
| `M21` | `client.ts:832` schedule `rationale` | **1** | client only |

**Caveat on `M15b` (R170's stated limit, applied).** 162 kills reads as very strong coverage of
`offset`. It is not: shifting the offset by one drops the first row of every page, which perturbs
almost every fixture in the tree. It proves the *value* reaches the fake and is used; it does not
prove any test is *about* paging. The honest reading is "offset is behaviourally live", not
"offset is well tested".

---

### 2.16 Two more hand-rolled fakes, outside the `MockAgent` grep

🔴 **The ticket's framing is "hand-rolled fakes"; its grep finds the `MockAgent` ones. Two more
fake HTTP at a different seam and the consolidation should know they exist.** Both inject a
`fetchImpl` into `createMarketDataAccess` rather than installing a global dispatcher, so
`grep -rln "MockAgent\|setGlobalDispatcher"` cannot see them:

| File | The fake | What it ignores |
| --- | --- | --- |
| `mcp/server.test.ts:514-522` | `vi.fn(async () => new Response("Date,…\n2026-01-02,1,1,1,180.5,10\n", {status: 200}))` | 🔴 **everything, including its own arguments** — the mock takes no parameters at all, so the URL is not merely unmatched, it is never received |
| `mcp/seriesdownload.test.ts:234-239` | the same shape, plus `expect(fetchImpl).toHaveBeenCalledTimes(1)` | the same; the **call count** is asserted, the URL is not |

Neither sends any parameter this audit has not already measured — they drive
`marketdata/stooq.ts`, whose four parameters are already at zero kills — so **they add no rows and
change no number**. They matter for two reasons. First, the ticket's grep is the audit's own
completeness argument, and it has a blind spot worth recording: **a fake that replaces `fetch` by
injection is invisible to a grep for the dispatcher.** Second, `seriesdownload.test.ts:246`'s
`toHaveBeenCalledTimes(1)` is a *third* kind of oracle — neither behavioural nor request-shape but
**call-count** — and it is the only assertion protecting the market-data cache. A shared fake
should offer that counter, and §5.1's list should be read as covering it.

## 3. The ranking

Ordered by what an ignored parameter is guarding. **The first class is the consolidation's first
wave.**

### Class 1 — SECURITY (7 undefended sends)

| # | Production line | Parameter | Guards | Kills | Why the fake is or is not to blame |
| --- | --- | --- | --- | --- | --- |
| 1 | `hypothesis/poller.ts:209` | `include_retracted` | a hostile retraction hiding the **locked spec** the poller evaluates against | **0** | fake ignores it (`poller.test.ts:312-338`) |
| 2 | `hypothesis/poller.ts:252` | `include_retracted` | a hostile retraction hiding the **go-live timestamp**, restarting the horizon clock | **0** | same fake |
| 3 | `hypothesis/provision.ts:518` | `include_retracted` | a hostile retraction hiding an **evaluation** row at provisioning | **0** | fake ignores it (`provision.test.ts:287-301`) |
| 4 | `routes/hypotheses.ts:774` | `include_retracted` | a hostile retraction hiding the **locked spec** on the detail page | **0** | fake is faithful (`:186`); **no fixture seeds a retracted spec** |
| 5 | `routes/hypotheses.ts:776` | `include_retracted` | same, **evaluation** rows | **0** | same |
| 6 | `routes/hypotheses.ts:777` | `include_retracted` | same, **verdict** rows | **0** | same |
| 7 | `marketdata/fred.ts:93` | `api_key` | the **outbound credential** on every FRED call | **0** | matcher inert (§2.6) — and this one *has* a claimant test |

Two tenancy/provenance filters are **not** in this class because they are defended: `user_email`
(78 kills) and the session-index `worker` filter (8 kills, three of them behavioural). W22 bought
both, and the audit confirms them.

### Class 2 — EVIDENCE INTEGRITY (6 undefended sends)

🔴 **The four `rationale` sends head this class by owner ruling (2026-08-26), moved here from
class 3.** A first pass of this document put them in class 3 and flagged the placement as a
judgement call rather than deciding it silently; the ruling is that they belong here, and the
reasoning generalises further than the four lines do.

| # | Production line | Parameter | Guards | Kills |
| --- | --- | --- | --- | --- |
| 8 | `hypothesis/provision.ts:620` | `rationale` on `DELETE /agent/schedules/{id}` | teardown: **why** the schedule was deleted | `V13` **0** |
| 9 | `hypothesis/provision.ts:642` | `rationale` on `DELETE /agent/workers/{name}` | teardown: **why** the worker was deleted | `V14b` **0** |
| 10 | `hypothesis/provision.ts:790` | `rationale` on `DELETE /agent/schedules/{id}` | go-live rollback | `V15` **0** |
| 11 | `hypothesis/provision.ts:797` | `rationale` on `DELETE /agent/workers/{name}` | go-live rollback | `V16` **0** |
| 12 | `marketdata/stooq.ts:140` | `s=<symbol>` | that a hypothesis's price evidence is **the series it names**. Delete it and the request asks Stooq for nothing in particular | `M37` **0** |
| 13 | `marketdata/stooq.ts:141` | `i=d` | the **daily** interval — the sampling frequency every condition is evaluated at | `M36` **0** |

**Why `rationale` is evidence integrity and not cost.** These four write the *explanation* field of
Orange's config log, which is written in the same transaction as every configuration mutation
(`go/agentdb/config_events.go`). Dropped, the log still records **that** a worker and a schedule
were destroyed and no longer records **why**. The record survives; its trustworthiness does not.

🔴 **And it is R185's rule, one layer out — which is why this is a ruling and not a preference.**
R185's general form is: *"a degradation is a narrowing of a return value, and every field narrowed
to its empty value is a **claim** — here, a false one."* An absent `rationale` is exactly that
narrowing, on a durable record instead of a return value: the config log then asserts **"this
teardown had no stated reason"**, which is false, and which is **unfalsifiable after the fact** —
the deletion has happened, the reason is not recoverable from anywhere, and nothing in the record
distinguishes "no reason was given" from "a reason was given and dropped in transit". Losing the
one property that makes an audit record worth keeping is not a cost concern.

The same test separates the two stooq rows from class 3: a wrong answer there is silently **wrong
data in a report a human will act on**, not a truncated page. Every member of this class produces a
record that reads as valid and is not.

**Consequence for sequencing:** the consolidation should treat all six as early work, immediately
behind class 1 — see §5.4.

### Class 3 — CORRECTNESS / COST (29 undefended sends)

🔴 **Fourteen of these were added in the fix round** (eighteen were added across classes 2 and 3
together, four of which the owner ruling then moved to class 2), when the granularity moved from
one row per parameter name to **one row per call site**. Marked ⊕. None changes the security
ranking; together they roughly double the surface a shared fake has to cover, and they are why
§5's estimates moved.

**Page caps on `GET /agent/memories` — 12 call sites, none defended behaviourally**

| Production line | Parameter | Guards | |
| --- | --- | --- | --- |
| `hypothesis/store.ts:1467` | `limit: BOARD_LIMIT` (`readEvaluationSummaries`) | the evaluation board's cap — the only one of the three `latest_per` reads with **no assertion of any kind** | |
| `hypothesis/store.ts:1639` | `limit: DETAIL_LIMIT` | the anomaly follow-up re-read | ⊕ |
| `hypothesis/store.ts:1661` | `limit: DETAIL_LIMIT` | 🔴 `readTemplate` — the sole path to a report template | ⊕ |
| `hypothesis/store.ts:1695` | `limit: DETAIL_LIMIT` | 🔴 `readLatestReport` — the read every report renderer goes through | ⊕ |
| `hypothesis/store.ts:1786` | `limit` | `readEvaluationRows`, the poller's evaluation history | ⊕ |
| `hypothesis/poller.ts:208` | `limit: ROW_LIMIT` | poller spec-read cap | |
| `hypothesis/poller.ts:251` | `limit: ROW_LIMIT` | poller go-live-timestamp read cap | ⊕ |
| `routes/series.ts:135` | `limit: ROW_LIMIT` | series spec-read cap | |
| `routes/report.ts:407` | `limit: ROW_LIMIT` | report spec-read cap | |
| `routes/report.ts:819` | `limit: 1` (value) | the amendment-decision guard's single-row read | |
| `routes/hypotheses.ts:581` | `limit: DETAIL_ROW_LIMIT` | detail-page cap | |
| `hypothesis/provision.ts:395` | `limit: ROW_LIMIT` | provisioning cap | |

**Page caps on other routes — 4 call sites, none defended**

| Production line | Parameter | Guards | |
| --- | --- | --- | --- |
| `hypothesis/store.ts:745` | `SESSION_PAGE_SIZE` value | request count of the session-index walk (§2.5: no shared fake fixes this one) | |
| `hypothesis/poller.ts:373` | `limit: SESSION_PAGE` | the tick-session sweep's page | ⊕ |
| `hypothesis/poller.ts:381` | `limit: DELIVERY_PAGE` | the in-flight delivery page the sweep excludes on | ⊕ |
| `hypothesis/provision.ts:562` | `limit: DELIVERY_PAGE` | the teardown drain's delivery page | ⊕ |

*(The four `rationale` sends were listed here in a first pass. **Owner ruling 2026-08-26 moved them
to class 2** — see there for the reasoning.)*

**`embed: false` on `POST /agent/memories` — 8 call sites, 7 undefended**

| Production line | Guards | |
| --- | --- | --- |
| `hypothesis/store.ts:1761` | the state row is not embedded (value flip `P35` **0**; deletion `V31` **0**) | |
| `routes/report.ts:724`, `:856`, `:981` | template / decision / amendment appends | ⊕ ×3 |
| `hypothesis/provision.ts:725`, `:832`, `:939` | spec / verdict / re-spec appends | ⊕ ×3 |
| *(`hypothesis/store.ts:1776` — `appendEvaluation` — is the ONE defended send: `V23` kills 2, both request-shape)* | | |

Every one of these is an **Orange-side cost** control: an embedded row pays for an embedding
Wolf never queries by vector. Seven of the eight can be deleted with the suite green.

**Market data — 5 call sites, none defended**

| Production line | Parameter | Guards | Evidence class |
| --- | --- | --- | --- |
| `routes/hypotheses.ts:1130` | `state: "open"` (value) | that the detail page lists **open** attention requests | E1 |
| `marketdata/fred.ts:94` | `file_type=json` | the response format — **has a claimant test** | E1 |
| `marketdata/fred.ts:158` | `observation_start` | the requested range | E1 |
| `marketdata/fred.ts:159` | `observation_end` | the requested range | E1 |
| `marketdata/stooq.ts:142`, `:143` | `d1`, `d2` | range start/end | 🔶 **E2 — never sent by any test** |

🔶 **The two E2 rows are weaker evidence and are excluded from the E1 count in §0.** "Zero kills"
for a parameter no test ever sends is a tautology, not a measurement; folding them into the same
total would be the same aggregation error §0 flags. They are listed because production *can* send
them and a shared fake would still have to implement them.

Also in this class, **defended only by a single URL assertion in `orange/client.test.ts`** and by
nothing on the consumer side: deliveries `limit`, the sessions `limit`, and both `rationale`
parameters at the client level. Those client-level rows are the *same* parameters as the
per-caller rows above; §2.15 gives the client-level number, this table gives the call sites.

---

## 4. R190 — **contradicted in its literal claim, confirmed in its substance**

R190 states: *"`BOARD_LIMIT = 100` (`store.ts:1170`) is decoration … the `limit` parameter is
ignored on every `latest_per` branch of both Fake A and Fake B — so no test can observe the cap,
and mutating it changes nothing."*

**Three findings, and they do not all point the same way.**

**(a) The citation is wrong.** At `a155c96`, `BOARD_LIMIT = 100` is at
**`api/src/hypothesis/store.ts:1219`**, not `:1170`. Line 1170 is a docstring line about
`readTemplate`. Line drift, presumably across the W22 merge; worth correcting in the log because
the constant is cited by number in the ticket as well.

**(b) The reading of the fakes is CONFIRMED, exactly as written.** Both `latest_per` branches
ignore `limit`, verified in each fake's own body:
- Fake A, `hypothesis/store.test.ts:260-277` — the branch tests `latest_per !== null` and
  dispatches on `include_retracted` and the selector's `kind=`; no line in it reads `limit`.
- Fake B, `routes/hypotheses.test.ts:296-309` — identical structure, same omission.

**(c) "Mutating it changes nothing" is FALSE, and the way it is false is the interesting part.**

| Mutation | Result |
| --- | --- |
| `BOARD_LIMIT` 100 → **1** | **2 kills**: `hypothesis/store.test.ts:571` and `:2448` |
| `readBoard` stops sending `limit` (`store.ts:1395`) | **1 kill**: `store.test.ts:571` |
| `readReportSummaries` stops sending `limit` (`store.ts:1596`) | **1 kill**: `store.test.ts:2448` |
| `readEvaluationSummaries` stops sending `limit` (`store.ts:1467`) | 🔴 **0 kills** |

The two survivors are **request-shape assertions**, and nothing else:

- `store.test.ts:571` — `expect(query.get("limit")).toBe("100")`
- `store.test.ts:2448` — `expect(path).toContain("limit=100")`

**The verdict, stated precisely.**

> 🔴 **`BOARD_LIMIT` is BEHAVIOURAL decoration and REQUEST-SHAPE evidence.** No test anywhere
> observes the *cap* — setting it to **1** does not change a single board, headline or record that
> any test inspects, which is the proof, because a fake that honoured `limit` would return one row
> and redden dozens of assertions. Two tests do assert the *number 100 appears on the URL*. R190's
> conclusion about the product — *"past 100 hypotheses, rows silently vanish from the board while
> the detail page still serves them"* — **stands unchallenged and untested**. R190's incidental
> claim that "mutating it changes nothing" is wrong by two tests, and its own §R180(2) predicts
> exactly which two.
>
> 🔴 **And R190 understates the problem by one third.** `BOARD_LIMIT` is sent on **three**
> `latest_per` reads, not one. The third — `readEvaluationSummaries`, `store.ts:1467` — has **no
> assertion of any kind, not even a URL one**. It is the only fully unguarded member of the trio,
> and R190 does not mention it.

R190's two-part fix is confirmed as necessary and as insufficient on its own: teaching the fakes
to honour `limit` on `latest_per` will indeed fail nothing today (no fixture exceeds 100 rows), so
the test that proves the cap has to be **written**, together with the owner decision about what the
board does at the boundary.

---

## 5. Scoping recommendation for the consolidation

### 5.1 What a shared fake would have to implement to be a floor

A single `api/src/testing/fake-orange.ts`, consumed by the Orange-facing files, would have to
implement — for real, with the real defaults — the following. This list is derived from the probe
inventory, so it is exactly what production sends and nothing speculative.

**`GET /agent/memories`** — the load-bearing route.
- `selector`: full Kubernetes-style term match on `k=v`, AND across comma-separated terms. Not a
  `kind=` prefix test: `kind=report` is a prefix of `kind=report-template`
  (`store.test.ts:252-255` records this having already bitten).
- `include_retracted`: filter retracted rows **unless** `=1`, and filter **before** any reduction,
  which is the whole reason production sends it (`store.ts:1371-1391`).
- `latest_per`: honour the **value**, reducing to the newest row per that label. Presence-only
  checking is what makes `latest_per=kind` indistinguishable from `latest_per=name` today.
- `limit`: default **20**, cap **100**, newest-first — Orange's real numbers
  (`report.test.ts:375-381` already records them). Two of today's fakes default to the *same
  number production sends*, which silently makes deletion of the parameter a no-op.
- `retracted_by` travels with the row, so the trust rule can judge it.

**`GET /agent/sessions`** — `user_email` (empty result unless matched), `worker`, `limit`
(default 200 in Orange's shape), `offset`, and a short final page.

**`GET /agent/datasets/{name}/download`** — `version`, answering **different bytes per version**
and 404 for an absent one (`report.test.ts:413-421` is the model).

**`POST /agent/memories`** — parse `labels` and `content`, **store the row so later reads see it**,
echo the labels back, honour `embed`, and answer **201** and only 201.

**Everything else it serves** — `/agent/attention-requests` (`state`),
`/agent/deliveries` (`status`, `limit`), `DELETE …?rationale=`,
`POST /agent/embed-token` (bind the token to the `session` in the body; field name **`expires_at`**),
`/agent/memories/{id}` (deliberately **not** retraction-filtered), `/agent/sessions/by-name/{n}`
(answer by name).

**A request-count and request-shape surface**, because two of the three oracle kinds in this tree
are not behavioural: the fake must record every request (path, body, headers) for assertion after
the fact — `orange/client.test.ts:99-118` is the model — and expose a call counter, which is the
only thing protecting the market-data cache (`mcp/seriesdownload.test.ts:246`, §2.16).

And, critically, **one fidelity test suite over the shared fake**, in the `report_fixture` style
(`report.test.ts:1552-1660`) — driving it through the real `BobClient` and asserting each
parameter's behaviour. That suite is the thing that makes every downstream defence honest, and it
is the only defence that a mutation of fixture code can ever have.

### 5.2 Which fakes could adopt it unchanged

**Adopt as-is (5)** — they need only a subset and assert nothing the shared fake would break:

| File | Note |
| --- | --- |
| `app.test.ts` | it is deliberately answer-only; adopting the shared fake **removes** its exemption notice and fixes the two unrouted routes for free |
| `routes/series.test.ts` | ignores everything today; nothing to preserve |
| `hypothesis/provision.test.ts` | needs `selector` + sessions filters, all of which the shared fake has |
| `routes/embed.test.ts` | one route; gains the session→token binding |
| `routes/auth.test.ts` | one route, no parameters; adopt for uniformity or leave alone |

**Adopt with a seam (4)** — they need response *injection* (a scripted 503, a scripted sequence of
answers, a metadata-read that bumps a version mid-flight). The shared fake must offer an
`inject(method, pathPrefix, answer)` hook and a per-name answer script; all four already have one:

| File | The seam it needs |
| --- | --- |
| `hypothesis/poller.test.ts` | `config.fail` prefix injection (`:247-253`); dataset version scripting |
| `routes/report.test.ts` | `failAppendWhere`, `unavailableDatasets`, `bumpOnMetadataRead` (`:164-172`) |
| `routes/hypotheses.test.ts` | `byName` answer sequences, `failMemoryById` (`:244-251`, `:280-281`) |
| `hypothesis/store.test.ts` | `appendStatus` (`:216`) |

**Keep bespoke (5)** — these are load-bearing in a way a shared Orange fake cannot serve:

| File | Why |
| --- | --- |
| `orange/client.test.ts` | 🔴 **its fake IS the request-shape oracle.** It must keep matching everything and capturing the raw path. Do not replace it; **label it** — it is the only place `state`, deliveries `limit` and both `rationale` parameters are checked at all |
| `marketdata/fred.test.ts` | a different upstream with a different shape |
| `marketdata/stooq.test.ts` | same |
| `bootstrap/bootstrap-project.test.ts` | exact-path, one-shot, body-asserting — **already stricter than the proposed floor**, and its strictness is the point. Leave it |
| `testing/undici-mock.smoke.test.ts` | tests the tool, not Orange |

🔴 **The two market-data fakes need a different fix from the Orange twelve, and it must not be
folded into the same wave.** Their problem is not a permissive route table; it is the **inertness
rule** of §2.6 — a matcher-based fake defends nothing in any test whose expected outcome is the
same as "no interceptor matched". The fix there is a rule, not a module: **every matcher-based
test either expects an outcome a miss cannot produce, or asserts the captured URL explicitly.**
Applied to what exists today, that means `marketdata/stooq.test.ts` gains URL capture (four
parameters currently at zero) and `marketdata/fred.test.ts`'s two "pin" tests are rewritten to
assert the captured URL rather than to expect `unavailable`.

### 5.3 How many existing tests would change meaning

Every number below is either measured or derived from a measured kill set; where it is an
estimate it says so and says from what.

**The denominator first.** Of the suite's **1696** tests, **538** live above one of the fourteen
hand-rolled fakes and **1158** never make an HTTP call at all:

| file | tests | | file | tests |
| --- | ---: | --- | --- | ---: |
| `hypothesis/store.test.ts` | 130 | | `routes/auth.test.ts` | 28 |
| `routes/report.test.ts` | 76 | | `bootstrap/bootstrap-project.test.ts` | 25 |
| `orange/client.test.ts` | 65 | | `routes/series.test.ts` | 22 |
| `routes/hypotheses.test.ts` | 60 | | `marketdata/stooq.test.ts` | 17 |
| `hypothesis/provision.test.ts` | 40 | | `marketdata/fred.test.ts` | 16 |
| `hypothesis/poller.test.ts` | 32 | | `routes/embed.test.ts` | 15 |
| `app.test.ts` | 10 | | `testing/undici-mock.smoke.test.ts` | 2 |

| Effect | Count | Basis |
| --- | --- | --- |
| Tests that **fail immediately** on adopting a strict shared fake | **0–5** | measured: `P32`, `M14`, `P33`, `P39`, `P40`, `P41`, `P42` all kill **0**, which is direct evidence that **no fixture in the tree exceeds any page cap production sends**. The one fixture in the tree that holds more than Orange's default page of 20 (`report.test.ts:1644`, 150 amendments) is in the one file whose fake already caps. The residual risk is `store.test.ts`'s three `latest_per` fixture bodies, which would start being reduced per name rather than returned whole |
| Tests whose **meaning changes** — they keep passing but start proving what their name claims | 🔴 **a range, ~30–116, not a measurement** | The measured quantity is: deleting `selector` (`M12`) kills **152** tests — the set that actually depends on `GET /agent/memories` returning the right rows. Distribution: `store.test.ts` 42, `report.test.ts` 36, `hypotheses.test.ts` 24, `poller.test.ts` 23, `provision.test.ts` 21, `app.test.ts` 3, `client.test.ts` 2, `series.test.ts` 1. **Turning 152 into an answer needs an exclusion criterion, and the criterion sits on a continuum, not at a point** — which is why this is a bracket. Excluding only files whose fake is strict on **all three** parameters (`report.test.ts`) gives the **116** end. Excluding every file that already dispatches on the selector's `kind=` term — which `store.test.ts` (`:256-259`) and `hypotheses.test.ts` (`:292-295`) also do, they are merely not strict on `limit` and `include_retracted` — gives the **~30** end. 🔴 **The consolidation must not be scoped off 116 as though it were measured.** If one number is needed for planning, take the low end: ~30 tests change what they prove *about the selector*, and the rest change what they prove about the two parameters those files already partly honour |
| Tests that must be **rewritten**, not merely re-run | **2** | `marketdata/fred.test.ts:78-92` and `:94-108` — their assertion is inert by construction and no shared fake fixes them |
| Tests that must be **written** (they do not exist) | **≈ 13 for the security and integrity classes; ~25–30 to close class 3 as well** | Unchanged for classes 1 and 2: one behavioural retraction test each for the poller's two reads, provisioning's evaluation read, and the detail page's spec/evaluation/verdict reads (6); the board-boundary test R190 asks for plus its two siblings (3); a stooq URL assertion covering `s`/`i` (1); **one captured-path assertion covering all four `rationale` sends (1, added by the class-2 ruling)**; the shared fake's own fidelity suite (1 file, ~10 cases, counted as 1); an `attention state=open` assertion (1). 🔴 **Classes 2 and 3 together grew from 17 sends to 35 in the fix round**, and closing it needs roughly one assertion per remaining call site — the twelve memory page caps collapse into ~3 tests once the fake honours `limit`, the four `rationale` sends into 1, the seven `embed` sends into 1 |
| Assertions that would be **retired or relabelled** | **≈ 8** | the request-shape assertions that become redundant once the behaviour is provable — `store.test.ts:571`, `:622`, `:2448`, `poller.test.ts:554`, `series.test.ts:496`, `hypotheses.test.ts:510`, `:514`, `:1553`. 🔴 **Recommendation: relabel, do not retire.** R180(2) is right that they are the assertions that survive a fixture regression, and the fixture is the thing being changed |

### 5.4 Sequencing, and the one thing to do first

1. 🔴 **Write the six missing retraction tests before touching any fake.** Four of the seven
   class-1 holes (`P16`, `P17`, `P18`, and the shape of `P15`) are *missing fixtures*, not
   permissive fakes — a shared fake would close none of them. This is the highest-value work in
   the whole consolidation and it does not depend on it.
1b. 🔴 **Close class 2 next — it is now six sends, not two, and four of them need no fake at all.**
   The four `rationale` sends (owner ruling, §3 class 2) are defended by **one** assertion: a
   captured `DELETE` path carrying `?rationale=`, in the style `orange/client.test.ts` already
   uses. It can be written today, against the existing fakes, before the consolidation starts.
   The two stooq rows need the matcher rule from step 4. Doing the `rationale` assertion early
   costs an hour and closes four of the six.
2. **Build the shared fake plus its own fidelity suite**, and adopt it in the five easy files
   first. The suite is what makes step 3 safe.
3. **Move the four seam files across**, one per commit, re-running the class-1 and class-2
   mutations after each to confirm the kill counts move from 0 to non-zero. The mutation list in
   this document is the acceptance test for the consolidation.
4. **Apply the matcher rule to `marketdata/*`** as a separate, small ticket.
5. **Decide the board boundary** (R190's second part) once `limit` is behaviourally observable.

**One number to carry into the decision — and it is a floor.** **At least forty-two** production
parameter sends are undefended, and only **two** of them are covered by a test that says it covers
them. **The consolidation's value is not mostly in repairing lies; it is in making it possible to
write the tests that were never written, because until the fakes filter, those tests cannot fail.**

🔴 **And treat the number as a floor for a specific reason.** The count went 24 → 40 → 42 across
two passes of the *same* audit, every increase coming from splitting an aggregated row into its
call sites, and the last increase found a call site two independent passes had missed. Whatever
count the consolidation is scoped against, the honest form of it is "at least".

---

## Appendix — mutation index

**92 distinct mutations** across seven batches; the control was additionally applied at three
earlier commits (96 applications). **91 reached the landing gate and all 91 passed it** — the one
that did not (`P06`) was refused by the apply-time occurrence check before the gate, along with
three later anchors (`V14`, `V17`, `V19`) that matched two sites each and were re-run with unique
anchors. Every mutation is an exact-string edit to one file under `api/src`, applied to a
`git archive` export, gated, run, and reverted. Raw per-mutation runner output is in the audit's
scratch directory (`logs/<id>.log`) and is not committed.

| Batch | ids | Landing gate | Notes |
| --- | --- | --- | --- |
| controls (open) | `CONTROL-open-A`, `-B` | 2/2 | both RED and attributed; re-run under rule 5, unchanged |
| batch 1 | `M01`–`M07`, `M10`–`M21`, `M30`–`M39` | 29/29 | `M15`'s gate **passed** and then the run `TIMED_OUT` — separate stages, which is the point; `M15b` is its replacement |
| batch 2 | `P01`–`P25`, `M15b` | 23/24 | `P06` refused at apply time (anchor built from non-adjacent line ranges) |
| batch 3 | `P06b`, `P31`–`P42`, `CONTROL-close` | 12/12 | closing control reproduced 78 kills exactly |
| harness self-check | `HARNESS-selfcheck-collection` | 1/1 | 🔴 rule 5's own control: a deliberate syntax error, correctly `COLLECTION_FAILURE` |
| batch 4 *(fix round)* | `V01`–`V05`, `V10`–`V23` | 16/19 | `V14`, `V17`, `V19` refused — ambiguous anchors; `routes/report.ts:718-725` and `:975-982` are byte-identical |
| batch 4b *(fix round)* | `V14b`, `V17b`, `V19b` | 3/3 | the three retries, all zero-kill |
| batch 5 *(fix round)* | `V30`, `V31` | 2/2 | the last two unmeasured call sites, both zero-kill |
| closing | `CONTROL-close-2` | 1/1 | under rule 5: 78 kills, identical per-file breakdown |
| commit sweep | `CONTROL-open-A` at `75414ab`, `6e5276f`, `1b80f76`, `a155c96` | n/a | resolves 78-vs-76 (§1.3) |

**Closing state.** `diff -rq pristine/api/src tree/api/src` → identical. Final unmutated run:
**36 files / 1696 tests, exit 0**. `cd agent-wolf && git status --porcelain` → **empty**;
`git log --oneline -1` → `a155c96`, unchanged.
