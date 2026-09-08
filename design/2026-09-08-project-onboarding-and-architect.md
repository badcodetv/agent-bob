# Project Onboarding & the Architect — Design & Implementation Plan

> **EXECUTION RULES (for agents):** Work ONE ticket at a time, in order unless
> dependencies say otherwise. Only the orchestrator changes ticket Status;
> workers may only append to Notes and the Discovered Issues Log. A ticket's
> checkbox is checked only after its Validation commands have been re-run by
> the orchestrator and pass. Do not expand scope; log surprises in the
> Discovered Issues Log instead.

Status: **SUPERSEDED** by `design/2026-09-08-memory-coordinated-organisation.md`.
Do not execute this plan. It is kept as the record of an approach that was
replaced: it designed a whole starting org chart in the onboarding interview,
and gave each worker a single long-lived conversation. Both were abandoned —
see the successor's Context for why (no cross-turn compaction exists; and the
architect, not the interview, owns the roster).

Revision: rev2 — rewritten 2026-09-08 after an adversarial review found three
blockers and eleven majors in rev1. Every correction is recorded inline as
**[rev2]**; do not "restore" a rev1 claim you find quoted elsewhere.

Relates: `docs/product/17-product-spec.md` §8.8 (first real use case),
`docs/product/10-topology-library.md` (the topology catalogue),
`design/2026-08-20-agent-wolf.md` R260/R261/R268/R269/R270 (the interview lessons),
`docs/product/16-work-plan-operator-console.md:228` (the "never say undo" rule).

---

## Context

### What already exists (do not rebuild it)

The topology catalogue is **reachable today**. `WorkersPage.tsx:22` imports
`TopologyOnboarding` and renders it at `:197` behind a sentinel selection, with
a "Start from a topology" button on the empty-project panel at `:213`;
`DeskPage`'s first-run panel routes there via `onStartFromTopology`
(`examples/web/src/App.tsx:339`), and `WorkersPage` is mounted at `App.tsx:349`.
Fifteen shapes (`go/topology/`, `solo` through `triage-lab`) are pickable, with
a diff, a collision refusal and a changelog receipt.

**[rev2]** rev1 claimed the catalogue was unreachable and made that the
justification for the whole plan. It was wrong. Any executor who reads a
"surface the hidden catalogue" framing elsewhere should ignore it.

`architect-archivist@v1` goes further than expected: its **first question is
already "What is this project for?"** (`go/topology/architectarchivist.go:100-105`)
and it renders an architect worker that designs and builds the rest of the org
using its own management tools. The idea in this plan is not new to the
codebase; what is missing is the path a human actually walks.

### What is actually missing

1. **Goal capture.** A project is a kebab-case string minted by
   `POST /auth/project-token` (`go/cmd/agentd/googleauth.go:531`). It has no
   row, no description, no goal. Nothing in the product knows why a project
   exists. The one shape that asks is one of fifteen, and only if you find it.
2. **An interview.** Picking a shape means understanding fifteen names and
   answering questions cold. A first-time user cannot do this. What works —
   proven in Agent Wolf — is a conversation: you say what you want, an agent
   asks questions, and a structured proposal comes out the far end.
3. **A durable goal the org can be steered by.** The architect's standing
   question should be *"given the goal and what has happened, should the org
   change?"* — and there is nowhere today that holds "the goal".
4. **Reversibility.** Nothing in the system can be put back. The changelog is
   read-only by construction (`go/httpapi/config_events.go:12-14`).

### The framing that governs every screen

*Onboarding is not about getting the org chart right.* It is about getting
going quickly. The first structure is expected to be wrong. Correctness is the
**architect's** job — a standing worker that wakes daily, reads the goal, reads
what has happened, and changes the org itself. That loop is the product;
onboarding gives it a starting position.

Two consequences:

- The approval gate is a **comprehension** gate, not a correctness gate. It
  exists so a first-time user sees what is about to be created.
- The architect must act **freely**. A manager that files a daily approval
  request is a suggestion box, ignored within a week. Free action is made safe
  by **revert**, not by gatekeeping: every *row* mutation records the full new
  state of what it changed (`go/agentdb/config_events.go:247-250`) and
  deletions preserve the deleted row (`go/agentdb/workers.go:415-449`), so
  "put it back" is reconstructible from data that already exists.
  **[rev2]** rev1 said "every change"; two actions are not row states —
  `project_prompt_write` (fixed by T19) and `topology_apply`, whose payload is
  a decision and which is revertable only as part of its batch (T21).

### Prior art reused rather than rebuilt

| Existing thing | Where | Reused for |
| --- | --- | --- |
| `topology.Bundle` | `go/topology/topology.go:95-102` | the shape of every proposal after resolution |
| `Topology.Instantiate` | `go/topology/topology.go:132` | rendering a named shape from answers |
| `Store.ApplyTopology` | `go/agentdb/topology_apply.go:178` | the one atomic apply transaction |
| `previewTopology` | `go/httpapi/topologies.go:112` | the diff — **after** the T6 refactor |
| `TopologyOnboarding` | `web/src/components/TopologyOnboarding.tsx` | the browse-and-pick half of the screen |
| `layoutOrgChart` | `web/src/orgchart.ts:287` | drawing the result |
| `ChangelogView` | `web/src/components/ChangelogView.tsx` | where revert actions live |
| `ask_user` | `sandbox/src/tools/builtin/ask_user.ts` | the interview's question cards |

---

## Architecture

### The flow

```
NEW PROJECT   name: acme-marketing
              goal: "grow inbound leads"     <- REQUIRED; stored as NO field
                     |
                     +--> POST /agent/topologies/apply  onboarding@v1
                     |        -> creates worker "interviewer"
                     |
                     +--> POST /agent/session {worker:"interviewer", name:"onboard"}
                     |        -> 202 {status:"creating"}
                     +--> POLL GET /agent/sessions/by-name/onboard until != creating
                     +--> POST /agent/session/{id}/message
                              the seed: session id + goal, verbatim, below a rule
                                    |
                                    v
 +----------------------- ONBOARDING SCREEN --------------------------+
 | +----------------------+   +-----------------------------------+  |
 | | 15 shape cards       |   | interview chat rail               |  |
 | | browse / pick direct |   | "I'd suggest supervisor, because" |  |
 | | INTERACTIVE AT ONCE  |   | ask_user question cards           |  |
 | +----------------------+   +-----------------------------------+  |
 | +----------------------------------------------------------------+|
 | | PROPOSAL PANEL - appears only when a VALID proposal exists      ||
 | |  +3 workers  +2 wires  +1 clock  +goal        [ Approve ]       ||
 | +----------------------------------------------------------------+|
 +--------------------------------------------------------------------+
```

