import type { DestList } from '@/api/accessControl'
import type { AccessToneKind } from '@/utils/accessControl'

// WHAT A LIST'S STATE MEANS ON THE PAGE (plan §7.4 S6). One table, so the row,
// the filter tile and the verdict agree on which lists are "a problem".

export type ListStatusKey = 'ready' | 'refreshing' | 'failed_old' | 'failed_never' | 'pending' | 'pending_used'

export interface ListStatus {
  key: ListStatusKey
  tone: AccessToneKind
  /** Counted by the "problems" tile and by `lst_state=problem`. */
  problem: boolean
}

/** `usedByEnabled`: an enabled policy names the list. */
export function listStatus(list: Pick<DestList, 'state' | 'last_fetched_at'>, usedByEnabled: boolean): ListStatus {
  switch (list.state) {
    case 'refreshing': return { key: 'refreshing', tone: 'measuring', problem: false }
    case 'failed':
      // Never fetched: there is no old content to fall back on, so it cannot say "still using".
      return list.last_fetched_at === null
        ? { key: 'failed_never', tone: 'failing', problem: true }
        : { key: 'failed_old', tone: 'attention', problem: true }
    case 'pending':
      return usedByEnabled
        ? { key: 'pending_used', tone: 'failing', problem: true }
        : { key: 'pending', tone: 'measuring', problem: false }
    default: return { key: 'ready', tone: 'ok', problem: false }
  }
}

/** Every reason and error code the parser reports (destlist/parse.go), each with a key under lists.report.reason. */
export const PARSE_REASON_CODES = [
  'rewritten_to_network', 'trailing_field_dropped', 'localhost', 'adguard_modifier', 'adguard_exception',
  'adguard_unsupported', 'not_applicable_on_node', 'unrecognized', 'invalid_domain', 'invalid_cidr', 'invalid_country',
  'invalid_geosite', 'broad_regexp', 'broad_keyword', 'broad_suffix', 'broad_cidr', 'regexp_invalid', 'regexp_too_long',
  'source_line_in_dest_list', 'dest_line_in_source_list',
] as const
export const PARSE_WARNING_CODES = ['regexp_no_anchor', 'regexp_long'] as const

const R = 'admin:access_control.lists.report.'
type T = (key: string, opts?: Record<string, unknown>) => string

/** A reason code as words; a code this build does not know reads as the generic sentence, never raw. */
export function reasonText(t: T, code: string): string {
  return t((PARSE_REASON_CODES as readonly string[]).includes(code) ? `${R}reason.${code}` : `${R}reason.unknown`)
}

export function warningText(t: T, code: string): string {
  return t((PARSE_WARNING_CODES as readonly string[]).includes(code) ? `${R}warn.${code}` : `${R}warn.unknown`)
}
