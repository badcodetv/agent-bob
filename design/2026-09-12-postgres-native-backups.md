# Postgres-native backups, and what that deletes from the LVM plan

An adversarial filter Kai put on `design/2026-09-11-ovh-compose-hosting.md` on 2026-09-12:

> *"It feels like a lot of layering and block virtualization, as opposed to saying every day we take
> a complete snapshot. What we really care about the backup for is Postgres… surely there's a more
> Postgres-native backup solution. Ideally we would only really use Postgres and object storage.
> I'm keen to understand if write-ahead logs is a way we could do this."*

**Verdict: adopt it.** The layering is not incidental complexity that a Postgres-native tool would
sit beside — the layering exists *only* to make a live database safely copyable. Take the database
out of the file-copy problem and the thin pool, the snapshot scripts and the pool-full alarm all
lose their reason to exist. This is a simplification, not an addition.

Written 2026-09-12. Nothing here is built. `docs/ops.md` is still the plan of record until Kai says
otherwise; §6 lists exactly what changes.

---

## 1. Write-ahead logs — what they are, and why they beat hourly snapshots

Postgres never writes a change straight into its data files. It writes it first into the
**write-ahead log** (WAL), a sequential record of every change, and only later does a background
process apply those changes to the real files. That is why Postgres survives a power cut: on
restart it replays the WAL and reconstructs whatever had not landed yet.

The backup trick is to **keep the WAL instead of throwing it away.**

```
  one base backup ─────────────────────────────────────────────▶ time
  (Sunday 03:00)   ├─ WAL ─┼─ WAL ─┼─ WAL ─┼─ WAL ─┼─ WAL ─┤
                                        ▲
                        restore the base, replay the WAL up to HERE
                        = the database exactly as it was at 14:32:07
```

Two settings do it:

- `archive_mode = on` plus an `archive_command` — each time a WAL segment fills (16 MB by default),
  Postgres hands it to a command that copies it into object storage.
- `archive_timeout = 60s` — force a segment to close every minute even if it is not full, so the
  furthest behind the archive can ever be is about a minute.

**What that buys, against the current plan:**

| | Hourly LVM snapshot → restic | Base backup + WAL archiving |
| --- | --- | --- |
| Worst-case data loss (RPO) | **1 hour** | **~1 minute** (`archive_timeout`), or seconds with a streaming receiver |
| Restore target | the top of an hour, and only those hours | **any moment**, to the second — "just before the bad `DELETE`" |
| What a restore gives you | the database as if the power were cut | a cleanly recovered database at a chosen transaction |
| Is the backup verifiable? | only by restoring the whole volume | the tool verifies its own repository, and can restore one database |
| Understands Postgres | no, it photographs blocks | yes — it knows about tablespaces, timelines, checksums |
| Bytes moved per cycle | the changed blocks of a whole volume | the changed *database* blocks, plus ~1 compressed WAL segment a minute |

Restoring to a chosen moment is called **point-in-time recovery (PITR)**. Block snapshots cannot do
it at all, at any cost. That is the capability being bought here, and it is the one that matters for
the failure we are actually likely to have: not a dead disk, but somebody or something writing
nonsense into a table.

**The honest caveat.** A base backup plus WAL is only as good as the WAL chain. Lose or corrupt a
segment in the middle and you can replay only up to the gap. This is why the tool below is chosen
for its *verification* features rather than its backup features, and why §5's drill is compulsory.

---

## 2. The tool: pgBackRest

Three real candidates. All three back up to Google Cloud Storage and all three do PITR.

| | **pgBackRest** ✅ | WAL-G | Barman |
| --- | --- | --- | --- |
| Written in | C | Go | Python |
| GCS support | native (`repo1-type=gcs`) | native | via `barman-cloud-*` |
| Full / differential / incremental | all three, plus block-level incrementals | full + delta | full + incremental |
| Repository **verification** | `pgbackrest verify` + `pgbackrest check` | thin | moderate |
| Repository encryption | built in (AES-256) | via cloud-side | via cloud-side |
| Parallelism | yes, `process-max` | yes | limited |
| Restore only changed files | yes, `restore --delta` | no | no |
| Shape it expects | runs beside PGDATA | runs beside PGDATA | usually a separate Barman host |

**Recommend pgBackRest.** The deciding feature is not speed, it is that it will tell you its
repository is intact without a full restore (`verify`) and that archiving is actually working
(`check`). Our current plan's single biggest weakness, stated in `docs/ops.md` 1.5, is *"the
unproven part is our own ~25-line script"*. pgBackRest replaces that script with software that a
large number of people restore from every day, and it replaces "we believe it works" with a command
that answers the question.

