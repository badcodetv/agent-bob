// agentContext — the documented marker for "context the application sent the
// agent", so a chat does not show it as the person's own words.
//
// An application embedding Bob usually opens a session with a first message
// that is mostly instructions: which tools to call, how to label memories, the
// record the conversation is about. It has to travel as a user message — that
// is the only way in — and the transcript then showed it as a big bubble in the
// person's name. Agent Wolf's first chat bubble was exactly that
// (2026-09-13). Bob's own onboarding seed has a bespoke matcher
// (charter.ts parseOnboardingSeed); this is the general one, for anyone.
//
// The wire shape (docs/19-embedding.md §3a):
//
//   <agent-context summary="Opened from the hypothesis page">
//   …instructions for the agent, any length…
//   </agent-context>
//   Optional words the person actually typed.
//
// Display only. The stored message is untouched, so the model reads exactly
// what was sent — a tag-delimited block is also a shape models read well. The
// opening tag must be the very first thing in the message, so a person who
// types something tag-like mid-sentence still sees their own words.

export interface AgentContextMessage {
  /** The one-line label from `summary="…"`; '' when absent. */
  summary: string
  /** Everything between the tags, trimmed. */
  context: string
  /** What follows the closing tag, trimmed — the person's own words, or ''. */
  rest: string
}

const OPEN = /^<agent-context(?:\s+summary="([^"\n]*)")?\s*>/
const CLOSE = '</agent-context>'

/** Recognise a message carrying the context marker; null when it does not. */
export function parseAgentContext(content: string): AgentContextMessage | null {
  const text = content.replace(/\r\n/g, '\n')
  const open = OPEN.exec(text)
  if (open === null) return null
  const end = text.indexOf(CLOSE, open[0].length)
  if (end < 0) return null
  return {
    summary: decodeAttr(open[1] ?? '').trim(),
    context: text.slice(open[0].length, end).trim(),
    rest: text.slice(end + CLOSE.length).trim(),
  }
}

/** Build a message carrying the marker — the inverse of parseAgentContext. */
export function formatAgentContext(context: string, summary = '', rest = ''): string {
  const attr = summary.trim() === '' ? '' : ` summary="${encodeAttr(summary.trim())}"`
  const head = `<agent-context${attr}>\n${context.trim()}\n${CLOSE}`
  return rest.trim() === '' ? head : `${head}\n${rest.trim()}`
}

function encodeAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/\n/g, ' ')
}

function decodeAttr(s: string): string {
  return s.replace(/&quot;/g, '"').replace(/&amp;/g, '&')
}
