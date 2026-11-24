import { createContext } from 'react'

/**
 * Where a SaveBar inside a DetailDialog goes: that dialog's footer element, once it is mounted. The bar keeps its place
 * in the form's React tree (so FormValidityProvider and its state are untouched) and is drawn into the footer through a
 * portal, fixed below the scrolling body instead of floating over the fields. Null outside a DetailDialog.
 *
 * React context passes through portals, so a plain Dialog or Sheet opened from inside a DetailDialog would inherit the
 * outer footer: DialogContent and SheetContent therefore provide null for their own subtree.
 */
export const DialogFooterSlot = createContext<HTMLElement | null>(null)
