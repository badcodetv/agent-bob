// ClampedText — long model-written text, shown short with "Show all".
//
// The cut is clamp.ts's; this only remembers whether the reader asked for the
// rest. Collapsed is the default everywhere it is used, because the surfaces
// that use it are feeds, and a feed is for noticing, not for reading.

import { useState } from 'react'
import { Box, Link, Typography, type TypographyProps } from '@mui/material'
import { clampText, stripMarkdown } from '../clamp.js'
import AgentMarkdown from './AgentMarkdown.js'

export interface ClampedTextProps {
  text: string
  maxLines?: number
  maxChars?: number
  color?: TypographyProps['color']
  sx?: TypographyProps['sx']
  'data-testid'?: string
  /**
   * The text is model-written markdown: the collapsed preview has its marks
   * stripped, and "Show all" (or a text short enough to need no clamp) renders
   * it through the chat's markdown renderer.
   */
  markdown?: boolean
}

export default function ClampedText({
  text,
  maxLines,
  maxChars,
  color,
  sx,
  'data-testid': testId,
  markdown = false,
}: ClampedTextProps) {
  const [open, setOpen] = useState(false)
  const { preview, clamped } = clampText(markdown ? stripMarkdown(text) : text, maxLines, maxChars)
  const whole = open || !clamped
  return (
    <Box data-testid={testId}>
      {markdown && whole ? (
        <Box
          sx={{ color, '& p': { my: 0.5 }, '& ul, & ol': { my: 0.5, pl: 3 }, '& > .agent-markdown > :first-of-type': { mt: 0 }, ...sx }}
          data-testid="clamped-markdown"
        >
          <AgentMarkdown content={text} />
        </Box>
      ) : (
        <Typography variant="body2" color={color} sx={{ whiteSpace: 'pre-wrap', ...sx }}>
          {open ? text : preview}
        </Typography>
      )}
      {clamped && (
        <Link
          component="button"
          type="button"
          variant="caption"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
        >
          {open ? 'Show less' : 'Show all'}
        </Link>
      )}
    </Box>
  )
}
