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

Agent Bob's product thesis is a self-revising organisation: an **architect**
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
> **Half of this was superseded by T25, as T25 was always meant to.** The alarm
> shipped disabled *through the build* so half-finished code could not run on a
> clock; `charter.Resolve` now renders it `Enabled: true`, and an approved
> charter starts the loop. The second half stands unchanged and is the load-
> bearing half: **"run now" must be an event and never a chat**, for the reason
> given below.
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
     204 (no body) — no charter deposited yet. Was a 404 until 2026-09-13; the
         onboarding screen polls this, and every 404 was logged by the browser.

POST /agent/charter/apply   {session, memory_id, rationale?}
     404 "no charter has been proposed yet — …" (unchanged)
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

### T5: `orgprompts` leaf package + the three prompts   [Status: done | Model: opus]
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
- [x] done
- Notes: `go/orgprompts/{prompts.go,interviewer.md,architect.md,registry.md}`
  plus `prompts_test.go` (6 tests). `architect.md` is T1's
  `architect-prompt.md` verbatim with the run README's three revisions folded
  in — elapsed time in STEP 3, the per-worker `worker.finished` filter in
  STEP 4, and the instrument check — plus a fourth paragraph for DI3 telling
  the architect to have any summary-writer it creates also write
  `{kind: "rolling-summary", worker: <subject>}`. STEP 0 and STEP 5 are
  untouched, as the README asks.
  **One correction the probe text needed:** it said to fix a briefing with
  "worker_update, passing briefing". `worker_update`'s schema
  (`mcp_management.go:542-562`) has no top-level `briefing` — it takes `name`
  and `fields`, and `briefing` lives inside `fields`. The probe's architect
  got this right anyway; the prompt was wrong and would have failed T6's
  argument assertion. Every tool named in either prompt is now named with its
  real argument names.
  The data-not-instructions rule gained a sentence saying it holds in a chat
  too, because a chat session gets no core preamble (B-scope note) — the rule
  has to live in the prompt or it is absent there.
  `registry.md` is the *frame* (how `name=`, `retracts=` and the automatic
  `kind=rolling-summary` section work, plus the starting vocabulary), not the
  whole note: T8 appends the charter's `label_rules` to it.
  `interviewer.md`'s worked example is fenced as ```` ```charter ````; a test
  pins that there is exactly one such fence, so T6 can extract it
  unambiguously. Verified out-of-band that the example parses through
  `charter.Parse` and returns zero `charter.Validate` issues — T6 makes that
  a permanent assertion.

### T6: the prompt/tool-schema assertions   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `go/cmd/agentd/orgprompts_test.go` (5 tests), worked after T10 as the
  ticket's own warning instructs. The extractor takes any
  `lowercase_with_underscores` token from a prompt line as a tool mention and
  any parenthesised identifier list right after it as that tool's arguments,
  then checks both against the list `main.go` registers.
  **It found the bug it was written for on its first run** — and one more.
  (a) `actor_worker`: named in prose in `architect.md` next to
  `config_history`. Real argument, not a tool; added to the explicit
  `promptSkipWords` list, which is deliberately a list and not a pattern so a
  genuine typo cannot hide in it.
  (b) `interviewer.md` named NO arguments in the parenthesised form, so its
  half of the check was silently vacuous. Caught by the `checked == 0` guard
  rather than by reading the output. Fixed in the prompt, which now writes
  `ask_user (question, options, context)`, `charter_validate (charter)` and
  `memory_create (content, labels)` — clearer for the model too.
  Nested properties are **not** flattened: `briefing` must not resolve as a
  top-level argument of `worker_update`, since flattening would have hidden
  DI5 exactly. `TestPromptExtractorCatchesABadArgument` feeds the live
  prompt's actual wrong line through the extractor and asserts it is caught,
  so a broken extractor and a clean prompt cannot look alike.
  The `ui` carve-out is `sandboxBuiltins`, a hand-written map of `ask_user`'s
  four arguments taken from `sandbox/src/tools/builtin/ask_user.ts`, with a
  comment saying that Go cannot read a Zod schema in TypeScript and that this
  is therefore a carve-out written down rather than an omission.
  The `mcp__<server>__<tool>` mapping is stated in the file header and the
  extractor strips the prefix. `coreToolsForPromptAssertions` mirrors
  `main.go:601-615` by hand — it cannot import a `func main()` — so
  `TestPromptToolListMirrorsTheServer` pins a floor on the tool count and
  spot-checks one tool per group.

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

### T8: `charter.Resolve`   [Status: done | Model: opus]
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
- [x] done
- Notes: `go/charter/resolve.go` + `resolve_test.go` (8 tests). Two constants
  are exported because three other places need the same strings:
  `RegistrySelector = "name=label-registry"` and `ArchitectRunInput`.
  Three choices worth recording:
  (a) the architect gets `Briefing: ["name=label-registry"]` of its **own**,
  duplicating the project-wide briefing. `BuildBriefingSections` de-duplicates
  by selector, so it costs nothing, and it means clearing the project-wide
  list later cannot silently strip the architect of the rulebook.
  (b) the settings patch carries goal AND measure as prose, not just the goal.
  A worker reads its prompt unconditionally and a memory only when handed one;
  the architect judges itself against the measure every run.
  (c) the registry seed is `orgprompts.LabelRegistry()` (the frame: `name=`,
  `retracts=`, the automatic `kind=rolling-summary` section, the starting
  vocabulary) followed by the charter's `label_rules`. T8's scope said
  "content from `LabelRules`"; `LabelRules` alone would have shipped a
  registry that never explains retraction or the one automatic briefing
  section, which is engine behaviour and not the charter's to state.
  `Resolve` re-checks `architect_name` and `architect_cron` even though
  `Validate` covers them — it is reachable from the MCP tool with text a model
  just wrote, and the alternative is a store error mid-transaction.

### T9: `onboarding@v1` topology   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `go/topology/onboarding.go` + `onboarding_test.go` (4 tests). Exports
  `topology.OnboardingWorker = "interviewer"`, because the name is needed in
  three packages (the topology, the session that sets `persona`, and
  `POST /agent/charter/apply` which disables it under A8) and a string literal
  in each is how they drift apart. `Questions: nil` — the create-project form
  already collects name and goal, and the interview's whole job is to ask the
  rest. No subscription and no schedule: the interview is a chat (A7), so
  nothing wakes this worker as a job. `topology.List()` now returns 16;
  nothing anywhere asserts a count, checked. The prompt is asserted
  byte-identical to `orgprompts.Interviewer()`.

