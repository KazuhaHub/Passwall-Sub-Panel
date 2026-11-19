import { useEffect, useRef, useState, useSyncExternalStore, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { focusFallbackTarget, focusIsLost } from './focusFallback'
import { Dialog as DialogPrimitive } from '@base-ui/react/dialog'

import { TONE_STYLE, type Tone } from '@/components/common/tones'
import { TOUCH_TARGET } from '@/components/common/touchTarget'
import { Button } from '@/components/ui/button'
import { IconClose, IconError, IconInfo, IconSuccess, IconWarning } from '@/lib/icons'
import { useInertMuiModals } from '@/lib/inertModals'
import { cn } from '@/lib/utils'
import { useSpacedT } from '@/i18n/spacing'
import { useAuthStore } from '@/stores/auth'
import { attachToastStore, registerToastLogOpener } from './toasts'
import { createToastStore, type LogEntry, type Severity, type ToastView } from './toastStore'

// Stacking, from the bottom: MUI modals, drawers and popovers 1300-1302, MUI tooltips
// 1500, the confirm dialog 1600, then these. A toast about the answer to a confirm must
// not be hidden by it, so toasts sit above it, and the log panel opened from a toast
// above them.
const TOASTS_Z = 'z-[1700]'
const PANEL_Z = 'z-[1800]'

const EXIT_MS = 150
// The cross-fade when toasts already on screen move to the top stack: out here, then in there.
const RELOCATE_MS = 150
// How long an announcement node stays in its live region before it is removed.
const ANNOUNCE_MS = 7000
// Of the messages announced within one window, the first few are read out one by one and the
// rest are one sentence. The window is not a single tick: six errors arriving in separate
// tasks were six assertive interruptions.
const ANNOUNCE_EACH = 3
const ANNOUNCE_WINDOW_MS = 250

// Only the icon is the toast's own; the colour is common/tones.ts's, so a toast, a badge and a dot
// can never disagree about what "warning" looks like.
const SEVERITY: Record<Severity, { Icon: typeof IconInfo; tone: Tone }> = {
  success: { Icon: IconSuccess, tone: 'success' },
  info: { Icon: IconInfo, tone: 'info' },
  warning: { Icon: IconWarning, tone: 'warning' },
  error: { Icon: IconError, tone: 'danger' },
}

// "还有 6 条提示，其中 4 条错误": a summary says what it holds, errors and warnings first.
function summaryText(t: (key: string, o?: Record<string, unknown>) => string, v: ToastView): string {
  const count = v.pending ?? 0
  const errors = v.errors ?? 0
  const warnings = v.warnings ?? 0
  if (errors && warnings) return t('ui.feedback.more_toasts_both', { count, errors, warnings })
  if (errors) return t('ui.feedback.more_toasts_errors', { count, errors })
  if (warnings) return t('ui.feedback.more_toasts_warnings', { count, warnings })
  return t('ui.feedback.more_toasts', { count })
}

function ToastRow({ view, leaving, fading, onDismiss, onHover }: {
  view: ToastView
  leaving?: boolean
  /** Fading out ahead of being moved to the other stack. */
  fading?: boolean
  onDismiss: () => void
  onHover: (on: boolean) => void
}) {
  const { t } = useSpacedT()
  const { Icon, tone } = SEVERITY[view.severity]
  const ink = TONE_STYLE[tone].ink
  return (
    <li
      data-slot="toast"
      data-toast-id={view.id}
      data-severity={view.severity}
      data-kind={view.kind}
      data-leaving={leaving ? '' : undefined}
      aria-hidden={leaving || undefined}
      onPointerEnter={() => onHover(true)}
      onPointerLeave={() => onHover(false)}
      className={cn(
        'pointer-events-auto flex w-full items-start gap-2.5 rounded-lg bg-popover px-3 py-2.5 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10',
        leaving
          ? 'pointer-events-none absolute inset-x-0 bottom-0 animate-out fade-out-0 fill-mode-forwards duration-150'
          : 'animate-in fade-in-0 slide-in-from-bottom-2 duration-150',
        fading && 'opacity-0 transition-opacity duration-150',
      )}
    >
      <Icon aria-hidden className={cn('mt-0.5 size-4 shrink-0', ink)} />
      <p className="min-w-0 flex-1 break-words">
        {view.kind === 'summary' ? summaryText(t, view) : view.message}
      </p>
      {view.count > 1 && <span data-slot="toast-count" className="shrink-0 rounded bg-muted px-1 text-xs tabular-nums text-muted-foreground">×{view.count}</span>}
      {view.action && (
        <Button type="button" variant="outline" size="xs" className={cn('shrink-0', TOUCH_TARGET)} onClick={view.action.onClick}>{view.action.label}</Button>
      )}
      <Button type="button" variant="ghost" size="icon-xs" aria-label={t('actions.close')} className={cn('-my-0.5 -mr-1 shrink-0', TOUCH_TARGET)} onClick={onDismiss}>
        <IconClose />
      </Button>
    </li>
  )
}

function LogPanel({ open, onClose, entries, omitted, returnFocus, language }: {
  /** The app's current language, for the time format: not the browser's. */
  language?: string
  open: boolean
  onClose: () => void
  entries: readonly LogEntry[]
  omitted: number
  /** Where focus goes when the panel closes: never back into the toast stack. */
  returnFocus: () => HTMLElement | false
}) {
  const { t } = useSpacedT()
  useInertMuiModals(open)
  const rows = [...entries].reverse()
  return (
    <DialogPrimitive.Root
      open={open}
      onOpenChange={o => { if (!o) onClose() }}
      onOpenChangeComplete={o => {
        if (o) return
        const el = returnFocus()
        if (el) { el.focus(); return }
        // Nowhere proper to go back to. Only step in when focus really is lost: if something
        // else already holds it, it is not ours to move.
        if (focusIsLost()) focusFallbackTarget()?.focus({ preventScroll: true })
      }}
    >
      <DialogPrimitive.Portal>
        <DialogPrimitive.Backdrop className={cn('fixed inset-0 isolate bg-black/10 duration-100 data-open:animate-in data-open:fade-in-0', PANEL_Z)} />
        <DialogPrimitive.Popup
          data-slot="toast-log"
          // Focus is returned by the host (onOpenChangeComplete below), not by the library: the
          // library's own return is to the trigger or the previously focused element, which here
          // is a toast that has since gone.
          finalFocus={false}
          className={cn('fixed top-1/2 left-1/2 flex max-h-[80vh] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-xl bg-popover p-4 text-sm text-popover-foreground ring-1 ring-foreground/10 outline-none', PANEL_Z)}
        >
          <div className="flex items-center justify-between gap-2">
            <DialogPrimitive.Title className="text-base font-medium">{t('ui.feedback.log_title')}</DialogPrimitive.Title>
            <DialogPrimitive.Close render={<Button type="button" variant="ghost" size="icon-sm" aria-label={t('actions.close')} className={TOUCH_TARGET} />}>
              <IconClose />
            </DialogPrimitive.Close>
          </div>
          <DialogPrimitive.Description className="text-muted-foreground">{t('ui.feedback.log_description')}</DialogPrimitive.Description>
          {rows.length === 0 ? (
            <p className="py-6 text-center text-muted-foreground">{t('ui.feedback.log_empty')}</p>
          ) : (
            <ul className="-mx-1 flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-1">
              {rows.map(l => {
                const { Icon, tone } = SEVERITY[l.severity]
                const ink = TONE_STYLE[tone].ink
                return (
                  <li key={l.id} data-slot="toast-log-row" data-state={l.state} className="flex items-start gap-2 rounded-md px-1.5 py-1.5 hover:bg-muted">
                    <Icon aria-hidden className={cn('mt-0.5 size-4 shrink-0', ink)} />
                    <span className="min-w-0 flex-1 break-words">
                      <span className="sr-only">{t('ui.feedback.severity_prefix', { severity: t(`ui.feedback.severity.${l.severity}`) })}</span>
                      {l.message}
                      {l.count > 1 && <span className="ml-1 text-xs text-muted-foreground tabular-nums">×{l.count}</span>}
                    </span>
                    <time dateTime={new Date(l.at).toISOString()} className="shrink-0 text-xs text-muted-foreground tabular-nums">
                      {new Date(l.at).toLocaleTimeString(language)}
                    </time>
                  </li>
                )
              })}
              {omitted > 0 && <li data-slot="toast-log-omitted" className="px-1.5 py-1 text-xs text-muted-foreground">{t('ui.feedback.log_omitted', { count: omitted })}</li>}
            </ul>
          )}
        </DialogPrimitive.Popup>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

// The toast layer's own containers are exempt from what dialog libraries do to the rest of
// the page. Base UI and MUI set aria-hidden (and Base UI inert) on every body child that is
// not the dialog, which silences a live region: an error pushed from inside a dialog was
// never announced. Each container carries an explicit aria-live (which Base UI leaves
// alone), and this strips aria-hidden/inert the moment any library puts it back (MUI's
// modal hides body children without looking).
function useLiveExempt(...refs: RefObject<HTMLElement | null>[]) {
  useEffect(() => {
    const observers: MutationObserver[] = []
    for (const ref of refs) {
      const el = ref.current
      if (!el) continue
      const strip = () => {
        el.removeAttribute('aria-hidden')
        el.removeAttribute('inert')
        el.removeAttribute('data-base-ui-inert')
      }
      strip()
      const observer = new MutationObserver(strip)
      observer.observe(el, { attributes: true, attributeFilter: ['aria-hidden', 'inert', 'data-base-ui-inert'] })
      observers.push(observer)
    }
    return () => observers.forEach(o => o.disconnect())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
}

const NARROW = '(max-width: 767px)'
// MODAL DIALOGS only: role dialog with aria-modal (Base UI's and MUI's both carry it) or an
// alertdialog. Not menus, listboxes or popovers (an MUI Select on a phone is a Popover with
// role=listbox, and must not move the toasts), not the mobile navigation sheet, and not the
// toasts' own log panel, whose title the toasts would sit on.
const MODAL = '[role="dialog"][aria-modal="true"]:not([data-slot="toast-log"]):not([data-slot="sidebar"]), [role="alertdialog"]'

// True on a phone-width screen while a modal dialog is open. Toasts that ENTER then are placed
// at the top of the screen: at the bottom they sat over the dialog's own buttons (a confirm's
// Cancel at 360x640, its footer at 390x600). Toasts already on screen are moved too, by a
// cross-fade (see relocate in the host), and not while the pointer is over the stack or
// keyboard focus is inside it: nothing jumps under the user's hand.
function useToastsAtTop(): boolean {
  const [top, setTop] = useState(false)
  useEffect(() => {
    const mql = typeof window.matchMedia === 'function' ? window.matchMedia(NARROW) : null
    const sync = () => setTop(Boolean(mql?.matches) && document.querySelector(MODAL) !== null)
    sync()
    const observer = new MutationObserver(sync)
    observer.observe(document.body, { childList: true })
    mql?.addEventListener?.('change', sync)
    return () => { observer.disconnect(); mql?.removeEventListener?.('change', sync) }
  }, [])
  return top
}

/**
 * THE TOAST HOST: the one component that draws toasts, over a store (toastStore.ts) that
 * owns the list and the time. Mount it once. It portals to <body>, bottom centre (top on a
 * phone while a dialog is open), above everything; offset by `--toast-bottom-offset` (a
 * bottom sticky save bar sets that variable, see useToastBottomOffset, so toasts never cover it).
 *
 * Announcements are made by two live regions that are rendered once and never remounted:
 * role=alert for errors and warnings (assertive), role=status for the rest (polite). Each
 * new message is announced exactly once by APPENDING a node to the right region, so a few
 * pushes in one tick are a few announcements rather than one last-write-wins text; past
 * three in one tick the rest are announced as one sentence with the number of errors in it.
 * The toasts themselves are not live regions.
 *
 * Time is paused by the pointer being over a toast and by KEYBOARD focus inside the toasts,
 * not by any focus: a mouse click on a toast, or the focus handed back when the log closes,
 * must not freeze every countdown.
 */
export default function ToastHost() {
  const { t, i18n } = useSpacedT()
  const tRef = useRef(t)
  tRef.current = t
  const alertRegion = useRef<HTMLDivElement>(null)
  const statusRegion = useRef<HTMLDivElement>(null)
  const bottomRoot = useRef<HTMLDivElement>(null)
  const topRoot = useRef<HTMLDivElement>(null)

  const [store] = useState(() => {
    const append = (region: HTMLElement | null, text: string) => {
      if (!region) return
      const node = document.createElement('div')
      node.textContent = text
      region.appendChild(node)
      setTimeout(() => node.remove(), ANNOUNCE_MS)
    }
    const tick = { n: 0, rest: 0, errors: 0, urgent: false, timer: null as ReturnType<typeof setTimeout> | null }
    const flush = () => {
      tick.timer = null
      if (tick.rest > 0) {
        const text = tick.errors > 0
          ? tRef.current('ui.feedback.announce_more_errors', { count: tick.rest, errors: tick.errors })
          : tRef.current('ui.feedback.announce_more', { count: tick.rest })
        append((tick.urgent ? alertRegion : statusRegion).current, text)
      }
      tick.n = 0; tick.rest = 0; tick.errors = 0; tick.urgent = false
    }
    return createToastStore({
      now: () => Date.now(),
      setTimer: (fn, ms) => setTimeout(fn, ms),
      clearTimer: h => clearTimeout(h as ReturnType<typeof setTimeout>),
      onAnnounce: ({ message, severity }) => {
        const urgent = severity === 'error' || severity === 'warning'
        if (tick.n < ANNOUNCE_EACH) {
          tick.n++
          append((urgent ? alertRegion : statusRegion).current, message)
        } else {
          tick.rest++
          if (severity === 'error') tick.errors++
          if (urgent) tick.urgent = true
        }
        if (tick.timer === null) tick.timer = setTimeout(flush, ANNOUNCE_WINDOW_MS)
      },
    })
  })
  const state = useSyncExternalStore(store.subscribe, store.getState)

  // Attach on mount (the cleanup detaches; StrictMode's replay re-attaches the same store,
  // and the buffer was emptied by the first attach, so nothing is pushed twice).
  useEffect(() => {
    store.setActive(true)
    const detach = attachToastStore(store)
    return () => { detach(); store.setActive(false) }
  }, [store])

  useLiveExempt(alertRegion, statusRegion, bottomRoot, topRoot)

  // A hidden tab pauses every countdown.
  useEffect(() => {
    const sync = () => store.setPaused('hidden', document.hidden)
    sync()
    document.addEventListener('visibilitychange', sync)
    return () => document.removeEventListener('visibilitychange', sync)
  }, [store])

  // Keyboard modality: focus pauses time only when it got there by keyboard. A click puts
  // focus on the button it hit, which says nothing about whether anyone is reading. A bare
  // modifier (⌘, Ctrl, Alt, Shift on their own, or with another key held) is not navigation.
  const keyboard = useRef(false)
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'Shift' || e.key === 'Control' || e.key === 'Alt' || e.key === 'Meta') return
      keyboard.current = true
    }
    const pointer = () => { keyboard.current = false }
    document.addEventListener('keydown', key, true)
    document.addEventListener('pointerdown', pointer, true)
    document.addEventListener('mousedown', pointer, true)
    document.addEventListener('touchstart', pointer, true)
    return () => {
      document.removeEventListener('keydown', key, true)
      document.removeEventListener('pointerdown', pointer, true)
      document.removeEventListener('mousedown', pointer, true)
      document.removeEventListener('touchstart', pointer, true)
    }
  }, [])

  // Hover pauses while the pointer is over any toast. Tracked by id, and pruned as toasts
  // go: a toast removed under the pointer never fires its own pointerleave.
  const hovered = useRef(new Set<number>())
  useEffect(() => {
    const ids = new Set(state.visible.map(v => v.id))
    for (const id of hovered.current) if (!ids.has(id)) hovered.current.delete(id)
    store.setPaused('hover', hovered.current.size > 0)
  }, [state.visible, store])

  // KEYBOARD FOCUS pauses too, but it is derived from where focus actually is, never from
  // blur events: a browser fires no blur when the focused element is removed from the DOM,
  // so a pause released only on blur stayed on for ever when the toast holding focus was
  // pre-empted, expired or collapsed into the log. It is recomputed whenever the list
  // changes, the log opens or closes, and focus moves.
  const inStack = (el: Element | null) => el?.closest?.('[data-psp-toast-stack]') != null
  const focusByKeyboard = useRef(false)
  const focusedId = useRef<number | null>(null)
  const syncFocusPause = () => store.setPaused('focus', inStack(document.activeElement) && focusByKeyboard.current)

  // Toasts that left keep animating out on their own, here, without holding a slot or a key.
  // Each has its own removal timer: one timer for the batch was cleared by the next change
  // of the list, and the exiting node stayed for ever.
  const [leaving, setLeaving] = useState<ToastView[]>([])
  const previous = useRef<readonly ToastView[]>([])
  const exitTimers = useRef(new Map<number, ReturnType<typeof setTimeout>>())
  // Where each toast was placed when it entered (see useToastsAtTop): it stays there.
  const placements = useRef(new Map<number, 'top' | 'bottom'>())
  const atTop = useToastsAtTop()
  for (const v of state.visible) if (!placements.current.has(v.id)) placements.current.set(v.id, atTop ? 'top' : 'bottom')

  const [panelOpen, setPanelOpen] = useState(false)

  // THE LOG BELONGS TO WHOEVER IS SIGNED IN. It lives in memory, and a sign-out in another tab
  // re-renders this one without reloading it, so when the identity changes (the user, or the
  // session generation) everything is forgotten: the toasts, the queue, the log, what the
  // live regions still hold. Nobody who signs in next can read the previous user's messages.
  const identity = useAuthStore(s => `${s.userId ?? ''}:${s.authEpoch}`)
  const lastIdentity = useRef(identity)
  useEffect(() => {
    if (identity === lastIdentity.current) return
    lastIdentity.current = identity
    store.reset()
    alertRegion.current?.replaceChildren()
    statusRegion.current?.replaceChildren()
    hovered.current.clear()
    placements.current.clear()
    store.setPaused('hover', false)
    setLeaving([])
    // The panel is closed here but focus is NOT moved here: in a browser the panel animates out
    // and its focused button is still in the DOM for a few frames, so a frame-delayed focus call
    // ran too early and found focus "still somewhere". LogPanel's onOpenChangeComplete restores
    // focus once the panel has really left; with no return target (the previous user's page is
    // gone) it lands on the account button or the main landmark, never on <body>.
    panelReturn.current = null
    restoreTo.current = null
    setPanelOpen(false)
  }, [identity, store])

  // When a modal dialog opens on a phone, toasts already at the bottom move to the top stack
  // so they do not cover its buttons: a cross-fade (out, then in at the new place, no slide
  // across the screen), and never while the pointer is over the stack or keyboard focus is
  // inside it, which would move them under the user's hand; those wait for the interaction
  // to end (relocate() is called again then).
  const [fading, setFading] = useState<ReadonlySet<number>>(new Set())
  const relocating = useRef(false)
  const relocate = () => {
    if (!atTopRef.current || relocating.current) return
    if (hovered.current.size > 0 || (inStack(document.activeElement) && focusByKeyboard.current)) return
    const ids = stateRef.current.visible.filter(v => placements.current.get(v.id) === 'bottom').map(v => v.id)
    if (ids.length === 0) return
    // With reduced motion there is no fade: they simply move.
    if (typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      for (const id of ids) placements.current.set(id, 'top')
      setFading(new Set())
      return
    }
    relocating.current = true
    setFading(new Set(ids))
    setTimeout(() => {
      for (const id of ids) if (placements.current.get(id) === 'bottom') placements.current.set(id, 'top')
      relocating.current = false
      setFading(new Set())
    }, RELOCATE_MS)
  }
  const atTopRef = useRef(atTop)
  atTopRef.current = atTop
  const relocateRef = useRef(relocate)
  relocateRef.current = relocate
  useEffect(() => { if (atTop) relocateRef.current() }, [atTop, state.visible])

  useEffect(() => {
    const before = previous.current
    const gone = before.filter(p => !state.visible.some(v => v.id === p.id))
    previous.current = state.visible

    // A toast that held keyboard focus and was removed by anything but its own ×: focus has
    // fallen to <body> and a keyboard user would be dropped silently. Move it on, to the
    // dismiss button of the toast that took its place, but only if focus really has fallen to
    // <body>: a dialog or a field that took focus in the same commit keeps it.
    if (focusedId.current !== null && !state.visible.some(v => v.id === focusedId.current)) {
      const index = before.findIndex(p => p.id === focusedId.current)
      focusedId.current = null
      const active = document.activeElement
      if (focusByKeyboard.current && (!active || active === document.body)) {
        const next = state.visible[Math.min(Math.max(index, 0), state.visible.length - 1)]
        const button = next ? document.querySelector<HTMLElement>(`[data-toast-id="${next.id}"] button[aria-label]`) : null
        if (button) button.focus()
        else focusByKeyboard.current = false
      } else if (!inStack(active)) {
        focusByKeyboard.current = false
      }
    }
    syncFocusPause()

    if (gone.length === 0) return
    setLeaving(l => [...l, ...gone])
    for (const g of gone) {
      exitTimers.current.set(g.id, setTimeout(() => {
        exitTimers.current.delete(g.id)
        placements.current.delete(g.id)
        setLeaving(l => l.filter(x => x.id !== g.id))
      }, EXIT_MS))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.visible])
  useEffect(() => { syncFocusPause() }, [panelOpen]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    const timers = exitTimers.current
    return () => { timers.forEach(clearTimeout); timers.clear() }
  }, [])

  // Focus returns to where it was: remember what had focus when it moved into the toasts.
  const restoreTo = useRef<HTMLElement | null>(null)
  const panelReturn = useRef<HTMLElement | null>(null)
  const omittedAtOpen = useRef(0)
  const stateRef = useRef(state)
  stateRef.current = state

  function dismiss(id: number) {
    store.dismiss(id)
    focusedId.current = null
    focusByKeyboard.current = false
    store.setPaused('focus', false)
    const back = restoreTo.current
    restoreTo.current = null
    if (back?.isConnected) back.focus()
  }

  const openLog = () => {
    // Remember what had focus before the panel, for when it closes: never the toast stack.
    const active = document.activeElement
    panelReturn.current = active instanceof HTMLElement && !inStack(active) && active !== document.body ? active : restoreTo.current
    omittedAtOpen.current = stateRef.current.omitted
    store.openLog()
    // Opening the log is looking at it: the unread count clears now, not when it is closed.
    store.markSeen()
    // Nothing to release here: the summary that held focus is gone, and the recompute that
    // follows the list change and the panel opening finds focus outside the stack.
    setPanelOpen(true)
  }
  const openLogRef = useRef(openLog)
  openLogRef.current = openLog
  useEffect(() => registerToastLogOpener(() => openLogRef.current()), [])

  const visible = state.visible.map(v => v.kind === 'summary' ? { ...v, action: { label: t('ui.feedback.view_all'), onClick: openLog } } : v)
  const rowsAt = (place: 'top' | 'bottom') => visible.filter(v => (placements.current.get(v.id) ?? 'bottom') === place)
  const leavingAt = (place: 'top' | 'bottom') => leaving.filter(v => (placements.current.get(v.id) ?? 'bottom') === place)

  const olHandlers = {
    onFocus: (e: React.FocusEvent<HTMLOListElement>) => {
      const from = e.relatedTarget
      if (from instanceof HTMLElement && !inStack(from)) restoreTo.current = from
      const row = (e.target as Element).closest('[data-toast-id]')
      focusedId.current = row ? Number(row.getAttribute('data-toast-id')) : null
      focusByKeyboard.current = keyboard.current
      syncFocusPause()
    },
    onBlur: (e: React.FocusEvent<HTMLOListElement>) => {
      if (!inStack(e.relatedTarget as Element | null)) {
        focusByKeyboard.current = false
        focusedId.current = null
        store.setPaused('focus', false)
        relocateRef.current()
      }
    },
  }

  const stack = (place: 'top' | 'bottom', root: RefObject<HTMLDivElement | null>) => {
    const rows = rowsAt(place)
    const out = leavingAt(place)
    return (
      <div
        ref={root}
        // Explicit aria-live (off: the toasts are not live regions) so the dialog libraries leave it alone.
        aria-live="off"
        data-psp-live-exempt=""
        data-psp-toast-stack=""
        data-position={place}
        className={cn('pointer-events-none fixed inset-x-0 flex justify-center px-4', place === 'top' ? 'top-0' : 'bottom-0', TOASTS_Z)}
        style={place === 'top'
          ? { paddingTop: '1rem' }
          : { paddingBottom: 'calc(1rem + var(--toast-bottom-offset, 0px))' }}
      >
        {(rows.length > 0 || out.length > 0) && (
          <ol aria-label={t('ui.feedback.notifications')} className="relative flex w-full max-w-md flex-col gap-2" {...olHandlers}>
            {rows.map(v => (
              <ToastRow
                key={v.id}
                view={v}
                onDismiss={() => dismiss(v.id)}
                fading={fading.has(v.id)}
                onHover={on => {
                  if (on) hovered.current.add(v.id); else hovered.current.delete(v.id)
                  store.setPaused('hover', hovered.current.size > 0)
                  if (!on) relocateRef.current()
                }}
              />
            ))}
            {out.map(v => <ToastRow key={`leaving-${v.id}`} view={v} leaving onDismiss={() => {}} onHover={() => {}} />)}
          </ol>
        )}
      </div>
    )
  }

  return (
    <>
      {createPortal(<div ref={alertRegion} role="alert" aria-live="assertive" data-slot="toast-live-alert" data-psp-live-exempt="" className="sr-only" />, document.body)}
      {createPortal(<div ref={statusRegion} role="status" aria-live="polite" data-slot="toast-live-status" data-psp-live-exempt="" className="sr-only" />, document.body)}
      {/* Top before bottom, so Tab order follows what is on screen. */}
      {createPortal(stack('top', topRoot), document.body)}
      {createPortal(stack('bottom', bottomRoot), document.body)}
      <LogPanel
        open={panelOpen}
        entries={state.log}
        omitted={panelOpen ? omittedAtOpen.current : 0}
        language={i18n.resolvedLanguage || i18n.language}
        returnFocus={() => (panelReturn.current?.isConnected && !inStack(panelReturn.current) ? panelReturn.current : false)}
        onClose={() => { setPanelOpen(false); store.acknowledgeLog() }}
      />
    </>
  )
}
