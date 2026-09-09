// Starting an onboarding interview, from the shell's side.
//
// Four HTTP calls in a fixed order, and every one of them is idempotent,
// because a human who navigates away and comes back must land in the SAME
// interview rather than a second one:
//
//   1. is there already an `onboard` session in this project?  reuse it.
//   2. apply onboarding@v1                                     409 = already applied.
//   3. re-enable the interviewer if a previous charter disabled it (A8).
//   4. create the session, wait for it to leave `creating`, seed it.
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
import { buildOnboardingSeed } from "@agentkit/chat-ui";

export const ONBOARD_SESSION_NAME = "onboard";
const INTERVIEWER = "interviewer";

/** How long to wait for a container before giving up and saying so. */
const CREATE_TIMEOUT_MS = 180_000;
const POLL_MS = 1_500;

export interface StartOnboardingOptions {
  apiBase: string;
  token: string;
  goal: string;
  /** Injected in tests; defaults to the global. */
  fetchImpl?: typeof fetch;
  /** Told the session id as soon as it exists, before it is ready — so the
   *  screen can stop saying "starting" the moment there is something to bind
   *  the rail to. */
  onSession?: (sessionId: string) => void;
}

/**
 * Returns the interview session's id. Throws with the server's own words —
 * "host port pool is exhausted" is a sentence an operator can act on, and the
 * screen renders it verbatim.
 */
export async function startOnboarding(opts: StartOnboardingOptions): Promise<string> {
  const { apiBase, token, goal } = opts;
  const doFetch = opts.fetchImpl ?? fetch;

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
      opts.onSession?.(row.id);
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
    body: JSON.stringify({ persona: INTERVIEWER, name: ONBOARD_SESSION_NAME }),
  });
  if (!created.ok) await failWith(created, "could not start the interview");
  const createdRow = (await created.json()) as { id?: string; sessionId?: string };
  const sessionId = createdRow.id ?? createdRow.sessionId ?? "";
  if (sessionId === "") throw new Error("the server created a session with no id");
  opts.onSession?.(sessionId);

  // The session is `creating` until its container is up, and a message sent
  // before then is lost. Poll the NAME rather than the id: the name is the
  // thing that is unique and the row is the same either way.
  const deadline = Date.now() + CREATE_TIMEOUT_MS;
  for (;;) {
    const res = await call(`/agent/sessions/by-name/${ONBOARD_SESSION_NAME}`);
    if (res.ok) {
      const row = (await res.json()) as { status?: string };
      if (row.status !== "creating") break;
    }
    if (Date.now() > deadline) {
      throw new Error(
        "the interview's container did not start within three minutes — check `docker compose logs agentd`",
      );
    }
    await sleep(POLL_MS);
  }

  // The seed. Fire-and-forget: the response is the turn's SSE stream, and the
  // rail is already attached to it — awaiting it here would block the screen
  // for the length of the model's first reply.
  void call(`/agent/session/${sessionId}/message`, {
    method: "POST",
    body: JSON.stringify({ content: buildOnboardingSeed(sessionId, goal) }),
  }).then(
    (res) => void res.text().catch(() => {}),
    () => {},
  );

  return sessionId;
}

/**
 * Enable the interviewer if it exists and is switched off.
 *
 * The read-then-write stays, and T27 is why it is still worth explaining.
 *
 * It used to be load-bearing against total loss: `PUT /agent/workers/{name}`
 * wrote every omitted field as its zero value, so a PUT of `{enabled: true}`
 * alone erased the interviewer's system prompt. T27 fixed that — description,
 * system_prompt, mcp_config, image and briefing are now keep-on-absent.
 *
 * But the fix did not reach all eight fields: max_instances, enabled and frozen
 * still replace on absent (DI11 records the split). So a PUT of `{enabled:
 * true}` alone would today reset max_instances to 1 and thaw a frozen
 * interviewer. Sending the whole row is correct under either rule and needs no
 * knowledge of which field is which, so that is what this does.
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
    body: JSON.stringify({
      description: worker.description ?? "",
      system_prompt: worker.system_prompt ?? "",
      mcp_config: worker.mcp_config ?? {},
      image: worker.image ?? "",
      briefing: worker.briefing ?? [],
      max_instances: worker.max_instances ?? 1,
      enabled: true,
      frozen: worker.frozen ?? false,
      rationale: "re-entering onboarding",
    }),
  });
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

// ---------------------------------------------------------------------------
// The shell's hook around it
// ---------------------------------------------------------------------------

export interface UseOnboardingSession {
  /** Empty until the session exists — which is the screen's waiting state. */
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
  goal: string | null;
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
      goal: opts.goal ?? "",
      onSession: setSessionId,
    })
      .then(setSessionId)
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : String(err));
      });
  }, [opts.apiBase, opts.enabled, opts.goal, opts.token]);

  return { sessionId, error };
}
