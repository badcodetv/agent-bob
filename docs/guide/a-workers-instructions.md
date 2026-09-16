---
title: A worker's instructions
slug: a-workers-instructions
part: 1
order: 3
surfaces: [workers, worker-configuration]
---

A worker is a set of instructions plus something that wakes it. The instructions are a plain
text prompt, the same kind of thing you'd write for a person starting a new job. Every time the
worker runs, it starts from that prompt and remembers nothing from any earlier run except what
somebody wrote down as a memory. Changing the prompt is the main way you change what a worker
does.

## In the console

Workers → click a worker → Configuration. The prompt is a plain text box;
saving requires a reason. → `#/guide/a-workers-instructions`

## The smallest useful thing you can do

Open a worker, read its prompt end to end, and add one
sentence that fixes something you noticed it doing wrong. Type a reason in **Why?** and save.

## Ellen's example

Ellen added one sentence to `newsletter-writer`'s prompt: "Mention the signing or event of the
week first if there is one." Why?: "the first draft buried the signing at the bottom".

## The trap

Every worker starts each run remembering nothing at all
(`go/orgprompts/interviewer.md` §3). A required reason becomes that change's entry in the
changelog, next to who made it: "Required. One line, stored with the change in the config log —
the changelog reads it next to who made it." (`web/src/components/WorkerEditor.tsx:387-389`).
Saving changes the *next* job, never one already running. Nothing stops one worker's prompt from
being rewritten by another except freezing it.

A worker called `ls1-reviewer` reads poems written by a worker called `ls1-poet`. The poet's
poems came out like this, with no title:

```
Soft rain on slate.
A gutter hums to itself.
The street goes silver.
```

The reviewer noticed the pattern was systemic, not a one-off, and rewrote the poet's prompt with
one added rule, giving the reason "the last poem shipped without a title line"
(`e2e/mock-scripts/learning-stories.json`, `e2e/features/learning-stories.stack.spec.ts:308`).
The very next poem from the same worker, same trigger, opened like this:

```
Title: Rain on Slate
Soft rain on slate.
A gutter hums to itself.
The street goes silver.
```

No human touched anything, and no code was deployed. One worker read another's output, decided
it was wrong, and edited the instructions with a reason attached.

## What this will not do

A prompt cannot see or restore what it looked like ten changes ago from
this screen; that history lives in the changelog, not here. It does not stop a worker from being
rewritten mid-run; a change lands on the next job only. It does not grant a worker any new tool or
capability; a prompt can only ask for what the worker was already given.

## Next

Back: [The goal and the charter](the-goal-and-the-charter.md). Forward:
[The rulebook](the-rulebook.md).
