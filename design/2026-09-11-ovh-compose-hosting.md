# OVH Compose Hosting — one box, many stacks, block-level backups to Google

Status: **DESIGN — nothing provisioned, nothing bought.** Written 2026-09-11.

> 🔴 **§3 and §4 are SUPERSEDED (2026-09-12).** The LVM thin pool, the per-app logical volume and
> the hourly snapshot-to-restic design described there **are not what we are building.** Kai put an
> adversarial filter on them — the block layering existed only to make a live database
> file-copyable — and they were replaced by **one Postgres with pgBackRest archiving the write-ahead
> log to Google Cloud Storage**: worst-case data loss ~60 seconds instead of an hour, restore to any
> moment, and five bespoke scripts plus the thin pool deleted. **Read
> `design/2026-09-12-postgres-native-backups.md`, and follow `docs/ops.md`.** §2's "used bytes are
> unknown" is also out of date: the whole estate measures **23.8 GB**
> (`design/2026-09-12-gke-to-box-migration.md` §1). The rest of this document — the box choice,
> Caddy, the host, and Appendix A's cluster inventory — still stands, and §9–§11 carry their own
> dated corrections.
Supersedes: `design/2026-09-09-kubernetes-deployment-playbook.md` (GKE was never applied; see §1).
Research: a 13-agent workflow (six Sonnet researchers, one adversarial verifier each, one
completeness critic), plus a read-only inventory of `prodcluster` and direct price checks. The
corrections the verifiers made are folded in; §11 lists what is still unverified.

---

## 0. Scope, in one paragraph

**This plan gets one OVH server ready to run Agent Bob, and makes adding the next app a
fifteen-minute checklist.** It does **not** migrate anything off GKE. Kai will move the other apps
one at a time later, because he knows each one's gotchas; Appendix A records what the research
found about them so that work starts ahead. Decisions Kai made on 2026-09-11:

| Question | Answer |
| --- | --- |
| Acceptable data loss | **1 hour**, the same as today (GKE snapshots every disk hourly) |
| Budget | **Up to ~£150/month for the box.** Backup cost is secondary |
| Where backups live | **Google Cloud Storage.** Not a second box |
| Region | **France** (Roubaix / Gravelines / Strasbourg) |
| Which apps move | All of them eventually. **None in this plan** |

## 1. Why not GKE

The whole of today's estate runs on **one** GKE node (e2-highmem-2: 2 vCPU, 16 GB) at ~26% CPU
and ~70% memory. Bob needs far more than that: up to ~100 concurrent session containers inside a
privileged Docker-in-Docker daemon. On GKE that compute is expensive. On OVH bare metal the same
money buys 16 physical cores. The k8s manifests in `deploy/k8s/` and the GKE playbook stay in the
tree as a record, but they are superseded.

## 2. The box

| Model (OVH Eco / Kimsufi "Rise") | CPU | RAM | Disks | £/month ex VAT | Setup |
| --- | --- | --- | --- | --- | --- |
| **RISE-L — recommended** | AMD Ryzen 9 9950X, **16 cores / 32 threads** | 128 GB DDR5 ECC | 2 × 960 GB NVMe, software RAID1 | **£128.99** | £128.99 once |
| RISE-XL | AMD EPYC Turin 9455, **48 cores / 96 threads** | 128 GB DDR5 ECC | 2 × 1.92 TB NVMe, software RAID1 | £257.99 | £257.99 once |
| ADVANCE-3 (2024) | AMD EPYC 4464P, 12c / 24t | 64 GB | 2 × 960 GB NVMe | £171.99 | £171.99 once |

Both Rise models ship in Gravelines, Roubaix and Strasbourg with "delivery in 120s", 1 Gbps
unmetered public and private bandwidth, and OVH's free anti-DDoS. One IPv4 is included, and one
is enough because Caddy routes by hostname.

**Recommendation: RISE-L.** 32 threads is "32 CPUs" in the same sense GKE counts vCPUs (a GKE
vCPU is one hardware thread), so it meets the 32-CPU floor inside the budget. RISE-XL is the
48-core option at twice the price. Move up to it when Bob's session load needs it, not before.

