// Starting an onboarding interview, from the shell's side.
//
// Four HTTP calls in a fixed order, and every one of them is idempotent,
// because a human who navigates away and comes back must land in the SAME
// interview rather than a second one:
//
//   1. is there already an `onboard` session in this project?  reuse it.
//   2. apply onboarding@v1                                     409 = already applied.
//   3. re-enable the interviewer if a previous charter disabled it (A8).
//   4. create the session and wait for it to leave `creating`.
//
// Both paths — reuse and create — wait for the container, and both refuse a
// session that failed to start.
//
// THIS FILE DOES NOT SEND THE SEED any more. It used to POST the first message
// itself and throw the SSE response away, while the rail was bound to nothing
// (it passed an id to <AgentChat/>, which reads messages and `send` from the
// provider's CURRENT session, and nothing had resumed it): the interviewer's
// first reply streamed into a void, and the human's Send was a silent no-op
// (reported 2026-09-13). OnboardingPage now resumes the session into the chat
// provider and sends the seed THROUGH it, once, when the transcript has no
// human message — so the reply streams into the rail, and a reload before the
// seed went out still starts the interview.
//
// Two details that are easy to get wrong and silent when you do:
//
// PERSONA, NOT WORKER. `persona` selects the prompt; `worker` is the identity
// that `emitIdleFinish` requires before it emits `worker.finished` — whose
// text is the WHOLE transcript. Setting `worker` here would put the entire
// interview, charter JSON included, onto the project's event spine, where a
// future archivist's unfiltered subscription would sweep it into memory
// (design Decision A7).
//
// THE NAME IS `onboard`, not `onboard-<project>`. Session names are already
// project-scoped, so the suffix would buy nothing and spend characters
// against a 64-byte limit.

import { useEffect, useRef, useState } from "react";

export const ONBOARD_SESSION_NAME = "onboard";
/** What the session list calls the interview. Without it the list read
 *  "Untitled" — the first message is instructions, not something to title. */
export const ONBOARD_SESSION_TITLE = "Onboarding interview";
const INTERVIEWER = "interviewer";

/**
 * Where "Your team is ready — go to the Desk" lands: `/p/<project>/desk`.
 *
 * Not a session permalink. The onboarding screen binds the chat provider to the
 * interview, so simply switching the view re-enabled the permalink and wrote
 * the INTERVIEW's session into the address bar — a reload of the Desk then
 * opened the interview transcript. `projectIdFromLocation` still reads the
 * project out of this path.
 */
export function deskPath(project: string): string {
  return `/p/${encodeURIComponent(project)}/desk`;
}

/** How long to wait for a container before giving up and saying so. */
const CREATE_TIMEOUT_MS = 180_000;
const POLL_MS = 1_500;

export interface StartOnboardingOptions {
  apiBase: string;
  token: string;
  /** Injected in tests; defaults to the global. */
  fetchImpl?: typeof fetch;
  /** Injected in tests so the container wait does not take real seconds. */
  pollMs?: number;
}

/**
 * Returns the interview session's id once its container is up — never
 * before, because a rail bound to a `creating` session has nothing to show
 * and nowhere to send. Throws with the server's own words: "host port pool is
 * exhausted" is a sentence an operator can act on, and the screen renders it
 * verbatim.
 */
