import { Alert, Autocomplete, Box, TextField } from '@mui/material'
import { isAxiosError } from 'axios'
import { useTranslation } from 'react-i18next'

import { AsyncButton } from '@/components/AsyncButton'
import { pushSnack } from '@/components/SnackbarHost'
import { useGeositeCategories, useUpdateGeosite } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { reportDestError } from '../destErrors'

/**
 * Picks a community category and, from the attributes that category really has
 * (the negated ones like !cn are shown literally), which of them to keep. With
 * no data downloaded (503) it says so and offers the download — never
 * downloading by itself.
 */
export default function GeositeCategoryPicker({ category, attrs, onChange, disabled }: {
  category: string
  attrs: string[]
  onChange: (category: string, attrs: string[]) => void
  disabled?: boolean
}) {
  const { t } = useTranslation(['admin'])
  const scope = useQueryScope()
  const q = useGeositeCategories(scope)
  const update = useUpdateGeosite(scope)
  const L = 'admin:access_control.lists.'

  if (q.isError && isAxiosError(q.error) && q.error.response?.status === 503) {
    return (
      <Alert severity="info" action={
        <AsyncButton size="small" color="inherit" onClick={async () => {
          try {
            await update.mutateAsync({})
            pushSnack(t(`${L}snack.downloaded`), 'success')
          } catch (err) { reportDestError(t, err) }
        }}>{t(`${L}geosite_download`)}</AsyncButton>
      }>
        {t(`${L}geosite_missing`)}
      </Alert>
    )
  }
  if (q.isError) return <Alert severity="error" action={<AsyncButton size="small" color="inherit" onClick={() => q.refetch()}>{t('admin:access_control.retry')}</AsyncButton>}>{t(`${L}load_failed`)}</Alert>

  const cats = q.data?.categories ?? []
  const current = cats.find(c => c.name === category) ?? null
  return (
    <Box sx={{ display: 'grid', gap: 1.5 }}>
      <Autocomplete size="small" options={cats} value={current} loading={q.isPending} disabled={disabled}
        getOptionLabel={c => c.name}
        isOptionEqualToValue={(a, b) => a.name === b.name}
        renderOption={(props, c) => <li {...props} key={c.name}>{t(`${L}dialog.category_option`, { name: c.name, entries: c.entries })}</li>}
        onChange={(_, c) => onChange(c?.name ?? '', [])}
        renderInput={params => <TextField {...params} label={t(`${L}dialog.category`)} />} />
      {current && current.attrs.length > 0 && (
        <Autocomplete multiple size="small" options={current.attrs.map(a => a.name)} value={attrs} disabled={disabled}
          onChange={(_, v) => onChange(category, v)}
          renderInput={params => <TextField {...params} label={t(`${L}dialog.attrs`)} />} />
      )}
    </Box>
  )
}