**The one real constraint is disk.** RAID1 over 2 × 960 GB leaves ~890 GB usable. That is ample
for Bob. It may **not** hold the later migrations: today's GKE disks are **provisioned** at
1.65 TB (the shared Postgres alone is 1 TB), though their **used** bytes are unknown, because
exec into production was not allowed. Before ordering, check whether RISE-L offers a larger disk
option. If it doesn't, plan the migrations around real `df` numbers (§10, Q3).

The researchers first priced 24-core boxes at $500–830/month because they were quoting the
US Advance and Scale ranges. The Rise range is the budget line, and the verifiers did not see it.

## 3. How the box is laid out

```
2 × NVMe ──mdadm RAID1──┬─ /boot, /  (ext4, ~60 GB)           OS only; rebuildable from git
                        └─ md PV ── VG vg0 ── thin pool vg0/pool (~800 GB)
                                        ├─ LV bob     → /srv/apps/bob     [tag: backup]
                                        ├─ LV caddy   → /srv/apps/caddy   [tag: backup]
                                        ├─ LV ops     → /srv/apps/ops     [tag: backup]  (monitoring)
                                        └─ LV docker  → /var/lib/docker   [NOT backed up]
```

- **One thin LV per app. It holds everything stateful that app owns**: database files, uploads,
  and its `.env`. An LVM snapshot is atomic per LV, so one LV per app gives each app a single
  point-in-time snapshot of all its state together. That is the property a GKE disk snapshot gives.
- **`/var/lib/docker` is rebuildable, so it isn't backed up**: images, stopped containers, and
  Bob's DinD image store. Keeping it out of the backup also keeps the hourly delta small.
- **Apps use bind mounts under `/srv/apps/<app>/`, never Docker named volumes.** A named volume
  lives in `/var/lib/docker`, which is the unbacked LV. This is the one rule that makes backups
  app-agnostic. Break it and that data silently isn't backed up.
- **OVH installer:** partition `/boot`, `/` and swap on RAID1, and give the remainder to a
  throwaway `/srv` partition. After first boot, unmount it, `pvcreate` the md device, and build
  `vg0` plus the thin pool. The exact installer options still need checking (§11).

## 4. Backups — the crucial part

### 4.1 The design

> **Every hour, for every LV tagged `backup`: take an LVM thin snapshot (an instant, copy-on-write,
> crash-consistent picture of the whole volume), mount it read-only, and back it up with
> `restic` into a GCS bucket. Then drop the snapshot.**

This is the nearest plain-Linux equivalent of GKE's hourly persistent-disk snapshots:

| Property | GKE PD snapshot (today) | LVM thin snapshot → restic → GCS |
| --- | --- | --- |
| App-agnostic | yes, the whole disk | yes, the whole LV, whatever is inside |
| Point in time | crash-consistent per disk | crash-consistent per LV; LVM freezes the fs for the instant (§11) |
| Incremental | yes, block deltas | yes: content-defined chunks, deduplicated across all apps and all hours |
| Encrypted | Google-managed | client-side (restic), and Google-managed at rest |
| Offsite | same GCP region | GCS europe-west1: a different company, country and network from OVH |
| Exotic filesystem | no | no: LVM2 + ext4, mainline for 20 years |
| Cadence | hourly | hourly. A thin snapshot takes under a second; the long pole is restic's incremental scan, minutes |

Alternatives the research considered and rejected:
- **Classic (thick) LVM snapshots:** every write while the snapshot exists is copied first.
  Documented write amplification; unfit for hourly use.
- **`thin_delta`, `thin-send-recv`, `bdsync`:** true block-image incrementals, but custom
  scripting and a second restore path to test, for no gain at this data size.
- **ZFS or Btrfs:** excluded by Kai's rule. Nothing here makes an overwhelming case for them.
- **Borg:** its object-storage backend only exists in Borg 2.0, which is still pre-release.
  restic has native `gs:` support today.
- **OVH Public Cloud volume snapshots:** they exist only for Public Cloud instances, not on bare
  metal, and scheduled volume backups are an unshipped roadmap item.

