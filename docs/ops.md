# Ops — the OVH box

How BadCode runs its apps on one OVH server: **what we decided and why** (Part 1), **how to set it
up, step by step** (Part 2), **how to run it day to day** (Part 3), and **what we already know
about moving the GKE apps later** (Part 4).

Written 2026-09-11. The long-form reasoning, with sources, is in
`design/2026-09-11-ovh-compose-hosting.md`. **This file is the one to follow.** Where the two
disagree, this file is newer.

> **Status: nothing is built yet.** Every command below is still to be run.
>
> **Backups changed on 2026-09-12.** The LVM-thin-snapshot design was replaced with
> **Postgres-native backups**: one Postgres, pgBackRest, write-ahead-log archiving to Google Cloud
> Storage, point-in-time recovery. Kai's reasoning was that the only local state worth protecting is
> a Postgres data directory, and the evaluation agreed — the block layering existed *only* to make a
> live database file-copyable, so removing the database from that problem deleted the thin pool and
> five bespoke scripts. **§1.5, Step 4, Step 9 and Step 13 are rewritten.** The argument, the
> version and extension choices, and every configuration number are in
> **`design/2026-09-12-postgres-native-backups.md`**.
> Commands on the box run as root (`sudo -i`). Commands marked **💻 laptop** run on your own
> machine, where `gcloud` is logged in.

---

# Part 1 — What we decided, and why

## 1.1 Decisions

Kai made these on 2026-09-11.

| Question | Decision |
| --- | --- |
| Where apps run | **One OVH dedicated server, model RISE-L**, in France |
| How apps run | **One Docker Compose stack per app**, each on its own port, behind **Caddy** for HTTPS |
| Where backups go | **Google Cloud Storage**, continuously, via pgBackRest. No second server |
| How much data we can lose | **At most ~1 minute** (`archive_timeout`), and any moment can be restored to. Better than GKE today |
| Databases | **One Postgres 18 for every app**, one database and one role each |
| Bob's address | **`bob.badcode.tv`**. Kai sets the DNS A record |
| Disk | **~890 GB (mirrored) is enough** |
| Scope of this first round | **Set up the box and run Agent Bob.** The other GKE apps move later, one at a time |

## 1.2 Why leave GKE

Today everything (booking system, franchisecloud, nocode, badcode, forum and the rest) runs on
**one** small GKE machine: 2 vCPUs and 16 GB, at ~26% CPU and ~70% memory. Agent Bob needs far
more, because it runs up to ~100 AI sessions, each in its own container. On Google that compute
is expensive; on OVH, £129 a month buys 16 real cores. Google stays for file storage (GCS) and
backups. The old GKE plan (`design/2026-09-09-kubernetes-deployment-playbook.md`) is superseded
and was never applied.

## 1.3 The server

| | **RISE-L (chosen)** | RISE-XL (upgrade path) |
| --- | --- | --- |
| CPU | AMD Ryzen 9 9950X: 16 cores, 32 threads ("32 CPUs" the way Google counts them) | AMD EPYC 9455: 48 cores, 96 threads |
| Memory | 128 GB | 128 GB |
| Disk | 2 × 960 GB NVMe, mirrored → **~890 GB usable** | 2 × 1.92 TB NVMe, mirrored |
| Price (ex VAT) | **£128.99/month** + £128.99 once | £257.99/month + £257.99 once |
| Network | 1 Gbps, unmetered, free DDoS protection | same |
| Where | Gravelines, Roubaix, Strasbourg (France) | same |

OVH sells it under the Kimsufi / "Eco" brand. It's ready about two minutes after you order.

## 1.4 Mirror or stripe? Mirror

The two disks are **mirrored** (RAID1): every write goes to both. The alternative, **striping**
(RAID0), doubles the space to ~1.8 TB, but:

| | Mirror (RAID1) | Stripe (RAID0) |
| --- | --- | --- |
| One disk dies | nothing stops; OVH swaps the disk and the mirror rebuilds while apps run | **everything stops**; all data on the box is gone |
| Getting back | automatic | new disk, reinstall, restore from Google: hours, up to 1 hour of data lost, plus download fees |

Backups cover **disasters**: deletion, hacking, fire, a bad update. The mirror covers the
**most common hardware failure**, a worn-out disk, at zero downtime. We need both. If you want
more space, look for a bigger-disk option on the RISE-L order page; changing disks later means
a reinstall.

## 1.5 How the backups work — in plain words

**Every database change is written twice: once into the database's own files, and once into a
running log of changes. We keep that log. So the backup is "the database as it was on Sunday, plus
every change since" — which means we can put it back as it was at any moment, to the second.**

The tool that does this is **pgBackRest**, the standard Postgres backup tool. The changes go to
Google Cloud Storage, encrypted before they leave the box.

> **Why not hourly disk photographs?** That was the plan until 2026-09-12, and it was replaced.
> Photographing a disk requires layers of block virtualisation — a thin pool, copy-on-write
> snapshots, a per-app virtual disk — *for one reason only:* you cannot safely file-copy a database
> that is being written to. Take the database out of the file-copy problem and that whole apparatus
> has no job left. The reasoning, and the list of what it deleted, is in
> `design/2026-09-12-postgres-native-backups.md`.

### The write-ahead log, which is the whole trick

Postgres never writes a change straight into its data files. It writes it first to the
**write-ahead log**, and applies it to the real files later. That is why Postgres survives a power
cut: on restart it replays the log.

Keeping that log instead of discarding it gives us this:

```
 full backup ─────────────────────────────────────────────────▶ time
 (Sunday 03:30)  ├─ log ─┼─ log ─┼─ log ─┼─ log ─┼─ log ─┤
                                      ▲
                    restore the backup, replay the log to HERE
                    = the database exactly as it was at 14:32:07
```

Restoring to a chosen instant is called **point-in-time recovery**. It is the thing that matters
for the failure we are actually likely to have — not a dead disk, but something writing nonsense
into a table.

### The schedule

| When | What |
| --- | --- |
| **continuously** | each closed log segment is pushed to Google. `archive_timeout = 60s` forces one every minute even on an idle database |
| **hourly** | an incremental backup (only the blocks that changed) |
| **daily** | a differential backup (everything changed since the last full) |
| **weekly** | a full backup, and `pgbackrest verify` on the repository |
| **weekly** | **a real restore, to a moment between two backups** — step 13 |
| **nightly** | a small `restic` copy of the things that are not databases: Caddy's certificates, each app's `.env` and compose file |

Retention: four full backups, fourteen days of differentials, and the log needed to reach them.

### What comes back, and what doesn't

| Thing | After a restore |
| --- | --- |
| **Any Postgres database** — including pgvector, PostGIS and full-text indexes | **Yes, to any moment in the retention window.** This is the point |
| Caddy's certificates | Yes (nightly restic) — and they are re-issuable from Let's Encrypt anyway |
| Each app's `.env` secrets | Yes (nightly restic, encrypted) — but the password manager is their real home |
| Bob's **archived** sessions, artifacts, datasets | Yes — they live in Google Cloud Storage already |
| Bob's sessions **running at that moment** | Up to 30 minutes of in-container work is lost; the conversation history is in Postgres and survives |
| Docker images and caches | **No, on purpose.** Rebuildable |
| Redis | **No, on purpose.** Nothing uses it as a system of record |

### How much it costs, and how long a restore takes

- **Storage in Google:** a few dollars a month. Backups are compressed with zstd, deduplicated by
  block, and the total data is ~24 GB.
- **Uploading to Google:** free.
- **Downloading (restoring):** about **$0.12 per GB**, so a full 25 GB restore is ~$3.
- **Restore speed:** `restore --delta` fetches only the blocks that differ, so a rollback is far
  faster than a first restore. **Step 13 measures the real number.**

### How sure are we?

- **pgBackRest is the standard tool**, used in production at large scale, and it backs up to Google
  Cloud Storage natively.
- **It verifies itself.** `pgbackrest check` answers "is archiving reaching Google right now?" and
  `pgbackrest verify` answers "is the repository intact?" — neither requires a full restore. The old
  plan's weakest sentence was *"the unproven part is our own ~25-line script"*. **There is no longer
  a bespoke script to be unproven.**
- **The genuinely unproven part is now our own configuration**, which is why step 13 does a real,
  timed, point-in-time restore before anything important depends on it, and why that drill runs
  every week.

### The one danger: one Postgres means one blast radius

The thin pool filling up and taking every app read-only at once is gone. The risk that replaces it
is **consolidation**: one memory pool, one write-ahead log, one restart, and four apps behind them.

*A forum reindex evicts the booking system's hot pages. `work_mem` applies per sort operation
rather than per query, so one runaway agent query can exhaust memory on the machine that processes
payments.*

The guards are configuration, set on the first day rather than after the first incident: **one
database and one role per app, with tight query-time and memory limits on the payment path and
loose ones on the agent** (step 9d), plus pgbouncer pools so no app can eat the whole connection
budget.


### A cheap second copy: OVH's Backup Agent — priced 2026-09-12, recommend yes

Since January 2026 OVH resells a Veeam-based agent for bare-metal servers. Researched 2026-09-12:

