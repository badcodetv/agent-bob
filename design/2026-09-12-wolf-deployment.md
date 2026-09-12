# Agent Wolf on the OVH box — the deployment recipe

Status: **written, not executed.** Nothing here has run on a server, because at the time of
writing no server exists (thread 01 owns building it). The local rehearsal that this document is
derived from is recorded in § "What the local rehearsal proved".

This turns `README-stack.md` § "What is real here, and what still differs from a deployment" —
a four-row table — into steps for the box described in `docs/ops.md`. It follows that document's
Part 3 "Add a new app" (`docs/ops.md:849`) and assumes Step 11 (Agent Bob) is already done and
green.

Audience: whoever is at the terminal. Every command is meant to be pasted.

---

## 0. What Agent Wolf actually needs, in one screen

Wolf is two containers and no database.

| | What | Where its state lives |
| --- | --- | --- |
| `wolf-api` | Node/Express. Hypothesis lifecycle, the market-data MCP server, the evaluation poller. | **Nowhere of its own.** |
| `wolf-web` | nginx serving a built React bundle. | Static files. |

**This is the single most important fact for operating it.** `agent-wolf/docker-compose.yml`
declares no volumes at all, and Wolf holds no server-side session state (the login cookie is
signed, not stored). Every authoritative fact — a hypothesis, its spec, its verdict — is a
**memory in Bob's Postgres**, written through Bob's API and trusted only because the server stamps
its provenance empty (`design/2026-08-20-agent-wolf.md` § "The trust model").

Two consequences, both load-bearing:

- **Restarting Wolf costs nothing.** No data is lost, nobody is signed out. This is what makes
  "add a tester" a ten-second operation (§ 6).
- **Backing up Wolf's disk protects almost nothing.** What protects a tester's work is **Bob's**
  hourly backup of `/srv/apps/bob`, which already exists. Wolf still gets a small disk (§ 2) so
  its secrets are backed up and the "every app has a volume" pattern holds — but do not mistake
  that volume for the safety net.

## 1. Decisions taken here, with reasons

| Decision | Value | Why |
| --- | --- | --- |
| Domain | `wolf.badcode.tv` | Kai, 2026-09-12. Matches `bob.badcode.tv`. |
| Internal port | `8120` | Already reserved for Agent Wolf in the port table, `docs/ops.md:190`. |
| First allowlist | `kaiyadavenport@gmail.com` | Kai, 2026-09-12: "for the moment it's just me". Others added later by § 6. |
| Disk size | 10G | It holds a `.env` and nothing else (§ 0). |
| Session image tag | a commit sha, never `:dev` | `:dev` is a stable tag with drifting contents (`README-stack.md` § "Registry mode"). A box should run bytes you can name. |
| Researcher schedule | Wolf's own default, daily 06:00 UTC | Cost. See § 9. |

## 2. The disk and the folder

```bash
new-app-volume wolf 10G          # /srv/apps/wolf, backed up hourly
mkdir -p /srv/apps/wolf/src
cd /srv/apps/wolf
git clone https://github.com/badcodetv/agent-wolf.git src
```

## 3. The networking rule that must survive the move

`wolf-api` shares Bob's Docker-in-Docker container's **network namespace**. This is not a local
convenience, it is mandatory in every environment
(`design/2026-08-20-agent-wolf.md` § "Local topology and networking"): the AI session containers
run nested inside DinD and **cannot resolve compose service names**, so a `wolf-api` on an
ordinary network is unreachable from the very sessions that need its market-data tools.

Wolf's compose file expects two names from Bob's stack:

- the network `agent-bob_default`
- the container `agent-bob-dind-1`

**Both hold on the box unchanged.** Bob's compose file pins its project name explicitly
(`docker-compose.yml:4`, `name: agent-bob`), so the names do not follow the directory — even
though `docs/ops.md:737` starts Bob from a directory called `src`. Nothing to configure. *This
was checked rather than assumed; a compose project that took its name from the directory would
have produced `src_default` / `src-dind-1` and a `wolf-api` that never starts.*

## 4. 🔴 A gap in `docs/ops.md` Step 11 that blocks Wolf