**[rev2]** `POST /agent/session` has **no message field** (body:
`go/httpapi/session.go:19-45`) and returns `status: "creating"` while
provisioning in a background goroutine (`:148-166`). The seed is a second call
after polling. Wolf hit this exact wall
(`AW/web/src/pages/NewHypothesis.tsx:16-24`).

**[rev2]** The session is named `onboard`, not `onboard-<project>`: names are
already project-scoped and `maxSessionNameLen` is 64
(`go/agentdb/sessions.go:167`), which a long project id would breach. Names are
also unique per project, so re-onboarding reuses the existing session rather
than creating a second one (T15).

### The emit, and the gate

```
INSIDE THE CONTAINER                     ON THE SERVER
 interviewer agent
   | mcp__core__topology_list ---------> the 15 shapes + their questions
   | mcp__core__orgchart_validate ----->  orgproposal.Validate + Resolve
   |     (read-only; CANNOT deposit)       -> {valid, errors[], preview}
   | mcp__core__memory_create ---------> append-only memory
   |     kind=org-proposal, name=<session id from the SEED MESSAGE>
   v
 CONSOLE
   GET  /agent/org-proposals/current?session=<id>
   POST /agent/org-proposals/apply {session, memory_id}
```

**Decision A1 — the agent proposes, a human approves.** Nothing the interview
says becomes real on its own. Rejected: letting it call `worker_create` /
`subscription_create` / `schedule_create` directly (it has those tools).
Rejected because a first-time user needs one moment where they see what is
about to exist.

**Decision A2 — the validator shares the apply path's code, exactly.**
`orgchart_validate` and `POST /agent/org-proposals/apply` both call
`orgproposal.Validate` then `orgproposal.Resolve` then `previewBundle`. This is
Wolf's R269 lesson (`design/2026-08-20-agent-wolf.md:6710`): a 27-rule schema
given to a model as prose produced **thirteen** errors on its first real
attempt; the fix was a validator the model calls before depositing, sharing the
gate's code path so "valid here" provably means "valid there". T10 pins it.

**Decision A3 — the validator cannot deposit.** Read-only. The deposit goes
through `memory_create` so it carries container provenance and is untrusted by
construction (`AW/api/src/mcp/specvalidate.ts:47-51`).

**Decision A4 — no end signal.** No "finish" tool, no terminal state, no
transcript parsing. The session stays alive and becomes the project's chat. The
agent re-deposits the **complete** proposal every time; newest wins; there is
no partial update.

**Decision A5 — the proposal is a union; the agent picks the register.**

1. **Shape only** — `{topology, answers}`. The default reach.
2. **Shape plus tailored words** — plus `workers[]` overriding rendered
   workers' prompts by name.
3. **Free-form** — no `topology`; rows given outright.

Resolution order is fixed: instantiate (if named) -> overlay `workers` by name
-> append subscriptions and schedules -> inject the architect if absent ->
attach `SettingsPatch` -> attach the goal `MemorySeed`.

**Decision A5b [rev2] — the proposal uses narrow DTOs, never `agentdb` rows.**
rev1 typed the proposal fields as `agentdb.Worker` / `Subscription` /
`Schedule`. Those are persistence structs carrying `id`, `project`,
`created_at`, `frozen`; `ApplyTopology` stamps `Project` (`topology_apply.go:260`)
but **not** `ID`, and `CreateSubscription`/`CreateSchedule` honour a supplied id
(`events.go:618`, `schedules.go:296`) — so a model-authored or
prompt-injected proposal could choose primary keys. It also contradicts A2:
R269's whole lesson is that the model must see an exact, minimal field table.
`orgproposal` therefore defines its own DTOs and maps them in `Resolve`.

**Decision A6 — the architect is always present.** `Resolve` injects a worker
(default name `architect`, prompt from the embedded architect prompt) plus a
daily schedule targeting it — **unless** the named topology is
`architect-archivist` at any version, or a worker of the target name already
exists in the resolved bundle.

**[rev2]** rev1 keyed this on "a worker named `architect`" and cited the
topology's own name. Wrong: the architect worker's name comes from a *question*
(`architectarchivist.go:107-113`) whose default is `"architect"` but which the
user may answer freely (`:236` sets `Name` from the answer). Answering "chief"
would have produced two architects. The topology-ref check is the structural
one; the name check is the belt.

**Decision A7 — the goal lives in two places, written by one function.**
The project settings prompt field (`ProjectSettings.SystemPrompt`) so every
worker and chat inherits it, **and** an append-only memory
`{kind: "project-goal", name: "project-goal"}` so every restatement is
preserved with its date and reasoning.

**[rev2]** Two corrections. (a) The memory's `name` is `project-goal`, not
`current`: `current` is a project-wide singleton key any worker could hijack.
(b) `memory_current`'s input schema is `{"name": string}` **only** — there is
no `kind` argument (`go/cmd/agentd/mcp_memory.go:261-266`). The architect
prompt must call `memory_current name=project-goal`. rev1 told the model to
pass a `kind`, which is R268's failure shape (an instruction referring to
something the model cannot reach).

**[rev2]** Also: describing `ProjectSettings.SystemPrompt` as "background, not
instructions" is a **product decision made in this plan**, not a fact about the
column. The code calls it "the project-level system prompt, prepended to every
worker's prompt" (`go/cmd/agentd/sessioncontext.go:187-189`) and
`SetProjectPrompt` rejects a blank value (`project_settings.go:211-213`). We
write goal-and-measure prose into it; we do not redefine the field.

**Decision A11 [rev2] — the `interviewer` worker stays.** `onboarding@v1`
creates it and nothing removes it. It becomes the worker behind the project's
ongoing chat, which is why the onboarding session is never torn down (A4). It
will appear in the Workers list and on the org chart; that is intended and the
docs say so (T24).

### Grouping and revert

```
config_events
  seq  batch_id      action                payload = FULL NEW ROW STATE
  101  apply:7f3e..  worker_create         {architect ...}
  102  apply:7f3e..  worker_create         {researcher ...}
  103  apply:7f3e..  schedule_create       {0 9 * * * -> architect}
  104  apply:7f3e..  topology_apply        {the decision — not a row}
  180  job:d91a..    worker_prompt_write   {researcher, new wording}
  181  job:d91a..    subscription_create   {...}
  195  (null)        worker_update         {a human edit in the console}

REVERT ONE   -> write that entity's PREVIOUS state forward, as a NEW event
REVERT A RUN -> the same for each member, in reverse seq order, one transaction
```

**Decision A8 — revert is a forward compensating write, never an erasure.**
`TestRestoreIsForward` (`go/agentdb/config_fold_test.go:516`) already fails any
implementation that rewrites history, and `go/agentdb/config_fold.go:30-67`
states there is deliberately no restore function.

