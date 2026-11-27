import { useCallback, useEffect, useMemo, useState, type ComponentType } from 'react'
import { useSearchParams } from 'react-router'
import { PageHeader } from '@/components/common/PageHeader'
import { useLeaveGuard } from '@/hooks/useLeaveGuard'
import { useTabParam } from '@/hooks/useTabParam'
import { discardChangesConfirm } from '../accessControl/confirmCopy'
import { useDirtyTabs } from '../nodes/detail/useDirtyTabs'
import { useAdminT } from '../useAdminT'
import { AudienceProvider, AudienceRail } from './audience'
import { BrandSection } from './BrandSection'
import { SettingsDirtyContext } from './dirtyContext'
import { GeneralSection } from './GeneralSection'
import { LegalSection } from './LegalSection'
import { MailSection } from './MailSection'
import { SECTIONS, type SectionKey } from './model'
import { PortalSection } from './PortalSection'
import type { SearchEntry } from './searchIndex'
import { SecuritySection } from './SecuritySection'
import { SettingsNav } from './SettingsNav'
import { SettingsSearch } from './SettingsSearch'
import { SsoSection } from './SsoSection'
import { SubscriptionSection } from './SubscriptionSection'
import { SUB_PARAM } from './SubTabs'
import { PLACES, firstSub, placeName, sectionOfPlace } from './tree'

type SectionComponent = ComponentType<{ active: boolean }>
const COMPONENTS: Record<SectionKey, SectionComponent> = {
  general: GeneralSection,
  security: SecuritySection,
  brand: BrandSection,
  subscription: SubscriptionSection,
  portal: PortalSection,
  mail: MailSection,
  sso: SsoSection,
  legal: LegalSection,
}

export const sectionDomId = (s: SectionKey) => `settings-section-${s}`

/**
 * Takes focus to a control that may not be on the page yet (its section mounts on first visit, its data may still be
 * loading). Looks for it for a moment; a control that is not there (it belongs to a switch that is off) falls back to
 * the switch named in `via`, then to the section itself, so the jump always lands somewhere useful.
 */
function useJump(entry: SearchEntry | null, done: () => void) {
  useEffect(() => {
    if (!entry) return
    let tries = 0
    const timer = setInterval(() => {
      tries++
      const own = document.getElementById(entry.id)
      const target = own ?? (tries >= 8 ? (entry.via && document.getElementById(entry.via)) || document.getElementById(sectionDomId(entry.section)) : null)
      if (!target && tries < 40) return
      clearInterval(timer)
      target?.scrollIntoView?.({ block: 'center' })
      // A switch keeps its id on a hidden input beside the control that takes focus: look for the control inside the field.
      const scope = target?.closest('[data-slot="field"]') ?? target
      const control = scope?.querySelector<HTMLElement>('[role="switch"], input:not([type="hidden"]):not([aria-hidden="true"]), select, textarea, .cm-content, [role="textbox"], button')
      if (control) control.focus()
      done()
    }, 50)
    return () => clearInterval(timer)
  }, [entry, done])
}

/**
 * The settings page: one section at a time, each split into sub-tabs that each have their own Save. A section is built
 * the first time it is shown and then kept (hidden while another is open), and so is each of its sub-tabs (SubTabs), so
 * what is typed survives a look at another one and a switch never asks anything. Leaving the PAGE asks once, and names
 * the sub-tabs that have unsaved changes.
 */
export function SettingsPage() {
  return <AudienceProvider><SettingsPageInner /></AudienceProvider>
}

function SettingsPageInner() {
  const t = useAdminT()
  const [tab] = useTabParam<SectionKey>('tab', 'general', SECTIONS)
  const [, setParams] = useSearchParams()
  const [visited, setVisited] = useState<ReadonlySet<SectionKey>>(new Set([tab]))
  const shown = visited.has(tab) ? visited : new Set(visited).add(tab)
  // Another section starts at its first sub-tab: `sub` belongs to the section it was set in. One update, because two
  // successive ones would each start from the same address and the second would undo the first.
  const select = (s: SectionKey) => {
    if (s === tab) return // the section already open keeps its sub-tab
    setVisited(v => (v.has(s) ? v : new Set(v).add(s)))
    setParams(prev => {
      const out = new URLSearchParams(prev)
      if (s === 'general') out.delete('tab'); else out.set('tab', s)
      out.delete(SUB_PARAM)
      return out
    }, { replace: true })
  }

  const { dirty: dirtyPlaces, report } = useDirtyTabs(PLACES)
  const dirtySections = SECTIONS.filter(s => dirtyPlaces.some(p => sectionOfPlace(p) === s))
  const names = dirtyPlaces.map(p => placeName(p, t)).join(t('settings.section_separator'))
  useLeaveGuard(dirtyPlaces.length > 0, { ...discardChangesConfirm(t), destructive: true, initialFocus: 'cancel', message: t('settings.dirty_sections', { sections: names }) },
    (next, current) => next.pathname !== current.pathname)
  const dirtyContext = useMemo(() => ({ dirty: dirtyPlaces, report }), [dirtyPlaces, report])

  const [jump, setJump] = useState<SearchEntry | null>(null)
  const done = useCallback(() => setJump(null), [])
  useJump(jump, done)
  const pick = (entry: SearchEntry) => {
    setVisited(v => (v.has(entry.section) ? v : new Set(v).add(entry.section)))
    setParams(prev => {
      const out = new URLSearchParams(prev)
      if (entry.section === 'general') out.delete('tab'); else out.set('tab', entry.section)
      if (entry.sub !== firstSub(entry.section)) out.set(SUB_PARAM, entry.sub); else out.delete(SUB_PARAM)
      return out
    }, { replace: true })
    setJump(entry)
  }
  return (
    <div className="p-4 sm:p-6">
      <PageHeader title={t('settings.title')} />
      <div className="grid gap-6 md:grid-cols-[15rem_minmax(0,1fr)]">
        <aside className="grid content-start gap-4 md:sticky md:top-4 md:self-start">
          <SettingsSearch onPick={pick} />
          {/* Who before what: the audience, then the sections. Both fit in an 800px-tall window (the audience list scrolls inside 14rem). */}
          <AudienceRail />
          <SettingsNav active={tab} onSelect={select} dirty={dirtySections} />
        </aside>
        <SettingsDirtyContext.Provider value={dirtyContext}><div className="min-w-0 max-w-3xl">
          {SECTIONS.map(s => {
            const Section = COMPONENTS[s]
            return shown.has(s) && (
              <div key={s} id={sectionDomId(s)} hidden={tab !== s}>
                <Section active={tab === s} />
              </div>
            )
          })}
        </div></SettingsDirtyContext.Provider>
      </div>
    </div>
  )
}
