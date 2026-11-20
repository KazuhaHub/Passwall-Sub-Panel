import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { flushSync } from 'react-dom'
import { InFormContext, revealersAround } from '@/lib/reveal'

export interface ValidityIssue {
  id: string
  message: string
  /** The field's accessible name (its label text), when it could be read; used to name it in the SaveBar. */
  label?: string
  /** The element that takes focus when the person is sent to the problem. */
  el?: HTMLElement | null
  /** false: a message to show, not a reason Save is off (see FormValidityProvider). Default true. */
  blocking?: boolean
  /** Reported by a Field about its control rather than by the control itself: yields to the control's own report. */
  weak?: boolean
}

/** Optional details of a report. */
export interface ReportOptions {
  /** The name to use instead of reading the element's accessible name (a Field knows its own label). */
  label?: () => string | undefined
  blocking?: boolean
  weak?: boolean
}

type Resolver = RefObject<HTMLElement | null> | (() => HTMLElement | null)
interface Entry { message: string; label?: string; blocking: boolean; weak: boolean }

interface Registry {
  issues: ValidityIssue[]
  report: (id: string, message: string | null, target?: Resolver, opts?: ReportOptions) => void
  onDiscard: (cb: () => void) => () => void
  discard: () => void
}

const ValidityContext = createContext<Registry | null>(null)

/**
 * The text a screen reader would speak for a control: aria-labelledby, then aria-label,
 * then its <label>. Parts that are aria-hidden (the required-field asterisk) are left out.
 */
function accessibleName(el: HTMLElement): string | undefined {
  const text = (node: Element) => visibleText(node)
  const ids = el.getAttribute('aria-labelledby')?.split(/\s+/).filter(Boolean) ?? []
  const fromIds = ids.map(id => { const n = el.ownerDocument.getElementById(id); return n ? text(n) : '' }).filter(Boolean).join(' ')
  if (fromIds) return fromIds
  const own = el.getAttribute('aria-label')?.trim()
  if (own) return own
  const labels = (el as HTMLInputElement).labels
  const fromLabel = labels && labels.length ? text(labels[0]) : ''
  return fromLabel || undefined
}

/** The text of a node as read aloud: what is aria-hidden (the required-field asterisk) is left out. */
export function visibleText(node: Element | null | undefined): string {
  if (!node) return ''
  const copy = node.cloneNode(true) as Element
  copy.querySelectorAll('[aria-hidden="true"]').forEach(n => n.remove())
  return (copy.textContent ?? '').replace(/\s+/g, ' ').trim()
}

/**
 * Problems that point at the same element are one problem. A control's own report comes first
 * (that is what has to be fixed in the box), so a Field's message about the same control, or a
 * page error naming it by id, folds into it; a merged entry blocks if either did.
 */
export function mergeByElement<P extends { el?: HTMLElement | null; weak?: boolean; blocking?: boolean; label?: string }>(list: P[]): P[] {
  const out: P[] = []
  for (const p of list) {
    const at = p.el ? out.findIndex(o => o.el === p.el) : -1
    if (at < 0) { out.push(p); continue }
    const kept = out[at]
    const winner = kept.weak && !p.weak ? p : kept
    const other = winner === kept ? p : kept
    out[at] = { ...winner, label: winner.label ?? other.label, blocking: kept.blocking !== false || p.blocking !== false }
  }
  return out
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
  const reveals = revealersAround(el)
  // Whatever hides the field (a collapsed section, an inactive tab) must be open before it can take focus: wait for it to render.
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
 * WHAT BLOCKS. Save never stores something other than what the box shows, and a person is
 * never stopped by something they cannot fix:
 *  - An invalid draft in a field (a number out of range, unreadable text, a required box left
 *    empty) BLOCKS, until it is fixed or abandoned (Escape).
 *  - A Field's `error` BLOCKS too, whatever its type (text or markup), until the page clears
 *    it, UNLESS the Field is given `errorBlocksSave={false}`: that is for a message that does not
 *    stop a retry (a refusal that came back from a failed save). It is still shown, still named
 *    and still reachable from the SaveBar, which then reads it as a notice, not a block.
 *  - A DISABLED field never blocks, and neither does an error on a disabled control: it cannot
 *    be fixed, so it must not hold the whole form hostage. To keep the first rule true, a
 *    field that becomes disabled puts an invalid draft back to its last committed value (and
 *    tells its parent), so what it shows is exactly what will be saved.
 *  - A field in a hidden tab still blocks: inside a provider, `TabsContent` keeps inactive panels
 *    mounted (hidden) by default, so their fields keep reporting. A panel given `keepMounted={false}`
 *    unmounts its fields, and fields that are not in the page cannot block Save.
 *  - One control is one problem: a Field's error, the control's own report and a page error that
 *    names the same element are merged into one entry, the control's own reason first.
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

  const report = useCallback((id: string, message: string | null, target?: Resolver, opts?: ReportOptions) => {
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
    const label = opts?.label?.() ?? (el ? accessibleName(el) : undefined)
    const blocking = opts?.blocking !== false
    const weak = opts?.weak === true
    setMap(prev => {
      const old = prev[id]
      return old && old.message === message && old.label === label && old.blocking === blocking && old.weak === weak ? prev : { ...prev, [id]: { message, label, blocking, weak } }
    })
  }, [])

  const issues = useMemo<ValidityIssue[]>(() => {
    const list = Object.entries(map).map(([id, e]) => ({ id, message: e.message, label: e.label, el: targets.current.get(id) ?? null, blocking: e.blocking, weak: e.weak }))
    // Page order, not the order fields happened to report in.
    return list.sort((a, b) => {
      if (a.el && b.el) return a.el === b.el ? 0 : a.el.compareDocumentPosition(b.el) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
      return a.el ? -1 : b.el ? 1 : 0
    })
  }, [map])

  const onDiscard = useCallback((cb: () => void) => { discardCbs.current.add(cb); return () => { discardCbs.current.delete(cb) } }, [])
  const discard = useCallback(() => { discardCbs.current.forEach(cb => cb()) }, [])

  const value = useMemo<Registry>(() => ({ issues, report, onDiscard, discard }), [issues, report, onDiscard, discard])
  return <InFormContext.Provider value><ValidityContext.Provider value={value}>{children}</ValidityContext.Provider></InFormContext.Provider>
}

/** What the nearest provider currently knows: the problems (in page order), and whether there are any. */
export function useFormValidity(): { issues: ValidityIssue[]; blocked: boolean; discard: () => void } {
  const ctx = useContext(ValidityContext)
  return { issues: ctx?.issues ?? [], blocked: ctx?.issues.some(i => i.blocking !== false) ?? false, discard: ctx?.discard ?? (() => {}) }
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
export function useReportValidity(message: string | null, target?: Resolver, opts?: ReportOptions): void {
  const ctx = useContext(ValidityContext)
  const id = useId()
  const report = ctx?.report
  // Read when the problem is reported; not a dependency, or an inline function would re-report every render.
  const targetRef = useRef(target)
  targetRef.current = target
  const optsRef = useRef(opts)
  optsRef.current = opts
  const blocking = opts?.blocking !== false
  useEffect(() => {
    if (!report) return
    report(id, message, targetRef.current, optsRef.current)
    return () => report(id, null)
  }, [report, id, message, blocking])
}
