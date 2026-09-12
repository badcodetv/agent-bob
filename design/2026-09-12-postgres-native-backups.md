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
| **n8n** | Postgres, with a Redis queue | ✅ **resolved 2026-09-12 — it is on Postgres, not SQLite.** `DB_TYPE` + `DB_POSTGRESDB_*` are set on the deployment and it mounts no volume at all. Nothing to back up beyond the database. Whether it is *wanted* is a separate question — §3a |
| **Redis** | badcode's is ephemeral (no volume); the shared one holds 1.8 MiB | ✅ treat as rebuildable; do not back it up. Referenced by bookingsystem, quoteright and panwww — §3a |
| **Elasticsearch ×2** | 1 GiB between them | 🔴 **NOT simply out of scope — see §3a.** Both clusters belong to NoCode and Franchise Cloud, but **the booking system's secret carries `ELASTICSEARCH_SERVICE_HOST` and `ELASTICSEARCH_INDEX_PREFIX`**, so the app that migrates *last* may depend on a cluster that is switched off *first* |

**Conclusion: the claim holds.** The one thing that looked like a counterexample (n8n on SQLite) is
not one. What is left that is not Postgres is Caddy's re-issuable certificates and `.env` files
whose real home is the password manager.

---

## 3a. What the datastore audit actually found

Read 2026-09-12 from each app's `appsecrets` — **key names only, never values**:

```sh
kubectl get secret -n <ns> appsecrets -o json | jq -r '.data | keys[]'
```

| App | Postgres | Redis | Elasticsearch |
| --- | --- | --- | --- |
| **bookingsystem** | ✅ `POSTGRES_SERVICE_HOST`, `POSTGRES_DB` | ✅ `REDIS_SERVICE_HOST` | 🔴 **`ELASTICSEARCH_SERVICE_HOST`, `ELASTICSEARCH_INDEX_PREFIX`** |
| **quoteright** | ✅ `POSTGRES_SERVICE_HOST` | ✅ `REDIS_SERVICE_HOST` | — |
| **panwww** | ✅ `POSTGRES_SERVICE_HOST` | ✅ `REDIS_SERVICE_HOST` | — |
| **forum** | ✅ `POSTGRES_HOST` | — | — |
| **badcode** | ✅ `POSTGRES_HOST` (its own ParadeDB) | its own ephemeral redis, for n8n's queue | — |
| **kellie** | **none at all** | — | — |

Three consequences, in order of how much they matter:

### 🔴 The booking system appears to use Elasticsearch

This is the one finding that reorders work. The decision was "NoCode and Franchise Cloud are not
coming with us, so we do not need Elasticsearch". But the two Elasticsearch clusters live in
`nocode-elasticsearch` and `franchisecloud-elasticsearch`, and **the booking system's secret points
at an Elasticsearch host**. The booking system is the app with paying customers and the last one
scheduled to move; NoCode and Franchise Cloud are switched off the week of 2026-09-14, first.

**A present environment variable is not proof of a live dependency** — it may be dead configuration
from a feature that was removed. But if it is live, then switching off NoCode or Franchise Cloud
breaks search in the booking system *before* any migration begins, and the replacement (Postgres
full-text search good enough to replace Elasticsearch 7.5.2) moves onto the booking system's
critical path rather than being a nice-to-have.

**Action: establish whether it is live before 2026-09-14.** Cheapest test is to search in the
booking system's UI and see whether results come back.

### n8n: on Postgres, but probably not wanted

`DB_TYPE` and `DB_POSTGRESDB_*` are set, `EXECUTIONS_MODE` is queue-based against Redis, and there
is no volume. So it is backed up by whatever backs up its database, and it needs no special care.

Whether to carry it is a different question, and the repository argues against it: BadCode's own
`docs/superpowers/specs/2026-06-02-comic-asset-tooling-design.md:11` describes the previous
`storyteller` project as *"a heavy stack — a Go API, a job queue, Postgres, n8n, and a web UI"* and
says **"that weight is exactly what we are shedding."** Nothing in the current badcode repository
references n8n in code — the only mentions are that sentence and this thread's own notes.