| | |
| --- | --- |
| Agent / licence | **£0** |
| Storage | **£0.0061 ex VAT per GB per month** (UK site; 0,007 € on the French site). It is simply OVH Object Storage at list price |
| **Cost for us** | **~£0.20/month** at today's ~25 GB; **~£1.60/month** at 200 GB |
| Schedule | nightly, started between 22:00 and 06:00 CET. First run a full image, then incrementals |
| Retention | 14 days by default, up to 30, with **14 days of immutability (WORM)** — nothing can delete or encrypt it |
| Granularity | whole-server image only; **restore** offers file-level *or* whole-server, self-service |
| Where | a vault in a deliberately distant OVH datacentre (documented anti-affinity) |
| Restore / egress | **free** — "OVHcloud will not charge for incoming/outgoing traffic, remote backup, or encryption" |

**Recommend switching it on**, once the box exists. Three reasons it is worth £0.20:
- it is a second copy at a **different company boundary** from Google, so one compromised or
  suspended Google account does not take both;
- the **14-day immutable lock** is a property our own pgBackRest repository does not have;
- with the LVM thin pool gone there is one fewer local safety net, and this replaces it for pennies.

It still **cannot be the main backup**: daily-only misses the 1-hour target, it is whole-server
rather than Postgres-aware, and it sits with the same company as the server.

**Two things to check, and one that is already fine:**
- 🟡 **Eco/Rise eligibility is unconfirmed.** The only documented restriction is "Dedicated Servers
  only" — no page names Eco, Rise or Kimsufi as included *or* excluded, and the marketing
  illustrates with the Advance and Scale ranges. Rise is the budget line, so **confirm it appears
  in the control panel after delivery** rather than counting on it.
- 🔴 **Public-IP only: incompatible with vRack and additional IPs.** Our plan uses one IPv4 and no
  vRack (Caddy routes by hostname), so this is compatible — but it forecloses adding a vRack later
  while the agent is on.
- ✅ **Our disk layout is supported.** Veeam Agent for Linux supports Linux native software RAID
  (`mdadm`) and ext4, which is exactly what OVH's installer produces. Note that Veeam does **not**
  back up LVM snapshots — one more small reason the thin pool is better gone. Confirm Debian 12's
  kernel against Veeam's compatible-OS list at install time.

### Free backup storage: Rise includes 500 GB, but do not rely on it

The Rise range advertises **500 GB of backup space included**, over FTP/FTPS, NFS or SMB, extendable
to 1/5/10 TB. 🔴 But OVH's own docs warn the feature *"might be unavailable or limited on servers of
the Eco product line"*, and Rise **is** the Eco line. As a backup target it is weak anyway: no
scheduler, no immutability, no image restore. **Treat it as a possible third copy, never as a
substitute for either of the two above.**

## 1.6 HTTPS and the front door: Caddy

- **Caddy** is one small program in front of every app. It gets and renews HTTPS certificates
  from Let's Encrypt automatically.
- **Each app listens on a port reachable only from inside the server** (`127.0.0.1:<port>`).
  Caddy maps `hostname → port`, so the internet can only reach Caddy.
- **Never publish an app port as `0.0.0.0`** (or as a bare `8080:8080`). Docker punches straight
  through the firewall when you do.
- **For the nocode customer domains later:** Caddy's "on-demand TLS" issues certificates for
  customer domains as they arrive, checked against nocode's list of allowed domains.

**Port table.** Each app gets its own block of ten:

| Port | App |
| --- | --- |
| 8100 | Agent Bob |
| 8110 | booking system (later) |
| 8120 | Agent Wolf (later) |
| 8130 | nocode-works (later) |

## 1.7 Security and monitoring, in brief

- **Admin access only over Tailscale**, a private network between your laptop and the box.
  SSH is closed to the internet. Only HTTPS (ports 80 and 443) is open.
- **Automatic security updates**, but no automatic reboots.
- **Agent Bob runs a privileged Docker-in-Docker container** (AI sessions run code inside it).
  That's acceptable while Bob is alone on the box. **Before the booking system or any customer
  data moves in, Bob must lose `privileged`** (by switching to the "sysbox" runtime) **or move into
  its own virtual machine.** That's a hard gate for Part 4.
- **Monitoring:**
  - **healthchecks.io** (free) raises an alert whenever a backup, cleanup, check or restore test
    doesn't report in on time. If the whole box dies, the reports stop, so you hear about that
    too.
  - **Uptime Kuma** (optional, on the box) checks the websites.

## 1.8 Not yet verified

These get checked during setup:
- **OVH's installer:** that it gives us a large `/srv` partition on RAID1 (step 2). Simpler than the
  old requirement, which needed a partition to hand to LVM.
- **Our pgBackRest configuration**, which is now the only unproven part. Step 13 is the proof, and
  it runs weekly thereafter.
- ~~**OVH Backup Agent:** its price and availability in Europe.~~ **Answered 2026-09-12** — £0 agent
  + £0.0061/GB/month, live on the UK and FR sites. Eco/Rise eligibility is the one open question;
  check the control panel after delivery.
- **Google's download price:** check it on the GCP pricing page.
- **Real restore speed, and that a point-in-time target is honoured:** measured in step 13.
- **The tuning figures in step 9b**, which are assembled from several sources rather than quoted
  from one. Benchmark them.
- ~~The "LVM freezes the filesystem" detail~~ — **moot.** Nothing depends on it any more.

---

# Part 2 — Step-by-step setup

**About two working days in total.** Do the steps in order. Every step ends with a **✅ Done
when** check. Don't move on until it passes.

> **Steps 3–7 are also a script:** `deploy/ovh/bootstrap.sh` runs them as idempotent phases
> (`base`, `lvm`, `tailscale`, `lockdown`, `docker`, `verify`), so a rebuild is a handful of
> commands rather than copy-paste. The steps below remain the explanation of *why*; where the two
> differ, fix the script. `bootstrap.sh verify` re-checks every "Done when" in Steps 3–7 at once.

## Step 0 — Before you order (15 min)

1. **Password manager:** make an entry called **"OVH box"**. Everything secret below goes in it.
2. **SSH key:** in the OVH control panel, go to **Account → SSH keys** and add your laptop's
   public key (`cat ~/.ssh/id_ed25519.pub`).
3. **healthchecks.io:** sign up (free) and create **five checks**:

   | Check name | Period | Grace | What it watches |
   | --- | --- | --- | --- |
   | pg-incr | 1 hour | 30 minutes | the hourly incremental backup |
   | pg-diff | 1 day | 2 hours | the daily differential |
   | pg-full | 1 week | 1 day | the weekly full backup |
   | pg-check | 6 hours | 1 hour | `pgbackrest check` — is archiving reaching Google? |
   | pg-drill | 1 week | 1 day | the real timed restore |
   | box-files | 1 day | 2 hours | the nightly restic copy of config files |

   Copy each check's ping URL into the password manager.
4. **Tailscale:** sign up (the free plan covers two people) and install it on your laptop.

✅ **Done when:** you have the SSH key uploaded, five ping URLs saved, and Tailscale running on
your laptop.

## Step 1 — Order the server (10 min)

1. Go to **kimsufi.com → Rise → RISE-L**.
2. Choose datacentre **Gravelines** or **Roubaix** (France).
3. Storage: take a bigger-disk option if one is offered cheaply. Keep it **mirrored**.
4. Pay monthly.

✅ **Done when:** OVH emails you that the server is delivered, with its IP address. Save the IP
in the password manager.

## Step 2 — Install Debian with the right disk layout (30 min)

In the OVH control panel, open the server and choose **Reinstall** (or **Install**). Pick
**Debian 12**, then **custom partitioning**:

| Mount | Filesystem | RAID | Size |
| --- | --- | --- | --- |
| `/boot` | ext4 | RAID1 | 1 GB |
| `/` | ext4 | RAID1 | 60 GB |
| swap | swap | — | 8 GB |
| `/srv` | ext4 | RAID1 | **all remaining space** |

`/srv` is where everything lives: the one Postgres, each app's compose file and `.env`, and
Docker's image store. **Plain ext4 on RAID1 — no LVM, no thin pool.** Step 4 only creates
directories in it. Select your SSH key.

✅ **Done when:** `ssh debian@<IP>` logs you in. (On Debian images OVH's default user is
`debian`.)

## Step 3 — First login and updates (10 min)

```bash
sudo -i
apt update && apt full-upgrade -y
apt install -y lvm2 thin-provisioning-tools curl git jq unattended-upgrades nftables
dpkg-reconfigure -plow unattended-upgrades      # answer Yes: security updates only
hostnamectl set-hostname box1
reboot
```

✅ **Done when:** you can log back in and `cat /proc/mdstat` shows every `md` device as `[UU]`
(both disks healthy).

## Step 4 — Lay out the disk (5 min)

> **This step used to build an LVM thin pool with a virtual disk per app, so that a live database
> could be safely snapshotted and file-copied. pgBackRest removes that need** — see
> `design/2026-09-12-postgres-native-backups.md` §6. What is left is a directory per app.

```bash
sudo -i
findmnt /srv                 # the installer's partition; keep it, it just needs subdirectories
mkdir -p /srv/apps/{caddy,postgres,bob,ops}
mkdir -p /srv/backup/spool   # pgBackRest's async spool (step 9c)
```

