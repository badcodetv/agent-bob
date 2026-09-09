# Memory-Coordinated Organisation — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless
> dependencies say otherwise. Only the orchestrator changes ticket Status;
> workers may only append to Notes and the Discovered Issues Log. A ticket's
> checkbox is checked only after its Validation commands have been re-run by
> the orchestrator and pass. Do not expand scope; log surprises in the
> Discovered Issues Log instead.

Status: **approved** (2026-09-08)
Revision: **rev2** — rewritten after an adversarial review found four blockers
and ten majors in rev1. Corrections are marked **[rev2]** inline. Three of the
four blockers were "the branch goes green and the feature does nothing" bugs;
do not undo them.

Supersedes: `design/2026-09-08-project-onboarding-and-architect.md`, which is
marked SUPERSEDED at its head. If a claim there contradicts this file, this
file wins. Findings from its two review rounds are folded in and marked **[R]**.

Relates: `docs/product/17-product-spec.md` (goal, atoms, principles P1–P8),
`docs/product/03-memory.md` (memory as mechanism, not policy),
`docs/product/2026-07-22-landscape-learnings.md` **L20** (the self-improvement
guardrails the architect prompt implements),
`docs/product/25-cooperative-patterns.md` §5 (what this substrate cannot do),
`go/topology/architectarchivist.go` (the existing architect, and the source of
the "three places the memory schema lives" problem).

---

## Context

### The idea

Agent Orange's product thesis is a self-revising organisation: an **architect**
that asks, on a clock, *"given the goal and what has actually happened, how
should this organisation change?"* Everything else — onboarding, the org chart,
the console — exists to give that loop something to work on.

Two things follow, and they are the whole plan.

**1. A worker is a job description plus a trigger, not a personality.**
Every invocation is a fresh conversation. Nothing carries over in the thread.
This was not the obvious choice: a single long-lived thread per worker was
designed first and abandoned when the substrate turned out to make it
unworkable. The findings are recorded here because they are permanent
properties of the harness, not bugs to route around.

- **There is no cross-turn compaction, anywhere.** The in-image harness passes
  `persistSession: false` (`sandbox/src/harness/claude-agent-sdk.ts:583`,
  inside the `query({...})` call opening at `:566`) and starts a **fresh SDK
  session every turn**; continuity is `conversationHistory` (`:182`) rendered
  to plain text and **appended to the system prompt** each turn (`:189-194`).
  The SDK's compaction settings are unused and would not help: they compact
  *messages*, and this harness keeps history in the *system prompt*.
- **Restore replays everything.** `rehydrateConversation`
  (`go/runner.go:1976`) rebuilds from `ListQueryEventsFlat`
  (`go/agentdb/messages.go:286-303`) — **no LIMIT, no cap on that path**. A
  container snapshot does not carry the history (`runner.go:1951`).
- **It fails silently, then forever.** There is no context-length error path;
  on overflow the SDK subprocess errors into a generic `AGENT_ERROR`
  (`claude-agent-sdk.ts:745-758`). The overflow is in the *system prompt*, so
  nothing can summarise its way out. A schedule firing into that session
  succeeds at delivery, so `noteProvisionFailure` never trips and the schedule
  keeps firing into a dead session. Estimated survival: **months 2–5**.
- **A worker-bound chat broadcasts its whole transcript.** `emitIdleFinish`
  (`runner.go:2570-2597`) emits `worker.finished` whose text is
  `renderTranscript(sessionID)` — the entire conversation to date — every time
  the session goes idle.
  **[rev2]** rev1 attributed this to `docs/18-workers-memory-events.md` §9 as a
  "rejected design". That was wrong: §9 (`:620-630`) rejects *routing chat
  creates through `ComposeJob`*, and explicitly **endorses** the emission
  ("so a human conversation can wake the archivist"). The behaviour is real and
  is a hazard for a forever thread; it is not a rejected design. See A7 for how
  this plan avoids it for the interview.

So: fresh thread per invocation. The compaction problem, the silent death and
the transcript growth all disappear together.

**2. Continuity moves to memory — and so does coordination.**
If threads are disposable, everything durable is a note. That makes the
*memory contract* — who writes what down, under which labels, and what each
worker reads before it acts — the real architecture. It matters more than the
roster. A customer-response worker told *"if the conversation touched money,
cancelling or leaving, label it `churn-risk`"* plus a weekly worker told
*"read everything labelled `churn-risk`"* is an organisation. Which boxes exist
is secondary.

### The problem this fixes, which the repo already documented

`go/topology/architectarchivist.go:128` calls one prompt *"one of the three
places the project's memory schema lives"*. The three (header, `:32-42`; also
`README.md:94-106`) are:

1. a seeded note `name=label-registry` — the vocabulary (`:197-210`);
2. **the architect's prompt** — which labels each role reads and writes;
3. the archivist's prompt — what finished work becomes.

`docs/product/27-simplification-inventory.md:87-88` says of the second:
*"Leg 2 is the one that is easy to miss, because it lives inside the roles the
architect writes rather than in any single editable field."* `README.md:105`
puts it more sharply: nothing names it.
**[rev2]** rev1 presented those two as a single quotation. That was a
fabricated quote; both sources are given separately here.

And `docs/product/03-memory.md:37-40`: *"**No controlled vocabulary is enforced
by core.**"* Curated-vocabulary tooling is listed under **Deferred (explicitly
not scheduled)** at `docs/product/06-work-plan.md:386`.

This plan does not build a vocabulary enforcer. It does something cheaper: it
makes the rulebook **reach every worker's jobs automatically**, and gives the
architect a structured lever to reconcile against.

### What the substrate already gives us (verified, do not rebuild)

| Capability | Where | Note |
| --- | --- | --- |
| Label selectors | `go/agentdb/labels.go:167-181` | one parser, reused by memories/images/datasets. Equality, `in`/`notin`, exists, negation. **No OR**, no nesting. |
| `latest_per` | `go/agentdb/memories.go:494-511` | newest row per value of a label key, applied **before** ranking. Keep. |
| `since`/`until` | `go/cmd/agentd/timearg.go:43-84` (JSON wrapper; grammar in `agentdb.ParseMSTime`) | RFC3339, unix ms, or a relative age like `"7d"` — so models don't do unit arithmetic. |
| Named singletons | `memory_current(name)` → `NewestMemory` (`memories.go:390`) | full content, retraction-filtered, `{found:false}` is a normal answer. The message-board primitive. |
| Retraction | `RetractionLabel` (`memories.go:315`), hard-filtered `:342` | withdraw without deleting. `docs/product/25-cooperative-patterns.md:1442` marks this BLOCKED — that verdict is **stale**; it shipped. |
| Graceful search degradation | `go/extension/embedding/embedding.go:109-116` | no provider ⇒ keyword+recency, **identical result shape**. Offline behaves the same. |
| Briefings | `BuildBriefingSections` (`go/compose.go:226-289`) | per-worker selector list; newest match per selector, injected before the job runs. Failure-isolated: a selector matching nothing is logged and skipped (`:259-279`). |
| Worker levers for an agent | `go/cmd/agentd/mcp_management.go` | `worker_create` (incl. `mcp_config`, `image`, `briefing`, `:921-930`); `worker_update` on `workerUpdatableFields` (`:1005`) = description, image, max_instances, **briefing**, enabled; `worker_prompt_write` (`workerPromptWriteArgs`, `:1147`; rationale required). |
| Atomic multi-row apply | `Store.ApplyTopology` (`go/agentdb/topology_apply.go:179`) | one transaction: workers, subscriptions, schedules, settings overlay, memory seeds (`:302-314`), config log. **Reused verbatim.** |
| Emitting an event | `POST /agent/events` (`go/httpapi/httpapi.go:441`), `web/src/components/EmitEventControl.tsx` | how "run the architect now" works. |

### The three gaps this plan closes

1. **No project-level briefing.** "Every worker reads the rulebook" is today
   N × `worker_update`, plus vigilance for every worker created afterwards.
2. **`created_by_worker` is stamped and unqueryable.** Every memory records its
   author automatically (`go/cmd/agentd/mcp_memory.go:326`) and
   `MemorySearchQuery` (`go/agentdb/memories.go:83-123`) has no provenance
   filter, so "read my own past" is only expressible as a forgeable label
   convention.
3. 🔴 **A live bug.** `RollingSummarySelector` (`go/compose.go:185-190`) is the
   built-in default briefing selector, `kind=rolling-summary,worker=<name>`.
   The shipped archivist policy writes `kind=summary, name=<thread-slug>`
   (`go/topology/architectarchivist.go:84-88`) — a different `kind` **and no
   `worker=` label at all**. The default section therefore matches nothing in
   every project.
   **[rev2]** rev1 called this a one-label mismatch fixable by changing either
   side. It is two labels; changing `rolling-summary`→`summary` alone still
   matches nothing. See T4.
   Severity is bounded: an unmatched selector is logged and skipped, so nothing
   renders wrongly. The loss is that archivist summaries never reach the worker
   they describe.

---

## Architecture

### A. Onboarding produces a charter, not a team

```
NEW PROJECT   name + goal          (goal REQUIRED)
     |
     +--> POST /agent/topologies/apply  onboarding@v1   -> worker "interviewer"
     +--> POST /agent/session {persona:"interviewer", name:"onboard"}  -> 202 creating
     +--> POLL  GET /agent/sessions/by-name/onboard    until != creating
     +--> POST /agent/session/{id}/message   seed = session id + the goal
     |
     v
 +--------------------- ONBOARDING SCREEN ----------------------+
 |  the interview, as a chat rail                               |
 |  "what does success look like? who would notice? how would   |
 |   you know in a month?"                                      |
 |  +--------------------------------------------------------+  |
 |  | THE CHARTER — appears only when a VALID one exists      |  |
 |  |  goal · measure · labelling rules · architect + cadence |  |
 |  |                                          [ Approve ]   |  |
 |  +--------------------------------------------------------+  |
 +---------------------------------------------------------------+
     |
     v   ONE approval -> ONE transaction (ApplyTopology) + one follow-up write
   architect worker
   + daily schedule, DISABLED
   + subscription to `architect.run`
   + memory seed: goal + measure
   + memory seed: label registry v1
   + project settings: background text AND project-wide briefing
   + the interviewer worker is DISABLED (A8)
     |
     v
   [ Run the architect now ]  -> POST /agent/events {type:"architect.run"}
```

