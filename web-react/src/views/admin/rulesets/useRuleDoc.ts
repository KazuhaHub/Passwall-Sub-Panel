import { useCallback, useMemo, useRef, useState } from 'react'
import { buildBlocks, type Block } from '@/utils/ruleset/blocks'
import { parseDoc, serializeDoc, type Doc } from '@/utils/ruleset/parse'

const HISTORY = 60

/**
 * The structured view's hold on the draft: `content` is the one source of truth (it is what is saved), the document
 * and its groups are read from it, and every structured edit is a function from document to document whose result is
 * written back as text. Each edit is one step of the undo history (design 3.12 item 8); text mode has CodeMirror's own
 * history, so a whole stretch in text mode counts as one step here (`beginText` / `endText`).
 *
 * The two stacks live in refs (an updater function must stay pure, and a step must not run twice) with a counter to
 * redraw the buttons that read them.
 */
export function useRuleDoc(content: string, setContent: (next: string) => void) {
  const doc = useMemo(() => parseDoc(content), [content])
  const blocks = useMemo(() => buildBlocks(doc), [doc])
  const past = useRef<string[]>([])
  const future = useRef<string[]>([])
  const [, redraw] = useState(0)
  const live = useRef(content)
  live.current = content

  const remember = (text: string) => { past.current = [...past.current.slice(-(HISTORY - 1)), text] }

  /** Applies an edit; false when it changed nothing. */
  const apply = useCallback((edit: (doc: Doc, blocks: Block[]) => Doc): boolean => {
    const before = live.current
    const d = parseDoc(before)
    const next = serializeDoc(edit(d, buildBlocks(d)))
    if (next === before) return false
    remember(before)
    future.current = []
    live.current = next
    setContent(next)
    redraw(n => n + 1)
    return true
  }, [setContent])

  const undo = useCallback(() => {
    const prev = past.current[past.current.length - 1]
    if (prev === undefined) return
    past.current = past.current.slice(0, -1)
    future.current = [live.current, ...future.current]
    live.current = prev
    setContent(prev)
    redraw(n => n + 1)
  }, [setContent])

  const redo = useCallback(() => {
    const [next, ...rest] = future.current
    if (next === undefined) return
    future.current = rest
    remember(live.current)
    live.current = next
    setContent(next)
    redraw(n => n + 1)
  }, [setContent])

  const textStart = useRef<string | null>(null)
  const beginText = useCallback(() => { textStart.current = live.current }, [])
  const endText = useCallback(() => {
    const start = textStart.current
    textStart.current = null
    if (start !== null && start !== live.current) { remember(start); future.current = []; redraw(n => n + 1) }
  }, [])

  return { doc, blocks, apply, undo, redo, canUndo: past.current.length > 0, canRedo: future.current.length > 0, beginText, endText }
}
