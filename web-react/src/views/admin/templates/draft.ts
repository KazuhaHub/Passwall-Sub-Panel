import type { Template } from '@/api/templates'

export const EMPTY: Template = { slug: '', name: '', client_type: 'mihomo', is_default: false, rule_sets: [], content: '' }

/** A copy that shares no array with the original, so editing the draft never touches the cached row. */
export const copyOf = (tpl: Template): Template => ({ ...tpl, rule_sets: tpl.rule_sets ? [...tpl.rule_sets] : [] })

export const sameTemplate = (a: Template, b: Template) => JSON.stringify(a) === JSON.stringify(b)

/** A copy of a template for the create flow: is_default is forced off so it does not fight the original for the per-client-type default. */
export function duplicateOf(tpl: Template): Template {
  return { ...tpl, slug: tpl.slug + '-copy', name: tpl.name + ' (Copy)', is_default: false, rule_sets: tpl.rule_sets ? [...tpl.rule_sets] : [] }
}