### T10: MCP `charter_validate`   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `go/cmd/agentd/mcp_charter.go` + `mcp_charter_test.go` (7 tests), and
  one `mcpSrv.register(newCharterTools().tools()...)` line in `main.go` inside
  the `if agentDB != nil` guard. `charterTools` is an **empty struct** — no
  store, no runner, no seam of any kind. That is how "must not write" is
  asserted: the acceptance criterion asked for an unchanged config-event count,
  but a row count only proves that *one call* did not write, whereas a type
  with no fields cannot write at all. The test states that reasoning.
  A malformed or invalid charter comes back as `{valid:false, errors:[...]}`,
  never as a tool error — the model is asking "is this right?", and a
  transport-level error reads as "the tool is broken" and invites it to stop
  calling. `Resolve` is run on a valid charter for its refusals only: a charter
  that validates but cannot resolve must not be reported valid, or the human
  presses Approve on something that fails.
  The bare-object form goes through `charter.Parse` too, with a synthetic
  summary line prepended and discarded, so there is exactly one decoder and one
  unknown-field rule.
  Reachability is tested over the real JSON-RPC path with a session row that
  has **no worker identity**, because that is the only kind of session that
  will ever call it (A7).

### T11: charter HTTP routes   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `go/httpapi/charter.go` + `charter_test.go` (10 tests), two lines in
  `httpapi.go`'s `Endpoints`.
  Three things the ticket did not specify, decided here:
  (a) **the session id is validated before it is spliced into a selector.** A
  comma is the selector language's AND, so an unchecked id could widen the read
  past the deposit it named. `agentdb.ValidateLabelValue`, the same guard
  `/agent/memories/current` applies (`memories.go:387`).
  (b) **a named `memory_id` is checked against the selector, not fetched
  blind.** Without that the route would apply any memory in the project whose
  content happens to parse as a charter — including one a worker wrote itself.
  (c) **disabling the interviewer is outside the transaction, and its failure
  is silent.** It runs after the charter has already committed; reporting it
  would tell the operator the approval failed when it did not, and rolling the
  charter back because a cleanup write failed would be far worse than an
  untidy project.
  A deposit that does not parse is a 200 with `valid:false`, not a 500 — the
  console's job on that screen is to show the human why the interview has not
  produced something approvable yet.
  The acceptance criterion "creates the worker, schedule, subscription, the
  project briefing and both memory seeds in one transaction" is asserted at
  this layer as *one `TopologyApplication` carrying all of them crosses the
  seam*; the transaction itself is `agentdb/topology_apply_test.go`'s, which
  is where it can actually be observed.

### T12: the shared-verdict test   [Status: done | Model: sonnet]
- **Scope:** Assert `charter_validate` and the apply route reach the same
  verdict. A table of charters (valid and invalid) through both; they must
  agree on `valid` and on the issue set for every case.
