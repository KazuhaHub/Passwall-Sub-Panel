import { useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { XIcon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { useReportValidity } from './FormValidity'
import { TOUCH_TARGET } from '@/components/data/touchTarget'

export interface TagsFieldProps {
  value: string[]
  onChange: (next: string[]) => void
  /** Known tags to pick from. Anything typed that is not among them can still be added. */
  suggestions?: readonly string[]
  placeholder?: string
  /** At least one tag is needed: while there is none, the form cannot be saved (see useReportValidity). */
  required?: boolean
  /** Most tags allowed. Past it, new ones are dropped and a message says so. */
  max?: number
  id?: string
  disabled?: boolean
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
  className?: string
}

// NFKC folds full-width letters and compatibility forms ('ＨＫ' is 'HK'), so tags that
// only differ in width are one tag.
const fold = (x: string) => x.normalize('NFKC').toLowerCase()
const same = (a: string, b: string) => fold(a) === fold(b)

// Half- and full-width commas, semicolons, the enumeration comma and newlines all
// separate tags: a list pasted from a spreadsheet or typed on a Chinese keyboard
// must not become one tag with punctuation in it.
const SEPARATORS = /[,，;；、\t\n\r]/
const SPLIT = /[,，;；、\t\n\r]+/
// During an IME composition the keys belong to the IME: Enter confirms a
// candidate, it is not "add this tag". keyCode 229 is how browsers that report
// isComposing late still mark those events.
const composing = (e: { nativeEvent: globalThis.KeyboardEvent; keyCode: number }) => e.nativeEvent.isComposing || e.keyCode === 229

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
  value, onChange, suggestions = [], placeholder, max, required, id, disabled, className, ...aria
}: TagsFieldProps) {
  const { t } = useSpacedT()
  const uid = useId()
  const listId = `${uid}-list`
  const input = useRef<HTMLInputElement>(null)
  const [text, setText] = useState('')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(-1)
  const isComposing = useRef(false)
  const [note, setNote] = useState<string | null>(null)
  useReportValidity(required && value.length === 0 ? t('ui.form.tags_required') : null)

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

  // Adds several tags at once: trimmed, empties dropped, duplicates (any case)
  // against the existing tags and within the batch dropped.
  const addMany = (raws: string[], rest = '') => {
    const next = [...value]
    let capped = false
    for (const raw of raws) {
      const tag = raw.trim()
      if (!tag || next.some(v => same(v, tag))) continue
      if (max !== undefined && next.length >= max) { capped = true; continue }
      next.push(tag)
    }
    setNote(capped ? t('ui.form.tags_max', { max }) : null)
    setText(rest)
    setActive(-1)
    if (next.length !== value.length) onChange(next)
  }
  const add = (raw: string) => addMany([raw])

  // Text containing a separator is split: every complete piece becomes a tag and
  // the last piece stays in the box as the next draft.
  const takeSeparators = (full: string) => {
    if (!SEPARATORS.test(full)) { setText(full); setOpen(true); setActive(-1); return }
    const parts = full.split(SPLIT)
    addMany(parts.slice(0, -1), parts[parts.length - 1])
  }
  const remove = (tag: string) => onChange(value.filter(v => v !== tag))

  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (composing(e)) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (!options.length) return
      e.preventDefault()
      setOpen(true)
      const step = e.key === 'ArrowDown' ? 1 : -1
      setActive(a => (a + step + options.length) % options.length)
    } else if (e.key === 'Enter') {
      if (active >= 0 && options[active]) { e.preventDefault(); add(options[active].label) }
      else if (typed) { e.preventDefault(); add(typed) }
    } else if (e.key === ',' || e.key === '，' || e.key === ';' || e.key === '；') {
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
    <div className="flex min-w-0 flex-col gap-1">
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
            className={cn(TOUCH_TARGET, 'rounded-full p-0.5 outline-none hover:bg-foreground/10 focus-visible:ring-2 focus-visible:ring-ring [&_svg]:size-3')}
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
        onChange={e => {
          if (e.nativeEvent instanceof InputEvent && e.nativeEvent.isComposing) { setText(e.target.value); return }
          if (isComposing.current) { setText(e.target.value); return }
          takeSeparators(e.target.value)
        }}
        onCompositionStart={() => { isComposing.current = true }}
        onCompositionEnd={e => { isComposing.current = false; takeSeparators(e.currentTarget.value) }}
        onPaste={e => {
          const pasted = e.clipboardData.getData('text')
          if (!SEPARATORS.test(pasted)) return
          e.preventDefault()
          // The whole paste is complete tags: nothing is left over as a draft.
          addMany(`${text}${pasted}`.split(SPLIT))
        }}
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
    {/* Always in the page, empty until the cap is hit. */}
    <p data-slot="tags-note" role="status" className={note ? 'text-xs text-destructive' : 'sr-only'}>{note}</p>
    </div>
  )
}