**Decision A1 — the interview creates the architect and nothing else.** The
architect is the bootstrap; it is the only thing that creates workers.
Rejected: the superseded plan's version, where the interview proposed a full
starting org from the fifteen-shape catalogue. Rejected because it puts roster
design in two places, and the first roster is expected to be wrong anyway.

**Decision A2 — the catalogue is untouched.** `TopologyOnboarding` stays
reachable from `web/src/components/WorkersPage.tsx:197` via the "Start from a
topology" button (`:215`). **[R]** The superseded plan claimed it was
unreachable; false. **[rev2]** One honest consequence to state in the docs: a
*new* project no longer lands anywhere near the catalogue, so the fifteen
shapes become something only an operator who already knows about them finds.

**Decision A3 — the goal field is required and is read by something.** Wolf's
sharpest UX scar (`/home/kai/projects/badcode/agent-wolf/web/src/pages/NewHypothesis.tsx:5-15`):
the equivalent field was once labelled *"optional"*, nothing read it, and it
was "the single most confusing thing in the product".

**Decision A4 — the agent proposes, a human approves, once.** The interview
deposits a charter as an append-only memory; the console reads it, validates it
server-side, and one approval applies it. The gate is for **comprehension**,
not correctness.

**Decision A5 — the validator shares the apply path's code exactly.**
`charter_validate` (MCP) and `POST /agent/charter/apply` both call
`charter.Validate` then `charter.Resolve`. Wolf's R269
(`/home/kai/projects/badcode/agent-wolf` lesson log, quoted in
`design/2026-08-20-agent-wolf.md:6710`): a schema described to a model as prose
produced **thirteen** errors on its first real attempt; the fix was a validator
the model calls before depositing, sharing the gate's code. T11 pins the
agreement. The validator is **read-only and cannot deposit** — the deposit goes
through `memory_create` so it carries container provenance.

**Decision A6 — no end signal.** No "finish" tool, no terminal state, no
transcript parsing. The agent re-deposits the **complete** charter every time
it revises; newest wins; there is no partial update.

**Decision A7 [rev2] — the interview session sets `persona`, not `worker`.**
`go/httpapi/session.go:186-196`: `Persona` selects the prompt (resolved live
each turn by `sessioncontext.go:148-165`), while `Worker` is the identity that
`emitIdleFinish` *requires before it will emit at all*. Setting only `persona`
gives the interview the interviewer's prompt and **no `worker.finished`
emission** — so the interview transcript, charter JSON included, never lands on
the project event spine where a future archivist's unfiltered subscription
would sweep it into memory. rev1 set `worker`, reproducing the hazard its own
Context section had just described.
Consequence, accepted: charter memories carry an empty `created_by_worker`.
That is fine — the charter is found by its `name=<session id>` label, not by
provenance, and an empty author is the same signal the HTTP memory route uses
for "the application did this".

**Decision A8 [rev2] — the interviewer is disabled once the charter applies.**
Otherwise it persists enabled, unwired, with no subscription and no schedule,
and the architect's reconciliation pass would find an orphan worker and be free
to rewrite or delete it on run one. `POST /agent/charter/apply` disables it as
one further config-logged mutation after the transaction commits. Re-entering
onboarding re-enables it. Rejected: deleting it (the charter's provenance
points at its session), and freezing it (C4).

### B. How a worker gets its context

```
worker wakes for a JOB (fresh conversation, remembers nothing)
  |
  |-- HANDED AUTOMATICALLY  (BuildBriefingSections; cannot be skipped)
  |     project-wide briefing  ->  the label registry        [NEW]
  |     the worker's own briefing entries
  |
  |-- FETCHES ITSELF  (because its prompt says to)
  |     memory_search created_by_worker=self, kind=summary, limit 10   [NEW]
  |     memory_current("message-board")
  |
  v
does the job; writes notes under the labels the registry defines
```

**Decision B1 — a project-wide briefing.** New `ProjectSettings.Briefing
SelectorList`, unioned into every worker's selector list inside
`BuildBriefingSections`. Set once; changing the rules is publishing a new
version of one note, with no config change at all.

⚠️ **[rev2] Scope of "every worker": JOBS only.** `BuildBriefingSections` has
exactly one caller — `ComposeJob`, reached from `go/cmd/agentd/dispatch.go:333`.
Both the router and the scheduler (worker-mode) reach it. **Chat sessions do
not**: they resolve through `go/cmd/agentd/sessioncontext.go:115-185`, which
returns project prompt + worker prompt and never builds a briefing. So a chat
never receives the registry, including the interview itself. rev1 claimed
"every worker is covered"; that is true of every *job*, not every session.
Note the pre-existing consequence, which this plan does not fix: the shipped
`architect-archivist@v1` architect is described as "an architect you talk to",
carries `Briefing: {"name=label-registry"}`, and in a chat has never received
it in any project.

**Decision B2 — automatic for the universal, instructed for the specific.**
The briefing mechanism is newest-**one**-per-selector, so "my last ten
summaries" is not expressible in it. The rulebook (a singleton) is automatic;
list-shaped reads are prompt instructions using `memory_search`. Rejected:
extending briefings to "newest N" — real work, and the architect can express
the same thing in prose today.

**Decision B3 — provenance becomes queryable.** `memory_search` gains
`created_by_worker`, accepting the sentinel `"self"` resolved **server-side**
from the caller's token. Rejected: auto-stamping a `worker=` label — cheaper,
forgeable, and it burns one of the 32 label slots.
**[rev2]** `"self"` works in any session that carries a worker identity, chat
sessions included (`mcpserver.go:536` sets `caller.Worker` from the session
row). Only a session with no worker identity gets the explanatory refusal.

### C. What the architect does

```
DESIRED STATE = the architect's prompt (the strategy)
ACTUAL STATE  = the workers: their prompts, their briefings, their wiring

STEP 0 — BOOTSTRAP.  If this project has no workers other than me and the
         interviewer, skip steps 1-3 entirely: design the initial roster and
         create it, INCLUDING at least one worker subscribed to
         worker.finished that writes summaries under the registry's labels.
         Nothing else in this system writes memory; until that worker exists
         there can be no evidence, and an evidence gate would stall forever.

THEN, EVERY RUN:
  1. what did I change last time?   config_history entity=worker:<x>
  2. did it help?                   write a kind=architect-verdict memory
                                    naming that change and one of
                                    helped / hurt / no-signal — BEFORE
                                    touching anything
  3. is there new evidence?         if fewer than N new summary/lesson
                                    memories since my last run: record
                                    "no signal", change nothing, finish
  4. reconcile, capped:
       briefings  -> EXACT.  structured; compare and correct precisely.
       prompts    -> JUDGED. AT MOST ONE rewrite per run; the rationale
                             must say what was wrong with the old wording.
       new roles  -> worker_create + subscription_create + schedule_create
  5. if anything changed: request_human_attention with what changed and
                          that it can be reverted
  6. three consecutive no-signal runs -> request_human_attention:
     "nothing in this project writes memory; I cannot steer it"
```

**Decision C0 [rev2] — the bootstrap branch is load-bearing.** Without it the
loop cannot start: a freshly-onboarded project holds exactly two memories (the
goal and the registry seeds) and **no** `kind=summary` or `kind=lesson`,
because A1 means no archivist exists yet and nothing else writes memory. The
evidence gate at step 3 would short-circuit on run one, then run two, then fire
step 6's "nothing writes memory" escalation daily, forever — and mock-mode e2e
cannot detect it, because the mock scripts the model's replies. rev1 had steps
1-6 with no bootstrap and no ticket creating an archivist.

**Decision C1 — the architect's prompt is the desired state; it reconciles.**
The reading half is structured data (`Worker.Briefing` is a selector list), so
reconciliation is exact. The writing half is prose, so it is a model's
judgement — hence the cap.

**Decision C2 — the one-rewrite-per-run brake.** Implements
`docs/product/2026-07-22-landscape-learnings.md` **L20**, and doc 25's "delayed
corrector" rule: a cron-batched corrector must be **weaker** than an
event-driven one and must read `config_history` for its own previous correction
first — *"that read is what breaks the delay term"*.

**Decision C3 — act-then-notify, never ask.** Every mutating run ends with
`request_human_attention` (`go/cmd/agentd/attention.go`), webhook-delivered and
surfaced in the Desk's Asks stack. Its own header says it is deliberately
**not** an approval gate (`attention.go:11`) — which is why it fits.

**Decision C4 — nothing is frozen by default.** The archivist in
`architect-archivist@v1` is frozen because *there* it is a measurement
instrument (`:253-257`). That is one use case. An archivist is just a worker
triggered by `worker.finished`; so are reviewers and checkers. The architect
creates and can rewrite them all. `frozen` remains available for a genuine
scorer, set by a human over HTTP — and if a measure-writer is ever introduced
it **must** be frozen, because a free architect holding `worker_prompt_write`
over its own scorekeeper is reward hacking in the most literal sense.

**Decision C5 — the daily alarm ships disabled; "run now" is an event.**
`charter.Resolve` creates the schedule with `Enabled: false` and a subscription
on `architect.run`; the console's button posts that event.
⚠️ This is not a stylistic choice: `BuildBriefingSections` runs only inside
`ComposeJob` on the dispatch path (verified — sole caller `dispatch.go:333`),
so triggering the architect by opening a chat with it would silently deprive it
of the rulebook. Manual runs must go through an event.

