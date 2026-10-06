import { Alert, Box, Button, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { DestinationListKind, DestinationParseReport } from '@/api/accessControl'
const P = 'admin:access_control.parse_report.'
export default function ParseReport({ report, kind, onLine }: { report: DestinationParseReport | null; kind: DestinationListKind; onLine?: (line: number) => void }) {
  const { t } = useTranslation('admin')
  if (!report) return <Typography color="text.secondary">{t(`${P}none`)}</Typography>
  const remaining = Math.max(0, report.ignored + report.rewritten - report.samples.length)
  return <Box sx={{ p: 2, borderRadius: 2, bgcolor: 'md.surfaceContainer', minWidth: 0 }}>
    <Typography variant="subtitle2">{t(`${P}summary`, { accepted: report.accepted, rewritten: report.rewritten, ignored: report.ignored })}</Typography>
    {!!report.ignored_broad && <Alert severity="warning" sx={{ my: 1 }}>{t(`${P}broad_removed`, { count: report.ignored_broad })}</Alert>}
    <Stack spacing={1} sx={{ mt: 1 }}>{report.samples.map((sample, index) => <Box key={`${sample.line}-${index}`} sx={{ minWidth: 0 }}>
      <Stack direction="row" spacing={1} sx={{ alignItems: 'baseline', flexWrap: 'wrap' }}>
        {onLine && kind === 'custom' ? <Button size="small" sx={{ p: 0, minWidth: 0 }} onClick={() => onLine(sample.line)}>{t(`${P}line`, { line: sample.line })}</Button> : <Typography variant="caption">{t(`${P}${kind === 'geosite' ? 'source_entry' : 'line'}`, { line: sample.line })}</Typography>}
        <Typography variant="caption" color={sample.reason === 'broad_entry' ? 'warning.main' : 'text.secondary'}>{t(`${P}reason.${sample.reason}`, { defaultValue: sample.reason })}</Typography>
      </Stack>
      <Typography variant="body2" sx={{ fontFamily: 'monospace', overflowWrap: 'anywhere' }}>{sample.text}</Typography>
      {sample.normalized && <Typography variant="body2" sx={{ fontFamily: 'monospace', overflowWrap: 'anywhere' }}>→ <Box component="span">{sample.normalized}</Box></Typography>}
    </Box>)}</Stack>
    {!!remaining && <Typography variant="caption">{t(`${P}remaining`, { count: remaining })}</Typography>}
  </Box>
}
