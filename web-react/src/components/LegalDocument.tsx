import { Box, Link, Typography } from '@mui/material'
import type { Components } from 'react-markdown'
import type { LegalPublicDocument } from '@/api/legal'
import SafeMarkdown from './SafeMarkdown'
import DataCollectionCard from './DataCollectionCard'

const heading: Components['h1'] = ({ children }) => <Typography component="h2" sx={{ fontSize: 21, fontWeight: 600, mt: 3, mb: 1 }}>{children}</Typography>
const components: Components = {
  h1: heading, h2: heading, h3: heading, h4: heading, h5: heading, h6: heading,
  p: ({ children }) => <Typography component="p" sx={{ fontSize: 15, lineHeight: 1.75, my: 1.5 }}>{children}</Typography>,
  a: ({ href, children }) => href ? <Link href={href} target="_blank" rel="noopener noreferrer" sx={{ display: 'inline-flex', alignItems: 'center', minHeight: 44 }}>{children}</Link> : <>{children}</>,
  img: ({ alt }) => alt ? <>{alt}</> : null,
  table: ({ children }) => <Box sx={{ overflowX: 'auto' }}><Box component="table" sx={{ borderCollapse: 'collapse', '& th, & td': { p: 1, borderBottom: '1px solid', borderColor: 'divider' } }}>{children}</Box></Box>,
}

// Only a standalone marker outside fenced code inserts the collection card.
// Text examples, inline code and fenced examples retain their original text.
export function splitCollectionMarker(content: string): [string, string] | null {
  const lines = content.split(/\r?\n/)
  let fence: { char: string; length: number } | null = null
  for (let i = 0; i < lines.length; i++) {
    const token = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(lines[i])
    if (token) {
      if (!fence) fence = { char: token[1][0], length: token[1].length }
      else if (token[1][0] === fence.char && token[1].length >= fence.length && token[2].trim() === '') fence = null
      continue
    }
    if (!fence && lines[i].trim() === '[[data-collection]]' && !/^ {4}|^\t/.test(lines[i])) {
      return [lines.slice(0, i).join('\n'), lines.slice(i + 1).join('\n')]
    }
  }
  return null
}

export default function LegalDocument({ document }: { document: LegalPublicDocument }) {
  const parts = splitCollectionMarker(document.content)
  return <Box sx={{ overflowWrap: 'anywhere', '& pre': { overflowX: 'auto' }, '& li': { fontSize: 15, lineHeight: 1.75 } }}>
    {parts ? <>
      <SafeMarkdown components={components}>{parts[0]}</SafeMarkdown>
      <DataCollectionCard data={document.data_collection} />
      <SafeMarkdown components={components}>{parts[1]}</SafeMarkdown>
    </> : <SafeMarkdown components={components}>{document.content}</SafeMarkdown>}
  </Box>
}
