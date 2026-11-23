import { createContext, useContext } from 'react'
import type { SectionKey } from './model'

/** Which sections hold unsaved changes, so a save that reloads the page can say what the reload drops. */
export const DirtySectionsContext = createContext<readonly SectionKey[]>([])

/** The sections other than `self` that have unsaved changes. */
export function useOtherDirtySections(self: SectionKey): SectionKey[] {
  return useContext(DirtySectionsContext).filter(s => s !== self)
}
