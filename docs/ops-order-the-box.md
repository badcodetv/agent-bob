# Order the box — Kai's checklist

> **Moved.** This document's home is now the private ops repository,
> `git@github.com:binocarlos/ops.git` (cloned at `~/projects/badcode/ops`), as
> `docs/order-the-box.md`. **Edit it there** — that copy is canonical. This copy is left in
> place because other documents link to it, and it will drift.

**Everything you need to do yourself, in order, in one file.** Written 2026-09-12.
Nothing here needs the repository open. Read it on your phone if you like.

When you have finished the last section, send me the two things it asks for and I run the rest
(about two working days of setup, unattended).

The full guide, with the reasoning and every command for the parts I do, is **`docs/ops.md`**.
You do not need it today.

---

## What you are buying, and why

One **OVH RISE-L** dedicated server, in France, **£128.99/month + £128.99 once**.

- 16 real cores / 32 threads, 128 GB RAM, 2 × 960 GB NVMe mirrored (~890 GB usable).
- Replaces a rented Google machine with 2 shared vCPUs and 16 GB that is at 68% memory and hosts
  eleven apps. Agent Bob needs far more, because it runs up to ~100 AI sessions in their own
  containers.
- **Everything BadCode has ever stored is 23.8 GB** (measured 2026-09-12), so disk is not a
  constraint. Don't pay for a bigger one.
- **Every database on the Google cluster is currently on a network-attached spinning disk**
  (`pd-standard`, confirmed). The box's local NVMe is one to two orders of magnitude faster for the
  small random reads a database does. This is the biggest single reason everything will feel quick.

---

## 1. Four accounts, about 15 minutes

### 1.1 Password manager entry

Make an entry called **"OVH box"**. Everything below goes in it. Nothing secret ever goes in the
repository or in a chat message.

### 1.2 Your SSH key, uploaded to OVH

On your laptop:

```sh
cat ~/.ssh/id_ed25519.pub
```

(If that file doesn't exist: `ssh-keygen -t ed25519` and press enter three times, then run it
again.)

Copy the whole line. In the OVH control panel: **top-right account menu → Products and services →
My services → SSH keys** (or **Account → SSH keys**, depending on which panel version you land in)
→ **Add an SSH key** → paste it, give it a name like `kai-laptop`.

**Why now:** the server is built with your key already trusted. Without it you get a root password
by email, which is worse in every way.

### 1.3 healthchecks.io — free, and it is how you find out something broke

Sign up at **healthchecks.io**. Create **six checks**. For each one, set the period and grace
exactly, then copy its **ping URL** into the password manager.

| Check name | Period | Grace | What it watches |
| --- | --- | --- | --- |
| `pg-incr` | 1 hour | 30 min | the hourly database backup |
| `pg-diff` | 1 day | 2 hours | the daily backup |
| `pg-full` | 1 week | 1 day | the weekly full backup |
| `pg-check` | 6 hours | 1 hour | is the live change-log actually reaching Google right now |
| `pg-drill` | 1 week | 1 day | a real restore, actually performed |
| `box-files` | 1 day | 2 hours | the nightly copy of certificates and config files |

**Why six:** each job reports in when it succeeds. If one stops reporting, you get an email. If the
whole box dies, they all stop reporting, so you hear about that too.

### 1.4 Tailscale — free, and it is how you get in

Sign up at **tailscale.com** (the free plan covers two people) and install it on your laptop.

**Why:** SSH will be closed to the internet entirely. Only ports 80 and 443 stay open. Admin access
comes over Tailscale, a private network between your laptop and the box.

✅ **Section 1 is done when:** the SSH key is uploaded, six ping URLs are in the password manager,
and Tailscale is running on your laptop.

---

## 2. Order the server, about 10 minutes

Go to **kimsufi.com → Rise → RISE-L**. (OVH sells this range under the Kimsufi / Eco brand;
`eco.ovhcloud.com/en/rise/` is the same thing.)