**Decision A9 — a new nullable `batch_id` column**, stamped from a context
value. Rejected: enriching the `topology_apply` payload instead (no migration)
— rejected because it solves onboarding only and leaves the architect's daily
runs ungrouped, which is the more frequent and more important case.

| Source | `batch_id` | Set where |
| --- | --- | --- |
| an org-chart apply | `apply:<uuid>` | `ApplyTopology` |
| one worker job run | `job:<delivery_id>` | **`go/cmd/agentd/mcpserver.go`** |
| a human edit | `NULL` | nowhere — ungrouped by design |

**[rev2]** rev1 put the job stamping in `dispatch.go`. That process performs
**no config writes at all** — its nine store calls are claims, counts and
status updates. A job's config writes happen *inside the container*, arriving
as separate `POST /mcp` requests with their own contexts
(`go/cmd/agentd/mcpserver.go`). Stamping dispatch's context would have reached
none of them, silently defeating the entire justification for A9. The stamp
must be resolved per MCP call: session token -> session id -> the
`EventDelivery` carrying that `SessionID` (`go/agentdb/events.go:258`) ->
`WithBatch(ctx, "job:"+delivery.ID)`.

**Decision A10 — no MCP revert tool.** Revert is a human affordance; giving it
to the architect would let it undo the human who just corrected it.
`go/cmd/agentd/mcp_config_log.go:11-18` already states there is no config write
verb.

### Phasing

**Phase 1 (T1–T16)** ships onboarding. The architect worker is created and its
daily schedule is created **with `enabled: false`**.

**Phase 2 (T17–T25)** builds grouping and revert, then enables that schedule.

The architect never acts freely before a human can walk it back. This ordering
is load-bearing. Do not enable the schedule early.

---

## File Structure

### Create

| Path | Purpose |
| --- | --- |
| `go/orgprompts/prompts.go` | **leaf package**, imports nothing from this module; `//go:embed` accessors |
| `go/orgprompts/interviewer.md` | the interviewer's system prompt |
| `go/orgprompts/architect.md` | the architect's system prompt |
| `go/orgprompts/prompts_test.go` | tool-name and worked-example assertions |
| `go/orgproposal/proposal.go` | `Proposal` DTOs; `Parse` |
| `go/orgproposal/validate.go` | `Validate` -> `[]Issue` |
| `go/orgproposal/resolve.go` | `Resolve` -> `*topology.Bundle`; architect injection; goal writes |
| `go/orgproposal/*_test.go` | table tests |
| `go/topology/onboarding.go` | `onboarding@v1` — the 16th built-in |
| `go/cmd/agentd/mcp_orgchart.go` | `topology_list`, `orgchart_validate` |
| `go/httpapi/orgproposals.go` | `GET .../current`, `POST .../apply` |
| `go/agentdb/config_revert.go` | `RevertEvent`, `RevertBatch` |
| `web/src/orgProposal.ts` | pure types + coercers |
| `web/src/useOrgProposal.ts` | polling hook |
| `web/src/components/OrgProposalPanel.tsx` | diff + Approve |
| `web/src/components/OnboardingPage.tsx` | the one screen |
| `e2e/mock-scripts/onboarding.json` | the mock model's interview script |
| `e2e/features/onboarding.stack.spec.ts` | the stack e2e |

**[rev2]** `go/orgprompts` is a new leaf package because rev1's arrangement was
a guaranteed import cycle: `go/orgproposal` must import `go/topology` (its
signatures take `*topology.Registry` and return `*topology.Bundle`, and T2
reuses `topology.ResolveAnswers`), so `go/topology/onboarding.go` calling
`orgproposal.InterviewerPrompt()` closes the loop. rev1's alternative — "pass
the prompt in at registration" — is impossible: every topology registers from a
package-local `init()` (`go/topology/solo.go:35`) with no injection point.

### Modify

| Path | Change |
| --- | --- |
| `go/httpapi/topologies.go` | extract `previewBundle`; make `topologyPreview.Topology` omitempty |
| `go/agentdb/migrations.go` | add `046_config_event_batch` (highest existing is 045) |
| `go/agentdb/config_events.go` | `BatchID` field; `WithBatch`; stamping in `WithConfigEvent` |
| `go/agentdb/project_settings.go:218` | `project_prompt_write` payload becomes the full row |
| `go/agentdb/topology_apply.go` | stamp `apply:<uuid>`; return it |
| `go/cmd/agentd/mcpserver.go` | resolve and stamp `job:<delivery_id>` per call |
| `go/cmd/agentd/main.go` | one `mcpSrv.register(...)` line, inside the `DATABASE_URL` guard |
| `go/cmd/agentd/mcp_config_log.go:65` | add `seq` + `batch_id` to `configHistoryRecord` |
| `go/httpapi/httpapi.go` | endpoint constants + `Mux()` registration |
| `go/httpapi/config_events.go` | `entity` + `seq` filters; read-one; two revert routes |
| `web/src/configLog.ts` | carry `seq` and `batch_id` |
| `web/src/pure.ts` | export `orgProposal.js` so the tier check actually covers it |
| `web/src/components/ChangelogView.tsx` | revert actions |
| `web/src/index.ts`, `web/src/components/index.ts` | export the new modules |
| `examples/web/src/App.tsx` | `onboarding` as a **shell-owned transient view** |
| `examples/web/src/ProjectPicker.tsx`, `Sidebar.tsx` | required goal field |
| `docs/18-workers-memory-events.md`, `CLAUDE.md` | documentation |

**[rev2]** `web/src/navReveal.ts` is **not** modified. rev1 wanted an entry
shown "while the project has no workers"; navReveal is monotonic and sticky by
design (`:92-98`: *"Once an entry is in here it stays"*), has no hide
mechanism, and every rule is a "has something" predicate. Worse, onboarding's
own first act creates the `interviewer` worker, so a "no workers" rule would
hide the entry before the user arrived. And `App.tsx:254` silently redirects
any view not in `visible` to the Desk. Onboarding is therefore a transient view
the shell owns directly, like the session permalink — not a nav entry.

### Delete

Nothing.

---

## Interfaces

### `go/orgprompts` (leaf)

```go
package orgprompts
func Interviewer() string
func Architect() string
```

### `go/orgproposal`

