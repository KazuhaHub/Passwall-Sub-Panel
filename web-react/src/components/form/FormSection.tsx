import { useId, useState, type ReactNode } from 'react'
import { ChevronRightIcon } from 'lucide-react'
import { useSpacedT } from '@/lib/cjkSpacing'
import { cn } from '@/lib/utils'

/** localStorage key for an advanced section's remembered open state. */
export function formSectionKey(sectionKey: string): string {
  return `psp.formSection.v1.${sectionKey}`
}

function readOpen(sectionKey: string | undefined, fallback: boolean): boolean {
  if (!sectionKey) return fallback
  try {
    const raw = localStorage.getItem(formSectionKey(sectionKey))
    return raw === '1' ? true : raw === '0' ? false : fallback
  } catch { return fallback }
}

export interface FormSectionProps {
  title: ReactNode
  description?: ReactNode
  /** Collapsed by default, with its open state remembered under `sectionKey`. */
  advanced?: boolean
  /** Identifies the section for the remembered state: unique per page and section. */
  sectionKey?: string
  /** Open it regardless of what was remembered, e.g. while a field inside has an error. */
  forceOpen?: boolean
  /** Right-aligned controls on the title row (a section-level reset, a help link). */
  actions?: ReactNode
  className?: string
  children: ReactNode
}

/**
 * A titled group of fields. The advanced variant is collapsed by default
 * (plan 3.3) and remembers how each person left it. Its body is hidden, not
 * unmounted, so a draft typed in it survives a collapse and the fields still
 * exist for validation.
 */
export function FormSection({ title, description, advanced, sectionKey, forceOpen, actions, className, children }: FormSectionProps) {
  const { t } = useSpacedT()
  const uid = useId()
  const titleId = `${uid}-title`
  const bodyId = `${uid}-body`
  const [remembered, setRemembered] = useState(() => readOpen(sectionKey, false))
  const open = !advanced || forceOpen === true || remembered

  const toggle = () => {
    const next = !remembered
    setRemembered(next)
    if (sectionKey) {
      try { localStorage.setItem(formSectionKey(sectionKey), next ? '1' : '0') } catch { /* remembered for this visit only */ }
    }
  }

  return (
    <section aria-labelledby={titleId} data-slot="form-section" className={cn('flex flex-col gap-3', className)}>
      <header className="flex items-start justify-between gap-3">
        {advanced ? (
          <button
            type="button"
            id={titleId}
            aria-expanded={open}
            aria-controls={bodyId}
            onClick={toggle}
            disabled={forceOpen === true}
            title={open ? t('ui.form.advanced_hide') : t('ui.form.advanced_show')}
            className="group/section -ml-1 flex min-w-0 items-start gap-1.5 rounded-md px-1 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <ChevronRightIcon className={cn('mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform', open && 'rotate-90')} aria-hidden="true" />
            <span className="flex min-w-0 flex-col gap-0.5">
              <span className="text-sm font-medium">{title}</span>
              {description && <span className="text-xs font-normal text-muted-foreground">{description}</span>}
            </span>
          </button>
        ) : (
          <div className="flex min-w-0 flex-col gap-0.5">
            <h3 id={titleId} className="text-sm font-medium">{title}</h3>
            {description && <p className="text-xs text-muted-foreground">{description}</p>}
          </div>
        )}
        {actions && <div className="flex shrink-0 items-center gap-1.5">{actions}</div>}
      </header>
      <div id={bodyId} hidden={!open} className="grid gap-4">{children}</div>
    </section>
  )
}
