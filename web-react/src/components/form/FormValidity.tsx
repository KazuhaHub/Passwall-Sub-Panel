import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { flushSync } from 'react-dom'
import { SectionRevealContext } from './editorContext'

export interface ValidityIssue {
  id: string
  message: string
  /** The field's accessible name (its label text), when it could be read; used to name it in the SaveBar. */
  label?: string
}

interface Target { el: HTMLElement | null; reveal: (() => void) | null }
interface Entry { message: string; label?: string }

interface Registry {
  issues: ValidityIssue[]
  report: (id: string, message: string | null, target?: Target) => void
  focusFirst: () => boolean
}

const ValidityContext = createContext<Registry | null>(null)

/**
 * The text a screen reader would speak for a control: aria-labelledby, then aria-label,
 * then its <label>. Parts that are aria-hidden (the required-field asterisk) are left out.
 */
function accessibleName(el: HTMLElement): string | undefined {
  const text = (node: Element) => {
    const copy = node.cloneNode(true) as Element
    copy.querySelectorAll('[aria-hidden="true"]').forEach(n => n.remove())
    return (copy.textContent ?? '').replace(/\s+/g, ' ').trim()
  }
  const ids = el.getAttribute('aria-labelledby')?.split(/\s+/).filter(Boolean) ?? []
  const fromIds = ids.map(id => { const n = el.ownerDocument.getElementById(id); return n ? text(n) : '' }).filter(Boolean).join(' ')
  if (fromIds) return fromIds
  const own = el.getAttribute('aria-label')?.trim()
  if (own) return own
  const labels = (el as HTMLInputElement).labels
  const fromLabel = labels && labels.length ? text(labels[0]) : ''
  return fromLabel || undefined
}

/**
 * Wrap a form (or a section with its own SaveBar) in this. Fields inside report
 * whether their input is usable (NumberField, TagsField, anything calling
 * useReportValidity), and a SaveBar inside reads the result, switches itself off,
 * names the fields to fix and can take the person to the first one. Without it fields
 * still show their own messages; nothing blocks.
 *
 * The page keeps its own state and its own validation; this only carries the
 * question the page cannot answer: is there text in a box that never became a value?
 * (a number being typed, "9999" in a field limited to 600, a box emptied but not
 * filled), so Save cannot store something other than what the box shows.
 *
 * NESTING. A field reports to the NEAREST provider only, and a SaveBar reads the
 * nearest one. So a form inside a dialog that sits inside a page form is its own
 * scope: an invalid field in the dialog blocks the dialog's SaveBar and nothing else,
 * and the page's SaveBar is not held up by it (the dialog's draft is not part of what
 * the page would save). The reverse holds as well: the page's invalid fields do not
 * block a SaveBar inside the dialog. Context crosses a portal, so a dialog rendered
 * through one still belongs to the provider it is written inside.
 */
export function FormValidityProvider({ children }: { children: ReactNode }) {
  const [map, setMap] = useState<Record<string, Entry>>({})
  // Elements are not state: a re-render for each ref would add nothing. They are read when
  // the list is built (to order it) and when focusing.
  const targets = useRef(new Map<string, Target>())

  const report = useCallback((id: string, message: string | null, target?: Target) => {
    if (message === null) {
      targets.current.delete(id)
      setMap(prev => {
        if (!(id in prev)) return prev
        const { [id]: _gone, ...rest } = prev
        void _gone
        return rest
      })
      return
    }
    if (target) targets.current.set(id, target)
    const label = target?.el ? accessibleName(target.el) : undefined
    setMap(prev => (prev[id]?.message === message && prev[id]?.label === label ? prev : { ...prev, [id]: { message, label } }))
  }, [])

  const issues = useMemo<ValidityIssue[]>(() => {
    const list = Object.entries(map).map(([id, e]) => ({ id, message: e.message, label: e.label }))
    // Page order, not the order fields happened to report in.
    return list.sort((a, b) => {
      const ea = targets.current.get(a.id)?.el
      const eb = targets.current.get(b.id)?.el
      if (ea && eb) return ea === eb ? 0 : ea.compareDocumentPosition(eb) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
      return ea ? -1 : eb ? 1 : 0
    })
  }, [map])

  const focusFirst = useCallback((): boolean => {
    for (const issue of issues) {
      const target = targets.current.get(issue.id)
      const el = target?.el
      if (!el || !el.isConnected) continue
      // The section must be open before the field can take focus: wait for it to render.
      if (target.reveal) flushSync(() => target.reveal?.())
      el.focus()
      // Centred, so the sticky save bar at the bottom does not end up covering it.
      el.scrollIntoView?.({ block: 'center' })
      return true
    }
    return false
  }, [issues])

  const value = useMemo<Registry>(() => ({ issues, report, focusFirst }), [issues, report, focusFirst])
  return <ValidityContext.Provider value={value}>{children}</ValidityContext.Provider>
}

/** What the nearest provider currently knows: the problems, and whether there are any. */
export function useFormValidity(): { issues: ValidityIssue[]; blocked: boolean; focusFirst: () => boolean } {
  const ctx = useContext(ValidityContext)
  return { issues: ctx?.issues ?? [], blocked: (ctx?.issues.length ?? 0) > 0, focusFirst: ctx?.focusFirst ?? (() => false) }
}

/**
 * A field reports its problem (null when fine). Cleans up on unmount, so a field that
 * leaves the form (an advanced section removed, an editor switched away) stops blocking it.
 *
 * `target` is the element that takes focus when the person is sent to the problem; its
 * accessible name is what the SaveBar calls the field. Without one the problem still
 * blocks and is still worded, it just cannot be named or focused.
 */
export function useReportValidity(message: string | null, target?: RefObject<HTMLElement | null>): void {
  const ctx = useContext(ValidityContext)
  const reveal = useContext(SectionRevealContext)
  const id = useId()
  const report = ctx?.report
  useEffect(() => {
    if (!report) return
    report(id, message, { el: target?.current ?? null, reveal })
    return () => report(id, null)
  }, [report, id, message, target, reveal])
}
