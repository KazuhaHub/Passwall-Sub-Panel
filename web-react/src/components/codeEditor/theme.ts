import { syntaxHighlighting } from '@codemirror/language'
import type { Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { tagHighlighter, tags } from '@lezer/highlight'
import type { EditorPalette } from './vscodePalette'
import { DARK_PLUS, LIGHT_PLUS } from './vscodePalette'

// Syntax tokens get STABLE class names (`psp-tok-*`) from a tag highlighter, and each theme colours
// those classes. That keeps the grammar -> role mapping in one place, the colours in the palette, and
// lets a test (or devtools) see which role a token got without reading generated hashes.
// Order is irrelevant: the most specific tag wins, so `definition(propertyName)` (a YAML key) beats
// `propertyName` (a JSON key) without a rule being placed first.
export const tokenHighlighter = tagHighlighter([
  { tag: tags.comment, class: 'psp-tok-comment' },
  { tag: tags.keyword, class: 'psp-tok-keyword' },
  { tag: tags.controlKeyword, class: 'psp-tok-control' },
  { tag: tags.definition(tags.propertyName), class: 'psp-tok-yaml-key' },
  { tag: tags.propertyName, class: 'psp-tok-json-key' },
  { tag: [tags.string, tags.special(tags.string), tags.attributeValue], class: 'psp-tok-string' },
  { tag: tags.number, class: 'psp-tok-number' },
  { tag: [tags.bool, tags.null], class: 'psp-tok-const' },
  { tag: tags.labelName, class: 'psp-tok-anchor' },
  { tag: tags.typeName, class: 'psp-tok-tag' },
  { tag: [tags.separator, tags.brace, tags.squareBracket, tags.punctuation, tags.meta], class: 'psp-tok-punct' },
  { tag: tags.heading, class: 'psp-tok-heading' },
  { tag: tags.quote, class: 'psp-tok-quote' },
  { tag: tags.list, class: 'psp-tok-list' },
  { tag: tags.monospace, class: 'psp-tok-code' },
  { tag: tags.strong, class: 'psp-tok-strong' },
  { tag: tags.emphasis, class: 'psp-tok-em' },
  { tag: tags.link, class: 'psp-tok-link' },
])

function chrome(p: EditorPalette) {
  return EditorView.theme({
    '&': { color: p.foreground, backgroundColor: p.background, fontSize: '13px' },
    '&.cm-focused': { outline: 'none' },
    '.cm-scroller': { fontFamily: 'var(--font-mono, ui-monospace, Menlo, Consolas, monospace)', lineHeight: '20px' },
    '.cm-content': { caretColor: p.cursor, padding: '4px 0' },
    '.cm-line': { padding: '0 6px 0 8px' },
    '.cm-cursor, .cm-dropCursor': { borderLeftColor: p.cursor },
    // The selection layer sits behind the text; the focused rule has to out-rank CodeMirror's own.
    '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': { backgroundColor: p.selection },
    '.cm-selectionBackground': { backgroundColor: p.selectionInactive },
    '.cm-activeLine': { backgroundColor: p.activeLine },
    '.cm-gutters': { backgroundColor: p.background, color: p.lineNumber, border: 'none' },
    '.cm-lineNumbers .cm-gutterElement': { padding: '0 10px 0 14px', minWidth: '3ch' },
    '.cm-activeLineGutter': { backgroundColor: p.activeLine, color: p.lineNumberActive },
    '&.cm-focused .cm-matchingBracket, &.cm-focused .cm-nonmatchingBracket': { backgroundColor: p.matchingBracket, outline: `1px solid ${p.lineNumber}66` },
    '.cm-placeholder': { color: p.placeholderText },
    // Tokens.
    '.psp-tok-comment': { color: p.comment },
    '.psp-tok-keyword': { color: p.keyword },
    '.psp-tok-control': { color: p.controlKeyword },
    '.psp-tok-yaml-key': { color: p.yamlKey },
    '.psp-tok-json-key': { color: p.jsonKey },
    '.psp-tok-string': { color: p.string },
    '.psp-tok-number': { color: p.number },
    '.psp-tok-const': { color: p.constant },
    '.psp-tok-anchor': { color: p.anchor },
    '.psp-tok-tag': { color: p.tag },
    '.psp-tok-punct': { color: p.punctuation },
    '.psp-tok-heading': { color: p.heading, fontWeight: 'bold' },
    '.psp-tok-quote': { color: p.quote },
    '.psp-tok-list': { color: p.list },
    '.psp-tok-code': { color: p.code },
    '.psp-tok-strong': { fontWeight: 'bold' },
    '.psp-tok-em': { fontStyle: 'italic' },
    '.psp-tok-link': { color: p.link, textDecoration: 'underline' },
    '.psp-tok-scalar-number': { color: p.scalarNumber },
    '.psp-tok-scalar-const': { color: p.scalarConst },
    // `> span` as well: the placeholder may sit outside a string token, whose own colour would otherwise win.
    '.psp-tok-placeholder, .psp-tok-placeholder > span': { color: p.placeholder },
    '.psp-tok-placeholder': { backgroundColor: p.placeholderBackground, borderRadius: '3px' },
  }, { dark: p.dark })
}

/** A disabled editor drops every token colour for one grey (not opacity, which would sink the lighter token colours under 3:1). */
export function vscodeDisabledText(dark: boolean): Extension {
  const p = dark ? DARK_PLUS : LIGHT_PLUS
  return EditorView.theme({ '.cm-content, .cm-content span': { color: p.disabledText }, '.cm-gutters': { color: p.disabledText } }, { dark: p.dark })
}

const cache = new Map<boolean, Extension>()

/** VS Code Dark+ (dark) or Light+ (light): colours, gutter, selection and the token highlighter. */
export function vscodeTheme(dark: boolean): Extension {
  let ext = cache.get(dark)
  if (!ext) {
    ext = [chrome(dark ? DARK_PLUS : LIGHT_PLUS), syntaxHighlighting(tokenHighlighter)]
    cache.set(dark, ext)
  }
  return ext
}
