import { createContext, useContext } from 'react'

/**
 * Which places (sub-tabs, see tree.ts) hold unsaved changes, and the reporter of each. A place is the unit of a draft:
 * it has its own form and its own Save, and stays built while another is shown, so this is what the page asks when it
 * is left, and what a save that reloads the page asks to say what the reload drops.
 */
export interface SettingsDirty {
  dirty: readonly string[]
  report: Readonly<Record<string, (dirty: boolean) => void>>
}

export const SettingsDirtyContext = createContext<SettingsDirty>({ dirty: [], report: {} })

/** The places other than `self` that have unsaved changes. */
export function useOtherDirtyPlaces(self: string): string[] {
  return useContext(SettingsDirtyContext).dirty.filter(p => p !== self)
}
