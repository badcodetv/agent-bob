# Onboarding and the Guide — a plan for the first person you invite

Status: **proposal** (2026-09-11). Nothing here is built. Written against `main` @ `09d6419`
after a full read of the console shell, the component library, the docs corpus (110 files),
the two onboarding designs, the auth code and the hosting plan. Every claim about what exists
was checked in source; citations are `file:line`.

Relates: `design/2026-09-08-memory-coordinated-organisation.md` (the interview, the charter,
the architect), `docs/product/15-operator-console-design.md` §11 (the editorial rules),
`docs/product/28-console-ia-design.md` §3 (the reveal choreography — the doctrine this plan
must not break), `docs/18-workers-memory-events.md` §9a, `docs/workflows.md`,
`design/2026-09-11-ovh-compose-hosting.md` (where the invitees will actually land).

---

## 0. The verdict, before the detail

Kai asked whether this is worth doing, and whether it should be done differently. Both answers
are "yes".

**Worth doing.** No document in the repository speaks to the person Kai wants to invite: someone
who is neither operator, embedder, nor agent, who will touch the system through the console and
mostly through prose. 110 markdown files, and the one written *to* the human approver is a single
paragraph in an operator reference (`docs/18-workers-memory-events.md:772`). The console itself
has, by written doctrine, no help, no tour, no glossary and no documentation link
(`28-console-ia-design.md` §3.3). The vocabulary — worker, job, event, subscription, schedule,
memory, selector, briefing, charter, architect, frozen, rewrite — is used from the first screen
with definitions scattered across individual empty states. A trusted, curious, non-technical
person arriving today would understand the interview and then be lost.

**Done differently, in three ways.**

1. **Do not build a wizard. The interview is the wizard, and it is better than one.** A wizard
   teaches; the interview *produces* — a charter the person recognises as their own. It already
   asks one question per turn, explains labelling before asking about it, and ends with a
   one-button approval. Building a click-through tour beside it would duplicate the strongest
   piece of UX in the product and cheapen it. What is missing is not a wizard before the
   interview but **what happens after "Approve"** — onboarding today ends at exactly the moment
   the person's real agency begins.

2. **Organise the documentation around the six things a person can touch, not around the atoms.**
   The engine docs are organised by table. A non-technical reader does not want to know what a
   subscription is; they want to know *what they are allowed to change and what will happen when
   they do*. There are exactly six such levers, and every guide page hangs off one of them (§4).

3. **Four of the prerequisites are not documentation, and they come first.** A friend cannot be
   invited without Kai editing a file on the server and restarting the stack; a guest project has
   no spend brake by default; the interview is a one-way door that permanently loses the
   charter if the person clicks "Desk" out of curiosity; and **the console's revert action is not
   reachable in the shipped shell** (§6, PR0) — while the charter panel, the run-architect dialog
   and the operator guide all say *"every change can be reverted from the changelog"*. Revert is
   the architect's entire control; the promise is currently false for everything but prompt
   rewrites. Writing a beautiful guide to a thing your friends cannot safely reach is the wrong
   first move. These are small (§6) and they gate everything else.

The rest of this document is the map: the three journeys (§2), the guidance layer in the console
(§3), the guide's page map with a brief per page (§4), voice and the art of it (§5), the
prerequisites (§6), sequencing and cost (§7), what not to do (§8), and the decisions only Kai and
Jack can make (§9).

---

## 1. What exists, in one screen

The research reports behind this section are long; this is what they add up to.

**The first run today** (`examples/web/src/App.tsx`, `ProjectPicker.tsx`, `onboarding.ts`,
`web/src/components/OnboardingPage.tsx`, `CharterPanel.tsx`):

| Step | Screen | Quality |
|---|---|---|
| Sign in | Google button or one test password (`LoginScreen.tsx:59-93`) | fine; no description of what this is |
| Pick or create a project | name + **required goal** (`ProjectPicker.tsx:70-82`) | create form only for wildcard accounts; a poorer twin of the Sidebar's dialog (`Sidebar.tsx:123-173`), which has the explanatory sentence and the id hint |
| The interview | `ask_user` cards, one question per turn (`go/orgprompts/interviewer.md`) | **the best writing in the product** |
| The charter | four parts, the architect chip, the "makes changes without asking" caption, one `Approve` (`CharterPanel.tsx:88-200`) | good; honest |
| Approved | "The architect exists." + `Run the architect now` (`OnboardingPage.tsx:231-247`) | good; then it stops |
| The Desk | four nav items, first-run panel, glyph legend (`DeskPage.tsx:514-547`) | the best screen in the console, and the last one that explains itself |

**The one trap:** `OnboardingPage` is mounted once (`App.tsx:446`), gated on a `localStorage`
pending record; any nav click calls `onOnboardingDone()` and clears it (`App.tsx:393-396`,
`:150`). `CharterPanel` is rendered nowhere else. The interview session survives in the sidebar;
the charter and the Approve button do not.

**Progressive disclosure:** exactly one mechanism, `web/src/navReveal.ts`. Memory, Activity and
Chart appear in place when the project first contains a memory, an event, or a second worker /
first subscription; the reveal is sticky per project in `localStorage`; a one-sentence
`role="status"` notice says what appeared and why (`navReveal.ts:177-188`). Doctrine: reveal is
**content-driven, never tutorial-driven**; no pulse, no badge, no timer, "there is no onboarding
state to keep" (`28-console-ia-design.md` §3.3). This plan keeps that doctrine intact and builds
inside it.