### 4.2 Does "whatever database is inside, it comes back" hold?

A crash-consistent snapshot restores exactly like pulling the power cord. That is the same
guarantee today's GKE snapshots give.

- **Postgres (any version, including pgvector and ParadeDB):** yes. WAL replay on start. The
  condition is that data dir, WAL and any tablespaces sit on the **same LV**, which one LV per
  app guarantees.
- **Redis (RDB or AOF), SQLite (with its `-wal` file), NATS JetStream:** yes, to each engine's
  own crash-durability limit. JetStream acknowledges messages before fsync by default; a snapshot
  doesn't make that worse than a real crash would.
- **Elasticsearch: the one exception.** Elastic states that filesystem copies of a live node are
  not a supported backup, and supports only its Snapshot API (which can target GCS). Nothing in
  Bob uses Elasticsearch. It matters for franchisecloud and nocode later (Appendix A).

**No per-app hooks.** The only generic hook worth having is the filesystem freeze LVM already
performs when it creates the snapshot. Do not `docker compose pause`: pausing Bob's DinD freezes
up to 100 session containers mid-turn.

### 4.3 The job

The hourly script is `/usr/local/sbin/box-backup`, run by a systemd timer at :05 past every hour.
It lives in the ops repo (§8).

> **The runnable script lives in `docs/ops.md` (step 9b)** and supersedes the sketch that used to be
> here: that sketch sourced its env file without exporting it, so restic would not have seen the
> repository or password.

Other jobs, all behind the same `flock` so they never overlap:

| When | Job | Why |
| --- | --- | --- |
| Hourly :05 | `box-backup` (above) | the 1-hour data-loss bound |
| Daily 03:30 | `restic forget --keep-within-hourly 14d --keep-within-daily 90d --keep-within-monthly 1y --prune` | today's 14 days of hourly, plus longer daily and monthly tails |
| Weekly | `restic check --read-data-subset=5%` | catches repository rot before a restore needs it |
| Weekly | **automated restore drill**: restore Bob's latest snapshot to a scratch LV, start `postgres` on it, run `pg_isready` and one row count, tear it all down, ping healthchecks | a backup nobody restores is an assumption |

### 4.4 The GCS side

Provision these in `webkit-servers`, next to the existing `webkit-servers-agent-bob`:

- **Bucket `webkit-servers-ovh-backups`** in `europe-west1`: Standard class, uniform access,
  **soft delete set to 14 days**. Soft delete is the ransomware brake: if the box is compromised
  and an attacker deletes the repository, Google keeps the objects for 14 days. Purging them
  early needs a bucket-level permission the box never holds.
- **Service account `ovh-backup`** with `roles/storage.objectAdmin` on **that bucket only**. Its
  key sits in `/etc/box-backup/` at mode 600.
- **The restic password** is the whole backup. Lose it and every snapshot is unreadable. Keep it
  in the password manager as well as on the box.

### 4.5 What it costs and how long a restore takes

- **Storage:** Bob's data is tens of GB, so a few dollars a month at Standard-class rates. Each
  later app adds its real used bytes (deduplicated, compressed) plus small hourly deltas. Today's
  hourly GKE deltas are ~25 MB/hour for the busiest disk.
- **Uploads to GCS are free.** Restores are billed as internet egress, about **$0.12/GB**
  (100 GB ≈ $12). A restore drill should restore a small app, not the whole estate.
- **Restore time:** the 1 Gbps link caps at ~110 MB/s, and restic realistically manages
  50–100 MB/s, so **100 GB takes ~20–35 minutes**. Rebuilding the whole box (§8) is dominated by
  this. Measure it in the first drill; don't trust these figures.

### 4.6 Optional second layer: OVH's free Backup Agent

OVH launched a **Veeam-based Backup Agent for bare metal in January 2026.** The licence is free;
you pay for the OVH Object Storage it uses (the US page says $0.008/GB/month). It takes one
**daily**, incremental, whole-server backup in a fixed 22:00–06:00 CET window, keeps 14–30 days,
and stores the backups **immutable, in a different OVH datacentre**. It can do a full bare-metal
restore from a boot ISO.

