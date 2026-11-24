import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { IconAutoMode, IconCheck, IconDark, IconDensityComfortable, IconDensityCompact, IconLight, IconPalette, type IconType } from '@/lib/icons'
import { IconButton } from '@/components/common/IconButton'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'
import { COLOR_PRESETS } from '@/theme/presets'
import { selectEffectiveColor, useAppearanceStore, type Density, type ThemeMode } from '@/stores/appearance'

// The same rule the store applies; a colour the store would refuse is flagged here
// before the user presses apply.
const isHex = (v: string) => /^#[0-9a-fA-F]{6}$/.test(v)

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      {children}
    </section>
  )
}

// A group of mutually exclusive choices drawn as buttons. role=radiogroup with
// aria-checked radios is what a screen reader hears as "one of three"; the checked
// one is also marked by a ring, not by colour alone.
function Choice<T extends string>({ label, value, onChange, options, columns }: {
  label: string
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string; icon: IconType }[]
  columns: string
}) {
  return (
    <div role="radiogroup" aria-label={label} className={cn('grid gap-1.5', columns)}>
      {options.map(o => {
        const Icon = o.icon
        const checked = o.value === value
        return (
          <Button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={checked}
            variant={checked ? 'secondary' : 'outline'}
            size="sm"
            className={cn(checked && 'ring-2 ring-brand')}
            onClick={() => onChange(o.value)}
          >
            <Icon />
            {o.label}
          </Button>
        )
      })}
    </div>
  )
}

/**
 * ONE MENU for how the app looks: light, dark or follow the system; one of the six brand
 * presets or a custom colour; compact or comfortable density. It replaces the old
 * appearance menu and the separate density toggle, and reads and writes the same
 * appearance store, which stays the single source of truth for all of it (the store's
 * mode, colour and density are mirrored onto <html> by useHtmlAppearance).
 */
export function ThemeMenu() {
  const { t } = useTranslation('appearance')
  const mode = useAppearanceStore(s => s.mode)
  const density = useAppearanceStore(s => s.density)
  const systemColor = useAppearanceStore(s => s.systemColor)
  const userColor = useAppearanceStore(s => s.userColor)
  const setMode = useAppearanceStore(s => s.setMode)
  const setDensity = useAppearanceStore(s => s.setDensity)
  const setUserColor = useAppearanceStore(s => s.setUserColor)
  const effective = useAppearanceStore(selectEffectiveColor).toUpperCase()

  const [draft, setDraft] = useState(effective)
  const valid = isHex(draft)

  return (
    <Popover onOpenChange={open => { if (open) setDraft(effective) }}>
      <PopoverTrigger render={<IconButton label={t('title')} />}>
        <IconPalette />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 gap-4 p-3">
        <Section title={t('mode_section')}>
          <Choice<ThemeMode>
            label={t('mode_section')}
            columns="grid-cols-3"
            value={mode}
            onChange={setMode}
            options={[
              { value: 'auto', label: t('mode.auto'), icon: IconAutoMode },
              { value: 'light', label: t('mode.light'), icon: IconLight },
              { value: 'dark', label: t('mode.dark'), icon: IconDark },
            ]}
          />
        </Section>

        <Section title={t('preset_section')}>
          <div role="radiogroup" aria-label={t('preset_section')} className="grid grid-cols-6 gap-2">
            {COLOR_PRESETS.map(p => {
              const checked = effective === p.hex.toUpperCase()
              return (
                <button
                  key={p.id}
                  type="button"
                  role="radio"
                  aria-checked={checked}
                  aria-label={t(p.labelKey)}
                  title={t(p.labelKey)}
                  onClick={() => setUserColor(p.hex)}
                  style={{ backgroundColor: p.hex }}
                  className={cn(
                    'flex size-8 items-center justify-center rounded-full text-white outline-none ring-offset-2 ring-offset-popover transition-transform hover:scale-105 focus-visible:ring-3 focus-visible:ring-ring/50',
                    checked && 'ring-2 ring-foreground',
                  )}
                >
                  {checked && <IconCheck className="size-4" />}
                </button>
              )
            })}
          </div>
        </Section>

        <Section title={t('custom_section')}>
          <div className="flex items-center gap-2">
            <input
              type="color"
              aria-label={t('color_input')}
              value={valid ? draft.toLowerCase() : '#6750a4'}
              onChange={e => setDraft(e.target.value.toUpperCase())}
              className="h-(--control-h) w-10 shrink-0 cursor-pointer rounded-md border border-input bg-transparent p-0.5"
            />
            <Input
              aria-label={t('color_input')}
              aria-invalid={!valid}
              value={draft}
              onChange={e => setDraft(e.target.value)}
              placeholder="#6750A4"
              className="font-mono"
            />
            <Button
              type="button"
              size="icon"
              aria-label={t('apply')}
              disabled={!valid || draft.toUpperCase() === effective}
              onClick={() => setUserColor(draft)}
            >
              <IconCheck />
            </Button>
          </div>
          {!valid && <p className="text-xs text-destructive">{t('custom_hint')}</p>}
          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>{userColor ? t('user_override') : t('system_default')}: {effective}</span>
            {userColor && (
              <Button type="button" variant="outline" size="xs" onClick={() => { setUserColor(null); setDraft(systemColor.toUpperCase()) }}>
                {t('reset')}
              </Button>
            )}
          </div>
        </Section>

        <Section title={t('density.section')}>
          <Choice<Density>
            label={t('density.section')}
            columns="grid-cols-2"
            value={density}
            onChange={setDensity}
            options={[
              { value: 'compact', label: t('density.compact'), icon: IconDensityCompact },
              { value: 'comfortable', label: t('density.comfortable'), icon: IconDensityComfortable },
            ]}
          />
        </Section>
      </PopoverContent>
    </Popover>
  )
}
