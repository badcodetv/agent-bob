# Kubernetes Deployment Playbook — Agent Orange on `prodcluster`

> **HOW TO USE THIS:** Work top to bottom. Every command is meant to be pasted.
> Section 2 must be done before section 4, or the pod boots and immediately
> dies. Section 1 is a record of checks already run — re-run them if more than
> a few weeks have passed, otherwise trust them and move on.

Status: **READY TO EXECUTE — never yet applied to any cluster.**
Written 2026-09-09. The manifests it drives were written 2026-08-11 (`406f52b`)
and their commit message says plainly: *"Nothing has been applied to any
cluster."* That is still true. Everything below is the first run.

Relates: `deploy/k8s/README.md` (the manifests' own guide — this playbook
supersedes it where they disagree, and section 2 lists exactly where),
`deploy/publish-base.sh`, `deploy/publish-images.sh`, `deploy/k8s/apply.sh`,
`deploy/gcp/setup.sh` (already run — see §1), `docs/15-standalone-stack.md`,
`docs/19-embedding.md` §Known hazards, `MIGRATION.md` §4a/§4b.

Shape borrowed from `forum/deploy/manual_deploy.sh`: **build locally, push to
the registry, `kubectl apply`.** No CI, no GitOps, no Helm. The two halves of
that script already exist here as `deploy/publish-images.sh` (its `build`) and
`deploy/k8s/apply.sh` (its `deploy`); Appendix A wraps them into one file if you
want the single-command version.

---

## 0. What this deploys, and what it does not

**Deploys:** Agent Orange itself — the API/orchestrator (`agentd`), its
Docker-in-Docker daemon, Postgres with pgvector, the web console, and a public
HTTPS entry point.

**Does not deploy:**

- **Agent Wolf.** It lives in the sibling checkout `../agent-wolf`, has a
  `docker-compose.yml` and **no Kubernetes manifests at all**. Deploying Wolf on
  top of this is separate, unwritten work.
- **Backups.** The Postgres volume holds every session, memory, worker and
  config event. Nothing snapshots it. Cloud SQL is the move when losing it would
  actually hurt.
- **Horizontal scale.** One replica, strategy `Recreate`, by design. Two
  `agentd` processes against one image store fight over host ports and container
  ownership. Scaling here means a bigger node, never more pods.

### The shape, and why it is this shape

`docker-compose.yml` runs `agentd` with `network_mode: "service:dind"` — it
**shares the Docker daemon's network namespace**. Three things depend on that:

1. `agentd` reaches the daemon at `localhost:2375`;
2. session containers reach `agentd` back at the DinD bridge gateway
   `172.17.0.1` (that is `AGENTKIT_SELF_URL`);
3. `agentd` leases one host port per session from a 100-port pool and then talks
   to the session on that port.

Containers in a single Kubernetes pod share a network namespace by definition,
so **one pod with two containers reproduces compose exactly**. Splitting them
across two pods would need a Service enumerating 100 ports and pod IPs that
move. This is why the `agent-orange` Deployment has a `dind` container and an
`agentd` container side by side.

The `agentd` Service is named **`dind`** because `deploy/web.nginx.conf` proxies
to `http://dind:8099`, baked into the web image. The odd name is what lets that
image run here unmodified.

### 🔴 What you are accepting by putting this on `prodcluster`

The `dind` container runs **privileged** — it manages cgroups, mounts and
network namespaces — and its entire job is to run bash that a language model
wrote. `docs/02-execution-environment.md` is explicit that plain Docker is not a
safe boundary for untrusted commands. That container will sit on the same node
as `bookingsystem`, `forum`, `franchisecloud`, `nocode-works` and the rest.

Second, **Kubernetes cannot see session containers.** They are started by the
Docker daemon *inside* the pod, so the scheduler does not know they exist and
cannot stop them competing for the node's 2 CPUs. The node currently has roughly
790 milli-CPU unreserved; Agent Orange's own four containers request 420 of it.

This was decided knowingly on 2026-09-09: the node is quiet, and Agent Orange is
IO-bound on remote model APIs rather than CPU-bound. Recorded here so the
decision is not mistaken later for an oversight.

---

## 1. Facts verified on the live cluster (2026-09-09)

All read-only, or server-side dry runs that wrote nothing.

