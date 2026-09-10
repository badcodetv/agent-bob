# Agent Orange → Agent Bob — Rename Playbook

> **HOW TO USE THIS:** Phases run in order and the order is load-bearing.
> Phase 0 (worktrees) must finish before anything else, or every later step is
> multiplied by 35. Do the whole thing **before the first Kubernetes deploy** —
> see "Why now" below. Each phase ends with a verification gate; do not start
> the next phase with a red gate.

Status: **PLANNED — not started. On hold until the `git-projection` PR merges into `main`** (coordinated with the session landing it, 2026-09-10); the rename runs against the settled `main`, never this branch.
Written 2026-09-09. Inventory verified against the live repos, the live GCP
project and the live cluster on the same day.

Relates: `design/2026-09-09-kubernetes-deployment-playbook.md` (the deploy this
must precede), `CLAUDE.md` (the operating guide, itself full of the old name),
`MIGRATION.md`, `deploy/gcp/setup.sh` (provisions the GCP side; parameterised,
so it re-provisions under the new names without editing).

## Why now

Nothing is deployed yet. That makes three of the most expensive things in a
rename **free**:

- the Kubernetes namespace does not exist, so there is nothing to migrate;
- the Postgres role and database do not exist, so `agentorange` → `agentbob` is
  a config edit rather than a dump-and-restore;
- the GCS bucket holds **44 bytes**, so the one Google resource that genuinely
  cannot be renamed can simply be recreated.

Every week this waits, those get more expensive. After the first real deploy
they become a migration.

---

## 0. Target state — the definition of done

| # | Statement | Verified by |
| --- | --- | --- |
| 1 | The engine repo is `github.com/badcodetv/agent-bob` | `git remote -v` |
| 2 | The Wolf repo is `github.com/badcodetv/agent-wolf` — **moved out of `binocarlos/`** | `git remote -v` |
| 3 | The Go module path is `github.com/badcodetv/agent-bob` | `head -1 go/go.mod` |
| 4 | **Zero** git worktrees in either repo | `git worktree list \| wc -l` returns 1, twice |
| 5 | No string `agent-orange`, `agentorange` or `Agent Orange` in either repo | `grep -rI` returns nothing |
| 6 | GCP: Artifact Registry repo `agent-bob`, bucket `webkit-servers-agent-bob`, service account `agent-bob-runtime` | `gcloud … list` |
| 7 | Wolf reaches Bob by the same mechanism it reached Orange | `./stack wolf up` comes up |
| 8 | **`agentkit` is untouched** — the module's internal name, the `AGENTKIT_` env prefix and the `@agentkit/*` npm scope all survive verbatim | `grep -c agentkit` unchanged |
| 9 | All three test suites green | §10 |

### 🔴 The one thing that must NOT change

`agentkit` stays. Decided 2026-09-09. It is 1139 occurrences across 186 files,
including the **`AGENTKIT_` environment-variable prefix (765 occurrences)** and
the **`@agentkit/*` npm scope**. Changing the prefix would invalidate every
`.env`, the compose file, the Kubernetes ConfigMap and any deployment anyone
has, and would buy nothing a user can see. It is the runtime layer's name and
it is allowed to differ from the product's.

If a later reader finds this confusing, the answer is a paragraph in
`CLAUDE.md`, not a rename.

---

## 1. Inventory — verified 2026-09-09

### In the Orange repo

| Pattern | Files | Hits | Nature |
| --- | --- | --- | --- |
| `agent-orange` | 337 | 882 | mechanical |
| `github.com/binocarlos/badcode-agent-orange` | — | 564 | the Go module path, mechanical |
| `Agent Orange` (prose) | 48 | 108 | mechanical |
| `agentorange` (Postgres role/db) | 14 | 47 | mechanical |
| bare `orange`, not part of the above | — | **1629** | **needs eyes — see §3 class C** |
| `agentkit` | 186 | 1139 | **DO NOT TOUCH** |

### Git worktrees — 35 to remove

