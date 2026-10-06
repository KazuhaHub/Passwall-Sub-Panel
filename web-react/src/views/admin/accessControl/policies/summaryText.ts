import type { TFunction } from 'i18next'
import type { DestinationPolicyInput } from '@/api/accessControl'
import { matchSummary } from './policyDraft'
export function summaryText(t: TFunction, input: DestinationPolicyInput, names: Map<number, string>): string {
  const match = matchSummary(input)
  const P = 'admin:access_control.editor.'
  const destinations = [...match.list_ids.map(id => t(`${P}list_named`, { name: names.get(id) ?? `#${id}` })),
    ...(input.new_list ? [t('admin:access_control.templates.category_match', { name: input.new_list.geosite_category })] : []),
    ...(match.cidrs ? [t(`${P}cidr_count`, { count: match.cidrs })] : []),
    ...(match.private ? [t(`${P}private`)] : []), ...(match.bt ? [t(`${P}bt`)] : [])]
  const main = destinations.length > 1 ? `(${destinations.join(t(`${P}or`))})` : destinations[0]
  return [main, match.ports ? t(`${P}ports_value`, { ports: match.ports }) : undefined, match.network.toUpperCase() || undefined]
    .filter(Boolean).join(t(`${P}and`))
}
