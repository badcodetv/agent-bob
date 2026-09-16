// AgentContextLine — one compact, collapsed line in place of the instructions
// an application sent the agent (agentContext.ts; docs/19-embedding.md §3a).
//
// Collapsed by default: the person did not write it and mostly does not need
// it, but hiding it outright would make the agent's first reply look like it
// came from nowhere. "Show" is there for the curious and for whoever is
// debugging the application's prompt.

import { useState } from 'react'
import { Box, Link } from '@mui/material'
import type { AgentContextMessage } from '../agentContext.js'

export default function AgentContextLine({ context }: { context: AgentContextMessage }) {
  const [open, setOpen] = useState(false)
  return (
    <Box
      data-role="user"
      data-testid="agent-context"
      sx={{
        mb: 2,
        mx: 'auto',
        maxWidth: '90%',
        px: 1.5,
        py: 1,
        border: '1px solid',
        borderColor: 'divider',
        backgroundColor: 'action.hover',
        fontSize: '0.8125rem',
        lineHeight: 1.6,
        color: 'text.secondary',
        wordBreak: 'break-word',
      }}
    >
      <Box component="span" sx={{ fontWeight: 600, color: 'text.primary' }}>
        Context sent to the agent
      </Box>
      {context.summary !== '' && `: ${context.summary}`}{' '}
      <Link component="button" type="button" variant="caption" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        {open ? 'Hide' : 'Show'}
      </Link>
      {open && (
        <Box sx={{ mt: 1, whiteSpace: 'pre-wrap' }} data-testid="agent-context-body">
          {context.context}
        </Box>
      )}
    </Box>
  )
}
