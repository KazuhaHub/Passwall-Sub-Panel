import type { ReactNode } from 'react'

/**
 * A row at phone width (DataTable's `renderCard`): the name (which opens the row), what identifies it, a state,
 * a line of detail and the row's actions. Long text truncates with the full text in `title`.
 */
export function RowCard({ title, subtitle, status, detail, actions, open }: {
  title: string
  subtitle?: string
  status?: ReactNode
  detail?: ReactNode
  actions?: ReactNode
  open?: () => void
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <div className="flex min-w-0 items-center gap-2">
        <button type="button" onClick={open} className="min-w-0 flex-1 truncate text-left text-sm font-medium outline-none focus-visible:underline" title={title}>{title}</button>
        {status}
        {actions}
      </div>
      {subtitle && <span className="truncate font-mono text-xs text-muted-foreground" title={subtitle}>{subtitle}</span>}
      {detail && <div className="min-w-0 text-xs text-muted-foreground">{detail}</div>}
    </div>
  )
}
