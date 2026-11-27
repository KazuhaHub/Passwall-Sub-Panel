import { useMemo, useState } from 'react'
import { IconSearch } from '@/lib/icons'
import { Input } from '@/components/ui/input'
import { useAdminT } from '../useAdminT'
import { SECTION_LABEL } from './model'
import { subsOf } from './tree'
import { searchSettings, type SearchEntry } from './searchIndex'

/** Finds a setting by the label it shows or the key it is stored under, and takes the admin to it. */
export function SettingsSearch({ onPick }: { onPick: (entry: SearchEntry) => void }) {
  const t = useAdminT()
  const [query, setQuery] = useState('')
  // Where a setting lives, in words: the section, then the sub-tab when the section has more than one.
  const where = useMemo(() => (e: SearchEntry) => {
    const subs = subsOf(e.section)
    const sub = subs.length > 1 ? subs.find(d => d.id === e.sub) : undefined
    return sub ? `${t(SECTION_LABEL[e.section])} · ${t(sub.label)}` : t(SECTION_LABEL[e.section])
  }, [t])
  const results = useMemo(() => searchSettings(query, e => t(e.label), where), [query, t, where])
  const pick = (e: SearchEntry) => { onPick(e); setQuery('') }
  return (
    <div role="search" className="grid gap-2">
      <div className="relative">
        <IconSearch aria-hidden className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input type="search" value={query} className="pl-8" placeholder={t('settings.search.placeholder')} aria-label={t('settings.search.label')}
          onChange={e => setQuery(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' && results[0]) { e.preventDefault(); pick(results[0]) }
            else if (e.key === 'Escape') setQuery('')
          }} />
      </div>
      {query.trim() && (
        results.length === 0
          ? <p role="status" className="px-1 text-sm text-muted-foreground">{t('settings.search.none', { query: query.trim() })}</p>
          : (
            <ul aria-label={t('settings.search.results')} className="grid gap-0.5 rounded-lg border p-1">
              {results.map(r => (
                <li key={`${r.section}-${r.sub}-${r.key}`}>
                  <button type="button" onClick={() => pick(r)} className="flex w-full flex-col rounded-md px-2 py-1.5 text-left outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50">
                    <span className="truncate text-sm">{t(r.label)}</span>
                    <span className="truncate text-xs text-muted-foreground">{where(r)} · <code>{r.key}</code></span>
                  </button>
                </li>
              ))}
            </ul>
          )
      )}
    </div>
  )
}
