import { useMemo } from 'react'
import type { DetailNav } from './DetailDialog'

/**
 * Previous / next for a record dialog, over the keys of the rows the list currently shows (loaded rows, in
 * their current order, after filtering and sorting). `select` is the list's own selection setter, so stepping
 * writes the URL exactly as a row click does. Returns undefined when the open record is not among the rows
 * (deleted, filtered away, not loaded), so the dialog shows no buttons rather than dead ones.
 */
export function useRecordNav<K extends string | number>(keys: readonly K[], current: K | null, select: (key: K) => void): DetailNav | undefined {
  return useMemo(() => {
    if (current === null) return undefined
    const i = keys.indexOf(current)
    if (i < 0) return undefined
    return {
      onPrev: i > 0 ? () => select(keys[i - 1]) : undefined,
      onNext: i < keys.length - 1 ? () => select(keys[i + 1]) : undefined,
      position: { current: i + 1, total: keys.length },
    }
  }, [keys, current, select])
}