| Repo | Extra worktrees | Clean and merged | Need care |
| --- | --- | --- | --- |
| Orange | 12 | 10 | **2** |
| Wolf | 23 | **23** | 0 |

The two that need care, both in Orange:

- **`agent-orange-phase-a`** — branch `feat/phase-a-board`, **1 uncommitted
  file** and **2 commits not in `main`**. Last touched 2026-06-25. Real work.
- **`.claude/worktrees/agent-a4f9f16dcf7d0ab40`** — clean, but **1 commit not in
  `main`**. An agent's working copy.

All 23 Wolf worktrees are clean, zero commits ahead of `main`, and safe to
remove without inspection.

### External resources

| Resource | Current | Notes |
| --- | --- | --- |
| GitHub — engine | `badcodetv/agent-orange` | rename in place; GitHub keeps a redirect |
| GitHub — Wolf | `binocarlos/agent-wolf` | **transfer to `badcodetv`**, then keep the name |
| Artifact Registry | `agent-orange`, `europe-west1`, **874 MB** | cannot rename; create `agent-bob`, republish, delete old |
| GCS bucket | `webkit-servers-agent-orange`, **44 bytes** | cannot rename; **effectively empty, just recreate** |
| Service account | `agent-orange-runtime@webkit-servers…` | cannot rename; create `agent-bob-runtime`, regrant, delete old |
| SA keys | one, **`SYSTEM_MANAGED`** | there is **no** user-managed key — which is why `secrets/gcp-key.json` is an empty directory |
| Project-level IAM | **none mentions orange** | grants are resource-level only: `objectAdmin` on the bucket, `artifactregistry.writer` on the repo. Two bindings, that is all. |
| Kubernetes | nothing deployed | free |
| DNS | `orange.badcode.tv` never created | free — create `bob.badcode.tv` instead |
| Local folder | `/home/kai/projects/badcode/agent-orange` | rename breaks 35 worktree pointers; `git worktree repair` fixes it |

---

## 2. Naming decisions

| Thing | From | To | Note |
| --- | --- | --- | --- |
| Product name | Agent Orange | **Agent Bob** | |
| GitHub repo | `badcodetv/agent-orange` | `badcodetv/agent-bob` | |
| Go module | `github.com/binocarlos/badcode-agent-orange` | **`github.com/badcodetv/agent-bob`** | Two fixes in one: the org was already wrong (module said `binocarlos`, remote said `badcodetv`), and the `badcode-` prefix is redundant inside a `badcodetv` org. Matching the repo path exactly is what Go expects. |
| Local folder | `…/badcode/agent-orange` | `…/badcode/agent-bob` | |
| AR repo | `agent-orange` | `agent-bob` | |
| GCS bucket | `webkit-servers-agent-orange` | `webkit-servers-agent-bob` | |
| Service account | `agent-orange-runtime` | `agent-bob-runtime` | |
| K8s namespace | `agent-orange` | `agent-bob` | |
| Postgres role + db | `agentorange` | `agentbob` | |
| Hostname | `orange.badcode.tv` | `bob.badcode.tv` | A → `104.155.75.230` |
| Compose container names | `agent-orange-dind-1` | `agent-bob-dind-1` | derived from the folder name; Wolf hardcodes these |
| Compose network | `agent-orange_default` | `agent-bob_default` | same |
| Session images | `session-base`, `session-core`, `session-wolf` | unchanged | never carried the product name |
| K8s Service `dind` | `dind` | unchanged | tied to `deploy/web.nginx.conf`; renaming it is a separate, unrelated cleanup |
| Everything `agentkit` | — | unchanged | §0 |

---

## 3. Four classes of hit, and why this is not one `sed`

**Class A — compound tokens. Mechanical, safe, ~1000 hits.**
`agent-orange` → `agent-bob`, `agentorange` → `agentbob`, `Agent Orange` →
`Agent Bob`, `AGENT_ORANGE` → `AGENT_BOB` if any. Order matters: do the longest
first (`badcode-agent-orange` before `agent-orange`) or you get `badcode-agent-bob`
where you wanted `agent-bob`.