**Action: open `n8n.badcode.tv`, look at the workflow list and the executions log.** Empty or stale
means drop it, and the badcode Redis goes with it, since its only visible job is n8n's queue.

### Redis: three apps reference it, none stores anything

badcode's Redis has no volume at all, so it is already pure cache. The shared one holds 1.8 MiB in a
20 GiB disk. Nothing here is a system of record. **Recommendation: run one small Redis on the box
for whoever genuinely needs a cache or a queue, back it up never, and drop it entirely if dropping
n8n removes the last real user.** Check the three referencing apps the same way — a key in a secret
is not a dependency.

### Two naming conventions, for whoever writes the migration script

Older apps use `POSTGRES_SERVICE_HOST` / `POSTGRES_DB`; newer ones use `POSTGRES_HOST` /
`POSTGRES_DATABASE`. Worth normalising while rewriting each `.env` by hand anyway.**

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

## 7. Decided by Kai, 2026-09-12

1. ✅ **Adopt pgBackRest, replacing the LVM snapshot design.** *"pgBackRest sounds like a good plan,
   because it sounds like exactly the kind of native tool that we need."* §6's deletions are
   therefore approved; `docs/ops.md` Steps 4 and 9 are to be rewritten before either is ever run.
2. ✅ **One Postgres for everything.** *"One Postgres is the goal… it's fine for us to handle the
   migration of existing Postgres databases into our new one-Postgres setup, that's okay, we're
   just going to do the migration."* One instance, one database per app.
3. ✅ **Postgres 9.6 → route A, dump and restore, with a per-app migration script.** *"It's very
   reasonable that we write a migration script for all data… the booking system for example isn't
   huge."* Confirmed by measurement: 22.7 GiB across every database on the cluster.
4. ✅ **No Elasticsearch, no Redis if it can be helped, and probably no n8n.** *"What we're really
   trying to do is really strip things back to a really simple basics."* The audit in §3a says how
   far that can go, and names the one thing standing in the way (the booking system's
   Elasticsearch reference).
5. 🟡 **Rare exceptions get a hand-written procedure, not a design.** *"In the rare case that we've
   got like a Mongo or a Redis or something, I doubt that we will, but we can just write manual
   backup procedures for those and let's not worry about that in this moment."* Recorded so it is a
   decision rather than an omission.

### Still open

6. 🔬 **Which Postgres, and which extensions** — Kai asked for this to be researched rather than
   guessed: *"the best thing to do would be to have a fully featured Postgres setup using best
   practice."* The capability list he gave is document/JSON used like Mongo, full indexing,
   full-text search, vector search, **hybrid search**, geospatial, time series, and the ability to
   add extensions later. Research in progress; §8 holds the answer when it lands.
7. ✅ **OVH's Backup Agent — answered 2026-09-12: £0 agent + £0.0061/GB/month, so ~£0.20/month
   today. Recommend switching it on.** Nightly whole-server image to a distant OVH datacentre, 14
   days retention with **14 days immutability**, file-level or whole-server restore, and **free
   egress on restore**. Worth it as a second copy at a different company boundary from Google, and
   the immutable lock is a property pgBackRest's repository does not have. Details and the three
   caveats are in `docs/ops.md` 1.5 — the live ones are that **Eco/Rise eligibility is
   unconfirmed** (check the control panel after delivery) and that the agent is **public-IP only,
   incompatible with vRack**. Our mdadm RAID1 + ext4 layout is supported; Veeam does not back up
   LVM snapshots, which is one more small reason the thin pool is better gone.
8. 🔴 **Is the booking system's Elasticsearch dependency live?** (§3a.) Must be answered before
   NoCode and Franchise Cloud are switched off the week of 2026-09-14, because their clusters are
   the only two that exist.

---

## 8. Which Postgres, and which extensions — researched 2026-09-12

Kai asked for this to be researched rather than guessed, against the capability list he gave:
document/JSON used like Mongo, full indexing, full-text search, vector search, **hybrid search**,
geospatial, time series, and the ability to add extensions later.

Researched against primary sources on 2026-09-12, including a download-and-grep of the live
`bookworm-pgdg` package index rather than blog posts. Sections E–G (the hybrid-search query
pattern, the tuning table and the final stack) are still landing and go in §9.

