/**
 * The addresses of the Users section. A user is a page (`/admin/users/7`), the create form another
 * (`/admin/users/new`), and the list keeps its own place in its query string.
 */
export const USERS_PATH = '/admin/users'
export const NEW_USER_PATH = `${USERS_PATH}/new`

export const USER_TABS = ['overview', 'settings', 'usage', 'activity', 'security'] as const
export type UserTab = (typeof USER_TABS)[number]

/** A user's page, on a tab; the first tab leaves the address clean. */
export function userPath(id: number, tab?: UserTab): string {
  return tab && tab !== USER_TABS[0] ? `${USERS_PATH}/${id}?tab=${tab}` : `${USERS_PATH}/${id}`
}

/**
 * Where an old `?user=<id>` / `?user=new` link on the list now goes. The sheet those links opened is gone, but
 * bookmarks and other pages still carry them, so the list sends them on (replacing, so Back does not return to the
 * redirect). `null` for anything that is not such a link, including a malformed id, which reads as no link at all.
 */
export function legacyUserTarget(search: string): string | null {
  const params = new URLSearchParams(search)
  const raw = params.get('user')
  if (raw === 'new') return NEW_USER_PATH
  if (!raw || !/^\d+$/.test(raw)) return null
  const id = Number(raw)
  if (!Number.isSafeInteger(id) || id <= 0) return null
  const tab = params.get('tab')
  return userPath(id, (USER_TABS as readonly string[]).includes(tab ?? '') ? (tab as UserTab) : undefined)
}