**Class B — the Go module path. Mechanical but distinct, 564 hits.**
`github.com/binocarlos/badcode-agent-orange` → `github.com/badcodetv/agent-bob`.
One `sed` across `go/` plus `go.mod`, then `go mod tidy`. Must be its own step
because the target is not derived from the class-A rule.

**Class C — bare `orange`. 1629 hits. NOT a `sed`.**
These need review, and they fall into four kinds:

- **Example hostnames in tests** — `https://orange.example.com` in
  `attention_test.go`, `permalink_test.go`. Cosmetic; rename freely.
- **Test fixture passwords** — `orange-e2e` in `.env.example` and the e2e rig.
  Rename freely.
- **Arbitrary test fixture names** — e.g. `agentdb.MCPServers{"orange": …}` in
  `compose_test.go`. **These are not the product name.** They are placeholder
  server names in a table test.
  > **Checked, because it mattered:** the core MCP server's real registered name
  > is **`"core"`** (`go/cmd/agentd/mcpserver.go:74`, `coreMCPServerName = "core"`),
  > not `"orange"`. If it had been `"orange"` this rename would have included a
  > **database migration** — the name lands in composed job configs and stored
  > project MCP config. It does not. Confirm this is still true before starting.
- **Prose in docs and design records** — the bulk of the 1629. See the decision
  below.

**Class C½ — the git projection's wire identifiers. NOT cosmetic.**
*Added 2026-09-10, after the git projection epic landed.* Three identifiers
written into every projected commit are also **read back** by the importer to
decide whether a commit is the renderer's own:

| Identifier | Where it is written | Where it is read |
| --- | --- | --- |
| Trailer keys `Orange-Project`, `Orange-Seq`, `Orange-Event`, `Orange-Action`, `Orange-Actor-Worker`, `Orange-Actor-Session` | `go/cmd/agentd/gitprojection.go:1028-1037` (`commitTrailers`) | `go/cmd/agentd/gitimport.go:1151` — `%(trailers:key=Orange-Seq,…)` and `Orange-Event` |
| Author name `Agent Orange` | `go/gitproj/repo.go:43` (`AuthorName`) | — |
| Author email `orange@agentorange.local` | `go/gitproj/repo.go:44` (`AuthorEmail`) | `gitimport.go` — `%ae` in the same `git log` format |
| The same email, pinned in the browser suite | `e2e/helpers/gitprojection.ts:63` (`BOT_AUTHOR_EMAIL`) | the e2e specs' "is this the bot's commit" assertions; line 176 is the DI4 forgery test |

Class A's `sed` **will** rewrite the name and the email (`Agent Orange`,
`agentorange`) but **will not** rewrite the trailer keys (bare `Orange-`). Half
of it changes and half does not, which is the worst outcome.

The danger is any repository that **already holds projected commits**: after a
rename, the importer no longer recognises the renderer's old commits as its
own, treats them as **human edits**, and applies them again as new config
events. The design's own warning (DI4) is that `Orange-Seq` decides whose
commit a commit is.

**Resolved 2026-09-10: rename outright, no dual-prefix reading.** The session
that built the git projection confirmed that **no real projected repository
exists**. Every run so far used a throwaway bare repo (`t.TempDir()` in the Go
tests, and a bare repo inside the e2e `agentd` container), nothing has been
pointed at GitHub or any other real remote, and no project has `git_remote` set.
So no history anywhere that matters carries `Orange-*` trailers.

Rename in **one change**, all four places together, or the importer will read
the renderer's own commits as human edits:

1. the writer: `commitTrailers` in `gitprojection.go` → `Bob-*` keys;
2. the identity: `gitproj.AuthorName` / `AuthorEmail` → `Agent Bob` /
   `bob@agentbob.local`;
3. the importer's "is this ours?" check in `gitimport.go`: both
   `%(trailers:key=…)` fields **and** the author-email comparison;
4. the e2e helper `e2e/helpers/gitprojection.ts` (`BOT_AUTHOR_EMAIL` and the
   trailer names its specs assert).

