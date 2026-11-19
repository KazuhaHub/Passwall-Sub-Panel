import type { Table } from '@tanstack/react-table'
import { Columns3Icon } from 'lucide-react'
import { useSpacedT } from '@/lib/cjkSpacing'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuGroup, DropdownMenuLabel, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

export function columnLabel<T>(col: ReturnType<Table<T>['getAllLeafColumns']>[number]): string {
  const meta = col.columnDef.meta
  if (meta?.label) return meta.label
  return typeof col.columnDef.header === 'string' && col.columnDef.header ? col.columnDef.header : col.id
}

/** The "Columns" button: a checklist of the columns that may be hidden. Renders nothing when none may. */
export function ColumnMenu<T>({ table }: { table: Table<T> }) {
  const { t } = useSpacedT()
  const cols = table.getAllLeafColumns().filter(c => c.getCanHide() && c.columnDef.meta?.hideable !== false)
  if (cols.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" size="sm" />}>
        <Columns3Icon aria-hidden="true" />
        {t('ui.data.columns')}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t('ui.data.columns_menu')}</DropdownMenuLabel>
          {cols.map(c => (
            <DropdownMenuCheckboxItem
              key={c.id}
              checked={c.getIsVisible()}
              onCheckedChange={v => c.toggleVisibility(v === true)}
              // Keep the menu open: turning on three columns should not take three trips.
              closeOnClick={false}
            >
              {columnLabel(c)}
            </DropdownMenuCheckboxItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
