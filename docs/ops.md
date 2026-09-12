# Ops — the OVH box

How BadCode runs its apps on one OVH server: **what we decided and why** (Part 1), **how to set it
up, step by step** (Part 2), **how to run it day to day** (Part 3), and **what we already know
about moving the GKE apps later** (Part 4).

Written 2026-09-11. The long-form reasoning, with sources, is in
`design/2026-09-11-ovh-compose-hosting.md`. **This file is the one to follow.** Where the two
disagree, this file is newer.

> **Status: nothing is built yet.** Every command below is still to be run.
>
> ⚠️ **Parts 1.5 and Step 4 and Step 9 are under review (2026-09-12).** Kai has proposed replacing
> the LVM-thin-snapshot backup design with **Postgres-native backups** (pgBackRest, WAL archiving to
> GCS, point-in-time recovery), on the grounds that the only local state worth protecting is a
> Postgres data directory. The evaluation agrees and recommends it: see
> **`design/2026-09-12-postgres-native-backups.md`**, whose §6 lists exactly what it deletes and
> whose §7 holds the five decisions it needs. **Do not run Step 4 or Step 9 until that is settled** —
> Steps 0–3 and 5–8 are unaffected.
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
| Where backups go | **Google Cloud Storage**, every hour. No second server |
| How much data we can lose | **At most 1 hour**, the same as GKE today |
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

**Every app keeps all of its data in its own "virtual disk". Once an hour, the server takes an
instant photo of each virtual disk and copies the photo to Google. Only the parts that changed
are uploaded, and they are encrypted first.** It doesn't matter what's inside, whether Postgres,
Redis or plain files: the whole disk is photographed. The tool that does this is LVM (Logical
Volume Manager), standard Linux for 20 years. No ZFS, no Btrfs.

### The layers

```
 2 × NVMe disks
      │  mirrored (RAID1)
      ▼
 one ~890 GB area handed to LVM
      ▼
 "thin pool": space handed out in small chunks, on demand
      ▼
 ┌──────────┬───────────┬──────────┬────────────┐
 │ bob      │ caddy     │ ops      │ docker     │   one virtual disk ("LV") per app
 │ backed up│ backed up │ backed up│ NOT backed │   each mounted at /srv/apps/<app>
 └──────────┴───────────┴──────────┴────────────┘
```

`docker` holds images and caches that can be rebuilt, so it is left out of the backups on
purpose.

### Why the photo is instant: copy-on-write

The thin pool keeps the data in ~64 KB chunks, plus a **map** for each virtual disk ("Bob's
block 1234 is in chunk 88"). **A snapshot copies the map, not the data**, so it takes
milliseconds even for a 500 GB disk. After that, a new write goes into a **fresh** chunk and only
the live disk's map is updated. The snapshot still points at the old chunk.

```
after snapshot:   bob ──────────► chunk 88      (shared, so it is never overwritten)
                  bob-snapshot ─► chunk 88

Postgres writes:  bob ──────────► chunk 501     (new data goes into a new chunk)
                  bob-snapshot ─► chunk 88      (the old moment, untouched)
```

At the instant of the snapshot, LVM pauses writes for a few milliseconds, so nothing is
half-written. The result is exactly like **pulling the power cord** at that moment. Databases
are built to recover from that, and it is the same guarantee Google's disk snapshots give us
today.

### The hourly job

1. **Snapshot** each app's disk (instant).
2. **Mount** the snapshot read-only. It is a frozen picture of, say, 14:05.
3. **restic** (the backup tool) uploads the changes since last hour to Google, encrypted. The app
   keeps running the whole time.
4. **Delete** the snapshot.

The history (14 days of hours, then 90 days of days, then a year of months) lives in Google,
not on the server.

### What comes back, and what doesn't

| Thing | After a restore |
| --- | --- |
| Postgres (any version, including pgvector and ParadeDB), Redis, SQLite, plain files | **Yes.** Recovers like after a power cut. Condition: the database's files all live on the same virtual disk, which "one disk per app" guarantees |
| Caddy's certificates | Yes (the `caddy` disk) |
| Each app's `.env` secrets | Yes (they live on the app's disk; restic encrypts them) |
| Bob's **archived** sessions | Yes (Bob saves idle sessions to GCS after 30 minutes; the Postgres row pointing at each one is backed up) |
| Bob's sessions **running at that moment** | Up to 30 minutes of in-container work is lost; the conversation history survives |
| Elasticsearch (not used by Bob; franchisecloud and nocode later) | **Not supported by Elastic** from a disk snapshot. Those apps need Elastic's own snapshot tool (Part 4) |

