/**
 * The Rules tab's layout rules, as functions of the width of the tab's own box (the page after the sidebar and its padding),
 * so that they can be tested without a layout engine. The window is the wrong thing to ask: with the sidebar open a 1280px
 * window leaves 976px, with it closed 1232px, and the tab has to behave the same for the same room.
 */

/** The groups column and the open group keep their room next to a docked routing test from this width (17rem + 40rem + 22rem + gaps). */
export const DOCK_MIN = 1360
/** From this width the toolbar says the counts itself; under it the directory's own line does. */
export const FULL_MIN = 960
/** Under this width the issues button and the target filter live in the More menu. */
export const COMPACT_MIN = 760

export type TesterPlacement = 'closed' | 'screen' | 'dock' | 'overlay'
export type ToolbarTier = 'full' | 'compact' | 'narrow'

/**
 * Where the routing test is drawn: not at all, a screen of its own (phone), a third column (very wide), or a panel over the
 * right side of the page (everything between). It is never squeezed in as a third column: three columns at 1280px left the
 * rules table about 225px.
 */
export function testerPlacement({ open, width, phone, subRule }: { open: boolean; width: number | null; phone: boolean; subRule: boolean }): TesterPlacement {
  if (!open || subRule) return 'closed'
  if (phone) return 'screen'
  return width !== null && width >= DOCK_MIN ? 'dock' : 'overlay'
}

/** What the toolbar shows at a width. An unknown width (not measured yet) is the full toolbar, so nothing flickers on the first render. */
export function toolbarTier(width: number | null): ToolbarTier {
  if (width === null || width >= FULL_MIN) return 'full'
  return width >= COMPACT_MIN ? 'compact' : 'narrow'
}