- **Files:** create/extend `go/cmd/agentd/mcp_charter_test.go`.
- **Acceptance criteria:** at least six cases covering every invalidity class
  from T7; any divergence fails naming the case.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/... -count=1`
- **Depends on:** T11
- [x] done
- Notes: `TestCharterValidateAndApplyAgree` in
  `go/cmd/agentd/mcp_charter_test.go`, 11 cases — every invalidity class from
  T7 plus the two parse failures and an all-fields-empty case.
  It drives the **real** `httpapi.Handlers.ApplyCharter` (with the router
  tests' existing `stubRunner`/`newFakeRouterStore`), not a re-implementation:
  a re-implemented route would agree with itself by construction and prove
  nothing. Comparison is on `valid` and on the **set of issue paths**, not the
  messages — prose may legitimately differ in framing between a tool result
  and an HTTP body; which fields are wrong may not.
  A refactor came out of writing it: `charter.Effects` and `charter.Summarise`
  moved into `go/charter`, so the MCP tool and the HTTP route describe the same
  charter through one implementation rather than two. A5's rule, applied to the
  description as well as to the verdict.

### T13: `architect.run` reaches a briefed job   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `go/cmd/agentd/charter_dispatch_test.go` (3 tests), beside the router
  and dispatch tests. The worker, the subscription and the settings are built
  by `charter.Resolve` and overlaid with `agentdb.TopologySettingsOverlay` —
  hand-writing them would keep passing after `Resolve` stopped producing them.
  **The negative case had to change shape.** As written the ticket asks for "a
  project with no project briefing produces a job without it"; that fails,
  because `Resolve` sets the registry selector **twice** — project-wide and on
  the architect itself, so clearing the project list later cannot silently
  strip the architect of the rulebook. The negative case therefore removes
  both, and a third test pins the redundancy directly: with the project
  briefing gone, the architect's own briefing still delivers the registry.
  **Found a real bug doing it — see DI6.** `BuildBriefingSections` panicked on
  a `BriefingMemorySource` that returns `(nil, nil)` for a miss. Fixed in
  `go/compose.go`.

### T14: `web/src/charter.ts` + `useCharter`   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `web/src/charter.ts` (+ `charter.test.ts`, 30 cases) and
  `web/src/useCharter.ts`; exported from `index.ts` and from `pure.ts`, and
  `pure.test.ts` still passes, so the tier line holds with it in the graph.
  `valid` is `raw.valid === true`, pinned against `undefined`, `"true"`, `1`,
  `null`, `0`, `""`, `{}` and `[]`. `schedule_enabled` takes the same
  treatment: the schedule ships off, so "off" is both the safe reading and the
  true one, and nothing but a literal `true` may claim the loop is running.
  Two decisions beyond the ticket:
  (a) `coerceCharter` fills `architect_name` and `architect_cron` with the
  engine's defaults when the server omits them, because the panel has to show
  what will actually happen and a blank there reads as "nothing will run".
  (b) `describeCharterCadence` renders only the cron shapes onboarding
  produces (`m h * * *` and `m h * * <0-6>`) and otherwise hands the
  expression back untouched — a wrong plain-English reading of a cron is worse
  than the cron, because the human cannot tell that it is wrong.
  The hook polls (default 4s) because the charter arrives from OUTSIDE the
  browser and A6 gives it no end signal; it stops polling once applied. A 404
  is the empty state and additionally does **not** clear a charter already
  seen. `apply` sends the `memory_id` the human was looking at, not "the
  newest" — the interview may revise between the render and the click, and
  approving something nobody read is what the gate exists to prevent.

### T15: `CharterPanel`   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `web/src/components/CharterPanel.tsx` + test (9 cases), exported from
  `components/index.ts` and `index.ts`. Router-free and store-free: it takes a
  charter and an `onApprove`.
  The labelling rules render with `whiteSpace: 'pre-wrap'` and no truncation,
  and the test asserts every line of a four-line rule block survives — they
  are the thing being agreed, and a human who cannot read the last one has not
  agreed to it.
  One thing added beyond the ticket: the panel says in words that scheduled
  runs start switched off. Without it a human reads "daily at 09:00" on the
  chip and expects a daily loop that will not happen until someone enables it.
  A 422's issues supersede the read's, because a charter can be revised
  between the render and the click.
  The double-approve guard is tested with `fireEvent`, not `userEvent`:
  `userEvent` refuses to click a disabled control at all, so it would have
  asserted the test library's behaviour rather than the panel's.

### T16: `OnboardingPage` + "Run the architect now"   [Status: done | Model: opus]
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
- [x] done
- Notes: `web/src/components/OnboardingPage.tsx` (7 tests) and a new
  `RunArchitectControl.tsx` (5 tests), both exported.
  **`EmitEventControl` did not fit and was not reused.** Its words are about
  tracing a wire on the org chart — "Emit this event", "Emit a real event?",
  a paragraph about which envelope fields core stamps — and its confirmation
  quotes a count of matching subscriptions, a list the onboarding screen has
  no reason to load and a count that would be a guess if it did. Same shape,
  different sentence, so `RunArchitectControl` is its own component. It still
  confirms first, for the same reason: this spends real tokens.
  **The run control went into `WokenBy` (`WorkerTriggers.tsx`), not
  `WorkersPage.tsx`.** `WokenBy` is the one place that already knows how a
  worker is woken and already has the subscription list; putting it there
  meant no extra fetch and no worker-name heuristic — the control is offered
  on the strength of an enabled `architect.run` subscription, so a project
  that named its architect something else still gets it.
  The rail is bound with `sessionId` explicitly and a test asserts the stub
  never receives `undefined`, which is what falling through to
  `AgentChatProvider`'s current session would look like.

### T17: app-shell wiring   [Status: done | Model: opus]
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
- [x] done
- Notes: `examples/web/src/onboarding.ts` (the four-call sequence and a hook
  around it), plus `App.tsx`, `ProjectPicker.tsx` and `Sidebar.tsx`.
  `Sidebar`'s `window.prompt` is gone: a new project needs two fields, and a
  prompt box cannot ask for two, cannot mark one required, cannot show the
  server's refusal and cannot say what the goal is FOR. It is a dialog with a
  form. The same required goal field is on `ProjectPicker`, labelled "Your
  goal — the interview starts from this".
  `buildOnboardingSeed` lives in `web/src/charter.ts`, not in the shell, so it
  is testable: the shell has no test rig. Three tests pin that the seed
  carries the session id, carries the goal verbatim below a rule, and
  attributes everything below the rule to the user — that attribution is the
  §6.2.4 boundary, without which a goal reading "ignore your instructions"
  arrives as though the system had said it.
  Two things added beyond the ticket:
  (a) **the pending onboarding is persisted to `localStorage`.** Without it a
  reload mid-interview drops the human on the Desk with a live interview they
  can no longer see — and re-entry is the path the ticket's own "reuses it
  rather than 409-ing" requirement describes, so without persistence that
  requirement had no way of being exercised at all.
  (b) **`enableInterviewer` round-trips every field** through
  `PUT /agent/workers/{name}`. That route is a whole-object replace, so a PUT
  of `{enabled: true}` alone would erase the interviewer's system prompt —
  DI2 exactly. This is the client-side half; T27 is the server-side half.
  `onboarding` is a shell-owned transient view widening `ViewNav`'s `view`
  prop, not a `NavEntry`: it highlights nothing, which is right for a thing
  you are doing rather than a place you go back to.

### T18: repair the browser e2e fixtures   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `openFreshProject` no longer goes through the create form at all. It
  mints the project token through `POST /auth/project-token` — the same route
  the shell itself uses for a wildcard grant — writes it into the shell's own
  `localStorage` state and reloads. That IS the fast path the ticket asks
  about, and it was not optional: creating a project through the UI now starts
  an interview, so thirteen specs that only wanted a namespace would each have
  provisioned a container and taken a host port, and `port-pool.stack.spec.ts`
  runs against a deliberately narrowed pool.
  **The consequence the ticket did not anticipate: `console.stack.spec.ts`
  needed no changes at all.** The fast path leaves the project genuinely empty
  — no interviewer, no session — so "This project has no workers yet" and the
  four-views assertion are still true, for the same reason they were before.
  `openOnboardingProject` is the slow path beside it, used only by T20, with a
  doc comment saying it provisions a container on purpose.
  **Two things the first full suite run found that the grep did not.**
  (a) `e2e/stack.spec.ts` — the root smoke spec, not under `features/` — drives
  the picker's create form directly rather than through the helper, so it was
  invisible to a search for `openFreshProject`. It sat clicking a Create button
  that stays disabled until the goal is filled. Fixing it exposed a second
  problem: creating a project now starts an interview, and that spec's teardown
  deleted sessions with PROJECT_A's token only — a delete for another project's
  session is a 404, so the interview's container leaked silently. The teardown
  now tries every project's token, including one minted for the project the
  test created.
  (b) The goal box could not be filled by test id at all. It is a multiline
  `TextField`, and MUI renders a second hidden textarea beside the visible one
  to measure auto-resize; the id lands on something that is not the control a
  human types into, so `fill` succeeds and the form stays empty. Both call
  sites now select it by its accessible label, which is the convention
  `e2e/README.md` already states for library components.
  (c) A third failure in that file was **pre-existing and impossible** — see
  DI8.

### T19: project briefing in the console   [Status: done | Model: sonnet]
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
- [x] done
- Notes: a `ProjectBriefing` section on `ProjectSettingsPage`, plus
  `validateProjectBriefing` in `projectSettings.ts` (6 pure tests, 6 page
  tests).
  The client-side selector check **borrows the engine's own grammar**:
  `parseMemorySelector` (written for the memory browser) is already a mirror of
  `agentdb/labels.go`, and the server applies exactly that parser to each
  entry. A second, stricter rule here would refuse selectors the server
  accepts, which is worse than not checking at all — the human would have no
  way to find out they were wrong.
  A blank row is an error rather than a silent drop: a row that vanishes when
  you save it reads as a bug.
  The wipe hazard the ticket warns about was **already closed** by T2 —
  `projectSettingsBody` spreads the whole draft and `coerceProjectSettings`
  fills `briefing`, so an unrelated save round-trips it. There is now a test
  saying so explicitly, because that is exactly the kind of correctness that
  gets refactored away by accident.

### T20: onboarding e2e (mock mode)   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `e2e/mock-scripts/onboarding.json` + `e2e/features/onboarding.stack.spec.ts`,
  gated on `STACK_MOCK_SCRIPT`, **passing** with 0/100 session ports in use
  afterwards.
  **The spec had to be restructured twice, and the reason is worth recording.**
  The charter is found by `name=<the interview's session id>`, and the mock
  model cannot write a label decided at run time — `modelproxy`'s script is
  fixed JSON with no templating. Pinning the session id instead does not work
  either: session ids are globally unique and rows are **soft-deleted**, so a
  pinned id is permanently taken after the first run. (Both were tried; the
  second failed with a primary-key collision on `agent_sessions_pkey`.)
  The shape that works splits the two halves and proves each properly. THE
  TOOL PATH is proved by the scripted interviewer, in a real container, calling
  `charter_validate` and then `memory_create` — the script only reaches the
  second call if the first returned, so its deposit existing proves both
  crossed the MCP boundary. THE SCREEN is proved against a charter deposited
  under the real session id through `POST /agent/memories`. What is
  consequently NOT proved is that a model reads its session id out of the seed
  and labels the deposit with it — that is discovery, which doc 25 §5 says mock
  mode never proves, and T1 observed it live.

