/**
 * The addresses of the Rule sets section. A rule set is a page (`/admin/rules/<slug>`), the create form another
 * (`/admin/rules/new`, `?copy=<slug>` for a duplicate), and the list keeps its own place in its query string.
 *
 * A rule set whose slug is `new` would collide with the create address (the router compares paths without regard to
 * case, so `NEW` as well), so the form refuses that slug; the backend itself accepts it.
 */
export const RULESETS_PATH = '/admin/rules'
export const NEW_RULESET_PATH = `${RULESETS_PATH}/new`

export const isReservedSlug = (slug: string): boolean => slug.toLowerCase() === 'new'

export function ruleSetPath(slug: string): string {
  return `${RULESETS_PATH}/${encodeURIComponent(slug)}`
}

export function newRuleSetPath(copy?: string): string {
  return copy ? `${NEW_RULESET_PATH}?copy=${encodeURIComponent(copy)}` : NEW_RULESET_PATH
}

/**
 * Where an old `?edit=<slug>` / `?edit=new[&copy=<slug>]` link on the list now goes: the side sheet those links opened is
 * gone, but bookmarks still carry them. `null` for anything that is not such a link.
 */
export function legacyRuleSetTarget(search: string): string | null {
  const params = new URLSearchParams(search)
  const edit = params.get('edit')
  if (!edit) return null
  if (edit === 'new') return newRuleSetPath(params.get('copy') || undefined)
  return ruleSetPath(edit)
}