It does not meet the 1-hour bound, the policy can't be changed, and the backups live on OVH
rather than Google. So it is **not the primary.** It is a cheap independent second copy that
covers the whole OS as well. Recommendation: switch it on once the box holds customer data,
after checking EU availability and price (§11).

### 4.7 The failure mode that matters most: a full thin pool

If the pool's data or metadata reaches 100%, **every** LV in it can go read-only at once, the
live apps included. Guard against it on four fronts:
- In `/etc/lvm/lvm.conf`, set `thin_pool_autoextend_threshold = 70` and
  `thin_pool_autoextend_percent = 20`. Create the pool with `--poolmetadatasize 2G`.
- A five-minute cron checks `data_percent` and `metadata_percent`. Above 80%, it fails its
  healthchecks.io check, so someone gets alerted.
- The backup script removes every snapshot it finds on entry and on exit, so snapshots never
  pile up.
- Don't over-provision: the virtual sizes of the thin LVs should add up to no more than the
  pool's real size until usage is known.

## 5. Caddy in front

**Caddy runs as its own tiny Compose stack with `network_mode: host`** (from `/srv/apps/caddy`),
so its certificates and ACME account live in a backed-up LV. Each app publishes **one** port,
bound to **`127.0.0.1` only**. Caddy proxies `hostname → localhost:PORT`.

- **Never publish on `0.0.0.0`.** Docker writes its NAT rules ahead of `ufw` and host firewall
  input rules, so a published port bypasses the firewall entirely.
- **No `docker.sock` for Caddy**, so no label-driven `caddy-docker-proxy`. Mounting the socket is
  root on the host.
- `caddy reload` validates the new config and then hot-swaps it with zero downtime.
- **HTTP/3** is on by default. Open 443/udp as well as tcp.
- Let's Encrypt limits (300 new orders per account per 3 hours, 50 certificates per registered
  domain per week) are far above what this estate needs.

```caddyfile
{
	email ops@badcode.tv
	on_demand_tls {
		ask http://127.0.0.1:8130/tls-allowed   # later: nocode-works' customer-domain check
	}
}

bob.badcode.tv {
	# No `encode` here: compression buffers SSE. Bob's nginx (`web`) already serves the assets.
	reverse_proxy 127.0.0.1:8100 {
		flush_interval -1   # SSE: flush every byte immediately
	}
}

# Later, one block per app:
# nowtakemybooking.com, www.nowtakemybooking.com {
# 	reverse_proxy 127.0.0.1:8110
# }
# :443 {                       # nocode-works customer domains, issued on demand
# 	tls { on_demand }
# 	reverse_proxy 127.0.0.1:8130
# }
```

**The port table** lives in the ops repo, and every app takes the next free block of ten:

| Port | App |
| --- | --- |
| 8100 | Agent Bob (`web`) |
| 8110 | booking system (later) |
| 8120 | Agent Wolf (later) |
| … | … |

## 6. Agent Bob on the box

**It needs no code changes.** A small override file, `compose.ovh.yml`, next to
`docker-compose.yml` does it:

- **`WEB_PORT=127.0.0.1:8100` in `.env`.** The compose line is `"${WEB_PORT:-8080}:8080"`, so this
  binds the UI to localhost only. `web` is the **only** published service; nginx inside `web`
  forwards `/agent/`, `/auth/` and `/dev/` to `agentd`.
- **`agentd`'s own port is never published.** Hazard **H6** in `docs/19-embedding.md`: in API-key
  mode, `/agent-proxy/` on agentd is unauthenticated and spends the real Anthropic key. It is
  safe only while agentd is unreachable from outside the compose network, which this layout
  guarantees. Keep it that way.
- **Volumes:** the override rebinds `pg-data` and `agentd-data` to `/srv/apps/bob/pg-data` and
  `/srv/apps/bob/agentd-data`, using the local driver with `type: none, o: bind`. That puts them
  in the backed-up LV. `dind-data` stays a named volume in `/var/lib/docker`, which isn't backed
  up (see the trade-off below).