Docker's image store is the one thing worth putting out of the way, because it is large and
rebuildable, and on a small root partition it will fill it:

```bash
# If /srv is a separate, large partition (the usual OVH layout), move Docker onto it.
systemctl is-active --quiet docker && systemctl stop docker
mkdir -p /srv/docker
# Point Docker at it via /etc/docker/daemon.json in step 7 ("data-root").
```

✅ **Done when:** `df -h /srv` shows the large partition, and `ls /srv/apps` lists the four
directories.

**Where state lives now:**

| What | Where | Backed up by |
| --- | --- | --- |
| Every app's database | one Postgres, `/srv/apps/postgres/data` | **pgBackRest → GCS, continuously** (step 9) |
| Caddy's certificates, each app's `.env`, compose files | `/srv/apps/<app>/` | the nightly restic pass (step 9e) |
| Docker images and caches | `/srv/docker` | **nothing, on purpose** — rebuildable |
| Bob's session snapshots, artifacts, datasets | Google Cloud Storage | Google |

## Step 5 — Tailscale and SSH lock-down (15 min)

```bash
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up                  # open the printed link and approve the box
tailscale ip -4               # note the box's Tailscale IP (100.x.y.z)
```

**From your laptop, before changing SSH:** confirm `ssh debian@<tailscale-ip>` works.

```bash
cat > /etc/ssh/sshd_config.d/10-hardening.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
EOF
systemctl reload ssh
```

✅ **Done when:** `ssh debian@<tailscale-ip>` still works from a **new** terminal.

## Step 6 — Firewall (15 min)

This allows web traffic from anywhere and everything else only over Tailscale. It is its own
table, so it never touches Docker's rules. **Don't** use `flush ruleset`: that would wipe Docker's
rules.

```bash
cat > /etc/nftables.conf <<'EOF'
#!/usr/sbin/nft -f
# Host firewall. Its own table only; Docker manages its own tables. Never "flush ruleset".
table inet host
delete table inet host
table inet host {
  chain input {
    type filter hook input priority 0; policy drop;
    ct state established,related accept
    ct state invalid drop
    iif lo accept
    iifname "tailscale0" accept                 # admin: SSH, dashboards
    iifname "docker0" accept
    iifname "br-*" accept                       # container → host traffic
    meta l4proto { icmp, ipv6-icmp } accept
    tcp dport { 80, 443 } accept                # Caddy
    udp dport 443 accept                        # HTTP/3
    udp dport 41641 accept                      # Tailscale direct connections
  }
}
EOF
systemctl enable --now nftables
nft list table inet host
```

✅ **Done when:**
- `ssh debian@<tailscale-ip>` still works;
- `ssh debian@<public-ip>` from your laptop now **times out**;
- 💻 from your laptop, `nmap -Pn <public-ip>` shows only 80 and 443 (443 closed or filtered is
  fine until Caddy runs).

If you lock yourself out, use **Rescue mode** in the OVH control panel, mount the root disk, and
fix `/etc/nftables.conf`.

## Step 7 — Docker (15 min)

```bash
# Docker's official apt repository
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable" \
  > /etc/apt/sources.list.d/docker.list
apt update && apt install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

cat > /etc/docker/daemon.json <<'EOF'
{
  "data-root": "/srv/docker",
  "log-driver": "local",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true,
  "default-address-pools": [ { "base": "172.20.0.0/14", "size": 24 } ]
}
EOF
systemctl restart docker
usermod -aG docker debian
docker run --rm hello-world
```

✅ **Done when:** `hello-world` prints its message, and `docker info | grep 'Docker Root Dir'`
shows `/srv/docker`, not the small root disk.

## Step 8 — Google side: the backup bucket and key (15 min, 💻 laptop)

```bash
gcloud storage buckets create gs://webkit-servers-ovh-backups \
  --project=webkit-servers --location=europe-west1 \
  --uniform-bucket-level-access --soft-delete-duration=14d

gcloud iam service-accounts create ovh-backup --project=webkit-servers \
  --display-name="OVH box backups"
gcloud storage buckets add-iam-policy-binding gs://webkit-servers-ovh-backups \
  --member=serviceAccount:ovh-backup@webkit-servers.iam.gserviceaccount.com \
  --role=roles/storage.objectAdmin

gcloud iam service-accounts keys create ovh-backup-key.json \
  --iam-account=ovh-backup@webkit-servers.iam.gserviceaccount.com
scp ovh-backup-key.json debian@<tailscale-ip>:/tmp/
shred -u ovh-backup-key.json      # the only copies now: the box, and the password manager
```

**Why soft delete matters:** if the box is ever hacked and the attacker deletes the backups,
Google keeps them for 14 more days, and the box's key has no permission to purge them early.

If key creation is refused, an organisation policy blocks service-account keys. Allow it for
this one service account.

✅ **Done when:** `gcloud storage buckets describe gs://webkit-servers-ovh-backups` shows
`softDeletePolicy` at 14 days, and the key is in `/tmp` on the box.

## Step 9 — The one Postgres, and pgBackRest backups (2–3 h)

> Rewritten 2026-09-12. The design, the version choice, the extension list and every configuration
> number below are argued in **`design/2026-09-12-postgres-native-backups.md`** — read §9 before
> changing any of them. ⚠️ The tuning figures are assembled from several sources rather than quoted
> from one; they are a sound starting point, not a benchmark result.

### 9a. Build the Postgres image

**PostgreSQL 18** (supported to 2030-11-14). The official image *is* the PostgreSQL project's own
Debian packages, so this adds extensions without putting anyone else's release cadence between us
and a security patch.

```bash
mkdir -p /srv/apps/postgres && cd /srv/apps/postgres
cat > Dockerfile <<'EOF'
FROM postgres:18-bookworm
# The PGDG apt repo is already configured in the official image.
RUN apt-get update && apt-get install -y --no-install-recommends \
      postgresql-18-pgvector \
      postgresql-18-postgis-3 postgresql-18-postgis-3-scripts \
      postgresql-18-cron \
      postgresql-18-partman \
      postgresql-18-pgaudit \
      postgresql-18-repack \
      pgbackrest \
      curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# Full-text search: ParadeDB's pg_search is the one component not in PGDG (AGPL-3.0).
# Pin the version; check https://github.com/paradedb/paradedb/releases for the current one.
ARG PG_SEARCH_VER=0.25.9
RUN curl -fsSL -o /tmp/pg_search.deb \
      "https://github.com/paradedb/paradedb/releases/download/v${PG_SEARCH_VER}/postgresql-18-pg-search_${PG_SEARCH_VER}-1PARADEDB-bookworm_amd64.deb" \
 && apt-get update && apt-get install -y /tmp/pg_search.deb \
 && rm -f /tmp/pg_search.deb && rm -rf /var/lib/apt/lists/*
EOF
```

### 9b. Configuration

```bash
mkdir -p /srv/apps/postgres/{data,conf,backup-conf}
cat > /srv/apps/postgres/conf/99-box.conf <<'EOF'
# ── Memory. 128 GB box. At ~24 GB of total data this caches everything. ──
shared_buffers = 32GB
effective_cache_size = 96GB
work_mem = 32MB                  # PER sort/hash NODE, not per query. See 9d.
maintenance_work_mem = 2GB
autovacuum_work_mem = 1GB
wal_buffers = 64MB

# ── Parallelism. 32 threads, but four tenants need headroom. ──
max_worker_processes = 32
max_parallel_workers = 16
max_parallel_workers_per_gather = 4
max_parallel_maintenance_workers = 4

# ── Local NVMe, not a spinning disk. ──
random_page_cost = 1.1
effective_io_concurrency = 200
io_method = worker               # PG18. Benchmark io_uring later; liburing IS available.
io_workers = 8
huge_pages = on                  # refuses to start without them — see the warning below

# ── Autovacuum ──
autovacuum_max_workers = 6
autovacuum_vacuum_cost_limit = 2000

# ── WAL archiving for pgBackRest. archive_timeout is what bounds data loss. ──
wal_level = replica
archive_mode = on                # NOT "always" — pgBackRest refuses archive_mode=always
archive_timeout = 60s            # PG docs: "a minute or so is usually reasonable"
archive_command = 'pgbackrest --stanza=main archive-push %p'
max_wal_size = 32GB
wal_compression = zstd
summarize_wal = off              # drives pg_basebackup's incremental, which pgBackRest does not use

# ── Extensions needing preload. Adding to this list is a RESTART of every app. ──
# pgaudit is in this list because it MUST be: `CREATE EXTENSION pgaudit` fails outright
# with "pgaudit must be loaded via shared_preload_libraries". Verified by running it
# 2026-09-12 — the research had not listed it as a preload extension.
shared_preload_libraries = 'pg_cron,pgaudit,pg_search'
cron.database_name = 'postgres'  # pg_cron lives in exactly ONE database per cluster

# ── Logging: name the offender before it becomes an incident ──
log_min_duration_statement = '1s'
log_temp_files = 0
log_checkpoints = on
log_autovacuum_min_duration = '1s'
EOF
```

