---
title: Talking to a worker
slug: talking-to-a-worker
part: 1
order: 6
surfaces: [chat]
---

A chat is not a job. It is a live conversation with the base agent or with one named worker, and
none of the project's ordinary machinery sits behind it: no schedule woke it, no event triggered
it, and nothing said there is remembered by the project unless the worker decides, on its own, to
write a memory recording it. Use it to ask a worker what it knows, to read its instructions back,
or to try a question before you turn it into a rule. It is the fastest way to see what a worker
actually understands, and the least reliable way to get it to remember anything.

## In the console

Open Chat from the sidebar for the base agent, or a worker's own Chat tab for
that worker specifically. → `#/guide/talking-to-a-worker`

## The smallest useful thing you can do

Open a worker's Chat tab and ask it about a piece of
work it has not seen yet, such as "what would you do if I sent you a complaint about a late
delivery?", and read how it reasons before you commit anything to its prompt.

## Ellen's example

Ellen wants to see exactly what `newsletter-writer` is set up to do before
she trusts it with anything. She opens its Chat tab and types, "show me your instructions." It
answers with its own prompt, so she reads exactly what it was told.

## The trap

A chat receives none of the project's briefing: not the rulebook (the label
registry), nothing. It answers from its base prompt and whatever you type, never from what the
project has learned. That is also why "Run the architect now" sends an event rather than opening a
chat: the architect's real work needs the briefing a chat cannot carry. A chat's transcript is
reported to the rest of the project only once it goes quiet, as one `worker.finished` event an
archivist might read; while you are mid-conversation, nothing about it exists anywhere else.

## What this will not do

A chat never runs as a job, never receives the project's briefing, and
is exempt from the daily budget. Nothing said in it becomes part of the project's memory unless the
worker itself writes it down.

## Next

Back: [Clocks and wake-ups](clocks-and-wake-ups.md). Forward:
[When a worker asks you](when-a-worker-asks-you.md).