**Decision C6 [rev2] — the brakes are prompt-level, and that is accepted.**
`worker_prompt_write`'s only guard is `if target.Frozen`
(`go/cmd/agentd/mcp_management.go:1179-1180`). There is **no self-write check
and no per-run rate limit anywhere in the engine**. So the architect can
rewrite the very prompt containing its evidence gate, its one-rewrite cap and
its notify-on-change clause — after which none of those exist. This was put to
the operator explicitly, alongside the alternatives (refuse self-rewrite in the
engine; freeze the architect), and the decision is to **leave it open and say
so plainly**. Therefore, stated without hedging: *the architect's loop has no
mechanical brake. Every rule in §C is an instruction it may choose to delete.
Revert is the entire control, and the failure mode to watch for is the system
quietly ceasing to tell you what it is doing.* T25 must put this sentence in
the operator documentation, not a softened version of it.

### D. Revert, narrowly

```
config_events already store the FULL NEW STATE of every row mutation
(config_events.go:247-250); deletions preserve the deleted row
(workers.go:415-449). So "what was it before" = the previous event for
the same entity. REVERT = write that state FORWARD, as a new event.
```

**Decision D1 — forward compensating writes only.** `TestRestoreIsForward`
(`go/agentdb/config_fold_test.go:517`) fails any implementation that rewrites
history.

**Decision D2 — refuse a non-newest revert.** **[R]** Payloads are whole rows,
so reverting event #180 after #195 touched the same entity would silently erase
#195. Revert of anything but the newest event for an entity **refuses**, naming
the intervening events.

**Decision D3 — single-entity only; grouping deferred.** Grouped "revert the
whole day" needs a new `batch_id` column and waits until a real architect run
shows multi-change days happen.

**Decision D4 — no MCP revert tool.** Giving it to the architect would let it
undo the human who just corrected it.

**Decision D5 — never the word "undo" in the UI.**
`docs/product/16-work-plan-operator-console.md:228`. Use "Revert to this
version".

**Decision D6 [rev2] — `project_prompt_write` keeps its narrow payload.**
rev1 had a ticket widening it to the full settings row "so revert is correct".
That was wrong twice over: reverting a prompt write means calling
`SetProjectPrompt` forward with the previous prompt, for which
`{project, system_prompt}` is exactly sufficient; and widening it reintroduces
the hazard the code comment at `go/agentdb/project_settings.go:190-205` exists
to prevent — *"rewriting the prompt through [PutProjectSettings] would silently
rewrite the budgets and the attention channel with whatever the caller last
read."* That comment also names three pinned consumers, including the fold.
The ticket is deleted.

### E. Ordering

```
T1        LIVE PROBE — zero engine code. The thesis, tested first.
T2-T4     memory primitives — INDEPENDENT of T1, ship regardless.
T5-T20    charter + onboarding
T21-T24   narrow revert
T25       switch the architect's daily alarm on
T26       end to end
```

**[rev2]** rev1 made T2-T4 depend on T1, which gated two safe, independently
valuable fixes behind a multi-day live experiment against a billing API. They
now depend on nothing and can be worked in parallel with the probe.

**[R]** The superseded plan tested the central assumption at ticket 24 of 25,
in offline mock mode — which `docs/product/25-cooperative-patterns.md` §5 says
*"proves transmission, never discovery"*. The riskiest assumption is not "can
we revert"; it is "does a cron-woken architect improve an organisation rather
than fidget with it".

---

## File Structure

### Create

| Path | Purpose |
| --- | --- |
| `go/orgprompts/prompts.go` | **leaf package** — imports nothing from this module; `//go:embed` accessors |
| `go/orgprompts/interviewer.md` | the interviewer's system prompt |
| `go/orgprompts/architect.md` | the architect's system prompt (§C, bootstrap branch included) |
| `go/orgprompts/registry.md` | the seed label registry, v1 |
| `go/cmd/agentd/orgprompts_test.go` | the tool-name **and tool-argument** assertions (must live here — see below) |
| `go/charter/charter.go` | `Charter` schema, `Parse`, `Validate` |
| `go/charter/resolve.go` | `Resolve` → `*topology.Bundle` |
| `go/charter/*_test.go` | table tests |
| `go/topology/onboarding.go` | `onboarding@v1` — one worker, `interviewer` |
| `go/cmd/agentd/mcp_charter.go` | one tool: `charter_validate` |
| `go/httpapi/charter.go` | `GET /agent/charter/current`, `POST /agent/charter/apply` |
| `go/agentdb/config_revert.go` | `RevertEvent` |
| `web/src/charter.ts` | pure types + coercers |
| `web/src/useCharter.ts` | polling hook + apply |
| `web/src/components/CharterPanel.tsx` | the charter + Approve |
| `web/src/components/OnboardingPage.tsx` | chat rail + panel |
| `e2e/mock-scripts/onboarding.json` | the mock model's interview script |
| `e2e/features/onboarding.stack.spec.ts` | the stack e2e |
| `docs/product/runs/2026-09-XX-architect-probe/README.md` | T1's findings + the prompt it used |

**Why `go/orgprompts` is its own leaf package:** `go/charter` must import
`go/topology` (it returns a `*topology.Bundle`), so `go/topology/onboarding.go`
reaching into `go/charter` for the interviewer prompt would be an import cycle.
**[R]** The superseded plan left this as a conditional for execution to
discover, and offered "pass the prompt in at registration" as an escape — which
is impossible, since every topology registers from a package-local `init()`
(`go/topology/solo.go:36`) with no injection point.

**[rev2] Why the prompt test lives in `go/cmd/agentd`, not `go/orgprompts`:**
the core tool list is built by `newMemoryTools` / `newManagementTools` / etc. in
**`package main`** under `go/cmd/agentd/`, registered at `main.go:602-607`. A
test in `go/orgprompts` cannot import `package main`, and `orgprompts` is
required to import nothing from this module. rev1 put the test in `orgprompts`
and validated it with `go test ./orgprompts/...`, which would have proved
nothing — and this is the very test held up as the guard against the superseded
plan's "call a tool with an argument it does not have" bug.

### Modify

| Path | Change |
| --- | --- |
| `go/agentdb/migrations.go` | `046_project_briefing` (`045_datasets` confirmed highest) |
| `go/agentdb/project_settings.go` | `Briefing SelectorList` + validation |
| **`go/agentdb/topology_apply.go`** | **add the `Briefing` case to `TopologySettingsOverlay`** — see below |
| `go/compose.go` | union the project briefing into the selector list in `BuildBriefingSections`; the default-selector fix |
| `go/topology/architectarchivist.go` | the other half of the default-selector fix (T4) |
| `go/agentdb/memories.go` | `MemorySearchQuery.CreatedByWorker`; filter in `SearchMemories` |
| `go/cmd/agentd/mcp_memory.go` | `created_by_worker` arg on `memory_search`, `"self"` resolved server-side |
| `go/cmd/agentd/main.go` | one `mcpSrv.register(...)` line, inside the `if agentDB != nil` guard (`:601`) |
| `go/httpapi/httpapi.go` | endpoint constants + `Mux()` registration |
| `go/httpapi/config_events.go` | `entity` + `seq` filters; `GET /agent/config-events/{id}`; the revert route |
| `go/httpapi/project_settings.go` | expose `briefing` |
| `go/cmd/agentd/mcp_config_log.go` | add `seq` to `configHistoryRecord` (`:68`) — **`entity` is already an argument** (`:178`) |
| `web/src/projectSettings.ts` | carry `briefing` |
| `web/src/memories.ts` | mirrors the default selector string at `:402` — must move with `go/compose.go` |
| `web/src/configLog.ts` | carry `seq` |
| `web/src/components/ChangelogView.tsx` | "Revert to this version" |
| `web/src/components/ProjectSettingsPage.tsx` | show/edit the project briefing |
| `web/src/components/BriefingPreview.tsx` + tests | follows the selector change |
| `web/src/index.ts`, `web/src/components/index.ts`, `web/src/pure.ts` | export the new modules |
| **`e2e/helpers/ui.ts`** + the specs using it | **`openFreshProject` breaks after T17** — see below |
| `examples/web/src/App.tsx` | `onboarding` as a **shell-owned transient view** |
| `examples/web/src/ProjectPicker.tsx`, `Sidebar.tsx` | required goal field |
| `docs/18-workers-memory-events.md`, `docs/product/03-memory.md`, `CLAUDE.md` | documentation |

🔴 **[rev2] `TopologySettingsOverlay` is the blocker rev1 missed.**
`ApplyTopology` never writes a `SettingsPatch` directly: it does
`GetProjectSettings → TopologySettingsOverlay(current, patch) → PutProjectSettings`
(`go/agentdb/topology_apply.go:290-300`), and the overlay
(`:108-150`) is a **hardcoded field-by-field switch over exactly nine named
fields**. A tenth field is silently dropped — no error, tests green, and the
project briefing, which is this plan's central mechanism, never reaches the
database. rev1 did not list `topology_apply.go` in any ticket.

🔴 **[rev2] `openFreshProject` breaks.** `e2e/helpers/ui.ts:77-85` fills
`new-project-input`, clicks `new-project-create`, then waits for
`session-sidebar`. After T17 a create needs a goal, lands on the onboarding
view, applies `onboarding@v1` and starts a container-backed session. Concretely
`e2e/features/console.stack.spec.ts:96` asserts *"This project has no workers
yet"* on a fresh project — false once the interviewer exists — and the
four-views assertion at `:102-104` is likewise affected. Every UI spec would
also burn a container and a host port per project, which matters because
`port-pool.stack.spec.ts` runs against a deliberately narrowed pool. T18 owns
this.

**`web/src/navReveal.ts` is NOT modified.** **[R]** It is monotonic and sticky
by design (`:92-98`: *"Once an entry is in here it stays"*), has no hide
mechanism, and every rule is a "has something" predicate. Also `App.tsx:254`
silently redirects any view not in `visible` to the Desk.

### Delete

Nothing.

---

## Interfaces

### `go/orgprompts` (leaf)