🔴 **`huge_pages = on` means Postgres refuses to start without them.** That is deliberate — it
fails loudly rather than silently losing the benefit — but it turns *"bumped `shared_buffers`,
forgot the huge pages"* into a **boot failure on the machine taking bookings.** Never change
`shared_buffers` without redoing this:

```bash
docker compose -f /srv/apps/postgres/compose.yml run --rm --entrypoint \
  postgres postgres -C shared_memory_size_in_huge_pages      # prints the number needed
# add ~5%, then:
echo 'vm.nr_hugepages = 16800' > /etc/sysctl.d/60-hugepages.conf   # ← use the real number
sysctl --system
echo never > /sys/kernel/mm/transparent_hugepage/enabled           # THP off; make it persistent
```

Also set the NVMe scheduler to `none` (persist it in a udev rule):

```bash
for d in /sys/block/nvme*/queue/scheduler; do echo none > "$d"; done
```

### 9c. pgBackRest → Google Cloud Storage

The bucket and service-account key come from **step 8**. `restic` is no longer the database's
backup tool, so step 8's bucket is now pgBackRest's repository.

```bash
install -d -m 700 /etc/pgbackrest
mv /tmp/ovh-backup-key.json /etc/pgbackrest/gcs-sa.json
chmod 600 /etc/pgbackrest/gcs-sa.json
openssl rand -base64 48 > /etc/pgbackrest/repo-passphrase
chmod 600 /etc/pgbackrest/repo-passphrase
cat /etc/pgbackrest/repo-passphrase   # ← PASSWORD MANAGER, NOW. Lose it = lose every backup.

cat > /etc/pgbackrest/pgbackrest.conf <<EOF
[global]
repo1-type=gcs
repo1-gcs-bucket=webkit-servers-ovh-backups
repo1-gcs-key=/etc/pgbackrest/gcs-sa.json
repo1-path=/pgbackrest
repo1-cipher-type=aes-256-cbc
repo1-cipher-pass=$(cat /etc/pgbackrest/repo-passphrase)
repo1-retention-full=4
repo1-retention-diff=14
repo1-bundle=y
repo1-block=y
compress-type=zst
process-max=8
start-fast=y
delta=y
log-level-console=warn
log-level-file=info

[global:archive-push]
archive-async=y
spool-path=/srv/backup/spool
process-max=4

[main]
pg1-path=/srv/apps/postgres/data
EOF
chmod 600 /etc/pgbackrest/pgbackrest.conf
```

> ⚠️ **We are not on Google Compute Engine**, so pgBackRest cannot pick up credentials from
> instance metadata (`repo1-gcs-key-type=auto`). The service-account key must be a file on disk.
> Treat it as a credential: mode 600, and a copy only in the password manager.
>
> **`repo1-cipher-pass` is in a config file.** That is why the file is 600 and why the passphrase
> is in the password manager. Without it the repository is unreadable — including by us.

Bring Postgres up, then create the repository:

```bash
cat > /srv/apps/postgres/compose.yml <<'EOF'
services:
  postgres:
    build: .
    restart: unless-stopped
    shm_size: 1g
    command: >
      postgres -c config_file=/etc/postgresql/postgresql.conf
    ports: ["127.0.0.1:5432:5432"]
    environment:
      POSTGRES_PASSWORD_FILE: /run/secrets/pg-superuser
    volumes:
      - /srv/apps/postgres/data:/var/lib/postgresql/data
      - /srv/apps/postgres/conf:/etc/postgresql/conf.d:ro
      - /srv/backup/spool:/srv/backup/spool
      - /etc/pgbackrest:/etc/pgbackrest:ro
    secrets: [pg-superuser]
secrets:
  pg-superuser:
    file: /srv/apps/postgres/secrets/superuser
EOF
mkdir -p /srv/apps/postgres/secrets
openssl rand -hex 24 > /srv/apps/postgres/secrets/superuser
chmod 600 /srv/apps/postgres/secrets/superuser   # ← password manager too

cd /srv/apps/postgres && docker compose up -d --build
docker compose exec -u postgres postgres pgbackrest --stanza=main stanza-create
docker compose exec -u postgres postgres pgbackrest --stanza=main check
docker compose exec -u postgres postgres pgbackrest --stanza=main backup --type=full
docker compose exec -u postgres postgres pgbackrest --stanza=main info
```

✅ **Done when:** `check` passes (which proves WAL is reaching Google) and `info` lists one full
backup.

### 9d. One database and one role per app — the guardrails

🔴 **This is the price of one Postgres, and it is paid here.** One memory pool, one WAL, one
restart. *A forum reindex evicts the booking system's hot pages; `work_mem` applies per sort node
rather than per query, so one runaway agent query can exhaust memory on the machine that processes
payments.* Set the limits on day one, not after the first incident.

```sql
-- Repeat per app. No app gets superuser.
CREATE ROLE app_booking LOGIN PASSWORD '…';
CREATE DATABASE booking OWNER app_booking;
REVOKE CONNECT ON DATABASE booking FROM PUBLIC;     -- apps cannot reach each other's data
GRANT  CONNECT ON DATABASE booking TO app_booking;

-- Tight for the payment path, loose for the agent. This asymmetry IS the mitigation.
ALTER ROLE app_booking  SET statement_timeout = '15s';
ALTER ROLE app_forum    SET statement_timeout = '30s';
ALTER ROLE app_internal SET statement_timeout = '120s';
ALTER ROLE app_bob      SET statement_timeout = '300s';
ALTER ROLE app_booking  SET work_mem = '16MB';
ALTER ROLE app_internal SET work_mem = '128MB';
ALTER ROLE app_booking  SET idle_in_transaction_session_timeout = '30s';
ALTER ROLE app_internal SET lock_timeout = '5s';
```

Then, per database that needs them: `CREATE EXTENSION vector;`, `postgis;`, `pg_partman;`,
`pg_search;`. **Verified on a throwaway PG18 on 2026-09-12** — every version is the one the
research predicted:

| Extension | Version installed | Needs preloading? |
| --- | --- | --- |
| `vector` (pgvector) | **0.8.6** | no |
| `postgis` | **3.6.4** | no |
| `pg_partman` | **5.5.0** | no |
| `pg_cron` | **1.6** | **yes** |
| `pg_repack` | **1.5.3** | no |
| `pgaudit` | **18.0** | **yes** — fails outright without it |
| `pgbackrest` (the binary, not an extension) | **2.59.1** | — |

And `postgres:18-bookworm` reports `PostgreSQL 18.6 (Debian 18.6-1.pgdg12+2)`, which confirms the
official image is carrying the PGDG package build.

Two notes that bite later:

- **`pg_cron` installs in exactly one database per cluster** (`cron.database_name`, set to
  `postgres` above). Schedule work in other databases with `cron.schedule_in_database()`.
- **`ALTER EXTENSION … UPDATE` must be run in every database separately.** Easy to miss one.

**Also add pgbouncer** with one pool per database and `max_db_connections` per pool, so no app can
eat the whole connection budget, plus `reserved_connections` so an operator can always get in while
something is saturating the server.

### 9e. Config files: a small nightly restic pass

Everything that is not a database is now a handful of near-static files — Caddy's certificates and
each app's `.env` and compose file. No snapshot is needed, because nothing is writing to them.

```bash
curl -fsSL https://github.com/restic/restic/releases/download/v0.18.0/restic_0.18.0_linux_amd64.bz2 \
  | bunzip2 > /usr/local/bin/restic && chmod +x /usr/local/bin/restic

install -d -m 700 /etc/box-files
cp /etc/pgbackrest/gcs-sa.json /etc/box-files/
openssl rand -base64 32 > /etc/box-files/restic-password && chmod 600 /etc/box-files/restic-password
cat /etc/box-files/restic-password   # ← PASSWORD MANAGER

cat > /etc/box-files.env <<'EOF'
RESTIC_REPOSITORY=gs:webkit-servers-ovh-backups:/files
RESTIC_PASSWORD_FILE=/etc/box-files/restic-password
GOOGLE_PROJECT_ID=webkit-servers
GOOGLE_APPLICATION_CREDENTIALS=/etc/box-files/gcs-sa.json
HC_FILES=https://hc-ping.com/REPLACE-ME
EOF
chmod 600 /etc/box-files.env
set -a; . /etc/box-files.env; set +a; restic init
```

```bash
cat > /usr/local/sbin/box-files <<'EOF'
#!/usr/bin/env bash
# NIGHTLY. Config files, secrets and compose files only. Databases are pgBackRest's job.
set -euo pipefail
set -a; . /etc/box-files.env; set +a
trap 'curl -fsS -m 10 "$HC_FILES/fail" >/dev/null || true' ERR
restic backup --quiet --host box1 \
  --exclude /srv/apps/postgres/data \
  --exclude /srv/apps/bob/pg-data \
  /srv/apps /etc/pgbackrest /etc/box-files.env
restic forget --quiet --keep-daily 30 --keep-monthly 12 --prune
curl -fsS -m 10 "$HC_FILES" >/dev/null
EOF
chmod +x /usr/local/sbin/box-files
```

### 9f. The timers