| Choice | Pick | Why |
| --- | --- | --- |
| **Model** | **RISE-L** | 32 threads meets the need inside budget. RISE-XL is twice the price |
| **Datacentre** | **Gravelines** | OVH's largest site, so best stock and least chance of a wait. ~10–15 ms from the UK. Not Strasbourg, which is where OVH's 2021 fire was |
| **Storage** | **the standard 2 × 960 GB NVMe, mirrored** | Don't take a bigger-disk upsell. Real data today is 23.8 GB |
| **Billing** | **monthly** | Keeps the exit cheap if the box turns out underpowered |
| **OS** | **leave it / Debian 12** if asked | Section 3 reinstalls it properly anyway |
| **SSH key** | **select the key from 1.2** | |
| **Backup storage / options** | **skip them all** | Rise advertises 500 GB free backup space, but OVH's own docs say it may be limited on this range, and it has no scheduler, no immutability and no image restore. We are not relying on it |

**Cost check before you confirm:** £128.99/month plus a one-off £128.99 setup. Anything much higher
means an option got added — go back and remove it.

✅ **Section 2 is done when:** OVH emails you that the server is delivered, with its **IP address**.
**Put the IP in the password manager.** Delivery is usually about two minutes.

---

## 3. Reinstall Debian with the right disk layout, about 30 minutes

This is the one part where a wrong click costs a reinstall, so it has its own section.

In the OVH control panel, open the server → **Reinstall** (or **Install**) → **Debian 12** →
choose **custom partitioning**.

Set up **exactly four partitions**, all **RAID 1** except swap:

| Mount point | Filesystem | RAID | Size |
| --- | --- | --- | --- |
| `/boot` | ext4 | RAID 1 | **1 GB** |
| `/` | ext4 | RAID 1 | **60 GB** |
| swap | swap | — | **8 GB** |
| `/srv` | ext4 | RAID 1 | **all remaining space** |

Then select **your SSH key** from 1.2 and start the install.

**What each one is for:** `/boot` and `/` are the operating system, rebuildable from scratch.
`/srv` is where everything that matters lives — the one Postgres, every app's config, and Docker's
image store. Making it one big plain ext4 partition is deliberate; an earlier version of this plan
used LVM and a thin pool, and that turned out to exist only to work around a problem we no longer
have.

**RAID 1 means the two disks mirror each other.** A worn-out disk is the most common hardware
failure; with mirroring, nothing stops and OVH swaps it while everything keeps running.

