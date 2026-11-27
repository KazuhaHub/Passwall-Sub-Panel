/**
 * The colours of the code editor, copied from VS Code's built-in "Dark+" and "Light+" themes
 * (extensions/theme-defaults/themes/dark_plus.json, light_plus.json and the dark_vs / light_vs
 * files they include). One file, explicit hex values, so a tweak is a one-line diff and nobody
 * has to guess where a colour came from.
 *
 * Token roles are named for what they are drawn from, not for a CodeMirror tag; `highlight.ts`
 * maps tags to roles. Two roles are the panel's own: `placeholder` (a `{{ name }}` of a template) and
 * `scalarConst` / `scalarNumber`, which VS Code's YAML grammar colours but the Lezer YAML parser does
 * not tell apart (see `scalars.ts`). The `placeholder` colour is not a VS Code token: it borrows the
 * "escape character" family (#d7ba7d in Dark+) so a placeholder stands out from every other token;
 * Light+ uses #ee0000 for that, which reads as an error, so light mode takes a dark amber instead.
 */
export interface EditorPalette {
  /** True for a dark theme: CodeMirror switches its built-in dark defaults on it. */
  dark: boolean
  // Chrome (workbench colours).
  background: string
  foreground: string
  cursor: string
  /** The selection while the editor has focus, and while it has not. */
  selection: string
  selectionInactive: string
  /** `editor.lineHighlightBackground`: VS Code draws a border only; a fill reads better in a small box. */
  activeLine: string
  lineNumber: string
  lineNumberActive: string
  matchingBracket: string
  placeholderText: string
  // Token roles.
  comment: string
  keyword: string
  controlKeyword: string
  string: string
  number: string
  /** `true`, `false`, `null` and the like. */
  constant: string
  /** A key of a YAML mapping. */
  yamlKey: string
  /** A key of a JSON object (a different colour from a YAML key in VS Code). */
  jsonKey: string
  /** An anchor or alias (`&a`, `*a`). */
  anchor: string
  /** A `!!tag`. */
  tag: string
  punctuation: string
  heading: string
  link: string
  quote: string
  code: string
  list: string
  placeholder: string
  placeholderBackground: string
  scalarNumber: string
  scalarConst: string
  /** Gold and teal of "function" and "type" in the theme, used for rarely seen nodes. */
  type: string
}

export const DARK_PLUS: EditorPalette = {
  dark: true,
  background: '#1e1e1e',
  foreground: '#d4d4d4',
  cursor: '#aeafad',
  selection: '#264f78',
  selectionInactive: '#3a3d41',
  activeLine: '#2a2d2e',
  lineNumber: '#858585',
  lineNumberActive: '#c6c6c6',
  matchingBracket: '#0064001a',
  placeholderText: '#858585',
  comment: '#6a9955',
  keyword: '#569cd6',
  controlKeyword: '#c586c0',
  string: '#ce9178',
  number: '#b5cea8',
  constant: '#569cd6',
  yamlKey: '#569cd6',
  jsonKey: '#9cdcfe',
  anchor: '#4ec9b0',
  tag: '#c586c0',
  punctuation: '#d4d4d4',
  heading: '#569cd6',
  link: '#4e94ce',
  quote: '#6a9955',
  code: '#ce9178',
  list: '#6796e6',
  placeholder: '#d7ba7d',
  placeholderBackground: '#d7ba7d26',
  scalarNumber: '#b5cea8',
  scalarConst: '#569cd6',
  type: '#4ec9b0',
}

export const LIGHT_PLUS: EditorPalette = {
  dark: false,
  background: '#ffffff',
  foreground: '#000000',
  cursor: '#000000',
  selection: '#add6ff',
  selectionInactive: '#e5ebf1',
  activeLine: '#f5f5f5',
  lineNumber: '#237893',
  lineNumberActive: '#0b216f',
  matchingBracket: '#0064001a',
  placeholderText: '#8a8a8a',
  comment: '#008000',
  keyword: '#0000ff',
  controlKeyword: '#af00db',
  string: '#a31515',
  number: '#098658',
  constant: '#0000ff',
  yamlKey: '#800000',
  jsonKey: '#0451a5',
  anchor: '#267f99',
  tag: '#af00db',
  punctuation: '#000000',
  heading: '#800000',
  link: '#0451a5',
  quote: '#0451a5',
  code: '#a31515',
  list: '#0451a5',
  placeholder: '#9a5b00',
  placeholderBackground: '#9a5b0020',
  scalarNumber: '#098658',
  scalarConst: '#0000ff',
  type: '#267f99',
}
