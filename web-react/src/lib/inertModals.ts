import { useEffect } from 'react'

/**
 * While `active`, every MUI modal on the page is made inert, including one that opens
 * AFTER this started (a MutationObserver on body's children sees MUI's portal arrive),
 * and all of it is released when `active` ends. MUI's focus trap pulls focus back into
 * its own dialog whenever it leaves it, which let Tab walk out of a Tailwind dialog into
 * the MUI dialog behind it, and an Escape then closed THAT dialog instead of the one on
 * top. An inert subtree cannot take focus or key events, so the dialog on top is the only
 * live thing. Used by the confirm and the toast log panel. Goes away with MUI.
 *
 * A modal that opens while this is active also tries to take focus, and can succeed in the
 * instant before it is made inert; making the element that holds focus inert drops focus to
 * <body>, from where Tab walks into the page behind both dialogs. So the last element
 * focused outside any MUI modal is remembered, and focus goes back to it when a late modal
 * is made inert and took focus with it.
 */
export function useInertMuiModals(active: boolean) {
  useEffect(() => {
    if (!active) return
    const held = new Set<HTMLElement>()
    const modals = () => document.querySelectorAll<HTMLElement>('.MuiModal-root:not(.MuiModal-hidden)')
    let lastFocus: HTMLElement | null = document.activeElement instanceof HTMLElement && !document.activeElement.closest('.MuiModal-root') ? document.activeElement : null
    const onFocusIn = (e: FocusEvent) => {
      if (e.target instanceof HTMLElement && !e.target.closest('.MuiModal-root')) lastFocus = e.target
    }
    document.addEventListener('focusin', onFocusIn, true)

    // Tracked so cleanup can cancel them: a late restore that fires after the
    // page (or a test's DOM) is gone has no document to read.
    const timers = new Set<ReturnType<typeof setTimeout>>()
    const restore = (late: HTMLElement[]) => {
      if (typeof document === 'undefined') return
      const ae = document.activeElement
      const lost = !ae || ae === document.body || late.some(el => el.contains(ae))
      if (lost && lastFocus?.isConnected && !lastFocus.closest('[inert]')) lastFocus.focus()
    }
    const hold = () => {
      const late: HTMLElement[] = []
      for (const el of modals()) {
        if (!el.hasAttribute('inert')) { el.setAttribute('inert', ''); held.add(el); late.push(el) }
      }
      if (late.length > 0) {
        restore(late)
        // MUI moves focus in its own effect, which can land after the observer fires.
        timers.add(setTimeout(() => restore(late), 0))
        timers.add(setTimeout(() => restore(late), 120))
      }
    }
    hold()
    const observer = new MutationObserver(hold)
    observer.observe(document.body, { childList: true })
    return () => {
      for (const t of timers) clearTimeout(t)
      timers.clear()
      document.removeEventListener('focusin', onFocusIn, true)
      observer.disconnect()
      held.forEach(el => el.removeAttribute('inert'))
    }
  }, [active])
}
