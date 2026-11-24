import type { TFunction } from 'i18next'
import { SEEDED_RULESET_SLUGS, type RuleSet } from '@/api/rules'
import { confirm } from '@/components/ConfirmHost'

/** Seeded rule sets can be restored from the build and cannot be deleted. */
export const isSeeded = (rs: Pick<RuleSet, 'slug'>) => SEEDED_RULESET_SLUGS.includes(rs.slug)

/** Both the list's row menu and a rule set's own page ask exactly this, with focus on Cancel. */
export function askDeleteRuleSet(t: TFunction, slug: string, usedBy: readonly string[]) {
  const usage = usedBy.length > 0 ? t('admin:rules.confirm.delete_used_suffix', { names: usedBy.join('、') }) : ''
  return confirm({
    title: t('admin:rules.confirm.delete_title'),
    message: t('admin:rules.confirm.delete_message', { slug, usage }),
    confirmText: t('common:actions.delete'),
    destructive: true,
    initialFocus: 'cancel',
  })
}

export function askResetRuleSet(t: TFunction, slug: string) {
  return confirm({
    title: t('admin:rules.confirm.reset_title'),
    message: t('admin:rules.confirm.reset_message', { slug }),
    confirmText: t('admin:rules.reset_to_default'),
    destructive: true,
    initialFocus: 'cancel',
  })
}
