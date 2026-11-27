import { useEffect, useRef, type MutableRefObject, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { IconButton } from '@/components/common/IconButton'
import { IconClose } from '@/lib/icons'
import { cn } from '@/lib/utils'

/** The top of the page's sticky strip (top bar, tabs, toolbar): the panel sticks just under it and is no taller than what is left. */
const STICK = 'var(--rs-stick, 10rem)'
/** The page's floating Save bar, when it is shown (`--rs-bottom` is set by the page): the panel ends above it, so the end of an answer is never under it. */
const BOTTOM = 'var(--rs-bottom, 0px)'

/**
 * The frame of the routing test next to the rules (design 3.2 and 3.9): a third column on a very wide page (`dock`), a panel over
 * the right side of the page below that (`overlay`). Both stay under the sticky toolbar while the page scrolls and scroll inside
 * themselves, so a long answer never pushes the rules away.
 *
 * The overlay is a dialog that does NOT take the page from the person: the rules stay in view and usable on the left (the panel
 * covers the right ~26rem of the group), and Escape or the close button put it away. It is deliberately not a modal: with the
 * answer open, a rule or a group is still one click away. `data-nonmodal` lets the page's own shortcuts (Ctrl+S) tell it from a dialog
 * that is the thing being worked on.
 */
export function TesterPanel({ placement, asked, onClose, children }: { placement: 'dock' | 'overlay'; /** Set by the button that opens the test: focus goes to the address once, and the request is cleared, so a remount or a move between dock and overlay never takes focus again. */ asked: MutableRefObject<boolean>; onClose: () => void; children: ReactNode }) {
  const { t } = useTranslation(['admin'])
  const box = useRef<HTMLDivElement>(null)
  const title = t('admin:rules.tester.title')
  // Opened by its button: the address is what comes next.
  useEffect(() => {
    if (!asked.current) return
    asked.current = false
    box.current?.querySelector<HTMLElement>('input, select')?.focus({ preventScroll: true })
  }, [asked])
  // Escape closes the over-the-page panel from anywhere on the page, not only from inside it (unless a modal dialog is on top, which it belongs to).
  useEffect(() => {
    if (placement !== 'overlay') return
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return
      if (document.querySelector('[role="dialog"]:not([data-nonmodal]), [role="alertdialog"], [role="menu"], [role="listbox"]')) return
      onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [placement, onClose])

  const header = (
    <div className="flex items-center justify-between gap-2">
      <h2 className="text-sm font-semibold">{title}</h2>
      <IconButton label={t('admin:rules.tester.hide_tester')} size="icon-xs" onClick={onClose}><IconClose /></IconButton>
    </div>
  )
  const surface = cn('flex min-w-0 flex-col gap-3 overflow-y-auto overscroll-contain rounded-xl border bg-popover p-3 text-popover-foreground')
  const height = { top: `calc(${STICK} + 0.5rem)`, maxHeight: `calc(100dvh - ${STICK} - 3rem - ${BOTTOM})` }

  if (placement === 'dock') {
    return (
      <aside ref={box} aria-label={title} className={cn(surface, 'sticky self-start')} style={height}>
        {header}
        {children}
      </aside>
    )
  }
  return (
    <div className="pointer-events-none absolute inset-y-0 right-0 z-20 flex w-[min(26rem,100%)] items-start justify-end">
      <div
        ref={box} role="dialog" aria-modal="false" aria-label={title} data-nonmodal
        className={cn(surface, 'pointer-events-auto sticky w-full shadow-lg motion-safe:animate-in motion-safe:fade-in-0 motion-safe:slide-in-from-right-4 motion-safe:duration-150')}
        style={height}
      >
        {header}
        {children}
      </div>
    </div>
  )
}
