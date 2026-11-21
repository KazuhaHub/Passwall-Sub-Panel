import { useCallback } from 'react'
import { useLocation } from 'react-router'
import { useUrlWriter } from '@/hooks/urlBatch'
import { parseUserId } from '../risk/riskParams'

/**
 * The EXACT user filter the risk center's drawer links with: `user_id` filters (the subscription and the
 * sign-in log both take it), `upn` only names the chip. The keyword box would not do: it is a fuzzy match
 * over user, IP, UA and client, and would also list every account whose name contains the one asked for.
 * The URL owns it, so Back returns to the filtered list; a malformed id filters nothing.
 */
export interface UserFilter {
  id: number | null
  upn: string
  /** Drops the filter from the URL in place (it is the same list, unfiltered) and starts from page 1. */
  clear: () => void
}

export function useUserFilter(): UserFilter {
  const params = new URLSearchParams(useLocation().search)
  const write = useUrlWriter()
  const id = parseUserId(params.get('user_id'))
  const upn = params.get('upn') ?? ''
  // Through the shared writer, so clearing it and resetting the keyword in one click is one navigation.
  const clear = useCallback(() => write(p => { p.delete('user_id'); p.delete('upn'); p.delete('page') }, 'replace'), [write])
  return { id, upn, clear }
}
