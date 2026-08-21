# Wave 6 — the guess log

> What an executor had to decide because the plan did not decide it. Wave 4 produced 82,
> wave 5 produced 114, wave 6 produced **73** across four tickets (O9 25 + 4 in its fix round,
> W9 32, W18 12; W16's are appended below when phase B closes). Reversibility as the implementer
> judged it: **62 trivial, 11 moderate, 0 hard**.

## The shape of it

The driver identified in wave 5 held again: **guess count tracks how many UNBUILT tickets a ticket
must keep promises to, not how hard the ticket is.** W9 — four routes, one file of its own, sitting
at the head of a five-ticket chain — produced 32. O9, a single 821-line test file that touches no
production code at all, produced 25, almost all of them "the plan told me to stand up a real
container and did not say which image / which network / which identity". W18, which creates two
files and modifies nothing and promises nothing to anyone, produced 12.

Zero **hard** guesses for the second wave running. The moderate ones cluster in exactly two places
and both are worth an owner's eye:

1. **W9's `/amend` has no stated source of truth.** The criterion says it "re-runs W3's validator
   over the amended spec" and nothing in the plan says where the amended spec text lives. The
   implementer read it out of the `kind=spec-amendment` memory named by `amendment_id`, and
   explicitly rejected the alternative — a spec supplied in the request body — on the grounds that
   a body-supplied spec would be a second, unaudited way for a human to set the scoreboard. That
   reasoning is sound and the choice should be ratified into the plan rather than left as a guess.
2. **W18's whole interface.** `buildSeriesPayload`'s input shape, output shape, and the existence
   of a second exported helper (`serialiseSeriesPayload`) were all invented, because W19 — the
   ticket that consumes them — is unbuilt. This is the ordinary cost of building a producer before
   its consumer and is not a defect, but W19 must be read against what W18 actually shipped rather
   than against the plan's description of it.

## The two guesses that became defects

- **O9's cross-dataset probe** (fixed in its one fix round, and the wave's only blocking defect):
  the test swapped the dataset NAME onto a URL that still carried the minted `?version=2`, against
  a dataset that only ever reaches version 1 — so the asserted 404 came from the version lookup and
  the scope check was never consulted. The verifier proved it by **deleting the `DatasetScope` block
  from `httpapi/datasets.go` outright and watching the whole test stay green.** In the plan's own
  words this file is "the single place where a mis-scoped token actually shows up", so a criterion
  that passes with the check removed is worse than no criterion.
- **W9 adding tests to `api/src/config.test.ts`, which is not on its Files line.** The implementer
  added them anyway and reported the discrepancy rather than skipping them — the right call, and
  the seventh ownership-table mismatch (see R110/R111).

## What the verifiers actually did

All three phase-A verifiers mutated the implementation rather than reading it, which is what the
"grade the ticket, not just the code" instruction is for:

- **O9** — four mutations proved four criteria bite (CRLF mangling → byte-identity fails; row-count
  off-by-one → CSV assertion fails; `?token=` leg disabled → in-container curl gets 401; scope check
  deleted → **nothing fails**, which is how the blocking defect was found).
- **W9** — swapped the schedule-delete and drain blocks and watched the ordered assertion go red;
  injected a `retracts` append into `rollback()` and watched three tests fail, proving the
  "step 1's locked spec is NOT withdrawn" rule is pinned by a real mechanism rather than by absence.
- **W18** — five mutations (leak non-spec datasets; drop the last point; drop the first point; omit
  the missing-dataset key; bare `JSON.stringify`), every one turning the suite red, plus a jsdom
  check at real HTML-parser level and a 2000-trial fuzz of the downsampler.

## Full list

See the workflow journal at
`subagents/workflows/wf_74bdfa3a-677/journal.jsonl` (phase A) for every guess with the plan text it
was made against.
