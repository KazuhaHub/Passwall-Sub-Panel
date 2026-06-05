import { useCallback, useEffect, useState } from 'react'
import {
  Autocomplete,
  Box,
  Button,
  Card,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  FormControlLabel,
  IconButton,
  InputLabel,
  MenuItem,
  Select,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import EditIcon from '@mui/icons-material/EditOutlined'
import AutorenewIcon from '@mui/icons-material/Autorenew'
import { useTranslation } from 'react-i18next'

import {
  createCert,
  createDNSCred,
  deleteCert,
  deleteDNSCred,
  listCerts,
  listDNSCreds,
  listDNSProviders,
  renewCert,
  updateDNSCred,
  type Cert,
  type DNSCredential,
} from '@/api/certs'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'

function statusColor(md: Record<string, string>, status: string): string {
  switch (status) {
    case 'active':
      return md.primary
    case 'failed':
      return md.error
    default: // pending / renewing
      return md.onSurfaceVariant
  }
}

function fmtDate(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return isNaN(d.getTime()) ? '—' : d.toLocaleDateString()
}

export default function CertificatesView() {
  const theme = useTheme()
  const md = theme.palette.md as unknown as Record<string, string>
  const { t } = useTranslation(['admin', 'common'])

  const [certs, setCerts] = useState<Cert[]>([])
  const [creds, setCreds] = useState<DNSCredential[]>([])
  const [providers, setProviders] = useState<string[]>([])
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const [c, d] = await Promise.all([listCerts(), listDNSCreds()])
      setCerts(c)
      setCreds(d)
    } catch {
      /* the axios interceptor surfaces the error toast */
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    reload()
    listDNSProviders().then(setProviders).catch(() => {})
  }, [reload])

  // ---- certificate dialog ----
  const [certOpen, setCertOpen] = useState(false)
  const [certName, setCertName] = useState('')
  const [certDomains, setCertDomains] = useState('')
  const [certCredId, setCertCredId] = useState<number | ''>('')
  const [certAutoRenew, setCertAutoRenew] = useState(true)
  const [certBusy, setCertBusy] = useState(false)

  function openCert() {
    setCertName('')
    setCertDomains('')
    setCertCredId(creds[0]?.id ?? '')
    setCertAutoRenew(true)
    setCertOpen(true)
  }

  async function submitCert() {
    const domains = certDomains
      .split(/[\s,]+/)
      .map(s => s.trim())
      .filter(Boolean)
    if (!certName.trim() || domains.length === 0 || certCredId === '') {
      pushSnack(t('admin:certs.validation_required'), 'error')
      return
    }
    setCertBusy(true)
    try {
      await createCert({
        name: certName.trim(),
        domains,
        dns_credential_id: Number(certCredId),
        auto_renew: certAutoRenew,
      })
      pushSnack(t('admin:certs.create_queued'), 'success')
      setCertOpen(false)
      reload()
    } catch {
      /* toast */
    } finally {
      setCertBusy(false)
    }
  }

  async function onRenew(c: Cert) {
    try {
      await renewCert(c.id)
      pushSnack(t('admin:certs.renew_queued'), 'success')
      reload()
    } catch {
      /* toast */
    }
  }

  async function onDeleteCert(c: Cert) {
    const ok = await confirm({
      title: t('admin:certs.delete_title'),
      message: t('admin:certs.delete_confirm', { name: c.name }),
      destructive: true,
      confirmText: t('common:delete'),
    })
    if (!ok) return
    await deleteCert(c.id)
    pushSnack(t('admin:certs.deleted'), 'success')
    reload()
  }

  // ---- DNS credential dialog ----
  const [credOpen, setCredOpen] = useState(false)
  const [credEditing, setCredEditing] = useState<DNSCredential | null>(null)
  const [credName, setCredName] = useState('')
  const [credProvider, setCredProvider] = useState('')
  const [credPairs, setCredPairs] = useState<{ k: string; v: string }[]>([{ k: '', v: '' }])
  const [credBusy, setCredBusy] = useState(false)

  function openCred(c?: DNSCredential) {
    setCredEditing(c ?? null)
    setCredName(c?.name ?? '')
    setCredProvider(c?.provider ?? '')
    // On edit the secret VALUES are write-only — prefill the key names with
    // blank values so the admin re-enters the secrets to change them.
    setCredPairs(c && c.keys.length ? c.keys.map(k => ({ k, v: '' })) : [{ k: '', v: '' }])
    setCredOpen(true)
  }

  function setPair(i: number, field: 'k' | 'v', val: string) {
    setCredPairs(prev => prev.map((p, idx) => (idx === i ? { ...p, [field]: val } : p)))
  }

  async function submitCred() {
    if (!credName.trim() || !credProvider.trim()) {
      pushSnack(t('admin:certs.validation_required'), 'error')
      return
    }
    const credentials: Record<string, string> = {}
    for (const p of credPairs) {
      if (p.k.trim()) credentials[p.k.trim()] = p.v
    }
    // On create every secret must be filled — a blank value only means
    // "keep the stored secret" when editing (the backend merges it).
    if (!credEditing && Object.values(credentials).some(v => !v.trim())) {
      pushSnack(t('admin:certs.validation_secret_required'), 'error')
      return
    }
    setCredBusy(true)
    try {
      if (credEditing) {
        await updateDNSCred(credEditing.id, { name: credName.trim(), provider: credProvider.trim(), credentials })
      } else {
        await createDNSCred({ name: credName.trim(), provider: credProvider.trim(), credentials })
      }
      pushSnack(t('common:saved'), 'success')
      setCredOpen(false)
      reload()
    } catch {
      /* toast */
    } finally {
      setCredBusy(false)
    }
  }

  async function onDeleteCred(c: DNSCredential) {
    const ok = await confirm({
      title: t('admin:certs.cred_delete_title'),
      message: t('admin:certs.cred_delete_confirm', { name: c.name }),
      destructive: true,
      confirmText: t('common:delete'),
    })
    if (!ok) return
    await deleteDNSCred(c.id)
    pushSnack(t('admin:certs.deleted'), 'success')
    reload()
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      {/* Certificates */}
      <Card sx={{ p: 2 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', mb: 1.5 }}>
          <Typography sx={{ fontSize: 18, fontWeight: 600, flex: 1 }}>{t('admin:certs.title')}</Typography>
          <Button startIcon={<AddIcon />} variant="contained" size="small" disabled={creds.length === 0} onClick={openCert}>
            {t('admin:certs.new')}
          </Button>
        </Box>
        {creds.length === 0 && (
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 1 }}>{t('admin:certs.need_cred_first')}</Typography>
        )}
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('admin:certs.col_name')}</TableCell>
                <TableCell>{t('admin:certs.col_domains')}</TableCell>
                <TableCell>{t('admin:certs.col_status')}</TableCell>
                <TableCell>{t('admin:certs.col_expiry')}</TableCell>
                <TableCell align="right">{t('common:actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {loading && (
                <TableRow>
                  <TableCell colSpan={5} align="center">
                    <CircularProgress size={22} />
                  </TableCell>
                </TableRow>
              )}
              {!loading && certs.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} align="center" sx={{ color: md.onSurfaceVariant }}>
                    {t('common:empty')}
                  </TableCell>
                </TableRow>
              )}
              {certs.map(c => (
                <TableRow key={c.id} hover>
                  <TableCell>{c.name}</TableCell>
                  <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{c.domains.join(', ')}</TableCell>
                  <TableCell>
                    <Tooltip title={c.last_error || ''} disableHoverListener={!c.last_error}>
                      <Chip
                        label={t(`admin:certs.status.${c.status}`, { defaultValue: c.status })}
                        size="small"
                        sx={{ bgcolor: statusColor(md, c.status), color: md.surface ?? '#fff', height: 22 }}
                      />
                    </Tooltip>
                  </TableCell>
                  <TableCell>{fmtDate(c.not_after)}</TableCell>
                  <TableCell align="right">
                    <Tooltip title={t('admin:certs.renew')}>
                      <IconButton size="small" onClick={() => onRenew(c)}>
                        <AutorenewIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                    <Tooltip title={t('common:delete')}>
                      <IconButton size="small" onClick={() => onDeleteCert(c)}>
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </Card>

      {/* DNS credentials */}
      <Card sx={{ p: 2 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', mb: 1.5 }}>
          <Typography sx={{ fontSize: 18, fontWeight: 600, flex: 1 }}>{t('admin:certs.cred_title')}</Typography>
          <Button startIcon={<AddIcon />} variant="contained" size="small" onClick={() => openCred()}>
            {t('admin:certs.cred_new')}
          </Button>
        </Box>
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('admin:certs.col_name')}</TableCell>
                <TableCell>{t('admin:certs.cred_provider')}</TableCell>
                <TableCell>{t('admin:certs.cred_keys')}</TableCell>
                <TableCell align="right">{t('common:actions')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {creds.map(c => (
                <TableRow key={c.id} hover>
                  <TableCell>{c.name}</TableCell>
                  <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{c.provider}</TableCell>
                  <TableCell sx={{ fontFamily: 'monospace', fontSize: 12, color: md.onSurfaceVariant }}>{c.keys.join(', ')}</TableCell>
                  <TableCell align="right">
                    <Tooltip title={t('common:edit')}>
                      <IconButton size="small" onClick={() => openCred(c)}>
                        <EditIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                    <Tooltip title={t('common:delete')}>
                      <IconButton size="small" onClick={() => onDeleteCred(c)}>
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  </TableCell>
                </TableRow>
              ))}
              {creds.length === 0 && (
                <TableRow>
                  <TableCell colSpan={4} align="center" sx={{ color: md.onSurfaceVariant }}>
                    {t('common:empty')}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </TableContainer>
      </Card>

      {/* create-cert dialog */}
      <Dialog open={certOpen} onClose={() => setCertOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>{t('admin:certs.new')}</DialogTitle>
        <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 2 }}>
          <TextField label={t('admin:certs.col_name')} value={certName} onChange={e => setCertName(e.target.value)} size="small" autoFocus />
          <TextField
            label={t('admin:certs.col_domains')}
            value={certDomains}
            onChange={e => setCertDomains(e.target.value)}
            size="small"
            multiline
            minRows={2}
            placeholder={'*.example.com\nexample.com'}
            helperText={t('admin:certs.domains_help')}
          />
          <FormControl size="small">
            <InputLabel>{t('admin:certs.cred_title')}</InputLabel>
            <Select label={t('admin:certs.cred_title')} value={certCredId} onChange={e => setCertCredId(e.target.value as number)}>
              {creds.map(c => (
                <MenuItem key={c.id} value={c.id}>
                  {c.name} ({c.provider})
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <FormControlLabel control={<Switch checked={certAutoRenew} onChange={e => setCertAutoRenew(e.target.checked)} />} label={t('admin:certs.auto_renew')} />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCertOpen(false)}>{t('common:cancel')}</Button>
          <Button variant="contained" onClick={submitCert} disabled={certBusy}>
            {certBusy ? <CircularProgress size={20} /> : t('common:create')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* create/edit DNS credential dialog */}
      <Dialog open={credOpen} onClose={() => setCredOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>{credEditing ? t('common:edit') : t('admin:certs.cred_new')}</DialogTitle>
        <DialogContent sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 2 }}>
          <TextField label={t('admin:certs.col_name')} value={credName} onChange={e => setCredName(e.target.value)} size="small" autoFocus />
          <Autocomplete
            freeSolo
            options={providers}
            value={credProvider}
            onChange={(_, v) => setCredProvider(v ?? '')}
            onInputChange={(_, v) => setCredProvider(v)}
            renderInput={params => <TextField {...params} label={t('admin:certs.cred_provider')} size="small" helperText={t('admin:certs.provider_help')} />}
          />
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:certs.cred_kv_help')}</Typography>
          {credPairs.map((p, i) => (
            <Box key={i} sx={{ display: 'flex', gap: 1 }}>
              <TextField label="KEY" value={p.k} onChange={e => setPair(i, 'k', e.target.value)} size="small" sx={{ flex: 1 }} placeholder="CF_DNS_API_TOKEN" />
              <TextField label="VALUE" value={p.v} onChange={e => setPair(i, 'v', e.target.value)} size="small" sx={{ flex: 2 }} type="password" />
              <IconButton size="small" onClick={() => setCredPairs(prev => prev.filter((_, idx) => idx !== i))}>
                <DeleteIcon fontSize="small" />
              </IconButton>
            </Box>
          ))}
          <Button size="small" startIcon={<AddIcon />} onClick={() => setCredPairs(prev => [...prev, { k: '', v: '' }])}>
            {t('admin:certs.cred_add_field')}
          </Button>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCredOpen(false)}>{t('common:cancel')}</Button>
          <Button variant="contained" onClick={submitCred} disabled={credBusy}>
            {credBusy ? <CircularProgress size={20} /> : t('common:save')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
