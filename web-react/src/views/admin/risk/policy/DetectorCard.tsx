import { useId, type ReactNode } from 'react'
import {
  Accordion, AccordionDetails, AccordionSummary, Box, Divider, FormControlLabel, Paper, Switch, Typography, useTheme,
} from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'

import type { RuntimeKnobValues } from '@/api/settings'
import PolicyField from '@/components/PolicyField'
import type { RiskPolicyKey, RiskPolicySettings } from './policyKeys'
import type { PolicyCardSpec, PolicyFieldSpec } from './policyLayout'
import { applyPreset, detectPreset } from './presets'
import PresetSelect from './PresetSelect'

const P = 'admin:risk_center.policy.'

/**
 * One detector's card: its switch, its preset, its key thresholds, what the
 * server will judge with, and the rest behind 高级. Laid out from the card's
 * spec (policyLayout), so the card itself decides nothing about which key
 * goes where.
 *
 * A card switched off stays editable, dimmed: an admin may set the
 * thresholds before turning the detector on, and a value that is kept while
 * off is not one the page should hide.
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
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const titleId = useId()
  const dimmed = on === false

  const field = (spec: PolicyFieldSpec) => {
    const wide = spec.kind === 'text' || spec.kind === 'switch' || spec.kind === 'inverted'
    return (
      // data-policy-key: where the page scrolls a field the server refused.
      <Box key={spec.key} data-policy-key={spec.key} sx={wide ? { gridColumn: '1 / -1' } : undefined}>
        <PolicyField spec={spec} value={draft[spec.key]} defaults={defaults}
          effective={spec.effective ? effective[spec.key as keyof RuntimeKnobValues] : undefined}
          error={errors[spec.key]}
          onChange={v => onPatch({ [spec.key]: v } as Partial<RiskPolicySettings>)} />
      </Box>
    )
  }
  const grid = (specs: PolicyFieldSpec[]) => (
    <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', sm: '1fr 1fr' }, gap: 2 }}>
      {specs.map(field)}
    </Box>
  )

  return (
    <Paper component="section" aria-labelledby={titleId} variant="outlined"
      sx={{ p: { xs: 2, sm: 2.5 }, borderRadius: 3, bgcolor: md.surfaceContainerLow }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 2, flexWrap: 'wrap' }}>
        <Typography id={titleId} component="h2" sx={{ fontSize: 16, fontWeight: 600 }}>{t(card.title)}</Typography>
        {onToggle && (
          <FormControlLabel label={t(`${P}${on === true ? 'enabled' : 'disabled'}`)} labelPlacement="start"
            sx={{ m: 0, gap: 0.5, '& .MuiFormControlLabel-label': { minWidth: '3em', textAlign: 'right', color: md.onSurfaceVariant } }}
            control={<Switch checked={on === true} onChange={(_, c) => onToggle(c)} />} />
        )}
      </Box>
      <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mt: 0.5 }}>
        {t(card.desc, descValues)}
      </Typography>

      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, mt: 2, opacity: dimmed ? 0.6 : 1 }}>
        {card.preset && (
          <PresetSelect value={detectPreset(card.preset, draft, defaults)}
            onApply={preset => onPatch(applyPreset(card.preset!, preset, defaults))} />
        )}
        {card.fields.length > 0 && grid(card.fields)}
        {lines}

        {card.disposal && (
          <>
            <Divider />
            <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600 }}>{t(`${P}disposal`)}</Typography>
            {grid(card.disposal)}
            {disposalLines}
          </>
        )}

        {card.advanced && (
          <Accordion disableGutters elevation={0} expanded={advancedOpen}
            onChange={(_, open) => onAdvancedChange(open)}
            sx={{ bgcolor: 'transparent', '&:before': { display: 'none' } }}>
            <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={{ px: 0 }}>
              <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{t(`${P}advanced`)}</Typography>
            </AccordionSummary>
            <AccordionDetails sx={{ px: 0 }}>{grid(card.advanced)}</AccordionDetails>
          </Accordion>
        )}
      </Box>
    </Paper>
  )
}