Keep the DI4 rule while editing: trailers are parsed with **git's own parser
over the last paragraph**, never by grepping the message.

### 🔴 Ordering rule: the rename lands before any project gets a real `git_remote`

This is the cheap path, and it is only cheap in this order. **Until the rename
has merged, do not set `git_remote` on any project, and do not point the git
projection at GitHub or any other real remote.** If that happens first, the
outright rename is off the table: the importer must then read **both**
prefixes and write only the new one. That is new code with its own tests, not
a rename, and it belongs in its own ticket.

**Also rename one file:** `deploy/k8s/20-agent-orange.yaml` → `20-agent-bob.yaml`.
It is named in `deploy/k8s/apply.sh`'s file list and in a comment at
`gitprojection.go:357`, so all three move together.

**Class D — external resources.** Phases 2 and 3. Nothing to `sed`.

### 🟡 Decision needed: do dated design documents get rewritten?

`design/` holds dated records — plans, audits, discovered-issues logs — that
describe what happened on a particular day under the name Agent Orange.

- **Rewrite them (recommended).** They are working documents, not legal records.
  A reader in three months hitting "Agent Orange" in a 2026-08 plan with no
  explanation is worse served than one reading a consistent name. Pair it with
  **one** provenance line in `README.md` and `CLAUDE.md`: *"Formerly Agent
  Orange; renamed 2026-09-09."*
- **Leave them.** Preserves testimony exactly. Costs you a permanently
  half-renamed repo, which is the worst of both.

Recommendation: **rewrite, plus the provenance line.** Half-renamed is worse
than either extreme.

---

## Phase 0 — Worktree cleanup

**Do this first.** 35 worktrees means every later `sed` risks touching a stale
copy, and the Phase 5 folder rename breaks all of their `.git` pointers at once.

**~20 minutes.**

### 0.1 Rescue the two that hold work

```sh
cd /home/kai/projects/badcode/agent-orange-phase-a
git status                      # LOOK at the one uncommitted file first
git log --oneline main..HEAD    # the 2 commits
```

Decide per branch: merge, push the branch to keep it on the remote, or discard
deliberately. **Do not delete this worktree until you have chosen.** Same for
`.claude/worktrees/agent-a4f9f16dcf7d0ab40` (1 commit ahead).

### 0.2 Remove the clean ones

Orange — 10 worktrees, all clean and merged:

```sh
cd /home/kai/projects/badcode/agent-orange
for w in wave2/O11 wave2/O2 wave2/O3 wave2/O4 wave4/O5 wave4/O6a wave4/O8 \
         wave5/O6b wave8/o12 agent-orange-readiness; do
  git worktree remove "/home/kai/projects/badcode/$w"
done
git worktree prune
```

Wolf — all 23, verified clean and zero commits ahead:

```sh
cd /home/kai/projects/badcode/agent-wolf
git worktree list --porcelain | awk '/^worktree /{print $2}' \
  | grep -v '^/home/kai/projects/badcode/agent-wolf$' \
  | while read -r w; do git worktree remove "$w"; done
git worktree prune
```

`git worktree remove` **refuses** a dirty worktree rather than destroying work.
That refusal is your safety net — if one refuses, stop and look rather than
reaching for `--force`.

### 0.3 Delete the merged branches too

Worktree removal leaves the branch. Once a branch is merged and its worktree is
gone, delete it — `git branch -d` (lowercase, refuses unmerged) not `-D`.

### ✅ Gate 0

```sh
git -C /home/kai/projects/badcode/agent-orange worktree list | wc -l   # 1
git -C /home/kai/projects/badcode/agent-wolf   worktree list | wc -l   # 1
```

---

## Phase 1 — Rename inside the engine repo

**~1.5 hours.** Land the in-flight charter work first, or stash it: a global
rewrite over a tree with nine files mid-edit is how conflicts happen.

### 1.1 Branch

```sh
cd /home/kai/projects/badcode/agent-orange
git checkout -b rename/orange-to-bob
```

