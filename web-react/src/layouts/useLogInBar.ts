import { useMediaQuery } from '@/hooks/useMediaQuery'

// WHERE THE OLD APP BARS HAVE ROOM FOR THE LOG BUTTON. Measured in Chrome in both languages: the
// admin bar (menu, title, log, bell, language, appearance, avatar) fits without overlap or
// clipping from 420px (404px of controls plus the bar's 16px gutter), the user bar from 390px.
// 480px is one breakpoint for both, with 60px to spare on the tighter one. It is decided in
// JavaScript so the layout renders the bar button OR the account-menu item, never both and never
// a hidden one. (MUI's sm is 600px and Tailwind's is 640px; using either left a band with no
// entry at all, or a hidden menu item that MUI still tried to focus.)
export const LOG_IN_BAR_MIN_WIDTH = 480

export function useLogInBar(): boolean {
  return useMediaQuery(`(min-width: ${LOG_IN_BAR_MIN_WIDTH}px)`)
}