```go
package orgproposal

// DTOs — deliberately narrow. NOT agentdb rows: no id, no project, no
// timestamps, no frozen flag. See Decision A5b.
type ProposalWorker struct {
    Name         string `json:"name"`
    Description  string `json:"description,omitempty"`
    SystemPrompt string `json:"system_prompt,omitempty"`
}
type ProposalSubscription struct {
    EventType string `json:"event_type"`
    Worker    string `json:"worker"`
}
type ProposalSchedule struct {
    Cron   string `json:"cron"`
    Worker string `json:"worker"`
    Input  string `json:"input,omitempty"`
}

type Proposal struct {
    Goal    string `json:"goal"`    // required
    Measure string `json:"measure"` // required — how the goal is judged

    Topology string         `json:"topology,omitempty"` // "supervisor@v1"
    Answers  map[string]any `json:"answers,omitempty"`  // required iff Topology set

    Workers       []ProposalWorker       `json:"workers,omitempty"`
    Subscriptions []ProposalSubscription `json:"subscriptions,omitempty"`
    Schedules     []ProposalSchedule     `json:"schedules,omitempty"`

    ArchitectName string `json:"architect_name,omitempty"` // default "architect"
    ArchitectCron string `json:"architect_cron,omitempty"` // default "0 9 * * *"

    ProjectBackground string `json:"project_background,omitempty"`
    Rationale         string `json:"rationale"` // required
}

type Issue struct {
    Path    string `json:"path"`    // "answers.cadence", "workers[1].name"
    Message string `json:"message"`
}

func Parse(content string) (p *Proposal, summary string, err error)
func Validate(p *Proposal, reg *topology.Registry) []Issue
func Resolve(p *Proposal, reg *topology.Registry) (*topology.Bundle, error)

const (
    MemoryKindProposal = "org-proposal"  // labels {kind, name: <session id>}
    MemoryKindGoal     = "project-goal"  // labels {kind, name: "project-goal"}
    DefaultArchitect   = "architect"
)
```

### `go/httpapi` — the extracted preview

```go
// previewBundle computes the diff and applicability for ANY bundle, however
// it was produced. previewTopology becomes a thin name-resolving wrapper.
func previewBundle(ctx context.Context, store TopologyStore, project string,
    b *topology.Bundle) (*topologyPreview, error)
```

**[rev2]** rev1 said T8 would "use the same helper the topologies route uses".
It cannot: `previewTopology` **starts from** `topology.Get(name, version)` and
404s when the name is not registered (`topologies.go:112-116`). Registers 2 and
3 of A5 have no registry entry and no `Instantiate` output to match. Without
this refactor two of the three proposal registers cannot be previewed at all.

### MCP tools (`go/cmd/agentd/mcp_orgchart.go`)

```
topology_list      {}                          -> {topologies:[{name,version,description,questions[]}]}
orgchart_validate  {proposal: string|object}   -> {valid, errors:[{path,message}], preview:{...}}
```

Both are Postgres-only: the registration block sits inside the `DATABASE_URL`
guard (`go/cmd/agentd/main.go:602-614`), like every other core tool.

### HTTP

```
GET  /agent/org-proposals/current?session=<id>
     200 {proposal, summary, memory_id, created_at, valid,
          errors:[{path,message}],
          preview:{diff, applicable, missing_images, missing_skills}}
     404 {"error":"no proposal has been deposited yet — the interview has to
                   deposit an org-proposal memory first"}

POST /agent/org-proposals/apply   {session, memory_id, rationale?}
     200 <agentdb.TopologyApplyResult> + {batch_id}
     409 the store's verbatim refusal string
     422 {errors:[{path,message}]}

GET  /agent/config-events/{id}
GET  /agent/config-events?entity=&seq=
POST /agent/config-events/{id}/revert     {rationale}
POST /agent/config-batches/{batch}/revert {rationale}
```

**[rev2]** There is no `preview.reasons[]`. rev1 invented it. `topologyPreview`
(`go/httpapi/topologies.go:88-96`) carries `MissingImages`, `MissingSkills`,
`Applicable` and nothing else; the refusal sentence is assembled inline in the
apply handler (`:250-263`) and never returned by preview. The panel derives its
prose from `colliding_workers` / `missing_images` / `missing_skills`, exactly as
`TopologyOnboarding` does.

### `go/agentdb`

```go
BatchID string `json:"batch_id,omitempty" gorm:"column:batch_id;type:varchar(64);index"`

func WithBatch(ctx context.Context, batchID string) context.Context
func (s *Store) RevertEvent(ctx context.Context, project, eventID string, cw ConfigWrite) ([]*ConfigEvent, error)
func (s *Store) RevertBatch(ctx context.Context, project, batchID string, cw ConfigWrite) ([]*ConfigEvent, error)
```

---

## Out of Scope

1. **Any change to how a chat session's system prompt is composed**
   (`go/cmd/agentd/sessioncontext.go:115-185`, `go/runner.go:2684-2714`).
   Agent Wolf's live hypothesis interviews create sessions exactly the way this
   plan does and run through that code.
2. **Adding the core preamble to chat sessions.** Same reason. The interviewer
   and architect prompts must name their own tools, because a chat session is
   never told what tools it has.
3. **Generalising `/agent/topologies/preview|apply`.** T6 extracts a shared
   helper; the routes stay as they are.
4. **Project-wide time-travel restore.** `FoldTo` (`config_fold.go:457`) is
   EXPERIMENTAL with three named divergences and no production callers.
5. **Reverting images or skills** — no delete verb exists for either
   (`config_events.go:812-815`).
6. **An MCP revert tool** (A10).
7. **Anything in `/home/kai/projects/badcode/agent-wolf`.**
8. **Seeding BadCode's real marketing manager** (spec §8.8) — a production act.
9. **A pre-chat recommendation from a separate model call.** The
   recommendation is the interview's first message.
10. **Any change to `web/src/navReveal.ts`** — see the Modify table.

---

## Tickets

### T1: `orgproposal` — DTOs and parser   [Status: pending | Model: sonnet]
- **Scope:** Create `go/orgproposal` with the DTOs from Interfaces, `Issue`, and
  `Parse(content) (*Proposal, summary string, err error)`. Line 1 is a human
  summary; everything after is one JSON object. Reject unknown keys at **every**
  level (top level and inside each row DTO), so `{"id": ...}` on a subscription
  is an error rather than silently ignored.
- **Files:** create `go/orgproposal/proposal.go`, `proposal_test.go`.
- **Acceptance criteria:** a well-formed deposit parses; an unknown key at any
  level errors naming its path; a row carrying `id`, `project`, `created_at` or
  `frozen` errors; a missing summary line errors; an empty body errors.
- **TDD:** yes
- **Validation:** `cd go && go test ./orgproposal/... -count=1 && go vet ./orgproposal/...`
- **Depends on:** —
- [ ] done
- Notes:

### T2: `orgproposal.Validate`   [Status: pending | Model: sonnet]
- **Scope:** Every rule, returning ALL issues at once. `goal`, `measure`,
  `rationale` non-empty; if `topology` is set it must resolve in the registry
  and `answers` must satisfy `topology.ResolveAnswers` (reuse it — do not
  reimplement question validation); if unset there must be at least one worker;
  worker names pass `agentdb.ValidateWorkerName` and are unique; every
  subscription and schedule names a worker present in the proposal or rendered
  by the named shape; cron strings are 5-field; `architect_name`, if given, is a
  valid worker name.
