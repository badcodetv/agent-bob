// clamp — cut long model-written text to a readable preview.
//
// A worker's ask, an event's text and a memory are all written by a model, and
// the first real-model walk (2026-09-13) found them rendered whole: the Desk
// was one architect note three screens tall, and Activity's top row was a
// scribe transcript that itself quoted another worker's transcript. The words
// are worth keeping; the feed is not the place to read all of them.
//
// Pure and character-based rather than CSS line-clamp: jsdom cannot measure a
// clamp, and "is there more?" has to be a fact the component can test.

export interface ClampResult {
  /** What to show while collapsed — the whole text when nothing was cut. */
  preview: string
  /** True when `preview` is shorter than the text. */
  clamped: boolean
}

/**
 * The first `maxLines` lines of `text`, and no more than `maxChars`
 * characters, cut at a word where one is near. Trailing blank lines are
 * dropped from the preview so a cut never ends on a gap.
 */
export function clampText(text: string, maxLines = 6, maxChars = 480): ClampResult {
  const normalised = text.replace(/\r\n/g, '\n')
  const lines = normalised.split('\n')
  let preview = lines.slice(0, Math.max(1, maxLines)).join('\n')
  if (preview.length > maxChars) {
    const cut = preview.slice(0, maxChars)
    const space = cut.lastIndexOf(' ')
    preview = space > maxChars * 0.6 ? cut.slice(0, space) : cut
  }
  preview = preview.replace(/\s+$/, '')
  const clamped = preview.length < normalised.trimEnd().length
  return { preview: clamped ? `${preview}…` : normalised, clamped }
}

/** The first non-empty line of `text`, cut to `maxChars` — a one-line title. */
export function firstLine(text: string, maxChars = 140): string {
  const line = text.replace(/\r\n/g, '\n').split('\n').find((l) => l.trim() !== '')?.trim() ?? ''
  return line.length > maxChars ? `${line.slice(0, maxChars).trimEnd()}…` : line
}

/**
 * Markdown's marks removed, for a one-glance preview: `**bold**`, `_em_`,
 * `` `code` ``, heading hashes, link syntax (the text is kept), divider lines
 * (`---`, `***`, `___`), and list markers turned into bullets. Models write
 * markdown, and a collapsed feed row showing `**` and backticks reads as noise;
 * the full text is still rendered as markdown when the reader asks for it.
 *
 * Deliberately conservative: a lone `*` or `_` inside a word (`snake_case`,
 * `2*3`) is left alone, because eating a character from a model's words is
 * worse than leaving a mark in them.
 */
export function stripMarkdown(text: string): string {
  return text
    .replace(/\r\n/g, '\n')
    .replace(/^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/gm, '')
    .replace(/^```[^\n]*\n?/gm, '')
    .replace(/`([^`\n]*)`/g, '$1')
    .replace(/!?\[([^\]\n]*)\]\([^)\n]*\)/g, '$1')
    .replace(/(\*\*|__)(?=\S)([^\n]*?\S)\1/g, '$2')
    .replace(/(^|[\s(])([*_])(?=\S)([^*_\n]*?\S)\2(?=[\s).,;:!?]|$)/gm, '$1$3')
    .replace(/^#{1,6}\s+/gm, '')
    .replace(/^(\s*)[-*+]\s+/gm, '$1• ')
    .replace(/^\s*>\s?/gm, '')
    .replace(/\n{3,}/g, '\n\n')
}