| Check | Result |
| --- | --- |
| Cluster | `gke_webkit-servers_europe-west1-b_prodcluster` |
| Nodes | **1** — `gke-prodcluster-two-cpu-pool-…`, GKE v1.35.7, **2 vCPU / 16 GiB** |
| Flavour | GKE **Standard** (node pool `two-cpu-pool`, machine family `e2`) — *not* Autopilot |
| Privileged pods | ✅ **allowed** — `kubectl apply --dry-run=server` on a privileged `docker:27-dind` probe returned `pod/dind-probe created (server dry run)` |
| CPU already requested | 1137m of ~1930m allocatable (**58%**) across 64 pods |
| Memory already requested | 2.76 GiB (**19%**) |
| Ingress controller | ✅ `ingressClass: nginx`, controller `k8s.io/ingress-nginx`, 3y old |
| Ingress external IP | **`104.155.75.230`** — every `*.badcode.tv` host already points here |
| Certificates | ✅ `ClusterIssuer/letsencrypt-prod`, `READY=True` |
| Storage class | ✅ `standard-rwo` (default), `WaitForFirstConsumer`, expandable |
| Namespace `agent-orange` | ❌ does not exist yet |
| Artifact Registry repo | ✅ `europe-west1-docker.pkg.dev/webkit-servers/agent-orange`, 874 MB, last written 2026-09-07 |
| Images already in it | `session-base`, `session-core`, `session-wolf` (all tagged `dev`), plus three digest-named snapshot images. **No `agentd`, no `web` — `publish-images.sh` has never run.** |
| GCS bucket | ✅ `webkit-servers-agent-orange`, `EUROPE-WEST1` |
| Runtime service account | ✅ `agent-orange-runtime@webkit-servers.iam.gserviceaccount.com` (created by `deploy/gcp/setup.sh`; holds `roles/storage.objectAdmin` on the bucket and `roles/artifactregistry.writer` on the repo) |
| Docker credential helpers | ✅ both `gcr.io` and `europe-west1-docker.pkg.dev` configured in `~/.docker/config.json` |
| `orange.badcode.tv` | ❌ **NXDOMAIN** — the DNS record does not exist yet |
| `secrets/gcp-key.json` | 🔴 **is an empty root-owned DIRECTORY**, not a key file (§2, D4) |

**Read of that table:** every hard prerequisite the manifests name is already
satisfied. The GCP side was provisioned back on 2026-06-25 and is still in
place. What is missing is a DNS record, a real key file, three manifest fixes,
and two images that have never been pushed.

---

## 2. Four defects to fix before the first apply

These are real, verified in the code, and each one either kills the boot or
silently removes a feature.

**Status: D1, D2 and D3 were FIXED IN THE MANIFESTS on 2026-09-09** — the
descriptions below are kept as the record of what was wrong and why, because
each fix is a comment in a YAML file that a future edit could quietly undo.
**D4 is a local machine fix and is still outstanding.**

### D1 ✅ FIXED — an empty project map killed the boot when login is on

`deploy/k8s/00-namespace-and-config.yaml` ships `project-map: ""` with the
comment *"Leave empty until another application needs to embed this one."*
`deploy/k8s/README.md` then tells you to create a `test-login` secret.

Those two instructions are incompatible. `go/cmd/agentd/main.go:543`:

```go
if projectCfg == nil {
    log.Fatal("[agentd] login modes require a project map: set AGENTKIT_PROJECT_MAP or AGENTKIT_PROJECT_MAP_FILE")
}
```

and `loadProjectSettingsOptional` (`googleauth.go:240`) returns `nil, nil` when
both `AGENTKIT_PROJECT_MAP` and `AGENTKIT_PROJECT_MAP_FILE` are empty. So:
setting *either* login mode with an empty map is a `CrashLoopBackOff` with one
clear log line — which you will only see if you know to look at the log.

**Fix.** Put a real map in the ConfigMap. The object form:

```yaml
  project-map: |
    {"users":{"kaiyadavenport@gmail.com":["*"]},"projects":{}}
```

`"*"` is the wildcard: every project in the map, plus the ability to create new
projects from the console. Add Jack's address alongside when he needs in.

### D2 ✅ FIXED — the `test-login` secret in the README was the wrong format

`deploy/k8s/README.md` says:

```sh
--from-literal=test-login='pick-something-unguessable'
```