### 1.2 Class B — the module path, first and alone

```sh
grep -rIl 'binocarlos/badcode-agent-orange' go/ \
  | xargs sed -i 's#github.com/binocarlos/badcode-agent-orange#github.com/badcodetv/agent-bob#g'
cd go && go mod tidy && go build ./... && cd ..
```

Build must be green before continuing. If it is not, nothing later will be
interpretable.

### 1.3 Class A — compound tokens, longest first

```sh
files=$(grep -rIl -e 'agent-orange' -e 'agentorange' -e 'Agent Orange' . \
        --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=dist \
        --exclude-dir=migration-reference)
echo "$files" | xargs sed -i \
  -e 's/badcode-agent-orange/agent-bob/g' \
  -e 's/Agent Orange/Agent Bob/g' \
  -e 's/agent-orange/agent-bob/g' \
  -e 's/agentorange/agentbob/g'
```

`migration-reference/` is excluded deliberately — it is a frozen copy of the
Platinum host pipeline, reference only, never built or imported.

### 1.4 Class C — bare `orange`, reviewed not swept

```sh
grep -rIn 'orange' . --exclude-dir=node_modules --exclude-dir=.git \
  --exclude-dir=dist --exclude-dir=migration-reference -i \
  | grep -iv 'agent-bob' > /tmp/bare-orange.txt
wc -l /tmp/bare-orange.txt
```

Work that list by file, applying §3 class C. Re-confirm the one that matters:

```sh
grep -n 'coreMCPServerName' go/cmd/agentd/mcpserver.go   # must still say "core"
```

### 1.5 Rename files and directories carrying the name

```sh
git ls-files | grep -i orange     # expect: the two design docs, and check for more
```

Rename `design/2026-09-09-kubernetes-deployment-playbook.md`'s references, and
this file, last — they are the two documents describing the rename.

### 1.6 The provenance line

Add to `README.md` and to `CLAUDE.md`'s opening:

> Agent Bob was called **Agent Orange** until 2026-09-09. The engine's internal
> name is still `agentkit` — that is deliberate and is not a leftover; see
> `design/2026-09-09-orange-to-bob-rename.md` §0.

### ✅ Gate 1

```sh
cd go && go build ./... && go vet ./... && go test ./...
cd ../sandbox && npm ci && npm run typecheck && npm test
cd ../web && npm ci && npm run typecheck && npm test && ./scripts/verify-package.sh
cd ../examples/web && yarn install --frozen-lockfile && yarn build
```

`web/` must be built before `examples/web` — the latter consumes the former's
emitted `dist/`. Then:

```sh
grep -rIn -e 'agent-orange' -e 'agentorange' -e 'Agent Orange' . \
  --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=migration-reference
# expect: no output
grep -rIc 'agentkit' --include='*.go' go/ | wc -l    # unchanged from before
grep -rn '"Orange-' go/ --include='*.go'    # expect: no output (Class C½)
grep -n 'AuthorName\|AuthorEmail' go/gitproj/repo.go   # Agent Bob / bob@agentbob.local
```

**After `sandbox/` npm work:** `git checkout sandbox/yarn.lock` — npm ≥7 syncs
the stale yarn lockfile and dirties the tree.

---

## Phase 2 — Google Cloud

**~45 minutes.** Create new, verify, then delete old. Never the other way round.

`deploy/gcp/setup.sh` is parameterised and idempotent, so it provisions the new
names without editing:

```sh
PROJECT=webkit-servers REGION=europe-west1 \
GCS_BUCKET=webkit-servers-agent-bob GCP_AR_REPO=agent-bob \
SA_NAME=agent-bob-runtime \
./deploy/gcp/setup.sh
```

That creates the bucket, the Artifact Registry repo, the service account, and
the two resource-level grants (`roles/storage.objectAdmin` on the bucket,
`roles/artifactregistry.writer` on the repo) — the only two bindings that exist
today.

### 2.1 The key file (this also closes the deploy playbook's D4)

