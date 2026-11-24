import { useEffect, useState } from 'react'
import type { Continuation } from '@/utils/ruleset/continuation'
import { findShadowed, type Finding } from '@/utils/ruleset/lint'
import type { Doc } from '@/utils/ruleset/parse'

/**
 * The covered-rule findings of a document, worked out when the browser is idle and never while the person is typing: an edit
 * clears the findings at once (they name lines of a document that is gone) and the analysis runs after a pause, in an idle
 * callback where there is one. The analysis itself is a single linear pass (utils/ruleset/lint), so a rule set of 50,000
 * rules takes a fraction of a second, but it is still kept off the keystroke.
 *
 * Whether a rule covers the ones below it depends on whether it is the last word (a group that may pass does not cover), which is
 * the backend's answer about the groups. So nothing is worked out without `cont`: it is null until that answer is in for this
 * draft, and `findings` is null while it is, and while the analysis for the current document is not in yet.
 */
export function useShadowFindings(doc: Doc, cont: Continuation | null): Finding[] | null {
  const [done, setDone] = useState<{ doc: Doc; cont: Continuation; findings: Finding[] } | null>(null)
  useEffect(() => {
    if (!cont) return
    let cancelled = false
    const run = () => { if (!cancelled) setDone({ doc, cont, findings: findShadowed(doc, cont) }) }
    const w = window as Window & { requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number; cancelIdleCallback?: (id: number) => void }
    // A short pause first, so a burst of edits is analysed once, at the end.
    const wait = window.setTimeout(() => {
      if (w.requestIdleCallback) idle = w.requestIdleCallback(run, { timeout: 2000 })
      else run()
    }, 200)
    let idle: number | undefined
    return () => {
      cancelled = true
      window.clearTimeout(wait)
      if (idle !== undefined) w.cancelIdleCallback?.(idle)
    }
  }, [doc, cont])
  return cont && done && done.doc === doc && done.cont === cont ? done.findings : null
}