**Bob as Step 11d configures it cannot pull Wolf's session image.** Step 11d's `.env`
(`docs/ops.md:745-800`) sets the *blob* backend to GCS but says nothing about the *registry*
backend, and the default is `blobarchive`, not Artifact Registry
(`go/cmd/agentd/backends.go:92`). Wolf's sessions launch from `session-wolf:<tag>` **in Artifact
Registry**; with the default backend `EnsurePresent` is a local no-op and every Wolf session fails
to start.

Add these four lines to `/srv/apps/bob/src/.env` before starting Wolf:

```bash
AGENTKIT_REGISTRY_BACKEND=ociregistry
AGENTKIT_REGISTRY_AUTH=gcp
GCP_REGION=europe-west1
GCP_AR_REPO=agent-bob
```

`GCP_PROJECT` and `GOOGLE_APPLICATION_CREDENTIALS` are already there from Step 11d. The
`agent-bob-runtime` service account key mounted at `/gcp/key.json` is the credential that does
the pull.

✅ **Already folded into `docs/ops.md` by thread 01** — commit `3ddce38` on `thread/01-ovh`, these
exact four lines. Nothing for this thread to do; the note stays because the two documents must
keep agreeing.

🔴 **Do NOT set `AGENTKIT_REGISTRY_ALWAYS_PULL=true` on the box.** It is correct on a laptop, where
`:dev` is a stable tag with drifting contents, and wrong here. `EnsurePresent` skips the pull when
the image already inspects locally — **but only when always-pull is off**
(`go/imageregistry/ociregistry/ociregistry.go`, the `if !r.alwaysPull` guard). With it on, agentd
force-pulls *every* image including the sandbox and core images the box built for itself, which
exist in no registry, and they fail. Raised by thread 01; verified here by reading that function.

🔴 **Switching to `ociregistry` turns snapshot reclamation off, and nobody is told.** On this
backend an idle session's snapshot is a **layer push to Artifact Registry**, and
`Registry.Remove` returns `nil` without deleting anything — deletion is explicitly out of band
(`go/imageregistry/ociregistry/ociregistry.go:268-274`). The reaper still calls it
(`go/snapshot_reaper.go:272`), so `AGENTKIT_SNAPSHOT_REAP_INTERVAL` clears the database row and
leaves the bytes. Archived sessions therefore accumulate **forever** — slow and deduplicated, but
unbounded.

This matters more for Wolf than for Bob: § 9's daily researcher creates a session per live
hypothesis per day, every one of which is eventually archived. The fix is a cleanup policy on the
`agent-bob` Artifact Registry repository, which thread 01 has recorded in
`design/2026-09-12-gke-to-box-migration.md` § 6 item 2, pending Kai's yes. 💰 Not this thread's to
approve or apply.

⚠️ `/srv/apps/bob/src/.env` is thread 01's file to edit, not this thread's.

## 5. Bob's side: the `wolf` project

Three things must be true inside Bob before Wolf will talk to it.

**5a. The project map gains a `wolf` entry.** On the box that file is
`/srv/apps/bob/secrets/projects.json` (`docs/ops.md:759-768`), and agentd re-reads it on SIGHUP
or every 60 seconds — no restart:

```json
{
  "users": { "kaiyadavenport@gmail.com": ["*"] },
  "projects": {
    "wolf": {
      "api_key_env": "WOLF_API_KEY",
      "allowed_origins": ["https://wolf.badcode.tv"]
    }
  }
}
```

**5b. Bob's environment must carry the two Wolf secrets**, in
`/srv/apps/bob/src/.env`:

```bash
WOLF_API_KEY=<generated, see 7>
WOLF_MCP_TOKEN=<generated, see 7>
AGENTKIT_MCP_ENV=WOLF_MCP_TOKEN
```

`WOLF_API_KEY` is resolved **at boot** from the name the project map gives in `api_key_env`, so
this one needs a Bob restart. `AGENTKIT_MCP_ENV` is the line that forwards the MCP token into
session containers; without it every `mcp__wolf__*` call returns 401 with no other symptom
(`stack:718-720`).

**5c. The project must be bootstrapped** — settings, the interviewer worker, the critic worker
and its schedule. Idempotent; safe to re-run. § 8 runs it.

## 6. The allowlist, and adding testers later

Wolf reads `WOLF_ALLOWED_EMAILS` **once, at boot** (`api/src/config.ts:773`). Two properties
worth knowing:

- **An empty list is refused at startup**, naming the variable. Empty never means everyone
  (`api/src/auth/session.ts:216-219`).
- **Removing someone is retroactive.** Their existing cookie stops working on the next request,
  not when it expires (`api/src/routes/auth.ts:191-203`).

Because Wolf is stateless (§ 0), "restart to apply" is free. Ship it as a script:

```bash
cat > /srv/apps/wolf/add-tester.sh <<'EOF'
#!/usr/bin/env bash
# add-tester.sh someone@gmail.com [more@gmail.com ...]
# Adds addresses to Wolf's sign-in allowlist and restarts wolf-api (no data is lost:
# Wolf stores nothing — every hypothesis lives in Bob's database).
set -euo pipefail
[[ $# -ge 1 ]] || { echo "usage: add-tester.sh email [email ...]" >&2; exit 1; }
ENV=/srv/apps/wolf/.env
current="$(grep -E '^WOLF_ALLOWED_EMAILS=' "$ENV" | cut -d= -f2-)"
for e in "$@"; do
  [[ "$e" == *@*.* ]] || { echo "not a full email address: $e" >&2; exit 1; }
  case ",$current," in *",$e,"*) echo "already present: $e"; continue ;; esac
  current="${current:+$current,}$e"
done
sed -i -E "s|^WOLF_ALLOWED_EMAILS=.*|WOLF_ALLOWED_EMAILS=${current}|" "$ENV"
echo "allowlist: $current"
/srv/apps/wolf/up.sh wolf-api
echo "done — they can sign in now."
EOF
chmod +x /srv/apps/wolf/add-tester.sh
```

Removing someone is the same file, by hand, then `/srv/apps/wolf/up.sh wolf-api`.

## 7. Secrets

Four generated, three copied. All into `/srv/apps/wolf/.env` at mode 600, **and into the password
manager** — `WOLF_API_KEY` in particular exists in two places (Bob's `.env` and Wolf's) and
nothing checks that they match.

```bash
umask 077
cat > /srv/apps/wolf/.env <<EOF
# ── who may sign in (see add-tester.sh) ──
WOLF_ALLOWED_EMAILS=kaiyadavenport@gmail.com

# ── generated, this box only ──
WOLF_API_KEY=$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')
WOLF_MCP_TOKEN=$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')
WOLF_SESSION_SECRET=$(openssl rand -base64 32)
WOLF_SERIES_TOKEN_SECRET=$(openssl rand -base64 32)

# ── copied from Bob's .env ──
VITE_GOOGLE_CLIENT_ID=<same GOOGLE_CLIENT_ID Bob uses>
FRED_API_KEY=<from agent-bob/.env>

# ── the one address, written three times (see § 8) ──
BOB_PUBLIC_URL=https://bob.badcode.tv
VITE_BOB_PUBLIC_URL=https://bob.badcode.tv

# ── deployment values ──
BOB_BASE_URL=http://localhost:8099
BOB_DIND_CONTAINER=agent-bob-dind-1
WOLF_MCP_URL=http://172.17.0.1:8100/mcp
WOLF_WEB_PORT=127.0.0.1:8120
WOLF_API_PORT=8100
NODE_ENV=production
WOLF_BASE_IMAGE=europe-west1-docker.pkg.dev/webkit-servers/agent-bob/session-wolf:<sha>
EOF
chmod 600 /srv/apps/wolf/.env
```

Notes on three of those:

- **`NODE_ENV=production`** makes the sign-in cookie `Secure` and makes the process **refuse** to
  start alongside `WOLF_TEST_LOGIN` — that route verifies no credential and would be a sign-in
  bypass (`.env.example:194-196`).
- **`BOB_BASE_URL=http://localhost:8099`** is correct and looks wrong. `wolf-api` is inside DinD's
  network namespace, so `localhost` there **is** agentd.
- **`WOLF_MCP_URL`** must be set explicitly on a real deployment; when set it wins over the boot
  probe. Confirm the address first with
  `docker exec agent-bob-dind-1 ip -4 -o addr show docker0`, because Docker allocates
  `172.17.0.0/16` only if it is free and a shift makes every market-data tool call time out with
  no obvious cause (R43/R235/R236 in the design doc).

