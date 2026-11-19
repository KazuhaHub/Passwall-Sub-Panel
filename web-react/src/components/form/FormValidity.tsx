import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { flushSync } from 'react-dom'
import { sectionReveals } from './editorContext'

export interface ValidityIssue {
  id: string
  message: string
  /** The field's accessible name (its label text), when it could be read; used to name it in the SaveBar. */
  label?: string
  /** The element that takes focus when the person is sent to the problem. */
  el?: HTMLElement | null
}

type Resolver = RefObject<HTMLElement | null> | (() => HTMLElement | null)
interface Entry { message: string; label?: string }

interface Registry {
  issues: ValidityIssue[]
  report: (id: string, message: string | null, target?: Resolver) => void
  onDiscard: (cb: () => void) => () => void
  discard: () => void
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

const SECTION = '[data-slot="form-section"]'

/** The title of the FormSection an element sits in, if any: used to tell apart two fields with one label. */
export function sectionTitleOf(el: Element | null | undefined): string | undefined {
  const title = el?.closest(SECTION)?.querySelector('[data-slot="form-section-title"]')
  return title?.textContent?.replace(/\s+/g, ' ').trim() || undefined
}

/**
 * Send focus to a problem's element, first opening every collapsed FormSection it is
 * inside, and say whether focus ACTUALLY landed (a disabled control, or one in a hidden
 * container such as an inactive tab that stays mounted, silently refuses). The caller
 * moves on to the next problem when it did not.
 */
export function focusProblem(el: HTMLElement | null | undefined): boolean {
  if (!el || !el.isConnected) return false
  const reveals: (() => void)[] = []
  for (let s = el.closest(SECTION); s; s = s.parentElement?.closest(SECTION) ?? null) {
    const reveal = sectionReveals.get(s)
    if (reveal) reveals.push(reveal)
  }
  // The section must be open before the field can take focus: wait for it to render.
  if (reveals.length) flushSync(() => reveals.forEach(r => r()))
  el.focus()
  if (el.ownerDocument.activeElement !== el) return false
  // Centred, so the sticky save bar at the bottom does not end up covering it.
  el.scrollIntoView?.({ block: 'center' })
  return true
}

/**
 * Wrap a form (or a section with its own SaveBar) in this. Fields inside report
 * whether their input is usable (NumberField, TagsField, a Field given an `error`,
 * anything calling useReportValidity), and a SaveBar inside reads the result, switches
 * itself off, names the fields to fix and can take the person to the first one. Without
 * it fields still show their own messages; nothing blocks.
 *
 * The page keeps its own state and its own validation; this only carries the
 * question the page cannot answer: is there text in a box that never became a value?
 * (a number being typed, "9999" in a field limited to 600, a box emptied but not
 * filled), so Save cannot store something other than what the box shows.
 *
 * A DISABLED field never reports a problem: the person cannot fix what they cannot
 * edit, so it must not hold the whole form hostage. (It reports again if it is enabled.)
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
  const targets = useRef(new Map<string, HTMLElement | null>())
  const discardCbs = useRef(new Set<() => void>())

  const report = useCallback((id: string, message: string | null, target?: Resolver) => {
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
    const el = typeof target === 'function' ? target() : target?.current ?? null
    targets.current.set(id, el)
    const label = el ? accessibleName(el) : undefined
    setMap(prev => (prev[id]?.message === message && prev[id]?.label === label ? prev : { ...prev, [id]: { message, label } }))
  }, [])

  const issues = useMemo<ValidityIssue[]>(() => {
    const list = Object.entries(map).map(([id, e]) => ({ id, message: e.message, label: e.label, el: targets.current.get(id) ?? null }))
    // Page order, not the order fields happened to report in.
    return list.sort((a, b) => {
      if (a.el && b.el) return a.el === b.el ? 0 : a.el.compareDocumentPosition(b.el) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
      return a.el ? -1 : b.el ? 1 : 0
    })
  }, [map])

  const onDiscard = useCallback((cb: () => void) => { discardCbs.current.add(cb); return () => { discardCbs.current.delete(cb) } }, [])
  const discard = useCallback(() => { discardCbs.current.forEach(cb => cb()) }, [])

  const value = useMemo<Registry>(() => ({ issues, report, onDiscard, discard }), [issues, report, onDiscard, discard])
  return <ValidityContext.Provider value={value}>{children}</ValidityContext.Provider>
}

/** What the nearest provider currently knows: the problems (in page order), and whether there are any. */
export function useFormValidity(): { issues: ValidityIssue[]; blocked: boolean; discard: () => void } {
  const ctx = useContext(ValidityContext)
  return { issues: ctx?.issues ?? [], blocked: (ctx?.issues.length ?? 0) > 0, discard: ctx?.discard ?? (() => {}) }
}

/**
 * Run `cb` when the form's SaveBar Discard is pressed. For a control that keeps state the
 * page does not hold (an InheritanceField's "set here, not yet written" start), which a
 * page-level reset of its draft cannot reach.
 */
export function useOnFormDiscard(cb: () => void): void {
  const ctx = useContext(ValidityContext)
  const latest = useRef(cb)
  latest.current = cb
  const subscribe = ctx?.onDiscard
  useEffect(() => subscribe?.(() => latest.current()), [subscribe])
}

/**
 * A field reports its problem (null when fine). Cleans up on unmount, so a field that
 * leaves the form (an advanced section removed, an editor switched away) stops blocking it.
 * A disabled field should report null (see FormValidityProvider).
 *
 * `target` is the element that takes focus when the person is sent to the problem (a ref,
 * or a function that finds it once the field has rendered); its accessible name is what
 * the SaveBar calls the field. Without one the problem still blocks and is still worded,
 * it just cannot be named or focused.
 */
export function useReportValidity(message: string | null, target?: Resolver): void {
  const ctx = useContext(ValidityContext)
  const id = useId()
  const report = ctx?.report
  // Read when the problem is reported; not a dependency, or an inline function would re-report every render.
  const targetRef = useRef(target)
  targetRef.current = target
  useEffect(() => {
    if (!report) return
    report(id, message, targetRef.current)
    return () => report(id, null)
  }, [report, id, message])
}