### 8.1 Version: PostgreSQL 18

| Version | Released | Latest patch | EOL |
| --- | --- | --- | --- |
| **18** | 2025-09-25 | **18.6** (2026-08-11) | **2030-11-14** |
| 17 | 2024-09-26 | 17.11 | 2029-11-08 |
| 14 | 2021-09-30 | 14.24 | **2026-11-12 — two months away** |

**Take 18.** Four-plus years of runway, and two PG18 features land directly on the requirements:

- **`extension_control_path`** lets an extension live outside the server's own directories, so one
  can be added **without rebuilding the server image**. This is the direct answer to "possibly
  we're going to need to install new plugins as we go".
- **`io_method`** is a new asynchronous I/O subsystem (`worker` by default, `io_uring`, `sync`),
  which matters on local NVMe. PGDG's `postgresql-18` links `liburing2 >= 2.3`, so `io_uring` is
  genuinely available rather than theoretical.

🔴 **Do not take PostgreSQL 19 on GA.** It is at Beta 3 (2026-08-13) with GA expected within weeks.
A booking system with paying customers should not be the first load on a two-week-old major, and
extensions lag a new major by 1–2 months (§8.4).

### 8.2 The base image: the choice we thought we had does not exist

This plan previously asked Kai to choose between "stock Postgres plus packages" and "a pre-built
image such as ParadeDB's". **That is not a fork in the road.** The official image's own Dockerfile:

```
FROM debian:bookworm-slim
ENV PG_MAJOR 18
ENV PG_VERSION 18.6-1.pgdg12+2
  aptRepo="... http://apt.postgresql.org/pub/repos/apt bookworm-pgdg main $PG_MAJOR"
```

That version string is **byte-identical** to the one in the PGDG index. The official Docker image
*is* the PostgreSQL project's own Debian packages, with the repository already wired in.

**So: `postgres:18-bookworm` plus a short Dockerfile adding the extensions.** Security patches
arrive on the project's own cadence via `docker compose pull`; a new extension a year from now is
`apt install postgresql-18-<ext>`; and **no third party sits between us and a Postgres security
patch.** That was the stated objection to ParadeDB's image holding the booking system's data, and
this removes it while still allowing ParadeDB's *extension* to be installed on top.

### 8.3 Per capability

`PGDG ✅` = confirmed present in the live index. `Vendor` = absent, must come from the maintainer.

| Capability | Use | Version | Licence | Source |
| --- | --- | --- | --- | --- |
| **Document / JSON, used like Mongo** | **built-in JSONB + GIN** | core | PostgreSQL | nothing to install |
| **Geospatial** | **PostGIS** | 3.6.4 | GPL-2.0 | PGDG ✅ `postgresql-18-postgis-3` |
| **Vector search** | **pgvector** | 0.8.6 | PostgreSQL | PGDG ✅ `postgresql-18-pgvector` |
| **Time series** | **pg_partman + pg_cron** | 5.5.0 / 1.6.8 | PostgreSQL | PGDG ✅ `-partman`, `-cron` |
| **Full-text search** | **pg_search** (ParadeDB, BM25) | 0.25.9 | **AGPL-3.0** | Vendor `.deb` |
| **Hybrid search** | a SQL pattern, no extension | — | — | — |
| **Add extensions later** | PG18 `extension_control_path` | core | PostgreSQL | nothing to install |

Also worth taking from PGDG while building: `postgresql-18-pgaudit`, `postgresql-18-repack`,
`postgresql-18-hypopg`.

**On the document requirement: nothing needs installing.** Built-in JSONB with a GIN index gives
nested documents, flexible schema and indexed queries on arbitrary paths. One real tuning choice —
use **`jsonb_path_ops`** when containment queries dominate (index is 20–30% of table size) rather
than the default `jsonb_ops` (60–80%); `jsonb_ops` only earns its size when the query paths are
unknown.

**Rejected: FerretDB / DocumentDB**, the MongoDB wire-protocol layer. It drags in
`documentdb_core`, `pg_cron`, `pgvector`, `postgis` *and* `tsm_system_rows`. Only worth that
surface if an application literally speaks a Mongo driver that cannot be changed. Nothing of
BadCode's does.

