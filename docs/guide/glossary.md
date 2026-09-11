---
title: Glossary
slug: glossary
part: 9
order: 1
surfaces: []
---

One line per term, the same word the console uses everywhere it appears. This page does
not teach any of these; it is here to look a word up once you have met it on a page or a
screen.

**project**: the namespace that owns one team's workers, memory, events and settings; everyone you invite works inside one.

**goal**: the sentence you write when you create a project, stating what the project is for.

**charter**: the document the interview produces, holding the goal, a measure of success and the label rules, that you approve once to start the team.

**interview**: the guided conversation, one question at a time, that a new project runs to build its charter.

**interviewer**: the worker that conducts the interview and writes the charter.

**architect**: the worker, created when you approve a charter, that designs and maintains the rest of the team.

**archivist**: a worker, usually built by the architect, that reads what happened and writes it down as memory.

**worker**: a named row in a project holding a prompt, its tools and whether it is enabled; not a running process.

**job**: one run of a worker, started by an event or a schedule, in a fresh session unless it resumes one.

**session**: the running container a job (or a chat) executes in; it can be snapshotted and restored.

**event**: a named piece of text delivered into a project, from outside (an email, a webhook) or from the runtime itself (a worker finishing).

**subscription**: a rule saying which event, matching which pattern, starts a job for which worker.

**schedule**: a rule saying at which times to start a job for a worker, with what instruction text.

**trigger**: the general word for what starts a job, a subscription or a schedule; also the name of the tab where you edit both.

**memory**: one saved, permanent note in a project's shared record; never edited or deleted, only added to.

**label**: a small tag on a memory, used to mark what kind of note it is and who it is for.

**selector**: a small query, built from labels, that says which memories a worker should read.

**briefing**: the set of memories a worker is handed automatically at the start of a job, chosen by its selector.

**rulebook**: what this guide calls the label registry, the project's shared agreement on which labels exist and what they mean.

**rationale** (labelled **Why?** in the console): the one required sentence explaining a change, which becomes its entry in the changelog.

**revert**: writing a new change that puts an earlier state back; nothing is ever erased, so a revert can itself be reverted.

**frozen**: a worker flag that stops anyone, including the architect, from changing its prompt.

**rewrite**: any change to a worker's prompt, whether made by a person or by another worker.

**the Desk**: the project's morning screen, showing what wants you, what changed and what broke.

**the rail**: the single, ever-growing timeline that events, jobs and config changes all appear on together.

**ask**: a request for your attention that a worker raises when it needs an answer before it can continue.

**changelog**: the readable list of configuration changes, each with its actor, its reason and a revert option.

**chart**: the console page showing which workers exist and which events and schedules connect them.

**topology**: a starting shape for a team, a set of workers and wiring you can hire in one step instead of building by hand.

**operator**: the person who can change a project's spend budget and caps; not every signed-in person is one.

**budget**: the daily limit on tokens a project may use, with a soft warning tier and a hard stop.

## Next

Back: [Field notes](field-notes.md). Forward: [What Bob is](what-bob-is.md).
