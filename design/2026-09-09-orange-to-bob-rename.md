# Agent Orange → Agent Bob — Rename Playbook

> **HOW TO USE THIS:** Phases run in order and the order is load-bearing.
> Phase 0 (worktrees) must finish before anything else, or every later step is
> multiplied by 35. Do the whole thing **before the first Kubernetes deploy** —
> see "Why now" below. Each phase ends with a verification gate; do not start
> the next phase with a red gate.

Status (superseded for execution by `design/2026-09-10-bob-migration-master-plan.md`): **Phase 0 done (two items wait on Kai); Phases 1–5 not started.**
PR #1 merged into `main` as `5f343cc` on 2026-09-10, so the hold is lifted.
**Re-verified against `main` `5f343cc` on 2026-09-10**; see "What changed
since 2026-09-09" below. Run it against `main`, never a feature branch.
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

## What changed since 2026-09-09

Re-verified 2026-09-10 against `main` `5f343cc`, after the org-chart and
git-projection work merged. The plan's shape still holds. Six things changed,
and each is corrected in place below:

1. 🔴 **Two values must NOT be renamed.** They are protocol constants, and
   Class A's `sed` would change both without anyone noticing. See §3 "Class P".
   The worst is `configchanged.go:80`: changing it makes **every past config
   change fire `config.changed` again**.
2. 🔴 **Agent Wolf is far bigger than the plan said.** Not 6 files but
   **154 references in 56 files**, plus a whole internal vocabulary
   (`OrangeClient`, `api/src/orange/`, `ORANGE_*` env vars) that Orange's
   `./stack` passes values into. Phase 4 is rewritten.
3. 🟡 **More stored values carry the name**: the git projection's folder
   `orange`, a browser storage key, four report schema ids, and the Postgres
   role. All are rename-together items in Class P.
4. ✅ **Phase 0 is done**, apart from two decisions waiting on Kai (§1).
5. 🟡 **Counts refreshed** (§1). The code grew; the method is unchanged.
6. ✅ **Every file:line reference still resolves on `main`.** The git
   projection lines did not move. GCP, the cluster and DNS are unchanged.

Estimate: ~5 hours → **~6 hours**, almost all of the increase in Wolf.

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

## 1. Inventory — re-verified 2026-09-10 on `main` `5f343cc`

### In the Orange repo

Counted with `git grep` over tracked files on `main`. The 2026-09-09 counts
used `grep -r` over the working tree, which also read untracked files, so the
bare-`orange` figure dropped because of the method, not because anything was
removed.

| Pattern | Files | Hits | Nature |
| --- | --- | --- | --- |
| `agent-orange` | 363 | 885 | mechanical, **except Class P** |
| `github.com/binocarlos/badcode-agent-orange` | 290 | 622 | the Go module path, mechanical |
| `Agent Orange` (prose) | 53 | 112 | mechanical |
| `agentorange` (Postgres role/db, bot email) | 15 | 47 | mechanical, see Class P |
| bare `orange`, not part of the above | — | ~1200 | **needs eyes — see §3 class C** |
| `AGENTKIT_` | 141 | 768 | **DO NOT TOUCH** |

### Git worktrees — the Phase 0 result (2026-09-10)

**33 removed**, 10 in Orange and 23 in Wolf, each with its branch deleted by
`git branch -d` (which refuses unmerged work; nothing refused). The 17 empty
`wave*` folders were removed too. **Wolf has zero extra worktrees.** Orange
has two left:

- **`agent-orange-ops`**: this plan's own branch (`ops-and-rename`). Remove it
  after that branch merges.
- **`.claude/worktrees/agent-a4f9f16dcf7d0ab40`**: commit `d9371a6`
  (2026-07-26) strengthens `go/runner_systemprompt_test.go` and adds
  `TestProviderResolutionCannotSupplyTheMarker`. It is **not in `main`**, and it
  applies cleanly (`git apply --check`). **Kai to decide:** merge it
  (recommended) or discard it.

And one that needed no action:

- **`agent-orange-phase-a`** was already gone before Phase 0 ran; something
  outside this plan removed it. Its 2 commits survive on the local-only branch
  `feat/phase-a-board`. Its 1 uncommitted file did not survive. The commits
  build on `go/orchestrator/board/`, which the 2026-07-15 reset deleted.
  **Kai to decide:** delete the branch (recommended) or keep it.

