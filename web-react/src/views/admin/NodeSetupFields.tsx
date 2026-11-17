import { useState } from 'react'
import {
  Alert, Autocomplete, Box, Button, FormControlLabel, MenuItem, Stack, Switch,
  Table, TableBody, TableCell, TableHead, TableRow, TextField, Typography, createFilterOptions,
} from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { NodeSetupEntry, NodeSetupPlan } from '@/api/nodes'
import type { Server } from '@/api/servers'
import type { RelayLine } from '@/api/types'
import { getServerPorts, suggestPort } from '@/api/fronts'
import InfoTip from '@/components/InfoTip'
import CdnLineFields, { CDN_DEFAULT_PORT } from './CdnLineFields'

export interface SetupRelay {
  key: string
  line: RelayLine
  tunnel?: NodeSetupEntry
}

export const newSetupRelay = (key: string): SetupRelay => ({
  key, line: { name: '', address: '', port: 0, enabled: true },
})

interface EntryProps {
  servers: Server[]
  recent: number[]
  entries: SetupRelay[]
  onChange: (entries: SetupRelay[]) => void
  canTunnel: boolean
  useDirect: boolean
  onDirectChange: (useDirect: boolean) => void
  hostOf: (server: Server) => string
  newKey: () => string
  randomSite: () => string
  tcpTLS: boolean
  onBusyChange: (busy: boolean) => void
}

