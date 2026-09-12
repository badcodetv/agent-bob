# Concern: session state is Docker layers, and nothing retires them

**Status: a concern, deliberately not a plan.** Raised by Kai on 2026-09-12 while deploying Agent
Wolf (`design/2026-09-12-wolf-deployment.md`), and parked by him in the same conversation: *"what I
don't really want to do right now is go into a big engineering hole because of this."* Nothing here
proposes a design. It records what the mechanism is, what it costs, and what a better story would
have to answer, so that whoever picks it up does not start from scratch.

Every claim below is a file or a line that was read on 2026-09-12.

---

## 1. How session state works today

A session is a container. Bob's archive loop snapshots any session idle longer than
`AGENTKIT_SESSION_IDLE_TIMEOUT` (default 30m) and drops the container, freeing its memory and one
of the host's 100 ports. The **session row survives**: the next message restores it and the
conversation continues (`go/cmd/agentd/gc.go`).

The snapshot is a **`ContainerCommit` of the whole container** (`go/execenv/docker/dind.go:354`),
stored by `Persist` at `<registry>/<session-id>:latest`
(`go/imageregistry/ociregistry/ociregistry.go`, `remoteRef`). Shared base layers deduplicate;
what is uploaded is that session's own diff.

**So the persistence guarantee is the strongest one available, and it is the only one offered:**

> Any file written anywhere in the filesystem is there when you come back.

There is no scratch tier, no declared-durable path, no per-session or per-worker way to say "I
need none of this". A session that only wants a clean environment from its image pays the same
storage as one holding a week of irreplaceable work.

## 2. What accumulates, and why nothing stops it

Each session owns a repository. Re-archiving overwrites the same `:latest` tag, so a session holds
**one tagged image plus one untagged leftover digest per archive/restore cycle**.

There is a retirement policy in the codebase and it is a good one:
`project_settings.snapshot_ttl_days` (default 30, `0` = never), enforced by `SnapshotReaper`,
which defers the reap while an image is still being launched from, and **tombstones** rather than
erasing — history, provenance and the version high-water mark all survive
(`go/snapshot_reaper.go`).

**It does not apply to session snapshots.** `SnapshotReaper` drives off
`ListCustomImageVersions` / `MarkCustomImageReaped` — the **named image catalogue** that agents
burn versions into. Idle-session archive snapshots are not catalogue rows, and no sweep visits
them. `Registry.Remove` has exactly **one** non-test caller in the whole module,
`go/snapshot_reaper.go:272`, and it is that sweep.

> **Nothing in Agent Bob ever deletes an idle-session snapshot, on any registry backend.**

On `blobarchive` (the default, `go/cmd/agentd/backends.go:92`) they accumulate as local blobs — on
a laptop, invisibly. On `ociregistry` they accumulate in Artifact Registry, where the deletion
half is explicitly out of band: `Remove` returns `nil` without deleting
(`go/imageregistry/ociregistry/ociregistry.go:268-274`).

This was not a decision anyone made badly. Local disk hid it; moving to a registry only made the
existing gap visible and billable.

## 3. Why Agent Wolf is what exposed it

Bob alone creates a session when a human chats to it. **Wolf creates one per live hypothesis per
day, forever, at 06:00 UTC** — that is the product working correctly
(`design/2026-08-20-agent-wolf.md`; `README-stack.md` § "Cost"). Every one is eventually archived.
Storage therefore grows with *elapsed time on the box*, not with how much anyone uses it.

And most of what it stores is worthless: the researcher's diff is market-data CSVs it would
happily refetch.

## 4. Kai's framing, kept in his terms

Three separate concerns came out of the conversation, and they are worth keeping separate:

**(a) Retention.** *"How long do your Docker image layers exist for? There needs to be a policy
around that."* Storage is unbounded in time. This is the immediate, billable one.

**(b) Archiving, not just deletion.** *"When you go into a session and say, look, we need to tidy
up — how are we going to tidy up where we don't actually lose very important context that you
really might have needed?"* A sweep that deletes is not the same as an archiving policy with a
stated restore window. Kai suggested **90 days of full restore** as a shape that sounds
manageable. Note that the conversation and its events live in Postgres, not in the image, so a
retired session is still *readable*; what is lost is its filesystem.

**(c) Whether commit-layers are the right mechanism at all.** *"I do wonder whether I have
over-complicated things... using Docker image layers as the core state mechanism for sessions and
their file system. For some scenarios it may well be, but I think we should be very explicit about
that."* The alternatives he named: object storage for what needs to persist, or sessions that
carry no local state and are configured purely by their image. He also named the risk of
answering this too eagerly, which is why it is parked.

The thing all three have in common: **the guarantee is currently implicit.** Nobody chose "every
byte, forever" — it is what commit-and-push happens to do.

## 5. What a better story would have to answer

Not proposals. The questions any design here must not dodge:

1. **Is there more than one tier?** Today there is exactly one. A *durable* path and an
   *ephemeral* remainder would remove most of the volume at source — but somebody must decide
   which files are which, and an agent writing to the wrong one loses work silently.
2. **Who declares it — the project, the worker, or the session?** Wolf's daily researcher and
   Wolf's interview session want opposite answers, in the same project.
3. **What does expiry actually do?** Delete, or degrade to transcript-only? The second is nearly
   free (Postgres already holds the conversation) and is probably what "archived" should mean.
4. **What tells a human before it happens?** The existing reaper logs a deferral loudly and
   never reaps silently. Whatever replaces it should keep that property.
5. **What happens to a session someone comes back to after the window?** `ErrCustomImageReaped`
   is the catalogue's answer and it is a hard failure. A session needs a softer one.

## 6. The stopgap, which is not the fix

An Artifact Registry cleanup rule — untagged digests, and session repositories older than the
window. It is a settings page, not code, and it bounds the bill without answering anything in § 5.
Thread 01 is holding it for Kai's yes
(`design/2026-09-12-gke-to-box-migration.md` § 6 item 2).

⚠️ If that rule is written, note that it deletes bytes **behind Bob's back**: a session whose image
is gone will fail to restore with a registry error, not with a clean "this was archived" message.
That is the § 5 item 4 problem arriving early, and it is the price of a stopgap.

## 7. Explicitly not being done now

No tiering, no new storage backend, no change to the archive loop, no change to
`SnapshotReaper`'s scope. Parked by Kai, 2026-09-12.
