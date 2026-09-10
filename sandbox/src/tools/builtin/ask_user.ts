import { tool } from '@anthropic-ai/claude-agent-sdk';
import { z } from 'zod';
import type { ToolPlugin } from '../registry.js';
import { buildAskUserPayload } from './ask_user_payload.js';

// The payload types and builder live in ask_user_payload.ts, dependency-free, so
// web/ can import the real builder without installing this tool's SDK or zod.
// Re-exported here so every existing importer of './ask_user.js' is unchanged.
export { buildAskUserPayload } from './ask_user_payload.js';
export type { AskUserPayload, AskUserPayloadOption } from './ask_user_payload.js';

/**
 * ask_user — pose a structured question to the user with selectable options.
 * Generic builtin: every agent product needs this.
 * Returns a __ask_user marker that the PostToolUse hook intercepts and emits
 * as an 'ask_user' SSE event. The rules that keep the card usable are
 * documented beside the payload, in ask_user_payload.ts.
 */

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