```bash
timer() {   # timer <name> <OnCalendar> <command…>
  cat > "/etc/systemd/system/$1.service" <<EOF
[Unit]
Description=$1
[Service]
Type=oneshot
ExecStart=$3
EOF
  cat > "/etc/systemd/system/$1.timer" <<EOF
[Unit]
Description=$1 schedule
[Timer]
OnCalendar=$2
Persistent=true
RandomizedDelaySec=60
[Install]
WantedBy=timers.target
EOF
}
PGB="/usr/local/sbin/pgb"
cat > "$PGB" <<'EOF'
#!/usr/bin/env bash
# pgb <hc-url> <pgbackrest args…>   — run a pgBackRest command and report to healthchecks.io
set -euo pipefail
hc=$1; shift
trap 'curl -fsS -m 10 "$hc/fail" >/dev/null || true' ERR
curl -fsS -m 10 "$hc/start" >/dev/null || true
docker compose -f /srv/apps/postgres/compose.yml exec -T -u postgres postgres \
  pgbackrest --stanza=main "$@"
curl -fsS -m 10 "$hc" >/dev/null
EOF
chmod +x "$PGB"

timer pg-incr  '*-*-* *:10:00'      "$PGB https://hc-ping.com/PG-INCR  backup --type=incr"
timer pg-diff  '*-*-* 02:30:00'     "$PGB https://hc-ping.com/PG-DIFF  backup --type=diff"
timer pg-full  'Sun *-*-* 03:30:00' "$PGB https://hc-ping.com/PG-FULL  backup --type=full"
timer pg-check '*-*-* 00/6:45:00'   "$PGB https://hc-ping.com/PG-CHECK check"
timer box-files '*-*-* 04:30:00'    "/usr/local/sbin/box-files"
systemctl daemon-reload
systemctl enable --now pg-incr.timer pg-diff.timer pg-full.timer pg-check.timer box-files.timer
systemctl list-timers 'pg-*' 'box-*'
```

Paste the real ping URLs from step 0 in place of the placeholders.

✅ **Done when:**
- `systemctl list-timers` lists all five with a next run;
- `pgbackrest --stanza=main info` shows a full plus at least one incremental;
- healthchecks.io shows **pg-incr**, **pg-check** and **box-files** green.

**Worst-case data loss is now `archive_timeout`, about 60 seconds** — down from an hour — and any
moment in the retention window can be restored to, which a block snapshot could not do at all.

## Step 10 — Caddy (20 min)

```bash
mkdir -p /srv/apps/caddy/{data,config}
cat > /srv/apps/caddy/compose.yml <<'EOF'
services:
  caddy:
    image: caddy:2
    network_mode: host          # reaches every app on 127.0.0.1:<port>
    restart: unless-stopped
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./data:/data            # certificates + ACME account: on the backed-up disk
      - ./config:/config
EOF
cat > /srv/apps/caddy/Caddyfile <<'EOF'
{
	email kai@badcode.dev
}

bob.badcode.tv {
	# No `encode` here: compression would hold back Bob's live chat stream (SSE).
	reverse_proxy 127.0.0.1:8100 {
		flush_interval -1
	}
}
EOF
cd /srv/apps/caddy && docker compose up -d
```

After any Caddyfile change, reload with zero downtime:

```bash
docker compose -f /srv/apps/caddy/compose.yml exec -w /etc/caddy caddy caddy reload
```

✅ **Done when:** `docker compose -f /srv/apps/caddy/compose.yml logs caddy` shows it running.
It can't get Bob's certificate until the DNS record exists (step 12).

## Step 11 — Agent Bob (1–2 h)

### 11a. Code and folders

```bash
cd /srv/apps/bob
git clone https://github.com/badcodetv/agent-bob.git src
mkdir -p agentd-data secrets
```

Create Bob's database and role in the one Postgres (step 9d's pattern):

```sql
CREATE ROLE app_bob LOGIN PASSWORD '…';           -- password manager
CREATE DATABASE bob OWNER app_bob;
REVOKE CONNECT ON DATABASE bob FROM PUBLIC;
GRANT  CONNECT ON DATABASE bob TO app_bob;
ALTER ROLE app_bob SET statement_timeout = '300s'; -- loose: agent work is slow by nature
\c bob
CREATE EXTENSION IF NOT EXISTS vector;             -- agentdb's migrations expect pgvector
```

### 11b. Bob's Google key

Bob keeps session snapshots in GCS. The service account `agent-bob-runtime` already exists; make
it a key (💻 laptop), copy it over, then delete the local copy:

```bash
gcloud iam service-accounts keys create gcp-key.json \
  --iam-account=agent-bob-runtime@webkit-servers.iam.gserviceaccount.com
scp gcp-key.json debian@<tailscale-ip>:/tmp/ && shred -u gcp-key.json
```

On the box:

```bash
mv /tmp/gcp-key.json /srv/apps/bob/secrets/ && chmod 600 /srv/apps/bob/secrets/gcp-key.json
```

### 11c. The OVH layer on top of Bob's normal compose file

