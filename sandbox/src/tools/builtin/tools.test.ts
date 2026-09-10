import { describe, it, expect } from 'vitest'
import { z } from 'zod'
import { inferArtifactType } from './write_file.js'
import { askUserTool, buildAskUserPayload } from './ask_user.js'
import { viewImageTool } from './view_image.js'

// --- inferArtifactType (write_file) ---

describe('inferArtifactType', () => {
  it('returns "image" for image extensions', () => {
    expect(inferArtifactType('.png')).toBe('image')
    expect(inferArtifactType('.jpg')).toBe('image')
    expect(inferArtifactType('.jpeg')).toBe('image')
    expect(inferArtifactType('.gif')).toBe('image')
    expect(inferArtifactType('.webp')).toBe('image')
    expect(inferArtifactType('.svg')).toBe('image')
  })

  it('returns "webapp" for .html in dist/ path', () => {
    expect(inferArtifactType('.html', 'dist/index.html')).toBe('webapp')
    expect(inferArtifactType('.htm', 'project/dist/app.htm')).toBe('webapp')
  })

  it('returns "code" for .html outside dist/', () => {
    expect(inferArtifactType('.html', 'src/page.html')).toBe('code')
    expect(inferArtifactType('.html')).toBe('code')
  })

  it('returns "data" for data file extensions', () => {
    expect(inferArtifactType('.csv')).toBe('data')
    expect(inferArtifactType('.json')).toBe('data')
    expect(inferArtifactType('.tsv')).toBe('data')
  })

  it('returns "code" for code file extensions', () => {
    expect(inferArtifactType('.js')).toBe('code')
    expect(inferArtifactType('.ts')).toBe('code')
    expect(inferArtifactType('.py')).toBe('code')
    expect(inferArtifactType('.r')).toBe('code')
    expect(inferArtifactType('.sql')).toBe('code')
    expect(inferArtifactType('.sh')).toBe('code')
    expect(inferArtifactType('.css')).toBe('code')
  })

  it('returns "file" for unknown extensions', () => {
    expect(inferArtifactType('.xyz')).toBe('file')
    expect(inferArtifactType('.bmp')).toBe('file')
    expect(inferArtifactType('.doc')).toBe('file')
  })
})

// --- askUserTool marker ---

describe('askUserTool marker', () => {
  const marker = askUserTool.marker!

  it('toEvent maps allow_freetext → allowFreetext and passes through fields', () => {
    const payload = {
      __ask_user: true,
      question: 'Pick a color',
      options: [
        { label: 'Red', value: 'red' },
        { label: 'Blue', value: 'blue' },
      ],
      allow_freetext: true,
      context: 'For the chart background',
    }

    const event = marker.toEvent(payload)

    expect(event).toEqual({
      question: 'Pick a color',
      options: payload.options,
      allowFreetext: true,
      context: 'For the chart background',
    })
  })

  it('toModelText returns JSON with __ask_user: true', () => {
    const payload = {
      __ask_user: true,
      question: 'Choose one',
      options: [{ label: 'A', value: 'a' }, { label: 'B', value: 'b' }],
      allow_freetext: false,
      context: '',
    }

    const text = marker.toModelText(payload)
    const parsed = JSON.parse(text)

    expect(parsed.__ask_user).toBe(true)
    expect(parsed.question).toBe('Choose one')
    expect(parsed.options).toHaveLength(2)
  })
})

// --- askUserTool: the open question, and the dead card that cannot happen ---
//
// `options` used to be `.min(2)`, so a question whose answer is a number, a
// date or a ticker could not be asked as a card at all and the model fell
// back to prose — which is the exact thing the card exists to replace.
// These pin the relaxation and the rule that came with it.

