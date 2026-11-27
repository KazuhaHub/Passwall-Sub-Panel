import { useId, type ReactNode } from 'react'

import type { RuntimeKnobValues } from '@/api/settings'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { IconChevronRight } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { useRiskT } from '../useRiskT'
import PolicyField from './PolicyField'
import type { RiskPolicyKey, RiskPolicySettings } from './policyKeys'
import type { PolicyCardSpec, PolicyFieldSpec } from './policyLayout'
import { applyPreset, detectPreset } from './presets'
import PresetSelect from './PresetSelect'

const P = 'admin:risk_center.policy.'

/**
 * One detector's card: its switch, its preset, its key thresholds, what the server will judge with, and
 * the rest behind 高级. Laid out from the card's spec (policyLayout), so the card itself decides nothing
 * about which key goes where.
 *
 * A card switched off stays editable, dimmed: an admin may set the thresholds before turning the detector
 * on, and a value that is kept while off is not one the page should hide.
 */
export default function DetectorCard({
  card, draft, defaults, effective, on, onToggle, onPatch, errors, advancedOpen, onAdvancedChange,
  lines, disposalLines, descValues,
}: {
  card: PolicyCardSpec
  /** The draft as the fields show it. */
  draft: RiskPolicySettings
  defaults: Record<string, number>
  effective: RuntimeKnobValues
  /** The card's switch, when it has one. */
  on?: boolean
  onToggle?: (on: boolean) => void
  onPatch: (patch: Partial<RiskPolicySettings>) => void
  /** Errors the server named, by key. */
  errors: Partial<Record<RiskPolicyKey, string>>
  advancedOpen: boolean
  onAdvancedChange: (open: boolean) => void
  /** Lines under the key thresholds: what is in effect, what it relies on. */
  lines?: ReactNode
  /** Lines under the 处置 section. */
  disposalLines?: ReactNode
  /** Values the card's description names (设备数: the shared days). */
  descValues?: Record<string, unknown>
}) {
  const { t } = useRiskT()
  const titleId = useId()
  const advancedId = useId()
  const dimmed = on === false

  const field = (spec: PolicyFieldSpec) => {
    const wide = spec.kind === 'text' || spec.kind === 'switch' || spec.kind === 'inverted'
    return (
      // data-policy-key: where the page scrolls a field the server refused.
      <div key={spec.key} data-policy-key={spec.key} className={wide ? 'sm:col-span-2' : undefined}>
        <PolicyField spec={spec} value={draft[spec.key]} defaults={defaults}
          effective={spec.effective ? effective[spec.key as keyof RuntimeKnobValues] : undefined}
          error={errors[spec.key]}
          onChange={v => onPatch({ [spec.key]: v } as Partial<RiskPolicySettings>)} />
      </div>
    )
  }
  const grid = (specs: PolicyFieldSpec[]) => <div className="grid gap-4 sm:grid-cols-2">{specs.map(field)}</div>

  return (
    <Card role="region" aria-labelledby={titleId}>
      <CardHeader>
        <CardTitle id={titleId} role="heading" aria-level={2}>{t(card.title)}</CardTitle>
        <CardDescription>{t(card.desc, descValues)}</CardDescription>
        {onToggle && (
          <CardAction className="flex items-center gap-2">
            <span className="min-w-[3em] text-right text-sm text-muted-foreground">{t(`${P}${on === true ? 'enabled' : 'disabled'}`)}</span>
            <Switch checked={on === true} onCheckedChange={onToggle} aria-labelledby={titleId} />
          </CardAction>
        )}
      </CardHeader>
      <CardContent className={cn('flex flex-col gap-4', dimmed && 'opacity-60')}>
        {card.preset && (
          <PresetSelect value={detectPreset(card.preset, draft, defaults)}
            onApply={preset => onPatch(applyPreset(card.preset!, preset, defaults))} />
        )}
        {card.fields.length > 0 && grid(card.fields)}
        {lines}

        {card.disposal && (
          <section className="flex flex-col gap-3 border-t pt-4">
            <h3 className="text-sm font-medium">{t(`${P}disposal`)}</h3>
            {grid(card.disposal)}
            {disposalLines}
          </section>
        )}

        {card.advanced && (
          <section className="border-t pt-3">
            <button type="button" aria-expanded={advancedOpen} aria-controls={advancedId}
              onClick={() => onAdvancedChange(!advancedOpen)}
              className="-ml-1 flex items-center gap-1.5 rounded-md px-1 text-sm font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
              <IconChevronRight aria-hidden className={cn('size-4 text-muted-foreground transition-transform', advancedOpen && 'rotate-90')} />
              {t(`${P}advanced`)}
            </button>
            {/* Hidden, not unmounted: a value typed in it survives a collapse and the fields still exist. */}
            <div id={advancedId} hidden={!advancedOpen} className="mt-3">{grid(card.advanced)}</div>
          </section>
        )}
      </CardContent>
    </Card>
  )
}