```go
package orgprompts
func Interviewer() string
func Architect() string
func LabelRegistry() string
```

### `go/charter`

```go
package charter

// Charter is what the interview deposits. It describes the project's purpose
// and its first labelling rules, and names the architect. It does NOT
// describe a roster — the architect builds that.
type Charter struct {
    Goal    string `json:"goal"`    // required
    Measure string `json:"measure"` // required — how you would know it works

    // LabelRules is the first version of the label registry: the vocabulary
    // and, per label, when it should be written. Free prose, deliberately —
    // core enforces no vocabulary (docs/product/03-memory.md:37-40).
    LabelRules string `json:"label_rules"` // required

    ArchitectName string `json:"architect_name,omitempty"` // default "architect"
    ArchitectCron string `json:"architect_cron,omitempty"` // default "0 9 * * *"

    ProjectBackground string `json:"project_background,omitempty"`
    Rationale         string `json:"rationale"` // required
}

type Issue struct {
    Path    string `json:"path"`
    Message string `json:"message"`
}

func Parse(content string) (c *Charter, summary string, err error)
func Validate(c *Charter) []Issue
func Resolve(c *Charter) (*topology.Bundle, error)

const (
    MemoryKindCharter  = "org-charter"    // labels {kind, name: <session id>}
    MemoryKindGoal     = "project-goal"   // labels {kind, name: "project-goal"}
    MemoryKindRegistry = "registry"       // labels {kind, name: "label-registry"}
    EventArchitectRun  = "architect.run"
    DefaultArchitect   = "architect"
)
```

`Resolve` produces a bundle containing exactly: one architect worker; one
schedule (`Enabled: false`); one subscription on `architect.run`; a
`SettingsPatch` carrying `SystemPrompt` and `Briefing: ["name=label-registry"]`;
and two `MemorySeeds`.

**[rev2] `Briefing` can never be cleared through a `SettingsPatch`, by design.**
The overlay's convention is zero-means-keep, and a `SelectorList` with
`omitempty` marshals nil and empty identically, so the distinction does not
survive the preview round-trip. The overlay case is therefore `len(patch.Briefing) > 0`.
Clearing is done through `PUT /agent/project-settings`. Say so in the docs.

### MCP (`go/cmd/agentd/mcp_charter.go`)

```
charter_validate  {charter: string|object}
  -> {valid, errors:[{path,message}],
      summary: {architect_name, architect_cron, schedule_enabled,
                subscription_event, memory_seed_labels: [...],
                settings_fields: [...]}}
```

**[rev2] It returns a SUMMARY, never the resolved bundle.** rev1's interface
returned `resolved:{workers,...}`, which puts `orgprompts.Architect()` — the
entire run structure, every tool name, every reconciliation rule — into the
interviewer's context on every validate call during an interview. Unbounded
token cost, and it hands the interviewer the architect's operating
instructions for no reason.

Read-only. Postgres-only, like every core tool.

### `memory_search` — one new argument

```
created_by_worker  string   // a worker name, or "self"
```

`"self"` resolves server-side from `caller.Worker`. A session carrying no
worker identity gets an explanatory refusal rather than an empty list.

### HTTP

```
GET  /agent/charter/current?session=<id>
     200 {charter, summary, memory_id, created_at, valid,
          errors:[{path,message}], summary_of_effects:{...}}
     404 {"error":"no charter has been proposed yet — the interview has to
                   deposit an org-charter memory first"}

POST /agent/charter/apply   {session, memory_id, rationale?}
     200 <agentdb.TopologyApplyResult>
     409 the store's verbatim refusal string
     422 {errors:[{path,message}]}

GET  /agent/config-events/{id}
GET  /agent/config-events?entity=&seq=
POST /agent/config-events/{id}/revert   {rationale}
     409 when the target is not the newest event for its entity, naming the
         intervening events
```

### `go/agentdb`

```go
// ProjectSettings gains:
Briefing SelectorList `json:"briefing,omitempty" gorm:"type:jsonb"`

func (s *Store) RevertEvent(ctx context.Context, project, eventID string, cw ConfigWrite) (*ConfigEvent, error)
```

---

## Out of Scope

1. **Long-lived worker threads / session-mode schedules.** See Context.
2. **Context compaction.** Not needed once threads are disposable. The findings
   are recorded so nobody re-proposes a long thread without reading them.
3. **Any change to how a chat session's system prompt is composed**
   (`go/cmd/agentd/sessioncontext.go:115-185`, `go/runner.go:2684-2714`).
   **Agent Wolf's live hypothesis interviews create sessions the way this plan
   does and run through that code.**
4. **Adding the core preamble to chat sessions.** Same reason. The interviewer
   and architect prompts must name their own tools.
5. **Changing the fifteen-shape catalogue or its routes.**
6. **Grouped/batch revert** and the `batch_id` column (D3).
7. **Project-wide time-travel restore.** `FoldTo` (`config_fold.go:457`) is
   EXPERIMENTAL with three named divergences and no production callers.
8. **A vocabulary enforcer**, memory deletion, a curation worker, retrieval
   decay, or memory pagination past 100 — all deferred upstream
   (`docs/product/06-work-plan.md:376-386`).
9. **Worker tooling after hire.** `mcp_config` is settable only at
   `worker_create` (`workerUpdatableFields`, `mcp_management.go:1005` omits
   it), and project-level MCP config is unreachable from inside a container.
   **[rev2]** More precisely: project-level MCP servers are merged into every
   job by `ComposeJob` regardless, so a new worker inherits the project's
   tools. What the architect cannot do is create a worker needing a tool the
   project does not already carry — **and it has no way to discover which
   tools the project carries**, since there is no project-settings read tool
   beyond `project_prompt_read`.
