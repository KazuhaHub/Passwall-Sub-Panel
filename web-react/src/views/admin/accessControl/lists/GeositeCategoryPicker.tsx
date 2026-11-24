import { isAxiosError } from 'axios'
import { useTranslation } from 'react-i18next'

import { AsyncButton } from '@/components/common/AsyncButton'
import { pushSnack } from '@/components/feedback/toasts'
import { Field } from '@/components/form/Field'
import { LevelAlert } from '@/components/LevelAlert'
import {
  Combobox, ComboboxChip, ComboboxChips, ComboboxChipsInput, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList,
  ComboboxValue, useComboboxAnchor,
} from '@/components/ui/combobox'
import { useGeositeCategories, useUpdateGeosite } from '@/query/accessControl'
import { useQueryScope } from '@/query/useQueryScope'
import { reportDestError } from '../destErrors'

interface Category { name: string; entries: number; attrs: { name: string }[] }

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
  const anchor = useComboboxAnchor()
  const L = 'admin:access_control.lists.'

  if (q.isError && isAxiosError(q.error) && q.error.response?.status === 503) {
    return (
      <LevelAlert level="info">
        <span className="flex flex-wrap items-center justify-between gap-2">
          <span>{t(`${L}geosite_missing`)}</span>
          <AsyncButton size="sm" variant="outline" onClick={async () => {
            try {
              await update.mutateAsync({})
              pushSnack(t(`${L}snack.downloaded`), 'success')
            } catch (err) { reportDestError(t, err) }
          }}>{t(`${L}geosite_download`)}</AsyncButton>
        </span>
      </LevelAlert>
    )
  }
  if (q.isError) {
    return (
      <LevelAlert level="error">
        <span className="flex flex-wrap items-center justify-between gap-2">
          <span>{t(`${L}load_failed`)}</span>
          <AsyncButton size="sm" variant="outline" onClick={() => q.refetch()}>{t('admin:access_control.retry')}</AsyncButton>
        </span>
      </LevelAlert>
    )
  }

  const cats = (q.data?.categories ?? []) as Category[]
  const current = cats.find(c => c.name === category) ?? null
  return (
    <div className="grid gap-4">
      <Field label={t(`${L}dialog.category`)}>
        {p => (
          <Combobox items={cats} value={current} disabled={disabled || q.isPending}
            onValueChange={(c: Category | null) => onChange(c?.name ?? '', [])}
            itemToStringLabel={(c: Category) => c.name} isItemEqualToValue={(a: Category, b: Category) => a.name === b.name}>
            <ComboboxInput id={p.id} disabled={disabled || q.isPending} aria-describedby={p['aria-describedby']} />
            <ComboboxContent>
              <ComboboxEmpty>{t(`${L}sheet.no_match`)}</ComboboxEmpty>
              <ComboboxList>
                {(c: Category) => <ComboboxItem key={c.name} value={c}>{t(`${L}dialog.category_option`, { name: c.name, entries: c.entries })}</ComboboxItem>}
              </ComboboxList>
            </ComboboxContent>
          </Combobox>
        )}
      </Field>
      {current && current.attrs.length > 0 && (
        <Field label={t(`${L}dialog.attrs`)}>
          {p => (
            <Combobox multiple items={current.attrs.map(a => a.name)} value={attrs} disabled={disabled}
              onValueChange={(v: string[]) => onChange(category, v)}>
              <ComboboxChips ref={anchor}>
                <ComboboxValue>
                  {(values: string[]) => (
                    <>
                      {values.map(v => <ComboboxChip key={v}>{v}</ComboboxChip>)}
                      <ComboboxChipsInput id={p.id} aria-describedby={p['aria-describedby']} />
                    </>
                  )}
                </ComboboxValue>
              </ComboboxChips>
              <ComboboxContent anchor={anchor}>
                <ComboboxEmpty>{t(`${L}sheet.no_match`)}</ComboboxEmpty>
                <ComboboxList>{(a: string) => <ComboboxItem key={a} value={a}>{a}</ComboboxItem>}</ComboboxList>
              </ComboboxContent>
            </Combobox>
          )}
        </Field>
      )}
    </div>
  )
}
