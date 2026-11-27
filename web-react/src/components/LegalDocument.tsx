import { Fragment, useMemo } from 'react'
import Markdown, { type Components } from 'react-markdown'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'
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
const sized = (Tag: 'h2' | 'h3' | 'h4', size: string): Components['h1'] => ({ children }) =>
  <Tag className={cn('mt-6 mb-2 leading-snug font-semibold', size)}>{children}</Tag>

const components: Components = {
  h1: sized('h2', 'text-[22px]'), h2: sized('h3', 'text-lg'), h3: sized('h4', 'text-base'), h4: sized('h4', 'text-[15px]'), h5: sized('h4', 'text-[15px]'), h6: sized('h4', 'text-[15px]'),
  p: ({ children }) => <p className="my-2 text-[15px] leading-[1.75]">{children}</p>,
  ...markdownLists,
  ...markdownBlocks,
  li: ({ children }) => <li className="text-[15px] leading-[1.75]">{children}</li>,
  ...legalLinkComponents,
}

/**
 * A published terms or privacy document: safe Markdown (the release notes'
 * rules: no raw HTML, http(s) and mailto links only, images as alt text), cut at the
 * [[data-collection]] line so the generated list sits where the author put it.
 * `dataCollection` null is the editor preview before there is anything to list.
 */
export default function LegalDocument({ content, dataCollection }: { content: string; dataCollection: DataCollection | null | undefined }) {
  const { t } = useTranslation('auth')
  const parts = useMemo(() => splitAtMarker(content), [content])
  // Only theme tokens, no layout of its own: it sits in the public page's column and in the editor's preview pane alike.
  return (
    <div data-slot="legal-document" className="text-foreground [overflow-wrap:anywhere]">
      {parts.map((part, i) => (
        <Fragment key={i}>
          {i > 0 && (dataCollection
            ? <DataCollectionCard data={dataCollection} />
            : <p className="my-4 rounded-xl bg-muted p-4 text-sm text-muted-foreground">{t('legal.collect_placeholder')}</p>)}
          {part.trim() && <Markdown {...legalMarkdownProps} components={components}>{part}</Markdown>}
        </Fragment>
      ))}
    </div>
  )
}
