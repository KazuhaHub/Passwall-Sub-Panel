import type { ReactNode } from 'react'
import Markdown, { type Components, type Options } from 'react-markdown'
import remarkGfm from 'remark-gfm'

import { cn } from '@/lib/utils'

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

const LINK = 'text-primary underline underline-offset-2 hover:no-underline'

export const safeLinkComponents: Pick<Components, 'a' | 'img'> = {
  a: ({ href, children }) => href
    ? <a href={href} target="_blank" rel="noopener noreferrer" className={LINK}>{children}</a>
    : <>{children}</>,
  img: ({ alt }) => alt ? <>{alt}</> : null,
}

// A mailto: link opens the mail client in place; only a web link gets a new window.
export const legalLinkComponents: typeof safeLinkComponents = {
  ...safeLinkComponents,
  a: ({ href, children }) => href?.startsWith('mailto:')
    ? <a href={href} className={LINK}>{children}</a>
    : href
      ? <a href={href} target="_blank" rel="noopener noreferrer" className={LINK}>{children}</a>
      : <>{children}</>,
}

// Tailwind's reset (global) strips what a browser gives these elements by
// default: list markers and indentation, block-quote and code-block styling, and
// table borders and padding. In a terms document the numbering IS content
// ("clause 3"), so both markdown renderers put it back here, in one place.
//
// A GFM task list (`- [x] done`) is an ul (or ol) that react-markdown tags
// `contains-task-list`, each item holding a disabled checkbox: the checkbox is the
// marker, so a bullet beside it is a second one. Such a list drops its marker and its
// indent; the items of an ordinary list around it keep theirs.
const isTaskList = (className?: string) => className?.split(' ').includes('contains-task-list') === true
const TASK_LIST = 'list-none pl-1 [&_input]:mr-1.5 [&_input]:align-middle'

export const markdownLists: Pick<Components, 'ul' | 'ol'> = {
  ul: ({ children, className }) => <ul className={cn('my-1 list-disc pl-6 [&_ol]:my-0 [&_ul]:my-0 [&_ul]:list-[circle]', isTaskList(className) && TASK_LIST)}>{children}</ul>,
  // `start` is what makes "3. third" render as 3, not 1: dropping it renumbered a list that continues across a block.
  ol: ({ children, className, start }) => <ol start={start ?? undefined} className={cn('my-1 list-decimal pl-6 [&_ol]:list-[lower-alpha] [&_ol]:my-0 [&_ul]:my-0', isTaskList(className) && TASK_LIST)}>{children}</ol>,
}

// GFM column alignment (`:-:`, `-:`) arrives as a style prop on th and td; the cell
// keeps its own default (left for a header, the table's for a body cell) otherwise.
type Align = 'left' | 'center' | 'right'
const alignOf = (style?: { textAlign?: string }): Align | undefined =>
  style?.textAlign === 'left' || style?.textAlign === 'center' || style?.textAlign === 'right' ? style.textAlign : undefined
// The alignment goes on as a class, not an inline style, and only for the three values above: nothing else from the document reaches the DOM.
const ALIGN: Record<Align, string> = { left: 'text-left', center: 'text-center', right: 'text-right' }

export const markdownBlocks: Pick<Components, 'blockquote' | 'pre' | 'code' | 'table' | 'th' | 'td' | 'hr'> = {
  blockquote: ({ children }: { children?: ReactNode }) =>
    <blockquote className="my-2 border-l-[3px] border-border py-0.5 pl-4 text-muted-foreground">{children}</blockquote>,
  pre: ({ children }: { children?: ReactNode }) =>
    <pre className="my-2 overflow-auto rounded-md bg-muted p-3 text-[13px] whitespace-pre [&_code]:bg-transparent [&_code]:p-0">{children}</pre>,
  code: ({ children }: { children?: ReactNode }) =>
    <code className="rounded-sm bg-muted px-1 font-mono text-[0.9em]">{children}</code>,
  table: ({ children }: { children?: ReactNode }) =>
    <div className="my-2 overflow-x-auto"><table className="border-collapse">{children}</table></div>,
  th: ({ children, style }: { children?: ReactNode; style?: { textAlign?: string } }) =>
    <th className={cn('border border-border px-2 py-1 font-semibold', ALIGN[alignOf(style) ?? 'left'])}>{children}</th>,
  td: ({ children, style }: { children?: ReactNode; style?: { textAlign?: string } }) =>
    <td className={cn('border border-border px-2 py-1', alignOf(style) && ALIGN[alignOf(style)!])}>{children}</td>,
  hr: () => <hr className="my-3 border-0 border-t border-border" />,
}

// Headings are demoted to one small size: the sheet's title is the page's
// heading, and a release body's "##" would otherwise render larger than it.
const heading: Components['h1'] = ({ children }) =>
  <h4 className="mt-2 text-sm font-medium first-of-type:mt-0">{children}</h4>

const components: Components = {
  h1: heading, h2: heading, h3: heading, h4: heading, h5: heading, h6: heading,
  p: ({ children }) => <p className="my-1 text-sm">{children}</p>,
  ...markdownLists,
  ...markdownBlocks,
  li: ({ children }) => <li className="my-0.5 text-sm">{children}</li>,
  ...safeLinkComponents,
}

export default function ReleaseNotes({ children }: { children: string }) {
  return <div className="[overflow-wrap:anywhere]">
    <Markdown {...safeMarkdownProps} components={components}>
      {children}
    </Markdown>
  </div>
}
