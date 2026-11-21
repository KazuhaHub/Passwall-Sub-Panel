import { useTranslation } from 'react-i18next'

import type { DataCollection, DataCollectionItem } from '@/api/legal'

// The order is the page's: what every account has first, the access-control
// records after. A key the server sends that is not listed has no sentence to
// say it in, so it is not drawn.
const ORDER = [
  'sub_log', 'auth_event', 'connection_history', 'hwid', 'flag_records', 'risk_signals', 'risk_reviews',
  'dest_hits', 'dest_trial', 'dest_usage',
] as const

/**
 * The "what we collect" list, generated from the settings the panel runs with,
 * so the page cannot drift from the retention it states. Every number comes from
 * the data (a retention of 0 is "kept for ever"); no text here states one.
 */
export default function DataCollectionCard({ data }: { data: DataCollection }) {
  const { t } = useTranslation('auth')

  const retention = (days: number) => days === 0 ? t('legal.retention_forever') : t('legal.retention_days', { days })

  function value(key: string, item: DataCollectionItem): string {
    if (item.enabled_groups !== undefined) return t('legal.groups_enabled', { groups: item.enabled_groups })
    if (key === 'risk_signals') return t('legal.risk_signals_value')
    if (key === 'risk_reviews') return t('legal.risk_reviews_value')
    const parts: string[] = []
    if (item.nodes !== undefined) parts.push(t('legal.nodes', { count: item.nodes }))
    if (item.retention_days !== undefined) parts.push(retention(item.retention_days))
    return parts.length ? parts.join(' · ') : t('legal.recorded')
  }

  const rows = ORDER.filter(k => data[k]?.enabled)

  return (
    <section aria-label={t('legal.collect_title')} className="my-5 rounded-xl border bg-muted/50 p-4 sm:p-5">
      <h2 className="mb-2 text-base font-semibold">{t('legal.collect_title')}</h2>
      <ul className="m-0 list-none p-0">
        {rows.map(key => (
          <li key={key} className="flex flex-col justify-between gap-0 border-t py-1.5 first:border-t-0 sm:flex-row sm:gap-4">
            <span className="text-sm">{t(`legal.item.${key}`)}</span>
            <span className="shrink-0 text-sm text-muted-foreground">{value(key, data[key])}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}
