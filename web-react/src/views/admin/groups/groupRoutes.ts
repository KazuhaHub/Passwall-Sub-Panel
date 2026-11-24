/**
 * The addresses of the Groups section. A group is a page (`/admin/groups/5`, its tab in `?tab=`), the create form
 * another (`/admin/groups/new`), and the list keeps its own place in its query string.
 */
export const GROUPS_PATH = '/admin/groups'
export const NEW_GROUP_PATH = `${GROUPS_PATH}/new`

export const GROUP_TABS = ['general', 'nodes', 'layout', 'limits', 'overrides', 'destination'] as const
export type GroupTab = (typeof GROUP_TABS)[number]

const tabQuery = (tab?: GroupTab) => (tab && tab !== GROUP_TABS[0] ? `?tab=${tab}` : '')

/** A group's page, on a tab; the first tab leaves the address clean. */
export function groupPath(id: number, tab?: GroupTab): string {
  return `${GROUPS_PATH}/${id}${tabQuery(tab)}`
}

/** The id an address names, or null for anything that is not a positive whole number (so no request is ever made for it). */
export function parseGroupId(raw: string | undefined): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const n = Number(raw)
  return Number.isSafeInteger(n) && n > 0 ? n : null
}

/**
 * Where an old `?edit=<id>` / `?edit=new` (with `&dtab=<tab>`) link on the list now goes. The side sheet those links
 * opened is gone, but bookmarks and the access-control page still carried them, so the list sends them on (replacing,
 * so Back does not return to the redirect). `null` for anything that is not such a link, including a malformed id.
 */
export function legacyGroupTarget(search: string): string | null {
  const params = new URLSearchParams(search)
  const raw = params.get('edit')
  const dtab = params.get('dtab') ?? ''
  const tab = (GROUP_TABS as readonly string[]).includes(dtab) ? (dtab as GroupTab) : undefined
  if (raw === 'new') return `${NEW_GROUP_PATH}${tabQuery(tab)}`
  const id = parseGroupId(raw ?? undefined)
  return id === null ? null : groupPath(id, tab)
}
