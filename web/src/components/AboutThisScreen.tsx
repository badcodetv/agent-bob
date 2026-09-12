// AboutThisScreen — one disclosure per surface (G2, work plan ticket C2).
//
// Reads its paragraph from whatever `GuideProvider` (`../guide/GuideProvider.js`)
// is mounted above it; the default (no provider, or no page written for this
// surface yet) is nothing at all — no chevron, no empty box, no line. That is
// the load-bearing acceptance criterion: a consumer of the published package
// with no guide gets a component library that still works, and a surface
// whose page has not been written yet (most of them, until Stream B lands)
// looks exactly like a surface with no About line, not a broken one.
//
// Three states, one component:
//   - collapsed: "About this screen" and a chevron, one line, under the title.
//   - open: the paragraph plus "Read more in the guide →" and Dismiss.
//   - dismissed: a small text control that only says "About this screen" —
//     clicking it reopens, it does not re-show the paragraph inline.
// Dismissal is sticky per (surface, project) in `localStorage`
// (`aboutDismissal.ts`), never global: an operator who has read the Desk's
// About line has not read the Memory page's.
//
// `alwaysExpanded` is the `project-create` surface's mode (§ Scope): no
// chevron, no Dismiss, because it is one sentence and the form does not
// belong to a project yet for a dismissal to be scoped to.
//
// The only animation here is the 180ms fade/collapse, gated on
// `usePrefersReducedMotion` (doc 21 §4.1) — nothing else moves.

import { useEffect, useState } from 'react'
import { Box, Collapse, Link, Stack, Typography, type SxProps, type Theme } from '@mui/material'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import usePrefersReducedMotion from '../useReducedMotion.js'
import { buildGuideHash } from '../guide/guideRoute.js'
import { useGuideParagraph } from '../guide/GuideProvider.js'
import { dismissAbout, isAboutDismissed, restoreAbout } from '../guide/aboutDismissal.js'
import type { SurfaceId } from '../guide/surfaces.js'

export interface AboutThisScreenProps {
  surface: SurfaceId
  /** Scopes dismissal. Ignored (dismissal never applies) when `alwaysExpanded`. */
  projectId: string
  /** `project-create`'s mode: always shown, no chevron, no Dismiss. */
  alwaysExpanded?: boolean
  /**
   * Extra styling for the root element (spacing to match a host layout that
   * has no page-title wrapper of its own, e.g. the Workers list column).
   * Never used to add chrome that would show when there is no paragraph —
   * this component still renders nothing in that case regardless of `sx`.
   */
  sx?: SxProps<Theme>
}

export default function AboutThisScreen({
  surface,
  projectId,
  alwaysExpanded = false,
  sx,
}: AboutThisScreenProps) {
  const paragraph = useGuideParagraph(surface)
  const reduced = usePrefersReducedMotion()
  const [dismissed, setDismissed] = useState(() => isAboutDismissed(projectId, surface))
  const [open, setOpen] = useState(false)

  // A different project (or, in principle, a different surface reusing the
  // same mounted component) re-derives dismissal rather than carrying the
  // previous project's over.
  useEffect(() => {
    setDismissed(isAboutDismissed(projectId, surface))
    setOpen(false)
  }, [projectId, surface])

  if (!paragraph) return null

  if (alwaysExpanded) {
    return (
      <Typography
        variant="body2"
        color="text.secondary"
        sx={[{ mb: 2 }, ...(Array.isArray(sx) ? sx : [sx])]}
        data-testid={`about-${surface}`}
      >
        {paragraph.text}
      </Typography>
    )
  }

  if (dismissed) {
    return (
      <Link
        component="button"
        type="button"
        variant="caption"
        color="text.secondary"
        underline="hover"
        data-testid={`about-restore-${surface}`}
        sx={sx}
        onClick={() => {
          restoreAbout(projectId, surface)
          setDismissed(false)
        }}
      >
        About this screen
      </Link>
    )
  }

  return (
    <Box sx={[{ mb: 2 }, ...(Array.isArray(sx) ? sx : [sx])]} data-testid={`about-${surface}`}>
      <Stack
        direction="row"
        spacing={0.5}
        alignItems="center"
        onClick={() => setOpen((o) => !o)}
        sx={{ cursor: 'pointer', width: 'fit-content' }}
        data-testid={`about-toggle-${surface}`}
      >
        <Typography variant="body2" color="text.secondary">
          About this screen
        </Typography>
        <ChevronRightIcon
          fontSize="small"
          sx={{
            color: 'text.secondary',
            transform: open ? 'rotate(90deg)' : 'none',
            transition: reduced ? 'none' : 'transform 180ms ease-out',
          }}
        />
      </Stack>
      <Collapse in={open} timeout={reduced ? 0 : 180} unmountOnExit>
        <Box sx={{ pt: 1 }}>
          <Typography variant="body2" color="text.secondary">
            {paragraph.text}
          </Typography>
          <Stack direction="row" spacing={2} sx={{ mt: 0.5 }}>
            <Link href={buildGuideHash(paragraph.slug)} variant="caption">
              Read more in the guide →
            </Link>
            <Link
              component="button"
              type="button"
              variant="caption"
              color="text.secondary"
              underline="hover"
              data-testid={`about-dismiss-${surface}`}
              onClick={() => {
                dismissAbout(projectId, surface)
                setDismissed(true)
                setOpen(false)
              }}
            >
              Dismiss
            </Link>
          </Stack>
        </Box>
      </Collapse>
    </Box>
  )
}
