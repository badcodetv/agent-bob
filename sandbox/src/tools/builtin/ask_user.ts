import { tool } from '@anthropic-ai/claude-agent-sdk';
import { z } from 'zod';
import type { ToolPlugin } from '../registry.js';

/**
 * ask_user — pose a structured question to the user with selectable options.
 * Generic builtin: every agent product needs this.
 * Returns a __ask_user marker that the PostToolUse hook intercepts and emits
 * as an 'ask_user' SSE event.
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

export const askUserTool: ToolPlugin = {
  name: 'ask_user',
  sdkTool: tool(
    'ask_user',
    [
      'Ask the user ONE question as a card with clickable options and/or a text box,',
      'instead of writing questions into your prose. Prefer this over asking in prose whenever',
      'you need something from the user.',
      '',
      'It returns immediately, and the answer does NOT come back as this tool\'s result —',
      'it arrives as the user\'s next ordinary message, on a new turn.',
      'So call it ONCE, then STOP and wait. Do not ask a second question in the same turn,',
      'and do not keep talking afterwards: that buries the card.',
      '',
      'Give 2-10 options when you know the plausible answers. Omit options entirely when the',
      'answer is free-form (a number, a date, a ticker, prose) — the card is then the question',
      'plus a text box. With no options, the text box is always shown.',
    ].join('\n'),
    {
      question: z.string().min(1).describe('The question to ask'),
      options: z.array(z.object({
        label: z.string().min(1).describe('Short button label'),
        value: z.string().min(1).describe('Value sent back when selected'),
        description: z.string().optional().describe('Longer description below the label'),
        advance: z.boolean().optional().describe('If true, clicking triggers phase advancement instead of sending a message'),
      })).max(10).optional().describe('Selectable options. Omit for an open question answered in the text box.'),
      allow_freetext: z.boolean().optional().describe('Show free-text input alongside options. Defaults to true when there are no options, false otherwise.'),
      context: z.string().optional().describe('Context shown above the options'),
    },
    async (args) => ({
      content: [{
        type: 'text' as const,
        text: JSON.stringify(buildAskUserPayload(args)),
      }],
    })
  ),
  marker: {
    key: '__ask_user',
    event: 'ask_user',
    toEvent: (payload: Record<string, unknown>) => {
      const resolved = buildAskUserPayload(payload);
      return {
        question: resolved.question,
        options: resolved.options,
        allowFreetext: resolved.allow_freetext,
        context: resolved.context,
      };
    },
    toModelText: (payload: Record<string, unknown>) =>
      JSON.stringify(buildAskUserPayload(payload)),
  },
};