`parseTestLogin` (`go/cmd/agentd/googleauth.go:597`) requires **`email:password`**
and the caller is `must(err)` — so a bare password is another boot fatal:

```
AGENTKIT_TEST_LOGIN must be "email:password"
```

**Fix, two options.** Prefer the first.

- **Google sign-in (recommended).** Set `google-client-id` in the ConfigMap to
  the value already in your local `.env`, and create **no** `test-login` secret
  at all. Only addresses listed in the project map can get in. Note that
  `agent-wolf` went Google-only in `c17ba7d` for the same reason (R271).
- **Password fallback.** If you want it, the value must carry both halves:
  `--from-literal=test-login='kai@badcode.tv:<a long random string>'`. `agentd`
  logs a warning at boot when this is on, and it **grants every project in the
  map**. Fine behind an unlisted URL, not fine afterwards.

### D3 ✅ FIXED — `AGENTKIT_MCP_ENV` was missing, so MCP credentials never reached sessions

`AGENTKIT_MCP_ENV` is the comma-separated **allowlist of environment variable
names forwarded from `agentd` into every session container** — the only channel
by which an MCP tool's credential can reach the process that needs it
(`go/cmd/agentd/mcpenv.go`). The manifests never set it, so today the answer is
"nothing is forwarded", silently.

It does not matter for a console-only first deploy. It matters the moment any
project uses an MCP server that needs a key.

**Fix.** Add an optional ConfigMap key and wire it:

```yaml
# 00-namespace-and-config.yaml, under data:
  mcp-env: ""        # e.g. "WOLF_MCP_TOKEN,GMAIL_API_KEY"
```

```yaml
# 20-agent-orange.yaml, in the agentd container's env:
            - name: AGENTKIT_MCP_ENV
              valueFrom: {configMapKeyRef: {name: agent-orange-config, key: mcp-env, optional: true}}
```

Each name you list must also exist as an env var on `agentd` (from a Secret),
or it is logged as missing at boot and forwarded as nothing. Ten names are
**refused** at boot rather than forwarded — `AGENTKIT_JWT_SECRET`,
`ANTHROPIC_API_KEY`, `DATABASE_URL` and friends. That refusal is deliberate.

### D4 🔴 OUTSTANDING — `secrets/gcp-key.json` on this machine is a directory, not a key

```
$ ls -la secrets/gcp-key.json
drwxr-xr-x 2 root root 4096 Jul 28 16:00 .
```

An empty root-owned directory — the classic artefact of a Docker bind mount
pointing at a path that did not exist. `kubectl create secret --from-file` will
not do anything useful with it.

**Fix.** Mint a real key (this writes a credential to disk — treat it
accordingly, and it is already covered by `.gitignore`):

```sh
sudo rm -rf secrets/gcp-key.json
gcloud iam service-accounts keys create secrets/gcp-key.json \
  --iam-account=agent-orange-runtime@webkit-servers.iam.gserviceaccount.com \
  --project=webkit-servers
```

`deploy/gcp/setup.sh` can also emit it (`EMIT_KEY=...`), and is idempotent if
you would rather re-run the whole provisioning step.

> **Longer-term:** Workload Identity would remove this key file entirely by
> binding the Kubernetes service account to `agent-orange-runtime`. Out of scope
> for the first deploy; worth doing before this is anything but a prototype.

---

## 3. Decide these five values

Fill these in before you start. Everything in section 4 refers back to them.

| # | Value | Recommendation | Why |
| --- | --- | --- | --- |
| 1 | **Public hostname** | `orange.badcode.tv` | Matches `forum.badcode.tv`, `n8n.badcode.tv`. Needs a DNS **A record → `104.155.75.230`**. Currently NXDOMAIN. |
| 2 | **Registry** | `europe-west1-docker.pkg.dev/webkit-servers/agent-orange` | Artifact Registry, *not* `gcr.io`. The repo is already provisioned there, the runtime service account already has `artifactregistry.writer` **on that repo specifically**, the session images are already in it, and both publish scripts default to it. Using `gcr.io` (as `forum` does) would mean re-granting IAM for no gain. |
| 3 | **Session base image** | `…/agent-orange/session-core:<tag>` | What a session container runs. Pin the **specific tag**, not `:latest` — Agent Orange records the digest each session launched from, and a moving tag makes that record the only way to tell two environments apart. |
| 4 | **Login mode** | Google (`GOOGLE_CLIENT_ID` from your `.env`) | See D2. Password login grants every project in the map. |
| 5 | **Who can log in** | `kaiyadavenport@gmail.com` → `["*"]` | The project map from D1. Add Jack when he needs access. |

