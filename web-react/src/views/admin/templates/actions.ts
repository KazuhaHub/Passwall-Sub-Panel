import type { TFunction } from 'i18next'
import { SEEDED_TEMPLATE_SLUGS, type Template } from '@/api/templates'
import { confirm } from '@/components/ConfirmHost'

/** Seeded templates can be restored from the build; they and the default one cannot be deleted. */
export const isSeeded = (tpl: Pick<Template, 'slug'>) => SEEDED_TEMPLATE_SLUGS.includes(tpl.slug)
export const isProtected = (tpl: Pick<Template, 'slug' | 'is_default'>) => tpl.is_default || isSeeded(tpl)

/** Both the list's row menu and a template's own page ask exactly this, with focus on Cancel. */
export function askDeleteTemplate(t: TFunction, slug: string) {
  return confirm({
    title: t('admin:templates.confirm.delete_title'),
    message: t('admin:templates.confirm.delete_message', { slug }),
    confirmText: t('common:actions.delete'),
    destructive: true,
    initialFocus: 'cancel',
  })
}

export function askResetTemplate(t: TFunction, slug: string) {
  return confirm({
    title: t('admin:templates.confirm.reset_title'),
    message: t('admin:templates.confirm.reset_message', { slug }),
    confirmText: t('admin:templates.reset_to_default'),
    destructive: true,
    initialFocus: 'cancel',
  })
}
