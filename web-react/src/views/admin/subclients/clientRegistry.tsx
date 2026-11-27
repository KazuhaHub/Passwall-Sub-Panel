// The client-family table of the Sub clients page (the detection-family list + its sheet). The data helpers
// live in ./presets and are re-exported here because SettingsView still imports normalizeRegistry from this path.
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { SubClientFamily } from '@/api/settings'
import { IconAdd, IconDelete, IconEdit } from '@/lib/icons'
import { MoreMenu } from '@/components/common/MoreMenu'
import { StatusDot } from '@/components/common/StatusDot'
import { applyReorder } from '@/components/data/dataTableLogic'
import { DataTable } from '@/components/data/DataTable'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Button } from '@/components/ui/button'
import { RowCard } from '../subscription/RowCard'
import { FamilyDialog } from './FamilyDialog'
import type { useRegistryActions } from './useRegistryActions'

export { CLIENT_PRESETS, PLATFORM_OPTIONS, clonePreset, normalizeRegistry } from './presets'

interface Row { f: SubClientFamily; i: number }
const sortless = { enableSorting: false } as const
const FIT = 'max-w-0 overflow-hidden text-ellipsis'

/**
 * The families in the order they are matched. Rows are identified by their position (a family has no id), which
 * is also why a reorder or a delete closes the sheet: it would otherwise point at a different family.
 */
export default function ClientRegistry({ families, onChange, actions }: {
  families: SubClientFamily[]
  onChange: (next: SubClientFamily[]) => void
  actions: ReturnType<typeof useRegistryActions>
}) {
  const { t } = useTranslation('admin')
  const { open, setOpen, addFamily, remove } = actions
  const rows = useMemo<Row[]>(() => families.map((f, i) => ({ f, i })), [families])
  const columns = useMemo<ColumnDef<Row, any>[]>(() => {
    const s = (k: string) => t(`settings.subscription.${k}`)
    return [
      { id: 'name', header: s('rule_table.name'), ...sortless, meta: { hideable: false, className: `${FIT} w-[26%]` },
        cell: c => { const n = c.row.original.f.name.trim(); return <span className={n ? 'font-medium' : 'text-muted-foreground'} title={n}>{n || s('unnamed_family')}</span> } },
      { id: 'keywords', header: s('rule_table.keywords'), ...sortless, meta: { className: `${FIT} w-[30%]` },
        cell: c => { const k = c.row.original.f.keywords.join(', '); return <span className="font-mono text-xs" title={k}>{k}</span> } },
      { id: 'format', header: s('rule_table.format'), ...sortless, meta: { className: 'w-28' }, cell: c => c.row.original.f.render_format },
      { id: 'apps', header: s('rule_table.apps'), ...sortless, meta: { align: 'right', className: 'w-16 tabular-nums' }, cell: c => c.row.original.f.apps.length },
      { id: 'enabled', header: s('rule_table.enabled'), ...sortless, meta: { className: 'w-28' },
        cell: c => c.row.original.f.enabled
          ? <StatusDot tone="success" label={s('rule_field.enabled')} />
          : <StatusDot tone="neutral" label={s('rule_table.disabled')} /> },
      { id: 'actions', header: '', ...sortless, meta: { hideable: false, align: 'right', label: s('rule_table.actions'), className: 'w-10' },
        cell: c => {
          const { f, i } = c.row.original
          const name = f.name.trim() || s('unnamed_family')
          return <MoreMenu size="icon-xs" label={t('settings.subscription.row_menu', { name })} items={[
            { id: 'edit', label: t('settings.subscription.edit_family'), icon: IconEdit, onSelect: () => setOpen(i) },
            { id: 'delete', label: s('delete_row'), icon: IconDelete, destructive: true, onSelect: () => void remove(i) },
          ]} />
        } },
    ]
  }, [t, setOpen, remove])

  return (
    <>
      <DataTable<Row>
        tableId="admin.subClients"
        data={rows}
        columns={columns}
        getRowId={r => String(r.i)}
        aria-label={t('settings.subscription.section_clients')}
        rowLabel={r => r.f.name || t('settings.subscription.unnamed_family')}
        pagination={false}
        columnMenu={false}
        onRowOpen={r => setOpen(r.i)}
        activeRowId={open === null ? null : String(open)}
        rowOptions={r => ({ state: r.f.enabled ? undefined : 'dimmed' })}
        renderCard={(r, ctx) => {
          const name = r.f.name.trim() || t('settings.subscription.unnamed_family')
          return (
            <RowCard title={name} subtitle={r.f.keywords.join(', ')} open={ctx.open}
              status={<StatusDot tone={r.f.enabled ? 'success' : 'neutral'} label={t(r.f.enabled ? 'settings.subscription.rule_field.enabled' : 'settings.subscription.rule_table.disabled')} />}
              detail={`${r.f.render_format} · ${t('settings.subscription.apps_count', { count: r.f.apps.length })}`}
              actions={<MoreMenu size="icon-xs" label={t('settings.subscription.row_menu', { name })} items={[
                { id: 'edit', label: t('settings.subscription.edit_family'), icon: IconEdit, onSelect: () => setOpen(r.i) },
                { id: 'delete', label: t('settings.subscription.delete_row'), icon: IconDelete, destructive: true, onSelect: () => void remove(r.i) },
              ]} />} />
          )
        }}
        onReorder={call => {
          // Only the order changes; every family keeps its content. The sheet closes because positions moved.
          setOpen(null)
          onChange(applyReorder(rows, r => String(r.i), call).map(r => r.f))
        }}
        empty={<EmptyState compact title={t('settings.subscription.no_rules')} action={<Button onClick={addFamily}><IconAdd />{t('settings.subscription.add_family')}</Button>} />}
      />
      <FamilyDialog families={families} index={open} onChange={onChange} onClose={() => setOpen(null)} />
    </>
  )
}