- **Production `.env`:**
  - a real login mode, not dev-open;
  - long random values for `AGENTKIT_JWT_SECRET` and a *different* `AGENTKIT_SESSION_JWT_SECRET`;
  - the model credential;
  - `AGENTKIT_BLOB_BACKEND=gcs` with bucket `webkit-servers-agent-bob`;
  - `GOOGLE_APPLICATION_CREDENTIALS` pointing at the `agent-bob-runtime` key in
    `/srv/apps/bob/secrets/`, mode 600.

  The GKE playbook's §2 audit of production settings still applies; reuse its list.
- **Images:** build on the box from a git checkout of `main` (`docker compose up -d --build`).
  It is the simplest path and needs no registry credential for `agentd` or `web`. Pulling session
  base images from Artifact Registry uses the same `agent-bob-runtime` key.
- **Resource limits:** `cpus` and `mem_limit` on `dind`, so a burst of sessions can't starve
  whatever app lands next to Bob. For example, 24 of the 32 threads and 96 GB.

**Trade-off, stated plainly: running sessions aren't in the hourly backup.** Bob already
snapshots idle sessions (after 30 minutes) to the blob store in GCS, and the Postgres row that
points at each snapshot *is* backed up. If the box dies, archived sessions come back. Sessions
that were live inside DinD at that moment lose up to 30 minutes of in-container work. The
conversation history is in Postgres, so it survives either way.

**Isolation gate, not needed now.** While Bob is the only app on the box, privileged DinD
endangers only Bob. **Before the booking system or any app with customer data moves in**, take
DinD off `privileged: true` by running it under the **sysbox** runtime (DinD without host
privilege), or move Bob into a KVM VM on the same box. This is a hard gate in the later
migration plan.

**Egress to watch:** each session-image pull from Artifact Registry, and each archived-session
restore from GCS, crosses from Google to OVH at about $0.12/GB. Images are pulled once into
DinD's cache, not once per session, so this should stay small. If it grows, the fix is already
built in: switch `AGENTKIT_BLOB_BACKEND` to the local fs backend or OVH Object Storage.

## 7. The host

- **OS:** Debian 12, with `unattended-upgrades` set to security updates only and no automatic
  reboots. Reboot at a time you choose.
- **Admin access:** **Tailscale** only (the free tier covers two people). SSH keys only, no root
  login, and port 22 closed to the internet.
- **Firewall:** `nftables`, with an explicit `DOCKER-USER` allowlist: 80 and 443 (tcp and udp)
  from anywhere, everything else from the Tailscale interface only. Use OVH's free Edge Network
  Firewall as a stateless outer layer, not as the only control.
- **Docker `daemon.json`:**
  - `"log-driver": "local"`, with `max-size` 10m and `max-file` 3;
  - `"live-restore": true`;
  - `default-address-pools` set outside Tailscale's `100.64.0.0/10`;
  - `data-root` on the `docker` LV.
- **Monitoring**, three small things and nothing more:
  1. **healthchecks.io** (free tier): dead-man switches for the backup, prune, check, drill and
     thin-pool jobs. If the box dies, the pings stop, so this also answers "is the box up".
  2. **Uptime Kuma** in `/srv/apps/ops`: HTTPS checks for each hostname, plus certificate expiry.
  3. **Beszel** in `/srv/apps/ops`: host and per-container CPU, memory and disk.

## 8. Rebuild from scratch

**A new private repo, `badcodetv/box`**, holds everything except data and secrets:
- `bootstrap.sh`: idempotent. It installs Docker, `nftables`, Tailscale, restic and LVM config;
  builds `vg0` and the thin pool; and installs the systemd timers;
- `caddy/Caddyfile`;
- the port table;
- `box-backup` and its sibling jobs;
- one directory per app holding its `compose.ovh.yml` or a pointer to the app repo.

