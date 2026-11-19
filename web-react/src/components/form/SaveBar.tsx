import { useId, type ReactNode } from 'react'
import { Loader2Icon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { focusProblem, sectionTitleOf, useFormValidity } from './FormValidity'

/** A problem the page found itself (a rule on its own draft, a server refusal). */
export interface SaveBarError {
  /** The field's name; without it the problem is counted but cannot be named. */
  label?: string
  message: string
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
   * Problems the page holds that no field reported. A Field given an `error` already
   * reports it, so this is for the rest (a rule across fields, a server refusal). They are
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

interface Problem { label?: string; message: string; el: HTMLElement | null; focus?: () => void }

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
 * The sticky bar at the bottom of a long form. It appears only when there is
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
  const visible = dirty || pending
  const { issues, discard } = useFormValidity()
  const reasonId = useId()
  // Built again at click time: an element named by id may not have been in the page when this rendered.
  const gather = () => inPageOrder([
    ...issues.map(i => ({ label: i.label, message: i.message, el: i.el ?? null })),
    ...(errors ?? []).map(e => ({ label: e.label, message: e.message, el: e.el ?? (e.id ? document.getElementById(e.id) : null), focus: e.focus })),
  ])
  const problems = gather()
  // `disabled` with a message is one more problem, not a replacement for the others.
  const legacy = disabled === true && message != null
  const all: Problem[] = legacy ? [...problems, { label: typeof message === 'string' ? message : undefined, message: '', el: null }] : problems
  const count = all.length
  const blocked = disabled === true || problems.length > 0

  // Two fields called "Port" read as "Port, Port": tell them apart by their section, then by position.
  const named = all.map(p => p.label)
  const dup = (l: string | undefined, list: (string | undefined)[]) => l !== undefined && list.filter(x => x === l).length > 1
  const original = [...named]
  if (count > 1) {
    all.forEach((p, i) => {
      if (!p.label || !dup(p.label, original)) return
      const section = sectionTitleOf(p.el)
      if (section) named[i] = `${section} › ${p.label}`
    })
    const base = [...named]
    const seen: Record<string, number> = {}
    named.forEach((l, i) => {
      if (l && dup(l, base)) { seen[l] = (seen[l] ?? 0) + 1; named[i] = t('ui.form.invalid_nth', { label: l, n: seen[l] }) }
    })
  }
  const reason = problems.length === 0 ? null
    : count === 1 ? (problems[0].label ? t('ui.form.invalid_field', { label: problems[0].label, message: problems[0].message }) : problems[0].message)
      : named.every(Boolean) ? t('ui.form.invalid_named', { count, names: named.join(t('ui.form.name_sep')) })
        : t('ui.form.invalid_count', { count })
  const canGo = problems.length > 0
  const goToFirst = () => {
    for (const p of gather()) {
      if (p.el && focusProblem(p.el)) return
      if (p.focus) { p.focus(); return }
    }
  }
  const spoken = pending ? t('ui.form.saving') : dirty ? (blocked && count > 0 ? t('ui.form.invalid_status', { count }) : t('ui.form.unsaved')) : ''
  return (
    <>
      {/* Always in the page: a live region announces a change in its text, not its own arrival. */}
      <p role="status" className="sr-only">{spoken}</p>
      {visible && (
    <div
      role="region"
      aria-label={t('ui.form.unsaved')}
      data-slot="save-bar"
      className={cn(
        'sticky bottom-0 z-20 -mx-1 flex min-h-14 flex-wrap items-center gap-x-4 gap-y-2 rounded-t-xl border-t bg-popover/95 px-4 py-2.5 shadow-[0_-4px_12px_-6px_rgb(0_0_0/0.2)] backdrop-blur supports-backdrop-filter:bg-popover/80',
        className,
      )}
    >
      <p id={reasonId} className={cn('min-h-5 min-w-0 flex-1 text-sm font-medium', reason && 'text-destructive')}>
        {reason && canGo ? (
          <button
            type="button"
            onClick={goToFirst}
            title={t('ui.form.invalid_goto')}
            className="-mx-1 rounded-md px-1 text-left underline decoration-dotted underline-offset-4 outline-none hover:decoration-solid focus-visible:ring-3 focus-visible:ring-ring/50"
          >{reason}</button>
        ) : (reason ?? message ?? t('ui.form.unsaved'))}
      </p>
      <div className="flex items-center gap-2">
        <Button variant="ghost" onClick={() => { discard(); onDiscard() }} disabled={pending}>{t('ui.form.discard')}</Button>
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
      )}
    </>
  )
}