export async function startOnboarding(opts: StartOnboardingOptions): Promise<string> {
  const { apiBase, token } = opts;
  const doFetch = opts.fetchImpl ?? fetch;
  const pollMs = opts.pollMs ?? POLL_MS;

  const call = async (path: string, init?: RequestInit): Promise<Response> =>
    doFetch(`${apiBase}${path}`, {
      ...init,
      headers: {
        ...(init?.body !== undefined ? { "Content-Type": "application/json" } : {}),
        ...(init?.headers ?? {}),
        Authorization: `Bearer ${token}`,
      },
    });

  const failWith = async (res: Response, what: string): Promise<never> => {
    const body = (await res.text()).trim();
    throw new Error(body === "" ? `${what} (HTTP ${res.status})` : body);
  };

  // 1. Reuse. A returning human continues the conversation they were having;
  //    a second interview would deposit a second charter under a different
  //    session name and neither screen would show the other's.
  const existing = await call(`/agent/sessions/by-name/${ONBOARD_SESSION_NAME}`);
  if (existing.ok) {
    const row = (await existing.json()) as { id?: string };
    if (typeof row.id === "string" && row.id !== "") {
      await enableInterviewer(call);
      // A reload while the container was still starting lands here too, so
      // the reuse path waits exactly as the create path does.
      await waitForContainer(call, pollMs);
      return row.id;
    }
  }

  // 2. The topology. A 409 means it is already applied, which on a re-entry is
  //    the normal case and not a failure.
  const applied = await call("/agent/topologies/apply", {
    method: "POST",
    body: JSON.stringify({ name: "onboarding", version: "v1", answers: {} }),
  });
  if (!applied.ok && applied.status !== 409) {
    await failWith(applied, "could not set up the interview");
  }

  // 3. A previous charter disabled the interviewer (A8). Re-entering
  //    onboarding re-enables it, or the session would resolve a prompt from a
  //    disabled worker.
  await enableInterviewer(call);

  // 4. The session. persona, never worker — see the note at the top.
  const created = await call("/agent/session", {
    method: "POST",
    body: JSON.stringify({ persona: INTERVIEWER, name: ONBOARD_SESSION_NAME, title: ONBOARD_SESSION_TITLE }),
  });
  if (!created.ok) await failWith(created, "could not start the interview");
  const createdRow = (await created.json()) as { id?: string; sessionId?: string };
  const sessionId = createdRow.id ?? createdRow.sessionId ?? "";
  if (sessionId === "") throw new Error("the server created a session with no id");

  await waitForContainer(call, pollMs);
  return sessionId;
}

/**
 * Waits for the `onboard` session to leave `creating`. Polls the NAME rather
 * than the id: the name is the thing that is unique and the row is the same
 * either way.
 *
 * `error` is a way of leaving `creating` too, and it used to count as ready:
 * the loop broke, the seed went to a session with no container, and the
 * screen showed an empty rail with no reason. It throws now, with the
 * engine's recorded cause when there is one.
 */
async function waitForContainer(
  call: (path: string, init?: RequestInit) => Promise<Response>,
  pollMs: number,
): Promise<void> {
  const deadline = Date.now() + CREATE_TIMEOUT_MS;
  for (;;) {
    const res = await call(`/agent/sessions/by-name/${ONBOARD_SESSION_NAME}`);
    if (res.ok) {
      const row = (await res.json()) as { status?: string; create_error?: string };
      if (row.status === "error") {
        const cause = (row.create_error ?? "").trim();
        throw new Error(
          `The interview's container failed to start: ${cause === "" ? "no reason was recorded on the session" : cause}. ` +
            "The session named `onboard` keeps its name, so delete it from the session list and reload to try again.",
        );
      }
      if (row.status !== "creating") return;
    }
    if (Date.now() > deadline) {
      throw new Error(
        "the interview's container did not start within three minutes — check `docker compose logs agentd`",
      );
    }
    await sleep(pollMs);
  }
}

/**
 * Enable the interviewer if it exists and is switched off.
 *
 * The body is just `{enabled: true}`, and that is now exactly right.
 *
 * This used to round-trip every field, because `PUT /agent/workers/{name}`
 * wrote whatever a body omitted as a zero or a default: `{enabled: true}` alone
 * erased the interviewer's system prompt before T27, and would have reset
 * max_instances and thawed a frozen interviewer before DI11. The route keeps
 * every omitted field now, so saying only what is changing is correct — and
 * safer than the round-trip was, which sent `briefing: worker.briefing ?? []`
 * and so turned "this worker has no briefing" into "clear it" (DI14's hazard).
 * The GET stays: it is how this learns there is an interviewer to enable, and
 * that it is not enabled already.
 */