export function SetupEntries({
  servers, recent, entries, onChange, canTunnel, useDirect, onDirectChange, hostOf, newKey, randomSite, tcpTLS, onBusyChange,
}: EntryProps) {
  const { t } = useTranslation(['admin', 'common'])
  const [busy, setBusy] = useState(false)
  const changeBusy = (value: boolean) => {
    setBusy(value)
    onBusyChange(value)
  }
  const recentGroup = t('admin:nodes.setup.recent')
  const groupOf = (server: Server) => recent.includes(server.id) ? recentGroup
    : [server.region, ...(server.tags ?? [])].filter(Boolean).join(' · ') || t('admin:nodes.setup.untagged')
  const options = [...servers].sort((a, b) => {
    const ra = recent.indexOf(a.id)
    const rb = recent.indexOf(b.id)
    if (ra >= 0 || rb >= 0) return (ra < 0 ? Infinity : ra) - (rb < 0 ? Infinity : rb)
    return groupOf(a).localeCompare(groupOf(b)) || a.name.localeCompare(b.name)
  })
  const filterOptions = createFilterOptions<Server>({
    stringify: s => [s.name, s.region, ...(s.tags ?? [])].filter(Boolean).join(' '),
  })
  const selected = servers.filter(s => entries.some(e => e.line.entry_panel_id === s.id))
  const patchLine = (key: string, patch: Partial<RelayLine>) =>
    onChange(entries.map(e => e.key === key ? { ...e, line: { ...e.line, ...patch } } : e))

  async function fillPort(server: Server, allowFront: boolean) {
    try {
      if (tcpTLS || allowFront) {
        const usage = await getServerPorts(server.id)
        const on443 = usage.filter(p => p.port === 443 || (p.range_high != null && p.port <= 443 && p.range_high >= 443))
        if (on443.length === 0 || (allowFront && on443.every(p => p.kind === 'front'))) return 443
      }
      return await suggestPort(server.id)
    } catch {
      // The entry remains editable when a suggestion is unavailable.
      return 0
    }
  }

  async function select(values: Server[]) {
    changeBusy(true)
    try {
      const picked = new Set(values.map(s => s.id))
      const kept = entries.filter(e => !e.line.entry_panel_id || picked.has(e.line.entry_panel_id))
      const added = values.filter(s => !selected.some(v => v.id === s.id))
      const additions = await Promise.all(added.map(async server => {
        const draft = newSetupRelay(newKey())
        return {
          ...draft,
          line: { ...draft.line, name: server.name, address: hostOf(server), port: await fillPort(server, tcpTLS), entry_panel_id: server.id },
        }
      }))
      onChange([...kept, ...additions])
    } finally { changeBusy(false) }
  }

  function changeMode(entry: SetupRelay, mode: 'forward' | 'tunnel') {
    const site = randomSite()
    onChange(entries.map(e => e.key === entry.key ? {
      ...e, line: { ...e.line, mode: mode === 'forward' ? undefined : mode },
      tunnel: mode === 'tunnel' ? e.tunnel ?? { listen: '', sni: site, target: `${site}:443` } : e.tunnel,
    } : e))
  }

  return (
    <Stack spacing={2}>
      <FormControlLabel label={t('admin:nodes.setup.direct')}
        control={<Switch checked={useDirect} onChange={(_, checked) => onDirectChange(checked)} />} />
      <Autocomplete multiple options={options} groupBy={groupOf} filterOptions={filterOptions} value={selected} loading={busy} disabled={busy}
        getOptionLabel={s => s.name} isOptionEqualToValue={(a, b) => a.id === b.id}
        onChange={(_, values) => void select(values)}
        renderInput={params => <TextField {...params} label={t('admin:nodes.setup.entries')} />} />
      {entries.map(({ key, line, tunnel }) => (
        <Stack key={key} spacing={1.25} sx={{ p: 1.5, border: 1, borderColor: 'divider', borderRadius: 2 }}>
          <Stack direction="row" sx={{ alignItems: 'center' }} spacing={1}>
            <TextField size="small" fullWidth value={line.name} label={t('admin:nodes.relay.name')}
              disabled={busy} onChange={e => patchLine(key, { name: e.target.value })} />
            <Button size="small" disabled={busy} onClick={() => onChange(entries.filter(e => e.key !== key))}>
              {t('admin:nodes.relay.remove')}
            </Button>
          </Stack>
          {line.entry_panel_id ? (
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <TextField select size="small" fullWidth label={t('admin:nodes.setup.mode')}
                value={line.mode ?? 'forward'} disabled={busy} onChange={e => changeMode({ key, line, tunnel }, e.target.value as 'forward' | 'tunnel')}>
                <MenuItem value="forward">{t('admin:nodes.tunnel.mode_forward')}</MenuItem>
                <MenuItem value="tunnel" disabled={!canTunnel}>{t('admin:nodes.tunnel.mode_tunnel')}</MenuItem>
              </TextField>
              <InfoTip text={t('admin:nodes.setup.mode_help')} />
            </Stack>
          ) : null}
          {line.mode === 'cdn' ? <CdnLineFields line={line} onPatch={patch => patchLine(key, patch)} /> : (
            <>
              <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1}>
                <TextField size="small" fullWidth required value={line.address} label={t('admin:nodes.relay.address')}
                  disabled={busy} onChange={e => patchLine(key, { address: e.target.value })} />
                <TextField size="small" type="number" required={!!line.entry_panel_id}
                  label={t('admin:nodes.relay.port')} value={line.port || ''}
                  disabled={busy} onChange={e => patchLine(key, { port: Number(e.target.value) || 0 })} />
                {line.entry_panel_id ? <Button disabled={busy} onClick={async () => {
                  const server = servers.find(s => s.id === line.entry_panel_id)
                  if (!server) return
                  changeBusy(true)
                  try {
                    const port = await suggestPort(server.id)
                    patchLine(key, { port })
                  } catch {
                    // The address and current port stay available for manual editing.
                  } finally { changeBusy(false) }
                }}>{t('admin:nodes.setup.random_port')}</Button> : null}
              </Stack>
              {line.mode === 'tunnel' && tunnel && (
                <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1}>
                  {(['listen', 'sni', 'target'] as const).map(field => (
                    <TextField key={field} size="small" fullWidth value={tunnel[field]} required={field !== 'listen'}
                      label={t(`admin:nodes.setup.${field}`)} disabled={busy}
                      onChange={event => onChange(entries.map(e => e.key === key
                        ? { ...e, tunnel: { ...tunnel, [field]: event.target.value } } : e))} />
                  ))}
                </Stack>
              )}
            </>
          )}
        </Stack>
      ))}
      <Stack direction="row" spacing={1}>
        <Button size="small" disabled={busy} onClick={() => onChange([...entries, newSetupRelay(newKey())])}>
          {t('admin:nodes.relay.entry_external')}
        </Button>
        <Button size="small" disabled={busy} onClick={() => {
          const entry = newSetupRelay(newKey())
          onChange([...entries, { ...entry, line: { ...entry.line, mode: 'cdn', port: CDN_DEFAULT_PORT } }])
        }}>{t('admin:nodes.relay.entry_cdn')}</Button>
      </Stack>
    </Stack>
  )
}