### 8.4 The two decisions inside this

#### Time series: drop TimescaleDB (recommended), or take it from the vendor

🔴 **PGDG's `timescaledb` package is Apache-only**, verbatim from its own `Description` field:
*"This package contains the Apache-licensed version of timescaledb."* Missing: compression /
columnstore, continuous aggregates, retention policies, job scheduling, SkipScan and the advanced
hyperfunctions. **Nothing errors — the features are simply absent.**

| | **A — pg_partman + pg_cron ✅** | B — TimescaleDB from TigerData's repo |
| --- | --- | --- |
| Licence | fully permissive | Apache core + TSL (source-available) for the useful parts |
| Source | the Postgres project's own packages | a third-party apt repo |
| Version pinning | nothing to wait for | **becomes the extension that decides our major version** (34 days behind PG18 GA) |
| Conflicts | none | **incompatible with Citus**; only partially compatible with pg_partman |
| Gets you | partitioning + scheduled maintenance | compression, continuous aggregates, retention |

**Recommend A.** There is no time-series workload in the audit (§3a) to justify B, partitioning
plus a scheduled job covers the stated need at this scale, and A keeps every extension permissive
and on one release train. Adding TimescaleDB later is a package install; switching apt repos on a
live instance is not. **Take B only if compression or continuous aggregates are already known to
be needed.**

The licence is *not* the reason to prefer A. TSL bars offering the software itself as a database
service but explicitly permits "Value Added Products/Services" using it as a backend component, so
the booking system, forum and internal app are fine. ⚠️ One clause to watch if B is ever taken:
that permission is conditioned on users being *"prohibited, either contractually or technically,
from defining, redefining, or modifying the database schema"* — which would get awkward if Agent
Bob ever exposed a DDL surface to third parties. That is a reading of licence text, not a ruling.

#### Full-text search: pg_search (recommended), with ten minutes of legal

`pg_search` is the quality answer and it is **AGPL-3.0**. ParadeDB's own position is that
*"AGPL doesn't prohibit commercial deployment. You can run ParadeDB yourself for business purposes
without restriction"* — self-hosting for one's own product is permitted.

⚠️ The unsettled question is whether AGPL §13's network source-offer reaches **the application
code**. The standard reading is no — an extension and a client application communicating over a
wire are separate programs. Widely held, **not court-tested**. `pg_textsearch` (TigerData, April
2026, permissive) exists partly to sidestep exactly this, but **cannot do phrase queries**, which
probably disqualifies it for a forum. **Worth ten minutes of legal advice before paying customers'
data depends on it** — not a blocker now.

Built-in `tsvector`/`tsquery` remains fine for the small sites. Its weakness is ranking: no
corpus-wide inverse document frequency, so relevance ordering is poor. That is precisely what
would be noticed when replacing Elasticsearch.

### 8.5 Traps to design around, not discover

1. **`pg_cron` installs in exactly ONE database per cluster** (`cron.database_name`, default
   `postgres`). With one database per app this bites on day one. `cron.schedule_in_database()` is
   the workaround, but one database ends up owning all scheduling metadata — including
   pg_partman's maintenance job. **Decide which, at build time.**
2. **`pg_search` 0.25+ hard-requires pgvector** (it uses the `vector` type; `CREATE EXTENSION
   pg_search CASCADE` pulls it in). Convenient for hybrid search, but it couples pgvector upgrades
   to pg_search's expectations — and **Agent Bob already depends on pgvector.**
3. **Four extensions need `shared_preload_libraries`, i.e. a restart of the one instance every app
   depends on**: `pg_cron`, `pg_search`, plus `timescaledb` (list first, if taken) and
   `pg_textsearch` (if taken). **Plan the whole list up front.** ⚠️ Sources contradict each other
   on whether `pg_search` truly requires it — the current README says yes unconditionally, older
   0.17-era docs said unnecessary on PG17+. Assume required; the documented failure mode is a
   connection crash or a hang during index creation.
