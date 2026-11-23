import { SegmentedControl } from '@/components/form/SegmentedControl'
import { useRiskT } from '../useRiskT'
import { PRESET_NAMES, type PresetName } from './presets'

const P = 'admin:risk_center.policy.'

/**
 * A card's preset: 宽松 / 标准 / 严格, plus 自定义. The lit one is DETECTED from the draft (detectPreset),
 * never stored, so a hand-typed value lights 自定义 at once. 自定义 cannot be chosen — there is nothing to
 * apply — and is lit only when no preset matches.
 */
export default function PresetSelect({ value, onApply }: {
  value: PresetName
  onApply: (preset: Exclude<PresetName, 'custom'>) => void
}) {
  const { t } = useRiskT()
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-[13px] text-muted-foreground">{t(`${P}preset`)}</span>
        <SegmentedControl<PresetName> value={value} aria-label={t(`${P}preset`)}
          options={[
            ...PRESET_NAMES.map(name => ({ value: name as PresetName, label: t(`${P}preset_${name}`) })),
            { value: 'custom' as PresetName, label: t(`${P}preset_custom`), disabled: true },
          ]}
          onChange={v => { if (v !== 'custom') onApply(v) }} />
      </div>
      <p className="text-xs text-muted-foreground">{t(`${P}preset_hint`)}</p>
    </div>
  )
}
