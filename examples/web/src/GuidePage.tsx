// The guide, served inside the product (design 2026-09-11-onboarding-and-the-guide.md
// §3 G7, work plan ticket C1).
//
// Deep-linkable at `#/guide/<slug>` (App.tsx owns reading/writing that hash;
// this component only reads the `slug` it is handed and, for the bare
// index, redirects to the first page). No server, no CMS: the pages are
// generated at build time from `docs/guide/*.md` by
// `scripts/build-guide.mjs` into `./guide/pages.generated.ts`, which is
// gitignored — a fresh checkout with no `docs/guide/` yet (true of this
// worktree today) builds an empty list, and this page says so rather than
// rendering a blank screen.
//
// Same theme, same type system as the rest of the shell (§3.4 of the console
// design): prose in the content face (`typography.fontFamily`), identifiers
// — code spans and blocks — in the identifier face (`theme.monoFontFamily`),
// hairlines rather than shadows, prose capped at ~68ch so a page reads like a
// page and not a fluid web app.
import { useEffect, useMemo } from "react";
import { Box, Typography, useTheme } from "@mui/material";
import { GUIDE_PART_LABELS, buildGuideHash } from "@agentkit/chat-ui";
import { GUIDE_PAGES, type GuidePage as GuidePageEntry } from "./guide/pages.generated.js";

export default function GuidePage({ slug }: { slug: string | null }) {
  const theme = useTheme();

  const byPart = useMemo(() => groupByPart(GUIDE_PAGES), []);

  // The bare index ("#/guide", slug === "") has no page of its own — render
  // the first page in canonical order rather than an empty pane, and rewrite
  // the address bar to name it (a history REPLACE, not a push: arriving at
  // the index and immediately landing on page one should not cost the reader
  // a back-button step). App.tsx's own hash state stays "" until the reader
  // navigates again, which is harmless — this component never reads that
  // state back, only the `slug` prop, so the two cannot disagree about what
  // is on screen.
  const firstSlug = GUIDE_PAGES[0]?.slug ?? null;
  const effectiveSlug = slug === "" ? firstSlug : slug;
  useEffect(() => {
    if (slug === "" && firstSlug) {
      window.history.replaceState(null, "", window.location.pathname + window.location.search + buildGuideHash(firstSlug));
    }
  }, [slug, firstSlug]);

  const current = effectiveSlug ? GUIDE_PAGES.find((p) => p.slug === effectiveSlug) ?? null : null;

  if (GUIDE_PAGES.length === 0) {
    return (
      <Box sx={{ p: 4 }}>
        <Typography variant="h5" sx={{ mb: 1 }}>Guide</Typography>
        <Typography color="text.secondary">The guide has not been written yet.</Typography>
      </Box>
    );
  }

  return (
    <Box sx={{ display: "flex", height: "100%", minHeight: 0 }}>
      <Box
        component="nav"
        aria-label="Guide contents"
        sx={{
          width: 260,
          flexShrink: 0,
          borderRight: 1,
          borderColor: "divider",
          overflowY: "auto",
          p: 2,
        }}
      >
        {byPart.map(({ part, pages }) => (
          <Box key={part} sx={{ mb: 2.5 }}>
            <Typography
              sx={{
                fontSize: 11,
                textTransform: "uppercase",
                letterSpacing: "0.08em",
                color: "text.secondary",
                mb: 0.75,
              }}
            >
              {GUIDE_PART_LABELS[part] ?? `Part ${part}`}
            </Typography>
            {pages.map((page) => (
              <Box
                key={page.slug}
                component="a"
                href={buildGuideHash(page.slug)}
                data-testid={`guide-nav-${page.slug}`}
                sx={{
                  display: "block",
                  fontSize: "0.875rem",
                  lineHeight: 1.7,
                  textDecoration: "none",
                  color: page.slug === current?.slug ? "text.primary" : "text.secondary",
                  fontWeight: page.slug === current?.slug ? 600 : 400,
                  "&:hover": { color: "text.primary" },
                }}
              >
                {page.title}
              </Box>
            ))}
          </Box>
        ))}
      </Box>

      <Box sx={{ flex: 1, minWidth: 0, overflowY: "auto", p: 4 }}>
        {current ? (
          <GuideArticle page={current} monoFontFamily={theme.monoFontFamily} />
        ) : slug ? (
          <NoSuchPage slug={slug} pages={GUIDE_PAGES} />
        ) : null}
      </Box>
    </Box>
  );
}

function GuideArticle({ page, monoFontFamily }: { page: GuidePageEntry; monoFontFamily: string }) {
  return (
    <Box sx={{ maxWidth: "68ch" }}>
      <Typography variant="h5" sx={{ mb: 2 }}>{page.title}</Typography>
      <Box
        data-testid="guide-article-body"
        // The HTML is generated at build time from docs/guide/*.md by our own
        // script (build-guide.mjs), never from a user or a server response —
        // there is no user-controlled input in this path.
        dangerouslySetInnerHTML={{ __html: page.html }}
        sx={{
          fontFamily: "inherit", // the content face — inherited from the theme's typography.fontFamily
          lineHeight: 1.6,
          "& h1, & h2, & h3": { fontWeight: 600, letterSpacing: "-0.01em", mt: 3, mb: 1 },
          "& p, & ul, & ol, & blockquote": { mb: 1.5 },
          "& code": {
            fontFamily: monoFontFamily,
            fontSize: "0.85em",
            bgcolor: "action.hover",
            borderRadius: "2px",
            padding: "0 4px",
          },
          "& pre": {
            fontFamily: monoFontFamily,
            fontSize: "0.8125rem",
            overflowX: "auto",
            p: 1.5,
            border: 1,
            borderColor: "divider",
            borderRadius: "2px",
          },
          "& pre code": { padding: 0, background: "none" },
          "& blockquote": {
            m: 0,
            pl: 2,
            borderLeft: 2,
            borderColor: "divider",
            color: "text.secondary",
          },
          "& a": { color: "primary.main" },
          "& hr": { border: 0, borderTop: 1, borderColor: "divider", my: 3 },
          "& table": { borderCollapse: "collapse" },
          "& th, & td": { border: 1, borderColor: "divider", padding: "4px 8px" },
          "& img": { maxWidth: "100%" },
        }}
      />
    </Box>
  );
}

function NoSuchPage({ slug, pages }: { slug: string; pages: GuidePageEntry[] }) {
  return (
    <Box>
      <Typography sx={{ mb: 2 }}>No such page: <code>{slug}</code></Typography>
      <Typography sx={{ mb: 1, color: "text.secondary" }}>Pages in the guide:</Typography>
      {pages.map((page) => (
        <Box key={page.slug} component="a" href={buildGuideHash(page.slug)} sx={{ display: "block", mb: 0.5 }}>
          {page.title}
        </Box>
      ))}
    </Box>
  );
}

function groupByPart(pages: GuidePageEntry[]): { part: number; pages: GuidePageEntry[] }[] {
  const parts = new Map<number, GuidePageEntry[]>();
  for (const page of pages) {
    const list = parts.get(page.part) ?? [];
    list.push(page);
    parts.set(page.part, list);
  }
  // GUIDE_PAGES already arrives sorted by part then order (build-guide.mjs);
  // Map preserves first-insertion order, which is therefore already correct.
  return [...parts.entries()].map(([part, pagesInPart]) => ({ part, pages: pagesInPart }));
}
