import { Alert, Button, Skeleton, Stack, Typography } from '@mui/material'
import { AsyncButton } from '@/components/AsyncButton'
import { useDestinationGroups } from '@/query/destinationGroups'
import { useDestinationLists } from '@/query/accessControl'
import type { QueryScope } from '@/query/session'
import { useAccessTranslation } from '../useAccessTranslation'
import AllowlistGroupCard, { type AllowlistCardActions } from './AllowlistGroupCard'

const P = 'admin:access_control.allowlist.'
export default function AllowlistTab({ scope, active, onEnable, ...actions }: AllowlistCardActions & { scope: QueryScope; active: boolean; onEnable: () => void }) {
  const { t } = useAccessTranslation(['admin', 'common'])
  const groups = useDestinationGroups(scope, active)
  const lists = useDestinationLists(scope, active)
  if (!active) return null
  if (groups.error) return <Alert severity="error" action={<AsyncButton pending={groups.isFetching} onClick={() => groups.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}read_failed`)}</Alert>
  if (groups.isPending || !groups.data) return <Stack spacing={2}>{[0, 1].map(key => <Skeleton key={key} role="progressbar" aria-label={t(`${P}loading`)} aria-busy="true" variant="rounded" height={224} />)}</Stack>
  const enabled = groups.data.items.filter(group => group.mode === 'allowlist')
  return <Stack spacing={2} sx={{ minWidth: 0 }}>
    <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: { xs: 'stretch', sm: 'center' }, justifyContent: 'space-between' }}>
      <Typography variant="body2" color="text.secondary">{t(`${P}intro`)}</Typography>
      <Button variant="outlined" sx={{ minHeight: 44, flexShrink: 0 }} onClick={onEnable}>{t(`${P}enable`)}</Button>
    </Stack>
    {lists.error && <Alert severity="warning" action={<AsyncButton pending={lists.isFetching} onClick={() => lists.refetch()}>{t('common:actions.retry')}</AsyncButton>}>{t(`${P}lists_failed`)}</Alert>}
    {enabled.length === 0 ? <Stack spacing={1} sx={{ alignItems: 'center', textAlign: 'center', py: 6 }}>
      <Typography component="h3" variant="h6">{t(`${P}empty_title`)}</Typography>
      <Typography color="text.secondary">{t(`${P}empty_detail`)}</Typography>
    </Stack> : enabled.map(group => <AllowlistGroupCard key={group.group_id} group={group} lists={lists.error ? undefined : lists.data?.items} {...actions} />)}
  </Stack>
}