interface ReviewProps {
  plan: NodeSetupPlan
  entries: SetupRelay[]
  direct: boolean
  peers: Server[]
  landingPort: number
  routeCount: number
  editingLoopbacks: Record<number, number>
  onLoopback: (server: number, port: number) => void
  editingLinkPorts: Record<string, number>
  onLinkPort: (link: string, port: number) => void
  changed: boolean
}

export function SetupReview({ plan, entries, direct, peers, landingPort, routeCount, editingLoopbacks, onLoopback, editingLinkPorts, onLinkPort, changed }: ReviewProps) {
  const { t } = useTranslation(['admin'])
  const peer = (id: number) => peers.find(p => p.id === id)?.name ?? `#${id}`
  return (
    <Stack spacing={1.5}>
      {changed && <Alert severity="info">{t('admin:nodes.setup.review_changes')}</Alert>}
      <Typography variant="body2">{t(direct ? 'admin:nodes.setup.direct_on' : 'admin:nodes.setup.direct_off')}</Typography>
      {routeCount > 0 && <Typography variant="body2">{t('admin:nodes.setup.routes_count', { count: routeCount })}</Typography>}
      {plan.warnings?.map((warning, i) => <Alert severity="warning" key={i}>{warning}</Alert>)}
      <Table size="small" aria-label={t('admin:nodes.setup.review')}>
        <TableHead><TableRow>
          <TableCell>{t('admin:nodes.setup.server')}</TableCell>
          <TableCell>{t('admin:nodes.setup.binding')}</TableCell>
          <TableCell>{t('admin:nodes.setup.sni')}</TableCell>
        </TableRow></TableHead>
        <TableBody>
          {[...plan.bindings].sort((a, b) => a.panel_id - b.panel_id).map((b, i) => {
            const link = b.kind === 'link' && !b.reused ? `${b.peer_id}/${b.panel_id}` : null
            return (
              <TableRow key={i}>
                <TableCell>{peer(b.panel_id)}</TableCell>
                <TableCell>
                  {t(`admin:nodes.setup.kind.${b.kind}`)}{b.name && ` · ${b.name}`}{b.peer_id ? ` · ${peer(b.peer_id)}` : ''}
                  <Typography variant="body2" component="div" color="text.secondary">
                    {b.address && `${b.address} · `}{b.listen || t('admin:nodes.setup.all_addresses')} · {b.port}
                    {b.reused && ` · ${t('admin:nodes.setup.reused')}`}
                  </Typography>
                  {b.front_id && <Typography variant="body2" color="text.secondary">{t('admin:nodes.setup.shared')}</Typography>}
                  {b.loopback ? (
                    <TextField size="small" type="number" sx={{ mt: 1 }} label={t('admin:nodes.setup.loopback')}
                      value={editingLoopbacks[b.panel_id] ?? b.loopback}
                      onChange={e => onLoopback(b.panel_id, Number(e.target.value) || 0)} />
                  ) : null}
                  {link && <TextField size="small" type="number" sx={{ mt: 1 }} label={t('admin:nodes.setup.link_port')}
                    value={editingLinkPorts[link] ?? b.port}
                    onChange={e => onLinkPort(link, Number(e.target.value) || 0)} />}
                </TableCell>
                <TableCell>{b.sni?.join(', ') || '—'}{b.target && <Box color="text.secondary">{b.target}</Box>}</TableCell>
              </TableRow>
            )
          })}
          {entries.filter(e => !e.line.entry_panel_id).map(({ key, line }) => (
            <TableRow key={key}>
              <TableCell>{t(line.mode === 'cdn' ? 'admin:nodes.relay.entry_cdn' : 'admin:nodes.relay.entry_external')}</TableCell>
              <TableCell>{line.address || line.domain} · {line.port || (line.mode === 'cdn' ? CDN_DEFAULT_PORT : landingPort)}</TableCell>
              <TableCell>{line.sni || line.domain || '—'}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Stack>
  )
}
