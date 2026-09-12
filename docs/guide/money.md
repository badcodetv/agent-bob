---
title: Money
slug: money
part: 3            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 14
surfaces: [settings]
---

Every project on this box draws from one shared API key. There is no per-user billing and no Stripe. A daily budget is meant to stop one project overspending, low by default until someone raises it.

## In the console

Settings, top tier, the first thing you see. <!-- true after A5 --> It shows today's spend against the limit, the last 7 and 30 days beside it, and one sentence for whether you are billed to the API key, running on the subscription, or on a mock model that costs nothing.

## The smallest useful thing you can do

Check today's spend on the Settings page before you leave a schedule running over a weekend.

## Ellen's example

<!-- fixture: to be captured by B7 --> A real week of the newsletter loop's spend, shown against the limit.

## The trap

A schedule resets the hop limit that caps a runaway chain of workers waking each other, so a two-worker loop that keeps waking itself is exactly what this budget exists to catch. <!-- true after A3 --> Chat is exempt by design: a blown budget can never lock you out of talking to your own workers. <!-- true after A3 -->

Everyone on the project can see the number; only the operator can change it. <!-- true after A5 and A3 --> The two counters reset at midnight, stack-local time. <!-- true after A3 -->

## What this will not do

It will not stop chat, split cost by person, or know what a run cost before the model reports it. The number shown comes straight from the model's own usage reports, and when none is reported the page says so instead of showing zero. <!-- true after A4 -->

Default daily limit: **100,000 tokens a day**, with a warning at 50,000. Every new project starts there. It is deliberately low — enough for a daily architect run and a handful of jobs, and low enough that two workers waking each other in a loop stop within minutes rather than overnight. If your project needs more, that is a normal thing to ask for: the operator raises it, and the change lands in the changelog like any other.

## Next

Back: [What Bob will not do](what-bob-will-not-do.md). Forward: [Field notes](field-notes.md).