## 8. 🔴 One address, written in four places, checked by nothing

`https://wolf.badcode.tv` must appear in **four** places, and no code compares them:

1. Wolf's `.env` → nothing; the Wolf origin is implied by the Caddy block (§ 10).
2. Bob's project map `allowed_origins` (§ 5a) — wrong here and the embedded chat iframe is blocked.
3. Google's OAuth client, **Authorized JavaScript origins** — wrong here and the Google button
   renders and the popup dies with `origin_mismatch`.
4. Nothing else — but `https://bob.badcode.tv` has the same problem and appears as **both**
   `BOB_PUBLIC_URL` and `VITE_BOB_PUBLIC_URL` (§ 7), which must be byte-identical.

`VITE_*` values are **inlined into the browser bundle at build time**. Changing one needs
`docker compose ... up -d --build wolf-web`; a restart changes nothing.

**Step 3 is Kai's, in the Google Cloud console, and cannot be scripted.** It is the one manual
step `README-stack.md` names.

## 9. The compose overlay and `up.sh`

```bash
cat > /srv/apps/wolf/compose.ovh.yml <<'EOF'
# Layered on top of src/docker-compose.yml. Caps Wolf so it cannot starve Bob.
services:
  wolf-api:
    mem_limit: 2g
    cpus: 2
  wolf-web:
    mem_limit: 256m
EOF

cat > /srv/apps/wolf/up.sh <<'EOF'
#!/usr/bin/env bash
# Start or update Wolf:  /srv/apps/wolf/up.sh [service...]
# Bob must be up first: wolf-api joins Bob's dind container's network namespace.
set -euo pipefail
cd /srv/apps/wolf
docker compose --project-directory src --env-file /srv/apps/wolf/.env \
  -f src/docker-compose.yml -f compose.ovh.yml up -d --build "$@"
EOF
chmod +x /srv/apps/wolf/up.sh
```

**Cost, which is the thing to watch.** Each *live* hypothesis triggers one real agent session per
day at 06:00 UTC, plus the critic's schedule. The bill scales with (testers × live hypotheses),
not with page visits. Sessions each hold a container and one of Bob's 100 host ports until
finished or idle for 30 minutes. Do **not** set a fast cron on the box: `*/15 * * * *` is roughly
96 billable sessions a day *per hypothesis* (`README-stack.md` § "Cost").

There is a **second, quieter cost** and § 4 names it: every one of those sessions is eventually
archived, archiving pushes a snapshot to Artifact Registry, and on this backend nothing ever
deletes it. Storage grows monotonically with the number of researcher ticks the box has ever run.
The Artifact Registry cleanup policy is the control, and it does not exist yet.

## 10. Caddy and DNS

Append to `/srv/apps/caddy/Caddyfile` (pattern from `docs/ops.md:645`):

```
wolf.badcode.tv {
	# No `encode`: compression holds back the live chat stream (SSE), same as Bob's block.
	reverse_proxy 127.0.0.1:8120 {
		flush_interval -1
	}
}
```

```bash
docker compose -f /srv/apps/caddy/compose.yml exec -w /etc/caddy caddy caddy reload
```

Then a DNS **A record: `wolf` → the box's public IP**. Caddy fetches the certificate on the first
visit, so DNS must exist first.

## 11. The session image

Wolf's sessions launch from `session-wolf`, which is `session-core` plus pandas/numpy/duckdb
(`agent-wolf/installations/wolf/Dockerfile`). It is built **on a laptop** and pushed; the box only
pulls.

```bash
# 💻 laptop, from agent-bob:
./stack publish-base "$(git rev-parse --short HEAD)"
cd ../agent-wolf
REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-bob \
  ./scripts/publish-image.sh "$(cd ../agent-bob && git rev-parse --short HEAD)"
```

Put the printed reference in `WOLF_BASE_IMAGE` (§ 7) and re-run the bootstrap (§ 12) — that value
lives in the `wolf` project's **settings row**, not in the container's environment, so restarting
`wolf-api` does not update it.

💰 This pushes to the shared production registry. Gate it on Kai.

## 12. Bring it up, in order

```bash
/srv/apps/bob/up.sh                                   # Bob first, always
/srv/apps/wolf/up.sh
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8120/api/auth/me   # expect 401
```

