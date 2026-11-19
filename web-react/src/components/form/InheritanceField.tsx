import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { SegmentedControl } from './SegmentedControl'

/**
 * One level of "全局 → 分组 → 本人". `value` is what is stored AT this level:
 * `undefined` means the level sets nothing and follows the one above. That
 * absence is deliberately a different thing from every real value, including
 * 0, "" and false, so "follow" can never be written by typing 0 (audit
 * section 3: create meant "follow the group" by 0 and edit meant "unlimited").
 */
export interface InheritanceLevel<T> {
  id: string
  label: string
  value: T | undefined
}

/** The deepest level that sets a value: that is the one in effect. */
export function effectiveLevel<T>(levels: readonly InheritanceLevel<T>[]): InheritanceLevel<T> | undefined {
  for (let i = levels.length - 1; i >= 0; i--) if (levels[i].value !== undefined) return levels[i]
  return undefined
}

export interface SourceTrailProps<T> {
  levels: readonly InheritanceLevel<T>[]
  /** Text for a value ("不限量", "50 GB"). A string, so it can also be spoken and copied. */
  renderValue: (v: T) => string
  className?: string
}

/**
 * The read-only trail: every level's value in order, the level in effect
 * highlighted and labelled. Used on its own wherever a value needs its origin
 * explained but not edited (the Settings scope rail, a user's effective quota).
 */
export function SourceTrail<T>({ levels, renderValue, className }: SourceTrailProps<T>) {
  const { t } = useTranslation('common')
  const inEffect = effectiveLevel(levels)
  return (
    <ol
      aria-label={t('ui.form.inherit_trail')}
      className={cn('grid gap-3', className)}
      style={{ gridTemplateColumns: `repeat(${levels.length}, minmax(0, 1fr))` }}
    >
      {levels.map(level => {
        const active = level === inEffect
        const set = level.value !== undefined
        return (
          <li
            key={level.id}
            aria-current={active ? 'true' : undefined}
            className={cn(
              'relative flex min-w-0 flex-col gap-0.5 rounded-lg border px-2.5 py-1.5',
              // A small chevron between cells; decorative, so it is CSS and not read out.
              "not-last:after:absolute not-last:after:top-1/2 not-last:after:-right-2.5 not-last:after:-translate-y-1/2 not-last:after:text-muted-foreground not-last:after:content-['›']",
              active ? 'border-brand bg-brand/10 ring-1 ring-brand/40' : 'bg-muted/30',
            )}
          >
            <span className="truncate text-xs text-muted-foreground">{level.label}</span>
            <span className={cn('truncate text-sm', set ? 'font-medium' : 'text-muted-foreground italic')}>
              {set ? renderValue(level.value as T) : t(level === levels[0] ? 'ui.form.inherit_not_set' : 'ui.form.inherit_follows')}
            </span>
            {active && (
              <span className="mt-0.5 w-fit rounded-full bg-brand px-1.5 text-[10px] leading-4 font-medium text-brand-foreground">
                {t('ui.form.inherit_in_effect')}
              </span>
            )}
          </li>
        )
      })}
    </ol>
  )
}

export interface InheritanceEditorProps<T> {
  id: string
  value: T
  onChange: (next: T) => void
  'aria-describedby'?: string
}

export interface InheritanceFieldProps<T> {
  label: ReactNode
  levels: readonly InheritanceLevel<T>[]
  /** The id of the one level this field edits (the lowest, usually). */
  editLevel: string
  /** `undefined` means "follow the level above"; any other value is an explicit choice. */
  onChange: (next: T | undefined) => void
  renderValue: (v: T) => string
  /** The control for an explicit value: a NumberField, a Switch, a select. */
  renderEditor: (p: InheritanceEditorProps<T>) => ReactNode
  /** What "set here" starts from when no level above defines a value. */
  defaultValue: T
  hint?: ReactNode
  error?: ReactNode
  className?: string
}

/**
 * Edits one level of a value that is inherited from above: shows the trail,
 * then a two-way switch "跟随上级 / 在此设置" and, only after "set here", the
 * editor. The switch is the single place follow is chosen, and choosing "set
 * here" seeds the editor with the value that was in effect, so nobody lands on
 * an empty or zero box and saves it by accident.
 */
export function InheritanceField<T>({
  label, levels, editLevel, onChange, renderValue, renderEditor, defaultValue, hint, error, className,
}: InheritanceFieldProps<T>) {
  const { t } = useTranslation('common')
  const uid = useId()
  const idx = levels.findIndex(l => l.id === editLevel)
  const own = idx >= 0 ? levels[idx] : undefined
  const above = effectiveLevel(levels.slice(0, Math.max(idx, 0)))
  const isSet = own?.value !== undefined
  const noteId = `${uid}-note`

  const setMode = (mode: 'follow' | 'set') => {
    if (mode === 'follow') onChange(undefined)
    else onChange(above?.value !== undefined ? above.value : defaultValue)
  }

  return (
    <div data-slot="inheritance-field" className={cn('flex flex-col gap-2', className)}>
      <span id={`${uid}-label`} className="text-sm leading-snug font-medium">{label}</span>
      <SourceTrail levels={levels} renderValue={renderValue} />
      {own && (
        <div className="flex flex-col gap-2">
          <SegmentedControl
            aria-label={t('ui.form.inherit_mode', { level: own.label })}
            size="sm"
            value={isSet ? 'set' : 'follow'}
            onChange={setMode}
            options={[
              { value: 'follow', label: t('ui.form.inherit_follow') },
              { value: 'set', label: t('ui.form.inherit_set_here') },
            ]}
          />
          {isSet ? (
            renderEditor({
              id: `${uid}-editor`,
              value: own.value as T,
              onChange: v => onChange(v),
              'aria-describedby': error || hint ? noteId : undefined,
            })
          ) : (
            <p className="text-xs text-muted-foreground">
              {above?.value !== undefined
                ? t('ui.form.inherit_following', { value: renderValue(above.value) })
                : t('ui.form.inherit_not_set')}
            </p>
          )}
        </div>
      )}
      {error
        ? <p id={noteId} aria-live="polite" className="text-xs text-destructive">{error}</p>
        : hint ? <p id={noteId} className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  )
}
