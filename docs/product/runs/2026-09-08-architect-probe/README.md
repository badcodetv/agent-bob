# T1 — live architect probe

**Date:** 2026-09-08 · **Verdict: PASS.** The §C structure survived contact.
Proceed to T5.

**Model:** whatever `CLAUDE_CODE_OAUTH_TOKEN` resolves to — the stack booted in
*subscription mode* (`[agentd] subscription mode (CLAUDE_CODE_OAUTH_TOKEN) →
sessions call api.anthropic.com directly`), so sessions billed the Claude
subscription, not the API key. Both credentials were present in `.env`; the log
line `both ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN set — the OAuth token
wins` records which one was used.

**Stack:** `NO_TMUX=1 ./stack start real local` — real model, session base image
built into DinD (no GCP credentials needed). Driven over HTTP, not the browser:
project tokens minted locally from `AGENTKIT_JWT_SECRET` in the `devclaims`
shape (`sid, customer, job, email, iat, exp`), because only Google login was
enabled and `POST /auth/password` needs a `testLogin` this stack did not set.

**Prompt used:** [`architect-prompt.md`](architect-prompt.md) beside this file,
**as installed** for the runs below. T5's handoff artifact. Revisions learned
here are listed under *Changes to fold into T5* and are NOT yet applied to that
file — it is the record of what actually ran.

---

## Why two projects, not the one the ticket describes

T1 as written says to apply `architect-archivist@v1` and drive it. That
topology creates an architect **and** an archivist, so the architect never sees
itself alone — and **STEP 0, the bootstrap branch, could never fire**. C0 calls
that branch load-bearing: without it a fresh project's architect declines to
act forever. Testing it needed a project where the architect is genuinely
alone.

`archivist` is a `bool` question on that topology, so `archivist: false` gives
exactly that, with no new code.

| | project | shape | tests |
| --- | --- | --- | --- |
| **A** | `t1-probe` | architect alone | STEP 0 — does it act when idle? |
| **B** | `t1-loop` | architect + archivist, 8 seeded memories across 5 days | steps 1-6 — does it thrash when there is nothing to judge? |

Scenario B's evidence was eight memories about a fictional bookshop newsletter
(`kind=summary` ×4, `kind=lesson` ×3, `kind=fact` ×1), written through
`POST /agent/memories` and then back-dated in SQL across 2026-09-04..09-08.
⚠️ `memories.created_at` is **milliseconds**; a first attempt subtracted
seconds and moved the rows by 86 seconds, not 86,400.

---

## The six questions

**1. Did it thrash on the no-signal run?** **No.** Scenario B run 2 (session
`43dc60f8`) made **zero** configuration changes — verified by attributing every
`config_events` row to its `actor_session`: seq 8-12 all belong to run 1, and
run 2 added none. It also correctly stayed silent, emitting no
`request_human_attention`, because step 5 fires only on change.

Its stated reason is **better than the rule it was given**. The prompt said
"fewer than three new summary/lesson memories ⇒ change nothing". It reasoned
about elapsed time instead:

> VERDICT: no-signal — on the run-1 bootstrap changes … **142 seconds elapsed
> between the change and the measurement. Nothing could have happened, and
> nothing did.**

This closes a hole the prompt had and the plan had not noticed: the archivist
writes a summary every time the architect finishes, so **the architect's own
activity manufactures the evidence that unlocks its next run**. A pure count
would ratchet open forever. Elapsed-time reasoning does not. See *Changes to
fold into T5*.

**2. Did it act at all on the bootstrap run?** **Yes, and well.** Scenario A's
architect, alone, built:

- three workers — `archivist`, `newsletter-editor`, `growth-strategist`, each
  with its own briefing selector list;
- **three `worker.finished` subscriptions, each filtered to ONE worker**, in
  its own words *"so it can never match its own completion and loop"* — a
  reflexive-wake hazard it identified unprompted;
- schedules for each worker, **including a weekly one for itself** (it had
  none — it was event-triggered only);
- a **v2 label registry** adding `kind=draft` and `kind=experiment` and
  recording which role reads and writes each label;
- the project prompt (previously empty), a `name=project-goal` memory, and a
  `kind=decision, name=initial-roster` memory stating what it did **not** build
  and what evidence would show the roster is wrong.

It then refused to invent facts about the real shop, listed the four facts it
needed, and argued the trade-off: *"a confidently-wrong fact gets inherited by
everything downstream, whereas an empty memory store is recoverable."*

**3. Did it read `config_history` for its own last change?** **Yes.** Run 1's
verdict cites the call and its result: *"config_history with
actor_worker=\"architect\" returns 0 records, and memory_search on
kind=architect-verdict returns 0. This is the first architect run."* This is
the read doc 25 says *"breaks the delay term"* for a cron-batched corrector.

**4. Did verdict-before-action hold?** **Yes, 3 of 3.** Every scenario-B run
wrote a `kind=architect-verdict` memory naming what it was judging before
touching configuration.

**5. Was one-change-per-run obeyed?** **Yes.** The cap is on *prompt rewrites*
(C1/C2), and no run made more than one. Run 3 made exactly one change of any
kind. Run 1 is the bootstrap-shaped exception the branch exists to allow.

