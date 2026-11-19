import type { ComponentType } from 'react'
import type { TFunction } from 'i18next'

import type { Role } from '@/api/types'
import type { NavSection } from './nav'
import { visibleItems } from './nav'

// THE COMMAND PALETTE'S EXTENSION POINT. The palette itself (CommandPalette.tsx)
// knows nothing about users, nodes or actions: it takes a list of providers, asks
// each for results as the query changes, and draws them as groups. A later package
// that wants "find a user" or "run an action" writes a provider in its own file and
// passes it to the shell; this file and the palette stay as they are.

export interface CommandContext {
  /** Opens a route through the router (the app's basename is applied). */
  navigate: (to: string) => void
  /** Closes the palette; an entry's `run` need not call it, the palette does. */
  close: () => void
  role: Role | ''
}

export interface CommandEntry {
  /** Unique within its provider. */
  id: string
  label: string
  /** A second line: what it is, where it lives. */
  description?: string
  icon?: ComponentType<{ className?: string }>
  /** Extra words that should match the query without being shown. */
  keywords?: string[]
  /** A route to open. Either this or `run`. */
  to?: string
  run?: (ctx: CommandContext) => void
}

export interface CommandProvider {
  /** Unique across providers. */
  id: string
  /** The group heading, already translated. */
  heading: string
  /**
   * Results for the query. Called when the palette opens and as the query changes,
   * so it does its own filtering (a server search has already filtered; a local list
   * can use `matchesQuery`). May be asynchronous; `signal` aborts when the query
   * moves on, and a stale answer is dropped either way.
   */
  search: (query: string, ctx: CommandContext, signal: AbortSignal) => CommandEntry[] | Promise<CommandEntry[]>
  /** Wait this long after the last keystroke before searching. Default 0 (local). */
  debounceMs?: number
  /** Do not search below this many characters; the provider shows nothing until then. Default 0. */
  minQueryLength?: number
  /** Show at most this many entries. Default 8. */
  limit?: number
  /** Lower sorts first. Default 0. */
  order?: number
}

/** Case-insensitive match of every word of the query against any of the fields. */
export function matchesQuery(query: string, ...fields: (string | undefined)[]): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return true
  const haystack = fields.filter(Boolean).join(' ').toLowerCase()
  return words.every(w => haystack.includes(w))
}

/**
 * The one provider the shell ships: every page the current role may open, the same
 * model and the same filter as the sidebar, so ⌘K never offers a page the sidebar
 * hides.
 */
export function navigationProvider(nav: readonly NavSection[], role: Role | '', t: TFunction, heading: string): CommandProvider {
  const entries: CommandEntry[] = visibleItems(nav, role).map(item => ({
    id: item.to,
    label: String(t(item.labelKey)),
    icon: item.icon,
    to: item.to,
    // The route is searchable too (`/admin/sync-tasks` finds "同步任务" for someone who
    // remembers the URL), but not shown.
    keywords: [item.to],
  }))
  return {
    id: 'navigation',
    heading,
    order: -100,
    limit: entries.length,
    search: query => entries.filter(e => matchesQuery(query, e.label, ...(e.keywords ?? []))),
  }
}
