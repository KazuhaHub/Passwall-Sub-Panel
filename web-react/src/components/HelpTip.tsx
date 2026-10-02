import { useState } from 'react'
import { Box, IconButton, Popover, Typography, useTheme } from '@mui/material'
import HelpOutlineIcon from '@mui/icons-material/HelpOutlined'
import { useTranslation } from 'react-i18next'

/**
 * A page's or a tab's explanation behind a "?" at its top right. The risk
 * center's tabs used to open on a paragraph each, read once and then scrolled
 * past on every visit; the text is still one click away, and the list is what
 * the admin sees first. The diagnostics page uses it the same way, for its own
 * long explanation and for each area card's purpose.
 *
 * `textKey` and `labelKey` are whole keys, namespace included, so the key
 * check (i18n/riskKeys.test.ts) sees every help text a page names. The label
 * defaults to the risk center's, which is what its pages always read; a page
 * with its own wording for the button passes its own key.
 */
export default function HelpTip({
  textKey,
  labelKey = 'admin:risk_center.help.label',
}: {
  textKey: string
  labelKey?: string
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  return (
    <Box sx={{ display: 'flex', flex: '0 0 auto', alignSelf: 'stretch', alignItems: 'center' }}>
      <IconButton size="small" aria-label={t(labelKey)} aria-haspopup="dialog"
        onClick={e => setAnchor(e.currentTarget)}>
        <HelpOutlineIcon fontSize="small" />
      </IconButton>
      <Popover open={anchor !== null} anchorEl={anchor} onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        slotProps={{ paper: { sx: { maxWidth: 420, p: 2 } } }}>
        <Typography sx={{ fontSize: 13, lineHeight: 1.6, color: md.onSurface }}>{t(textKey)}</Typography>
      </Popover>
    </Box>
  )
}