### 🔴 Commit before you publish

Both publish scripts derive their default tag from git, and append **`-dirty`**
when the working tree is not clean — because the bytes in the image are then not
the bytes at that commit. Right now:

```
sha: 7210e33   dirty: YES   branch: wolf-dev-workflow-and-ux
```

So a publish today produces `7210e33-dirty`, which cannot be traced back to
anything. **Commit (or stash) first**, then publish. Pass an explicit tag as
`$1` to either script if you want to override.

---

## 4. The deploy sequence

Nine steps. Steps 1–3 are one-time setup; 4–9 are the deploy itself and are
what you repeat on every subsequent release (see §7 for the short form).

### Step 1 — Create the DNS record

An **A record** for your chosen hostname pointing at the ingress controller:

```
orange.badcode.tv.   A   104.155.75.230
```

Do this first: cert-manager will try to issue a Let's Encrypt certificate the
moment the Ingress lands, and it needs the name to resolve. Verify:

```sh
getent hosts orange.badcode.tv     # must print 104.155.75.230
```

### Step 2 — Apply the four fixes from section 2

D1 and D3 edit `deploy/k8s/00-namespace-and-config.yaml` and
`deploy/k8s/20-agent-orange.yaml`. D2 is a choice, not an edit. D4 is the
`gcloud iam service-accounts keys create` above.

### Step 3 — Edit the two files carrying your specifics

**`deploy/k8s/00-namespace-and-config.yaml`:**

```yaml
  public-base-url: "https://orange.badcode.tv"
  base-image: "europe-west1-docker.pkg.dev/webkit-servers/agent-orange/session-core:<TAG from step 4>"
  gcp-project: "webkit-servers"        # already correct
  gcp-region: "europe-west1"           # already correct
  gcp-ar-repo: "agent-orange"          # already correct
  gcs-bucket: "webkit-servers-agent-orange"   # already correct
  google-client-id: "<the value from your local .env>"
  project-map: |
    {"users":{"kaiyadavenport@gmail.com":["*"]},"projects":{}}
  mcp-env: ""
  embedding-backend: ""    # "" = memory search is keyword + recency only, which works
```

**`deploy/k8s/40-ingress.yaml`** — the hostname appears **twice**, in
`spec.tls[0].hosts` and `spec.rules[0].host`. Both must change, and both must
match `public-base-url` exactly, scheme included. A mismatch produces session
permalinks that 404 rather than an error anyone notices.

### Step 4 — Publish the SESSION base image

This is what a session container runs, and what any project's custom image is
built `FROM`. Distinct from step 5, and confusing the two is the easiest mistake
in this whole document.

```sh
cd /home/kai/projects/badcode/agent-orange
export REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-orange
./deploy/publish-base.sh
```

Pushes `session-base` (the harness alone) and `session-core` (harness plus
`curl`, `jq`, `git`, `ripgrep`, coreutils) at your git tag **and** `latest`. It
prints the tag and the `session-core` digest — **copy the tag into
`base-image` in step 3.**

`PUSH_LATEST=false` leaves `latest` alone; use it if you do not want to move the
shared tag for everyone.

### Step 5 — Publish the SERVICE images

Agent Orange itself. Compose builds these locally; Kubernetes cannot build, so
they must be pushed. **Neither has ever been pushed to this registry.**

```sh
export REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-orange
./deploy/publish-images.sh
```

Builds and pushes:

- `agentd` — `deploy/agentd.Dockerfile`, build context `go/`. Go 1.25 alpine,
  static binary, `ENTRYPOINT /usr/local/bin/agentd`.
- `web` — `deploy/web.Dockerfile`, build context the repo root. Builds `web/`
  **first** (it is a published package as of O12 and `examples/web` consumes its
  emitted `dist/`), then `examples/web`, then serves the bundle from nginx.

Note the tag it prints. It is the `IMAGE_TAG` for step 8.

> A UI change is invisible until this runs again. It is a built bundle, not a
> dev server.

### Step 6 — Create the namespace and the secrets

Secrets are created **by hand**, never by a script whose output might be
committed.