**Encrypt inside pgBackRest** (`repo1-cipher-type=aes-256-cbc`), exactly as restic does today, so
the bytes in Google are unreadable without a passphrase held only in the password manager. Same
rule as now: **lose the passphrase, lose every backup.**

### The shape on the box

pgBackRest wants to sit next to the data directory as the `postgres` user. Since Postgres runs in a
container here, build one small image:

```
FROM <the chosen postgres image>
RUN apt-get update && apt-get install -y pgbackrest && rm -rf /var/lib/apt/lists/*
```

- `archive_command = pgbackrest --stanza=box archive-push %p`, with `archive-async=y` so a slow
  upload to Google can never stall a commit.
- Backups are triggered from the **host**, by systemd timers doing `docker exec` — the same timer
  pattern `docs/ops.md` step 9c already uses, so that machinery survives unchanged.
- Schedule: **full weekly, differential daily, incremental hourly**, plus continuous WAL. Retention
  by `repo1-retention-full`, which also expires the WAL that is no longer reachable.

---

## 3. "The only local state is the Postgres disk" — how true is it?

This is the load-bearing claim. If it holds, §1's argument is decisive. Measured today:

| State | Where it lives now | Is it really only Postgres? |
| --- | --- | --- |
| **Agent Bob** | sessions + the whole product layer in Postgres; artifacts, datasets and session snapshots in GCS; images in Artifact Registry | ✅ **already exactly this shape.** With Step 11d's `.env`, `agentd-data` is near-empty and `dind-data` is a rebuildable image cache |
| **Caddy** | certificates and the ACME account on disk | 🟡 not Postgres — but **re-issuable from Let's Encrypt in seconds**. Losing it costs a re-issue, not data. Watch the rate limits on a mass re-issue |
| **Each app's `.env`** | a file on disk | 🟡 not Postgres — but these belong in the password manager as the primary copy anyway, which is already the rule |
| **badcode / bookingsystem / forum / kellie / quoteright / panwww** | Postgres, plus a 30 KB forum volume | ✅ effectively yes |
| **n8n** | workflows and credentials | 🔴 **must be checked per install.** n8n defaults to **SQLite on disk**; it only lives in Postgres if explicitly configured. If it is SQLite today, that is a file-on-disk database — exactly the thing the block-snapshot design existed for |
| **Redis** | 1.8 MiB, used as a cache | ✅ treat as rebuildable; do not back it up |
| **Elasticsearch ×2** | 1 GiB between them | ✅ **out of scope** — both belong to NoCode and Franchise Cloud, shut down the week of 2026-09-14. This is what makes "only Postgres" reachable at all |

