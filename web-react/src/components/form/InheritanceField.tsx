import { useId, useState, type CSSProperties, type ReactNode } from 'react'
import { useSpacedT } from '@/i18n/spacing'
import { cn } from '@/lib/utils'
import { SegmentedControl } from './SegmentedControl'
import { InheritEditorContext } from './editorContext'

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
  const { t } = useSpacedT()
  const inEffect = effectiveLevel(levels)
  return (
    // Stacked on a phone, where three cells side by side leave each about 100px and
    // cut the state off; side by side from sm up.
    <ol
      aria-label={t('ui.form.inherit_trail')}
      className={cn('flex flex-col gap-2 sm:grid sm:gap-3 sm:[grid-template-columns:repeat(var(--levels),minmax(0,1fr))]', className)}
      style={{ '--levels': levels.length } as CSSProperties}
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
              "sm:not-last:after:absolute sm:not-last:after:top-1/2 sm:not-last:after:-right-2.5 sm:not-last:after:-translate-y-1/2 sm:not-last:after:text-muted-foreground sm:not-last:after:content-['›']",
              active ? 'border-brand bg-brand/10 ring-1 ring-brand/40' : 'bg-muted/30',
            )}
          >
            <span className="text-xs text-foreground/70">{level.label}</span>
            <span className={cn('text-sm break-words', set ? 'font-medium' : 'text-foreground/70 italic')}>
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
  /** The field label's id: the editor must be named by it, or a screen reader hears a bare number box. */
  'aria-labelledby': string
  'aria-describedby'?: string
}

export interface InheritanceFieldProps<T> {
  /** A string, because it also names the controls inside: the editor and the follow / set switch. */
  label: string
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
  /**
   * What the editor can actually hold of an inherited value (rounded to a whole number,
   * clamped to its range). Give it whenever the editor is narrower than the level above.
   * If the result differs from the inherited value, "set here" does NOT write it: that would
   * store a number nobody chose and make the form dirty. The editor starts from the adjusted
   * value, a note says so, and nothing is written until the person edits it.
   */
  fitInherited?: (inherited: T) => T
  hint?: ReactNode
  error?: ReactNode
  className?: string
}

/**
 * Edits one level of a value that is inherited from above: shows the trail,
 * then a two-way switch "跟随上级 / 在此设置" and, only after "set here", the
 * editor. The switch is the ONLY way back to following: the editor offers none
 * of its own, and an emptied editor is simply invalid until it is filled (or the
 * person picks "跟随上级"), so no path unmounts a control that has focus. Choosing
 * "set here" seeds the editor with the value that was in effect, so nobody lands
 * on an empty or zero box and saves it by accident.
 */
export function InheritanceField<T>({
  label, levels, editLevel, onChange, renderValue, renderEditor, defaultValue, hint, error, className, fitInherited,
}: InheritanceFieldProps<T>) {
  const { t } = useSpacedT()
  const uid = useId()
  const idx = levels.findIndex(l => l.id === editLevel)
  const own = idx >= 0 ? levels[idx] : undefined
  const above = effectiveLevel(levels.slice(0, Math.max(idx, 0)))
  const isSet = own?.value !== undefined
  // "Set here" chosen, but the value to start from had to be adjusted: shown in the editor,
  // not yet written. Dropped as soon as a real value exists (an edit) or the mode goes back.
  const [pending, setPending] = useState<{ value: T; parent: T } | null>(null)
  if (isSet && pending) setPending(null)
  const adjusting = !isSet && pending !== null
  const noteId = `${uid}-note`
  const labelId = `${uid}-label`

  const setMode = (mode: 'follow' | 'set') => {
    setPending(null)
    if (mode === 'follow') { onChange(undefined); return }
    const inherited = above?.value
    if (inherited !== undefined && fitInherited) {
      const fitted = fitInherited(inherited)
      if (!Object.is(fitted, inherited)) { setPending({ value: fitted, parent: inherited }); return }
    }
    onChange(inherited !== undefined ? inherited : defaultValue)
  }

  return (
    <div data-slot="inheritance-field" className={cn('flex flex-col gap-2', className)}>
      <span id={labelId} className="text-sm leading-snug font-medium">{label}</span>
      <SourceTrail levels={levels} renderValue={renderValue} />
      {own && (
        <div className="flex flex-col gap-2">
          <div className="w-fit max-w-full">
          <SegmentedControl
            aria-label={t('ui.form.inherit_mode', { label, level: own.label })}
            size="sm"
            value={isSet || adjusting ? 'set' : 'follow'}
            onChange={setMode}
            options={[
              { value: 'follow', label: t('ui.form.inherit_follow') },
              { value: 'set', label: t('ui.form.inherit_set_here') },
            ]}
          />
          </div>
          {isSet || adjusting ? (
            // The editor is told it is inside an inheritance field: an empty box is
            // invalid-until-filled there, and "follow" is the mode control's alone.
            <>
            <InheritEditorContext.Provider value>
              {renderEditor({
                id: `${uid}-editor`,
                value: isSet ? own.value as T : pending!.value,
                onChange: next => { setPending(null); onChange(next) },
                'aria-labelledby': labelId,
                'aria-describedby': error || hint ? noteId : undefined,
              })}
            </InheritEditorContext.Provider>
            {adjusting && (
              <p className="text-xs text-muted-foreground">
                {t('ui.form.inherit_adjusted', { parent: renderValue(pending!.parent), value: renderValue(pending!.value) })}
              </p>
            )}
            </>
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