```sh
kubectl create namespace agent-orange

PGPASS="$(openssl rand -hex 24)"

kubectl -n agent-orange create secret generic agent-orange \
  --from-literal=postgres-password="$PGPASS" \
  --from-literal=database-url="postgres://agentorange:${PGPASS}@postgres:5432/agentorange?sslmode=disable" \
  --from-literal=jwt-secret="$(openssl rand -hex 32)" \
  --from-literal=anthropic-api-key='sk-ant-…'

kubectl -n agent-orange create secret generic agent-orange-gcp \
  --from-file=key.json=./secrets/gcp-key.json
```

The password appears twice on purpose: `agentd` takes a whole URL, and splitting
it into parts to reassemble later would be a second place for it to be wrong.

Add `--from-literal=test-login='kai@badcode.tv:<random>'` **only** if you chose
the password fallback in D2. Add `--from-literal=openai-api-key='…'` only if you
set `embedding-backend: openai`.

> **The Postgres trap, borrowed from compose and equally true here:** the volume
> initialises the role and database exactly **once**, on first boot. Changing
> `postgres-password` afterwards re-renders `database-url` but does not touch the
> database that already exists — `agentd` then dies with *"password
> authentication failed"*. Decide the password before the first apply, or delete
> the PersistentVolumeClaim to start over (which destroys every session, memory
> and config event).

### Step 7 — Rehearse with a server-side dry run

Full admission on the real API server. Writes nothing.

```sh
cd deploy/k8s
REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-orange \
IMAGE_TAG=<tag from step 5> \
./apply.sh --dry-run
```

Every line should end `(server dry run)`. An admission error here names exactly
what blocked it — and finding that out now costs nothing.

> **🟡 On the FIRST run this stops after one line.** Verified 2026-09-09:
>
> ```
> namespace/agent-orange created (server dry run)
> Error from server (NotFound): namespaces "agent-orange" not found
> ```
>
> That is not a defect in the manifests. A server dry run writes nothing, so the
> namespace it "created" does not exist for the four files that follow. The
> rehearsal is only fully useful once the namespace is real.
>
> **Do this instead on the first deploy:** create the namespace for real (it is
> an empty container, and step 6 needs it anyway), then rehearse the rest.
>
> ```sh
> kubectl create namespace agent-orange     # real, not a dry run
> REGISTRY=… IMAGE_TAG=… ./apply.sh --dry-run
> ```
>
> To validate the YAML alone without touching the cluster at all, client-side
> validation still checks every field against the server's schema:
>
> ```sh
> for f in 10-postgres.yaml 20-agent-orange.yaml 30-web.yaml 40-ingress.yaml; do
>   sed -e 's#IMAGE_AGENTD#reg/agentd:t#' -e 's#IMAGE_WEB#reg/web:t#' "$f" \
>     | kubectl apply --dry-run=client --validate=strict -f - >/dev/null \
>     && echo "OK  $f"
> done
> ```
>
> All four pass as of 2026-09-09.

### Step 8 — Apply

```sh
REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-orange \
IMAGE_TAG=<tag from step 5> \
./apply.sh
```

`apply.sh` substitutes `IMAGE_AGENTD` and `IMAGE_WEB` into the manifests with
`sed` and pipes each file to `kubectl apply -f -`, in order:

```
00-namespace-and-config.yaml → 10-postgres.yaml → 20-agent-orange.yaml
→ 30-web.yaml → 40-ingress.yaml
```

### Step 9 — Watch it come up

```sh
kubectl -n agent-orange get pods -w
```

Expect: `postgres-0` Running, then `agent-orange-…` with **2/2** containers
Ready, then `web-…`. The `dind` container has a `docker info` readiness probe
with a 10-second initial delay, so 2/2 takes a moment.

---

## 5. Verification

Run these in order. Each answers a different question.

**1. Did `agentd` get a real model credential?** The single most useful boot
line — it tells you whether you are about to spend money or talk to the mock:

```sh
kubectl -n agent-orange logs deploy/agent-orange -c agentd | grep -i "model proxy"
```

`ANTHROPIC_API_KEY unset → MOCK model proxy` means the secret is not arriving.
If you *meant* to run a real agent, fix that. If you did not mean to bill
anything, that line is the proof.

**2. Did it boot at all, and with what?**

```sh
kubectl -n agent-orange logs deploy/agent-orange -c agentd | head -40
```

