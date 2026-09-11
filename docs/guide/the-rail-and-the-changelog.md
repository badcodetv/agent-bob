---
title: The rail and the changelog
slug: the-rail-and-the-changelog
part: 2            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 9
surfaces: [activity, worker-history]
---

Every job that ran, every event that arrived, and every change anyone made to your project lands
on one timeline, in the order it happened. This page is about the change part of that timeline:
the changelog, where you see who changed what and why, and can put a change back if it turns out
to be wrong. Putting one back writes a new entry; it never deletes the old one.

## In the console

Activity, filtered to the **changes** lens; or open a worker and read its own
History tab. → `#/guide/the-rail-and-the-changelog`

<!-- guide note: revert is described here as it will work once ticket A1 lands, mounting the
existing revert control on Activity's changes rows. Until then, "Revert to this version" exists
only inside the retired ChangelogView component, which the shipped console shell no longer mounts
(design/2026-09-11-onboarding-and-the-guide.md §6, PR0). -->

## The smallest useful thing you can do

Open Activity, switch to the **changes** lens, find the
entry you want to put back, and press **Revert to this version**. Type why, and confirm.

## Ellen's example

<!-- fixture: to be captured by B7 -->

The confirmation names exactly what is about to change:

> This puts *\<what>* back to the state it was in after *\<the change>*, made *\<when>* by
> *\<who>*. Nothing is erased. Reverting writes a new change that puts the old state back, and
> both stay in this log — so you can revert the revert.

(`web/src/components/ChangelogView.tsx:427-431`.)

## The trap

Only the newest change to a thing can be put back this way. If something else has
changed it since, the button is disabled and a note beside it names what is in the way, so "why
can't I do this" has an answer without guessing. The rail marks who acted, not what: an agent's
change is warm (ember), a human's is unmarked, so the warm marks down the rail *are* the
self-improvement loop and their absence is you (`docs/product/15-operator-console-design.md`
§3.2). It is one rail because it is one table shape: "everything hangs off a rail, because
everything is an append" (`docs/product/15-operator-console-design.md` §3.6).

## What this will not do

It does not erase anything: the original change and the one that put
it back both stay, forever. It does not stop whatever made the change from making it again:
putting a worker's prompt back changes what it is running now, not what it decides to do next
time.

## Next

Back: [The Desk](the-desk.md). Forward: [Memory](memory.md).