**Conclusion: the claim holds, with one open item (n8n) and two things that are not Postgres but are
cheap to lose (Caddy's certs, and `.env` files whose real home is the password manager).**

So the residue after pgBackRest is a few megabytes of near-static configuration files. Those do not
need an instant copy-on-write snapshot; a nightly `restic` of `/srv/apps` — excluding every data
directory — is plenty, because nothing is writing to them while it runs.

**Rule to carry into every app migration:** *an app moves to the box only once its state is
Postgres plus object storage, or is demonstrably rebuildable.* If it is neither, that is migration
work, not a backup problem.

---

## 4. "Will it actually be fast?" — yes, and by more than you might think

Measured on the GKE cluster today:

- **The databases are on spinning disk, over the network.** Every volume uses the `standard`
  storage class, which is `kubernetes.io/gce-pd` with `type=pd-standard`
  (`kubectl get storageclass standard -o jsonpath='{.provisioner} {.parameters.type}'`). That is a
  network-attached HDD. Typical latency is measured in **milliseconds**.
- **The box's disks are local NVMe.** Latency measured in **tens of microseconds**. For the random
  small reads a database does, this is a one-to-two-orders-of-magnitude difference, and it is the
  single biggest performance fact in this whole migration.
- **CPU:** 2 shared vCPUs today, for eleven apps, at 27% (`kubectl top node`). The box is 16 real
  cores / 32 threads, and Postgres can use them for parallel query.
- **RAM:** 16 GB today at 68% used, with the shared Postgres holding 2.5 GB. The box has 128 GB.
  **The entire 22.7 GiB database fits in RAM roughly five times over**, so after warm-up most reads
  never touch a disk at all.

So the expected outcome is not "a bit faster". It is "the working set lives in memory, and the
misses hit NVMe instead of a network HDD".

Tuning to set at install, in one place, rather than discovering later: `shared_buffers` ~25% of RAM,
`effective_cache_size` ~60–75%, `work_mem` sized per-connection against the concurrency, and
`max_parallel_workers` against the core count. All boring, all documented, none of it clever.

---

## 5. Confidence: the drill is the only reason to believe any of it

Unchanged in spirit from `docs/ops.md` step 13, but now it proves something stronger. Weekly, on a
timer, reporting to healthchecks.io:

1. `pgbackrest check` — is archiving actually reaching Google right now?
2. `pgbackrest verify` — is the repository internally consistent?
3. **Restore to a point in time into a scratch directory**, start Postgres on it, run a counting
   query, and report the elapsed seconds.
4. Additionally, and this is the new capability: pick a moment *between* two backups and prove the
   restore lands there. That is the test that the WAL chain is whole, and it is the thing an hourly
   block snapshot could never be asked.

**Write the measured restore time into the design's §4.5**, as the current plan already requires.

---

## 6. What this deletes

If Kai says go, these stop being needed. Listed so the change is a decision, not a drift.

| In `docs/ops.md` today | Becomes |
| --- | --- |
| Step 4, the **LVM thin pool** (`lvcreate --type thin-pool`, autoextend, metadata sizing) | **deleted.** Plain ext4 on `/srv`. Keep plain LVM only if per-app resizing is wanted; drop the thin pool and snapshots either way |
| `new-app-volume`, one virtual disk per app | **deleted.** Apps become plain directories under `/srv/apps/<app>/` |
| `box-backup` — hourly snapshot → mount → restic → drop | **deleted.** Replaced by pgBackRest's own schedule |
| `box-pool-check` — the 80%-full thin-pool alarm | **deleted.** No thin pool, so the "every app goes read-only at once" failure mode disappears with it |
| `box-prune`, `box-check` — restic retention and read-verify | **replaced** by `repo1-retention-*` and `pgbackrest verify` |
| `box-drill` | **kept and strengthened** — §5 |
| restic | **kept, demoted**: one nightly pass over `/srv/apps` config files, no snapshot needed |
| §1.5's "the unproven part is our own ~25-line script" | **gone.** That was the plan's weakest sentence |
| §1.8's "LVM freezes the filesystem — believed, no primary source" | **moot.** Nothing depends on it any more |

Net: **one well-known tool and one drill, instead of a thin pool plus five bespoke scripts.** Worst-
case data loss improves from 1 hour to about 1 minute, and restore gains a capability it did not
have.

**What is given up:** the *app-agnostic* property. The block design did not need to know what was
inside a volume; this one does. Kai's answer is the right one — if the answer is always "Postgres
and object storage", then knowing is free. §3's rule is what keeps that true, and n8n is the first
test of it.

---

## 7. Decisions this needs from Kai

1. **Adopt this, replacing the LVM design?** (Recommend yes.) It changes Step 4 and Step 9 of
   `docs/ops.md` before they are ever run, which is the cheapest possible moment.
2. **One Postgres instance for every app, or one per app?** Recommend **one instance, one database
   per app**: one backup configuration, one set of tuning, best use of 128 GB. The cost is a shared
   upgrade window and a shared blast radius. Per-app instances are the isolation-first answer and
   are affordable on this hardware — this is a real trade, not a formality.
3. **Which Postgres, and which extension bundle?** The ask is "one Postgres with vector, geospatial,
   document and search extensions available". Two routes:
   - **A: stock Postgres + extensions from packages** (pgvector, PostGIS, and `pg_search` if
     full-text is wanted). Security patches arrive on Postgres's own schedule. Recommend this for
     the instance holding the booking system.
   - **B: the ParadeDB image**, which is what badcode already runs (`paradedb/paradedb:v0.21.0-pg17`)
     and which bundles search, analytics and vectors. Convenient, but it puts a third party's
     release cadence between us and a Postgres security patch on a database holding paying
     customers' data.
   Either way, **the major version is pinned by whichever extension lags most** — check current
   support before choosing a number, do not assume the newest release is usable.
4. **Is n8n on SQLite or Postgres?** (§3.) If SQLite, it needs converting during its migration, or
   it is the one app that still wants a file-level backup.
5. **OVH's Backup Agent** as a cheap second, whole-server, daily copy — still open from
   `design/2026-09-11-ovh-compose-hosting.md` §10 Q4. It matters slightly more now: with the thin
   pool gone there is one fewer local safety net, though Google still holds the real backups.