10. **Protecting the label registry from being overwritten.** Any worker can
    publish a newer `name=label-registry`. Accepted; stated in the docs. The
    CAS pattern exists next door (`dataset_put`'s `if_version`) if it matters
    later.
11. **A mechanical brake on the architect** — self-write refusal or freezing.
    Explicitly decided against by the operator; see C6. The consequence must be
    documented in T25 in the words C6 gives.
12. **Fixing the chat-session briefing gap** (B1's warning), including the
    shipped `architect-archivist@v1` architect that has never received its
    label registry in a chat. Noted, not fixed.
13. **Anything in `/home/kai/projects/badcode/agent-wolf`.**
14. **Seeding BadCode's real marketing manager** (spec §8.8) — a production act.

---

## Tickets

### T1: Live architect probe — ZERO engine code   [Status: done | Model: opus]
- **Scope:** Test the plan's central assumption before building anything.
  **Procedure** (rev1 gave none; this is followable as written):
  1. `NO_TMUX=1 ./stack start` — the **real** model, not mock. `./stack status`
     must show real-model mode. This is billable; use a scratch project.
  2. Mint a project, apply `architect-archivist@v1` through the console
     (Workers → "Start from a topology").
  3. Replace the architect's prompt with the §C structure, bootstrap branch
     included, via the console's worker prompt editor. **Save the exact text
     used into the run README — it is T5's input.**
  4. Seed evidence with `POST /agent/memories` (the trust-anchor route: an API
     key or console JWT may append; provenance is server-stamped empty). Write
     three to five days' worth of plausible `kind=summary` and `kind=lesson`
     memories, dated across several days.
  5. Trigger runs without waiting for cron: emit the architect's subscribed
     event, or create a worker-mode schedule with a one-minute cron and disable
     it after each firing. Run at least four times, **including one run with no
     new memories since the previous run**.
  6. Delete the sessions afterwards — the host port pool is the hard
     concurrency ceiling.
- **Record, per run:** did it change anything on the no-signal run (thrash)?
  Did it act at all on the bootstrap run (idle)? Did it read `config_history`
  for its own last change? Did verdict-before-action hold? Was
  one-change-per-run obeyed? Did it send `request_human_attention`?
- **Files:** create `docs/product/runs/2026-09-XX-architect-probe/README.md`
  (use the real date) with the prompt text used, the transcripts, and a verdict.
- **Acceptance criteria:** a written verdict answering the six questions, an
  explicit statement of whether the §C structure survived contact, and the
  prompt text preserved verbatim as T5's handoff artifact. **If the structure
  did not survive, stop and re-plan rather than proceeding to T5.**
- **TDD:** no (experiment)
- **Validation:** `./stack status` showed real-model mode; `./stack sessions`
  is empty afterwards; the README exists and names the model used.
- **Depends on:** —
- [x] done
- Notes: **PASS (2026-09-08).** Findings, transcripts and the prompt as
  installed: `docs/product/runs/2026-09-08-architect-probe/`. Run against the
  real model in subscription mode. Two projects, not one: the ticket's
  `architect-archivist@v1` always gives the architect a colleague, so STEP 0
  could never fire — `archivist:false` gives an architect genuinely alone.
  All six questions answered: no thrash on the no-signal run (zero config
  changes, verified per `actor_session`), substantial correct action on the
  bootstrap run, `config_history` read for its own last change, verdict
  before action 3/3, one prompt rewrite per run, and
  `request_human_attention` only when something changed. NOT proven: STEP 6's
  three-no-signal escalation (run 4 was killed mid-flight — it was exactly the
  run that should have escalated), and anything about behaviour over weeks.
  T5 must fold in three prompt revisions listed in the run README.

### T2: project-wide briefing   [Status: done | Model: sonnet]
- **Scope:** `ProjectSettings.Briefing SelectorList` + migration
  `046_project_briefing`; union it into the selector list in
  `BuildBriefingSections` (`go/compose.go:239-254`), after the built-in default
  and before the worker's own entries, through the existing `add` helper.
  ⚠️ **Add the `Briefing` case to `TopologySettingsOverlay`**
  (`go/agentdb/topology_apply.go:108-150`) — it is a hardcoded nine-field
  switch and a tenth field is otherwise silently dropped, which is exactly how
  this feature ships green and does nothing. Use `len(patch.Briefing) > 0` as
  the zero-means-keep test and append `"briefing"` to the changed-fields list.
  Expose the field on `GET/PUT /agent/project-settings` and in the TS mirror.
  Each entry must parse as a label selector.
  **[rev2]** `add` (`compose.go:243-249`) compares raw strings and skips
  empties but does **not** trim; the worker loop trims before calling. Trim the
  project entries identically or dedup only works on byte-identical strings.
  Give project sections the `briefingHeadingPrefix + selector` heading, not
  `DefaultBriefingHeading` (which is taken).
- **Files:** modify `go/agentdb/migrations.go`, `project_settings.go`,
  **`topology_apply.go`**, `go/compose.go`,
  `go/httpapi/project_settings.go`, `web/src/projectSettings.ts`; tests in each.
- **Acceptance criteria:** a worker with no briefing of its own still receives
  the project's; a duplicate between project and worker lists (including one
  differing only by surrounding whitespace) produces one section; an
  unparseable project entry is logged and skipped without failing the job
  (`compose.go:259-279`); an existing project with no project briefing behaves
  exactly as today; **a `SettingsPatch` carrying a briefing survives
  `TopologySettingsOverlay` and lands in `project_settings`** — assert this
  specifically, it is the blocker rev1 missed.
- **TDD:** yes
- **Validation:** `./stack test-go` (repo root; it appends `./...` itself — do
  not pass package paths) and `(cd web && npm ci && npm test)`
- **Depends on:** —
- [x] done
- Notes:

### T3: memory provenance filter   [Status: done | Model: sonnet]
- **Scope:** `MemorySearchQuery.CreatedByWorker` filtered in `SearchMemories`
  (`go/agentdb/memories.go`), exposed as `created_by_worker` on `memory_search`
  (`go/cmd/agentd/mcp_memory.go:230-250`), with the sentinel `"self"` resolved
  **server-side** from `caller.Worker` — never from an argument. Put the filter
  in the **hard filter** (the `filtered` CTE) alongside project, selector,
  since/until and the retraction check, so it applies before `latest_per`
  and before both ranking legs — not in either leg.
- **Files:** modify `go/agentdb/memories.go`, `go/cmd/agentd/mcp_memory.go`;
  tests.
- **Acceptance criteria:** `"self"` returns only the caller's own memories and
  cannot be spoofed by an argument; a plain worker name filters correctly;
  combining it with `latest_per` groups only within the filtered set; a session
  with no worker identity gets an explanatory error (note a worker-attached
  **chat** does have an identity — `mcpserver.go:536`).
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** —
- [x] done
- Notes: Implemented — `MemorySearchQuery.CreatedByWorker` (`go/agentdb/memories.go`)
  joins the hard `where` string right after Since/Until and before the
  LatestPer block, so it lands in the `filtered` CTE ahead of `latest_per` and
  both RRF legs, same as every other narrowing. `created_by_worker` arg added
  to `memory_search` (`go/cmd/agentd/mcp_memory.go`); `"self"` is resolved from
  `caller.Worker` inside the handler, never from the argument, and refused
  with an explanatory error (not an empty list) when `caller.Worker == ""`.
  Store-level tests in new `go/agentdb/memories_provenance_live_test.go`
  (live Postgres); MCP-level tests in `go/cmd/agentd/mcp_memory_test.go`
  (`TestMemoryToolsSearchCreatedByWorker`). All four acceptance criteria
  covered; full targeted run green against an isolated Postgres.

### T4: the dead default briefing   [Status: done | Model: sonnet]
- **Scope:** `RollingSummarySelector` (`go/compose.go:185-190`) selects
  `kind=rolling-summary,worker=<name>`. The shipped archivist policy
  (`go/topology/architectarchivist.go:84-88`) writes `kind=summary,
  name=<thread-slug>` — a different `kind` **and no `worker=` label at all**,
  so the default section matches nothing in every project.
  **[rev2]** Fixing one label is not enough. Bring both sides into agreement:
  either make the archivist policy write `kind=rolling-summary,worker=<the
  subject worker>`, or change the selector to something the policy actually
  produces. State which you chose and why in Notes. The archivist must be able
  to know *which worker* a finished conversation was for — the `worker.finished`
  envelope carries it.
  ⚠️ `web/src/memories.ts:402` mirrors the selector string verbatim, and
  `BriefingPreview.tsx` plus its tests render it; the console's briefing
  preview goes permanently out of sync with core if only the Go side moves.
  Doc text at `go/compose.go:83` and `:147` also names the label.
- **Files:** modify `go/compose.go` and/or
  `go/topology/architectarchivist.go`, `web/src/memories.ts`,
  `web/src/components/BriefingPreview.tsx` + tests.
- **Acceptance criteria:** in a project seeded with `architect-archivist@v1`
  where the archivist has written one summary, the subject worker's **default**
  briefing section now matches it — asserted by a test that would have found
  nothing before this ticket; the console preview and core agree on the
  selector string (assert they are derived from one source if practical).
- **TDD:** yes
- **Validation:** `./stack test-go` and `(cd web && npm test)`
- **Depends on:** —
- [x] done
- Notes: Chose the archivist-writes side, not the selector side (rev2's (a),
  not (b)): `RollingSummarySelector` (`go/compose.go:185-190`) is unchanged and
  correct — it's tested against the actual worker-briefing wiring in
  `go/compose_briefing_test.go` and mirrored verbatim (already, not newly) in
  `web/src/memories.ts:402` (`rollingSummarySelector`) and pinned by both
  `web/src/memories.test.ts:238-239` and
  `web/src/components/BriefingPreview.test.tsx:32,72` — so the console was
  never the mismatched side and needed no change. The bug was entirely that
  `archivistPrompt` (`go/topology/architectarchivist.go`) never told the model
  to write under `kind=rolling-summary,worker=<name>` at all. Added one
  mechanics bullet to `archivistPrompt` (after the label-conventions bullet)
  instructing it to ALSO write a `kind=rolling-summary, worker=<name>` memory
  alongside its policy-driven `kind=summary`, naming the subject worker from
  the "From worker: " line `renderFirstMessage` (`go/compose.go:564-566`)
  stamps at the top of the triggering `worker.finished` event — that's how the
  archivist learns *which* worker a finished conversation was for. Also
  documented the new label in `labelRegistrySeed` (marked "do not write it
  yourself" for other workers, since it's mechanical, not part of the schema
  they design). New test file
  `go/topology/architectarchivist_briefing_test.go`:
  `TestArchivistPromptInstructsRollingSummary` asserts the rendered archivist
  prompt names `rolling-summary`, `worker=` and `From worker` — confirmed
  failing against the pre-fix prompt (ran it before the edit; it failed with
  "archivist prompt does not mention rolling-summary at all"), passing after.
  `TestArchivistRollingSummaryReachesSubjectWorkersBriefing` is the end-to-end
  wiring check: seeds `architect-archivist@v1`, writes a fake memory under
  `agentkit.RollingSummarySelector(architect.Name)`, and asserts
  `BuildBriefingSections` returns it as the worker's default section (it also
  asserts an old-shaped `kind=summary,name=<slug>` memory, with no `worker=`,
  matches nothing — the historical failure, still true, just no longer what
  the archivist writes). `./stack test-go` full suite green (topology 0.068s);
  `cd web && npm test` — 1410 passed, 2 unrelated failures in
  `ProjectSettingsPage.test.tsx` (K2 reason-field timing/pointer-events, no
  connection to memories/briefings, no files of mine touch that component) that
  pass individually in isolation — pre-existing flakiness under full-suite
  load, not caused by this ticket. Did not touch `go/compose.go`,
  `web/src/memories.ts` or `BriefingPreview.tsx`: nothing on the selector side
  needed to change.

