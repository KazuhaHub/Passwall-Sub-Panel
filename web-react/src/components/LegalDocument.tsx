import { Fragment, useMemo } from 'react'
import { Box, Typography, useTheme } from '@mui/material'
import Markdown, { type Components } from 'react-markdown'
import { useTranslation } from 'react-i18next'

import { DATA_COLLECTION_MARKER, type DataCollection } from '@/api/legal'
import DataCollectionCard from './DataCollectionCard'
import { legalLinkComponents, legalMarkdownProps, markdownBlocks, markdownLists } from './ReleaseNotes'

// The marker is a line of its own, as the editor inserts it.
const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
const MARKER_LINE = new RegExp(`^[ \\t]*${escapeRegExp(DATA_COLLECTION_MARKER)}[ \\t]*$`, 'm')

/** The text cut at every marker line; one more segment than markers. */
export function splitAtMarker(content: string): string[] {
  const out: string[] = []
  let rest = content
  for (;;) {
    const m = MARKER_LINE.exec(rest)
    if (!m) break
    out.push(rest.slice(0, m.index))
    rest = rest.slice(m.index + m[0].length)
  }
  out.push(rest)
  return out
}

// The document is the first-class text of its page, so its headings keep their
// sizes (unlike a release note's). The page's own title is the h1: the text's
// "#" is drawn as a section heading.
const sized = (variant: 'h2' | 'h3' | 'h4', size: number): Components['h1'] => ({ children }) =>
  <Typography component={variant} sx={{ fontSize: size, fontWeight: 600, mt: 3, mb: 1, lineHeight: 1.4 }}>{children}</Typography>

const components: Components = {
  h1: sized('h2', 22), h2: sized('h3', 18), h3: sized('h4', 16), h4: sized('h4', 15), h5: sized('h4', 15), h6: sized('h4', 15),
  p: ({ children }) => <Typography sx={{ fontSize: 15, lineHeight: 1.75, my: 1 }}>{children}</Typography>,
  ...markdownLists,
  ...markdownBlocks,
  li: ({ children }) => <Typography component="li" sx={{ fontSize: 15, lineHeight: 1.75 }}>{children}</Typography>,
  ...legalLinkComponents,
}

/**
 * A published terms or privacy document: safe Markdown (the release notes'
 * rules: no raw HTML, http(s) and mailto links only, images as alt text), cut at the
 * [[data-collection]] line so the generated list sits where the author put it.
 * `dataCollection` null is the editor preview before there is anything to list.
 */
export default function LegalDocument({ content, dataCollection }: { content: string; dataCollection: DataCollection | null | undefined }) {
  const md = useTheme().palette.md
  const { t } = useTranslation('auth')
  const parts = useMemo(() => splitAtMarker(content), [content])
  return (
    <Box sx={{
      overflowWrap: 'anywhere', color: md.onSurface,
      '& table': { borderCollapse: 'collapse', my: 1, display: 'block', overflowX: 'auto' },
      '& th, & td': { border: `1px solid ${md.outlineVariant}`, px: 1, py: 0.5, fontSize: 14 },
      '& blockquote': { m: 0, my: 1, pl: 2, borderLeft: `3px solid ${md.outlineVariant}`, color: md.onSurfaceVariant },
      '& hr': { border: 0, borderTop: `1px solid ${md.outlineVariant}`, my: 2 },
      '& code': { fontFamily: 'monospace', fontSize: 13 },
    }}>
      {parts.map((part, i) => (
        <Fragment key={i}>
          {i > 0 && (dataCollection
            ? <DataCollectionCard data={dataCollection} />
            : <Typography sx={{ my: 2, p: 2, borderRadius: 3, fontSize: 14, bgcolor: md.surfaceContainer, color: md.onSurfaceVariant }}>
              {t('legal.collect_placeholder')}
            </Typography>)}
          {part.trim() && <Markdown {...legalMarkdownProps} components={components}>{part}</Markdown>}
        </Fragment>
      ))}
    </Box>
  )
}
