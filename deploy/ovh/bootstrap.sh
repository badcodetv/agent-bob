#!/usr/bin/env bash
#
# bootstrap.sh — build a fresh OVH box up to the point where Caddy and Bob can be started.
# This is the executable form of docs/ops.md Steps 3-7. The doc explains WHY; this runs it.
#
#   scp deploy/ovh/bootstrap.sh debian@<ip>:/tmp/ && ssh debian@<ip>
#   sudo -i
#   /tmp/ovh/bootstrap.sh base       # Step 3  — packages, unattended-upgrades, hostname   (~10 min, reboots)
#   /tmp/ovh/bootstrap.sh disk       # Step 4  — /srv/apps/* + the pgBackRest spool dir     (~1 min)
#   /tmp/ovh/bootstrap.sh tailscale  # Step 5a — install + `tailscale up` (prints a link)  (~3 min)
#   ### now prove `ssh debian@<tailscale-ip>` works from a NEW terminal on your laptop ###
#   /tmp/ovh/bootstrap.sh lockdown --i-can-ssh-over-tailscale   # Step 5b + 6 — SSH keys only, nftables
#   /tmp/ovh/bootstrap.sh docker     # Step 7  — Docker CE onto the un-backed-up LV        (~5 min)
#   /tmp/ovh/bootstrap.sh verify     # re-checks every "Done when" in one go
#
# Steps 8-13 (the GCS bucket, the one Postgres, pgBackRest, Caddy, Bob, DNS, the restore
# drill) are NOT in here yet — follow docs/ops.md for those.
#
# Every phase is idempotent: re-running one is safe.
#
set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
note() { echo; echo "== $*"; }
ok()   { echo "   ok: $*"; }

[[ $EUID -eq 0 ]] || die "run as root (sudo -i)"

# ---------------------------------------------------------------- Step 3: base
phase_base() {
  note "Step 3 — packages and host settings"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get full-upgrade -y
  apt-get install -y lvm2 thin-provisioning-tools curl git jq unattended-upgrades nftables \
                     nmap bzip2 ca-certificates gnupg

  # Security updates only, never an automatic reboot (ops.md 1.7).
  cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
  cat > /etc/apt/apt.conf.d/51-no-auto-reboot <<'EOF'
Unattended-Upgrade::Automatic-Reboot "false";
EOF
  hostnamectl set-hostname box1
  ok "packages installed, unattended-upgrades on, hostname box1"

  note "RAID health (both disks must show [UU])"
  cat /proc/mdstat
  echo
  echo ">>> REBOOT NOW:  reboot     then run:  bootstrap.sh lvm"
}

# ------------------------------------------------------ Step 4: lay out /srv
# Was an LVM thin pool with one virtual disk per app, which existed only so a live
# database could be snapshotted and file-copied. pgBackRest removed that need
# (design/2026-09-12-postgres-native-backups.md §6), so this is now directories.
phase_lvm() { phase_disk "$@"; }        # old name kept so stale notes still work
phase_disk() {
  note "Step 4 — lay out /srv"

  findmnt /srv >/dev/null 2>&1 \
    || die "/srv is not mounted. Expected the OVH installer to give it the remaining space.
       Re-check step 2's partitioning before continuing."

  mkdir -p /srv/apps/caddy /srv/apps/postgres /srv/apps/bob /srv/apps/ops
  mkdir -p /srv/backup/spool          # pgBackRest's async archive-push spool
  mkdir -p /srv/docker                # Docker's data-root; set in daemon.json (phase docker)
  ok "/srv/apps/{caddy,postgres,bob,ops}, /srv/backup/spool, /srv/docker"

  df -h /srv
  echo
  echo "   Databases are backed up by pgBackRest (ops.md step 9), not from this filesystem."
  echo "   /srv/docker is deliberately NOT backed up: images and caches are rebuildable."
}

# -------------------------------------------------------- Step 5a: Tailscale up
phase_tailscale() {
  note "Step 5a — Tailscale"
  command -v tailscale >/dev/null 2>&1 || curl -fsSL https://tailscale.com/install.sh | sh
  tailscale up
  echo
  echo "   box Tailscale IP: $(tailscale ip -4)"
  echo
  echo ">>> STOP. From a NEW terminal on your laptop, prove this works:"
  echo ">>>   ssh debian@$(tailscale ip -4)"
  echo ">>> Only then run:  bootstrap.sh lockdown --i-can-ssh-over-tailscale"
}

