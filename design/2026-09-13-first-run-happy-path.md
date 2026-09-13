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

1. Wolf finish-line fixes are BUILT (agent-wolf `5c9291f`..`42601a2`, gates green). Needs a live
   real-model walk once the stack is free.
2. Bob chat-wiring fix is DONE and proven in a browser (mock model). Screens: `design/2026-09-13-first-run-screens/bob-01-*`.
3. Next for Bob: after Approve, run the architect **immediately** and show the team forming live;
   then a "speed up" control for the first hour.

## Checklist

### Bob: new project → watching a team work
- ✅ **Interview chat actually works** (the panel loads the interview session; the goal is sent once; replies stream; reload keeps the conversation; also fixed: the goal was being forgotten a second after creation, so the interviewer was told there was none) — `652d702`..`56b49e5`, screens `bob-01-*`
- ⬜ **Interview is short** (3–5 questions) and ends with a charter (the plan the human approves)
- ⬜ **Approve → architect runs straight away** (no waiting for 09:00)
- ⬜ **Team forming is visible** (the workers the architect creates appear as they land)
- ⬜ **Speed-up control** (run a cycle now instead of waiting for schedules)
- ⬜ **Watching:** events, memories and architect changes readable from the Desk
- ⬜ **Walked end to end on the real model, with screenshots**

### Wolf: sign in → locked hypothesis → research
- 🟡 **Bounded interview with a clear closing message** (built: ≤3 questions, defaults stated for vague ideas, tool-call caps, template skeleton, new `report_validate` tool; not yet seen on the real model)
- 🟡 **The page notices when the interview is done** (built: polls every 5s while draft, progress line, primary "Review and go live" button; not yet seen live)
- ⬜ **Go live → scoreboard + visible research progress**
- ⬜ **Walked end to end on the real model, with screenshots**

## Decisions taken (and why)

- **Keep the design's split: the interviewer writes the charter, the architect designs the team.**
  Kai's wish ("the architect says: here's the team I'd deploy") is met by running the architect
  the moment the charter is approved and showing its roster land, rather than making the
  interviewer design workers (the prompt forbids that on purpose — doc 18 §9a).
- **Mock model for wiring, real model only to judge quality.** Real-session budget: 25; used: 0.
- **Test logins turned on locally** (`AGENTKIT_TEST_LOGIN`, `--test-login`) so agents can drive the
  browser; Google sign-in still works alongside.

- **Wolf: a validator tool rather than a looser prompt.** The server already had the review screen's
  template check; `report_validate` calls the same code (a test pins they agree), so the model can
  stop redesigning once the template passes.

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
