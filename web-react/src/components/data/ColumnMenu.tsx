import type { Table } from '@tanstack/react-table'
import { Columns3Icon, RulerIcon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Button } from '@/components/ui/button'
import { TOUCH_HEIGHT } from '@/components/common/touchTarget'
import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

export function columnLabel<T>(col: ReturnType<Table<T>['getAllLeafColumns']>[number]): string {
  const meta = col.columnDef.meta
  if (meta?.label) return meta.label
  return typeof col.columnDef.header === 'string' && col.columnDef.header ? col.columnDef.header : col.id
}

/** The "Columns" button: a checklist of the columns that may be hidden, then "Reset column widths". Renders nothing when there is neither. */
export function ColumnMenu<T>({ table, onResetWidths, canResetWidths = false }: { table: Table<T>; onResetWidths?: () => void; canResetWidths?: boolean }) {
  const { t } = useSpacedT()
  const cols = table.getAllLeafColumns().filter(c => c.getCanHide() && c.columnDef.meta?.hideable !== false)
  if (cols.length === 0 && !onResetWidths) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" size="sm" className={TOUCH_HEIGHT} />}>
        <Columns3Icon aria-hidden="true" />
        {t('ui.data.columns')}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
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
        {onResetWidths && (
          <>
            {cols.length > 0 && <DropdownMenuSeparator />}
            {/* Disabled, not hidden, while nothing was resized: the item is where the person looks for it. */}
            <DropdownMenuItem disabled={!canResetWidths} onClick={onResetWidths} className="whitespace-nowrap">
              <RulerIcon aria-hidden="true" />
              {t('ui.data.reset_widths')}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
