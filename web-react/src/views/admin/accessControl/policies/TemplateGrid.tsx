import { useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, ToggleButton, ToggleButtonGroup, Typography, useTheme,
} from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestCatalog, DestState, DestTemplate, DestUnavailableTemplate } from '@/api/accessControl'
import { AsyncButton } from '@/components/AsyncButton'
import ToneBadge from '@/components/ToneBadge'
import { accessTone } from '@/utils/accessControl'
import { pickLocalized } from './templates'

const T = 'admin:access_control.templates.'

/**
 * The catalog as cards (S4): what a rule group blocks, in the server's own
 * words for the UI language. A group already added says so quietly and offers
 * "add another". The groups the catalog lists as unavailable are honest
 * cards: they say why there is no generic category and send the admin to
 * write a list of their own, instead of passing off a one-country category.
 */
export default function TemplateGrid({ catalog, onUse, onGoToLists }: {
  catalog: DestCatalog
  onUse: (tpl: DestTemplate) => void
  onGoToLists: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const theme = useTheme()
  const md = theme.palette.md
  const card = { p: 1.5, borderRadius: 3, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}`, display: 'flex', flexDirection: 'column', gap: 0.75, minWidth: 0 } as const
  return (
    <Box sx={{ display: 'grid', gap: 1.5, gridTemplateColumns: { xs: 'minmax(0, 1fr)', sm: 'repeat(2, minmax(0, 1fr))', md: 'repeat(3, minmax(0, 1fr))' } }}>
      {catalog.templates.map(tpl => (
        <Box key={tpl.key} sx={card} data-testid="template-card">
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
            <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{pickLocalized(tpl.name, i18n.language)}</Typography>
            {tpl.added && <ToneBadge tone={accessTone(theme, 'none')} label={t(`${T}added`)} />}
          </Box>
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant, flex: 1 }}>{pickLocalized(tpl.description, i18n.language)}</Typography>
          <Box><Button size="small" variant="outlined" onClick={() => onUse(tpl)}>{t(tpl.added ? `${T}add_another` : `${T}use`)}</Button></Box>
        </Box>
      ))}
      {catalog.unavailable.map(u => <UnavailableCard key={u.key} item={u} onGoToLists={onGoToLists} card={card} />)}
    </Box>
  )
}

function UnavailableCard({ item, onGoToLists, card }: { item: DestUnavailableTemplate; onGoToLists: () => void; card: object }) {
  const { t, i18n } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <Box sx={{ ...card, borderStyle: 'dashed' }} data-testid="template-unavailable">
      <Typography sx={{ fontSize: 14, fontWeight: 600 }}>{pickLocalized(item.name, i18n.language)}</Typography>
      <Typography variant="body2" sx={{ color: md.onSurfaceVariant, flex: 1 }}>{pickLocalized(item.reason, i18n.language)}</Typography>
      <Box><Button size="small" variant="outlined" onClick={onGoToLists}>{t(`${T}new_list`)}</Button></Box>
    </Box>
  )
}

/** The groups without a generic category, from the menu's footer. */
export function UnavailableDialog({ open, catalog, onClose, onGoToLists }: {
  open: boolean
  catalog: DestCatalog | undefined
  onClose: () => void
  onGoToLists: () => void
}) {
  const { t } = useTranslation(['admin'])
  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="sm">
      <DialogTitle>{t(`${T}unavailable_title`)}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 1.5 }}>
        {(catalog?.unavailable ?? []).map(u => <UnavailableCard key={u.key} item={u} card={{ display: 'grid', gap: 0.75, p: 1.5 }} onGoToLists={() => { onClose(); onGoToLists() }} />)}
      </DialogContent>
      <DialogActions><Button onClick={onClose}>{t(`${T}close`)}</Button></DialogActions>
    </Dialog>
  )
}

/**
 * Adding a rule group asks for its state: nothing is enabled by default, so
 * the admin says Off or On here (and nothing is chosen until they do).
 */
export function TemplateChooser({ template, onClose, onConfirm, error, onOpenSettings }: {
  template: DestTemplate | null
  onClose: () => void
  onConfirm: (state: DestState) => Promise<void>
  /** A refusal to show in the dialog (community data not downloaded, ...). */
  error: { text: string; needsData: boolean } | null
  onOpenSettings: () => void
}) {
  const { t, i18n } = useTranslation(['admin'])
  const [state, setState] = useState<DestState | null>(null)
  return (
    <Dialog open={template !== null} onClose={onClose} fullWidth maxWidth="sm" slotProps={{ transition: { onExited: () => setState(null) } }}>
      <DialogTitle>{t(`${T}chooser_title`, { name: template ? pickLocalized(template.name, i18n.language) : '' })}</DialogTitle>
      <DialogContent sx={{ display: 'grid', gap: 1.5 }}>
        <Typography variant="body2">{template ? pickLocalized(template.description, i18n.language) : ''}</Typography>
        <Typography variant="body2" sx={{ fontWeight: 600 }}>{t(`${T}chooser_state`)}</Typography>
        <ToggleButtonGroup exclusive fullWidth value={state} onChange={(_, v: DestState | null) => setState(v)} aria-label={t(`${T}chooser_state`)}>
          <ToggleButton value="off">{t('admin:access_control.policies.state.off')}</ToggleButton>
          <ToggleButton value="on">{t('admin:access_control.policies.state.on')}</ToggleButton>
        </ToggleButtonGroup>
        <Typography variant="caption">{t(`${T}chooser_help`)}</Typography>
        {error && (
          <Alert severity="error" action={error.needsData ? <Button color="inherit" size="small" onClick={onOpenSettings}>{t(`${T}update_data`)}</Button> : undefined}>
            {error.text}
          </Alert>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t(`${T}cancel`)}</Button>
        <AsyncButton variant="contained" disabled={state === null} onClick={async () => { if (state) await onConfirm(state) }}>{t(`${T}add`)}</AsyncButton>
      </DialogActions>
    </Dialog>
  )
}