---

*Revert phase. The architect's daily schedule stays disabled until T25.*

### T21: config-event addressability   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `ConfigEventQuery.Seq` and `Store.GetConfigEvent` in `agentdb`;
  `entity` and `seq` query parameters plus `GET /agent/config-events/{id}` in
  `httpapi`; `seq` on the MCP `configHistoryRecord`; `seq` on the web
  `ConfigEvent` and its coercer.
  Two decisions:
  (a) **a record in another project is NOT FOUND, and reads identically to one
  that does not exist.** A distinguishable refusal would let a caller learn
  that an id is real. The test asserts the two response bodies are byte-equal.
  (b) **the changelog sorts by `seq` only when every record has one**, falling
  back to the clock otherwise. `seq` is commit order and is total, so two
  changes inside one millisecond sort arbitrarily by the clock — and this list
  is what the diffs are computed against, so an arbitrary order there produces
  a diff against the wrong neighbour. The fallback is not tidiness: an older
  agentd sends no `seq` at all, and sorting a whole page to 0 would be worse
  than the clock.

### T22: `RevertEvent`   [Status: done | Model: opus]
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
- [x] done
- Notes: `go/agentdb/config_revert.go` + `config_revert_test.go` (9 tests, all
  on sqlite so they run on every `go test`).
  **"Already reverted" is refused, not a no-op, and it falls out of D2 rather
  than being coded for**: the first revert appends a new record for the entity,
  so the original is no longer newest and a second attempt hits the
  newest-only rule. The way back is to revert the REVERT, which *is* newest —
  tested, and it works.
  The refusal NAMES the intervening records (`action (seq N)`), because "not
  the newest" without saying what is in the way is a dead end for whoever is
  reading it.
  Beyond the ticket: **`image_create` and `skill_create` are refused too**,
  with their own sentence. Both are append-only at the tool surface, so there
  is no delete verb to compensate a create with — the ticket only named
  `topology_apply`, but the same argument applies and silently doing something
  approximate would be worse.
  `decodePayload` goes through JSON so the struct tags decide, exactly as they
  did on the way in; a payload that will not decode is an error, never a
  partial row, because reverting to half a worker would look like success.

### T23: revert HTTP route   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `POST /agent/config-events/{id}/revert` on the `ConfigLogStore` seam,
  5 tests. The store's refusal is passed through **verbatim** as the 409 body:
  it names the records standing in the way, and a paraphrase would drop exactly
  the part that tells the human what to do next.
  The reason is optional on the wire — the store fills in
  `revert of <action> (seq N, event <id>)` — because an empty rationale in a
  changelog is worse than a mechanical one, and refusing a revert over a
  missing JSON body would be pedantry.
  One test-design note worth keeping: an unrouted path is ALSO a 404, so the
  "another project's record is a 404" case asserts the store was *reached*.
  Without that it passed while the route did not exist at all — which is how it
  was written the first time, and the mistake it caught was mine.

### T24: revert in the changelog UI   [Status: done | Model: sonnet]
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
- [x] done
- Notes: a `RevertControl` on every changelog entry plus `revertBlocks` in
  `configLog.ts`; 9 tests in `web/src/components/ChangelogRevert.test.tsx`.
  The word "undo" is asserted absent from the whole rendered document, dialog
  included — including out of the *blocked* reasons, where the first draft had
  smuggled it back in ("would undo that change too") and the test caught it.
  `revertBlocks` mirrors the store's two rules client-side (newest-only, and
  the kinds with no inverse) so the button is not offered for something the
  server will refuse — a control that fails after the human has decided to
  click it is worse than one that was never offered. It is computed from the
  loaded page, so it can be wrong at a page boundary; the server checks again
  and its refusal renders verbatim. The doc comment says exactly that.
  The confirmation names the thing that will change and says **nothing is
  erased**, because "are you sure?" is not a confirmation when the reader has
  been scrolling a list of similar entries.
  The success notice lives on the VIEW, not the card: a successful revert
  reloads the list, which remounts every card, so a notice held inside one
  vanished at exactly the moment it was earned. Caught by the test.

