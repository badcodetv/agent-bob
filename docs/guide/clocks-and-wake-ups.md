---
title: Clocks and wake-ups
slug: clocks-and-wake-ups
part: 1
order: 5
surfaces: [worker-triggers]
---

Something has to wake a worker up before it can do anything. A schedule wakes it at set times; a
subscription wakes it when an event of some kind arrives. Both tell the worker the same two
things: to start a job, and what to say to it. "A subscription says: when an event of this type
arrives, start a job for this worker. A schedule says: at these times, tell this worker to do
this." (`web/src/components/WorkerTriggers.tsx:216-220`)

## In the console

Workers → a worker → Triggers. → `#/guide/clocks-and-wake-ups`

## The smallest useful thing you can do

Open a worker's Triggers tab, add a schedule, and
describe when and what in plain words. The assist proposes a compiled time and input text; read
it, then apply it yourself.

## Ellen's example

She types "every Monday at 08:00" and "draft this week's newsletter". The
assist reads the day and the clock time and proposes the five-field cron `0 8 * * 1`, which she
then applies. Nothing fires until she does.

That is exactly the trigger the architect itself saved on `newsletter-writer`'s Triggers tab when
it built the roster: cron `0 8 * * 1` → input "Draft this week's newsletter." — the same shape,
because that is what "every Monday at 08:00" compiles to.

## The trap

"The input text is the point: '10:00 → *write the morning tweet*', '17:00 → *write
the evening tweet*' — same worker, different instruction per trigger"
(`docs/product/17-product-spec.md:141-142`). A schedule with no text wakes a worker with
nothing to do. The plain-words assist only proposes; it never saves on its own: "nothing here is
ever saved without a human seeing the result first: `compile*` returns a proposal, the editor
shows it, and the human clicks Apply" (`web/src/nlAssist.ts:6-14`). And a worker is never woken by
its own finishing: "A worker is never woken by its own completion." (`go/cmd/agentd/router.go:420`)
So a worker that wants to keep working needs a different trigger, not a subscription to itself.

## What this will not do

The assist understands a fixed set of plain-English time phrasings and
refuses the rest loudly rather than guessing; anything it doesn't recognise, you write the five
cron fields yourself. It cannot express "every ten seconds": the scheduler wakes once a minute at
most. And a subscription filter is equality only: it matches an event's fields exactly, it cannot
express "any of these three types" in one row.

## Next

Back: [The rulebook](the-rulebook.md). Forward: [Talking to a worker](talking-to-a-worker.md).
