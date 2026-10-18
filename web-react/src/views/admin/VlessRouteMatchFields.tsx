import { Accordion, AccordionDetails, AccordionSummary, Box, Checkbox, ListItemText, MenuItem, TextField, Typography } from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'
import type { VlessRouteLine } from '@/api/vlessRoute'

const protocols = ['http', 'tls', 'quic', 'bittorrent']

export default function VlessRouteMatchFields({ line, update, disabled }: {
  line: VlessRouteLine; update: (patch: Partial<VlessRouteLine>) => void; disabled: boolean
}) {
  const { t } = useTranslation('admin')
  const key = 'nodes.vless_route.match.'
  return <Accordion disableGutters elevation={0} slotProps={{ transition: { unmountOnExit: true } }} sx={{ bgcolor: 'transparent', '&:before': { display: 'none' }, borderTop: 1, borderColor: 'divider', '& .MuiAccordionSummary-root': { px: 0, minHeight: 40 }, '& .MuiAccordionDetails-root': { px: 0, pb: 0 } }}>
    <AccordionSummary expandIcon={<ExpandMoreIcon />}><Typography sx={{ fontSize: 13 }}>{t(`${key}title`)}</Typography></AccordionSummary>
    <AccordionDetails>
      <Box role="group" aria-label={t(`${key}title`)} sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        <TextField select fullWidth size="small" label={t(`${key}network`)} value={line.network || ''} disabled={disabled} slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }} onChange={event => update({ network: event.target.value as VlessRouteLine['network'] })}>
          <MenuItem value="">{t(`${key}any`)}</MenuItem><MenuItem value="tcp">TCP</MenuItem><MenuItem value="udp">UDP</MenuItem>
        </TextField>
        <TextField select fullWidth size="small" label={t(`${key}protocols`)} value={line.protocols || []} disabled={disabled} slotProps={{ select: { multiple: true, renderValue: value => (value as string[]).join(', ') } }} onChange={event => update({ protocols: typeof event.target.value === 'string' ? event.target.value.split(',') : event.target.value as string[] })}>
          {protocols.map(protocol => <MenuItem key={protocol} value={protocol}><Checkbox size="small" checked={(line.protocols || []).includes(protocol)} /><ListItemText primary={protocol} /></MenuItem>)}
        </TextField>
        <TextField fullWidth multiline minRows={2} maxRows={6} size="small" label={t(`${key}ip`)} value={(line.ip || []).join('\n')} disabled={disabled} onChange={event => update({ ip: event.target.value.split('\n') })} placeholder={'192.0.2.0/24\ngeoip:private'} />
        <TextField fullWidth multiline minRows={2} maxRows={6} size="small" label={t(`${key}domain`)} value={(line.domain || []).join('\n')} disabled={disabled} onChange={event => update({ domain: event.target.value.split('\n') })} placeholder={'domain:example.com\ngeosite:cn'} />
        <TextField fullWidth size="small" label={t(`${key}port`)} value={line.port || ''} disabled={disabled} onChange={event => update({ port: event.target.value })} placeholder="53,443,1000-2000" />
      </Box>
    </AccordionDetails>
  </Accordion>
}