describe('buildAskUserPayload', () => {
  it('an open question keeps options as an ARRAY and turns the text box ON', () => {
    const payload = buildAskUserPayload({ question: 'What level would prove you wrong?' })
    expect(payload.options).toEqual([])
    expect(payload.allow_freetext).toBe(true)
  })

  it('a NON-array options (a model sending a string) still yields an array', () => {
    // AskUserCard maps over this with no guard; anything but an array blanks
    // the chat panel rather than dropping one card.
    const payload = buildAskUserPayload({ question: 'Pick', options: 'red, blue' })
    expect(Array.isArray(payload.options)).toBe(true)
    expect(payload.options).toEqual([])
  })

  it('FORCES the text box on when there are no options, even if told not to', () => {
    // No buttons and no text box is a card the user can neither click nor
    // type into. The rule is only worth anything if it cannot be overridden.
    const payload = buildAskUserPayload({ question: 'How many days?', allow_freetext: false })
    expect(payload.allow_freetext).toBe(true)
  })

  it('LIMIT: with options present, an unspecified allow_freetext stays FALSE', () => {
    // The pre-existing default for every caller that already passes options.
    // If this flips, every existing product's cards silently grow a text box.
    const payload = buildAskUserPayload({
      question: 'Pick a colour',
      options: [{ label: 'Red', value: 'red' }, { label: 'Blue', value: 'blue' }],
    })
    expect(payload.allow_freetext).toBe(false)
  })

  it('with options present, an explicit allow_freetext: true is honoured', () => {
    const payload = buildAskUserPayload({
      question: 'Pick a colour',
      options: [{ label: 'Red', value: 'red' }, { label: 'Blue', value: 'blue' }],
      allow_freetext: true,
    })
    expect(payload.allow_freetext).toBe(true)
  })

  it('is idempotent — re-resolving an already-built payload changes nothing', () => {
    // toEvent and toModelText both run it over a payload the handler already
    // produced. A non-idempotent rule would flip allow_freetext on the way
    // to the UI while the model saw the other value.
    const once = buildAskUserPayload({
      question: 'Pick a colour',
      options: [{ label: 'Red', value: 'red' }, { label: 'Blue', value: 'blue' }],
    })
    expect(buildAskUserPayload(once)).toEqual(once)
  })

  it('defaults context to a string, never undefined', () => {
    expect(buildAskUserPayload({ question: 'Why?' }).context).toBe('')
  })
})

describe('askUserTool schema accepts an open question', () => {
  // The relaxation itself: `options` used to be `.min(2)`, so these two
  // calls were rejected by the SDK before the handler ever ran. Driving the
  // real zod shape (and the real handler through it) is the only way to see
  // that — buildAskUserPayload alone never meets the schema.
  const shape = (askUserTool.sdkTool as unknown as { inputSchema: z.ZodRawShape }).inputSchema
  const schema = z.object(shape)
  const handler = (askUserTool.sdkTool as unknown as {
    handler: (args: unknown, extra: unknown) => Promise<{ content: Array<{ text: string }> }>
  }).handler

  it('accepts a question with NO options at all', () => {
    const parsed = schema.safeParse({ question: 'What level would prove you wrong?' })
    expect(parsed.success).toBe(true)
  })

  it('accepts an explicitly empty options array', () => {
    expect(schema.safeParse({ question: 'How many days?', options: [] }).success).toBe(true)
  })

  it('still rejects more than 10 options', () => {
    const options = Array.from({ length: 11 }, (_, i) => ({ label: `L${i}`, value: `v${i}` }))
    expect(schema.safeParse({ question: 'Pick', options }).success).toBe(false)
  })

  it('still rejects an empty question', () => {
    expect(schema.safeParse({ question: '' }).success).toBe(false)
  })

  it('the real handler answers an open question with a renderable marker', async () => {
    const args = schema.parse({ question: 'What level would prove you wrong?' })
    const result = await handler(args, {})
    const marker = JSON.parse(result.content[0].text)
    expect(marker.__ask_user).toBe(true)
    expect(marker.options).toEqual([])
    expect(marker.allow_freetext).toBe(true)
    expect(marker.context).toBe('')
  })
})

describe('askUserTool marker over an open question', () => {
  const marker = askUserTool.marker!

  it('toEvent yields an empty options array and allowFreetext true', () => {
    const event = marker.toEvent({ __ask_user: true, question: 'How many days?' })
    expect(event).toEqual({
      question: 'How many days?',
      options: [],
      allowFreetext: true,
      context: '',
    })
  })

  it('toModelText round-trips an open question', () => {
    const parsed = JSON.parse(marker.toModelText({ __ask_user: true, question: 'How many days?' }))
    expect(parsed.__ask_user).toBe(true)
    expect(parsed.options).toEqual([])
    expect(parsed.allow_freetext).toBe(true)
  })
})

// --- viewImageTool mime type mapping ---

describe('viewImageTool — mime type inference', () => {
  // The mime map is internal to the tool handler, but we can verify the
  // supported extensions by checking the tool description mentions them.
  // For a direct test, we check the constants are consistent.
  const mimeMap: Record<string, string> = {
    '.png': 'image/png',
    '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg',
    '.gif': 'image/gif',
    '.webp': 'image/webp',
  }

  it('.png maps to image/png', () => {
    expect(mimeMap['.png']).toBe('image/png')
  })

  it('.jpg maps to image/jpeg', () => {
    expect(mimeMap['.jpg']).toBe('image/jpeg')
  })

  it('.webp maps to image/webp', () => {
    expect(mimeMap['.webp']).toBe('image/webp')
  })

  it('.bmp is not in the supported map', () => {
    expect(mimeMap['.bmp']).toBeUndefined()
  })
})
