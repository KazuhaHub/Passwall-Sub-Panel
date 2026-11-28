import { useCallback, useMemo, useRef, useState } from 'react'
import { buildBlocks, type Block } from '@/utils/ruleset/blocks'
import { serializeDoc, type Doc } from '@/utils/ruleset/parse'
import { readDoc, seedDoc, SETTLE_MS, useSettledFlush } from './docCache'

const HISTORY = 60

/**
 * The structured view's hold on the draft. `content` is the one source of truth (it is what is saved); the DOCUMENT read
 * from it is kept alongside, because lines carry ids that must survive edits (selections and open dialogs name lines by
 * id). While `content` is what the last edit produced, the kept document is used as it is; any other content (the text
 * view typed into, a discard, a restore) is read afresh and its lines get new ids.
 *
 * Every structured edit is a function from document to document and one step of the undo history, which holds documents,
 * not text, so undoing brings back the old ids with the old lines (design 3.12 item 8). Text mode has CodeMirror's own
 * history: a whole stretch in text mode is one step here (`beginText` / `endText`).
 *
 * The stacks live in refs (a step must not run twice) with a counter to redraw the buttons that read them.
 *
 * `typing` is the text view: the person is typing into the whole text, and reading all of it again on every keystroke is
 * what made typing slow on a long rule set. The document then follows the text once typing pauses (everything that reads it
 * there, the foreign-line notice, the routing test and the problems panel, is happy with that); leaving the text view reads
 * the latest text at once.
 */
export function useRuleDoc(content: string, setContent: (next: string) => void, typing = false) {
  const { value: source, flush } = useSettledFlush(content, typing ? SETTLE_MS : 0)
  const latest = useRef(content)
  latest.current = content
  const kept = useRef<{ content: string; doc: Doc } | null>(null)
  if (kept.current === null || kept.current.content !== source) kept.current = { content: source, doc: readDoc(source) }
  const doc = kept.current.doc
  const blocks = useMemo(() => buildBlocks(doc), [doc])
  const past = useRef<Doc[]>([])
  const future = useRef<Doc[]>([])
  const [, redraw] = useState(0)

  const remember = (d: Doc) => { past.current = [...past.current.slice(-(HISTORY - 1)), d] }
  const show = (d: Doc) => {
    const text = serializeDoc(d)
    kept.current = { content: text, doc: d }
    seedDoc(text, d)
    setContent(text)
    redraw(n => n + 1)
  }

  /** Applies an edit to the document as it is now; false when it changed nothing. */
  const apply = useCallback((edit: (doc: Doc, blocks: Block[]) => Doc): boolean => {
    const before = kept.current!.doc
    const next = edit(before, buildBlocks(before))
    if (next === before || serializeDoc(next) === kept.current!.content) return false
    remember(before)
    future.current = []
    show(next)
    return true
    // `show` only touches refs and the stable setters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setContent])

  const undo = useCallback(() => {
    const prev = past.current[past.current.length - 1]
    if (!prev) return
    past.current = past.current.slice(0, -1)
    future.current = [kept.current!.doc, ...future.current]
    show(prev)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setContent])

  const redo = useCallback(() => {
    const [next, ...rest] = future.current
    if (!next) return
    future.current = rest
    remember(kept.current!.doc)
    show(next)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setContent])

  const textStart = useRef<Doc | null>(null)
  const beginText = useCallback(() => { textStart.current = kept.current!.doc }, [])
  const endText = useCallback(() => {
    const start = textStart.current
    textStart.current = null
    if (start && serializeDoc(start) !== latest.current) { remember(start); future.current = []; redraw(n => n + 1) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  /** The document is still the one read before the latest typing: what is derived from it is about to change. */
  const behind = source !== content
  return { doc, blocks, behind, settle: flush, apply, undo, redo, canUndo: past.current.length > 0, canRedo: future.current.length > 0, beginText, endText }
}
