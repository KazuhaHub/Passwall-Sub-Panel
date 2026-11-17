import { useEffect, useState } from 'react'
import { Alert, Box, Button, Collapse, MenuItem, TextField, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { getTransitLink, updateTransitLink, type TransitLink, type TransitLinkProtocol } from '@/api/nodes'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import LinkQualityCell from '../LinkQualityCell'

const SITES = ['www.asus.com', 'www.samsung.com', 'www.nvidia.com', 'www.amd.com', 'www.intel.com']
const randomSite = () => SITES[Math.floor(Math.random() * SITES.length)]

/**
 * The tunnel between the node's server and one exit server a route uses, with
 * its own Save: the settings are shared by every node of that pair, so they are
 * not part of the routes' save. Loaded only once opened.
 */
export default function RouteTunnelSettings({ entry, exit, name, defaultExitNodes = [] }: {
  entry: number
  exit: number
  name: string
  /** Nodes on the entry server whose default exit is this exit server: the tunnel they carry everything through. */
  defaultExitNodes?: string[]
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const E = 'admin:nodes.egress.'
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<TransitLink | null>(null)
  const [dirty, setDirty] = useState(false)
  const [failed, setFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  // The protocol the tunnel had when it was loaded: changing it re-handshakes the link.
  const [loadedProtocol, setLoadedProtocol] = useState<TransitLinkProtocol | null>(null)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setFailed(false)
    getTransitLink(entry, exit)
      .then(link => { if (!cancelled) { setForm(link); setLoadedProtocol(link.protocol); setDirty(false) } })
      .catch(() => { if (!cancelled) { setForm(null); setFailed(true) } })
    return () => { cancelled = true }
  }, [open, entry, exit])

  const patch = (p: Partial<TransitLink>) => { setForm(f => (f ? { ...f, ...p } : f)); setDirty(true) }

  async function save() {
    if (!form) return
    const protocolChanged = loadedProtocol !== null && form.protocol !== loadedProtocol
    // A pure address edit that no default exit depends on needs no go-ahead.
    if (protocolChanged || defaultExitNodes.length > 0) {
      const nodes = defaultExitNodes.join(', ')
      const ok = await confirm({
        title: t('admin:nodes.routes.tunnel.confirm_title', { name }),
        message: [
          defaultExitNodes.length > 0 ? t('admin:nodes.routes.tunnel.confirm_nodes', { nodes }) : '',
          protocolChanged ? t('admin:nodes.routes.tunnel.confirm_protocol') : '',
        ].filter(Boolean).join('\n\n'),
        destructive: protocolChanged,
        confirmText: t('admin:nodes.routes.tunnel.save'),
      })
      if (!ok) return
    }
    setBusy(true)
    try {
      const reality = form.protocol === 'vless_reality'
      const link = await updateTransitLink(entry, exit, {
        protocol: form.protocol,
        exit_address: form.exit_address.trim(),
        reality_server_name: reality ? form.reality_server_name.trim() : undefined,
        reality_target: reality ? form.reality_target.trim() : undefined,
      })
      setForm(link)
      setLoadedProtocol(link.protocol)
      setDirty(false)
      pushSnack(t('admin:nodes.routes.tunnel.saved'), 'success')
    } catch { /* the shared client already reports the refusal */ } finally { setBusy(false) }
  }

  return (
    <Box data-testid="route-tunnel" sx={{ border: `1px solid ${md.outlineVariant}`, borderRadius: 2, p: 1 }}>
      <Button size="small" data-testid="route-tunnel-toggle" aria-expanded={open} onClick={() => setOpen(o => !o)}>
        {t('admin:nodes.routes.tunnel.title', { name })}
      </Button>
      <Collapse in={open} unmountOnExit>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, pt: 1 }}>
          {failed && <Alert severity="error">{t(`${E}load_failed`)}</Alert>}
          {form && (
            <>
              <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t(`${E}shared_hint`)}</Typography>
              {defaultExitNodes.length > 0 && (
                <Typography data-testid="route-tunnel-users" sx={{ fontSize: 12, color: md.tertiary }}>
                  {t('admin:nodes.routes.tunnel.default_users', { nodes: defaultExitNodes.join(', ') })}
                </Typography>
              )}
              <LinkQualityCell entry={entry} exit={exit} />
              <TextField select size="small" label={t(`${E}protocol`)} value={form.protocol}
                onChange={e => {
                  const protocol = e.target.value as TransitLinkProtocol
                  const site = form.reality_server_name || randomSite()
                  patch(protocol === 'vless_reality'
                    ? { protocol, reality_server_name: site, reality_target: form.reality_target || `${site}:443` }
                    : { protocol })
                }}>
                <MenuItem value="ss2022">Shadowsocks 2022</MenuItem>
                <MenuItem value="vless_reality">VLESS + REALITY</MenuItem>
              </TextField>
              <TextField size="small" label={t(`${E}exit_address`)} value={form.exit_address} placeholder={form.server_address}
                onChange={e => patch({ exit_address: e.target.value })}
                helperText={form.server_address ? t(`${E}exit_address_default`, { address: form.server_address }) : t(`${E}exit_address_unknown`)} />
              {form.protocol === 'vless_reality' && (
                <Box sx={{ display: 'flex', gap: 1, alignItems: 'flex-start' }}>
                  <TextField size="small" label={t(`${E}reality_server_name`)} value={form.reality_server_name} sx={{ flex: 1 }}
                    onChange={e => { const n = e.target.value; patch({ reality_server_name: n, reality_target: n.trim() ? `${n.trim()}:443` : '' }) }} />
                  <Button size="small" sx={{ mt: 0.5 }} onClick={() => { const s = randomSite(); patch({ reality_server_name: s, reality_target: `${s}:443` }) }}>
                    {t(`${E}random_site`)}
                  </Button>
                </Box>
              )}
              <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                <Button size="small" variant="contained" data-testid="route-tunnel-save" disabled={!dirty || busy} onClick={() => void save()}>
                  {t('admin:nodes.routes.tunnel.save')}
                </Button>
              </Box>
            </>
          )}
        </Box>
      </Collapse>
    </Box>
  )
}
