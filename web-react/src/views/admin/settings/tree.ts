import { SECTIONS, SECTION_LABEL, SUB_KEYS, type SectionKey } from './model'

/**
 * WHERE EVERY SETTING LIVES: the page is eight sections, each split into sub-tabs that are each about one screen of
 * related settings. This file is the one place that says so. A sub-tab is:
 *  - `keys`: the settings it shows, by the key the search index knows them under (the record key for the settings
 *    record, a plain name for mail, `saml.` / `oidc.` for SSO). A setting stands in exactly one sub-tab; a test walks
 *    this tree against the search index to keep it so.
 *  - `scope`: what a group can override HERE. Per-group overrides are listed in the sub-tab that holds the matching
 *    global setting, never twice, so an audience choice means the same thing on every tab: a tab with `scope` shows
 *    the group's overrides (`mixed` when it also holds settings that cannot be overridden, which it says), a tab
 *    without it is global-only and says so.
 * A sub-tab's URL is `?tab=<section>&sub=<id>`; the first one of a section is the default and leaves `sub` out.
 */
export interface SubDef {
  id: string
  /** The i18n key of its name. */
  label: string
  keys: readonly string[]
  /** SSO's keys are `saml.x` / `oidc.x`: a prefix instead of a list. */
  prefix?: string
  scope?: { categories: readonly string[]; keys?: readonly string[]; mixed?: boolean }
}

const L = (section: string, id: string) => `settings.sub.${section}.${id}`

export const TREE = {
  general: [
    { id: 'runtime', label: L('general', 'runtime'), keys: SUB_KEYS.general.runtime },
    { id: 'retention', label: L('general', 'retention'), keys: SUB_KEYS.general.retention },
    { id: 'reminders', label: L('general', 'reminders'), keys: SUB_KEYS.general.reminders, scope: { categories: ['notify', 'emergency'] } },
    { id: 'network', label: L('general', 'network'), keys: SUB_KEYS.general.network },
    // The link-probe card has its own record and save; none of its fields are in the settings record or the search index.
    { id: 'probe', label: L('general', 'probe'), keys: [] },
  ],
  security: [
    { id: 'signin', label: L('security', 'signin'), keys: SUB_KEYS.security.signin, scope: { categories: ['login'], mixed: true } },
    { id: 'account', label: L('security', 'account'), keys: SUB_KEYS.security.account },
    { id: 'twofa', label: L('security', 'twofa'), keys: SUB_KEYS.security.twofa, scope: { categories: ['2fa'], mixed: true } },
    { id: 'protection', label: L('security', 'protection'), keys: SUB_KEYS.security.protection },
  ],
  brand: [
    { id: 'identity', label: L('brand', 'identity'), keys: SUB_KEYS.brand.identity },
    { id: 'addresses', label: L('brand', 'addresses'), keys: SUB_KEYS.brand.addresses },
  ],
  subscription: [
    { id: 'link', label: L('subscription', 'link'), keys: SUB_KEYS.subscription.link },
    {
      id: 'content', label: L('subscription', 'content'), keys: SUB_KEYS.subscription.content,
      scope: { categories: ['sub'], keys: ['sub.sub_update_interval_hours', 'sub.sub_profile_name_template', 'sub.sub_region_flag_prefix'] },
    },
    {
      id: 'protection', label: L('subscription', 'protection'), keys: SUB_KEYS.subscription.protection,
      scope: { categories: ['sub'], keys: ['sub.sub_block_auto_disable', 'sub.sub_block_auto_disable_count', 'sub.sub_block_notify_user', 'sub.sub_block_notify_max_per_day'] },
    },
  ],
  portal: [
    { id: 'links', label: L('portal', 'links'), keys: SUB_KEYS.portal.links },
    { id: 'announcement', label: L('portal', 'announcement'), keys: SUB_KEYS.portal.announcement },
  ],
  mail: [
    { id: 'smtp', label: L('mail', 'smtp'), keys: ['enabled', 'smtp_host', 'smtp_port', 'encryption', 'smtp_username', 'smtp_password', 'from_email', 'from_name'] },
    { id: 'templates', label: L('mail', 'templates'), keys: ['templates'] },
  ],
  sso: [
    { id: 'saml', label: 'settings.sso.tab_saml', keys: [], prefix: 'saml.' },
    { id: 'oidc', label: 'settings.sso.tab_oidc', keys: [], prefix: 'oidc.' },
  ],
  // One screen: the section has no sub-tab bar.
  legal: [
    { id: 'main', label: SECTION_LABEL.legal, keys: SUB_KEYS.legal.main },
  ],
} as const satisfies Record<SectionKey, readonly SubDef[]>

export type SubId<S extends SectionKey> = (typeof TREE)[S][number]['id']

export const subsOf = (s: SectionKey): readonly SubDef[] => TREE[s]
export const firstSub = (s: SectionKey): string => TREE[s][0].id

/** A place is one sub-tab, the unit of "unsaved changes": `<section>/<sub>`. */
export const placeId = (s: SectionKey, sub: string) => `${s}/${sub}`
export const PLACES: readonly string[] = SECTIONS.flatMap(s => TREE[s].map(d => placeId(s, d.id)))
export const sectionOfPlace = (place: string): SectionKey => place.split('/')[0] as SectionKey

/** The sub-tab of a section that shows the setting with this search key. */
export function subOf(section: SectionKey, key: string): string {
  const hit = (subsOf(section) as readonly SubDef[]).find(d => d.keys.includes(key) || (d.prefix !== undefined && key.startsWith(d.prefix)))
  return hit ? hit.id : firstSub(section)
}

/** The words for a place: "Section · Sub-tab", or just the section's when it has one screen. */
export function placeName(place: string, t: (key: string) => string): string {
  const section = sectionOfPlace(place)
  const sub = place.slice(section.length + 1)
  const def = subsOf(section).find(d => d.id === sub)
  return subsOf(section).length > 1 && def ? `${t(SECTION_LABEL[section])} · ${t(def.label)}` : t(SECTION_LABEL[section])
}
