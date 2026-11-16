import { parseUserId } from '@/hooks/useDrawerParam'
import { parseNodeFilter, type NodeFilter } from '@/utils/accessControl'

// THE ACCESS-CONTROL PAGE'S URL (plan §7.1). The tab, the lists tab's filter,
// the sheet that is open and the account drawer live in the query string, so a
// copied link, a reload and Back all mean the same view. What does NOT:
// destinations and search terms (R12): they ride in history state (a sheet's
// `prefill`) or live in component state. Pure: parse and build, no router.

/** Policies, lists, the allowlist groups and records. */
export const ACCESS_TABS = ['policies', 'lists', 'allowlist', 'records'] as const
export type AccessTab = (typeof ACCESS_TABS)[number]

/** Anything else (a typo) reads as the policies tab and is not rewritten in the URL. */
export function parseAccessTab(raw: string | null): AccessTab {
  return (ACCESS_TABS as readonly string[]).includes(raw ?? '') ? raw as AccessTab : 'policies'
}

/** The page-level sheets; only one drawer is open at a time. */
export const ACCESS_SHEETS = ['test', 'nodes', 'exemptions', 'list'] as const
export type AccessSheet = (typeof ACCESS_SHEETS)[number]

export function parseAccessSheet(raw: string | null): AccessSheet | null {
  return (ACCESS_SHEETS as readonly string[]).includes(raw ?? '') ? raw as AccessSheet : null
}

/** The params that name a drawer: opening one drops the others. */
export const DRAWER_PARAMS = ['sheet', 'user'] as const

/** The list the entries sheet shows (`sheet=list&list=<id>`). */
export function parseListId(raw: string | null): number | null {
  return parseUserId(raw)
}

/** The lists tab's state filter: `problem` only (`lst_state`). */
export type ListStateFilter = 'problem'
export function parseListStateFilter(raw: string | null): ListStateFilter | null {
  return raw === 'problem' ? 'problem' : null
}

/** The lists tab's kind filter (`lst_kind`). */
export const LIST_KINDS = ['custom', 'remote', 'geosite'] as const
export type ListKindFilter = (typeof LIST_KINDS)[number]
export function parseListKindFilter(raw: string | null): ListKindFilter | null {
  return (LIST_KINDS as readonly string[]).includes(raw ?? '') ? raw as ListKindFilter : null
}

export { parseNodeFilter, type NodeFilter }

/**
 * What a tab or sheet may be handed through history STATE when another part of
 * the page asks it to do something on arrival (a used-by link in the lists tab
 * opens a policy; a test result opens one). Consumed once on mount and cleared
 * by a replace. Never in the URL.
 */
export interface AccessNavState {
  /** Open the policy editor for this policy (policies tab). */
  openPolicy?: number
  /** Scroll to this policy's row and highlight it for a moment (policies tab). */
  highlightPolicy?: number
  /** Open the list dialog: a new one of a kind, or an existing one by id (lists tab). */
  openList?: 'new_custom' | 'new_remote' | 'new_geosite' | number
}

/**
 * The allowlist tab's params: `al_group` is the group a card is highlighted
 * for (and, with `al_view=report`, the report of that group), `al_enable` opens
 * the enable dialog on a group, `al_days` is the report's range. All ids and
 * numbers; a destination or a search term is never here (R12).
 */
export interface AllowlistParams {
  group: number | null
  /** The report view of `group`; without a group there is nothing to report on. */
  report: boolean
  enable: number | null
  days: number | null
}

export function parseAllowlistParams(params: URLSearchParams): AllowlistParams {
  const group = parseUserId(params.get('al_group'))
  const days = Number(params.get('al_days'))
  return {
    group,
    report: group !== null && params.get('al_view') === 'report',
    enable: parseUserId(params.get('al_enable')),
    days: Number.isInteger(days) && days >= 1 ? days : null,
  }
}

/** The query after a change to the allowlist params; anything left out keeps its value, null removes it. */
export function allowlistSearch(current: URLSearchParams, patch: Partial<AllowlistParams>): URLSearchParams {
  const out = new URLSearchParams(current)
  const put = (key: string, value: string | null) => { if (value === null) out.delete(key); else out.set(key, value) }
  if ('group' in patch) put('al_group', patch.group == null ? null : String(patch.group))
  if ('report' in patch) put('al_view', patch.report ? 'report' : null)
  if ('enable' in patch) put('al_enable', patch.enable == null ? null : String(patch.enable))
  if ('days' in patch) put('al_days', patch.days == null ? null : String(patch.days))
  return out
}

/** Where the settings dialog is asked to put the focus. */
export type SettingsFocus = 'apply' | 'refresh' | 'rules' | 'retention' | 'trial' | 'usage'
