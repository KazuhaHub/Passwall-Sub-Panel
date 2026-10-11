import { Alert, Button, Skeleton, Stack, Typography } from '@mui/material'
import { useDestinationGroup } from '@/query/destinationGroups'
import { useDestinationLists } from '@/query/accessControl'
import type { QueryScope } from '@/query/session'
import { AsyncButton } from '@/components/AsyncButton'
import FieldHint from '@/components/FieldHint'
import { useAccessTranslation } from '../useAccessTranslation'

const P = 'admin:groups.destination.'
export default function GroupDestinationSummary({ scope, groupID, busy, onOpen }: { scope: QueryScope; groupID: number | null; busy: boolean; onOpen: (url: string) => void }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  if (groupID === null) return <Typography color="text.secondary">{t(`${P}create_hint`)}</Typography>
  return <Summary scope={scope} groupID={groupID} busy={busy} onOpen={onOpen} />
}
function Summary({ scope, groupID, busy, onOpen }: { scope: QueryScope; groupID: number; busy: boolean; onOpen: (url: string) => void }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const read = useDestinationGroup(scope, groupID), group = read.error ? undefined : read.data
  const lists = useDestinationLists(scope, group?.mode === 'allowlist')
  if (read.error) return <Alert severity="error" action={<AsyncButton pending={read.isFetching} onClick={() => read.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert>
  if (!group) return <Stack spacing={1} aria-busy="true">{[0, 1, 2].map(key => <Skeleton key={key} variant="rounded" height={48} />)}</Stack>
  const trialDay = group.stage_changed_at === null ? null : Math.max(1, Math.floor((Date.now() - group.stage_changed_at) / 86400000) + 1)
  const ids = [...new Set([...group.list_ids, group.extra_list_id])]
  const byID = new Map(lists.data?.items.map(list => [list.id, list]))
  const known = !lists.error && !!lists.data && ids.every(id => byID.has(id)) && byID.has(group.base_list_id)
  return <Stack spacing={1.5} sx={{ minWidth: 0 }}>
    <Typography>{t(`${P}mode`)} · {t(group.mode === 'open' ? `${P}open` : group.stage === 'trial' ? trialDay === null ? `${P}trial` : `${P}trial_day` : `${P}enforced`, { days: trialDay })}</Typography>
    {group.mode === 'open' ? <Typography variant="body2">{t(`${P}open_notice`)}</Typography> : <>
      {known ? <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{t(`${P}lists`, { names: ids.map(id => t('admin:access_control.allowlist.list_count', { name: byID.get(id)!.name, count: byID.get(id)!.entry_count })).join(' · '), count: byID.get(group.base_list_id)!.entry_count })}</Typography> : <Typography variant="body2" color="text.secondary">{t(`${P}lists_unknown`)}</Typography>}
      {lists.error && <AsyncButton pending={lists.isFetching} onClick={() => lists.refetch()}>{t('common:actions.retry')}</AsyncButton>}
      <Typography variant="body2">{t(`${P}nodes`, { ok: group.eligible_nodes, total: group.total_nodes })}</Typography>
      {group.ineligible.length > 0 && <FieldHint tone="muted" summary={t(`${P}excluded`, { count: group.ineligible.length })} detail={group.ineligible.map(node => `${node.name} · ${t(`admin:access_control.allowlist.reason.${node.reason}`)}`).join('; ')} />}
      <Typography variant="body2" color="text.secondary">{t(`${P}notice`)}</Typography>
    </>}
    <Button disabled={busy} sx={{ minHeight: 44, alignSelf: 'flex-end' }} onClick={() => onOpen(`/admin/access-control?tab=allowlist&${group.mode === 'open' ? 'al_enable' : 'al_group'}=${groupID}`)}>{t(group.mode === 'open' ? `${P}enable` : `${P}edit`)}</Button>
  </Stack>
}