**Guidance today:** empty-state sentences, some excellent (Triggers: *"A subscription says: when
an event of this type arrives, start a job for this worker. A schedule says: at these times, tell
this worker to do this."* `WorkerTriggers.tsx:216-222`), some absent (Chat renders a blank scroll
area, `AgentChat.tsx:388-397`), and Settings is a flat wall of expert fields with no tiering.

**Multi-user:** no users table, no invite, no signup. Access is a JSON map in an environment
variable read once at boot (`go/cmd/agentd/googleauth.go:91`); changing it means editing `.env`
on the box and running `up.sh` (`docs/ops.md:744-788`). Google login is real. A non-wildcard
account gets exactly the projects listed against it and cannot create another.

**Money:** one `ANTHROPIC_API_KEY` per instance, shared by every project (`main.go:215`). Both
daily token budgets ship at `0 = off` (`go/agentdb/project_settings.go:132-141`,
`router.go:760-761`). Interactive chat is exempt from the budget by design (`router.go:699-702`).
The only zero-config ceiling is the 100-port host pool.

**Voice and identity:** a six-rule editorial code (`15-operator-console-design.md` §11); a palette
where **an agent's change is marked in ember and a human's is unmarked**; Instrument Sans for
prose, IBM Plex Mono for identifiers; "the spine" as the signature device. Bob's visual identity
is an open ticket with Jack's name on it (`2026-09-10-bob-migration-master-plan.md:312`). There is
no mark, no character, and nothing in the repository says why the name is Bob.

---

## 2. The three journeys

The guide and the console changes are derived from these. If a page or a sentence does not serve
one of these three, it is cut.

### J1 — The invitee's first hour

The person is Ellen. She runs a bookshop in Bristol. She is the worked example the interviewer
prompt already uses (`go/orgprompts/interviewer.md`, "A worked example") and she becomes the
running character of the whole guide (§5).

1. **A link from Kai**, with one sentence of context and one sentence of warning (money, and that
   the thing she is about to approve will act without asking).
2. **Sign in with Google.** She lands directly in her project because she has one
   (`examples/web/src/auth.ts:66`). *Today: she cannot create it herself. See §6.*
3. **"What is this project for?"** She types a goal. *One new sentence beneath the box tells her
   this is the first message of an interview, not a form field.*
4. **The interview.** Three to five questions. *Unchanged.*
5. **The charter.** She reads it, recognises it, presses Approve. *Unchanged, except the panel
   must survive her clicking around (§3, G1).*
6. **"Run the architect now."** She presses it, and the console tells her the architect is
   reading the goal and the rulebook. *Unchanged.*
7. **The wait — the first place we lose her today.** Somewhere between one and several minutes
   pass. Then the architect creates workers, writes memories, wires triggers, and calls
   `request_human_attention` to tell her what it did. Each of these is a first: the first worker,
   the first memory, the first ask. **The Desk should narrate each first as it lands**, in one
   sentence, with a link to the guide page for that thing (§3, G4). Nav items are already
   revealed this way; records should be too.
8. **The first ask.** A rose diamond on the Desk: "architect is waiting for you." She opens the
   thread and reads what it built and why. This is the moment she understands the product.
9. **The first edit.** She opens one worker, reads its instructions, changes a sentence, is asked
   for a reason, saves. The changelog shows her edit unmarked beside the architect's in ember.
   *This is the end of the first hour and the guide's chapter 3.*

### J2 — The week after

What she does on days two to seven, each of which is a guide page:

- reads the Desk each morning (three questions: what wants me, what changed, what broke);
- reads a changelog entry and understands who made it;
- reverts one thing, and sees that nothing was erased;
- edits the rulebook (the label registry) and understands that this is publishing a new version
  of a note, not a configuration change;
- adds a clock ("every Monday at 08:00, draft this week's newsletter") in plain words;
- talks to a worker, and learns that a chat is not a job;
- notices the architect has gone quiet, and learns that a quiet architect is the alarm.

### J3 — Kai inviting someone

The operator's runbook. Today: get the Google address, edit the map, restart, authorise the
origin, set a budget, send the URL (`docs/ops.md` §11d). After §6: get the address, one command
or one form, send the URL. This journey is one page in the guide's operator appendix and is
mostly a prerequisite, not documentation.

---

## 3. The guidance layer in the console

Seven pieces of UI work. All of them are words, disclosure and links; none of them is a tour,
a tooltip carpet, a badge, a pulse or a timer. Each is checked against `28-console-ia-design.md`
§3.3 ("reveal is content-driven, not tutorial-driven") and `15-operator-console-design.md` §11.

### G1 — The charter is reachable until it is approved 🔴 (blocker)

**Problem.** `onOnboardingDone()` fires on any nav click and clears the only path to the charter
(`App.tsx:393-396`). A person who clicks "Desk" mid-interview loses Approve forever.

**Fix.** The interview is a *state of the project*, not a state of the browser. Derive it from the
server: a project whose `onboard` session exists and whose newest `kind=org-charter` memory has not
been applied is "in interview". While that is true:

- the Desk's first-run panel carries a **"Finish setting up this project"** row linking back to
  the onboarding view;