async function enableInterviewer(
  call: (path: string, init?: RequestInit) => Promise<Response>,
): Promise<void> {
  const res = await call(`/agent/workers/${INTERVIEWER}`);
  if (!res.ok) return; // no interviewer, or no product layer — nothing to enable
  const worker = (await res.json()) as Record<string, unknown>;
  if (worker.enabled === true) return;
  await call(`/agent/workers/${INTERVIEWER}`, {
    method: "PUT",
    body: JSON.stringify({ enabled: true, rationale: "re-entering onboarding" }),
  });
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

// ---------------------------------------------------------------------------
// The shell's hook around it
// ---------------------------------------------------------------------------

export interface UseOnboardingSession {
  /** Empty until the session's container is up — which is the screen's waiting state. */
  sessionId: string;
  /** The server's own words, or null. */
  error: string | null;
}

/**
 * Starts (or rejoins) the interview exactly once per project.
 *
 * The ref guard is doing real work: `startOnboarding` creates a container, and
 * React's development StrictMode double-invokes effects, so an unguarded
 * effect would try to create two sessions called `onboard` and the second
 * would 409 on the taken name — after provisioning.
 */
export function useOnboardingSession(opts: {
  apiBase: string;
  token: string;
  enabled: boolean;
}): UseOnboardingSession {
  const [sessionId, setSessionId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const started = useRef(false);

  useEffect(() => {
    if (!opts.enabled || opts.token === "" || started.current) return;
    started.current = true;
    void startOnboarding({
      apiBase: opts.apiBase,
      token: opts.token,
    })
      .then(setSessionId)
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err));
      });
  }, [opts.apiBase, opts.enabled, opts.token]);

  return { sessionId, error };
}

// ---------------------------------------------------------------------------
// Deriving "in interview" from the server (A2 / design §3 G1, §6 PR1)
// ---------------------------------------------------------------------------

/** How often to re-check while the interview is still unresolved. Matches
 *  `useCharter`'s own default poll (`web/src/useCharter.ts`): both are waiting
 *  on the same event, the interview ending, which arrives from outside the
 *  browser with nothing in the chat stream announcing it. */
const INTERVIEW_POLL_MS = 4_000;

// THERE IS NO ARCHITECT NAME HERE ANY MORE, and that is the fix.
//
// This module used to hold `ARCHITECT_WORKER_NAME = "architect"` and decide
// that an interview was over when `GET /agent/workers` contained a worker by
// that name — because approving a charter's one immediate roster effect is to
// create the architect. It was documented as a known gap and it was a real
// defect: the charter schema lets an interview name its architect anything,
// and a charter that did left the project reading as still-in-interview
// FOREVER, showing "Finish setting up this project" with no way past it
// (DI10). The server now reports the fact outright — `applied` on
// `GET /agent/charter/current`, from the append-only config log — so this file
// reads it instead of guessing at it. Do not reintroduce a name check.

export interface InterviewState {
  /**
   * True while the project's onboarding interview is unresolved: an
   * `onboard` session exists and the charter it deposited has not been
   * applied. Undefined (both fields false/null) before the first check
   * settles.
   */
  inInterview: boolean;
  /** The `onboard` session's id, once known — null if none exists (or the
   *  check has not settled yet). Used to route a sidebar click at it to the
   *  onboarding view instead of plain chat while `inInterview` is true. */
  onboardSessionId: string | null;
  /** True once the first check has settled. Distinguishes "not yet known"
   *  from "confirmed not in interview" — the difference matters because
   *  acting on the latter (e.g. forgetting a stale onboarding goal) before
   *  the former would race the very first render. */
  resolved: boolean;
}

