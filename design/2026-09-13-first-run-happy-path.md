# First-run happy path — Agent Bob + Agent Wolf

Started 2026-09-13 while Kai is away. Autonomous loop; this file is the remote window onto it.
Branches: `feat/first-run-happy-path` in **agent-bob** and **agent-wolf** (never pushed, never merged).

## Summary (plain English)

Kai tried both apps as a new user and neither felt finished.

- **Bob:** a new project opens with an interview, but **typing in the interview chat did nothing**.
  That is a real bug: the chat panel was never connected to the interview session, so messages went
  nowhere and the interviewer's first question never showed. Kai wants a short chat, then a team
  of workers he can watch, plus a way to speed things up at the start.
- **Wolf:** the hypothesis interview **had no finish line**. The prompt told it to push
  "relentlessly" and never said when to stop, and the page never refreshed itself, so even a
  finished interview looked unfinished.

## RESUME HERE

1. Wolf WALKED on the real model twice (screens `wolf-01-*`): interview finishes cleanly; three
   post-interview bugs fixed (`ed18f44` `ee13263` `96bab08`). Remaining: no "research running"
   signal, review screen hides the spec, seed shown as user bubble, "unknown writer", draft page
   says "No report template yet" after the interview, scoreboard 0.00 on day one.
2. Bob chat-wiring fix is DONE and proven in a browser (mock model). Screens: `design/2026-09-13-first-run-screens/bob-01-*`.
3. Bob team forming + "Run a cycle now" BUILT and MERGED (branch `feat/first-run-team-forming`),
   polish fixes merged. Gates re-running on the merge. NEXT: a real-model Bob walk (interview →
   approve → team forms → run a cycle) once the Wolf live walk frees the stack.

## Checklist

### Bob: new project → watching a team work
- ✅ **Interview chat actually works** (the panel loads the interview session; the goal is sent once; replies stream; reload keeps the conversation; also fixed: the goal was being forgotten a second after creation, so the interviewer was told there was none) — `652d702`..`56b49e5`, screens `bob-01-*`
- ⬜ **Interview is short** (3–5 questions) and ends with a charter (the plan the human approves)
- 🟡 **Approve → architect runs straight away** (built `c22c790`: the approve route writes the same `architect.run` event as POST /agent/events; not yet seen live)
- 🟡 **Team forming is visible** (built `ad010b0`: "Your team is forming" panel lists workers as they land, then "Your team is ready — go to the Desk"; not yet seen live)
- 🟡 **Speed-up control** (built `e6d7508`+`ad010b0`: "Run a cycle now" fires every enabled schedule once via new `POST /agent/schedules/{id}/run`, through the scheduler's own gate; not yet seen live)
- ⬜ **Watching:** events, memories and architect changes readable from the Desk
- ⬜ **Walked end to end on the real model, with screenshots**

### Wolf: sign in → locked hypothesis → research
- ✅ **Bounded interview with a clear closing message** (real model: 1 question, 7 tool calls, closing message names the button; built: ≤3 questions, defaults stated for vague ideas, tool-call caps, template skeleton, new `report_validate` tool; not yet seen on the real model)
- ✅ **The page notices when the interview is done** (real model: button appeared ≤6s after the closing message, no reload; built: polls every 5s while draft, progress line, primary "Review and go live" button; not yet seen live)
- 🟡 **Go live → scoreboard + visible research progress** (built `d7b619d` `eebb79b`: "Research is running — started 2 minutes ago…" / "Last research run finished … · next run …", polls 10s while running; review screen now shows the spec being locked `8f83511`; report stamp names the researcher `c212f9e`; chart years `f0c0c00`; draft says a template is ready `825ea04`; day one says "Waiting for the first observation" `3521e1c`; seed bubble is one short line `d641d94`. NOT yet seen live. Earlier: go-live feedback fixed; report + scoreboard land without reload ~3.5/6.5 min after a run; still NO signal while research runs)
- ⬜ **Walked end to end on the real model, with screenshots**

## Decisions taken (and why)

- **Keep the design's split: the interviewer writes the charter, the architect designs the team.**
  Kai's wish ("the architect says: here's the team I'd deploy") is met by running the architect
  the moment the charter is approved and showing its roster land, rather than making the
  interviewer design workers (the prompt forbids that on purpose — doc 18 §9a).
- **Mock model for wiring, real model only to judge quality.** Real-session budget: 25; used: 4 (Wolf walks).
- **Test logins turned on locally** (`AGENTKIT_TEST_LOGIN`, `--test-login`) so agents can drive the
  browser; Google sign-in still works alongside.

- **Wolf: a validator tool rather than a looser prompt.** The server already had the review screen's
  template check; `report_validate` calls the same code (a test pins they agree), so the model can
  stop redesigning once the template passes.

- **Bob: "Run a cycle now" fires schedules, not synthetic events,** so jobs get the same briefing,
  capacity and budget checks as a real 09:00 run. Same-minute double press fires once.
- **Bob: "no charter yet" is 204, not 404** (only the read; both clients still accept 404), so
  the browser console stops filling with red every 4 seconds.

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