- the `onboard` session in the sidebar opens the onboarding view, not the plain chat;
- `onboarding` remains **not** a nav entry (the shell's reasoning at `App.tsx:290-294` stands),
  but it becomes returnable.

Drop the `localStorage` pending record as the source of truth; keep it only as a hint for the
goal text before the session exists. Size: **S**. Test: the e2e spec navigates away mid-interview
and returns to find the charter and Approve.

### G2 — "About this screen": one disclosure per surface

**What.** Every page and every worker tab gets a single collapsed line under its title, set in
the content face, opening to **two to four sentences** in the product's voice plus one link:
*"Read more in the guide →"*. Dismissal is per surface, per project, sticky, stored beside the
navReveal keys (`useNavReveal.ts:43-45` is the precedent). Opening it again is a small text
control, never hidden.

**Why this and not tooltips.** Rule 4 of the editorial code: an empty screen is an invitation.
The disclosure is the invitation for a *non-empty* screen. It is words, it is optional, and it
is in one place. A field-by-field tooltip carpet would violate "quiet MUI, no accents"
(`15-…` §3.7) and would explain the fields without ever explaining the screen.

**Copy source.** The two-to-four sentences are the first paragraph of the matching guide page
(§4), so there is one source and the console never drifts from the guide. Ship them as a small
JSON/TS map in `web/src/guide/` generated from the guide's front matter, or hand-copied with a
test that diffs them — the former is less work over a year.

**Surfaces and the page each links to:** see the table in §4.4.

Size: **S** per surface, **M** total (eleven surfaces).

### G3 — Chat has an empty state

**Problem.** `AgentChat` renders zero rows and a `Type a message...` composer
(`AgentChat.tsx:388-397`). It is the most-used surface and teaches nothing. It also violates the
library's own doctrine, which every other list obeys.

**Fix.** An empty transcript shows: one sentence of what this session is (a chat with the base
agent, or a chat with a named worker), one sentence of the limit that matters (*"A chat is not a
job: it gets none of the project's briefing, and nothing it says is remembered unless it writes a
memory"* — `docs/18-…` "Known limits"), and **three suggested first messages** as plain text
buttons, chosen per context:

- base chat: *"What is in this project's memory?"* / *"Which workers exist and what wakes them?"*
  / *"Write a memory that records …"*
- worker chat: *"Show me your instructions."* / *"What did you do last time you ran?"* /
  *"What would you do if I sent you: …"*

Size: **S**.

### G4 — The Desk narrates firsts

**What.** The Desk's "Changes" stack already lists what happened since the person last looked.
The first time a project contains each *kind* of record — first worker created, first memory
written, first schedule, first subscription, first rewrite, first ask, first revert — the row
carries one extra sentence and a guide link:

> ● 09:14 architect created `newsletter-writer`
> **This is the project's first worker.** A worker is a set of instructions and a trigger — read
> how to change one → *guide: a worker's instructions*

**Why this is inside the doctrine.** It is content-driven: it fires because a record exists, not
because a timer or a checklist says so. It is sticky and per project, the same shape as
navReveal's set (`navReveal.ts:113-120`). It never appears twice. It is one sentence, not a
panel. It is the mechanism §3.2 of doc 28 already chose — *"words carry the news"* — applied to
records instead of nav items.

**Scope guard.** Seven kinds, seven sentences, one link each. Not a checklist, not a progress
bar, no "3 of 7", nothing to complete.

Size: **M** (the fold is `desk.ts`; the state is a per-project seen-set; the copy is seven
sentences).

### G5 — Settings gets two tiers

**Problem.** `ProjectSettingsPage.tsx` presents base image, MCP server JSON, four token budgets,
briefing selectors and six git-projection fields as one flat form.

**Fix.** Two sections with a hairline between them. **"You may want to change these"**: the
project background prose, the project briefing (the rulebook pointer), the daily budget hard
stop. **"Advanced — leave alone unless you know why"**: everything else, collapsed by default,
sticky. No field moves or changes; only the grouping. The budget field's helper text gains one
sentence in plain words about what a day costs (§4, page 14).

Size: **S**.

### G6 — Project creation says what it is, wherever it is

**Problem.** Two create-project forms; the picker's (`ProjectPicker.tsx:55-87`) lacks the
sentence the Sidebar's has: *"Creating a project starts an interview. It asks what the project is
for and how you would know it is working, then writes that down for you to approve."*

**Fix.** One shared form component; the Sidebar's copy wins. Size: **XS**.

### G7 — The guide is served inside the product

**What.** `examples/web` gains a `/guide/<slug>` route rendering the guide's markdown from
`docs/guide/` (bundled at build time, no server, no CMS). Same theme, same type system: prose in
Instrument Sans, identifiers in Plex Mono, hairlines not shadows. Every G2 and G4 link resolves
here, and every guide page's *"in the console"* line links back to the surface it describes.
Deep-linkable, so Kai can paste `…/guide/the-architect` into a message.

**Why inside and not a docs site.** The links from the console must land somewhere that shares the
session and the look. A separate documentation site (Docusaurus, MkDocs) is a second product to
run and theme, and the audience is a handful of invited people, not the public. If Bob goes public
later, the same markdown publishes anywhere.

Size: **M** (a markdown renderer with the theme's typography; a route; a sidebar of ~16 entries;
the build step).

### G8 — A person can write a note from the Memory page 🟡 (lever 4 depends on it)

**Problem.** The Memory page is read-only (`MemoryBrowserPage.tsx` has search, grouping and
"open the session", and no write control), and its empty state says *"there is nothing to add
here"*. Yet the rulebook — the label registry every job is briefed with — is an ordinary memory,
and the settings page invites a person to rely on it. Today a human can change the rulebook only
by asking a chat session to call `memory_create` for them, which the guide cannot honestly
present as "editing the rulebook". The server side already exists: `POST /agent/memories` accepts
a console JWT, stamps provenance empty, and refuses a body that supplies it (`docs/20-datasets.md`).

**Fix.** One "Write a note" control on the Memory page: content, labels (mono, one `key=value`
per line), a required reason. For the rulebook specifically, a "Publish a new version" action on
the current `name=label-registry` row that pre-fills its labels and content. Provenance empty is
exactly what a human-written note should carry, so no new route. Size: **S**.

### G9 — Four copy defects an invitee would hit in the first hour (XS each)

- **Save looks broken.** Worker, subscription and settings saves stay disabled until the
  bottom-of-form `Why?` is filled (`WorkerEditor.tsx:393`, `:377`) and nothing near the button
  says so. One line beside the disabled button: *"Saving needs a reason — it becomes this
  change's entry in the changelog."*
- **Chat and briefing contradict each other.** `WorkerChatPanel.tsx:60-79` promises the briefing
  is composed *"exactly as they would be for an automated job"*; the settings page and
  `docs/18-…` "Known limits" say chat sessions receive no briefing. The settings page is right.
  Fix the panel's sentence; page 6 of the guide depends on it being true.
- **The Desk names two pages that no longer exist.** `DeskPage.tsx:260-262` sends a person to
  "the Events view" and "Automation", both replaced by Activity and per-worker Triggers.
- **The reason field has four names.** `Why?` on workers, subscriptions, settings and revert;
  `Rationale` on schedules; two long forms on the chart. Rule 6: one word everywhere. `Why?`.

---

## 4. The guide: the page map and the brief for each page

### 4.1 Shape of every page

Every page has the same skeleton, in this order, so a reader learns to skim once and skims
everywhere. Target **150–400 words**. No page exceeds one screen of prose plus one example.

1. **The one line.** What this is, in a sentence a person could repeat.
2. **In the console.** Where it lives and what the button says. One line, with the link.
3. **The smallest useful thing you can do.** One action, three steps at most.
4. **Ellen's example.** The bookshop. A real prompt, label, schedule or memory — mono, whole,
   never truncated.
5. **The trap.** The one thing that surprises people. Sourced from the Discovered Issues Log or
   the known-limits sections; never invented.
6. **What this will not do.** One to three lines. This is rule 3 of the editorial code — *"say
   what is not modelled"* — and it is the guide's signature.
7. **Next.** One link forward, one link back.

The **first paragraph (items 1–2)** is what G2 surfaces in the console. Write it to stand alone.

### 4.2 The page map

Sixteen pages, a glossary and an operator appendix. Grouped by the reader's question, not by the
schema.

#### Part 0 — Start here

| # | Slug | Answers | Notes for the writer |
|---|---|---|---|
| 0 | `what-bob-is` | What is this, and is it for me? | Lift from `README.md:13-39` ("Why this exists") and `docs/workflows.md:22-38`. The honest concession first: *if you want one task done, use a chatbot; Bob is for work that outlives one conversation.* Then the shape: workers woken by clocks and events, one shared memory, a team that edits itself and writes down why. **Why "Bob"**: a paragraph only Kai and Jack can write (§9). ≤300 words. |
| 1 | `your-first-hour` | What will happen to me, in order? | J1 as narrative, with one screenshot per step. Say plainly where the waiting is and what to do while waiting (nothing; go and make tea; the Desk will tell you). Say plainly that approving the charter switches on a daily schedule for a worker that acts without asking, and that the changelog is how you watch it. Link every step to its Part 1 page. ≤400 words. |

#### Part 1 — The six things you can touch

These are the levers. Everything a non-technical person can influence is one of these.

| # | Slug | The lever | Console surface (G2 link) | Ellen's example | The trap (sourced) |
|---|---|---|---|---|---|
| 2 | `the-goal-and-the-charter` | The goal you type; the charter you approve | Project create form; `OnboardingPage`; `CharterPanel` | the bookshop charter verbatim from `interviewer.md` | "Approval is for comprehension, not correctness" (`docs/18-…:772`). Depositing changes nothing; approving creates *one* worker and an **enabled** schedule. The charter is always whole; revising means writing it all again. |
| 3 | `a-workers-instructions` | A worker's prompt | Workers → Configuration (`WorkerEditor`) | a 12-line `newsletter-writer` prompt, then the same prompt after Ellen changes one sentence. Sidebar: **the missing title** — learning story 1 (`e2e/features/learning-stories.stack.spec.ts:308`): a poet keeps shipping poems without a title, a reviewer adds one sentence to its prompt with the rationale *"the last poem shipped without a title line"*, the next poem has a title. The whole product in thirty seconds, no human, no deploy. | Every run starts remembering nothing (`interviewer.md` §3). A rationale is required and becomes the commit message. Changing a prompt changes the *next* job, never a running one. Nothing protects one worker's prompt from another except `frozen`. |
| 4 | `the-rulebook` | The label registry — what gets written down, under which label | Memory (`MemoryBrowserPage`, **after G8**; today a human cannot write a memory from the console); Settings → project briefing | the `label_rules` prose from the charter, then Ellen adding `kind=complaint` | Editing the rulebook is publishing a new version of one note, not a settings change — it shows in Memory, not in the changelog. Newest wins; any worker can publish one (`docs/18-…` "Known limits"). No OR in a selector; one clause at a time (`MemoryBrowserPage` no-match copy). |
| 5 | `clocks-and-wake-ups` | Schedules and subscriptions | Workers → Triggers (`WorkerTriggers`, `ScheduleEditor`, `SubscriptionEditor`) | "every Monday at 08:00 → *draft this week's newsletter*" typed in plain words and compiled by the assist | **The input text is the point** (`17-product-spec.md` §3, Schedule). A schedule with no text wakes a worker with nothing to do. The plain-words assist proposes and never saves (`nlAssist.ts:6-14`). A worker is never woken by its own completion. |
| 6 | `talking-to-a-worker` | Chat | Chat; Workers → Chat (`WorkerChatPanel`) | Ellen asking `newsletter-writer` "show me your instructions" | **A chat is not a job.** It gets no briefing (`docs/18-…` "Known limits"), it is exempt from the budget, and the whole transcript is emitted as `worker.finished` when it goes quiet, so the archivist may summarise it. "Run the architect now" is an event for this reason. |
| 7 | `when-a-worker-asks-you` | Answering `request_human_attention` | Desk → Asks; the rose diamond | the architect's first message after it built the team. Sidebar: **the unasked question** — learning story 4 (`learning-stories.stack.spec.ts:550`): a booking worker guesses at an ambiguous request; its mentor adds "ask before assuming"; next run it asks, and the delivery parks at `awaiting_human`. Asking a human arrived as a *learned* behaviour, not a configured gate. | The worker has *stopped* and is waiting; reply in its thread and it continues. Asks can expire (`docs/workflows.md` §6: always pass `expires_in`). Silence is not consent: a paused worker does nothing until you answer or it times out. |

#### Part 2 — Reading what happened

| # | Slug | Answers | Console surface | Notes |
|---|---|---|---|---|
| 8 | `the-desk` | What should I look at each morning? | Desk | Three questions: what wants me, what changed, what broke. The glyphs: filled = a worker did it, hollow = you did it, diamond = waiting for you, cross = a failure. *"A quiet Desk means the fleet ran and nobody needed you."* ≤200 words. |
| 9 | `the-rail-and-the-changelog` | Who did this, and can I put it back? | Activity (**after PR0**; today the revert button is not mounted) | The spine: distance down the page is elapsed time; a quiet night reads as a quiet night. Ember marks are the machine; unmarked is you. **Revert is a forward write**: nothing is erased, both changes stay, you can revert the revert; only the newest change to a thing can be reverted, and the refusal says what is in the way. Never call it undo (rule 2). |
| 10 | `memory` | What has the project remembered? | Memory | Labels are identifiers, content is prose. Search by words or by label. Retraction: a memory can be withdrawn without being deleted, *so the record of the mistake survives* (`go/orgprompts/registry.md`). Memories are references, not rules (the core preamble). |
| 11 | `the-chart` | Who wakes whom? | Chart | A patchbay, not a flowchart: name plates, wires carrying event types, dials for clocks. Appears when the project has a second worker or a first wire. Every gesture on it is a proposal with a reason attached; the one exception, emitting a real event, says so and asks. |

#### Part 3 — The architect and the loop

| # | Slug | Answers | Notes |
|---|---|---|---|
| 12 | `the-architect` | What is the thing that keeps changing my project? | Its loop in plain words (`docs/18-…` §9a): what did I change, did it help, is there anything new, reconcile at most one rewrite, say so. **A quiet architect is the alarm.** How to turn it off: one toggle on its Triggers tab. How to stop it touching one worker: freeze that worker. Say the unbraked truth in the design's own words, unsoftened, in a quoted block. ≤350 words. |
| 13 | `what-bob-will-not-do` | Where are the edges? | Lift `docs/workflows.md:175-210` ("Use something else for these") and the known limits: no authorization between workers, no retry, a failed job is terminal, tools cannot be granted after hire, chat gets no briefing, the rulebook is unprotected. This page is the guide's credibility; keep every item. |
| 14 | `money` | What does this cost and how do I stop it? | One key per instance, shared. What a run roughly costs (Kai to supply a real number from the ledger — do not invent one). The two brakes and that they ship off; how to set the hard stop; that chat is exempt; that a two-worker loop is the thing the brake exists for (`docs/workflows.md` §5). ≤250 words. |

#### Part 4 — Field notes (the art)

| # | Slug | What it is |
|---|---|---|
| 15 | `field-notes` | Six to ten very short true stories from the Discovered Issues Log, each ≤120 words, each ending in the rule it produced. The point is to let the invitee see the character of the thing: a system that publishes the run where it learned nothing. Candidates, all sourced: **Pineapple and banana** (the injection test the model passed); **the placebo that tied** (`sham-critic` scoring 2 ±0 against the real critic — churn is labelled churn); **the architect that noticed 142 seconds** (it refused to act on evidence it had manufactured, and the rule was folded into its prompt); **the brakes that were decoration** (every spend ceiling summed zero for a month — OM-8); **the abandoned cron line that took out a host**; **a worker noticed its own escape hatch was broken** (a probe archivist wrote *"request_human_attention in this project may silently reach nobody"* — and was right); **the rejected inversion** (git as truth, killed on three grounds, arrow reversed); **the forgotten sign-off** (learning story 2, `learning-stories.stack.spec.ts:429`: a support answerer keeps sending unsigned replies, an editor adds a standing rule, the next reply ends `-- The Answering Desk` — improvement is usually this boring, and that is the point). |

#### Reference

| Slug | What |
|---|---|
| `glossary` | The vocabulary table from `17-product-spec.md` §3 and the console: one line each, **the same word everywhere** (rule 6). Worker, job, event, schedule, subscription, memory, label, selector, briefing, rulebook (= label registry), charter, architect, archivist, interviewer, frozen, rewrite, rationale, revert, the Desk, the rail, ask. |
| `for-operators/inviting-someone` | J3, for Kai. The runbook today and after §6. Includes the two standing rules from the hosting plan: keep a login mode set; publish only `web`, on loopback. |

### 4.3 What the guide is not

Not an API reference (that is `docs/19-embedding.md`). Not the engine docs. Not a spec. It does
not explain images, skills, datasets, MCP configuration or the git projection beyond one line
each in the glossary, because none of the six levers needs them and an invitee who reaches for
them has become an operator and should read `docs/18-…`. If the writer feels a page needs a
seventh lever, the answer is a sentence in `what-bob-will-not-do`, not a new page.

### 4.4 The optimum-moment table (G2 wiring)

| Console surface | Disclosure text is the first paragraph of | Fires |
|---|---|---|
| Project create form | `the-goal-and-the-charter` | always (it is one sentence) |
| `OnboardingPage` right column | `your-first-hour` | until approved |
| Desk | `the-desk` | first visit per project |
| Chat (empty) | `talking-to-a-worker` | G3, every empty transcript |
| Workers list | `a-workers-instructions` | first visit |
| Worker → Configuration | `a-workers-instructions` | first visit |
| Worker → Triggers | `clocks-and-wake-ups` | first visit |
| Worker → History | `the-rail-and-the-changelog` | first visit |
| Memory | `the-rulebook` + `memory` | first visit |
| Activity / Changelog | `the-rail-and-the-changelog` | first visit |
| Chart | `the-chart` | first visit |
| Settings | `money` (top tier) | first visit |
| Desk → an ask row | `when-a-worker-asks-you` | first ask (G4) |
| Desk → architect rewrite row | `the-architect` | first rewrite (G4) |

---

## 5. Voice, and the art of it

### 5.1 The voice is inherited, not invented

The guide is written under the console's six editorial rules
(`15-operator-console-design.md:699-719`), and the writer should be handed them verbatim:

1. Name things as the operator controls them, in their own vocabulary; show the exact fields
   beneath, in mono, because they are also true.
2. Never call a forward write an undo.
3. Say what is not modelled. *"The docs are unusually honest about this system's edges; the UI
   should inherit that voice rather than smoothing it."* The guide inherits it too.
4. An empty screen is an invitation, and a broken one is a diagnosis.
5. Churn is labelled churn.
6. The word for the thing stays the same everywhere.

Three more, for prose specifically:

7. **Second person, present tense, short sentences.** The interviewer prompt is the register:
   *"A person who is asked three things at once answers the easiest one, and you lose the other
   two."*
8. **No marketing.** Nothing is powerful, seamless, intelligent or effortless. The strongest
   sentence in the repository is a concession (*"if you are trying to decompose one task, you
   should not be here"*). Lead with the concession on every page that has one.
9. **Identifiers in mono, always whole.** A prompt, a label, a cron line or a memory is shown
   complete, in the code face, never paraphrased and never elided with `…`.

### 5.2 Ellen's bookshop is the through-line

The interviewer prompt already contains a complete, believable, specific worked example: an
independent bookshop in Bristol, one shopfront, about 430 people on the list, Ellen who writes
everything herself, a newsletter judged on whether it went out. Every page's example is Ellen's.
By page 7 the reader knows a small fictional organisation as well as they know their own, and
the abstract machinery has a face. This is the cheapest "art" available and the most effective:
documentation with a protagonist.

The writer should produce, once, a **fixture project**: Ellen's charter, the architect's first
roster (three or four workers with real prompts), the rulebook, a week of memories and a
changelog with one human edit and one revert. Capture it from a real run of the mock stack, not
authored to match the pages (`22-readiness.md:37-39`: an invented fixture is a silent-success
generator). Every screenshot in the guide comes from it. It doubles as a demo project Kai can
open in front of anyone.

### 5.3 Where the art actually is

Kai's phrase was "partially art". The repository already holds the positions; the guide's job is
to let an invitee see them rather than to add decoration.

- **The one screen.** Doc 15 §7.2 names it: *before / the rewrite / after* — two neighbouring
  jobs and the prompt change between them. *"It is the one screen to put in front of someone who
  asks what Agent Bob is."* It is not built as a first-class surface (it survives as
  `BeforeAfterView` inside worker history). The guide's page 9 should reproduce one, from Ellen's
  fixture, and if the project ever wants a single image that *is* Bob, this is it — not a mesh of
  agents, which the old banner's README explicitly warned against (*"a hero image that shows
  agents wired to each other in a mesh would be showing the wrong system"*).
- **The rail.** *"Agent Bob is the tool where everything hangs off a rail, because everything is
  an append."* The guide is typeset with the same rail down its left margin on pages 8–9, so the
  reader learns the device by reading about it.
- **The unbraked loop, stated.** The decision to leave the architect able to delete its own rules,
  and to say so unsoftened, is the project's most distinctive authorial act. Page 12 quotes it in
  full. Do not paraphrase it into comfort.
- **Field notes.** A system that files a finding every place it could have flattered itself is
  rare, and the stories are good. Page 15 is where an invitee falls for the thing.
- **The name and the mark.** Nothing in the repository says why it is Bob, and there is no mark.
  The old orange — *"segments are workers, walls are the fact that workers never talk to each
  other directly, the core is the shared memory every segment touches"* — was a genuinely good
  reading and is recoverable (`git show bacb436^:docs/assets/agent-orange.svg`). Whether Bob
  gets a character, a mark that keeps the orange's reading, or nothing, is Jack's decision (§9).
  The guide's page 0 has a paragraph-shaped hole for it.

---

## 6. Prerequisites that are not documentation

These gate the invitation. None of them is large. They are listed in the order they should land.

### PR0 — Revert is reachable again 🔴 (found during this pass; verified in source)

**The facts.** `RevertControl` ("Revert to this version", the confirm dialog, the forward
compensating write) lives only inside `ChangelogView` (`web/src/components/ChangelogView.tsx:289,
324-395`). `ChangelogView` is mounted by `EventsPage`, `WorkerLineage` and `BeforeAfterView` —
all three marked SUPERSEDED and **unmounted from the shell** when Activity replaced them
(`docs/product/29-work-plan-console-ia.md:309-317`, which notes `ChangelogView` "survives untouched
and is still mounted by `EventsPage`" without noting that `EventsPage` itself no longer is). The
shell mounts `ActivityPage` (`examples/web/src/App.tsx:438`), whose `changes` lens renders config
events with an "open the session" action and nothing else (`ActivityPage.tsx:273-279`;
`grep -i revert web/src/activity.ts web/src/components/ActivityPage.tsx` is empty). `WorkerHistory`
imports only `DiffBlock` from `ChangelogView`. No e2e spec under `e2e/features/` contains the
string "Revert to this version".

**What still works.** A worker's *prompt* can be restored from Workers → History via
`WorkerPromptVersion` (`WorkersPage.tsx:262`), which appends a new prompt write with a restore
rationale. That covers the architect's rewrites. It does not cover the architect **creating a
worker, a schedule, a subscription, or patching project settings** — the other four things it does
on every bootstrap run — and none of those can be put back from the console today.

**What the console promises.** `CharterPanel.tsx:140` — *"Every change it makes can be reverted
from the changelog"*; `RunArchitectControl.tsx:128` — *"every change can be reverted from the
changelog"*; `docs/18-workers-memory-events.md` §9a — *"a 'Revert to this version' action sits on
every entry"*; `CLAUDE.md` — the same. All four are true of the component and false of the shipped
shell. For an invitee who has just been told the architect acts without asking, this is the one
sentence that made approval reasonable.

**Fix (smallest).** Mount `RevertControl` on the `changes` rows of the Activity rail — the row
already carries the config event; the control needs the entry and the revert block the API
returns. The nine-lens filtering doc 29 worried about losing is a separate question and does not
have to be answered to restore the button. Add the missing e2e: make a change, revert it from
Activity, assert the compensating event and the refusal on a non-newest entry. **S.** Until it
lands, the three promises should be softened to what is true, or the guide's page 9 will be
teaching a button that is not there.

### PR1 — The interview survives navigation (= G1) 🔴

Without it, an invitee can lose their project's only approval path by clicking one button. **S.**

### PR2 — Guest projects get a spend brake by default 🔴

Today `daily_tokens_hard` is `0 = off` for every project and the operator must remember to set
it. For a project a friend runs on Kai's key, that is the wrong default.

**Smallest change:** one env var, `AGENTKIT_DEFAULT_DAILY_TOKENS_HARD` (and `_SOFT`), applied by
`DefaultProjectSettings` when the row is first created, so every *new* project starts braked and
the console shows the number in Settings' top tier (G5). The operator can raise it per project.
Existing projects are untouched — the same principle as the disabled-architect migration rule
(`docs/18-…` §9a: never flip other people's settings from a code change).

Also required before believing it: the ledger bug that made the brakes inert for a month is
fixed (`go/agentdb/token_usage.go:6-9`), but OM-8 says *"verify they can physically fire"* — one
live check on the OVH box with a tiny budget, recorded in the ops doc. **S + one live check.**

### PR3 — Inviting someone without a restart

Today the map is read once at boot. Three options, smallest first:

| Option | What | Cost | Note |
|---|---|---|---|
| **A. Reload the map file on a signal or on a timer** | `AGENTKIT_PROJECT_MAP_FILE` re-read on `SIGHUP` or every N seconds; keep the file as the source of truth | **S** | No schema change. Kai still edits a file, but no restart, no dropped sessions. Enough for a handful of friends. |
| B. An `invites` table and one admin route | wildcard holder `POST /admin/invites {email, project}`; login consults table ∪ map | **M** | Migration, route, a small form in the console for wildcard users. Removes the SSH step entirely. |
| C. Let any signed-in account create N projects | per-user quota in the map's object form | **M** | Changes the tenancy story; every guest becomes a mini-operator. Not recommended for the first invitees. |

**Recommendation: A now, B when the third friend arrives.** Both keep the project as the only
boundary and neither touches the JWT shape.

### PR4 — Say the isolation caveat out loud

The hosting plan is explicit: while Bob is alone on the box, privileged Docker-in-Docker
endangers only Bob (`2026-09-11-ovh-compose-hosting.md:288`). Invitees run code inside those
containers. For *trusted* friends this is acceptable and should be **written in the invitation
and in `for-operators/inviting-someone`**, not discovered. No code.

### PR5 — Every guest project's architect runs on the shared key

Not a change; a sentence for Kai's invitation and for page 14. A guest who approves a charter has
switched on a daily job that spends Kai's money. PR2 bounds it; the sentence makes it consented.

---

## 7. Sequencing and cost

| Phase | Work | Size | Who |
|---|---|---|---|
| **A. Make it safe to invite** | PR0, PR1/G1, PR2, PR3-A, PR4 sentence | S + S + S + S | engineer (Kai or an agent), ~2 days |
| **B. Write the guide** | 16 pages + glossary + operator page, Ellen fixture captured from a real mock run, screenshots | ~6k words | the writer, with this document as the brief; ~3–5 days |
| **C. Wire the console** | G2 (11 surfaces), G3, G4, G5, G6, G7 route, G8 write-a-note, G9 copy fixes | M + S + M + S + XS + M + S + XS | engineer; ~4–5 days, can overlap B once page first-paragraphs exist. G8 and G9 can land in Phase A if the writer needs them true early |
| **D. Identity** | why Bob, mark or character, page 0's paragraph | — | Jack + Kai; unblocked by nothing, blocks nothing |
| **E. Invite the first person** | send J3; watch J1 happen; log what breaks in a DI log in this file | — | Kai |

Phase B can start the moment this document is accepted. Phase C's G2 copy depends on B's first
paragraphs and nothing else. Phase A is the only hard gate before Phase E.

**Validation.** One e2e spec per G item against the mock stack (`e2e/features/`), in the style of
`console.stack.spec.ts:120-168` (the nav-reveal spec): assert the disclosure exists, opens, links
to a guide URL that renders, and stays dismissed after reload. For G1: navigate away mid-interview
and back; assert Approve. For PR2: create a project; assert `daily_tokens_hard` is the env
default. For PR0: revert from the Activity rail, assert the compensating event. The guide itself is validated by the one thing that matters: Phase E, with Kai watching
over a shoulder and writing down every place the person stalled.

---

## 8. What this plan deliberately does not do

- **No click-through wizard.** The interview is it.
- **No tooltips on every field, no tour, no badges, no pulses, no progress bar, no "3 of 7".**
  Doc 28 §3 settled this and was right.
- **No sample data injected into a real project.** Ellen lives in the guide and in a demo project,
  never inside the invitee's own.
- **No separate documentation site** until there is a public audience.
- **No video.** Screenshots from the fixture; a video goes stale the first time a button moves.
- **No roles, read-only access or per-project permissions.** Trusted invitees have Kai's powers
  inside their project, and the invitation says so. The project is the boundary
  (`17-product-spec.md` §10, single-trusted-org posture).
- **No change to the architect's autonomy.** The unbraked loop is the product; the guide states
  it; PR2 bounds what it can cost.
- **No documentation of images, skills, datasets, MCP or git projection for invitees.** One line
  each in the glossary.

---

## 9. Decisions only Kai and Jack can make

1. **Why is it called Bob?** Page 0 needs one paragraph. A character, a person, a joke, a
   deliberate blank — any of these works; silence does not.
2. **Mark or no mark.** Keep the orange's reading (workers as segments that never touch, memory
   as the core) under a new form, or nothing until Jack decides. The guide can ship without it.
3. **PR3: A (file reload) or straight to B (invites table).** Recommendation: A.
4. **The default daily budget for a guest project.** A number, in tokens, that Kai is comfortable
   losing per friend per day. Page 14 needs it and PR2 sets it.
5. **Should an invitee be able to create a second project?** Recommendation: not yet.
6. **Topologies and the interview are not reconciled anywhere.** The Desk and Workers empty
   states offer "Start from an org chart" (the fifteen seeds); the interview never mentions them,
   and design A2 of the organisation plan made them deliberately undiscoverable. For invitees,
   either hide the seed buttons until a charter is approved, or give the guide one sentence
   saying the seeds are for people who already know what team they want. Recommendation: hide
   until approved; the architect is the designed default.
7. **Ellen.** Keep the bookshop as the running example, or substitute a real BadCode case (the
   marketing manager, spec §8.8)? Recommendation: Ellen for the guide, because the reader must
   not be able to mistake the example for a promise; the marketing manager is Kai's production
   act and belongs in the ops doc when it happens.

---

## Appendix A — The brief for the writer, in one screen

You are writing sixteen short pages for a curious, trusted, non-technical person who has been
invited onto Agent Bob and will influence it mostly by editing prose. The map is §4.2; the
skeleton every page follows is §4.1; the voice is §5.1 and the interviewer prompt at
`go/orgprompts/interviewer.md`, which you should read twice before writing a word. Your running
example is Ellen's bookshop, and every prompt, label, schedule and memory you show is captured
from the fixture project, whole, in mono. Every page says what the thing will not do. Nothing is
powerful or seamless. When you are unsure what is true, the order of authority is: the code, then
the Discovered Issues Log in `docs/product/06-work-plan.md`, then `docs/18-workers-memory-events.md`,
then everything else — and if you cannot find it, write "unknown" rather than a guess. Your first
paragraph on each page will appear inside the console on the screen it describes, so it must stand
alone. Target 150–400 words a page, 6,000 in total.

## Appendix B — Sources

Research reports compiled for this plan (session scratch, not committed): a screen-by-screen
account of the console today; the docs inventory and the invite path; the history, philosophy and
voice. Primary sources cited inline throughout. The onboarding flow was confirmed against
`e2e/features/onboarding.stack.spec.ts:150-177` and `console.stack.spec.ts:120-168`.

## Discovered Issues Log

*(empty — append here during Phases A–E; do not edit the sections above to match what was found,
log it.)*