/**
 * A project is "in interview" when its `onboard` session exists and its
 * charter has not been applied — the server-derived replacement for the
 * `localStorage`-only gate this used to be (design §3 G1: "the interview is a
 * state of the project, not a state of the browser").
 *
 * Two reads, and the second only when the first finds something:
 *
 *   1. `GET /agent/sessions/by-name/onboard` — is there an interview session?
 *      No session means no interview has ever started. That is NOT the same as
 *      "the interview is over", and conflating the two is what DI29 was.
 *   2. `GET /agent/charter/current?session=<id>` — has its charter been
 *      approved? A 404 here is the ordinary mid-interview state (nothing
 *      deposited yet), not an error. `applied: true` is the ONLY thing that
 *      ends the interview, and it is a server fact now rather than something
 *      this file infers from the roster (DI10).
 *
 * Sequential rather than parallel, because the charter read needs the session
 * id. The extra round trip only happens once an interview exists, and while
 * one does this hook is the thing keeping the human's way back to it visible.
 */
export function useInterviewState(opts: {
  apiBase: string;
  token: string;
  refreshMs?: number;
}): InterviewState {
  const { apiBase, token, refreshMs = INTERVIEW_POLL_MS } = opts;
  const [state, setState] = useState<InterviewState>({
    inInterview: false,
    onboardSessionId: null,
    resolved: false,
  });
  // Stops polling once the interview is provably OVER — which means the
  // architect exists, and nothing un-applies a charter.
  //
  // It must not mean "inInterview is false". Those are different states and
  // conflating them is what made this hook unable to do its job: the FIRST
  // check runs at mount, typically before the `onboard` session exists, so
  // `inInterview` is false for the ordinary reason that the interview has not
  // started yet. Latching there stopped the interval permanently and the Desk
  // could never learn that an interview had begun — the exact failure A2 was
  // written to prevent. Caught by D2 (the stack e2e), not by a unit test:
  // `examples/web` has no test runner, and A2's 59 tests all fed `inInterview`
  // in as a prop rather than computing it. See DI29.
  const settled = useRef(false);

  useEffect(() => {
    if (token === "") return;
    let cancelled = false;
    const headers = { Authorization: `Bearer ${token}` };

    const check = async () => {
      try {
        const sessionRes = await fetch(
          `${apiBase}/agent/sessions/by-name/${ONBOARD_SESSION_NAME}`,
          { headers },
        );
        if (cancelled) return;

        let onboardSessionId: string | null = null;
        if (sessionRes.ok) {
          const row = (await sessionRes.json()) as { id?: string };
          if (typeof row.id === "string" && row.id !== "") onboardSessionId = row.id;
        }

        if (onboardSessionId === null) {
          // No interview has ever started. Resolved, not settled: this is the
          // state every ordinary project is in at mount, and the watch must
          // keep running so that a human who clicks "set up this project" is
          // noticed. Latching here is exactly DI29.
          setState({ inInterview: false, onboardSessionId: null, resolved: true });
          return;
        }

        const charterRes = await fetch(
          `${apiBase}/agent/charter/current?session=${encodeURIComponent(onboardSessionId)}`,
          { headers },
        );
        if (cancelled) return;

        // A 204 is the ordinary case: the interview is running and has not
        // deposited a charter yet (an older server says 404). Neither has a
        // body to parse. Any other non-OK answer also reads as "not applied",
        // which errs towards showing the human their way back into setup — the
        // same direction every other decision in this hook errs.
        let applied = false;
        if (charterRes.status === 200) {
          const body = (await charterRes.json()) as { applied?: unknown };
          applied = body.applied === true;
        }

        if (cancelled) return;
        setState({ inInterview: !applied, onboardSessionId, resolved: true });
        // Only the server's `applied` ends the watch, and nothing un-applies a
        // charter. A project that never onboards therefore keeps polling one
        // cheap GET every INTERVIEW_POLL_MS for as long as its console tab is
        // open — the same order as the Desk's own live refresh, and the price
        // of not being wrong in the direction that strands a human
        // mid-interview.
        if (applied) settled.current = true;
      } catch {
        // A transient failure leaves the previous state — the same posture as
        // useCharter's "a 404 is the empty state, not an error": a shell-level
        // gate flickering off because one request dropped would hide "Finish
        // setting up this project" for the one navigation that needed it.
      }
    };

    void check();
    const id = setInterval(() => {
      if (!settled.current) void check();
    }, refreshMs);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [apiBase, token, refreshMs]);

  return state;
}