There is **no user-managed key** on the old service account, which is why
`secrets/gcp-key.json` is an empty root-owned directory. Clear it and mint a
real one:

```sh
sudo rm -rf secrets/gcp-key.json
gcloud iam service-accounts keys create secrets/gcp-key.json \
  --iam-account=agent-bob-runtime@webkit-servers.iam.gserviceaccount.com \
  --project=webkit-servers
```

This writes a credential to disk. It is gitignored; keep it that way.

### 2.2 Republish images under the new repo

```sh
export REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-bob
./deploy/publish-base.sh        # session-base + session-core
./deploy/publish-images.sh      # agentd + web
```

The old repo's 874 MB is rebuildable output, not source. Nothing is lost.

### 2.3 Delete the old resources — only after §2.2 succeeds

```sh
gcloud artifacts repositories delete agent-orange --location=europe-west1 --project=webkit-servers
gcloud storage rm -r gs://webkit-servers-agent-orange
gcloud iam service-accounts delete agent-orange-runtime@webkit-servers.iam.gserviceaccount.com --project=webkit-servers
```

> 🔴 **Check Wolf first.** Wolf's `installations/wolf/Dockerfile` builds `FROM`
> an image in the old repo (`agent-orange-core`). Deleting the old repo before
> Phase 4 breaks Wolf's build. Either do §2.3 after Phase 4, or accept a broken
> Wolf build in between.

### ✅ Gate 2

```sh
gcloud artifacts repositories list --project=webkit-servers | grep -i 'orange\|bob'
gcloud storage ls | grep -i 'orange\|bob'
gcloud iam service-accounts list --project=webkit-servers | grep -i 'orange\|bob'
```

Each should show `bob` and no `orange`.

---

## Phase 3 — GitHub

**~20 minutes.**

1. **Rename the engine repo** — `badcodetv/agent-orange` → `badcodetv/agent-bob`
   in repo settings. GitHub leaves a redirect, so existing clones keep working
   until they are updated. Update anyway:
   ```sh
   git remote set-url origin git@github.com:badcodetv/agent-bob.git
   ```
2. **Transfer Wolf** — `binocarlos/agent-wolf` → `badcodetv/agent-wolf`. A
   transfer, not a rename; it needs admin on both sides and the name stays.
   ```sh
   git -C ../agent-wolf remote set-url origin git@github.com:badcodetv/agent-wolf.git
   ```
   Push Wolf's **3 unpushed commits** on `dev-workflow-and-ux` before
   transferring.
3. Push the rename branch, open a PR, merge.

### ✅ Gate 3

`git remote -v` in both repos names `badcodetv`; `git fetch` succeeds in both.

---

## Phase 4 — Agent Wolf

**~1 hour.** Wolf is a separate repository at
`/home/kai/projects/badcode/agent-wolf` (`api/`, `web/`, `docs/`, `e2e/`,
`installations/`, its own `docker-compose.yml`). It **embeds** Bob as a service;
it is not part of this repo and never was. Six files reference the old name:

| File | What it holds |
| --- | --- |
| `docker-compose.yml:22` | container name `agent-orange-dind-1` |
| `docker-compose.yml:119` | network `agent-orange_default` |
| `installations/wolf/Dockerfile:22` | base image `agent-orange-core` |
| `package.json:5` | a reference in metadata |
| `api/src/mcp/__fixtures__/README.md:11` | `agent-orange-postgres-1` |
| `web/src/import-boundary.test.ts:123` | `agent-orange-mention` — **read this one before editing**; a boundary test asserting on a name behaves differently from a name in config |

The container and network names are **derived from the Orange folder name**, so
they only become correct after Phase 5. Do Phase 4's edits, then Phase 5, then
verify together.

```sh
cd /home/kai/projects/badcode/agent-wolf
git checkout -b rename/orange-to-bob
grep -rIl 'agent-orange\|agentorange\|Agent Orange' . --exclude-dir=node_modules --exclude-dir=.git \
  | xargs sed -i -e 's/Agent Orange/Agent Bob/g' -e 's/agent-orange/agent-bob/g' -e 's/agentorange/agentbob/g'
```

