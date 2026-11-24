import { useMemo } from 'react'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { choiceCodec } from '../logs/shared'
import { certHealth, sortCerts, type CertHealthId } from './model'
import type { CertData } from './useCertData'

export const HEALTH: readonly CertHealthId[] = ['failed', 'expired', 'expiring', 'issuing', 'valid']

/**
 * The certificates the list shows, in the order it shows them: attention first, narrowed by the keyword and
 * the state in the address. The list draws them and the open certificate's previous / next buttons walk them,
 * so both ask here and cannot disagree about the order.
 */
export function useCertRows(data: CertData) {
  const [f, setF] = useUrlStates({ q: urlCodecs.string(), status: choiceCodec(HEALTH) })
  // The words ("expires in 12 days") are as of the last read, not of every render.
  const now = useMemo(() => Date.now(), [data.certs.dataUpdatedAt]) // eslint-disable-line react-hooks/exhaustive-deps
  const { renewBeforeDays } = data
  const all = data.certs.data
  const sorted = useMemo(() => (all ? sortCerts(all, now, renewBeforeDays) : []), [all, now, renewBeforeDays])
  const rows = useMemo(() => {
    const q = f.q.trim().toLowerCase()
    return sorted.filter(c => {
      if (f.status && certHealth(c, now, renewBeforeDays).id !== f.status) return false
      return !q || c.name.toLowerCase().includes(q) || c.domains.some(d => d.toLowerCase().includes(q))
    })
  }, [sorted, f.q, f.status, now, renewBeforeDays])
  const counts = useMemo(() => {
    const out: Partial<Record<CertHealthId, number>> = {}
    for (const c of sorted) { const id = certHealth(c, now, renewBeforeDays).id; out[id] = (out[id] ?? 0) + 1 }
    return out
  }, [sorted, now, renewBeforeDays])
  return { f, setF, now, rows, counts }
}
