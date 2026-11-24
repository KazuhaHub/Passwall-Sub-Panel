import { useContext, useId, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { DialogFooterSlot } from '@/components/data/DetailDialog'
import { Loader2Icon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { focusProblem, mergeByElement, sectionTitleOf, useFormValidity } from './FormValidity'

/** A problem the page found itself (a rule on its own draft, a server refusal). */
export interface SaveBarError {
  /** The field's name; without it the problem is counted but cannot be named. */
  label?: string
  message: string
  /** false: a message to show, not a reason Save is off (see FormValidityProvider). Default true. */
  blocking?: boolean
  /**
   * Where the problem is. `id` is the id of the field's control (what a Field puts on it;
   * `useFieldErrors().saveBarErrors` fills it in), `el` the element itself. With either,
   * the problem is ordered among the fields by its place in the page and focus can go to
   * it, opening the collapsed section it is in. Without one it comes last in the list.
   */
  id?: string
  el?: HTMLElement | null
  /** For a problem with no element to point at: moves focus however the page can. Tried when `el` is absent or refuses focus. */
  focus?: () => void
}

export interface SaveBarProps {
  dirty: boolean
  /** A save is in flight: both buttons lock and the label says so. */
  pending?: boolean
  /** The form cannot be saved yet (invalid); `message` says what to fix. */
  disabled?: boolean
  /** Replaces "有未保存的修改" when there is something more useful to say, such as how many fields to fix. */
  message?: ReactNode
  /**
   * Problems the page holds that no field reported. A Field given an `error` reports it
   * itself, so a page does NOT pass the same error here again (if it names the same element
   * by `id` the two are merged into one anyway); this is for the rest (a rule across fields,
   * a refusal with no field of its own). They are
   * ordered TOGETHER with the fields' own, by position in the page, and the first one in
   * that order is where Save and the message take the person. (`disabled` with a `message`
   * still works and counts, and is named, as one problem, last.)
   */
  errors?: readonly SaveBarError[]
  saveLabel?: string
  onSave: () => void
  onDiscard: () => void
  className?: string
}

interface Problem { label?: string; message: string; el: HTMLElement | null; focus?: () => void; blocking: boolean; weak?: boolean }

/** In page order; a problem with no element goes last, in the order it came. */
function inPageOrder(list: Problem[]): Problem[] {
  return list.map((p, i) => ({ p, i })).sort((x, y) => {
    const a = x.p.el; const b = y.p.el
    if (a && b && a !== b) return a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
    if (a && !b) return -1
    if (!a && b) return 1
    return x.i - y.i
  }).map(x => x.p)
}

/**
 * The floating bar at the bottom of a long form (sticky, see the class comment). It appears only when there is
 * something to save, so a clean form has nothing asking for attention, and it
 * stays up while a save is in flight even if the draft already matches. One bar
 * serves one section or tab: each saves on its own (audit section 0).
 *
 * While saving is blocked the message names what to fix (the label of each field, or
 * "label: what is wrong" when there is one) and is a button: activating it, or pressing
 * Save, moves focus to the first problem IN PAGE ORDER whether a field or the page
 * reported it, opening the advanced section it is in if that is collapsed, and trying the
 * next one when focus does not land. It is a real button so the keyboard reaches it. The
 * status region speaks the blocked state and the count, not every change of wording: the
 * fields announce their own messages.
 */
export function SaveBar({ dirty, pending, disabled, message, errors, saveLabel, onSave, onDiscard, className }: SaveBarProps) {
  const { t } = useSpacedT()
  // Inside a DetailDialog the bar lives in the dialog's footer, flat and full width, not floating over the body.
  const footerSlot = useContext(DialogFooterSlot)
  const visible = dirty || pending
  const { issues, discard } = useFormValidity()
  const reasonId = useId()
  // Built again at click time: an element named by id may not have been in the page when this rendered.
  const gather = () => inPageOrder(mergeByElement<Problem>([
    ...issues.map(i => ({ label: i.label, message: i.message, el: i.el ?? null, blocking: i.blocking !== false, weak: i.weak })),
    ...(errors ?? []).map(e => ({ label: e.label, message: e.message, el: e.el ?? (e.id ? document.getElementById(e.id) : null), focus: e.focus, blocking: e.blocking !== false })),
  ]))
  const problems = gather()
  // `disabled` with a message is one more problem, not a replacement for the others.
  const legacy = disabled === true && message != null
  const all: Problem[] = legacy ? [...problems, { label: typeof message === 'string' ? message : undefined, message: '', el: null, blocking: true }] : problems
  const count = all.length
  // Only problems that block switch Save off; a message that does not is named like the others but reads as a notice.
  const blocked = disabled === true || problems.some(p => p.blocking)
  const [hidden, setHidden] = useState<{ name?: string } | null>(null)

  // Two fields called "Port" read as "Port, Port": tell them apart by their section, then by position.
  const named = all.map(p => p.label)
  const dup = (l: string | undefined, list: (string | undefined)[]) => l !== undefined && list.filter(x => x === l).length > 1
  const original = [...named]
  if (count > 1) {
    all.forEach((p, i) => {
      if (!p.label || !dup(p.label, original)) return
      const section = sectionTitleOf(p.el)
      if (section) named[i] = t('ui.form.invalid_in_section', { section, label: p.label })
    })
    const base = [...named]
    const seen: Record<string, number> = {}
    named.forEach((l, i) => {
      if (l && dup(l, base)) { seen[l] = (seen[l] ?? 0) + 1; named[i] = t('ui.form.invalid_nth', { label: l, n: seen[l] }) }
    })
  }
  // What needs fixing (blocking problems, and the `disabled` message) and what only has a message
  // (non-blocking ones) are separate sentences: a notice is never counted as something to fix.
  const part = (indices: number[], kind: 'invalid' | 'notice'): string | null => {
    const sel = indices.map(i => all[i])
    if (sel.length === 0) return null
    if (sel.length === 1) return sel[0].label ? (sel[0].message ? t('ui.form.invalid_field', { label: sel[0].label, message: sel[0].message }) : sel[0].label) : sel[0].message
    const names = indices.map(i => named[i])
    return names.every(Boolean)
      ? t(kind === 'invalid' ? 'ui.form.invalid_named' : 'ui.form.notice_named', { count: sel.length, names: names.join(t('ui.form.name_sep')) })
      : t(kind === 'invalid' ? 'ui.form.invalid_count' : 'ui.form.notice_count', { count: sel.length })
  }
  const indicesOf = (blocking: boolean) => all.flatMap((p, i) => (p.blocking === blocking ? [i] : []))
  const reason = problems.length === 0 ? null
    : [part(indicesOf(true), 'invalid'), part(indicesOf(false), 'notice')].filter(Boolean).join(t('ui.form.summary_sep'))
  const canGo = problems.length > 0
  const goToFirst = () => {
    const list = gather()
    for (const p of list) {
      if (p.el && focusProblem(p.el)) { setHidden(null); return }
      if (p.focus) { p.focus(); setHidden(null); return }
    }
    // Nothing could take focus (what holds them is not shown, and cannot be asked to show them):
    // say so rather than do nothing.
    setHidden({ name: list[0]?.label })
  }
  const hiddenText = hidden && canGo ? (hidden.name ? t('ui.form.invalid_hidden', { name: hidden.name }) : t('ui.form.invalid_hidden_unnamed')) : null
  const spoken = pending ? t('ui.form.saving') : hiddenText ?? (dirty ? (blocked && count > 0 ? t('ui.form.invalid_status', { count }) : t('ui.form.unsaved')) : '')
  const bar = (
    <div
      role="region"
      aria-label={t('ui.form.unsaved')}
      data-slot="save-bar"
      className={cn(
        // A floating card, not a strip attached to the edge. While the page scrolls it sticks 16px above the viewport's
        // bottom edge; sticky is bounded by the container's content box, so at the end of the page it rests in normal flow
        // with exactly the container's own bottom padding below it (no band, no divider line). `mt-4` is the gap above it
        // that every section has; a page may override it.
        footerSlot
          ? 'flex w-full flex-wrap items-center gap-x-4 gap-y-2'
          : 'sticky bottom-4 z-20 mt-4 flex min-h-14 flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border bg-popover/95 px-4 py-2.5 shadow-lg backdrop-blur supports-backdrop-filter:bg-popover/80',
        className,
      )}
    >
      <p id={reasonId} className={cn('min-h-5 min-w-0 flex-1 text-sm font-medium', reason && (blocked ? 'text-destructive' : 'text-warning-ink'))}>
        {reason && canGo ? (
          <button
            type="button"
            onClick={goToFirst}
            title={t('ui.form.invalid_goto')}
            className="-mx-1 rounded-md px-1 text-left underline decoration-dotted underline-offset-4 outline-none hover:decoration-solid focus-visible:ring-3 focus-visible:ring-ring/50"
          >{reason}</button>
        ) : (reason ?? message ?? t('ui.form.unsaved'))}
        {/* A real space, so the two texts read apart in the accessible name as well as on screen. */}
        {hiddenText && <>{' '}<span className="mt-0.5 block text-xs font-normal">{hiddenText}</span></>}
      </p>
      <div className="flex items-center gap-2">
        <Button variant="outline" onClick={() => { discard(); onDiscard() }} disabled={pending}>{t('ui.form.discard')}</Button>
        {/* aria-disabled, not disabled: a click on it does nothing, but it stays focusable, and
            the reason beside it is what describes it. */}
        <Button
          onClick={() => { if (!blocked) onSave(); else if (canGo) goToFirst() }}
          disabled={pending}
          aria-disabled={blocked || undefined}
          aria-describedby={blocked ? reasonId : undefined}
          aria-busy={pending ? true : undefined}
          className={cn(blocked && 'cursor-not-allowed opacity-50')}
        >
          {pending && <Loader2Icon className="animate-spin" aria-hidden="true" />}
          {pending ? t('ui.form.saving') : (saveLabel ?? t('actions.save'))}
        </Button>
      </div>
    </div>
  )
  return (
    <>
      {/* Always in the page: a live region announces a change in its text, not its own arrival. */}
      <p role="status" className="sr-only">{spoken}</p>
      {visible && (footerSlot ? createPortal(bar, footerSlot) : bar)}
    </>
  )
}
