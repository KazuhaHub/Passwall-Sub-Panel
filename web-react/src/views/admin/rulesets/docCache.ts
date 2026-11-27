import { useEffect, useRef, useState } from 'react'
import { parseDoc, type Doc } from '@/utils/ruleset/parse'

const KEEP = 4
const recent = new Map<string, Doc>()

/**
 * The document read from a rules text, remembered for the last few texts. A page asks several questions of the same draft (the
 * editor's lines, whether anything is foreign, which groups the rules name) and each used to read the text again: a rule set
 * of 9,500 lines takes about 30 ms to read, so three readers cost a tenth of a second on one render. Documents are never
 * changed in place (every edit returns a new one), so sharing one between readers is safe, and it keeps their line ids equal.
 */
export function readDoc(content: string): Doc {
  const hit = recent.get(content)
  if (hit) { recent.delete(content); recent.set(content, hit); return hit }
  const doc = parseDoc(content)
  recent.set(content, doc)
  if (recent.size > KEEP) recent.delete(recent.keys().next().value as string)
  return doc
}

/** How long typing must pause before what is derived from the text is worked out again. */
export const SETTLE_MS = 300

/**
 * `value` as it was when it last stood still for `ms`: while it keeps changing the old one is returned, so something worked
 * out from it (a parse of the whole text) is not redone on every keystroke. With `ms` at 0 the value is returned as it is,
 * and the held one follows it, so switching the delay on later does not start from an old value.
 */
export function useSettled<T>(value: T, ms: number): T {
  const held = useRef(value)
  const [, redraw] = useState(0)
  if (ms <= 0) held.current = value
  useEffect(() => {
    if (ms <= 0 || Object.is(held.current, value)) return
    const id = window.setTimeout(() => { held.current = value; redraw(n => n + 1) }, ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return held.current
}