Outside "zero worktrees": about 30 old local branches remain in Orange with no
checkout. Pruning them with `git branch -d` is optional.

### External resources — unchanged on 2026-09-10

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
| Local folder | `/home/kai/projects/badcode/agent-orange` | rename breaks the remaining worktree pointers; `git worktree repair` fixes it |
| Local Docker volumes | only `agent-orange-stack-e2e_*`, the e2e rig's (Postgres 71.5 MB) | disposable test data; see Phase 5 |

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

## 3. Six classes of hit, and why this is not one `sed`

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
4. the browser suite: `e2e/helpers/gitprojection.ts` (`BOT_AUTHOR_NAME`,
   `BOT_AUTHOR_EMAIL` and the trailer names) and
   `e2e/features/git-projection.stack.spec.ts:307-308`, which assert the
   bot's name and email on HEAD.

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

**Class P — protocol constants. Two of them must NOT be renamed.**
*Added 2026-09-10.* A protocol constant is a value that is stored, hashed or
matched across versions, so changing it changes what happens to data that
already exists. Class A's `sed` hits all of these, because each contains
`agent-orange` or `agentorange`. Each gets a ruling:

| Value | Where | If renamed | Ruling |
| --- | --- | --- | --- |
| `https://agent-orange.badcode.dev/events/config.changed` | `go/cmd/agentd/configchanged.go:80`: the UUIDv5 namespace that `configChangedEventID` derives from | Every config event already in a database gets a new derived id and **fires `config.changed` again**, so each subscribed worker wakes once per past change. The code comment says so. The local e2e database alone holds 1402 config events. | 🔴 **KEEP VERBATIM, forever.** It is an opaque seed, not a name anyone reads. |
| `agent-orange/session-token/v1` | `go/cmd/agentd/sessionsecret.go:60`: the label the session-token signing key is derived with | Every live session token stops verifying at the core MCP server for up to its 1-hour lifetime (tokens are re-minted on provision and restore). The damage is bounded, and no user ever sees this string. | 🟡 **KEEP VERBATIM.** No benefit, and a real window of broken tools. |
| `orange`, the git projection's folder | `go/gitproj/paths.go:12` (`DefaultSubfolder`), `go/agentdb/project_settings.go:49` (`DefaultGitSubfolder`), the README that `go/gitproj/render.go:324-351` writes, the `ProjectSettingsPage.tsx:493` placeholder | The projection writes to a different folder inside the repository. | ✅ **Rename to `bob`.** As with the trailers, this is safe only because no real projected repository exists. |
| `agent-orange-auth` | `examples/web/src/auth.ts:33`: the browser `localStorage` key | Every browser is signed out once. | ✅ **Rename.** |
| `agent-orange/triagelab/gauntlet-truths@1`, `agent-orange/hypolab/truths@1`, `agent-orange/triagelab/truths@1`, `agent-orange/experiments/compare-report@1` | `go/cmd/{gauntletgen,hypolabgen,triagelabgen}/main.go`; checked by prefix in `web/src/benchreport.ts:228` | The viewer rejects report files generated before the rename. Checked-in fixtures get renamed by the same `sed`. | ✅ **Rename.** Only the experiments rig uses these. |
| `agentorange` Postgres role and database | `docker-compose.yml:72` defaults, `stack:377-382`, `dbconnect.go:58`, `deploy/k8s/10-postgres.yaml:79`, the k8s README | A Postgres volume created as `agentorange` will not connect as `agentbob`. | ✅ **Rename.** The only local volumes are the disposable e2e rig's (Phase 5). |

**How to keep the two KEEP values safe:** pin their **outputs** in a test
before any `sed` runs (Phase 1, step 1.2a). Pinning the strings themselves would
not work: the `sed` rewrites a pinned string and its constant together, so the
test stays green while the protocol changes.

One stale example to fix by hand rather than by `sed`:
`go/cmd/agentd/orgprompts_test.go:21` shows `mcp__agent-orange__memory_search`,
but the server segment is `coreMCPServerName`, which is `"core"`. Make it
`mcp__core__memory_search`.

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

## Phase 0 — Worktree cleanup — ✅ done 2026-09-10 (result in §1)

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

### 1.2a Pin the two protocol constants before any `sed`

*Added 2026-09-10.* In `go/cmd/agentd/`, add one test that pins **outputs**,
which the `sed` cannot touch:

```go
// TestProtocolConstantsNeverChange: these two values look like the product
// name and are not. One seeds the UUIDv5 namespace behind config.changed ids,
// the other domain-separates the session-token key. Changing the first
// re-emits every past config.changed event; changing the second breaks every
// live session token. The Orange→Bob rename left both alone on purpose.
func TestProtocolConstantsNeverChange(t *testing.T) {
	// 1. the namespace, by its UUID value (no product name in it)
	// 2. the session key derived from a fixed test secret, as hex
}
```

Print today's two values once, paste them in as literals, and commit the test
on `main`'s code. Step 1.3's `sed` will then turn it **red**. Restore the two
constant lines by hand (`configchanged.go:80`, `sessionsecret.go:60`) until it
is green again.

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
git ls-files | grep -i orange
# on 2026-09-10: deploy/k8s/20-agent-orange.yaml,
#   design/2026-08-06-embeddable-agent-orange.md, docs/assets/agent-orange.svg
#   (plus this plan and the deploy playbook once ops-and-rename merges)
```

`docs/assets/agent-orange.svg`, the README banner, draws **AGENT ORANGE** as
`<text>` in an orange palette. Changing the text is a one-line edit. Whether
the look stays orange is Jack's call, not a `sed`'s.

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

**~2.5 hours** (was ~1 hour). *Rewritten 2026-09-10: the original said six
files, but the real figure is 154 references in 56 files, plus an internal
vocabulary.* Wolf is a separate repository at
`/home/kai/projects/badcode/agent-wolf` that **embeds** Bob as a service.

### 4.0 Settle Wolf's `main` first

Wolf is on `dev-workflow-and-ux`, **6 commits ahead of Wolf's `main`**, and its
remote is still `binocarlos/agent-wolf`. Apply the same rule as Orange: merge
into Wolf's `main`, then rename against `main`.

### 4.1 What carries the name

| Kind | What | Notes |
| --- | --- | --- |
| Cross-repo contract: env vars | `ORANGE_BASE_URL`, `ORANGE_PUBLIC_URL`, `VITE_ORANGE_PUBLIC_URL`, `ORANGE_DIND_CONTAINER`, `ORANGE_REPO`, `ORANGE_PROJECT`, `ORANGE_WEB_PORT`, `ORANGE_ENV` | Orange's `./stack` sets these when it starts Wolf (`stack:678-692`, `:791-792`). **Rename them in both repos in the same step**, or `./stack wolf up` starts Wolf with its config unset. `VITE_ORANGE_PUBLIC_URL` is baked into Wolf's web bundle at build time, so rebuild. |
| Cross-repo contract: Docker names | `agent-orange-dind-1`, `agent-orange_default`, `agent-orange-postgres-1`, image tag `agent-orange-core:dev` | Derived from Orange's folder name (container, network), or built by Orange's stack inside DinD (image). They only become correct after Phase 5. |
| Internal code vocabulary | `api/src/orange/` (client, types and their tests), `OrangeClient`, `createOrangeClient`, `CreateOrangeClientOptions`, `OrangeSession`, `OrangeRows`, `OrangeResponse`, `withOrange`, `web/src/components/OrangeChatFrame.tsx`, `e2e/orange-override.yml` | About 1440 case-insensitive `orange` hits in all. This is a refactor: `git mv` plus identifier renames, with `yarn typecheck` finding the stragglers. |
| 🔴 A guard that checks for the name | `tools/import-boundary/src/index.ts:319` (`specifier.includes("agent-orange")`), `agentOrangeMentionViolations`, and the `import-boundary.test.ts` in `api/` and `web/` | After the rename this must check for `agent-bob`. A boundary check looking for a name that no longer exists **passes silently**, the R179 shape. Prove it still fires: plant an import containing `agent-bob`, watch the test go red, then remove it. |
| Outbound | `api/src/marketdata/yahoo.ts:102`, `DEFAULT_USER_AGENT`, sent to Yahoo Finance | It points at `github.com/binocarlos/badcode-agent-orange`. Change it to `github.com/badcodetv/agent-bob`. |
| Fixtures and docs | `gs://webkit-servers-agent-orange/…` in `artifacts.test.ts`, fixture READMEs, about 40 comments citing "(agent-orange repo)" | Mechanical. |

