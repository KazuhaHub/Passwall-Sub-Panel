import { memo, useContext, useEffect, useState, type ComponentType } from 'react'
import { useSearchParams } from 'react-router'
import { Notice } from '@/components/common/Notice'
import { NativeSelect } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useIsMobile } from '@/hooks/use-mobile'
import { useAdminT } from '../useAdminT'
import { useAudience } from './audience'
import { SettingsDirtyContext } from './dirtyContext'
import type { SectionKey } from './model'
import { ScopeShell, useScopedDirty } from './ScopeShell'
import { placeId, subsOf, type SubDef } from './tree'

export const SUB_PARAM = 'sub'

/** One sub-tab's body: its form (with its own Save), told where to report unsaved changes. */
export type SubPanel = ComponentType<{ onDirtyChange: (dirty: boolean) => void }>

/**
 * The sub-tabs of one section: a bar on a wide screen, a menu on a phone (the same choice the rule-set page makes: a
 * row that scrolls hides what is off screen). The selected one is `?sub=` (the first leaves the URL clean), so a
 * sub-tab is a link and a reload stays put; a `sub` this section does not have reads as its first.
 *
 * A sub-tab is built on its first visit and then KEPT, hidden, like the sections themselves: what is typed in one
 * survives a look at another and a switch never asks anything. Each has its own Save; one with unsaved changes carries
 * a dot (and the word on a phone), and leaving the PAGE asks once and names it (SettingsPage).
 *
 * Every section is mounted from its first visit, hidden or not, so only the section on screen (`active`) reads
 * `?sub=`: a hidden one would otherwise build the panel a link meant for another section.
 */
export const SubTabs = memo(function SubTabs({ section, active, panels }: {
  section: SectionKey
  active: boolean
  panels: Readonly<Record<string, SubPanel>>
}) {
  const t = useAdminT()
  const phone = useIsMobile()
  const subs = subsOf(section)
  const [params, setParams] = useSearchParams()
  const wanted = params.get(SUB_PARAM)
  const first = subs[0].id
  const [kept, setKept] = useState(first)
  const current = active && wanted && subs.some(d => d.id === wanted) ? wanted : active ? first : kept
  const [visited, setVisited] = useState<ReadonlySet<string>>(new Set([current]))
  // A URL-driven change (a link, the back button, the search) must be remembered too, or the next switch would unmount it.
  useEffect(() => {
    if (active) setKept(current)
    setVisited(v => (v.has(current) ? v : new Set(v).add(current)))
  }, [current, active])
  const shown = visited.has(current) ? visited : new Set(visited).add(current)
  const { dirty, report } = useContext(SettingsDirtyContext)
  const isDirty = (id: string) => dirty.includes(placeId(section, id))

  const go = (id: string) => setParams(prev => {
    const out = new URLSearchParams(prev)
    if (id === first) out.delete(SUB_PARAM); else out.set(SUB_PARAM, id)
    return out
  }, { replace: true })

  const body = (def: SubDef) => {
    const place = placeId(section, def.id)
    const Panel = panels[def.id]
    if (!Panel) return null
    return <ScopedPanel def={def} Panel={Panel} report={report[place]} />
  }

  // One screen: no bar, nothing to switch.
  if (subs.length === 1) return <div data-subtab={placeId(section, subs[0].id)}>{body(subs[0])}</div>

  const mark = (id: string) => (isDirty(id) ? ` (${t('settings.unsaved_short')})` : '')
  return (
    <Tabs value={current} onValueChange={v => go(v as string)} className="gap-4">
      {phone ? (
        <NativeSelect aria-label={t('settings.sub_label')} value={current} onChange={e => go(e.target.value)}>
          {subs.map(d => <option key={d.id} value={d.id}>{t(d.label) + mark(d.id)}</option>)}
        </NativeSelect>
      ) : (
        <div className="max-w-full overflow-x-auto">
          <TabsList aria-label={t('settings.sub_label')}>
            {subs.map(d => (
              <TabsTrigger key={d.id} value={d.id}>
                {t(d.label)}
                {isDirty(d.id) && <span role="img" aria-label={t('settings.unsaved_short')} title={t('settings.unsaved_short')} className="size-1.5 rounded-full bg-warning" />}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
      )}
      {subs.map(d => (
        // keepMounted: a panel builds its own form, which the Tabs component cannot see, so it would unmount the draft.
        <TabsContent key={d.id} value={d.id} keepMounted data-subtab={placeId(section, d.id)}>
          {shown.has(d.id) ? body(d) : null}
        </TabsContent>
      ))}
    </Tabs>
  )
})

/**
 * What the audience means on this sub-tab. A tab with overrides shows the chosen group's overrides in place of its
 * global form; a tab without says that it is global-only and keeps its form, so the audience never changes what a
 * setting means from one tab to the next.
 */
function ScopedPanel({ def, Panel, report }: { def: SubDef; Panel: SubPanel; report: (dirty: boolean) => void }) {
  const t = useAdminT()
  const { groupId } = useAudience()
  if (!def.scope) {
    return (
      <>
        {groupId !== 0 && <Notice tone="info" className="mb-4">{t('settings.scope.global_only')}</Notice>}
        <Panel onDirtyChange={report} />
      </>
    )
  }
  return <ScopedWithOverrides scope={def.scope} Panel={Panel} report={report} />
}

function ScopedWithOverrides({ scope, Panel, report }: { scope: NonNullable<SubDef['scope']>; Panel: SubPanel; report: (dirty: boolean) => void }) {
  const { setForm, setScope } = useScopedDirty(report)
  return (
    <ScopeShell scope={scope} onScopeDirty={setScope}>
      <Panel onDirtyChange={setForm} />
    </ScopeShell>
  )
}