`401` is the pass: Wolf is up **and** refusing anonymous callers.

Then bootstrap the project (idempotent) from the laptop or the box, with Wolf's secrets in the
environment:

```bash
cd /srv/apps/wolf/src && npx --yes tsx scripts/bootstrap-project.ts
```

## 13. ✅ Done when

- `ss -tlnp` shows port 8120 bound to **127.0.0.1 only**. Never `0.0.0.0` — Docker punches
  straight through the firewall (`docs/ops.md:177`).
- `https://wolf.badcode.tv` serves the sign-in page over HTTPS.
- Kai signs in with Google and reaches the board.
- Kai states a hypothesis and completes the interview.
- The next morning, the researcher has run and the hypothesis shows a score.
- `/srv/apps/wolf/add-tester.sh` adds a second address and that person can sign in.

## 14. What the local rehearsal proved

Run 2026-09-12 on Kai's laptop, exit code 0, all six steps green:

    AO_IMAGE_TAG=04e2703 ./stack wolf up mock --skip-image --no-tmux

`AO_IMAGE_TAG` is not the documented default. The registry holds **no `:dev` tag** — only
`04e2703` and `latest`, published 2026-09-10 (`gcloud artifacts docker images list
europe-west1-docker.pkg.dev/webkit-servers/agent-bob`) — so a bare `./stack wolf up mock` dies
pulling `session-core:dev`, and creating that tag means a rebuild and a push to the shared
production registry. The published sha was used instead.

What each step actually proved:

| | Proof |
| --- | --- |
| Bob up, `wolf` project live | `bob: the wolf project's API key is accepted` — the merged project map parsed **and** agentd resolved `api_key_env` at boot. |
| Wolf up and closed | `/api/auth/me` answered **401**: serving, and refusing anonymous callers. |
| Bootstrap idempotent | `{"projectSettings":"updated","interviewer":"unchanged","critic":"unchanged","criticSchedule":"unchanged"}` on an already-seeded database. |
| Allowlist resolution | Resolved to `kaiyadavenport@gmail.com` from Bob's own project map — the fallback path in `stack:636-657` works. |
| **Nothing was billed** | `[agentd] ANTHROPIC_API_KEY unset → MOCK model proxy`, and neither `real model proxy →` nor `subscription mode →` appears anywhere in agentd's log. Checked because `./stack wolf env` redacts those two variables **by name** and prints `<redacted>` even when they are empty — that dump is a declaration of what was configured, never evidence of which model ran. |

**The § 3 topology was verified at runtime, not from the compose file** (the Validation rule,
`design/2026-08-20-agent-wolf.md:166`). From inside Bob's DinD container, where the AI sessions
live:

    $ docker exec agent-bob-dind-1 ip -4 -o addr show docker0   →  172.17.0.1/16
    $ docker exec agent-bob-dind-1 wget -S -O /dev/null http://172.17.0.1:8100/mcp
      HTTP/1.1 401 Unauthorized

`401` is the pass and `exit 7` would be the failure: reachable, credential refused. This is the
one check `docker compose config` cannot make, and the one an nginx bug survived in W1.

## 15. Open, and owned by someone else

- **The box itself** — thread 01. Nothing above can run until it reports.
- **A verified Bob** — thread 02. Wolf runs on Bob; five unverified branches is not a base to
  ship on.
- **§ 4's four lines in Bob's `.env`** — thread 01's file; **done**, commit `3ddce38`.
- **An Artifact Registry cleanup policy** (§ 4, § 9) — thread 01 is holding it for Kai's yes.
  Without it Wolf's daily researcher grows registry storage without bound.
- **§ 8 step 3, the Google console** — Kai only.

## 16. Open question, parked by Kai on 2026-09-12 — session storage has no retirement policy

Wolf is what exposed it — its daily researcher creates a session per live hypothesis per day, all
of which are eventually archived, and **nothing in Agent Bob ever deletes an idle-session
snapshot, on any registry backend**. Not this thread's to solve, and deliberately not solved:
written up as a concern in
[`2026-09-12-session-state-and-snapshot-retention.md`](./2026-09-12-session-state-and-snapshot-retention.md).

The stopgap that bounds the bill is the Artifact Registry cleanup rule in § 4, which thread 01
holds for Kai's yes.
