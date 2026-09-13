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
