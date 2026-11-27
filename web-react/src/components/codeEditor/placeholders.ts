import { Decoration, MatchDecorator, ViewPlugin, type DecorationSet, type EditorView, type ViewUpdate } from '@codemirror/view'

// A `{{ name }}` of a template (the subscription templates and the mail templates) gets its own
// colour in every language, because it is replaced before the text is read as YAML, JSON or text and so
// belongs to no grammar. MatchDecorator only looks at the visible ranges and the changed lines.
const mark = Decoration.mark({ class: 'psp-tok-placeholder' })
const matcher = new MatchDecorator({
  regexp: /\{\{[^{}\n]*\}\}/g,
  decoration: () => mark,
})

export const templatePlaceholders = ViewPlugin.fromClass(class {
  decorations: DecorationSet
  constructor(view: EditorView) { this.decorations = matcher.createDeco(view) }
  update(u: ViewUpdate) { this.decorations = matcher.updateDeco(u, this.decorations) }
}, { decorations: p => p.decorations })
