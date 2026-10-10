import { diffChars } from 'diff'

export const MAX_LEGAL_BYTES = 60_000
export const legalBytes = (text: string) => new TextEncoder().encode(text).length

export interface LegalChanges { added: number; removed: number }

// Async Myers diff counts Unicode code points, including non-BMP characters.
// A radically different full-size document must not freeze the editor.
export function countLegalChanges(previous: string, next: string): Promise<LegalChanges | null> {
  return new Promise(resolve => {
    diffChars(previous, next, { timeout: 2000, callback: changes => {
      if (!changes) { resolve(null); return }
      resolve(changes.reduce((counts, change) => ({
        added: counts.added + (change.added ? change.count : 0),
        removed: counts.removed + (change.removed ? change.count : 0),
      }), { added: 0, removed: 0 }))
    } })
  })
}
