# First-run happy path — Agent Bob + Agent Wolf

Started 2026-09-13 while Kai is away. Autonomous loop; this file is the remote window onto it.
Branches: `feat/first-run-happy-path` in **agent-bob** and **agent-wolf** (never pushed, never merged).

## Summary (plain English)

**Done 2026-09-13.** Both apps now have a first run that works end to end on the real model,
walked three times each with screenshots. Nothing is pushed or merged: it all sits on
`feat/first-run-happy-path` in agent-bob and agent-wolf, ready for Kai to review.

**Bob, before:** typing in the interview chat did nothing, because the chat panel was never
connected to its session. The goal was also forgotten a second after creating the project, and
after Approve nothing happened until 09:00 the next day.
**Bob, now:**
- Type a goal, then answer a short interview (3 short questions) that ends in a charter (the
  one-page plan you approve).
- Approve, and the architect (the worker that designs the team) starts at once. The screen shows
  its steps ("Creating numbers-clerk…") and each worker as it lands.
- "Go to the Desk". The Desk shows what the team wrote down, notes from the team with a Got it
  button, and questions you can answer in a thread, which then clear.
- "Run a cycle now" hurries the clock, skipping workers that just ran.
- On the real model, a team of four was ready about 5½ minutes after Approve.

**Wolf, before:** the interview looped on tool calls, never ended, and the page never noticed when
it was done.
**Wolf, now:**
- State a thesis. The interviewer asks at most 3 questions, fills in sensible defaults, and ends
  with a message naming the next button.
- "Review and go live" appears by itself. The review screen shows the spec you are locking in.
- Go live, and the page says "Research is running — started …". The report, scoreboard and charts
  then appear without a reload.
- On the real model: 1 question, 7 tool calls, finished in under 2 minutes.

**Gates (re-run on the final commits):**
- agent-bob: Go build, vet and test pass; web 1833 tests and examples/web 13 tests pass.
- agent-wolf: api 2007 tests and web 534 tests pass.
- A database migration was added (`051`, which records whether an attention request is a question
  or a notice). It applies on boot and must go out with the deploy.

**Not verified / left for Kai:**
- The final walk's own written report never reached the coordinator. The walk finished around
  11:55 and committed its screenshots and 5 fixes.
- Screenshots of Approve and the team forming exist only from the second walk (`bob-02-06`…`-08`),
  not from the final walk.
- Two mock-mode sessions and the test projects and hypotheses are left on the local stack on purpose.
- Real-model sessions used: 15 before the final walk, and at most 9 in it.

## RESUME HERE

Finished. Next steps are Kai's: review both branches, merge, and deploy (Bob needs migration 051).
The local stack is in mock mode with test logins: `test@example.com` / `bob-e2e` for Bob, and
Wolf's dev login `dev@example.com` (see `ff0564d`).

## Checklist