4. **`ALTER EXTENSION … UPDATE` must be run per database.** Easy to miss one on a four-app instance.
5. **`pg_search` write amplification.** Its index segments are immutable, so updating one field
   rewrites its neighbours, and background merges consolidate many small segments. Mutable segments
   and background merging mitigate it, but **a forum with heavy editing will feel it. Test on the
   real corpus at real write rates before switching Elasticsearch off.**

**Reassurance on version risk:** the PostgreSQL wiki's PG18 extension-bug page lists 140+ working
extensions and **none of our candidates are in its broken list**. pgvector and PostGIS track
Postgres *betas* — they were ready before PG18 shipped, not after.

### 8.6 🔴 The real cost of one Postgres is not extensions

One `shared_buffers`, one WAL, one restart, one out-of-memory event, one blast radius.

**A forum reindex evicts the booking system's hot pages. `work_mem` is applied per sort or hash
node rather than per query, so one runaway agent query can exhaust memory on the machine that
processes payments.**

This is the honest price of the consolidation Kai chose, and it is paid in configuration and
discipline rather than software. It does not argue against one Postgres — it argues for setting
per-app limits on day one rather than after the first incident. The concrete day-one configuration
is in §9.

---

## 9. The recommended stack, the configuration, and the day-one guardrails

Completes §8. Researched 2026-09-12. ⚠️ The configuration table below is **assembled** from the
PostgreSQL wiki's tuning page, Crunchy Data and EDB plus the PG18 documentation — it is not quoted
from one authority. Benchmark it; do not adopt it blind.

### 9.1 The stack

**PostgreSQL 18.6 on `postgres:18-bookworm`, plus a thin Dockerfile:**

