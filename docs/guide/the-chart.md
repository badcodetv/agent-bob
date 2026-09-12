---
title: The chart
slug: the-chart
part: 2            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 11
surfaces: [chart]
---

The chart draws every worker in your project as a name plate, and every event or clock that wakes
one as a wire running between plates. It is a wiring diagram of who wakes whom, not an org chart
of who reports to whom. A frozen worker is drawn double-ruled, and a schedule is a small clock
face docked beside the plate it wakes.

## In the console

Chart. It appears once your project has a second worker, or its first wire.
One worker alone has nothing to wire, so there is nothing to draw yet. → `#/guide/the-chart`

## The smallest useful thing you can do

Drag one worker's plate onto another, or use its menu,
to propose a wire between them. Read the sentence and the exact event type the card shows, type
why, and submit.

## Ellen's example

The wiring the architect actually built on its first run, read back from the project: two plates,
`newsletter-writer` and `newsletter-archivist`. A clock wakes `newsletter-writer` on `0 8 * * 1`
(every Monday at 08:00) with "Draft this week's newsletter." A wire from `worker.finished` wakes
`newsletter-archivist`, filtered to `{"worker":"newsletter-writer"}` so it only wakes on that one
worker's completion, not its own.

## The trap

Every gesture here asks before it acts: dragging a wire open, cutting one, and
freezing a worker each open a card that states the exact fields in plain mono type and requires a
reason before anything is written. There is no drag that saves on its own. Even the one action
that fires something real, sending a test event into the project so you can watch the wires light
up, says so plainly first: "Emit a real event?" (`web/src/components/EmitEventControl.tsx:118`.)
Cutting a wire is never called undoing; it is a new record, and the worker it fed keeps every
other way it is still woken.

## What this will not do

It does not let you edit a schedule here; click its clock face and you
go to that worker's Triggers, where the schedule actually lives. It does not remember how you laid
it out: panning and zooming live only in your browser while you look, so two people looking at the
same project see the same chart, and a chart that changed shape means the organisation changed
shape. It does not draw a hierarchy: there is no top of this chart, only wires.

## Next

Back: [Memory](memory.md). Forward: [The architect](the-architect.md).