### ✅ Gate 4

```sh
cd /home/kai/projects/badcode/agent-wolf
yarn install --frozen-lockfile && yarn typecheck && yarn test
```

---

## Phase 5 — The local folder, and worktree repair

**~30 minutes.** Last, because everything above assumes the current path.

```sh
cd /home/kai/projects/badcode
mv agent-orange agent-bob
```

This breaks the `.git` pointer of **every remaining worktree**. After Phase 0
there should be at most the two you rescued:

```sh
cd /home/kai/projects/badcode/agent-bob
git worktree repair
git worktree list          # paths resolve, no errors
```

`git worktree repair` exists for exactly this and fixes pointers in both
directions. Run it in Wolf too if any worktree survived there.

Then update anything holding the old absolute path: shell aliases, editor
workspaces, `WOLF_REPO` if you set it, and the Claude Code project directory.

> **Note:** the Claude Code memory and settings directory is keyed on the
> project path (`-home-kai-projects-badcode-agent-orange`). After the folder
> rename, a new session opens against a new key and will not see the existing
> memories until that directory is renamed to match.

### ✅ Gate 5

```sh
cd /home/kai/projects/badcode/agent-bob
git status && git worktree list | wc -l    # clean; 1
./stack start mock                          # the stack comes up, free
```

---

## 10. Final verification

Run all of it, in this order.

```sh
# 1. Nothing named Orange survives in either repo
for r in agent-bob agent-wolf; do
  grep -rIn -e 'agent-orange' -e 'agentorange' -e 'Agent Orange' \
    /home/kai/projects/badcode/$r --exclude-dir=node_modules --exclude-dir=.git \
    --exclude-dir=migration-reference
done

# 2. agentkit is intact
grep -rIo 'AGENTKIT_' /home/kai/projects/badcode/agent-bob --include='*.go' | wc -l

# 3. Zero worktrees, both repos
git -C /home/kai/projects/badcode/agent-bob   worktree list | wc -l
git -C /home/kai/projects/badcode/agent-wolf  worktree list | wc -l

# 4. All three suites
cd /home/kai/projects/badcode/agent-bob/go && go build ./... && go vet ./... && go test ./...
cd ../sandbox && npm ci && npm run typecheck && npm test
cd ../web && npm ci && npm run typecheck && npm test && ./scripts/verify-package.sh

# 5. The stack, offline and free
cd /home/kai/projects/badcode/agent-bob && ./stack start mock

# 6. Wolf against Bob
./stack wolf up
```

Then, and only then, run
`design/2026-09-09-kubernetes-deployment-playbook.md` — which itself needs its
names updated by Phase 1 (namespace `agent-bob`, hostname `bob.badcode.tv`,
registry `.../agent-bob`, Postgres role `agentbob`).

---

## 11. Rollback

Cheap up to the end of Phase 1 — it is a branch; delete it.

After Phase 2 the GCP resources exist under both names, which is a safe
intermediate state: nothing is destroyed until §2.3.

After Phase 3, GitHub's redirect means the old URLs still resolve; a rename can
be reversed in settings.

Phase 5 reverses with `mv` plus another `git worktree repair`.

**The one-way door is §2.3.** Deleting the old bucket, repo and service account
is irreversible. Do it last, after Gate 4, once Wolf builds against the new
registry.

---

## 12. Order of operations, in one screen

```
Phase 0  worktrees          35 → 0        (2 need rescuing first)      ~20m
Phase 1  engine repo        sed + review + 3 test suites               ~1.5h
Phase 2  GCP                create new, republish, DELETE LAST         ~45m
Phase 3  GitHub             rename Bob, TRANSFER Wolf to badcodetv     ~20m
Phase 4  Agent Wolf         6 files, its own repo                      ~1h
Phase 5  local folder       mv + git worktree repair                   ~30m
──────────────────────────────────────────────────────────────────────────
                                                        total  ~4.5–5 hours
```

Then the Kubernetes deploy, under the new names, with nothing to migrate.
