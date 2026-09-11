# Agent Orange → Agent Bob — Master Migration Plan (both repositories)

> **HOW TO USE THIS:** This is the executable plan. The reasoning record lives in
> `design/2026-09-09-orange-to-bob-rename.md` (the playbook): read it for *why*,
> read this for *what, in what order, by whom*. Stages run in order; tickets inside
> a stage run in parallel. Every ticket names its files, its rules and its gate, so
> a fast medium-effort agent can do it without reading anything else.
> **Do not start a stage until the previous stage's gate is green.**

Status: **READY — not started.** Written 2026-09-10 by the ops-and-rename session.
Base: Orange `main` `5f343cc`, Wolf `dev-workflow-and-ux` `c17ba7d`.
Evidence: a 15-agent read-only research workflow (run `wf_e060715d-1f3`, 2026-09-10) mapped
every one of ~4,100 case-insensitive `orange` hits in both repos; a critic confirmed **zero
files uncovered**; an adversarial verifier upheld all 15 high-stakes rulings. Per-file ledgers:
`<session scratchpad>/rename-research/*.tsv` (guidance only — line numbers drift; executors
re-derive with `git grep`).

---

## 0. Guardrails — the executor re-checks these before S0 and obeys them throughout

1. **Only renames.** Every change is an Orange→Bob rename from §4, or one of the small safeguards
   listed there by name (the pin test, the compose `name:`, the two-prefix report check). Nothing
   else is added, "improved" or tidied. Unrelated work found on the way is reported, not done.
2. **Nothing is deleted except the S5 items, and only after Kai says go at STOP B.** Branches are
   never deleted. Worktrees are removed only with `git worktree remove` (which refuses dirty ones).
   The only other removals are the ones Kai decided: `docs/assets/` (the banner) and, only if T-LIC
   runs, `migration-reference/`.
3. **No force-push, no history rewrite, no `--force` anything.**
4. **No billable runs.** Mock mode only; never `./stack start` without `mock`, never `./stack wolf up`
   without `mock`.
5. **Stop and ask** if a base commit differs from the plan, a gate is red after one fix attempt, or
   anything needs a decision the plan doesn't already make.

## 1. Definition of done

1. Engine repo is `github.com/badcodetv/agent-bob`, **public, MIT-licensed**.
2. Wolf repo is `github.com/badcodetv/agent-wolf` (transferred out of `binocarlos/`), private.
3. Go module is `github.com/badcodetv/agent-bob`.
4. `git grep -n -i orange` in both repos returns **only** the allowlist in §3.
5. Zero extra git worktrees in either repo; local folder is `/home/kai/projects/badcode/agent-bob`.
6. GCP: `agent-bob` Artifact Registry repo, `webkit-servers-agent-bob` bucket,
   `agent-bob-runtime` service account; the three `agent-orange` resources deleted.
7. `binocarlos/badcode-agent-orange` deleted (its three merged-PR descriptions saved first).
8. All gates green in both repos, including both mock end-to-end rigs (§7).
9. **Unchanged, by design:** everything `agentkit` / `AGENTKIT_` / `@agentkit/*`; the two
   protocol constants in §3; session image names `session-base|core|wolf`; the k8s Service `dind`.

## 2. Decisions (all settled 2026-09-10)

