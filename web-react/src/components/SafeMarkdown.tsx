import Markdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'

function safeURL(url: string): string | undefined {
  try {
    const parsed = new URL(url)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : undefined
  } catch {
    return undefined
  }
}

// Shared policy for administrator-authored and externally sourced Markdown.
// Raw HTML is dropped. Callers render images as alt text and links with
// noopener/noreferrer so viewing text never fetches an embedded remote image.
export default function SafeMarkdown({ children, components }: { children: string; components: Components }) {
  return <Markdown remarkPlugins={[remarkGfm]} skipHtml components={components} urlTransform={safeURL}>{children}</Markdown>
}