- **Files:** create `go/orgproposal/validate.go`, `validate_test.go`.
- **Acceptance criteria:** one table case per rule; a proposal breaking four
  rules returns four issues; `Path` values are addressable.
- **TDD:** yes
- **Validation:** `cd go && go test ./orgproposal/... -count=1`
- **Depends on:** T1
- [ ] done
- Notes:

### T3: `orgprompts` leaf package + the two prompts   [Status: pending | Model: opus]
- **Scope:** Create `go/orgprompts` importing **nothing** from this module, with
  `//go:embed` accessors `Interviewer()` and `Architect()`. This package exists
  to break the cycle described in File Structure; do not put the prompts in
  `orgproposal`.
  **The interviewer prompt must:** state that its job is a good starting shape
  quickly, not a perfect one; name `mcp__ui__ask_user` and require one question
  per turn; name `mcp__core__topology_list` and require reading the catalogue
  before recommending; name `mcp__core__orgchart_validate` and require calling
  it with the exact content about to be deposited, depositing only once valid;
  name `mcp__core__memory_create` and give the deposit contract (labels
  `{kind: "org-proposal", name: <the session id given in the first message>}`,
  line 1 summary, then JSON only, always the complete proposal); forbid
  claiming the org is live; include a worked example.
  **The architect prompt must:** state the standing question; require calling
  `memory_current` with **`name=project-goal`** (its schema has no `kind`
  argument) at the start of every run; name the tools it may use
  (`worker_create`, `worker_update`, `worker_prompt_write`,
  `subscription_create`, `subscription_delete`, `schedule_*`,
  `project_prompt_write`); require a rationale on every change; state that
  revising the goal means writing BOTH the project prompt and a new
  `project-goal` memory.
- **Files:** create `go/orgprompts/prompts.go`, `interviewer.md`, `architect.md`,
  `prompts_test.go`.
- **Acceptance criteria:** both accessors non-empty; a test asserts every tool
  named in either prompt exists in the registered tool list with that exact
  name **and that each named argument exists in that tool's input schema**
  (this is what would have caught the `memory_current kind=` error); a test
  asserts the worked example parses and validates — an example that does not
  validate teaches the model to fail and looks fine in review.
- **TDD:** no (prose), but the two assertions above are required tests.
- **Validation:** `cd go && go test ./orgprompts/... ./orgproposal/... -count=1`
- **Depends on:** T2
- [ ] done
- Notes:

### T4: `orgproposal.Resolve`   [Status: pending | Model: opus]
- **Scope:** Proposal -> `*topology.Bundle`, pure, mapping DTOs to `agentdb`
  rows and leaving `ID`/`Project`/timestamps zero for apply to stamp. Order:
  instantiate the named shape; overlay `Workers` by name (non-empty fields
  replace; a new name appends); append subscriptions and schedules; inject the
  architect **unless** the topology ref begins `architect-archivist@` or a
  worker named `ArchitectName` (default `architect`) already exists; create its
  schedule with **`Enabled: false`** (Phase 1); set `SettingsPatch.SystemPrompt`
  from `ProjectBackground`, defaulting to a rendering of goal + measure; append
  a `MemorySeeds` entry labelled `{kind: "project-goal", name: "project-goal"}`
  carrying goal, measure and rationale.
- **Files:** create `go/orgproposal/resolve.go`, `resolve_test.go`.
- **Acceptance criteria:** all three registers produce a valid bundle; the
  architect is injected exactly once; **`architect-archivist@v1` with the
  non-default answer `architect-name: "chief"` produces exactly one architect**
  (the case rev1 would have failed); the architect schedule is disabled; the
  goal seed and the settings patch both carry the goal; `Resolve` is
  deterministic (asserted twice in one test).
- **TDD:** yes
- **Validation:** `cd go && go test ./orgproposal/... -count=1 && go vet ./...`
- **Depends on:** T3
- [ ] done
- Notes:

### T5: `onboarding@v1` topology   [Status: pending | Model: sonnet]
- **Scope:** A 16th built-in in `go/topology/onboarding.go` following `solo.go`
  (`init()` + `Register(&Topology{...})` + a pure renderer). No questions; one
  worker, `interviewer`, with `orgprompts.Interviewer()`, enabled, no
  subscriptions, no schedules. Importing `go/orgprompts` is cycle-free by
  construction.
- **Files:** create `go/topology/onboarding.go`, `onboarding_test.go`.
- **Acceptance criteria:** appears in `topology.List()`; boot-time
  `validateTopology` passes; the rendered prompt is byte-identical to
  `orgprompts.Interviewer()`; `go build ./...` shows no cycle.
- **TDD:** yes
- **Validation:** `cd go && go build ./... && go test ./topology/... -count=1`
- **Depends on:** T3
- [ ] done
- Notes:

### T6: extract `previewBundle`   [Status: pending | Model: sonnet]
- **Scope:** Refactor `go/httpapi/topologies.go` so the diff/applicability
  computation takes a `*topology.Bundle` rather than a topology name.
  `previewTopology` becomes a thin wrapper that resolves name+version, calls
  `Instantiate`, then `previewBundle`. Make `topologyPreview.Topology`
  omitempty so a bundle with no registry entry can be previewed.
- **Files:** modify `go/httpapi/topologies.go`; extend `topologies_test.go`.
- **Acceptance criteria:** the existing topology preview/apply tests pass
  unchanged; a hand-built bundle with no registry entry previews correctly,
  including a worker-name collision; the wire shape for existing callers is
  byte-identical except for the now-omittable `topology` field.
- **TDD:** yes
- **Validation:** `cd go && go test ./httpapi/... -count=1 && cd ../web && npm test`
- **Depends on:** T4
- [ ] done
- Notes:

### T7: MCP tools `topology_list` + `orgchart_validate`   [Status: pending | Model: sonnet]
- **Scope:** One new file per the repo rule, plus one `mcpSrv.register(...)`
  line in `main.go` **inside the `DATABASE_URL` guard** (`main.go:602-614`) —
  these tools are Postgres-only like every other core tool. Follow
  `mcp_management.go` for shape. `orgchart_validate` accepts the proposal as a
  string (as deposited) or an object and returns `{valid, errors, preview}`; it
  must not write.