**6. Did it send `request_human_attention`?** **Yes, and only when it should.**
Runs that changed something ended `awaiting_human` with a message naming what
changed, why, and the config event to revert to. Run 2, which changed nothing,
correctly sent none.

---

## The strongest single result

Scenario B run 3 diagnosed its own instrument rather than churning the roster:

> This project is scored on one number and nothing in it can read that number —
> no worker here touches the email platform. `subscriber-count` has said 430
> since 2026-09-06 and only moves when a person types it in. So every verdict I
> write scores itself against a criterion that can only ever return "not met",
> which makes real progress and total failure look identical to me and **would
> eventually push me to churn the roster hunting a fault that is in the
> instrument, not the work.**

Its fix was one schedule input: the newsletter worker now checks whether the
count is stale, asks a human if so, **and drafts anyway** so an unanswered
question cannot cost a week's work. It rejected the alternative — a worker
whose job is to write the count — because it would have to guess:
*"a confident wrong number is worse than an honest stale one."*

That is L20's loop and doc 25's delayed-corrector warning, self-applied.

---

## What this does NOT prove

- **Four runs, not four weeks.** Nothing here shows behaviour over months, or
  what happens once real evidence accumulates and verdicts start reading
  `helped` / `hurt` rather than `no-signal`. All three verdicts were
  `no-signal`, honestly so — the runs were minutes apart.
- **Run 4 was killed mid-flight** and produced no data. STEP 6 (three
  consecutive no-signal runs ⇒ escalate "nothing writes memory") was therefore
  **never observed firing**. Runs 1-3 all wrote `no-signal`, so run 4 was
  exactly the run that should have escalated. Untested.
- **T4's archivist fix is untested here.** Both archivists in this probe were
  authored by the *architect*, not instantiated from `archivistPrompt`, so the
  shipped instruction never ran. See DI3.
- **Nothing about cost or latency at scale.** Runs took 2-5 minutes each.

---

## Changes to fold into T5's `architect.md`

1. **Make elapsed time explicit in the evidence gate.** Add to STEP 3: *"and if
   your last change was made minutes ago, no evidence about it can exist yet,
   whatever the count says."* The model reached this alone; a weaker one may
   not, and without it the gate ratchets open on self-generated evidence.
2. **Endorse per-worker subscription filters.** Add to STEP 4's *new roles*:
   when subscribing a worker to `worker.finished`, filter per subject worker
   rather than subscribing to all of them, so it cannot be woken by its own
   completion.
3. **Add an instrument check.** If the project's measure cannot be read by any
   worker, say so and fix the instrument — do not rewrite roles hunting a fault
   that is in the measurement.
4. **Keep the wording of STEP 0 and STEP 5 unchanged.** Both produced exactly
   the intended behaviour.

## What C6 looked like in practice

The architect gave **itself** a schedule and rewrote **its own** briefing —
benignly, and arguably correctly (it added `name=project-goal` and its own last
verdict, which its loop needs). But it confirms C6 without ambiguity: there is
no mechanical brake, and self-modification is one tool call away. T25 must
carry C6's sentence unhedged.

---

## Discovered issues

**DI2 — `PUT /agent/workers/{name}` silently wipes any field the body omits.**
`PutWorker` (`go/httpapi/workers.go:111-116`) builds a fresh `agentdb.NewWorker`
and assigns `Description`, `SystemPrompt`, `MCPConfig`, `Image` and **`Briefing`**
unconditionally from the body. Installing the probe prompt with
`{"system_prompt": …}` therefore **erased the architect's briefing** — the exact
mechanism T2 exists to deliver. Nothing failed; the field was simply gone.
Defensible as PUT semantics, and the identical hazard is already documented one
level up for `PUT /agent/project-settings` (T19's warning), but the worker-level
twin is not, and the console's worker editor is the obvious place it will bite.
**Wants a ticket:** either round-trip every field in the console, or make the
route a merge.

**DI3 — a self-designed archivist does not write T4's label.** The architects in
both scenarios independently invented `kind=summary` **plus a `worker=<name>`
label** — convergent with T4's fix in spirit, but the default briefing selector
reads `kind=rolling-summary`, so it still matches nothing. T4 changed the shipped
`archivistPrompt`; it cannot reach an archivist the architect writes itself.
Worth considering whether `RollingSummarySelector` should follow the convention
models reach for (`kind=summary,worker=<name>`) rather than the reverse.

**DI4 — the archivist flagged that `request_human_attention` may reach nobody.**
It wrote `kind=lesson`: *"request_human_attention in this project may silently
reach nobody."* Correct for this stack (no attention webhook configured
locally) and a good catch, but a local-config artifact rather than an engine
bug. Noted because C3 makes that channel the whole notification path.

## Reproducing

```sh
NO_TMUX=1 ./stack start real local     # BILLABLE
./stack status                          # must show real-model mode
# apply architect-archivist@v1 with archivist:false (scenario A) / true (B)
# PUT the prompt, POST a subscription on architect.run, POST that event
./stack sessions && ./e2e/run-stack-e2e.sh clean
```

Driver scripts were session-scratch and are not preserved; the HTTP calls are
the four lines above.