### How much it costs, and how long a restore takes

- **Storage in Google:** a few dollars a month for Bob. Only changes are stored, and they're
  deduplicated.
- **Uploading to Google:** free.
- **Downloading (restoring) from Google:** about **$0.12 per GB**, so 100 GB costs ~$12.
- **Restore speed:** about **100 GB in 20–35 minutes** over the 1 Gbps link. Step 13 measures the
  real number.

### How sure are we?

- **LVM thin snapshots are mainstream.** They've been in Linux since about 2012. Red Hat supports
  them, Proxmox (a popular hypervisor) uses them by default for virtual machine disks, and
  "snapshot, back up the frozen copy, delete the snapshot" is a textbook sysadmin pattern.
- **restic is widely used**, and it backs up to Google directly.
- **The unproven part is our own ~25-line script.** That's why step 13 does a real, timed restore
  before anything important depends on it, and why a restore test runs automatically every week.

### The one danger: the thin pool filling up

Space is handed out on demand, so the pool can run out. At 100%, **every** app's disk can go
read-only at once. The guards:
- an alert at 80% full, checked every 5 minutes;
- the pool grows itself while spare space remains;
- the backup script always deletes its snapshots, even after a crash.

### An optional extra copy: OVH's Backup Agent

Since January 2026, OVH offers a free Veeam-based agent. It takes a **daily** backup of the whole
server into OVH storage in a different datacentre. You pay only for that storage (about $0.008 per
GB per month on OVH's US page). It can't meet our 1-hour target and it isn't on Google, so it's
**an optional second copy, not the main backup.** Consider switching it on once customer data
lives on the box.

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
- **OVH's installer:** that it lets us leave a partition for LVM (step 2).
- **The "LVM freezes the filesystem" detail:** believed true, but not yet confirmed from a primary
  source. Step 13's restore test is the proof.
- **OVH Backup Agent:** its price and availability in Europe.
- **Google's download price:** check it on the GCP pricing page.
- **Real restore speed:** measured in step 13.

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

   | Check name | Period | Grace |
   | --- | --- | --- |
   | box-backup | 1 hour | 30 minutes |
   | box-prune | 1 day | 2 hours |
   | box-check | 1 week | 1 day |
   | box-drill | 1 week | 1 day |
   | box-pool | 5 minutes | 10 minutes |

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

`/srv` is a placeholder: step 4 wipes it and hands it to LVM. If the installer offers an **LVM**
option directly, you can use that instead, but the placeholder route always works. Select your
SSH key.

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

## Step 4 — Build the LVM thin pool (20 min)

```bash
sudo -i
findmnt /srv                 # note the device, e.g. /dev/md3 — used below as $DEV
DEV=/dev/md3                 # ← change if findmnt said something else
umount /srv
sed -i '\#[[:space:]]/srv[[:space:]]#d' /etc/fstab   # remove the placeholder's line
wipefs -a "$DEV"

pvcreate "$DEV"
vgcreate vg0 "$DEV"
# Use 90% for the pool; the last 10% is room for the pool to grow itself.
lvcreate --type thin-pool -l 90%FREE --poolmetadatasize 2G -n pool vg0
```

Let the pool grow itself when it's 70% full. Edit `/etc/lvm/lvm.conf`: find these two lines in
the `activation { … }` section, uncomment them, and set:

```
thin_pool_autoextend_threshold = 70
thin_pool_autoextend_percent = 20
```

Next, a helper that creates one backed-up disk per app. You'll use it for every new app:

```bash
cat > /usr/local/sbin/new-app-volume <<'EOF'
#!/usr/bin/env bash
# new-app-volume <app> <size>   e.g.  new-app-volume bob 100G
# Creates a thin virtual disk for one app, tags it for hourly backup, mounts it at /srv/apps/<app>.
set -euo pipefail
app=$1; size=$2
lvcreate -qq -V "$size" -T vg0/pool -n "$app" --addtag backup
mkfs.ext4 -q -L "$app" "/dev/vg0/$app"
mkdir -p "/srv/apps/$app"
echo "/dev/vg0/$app /srv/apps/$app ext4 defaults,noatime 0 2" >> /etc/fstab
mount "/srv/apps/$app"
echo "ready: /srv/apps/$app (backed up hourly)"
EOF
chmod +x /usr/local/sbin/new-app-volume

new-app-volume bob 100G
new-app-volume caddy 2G
new-app-volume ops 10G

# Docker's own storage: a disk with NO backup tag (images and caches are rebuildable).
lvcreate -qq -V 300G -T vg0/pool -n docker
mkfs.ext4 -q -L docker /dev/vg0/docker
mkdir -p /var/lib/docker
echo "/dev/vg0/docker /var/lib/docker ext4 defaults,noatime 0 2" >> /etc/fstab
mount /var/lib/docker
```

✅ **Done when:**
- `lvs vg0` lists `pool`, `bob`, `caddy`, `ops` and `docker`;
- `lvs -o lv_name,lv_tags vg0` shows `backup` on bob, caddy and ops only;
- `df -h /srv/apps/bob` shows it mounted.

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

✅ **Done when:** `hello-world` prints its message, and `df -h /var/lib/docker` shows the
`docker` LV, not the root disk.

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

## Step 9 — Backups: restic, the scripts and the timers (1–2 h)

### 9a. Install restic and set up its settings

```bash
# The newest restic from its releases page (Debian's package is older and can't self-update)
curl -fsSL https://github.com/restic/restic/releases/download/v0.18.0/restic_0.18.0_linux_amd64.bz2 \
  | bunzip2 > /usr/local/bin/restic && chmod +x /usr/local/bin/restic
restic self-update               # moves to the latest release

install -d -m 700 /etc/box-backup
mv /tmp/ovh-backup-key.json /etc/box-backup/ && chmod 600 /etc/box-backup/ovh-backup-key.json
openssl rand -base64 32 > /etc/box-backup/restic-password && chmod 600 /etc/box-backup/restic-password
cat /etc/box-backup/restic-password   # ← PUT THIS IN THE PASSWORD MANAGER NOW. Lose it = lose every backup.

cat > /etc/box-backup.env <<'EOF'
RESTIC_REPOSITORY=gs:webkit-servers-ovh-backups:/restic
RESTIC_PASSWORD_FILE=/etc/box-backup/restic-password
GOOGLE_PROJECT_ID=webkit-servers
GOOGLE_APPLICATION_CREDENTIALS=/etc/box-backup/ovh-backup-key.json
HC_BACKUP=https://hc-ping.com/REPLACE-ME
HC_PRUNE=https://hc-ping.com/REPLACE-ME
HC_CHECK=https://hc-ping.com/REPLACE-ME
HC_DRILL=https://hc-ping.com/REPLACE-ME
HC_POOL=https://hc-ping.com/REPLACE-ME
EOF
chmod 600 /etc/box-backup.env
nano /etc/box-backup.env         # paste the five ping URLs from step 0

set -a; . /etc/box-backup.env; set +a
restic init
```

### 9b. The five scripts

```bash
cat > /usr/local/sbin/box-backup <<'EOF'
#!/usr/bin/env bash
# HOURLY. For every LV tagged "backup": thin snapshot → mount read-only → restic → GCS → remove snapshot.
set -euo pipefail
set -a; . /etc/box-backup.env; set +a
exec 9>/run/box-backup.lock
flock -w 3000 9 || { curl -fsS -m 10 "$HC_BACKUP/fail" >/dev/null || true; exit 1; }

snapshots() { lvs --noheadings -o lv_name vg0 | awk '/-snap-/ {print $1}'; }
cleanup() {
  for m in /mnt/snap/*/; do
    if mountpoint -q "$m"; then umount "$m"; fi
  done
  for s in $(snapshots); do lvremove -qy "vg0/$s"; done
}
trap cleanup EXIT
trap 'curl -fsS -m 10 "$HC_BACKUP/fail" >/dev/null || true' ERR

curl -fsS -m 10 "$HC_BACKUP/start" >/dev/null || true
cleanup                                # a crashed earlier run must never leave a snapshot behind
stamp=$(date -u +%Y%m%dT%H%M)
for lv in $(lvs --noheadings -o lv_name @backup | awk '!/-snap-/ {print $1}'); do
  snap="${lv}-snap-${stamp}"
  lvcreate -qq --snapshot --setactivationskip n --name "$snap" "vg0/$lv"   # instant; LVM freezes the fs for the moment
  mkdir -p "/mnt/snap/$lv"
  mount -o ro,noload "/dev/vg0/$snap" "/mnt/snap/$lv"
  restic backup --quiet --host box1 --tag "app=$lv" "/mnt/snap/$lv"
done
curl -fsS -m 10 "$HC_BACKUP" >/dev/null
EOF

cat > /usr/local/sbin/box-prune <<'EOF'
#!/usr/bin/env bash
# DAILY. Keep 14 days of hourly, 90 days of daily, 1 year of monthly; delete the rest.
set -euo pipefail
set -a; . /etc/box-backup.env; set +a
exec 9>/run/box-backup.lock; flock -w 3000 9
trap 'curl -fsS -m 10 "$HC_PRUNE/fail" >/dev/null || true' ERR
restic forget --quiet --keep-within-hourly 14d --keep-within-daily 90d --keep-within-monthly 1y --prune
curl -fsS -m 10 "$HC_PRUNE" >/dev/null
EOF

cat > /usr/local/sbin/box-check <<'EOF'
#!/usr/bin/env bash
# WEEKLY. Re-reads 5% of the backup data from Google to catch damage early.
set -euo pipefail
set -a; . /etc/box-backup.env; set +a
exec 9>/run/box-backup.lock; flock -w 3000 9
trap 'curl -fsS -m 10 "$HC_CHECK/fail" >/dev/null || true' ERR
restic check --read-data-subset=5%
curl -fsS -m 10 "$HC_CHECK" >/dev/null
EOF

cat > /usr/local/sbin/box-pool-check <<'EOF'
#!/usr/bin/env bash
# EVERY 5 MIN. Alerts when the thin pool is 80% full (data or metadata). Full = every app read-only.
set -euo pipefail
set -a; . /etc/box-backup.env; set +a
read -r data meta < <(lvs --noheadings -o data_percent,metadata_percent vg0/pool)
if awk -v d="$data" -v m="$meta" 'BEGIN { exit !(d < 80 && m < 80) }'; then
  curl -fsS -m 10 "$HC_POOL" >/dev/null
else
  curl -fsS -m 10 --data-raw "thin pool data ${data}% metadata ${meta}%" "$HC_POOL/fail" >/dev/null
fi
EOF

cat > /usr/local/sbin/box-drill <<'EOF'
#!/usr/bin/env bash
# WEEKLY. Proves Bob's newest backup really comes back: restore it into a scratch disk,
# start Postgres on it, count the sessions, report the time taken, clean up.
set -euo pipefail
set -a; . /etc/box-backup.env; set +a
exec 9>/run/box-backup.lock; flock -w 3000 9
trap 'curl -fsS -m 10 "$HC_DRILL/fail" >/dev/null || true' ERR
cleanup() {
  docker rm -f drill-pg >/dev/null 2>&1 || true
  if mountpoint -q /mnt/drill; then umount /mnt/drill; fi
  if lvs vg0/drill >/dev/null 2>&1; then lvremove -qy vg0/drill; fi
}
trap cleanup EXIT
cleanup
lvcreate -qq -V 150G -T vg0/pool -n drill
mkfs.ext4 -q /dev/vg0/drill
mkdir -p /mnt/drill && mount /dev/vg0/drill /mnt/drill
start=$(date +%s)
restic restore --quiet latest --tag app=bob --target /mnt/drill
secs=$(( $(date +%s) - start ))
docker run -d --name drill-pg -v /mnt/drill/mnt/snap/bob/pg-data:/var/lib/postgresql/data \
  pgvector/pgvector:pg16 >/dev/null
for _ in $(seq 60); do docker exec drill-pg pg_isready -q -U agentbob && break; sleep 2; done
rows=$(docker exec drill-pg psql -U agentbob -d agentbob -Atc 'select count(*) from agent_sessions')
curl -fsS -m 10 --data-raw "restored bob in ${secs}s; agent_sessions=${rows}" "$HC_DRILL" >/dev/null
EOF

chmod +x /usr/local/sbin/box-*
```

### 9c. The timers (so the scripts run by themselves)

```bash
timer() {   # timer <name> <when, systemd OnCalendar format>
  cat > "/etc/systemd/system/$1.service" <<EOF
[Unit]
Description=$1
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/$1
EOF
  cat > "/etc/systemd/system/$1.timer" <<EOF
[Unit]
Description=$1 schedule
[Timer]
OnCalendar=$2
Persistent=true
[Install]
WantedBy=timers.target
EOF
}
timer box-backup     '*-*-* *:05:00'       # every hour at :05
timer box-prune      '*-*-* 03:30:00'      # daily
timer box-check      'Sun *-*-* 04:30:00'  # weekly
timer box-drill      'Sun *-*-* 05:30:00'  # weekly (not until Bob exists — step 13)
timer box-pool-check '*:0/5'               # every 5 minutes
systemctl daemon-reload
systemctl enable --now box-backup.timer box-prune.timer box-check.timer box-pool-check.timer
systemctl list-timers 'box-*'
```

### 9d. Test it by hand

```bash
echo "hello from $(date)" > /srv/apps/ops/test.txt
/usr/local/sbin/box-backup
restic snapshots                          # three snapshots: app=bob, app=caddy, app=ops
rm /srv/apps/ops/test.txt
restic restore latest --tag app=ops --target /tmp/r --include /mnt/snap/ops/test.txt
cat /tmp/r/mnt/snap/ops/test.txt          # the file is back
lvs vg0                                    # no *-snap-* left over
```

✅ **Done when:**
- the file comes back;
- no `-snap-` volumes are left;
- healthchecks.io shows **box-backup** and **box-pool** green.

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
mkdir -p pg-data agentd-data secrets
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

```bash
cat > /srv/apps/bob/compose.ovh.yml <<'EOF'
# Layered on top of src/docker-compose.yml. Puts Bob's data on the backed-up disk,
# caps Docker-in-Docker so it can't starve other apps, and mounts the Google key.
services:
  dind:
    cpus: 24
    mem_limit: 96g
  agentd:
    volumes:
      - /srv/apps/bob/secrets/gcp-key.json:/gcp/key.json:ro
      - /srv/apps/bob/secrets/projects.json:/secrets/projects.json:ro
volumes:
  pg-data:
    driver: local
    driver_opts: { type: none, o: bind, device: /srv/apps/bob/pg-data }
  agentd-data:
    driver: local
    driver_opts: { type: none, o: bind, device: /srv/apps/bob/agentd-data }
  # dind-data stays a normal Docker volume on the un-backed-up docker disk (rebuildable cache).
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

# ── Model: one of these ──
CLAUDE_CODE_OAUTH_TOKEN=
ANTHROPIC_API_KEY=

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
nano /srv/apps/bob/src/.env     # fill GOOGLE_CLIENT_ID and ONE model credential
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

**Three rules for this file:**
- **Keep a login mode set.** With no `GOOGLE_CLIENT_ID` (and no test login), Bob runs with **no
  authentication at all**.
- **Add Bob's address to Google login.** In the Google Cloud console, open the OAuth client
  behind `GOOGLE_CLIENT_ID` and add **`https://bob.badcode.tv`** as an authorised JavaScript
  origin.
- **Only the `web` service is published, and only on 127.0.0.1.** Bob's API service (`agentd`)
  has an unauthenticated route that spends the Anthropic key (`docs/19-embedding.md`, hazard
  H6). It is safe only while `agentd` is unreachable from outside, which this setup guarantees.
  Never publish another port.

**Adding someone later** is editing `/srv/apps/bob/secrets/projects.json` and either waiting for
the next reload (`AGENTKIT_PROJECT_MAP_RELOAD`, default 60s) or sending SIGHUP for an immediate
one: `docker compose --project-directory /srv/apps/bob/src -f /srv/apps/bob/src/docker-compose.yml
-f /srv/apps/bob/compose.ovh.yml kill -s SIGHUP agentd`. No restart, no downtime.

### 11e. Start it

```bash
/srv/apps/bob/up.sh
docker compose --project-directory /srv/apps/bob/src -f /srv/apps/bob/src/docker-compose.yml \
  -f /srv/apps/bob/compose.ovh.yml ps
curl -sI http://127.0.0.1:8100 | head -1      # HTTP/1.1 200 OK
ls /srv/apps/bob/pg-data | head -3            # Postgres files are on the backed-up disk
```

The first start takes a few minutes: it builds the images and the sandbox.

✅ **Done when:**
- `curl` returns `200`;
- `/srv/apps/bob/pg-data` holds Postgres files;
- `ss -tlnp` shows nothing but `127.0.0.1` for ports 8100 and up.

## Step 12 — DNS and your manual test (15 min)

1. At your DNS provider for `badcode.tv`, add an **A record: `bob` → the box's public IP**.
2. Wait a minute. `dig +short bob.badcode.tv` should print the box's IP.
3. Open **https://bob.badcode.tv**. Caddy fetches the certificate on the first visit.
4. Log in with Google and start a session. Send a few messages.

✅ **Done when:** the chat works over HTTPS and replies stream in word by word, not all at once.
**This is Kai's manual test of Bob.**

## Step 13 — The first real restore test (30 min)

```bash
/usr/local/sbin/box-backup          # back up Bob now that it has real data
/usr/local/sbin/box-drill           # restore it into a scratch disk and start Postgres on it
systemctl enable --now box-drill.timer
```

Check healthchecks.io: **box-drill** shows a message like
`restored bob in 94s; agent_sessions=3`. Write the real time here:

> **Measured restore time:** _____ seconds for _____ GB (date: ______)

✅ **Done when:** box-drill is green, and the number above is filled in.
**The box is now live, and its backups are proven.**

---

# Part 3 — Everyday operations

## Add a new app (15 minutes)

1. **Pick a port** from the table in 1.6 and add the app to the table.
2. **Make its disk:** `new-app-volume <app> 50G`. It's backed up hourly from now on, with nothing
   else to set up.
3. **Put the app at `/srv/apps/<app>/`**, with a compose file following three rules:
   - data only in **bind mounts** under `/srv/apps/<app>/…`, **never** named Docker volumes
     (those land on the unbacked disk);
   - ports only as `"127.0.0.1:<port>:<container-port>"`;
   - `restart: unless-stopped`, plus `cpus:` and `mem_limit:` for anything heavy.
4. **Secrets** go in `/srv/apps/<app>/.env` (mode 600), and in the password manager.
5. **Caddy:** add a site block (`app.example.com { reverse_proxy 127.0.0.1:<port> }`) and reload.
6. **DNS:** point the hostname's A record at the box.

**Before the first app with customer data:** Bob's Docker-in-Docker must stop being
`privileged` (1.7).

## Update Bob

```bash
cd /srv/apps/bob/src && git pull && /srv/apps/bob/up.sh
```

## Undo a mistake in one app (roll back to an earlier hour)

```bash
set -a; . /etc/box-backup.env; set +a
restic snapshots --tag app=bob                      # pick a snapshot ID from the time you want
cd /srv/apps/bob && docker compose --project-directory src -f src/docker-compose.yml -f compose.ovh.yml down
mkdir -p /srv/restore && restic restore <ID> --target /srv/restore
# Look at /srv/restore/mnt/snap/bob, then swap the folders you need, for example:
mv /srv/apps/bob/pg-data /srv/apps/bob/pg-data.broken
cp -a /srv/restore/mnt/snap/bob/pg-data /srv/apps/bob/pg-data
/srv/apps/bob/up.sh
```

Delete `pg-data.broken` and `/srv/restore` once you're happy.

## The whole box died

Target: **under 2 hours for Bob.**

1. Order a new RISE-L (step 1).
2. Do steps 2–7, then the restic install and settings in 9a, with **`restic init` skipped**. The
   repository already exists; paste the restic password and the backup key from the password
   manager. Recreate the `bob`, `caddy` and `ops` disks with `new-app-volume`.
3. Restore each app's disk:
   ```bash
   restic restore latest --tag app=bob --target /tmp/r && cp -a /tmp/r/mnt/snap/bob/. /srv/apps/bob/
   restic restore latest --tag app=caddy --target /tmp/r && cp -a /tmp/r/mnt/snap/caddy/. /srv/apps/caddy/
   ```
4. Do step 9b–c (scripts and timers). Start Caddy (`docker compose up -d` in `/srv/apps/caddy`),
   then run `/srv/apps/bob/up.sh`.
5. Change the DNS A record to the new IP.

**Before the booking system moves in**, rehearse this once on a second RISE-L rented for a day.

## Where everything is

| What | Where |
| --- | --- |
| Each app's everything (data, compose files, `.env`) | `/srv/apps/<app>/`, one virtual disk each, backed up hourly |
| Docker images and caches | `/var/lib/docker`, **not** backed up (rebuildable) |
| Backup settings and keys | `/etc/box-backup.env`, `/etc/box-backup/` |
| Backup scripts | `/usr/local/sbin/box-*`, `new-app-volume` |
| Schedules | `systemctl list-timers 'box-*'` |
| Backups | `gs://webkit-servers-ovh-backups/restic` (encrypted) |
| Secrets, off the box | the "OVH box" password-manager entry |

## Health at a glance

```bash
systemctl list-timers 'box-*'                 # when each job last ran and runs next
lvs -o lv_name,data_percent,metadata_percent vg0/pool   # stay under 80%
cat /proc/mdstat                              # both disks [UU]
set -a; . /etc/box-backup.env; set +a; restic snapshots --latest 1
```

**Built since:** `deploy/ovh/bootstrap.sh` + `deploy/ovh/new-app-volume` in this repo cover
Steps 3–7. **Still not built:** the `box-*` backup scripts as repo files (Step 9 is still
copy-paste), Uptime Kuma, and OVH's Backup Agent as a second copy.

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
- **Order:** small apps first, then the Elasticsearch apps, then badcode, and **the booking system
  last**. Pass the Bob isolation gate (1.7) first.

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
- **LVM, restic and Caddy:**
  - [lvmthin(7)](https://man7.org/linux/man-pages/man7/lvmthin.7.html)
  - [restic with Google Cloud Storage](https://restic.readthedocs.io/en/stable/030_preparing_a_new_repo.html#google-cloud-storage)
  - [restic forget policies](https://restic.readthedocs.io/en/stable/060_forget.html)
  - [Caddy reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)
  - [Caddy on-demand TLS](https://caddyserver.com/docs/automatic-https#on-demand-tls)
- **Other services:**
  - [Let's Encrypt rate limits](https://letsencrypt.org/docs/rate-limits/)
  - [GCP network pricing](https://cloud.google.com/network-tiers/pricing)
  - [Docker on Debian](https://docs.docker.com/engine/install/debian/)
  - [healthchecks.io](https://healthchecks.io/docs/)