# ------------------------------------------- Step 5b + 6: SSH hardening, firewall
phase_lockdown() {
  [[ "${1:-}" == "--i-can-ssh-over-tailscale" ]] || die \
"This phase closes SSH to the internet. Lock yourself out and the only way back is OVH Rescue mode.
 Prove Tailscale SSH works from a NEW terminal first, then re-run:
   bootstrap.sh lockdown --i-can-ssh-over-tailscale"

  tailscale status >/dev/null 2>&1 || die "tailscale is not up — run: bootstrap.sh tailscale"

  note "Step 5b — SSH: keys only, no root"
  mkdir -p /etc/ssh/sshd_config.d
  cat > /etc/ssh/sshd_config.d/10-hardening.conf <<'EOF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
EOF
  sshd -t || die "sshd config is invalid — NOT reloading, fix /etc/ssh/sshd_config.d/10-hardening.conf"
  systemctl reload ssh
  ok "sshd reloaded"

  note "Step 6 — nftables"
  # Its own table only. NEVER `flush ruleset`: that wipes Docker's NAT rules.
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
    iifname "br-*" accept                       # container -> host traffic
    meta l4proto { icmp, ipv6-icmp } accept
    tcp dport { 80, 443 } accept                # Caddy
    udp dport 443 accept                        # HTTP/3
    udp dport 41641 accept                      # Tailscale direct connections
  }
}
EOF
  nft -c -f /etc/nftables.conf || die "nftables ruleset is invalid — NOT applying"
  systemctl enable --now nftables
  systemctl reload nftables 2>/dev/null || nft -f /etc/nftables.conf
  nft list table inet host
  ok "firewall up: 80/443 public, everything else over Tailscale only"
  echo
  echo ">>> From your laptop: 'ssh debian@<public-ip>' must now TIME OUT,"
  echo ">>> and 'nmap -Pn <public-ip>' must show only 80 and 443."
  echo ">>> Locked out? OVH control panel -> Rescue mode -> fix /etc/nftables.conf"
}

# ------------------------------------------------------------- Step 7: Docker
phase_docker() {
  note "Step 7 — Docker CE"
  [[ -d /srv/docker ]] \
    || die "/srv/docker does not exist — run 'bootstrap.sh disk' first, otherwise every image
       would land on the small root partition."

  if ! command -v docker >/dev/null 2>&1; then
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
    chmod a+r /etc/apt/keyrings/docker.asc
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
      > /etc/apt/sources.list.d/docker.list
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
      docker-ce docker-ce-cli containerd.io docker-compose-plugin
  fi

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
  docker run --rm hello-world >/dev/null && ok "docker works"
  docker info --format '{{.DockerRootDir}}' | grep -qx /srv/docker \
    || die "Docker's root dir is not /srv/docker — check /etc/docker/daemon.json"
  df -h /srv/docker
}

# ---------------------------------------------------------------- verify
phase_verify() {
  local fail=0
  check() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else echo "   FAIL: $1"; fail=1; fi; }

  note "Verifying every 'Done when' from Steps 3-7"
  check "RAID: all md devices [UU]"          '! grep -qE "\[[U_]*_[U_]*\]" /proc/mdstat'
  check "/srv is mounted"                    'findmnt /srv'
  check "/srv/apps/postgres exists"          '[ -d /srv/apps/postgres ]'
  check "/srv/apps/caddy exists"             '[ -d /srv/apps/caddy ]'
  check "/srv/apps/bob exists"               '[ -d /srv/apps/bob ]'
  check "pgBackRest spool dir exists"        '[ -d /srv/backup/spool ]'
  check "Docker root dir is /srv/docker"     'docker info --format "{{.DockerRootDir}}" | grep -qx /srv/docker'
  check "tailscale up"                       'tailscale status'
  check "sshd: no passwords"                 'sshd -T | grep -q "^passwordauthentication no"'
  check "nftables host table loaded"         'nft list table inet host'
  check "docker running"                     'docker info'
  echo
  df -h /srv /srv/docker
  echo
  [[ $fail -eq 0 ]] && echo "ALL GREEN — next: docs/ops.md Step 8 (the GCS bucket, from your laptop)" \
                    || { echo "SOME CHECKS FAILED (above)"; exit 1; }
}

case "${1:-}" in
  base)      phase_base ;;
  disk|lvm)  phase_disk ;;
  tailscale) phase_tailscale ;;
  lockdown)  shift; phase_lockdown "${1:-}" ;;
  docker)    phase_docker ;;
  verify)    phase_verify ;;
  *) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