| From | Packages |
| --- | --- |
| **PGDG apt** (the Postgres project's own) | `postgresql-18-pgvector` 0.8.6 · `postgresql-18-postgis-3` + `-scripts` 3.6.4 · `postgresql-18-cron` 1.6.8 · `postgresql-18-partman` 5.5.0 · `postgresql-18-pgaudit` · `postgresql-18-repack` |
| **Vendor `.deb`** | `postgresql-18-pg-search_0.25.9-1PARADEDB-bookworm_amd64.deb` |
| **Nothing to install** | JSONB + GIN (`jsonb_path_ops`) for documents · `extension_control_path` for later additions |

```
shared_preload_libraries = 'pg_cron,pg_search'
```

Time series is **native partitioning + pg_partman + pg_cron**. TimescaleDB Community, if a concrete
need for columnstore compression or continuous aggregates ever appears, comes **from TigerData's
packagecloud repo and never from PGDG** (§8.4).

### 9.2 Hybrid search: there is no native hybrid search, and `pg_search` does not fuse for you

ParadeDB's own guide confirms Reciprocal Rank Fusion is implemented by hand. RRF converts *ranks*
into scores rather than trying to normalise BM25 scores against cosine distances, which are not on
comparable scales. `k = 60` is the convention.

```sql
WITH
fulltext AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY pdb.score(id) DESC) AS r
  FROM documents WHERE content ||| 'search_term' LIMIT 20      -- ||| is pg_search's operator
),
semantic AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY embedding <=> '[vector]') AS r
  FROM documents LIMIT 20
),
rrf AS (
  SELECT id, 1.0/(60+r) AS s FROM fulltext
  UNION ALL
  SELECT id, 1.0/(60+r) AS s FROM semantic
)
SELECT m.id, SUM(s) AS score, m.content
FROM rrf JOIN documents m USING (id)
GROUP BY m.id, m.content ORDER BY score DESC;
```

🔴 **The pitfall: keep `LIMIT` *inside* each CTE.** Compute `ROW_NUMBER()` over the whole table and
limit afterwards, and Postgres must evaluate the window function over every matching row before
applying the limit — a full scan that throws away both indexes.

Weighting, if the two halves are weighted rather than summed equally: guidance found suggests
leaning ~0.7 toward the vector side for prose, dropping to 0.3–0.4 when users type exact
identifiers. **Booking references want the low end; forum prose the high end.**

### 9.3 Configuration for 128 GB / 32 threads / NVMe

**Note first: at ~24 GB of total data, a 32 GB `shared_buffers` caches the entire estate.** Tuning
here is about write behaviour and headroom, not rescuing reads.

```
shared_buffers = 32GB                      # 1/4 of RAM; above ~40% rarely helps
effective_cache_size = 96GB
work_mem = 32MB                            # PER sort/hash NODE, not per query — see 9.5
maintenance_work_mem = 2GB                 # held below EDB's 5% because autovacuum workers multiply it
autovacuum_work_mem = 1GB
wal_buffers = 64MB                         # the default caps at 16MB, too small here

max_worker_processes = 32
max_parallel_workers = 16                  # deliberately not 32 — four tenants need headroom
max_parallel_workers_per_gather = 4
max_parallel_maintenance_workers = 4

random_page_cost = 1.1                     # the default 4.0 assumes a spinning disk
effective_io_concurrency = 200
io_method = worker                         # PG18, restart-only. io_workers = 8 (default is 3)
huge_pages = on
```

Outside Postgres: set the NVMe I/O scheduler to `none`, raise `autovacuum_max_workers` to ~6 with
`autovacuum_vacuum_cost_limit` 2000+ (⚠️ uncited judgement), and disable Transparent Huge Pages
alongside the explicit huge pages (⚠️ standard practice, no citation found this pass).

🔴 **`huge_pages = on` means Postgres refuses to start without them.** That is the point — but it
turns "bumped `shared_buffers`, forgot the huge pages" into a **boot failure on the machine taking
bookings.** Size them from Postgres rather than guessing:

```sh
postgres -C shared_memory_size_in_huge_pages     # PG15+ tells you the number
# add ~5%, then set vm.nr_hugepages in /etc/sysctl.d/
```

✅ **`io_uring` is genuinely available**: PGDG's `postgresql-18` declares
`Depends: … liburing2 (>= 2.3)` and Debian 12's kernel is well past the 5.1 requirement. Start on
`worker`, benchmark `io_uring`.

### 9.4 The settings that matter for pgBackRest → GCS

```
wal_level = replica                        # docs: "replica or higher" to enable archiving
archive_mode = on                          # NOT "always" — pgBackRest disallows archive_mode=always
archive_timeout = 60s
archive_command = 'pgbackrest --stanza=main archive-push %p'
max_wal_size = 32GB
wal_compression = zstd
summarize_wal = off
```

**`archive_timeout = 60s` is the number the worst-case data loss depends on, and it is sourced.**
PostgreSQL 18's documentation, verbatim: *"archive_timeout settings of a minute or so are usually
reasonable"*, with the reason not to go shorter, also verbatim: *"archived files that are archived
early due to a forced switch are still the same length as completely full files. It is therefore
unwise to set a very short archive_timeout — it will bloat your archive storage."*

So **worst-case loss is about 60 seconds**, paid for by pushing a full 16 MB segment every minute on
an otherwise quiet database. Going to 10s multiplies GCS storage for almost no gain.

🔴 **`summarize_wal` stays OFF.** It drives PG17's *native* `pg_basebackup --incremental` through a
background summariser process. **pgBackRest has its own block-level incremental and does not use
it** — switching it on costs a process and disk writes for nothing.

**`wal_keep_size` is a red herring here.** It retains WAL for streaming replicas and slots, not for
archiving. With no replica, the default is right.

⚠️ **`wal_compression = zstd` is reasoning, not a pgBackRest statement.** Postgres compresses
full-page images *inside* WAL records; pgBackRest compresses repository objects. Different layers,
so complementary rather than double compression — and pgBackRest states it *"does not compress more
than once"* internally.

**pgBackRest's own side:**

```
compress-type=zst            # their docs: default is gz, "zst is recommended because it is much
                             # faster and provides compression similar to gz"
archive-async=y              # plus a spool-path, so a slow GCS upload never stalls a commit
repo1-type=gcs
repo1-gcs-bucket=<bucket>
repo1-gcs-key=/etc/pgbackrest/gcs-sa.json
repo1-path=/repo             # a prefix, not the bucket root
repo1-cipher-type=aes-256-cbc
start-fast=y
delta=y
```

⚠️ **We are not on Google Compute Engine**, so `repo1-gcs-key-type=auto` (instance metadata) is
unavailable — a service-account key file must sit on disk. **Treat it as a credential**: mode 600,
and the passphrase and key both in the password manager.

**Does PG18 change the older advice? Essentially no, for a pgBackRest user.** PG18's backup work is
`pg_verifybackup` tar support and `pg_combinebackup --link`, both on the `pg_basebackup` path
pgBackRest does not use. GCS support has been in pgBackRest since 2.33.

### 9.5 🔴 Risk 1, and the day-one configuration that answers it

Four apps and paying customers behind one `shared_buffers`, one WAL and one restart. This is the top
risk of the consolidation, and it is **actionable rather than merely worrying**. Set this up on the
first day, not after the first incident:

```sql
-- one database and one role per app; no app gets superuser
REVOKE CONNECT ON DATABASE booking FROM PUBLIC;      -- per database: apps cannot reach each other
GRANT  CONNECT ON DATABASE booking TO app_booking;

-- per-role guardrails, applied at login
ALTER ROLE app_booking  SET statement_timeout = '15s';
ALTER ROLE app_forum    SET statement_timeout = '30s';
ALTER ROLE app_internal SET statement_timeout = '120s';   -- the agent / analytics
ALTER ROLE app_bob      SET statement_timeout = '300s';
ALTER ROLE app_booking  SET work_mem = '16MB';
ALTER ROLE app_internal SET work_mem = '128MB';           -- the only one that needs it
ALTER ROLE app_booking  SET idle_in_transaction_session_timeout = '30s';
ALTER ROLE app_internal SET lock_timeout = '5s';
```

And outside SQL:

- **pgbouncer with a separate pool per database**, and `max_db_connections` per pool, so one app
  cannot eat the whole connection budget. Give booking a reserved slice.
- `reserved_connections` / `superuser_reserved_connections`, so an operator can always get in while
  something is saturating the server.
- `log_min_duration_statement = '1s'` and `log_temp_files = 0`, to name the offender *before* it
  becomes an incident.
- **Budget `work_mem` as global × concurrent nodes, not per query.** That product is what actually
  exhausts memory.
- **A pgBackRest restore that has actually been performed.** Booking is the one that must come back.

The load-bearing idea: **the booking system's limits are tight and the agent's are loose, set per
role, so the agent cannot borrow the booking system's memory or time.**

### 9.6 The other two risks

**Risk 2 — `pg_search` is the one soft spot on the critical path.** It is the only non-PGDG
component, it is AGPL, it carries a Rust/pgrx build dependency, it needs a
`shared_preload_libraries` restart, it has documented write amplification, **and it is what stands
between us and switching Elasticsearch off** — which §3a says the booking system may still need.
*Mitigation:* prove it on the real forum corpus at real write rates first; leave the small sites on
built-in `tsvector`; keep `pg_textsearch` on the watchlist as the permissive escape hatch once it
grows phrase queries.

**Risk 3 — a major upgrade becomes a four-app event gated by the slowest extension.** TimescaleDB
was 34 days behind PG18; pgvector was *ahead* of it. Every non-PGDG extension widens that window.
*Mitigation:* keep the list permissive and PGDG-packaged — which is precisely why the partitioning
route for time series earns its keep.

### 9.7 Stated plainly: what is not confirmed

Carried forward so none of it reads as settled:

- **`pg_search`'s `shared_preload_libraries` requirement** — the current README says it is required
  unconditionally; 0.17-era docs said it was unnecessary on PG17+. **Assume required.**
- **AGPL §13's reach into application code** — standard reading is that it does not; not
  court-tested.
- **TSL's "modify the schema" clause** versus an Agent Bob DDL surface, if TimescaleDB is ever
  taken — a reading of licence text, not a ruling.
- **`wal_compression` versus repository compression** — reasoning, not a pgBackRest statement.
- **Transparent Huge Pages and the autovacuum numbers** — standard practice, uncited this pass.
- **The whole of §9.3** — assembled from several reputable sources, not quoted from one. Benchmark.
- **TimescaleDB's PG17 lag** — unverifiable, so "34 days behind PG18" is one data point, not a trend.