- **Files:** create `go/cmd/agentd/mcp_orgchart.go`; modify `main.go`.
- **Acceptance criteria:** both tools appear in the core tool list; a valid
  proposal returns `valid: true` with a populated diff; an invalid one returns
  every issue; reachable from an HTTP-created chat session; the config-event
  count is unchanged after a validate call.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/... -count=1 && go build ./...`
- **Depends on:** T6
- [ ] done
- Notes:

### T8: `GET /agent/org-proposals/current`   [Status: pending | Model: sonnet]
- **Scope:** Read the newest memory labelled `{kind: "org-proposal", name:
  <session>}` for the credential's project, `Parse`, `Validate`, `Resolve`,
  `previewBundle`, and return the documented body. 404 with the documented
  sentence when there is no deposit. 501 when the product store is absent.
- **Files:** create `go/httpapi/orgproposals.go`, `orgproposals_test.go`;
  modify `httpapi.go`.
- **Acceptance criteria:** no deposit -> 404 with that sentence; invalid deposit
  -> 200 with `valid: false` and issues; valid -> 200 with a populated diff; a
  session in another project -> 404, never an existence oracle.
- **TDD:** yes
- **Validation:** `cd go && go test ./httpapi/... -count=1`
- **Depends on:** T7
- [ ] done
- Notes:

### T9: `POST /agent/org-proposals/apply`   [Status: pending | Model: sonnet]
- **Scope:** Re-read the named memory (never trust a body-supplied proposal),
  re-validate, re-resolve, build an `agentdb.TopologyApplication` and call
  `Store.ApplyTopology`. `Topology` is the proposal's ref, or `"custom"` when
  free-form. 422 with issues when invalid; 409 with the store's verbatim
  refusal on collision or unmet precondition (reuse `writeTopologyApplyErr`,
  `topologies.go:305`).
- **Files:** modify `go/httpapi/orgproposals.go`, tests; `httpapi.go`.
- **Acceptance criteria:** a valid apply creates workers, subscriptions,
  schedules, the settings patch and the goal memory in one transaction (the
  `MemorySeeds` loop is `topology_apply.go:302-314`); the architect exists with
  a disabled schedule; a colliding name yields 409 and writes nothing; a body
  carrying a proposal object is ignored in favour of the stored memory.
- **TDD:** yes
- **Validation:** `./stack test-go` from the repo root (it runs the whole Go
  suite against a live Postgres; see T25's note — it appends `./...` itself, so
  do not pass package paths)
- **Depends on:** T8
- [ ] done
- Notes:

### T10: the shared-path test   [Status: pending | Model: sonnet]
- **Scope:** Assert the validator tool and the apply route reach the same
  verdict. A table of proposals (valid and invalid) is run through
  `orgchart_validate` and through the apply route's pre-flight; the two must
  agree on `valid` and on the issue set for every case. Without this ticket A2's
  guarantee is decorative.
- **Files:** create/extend `go/cmd/agentd/mcp_orgchart_test.go`.
- **Acceptance criteria:** at least eight cases covering every invalidity class
  from T2; any divergence fails naming the case.
- **TDD:** yes
- **Validation:** `cd go && go test ./cmd/agentd/... -count=1`
- **Depends on:** T9
- [ ] done
- Notes:

### T11: `web/src/orgProposal.ts`   [Status: pending | Model: sonnet]
- **Scope:** Pure types, coercers and helpers mirroring `web/src/topologies.ts`
  in style (no React, no fetch). `valid` coerces as `raw.valid === true` so an
  absent or garbled field reads as INVALID — the fail-safe posture of
  `coerceTopologyPreview` (`web/src/topologies.ts:198`). Add
  `export * from './orgProposal.js'` to `web/src/pure.ts` so the tier check
  actually walks this module.
- **Files:** create `web/src/orgProposal.ts`, `orgProposal.test.ts`; modify
  `web/src/index.ts`, `web/src/pure.ts`.
- **Acceptance criteria:** coercers fill every omitted field; `valid` is false
  for `undefined`, `"true"`, `1` and `null`; `pure.test.ts` passes with the new
  export present (it walks `pure.ts`'s graph only, which is why the export is
  required rather than optional).
- **TDD:** yes
- **Validation:** `cd web && npm ci && npm test && npm run typecheck`
- **Depends on:** T8
- [ ] done
- Notes:

### T12: `useOrgProposal` hook   [Status: pending | Model: sonnet]
- **Scope:** Polls `GET /agent/org-proposals/current` for a session; exposes
  `{state, loading, error, reload, apply, applying, applyError}`. Model on
  `web/src/useTopologies.ts` — imperative `apply`, server text surfaced
  verbatim, 409 refusals shown as written.
- **Files:** create `web/src/useOrgProposal.ts`; modify `web/src/index.ts`.
- **Acceptance criteria:** polling stops on unmount; a 404 is the empty state,
  not an error; `applyError` carries the server's own words.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck`
- **Depends on:** T11
- [ ] done
- Notes:

### T13: `OrgProposalPanel`   [Status: pending | Model: sonnet]
- **Scope:** Renders the summary, the rationale as a commit message, the diff
  (`+n workers`, `+n wires`, `+n clocks`, the goal), the workers' prompts in a
  collapsible so a human can read what they are approving, and one Approve
  button. Approve is disabled unless `valid && preview.applicable`, with the
  reasons rendered where the disabled button is — derived from
  `colliding_workers` / `missing_images` / `missing_skills`, as
  `TopologyOnboarding` does (`:464` is the disable rule; there is no shared
  `reasons` field). Router-free.
- **Files:** create `web/src/components/OrgProposalPanel.tsx`, test; modify
  `web/src/components/index.ts`.
- **Acceptance criteria:** invalid -> no Approve, issues in words; not
  applicable -> no Approve, reasons shown; valid -> Approve fires `apply`; a 409
  renders verbatim.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck`
- **Depends on:** T12
- [ ] done
- Notes:

### T14: `OnboardingPage`   [Status: pending | Model: opus]
- **Scope:** The one screen. Left: the catalogue, reusing `TopologyOnboarding`.
  Right: a chat rail showing the **named onboarding session specifically** —
  `AgentChat` takes all-optional props and otherwise falls back to
  `AgentChatProvider`'s *current* session (`AgentChat.tsx:81-129`), so this
  ticket must wire it to the resolved session id explicitly rather than
  rendering a bare `<AgentChat />`. Below: `OrgProposalPanel` once a proposal
  exists. The catalogue must be interactive **immediately** while the rail
  shows a named waiting state — Wolf's scar
  (`AW/web/src/pages/NewHypothesis.tsx:17-30`): session creation is slow by
  construction and a frozen-looking screen reads as a bug and gets clicked
  again. Name the cause ("starting a container, this can take a minute").
  Rail sizing per Wolf's measured lesson: `clamp(360px, 32vw, 620px)`, full
  height of a flex column, never a pixel height.
- **Files:** create `web/src/components/OnboardingPage.tsx`, test; modify
  `web/src/components/index.ts`.
- **Acceptance criteria:** the catalogue is usable before the session is ready;
  the waiting copy names the cause; the rail is bound to the named session, not
  the provider's current one; picking a card and approving a proposal go
  through **one shared handler** (asserted), so the two doors cannot diverge;
  the panel appears without a reload when a valid proposal lands.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck && npm run build`
