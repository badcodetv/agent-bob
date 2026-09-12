---
title: Inviting someone
slug: for-operators/inviting-someone
part: 9            # 0 start here · 1 levers · 2 reading · 3 architect · 4 field notes · 9 reference
order: 2
surfaces: []
---

This page is for Kai, not for the person being invited. It is the runbook for adding a friend to
the box: what to set before you send the link, and the one paragraph you send with it.

## In the console

There is no button for this page; inviting someone is editing a file on the server, not a form in
Bob. See `docs/ops.md` §11d for the exact commands.

## The smallest useful thing you can do

Before the first invite: set `AGENTKIT_DEFAULT_DAILY_TOKENS_HARD` (and `_SOFT`) in
`/srv/apps/bob/src/.env` so a brand-new project starts with a spend brake, not with
`daily_tokens_hard=0` (off). An existing project's own row is never touched by changing these.

## The message you send

One paragraph, every time:

> This runs on my own Anthropic key with a small daily token budget, so an unattended job that
> goes wrong stops itself rather than running up an unbounded bill — tell me if you need more
> room. Once you approve your project's charter, an automatic "architect" edits it once a day on
> its own: it creates workers, writes memories and wires triggers without asking either of us
> first, and what it did shows up afterwards for you to keep, edit or revert. Your project's
> containers run on a shared box without the isolation a system holding customer data would need,
> so I'm only sending this to people I trust with that. If that's all fine, here's your link:
> `<url>`.

The third sentence is not boilerplate: while Bob is the only thing on this box, its
Docker-in-Docker runs privileged, which endangers only Bob — but an invitee's sessions run inside
those containers (`design/2026-09-11-ovh-compose-hosting.md:288`). Send this to people you trust
with that, not a general invite link.

## The trap

Adding someone is editing `/srv/apps/bob/secrets/projects.json`; the map reloads on its own on
`AGENTKIT_PROJECT_MAP_RELOAD` (default 60s) or immediately on `SIGHUP` — never restart `agentd` to
add a user. There is also no dedicated route to delete a project: removing it from the map file
revokes login, but its sessions, workers and memory are not separately purged, so a throwaway
project used only to check the spend brake (`docs/ops.md` §11f, OM-8) should hold nothing you would
mind existing.

## What this will not do

It will not create the project for you (you still edit a file), split the shared key's bill by
person, or stop an invitee's job the moment a budget is crossed — the hard limit stops the *next*
non-interactive job from starting, not the one already running.

## Next

Back: [Glossary](../glossary.md). Forward: [What Bob is](../what-bob-is.md).
