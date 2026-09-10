/**
 * The ask_user payload: the shape every consumer of an `ask_user` card reads,
 * and the ONE function that builds it.
 *
 * This file has NO imports, and must keep it that way. ask_user.ts also
 * defines the SDK tool, which pulls in `@anthropic-ai/claude-agent-sdk` and
 * `zod` — packages only sandbox/ installs. web/'s AskUserCard test imports this
 * builder across the package boundary on purpose, so the card is always
 * rendered from the REAL payload rule. While the builder shared a file with
 * the tool, that import dragged both packages into web's typecheck and test,
 * which passed only on a machine where sandbox/node_modules happened to exist
 * next door. CI's web job installs web/ alone, and failed on it.
 *
 * ── Two rules that keep the card usable, learned from Agent Wolf ─────────
 *
 * `options` is OPTIONAL. It used to be `.min(2)`, which meant a genuinely
 * open question ("what price level would prove you wrong?") could not be
 * asked as a card at all — the model's only recourse was prose, which is
 * what the card exists to replace. With no options the card is the question
 * plus a text box, which is a perfectly good question.
 *
 * `allow_freetext` therefore defaults to `options.length === 0` rather than
 * to a flat `false`. That is the one rule that makes a DEAD card
 * impossible: no buttons and no text box renders a card the user can
 * neither click nor type into, which reads as "the agent is waiting on me"
 * while offering no way to answer. Callers that pass options and say
 * nothing about freetext keep the old `false` — no existing card changes.
 *
 * `options` is also always emitted as an ARRAY, never omitted. The chat
 * reducer copies it verbatim (`web/src/agentEventReducer.ts:291`) and
 * `AskUserCard` calls `.map` on it with no guard
 * (`web/src/components/AskUserCard.tsx:44`), so an absent field is not a
 * missing card — it throws inside React and blanks the chat panel.
 */

/** One option as the marker carries it. */
export interface AskUserPayloadOption {
  label: string;
  value: string;
  description?: string;
  advance?: boolean;
}

export interface AskUserPayload {
  __ask_user: true;
  question: string;
  options: AskUserPayloadOption[];
  allow_freetext: boolean;
  context: string;
}

/**
 * Resolves the tool's arguments (and, on the hook's side, an already-parsed
 * marker) into the payload every consumer reads. ONE implementation, so the
 * tool result, the SSE event and the model-visible text cannot disagree
 * about whether a card has a text box.
 */
export function buildAskUserPayload(args: {
  question?: unknown;
  options?: unknown;
  allow_freetext?: unknown;
  context?: unknown;
}): AskUserPayload {
  const options = Array.isArray(args.options) ? (args.options as AskUserPayloadOption[]) : [];
  // With no options the text box is FORCED on, overriding an explicit
  // `allow_freetext: false`. Honouring that false would render a card with
  // nothing to click and nothing to type into — see the note above; the rule
  // is only worth anything if it cannot be argued out of.
  const allowFreetext =
    options.length === 0
      ? true
      : typeof args.allow_freetext === 'boolean'
        ? args.allow_freetext
        : false;
  return {
    __ask_user: true,
    question: String(args.question ?? ''),
    options,
    allow_freetext: allowFreetext,
    context: typeof args.context === 'string' ? args.context : '',
  };
}