✅ **Section 3 is done when:** `ssh debian@<IP>` logs you in from your laptop.
(On OVH's Debian images the default user is `debian`.)

---

## 4. Send me two things

1. **The server's public IP address.**
2. Once you have SSH'd in once, that's all I need — I'll bring up Tailscale myself and give you the
   link to approve, then you confirm `ssh debian@<tailscale-ip>` works before I close SSH to the
   internet.

I'll also need, when we reach them (not now):

- **`GOOGLE_CLIENT_ID`** from your existing `.env`, so Google login works, and you'll need to add
  `https://bob.badcode.tv` as an authorised JavaScript origin on that OAuth client.
- **`ANTHROPIC_API_KEY`** — the metered key, **not** the Claude subscription token. This is settled,
  not a preference: if both are set the subscription token wins and the box bills to your
  subscription, where Bob's daily token budget cannot brake it at all. Thread 02 established this
  while building the budget; `docs/ops.md` step 11d has the reasoning. Leave
  `CLAUDE_CODE_OAUTH_TOKEN` blank.
- **A DNS A record: `bob` → the box's IP**, at your DNS provider for `badcode.tv`. Last step, after
  the app answers on the box.

**Already decided, nothing needed from you:** the default daily token budget for a brand-new
project is **50,000 soft / 100,000 hard** (your call, 2026-09-12). It only applies to a project
that has never had settings written — an invited friend's first project. It goes in the same
`.env`, and there is a separate check (`docs/ops.md` step 11f) that proves the brake actually
*fires* rather than merely being configured, to run before the first real invite.

---

## 5. Decisions I still need — but NOT today

None of these block ordering or the first three sections. Answer whenever; I need them by the time
I build the database, which is a day or two in.

| # | Question | My recommendation |
| --- | --- | --- |
| 1 | Time series: `pg_partman` + `pg_cron`, or TimescaleDB from the vendor? | **pg_partman + pg_cron.** Debian's TimescaleDB silently omits the features people want it for; the real one needs a third-party repo that then dictates our Postgres version. No time-series workload exists today. Adding it later is a package install |
| 2 | Full-text search: ParadeDB's `pg_search`, or Postgres's built-in? | **`pg_search`.** Better ranking than Postgres's built-in search. It's AGPL — self-hosting is explicitly permitted, but worth ten minutes of legal before booking-system data depends on it. **Not urgent:** nothing is waiting on it now that the booking system turns out to use plain Postgres search (§6) |
| 3 | Should Agent Bob use the box's Postgres 18, or keep its own bundled Postgres 16? | **The box's.** I tested it: Bob's full database suite passes on 18, all 49 migrations apply, the vector and label indexes all build. A fresh Bob has no data, so install time is the only moment this move is free |
| 4 | Switch on OVH's Backup Agent as a cheap second copy? | **Yes, once the box exists** — £0 agent + £0.0061/GB/month, so about **£0.20/month**. Nightly whole-server image in a different OVH datacentre, 14 days of it immutable, and restores are free. Check it actually appears in the control panel first; OVH's docs don't confirm this range is eligible |
| 5 | The old shared Postgres 9.6: dump-and-restore, or `pg_upgrade`? | **Dump and restore.** It's 22.7 GB, so 20–40 minutes, and a failure leaves the old one untouched |

---

## 6. One thing you can check in a minute

✅ **The booking-system search worry is closed — you don't need to test it.** Its configuration
pointed at an **Elasticsearch host** (a separate search server), and the only two Elasticsearch
servers belong to NoCode Works and Franchise Cloud, which you switch off **the week of Monday
2026-09-14** — while the booking system is the last thing to move. That looked like a trap.

It isn't. Reading the booking system's own code on 2026-09-12 shows its search is **already
Postgres**: `api/src/store/booking.js:145-158` searches a Postgres text column, and nothing
anywhere in the code loads the Elasticsearch client or reads that setting. The setting, the npm
package and a component called `ElasticSearchBox` are all leftovers from a feature that was
replaced. **Switch NoCode and Franchise Cloud off on schedule; nothing breaks.** The dead settings
get deleted when the booking system moves.

🟡 **Open `n8n.badcode.tv`** and look at the workflow list and executions log. n8n is workflow
automation left over from the old `storyteller` stack — your own repo calls that stack *"exactly
what we are shedding"*, and nothing in the current code references it. If the list is empty or
stale, it goes, and badcode's Redis goes with it.

---

## 7. What happens after you hand me the IP

For information — none of it needs you until the last line.

1. Debian updates, packages, firewall, Tailscale, Docker. *(You approve one Tailscale link.)*
2. `/srv` laid out; Docker moved onto it.
3. A Google Cloud Storage bucket and key for backups.
4. **One PostgreSQL 18**, with vector search, geospatial, full-text and partitioning, tuned for
   128 GB and 32 threads — and **pgBackRest** shipping every change to Google continuously.
   Worst-case data loss: **about 60 seconds**, and any moment can be restored to.
5. Caddy for HTTPS, then Agent Bob.
6. **You** add the DNS record and test the chat at `https://bob.badcode.tv`.
7. A real timed restore, proving a backup comes back **to a chosen moment** — then that drill runs
   itself weekly.
8. Two checks that can only be done on a real box, before anyone is invited: that the daily spend
   brake actually **fires** rather than merely being configured, and that **adding a friend to the
   allowlist works without restarting Bob** (`docs/ops.md` steps 11f and 11g). The second one is a
   ticket thread 02 could not close offline.

---

## The three things you must never lose

Once the box is running, these go in the password manager and **cannot be regenerated**:

1. **The pgBackRest repository passphrase** — without it every database backup is permanently unreadable.
2. **The restic password** — same, for the config-file backups.
3. **The Google service-account key** for the backup bucket.

I will print each one once, at the moment it is created, and tell you to save it. Save it then.