> 🔴 **One decision to make here, and now is the cheapest it will ever be.** Bob's own
> `docker-compose.yml` ships a bundled Postgres (`pgvector/pgvector:pg16`) and hardcodes
> `DATABASE_URL` at `docker-compose.yml:96` to point at it. "One Postgres" says Bob should use the
> box's Postgres 18 instead.
>
> **Recommended: point Bob at the box's Postgres.** A fresh Bob on a new box **has no data**, so the
> PG16 → PG18 move costs nothing today; in six months it means migrating live sessions. It also puts
> Bob's conversations under pgBackRest from the first message rather than leaving a second,
> separately-handled database on the box. ~~The risk is that `agentdb`'s migrations must run clean
> on PG18.~~ ✅ **Tested 2026-09-12 and they do**: `go test ./agentdb/` against a throwaway
> PostgreSQL 18.6 returns `ok … 232.342s`, all 49 migrations apply, and the HNSW pgvector index and
> the jsonb label indexes are all created — `design/2026-09-12-postgres-native-backups.md` §9.6a.
> **The recommendation is no longer a judgement call.**
>
> The alternative (keep Bob's bundled PG16 for round one) is lower risk on day one and higher cost
> later, and it means pgBackRest needs a second stanza or Bob's database is unbacked.
>
> The block below takes the recommended route. To keep the bundled Postgres instead, drop the
> `postgres` override and the `DATABASE_URL` line.

```bash
cat > /srv/apps/bob/compose.ovh.yml <<'EOF'
# Layered on top of src/docker-compose.yml:
#  - points Bob at the box's one Postgres and disables its bundled one
#  - caps Docker-in-Docker so it cannot starve other apps
#  - mounts the Google key and the project map
services:
  postgres:
    # Bob's bundled Postgres is not used; the box's one Postgres is. `scale: 0` keeps the
    # service defined (so healthcheck references resolve) without running it.
    deploy:
      replicas: 0
  agentd:
    environment:
      # host.docker.internal resolves to the box; Postgres listens on 127.0.0.1:5432 only.
      DATABASE_URL: postgres://app_bob:PASSWORD@host.docker.internal:5432/bob?sslmode=disable
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - /srv/apps/bob/secrets/gcp-key.json:/gcp/key.json:ro
      - /srv/apps/bob/secrets/projects.json:/secrets/projects.json:ro
  dind:
    cpus: 24
    mem_limit: 96g
volumes:
  agentd-data:
    driver: local
    driver_opts: { type: none, o: bind, device: /srv/apps/bob/agentd-data }
  # dind-data stays a normal Docker volume in /srv/docker (rebuildable image cache).
EOF

cat > /srv/apps/bob/up.sh <<'EOF'
#!/usr/bin/env bash
# Start or update Bob:  /srv/apps/bob/up.sh
set -euo pipefail
cd /srv/apps/bob
docker compose --project-directory src -f src/docker-compose.yml -f compose.ovh.yml up -d --build "$@"
EOF
chmod +x /srv/apps/bob/up.sh
```

### 11d. Production settings (`/srv/apps/bob/src/.env`)

The project map (who may log in) lives in its own file, not inline in `.env`, so that adding
someone is "edit the file", not "edit `.env` and restart the stack" — agentd re-reads
`AGENTKIT_PROJECT_MAP_FILE` on SIGHUP and every `AGENTKIT_PROJECT_MAP_RELOAD` (default 60s; see
`.env.example`). A malformed rewrite is logged and changes nothing; the previous map keeps
serving.

```bash
cat > /srv/apps/bob/secrets/projects.json <<'EOF'
{"kaiyadavenport@gmail.com":["*"]}
EOF
chmod 600 /srv/apps/bob/secrets/projects.json

cat > /srv/apps/bob/src/.env <<EOF
# ── Network: only reachable from Caddy, never from the internet directly ──
WEB_PORT=127.0.0.1:8100
AGENTKIT_PUBLIC_BASE_URL=https://bob.badcode.tv

# ── Login: Google, and who may log in (see compose.ovh.yml's projects.json mount) ──
AGENTKIT_JWT_SECRET=$(openssl rand -hex 32)
GOOGLE_CLIENT_ID=PASTE-FROM-YOUR-LAPTOP-.env
AGENTKIT_PROJECT_MAP_FILE=/secrets/projects.json

# ── Database password (Postgres only listens inside the stack) ──
POSTGRES_PASSWORD=$(openssl rand -hex 24)

# ── Model: API-key mode. Leave CLAUDE_CODE_OAUTH_TOKEN blank — see below ──
CLAUDE_CODE_OAUTH_TOKEN=
ANTHROPIC_API_KEY=

# ── Default daily token budget for a BRAND NEW project (onboarding-work-plan
# §1.3, go/cmd/agentd/defaultbudgets.go). Applies only to a project that has
# never had a settings row written — an invited friend's first project, not
# an existing one. Unset or 0 = off, i.e. NOT braked — fill both numbers in
# before inviting anyone (see "Why API-key mode" below and OM-8 further down).
# Kai chose these on 2026-09-12, deliberately low: the default is a brake he
# raises per project once he can see what one actually costs, not an allowance.
# Raising it is one console edit by the operator; starting high and discovering
# the number later is the mistake that cannot be undone.
AGENTKIT_DEFAULT_DAILY_TOKENS_SOFT=50000
AGENTKIT_DEFAULT_DAILY_TOKENS_HARD=100000

# ── Google Cloud Storage: artifacts, datasets, session snapshots ──
AGENTKIT_BLOB_BACKEND=gcs
GCS_BUCKET=webkit-servers-agent-bob
GCP_PROJECT=webkit-servers
GOOGLE_APPLICATION_CREDENTIALS=/gcp/key.json

# ── Image registry: pull session base images from Artifact Registry ──
# Without these the backend defaults to "blobarchive", whose EnsurePresent is
# a no-op (go/imageregistry/blobarchive: `return nil`), so an image that is not
# ALREADY inside DinD is never pulled and the session fails to start. That is
# fine for the sandbox image, which init-sandbox builds locally, and fatal for
# any image built elsewhere and pushed — Agent Wolf's, for one.
AGENTKIT_REGISTRY_BACKEND=ociregistry
AGENTKIT_REGISTRY_AUTH=gcp
GCP_REGION=europe-west1
GCP_AR_REPO=agent-bob
EOF
chmod 600 /srv/apps/bob/src/.env
nano /srv/apps/bob/src/.env     # fill GOOGLE_CLIENT_ID and the two budget numbers
```

**Two consequences of the registry lines, worth knowing before you set them:**
- **Locally built images still work.** `ociregistry`'s `EnsurePresent` inspects first and skips
  the pull when the image is already in DinD, so the sandbox and `core` images that `init-sandbox`
  builds are untouched. Do **not** set `AGENTKIT_REGISTRY_ALWAYS_PULL=true` on the box: it forces a
  pull even for those, and they are not in any registry.
- 🟡 **It decides where unreclaimed session archives pile up.** Idle-session archives are never
  reclaimed automatically — on **either** backend, and they never were: the snapshot reaper only
  sweeps the named-image catalogue, and an archive is not a catalogue row
  (`go/snapshot_reaper.go`; `go/runner.go:878-885` writes `SetSnapshotHandle`, not
  `CreateCustomImage`), so `snapshot_ttl_days` has never applied to one. This setting does not
  cause that and reverting it would not fix it. What it changes is the destination: `blobarchive`
  leaves a gzipped `docker save` per archive in GCS (cappable with a bucket lifecycle rule);
  `ociregistry` leaves one repo per session at `<registry>/<session-id>:latest` plus an untagged
  leftover per cycle (cappable with an Artifact Registry cleanup policy on `agent-bob`). Layer
  dedup keeps each push small, so it is a slow leak, not a cliff — but it is unbounded. **The real
  fix is parked** as "a session archiving policy with a stated restore window"
  (`design/2026-09-12-wolf-deployment.md`); Kai has declined to design it yet. Setting a cleanup
  policy is itself a decision about how far back a session can be restored, so ask first.

**Why API-key mode, and the subscription token stays blank.** The box runs on
`ANTHROPIC_API_KEY` (a metered key), never `CLAUDE_CODE_OAUTH_TOKEN` (the Claude subscription
token from `claude setup-token`) — if both are set, the OAuth token wins and the box would be
billed to the subscription instead (`go/cmd/agentd/main.go:220-235`). Two reasons this box stays
on the key:
- **The daily budget above only bounds a metered key.** `daily_tokens_hard` stops new
  non-interactive jobs and is read against usage the store can price
  (`go/agentdb/token_usage.go`); a subscription's own usage is not something Bob's ledger meters
  or caps, so a guest project on the subscription would have no brake at all.
- **The subscription authenticates one login, not one guest per project.** It carries only so
  many concurrently-authenticated sessions before something else using the same login gets
  signed out; a shared API key has no such ceiling.

**Four rules for this file:**
- **Keep a login mode set.** With no `GOOGLE_CLIENT_ID` (and no test login), Bob runs with **no
  authentication at all**.
- **Add Bob's address to Google login.** In the Google Cloud console, open the OAuth client
  behind `GOOGLE_CLIENT_ID` and add **`https://bob.badcode.tv`** as an authorised JavaScript
  origin.
- **Only the `web` service is published, and only on 127.0.0.1.** Bob's API service (`agentd`)
  has an unauthenticated route that spends the Anthropic key (`docs/19-embedding.md`, hazard
  H6). It is safe only while `agentd` is unreachable from outside, which this setup guarantees.
  Never publish another port.
- **Set the default daily budget before the first invite.** Without it, a brand-new guest
  project starts with no spend brake at all (`daily_tokens_hard=0` is off) — see OM-8 below for
  how to check the brake actually fires, not just that a number is set.

**Adding someone later** is editing `/srv/apps/bob/secrets/projects.json` and either waiting for
the next reload (`AGENTKIT_PROJECT_MAP_RELOAD`, default 60s) or sending SIGHUP for an immediate
one: `docker compose --project-directory /srv/apps/bob/src -f /srv/apps/bob/src/docker-compose.yml
-f /srv/apps/bob/compose.ovh.yml kill -s SIGHUP agentd`. No restart, no downtime — this box never
needs a restart to add a user, whichever way the map is edited.

**The message you send them.** One paragraph, every time, no exceptions
(`design/2026-09-11-ovh-compose-hosting.md:288` is the isolation caveat in the third sentence):

> This runs on my own Anthropic key with a small daily token budget, so an unattended job that
> goes wrong stops itself rather than running up an unbounded bill — tell me if you need more
> room. Once you approve your project's charter, an automatic "architect" edits it once a day on
> its own: it creates workers, writes memories and wires triggers without asking either of us
> first, and what it did shows up afterwards for you to keep, edit or revert. Your project's
> containers run on a shared box without the isolation a system holding customer data would need,
> so I'm only sending this to people I trust with that. If that's all fine, here's your link:
> `<url>`.

Send it to people you trust, not a general invite link — see
`docs/guide/for-operators/inviting-someone.md` for the operator's runbook this paragraph lives
in.

### 11e. Start it

```bash
/srv/apps/bob/up.sh
docker compose --project-directory /srv/apps/bob/src -f /srv/apps/bob/src/docker-compose.yml \
  -f /srv/apps/bob/compose.ovh.yml ps
curl -sI http://127.0.0.1:8100 | head -1      # HTTP/1.1 200 OK
docker compose exec -T -u postgres postgres psql -d bob -c '\dt' | head   # agentd's tables exist
```

The first start takes a few minutes: it builds the images and the sandbox.

✅ **Done when:**
- `curl` returns `200`;
- Bob's tables exist in the `bob` database on the box's Postgres, so pgBackRest already covers them;
- `ss -tlnp` shows nothing but `127.0.0.1` for ports 8100 and up **and for 5432**.

### 11f. Verify the spend brake actually fires (OM-8)

Setting the two env vars above only proves a number is stored. It does not prove a hard-stopped
project's jobs actually stop — the ledger that budgets read against was itself broken for a
month before it was fixed (`go/agentdb/token_usage.go:6-9`), so "it's configured" is not the same
claim as "it fires." Do this once, before the first real invite, on a project nobody depends on:

1. **Create a throwaway project.** Log in as the wildcard account and create a new project from
   the console (e.g. `brake-test`).
2. **Set a tiny hard limit.** In that project's Settings, set `daily_tokens_hard` to a very small
   number — small enough that one job's normal usage crosses it (a few hundred tokens, not the
   real default). `PUT /agent/project-settings` is the route underneath, if you'd rather script
   it than click it.
3. **Run a job.** Approve the project's charter and run the architect once (or trigger any
   worker's schedule) so at least one non-interactive job runs and spends tokens against the
   project.
4. **Watch the delivery queue.** Trigger a second job for the same project — run the architect
   again, or wait for its daily schedule — and check `GET /agent/deliveries?status=pending` (or
   the Activity page). With the hard limit already crossed, the delivery should sit at
   **`pending`** rather than dispatching: `go/cmd/agentd/router.go` (`tokenBudget.Allow`) is what
   is refusing it, and it goes out again automatically once the day rolls over stack-local
   midnight, or once you raise the limit.
