// ClampedText — long model-written text, shown short with "Show all".
//
// The cut is clamp.ts's; this only remembers whether the reader asked for the
// rest. Collapsed is the default everywhere it is used, because the surfaces
// that use it are feeds, and a feed is for noticing, not for reading.

import { useState } from 'react'
import { Box, Link, Typography, type TypographyProps } from '@mui/material'
import { clampText } from '../clamp.js'

export interface ClampedTextProps {
  text: string
  maxLines?: number
  maxChars?: number
  color?: TypographyProps['color']
  sx?: TypographyProps['sx']
  'data-testid'?: string
}

export default function ClampedText({
  text,
  maxLines,
  maxChars,
  color,
  sx,
  'data-testid': testId,
}: ClampedTextProps) {
  const [open, setOpen] = useState(false)
  const { preview, clamped } = clampText(text, maxLines, maxChars)
  return (
    <Box data-testid={testId}>
      <Typography variant="body2" color={color} sx={{ whiteSpace: 'pre-wrap', ...sx }}>
        {open ? text : preview}
      </Typography>
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
