import type { QuickLink } from '@/api/settings'
import { IconButton } from '@/components/common/IconButton'
import { EmptyState } from '@/components/feedback/EmptyState'
import { Field } from '@/components/form/Field'
import { QUICK_LINK_ICONS, QuickLinkIcon } from '@/components/QuickLinkIcon'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Switch } from '@/components/ui/switch'
import { IconAdd, IconApps } from '@/lib/icons'
import { validateUrl } from '@/utils/validators'
import { useAdminT } from '../useAdminT'
import { Group, RowTools, moved } from './parts'

type T = ReturnType<typeof useAdminT>
type UrlCheck = typeof validateUrl

/**
 * What stops the portal showing an empty chip or a dead link: every ENABLED row needs a label and a valid URL
 * (a disabled row may stay blank). Keyed by row, `quick_link_<n>_label` / `_url`, so a row can show its own.
 */
export function quickLinkProblems(links: readonly QuickLink[], t: T, check: UrlCheck): Record<string, string> {
  const out: Record<string, string> = {}
  links.forEach((l, i) => {
    if (!l.enabled) return
    if (!l.label.trim()) out[`quick_link_${i}_label`] = t('validation.required')
    const url = check(l.url, { required: true })
    if (url) out[`quick_link_${i}_url`] = t(url)
  })
  return out
}

/** One field for a link's icon: an emoji, an image URL or a built-in icon ("mui:Name"), with a live preview and a picker. */
function IconInput({ id, value, onChange }: { id: string; value: string; onChange: (v: string) => void }) {
  const t = useAdminT()
  return (
    <div className="flex items-center gap-1.5">
      <span className="flex size-(--control-h) shrink-0 items-center justify-center rounded-lg border bg-muted/40" aria-hidden>
        {value ? <QuickLinkIcon icon={value} className="size-4.5" /> : null}
      </span>
      <Input id={id} value={value} placeholder="😀 / https://… / mui:" onChange={e => onChange(e.target.value)} />
      <Popover>
        <PopoverTrigger render={<IconButton label={t('settings.portal.link_table.icon_pick')} variant="outline" />}><IconApps /></PopoverTrigger>
        <PopoverContent align="end" className="max-h-80 w-64 overflow-y-auto p-1">
          <button type="button" className="w-full rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent" onClick={() => onChange('')}>
            {t('settings.portal.link_table.icon_none')}
          </button>
          {QUICK_LINK_ICONS.map(ic => (
            <button key={ic.key} type="button" aria-pressed={value === `mui:${ic.key}`} onClick={() => onChange(`mui:${ic.key}`)}
              className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent aria-pressed:bg-accent">
              <ic.Icon aria-hidden className="size-4" />{ic.label}
            </button>
          ))}
        </PopoverContent>
      </Popover>
    </div>
  )
}

/**
 * The portal's shortcut links. They are part of the settings record and only persist with this section's Save, so
 * reordering is a local renumbering of `sort`, no request. Order is what the portal shows.
 */
export function QuickLinksEditor({ links, onChange, problems, show }: {
  links: QuickLink[]
  onChange: (next: QuickLink[]) => void
  problems: Record<string, string>
  /** A save was tried: show the "required" problems too, not only a URL that is typed wrong. */
  show: boolean
}) {
  const t = useAdminT()
  const update = (i: number, p: Partial<QuickLink>) => onChange(links.map((l, idx) => (idx === i ? { ...l, ...p } : l)))
  const add = () => onChange([...links, { label: '', url: '', icon: '', description: '', group: '', highlight: false, new_window: true, enabled: true, sort: (links.at(-1)?.sort ?? 0) + 10 }])
  const move = (from: number, to: number) => onChange(moved(links, from, to).map((l, idx) => ({ ...l, sort: (idx + 1) * 10 })))

  return (
    <Group id="setting-quick_links" title={t('settings.portal.section_quick_links')}>
      {links.length === 0 ? (
        <EmptyState compact title={t('settings.portal.no_links')} />
      ) : (
        <ol className="grid gap-3">
          {links.map((l, i) => {
            const name = t('settings.portal.link_name', { n: i + 1 })
            const urlErr = l.url || show ? problems[`quick_link_${i}_url`] : undefined
            const labelErr = show ? problems[`quick_link_${i}_label`] : undefined
            return (
              <li key={i} className="grid gap-3 rounded-lg border bg-muted/20 p-3" aria-label={name}>
                <div className="flex items-start gap-2">
                  <div className="grid min-w-0 flex-1 grid-cols-1 gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
                    <Field label={t('settings.portal.link_table.label')} required={l.enabled} error={labelErr}>
                      <Input id={`setting-quick_link_${i}_label`} value={l.label} onChange={e => update(i, { label: e.target.value })} />
                    </Field>
                    <Field label={t('settings.portal.link_table.url')} required={l.enabled} error={urlErr}>
                      <Input id={`setting-quick_link_${i}_url`} value={l.url} onChange={e => update(i, { url: e.target.value })} />
                    </Field>
                  </div>
                  <div className="pt-6"><RowTools index={i} count={links.length} name={name} onMove={to => move(i, to)} onRemove={() => onChange(links.filter((_, idx) => idx !== i))} /></div>
                </div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <Field label={t('settings.portal.link_table.icon')}><IconInput id={`setting-quick_link_${i}_icon`} value={l.icon} onChange={v => update(i, { icon: v })} /></Field>
                  <Field label={t('settings.portal.link_table.group')}><Input value={l.group} onChange={e => update(i, { group: e.target.value })} /></Field>
                  <Field label={t('settings.portal.link_table.description')}><Input value={l.description} onChange={e => update(i, { description: e.target.value })} /></Field>
                </div>
                <div className="flex flex-wrap gap-x-6 gap-y-2">
                  <Field orientation="horizontal" className="gap-2" label={t('settings.portal.link_table.new_window')}><Switch checked={l.new_window} onCheckedChange={c => update(i, { new_window: c })} /></Field>
                  <Field orientation="horizontal" className="gap-2" label={t('settings.portal.link_table.enabled')}><Switch checked={l.enabled} onCheckedChange={c => update(i, { enabled: c })} /></Field>
                  <Field orientation="horizontal" className="gap-2" label={t('settings.portal.link_table.highlight')}><Switch checked={l.highlight} onCheckedChange={c => update(i, { highlight: c })} /></Field>
                </div>
              </li>
            )
          })}
        </ol>
      )}
      <div><Button type="button" variant="outline" onClick={add}><IconAdd />{t('settings.portal.add_link')}</Button></div>
    </Group>
  )
}
