import { Alert, Autocomplete, CircularProgress, Stack, TextField, Typography } from '@mui/material'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import { useQueryScope } from '@/query/useQueryScope'
import { useDestinationCategories, useRefreshDestinationCategories } from '@/query/accessControl'
import { destinationError } from '../errors'
import { AsyncButton } from '@/components/AsyncButton'
import GeositeDownloadNotice from './GeositeDownloadNotice'
import { categoryRefreshState } from '@/utils/destinationCategories'
const P = 'admin:access_control.categories.'
export default function GeositeCategoryPicker({ category, attrs, disabled, onChange }: { category: string; attrs: string; disabled: boolean; onChange: (category: string, attrs: string) => void }) {
  const { t, dateTime } = useAccessTranslation(['admin', 'common']), scope = useQueryScope()
  const query = useDestinationCategories(scope, true), refresh = useRefreshDestinationCategories(scope)
  const state = categoryRefreshState(query.data, query.error), pending = refresh.isPending || state.refreshing
  const selected = query.data?.categories.find(item => item.name === category)
  const download = async () => { try { await refresh.mutateAsync() } catch { /* The mutation error is shown inline. */ } }
  if (!query.data && destinationError(query.error).status === 503) return <GeositeDownloadNotice pending={pending} disabled={disabled} failed={!pending && (!!refresh.error || state.failed)} onDownload={download} />
  if (!query.data) return <Stack spacing={1}>
    {query.isPending ? <CircularProgress size={20} /> : <Alert severity={destinationError(query.error).status === 503 ? 'info' : 'error'}>{t(`${P}${destinationError(query.error).status === 503 ? 'missing' : 'failed'}`)}</Alert>}
    {!query.isPending && <AsyncButton disabled={disabled} pending={pending} onClick={download} sx={{ minHeight: 44 }}>{t(`${P}download`)}</AsyncButton>}
    {refresh.error && <Alert severity="error">{destinationError(refresh.error).error}</Alert>}
  </Stack>
  return <Stack spacing={2} sx={{ '& .MuiAutocomplete-popupIndicator, & .MuiAutocomplete-clearIndicator': { minWidth: 44, minHeight: 44 }, '& .MuiAutocomplete-endAdornment': { top: '50%', transform: 'translateY(-50%)' }, '& .MuiAutocomplete-inputRoot': { minHeight: 56, pr: '90px !important' } }}>
    {state.failed && <Alert severity="warning">{t(`${P}failed`)}</Alert>}
    <Autocomplete options={query.data.categories.map(item => item.name)} value={category || null} disabled={disabled}
      getOptionLabel={name => { const item = query.data!.categories.find(item => item.name === name); return item ? t(`${P}option`, { name, count: item.count, regexps: item.regexp_count }) : name }}
      onChange={(_, name) => onChange(name ?? '', '')} renderInput={p => <TextField {...p} label={t(`${P}category`)} />} />
    <Autocomplete multiple options={selected?.attrs ?? []} value={attrs ? attrs.split(',') : []} disabled={disabled || !selected} slotProps={{ chip: { sx: { minHeight: 44, '& .MuiChip-deleteIcon': { width: 44, height: 44, p: '13px', boxSizing: 'border-box', mx: 0 } } } }}
      onChange={(_, values) => onChange(category, values.join(','))} renderInput={p => <TextField {...p} label={t(`${P}attrs`)} />} />
    <Typography variant="caption" color="text.secondary">{t(`${P}source`, { time: dateTime(query.data.updated_at) })}</Typography>
  </Stack>
}
