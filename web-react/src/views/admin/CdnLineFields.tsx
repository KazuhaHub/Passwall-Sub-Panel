import { Box, FormControlLabel, Switch, TextField, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { RelayLine } from '@/api/types'
import InfoTip from '@/components/InfoTip'

/** The port a CDN line is made with when none is given. */
export const CDN_DEFAULT_PORT = 443

/** Ports Cloudflare proxies for plain HTTP; a line on one defaults to no TLS to the CDN. */
const CDN_HTTP_PORTS = [80, 8080, 8880, 2052, 2082, 2086, 2095]
export const defaultClientTLS = (port: number) => !CDN_HTTP_PORTS.includes(port)

interface Props {
  line: RelayLine
  onPatch: (patch: Partial<RelayLine>) => void
}

/** The fields of a CDN line (CDN（如 Cloudflare）): domain, optimised address, port, and the SNI and Host the CDN routes by. */
export default function CdnLineFields({ line, onPatch }: Props) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const issues = line.cdn_issues ?? []
  const domainPlaceholder = line.domain?.trim() || 'cdn.example.com'
  return (
    <Box data-testid="cdn-line" sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:nodes.cdn.no_entry')}</Typography>
        <InfoTip text={t('admin:nodes.cdn.tip')} />
      </Box>
      <Box sx={{ display: 'flex', gap: 1 }}>
        <TextField size="small" required label={t('admin:nodes.cdn.domain')} value={line.domain ?? ''} sx={{ flex: 2 }}
          onChange={e => onPatch({ domain: e.target.value })} placeholder="cdn.example.com"
          helperText={t('admin:nodes.cdn.domain_hint')} />
        <TextField size="small" type="number" label={t('admin:nodes.cdn.port')} value={line.port || ''} sx={{ width: 120 }}
          onChange={e => onPatch({ port: Number(e.target.value) || 0 })} placeholder={String(CDN_DEFAULT_PORT)}
          helperText={t('admin:nodes.cdn.port_hint')} />
      </Box>
      <TextField size="small" label={t('admin:nodes.cdn.address')} value={line.address} placeholder={domainPlaceholder}
        onChange={e => onPatch({ address: e.target.value })} helperText={t('admin:nodes.cdn.address_hint')} />
      <Box sx={{ display: 'flex', gap: 1 }}>
        <TextField size="small" label={t('admin:nodes.relay.sni')} value={line.sni ?? ''} sx={{ flex: 1 }} placeholder={domainPlaceholder}
          onChange={e => onPatch({ sni: e.target.value })} helperText={t('admin:nodes.cdn.sni_hint')} />
        <TextField size="small" label={t('admin:nodes.cdn.host')} value={line.host ?? ''} sx={{ flex: 1 }} placeholder={domainPlaceholder}
          onChange={e => onPatch({ host: e.target.value })} helperText={t('admin:nodes.cdn.host_hint')} />
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <FormControlLabel sx={{ m: 0, '& .MuiFormControlLabel-label': { ml: 0.75, fontSize: 13 } }} label={t('admin:nodes.cdn.client_tls')}
          control={<Switch size="small" checked={line.client_tls ?? defaultClientTLS(line.port || CDN_DEFAULT_PORT)}
            onChange={(_, checked) => onPatch({ client_tls: checked })} />} />
        <InfoTip text={t('admin:nodes.cdn.client_tls_tip')} />
      </Box>
      {issues.length > 0 && (
        <Box data-testid="cdn-issues" component="ul" sx={{ m: 0, pl: 2.5, display: 'flex', flexDirection: 'column', gap: 0.25 }}>
          {issues.map(issue => (
            <Typography key={issue.code} component="li" data-code={issue.code}
              sx={{ fontSize: 12, color: issue.warn ? md.error : md.onSurfaceVariant }}>
              {issue.code === 'landing_not_capable'
                ? t(`admin:nodes.cdn.reason.${issue.params?.reason_code}`, { ...issue.params, defaultValue: issue.params?.reason })
                : t(`admin:nodes.cdn.issue.${issue.code}`, { ...issue.params })}
            </Typography>
          ))}
        </Box>
      )}
    </Box>
  )
}
