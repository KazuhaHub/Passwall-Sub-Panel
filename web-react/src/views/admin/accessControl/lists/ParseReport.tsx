import { Box, Link, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { DestParseReport } from '@/api/accessControl'
import { amber } from '@/components/ToneBadge'
import { reasonText, warningText } from './listStatus'

/** At most this many ignored lines are listed; the rest are a count. */
const MAX_LISTED = 20

/**
 * WHAT THE LIST WAS UNDERSTOOD AS: how many entries were kept, rewritten and
 * left out, and why, line by line. A line number jumps the editor to it.
 * Warnings are amber and never block the save.
 */
export default function ParseReport({ report, onJump }: { report: DestParseReport; onJump?: (line: number) => void }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const a = amber(useTheme())
  const P = 'admin:access_control.lists.report.'
  const samples = report.samples ?? []
  const warnings = report.warnings ?? []
  const listed = samples.slice(0, MAX_LISTED)
  const more = Math.max(0, report.ignored + report.rewritten - listed.length)
  const mono = { fontFamily: 'ui-monospace, monospace', fontSize: 13, overflowWrap: 'anywhere' as const }
  const lineLink = (line: number) => onJump
    ? (
      <Link component="button" type="button" underline="hover" aria-label={t(`${P}jump`, { line })}
        onClick={() => onJump(line)} sx={{ font: 'inherit', fontVariantNumeric: 'tabular-nums' }}>
        {t('admin:access_control.lists.dialog.line_n', { line })}
      </Link>
    )
    : t('admin:access_control.lists.dialog.line_n', { line })
  return (
    <Box data-testid="parse-report" sx={{ bgcolor: md.surfaceContainer, borderRadius: 2, p: 1.5 }}>
      <Typography sx={{ fontSize: 13, fontWeight: 600 }}>
        {t(`${P}summary`, { accepted: report.accepted, rewritten: report.rewritten, ignored: report.ignored })}
        {report.duplicates > 0 ? ` · ${t(`${P}duplicates`, { count: report.duplicates })}` : ''}
      </Typography>
      {listed.map(s => (
        <Box key={`${s.line}:${s.reason}`} sx={{ mt: 0.75, display: 'grid', gridTemplateColumns: { xs: 'minmax(0,1fr)', sm: '5.5rem minmax(0,1fr) minmax(0,1.2fr)' }, columnGap: 1 }}>
          <Typography component="span" sx={{ fontSize: 13 }}>{lineLink(s.line)}</Typography>
          <Typography component="span" sx={mono}>{s.text}</Typography>
          <Typography component="span" sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{reasonText(t, s.reason)}</Typography>
        </Box>
      ))}
      {more > 0 && <Typography sx={{ mt: 0.75, fontSize: 13, color: md.onSurfaceVariant }}>{t(`${P}more`, { count: more })}</Typography>}
      {warnings.length > 0 && (
        <Box sx={{ mt: 1, p: 1, borderRadius: 1.5, bgcolor: a.bg }}>
          <Typography sx={{ fontSize: 12.5, fontWeight: 600 }}>{t(`${P}warnings`)}</Typography>
          {warnings.map(w => (
            <Box key={`${w.line}:${w.code}`} sx={{ mt: 0.5, display: 'grid', gridTemplateColumns: { xs: 'minmax(0,1fr)', sm: '5.5rem minmax(0,1fr) minmax(0,1.2fr)' }, columnGap: 1 }}>
              <Typography component="span" sx={{ fontSize: 13 }}>{lineLink(w.line)}</Typography>
              <Typography component="span" sx={mono}>{w.text}</Typography>
              <Typography component="span" sx={{ fontSize: 13 }}>{warningText(t, w.code)}</Typography>
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}