- **Depends on:** T13
- [ ] done
- Notes:

### T15: app-shell wiring   [Status: pending | Model: opus]
- **Scope:** In `examples/web`: add a required goal field to project creation
  (`ProjectPicker.tsx`, and replace `Sidebar.tsx`'s `window.prompt` with a real
  form — a prompt box cannot carry two fields). Label it for what it DOES
  ("your goal — the interview starts from this"), never "optional": Wolf's
  most-confusing-field lesson. On create, in order: mint the token; apply
  `onboarding@v1`; `POST /agent/session {worker:"interviewer", name:"onboard"}`;
  **poll `GET /agent/sessions/by-name/onboard` until status leaves
  `creating`**; then `POST /agent/session/{id}/message` with the seed — the
  session id and the goal, the goal verbatim below a rule, with a line
  attributing everything below the rule to the user (the §6.2.4 provenance
  boundary, and the reason the prompt reads the id from the message rather than
  from `$SESSION_ID`). Surface any create failure **verbatim**, including "host
  port pool is exhausted". Add `onboarding` as a shell-owned transient view in
  `App.tsx`'s render switch — **not** a `navReveal` entry (see File Structure).
  Re-entering onboarding for a project that already has an `onboard` session
  reuses it rather than 409-ing on the taken name.
- **Files:** modify `examples/web/src/App.tsx`, `ProjectPicker.tsx`,
  `Sidebar.tsx`.
- **Acceptance criteria:** creating a project with a goal lands on onboarding;
  the goal and the session id are visible as the first message; a second visit
  reuses the session; `web/` is built before `examples/web` typechecks (it is a
  published package aliased to `../../web/dist` —
  `examples/web/tsconfig.json:23-25`).
- **TDD:** no (wiring)
- **Validation:** `(cd web && npm run build) && (cd examples/web && yarn && yarn typecheck)`
- **Depends on:** T14
- [ ] done
- Notes:

### T16: onboarding e2e (mock mode)   [Status: pending | Model: sonnet]
- **Scope:** One spec in the only rig (`e2e/features/` +
  `playwright.stack.config.ts`) plus the mock script that drives it. The stack's
  mock is `go/modelproxy` fed by `AGENTKIT_MOCK_MODEL_SCRIPT`
  (`go/cmd/agentd/modelproxy.go:107-108`, `docker-compose.yml:172-173`), and
  scripts live as JSON in `e2e/mock-scripts/` — follow
  `e2e/mock-scripts/architect-archivist.json` and `topologies.json`. The script
  must make the interviewer call `orgchart_validate` and then `memory_create`
  with a valid proposal. Because `--mock-script` is agentd-wide and read at
  boot (`e2e/run-stack-e2e.sh:138-150` reloads agentd), this spec runs in its
  own invocation and must not assume it can share a run with other specs.
  Flow: create a project with a goal, land on onboarding, assert the catalogue
  is interactive before the rail is ready, let the deposit land, assert the
  panel appears, approve, assert the workers exist and the architect's schedule
  is disabled.
- **Files:** create `e2e/mock-scripts/onboarding.json`,
  `e2e/features/onboarding.stack.spec.ts`.
- **Acceptance criteria:** green offline with no API key; deletes the sessions
  it creates (the host port pool is the hard ceiling on concurrency).
- **TDD:** no (e2e)
- **Validation:** `./e2e/run-stack-e2e.sh run mock --mock-script e2e/mock-scripts/onboarding.json`
  then `./stack sessions`
  **[rev2]** Do **not** write `./stack start mock && ...`: `start` ends in
  `tmux_wall` and never returns without `NO_TMUX=1` (`stack:339-345`), and a
  bare `run-stack-e2e.sh` is a clean-room cycle that tears the stack back down.
- **Depends on:** T15
- [ ] done
- Notes:

---

*Phase 2 begins here. Do not enable the architect's schedule before T24.*

### T17: migration 046 — `batch_id`   [Status: pending | Model: sonnet]
- **Scope:** A nullable `batch_id varchar(64)` column with an index on
  `config_events` (highest existing migration is `045_datasets`), the `BatchID`
  field on `ConfigEvent`, and `WithBatch(ctx, id)` read by `WithConfigEvent`
  (`config_events.go:356`) when stamping. That function is load-bearing and
  guarded — change it minimally.
- **Files:** modify `go/agentdb/migrations.go`, `config_events.go`; extend
  `config_events_test.go`.
- **Acceptance criteria:** existing rows read back with an empty batch; a write
  under `WithBatch` stamps it; a write without one leaves NULL;
  `TestMutationsAreLogged` passes unchanged.
- **TDD:** yes
- **Validation:** `./stack test-go` (repo root; runs the full suite against a
  live Postgres)
- **Depends on:** T16
- [ ] done
- Notes:

### T18: stamp batches at the two real sources   [Status: pending | Model: opus]
- **Scope:** (a) `ApplyTopology` (`topology_apply.go:178`) wraps its transaction
  in `WithBatch("apply:" + uuid)` and returns the id. (b) **`mcpserver.go`** —
  not `dispatch.go` — resolves the calling session's active `EventDelivery`
  (deliveries carry `SessionID`, `go/agentdb/events.go:258`) and wraps that
  call's context in `WithBatch("job:" + delivery.ID)`. A chat session with no
  delivery gets no batch. Human HTTP edits stay unstamped.
  **[rev2]** `dispatch.go` performs no config writes whatsoever; a job's config
  writes arrive as separate `POST /mcp` requests. Stamping there reaches
  nothing.
- **Files:** modify `go/agentdb/topology_apply.go`,
  `go/cmd/agentd/mcpserver.go`; tests.
- **Acceptance criteria:** an apply produces N events sharing one batch id
  including the bracket event; two `worker_prompt_write` calls from one job
  session share one batch id; the same call from a chat session has none; an
  HTTP worker edit has none.
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T17
- [ ] done
- Notes:

### T19: full-row payload for `project_prompt_write`   [Status: pending | Model: sonnet]
- **Scope:** `go/agentdb/project_settings.go:218` logs only
  `{project, system_prompt}` — the one row action whose payload is not full
  state, which would make its revert wrong. Make it the whole `ProjectSettings`
  row while keeping the `system_prompt` key so `web/src/configLog.ts:238`'s
  prompt diff keeps working. Note `project_settings_put` (the action
  `ApplyTopology` uses) is already full-state and needs no change.
- **Files:** modify `go/agentdb/project_settings.go`; tests; verify
  `web/src/configLog.ts`.
