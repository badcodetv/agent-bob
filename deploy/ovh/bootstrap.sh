#!/usr/bin/env bash
#
# bootstrap.sh — build a fresh OVH box up to the point where Caddy and Bob can be started.
# This is the executable form of docs/ops.md Steps 3-7. The doc explains WHY; this runs it.
#
#   scp -r deploy/ovh debian@<ip>:/tmp/ && ssh debian@<ip>
#   sudo -i
#   /tmp/ovh/bootstrap.sh base       # Step 3  — packages, unattended-upgrades, hostname   (~10 min, reboots)
#   /tmp/ovh/bootstrap.sh lvm        # Step 4  — thin pool + the bob/caddy/ops/docker LVs  (~5 min)
#   /tmp/ovh/bootstrap.sh tailscale  # Step 5a — install + `tailscale up` (prints a link)  (~3 min)
#   ### now prove `ssh debian@<tailscale-ip>` works from a NEW terminal on your laptop ###
#   /tmp/ovh/bootstrap.sh lockdown --i-can-ssh-over-tailscale   # Step 5b + 6 — SSH keys only, nftables
#   /tmp/ovh/bootstrap.sh docker     # Step 7  — Docker CE onto the un-backed-up LV        (~5 min)
#   /tmp/ovh/bootstrap.sh verify     # re-checks every "Done when" in one go
#
# Every phase is idempotent: re-running one is safe.
#
set -euo pipefail

die()  { echo "ERROR: $*" >&2; exit 1; }
note() { echo; echo "== $*"; }
ok()   { echo "   ok: $*"; }

[[ $EUID -eq 0 ]] || die "run as root (sudo -i)"
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

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

# ------------------------------------------------------------- Step 4: LVM pool
phase_lvm() {
  note "Step 4 — LVM thin pool"

  if vgs vg0 >/dev/null 2>&1; then
    ok "vg0 already exists, skipping pool creation"
  else
    local dev
    dev=$(findmnt -no SOURCE /srv 2>/dev/null || true)
    [[ -n "$dev" ]] || die "/srv is not mounted. Expected the OVH installer's placeholder partition.
       If you used the installer's own LVM option instead, create vg0 + vg0/pool by hand, then re-run."
    echo "   /srv is on $dev — this will be WIPED and handed to LVM."
    read -rp "   type the device path again to confirm: " confirm
    [[ "$confirm" == "$dev" ]] || die "mismatch, aborting"

    umount /srv
    sed -i '\#[[:space:]]/srv[[:space:]]#d' /etc/fstab
    wipefs -a "$dev"
    pvcreate "$dev"
    vgcreate vg0 "$dev"
    lvcreate --type thin-pool -l 90%FREE --poolmetadatasize 2G -n pool vg0
    ok "vg0/pool created on $dev"
  fi

  # Let the pool grow itself at 70% full, while the spare 10% of the VG lasts.
  sed -i -E 's/^[[:space:]]*#?[[:space:]]*thin_pool_autoextend_threshold = .*/\tthin_pool_autoextend_threshold = 70/' /etc/lvm/lvm.conf
  sed -i -E 's/^[[:space:]]*#?[[:space:]]*thin_pool_autoextend_percent = .*/\tthin_pool_autoextend_percent = 20/'   /etc/lvm/lvm.conf
  grep -qE '^\s*thin_pool_autoextend_threshold = 70' /etc/lvm/lvm.conf \
    || die "could not set thin_pool_autoextend_threshold in /etc/lvm/lvm.conf — set it by hand"
  ok "pool autoextends at 70%, by 20%"

  install -m 0755 "$HERE/new-app-volume" /usr/local/sbin/new-app-volume
  ok "/usr/local/sbin/new-app-volume installed"

  new-app-volume bob   100G
  new-app-volume caddy 2G
  new-app-volume ops   10G

  # Docker's store: NO backup tag. Images and caches are rebuildable (ops.md 1.5).
  if ! lvs vg0/docker >/dev/null 2>&1; then
    systemctl is-active --quiet docker && systemctl stop docker
    lvcreate -qq -V 300G -T vg0/pool -n docker
    mkfs.ext4 -q -L docker /dev/vg0/docker
    mkdir -p /var/lib/docker
    grep -q '^/dev/vg0/docker ' /etc/fstab \
      || echo "/dev/vg0/docker /var/lib/docker ext4 defaults,noatime 0 2" >> /etc/fstab
    mount /var/lib/docker
    ok "vg0/docker mounted at /var/lib/docker (NOT backed up, by design)"
  else
    ok "vg0/docker already exists"
  fi

  lvs -o lv_name,lv_size,lv_tags vg0
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
  findmnt -no SOURCE /var/lib/docker | grep -q '/dev/mapper/vg0-docker' \
    || die "/var/lib/docker is not on the vg0/docker LV — run 'bootstrap.sh lvm' first,
       otherwise every image would land on the small root disk."

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
  "log-driver": "local",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true,
  "default-address-pools": [ { "base": "172.20.0.0/14", "size": 24 } ]
}
EOF
  systemctl restart docker
  usermod -aG docker debian
  docker run --rm hello-world >/dev/null && ok "docker works"
  df -h /var/lib/docker
}

# ---------------------------------------------------------------- verify
phase_verify() {
  local fail=0
  check() { if eval "$2" >/dev/null 2>&1; then ok "$1"; else echo "   FAIL: $1"; fail=1; fi; }

  note "Verifying every 'Done when' from Steps 3-7"
  check "RAID: all md devices [UU]"          '! grep -qE "\[[U_]*_[U_]*\]" /proc/mdstat'
  check "vg0/pool exists"                    'lvs vg0/pool'
  check "LV bob exists"                      'lvs vg0/bob'
  check "LV caddy exists"                    'lvs vg0/caddy'
  check "LV ops exists"                      'lvs vg0/ops'
  check "LV docker exists"                   'lvs vg0/docker'
  check "backup tag on bob only where meant" '[ "$(lvs --noheadings -o lv_name @backup | tr -d " " | sort | tr "\n" ",")" = "bob,caddy,ops," ]'
  check "/srv/apps/bob mounted"              'mountpoint -q /srv/apps/bob'
  check "/var/lib/docker on its own LV"      'findmnt -no SOURCE /var/lib/docker | grep -q vg0-docker'
  check "tailscale up"                       'tailscale status'
  check "sshd: no passwords"                 'sshd -T | grep -q "^passwordauthentication no"'
  check "nftables host table loaded"         'nft list table inet host'
  check "docker running"                     'docker info'
  echo
  lvs -o lv_name,lv_size,data_percent,lv_tags vg0
  echo
  [[ $fail -eq 0 ]] && echo "ALL GREEN — next: docs/ops.md Step 8 (the GCS bucket, from your laptop)" \
                    || { echo "SOME CHECKS FAILED (above)"; exit 1; }
}

case "${1:-}" in
  base)      phase_base ;;
  lvm)       phase_lvm ;;
  tailscale) phase_tailscale ;;
  lockdown)  shift; phase_lockdown "${1:-}" ;;
  docker)    phase_docker ;;
  verify)    phase_verify ;;
  *) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
