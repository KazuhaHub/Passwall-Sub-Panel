import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

/**
 * The `content` of the seeded default rule set, read from the Go source tree (the file the panel ships): the real
 * ~9.5k-rule document the editor has to handle. The YAML block scalar is un-indented by hand, which is all this
 * file needs (a `|` block with the default clip chomping: every line, one trailing newline).
 */
export function defaultRulesContent(): string {
  const path = fileURLToPath(new URL('../../../../internal/seed/files/rulesets/default-rules.yaml', import.meta.url))
  const lines = readFileSync(path, 'utf8').split('\n')
  const start = lines.findIndex(l => l === 'content: |')
  if (start < 0) throw new Error('seed rule set has no content block')
  const body = lines.slice(start + 1)
  while (body.length > 0 && body[body.length - 1] === '') body.pop()
  return body.map(l => (l.startsWith('  ') ? l.slice(2) : l)).join('\n') + '\n'
}
