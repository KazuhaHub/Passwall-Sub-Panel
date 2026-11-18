import { Box, Button, MenuItem, TextField } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { TransitLink, TransitLinkProtocol } from '@/api/nodes'
import { randomRealitySite } from '@/utils/xrayReality'

export default function TransitLinkFields({ link, onChange }: {
  link: TransitLink
  onChange: (patch: Partial<TransitLink>) => void
}) {
  const { t } = useTranslation(['admin'])
  const E = 'admin:nodes.egress.'
  return (
    <>
      <TextField select size="small" label={t(`${E}protocol`)} value={link.protocol}
        onChange={e => {
          const protocol = e.target.value as TransitLinkProtocol
          const site = link.reality_server_name || randomRealitySite()
          onChange(protocol === 'vless_reality'
            ? { protocol, reality_server_name: site, reality_target: link.reality_target || `${site}:443` }
            : { protocol })
        }}
        helperText={link.reality_refused ? t(`${E}reality_refused`) : undefined}>
        <MenuItem value="ss2022">Shadowsocks 2022</MenuItem>
        <MenuItem value="vless_reality" disabled={link.reality_refused && link.protocol !== 'vless_reality'}>VLESS + REALITY</MenuItem>
      </TextField>
      <TextField size="small" label={t(`${E}exit_address`)} value={link.exit_address} placeholder={link.server_address}
        onChange={e => onChange({ exit_address: e.target.value })}
        helperText={link.server_address ? t(`${E}exit_address_default`, { address: link.server_address }) : t(`${E}exit_address_unknown`)} />
      {link.protocol === 'vless_reality' && (
        <>
          <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
            <TextField size="small" label={t(`${E}reality_server_name`)} value={link.reality_server_name} sx={{ flex: 1 }}
              onChange={e => onChange({ reality_server_name: e.target.value })} />
            <Button type="button" size="small" sx={{ mt: 0.5 }} onClick={() => {
              const site = randomRealitySite([link.reality_server_name])
              onChange({ reality_server_name: site, reality_target: `${site}:443` })
            }}>{t(`${E}random_site`)}</Button>
          </Box>
          <TextField size="small" label={t(`${E}reality_target`)} value={link.reality_target}
            onChange={e => onChange({ reality_target: e.target.value })} />
        </>
      )}
    </>
  )
}
