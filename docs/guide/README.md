# The Bob guide

This folder is the user guide: not the API reference (`docs/19-embedding.md`), not the engine
docs, not a spec. It is written for the person who was just invited into a project, not for
someone building on top of Agent Bob. If a page or a sentence does not serve one of the three
reader journeys in `design/2026-09-11-onboarding-and-the-guide.md` §2, it does not belong here.

## Reading order

Sixteen pages, a glossary, and one operator page. Read top to bottom the first time; after that,
each page's own "Next" line is the path.

**Part 0: Start here**

0. [What Bob is](what-bob-is.md)
1. [Your first hour](your-first-hour.md)

**Part 1: The six things you can touch (the levers)**

2. [The goal and the charter](the-goal-and-the-charter.md)
3. [A worker's instructions](a-workers-instructions.md)
4. [The rulebook](the-rulebook.md)
5. [Clocks and wake-ups](clocks-and-wake-ups.md)
6. [Talking to a worker](talking-to-a-worker.md)
7. [When a worker asks you](when-a-worker-asks-you.md)

**Part 2: Reading what happened**

8. [The Desk](the-desk.md)
9. [The rail and the changelog](the-rail-and-the-changelog.md)
10. [Memory](memory.md)
11. [The chart](the-chart.md)

**Part 3: The architect and the loop**

12. [The architect](the-architect.md)
13. [What Bob will not do](what-bob-will-not-do.md)
14. [Money](money.md)

**Part 4: Field notes (the art)**

15. [Field notes](field-notes.md)

**Reference**

- [Glossary](glossary.md)
- [Inviting someone](for-operators/inviting-someone.md): Kai's runbook, not written for an
  invitee.

## The shape of every page

Every page follows the same skeleton, in this order, so a reader learns to skim once and skims
everywhere. Target **150–400 words**, hard cap **500** (`wc -w <page>.md`). No page exceeds one
screen of prose plus one example.

> The cap was **450** until 2026-09-12, and was set while every page's example was a
> one-line placeholder. Real captured output — an actual worker prompt, an actual ask
> message — costs 30–60 words, which put four finished pages at 452–475. Getting them
> under 450 meant truncating a verbatim quote, which this file forbids two sections
> below, so the cap moved instead of the quotes (DI35).

1. **The one line and In the console**: what this is, in a sentence a person could repeat, and
   where it lives in the console with what the button says. These two together are the page's
   **first paragraph**, written to stand alone with no links: it is what the console's "About this
   screen" disclosure shows verbatim, so it never assumes the reader has seen anything else.
2. **The smallest useful thing you can do**: one action, three steps at most.
3. **Ellen's example**: the bookshop. A real prompt, label, schedule or memory, shown in mono,
   whole, never truncated with `…`. If the real thing has not been captured yet, mark it
   `<!-- fixture: to be captured by B7 -->` rather than inventing one.
4. **The trap**: the one thing that surprises people. Sourced from the Discovered Issues Log or a
   known-limits section; never invented.
5. **What this will not do**: one to three lines. Editorial rule 3, say what is not modelled.
   Every page ends with this section, in these words.
6. **Next**: one link back, one link forward, to slugs in this folder only.

## Voice

Inherited from the console's own editorial rules
(`docs/product/15-operator-console-design.md:699-719`), not invented for the guide:

1. Name things as the operator controls them, in their own vocabulary; show the exact fields
   beneath, in mono, because they are also true.
2. Never call a forward write an undo.
3. Say what is not modelled.
4. An empty screen is an invitation, and a broken one is a diagnosis.
5. Churn is labelled churn.
6. The word for the thing stays the same everywhere: the vocabulary in
   `docs/product/17-product-spec.md` §3 is the vocabulary here, including in headings.

Three more, for prose specifically:

7. Second person, present tense, short sentences, one idea at a time.
8. No marketing. Nothing is powerful, seamless, intelligent or effortless. Lead with the
   concession, on every page that has one.
9. Identifiers in mono, always whole: a prompt, a label, a cron line or a memory is shown
   complete, never paraphrased and never elided with `…`. No em-dashes in prose; use a comma, a
   full stop or a colon instead.

Ellen runs a bookshop in Bristol, from `go/orgprompts/interviewer.md`'s worked example. She is the
running example on every page from "Your first hour" on.

## Front-matter contract

```yaml
---
title: A worker's instructions
slug: a-workers-instructions
part: 1            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 3
surfaces: [workers, worker-configuration]   # console surfaces whose About line uses this page
---
```

`surfaces` is the closed set in `web/src/guide/surfaces.ts`: `desk`, `chat`, `workers`,
`worker-configuration`, `worker-triggers`, `worker-history`, `memory`, `activity`, `chart`,
`settings`, `onboarding`, `project-create`. A page with no console surface (page 0, the glossary,
the operator page) uses `surfaces: []`. Screenshots live at `docs/guide/img/<slug>-<n>.png`; until
captured, a page carries `<!-- screenshot: <what it should show> -->` instead.