| # | Decision | By |
| --- | --- | --- |
| D1 | Rename only Orange→Bob; `agentkit` stays | Kai |
| D2 | Both repos under `badcodetv`; Wolf transfers | Kai |
| D3 | Zero worktrees in both repos | Kai |
| D4 | Delete `binocarlos/badcode-agent-orange` (all 3 branches + 3 merged PR heads verified present locally; no wiki, tags, releases, stars, forks) | Kai |
| D5 | `badcodetv/core` is a **separate follow-up**, not this migration | Kai |
| D6 | Engine repo stays **public** and gets the **MIT** license | Kai |
| D7 | Remove the README banner (`docs/assets/` entirely + README line 1) | Kai |
| D8 | Wolf goes all the way: internal vocabulary renamed too | ops session (Kai: "trust your judgment") |
| D9 | Protocol constants `configchanged.go:80` and `sessionsecret.go:60` **kept verbatim** | ops session; two research agents dissented on D9's second item, overruled: no visible benefit, breaks live session tools for ≤1 h |
| D10 | Git projection trailers, bot identity and default subfolder renamed **outright, no dual-read** — no real projected repository exists (confirmed by the session that built it) | ops session |
| D11 | Report schema ids renamed; `web/src/benchreport.ts` accepts the old prefix too (one line) | ops session |
| D12 | Postgres role/db `agentorange`→`agentbob`; only disposable e2e volumes exist locally | ops session |
| D13 | Dated design-doc **filenames** containing `agent-orange` are renamed, citations updated | ops session (Kai's rule) |
| D14 | **Pin the compose project name**: `name: agent-bob` at the top of `docker-compose.yml`, so container/network/volume names stop depending on the folder name | ops session |
| D15 | Re-vendor `@agentkit/chat-ui` into Wolf as **0.1.3** (the 0.1.2 tarball has old strings compiled in) | ops session |
| D16 | Fruit placeholders stay (`apples-oranges`, `acme/orange`, the Zorblatt fixture) | ops session |
| D17 | `.gitignore`'s `oranged-data/` is dead (the `oranged` component was deleted 2026-07-15) — delete the line | ops session |
| D18 | **Rename only**: do NOT merge agent commit `d9371a6` and do NOT delete `feat/phase-a-board`. Remove the agent's worktree only; its commit stays on branch `worktree-agent-a4f9f16dcf7d0ab40`. Both are separate decisions for later | Kai's sanity rule, 2026-09-10 |

**Cleared false alarms** (research agents assumed these; all checked live 2026-09-10): no k8s
deployment exists (`namespace agent-orange` NotFound); no real git-projection repository
exists; the old service account has **no** user-managed key (the one key is Google-managed) and
nothing local uses it (this machine authenticates as Kai's own gcloud user); neither GitHub repo
has webhooks, Actions secrets, branch protection or open PRs; no crontab, systemd unit, kube
context or shell rc references the old name.

## 3. The allowlist — what may still say "orange" when we are done

The final gate (§7 G-FINAL) is `git grep -n -i orange` minus exactly these:

| Pattern | Where | Why |
| --- | --- | --- |
| `https://agent-orange.badcode.dev/events/config.changed` | `go/cmd/agentd/configchanged.go` | UUIDv5 seed; changing it re-emits every past `config.changed` |
| `agent-orange/session-token/v1` | `go/cmd/agentd/sessionsecret.go` | key-derivation label |
| the pin test for the two above | `go/cmd/agentd/protocolconstants_test.go` | the guard |
| `apples-oranges`, `acme/orange`, `the orange repository` | tests, fixtures, examples | fruit placeholders (D16) |
| `formerly Agent Orange` | `README.md`, `CLAUDE.md` | the one provenance line |
| whole files | `design/2026-09-09-orange-to-bob-rename.md`, this plan | they are the record of the rename |
| whole directory | `migration-reference/` **only if** T-LIC does not remove it | frozen third-party reference |
| `agent-orange/experiments/compare-report` + its comment | `web/src/benchreport.ts` | D11: reports written before the rename still load (added in S2) |
| `agentorange` role note | `README-stack.md` | tells an operator an old `pg-data` volume must be recreated (added in S2) |
| `safety orange` | `docs/product/15-operator-console-design.md` | the colour, not the product (added in S2) |
| `cmd/oranged/` | `docs/product/17-product-spec.md` | history: the path of a component deleted 2026-07-15 (added in S2) |

Anything else that survives is a bug in the migration, not a judgment call.

## 4. The canonical name map (single source of truth for every executor)

Sweep rules, **applied in this order** (longest first) by one script per repo in Stage 1a:

| # | From | To | Scope |
| --- | --- | --- | --- |
| R1 | `github.com/binocarlos/badcode-agent-orange` | `github.com/badcodetv/agent-bob` | both (includes Wolf's Yahoo `DEFAULT_USER_AGENT` URL) |
| R2 | `binocarlos/agent-wolf` | `badcodetv/agent-wolf` | both |
| R3 | `badcode-agent-orange` | `agent-bob` | both (leftovers after R1) |
| R4 | `AGENT ORANGE` / `Agent Orange` / `Agent-Orange` / `agent orange` | `AGENT BOB` / `Agent Bob` / `Agent-Bob` / `agent bob` | both |
| R5 | `agent-orange` | `agent-bob` | both — covers `agent-orange-core:dev`, `agent-orange-dind-1`, `agent-orange_default`, `agent-orange-auth`, `agent-orange/…@1` schema ids, `webkit-servers-agent-orange`, `agent-orange-runtime`, `agent-orange-stack-e2e`, design filenames |
| R6 | `agentorange` | `agentbob` | both |
| R7 | `orange@agentbob.local` | `bob@agentbob.local` | both (runs after R6) |
| R8 | `Orange-(Project\|Seq\|Event\|Action\|Actor-Worker\|Actor-Session)` | `Bob-\1` | both |
| R9 | `VITE_ORANGE_` → `VITE_BOB_`, `X1_ORANGE_` → `X1_BOB_`, then `\bORANGE_` → `BOB_` | | both (Orange's `stack` sets Wolf's env) |
| R10 | Wolf symbols: `createOrangeClient`, `CreateOrangeClientOptions`, `OrangeClient`, `OrangeSession`, `OrangeRows`, `OrangeResponse`, `OrangeChatFrameProps`, `OrangeChatFrame`, `withOrange`, `agentOrangeMentionViolations`, `orange_compose` | `Bob…` equivalents | Wolf (+ `orange-chat-frame` test ids → `bob-chat-frame`) |
| R11 | import paths `/orange/` in `api/src` | `/bob/` | Wolf |

**The sweep script must skip any line containing** `agent-orange.badcode.dev/events/config.changed`
or `agent-orange/session-token/v1`, and must not touch `migration-reference/`, `node_modules/`,
`dist/`, `*.tgz`, or the two whole-file allowlist entries.

File moves (`git mv`) done by the sweep:

| Repo | From | To |
| --- | --- | --- |
| Orange | `deploy/k8s/20-agent-orange.yaml` | `deploy/k8s/20-agent-bob.yaml` |
| Orange | `design/2026-08-06-embeddable-agent-orange.md` | `design/2026-08-06-embeddable-agent-bob.md` |
| Orange | `docs/assets/` | **deleted** (D7) |
| Wolf | `api/src/orange/` | `api/src/bob/` |
| Wolf | `web/src/components/OrangeChatFrame.tsx` / `.test.tsx` | `BobChatFrame.tsx` / `.test.tsx` |
| Wolf | `e2e/orange-override.yml` | `e2e/bob-override.yml` |

Not sweepable — owned by a Stage 1b ticket:

| Value | New | Ticket |
| --- | --- | --- |
| Git projection default subfolder `"orange"` (`go/gitproj/paths.go:12`, `go/agentdb/project_settings.go:49`, `ProjectSettingsPage.tsx` placeholder, `go/gitproj/render.go` README text) | `"bob"` | T-O-GO, T-O-WEB |
| Bare `Orange` / `orange` meaning the product, in prose, logs and error strings (~1,200 Orange + ~1,300 Wolf, mostly handled by R4–R10; the residue is judgment) | `Bob` / `bob` | every Stage 1b ticket, for its own files |
| `docs/19-embedding.md` example host `orange.badcode.dev` | `bob.badcode.tv` (not the sed result `.dev`) | T-O-DOCS |
| `docs/product/15-operator-console-design.md:110-111` ("`ember` … the product is called Agent Orange") | rewrite the rationale without the fruit | T-O-DOCS |
| `go/cmd/agentd/orgprompts_test.go:21` example `mcp__agent-orange__memory_search` (already wrong) | `mcp__core__memory_search` | T-O-GO |
| `web/src/benchreport.ts:228` prefix check | accept `agent-orange/…` **and** `agent-bob/…` (allowlist the old literal there) | T-O-WEB |

## 5. Stage map

```
S0  PREP              coordinator, serial                      ~20 min
S1a SWEEP             coordinator, one script per repo        ~10 min
S1b JUDGMENT          7 tickets in parallel (medium agents)    ~45 min wall
S1c GCP ADDITIVE      coordinator, alongside S1b               ~15 min
S2  INTEGRATE+GATES   coordinator + 2 reviewers                ~60 min (e2e rigs are slow)
─── STOP A: Kai sees the green gates and the diff summary ───
S3  LAND + GO OUTWARD coordinator, serial                      ~20 min
S4  FOLDER            last act of the executing session        ~20 min
─── STOP B: confirm the two irreversible deletions ───
S5  IRREVERSIBLE      coordinator                              ~10 min
                                                   total ≈ 3–3.5 h wall
```

**Execution order:** S0 → S1 → S2 → STOP A → S3 → STOP B → S5 → S4. (S4 goes last because it
moves the folder the executing session lives in.)

Parallelism lives in S1b (seven disjoint file sets) and S1c; S2's Go/JS/Wolf suites run
concurrently; the two e2e rigs share Docker and run one after the other.

## 6. Stages and tickets

### S0 — Prep (coordinator)

1. **Freeze.** No other session commits to either repo until S4 ends (at 2026-09-10 no other
   session works in them; `agent-tests` is in Platinum).
2. **Save the stray repo's PR descriptions** (D4): `gh api repos/binocarlos/badcode-agent-orange/pulls?state=all`
   → a local file outside both repos.
3. **Wolf:** fast-forward `main` to `dev-workflow-and-ux` (`c17ba7d`, 6 commits, `main` has not
   moved — a pure fast-forward).
4. **Orange:** merge `ops-and-rename` (the two playbooks, this plan, the k8s boot-crash fixes) into
   `main`. Do not merge `d9371a6` or delete any branch (D18).
5. **Pin the protocol constants** (playbook §1.2a): add `go/cmd/agentd/protocolconstants_test.go`
   pinning the *outputs* — `configChangedNamespace`'s UUID value, and the session key derived
   from a fixed test secret, as hex. Commit on `main`.
6. Create branches `rename/orange-to-bob` in both repos from their `main`.

**Gate S0:** `go test ./cmd/agentd -run TestProtocolConstantsNeverChange` green; Wolf `main` ==
`c17ba7d`; Orange `main` contains `ops-and-rename`.

### S1a — Mechanical sweep (coordinator, scripted)

Apply §4's rules and file moves with one script per repo on `rename/orange-to-bob`, then
`cd go && go mod tidy`. Commit as **"Rename sweep: mechanical Orange→Bob (rules R1–R11)"**.
Do not fix fallout here — record it for the tickets:

```sh
cd go && go build ./... 2>&1 | head -50      # expect green: R1 is total
cd ../../agent-wolf && yarn typecheck 2>&1 | head -50   # residue goes to T-W-*
```

**Gate S1a:** the pin test is still green (the sweep skipped both lines); `go build ./...` green.

### S1b — Judgment tickets (parallel)

Each ticket: `git worktree add <scratch>/wt-<ticket> rename/orange-to-bob` → edit **only its
files** → run its gate → commit on branch `rename/<ticket>` → report. Files are disjoint, so the
seven branches merge without conflicts in S2. Every ticket applies the same residual rule: a
remaining bare `Orange`/`orange` that means the product becomes `Bob`/`bob`; one that does not
(§3 fruit) stays; anything else is reported, not guessed.

| Ticket | Repo | Files (exclusive) | Work beyond the residual rule | Gate |
| --- | --- | --- | --- | --- |
| **T-O-GO** | Orange | `go/**` | subfolder default `orange`→`bob` in `gitproj/paths.go`, `agentdb/project_settings.go`, `gitproj/render.go` README text, and the tests pinning them; `orgprompts_test.go:21` fix; confirm `gitimport.go`'s "is this ours?" check reads `Bob-Seq`/`Bob-Event` + `bob@agentbob.local` and `e2e`-independent tests cover it; `dbconnect.go` hint text says `agentbob`; second project id `"orange"` in the webhook-poll test → `"acme"` | `go build ./... && go vet ./... && go test ./...` (+ `./stack test-go` for live-PG if Docker is free) |
| **T-O-WEB** | Orange | `web/**`, `examples/**`, `sandbox/**`, `installations/**` | `benchreport.ts` accepts both prefixes (D11); `ProjectSettingsPage.tsx` placeholder `bob`; confirm `examples/web/src/auth.ts` key is `agent-bob-auth` | `web`: `npm ci && npm run typecheck && npm test && ./scripts/verify-package.sh`; `examples/web`: `yarn install --frozen-lockfile && yarn build` (after `web` build); `sandbox`: `npm ci && npm run typecheck && npm test`, then `git checkout sandbox/yarn.lock` |
| **T-O-E2E** | Orange | `e2e/**`, `.github/**` | `e2e/helpers/gitprojection.ts` `BOT_AUTHOR_NAME`/`EMAIL`; experiments `TRUTHS_SCHEMA` constants match the Go writers; the checked-in `gauntlet-smoke-6.report.json` schema; CI `-p agent-bob-stack-e2e` | `npx tsc --noEmit -p e2e` if configured; else review-only — the rig runs in S2 |
| **T-O-OPS** | Orange | root files (`stack`, `docker-compose*.yml`, `.env.example`, `README-stack.md`, `MIGRATION.md`, `.gitignore`), `deploy/**`, `scripts/**` | `name: agent-bob` in `docker-compose.yml` (D14); compose Postgres defaults `agentbob`; `stack` sets `BOB_*` for Wolf and its tmux default `agent-bob`; `apply.sh` lists `20-agent-bob.yaml`; `setup.sh` defaults; delete `oranged-data/` (D17); README-stack note: old `pg-data` volumes must be recreated | `bash -n stack deploy/*.sh deploy/k8s/apply.sh`; `docker compose config -q`; strict k8s validation loop from the deploy playbook |
| **T-O-DOCS** | Orange | `docs/**`, `design/**` (except the two allowlisted files), `README.md`, `CLAUDE.md` | remove banner line + `docs/assets/` (D7); `docs/19` host `bob.badcode.tv`; the `ember` rationale; provenance line "formerly Agent Orange" in README and CLAUDE.md; CLAUDE.md "Module path" rule → `github.com/badcodetv/agent-bob`; memory-file name references | `markdown` links to renamed files resolve (`git grep` each renamed path) |
| **T-W-API** | Wolf | `api/**` | prose in log/error strings ("Orange unavailable" → "Bob unavailable"); fixture READMEs' capture provenance (repo → `agent-bob`, commit SHA unchanged — it exists in `agent-bob`); `artifacts.test.ts` `BLOB_PATH` bucket | `yarn workspace agent-wolf-api typecheck && yarn workspace agent-wolf-api test` |
| **T-W-WEB** | Wolf | `web/**` (not `web/vendor`), `tools/**`, `e2e/**`, `scripts/**`, `installations/**`, root files | import-boundary checker targets `agent-bob` — **prove it fires**: plant an `agent-bob` import, see red, remove; `VITE_BOB_PUBLIC_URL`; `e2e/run.sh` (`BOB_PROJECT` default `agent-bob`, sibling lookup `../agent-bob` — pass `BOB_REPO` explicitly until S4); `scripts/*` `BOB_DIND_CONTAINER`, `agent-bob-core:dev`; `installations/wolf/Dockerfile` `BASE_IMAGE` | `yarn workspace agent-wolf-web typecheck && yarn workspace agent-wolf-web test`; `bash -n e2e/run.sh scripts/*.sh` |

### S1c — GCP, additive only (coordinator, alongside S1b)

```sh
PROJECT=webkit-servers REGION=europe-west1 GCS_BUCKET=webkit-servers-agent-bob \
GCP_AR_REPO=agent-bob SA_NAME=agent-bob-runtime ./deploy/gcp/setup.sh
```

Creates the repo, bucket, service account and the two resource-level grants. Nothing is deleted;
fully reversible. Old bucket holds 44 bytes and images are rebuildable, so there is nothing to copy.

### S2 — Integrate and gate (coordinator)

1. Merge the five Orange ticket branches and two Wolf ticket branches into each repo's
   `rename/orange-to-bob`. Remove the ticket worktrees.
2. **Re-vendor** (D15): in Orange, bump `web/package.json` to `0.1.3`, `npm run build && npm pack`;
   copy into Wolf `web/vendor/agentkit-chat-ui-0.1.3.tgz`, delete 0.1.2, update Wolf
   `web/package.json` + `yarn.lock`.
3. Gates, concurrently: Orange G-GO, G-WEB, G-SANDBOX, G-EXAMPLES; Wolf G-WOLF (commands in §7).
4. G-FINAL (§7) in both repos.
5. **Adversarial review** — two reviewer agents over the combined diffs, each told to *refute*
   "this diff changes nothing but names": one hunts behaviour changes (stored/matched values,
   §3 constants, protocol), one hunts cross-repo mismatches (every `BOB_*` Wolf reads is set by
   `stack`; every Docker name Wolf joins is what compose now produces).
6. End to end, one after the other (both mock, free): Orange `./e2e/run-stack-e2e.sh run mock`,
   then Wolf `BOB_REPO=/home/kai/projects/badcode/agent-orange ./e2e/run.sh`.

**STOP A — checkpoint:** report gate results, diff stats per repo and reviewer findings to Kai. If every
gate is green and no reviewer finding is unresolved, **continue**; if anything is red, stop and ask.

### S3 — Land and go outward (coordinator)

1. Merge `rename/orange-to-bob` into `main` in both repos (the rename lands **before** any project
   gets a real `git_remote` — playbook ordering rule).
2. **T-LIC** (D6) — **done 2026-09-11, after the rename landed; see §8.** Add `LICENSE` (MIT, `Copyright (c) 2026 BadCode`), `"license": "MIT"` in the
   three `package.json` files, and replace the three "private use" statements
   (`README.md:251`, `MIGRATION.md:15`, `CLAUDE.md:39`). **Remove `migration-reference/`** from
   the tree: it is Platinum code (139 files, including a theme using Channel 4's proprietary
   fonts) that should not ship under the MIT license.
3. Push both `main`s.
4. GitHub: rename `badcodetv/agent-orange` → `badcodetv/agent-bob`; transfer
   `binocarlos/agent-wolf` → `badcodetv`; `git remote set-url` in both checkouts; `git fetch`.
5. Republish images to the new registry: `REGISTRY=europe-west1-docker.pkg.dev/webkit-servers/agent-bob`
   `./deploy/publish-base.sh`, `./deploy/publish-images.sh`, Wolf `./scripts/publish-image.sh`.

**Gate S3:** `git remote -v` names `badcodetv` in both; `gh repo view badcodetv/agent-bob` shows
MIT; the new registry lists `session-base`, `session-core`, `session-wolf`, `agentd`, `web`.

### S4 — Folder (runs LAST, after S5; the folder move is done by Kai after the session exits)

The executing session lives inside `/home/kai/projects/badcode/agent-orange` and writes its
transcript under `~/.claude/projects/-home-kai-projects-badcode-agent-orange`, so it cannot move
either safely. It prepares everything, then hands Kai three commands.

1. Stop every stack (`./stack stop`, `./e2e/run-stack-e2e.sh down --purge`, Wolf `./e2e/run.sh --down`).
2. Remove the remaining worktrees with `git worktree remove`: `agent-orange-ops` (its branch is
   merged by now) and `.claude/worktrees/agent-a4f9f16dcf7d0ab40` (its commit stays on its branch).
3. Delete the orphaned e2e volumes `agent-orange-stack-e2e_*` (disposable test data).
4. Print these for Kai to run **after exiting the session**:
   ```sh
   mv /home/kai/projects/badcode/agent-orange /home/kai/projects/badcode/agent-bob
   mv ~/.claude/projects/-home-kai-projects-badcode-agent-orange    ~/.claude/projects/-home-kai-projects-badcode-agent-bob
   mv ~/.claude/projects/-home-kai-projects-badcode-agent-orange-go ~/.claude/projects/-home-kai-projects-badcode-agent-bob-go
   ```
5. Kai then opens a new session in `/home/kai/projects/badcode/agent-bob` and checks:
   `./stack start mock` comes up, and Wolf `./e2e/run.sh` passes without `BOB_REPO`.

**Gate S4:** both repos `git worktree list | wc -l` = 1; G-FINAL still clean.

### S5 — Irreversible (after STOP B)

1. Delete `binocarlos/badcode-agent-orange` (PR descriptions saved in S0).
2. Delete the old GCP Artifact Registry repo, bucket and service account (playbook §2.3 commands)
   — only after Wolf builds from the new registry.

## 7. Gates

| Gate | Command |
| --- | --- |
| G-GO | `cd go && go build ./... && go vet ./... && go test ./...` |
| G-WEB | `cd web && npm ci && npm run typecheck && npm test && ./scripts/verify-package.sh` |
| G-SANDBOX | `cd sandbox && npm ci && npm run typecheck && npm test && git checkout sandbox/yarn.lock` |
| G-EXAMPLES | `cd web && npm run build && cd ../examples/web && yarn install --frozen-lockfile && yarn build` |
| G-WOLF | `cd agent-wolf && yarn install --frozen-lockfile && yarn typecheck && yarn test` |
| G-E2E-ORANGE | `./e2e/run-stack-e2e.sh run mock` |
| G-E2E-JOINT | Wolf `./e2e/run.sh` (asserts mock mode itself) |
| G-FINAL | `git grep -n -i orange -- . ':!migration-reference' ':!node_modules' \| grep -v -E -f <allowlist>` returns nothing, in both repos |
| G-AGENTKIT | `git grep -c agentkit` count unchanged from `main` before S1a (D1) |

## 8. Open items

- **T-LIC done 2026-09-11.** Kai confirmed BadCode holds full copyright over the source, and asked
  that every reference to the previous owner be removed from this repo.
  Note: the repo is already public, so git **history** still contains those files; rewriting
  history is a separate, larger decision not taken here.
- **Follow-ups, not this migration:** `badcodetv/core` docs (D5); the deploy playbook's refresh for
  the git projection (`AGENTKIT_GIT_CLONE_ROOT` on the `agentd-data` volume, four moved line
  references); Bob's visual identity (Jack); ~30 stale local branches in Orange (optional prune).
