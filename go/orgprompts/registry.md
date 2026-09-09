The label conventions for this project. Read this before writing a memory.
When you introduce a label, extend this note by writing a new version of it —
same labels, whole content — so everybody else learns it too.

HOW LABELS WORK HERE

- kind=<x> says what sort of thing a memory is. Selecting on it is how a
  worker asks for "everything of this sort".
- name=<x> means "the current value of x": the newest memory carrying that
  label wins, and the older ones stay as history. This is how a single value
  — a count, a rulebook, a standing decision — is read back. memory_current
  reads exactly this.
- retracts=<memory-id> withdraws another memory. Write one saying why. The
  retracted memory stops appearing in briefings and searches without being
  deleted, so the record of the mistake survives.
- A memory is written once and never edited. Changing something means
  writing the whole thing again.

THE STARTING VOCABULARY

kind=decision   — a choice that was made, and why. Written by whoever made
                  it, at the time they made it.
kind=summary    — what happened in one thread of work. name=<thread-slug>.
kind=lesson     — something that should change how this project acts next
                  time.
kind=fact       — a durable fact about the world. name=<what-it-is>, so the
                  current value can be read back.
kind=retraction — withdraws another memory, via retracts=<id>. Say why.

TWO LABELS THAT ARE NOT YOURS TO DESIGN

kind=rolling-summary, worker=<name>
    The one briefing section every worker is handed automatically, with no
    configuration: the newest memory with that worker's name on it. Whoever
    summarises finished work writes these, one per worker it summarises, in
    addition to whatever else it writes. Do not select on it — it is
    delivered, not fetched — and do not repurpose it.

kind=architect-verdict
    The architect's own judgement of its last change: helped, hurt, or
    no-signal, and the evidence it used. The architect writes these and reads
    its own back. Nothing else should write one.

WHAT THIS PROJECT ADDS

The project's own rules follow. If nothing follows, this project has not
added any labels yet, and the vocabulary above is all there is.
