import { useEffect, useId, useState } from 'react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { buildTzOptions, isKnownTz } from './tz'
import { useAdminT } from './useAdminT'

/**
 * The time zone the trend is bucketed in: free text with the browser's zone list as suggestions.
 * The URL (and so the chart request) only changes when the text is committed, on Enter, on leaving
 * the box or on picking a suggestion, never per keystroke. An unknown name is reported next to the
 * field and not committed, so the chart never asks the server for a zone it cannot resolve.
 */
export function TzField({ value, panelTz, onCommit }: { value: string; panelTz: string; onCommit: (tz: string) => void }) {
  const t = useAdminT()
  const id = useId()
  const listId = `${id}-zones`
  const errorId = `${id}-error`
  const [draft, setDraft] = useState(value)
  const [invalid, setInvalid] = useState(false)
  // The committed value moved from outside (the default realigning once the panel zone loads, Back, a link).
  useEffect(() => { setDraft(value); setInvalid(false) }, [value])

  const commit = (text: string) => {
    const next = text.trim()
    if (!isKnownTz(next)) { setInvalid(true); return }
    setInvalid(false)
    if (next !== value) onCommit(next)
  }

  return (
    <div className="min-w-0">
      <Label htmlFor={id} className="mb-1.5">{t('traffic.trend.timezone')}</Label>
      <Input
        id={id}
        className="w-full sm:w-56"
        list={listId}
        value={draft}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={invalid || undefined}
        aria-describedby={invalid ? errorId : undefined}
        onChange={e => {
          setDraft(e.target.value)
          // A pick from the suggestion list arrives as a change to exactly one of the names: commit it now.
          if (buildTzOptions(panelTz).includes(e.target.value)) commit(e.target.value)
          else setInvalid(false)
        }}
        onBlur={() => commit(draft)}
        onKeyDown={e => { if (e.key === 'Enter') commit(draft) }}
      />
      <datalist id={listId}>
        {buildTzOptions(panelTz).map(z => <option key={z} value={z} />)}
      </datalist>
      {invalid && <p id={errorId} role="alert" className="mt-1 text-xs text-destructive">{t('traffic.trend.timezone_unknown')}</p>}
    </div>
  )
}
