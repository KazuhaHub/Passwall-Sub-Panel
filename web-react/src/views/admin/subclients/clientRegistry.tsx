// The client-family table of the Sub clients page (the detection-family list + its sheet). The data helpers
// live in ./presets and are re-exported here because SettingsView still imports normalizeRegistry from this path.
import { useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import type { ColumnDef } from '@tanstack/react-table'
import type { SubClientFamily } from '@/api/settings'
import { IconAdd, IconDelete, IconEdit } from '@/lib/icons'
import { MoreMenu } from '@/components/common/MoreMenu'
import { applyReorder } from '@/components/data/dataTableLogic'
import { DataTable } from '@/components/data/DataTable'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { RowCard } from '../subscription/RowCard'
import { FamilyDialog } from './FamilyDialog'
import type { useRegistryActions } from './useRegistryActions'

export { CLIENT_PRESETS, PLATFORM_OPTIONS, clonePreset, normalizeRegistry } from './presets'

interface Row { f: SubClientFamily; i: number }

// A family has no id of its own, and its position is not one: a reorder would give every row a different
// identity, and the table's drop animation (which follows rows by id) would draw them through each other's
// places. The object itself is what stays the same across a reorder (it is only moved), so it is what carries
// the id. An edit replaces the object and so gives a new row, which is right: it is a different value.
const familyIds = new WeakMap<SubClientFamily, string>()
let familySeq = 0
const familyId = (f: SubClientFamily) => {
  let id = familyIds.get(f)
  if (id === undefined) { id = `family-${++familySeq}`; familyIds.set(f, id) }
  return id
}
const sortless = { enableSorting: false } as const
const FIT = 'max-w-0 overflow-hidden text-ellipsis'

/**
 * The families in the order they are matched. The open sheet is tracked by position, which is why a reorder or a
 * delete closes it: it would otherwise point at a different family. Row ids are not positions (see familyId).
 */
export default function ClientRegistry({ families, onChange, actions }: {
  families: SubClientFamily[]
  onChange: (next: SubClientFamily[]) => void
  actions: ReturnType<typeof useRegistryActions>
}) {
  const { t } = useTranslation('admin')
  const { open, setOpen, addFamily, remove } = actions
  const rows = useMemo<Row[]>(() => families.map((f, i) => ({ f, i })), [families])
  // The switch edits the draft like every other field of the page: it is saved with the page's Save.
  const setEnabled = useCallback((i: number, enabled: boolean) => {
    onChange(families.map((f, n) => (n === i ? { ...f, enabled } : f)))
  }, [families, onChange])
  const enableSwitch = (r: Row) => (
    <span data-no-row-click="" className="inline-flex">
      <Switch size="sm" checked={r.f.enabled} onCheckedChange={v => setEnabled(r.i, v)}
        aria-label={t('settings.subscription.enable_named', { name: r.f.name.trim() || t('settings.subscription.unnamed_family') })} />
    </span>
  )
  const columns = useMemo<ColumnDef<Row, any>[]>(() => {
    const s = (k: string) => t(`settings.subscription.${k}`)
    return [
      { id: 'name', header: s('rule_table.name'), ...sortless, meta: { hideable: false, className: `${FIT} w-[26%]` },
        cell: c => { const n = c.row.original.f.name.trim(); return <span className={n ? 'font-medium' : 'text-muted-foreground'} title={n}>{n || s('unnamed_family')}</span> } },
      { id: 'keywords', header: s('rule_table.keywords'), ...sortless, meta: { className: `${FIT} w-[30%]` },
        cell: c => { const k = c.row.original.f.keywords.join(', '); return <span className="font-mono text-xs" title={k}>{k}</span> } },
      { id: 'format', header: s('rule_table.format'), ...sortless, meta: { className: 'w-28' }, cell: c => c.row.original.f.render_format },
      { id: 'apps', header: s('rule_table.apps'), ...sortless, meta: { align: 'right', className: 'w-16 tabular-nums' }, cell: c => c.row.original.f.apps.length },
      { id: 'enabled', header: s('rule_table.enabled'), ...sortless, meta: { className: 'w-20' },
        cell: c => enableSwitch(c.row.original) },
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t, setOpen, remove, setEnabled])

  return (
    <>
      <DataTable<Row>
        tableId="admin.subClients"
        data={rows}
        columns={columns}
        getRowId={r => familyId(r.f)}
        aria-label={t('settings.subscription.section_clients')}
        rowLabel={r => r.f.name || t('settings.subscription.unnamed_family')}
        pagination={false}
        columnMenu={false}
        onRowOpen={r => setOpen(r.i)}
        activeRowId={open === null || !families[open] ? null : familyId(families[open])}
        rowOptions={r => ({ state: r.f.enabled ? undefined : 'dimmed' })}
        renderCard={(r, ctx) => {
          const name = r.f.name.trim() || t('settings.subscription.unnamed_family')
          return (
            <RowCard title={name} subtitle={r.f.keywords.join(', ')} open={ctx.open}
              status={enableSwitch(r)}
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
          onChange(applyReorder(rows, r => familyId(r.f), call).map(r => r.f))
        }}
        empty={<EmptyState compact title={t('settings.subscription.no_rules')} action={<Button onClick={addFamily}><IconAdd />{t('settings.subscription.add_family')}</Button>} />}
      />
      <FamilyDialog families={families} index={open} onChange={onChange} onClose={() => setOpen(null)} />
    </>
  )
}
