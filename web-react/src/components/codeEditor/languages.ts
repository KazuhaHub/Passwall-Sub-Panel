import { StreamLanguage, type StreamParser } from '@codemirror/language'
import { yaml } from '@codemirror/lang-yaml'
import { tags, type Tag } from '@lezer/highlight'
import type { Extension } from '@codemirror/state'
import { yamlScalars } from './scalars'

// @codemirror/lang-json and the legacy modes are not installed, so JSON, PEM and Markdown are small
// StreamLanguages written here: each is a line tokenizer of a few lines, with no parse tree, which
// is all a highlighter needs. (Valid JSON is also valid YAML, but YAML mode paints JSON keys as YAML
// keys and cannot tell a key from a value in `{"a": "b"}` the way VS Code's JSON does.)
export type EditorLanguage = 'yaml' | 'json' | 'markdown' | 'pem' | 'plain'

// Token names a stream parser may return, mapped to tags in `tokenTable`.
const TABLE: Record<string, Tag> = {
  jsonKey: tags.propertyName,
  str: tags.string,
  num: tags.number,
  bool: tags.bool,
  nul: tags.null,
  brace: tags.brace,
  bracket: tags.squareBracket,
  sep: tags.separator,
  pemMarker: tags.keyword,
  pemBody: tags.string,
  mdHeading: tags.heading,
  mdQuote: tags.quote,
  mdList: tags.list,
  mdCode: tags.monospace,
  mdStrong: tags.strong,
  mdEm: tags.emphasis,
  mdLink: tags.link,
}

const json: StreamParser<null> = {
  name: 'json',
  startState: () => null,
  tokenTable: TABLE,
  token(stream) {
    if (stream.eatSpace()) return null
    const ch = stream.peek()
    if (ch === '"') {
      stream.next()
      let escaped = false
      for (let c = stream.next(); c != null; c = stream.next()) {
        if (escaped) escaped = false
        else if (c === '\\') escaped = true
        else if (c === '"') break
      }
      // A string followed by a colon is a key; the lookahead does not consume it.
      return stream.match(/^\s*:/, false) ? 'jsonKey' : 'str'
    }
    if (stream.match(/^-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?/)) return 'num'
    if (stream.match(/^(?:true|false)\b/)) return 'bool'
    if (stream.match(/^null\b/)) return 'nul'
    stream.next()
    if (ch === '{' || ch === '}') return 'brace'
    if (ch === '[' || ch === ']') return 'bracket'
    if (ch === ',' || ch === ':') return 'sep'
    return null
  },
}

const pem: StreamParser<null> = {
  name: 'pem',
  startState: () => null,
  tokenTable: TABLE,
  token(stream) {
    if (stream.sol() && stream.match(/^-----(?:BEGIN|END) [A-Z0-9 ]+-----/)) return 'pemMarker'
    stream.skipToEnd()
    return 'pemBody'
  },
}

// Enough Markdown for the legal documents: headings, quotes, lists, fences, code spans, emphasis, links.
const markdown: StreamParser<{ fence: boolean }> = {
  name: 'markdown',
  startState: () => ({ fence: false }),
  tokenTable: TABLE,
  token(stream, state) {
    if (stream.sol()) {
      if (stream.match(/^\s*(?:```|~~~).*/)) { state.fence = !state.fence; return 'mdCode' }
      if (state.fence) { stream.skipToEnd(); return 'mdCode' }
      if (stream.match(/^#{1,6}\s.*/)) return 'mdHeading'
      if (stream.match(/^\s*>\s?/)) return 'mdQuote'
      if (stream.match(/^\s*(?:[-*+]|\d+[.)])\s/)) return 'mdList'
    } else if (state.fence) { stream.skipToEnd(); return 'mdCode' }
    if (stream.match(/^`[^`\n]+`/)) return 'mdCode'
    if (stream.match(/^\*\*[^*\n]+\*\*/)) return 'mdStrong'
    if (stream.match(/^\*[^*\s][^*\n]*\*/)) return 'mdEm'
    if (stream.match(/^\[[^\]\n]*\]\([^)\n]*\)/)) return 'mdLink'
    // Plain text up to the next character that could start something above.
    stream.next()
    stream.eatWhile(/[^`*\[]/)
    return null
  },
}

const jsonLanguage = StreamLanguage.define(json)
const pemLanguage = StreamLanguage.define(pem)
const markdownLanguage = StreamLanguage.define(markdown)

/** The extensions that make `language` readable; 'plain' has none by design (a list is not YAML). */
export function languageExtensions(language: EditorLanguage): Extension[] {
  switch (language) {
    case 'yaml': return [yaml(), yamlScalars]
    case 'json': return [jsonLanguage]
    case 'pem': return [pemLanguage]
    case 'markdown': return [markdownLanguage]
    default: return []
  }
}