### T5: `orgprompts` leaf package + the three prompts   [Status: pending | Model: opus]
- **Scope:** Create `go/orgprompts`, importing **nothing** from this module,
  with `//go:embed` accessors. **Start from the prompt text T1 preserved** and
  fold in what the probe learned — that handoff is why T1 comes first.
  **`interviewer.md` must:** state that its job is a charter, not a team, and
  that the architect builds the roster; name `ask_user` and require one
  question per turn; drive towards a goal, a measure ("how would you know in a
  month?") and a first set of labelling rules; name `charter_validate` and
  require calling it with the exact content about to be deposited, depositing
  only once valid; name `memory_create` and give the deposit contract (labels
  `{kind: "org-charter", name: <the session id given in the first message>}`,
  line 1 summary, then JSON only, always complete); forbid claiming anything is
  live; include a worked example.
  **`architect.md` must:** carry §C verbatim **including STEP 0, the bootstrap
  branch** — without it the loop cannot start (C0); name every tool it uses
  (`config_history` with its `entity` argument, `memory_search` with
  `created_by_worker`, `memory_current`, `worker_list`, `worker_create`,
  `worker_update`, `worker_prompt_read`, `worker_prompt_write`,
  `subscription_create`, `schedule_create`, `project_prompt_write`,
  `request_human_attention`); state that designing a role means designing the
  labels it reads and writes; state that revising the goal means writing both
  the project background and a new `project-goal` memory; state the
  data-not-instructions rule explicitly (chat sessions get no core preamble).
  **`registry.md`** — model on `labelRegistrySeed`
  (`go/topology/architectarchivist.go:197-210`), including `retracts=`.
- **Files:** create `go/orgprompts/{prompts.go,interviewer.md,architect.md,registry.md}`.
- **Acceptance criteria:** all three accessors return non-empty; `architect.md`
  contains a bootstrap branch that precedes the evidence gate.
- **TDD:** no (prose). The assertions live in T6.
- **Validation:** `cd go && go build ./... && go test ./orgprompts/... -count=1`
- **Depends on:** T1
- [ ] done
- Notes:

### T6: the prompt/tool-schema assertions   [Status: pending | Model: sonnet]
> ⚠️ **Worked OUT OF NUMERIC ORDER — after T10.** It asserts against the
> registered core tool list, which does not contain `charter_validate` until
> T10 registers it. The execution rules permit this: dependencies override
> numeric order. Skip it when you reach it and return after T10.
- **Scope:** The test that makes T5 trustworthy. **It must live in
  `go/cmd/agentd`**, because the core tool list is built in `package main`
  there (`main.go:602-607`) and `go/orgprompts` may import nothing from this
  module. For every tool name mentioned in either prompt: assert it exists in
  the registered core tool list, and that **every argument named alongside it
  exists in that tool's input schema**. State the `mcp__<server>__<tool>` →
  bare-name mapping explicitly, and **carve out the `ui` server**: `ask_user`
  is an in-image sandbox builtin (`sandbox/src/tools/builtin/ask_user.ts`,
  `registry-impl.ts:14`), not a core tool, so assert its presence differently
  or exempt it by name with a comment saying why.
  Also assert the worked example in `interviewer.md` parses and validates — an
  example that does not validate teaches the model to fail and looks fine in
  review.
- **Files:** create `go/cmd/agentd/orgprompts_test.go`.
- **Acceptance criteria:** the test fails if a prompt names a tool that does
  not exist, **or an argument a tool does not accept** — this is the guard
  against the superseded plan's `memory_current(kind:)` error; it passes with
  the shipped prompts; the `ui` carve-out is explicit and commented.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/... -count=1`
- **Depends on:** T5, T10
- [ ] done
- Notes:

### T7: `charter` schema, parser and validator   [Status: done | Model: sonnet]
- **Scope:** `go/charter` with the `Charter` and `Issue` types,
  `Parse(content) (*Charter, summary string, err error)` — line 1 a human
  summary, then one JSON object, unknown keys rejected — and `Validate`
  returning **all** issues at once: `goal`, `measure`, `label_rules`,
  `rationale` non-empty; `architect_name` (if given) passes
  `agentdb.ValidateWorkerName`; `architect_cron` (if given) is a valid 5-field
  cron.
  **[rev2]** rev1 split parse and validate across two tickets for a six-field
  schema; merged.
- **Files:** create `go/charter/charter.go`, `charter_test.go`.
- **Acceptance criteria:** a well-formed deposit parses; an unknown key errors
  naming its path; a missing summary line errors; an empty body errors; a
  charter breaking three rules returns three issues with addressable `Path`s.
- **TDD:** yes
- **Validation:** `cd go && go test ./charter/... -count=1 && go vet ./charter/...`
- **Depends on:** T5
- [x] done
- Notes: Implemented in `go/charter/charter.go` + `go/charter/charter_test.go`
  (13 table-test cases, all pass; `go build ./...` and `go vet ./charter/...`
  green). The stated T5 dependency did not hold in practice — `Parse` and
  `Validate` touch no prompt text, only `Resolve` (T8) will need
  `go/orgprompts`/`go/topology` — so this was built without T5 in place.
  "Missing summary line" vs. "empty body" were split: an empty/whitespace-only
  deposit, or a summary line with nothing (or only blank lines) after it, is
  treated as "empty body"; a deposit whose line 1 is itself blank but has a
  JSON body after it is "missing summary line". Unknown-field errors are
  extracted from `encoding/json`'s fixed `unknown field "foo"` message via
  regexp — safe because `Charter` is flat (no nested objects), so the field
  name IS the JSON path; a nested schema would need a real path-tracking
  decoder instead.

### T8: `charter.Resolve`   [Status: pending | Model: opus]
- **Scope:** Charter → `*topology.Bundle`, pure, leaving `ID`/`Project`/
  timestamps zero for apply to stamp. Produces exactly: the architect worker
  (prompt `orgprompts.Architect()`, briefing including `name=label-registry`);
  one schedule at `ArchitectCron` with **`Enabled: false`**; one subscription
  on `architect.run`; a `SettingsPatch` with `SystemPrompt` (goal + measure as
  background prose) and `Briefing: ["name=label-registry"]`; two `MemorySeeds`
  — the goal (`{kind:"project-goal", name:"project-goal"}`) and the registry
  (`{kind:"registry", name:"label-registry"}`, content from `LabelRules`).
- **Files:** create `go/charter/resolve.go`, `resolve_test.go`.
- **Acceptance criteria:** exactly one worker; the schedule is disabled; the
  subscription targets the architect on `architect.run`; both memory seeds
  carry the exact labels above; the settings patch carries both the background
  and the briefing; `Resolve` is deterministic (asserted twice in one test).
- **TDD:** yes
- **Validation:** `cd go && go test ./charter/... -count=1 && go vet ./...`
- **Depends on:** T7, T2
- [ ] done
- Notes:

### T9: `onboarding@v1` topology   [Status: pending | Model: sonnet]
- **Scope:** A 16th built-in in `go/topology/onboarding.go` following
  `solo.go`. No questions; one worker, `interviewer`, with
  `orgprompts.Interviewer()`, enabled; no subscriptions, no schedules.
- **Files:** create `go/topology/onboarding.go`, `onboarding_test.go`.
- **Acceptance criteria:** appears in `topology.List()`; boot-time
  `validateTopology` passes; the rendered prompt is byte-identical to
  `orgprompts.Interviewer()`; `go build ./...` shows no import cycle.
- **TDD:** yes
- **Validation:** `cd go && go build ./... && go test ./topology/... -count=1`
- **Depends on:** T5
- [ ] done
- Notes:

### T10: MCP `charter_validate`   [Status: pending | Model: sonnet]
- **Scope:** One new file plus one `mcpSrv.register(...)` line in `main.go`
  inside the `if agentDB != nil` guard (`:601`) — Postgres-only like every core
  tool. Accepts the charter as a string (as deposited) or an object. **Returns
  a summary, never the resolved bundle** — see Interfaces: the bundle contains
  the entire architect prompt, and the interviewer calls this repeatedly.
  Must not write.
- **Files:** create `go/cmd/agentd/mcp_charter.go`; modify `main.go`.
- **Acceptance criteria:** the tool appears in the core tool list; a valid
  charter returns `valid: true` with the summary fields; an invalid one returns
  every issue; reachable from an HTTP-created chat session; the config-event
  count is unchanged after a validate call; **the response does not contain the
  architect's system prompt** (assert on a substring of it).
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/... -count=1 && go build ./...`
- **Depends on:** T8
- [ ] done
- Notes:

### T11: charter HTTP routes   [Status: pending | Model: sonnet]
- **Scope:** `GET /agent/charter/current?session=` reads the newest memory
  labelled `{kind:"org-charter", name:<session>}` for the credential's project;
  `Parse`, `Validate`, and return the documented body; 404 with the documented
  sentence when absent; 501 when the product store is absent.
  `POST /agent/charter/apply` re-reads the **stored memory** (never a
  body-supplied charter — the container is the untrusted party), re-validates,
  re-resolves, builds an `agentdb.TopologyApplication` with `Topology:
  "charter"` and calls `Store.ApplyTopology`. 422 with issues when invalid; 409
  with the store's verbatim refusal, reusing `writeTopologyApplyErr`
  (`go/httpapi/topologies.go:305`). **After the transaction commits, disable
  the `interviewer` worker** as one further config-logged mutation (A8).
- **Files:** create `go/httpapi/charter.go`, `charter_test.go`; modify
  `httpapi.go`.
- **Acceptance criteria:** no deposit → 404 with that sentence; invalid → 200
  with `valid:false` and issues; valid → 200; a session in another project →
  404, never an existence oracle; a valid apply creates the worker, schedule,
  subscription, **the project briefing on `project_settings`** and both memory
  seeds in one transaction (`topology_apply.go:302-314` is the seeds loop); the
  interviewer is disabled afterwards; a second apply yields 409 on the
  architect name collision and writes nothing.
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T10
- [ ] done
- Notes:

### T12: the shared-verdict test   [Status: pending | Model: sonnet]
- **Scope:** Assert `charter_validate` and the apply route reach the same
  verdict. A table of charters (valid and invalid) through both; they must
  agree on `valid` and on the issue set for every case.
- **Files:** create/extend `go/cmd/agentd/mcp_charter_test.go`.
- **Acceptance criteria:** at least six cases covering every invalidity class
  from T7; any divergence fails naming the case.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/... -count=1`
- **Depends on:** T11
- [ ] done
- Notes:

### T13: `architect.run` reaches a briefed job   [Status: pending | Model: sonnet]
- **Scope:** **[rev2]** rev1 put this assertion inside a web ticket whose
  validation could never run it. Prove, in Go, that emitting `architect.run`
  produces a dispatched architect job whose **composed prompt contains the
  label registry** — i.e. the subscription matches, the delivery dispatches,
  and `BuildBriefingSections` picked up the project briefing. This is the
  end-to-end proof of C5 and B1 together.
- **Files:** create/extend a test under `go/cmd/agentd/` beside the existing
  router/dispatch tests.
- **Acceptance criteria:** the subscription created by `charter.Resolve`
  matches an `architect.run` event; the resulting composed prompt contains the
  registry content; a project with no project briefing produces a job without
  it (the negative case).
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T11
- [ ] done
- Notes:

### T14: `web/src/charter.ts` + `useCharter`   [Status: pending | Model: sonnet]
- **Scope:** Pure types, coercers and the polling hook, mirroring
  `web/src/topologies.ts` and `useTopologies.ts`. `valid` coerces as
  `raw.valid === true` so an absent or garbled field reads as INVALID
  (`web/src/topologies.ts:198`). Add `export * from './charter.js'` to
  `web/src/pure.ts` — the pure-tier test walks `pure.ts`'s graph only, so
  without the export the criterion is vacuous.
- **Files:** create `web/src/charter.ts`, `charter.test.ts`,
  `web/src/useCharter.ts`; modify `web/src/index.ts`, `web/src/pure.ts`.
- **Acceptance criteria:** coercers fill every omitted field; `valid` is false
  for `undefined`, `"true"`, `1` and `null`; a 404 is the hook's empty state,
  not an error; `applyError` carries the server's own words; `pure.test.ts`
  passes with the new export.
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm test && npm run typecheck`
- **Depends on:** T11
- [ ] done
- Notes:

### T15: `CharterPanel`   [Status: pending | Model: sonnet]
- **Scope:** Renders the summary, the rationale as a commit message, and the
  charter's four substantive parts — goal, measure, labelling rules, architect
  and cadence — plus one Approve button, disabled unless `valid`, with the
  issues in words where the disabled button is. Router-free.
- **Files:** create `web/src/components/CharterPanel.tsx`, test; modify
  `web/src/components/index.ts`.
- **Acceptance criteria:** invalid → no Approve, issues listed in words; valid
  → Approve fires apply; a 409 renders verbatim; the labelling rules render in
  full, not truncated — they are the thing being agreed.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck`
- **Depends on:** T14
- [ ] done
- Notes:

### T16: `OnboardingPage` + "Run the architect now"   [Status: pending | Model: opus]
- **Scope:** The screen: a chat rail bound to the **named onboarding session
  specifically** — `AgentChat` takes all-optional props and otherwise falls
  back to `AgentChatProvider`'s *current* session
  (`web/src/components/AgentChat.tsx:81-129`), so wire it to the resolved
  session id explicitly rather than rendering it bare. `CharterPanel` beneath
  it once a charter exists. The rail must show a **named** waiting state while
  the session starts — session creation is slow by construction and a
  frozen-looking screen reads as a bug and gets clicked again; name the cause
  ("starting a container, this can take a minute"). Rail sizing
  `clamp(360px, 32vw, 620px)`, full height of a flex column, never a pixel
  height. After a successful apply, show a **"Run the architect now"** control
  that posts `{type: "architect.run"}` to `POST /agent/events`
  (`go/httpapi/httpapi.go:441`); reuse `EmitEventControl.tsx` if it fits. Add
  the same control to `WorkersPage` for the architect.
  ⚠️ It **must** be an event, not a chat with the architect — a chat session
  receives no briefing at all (B1).
- **Files:** create `web/src/components/OnboardingPage.tsx`, test; modify
  `web/src/components/WorkersPage.tsx`, `web/src/components/index.ts`.
- **Acceptance criteria:** the waiting copy names the cause; the rail is bound
  to the named session; the panel appears without a reload when a valid charter
  lands; the run control appears only after apply and reports server errors
  verbatim.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck && npm run build`
- **Depends on:** T15, T13
- [ ] done
- Notes:

### T17: app-shell wiring   [Status: pending | Model: opus]
- **Scope:** In `examples/web`: a **required** goal field on project creation
  (`ProjectPicker.tsx`, and replace `Sidebar.tsx`'s `window.prompt` with a real
  form — a prompt box cannot carry two fields). Label it for what it DOES
  ("your goal — the interview starts from this"), never "optional".
  On create, in order: mint the token; apply `onboarding@v1`;
  **`POST /agent/session {persona:"interviewer", name:"onboard"}` — `persona`,
  NOT `worker`** (A7: `worker` would make every idle emit the whole interview
  onto the event spine); **poll `GET /agent/sessions/by-name/onboard` until
  status leaves `creating`**; then `POST /agent/session/{id}/message` with the
  seed — the session id and the goal, the goal verbatim below a rule, with a
  line attributing everything below the rule to the user. Surface any create
  failure **verbatim**, including "host port pool is exhausted". Add
  `onboarding` as a shell-owned transient view in `App.tsx`'s render switch —
  **not** a `navReveal` entry. Re-entering onboarding for a project that
  already has an `onboard` session reuses it rather than 409-ing on the taken
  name; if the interviewer was disabled by a previous apply, re-enable it.
  The session name is `onboard`, not `onboard-<project>`: names are already
  project-scoped and `maxSessionNameLen` is 64
  (`go/agentdb/sessions.go:167`).
- **Files:** modify `examples/web/src/App.tsx`, `ProjectPicker.tsx`,
  `Sidebar.tsx`.
- **Acceptance criteria:** creating a project with a goal lands on onboarding;
  the goal and session id are visible as the first message; a second visit
  reuses the session; the created session row has an empty `worker` column and
  `persona: "interviewer"`; `web/` is built before `examples/web` typechecks
  (`examples/web/tsconfig.json:23-25` aliases `../../web/dist`).
- **TDD:** no (wiring)
- **Validation:** `(cd web && npm run build) && (cd examples/web && yarn && yarn typecheck)`
- **Depends on:** T16
- [ ] done
- Notes:

### T18: repair the browser e2e fixtures   [Status: pending | Model: sonnet]
- **Scope:** **[rev2]** T17 breaks the existing suite and rev1 had no ticket
  for it. `e2e/helpers/ui.ts:77-85`'s `openFreshProject` fills
  `new-project-input`, clicks `new-project-create` and waits for
  `session-sidebar`; after T17 a create needs a goal and lands on onboarding.
  Update the helper; decide and implement whether specs that do not care about
  onboarding get a fast path (e.g. mint the project via the API and navigate
  straight in) so they do not each burn a container and a host port —
  `port-pool.stack.spec.ts` runs against a deliberately narrowed pool. Fix
  `e2e/features/console.stack.spec.ts:96` ("This project has no workers yet",
  now false — the interviewer exists) and the four-views assertion at
  `:102-104`, plus any sibling spec the helper touches.
- **Files:** modify `e2e/helpers/ui.ts`, `e2e/features/console.stack.spec.ts`,
  and any other spec calling `openFreshProject`.
- **Acceptance criteria:** the full stack suite passes; no spec creates a
  container-backed session it does not need; the helper's doc comment states
  which path it now takes and why.
- **TDD:** no (fixtures)
- **Validation:** `NO_TMUX=1 ./stack start mock && ./e2e/run-stack-e2e.sh test mock`
- **Depends on:** T17
- [ ] done
- Notes:

### T19: project briefing in the console   [Status: pending | Model: sonnet]
- **Scope:** Show and edit `ProjectSettings.Briefing` on
  `ProjectSettingsPage.tsx`, with copy explaining these selectors are handed to
  **every job** in the project (and noting chat sessions do not receive them —
  B1). ⚠️ `PUT /agent/project-settings` is whole-object, so until this ships
  any settings save from the console writes `briefing: null` and wipes it —
  keep T2 and T19 in the same merge.
- **Files:** modify `web/src/components/ProjectSettingsPage.tsx`,
  `web/src/projectSettings.ts`; tests.
- **Acceptance criteria:** entries can be added and removed; an unparseable
  selector is refused client-side by the same rule the server applies; the page
  explains the blast radius and the chat-session exception in a sentence each.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck`
- **Depends on:** T2, T17
- [ ] done
- Notes:

### T20: onboarding e2e (mock mode)   [Status: pending | Model: sonnet]
- **Scope:** One spec in the only rig (`e2e/features/` +
  `playwright.stack.config.ts`) plus the mock script driving it. The stack's
  mock is `go/modelproxy` fed by `AGENTKIT_MOCK_MODEL_SCRIPT`
  (`go/cmd/agentd/modelproxy.go:107-108`, `docker-compose.yml:172-173`);
  scripts are JSON in `e2e/mock-scripts/` — follow `architect-archivist.json`.
  The script must make the interviewer call `charter_validate` then
  `memory_create` with a valid charter. **Gate the spec on
  `STACK_MOCK_SCRIPT`** the way its siblings do (`e2e/run-stack-e2e.sh:161`,
  `:276`) so it skips in a plain run — otherwise T26's script-less suite run
  fails on it. Flow: create a project with a goal, land on onboarding, assert
  the waiting copy, let the deposit land, assert the panel, approve, assert the
  architect exists with a **disabled** schedule, both memory seeds are present,
  the project briefing is set, and the interviewer is disabled.
- **Files:** create `e2e/mock-scripts/onboarding.json`,
  `e2e/features/onboarding.stack.spec.ts`.
- **Acceptance criteria:** green offline with no API key; skips cleanly without
  the script; deletes the sessions it creates.
- **TDD:** no (e2e)
- **Validation:**
  ```sh
  ./e2e/run-stack-e2e.sh up mock
  ./e2e/run-stack-e2e.sh test mock --mock-script e2e/mock-scripts/onboarding.json
  ./stack sessions
  ```
  **[rev2]** Do **not** use `run` with `--mock-script`: `cmd_run`
  (`e2e/run-stack-e2e.sh:342-356`) takes only a mode and passes nothing onward,
  so the flag is silently dropped and the spec fails against the default canned
  mock. `run` is also a clean-room cycle that tears the stack down.
- **Depends on:** T18, T19
- [ ] done
- Notes:

---

*Revert phase. The architect's daily schedule stays disabled until T25.*

### T21: config-event addressability   [Status: pending | Model: sonnet]
- **Scope:** `GET /agent/config-events` gains `entity` and `seq` filters — the
  store's `ConfigEventQuery.Entity` exists (`go/agentdb/config_events.go:469`)
  but is not exposed over HTTP; add `GET /agent/config-events/{id}`; carry
  `seq` on the web `ConfigEvent` type and coercer
  (`web/src/configLog.ts:80-100`) and add `seq` to `configHistoryRecord`
  (`go/cmd/agentd/mcp_config_log.go:68`).
  **[rev2]** `entity` is **already** an argument on `config_history`
  (`mcp_config_log.go:178`) and already a field on the record (`:68`); rev1
  implied both were missing. Only `seq` is missing on the MCP side. The HTTP
  half of the ticket stands.
- **Files:** modify `go/httpapi/config_events.go`, `httpapi.go`,
  `go/cmd/agentd/mcp_config_log.go`, `web/src/configLog.ts`; tests.
- **Acceptance criteria:** an entity's chain is fetchable over HTTP in one
  call; one event is fetchable by id; the browser sorts by `seq` rather than
  timestamp.
- **TDD:** yes
- **Validation:** `./stack test-go` and `(cd web && npm test)`
- **Depends on:** T20
- [ ] done
- Notes:

### T22: `RevertEvent`   [Status: pending | Model: opus]
- **Scope:** A **forward** compensating write through the ordinary logged
  mutations, in one transaction, with a rationale quoting the reverted event
  id. Revert of a create is a delete; revert of a delete recreates the row
  **with its original id** — the store's `CreateSubscription` /
  `CreateSchedule` honour a supplied id (`go/agentdb/events.go:618`,
  `go/agentdb/schedules.go:296`) while the HTTP routes do not, so revert must
  use the store seam directly; revert of an update writes the previous event's
  payload for the same entity key forward. "Previous" is the preceding event
  for the same `EntityRef` (`go/agentdb/config_fold.go:245`) — do **not** use
  `FoldTo`. A create with no predecessor reverts to a delete. **Refuse a
  non-newest revert** (D2), naming the intervening events. Document that
  `topology_apply` events carry a decision, not a row, and are not individually
  revertable.
- **Files:** create `go/agentdb/config_revert.go`, `config_revert_test.go`.
- **Acceptance criteria:** create/update/delete round-trip; a restored
  subscription keeps its id; reverting event N when N+1 touched the same entity
  **refuses** naming N+1; `TestRestoreIsForward` still passes; reverting an
  already-reverted event is a no-op or an explicit refusal (choose, document,
  test); reverting a lone `topology_apply` refuses with an explanation.
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T21
- [ ] done
- Notes:

### T23: revert HTTP route   [Status: pending | Model: sonnet]
- **Scope:** `POST /agent/config-events/{id}/revert` taking `{rationale}`,
  returning the event written. Project from the credential. No MCP equivalent
  (D4).
- **Files:** modify `go/httpapi/config_events.go`, `httpapi.go`; tests.
- **Acceptance criteria:** another project's event → 404; a frozen worker may
  be reverted by a human (the freeze blocks agents, not people — the `Frozen`
  comment in `go/agentdb/workers.go`); a non-newest target → 409 naming the
  intervening events.
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T22
- [ ] done
- Notes:

### T24: revert in the changelog UI   [Status: pending | Model: sonnet]
- **Scope:** A "Revert to this version" action on `ChangelogView` entry cards,
  requiring a typed rationale and showing a confirmation naming exactly what
  will change. **Never the word "undo"**
  (`docs/product/16-work-plan-operator-console.md:228`). A non-newest entry
  shows the action disabled with the server's reason.
- **Files:** modify `web/src/components/ChangelogView.tsx`,
  `web/src/configLog.ts`; tests.
- **Acceptance criteria:** "undo" appears nowhere in rendered output
  (asserted); the rationale is required; a server refusal renders verbatim.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck`
- **Depends on:** T23
- [ ] done
- Notes:

### T25: switch the architect on + document   [Status: pending | Model: sonnet]
- **Scope:** Flip the architect's schedule to `Enabled: true` in
  `charter.Resolve` (T8 created it disabled) and update T8's test.
  **[rev2]** Note in the docs that projects onboarded before this ticket keep a
  disabled architect; there is no migration, and the console's schedule editor
  is how an operator enables one.
  Document: onboarding and the charter; the architect's standing loop; the
  project briefing and the memory contract; and — **in the words C6 gives, not
  a softened version** — that the loop has no mechanical brake, that every rule
  in its prompt is an instruction it may delete, that revert is the entire
  control, and that the failure mode to watch for is the system quietly
  ceasing to report what it is doing. Also document the known limits: tools
  cannot be granted after hire and the architect cannot discover which the
  project carries; the label registry is unprotected; briefings are
  newest-one-per-selector; chat sessions receive no briefing; a project
  briefing cannot be cleared through a charter, only through
  `PUT /agent/project-settings`.
- **Files:** modify `go/charter/resolve.go`, tests,
  `docs/18-workers-memory-events.md`, `docs/product/03-memory.md`,
  `CLAUDE.md`.
- **Acceptance criteria:** a newly applied charter has an enabled daily
  architect schedule; the docs contain C6's statement unhedged.
- **TDD:** no (config + docs)
- **Validation:** `cd go && go test ./charter/... -count=1`
- **Depends on:** T24
- [ ] done
- Notes:

### T26: End-to-end verification   [Status: pending | Model: opus]
- **Scope:**
  1. `NO_TMUX=1 ./stack start mock`; create a project with a goal; confirm the
     waiting copy; drive the interview to a charter; approve; confirm the
     architect, its schedule, both memory seeds, the project briefing on
     `project_settings`, and that the interviewer is disabled.
  2. Press "Run the architect now"; confirm the resulting job's composed prompt
     **contains the label registry**.
  3. Let the architect make a change; revert it from the changelog; confirm the
     previous state is restored and history contains both.
  4. Confirm a non-newest revert is refused with a readable reason.
  5. `./stack sessions`, then `./e2e/run-stack-e2e.sh clean`.
- **Files:** none (verification), plus Discovered Issues Log entries.
- **Acceptance criteria:** all five steps observed; all gates green.
- **TDD:** no
- **Validation:** run each line from the **repo root**:
  ```sh
  (cd go && go build ./... && go vet ./... && go test ./...)
  ./stack test-go
  (cd web && npm ci && npm test && npm run typecheck && npm run build)
  bash web/scripts/verify-package.sh
  (cd sandbox && npm ci && npm test && git checkout yarn.lock)
  (cd examples/web && yarn && yarn typecheck)
  NO_TMUX=1 ./stack start mock
  ./e2e/run-stack-e2e.sh test mock
  ```
  Notes an executor needs: `./stack test-go` **appends `./...` unconditionally**
  (`stack:481-484`) — pass flags only, never package paths.
  `./stack test_pre_pr` does **not** exist here; it survives only in
  `migration-reference/stack:1745`, which is reference-only. `sandbox/` tracks
  a stale `yarn.lock` that npm dirties — run the checkout from inside
  `sandbox/` so the pathspec is `yarn.lock`.
- **Depends on:** T25
- [ ] done
- Notes:

---

## Discovered Issues Log

(appended by executors during implementation)

### DI1 (wave 1, orchestrator) — the live-Postgres suite leaked a connection pool per test

`agentdb.Store` had **no `Close` method at all**, and `Open` builds a pool.
Production opens one Store and keeps it for the process's life, so nothing ever
needed to hand a pool back — but a test binary opens one per test, and each held
its idle connections until the binary exited. The `agentdb` package alone drifted
past Postgres's default `max_connections` of 100 and then failed at CONNECT with
`FATAL: sorry, too many clients already (SQLSTATE 53300)`.

Two properties made it read as a flake rather than a bug: the failure landed on
whichever test opened next (`TestApplyTopology_LivePG`, `TestWorkersLivePG_SchemaDefaults`
— neither of which leaks anything), and re-running that test alone always passed.
It was **pre-existing**, not caused by T2/T3: reproduced against a freshly
restarted database with 6 connections, running `./agentdb/...` on its own.
T2's and T3's new live-PG tests are simply what pushed it over the line.

Fixed (orchestrator, outside any ticket's scope, because it blocks every ticket's
validation gate):
- `go/agentdb/store.go` — new `Store.Close()`, documenting why it never existed
  and the `NewStore` caveat (there the caller owns the `*gorm.DB`).
- `t.Cleanup(func() { _ = store.Close() })` at 20 test sites across `agentdb`,
  `httpapi` and `cmd/agentd`, registered immediately after the open so it runs
  LAST (`t.Cleanup` is LIFO) and the data cleanups below it still have a pool.
- `go/agentdb/pool_leak_test.go` — two guards. `TestStoreCloseReleasesThePool`
  opens 8 stores, warms them, closes them and watches the server's own
  `pg_stat_activity` count return to baseline (polled: the server lags a client
  disconnect by milliseconds; the probe's pool is pinned to one connection so it
  cannot drift the count itself). `TestEveryTestThatOpensAStoreClosesIt` scans
  the module's 18 test files that open a Store and fails any that never closes
  one, naming the fix. `agentdb/store_test.go` is exempt with its reason stated:
  it asserts `Open` FAILS on a bad DSN, so no pool exists.

Also noted, not fixed (pre-existing, untouched by this session):
`go/cmd/agentd/timearg_test.go` is not `gofmt`-clean.

### DI2 (T1 probe) — `PUT /agent/workers/{name}` silently wipes any omitted field

`PutWorker` (`go/httpapi/workers.go:111-116`) builds a fresh `agentdb.NewWorker`
and assigns `Description`, `SystemPrompt`, `MCPConfig`, `Image` and **`Briefing`**
unconditionally from the request body. A PUT carrying only `{"system_prompt": …}`
therefore **erases the worker's briefing** — the mechanism T2 exists to deliver.
No error, nothing logged, the field is simply gone. Found live: installing the
probe prompt wiped the architect's `name=label-registry` briefing in both probe
projects.

Defensible as PUT semantics, and T19 already carries the identical warning one
level up for `PUT /agent/project-settings`. The worker-level twin is not
documented anywhere, and the console's worker editor is where it will bite.
**Wants a ticket** (not opened here, to avoid expanding scope mid-plan): either
round-trip every field in the console editor, or make the route a merge.

### DI3 (T1 probe) — a self-designed archivist does not write T4's label

Both probe architects independently invented `kind=summary` **plus a
`worker=<name>` label** for their archivists — convergent with T4 in spirit, but
`RollingSummarySelector` reads `kind=rolling-summary`, so the default briefing
section still matches nothing. T4 changed the shipped `archivistPrompt`; it
cannot reach an archivist the architect authors itself, which A1 makes the
normal case. Worth reconsidering whether the selector should follow the
convention models reach for rather than the reverse.

### DI4 (T1 probe) — the attention channel may reach nobody, and a worker noticed

A probe archivist wrote `kind=lesson`: *"request_human_attention in this project
may silently reach nobody."* Correct for a local stack with no attention webhook
configured, so a config artifact rather than an engine bug — recorded because C3
makes that channel the entire notification path for every architect change.
