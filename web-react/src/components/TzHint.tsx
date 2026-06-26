import { Typography } from '@mui/material'
import type { SxProps, Theme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import { useSiteStore } from '@/stores/site'

// TzHint is a one-line caption disclosing that nearby times are rendered in the
// PANEL timezone, naming the viewer's browser timezone too — but ONLY when the
// two differ (it renders nothing when they match, so the common single-tz case
// stays uncluttered). Use it next to panel-tz charts / tables where per-value
// dual rendering (formatDualTz) isn't practical — e.g. a chart's date axis or a
// table column of panel-tz calendar days. Drop-in: reads the panel tz from the
// site store and the browser tz from Intl, no props required.
export default function TzHint({ sx }: { sx?: SxProps<Theme> }) {
  const { t } = useTranslation('common')
  const panelTz = useSiteStore(s => s.timezone)
  let browserTz = ''
  try {
    browserTz = Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    browserTz = ''
  }
  if (!panelTz || !browserTz || panelTz === browserTz) return null
  return (
    <Typography variant="caption" color="text.secondary" sx={sx}>
      {t('tz_hint', {
        panel: panelTz,
        browser: browserTz,
        defaultValue: '时间按面板时区 {{panel}} 显示（你的本地：{{browser}}）',
      })}
    </Typography>
  )
}
