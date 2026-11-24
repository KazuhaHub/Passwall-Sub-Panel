import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'
import { cn } from '@/lib/utils'
import { NAV_ACTIVE, NAV_ITEM } from './audience'
import { useAdminT } from '../useAdminT'
import { SECTIONS, SECTION_LABEL, type SectionKey } from './model'

/**
 * The sections: a list down the side on a wide screen, a select on a phone (eight names do not fit in a row, and a
 * scrolling strip hides the ones off screen). A section with unsaved changes is marked in words, not only by colour.
 */
export function SettingsNav({ active, onSelect, dirty }: { active: SectionKey; onSelect: (s: SectionKey) => void; dirty: readonly SectionKey[] }) {
  const t = useAdminT()
  const mark = (s: SectionKey) => (dirty.includes(s) ? ` (${t('settings.unsaved_short')})` : '')
  return (
    <>
      <nav aria-label={t('settings.nav_label')} className="hidden md:block">
        <ul className="grid gap-0.5">
          {SECTIONS.map(s => (
            <li key={s}>
              <button type="button" aria-current={active === s ? 'page' : undefined} onClick={() => onSelect(s)}
                className={cn(NAV_ITEM, active === s && NAV_ACTIVE)}>
                <span className="min-w-0 truncate">{t(SECTION_LABEL[s])}</span>
                {dirty.includes(s) && (
                  <>
                    <span aria-hidden className="size-2 shrink-0 rounded-full bg-warning" />
                    <span className="sr-only">{t('settings.unsaved_short')}</span>
                  </>
                )}
              </button>
            </li>
          ))}
        </ul>
      </nav>
      <div className="md:hidden">
        <Field label={t('settings.section_label')}>
          <NativeSelect value={active} onChange={e => onSelect(e.target.value as SectionKey)}>
            {SECTIONS.map(s => <option key={s} value={s}>{t(SECTION_LABEL[s]) + mark(s)}</option>)}
          </NativeSelect>
        </Field>
      </div>
    </>
  )
}
