import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** A titled block of a user-page tab: a heading, an optional line under it, then the content. */
export function Block({ title, description, actions, children, className }: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children?: ReactNode
  className?: string
}) {
  return (
    <section className={cn('flex flex-col gap-3 rounded-xl border p-4', className)}>
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1">
        <div className="min-w-0">
          <h3 className="text-sm font-medium">{title}</h3>
          {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  )
}

/** Label / value rows. */
export function Facts({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">{children}</dl>
}

export function Fact({ label, children, mono, hidden }: { label: ReactNode; children: ReactNode; mono?: boolean; hidden?: boolean }) {
  if (hidden) return null
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn('min-w-0 break-words', mono && 'font-mono text-[0.8125rem] break-all')}>{children}</dd>
    </>
  )
}