- **Acceptance criteria:** the payload round-trips to a complete
  `ProjectSettings`; the changelog's prompt diff still renders.
- **TDD:** yes
- **Validation:** `./stack test-go` and `(cd web && npm test)`
- **Depends on:** T17
- [ ] done
- Notes:

### T20: addressability   [Status: pending | Model: sonnet]
- **Scope:** Three gaps. `GET /agent/config-events` gains `entity` and `seq`
  filters (the store's `ConfigEventQuery.Entity` exists at
  `config_events.go:469` but is not exposed over HTTP); add
  `GET /agent/config-events/{id}`; carry `seq` and `batch_id` on the web
  `ConfigEvent` type and coercer (`web/src/configLog.ts:80-100`) and on the MCP
  `configHistoryRecord` (`go/cmd/agentd/mcp_config_log.go:65`).
- **Files:** modify `go/httpapi/config_events.go`, `httpapi.go`,
  `go/cmd/agentd/mcp_config_log.go`, `web/src/configLog.ts`; tests.
- **Acceptance criteria:** an entity's chain is fetchable in one call; one event
  is fetchable by id; the browser sorts by `seq` rather than timestamp.
- **TDD:** yes
- **Validation:** `./stack test-go` and `(cd web && npm test)`
- **Depends on:** T17
- [ ] done
- Notes:

### T21: store-level revert   [Status: pending | Model: opus]
- **Scope:** `RevertEvent` and `RevertBatch`. Both are **forward** compensating
  writes through the ordinary logged mutations, in one transaction, stamped with
  a fresh batch id and a rationale quoting the reverted event id(s). Semantics:
  revert of a create is a delete; revert of a delete recreates the row **with
  its original id** — the store's `CreateSubscription`/`CreateSchedule` honour a
  supplied id (`events.go:618`, `schedules.go:296`) while the HTTP routes do
  not, so revert must use the store seam directly; revert of an update writes
  the previous event's payload for the same entity key forward. "Previous" is
  the preceding event for the same `EntityRef` (`config_fold.go:245`) — do NOT
  use `FoldTo`. A create with no predecessor reverts to a delete. Batch revert
  processes members in reverse `seq` order. Document explicitly that
  `topology_apply` events carry a decision, not a row, and are revertable only
  as part of their batch.
- **Files:** create `go/agentdb/config_revert.go`, `config_revert_test.go`.
- **Acceptance criteria:** all three cases round-trip; a restored subscription
  keeps its id; batch revert of a full apply returns the project to its prior
  state with history containing both; `TestRestoreIsForward` passes; reverting
  an already-reverted event is either a no-op or an explicit refusal (choose,
  document, test); reverting a lone `topology_apply` event refuses with a
  message naming its batch.
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T18, T19, T20
- [ ] done
- Notes:

### T22: revert HTTP routes   [Status: pending | Model: sonnet]
- **Scope:** `POST /agent/config-events/{id}/revert` and
  `POST /agent/config-batches/{batch}/revert`, both taking `{rationale}` and
  returning the events written. Project from the credential. No MCP equivalent
  (A10).
- **Files:** modify `go/httpapi/config_events.go`, `httpapi.go`; tests.
- **Acceptance criteria:** another project's event -> 404; a frozen worker may
  be reverted by a human (the freeze blocks agents, not people — the `Frozen`
  comment in `go/agentdb/workers.go`); the response names every event written.
- **TDD:** yes
- **Validation:** `./stack test-go`
- **Depends on:** T21
- [ ] done
- Notes:

### T23: revert in the changelog UI   [Status: pending | Model: sonnet]
- **Scope:** Two actions on `ChangelogView` entry cards: "Revert to this
  version" (one entry) and "Revert this run" (when the entry carries a batch id,
  naming how many changes it covers). Both require a typed rationale and show a
  confirmation naming exactly what will change. **Never use the word "undo"** —
  `docs/product/16-work-plan-operator-console.md:228`. Group entries sharing a
  batch id visually so a run reads as one thing.
- **Files:** modify `web/src/components/ChangelogView.tsx`,
  `web/src/configLog.ts`; tests.
- **Acceptance criteria:** the word "undo" appears nowhere in rendered output
  (asserted); a batch of four renders as one group offering one revert; the
  rationale is required; a server refusal renders verbatim.
- **TDD:** yes
- **Validation:** `cd web && npm test && npm run typecheck`
- **Depends on:** T22
- [ ] done
- Notes:

### T24: enable the architect   [Status: pending | Model: sonnet]
- **Scope:** Flip the architect's schedule to `Enabled: true` in
  `orgproposal.Resolve` (T4 created it disabled) and update T4's test. Document
  the whole feature in `docs/18-workers-memory-events.md`: onboarding, the
  architect's standing loop, that it acts **without approval** and revert is the
  control, batches, and that the `interviewer` worker is permanent and why
  (A11). Update the status paragraph in `CLAUDE.md`.
- **Files:** modify `go/orgproposal/resolve.go`, tests,
  `docs/18-workers-memory-events.md`, `CLAUDE.md`.
- **Acceptance criteria:** a newly applied org has an enabled daily architect
  schedule; docs describe the loop and the revert control.
- **TDD:** no (config + docs)
- **Validation:** `cd go && go test ./orgproposal/... -count=1`
- **Depends on:** T23
- [ ] done
- Notes:

### T25: End-to-end verification   [Status: pending | Model: opus]
- **Scope:** Prove the whole feature works against the running stack, then run
  every gate.
  1. `NO_TMUX=1 ./stack start mock`; create a project with a goal through the
     UI; confirm the catalogue is usable while the rail is starting; drive the
     interview to a deposit; approve; confirm the org chart renders the new
     workers.
  2. Confirm the architect exists, its schedule is enabled, and a forced run
     produces config events sharing one batch id.
  3. Revert that run from the changelog; confirm the org returns to its prior
     state and history contains both.
  4. `./stack sessions`, then `./e2e/run-stack-e2e.sh clean` — leave no
     sessions holding host ports.
- **Files:** none (verification), plus Discovered Issues Log entries.
- **Acceptance criteria:** all four steps observed; all gates green.
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
  **[rev2]** Notes an executor needs: `./stack test-go` **appends `./...`
  unconditionally** (`stack:481-484`), so passing package paths runs the whole
  suite rather than narrowing it — pass flags only. `./stack test_pre_pr` does
  **not** exist here; it survives only in `migration-reference/stack:1745`,
  which is reference-only. `sandbox/` tracks a stale `yarn.lock` that npm
  dirties, hence the checkout — run it from inside `sandbox/`, so the pathspec
  is `yarn.lock`, not `sandbox/yarn.lock`.
- **Depends on:** T24
- [ ] done
- Notes:

---

## Discovered Issues Log

(appended by executors during implementation)
