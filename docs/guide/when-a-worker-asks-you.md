---
title: When a worker asks you
slug: when-a-worker-asks-you
part: 1
order: 7
surfaces: []   # Desk ROW link (G4/C5), not the Desk's About line — design §4.4
---

Sometimes a worker reaches a point where it should not guess. It stops, mid-job, and asks you
directly instead of carrying on with a guess. This is not an error and not a crash: the job is
paused, waiting, and it picks up again the moment you answer, in the same thread.

## In the console

Desk → Asks. A rose diamond marks a row waiting for you; open it to read what
the worker needs and reply in its thread. → `#/guide/when-a-worker-asks-you`

## The smallest useful thing you can do

Open an ask, read the message the worker wrote, and
answer it in plain words in that thread. The worker continues from your reply.

## Ellen's example

<!-- fixture: to be captured by B7 -->

The architect's first message, once it has built the roster, is itself an ask: it tells Ellen what
it created and why, and waits for her to read it.

Sidebar: **the unasked question.** A booking worker once guessed at an ambiguous request and let
the job finish as though the guess were an answer. Its mentor rewrote its prompt with the reason
"the planner guessed at an ambiguous request instead of asking." The next time the same kind of
request arrived, the worker asked instead of guessing, and the job parked at `awaiting_human`
until a person replied (`e2e/features/learning-stories.stack.spec.ts:550`). Asking a human arrived
as something the worker learned, not something anyone configured.

## The trap

Silence is not consent. A paused worker does nothing at all until you answer it or
its ask expires: it does not fall back to a guess, and it does not go away on its own. Always
check that an ask carries an expiry: one made without it is never marked answered, even after you
reply, and it sits in the Asks list forever (`docs/workflows.md` §6). A job waiting on you can also
keep looking parked in its own history after you've already answered it. It holds no capacity and
blocks nothing else, but the row itself keeps saying "waiting."

## What this will not do

Answering an ask does not retry the part of the job that already ran; it
only lets the paused worker continue from where it stopped. It will not chase you for an answer;
there is no reminder beyond the ask sitting on the Desk.

## Next

Back: [Talking to a worker](talking-to-a-worker.md). Forward: [The Desk](the-desk.md).
