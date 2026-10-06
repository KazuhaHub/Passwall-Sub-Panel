import { Box, Stack, Typography } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { DestinationTestResult } from '@/api/accessControl'
import { pipelineSteps } from '@/utils/accessControl'
import { PipelineRail, PipelineStep } from './PipelineRail'
export default function EvalTrace({ result }: { result: DestinationTestResult }) {
  const { t } = useTranslation(['admin'])
  return <PipelineRail>{pipelineSteps(result.steps.some(row => row.step === 'group')).map((step, index) => {
    const rows = result.steps.filter(row => row.step === step)
    return <PipelineStep key={step} index={index}><Typography component="h3" variant="subtitle1">{t(`admin:access_control.test.step_${step}`)}</Typography><Stack spacing={1}>{rows.length ? rows.map((row, i) => <Box key={i} data-result={row.result}>
      {row.name && <Typography variant="body2" sx={{ overflowWrap: 'anywhere' }}>{row.name}</Typography>}
      <Typography variant="body2" color={row.result === 'shadowed' ? 'warning.main' : 'text.secondary'}>{t(`admin:access_control.test.result_${row.result === 'n/a' ? 'na' : row.result}`)}</Typography>
      {row.entry && <Typography variant="caption" sx={{ overflowWrap: 'anywhere', fontFamily: 'monospace' }}>{row.entry}</Typography>}
    </Box>) : <Typography variant="body2" color="text.secondary">{t('admin:access_control.test.no_step_rules')}</Typography>}</Stack></PipelineStep>
  })}</PipelineRail>
}
