import { useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { XIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

export interface TagsFieldProps {
  value: string[]
  onChange: (next: string[]) => void
  /** Known tags to pick from. Anything typed that is not among them can still be added. */
  suggestions?: readonly string[]
  placeholder?: string
  id?: string
  disabled?: boolean
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
  className?: string
}

const same = (a: string, b: string) => a.toLowerCase() === b.toLowerCase()

/**
 * Free tags with suggestions (replaces TagsAutocomplete). Chosen tags are
 * chips; the box adds a tag on Enter or comma, and the typed text is ALSO
 * offered as an "Add" row, because Enter alone is a dead end on a phone. Text
 * left in the box when focus moves away is added, not thrown away: silently
 * dropping what someone typed is worse than adding a tag they can remove.
 *
 * Order is the person's: tags are never sorted, and a duplicate (any case) is
 * ignored rather than added twice.
 */
export function TagsField({
  value, onChange, suggestions = [], placeholder, id, disabled, className, ...aria
}: TagsFieldProps) {
  const { t } = useTranslation('common')
  const uid = useId()
  const listId = `${uid}-list`
  const input = useRef<HTMLInputElement>(null)
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)

  const typed = text.trim()
  const options = useMemo(() => {
    const free = suggestions.filter(s => !value.some(v => same(v, s)))
    const matching = typed ? free.filter(s => s.toLowerCase().includes(typed.toLowerCase())) : free
    const creatable = typed && !value.some(v => same(v, typed)) && !suggestions.some(s => same(s, typed))
    return [
      ...matching.map(label => ({ label, create: false })),
      ...(creatable ? [{ label: typed, create: true }] : []),
    ]
  }, [suggestions, value, typed])

  const add = (raw: string) => {
    const tag = raw.trim()
    setText('')
    setActive(-1)
    if (!tag || value.some(v => same(v, tag))) return
    onChange([...value, tag])
  }
  const remove = (tag: string) => onChange(value.filter(v => v !== tag))

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (!options.length) return
      e.preventDefault()
      setOpen(true)
      const step = e.key === 'ArrowDown' ? 1 : -1
      setActive(a => (a + step + options.length) % options.length)
    } else if (e.key === 'Enter') {
      if (active >= 0 && options[active]) { e.preventDefault(); add(options[active].label) }
      else if (typed) { e.preventDefault(); add(typed) }
    } else if (e.key === ',') {
      e.preventDefault()
      add(text)
    } else if (e.key === 'Backspace' && text === '' && value.length) {
      remove(value[value.length - 1])
    } else if (e.key === 'Escape') {
      if (open) { e.stopPropagation(); setOpen(false); setActive(-1) }
    }
  }

  const showList = open && options.length > 0
  return (
    <div
      data-slot="tags-field"
      className={cn(
        'relative flex min-h-(--control-h) w-full min-w-0 flex-wrap items-center gap-1 rounded-lg border border-input bg-transparent px-1.5 py-1 transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 dark:bg-input/30',
        'data-[invalid=true]:border-destructive data-[invalid=true]:ring-3 data-[invalid=true]:ring-destructive/20',
        disabled && 'pointer-events-none opacity-50',
        className,
      )}
      data-invalid={aria['aria-invalid'] === true || aria['aria-invalid'] === 'true' ? 'true' : undefined}
      onClick={() => input.current?.focus()}
    >
      {value.map(tag => (
        <Badge key={tag} variant="secondary" className="h-5 max-w-full gap-0.5 pr-0.5">
          <span className="truncate">{tag}</span>
          <button
            type="button"
            aria-label={t('ui.form.tags_remove', { tag })}
            title={t('ui.form.tags_remove', { tag })}
            onClick={e => { e.stopPropagation(); remove(tag); input.current?.focus() }}
            className="rounded-full p-0.5 outline-none hover:bg-foreground/10 focus-visible:ring-2 focus-visible:ring-ring [&_svg]:size-3"
          >
            <XIcon />
          </button>
        </Badge>
      ))}
      <input
        ref={input}
        id={id}
        role="combobox"
        aria-expanded={showList}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={showList && active >= 0 ? `${listId}-${active}` : undefined}
        aria-label={aria['aria-label']}
        aria-labelledby={aria['aria-labelledby']}
        aria-describedby={aria['aria-describedby']}
        aria-invalid={aria['aria-invalid']}
        disabled={disabled}
        value={text}
        placeholder={value.length ? undefined : (placeholder ?? t('ui.form.tags_placeholder'))}
        autoComplete="off"
        onChange={e => { setText(e.target.value); setOpen(true); setActive(-1) }}
        onFocus={() => setOpen(true)}
        onBlur={() => { setOpen(false); if (typed) add(typed) }}
        onKeyDown={onKey}
        className="h-6 min-w-24 flex-1 bg-transparent px-1 text-base outline-none placeholder:text-muted-foreground md:text-sm"
      />
      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label={t('ui.form.tags_suggestions')}
          // Keeps focus in the box while an option is pressed, so the blur
          // handler does not fire first and add the half-typed text.
          onMouseDown={e => e.preventDefault()}
          className="absolute top-full left-0 z-30 mt-1 max-h-56 w-full min-w-40 overflow-auto rounded-lg bg-popover p-1 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10"
        >
          {options.map((o, i) => (
            <li
              key={`${o.create ? 'new' : 'old'}-${o.label}`}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              onClick={() => { add(o.label); input.current?.focus() }}
              onMouseEnter={() => setActive(i)}
              className={cn('cursor-pointer rounded-md px-2 py-1.5', i === active && 'bg-accent text-accent-foreground', o.create && 'font-medium')}
            >
              {o.create ? t('ui.form.tags_add', { tag: o.label }) : o.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
