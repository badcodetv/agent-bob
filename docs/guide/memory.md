---
title: Memory
slug: memory
part: 2            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 10
surfaces: [memory]
---

Memory is everything anyone in your project has written down: decisions, summaries of finished
work, facts worth keeping, lessons that should change what happens next. Every worker starts each
run remembering nothing except what somebody put here. A memory is written once and never edited;
changing something means writing the whole thing again, and the newest copy under the same name
wins. Nothing here is deleted, even when it is withdrawn.

## In the console

Memory. Search by words, or by a label like `kind=fact`. → `#/guide/memory`

<!-- guide note: "Write a note" is described here as it will work once ticket C4 lands. Today
Memory is read-only in the console; a memory can only be written by a worker, through its tools
(design/2026-09-11-onboarding-and-the-guide.md §3 G8). -->

## The smallest useful thing you can do

Open Memory, press **Write a note**, type what you want
kept and one label such as `kind=fact`, say why, and save.

## Ellen's example

The kinds of thing worth keeping in her project, from her charter, word for
word:

> kind=decision - a choice that was made and why, written by whoever made it. kind=summary - what
> happened in one piece of work; name=<slug> so it can be found again. kind=lesson - something
> that should change how we do this next time. kind=fact - a durable fact about the shop or the
> list, name=<what-it-is> so the current value can be read back; subscriber-count lives here.
> kind=draft - a newsletter that has been written but not sent; name=<issue-slug>.

(`go/orgprompts/interviewer.md`, the worked example.) A real note from Ellen's project, as
written:

```
kind=fact name=subscriber-count

Subscriber count as of today: 447, up from the 430 the charter started at.
```

## The trap

There is no OR in a selector: "No memory matches. There is no OR in a selector: try
one clause at a time." (`web/src/components/MemoryBrowserPage.tsx:179`.) Looking for
`kind=fact` or `kind=lesson` takes two searches, not one. Retracting a wrong memory withdraws it
from search and from the rolling summary every worker is handed, without deleting it, "so the
record of the mistake survives" (`go/orgprompts/registry.md`).

## What this will not do

It does not make any worker obey what is written here: "a memory
records what someone previously believed or did," and every worker weighs it against its current
task first (`go/compose.go`, the core preamble). It does not let you edit a memory in place;
someone writes the whole thing again, and the old copy stays as history.

## Next

Back: [The rail and the changelog](the-rail-and-the-changelog.md). Forward:
[The chart](the-chart.md).
