---
title: The rulebook
slug: the-rulebook
part: 1
order: 4
surfaces: [memory]
---

The rulebook is the label registry: a written statement of what gets written down in this
project's memory, and under which label. Nothing else holds a project together as much as this
does, because every worker forgets everything else between runs. The rulebook is itself an
ordinary memory, not a special kind of setting.

## In the console

Memory, and Settings → project briefing, which points at the rulebook. →
`#/guide/the-rulebook`

## The smallest useful thing you can do

Open Memory, find the newest row labelled
`kind=org-charter` or the label registry itself, read its rules, and use **Publish a new
version** to add one more label you know this project needs.

## Ellen's example

Her charter's label rules, written down during her interview, word for word:

> kind=decision - a choice that was made and why, written by whoever made it. kind=summary - what
> happened in one piece of work; name=<slug> so it can be found again. kind=lesson - something
> that should change how we do this next time. kind=fact - a durable fact about the shop or the
> list, name=<what-it-is> so the current value can be read back; subscriber-count lives here.
> kind=draft - a newsletter that has been written but not sent; name=<issue-slug>.

(`go/orgprompts/interviewer.md`, the worked example.) Later, if Ellen decides complaints need
their own trail, she publishes a new version with a line added:

<!-- fixture: to be captured by B7 -->

## The trap

"The label registry is unprotected. It is an ordinary memory. Any worker that can
write memory can publish a new version of the rulebook, and the newest wins."
(`docs/18-workers-memory-events.md:884-886`). Editing the rulebook shows up in Memory as a new
row, not in the changelog: it isn't a settings change. There is no OR in a selector: "No memory
matches. There is no OR in a selector: try one clause at a time."
(`web/src/components/MemoryBrowserPage.tsx:179`).

## What this will not do

Publishing a new version does not merge with the old one; you write
the whole rulebook again, the same as the charter. It does not stop a mistaken label from being
used before someone notices and republishes. It does not enforce that any worker actually follows
the labels it names: the rulebook is a reference, not a rule engine.

## Next

Back: [A worker's instructions](a-workers-instructions.md). Forward:
[Clocks and wake-ups](clocks-and-wake-ups.md).

<!--
Note: this page describes Memory as a writable surface (a "Publish a new version" action and
a "Write a note" control) as it will be after ticket C4 lands. Today the Memory page is
read-only; see design/2026-09-11-onboarding-and-the-guide.md §3 G8.
-->