### T25: switch the architect on + document   [Status: done | Model: sonnet]
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
- [x] done
- Notes: `charter.Resolve` now renders the schedule `Enabled: true`, and four
  places that said the opposite were corrected with it: T8's test, the
  `charter_validate` summary test, `CharterPanel`'s chip and caption, and the
  T20 e2e.
  The panel's copy changed shape rather than polarity. It now says the
  architect "runs on this schedule from now on", that it "makes changes without
  asking", and that every change can be reverted from the changelog — because
  the moment a human approves is the only moment they are certainly reading,
  and a chip saying `every day at 09:00` does not tell anyone that a loop is
  about to start editing their project.
  Docs: **`docs/18-workers-memory-events.md` §9a** (new) carries onboarding,
  the charter, the standing loop, the memory contract, C6 **unhedged as a block
  quote**, the no-migration note for projects onboarded before this, and the
  six known limits. `docs/product/03-memory.md` gains the paragraph explaining
  how the label-registry convention actually reaches a worker — and that it is
  an ordinary, unprotected memory. `CLAUDE.md` gains the workstream paragraph
  and repeats C6's sentence, because that file is what an agent reads first.

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
- [x] done
- Notes: All five steps observed on 2026-09-09 against the mock stack.

  **Steps 1 and the whole approve path** are `e2e/features/onboarding.stack.spec.ts`,
  run with its mock script and green in 11.6s — but only after fixing a poll
  that had never waited (DI15 below). It proves the interview runs in a real
  container, deposits through the core tools, the panel shows the rules whole
  and says the loop starts, and approving yields: the architect and no roster;
  the interviewer disabled with its prompt intact; ONE enabled daily schedule;
  the `architect.run` subscription; `project_settings.briefing` =
  `["name=label-registry"]`; the project prompt carrying the goal; both memory
  seeds.

  **Step 2** by hand over HTTP (`scratchpad/walk.py`): deposit → validate →
  apply → post an `architect.run` EVENT → read the woken session's
  `composed_prompt`. 13,384 bytes, and it carries the registry as a real
  briefing section, not merely the selector name:

  ```
  --- Your memory briefing: name=label-registry [efaaaf3e] ---
  The label conventions for this project. Read this before writing a memory.
  …
  [… briefing section truncated at 2048 bytes]
  ```

  Two things a reader should know. The job IS a session — `composed_prompt`
  lives on the session row and there is no `/agent/jobs` route; the console
  derives jobs from `/agent/deliveries`, which is what names the session id.
  And a finished mock job's session does not linger, so the read has to be
  prompt: poll deliveries fast and fetch immediately, or you find the delivery
  and a 404 behind it.

  **Steps 3 and 4** by hand (`scratchpad/revert.py`). A non-newest revert is
  refused with 409 and this body, which is the readable reason the ticket asks
  for:

  > agentdb: revert refused: worker_update (seq 2) is not the newest change to
  > worker:answerer — worker_update (seq 3) came after it. Payloads are whole
  > rows, so putting this one back would erase that one too. Revert the newest
  > change first

  Reverting the newest one returns 200, the worker reads `v2` again, and the
  history goes from 3 records to 4 — a forward compensating write, nothing
  erased. The same run also demonstrated T27 in passing: two prompt-only PUTs
  left `description` intact.

  **Step 5** `./e2e/run-stack-e2e.sh clean`, then `docker ps -a --filter
  name=sandbox-` inside DinD: **0 containers**, 0/100 ports in use.
  (`./stack sessions` targets the ORDINARY compose stack, which is not the one
  these tests run against — it answers "postgres is not running". The e2e
  runner's own accounting is the right instrument here.)

  **Gates.** `go build ./... && go vet ./... && go test ./...` green;
  `./stack test-go` green with live Postgres (agentdb ran 226s, so the
  pgvector/jsonb paths actually executed rather than skipping); web 1500 tests,
  typecheck and build green; `verify-package.sh` green after being unblocked
  from an upstream npm crash; sandbox 180 tests green; `examples/web`
  typechecks and builds. Browser suite: **73 passed, 0 failed**, run in two
  batches — this machine cannot finish it in one process (DI16).

### T27: `PUT /agent/workers/{name}` must stop wiping omitted fields   [Status: pending | Model: sonnet]
> Opened out of the original 26 by the orchestrator, from **DI2**, found live
> during T1. It is not a prerequisite for anything in T5–T26 and is deliberately
> **not** folded into another ticket's scope; work it when the charter chain is
> done, or sooner if the console's worker editor is touched first.
- **Scope:** `PutWorker` (`go/httpapi/workers.go:111-116`) builds a fresh
  `agentdb.NewWorker` and then assigns `Description`, `SystemPrompt`,
  `MCPConfig`, `Image` and `Briefing` **unconditionally** from the request
  body. Any of those five that the body omits is written as its zero value.
  Installing the probe prompt with `{"system_prompt": …}` during T1 therefore
  erased the architect's briefing — silently, with a 200 and a read-back echo
  that looked correct because it echoed what had just been stored.
  **The defect is inconsistency inside this one handler, not PUT purity.** The
  other three writable fields — `MaxInstances`, `Enabled`, `Frozen` — are
  already `*T` in `workerBody` and are already keep-on-absent (`:116-124`). So
  the route today wipes five fields and preserves three, with nothing saying
  which is which.
  **Chosen fix:** make the five pointers too, matching the three that already
  behave this way. Absent (or JSON `null`) keeps the stored value; an explicit
  `""`, `{}` or `[]` clears it. On create there is no stored value, so absent
  is the zero value and nothing changes. This keeps one rule for the whole
  body and leaves clearing possible, which a plain merge does not.
  Rejected: (a) leaving the route alone and only round-tripping every field in
  the console's worker editor — it fixes the console and leaves every API
  embedder holding the same loaded gun, and `docs/19-embedding.md` points third
  parties at exactly this route; (b) a blanket merge with no way to clear —
  `Briefing` then becomes append-only through HTTP.
  Also: **round-trip every field in the console's worker editor anyway.** A
  client that sends the whole row is correct under either semantics, and it is
  the surface an operator actually uses.
  Out of scope: `PUT /agent/project-settings`, whose identical hazard is
  already documented (T19's warning) and whose fix is a separate decision — the
  `project_settings.go:190-205` comment names three pinned consumers.
- **Files:** modify `go/httpapi/workers.go` (`workerBody` + `PutWorker`),
  `go/httpapi/workers_test.go`; audit `web/src/components/` for the worker
  editor's PUT body and any other caller of the route.
- **Acceptance criteria:** a PUT omitting `briefing` on a worker that has one
  leaves it intact — asserted by reading the row back from the store, not from
  the response echo; a PUT sending `"briefing": []` clears it; the same pair of
  assertions for `system_prompt` and `mcp_config`; creating a worker through
  PUT is unchanged; `docs/18-workers-memory-events.md` states the rule in one
  sentence.
- **TDD:** yes — write the omitted-`briefing` test first and watch it fail
  against today's handler.
- **Validation:** `cd go && go build ./... && go test ./httpapi/... -count=1`
  and, for the console half, `cd web && npm test`
- **Depends on:** nothing
- [x] done
- Notes: Written test-first — the omitted-`briefing` case failed against the
  old handler on all five fields before anything was written.

  The five are keep-on-absent now: the handler seeds them from the stored row,
  so absent or `null` keeps and an explicit `""`/`{}`/`[]` clears. A store
  error that is not "no such row" fails the request rather than falling
  through to the defaults, because falling through is the same wipe arriving
  intermittently. Only the three strings needed pointers — `encoding/json`
  already distinguishes absent/`null` (nil) from `[]`/`{}` (non-nil empty) for
  a slice or map, which IS the distinction, and a `*agentdb.SelectorList`
  would buy nothing.

  **The ticket's premise was wrong** and the result is two rules, not one:
  `MaxInstances`/`Enabled`/`Frozen` are NOT keep-on-absent and never were —
  `TestWorkersHTTP_FreezeAndUnfreezeRoundTrip` pins the opposite in as many
  words. Unifying them is a decision about what an omitted `frozen` should
  mean for a human-only safety flag, which is not an executor's to take. The
  handler and `docs/18` now state both rules instead of implying one. **DI11.**

  Acceptance criteria all met, asserted against the store and never the echo.
  Client audit: `workerBody`, `OrgChartPage` and `examples/web`'s
  `enableInterviewer` all already send whole rows, correct under either rule.
  One needed changing, and getting it wrong caused a live regression the stack
  caught — **DI14**, worth reading.

  Also confirmed in the T26 walkthrough: two prompt-only PUTs left
  `description` intact.

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
**Now ticketed as T27** (opened 2026-09-09 by the orchestrator, after the
charter chain was clear of it). The chosen fix is neither of the two options
first sketched here: the five wiped fields become pointers, matching
`MaxInstances`, `Enabled` and `Frozen`, which are already keep-on-absent in the
same handler. The real defect is that one handler has two rules and says so
nowhere.

### DI3 (T1 probe) — a self-designed archivist does not write T4's label

Both probe architects independently invented `kind=summary` **plus a
`worker=<name>` label** for their archivists — convergent with T4 in spirit, but
`RollingSummarySelector` reads `kind=rolling-summary`, so the default briefing
section still matches nothing. T4 changed the shipped `archivistPrompt`; it
cannot reach an archivist the architect authors itself, which A1 makes the
normal case. Worth reconsidering whether the selector should follow the
convention models reach for rather than the reverse.

**Addressed in T5, from the other end.** `orgprompts/architect.md` now tells the
architect that any summary-writing worker it creates must ALSO write
`{kind: "rolling-summary", worker: <subject>}`, and says why: that exact label
is the one briefing section delivered automatically, so a summary under any
other convention only reaches its subject if that subject happens to search for
it — which, for a fresh conversation with a blank memory, means never.
`orgprompts/registry.md` repeats it as a mechanic, under "two labels that are
not yours to design". This instructs rather than enforces; changing
`RollingSummarySelector` itself remains open and is not scheduled.

### DI4 (T1 probe) — the attention channel may reach nobody, and a worker noticed

A probe archivist wrote `kind=lesson`: *"request_human_attention in this project
may silently reach nobody."* Correct for a local stack with no attention webhook
configured, so a config artifact rather than an engine bug — recorded because C3
makes that channel the entire notification path for every architect change.

### DI5 (T5) — the probe's architect prompt named an argument `worker_update` does not have

`architect-prompt.md`, the text that actually ran in T1, says to correct a
briefing with *"worker_update, passing briefing"*. `worker_update`'s input
schema (`go/cmd/agentd/mcp_management.go:542-562`) has exactly three top-level
arguments — `name`, `fields`, `rationale` — and `briefing` lives inside
`fields`, alongside `description`, `image`, `max_instances` and `enabled`, with
`additionalProperties: false`.

The probe's architect got it right anyway, which is why the run passed and the
error survived into the record. That is the failure mode T6 exists for: a prompt
that names a tool argument the tool does not accept looks fine in review, passes
every Go test, and costs a model one wasted call and one error message at
runtime — or, with a less capable model, a loop. Corrected in
`go/orgprompts/architect.md` (T5); the probe file is left as-is, because it is
the record of what ran.

Two smaller ones fixed in the same pass, for the same reason: `config_history`
is now shown taking `entity` as a named argument rather than as prose, and
`schedule_create` is named with `worker`, `cron` and `input` — its `worker`
versus `target_session` exclusivity is enforced in the handler, not the schema,
so a prompt that named both would validate and then fail.

### DI6 (T13) — `BuildBriefingSections` panicked on a briefing source that returns no row and no error

`go/compose.go:266-283`: the miss branch lived entirely inside `if err != nil`,
so a `BriefingMemorySource` whose `NewestMemory` returns `(nil, nil)` fell
through to `strings.TrimSpace(mem.Content)` and **panicked the dispatcher**
mid-compose.

`*agentdb.Store` returns `ErrMemoryNotFound`, so production never hit it, and
the one existing test using a map-backed fake (`fakeBriefingSource` in
`router_test.go`) happened to have every selector match. T13's negative case —
a worker whose briefing selects something deliberately absent — is the first
thing that ever asked for a miss through that seam.

`BriefingMemorySource` is **exported**, and its method documentation promises
neither an error nor a nil on a miss. A host implementing it the obvious way
would take down its own dispatcher.

Fixed: the miss branch now covers `err != nil || mem == nil` and logs the same
RD19 "thinner prompt" line either way. One line of behaviour change; a panic
in the dispatch path is a considerably worse answer than a thinner prompt.

### DI7 (T20) — a session id can never be reused, because deletes are soft and ids are global

Two properties of `agent_sessions`, neither documented together anywhere, and
together they make a pinned session id unusable in a re-runnable test:

- `id` is the **primary key of one global table** — it is not scoped by
  project, so two projects cannot both hold a session called `onboard-1`;
- `DELETE /agent/session/{id}` is a **soft delete**: it stamps `deleted_at` and
  leaves the row, so the key stays taken forever.

The visible symptom was a browser test failing on
`duplicate key value violates unique constraint "agent_sessions_pkey"` for an
id whose session had been deleted, in a project that no longer had any
sessions, minutes earlier. `cleanup()` had worked exactly as designed.

Not a bug — soft delete is the right call for a table whose rows are the
provenance of everything else, and a global id space is what makes a permalink
work. Recorded because the combination is invisible from either half, and the
consequence ("ids are consumed permanently; never pin one") is the sort of
thing that costs an afternoon each time it is rediscovered. It is also why
T20's mock script cannot label a charter with a session id at all — see that
ticket's notes.

### DI8 (T18) — `console.stack.spec.ts` had never been run, and had FIVE defects

The file's own header says it: **UNRUN AT AUTHORING TIME (2026-08-14)** —
*"It typechecks; that is all that is currently proved."* This is the first time
anybody has executed it. Five separate things in it could not have passed.

**1. A reveal announcement that can never appear.** The test posted an event and
expected the nav-reveal notice to name **Activity**. Activity is revealed by the
project's first EVENT — and hiring a worker produces one, because every
configuration mutation emits `config.changed` as a project event (§15.8). So the
first `PUT /agent/workers/{name}` is also the first thing that ever happened in
the project, Activity had been sticky for two reloads, `appeared` was empty and
the notice was not drawn at all. Verified against the running stack: a fresh
project goes 0 → 1 events on that call, with
`type: "config.changed", text: "A human hired worker \"probe-one\"."`.

**2 and 3. Two field labels that do not exist.** `ScheduleEditor`'s fields are
**Instruction** and **Rationale**; the test asked for `/what should .* do|input/i`
("Instruction" does not contain "input") and `/^Why\??$/`.

**4. A button name that does not exist.** For a new schedule the button reads
**"Create schedule"**; the test asked for `/save/i`.

**5. A required field never filled, and a save never waited for.** The editor
does not prefill the worker even when opened from that worker's own Triggers
tab, so `validateSchedule` kept the save button disabled forever. And the final
assertion read `api.listSchedules()` in the same tick as the click, before the
round trip could have completed — it could only ever have passed by luck.

All five fixed here, each with a comment saying what was wrong, because T18's
acceptance criterion is that the stack suite passes and because the next reader
should not have to re-derive any of it. The wider lesson is the one the header
already stated and nobody acted on: **a test that has never been run is not a
test.** Nothing here was caused by this plan.

### DI9 (T18) — a trigger editor opened from a worker does not prefill that worker

`WorkerTriggers` renders "New schedule" / "New subscription" on a tab whose
entire premise is *triggers are edited on the worker* — and then hands
`ScheduleEditor` / `SubscriptionEditor` a null draft, keeping `workerName` to
itself. The editor asks which worker the trigger is for, and
`validateSchedule` refuses to enable Save until you answer.

Both editors behave the same way, so it is a consistent design choice rather
than a slip — possibly deliberate, so a trigger can be retargeted without
leaving the page. But it undercuts the tab's own premise, and it reads as the
software not paying attention to where you are standing.

**Fixed** (Kai approved it in-session, after this was recorded for a decision).

Both editors take an optional `defaultWorker`, used as the seed's `worker` when
creating and ignored when editing a stored row — so retargeting an existing
trigger is untouched, which was the one thing that might have made the old
behaviour deliberate. `WorkerTriggers` passes `workerName` to both.

The regression test is `web/src/components/WorkerTriggers.test.tsx`, and it
aims at the SEAM rather than at the new prop: the bug was a passing-through
failure, so a test that only checked `defaultWorker` on the editors would have
passed against the broken wiring. It asserts the name reaches the field, that a
schedule saves with the Worker input never touched, and — the guard on the fix
— that a stored row's own worker is not overwritten. Nothing rendered this
component before, which is why the gap survived every existing test.

`e2e/features/console.stack.spec.ts` typed the worker in by hand with a comment
explaining why it had to; that line is now the assertion that it no longer has
to, and the comment says so rather than disappearing.

### DI10 (T18) — the browser suite runs three workers with no retries, and three of its tests are time-boxed

`playwright.stack.config.ts` sets `workers: 3` (overridable with
`STACK_E2E_WORKERS`) and `retries: 0`. Each worker drives its own browser and
its own session containers inside one DinD, on one machine.

Three tests carry hard time limits that this hardware cannot always meet under
that load, and they failed in different combinations on three consecutive full
runs while passing individually every time:

- `stack.spec.ts` — waits 30s for a replayed assistant message to render;
- `product-ui.stack.spec.ts` — waits 120s for a reply to settle;
- `port-pool.stack.spec.ts` — waits 45s for a session deleted mid-create to
  disappear, on the strength of an orphan once "measured arriving ~14s after
  the delete".

Serial (`STACK_E2E_WORKERS=1`) the suite went from 57 to 60 passing and the
first two of those stopped failing.

⚠️ **The engine is not at fault, and this was checked rather than assumed.**
Reproduced the third case by hand against the running stack on an idle machine:
create a session, `DELETE` it a second later while it is still `creating`, wait
50s — the row is gone and `docker ps` inside DinD shows no `sandbox-<id>`
container. The cancellation path (`b34c366`) works; the test's 45 seconds is
simply not always enough here.

**Not changed here.** Turning the default down would slow every run including
CI, on the evidence of one WSL2 laptop that was also running Go suites at the
time. Recorded so the next person does not spend an afternoon on it, and so
that a red run on this machine is read correctly before anything is "fixed".

### DI11 (T27) — the worker PUT now has TWO absent-field rules, and the split is not a decision anyone made

T27's ticket says the fix is to make `Description`, `SystemPrompt`, `MCPConfig`,
`Image` and `Briefing` keep-on-absent, "matching `MaxInstances`/`Enabled`/
`Frozen` which are already keep-on-absent in the same handler".

**Those three are not keep-on-absent.** They are pointers so that a meaningful
zero (`0`, `false`) can be told apart from absence, but an absent one still
writes this route's *default* — `1`, `true`, `false` — not the stored value.
`TestWorkersHTTP_FreezeAndUnfreezeRoundTrip` pins it in as many words:

> And an omitted field means false, per this route's replace semantics.
> `PUT is create-or-replace: an omitted frozen must read as false`

So the ticket's premise was wrong, and its stated goal — "one rule for the whole
body" — is not what T27 as specified produces. What shipped is T27's
**acceptance criteria** exactly: five fields keep on absent, three still replace.
The handler and `docs/18-workers-memory-events.md` both now say which is which
instead of implying a single rule.

**The same hazard remains on `frozen`, and it is arguably the worse one.** A
partial PUT still silently thaws a frozen worker, and freeze is the flag
reserved for humans — `docs/product/10-topology-library.md` §3 is the whole
boundary. `max_instances` silently resets to 1 the same way.

**Not changed here, deliberately.** Making the three keep-on-absent contradicts
a test that states its reasoning, and what an omitted `frozen` should mean is a
product decision about the safety flag, not a bug fix — widening T27 to take it
unilaterally is not the executor's call. Every caller in this repo already sends
the whole row (`web/src/workers.ts`'s `workerBody`, `OrgChartPage`'s
read-modify-write, `examples/web`'s `enableInterviewer`), which is correct under
either rule, so nothing here is exposed today. An outside embedder following
`docs/19-embedding.md` is.

**One caller did need changing.** `workerBody` sent `briefing: string[] | null`,
and the console's editor sets the draft to `null` when the last selector is
removed. Under the new rule `null` means KEEP, so "remove every selector, save"
would have become a silent no-op. It now sends `[]`, which is the explicit clear.
That is the general shape of the hazard in this change: a client that used null
to mean "nothing" now says "don't touch". *(Superseded by DI14: `workerBody` now
sends the draft verbatim, and the editor keeps `[]` when the last selector is
removed, so null and `[]` stay distinct end to end.)*

**Resolved 2026-09-10, before the merge to `main`, on Kai's decision.** All eight
fields are keep-on-absent now, so the route has one rule: create-or-keep.
Omitted on create still means the default. The deciding facts, checked in the
code before changing anything: the HTTP PUT was the ONLY writer that reset
anything (the agents' `worker_update` reads and patches; the git importer
field-merges, citing DI2), and all three reset directions were the unsafe ones —
a silent thaw, a silent re-enable that resumes spending, and a silent drop to
one instance. `TestWorkersHTTP_FreezeAndUnfreezeRoundTrip`'s last assertion
pinned the rule rather than a safety property (its real concern — that an
explicit unfreeze lands — still holds), so it was inverted with its reason.
Written test-first: `TestWorkersHTTP_PutKeepsOmittedControlFields` failed on all
three fields against the T27 handler before the fix.

`examples/web`'s `enableInterviewer` now sends only `{enabled: true}`. Its old
whole-row body had carried `briefing: worker.briefing ?? []` — DI14's hazard,
turning "no briefing" into "clear it" — which the simplification also removes.

### DI12 (T26) — the console's progressive nav never re-counts, so a tab cannot appear until a reload

Found by the sixth defect in `console.stack.spec.ts` — the same never-run file
as DI8. `applying actor-critic@v1 draws the chart…` applies the topology through
the UI, waits for the apply's own done step, then clicks `nav-chart` and times
out after the full 300s. The page snapshot shows the nav holding only Desk,
Chat, Workers and Settings: not just Chart missing, but Memory and Activity too,
on a project that by then has two workers, two subscriptions and a handful of
`config.changed` events.

`revealedNav`'s rule is satisfied (`chart: subscriptions > 0 || workers >= 2`).
The counts feeding it are not. `useNavReveal` reads them from `useWorkers`,
`useMemories`, `useSubscriptions` and `useEventsOverview`, and **none of those
four polls** — each is a render-phase ref-guarded fetch that runs once. They are
also separate instances from the ones the Workers page holds, so a mutation made
on a page cannot reach the nav's copy. The nav's counts are therefore frozen at
whatever they were when the shell mounted, and every reveal waits for a reload.

That contradicts the design this machinery exists for: `appeared`,
`acknowledge` and `navRevealSentence` are all there so the shell can *name a
newly revealed entry in the confirmation of the action that caused it*
(`docs/product/28-console-ia-design.md` §3.2). A reveal that only lands on the
next page load has no action left to be announced beside.

**Fixed** (Kai approved it in-session). `useNavReveal` now re-counts on a timer
— `NAV_REVEAL_POLL_MS`, 10s, overridable per call and switchable off with
`pollMs: 0` — and **stops for good** once every conditional entry has been
revealed, which the new pure `everythingRevealed(sticky)` decides. Reveals are
sticky and one-way, so once all three are out there is nothing further to watch
for and polling four list endpoints forever would be spending requests on a
settled question.

Two things worth knowing if this is touched again:

- The four `reload` callbacks are held in a **ref**, not in the effect's
  dependency array. They are `useCallback`s whose own dependencies change as
  their data changes, so depending on them directly rebuilds the interval on
  render after render — and an interval rebuilt more often than its period
  never fires at all, which is the original bug wearing a different hat.
- The tests deliberately do **not** install fake timers. The thing under test is
  the timer; a test with its own proves only that an interval was requested, not
  that it ever runs. They use a 20ms period against real time instead.

`console.stack.spec.ts` needed no change for this — its `gotoView(page,
'chart')` is a Playwright auto-waiting click, so a reveal that arrives one poll
later still satisfies it.

### DI13 (T26) — `console.stack.spec.ts` is now at EIGHT defects, and each fix uncovers the next

DI8 recorded five, found by reading. DI12 was the sixth. Fixing DI12 let the
file get further than it ever had, and the seventh and eighth appeared
immediately:

- **Seventh** — `config-and-workers.stack.spec.ts`'s worker CRUD test asserted
  that a briefing was *gone* after a PUT that omitted it, commenting that this
  was PUT's replace semantics. It was describing DI2, not a decision. T27
  inverted it. The three tests after it in the same serial describe had been
  skipped ever since and had therefore never run either; they pass.
- **Eighth** — `console.stack.spec.ts`'s freeze-from-the-chart test creates ONE
  worker and then waits for the Chart tab, which is revealed by
  `subscriptions > 0 || workers >= 2`. It could never have passed. It never ran
  to find out: it sits behind the topology test, which failed first on DI12, so
  every previous attempt reported it "did not run".

**The pattern is the finding.** `test.describe.configure({ mode: 'serial' })`
means the first failure in a file skips every test after it, so a never-run
file cannot be assessed by running it once — you learn about exactly one defect
per run, and only after fixing the previous one. Three full-suite runs said "2
failed" and each time the two were different. Anyone auditing a suite for
whether it has ever actually executed should count "did not run" as a much
louder signal than a failure.

### DI14 (T27) — fixing a silent no-op created a silent mislabelling, and the config log caught it

Recorded because the fix is a nice illustration of what the config log is for.

T27 made an absent briefing keep the stored value. The console's editor
collapsed its draft to null when the last selector was removed, so under the
new rule "remove every selector, save" would have done nothing at all. The
obvious client-side patch — always send `[]` — fixed that and broke something
else: freezing a worker from the chart began recording `worker_update` instead
of `worker_freeze`, because §15.3 picks the narrow action only when every other
field is byte-identical, and null → `[]` is a change.

Both failure modes are silent to the operator. The second is the worse one: a
change log that records the wrong reason is less use than one that records
none, and this system's entire safety story for the architect is *revert is the
control* — which is only as good as the log's account of what happened.

The fix keeps the distinction where the human makes it: emptying the list
leaves `[]` ("clear it"), a row that never had one stays null ("leave it
alone"), and every layer passes it through untouched. No coercion in either
direction, at any layer.

**Neither `npm test` nor the Go suite could have caught it** — the coerced body
was internally consistent and every unit test agreed with it. It took the live
stack, asserting on which action the config log chose. That is worth
remembering the next time a browser suite looks like an expensive way to
re-test what unit tests already cover.

### DI15 (T26) — a poll whose success condition is "not the one bad value"

`onboarding.stack.spec.ts`'s `waitForOnboardSession` polled
`/agent/sessions/by-name/onboard`, returned the string `'absent'` when the row
was not there yet, and asserted `.not.toBe('creating')`. `'absent'` is not
`'creating'`, so the poll was satisfied by its own first request — sent before
the shell had even asked for the session. It fell through to a null guard and
failed the entire spec in **3.2 seconds** with the message `no onboard
session`, which reads like the engine refusing to start an interview.

The shape is worth naming because it is invisible in review: a poll that
excludes ONE bad value accepts every other one, including the "nothing is here
yet" value that is the whole reason for polling. Excluding both states makes
the spec pass in 11.6s.

This one had also never run — it is gated behind `--mock-script`, so it sits
out every ordinary suite run. Gated specs need running deliberately; nothing
else will notice them.

### DI16 (T26) — this machine cannot finish the browser suite in one process

Two consecutive full runs were killed by the OS for memory, at roughly the
same point. Not a test failure and nothing partial to show for the first one,
because the command piped through `tail` and printed only at the end.

Run to a log file instead, and split: seven spec files, then the remaining
nine. **73 passed, 0 failed**, no other change. The suite is fine; the box is
the constraint — 23 GB shared with Docker, a 1 GB language server, several
agent sessions, and a second developer's e2e runs landing in the same stack
(`e2e-gitproj-*` projects appeared in agentd's log mid-run).

Together with DI10 that is three separate occasions in one day where load,
not code, produced a red result. Anyone reading a red run on this hardware
should establish which kind it is before changing anything: the cheap test is
to re-run the failing file alone.
