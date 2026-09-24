import { Box, Link, Typography } from '@mui/material'
import Markdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'

// A RELEASE BODY IS MARKDOWN WRITTEN FOR A GITHUB RELEASE PAGE — a heading, one
// bulleted pull request per line, a bold "Full Changelog" line — and GitHub
// autolinks its bare URLs, which is why GFM is on. Shown as plain text the
// operator reads the syntax instead of the notes.
//
// IT IS STILL TEXT FROM ANOTHER REPOSITORY, so it renders as data, never as page
// markup: raw HTML is dropped (skipHtml), a link is kept only for an absolute
// http(s) target, and an image is shown as its alt text, because fetching it would
// report the operator's address to whatever host the body names.

const allowedProtocols = new Set(['http:', 'https:'])

function safeURL(url: string): string | undefined {
  try {
    const parsed = new URL(url)
    return allowedProtocols.has(parsed.protocol) ? parsed.href : undefined
  } catch {
    return undefined
  }
}

// Headings are demoted to one small size: the dialog title is the page's
// heading, and a release body's "##" would otherwise render larger than it.
const heading: Components['h1'] = ({ children }) =>
  <Typography component="h4" variant="subtitle2" sx={{ mt: 1, '&:first-of-type': { mt: 0 } }}>{children}</Typography>

const components: Components = {
  h1: heading, h2: heading, h3: heading, h4: heading, h5: heading, h6: heading,
  p: ({ children }) => <Typography variant="body2" sx={{ my: 0.5 }}>{children}</Typography>,
  ul: ({ children }) => <Box component="ul" sx={{ my: 0.5, pl: 2.5 }}>{children}</Box>,
  ol: ({ children }) => <Box component="ol" sx={{ my: 0.5, pl: 2.5 }}>{children}</Box>,
  li: ({ children }) => <Typography component="li" variant="body2" sx={{ my: 0.25 }}>{children}</Typography>,
  a: ({ href, children }) => href
    ? <Link href={href} target="_blank" rel="noopener noreferrer">{children}</Link>
    : <>{children}</>,
  img: ({ alt }) => alt ? <>{alt}</> : null,
}

export default function ReleaseNotes({ children }: { children: string }) {
  return <Box sx={{ overflowWrap: 'anywhere' }}>
    <Markdown remarkPlugins={[remarkGfm]} skipHtml components={components} urlTransform={url => safeURL(url)}>
      {children}
    </Markdown>
  </Box>
}
