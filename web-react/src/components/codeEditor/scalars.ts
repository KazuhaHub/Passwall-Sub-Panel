import { syntaxTree } from '@codemirror/language'
import { RangeSetBuilder } from '@codemirror/state'
import { Decoration, EditorView, ViewPlugin, type DecorationSet, type ViewUpdate } from '@codemirror/view'

// The Lezer YAML parser calls every plain scalar `Literal` (and a `|` block's text `BlockLiteralContent`),
// so `true`, `8080` and `abc` look the same to a highlighter, while VS Code's grammar colours them three
// ways: a constant, a number, and (everything else) a string. This overlay does that for the scalars of
// VALUES only (a `Key` parent means it is a mapping key, left to the key colour). It is the only thing
// that colours them, so there is no second colour underneath to fight with. The walk is limited to the
// visible ranges, so it costs the same on a 9.5k-line document as on a small one.
const NUMBER = /^[-+]?(?:0x[0-9a-f_]+|\d[\d_]*(?:\.\d*)?(?:e[-+]?\d+)?|\.\d+(?:e[-+]?\d+)?)$/i
const CONSTANT = /^(?:true|false|null|~|yes|no|on|off)$/i

const numberMark = Decoration.mark({ class: 'psp-tok-scalar-number' })
const constMark = Decoration.mark({ class: 'psp-tok-scalar-const' })
const stringMark = Decoration.mark({ class: 'psp-tok-string' })

function build(view: EditorView): DecorationSet {
  const builder = new RangeSetBuilder<Decoration>()
  const { state } = view
  const tree = syntaxTree(state)
  for (const { from, to } of view.visibleRanges) {
    tree.iterate({
      from, to,
      enter(node) {
        if (node.name === 'BlockLiteralContent') { builder.add(node.from, node.to, stringMark); return false }
        if (node.name !== 'Literal') return
        // A key's literal is not a value. `node.node.parent` is the immediate parent: `Key` wraps it.
        if (node.node.parent?.name === 'Key') return false
        // A multi-line plain scalar is prose, never a number or a constant: only a short one is tested.
        const text = node.to - node.from <= 40 ? state.sliceDoc(node.from, node.to) : ''
        builder.add(node.from, node.to, NUMBER.test(text) ? numberMark : CONSTANT.test(text) ? constMark : stringMark)
        return false
      },
    })
  }
  return builder.finish()
}

export const yamlScalars = ViewPlugin.fromClass(class {
  decorations: DecorationSet
  constructor(view: EditorView) { this.decorations = build(view) }
  update(u: ViewUpdate) {
    // The tree grows in the background after the first paint: rebuild when it changed too.
    if (u.docChanged || u.viewportChanged || syntaxTree(u.startState) !== syntaxTree(u.state)) this.decorations = build(u.view)
  }
}, { decorations: p => p.decorations })