### Bob: new project → watching a team work
- ✅ **Interview chat actually works** (the panel loads the interview session; the goal is sent once; replies stream; reload keeps the conversation; also fixed: the goal was being forgotten a second after creation, so the interviewer was told there was none) — `652d702`..`56b49e5`, screens `bob-01-*`
- ✅ **Interview is short** (real model: 3 questions, charter on screen 2m29s after creating the project; Q3 and the label rules read as walls of text)
- ✅ **Approve → architect runs straight away** (real model: exactly one run, team ready 5m32s after Approve; built `c22c790`: the approve route writes the same `architect.run` event as POST /agent/events; not yet seen live)
- ✅ **Team forming is visible** (real model: numbers-clerk, copywriter, weekly-review, scribe landed live; ~2 min spinner-only at start still to fix; built `ad010b0`: "Your team is forming" panel lists workers as they land, then "Your team is ready — go to the Desk"; not yet seen live)
- ✅ **Speed-up control** (real model: a cycle settled in ~4 min, workers asked sharp questions and wrote notes; built `e6d7508`+`ad010b0`: "Run a cycle now" fires every enabled schedule once via new `POST /agent/schedules/{id}/run`, through the scheduler's own gate; not yet seen live)
- ✅ **Watching:** Desk now has "Written down" (newest memories, live, by writer) and Activity shows each job's closing words `f03d814` `0c16728`; parked jobs never clear
- ✅ **Walked end to end on the real model, with screenshots** (`bob-02-01`…`-18`)

### Wolf: sign in → locked hypothesis → research
- ✅ **Bounded interview with a clear closing message** (real model: 1 question, 7 tool calls, closing message names the button; built: ≤3 questions, defaults stated for vague ideas, tool-call caps, template skeleton, new `report_validate` tool; not yet seen on the real model)
- ✅ **The page notices when the interview is done** (real model: button appeared ≤6s after the closing message, no reload; built: polls every 5s while draft, progress line, primary "Review and go live" button; not yet seen live)
- ✅ **Go live → scoreboard + visible research progress** (final walk: `wolf-02-07-research-running`, `-08`, `-09`, `-10`) (built `d7b619d` `eebb79b`: "Research is running — started 2 minutes ago…" / "Last research run finished … · next run …", polls 10s while running; review screen now shows the spec being locked `8f83511`; report stamp names the researcher `c212f9e`; chart years `f0c0c00`; draft says a template is ready `825ea04`; day one says "Waiting for the first observation" `3521e1c`; seed bubble is one short line `d641d94`. NOT yet seen live. Earlier: go-live feedback fixed; report + scoreboard land without reload ~3.5/6.5 min after a run; still NO signal while research runs)
- ✅ **Walked end to end on the real model, with screenshots** (`wolf-01-*`; re-check of the post-interview pass pending)

## Decisions taken (and why)

- **Keep the design's split: the interviewer writes the charter, the architect designs the team.**
  Kai's wish ("the architect says: here's the team I'd deploy") is met by running the architect
  the moment the charter is approved and showing its roster land, rather than making the
  interviewer design workers (the prompt forbids that on purpose — doc 18 §9a).
- **Mock model for wiring, real model only to judge quality.** Real-session budget: 25; used: 15 (Wolf 4, Bob 11).
- **Test logins turned on locally** (`AGENTKIT_TEST_LOGIN`, `--test-login`) so agents can drive the
  browser; Google sign-in still works alongside.

- **Wolf: a validator tool rather than a looser prompt.** The server already had the review screen's
  template check; `report_validate` calls the same code (a test pins they agree), so the model can
  stop redesigning once the template passes.

- **Bob: "Run a cycle now" fires schedules, not synthetic events,** so jobs get the same briefing,
  capacity and budget checks as a real 09:00 run. Same-minute double press fires once.
- **Bob: "no charter yet" is 204, not 404** (only the read; both clients still accept 404), so
  the browser console stops filling with red every 4 seconds.

- **Wolf: the board list keeps its day-one "+0.00".** The detail page is now honest; teaching the
  board would make every board load heavier. Leave until someone minds.
- **Bob: a generic `<agent-context summary="…">` block** (`ad3fc3c`, docs/19 §3a) collapses an
  embedding app's machine instructions in a first message to one line, instead of each app
  special-casing its own seed.

## Open questions for Kai

(none yet)

## Log

- 2026-09-13 — branches cut; both flows mapped. Bob: onboarding chat never resumes the interview
  session (`OnboardingPage.tsx:115` passes only an id; `useAgentSession.sendMessage` no-ops with no
  current session); seed reply stream discarded (`onboarding.ts:135`). Wolf: no end condition in
  `prompts/interviewer.md`; `HypothesisDetail.tsx` loads once and never polls.
- 2026-09-13 — Wolf: interviewer rewritten (≤3 questions, hard tool caps, closing message naming the
  "Review and go live" button), `report_validate` added, detail page polls while draft, NextStep
  shows progress. api 1992 / web 506 tests green. Commits `5c9291f` `d2f59bf` `2a65124` `42601a2`.
- 2026-09-13 — Bob: onboarding chat wired to its session; goal seeded through the chat once; wait for
  the container on reload; error shown if the session fails; goal no longer lost after creation;
  input disabled with a reason when no session is open. web 1720 / examples-web 9 tests green.
  Local stack left in mock mode with test login `test@example.com` / `bob-e2e`.
  Noticed, not yet fixed: goal message shows as a big user bubble with internal instructions; the
  interview is listed as "Untitled · Unknown · agent"; create-project field labels overlap text;
  "Activity is now in the sidebar" notice is wrong; failed `onboard` session keeps the name taken;
  `./stack clean` fails with login on; charter poll 404s spam the browser console.
- 2026-09-13 — Bob polish: goal seed renders as "You set the goal: …"; interview listed as
  "Onboarding interview · interviewer" (`POST /agent/session` takes an optional `title`); notice copy
  fixed; charter read returns 204 when empty; "charter"/"architect" explained on first use. The
  "overlapping labels" were a screenshot taken mid-animation, not a bug. `8fc85ce`..`a65deaa`.
- 2026-09-13 — Bob team forming merged: approve starts the architect (`c22c790`), schedule run-now
  route (`e6d7508`), team-forming panel + Run a cycle now (`ad010b0`), shell link + specs (`a56f444`).
- 2026-09-13 — Wolf real-model walks ×2 (4 sessions). Interview: create→first question 23s,
  answer→closing message ~40s, button ≤6s later. Closing message: "It's ready for you: press Review
  and go live at the top of the hypothesis page…". Fixed: accept/go-live gave no feedback, live
  hypothesis still offered Go live, day-one scoreboard said "no observations" above a price chart.
  wolf gates 2505 tests green. Two test hypotheses left live for walker@example.com (local only).
- 2026-09-13 — Wolf after-interview pass (code only, api 2002 / web 534 green): research state in the
  detail payload from Bob deliveries for the researcher schedule; review screen shows the spec;
  report writer, chart years, draft copy, day-one wording, shorter seed. Needs one live re-walk.
- 2026-09-13 — Bob real-model walk (11 sessions), project `bakery-newsletter`. First question 20s;
  charter 2m29s; Approve → team ready 5m32s (one architect run). Workers: numbers-clerk, copywriter,
  weekly-review, scribe. A cycle settled in 4 min. Fixed: Activity shows closing words with Show all
  (`f03d814`); Desk "Written down" + human schedule names (`0c16728`); cycle confirm names schedule
  times (`701c091`); generic agent-context block (`ad3fc3c`). web 1781 / examples-web 11 green.
- 2026-09-13 — Bob round 2 (code, web 1828 green): `ef3fad0` reply settles parked job + notice flag;
  `d385c35` architect sends a notice, interviewer proposes labels; `da32193` Desk "From the team" with
  Got it; `cb26171` live architect steps; `4318341` label rules list; `40174d3` one menu notice;
  `289d6d4` sidebar; `2bd1d3d` picker; `6a11626` previews; `f3edb56` Desk link; `4f3b9aa` cycle skips
  recent; `4f0dd77` subscription budget line. Wolf `65737f5` `cdd5b8e` `dcb6d4b` (api 2007 green).
- 2026-09-13: final real-model walk of both apps (screens `bob-03-*`, `wolf-02-*`; commit `c670f88`).
  Fixed on the way:
  - `282d6ee`: the Desk lists a question a worker asks again after a reply.
  - `b69798a`: previews drop markdown divider lines.
  - `ee4aeb8`: Approve scrolls the team-forming panel into view.
  - `ff0564d`: `./stack wolf up --test-login` uses dev@example.com, which Wolf accepts.
  The coordinator re-ran the gates afterwards: all green.
