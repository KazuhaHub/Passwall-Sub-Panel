/**
 * The addresses of the Templates section. A template is a page (`/admin/templates/<slug>`), the create form another
 * (`/admin/templates/new`, with `?copy=<slug>` for a duplicate), and the list keeps its own place in its query string.
 *
 * A template whose slug is `new` would collide with the create address, so the form refuses that slug
 * (`RESERVED_SLUG`); the backend itself accepts it, which is why a template made elsewhere under that name is not reachable here.
 */
export const TEMPLATES_PATH = '/admin/templates'
export const NEW_TEMPLATE_PATH = `${TEMPLATES_PATH}/new`
export const RESERVED_SLUG = 'new'

export function templatePath(slug: string): string {
  return `${TEMPLATES_PATH}/${encodeURIComponent(slug)}`
}

/** The create page, optionally seeded as a copy of `copy`. */
export function newTemplatePath(copy?: string): string {
  return copy ? `${NEW_TEMPLATE_PATH}?copy=${encodeURIComponent(copy)}` : NEW_TEMPLATE_PATH
}

/**
 * Where an old `?edit=<slug>` / `?edit=new[&copy=<slug>]` link on the list now goes. The side sheet those links opened is
 * gone, but bookmarks still carry them, so the list sends them on (replacing, so Back does not return to the redirect).
 * `null` for anything that is not such a link.
 */
export function legacyTemplateTarget(search: string): string | null {
  const params = new URLSearchParams(search)
  const edit = params.get('edit')
  if (!edit) return null
  if (edit === 'new') return newTemplatePath(params.get('copy') || undefined)
  return templatePath(edit)
}
