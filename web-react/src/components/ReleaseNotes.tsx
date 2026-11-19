import { Box, Link, Typography } from '@mui/material'
import type { ReactNode } from 'react'
import Markdown, { type Components, type Options } from 'react-markdown'
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

export function safeURL(url: string, protocols: ReadonlySet<string> = allowedProtocols): string | undefined {
  try {
    const parsed = new URL(url)
    return protocols.has(parsed.protocol) ? parsed.href : undefined
  } catch {
    return undefined
  }
}

// A policy ends with a contact address, so the legal pages also keep a mailto:
// link (an operator's own text, not a release body from another repository).
// Still nothing script-like: javascript:, data: and the rest stay plain text.
const legalProtocols = new Set([...allowedProtocols, 'mailto:'])

// What every safe rendering of untrusted Markdown shares: GFM, no raw HTML, a link
// only for http(s), and a link element that opens a new window without leaking
// the opener. The legal pages build their own typography on top of it, so the
// rules that decide what may be rendered live in one place.
export const safeMarkdownProps: Pick<Options, 'remarkPlugins' | 'skipHtml' | 'urlTransform'> = {
  remarkPlugins: [remarkGfm],
  skipHtml: true,
  urlTransform: url => safeURL(url),
}

export const legalMarkdownProps: typeof safeMarkdownProps = {
  ...safeMarkdownProps,
  urlTransform: url => safeURL(url, legalProtocols),
}

export const safeLinkComponents: Pick<Components, 'a' | 'img'> = {
  a: ({ href, children }) => href
    ? <Link href={href} target="_blank" rel="noopener noreferrer">{children}</Link>
    : <>{children}</>,
  img: ({ alt }) => alt ? <>{alt}</> : null,
}

// A mailto: link opens the mail client in place; only a web link gets a new window.
export const legalLinkComponents: typeof safeLinkComponents = {
  ...safeLinkComponents,
  a: ({ href, children }) => href?.startsWith('mailto:')
    ? <Link href={href}>{children}</Link>
    : href
      ? <Link href={href} target="_blank" rel="noopener noreferrer">{children}</Link>
      : <>{children}</>,
}

// Tailwind's reset (global now) strips what a browser gives these elements by
// default: list markers and indentation, block-quote and code-block styling, and
// table borders and padding. In a terms document the numbering IS content
// ("clause 3"), so both markdown renderers put it back here, in one place.
export const markdownLists: Pick<Components, 'ul' | 'ol'> = {
  ul: ({ children }) => <Box component="ul" sx={{ my: 0.5, pl: 3, listStyleType: 'disc', '& ul': { listStyleType: 'circle' }, '& ol, & ul': { my: 0 } }}>{children}</Box>,
  ol: ({ children }) => <Box component="ol" sx={{ my: 0.5, pl: 3, listStyleType: 'decimal', '& ol': { listStyleType: 'lower-alpha' }, '& ol, & ul': { my: 0 } }}>{children}</Box>,
}

export const markdownBlocks: Pick<Components, 'blockquote' | 'pre' | 'code' | 'table' | 'th' | 'td' | 'hr'> = {
  blockquote: ({ children }: { children?: ReactNode }) =>
    <Box component="blockquote" sx={{ my: 1, mx: 0, pl: 2, py: 0.25, borderLeft: 3, borderColor: 'divider', borderStyle: 'solid', borderWidth: '0 0 0 3px', color: 'text.secondary' }}>{children}</Box>,
  pre: ({ children }: { children?: ReactNode }) =>
    <Box component="pre" sx={{ my: 1, p: 1.5, overflow: 'auto', whiteSpace: 'pre', borderRadius: 1, bgcolor: 'action.hover', fontSize: 13, '& code': { bgcolor: 'transparent', p: 0 } }}>{children}</Box>,
  code: ({ children }: { children?: ReactNode }) =>
    <Box component="code" sx={{ px: 0.5, borderRadius: 0.5, bgcolor: 'action.hover', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: '0.9em' }}>{children}</Box>,
  table: ({ children }: { children?: ReactNode }) =>
    <Box sx={{ my: 1, overflowX: 'auto' }}><Box component="table" sx={{ borderCollapse: 'collapse' }}>{children}</Box></Box>,
  th: ({ children }: { children?: ReactNode }) =>
    <Box component="th" sx={{ border: 1, borderColor: 'divider', borderStyle: 'solid', px: 1, py: 0.5, textAlign: 'left', fontWeight: 600 }}>{children}</Box>,
  td: ({ children }: { children?: ReactNode }) =>
    <Box component="td" sx={{ border: 1, borderColor: 'divider', borderStyle: 'solid', px: 1, py: 0.5 }}>{children}</Box>,
  hr: () => <Box component="hr" sx={{ my: 1.5, border: 0, borderTop: 1, borderColor: 'divider', borderStyle: 'solid', borderWidth: '1px 0 0' }} />,
}

// Headings are demoted to one small size: the dialog title is the page's
// heading, and a release body's "##" would otherwise render larger than it.
const heading: Components['h1'] = ({ children }) =>
  <Typography component="h4" variant="subtitle2" sx={{ mt: 1, '&:first-of-type': { mt: 0 } }}>{children}</Typography>

const components: Components = {
  h1: heading, h2: heading, h3: heading, h4: heading, h5: heading, h6: heading,
  p: ({ children }) => <Typography variant="body2" sx={{ my: 0.5 }}>{children}</Typography>,
  ...markdownLists,
  ...markdownBlocks,
  li: ({ children }) => <Typography component="li" variant="body2" sx={{ my: 0.25 }}>{children}</Typography>,
  ...safeLinkComponents,
}

export default function ReleaseNotes({ children }: { children: string }) {
  return <Box sx={{ overflowWrap: 'anywhere' }}>
    <Markdown {...safeMarkdownProps} components={components}>
      {children}
    </Markdown>
  </Box>
}