Look for the project-map line (`project map: N mapped account(s)`), the login
mode line, and the embed-framing line. A `log.Fatal` from D1 or D2 appears here
as the last line before a restart.

**3. Is the product layer actually on?** It is wired **only** when
`DATABASE_URL` is set, and when it is not, the router never routes, schedules
never fire, the core MCP server is not mounted, and **nothing fails at use
time** — it just quietly does nothing. Confirm the absence of:

```
[agentd] no DATABASE_URL — event routing, schedules and request_human_attention are unavailable
```

**4. Did the certificate issue?**

```sh
kubectl -n agent-orange get ingress
kubectl -n agent-orange get certificate
```

`READY=True` on the certificate. If it stays False, `kubectl -n agent-orange
describe certificate` names the ACME failure, and the usual cause is DNS.

**5. Does the front door work?**

```sh
curl -sS https://orange.badcode.tv/health
```

Then open it in a browser and sign in.

**6. Does a session actually run?** Create one from the console and send a
message. **The first one is slow** — the daemon pulls the session base image on
first use, not at startup. That is the pull, not a hang. Watch it:

```sh
kubectl -n agent-orange logs deploy/agent-orange -c dind --tail=50
```

**7. What did it cost the node?**

```sh
kubectl describe node | sed -n '/Allocated resources/,/Events/p'
```

Expect CPU requests to move from ~58% to ~80%. Remember that session containers
do **not** appear here at all.

---

## 6. Rollback and teardown

**Roll back to a previous image** — the manifests carry an explicit tag, so
re-apply the old one:

```sh
IMAGE_TAG=<previous tag> ./apply.sh
```

`kubectl -n agent-orange rollout undo deploy/agent-orange` also works, but note
the Deployment strategy is `Recreate`: there is a gap with no `agentd` running.

**Stop it without losing data** — scale to zero. The PersistentVolumeClaims and
the Postgres StatefulSet survive:

```sh
kubectl -n agent-orange scale deploy/agent-orange --replicas=0
```

**🔴 Full teardown, destroying everything.** Deleting the namespace deletes the
PVCs, and the storage class reclaim policy is `Delete`. Every session, memory,
worker, dataset and config event goes with it, irrecoverably. There are no
backups.

```sh
kubectl delete namespace agent-orange
```

---

## 7. Redeploying a change

The short loop, once the one-time setup is done:

```sh
git commit …                                    # avoid a -dirty tag
export REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-orange
./deploy/publish-images.sh                      # note the tag it prints
cd deploy/k8s && IMAGE_TAG=<that tag> ./apply.sh
```

You only re-run `publish-base.sh` when the **session** image changes —
`sandbox/` or `installations/core/`. Changing it means editing `base-image` in
the ConfigMap too, and existing sessions keep the image they were launched from.

Schema migrations need no step: `agentd` runs `agentdb`'s own migrations at
boot (currently through `046_project_briefing`). This is why the manifests being
a month old costs nothing here.

---

## 8. Traps, in the order they will bite

1. **Boot fatal from D1 or D2.** `CrashLoopBackOff` with one clear log line you
   have to know to look for. Both are fixed in section 2.
2. **The `-dirty` tag.** Publishing from a dirty tree produces an image that
   cannot be traced to a commit. §3.
3. **The first conversation is slow.** The session base is pulled on first
   session, not at startup.
4. **SSE needs buffering off.** Already set in the Ingress annotations
   (`proxy-buffering: "off"`, read/send timeouts 3600s). Without them the chat
   appears to hang and then delivers every message at once.
5. **Sessions hold a container and a host port.** The archive loop
   (`go/cmd/agentd/gc.go`) snapshots and releases a session idle for
   `AGENTKIT_SESSION_IDLE_TIMEOUT` (default 30m) — reclamation, not deletion;
   the next message restores it. The port pool is 100 and that is the hard
   ceiling on *concurrent* sessions. At zero free, every new session fails with
   `host port pool is exhausted`.

Beyond five, but worth knowing:

- **The model proxy is not exposed, and must stay that way.**
  `docs/19-embedding.md` H6 records that `/agent-proxy/` is mounted outside the
  auth middleware and stamps the real Anthropic key onto upstream requests —
  anyone who can reach `agentd`'s port directly can spend the key. This
  deployment is safe *by accident of routing*: `deploy/web.nginx.conf` proxies
  only `/agent/`, `/auth/`, `/dev/`, `/embed/` and `/internal/embed-csp`, and
  the Ingress points at `web`, never at `dind:8099`. **Never add a second
  Ingress rule pointing at the `dind` Service.**
- **`/dev/token` is off.** It is registered only when *no* login mode is set
  (`main.go:561`). With Google login on, it does not exist. With **neither**
  login mode set, `agentd` falls back to dev-open with **no authentication at
  all** — never apply that to a public host.
- **Reap intervals are defaulted, not configured.** The snapshot TTL reaper (6h)
  and the dataset version reaper (6h, keep 30) run on defaults because the
  manifests do not set their env vars. Fine; noted so it is not a surprise.
- **`AGENTKIT_PORT_RANGE_START`/`_END` are unset**, so the pool is 30001–30100.
  Set both or neither; nonsense is refused at boot.

---

## Appendix A — the one-script version

`forum/deploy/manual_deploy.sh` is a single file with `build` and `deploy`
functions, invoked as `./manual_deploy.sh build` / `./manual_deploy.sh deploy`.
Agent Orange already has both halves as separate scripts, which is arguably
better — publishing the **session base** and publishing the **services** are
genuinely different acts with different cadences. If you want the single
entry point anyway:

```bash
#!/usr/bin/env bash
# deploy/k8s/manual_deploy.sh — build → push → apply, the forum shape.
#
#   ./deploy/k8s/manual_deploy.sh build     # services only
#   ./deploy/k8s/manual_deploy.sh base      # the session base image
#   ./deploy/k8s/manual_deploy.sh deploy    # kubectl apply at the current tag
#   ./deploy/k8s/manual_deploy.sh all       # build + deploy
set -euo pipefail
IFS=$'\n\t'

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

export REGISTRY="${REGISTRY:-europe-west1-docker.pkg.dev/webkit-servers/agent-orange}"

tag() {
  local sha dirty=''
  sha="$(git rev-parse --short HEAD)"
  [ -n "$(git status --porcelain)" ] && dirty='-dirty'
  printf '%s%s' "$sha" "$dirty"
}
export IMAGE_TAG="${IMAGE_TAG:-$(tag)}"

base()   { ./deploy/publish-base.sh "$IMAGE_TAG"; }
build()  { ./deploy/publish-images.sh "$IMAGE_TAG"; }
deploy() { (cd deploy/k8s && ./apply.sh); }
all()    { build; deploy; }

eval "$@"
```

Two differences from forum's, both deliberate: the tag is the commit rather
than the full SHA and carries `-dirty` when it would otherwise lie, and the
manifests are substituted by `apply.sh`'s `sed` rather than `envsubst` (the
manifests use `IMAGE_AGENTD` / `IMAGE_WEB` placeholders, not `$VAR`, so a stray
`$` in a YAML comment cannot break the render).

---

## Appendix B — resource budget

Requests, which is what the scheduler actually reserves:

| Container | CPU | Memory | Limit |
| --- | --- | --- | --- |
| `dind` | 200m | 512Mi | mem 4Gi; **no CPU limit on purpose** — image pulls are bursty, and a limit makes the first session of the day look like a hang |
| `agentd` | 100m | 256Mi | mem 1Gi |
| `postgres` | 100m | 512Mi | mem 2Gi |
| `web` | 20m | 64Mi | mem 256Mi |
| **Total** | **420m** | **~1.3Gi** | |

Node: 2 vCPU (~1930m allocatable), 16 GiB. Already committed: 1137m CPU, 2.76
GiB memory. **After this deploy: ~1557m CPU requested (~81%), ~4 GiB memory
(~28%).** Session containers are additional and invisible to the scheduler.

Volumes: `dind-data` 30Gi (the daemon's image store), `agentd-data` 5Gi,
plus the Postgres claim. All `standard-rwo`, reclaim policy `Delete`.

---

## Open items after this deploy

- **Backups.** Nothing snapshots Postgres. First thing to fix if this stops
  being a prototype.
- **Workload Identity**, replacing the mounted service-account key (D4).
- **Agent Wolf** has no manifests. Separate work.
- **A dedicated node pool**, if the CPU contention accepted in §0 turns out to
  matter after all. It is the same manifests plus a node selector and a taint.