5. **Delete the throwaway project.** There is no dedicated delete-project route (grep confirms:
   `grep -rn "DELETE /agent/project" go/httpapi go/cmd/agentd` is empty) — remove
   `brake-test` from `/srv/apps/bob/secrets/projects.json` instead. That revokes login access;
   its sessions, workers and data are not separately purged, so don't put anything in a
   throwaway project you'd mind existing.

✅ **Done when:** a delivery for the throwaway project actually shows `pending` while the hard
limit is in force, not just a number sitting in Settings.

### 11g. Prove you can add a friend without restarting Bob (A6)

Thread 02 left ticket **A6** — *"the project map reloads without a restart"* — open on purpose,
because it **cannot be proven offline**: the e2e stack supplies the map as **inline JSON**, and
inline wins over the file (`go/cmd/agentd/googleauth.go:249-252`), so the mounted-file watch path
is never exercised there. **The box is the only place this claim can be tested.** Do it once,
before you invite anyone.

🔴 **First, the trap that makes this worth testing at all.** `AGENTKIT_PROJECT_MAP` (inline JSON)
**silently wins** over `AGENTKIT_PROJECT_MAP_FILE`, and `docker-compose.yml:168` passes it through
from `.env`. So a leftover inline value in `.env` disables the whole reload mechanism **with no
error at all** — adding a friend would then need a restart, and you would only find out while
someone waited. Check it is empty:

```bash
grep -n '^AGENTKIT_PROJECT_MAP=' /srv/apps/bob/src/.env   # must print NOTHING
```

Then confirm from the boot log that the file is what got loaded:

```bash
cd /srv/apps/bob
docker compose --project-directory src -f src/docker-compose.yml -f compose.ovh.yml \
  logs agentd | grep -i 'project map'
# want: "[agentd] project map: 1 mapped account(s), N configured project(s)"
```

**The test.** Add a second email to the mounted file, wait, and log in as it — no restart:

```bash
# 1. Add someone. This file is the allowlist; nothing else needs touching.
cat > /srv/apps/bob/secrets/projects.json <<'EOF'
{"kaiyadavenport@gmail.com":["*"],"friend@example.com":["testproject"]}
EOF

# 2. Either wait up to 60s for the timer (AGENTKIT_PROJECT_MAP_RELOAD, default 60s)…
#    …or send SIGHUP for an immediate reload:
docker compose --project-directory src -f src/docker-compose.yml -f compose.ovh.yml \
  kill -s SIGHUP agentd

# 3. Watch it happen. SIGHUP logs first, then the reload result.
docker compose --project-directory src -f src/docker-compose.yml -f compose.ovh.yml \
  logs --tail=20 agentd | grep -i 'project map'
```

You are looking for these two lines:

```
[agentd] project map: SIGHUP received, reloading /secrets/projects.json
[agentd] project map reloaded from /secrets/projects.json: 2 mapped account(s), 1 configured project(s)
```

**`2 mapped account(s)` is the proof** — the count went up without a restart.

**Then the part that actually matters:** have that second account log in at
`https://bob.badcode.tv` and reach its project. A reload that updates a counter but not the login
path would be a false pass.

⚠️ **A malformed file changes nothing and keeps serving the old map**
(`googleauth.go:395-400`, *"keeping the previous map"*). That is the safe behaviour, but it means
**a typo looks like "the reload didn't happen"** rather than like an error. If the count doesn't
move, check the log for `keeping the previous map` before assuming the mechanism is broken.

✅ **Done when:**
- `AGENTKIT_PROJECT_MAP=` is absent from `.env`;
- the reload log line shows the account count going up;
- the new account logs in successfully, **with no restart and no downtime**;
- **tell thread 02 so it can tick A6.**

Note: `api_key_env` and `allowed_origins` live in the same file's `projects` section, so a
successful reload also refreshes the API-key index (`main.go:648-649`). Adding an embedding
application's key is the same edit, with the same no-restart property.

## Step 12 — DNS and your manual test (15 min)

1. At your DNS provider for `badcode.tv`, add an **A record: `bob` → the box's public IP**.
2. Wait a minute. `dig +short bob.badcode.tv` should print the box's IP.
3. Open **https://bob.badcode.tv**. Caddy fetches the certificate on the first visit.
4. Log in with Google and start a session. Send a few messages.

✅ **Done when:** the chat works over HTTPS and replies stream in word by word, not all at once.
**This is Kai's manual test of Bob.**

## Step 13 — The first real restore test (30 min)

**This is the only reason to believe any of the above.** It proves two different things: that a
backup comes back, and — new with WAL archiving — that it comes back **to a chosen moment**, which
is the test that the WAL chain is unbroken.

```bash
cd /srv/apps/postgres
PGB="docker compose exec -T -u postgres postgres pgbackrest --stanza=main"

# 1. Is archiving actually reaching Google, right now?
$PGB check

# 2. Is the repository internally consistent?
$PGB verify

# 3. Write a marker, note the time, then write another.
docker compose exec -T -u postgres postgres psql -c \
  "CREATE TABLE IF NOT EXISTS drill(t timestamptz default now(), note text);
   INSERT INTO drill(note) VALUES ('before');"
sleep 5; TARGET=$(date -u +'%Y-%m-%d %H:%M:%S')+00 ; sleep 5
docker compose exec -T -u postgres postgres psql -c "INSERT INTO drill(note) VALUES ('after');"
$PGB backup --type=incr

# 4. Restore to the moment BETWEEN them, into a scratch directory, and time it.
mkdir -p /srv/drill && chown 999:999 /srv/drill
start=$(date +%s)
$PGB restore --delta --target-action=promote \
    --type=time --target="$TARGET" --pg1-path=/srv/drill
echo "restore took $(( $(date +%s) - start ))s"
```

Then start a throwaway Postgres on `/srv/drill`, let it finish recovery, and check:

```sql
SELECT note FROM drill;      -- must show 'before' and NOT 'after'
```

`before` present and `after` absent is the proof. If `after` is there, the point-in-time target was
not honoured; if neither is there, the WAL chain is broken. **Either is a stop-everything finding.**

```bash
systemctl enable --now pg-drill.timer     # weekly, from step 9f's pattern
rm -rf /srv/drill
```

Write the real number here:

> **Measured restore time:** _____ seconds for _____ GB (date: ______)
>
> **Point-in-time target honoured:** yes / no (date: ______)

✅ **Done when:** both lines above are filled in and **pg-drill** is green on healthchecks.io.
**The box is now live, and its backups are proven.**

# Part 3 — Everyday operations

## Add a new app (15 minutes)

1. **Pick a port** from the table in 1.6 and add the app to that table.
2. **Make its database and role** in the one Postgres, with its own limits (step 9d). Tight
   `statement_timeout` and `work_mem` unless it genuinely needs more.
3. **Put the app at `/srv/apps/<app>/`**, with a compose file following three rules:
   - data only in **bind mounts** under `/srv/apps/<app>/…`, never named Docker volumes
     (those land in `/srv/docker`, which is not backed up);
   - **no local database.** It uses the one Postgres at `127.0.0.1:5432`. If it insists on its own
     datastore, that is migration work — see `design/2026-09-12-postgres-native-backups.md` §3;
   - ports only as `"127.0.0.1:<port>:<container-port>"`, plus `restart: unless-stopped` and
     `cpus:`/`mem_limit:` for anything heavy.
4. **Secrets** go in `/srv/apps/<app>/.env` (mode 600), and in the password manager.
5. **Caddy:** add a site block (`app.example.com { reverse_proxy 127.0.0.1:<port> }`) and reload.
6. **DNS:** point the hostname's A record at the box, **last**, once the app answers on the box.

Nothing else to set up: the database is already backed up continuously, and `/srv/apps/<app>/`'s
config files are in the nightly restic pass.

**Before the first app with customer data:** Bob's Docker-in-Docker must stop being `privileged`
(1.7).

## Update Bob

```bash
cd /srv/apps/bob/src && git pull && /srv/apps/bob/up.sh
```

## Undo a mistake — roll a database back to a chosen moment

This is the thing the old design could not do. You do not need a backup from the right hour; you
name the moment.

```bash
cd /srv/apps/postgres
PGB="docker compose exec -T -u postgres postgres pgbackrest --stanza=main"
$PGB info                                   # what is available, and how far back

# Safest: restore a COPY alongside, look at it, then swap. Never restore over the only copy.
mkdir -p /srv/restore && chown 999:999 /srv/restore
$PGB restore --delta --type=time --target='2026-09-12 14:32:07+00' \
      --target-action=promote --pg1-path=/srv/restore
```

Start a throwaway Postgres on `/srv/restore`, check the data is what you expected, then move the
rows or the whole database across. Delete `/srv/restore` when you are happy.

To roll the **live** database back instead, stop every app first, then restore over
`/srv/apps/postgres/data` with the same command. **Take a fresh `backup --type=incr` before you do
it**, so the current state is still recoverable if the target moment turns out to be wrong.

## The whole box died

Target: **under 2 hours.**

1. Order a new RISE-L (step 1).
2. Do steps 2–8, then install pgBackRest's config from the password manager — the repository
   already exists, so **skip `stanza-create`**. You need the repository passphrase and the Google
   service-account key; without the passphrase the backups are unreadable.