No untracked Wolf `.env` carries an `ORANGE_*` key (checked 2026-09-10), and
neither does Orange's `.stack-wolf-secrets.env`.

### 4.2 Decision: how deep inside Wolf

Kai's instruction was that everything named Orange becomes Bob.
**Recommendation: go all the way**, including the internal vocabulary
(`OrangeClient` → `BobClient`, `api/src/orange/` → `api/src/bob/`). The
alternative is to rename only the cross-repo contract and the docs (about an
hour) and keep the internal symbols, which leaves the client for Agent Bob
called `OrangeClient` for good.

### 4.3 Commands

```sh
cd /home/kai/projects/badcode/agent-wolf
git checkout main && git pull            # after 4.0
git checkout -b rename/orange-to-bob

# a) compound tokens, longest first (same rule as Phase 1)
git grep -I -l -i -e 'agent-orange' -e 'agentorange' -e 'Agent Orange' -- . ':!**/node_modules/**' \
  | xargs sed -i -e 's#binocarlos/badcode-agent-orange#badcodetv/agent-bob#g' \
                 -e 's/Agent Orange/Agent Bob/g' -e 's/agent-orange/agent-bob/g' -e 's/agentorange/agentbob/g'

# b) env vars: VITE_ first (\b does not match inside VITE_ORANGE_), and make
#    the same change in Orange's ./stack in the same sitting
git grep -I -l 'ORANGE_' -- . ':!**/node_modules/**' \
  | xargs sed -i -e 's/VITE_ORANGE_/VITE_BOB_/g' -e 's/\bORANGE_/BOB_/g'

# c) files, then symbols (typecheck lists every miss)
git mv api/src/orange api/src/bob
git mv web/src/components/OrangeChatFrame.tsx      web/src/components/BobChatFrame.tsx
git mv web/src/components/OrangeChatFrame.test.tsx web/src/components/BobChatFrame.test.tsx
git mv e2e/orange-override.yml e2e/bob-override.yml
```

Prose such as "Orange unavailable" in log and error messages is Class C:
review it, don't sweep it.

### ✅ Gate 4

```sh
cd /home/kai/projects/badcode/agent-wolf
yarn install --frozen-lockfile && yarn typecheck && yarn test
git grep -n -i -e 'agent-orange' -e 'agentorange' -e 'ORANGE_' -e 'OrangeClient' -- . ':!**/node_modules/**'
# expect: no output. Then prove the import-boundary guard still fires (4.1).
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

**Docker volumes follow the folder name.** `docker-compose.yml` has no
top-level `name:`, so the compose project name *is* the folder name, and the
volumes are named after it. After the `mv`, `docker compose up` creates **new,
empty** volumes. On 2026-09-10 the only local volumes carrying the name are the
e2e rig's, `agent-orange-stack-e2e_{pg-data,agentd-data,stack-e2e-dind}`
(Postgres 71.5 MB). Their project name is a literal `-p agent-orange-stack-e2e`
(in `.github/workflows/ci.yml` and `e2e/README.md`), which Phase 1's `sed`
renames. They are disposable test data: stop that stack before the rename and
`docker volume rm` them afterwards. Re-check with `docker volume ls | grep
orange` on the day. If a real development database has appeared as
`agent-orange_pg-data`, dump it first.

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
Phase 0  worktrees          ✅ 33 removed; 2 items wait on Kai        done
Phase 1  engine repo        pin 2 constants, sed + review, 3 suites    ~1.5h
Phase 2  GCP                create new, republish, DELETE LAST         ~45m
Phase 3  GitHub             rename Bob, TRANSFER Wolf to badcodetv     ~20m
Phase 4  Agent Wolf         settle Wolf main; 56 files + vocabulary    ~2.5h
Phase 5  local folder       mv + worktree repair + e2e volumes         ~30m
──────────────────────────────────────────────────────────────────────────
                                                          total  ~6 hours
```

Then the Kubernetes deploy, under the new names, with nothing to migrate.

> **Note (2026-09-10):** the deploy playbook was written before the git
> projection merged. Before it is run, it needs `AGENTKIT_GIT_CLONE_ROOT`
> placed on the `agentd-data` volume (`design/2026-09-09-git-projection.md`)
> and four line references refreshed (`main.go:543`→`599`, `:561`→`617`,
> `googleauth.go:240`→`261`, `:597`→`620`).
