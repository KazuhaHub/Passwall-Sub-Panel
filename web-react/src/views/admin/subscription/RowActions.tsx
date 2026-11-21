import { IconButton } from '@/components/common/IconButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { IconEdit } from '@/lib/icons'

/**
 * The end of a row of the rule-set and template tables: ONE visible action (edit) and the rest in the row menu,
 * both named after the row so a screen reader hears which one.
 */
export function RowActions({ name, editLabel, onEdit, busy, menu, menuLabel }: {
  name: string
  editLabel: string
  onEdit: () => void
  /** A request for this row is in flight: its actions wait. */
  busy?: boolean
  menu: MoreMenuItem[]
  menuLabel: string
}) {
  return (
    <span className="inline-flex items-center justify-end gap-0.5">
      <IconButton label={`${editLabel}: ${name}`} size="icon-xs" disabled={busy} onClick={onEdit}><IconEdit /></IconButton>
      <MoreMenu items={menu} size="icon-xs" label={menuLabel} />
    </span>
  )
}