**Secrets** (the restic password, the service-account keys, every app's `.env`) go in the
password manager. App `.env` files also live inside each app's LV, so restic has them, encrypted.

**Drill: the box is gone.**
1. Order a new box: two minutes.
2. `bootstrap.sh`: about 30 minutes.
3. Paste in the restic password and the backup key.
4. `restic restore latest --tag app=<x>` into each fresh LV.
5. `docker compose up` for each app, and `caddy start`.
6. Point DNS at the new IP.

**The target is under 2 hours for Bob alone.** Run this drill once, for real, on a second
RISE-L rented for a day (about £9 pro-rata plus a setup fee) before the booking system moves in.
That is the only way to know the number.

## 9. Order of work

| # | Step | Time | Done when |
| --- | --- | --- | --- |
| 1 | Kai answers §10 and orders the box | 15 min | box delivered |
| 2 | Install Debian (RAID1), build `vg0` and the thin pool, run `bootstrap.sh` (Docker, nftables, Tailscale) | half a day, including writing `bootstrap.sh` | SSH works over Tailscale; ports 80 and 443 are the only public ones (`nmap` from outside) |
| 3 | GCS bucket and `ovh-backup` SA; restic repo; `box-backup` and timers; healthchecks | 2–3 h | a test LV's file appears in `restic snapshots`; deleting it and restoring it works |
| 4 | Caddy stack and a DNS record for Bob | 30 min | `https://bob.badcode.tv` returns Caddy's placeholder with a valid certificate |
| 5 | Bob: clone, `compose.ovh.yml`, production `.env`, `up --build` | 1–2 h | **Kai's manual test passes on the box** |
| 6 | First weekly drill runs green; timed restore of Bob's LV | 30 min | a restore time is written into §4.5 |

**Total: about two working days**, most of it step 2 and step 3.

## 10. Decisions

Kai decided these on 2026-09-11:

1. **Box: RISE-L** (16 cores / 32 threads, £128.99 a month). Move up to RISE-XL only when Bob's
   load proves the need.
2. **Hostname: `bob.badcode.tv`.** Kai controls DNS and will point the A record at the new box.
3. **Disk: ~890 GB is enough.** Kai judges the later migrations fit.
   **Confirmed by measurement 2026-09-12:** the whole GKE estate uses **23.8 GiB**, not the 1.65 TB
   provisioned (`design/2026-09-12-gke-to-box-migration.md` §1). No disk upgrade needed at order time.

Still open:

4. **Switch on OVH's Backup Agent** as a second, daily, whole-server copy once customer data
   arrives?

## 11. Unverified, and how to settle each

| Claim | Status | How to settle |
| --- | --- | --- |
| OVH's installer can leave a partition to hand over to LVM, on top of RAID1 | likely; not checked | look at the reinstall screen before ordering |
| RISE-L disk-upgrade options | not shown on the product page | the order configurator |
| LVM freezes the filesystem when it creates a thin snapshot (device-mapper suspend without `--nolockfs`) | believed; no primary source cited | test on the box: write in a loop, snapshot, check with `fsck -n` on the snapshot |
| OVH Backup Agent: EU availability and EU price | the EU press release says Europe; the US product page says "US only at this time" | the OVH control panel once the box exists |
| GCS internet egress $0.12/GB (a researcher cited a 2026-05-01 rate change) | third-party source | the GCP pricing page |
| Restore throughput of 50–100 MB/s | community benchmarks | step 6 measures it |

Corrections the verifiers made to the first research drafts:
- The Scale range starts at $689, not $822.
- OVH RAID1 usually rebuilds itself after a disk swap; it is not always manual.
- Postgres 9.6 reached end of life on 2021-11-11, not in October.
- Borg 2.0 does have native S3 support.
- The GKE zonal-cluster management fee may already be offset by the free-tier credit, so
  today's GKE bill is probably ~$190–250/month rather than $260–320.

Sources:
[RISE-L](https://www.kimsufi.com/en-gb/rise/rise-l/) ·
[RISE-XL](https://www.kimsufi.com/en-gb/rise/rise-xl/) ·
[Advance](https://www.ovhcloud.com/en-gb/bare-metal/advance/) ·
[OVH Backup Agent announcement](https://corporate.ovhcloud.com/en/newsroom/news/ovhcloud-backup-agent/) ·
[Backup Agent docs](https://docs.ovhcloud.com/en/guides/storage-and-backup/backup-agent/product-presentation) ·
[Backup Agent pricing (US)](https://us.ovhcloud.com/storage-solutions/backup-agent/) ·
[OVH RAID](https://docs.ovhcloud.com/en/guides/bare-metal-cloud/dedicated-servers/raid-soft) ·
[OVH disk replacement](https://docs.ovhcloud.com/en/guides/bare-metal-cloud/dedicated-servers/disk-replacement) ·
[GCP network pricing](https://cloud.google.com/network-tiers/pricing) ·
[Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/) ·
[Caddy on-demand TLS](https://caddyserver.com/docs/automatic-https#on-demand-tls) ·
[restic GCS backend](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html#google-cloud-storage) ·
[lvmthin(7)](https://man7.org/linux/man-pages/man7/lvmthin.7.html).
The full research output, with every per-claim source, is in the workflow run `wf_b2503c01-1cf`.

---

## Appendix A — for the later migrations (not this plan)

A read-only inventory of `prodcluster` (GKE, `europe-west1-b`), taken 2026-09-11.

**Apps (11 namespaces):**
- **bookingsystem:** api and frontend.
- **franchisecloudprod:** api-all, frontend, ft-booking, marketingsite, NATS 2.9, screenshotter
  and widgets, plus Elasticsearch 7.5.2.
- **nocode-works:** api, frontend, proxy and screenshotter, plus Elasticsearch 7.5.2 and about
  30 customer domains.
- **badcode:** agent, api, frontend, worker, n8n, ParadeDB pg17 and Redis.
- **Small apps:** forum, kellie, quoteright, panwww and zps-apps.
- **Shared:** Postgres **9.6.21** (1 TB disk) and Redis.

**Findings to carry forward:**
- **Postgres 9.6 is five years past end of life.** Upgrade it during its move, with `pg_upgrade`
  or dump and restore. Don't lift it as-is.
- **Elasticsearch 7.5.2 is past end of life too, and it is the one datastore Elastic won't
  support restoring from a block snapshot.**
  - Add an ES snapshot job to GCS for both clusters, or accept crash-consistent block snapshots
    as an unsupported but usually-fine risk.
  - Upgrading to 7.17 is the usual first step.
- **The nocode customer domains point at `104.155.75.230` and `35.240.61.4`, and the customers own
  that DNS.**
  - Caddy on-demand TLS, with an `ask` endpoint backed by nocode's domain table, replaces the
    per-domain cert-manager ingresses.
  - Moving the domains themselves needs either customer DNS changes, or the GCP IPs kept alive
    for a while as a forwarding hop.
- **Several cert-manager HTTP-01 solvers have been stuck for 1 to 9 days right now**
  (`nocode-domains`, `zps-apps`). ~~Some certificate renewals are probably failing today.~~
  **Re-checked 2026-09-12 and downgraded:** all 14 are for domains that no longer point at the
  cluster (three do not resolve; `zps8.co.uk` / `zeteticmind.com` / `strategy.zeteticmind.com` now
  answer from Krystal `185.194.90.31` with certificates valid to 2026-10-24; `nocode.works` itself
  returns 200 on a valid Let's Encrypt certificate). **Nothing user-facing is broken.**
- **Google Container Registry is retired.** `gcr.io` paths are served by Artifact Registry, and
  the box pulls with a reader-only service-account key.
- **Apps that read GCS will need credentials instead of GKE's metadata server.** Heavy readers
  pay about $0.12/GB egress; `nocode-websites` and `forum-videos` are the ones to measure first.
- **Snapshot housekeeping:** the project holds **9,147 disk snapshots (378.6 GB, oldest
  2017-04-04; re-confirmed 2026-09-12)**. Many date from
  2017–2025, before the current `hourly-backup` policy (14 days) existed, so the policy never
  deletes them. Review and delete the stale ones. It costs a little each month and clutters
  every restore search.
- **Order:** the small apps first (kellie, quoteright, panwww, zps-apps, forum), then the ES apps,
  then badcode, and the **booking system last**. Pass the sysbox isolation gate (§6) before any
  of them lands next to Bob.
