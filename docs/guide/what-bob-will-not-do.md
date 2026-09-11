---
title: What Bob will not do
slug: what-bob-will-not-do
part: 3            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 13
surfaces: []
---

This page is the honest edge of the product. Nothing on it is a bug waiting for a fix; each limit was chosen on purpose. Read it before you rely on Bob for one of these.

## In the console

There is no button for this page; it exists so no screen has to overpromise.

## The smallest useful thing you can do

Before asking the architect for something new, check this list; if it is here, build it differently instead of asking harder.

## Where the edges are

- **Decomposing one task.** Break it into steps with subagents inside one chat, not a project; a fleet costs more and is slower for work that finishes in one sitting.
- **Waiting on more than about a hundred parallel branches to report back.** There is no join, no barrier, no quorum.
- **A boundary between workers.** There isn't one: every worker holds every core tool, including the one that rewrites another's instructions, and can read any other worker's setup. The project is the only boundary; use two if you need two.
- **Multi-agent debate to raise accuracy.** One model, given the same revision budget and no peer text, matched or beat a ten-agent debate in most trials, at a third of the cost.
- **Searching automatically for the best team shape.** Every generator tested lost to a team designed by hand. Design the roster, or let the architect; nothing here searches for you.
- **Work that must not lose a step on failure.** A failed job stops and keeps only an error string, not its transcript or the input that triggered it. Nothing retries it.
- **Giving a worker a new tool after it is hired.** A worker's tools are fixed at creation; the architect cannot see what else is available.
- **A worker's chat carrying the rulebook.** Chat gets none of the project's memory rules. That is why running the architect is a button, not a typed message.
- **Protecting the rulebook.** It is an ordinary memory. Any worker that can write memory can publish a new version of it, and the newest one wins.

## What this will not do

Bob will not retry a failed step, stop one worker touching another's setup, search for the best org chart, or protect the rulebook from being overwritten. These are today's edges, not tomorrow's promise.

## Next

Back: [The architect](the-architect.md). Forward: [Money](money.md).
