// WHERE FOCUS GOES WHEN THE ELEMENT IT SHOULD RETURN TO IS GONE. The toast log panel is modal, so
// focus is inside it until it leaves; whatever had focus before it opened may be gone by then (the
// bar button was unmounted by a resize below the bar breakpoint, or a sign-out replaced the whole
// page). A removed focused element drops focus on <body> at the top of the page, which for a
// keyboard or screen-reader user is losing their place. The two stable landmarks are the account
// menu button (it exists on every screen width, in the old bars and in the new shell) and the main
// landmark.

/** Marks the account-menu button of whichever bar is on screen; the old MUI bars and the shell both carry it. */
export const ACCOUNT_MENU_ATTR = 'data-psp-account-menu'

/** The element to focus when the proper return target is gone, or null when the page has neither. */
export function focusFallbackTarget(): HTMLElement | null {
  const account = document.querySelector<HTMLElement>(`[${ACCOUNT_MENU_ATTR}]`)
  if (account?.isConnected) return account
  const main = document.querySelector<HTMLElement>('#main-content, main')
  if (!main) return null
  // The old layouts' <main> has no tabindex, and an element without one cannot take focus.
  if (!main.hasAttribute('tabindex')) main.tabIndex = -1
  return main
}

/**
 * Focus is "lost" when nothing outside the closing panel holds it: it sits on <body> (the focused
 * element already left the DOM), or it is still on an element inside the panel, which the library
 * removes right after reporting the close complete.
 */
export function focusIsLost(): boolean {
  const el = document.activeElement
  return el === null || el === document.body || el.closest('[data-slot="toast-log"]') !== null
}