3. Restore the databases:
   ```bash
   pgbackrest --stanza=main restore --pg1-path=/srv/apps/postgres/data
   ```
   Then start Postgres and let it finish recovery.
4. Restore the config files:
   ```bash
   set -a; . /etc/box-files.env; set +a
   restic restore latest --target / --include /srv/apps
   ```
5. Start Caddy, then `/srv/apps/bob/up.sh`.
6. Change the DNS A records to the new IP.

**Before the booking system moves in**, rehearse this once on a second RISE-L rented for a day.

## Where everything is

| What | Where |
| --- | --- |
| Every app's data | **one Postgres**, `/srv/apps/postgres/data` |
| Each app's compose file, `.env`, and any files it owns | `/srv/apps/<app>/` |
| Docker images and caches | `/srv/docker`, **not** backed up (rebuildable) |
| pgBackRest config, repo passphrase, Google key | `/etc/pgbackrest/` (mode 600) |
| The nightly file pass's config | `/etc/box-files.env`, `/etc/box-files/` |
| Schedules | `systemctl list-timers 'pg-*' 'box-*'` |
| Database backups | `gs://webkit-servers-ovh-backups/pgbackrest` (compressed, encrypted) |
| Config-file backups | `gs://webkit-servers-ovh-backups/files` (encrypted) |
| Bob's session snapshots, artifacts, datasets | `gs://webkit-servers-agent-bob`, and Artifact Registry |
| Secrets, off the box | the "OVH box" password-manager entry |

🔴 **Two things in the password manager are unrecoverable if lost:** the pgBackRest repository
passphrase, and the restic password. Neither can be regenerated. Without them every backup is
permanently unreadable.

## Health at a glance

```bash
systemctl list-timers 'pg-*' 'box-*'          # when each job last ran and runs next
cd /srv/apps/postgres && docker compose exec -T -u postgres postgres \
  pgbackrest --stanza=main info               # backups available, and how far back
docker compose exec -T -u postgres postgres \
  pgbackrest --stanza=main check              # is WAL reaching Google right now?
cat /proc/mdstat                              # both disks [UU]
df -h /srv /srv/docker                        # disk headroom
docker compose exec -T postgres psql -U postgres -c \
  "SELECT datname, pg_size_pretty(pg_database_size(datname)) FROM pg_database ORDER BY 2 DESC;"
```

If `pg-check` is green on healthchecks.io, archiving is working. If `pg-drill` is green, a restore
has actually been performed this week.


**Built since:** `deploy/ovh/bootstrap.sh` in this repo covers Steps 3–7. **Still not built:** the
Postgres and pgBackRest setup as repo files (Step 9 is still copy-paste), Uptime Kuma, pgbouncer,
and OVH's Backup Agent switched on as a second copy (priced and recommended in 1.5).

---

# Part 4 — Later: moving the GKE apps (what we already know)

**Not part of the first round.** Kai will move these one at a time. Found read-only on
2026-09-11, re-measured 2026-09-12.

> **The per-app plan lives in `design/2026-09-12-gke-to-box-migration.md`**: real disk usage, the
> order, and the specific gotcha for each app. NoCode Works and Franchise Cloud are **out of
> scope** there — both are being shut down the week of 2026-09-14.

**What's there:**
- **11 apps on one GKE machine:**
  - **booking system:** api and frontend;
  - **franchisecloud:** 7 services, NATS, Elasticsearch 7.5.2;
  - **nocode-works:** 4 services, Elasticsearch 7.5.2, ~30 customer domains;
  - **badcode:** app, worker, n8n, Postgres 17 (ParadeDB), Redis;
  - **small apps:** forum, kellie, quoteright, panwww, zps-apps;
  - **shared:** Postgres **9.6** (1 TB disk) and Redis.
- **Disks today:** 6, provisioned at 1.65 TB — but **only 23.8 GiB is actually used**, measured
  2026-09-12 from the node's kubelet statistics (no exec needed; the command is in
  `design/2026-09-12-gke-to-box-migration.md` §1). Shared Postgres 9.6 **22.7 GiB**, nocode
  Elasticsearch 841 MiB, franchisecloud Elasticsearch 164 MiB, badcode ParadeDB **111 MiB**, redis
  1.8 MiB, forum 30 KB. So ~890 GB is ample, and the 9.6 upgrade is a 20–40 minute job, not an
  all-day outage.

**Gotchas per app:**
- **The shared Postgres 9.6 is five years past end of life.** Upgrade it during its move
  (`pg_upgrade`, or dump and restore). Don't carry 9.6 over.
- **Elasticsearch 7.5.2 is past end of life, and Elastic doesn't support restoring it from a disk
  snapshot.** Those two apps need Elastic's own snapshot tool writing to GCS, on a schedule.
  Upgrading to 7.17 is the usual first step.
- **nocode's customer domains point at Google's IPs (`104.155.75.230`, `35.240.61.4`), and the
  customers control that DNS.**
  - Caddy's on-demand TLS will serve them.
  - Moving them needs either customers to change DNS, or those Google IPs kept for a while as a
    forwarding hop.
- **Images live in `gcr.io`**, which is now served by Artifact Registry. The box pulls them with a
  read-only service-account key.
- **Apps that read GCS need a key file** instead of GKE's automatic credentials. Heavy readers pay
  Google's download fee; check `nocode-websites` and `forum-videos` first.
- **Order:** small apps first, then badcode, and **the booking system last**. Pass the Bob isolation
  gate (1.7) first. The two Elasticsearch apps are not in the order at all — they are NoCode Works
  and Franchise Cloud, which are switched off rather than moved, and nothing we keep uses their
  clusters (`design/2026-09-12-gke-to-box-migration.md` §4.8). The full per-app plan, with measured
  disk usage, is that file.

**Found in passing, worth doing now, independent of any move:**
- 🟡 **The "stuck HTTPS renewals" are stale, not urgent.** Re-checked 2026-09-12: all 14 pending
  cert-manager challenges are for domains that no longer point at this cluster — three
  (`andovernetball.co.uk`, `arimatheafilms.com`, `digitaltribe.me`, `ecepodcasts.com`) do not
  resolve at all; `zps8.co.uk`, `zeteticmind.com` and `strategy.zeteticmind.com` now answer from
  Krystal (`185.194.90.31`) with valid certificates to 2026-10-24; and `nocode.works` itself
  returns HTTP 200 on a valid Let's Encrypt certificate to 2026-10-24. **Nothing user-facing is
  broken**, and most of it disappears when NoCode is switched off. Housekeeping.
- **Google holds 9,147 old disk snapshots (378.6 GB)**, oldest 2017-04-04, that the current
  14-day policy never deletes (confirmed 2026-09-12,
  `gcloud compute snapshots list --project=webkit-servers`). Review and delete the stale ones.

---

## Sources

- **OVH:**
  - [RISE-L](https://www.kimsufi.com/en-gb/rise/rise-l/)
  - [RISE-XL](https://www.kimsufi.com/en-gb/rise/rise-xl/)
  - [Rise range](https://www.kimsufi.com/en/rise/)
  - [OVH software RAID](https://docs.ovhcloud.com/en/guides/bare-metal-cloud/dedicated-servers/raid-soft)
  - [OVH disk replacement](https://docs.ovhcloud.com/en/guides/bare-metal-cloud/dedicated-servers/disk-replacement)
  - [OVH Backup Agent](https://corporate.ovhcloud.com/en/newsroom/news/ovhcloud-backup-agent/)
  - [Backup Agent docs](https://docs.ovhcloud.com/en/guides/storage-and-backup/backup-agent/product-presentation)
- **Postgres and pgBackRest:**
  - [pgBackRest user guide](https://pgbackrest.org/user-guide.html)
  - [pgBackRest configuration reference](https://pgbackrest.org/configuration.html)
  - [PostgreSQL continuous archiving and PITR](https://www.postgresql.org/docs/18/continuous-archiving.html)
  - [PostgreSQL WAL configuration](https://www.postgresql.org/docs/18/runtime-config-wal.html)
  - [PostgreSQL versioning policy](https://www.postgresql.org/support/versioning/)
  - [PostgreSQL wiki: tuning your server](https://wiki.postgresql.org/wiki/Tuning_Your_PostgreSQL_Server)
  - [PGDG apt repository](https://wiki.postgresql.org/wiki/Apt)
  - [pgvector](https://github.com/pgvector/pgvector) · [PostGIS](https://postgis.net/) · [pg_partman](https://github.com/pgpartman/pg_partman) · [pg_cron](https://github.com/citusdata/pg_cron)
  - [ParadeDB pg_search](https://github.com/paradedb/paradedb) (AGPL-3.0)
- **restic and Caddy:**
  - [restic with Google Cloud Storage](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html#google-cloud-storage)
  - [restic forget policies](https://restic.readthedocs.io/en/stable/060_forget.html)
  - [Caddy reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)
  - [Caddy on-demand TLS](https://caddyserver.com/docs/automatic-https#on-demand-tls)
- **Other services:**
  - [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/)
  - [GCP network pricing](https://cloud.google.com/network-tiers/pricing)
  - [Docker on Debian](https://docs.docker.com/engine/install/debian/)
  - [healthchecks.io](https://healthchecks.io/docs/)
