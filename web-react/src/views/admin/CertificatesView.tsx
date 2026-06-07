import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
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
  Divider,
  FormControl,
  FormControlLabel,
  IconButton,
  InputLabel,
  MenuItem,
  Select,
  Switch,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Tabs,
  TextField,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutline'
import EditIcon from '@mui/icons-material/EditOutlined'
import AutorenewIcon from '@mui/icons-material/Autorenew'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import DownloadIcon from '@mui/icons-material/Download'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import VisibilityIcon from '@mui/icons-material/VisibilityOutlined'
import { useTranslation } from 'react-i18next'

import {
  createACMEAccount,
  createCert,
  createDNSCred,
  deleteACMEAccount,
  deleteCert,
  deleteDNSCred,
  downloadCert,
  getCertDetail,
  listACMEAccounts,
  listACMEKeyTypes,
  listCerts,
  listDNSCreds,
  listDNSProviders,
  renewCert,
  updateACMEAccount,
  updateCert,
  updateDNSCred,
  type ACMEAccount,
  type Cert,
  type CertPEM,
  type CertTask,
  type DNSCredential,
  type DNSProviderInfo,
} from '@/api/certs'
import { getUISettings, putUISettings, type UISettings } from '@/api/settings'
import PageHeader from '@/components/PageHeader'
import { PagedTableFooter } from '@/components/PagedTableFooter'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { copyToClipboard } from '@/utils/clipboard'
import { formatDualTz } from '@/utils/datetime'
import { useSiteStore } from '@/stores/site'

const LE_PROD = 'https://acme-v02.api.letsencrypt.org/directory'
const LE_STAGING = 'https://acme-staging-v02.api.letsencrypt.org/directory'

// CA directory presets for the ACME-account form. ZeroSSL + Google Public CA
// require External Account Binding (eab=true), so the form shows the EAB fields.
const CA_PRESETS: { label: string; url: string; eab?: boolean }[] = [
  { label: "Let's Encrypt", url: LE_PROD },
  { label: 'LE Staging', url: LE_STAGING },
  { label: 'ZeroSSL', url: 'https://acme.zerossl.com/v2/DV90', eab: true },
  { label: 'Google', url: 'https://dv.acme-v02.api.pki.goog/directory', eab: true },
]

// caLabel maps a directory URL to a short CA label for the account list / cert
// selector; an unrecognized URL shows its hostname so custom CAs still read well.
function caLabel(directory: string): string {
  const preset = CA_PRESETS.find(p => p.url === directory)
  if (preset) return preset.label
  try {
    return new URL(directory).hostname
  } catch {
    return directory
  }
}

function statusColor(md: Record<string, string>, status: string): string {
  switch (status) {
    case 'active':
      return md.primary
    case 'failed':
      return md.error
    case 'expired':
      return '#c98a2b' // amber — a valid cert past its expiry, needs renewal
    default: // pending (issuing)
      return md.onSurfaceVariant
  }
}

function DetailRow({ md, label, children }: { md: Record<string, string>; label: string; children: ReactNode }) {
  return (
    <Box sx={{ display: 'flex', gap: 2, alignItems: 'flex-start' }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, minWidth: 96, pt: 0.25 }}>{label}</Typography>
      <Box sx={{ fontSize: 13, flex: 1, minWidth: 0 }}>{children}</Box>
    </Box>
  )
}

// PemBox renders one read-only PEM block with a copy button in its header —
// the cert-chain / private-key reveal in the "View certificate & key" popup.
function PemBox({ md, label, value, copyTitle, danger }: {
  md: Record<string, string>; label: string; value: string; copyTitle: string; danger?: boolean
}) {
  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', mb: 0.5 }}>
        <Typography sx={{ fontSize: 12, fontWeight: 600, color: danger ? md.error : md.onSurfaceVariant, flex: 1 }}>{label}</Typography>
        <Tooltip title={copyTitle}>
          <IconButton size="small" onClick={() => copyToClipboard(value)}><ContentCopyIcon fontSize="small" /></IconButton>
        </Tooltip>
      </Box>
      <Box sx={{
        p: 1.25, borderRadius: 1.5, bgcolor: md.surfaceContainerHighest ?? 'rgba(0,0,0,.25)',
        border: danger ? `1px solid ${md.error}` : undefined,
        maxHeight: 200, overflow: 'auto',
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 11.5,
        whiteSpace: 'pre', color: md.onSurface,
      }}>{value}</Box>
    </Box>
  )
}

export default function CertificatesView() {
  const theme = useTheme()
  const md = theme.palette.md as unknown as Record<string, string>
  const { t } = useTranslation(['admin', 'common'])
  const panelTz = useSiteStore(s => s.timezone)

  const [tab, setTab] = useState(0)
  const [certs, setCerts] = useState<Cert[]>([])
  const [creds, setCreds] = useState<DNSCredential[]>([])
  const [accounts, setAccounts] = useState<ACMEAccount[]>([])
  const [keyTypes, setKeyTypes] = useState<string[]>([])
  const [providers, setProviders] = useState<DNSProviderInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [certPage, setCertPage] = useState(1)
  const [certPageSize, setCertPageSize] = useState(25)
  const [credPage, setCredPage] = useState(1)
  const [credPageSize, setCredPageSize] = useState(25)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const [c, d, a] = await Promise.all([listCerts(), listDNSCreds(), listACMEAccounts()])
      setCerts(c)
      setCreds(d)
      setAccounts(a)
    } catch {
      /* the axios interceptor surfaces the error toast */
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    reload()
    listDNSProviders().then(setProviders).catch(() => {})
    listACMEKeyTypes().then(setKeyTypes).catch(() => {})
    getUISettings().then(setSettings).catch(() => {})
  }, [reload])

  // ---- certificate dialog ----
  const [certOpen, setCertOpen] = useState(false)
  const [certEditing, setCertEditing] = useState<Cert | null>(null)
  const [certName, setCertName] = useState('')
  const [certDomains, setCertDomains] = useState('')
  const [certAccountId, setCertAccountId] = useState<number | ''>('')
  const [certCredId, setCertCredId] = useState<number | ''>('')
  const [certAutoRenew, setCertAutoRenew] = useState(true)
  const [certBusy, setCertBusy] = useState(false)

  function openCert(c?: Cert) {
    if (c) {
      setCertEditing(c)
      setCertName(c.name)
      setCertDomains(c.domains.join('\n'))
      setCertAccountId(c.acme_account_id || accounts[0]?.id || '')
      setCertCredId(c.dns_credential_id || creds[0]?.id || '')
      setCertAutoRenew(c.auto_renew)
    } else {
      setCertEditing(null)
      setCertName('')
      setCertDomains('')
      setCertAccountId(accounts[0]?.id ?? '')
      setCertCredId(creds[0]?.id ?? '')
      setCertAutoRenew(true)
    }
    setCertOpen(true)
  }

  async function submitCert() {
    const domains = certDomains
      .split(/[\s,]+/)
      .map(s => s.trim())
      .filter(Boolean)
    if (!certName.trim() || domains.length === 0 || certAccountId === '' || certCredId === '') {
      pushSnack(t('admin:certs.validation_required'), 'error')
      return
    }
    setCertBusy(true)
    try {
      const req = {
        name: certName.trim(),
        domains,
        acme_account_id: Number(certAccountId),
        dns_credential_id: Number(certCredId),
        auto_renew: certAutoRenew,
      }
      if (certEditing) {
        await updateCert(certEditing.id, req)
        pushSnack(t('common:saved', { defaultValue: '已保存' }), 'success')
      } else {
        await createCert(req)
        pushSnack(t('admin:certs.create_queued'), 'success')
      }
      setCertOpen(false)
      reload()
    } catch {
      /* toast */
    } finally {
      setCertBusy(false)
    }
  }

  // ---- ACME account dialog ----
  const [acctOpen, setAcctOpen] = useState(false)
  const [acctEditing, setAcctEditing] = useState<ACMEAccount | null>(null)
  const [acctName, setAcctName] = useState('')
  const [acctEmail, setAcctEmail] = useState('')
  const [acctDir, setAcctDir] = useState(LE_PROD)
  const [acctKeyType, setAcctKeyType] = useState('EC256')
  const [acctEABKid, setAcctEABKid] = useState('')
  const [acctEABHmac, setAcctEABHmac] = useState('')
  const [acctBusy, setAcctBusy] = useState(false)

  function openAcct(a?: ACMEAccount) {
    if (a) {
      setAcctEditing(a)
      setAcctName(a.name); setAcctEmail(a.email); setAcctDir(a.directory)
      setAcctKeyType(a.key_type || 'EC256'); setAcctEABKid(a.eab_key_id); setAcctEABHmac('')
    } else {
      setAcctEditing(null)
      setAcctName('')
      setAcctEmail('')
      setAcctDir(LE_PROD)
      setAcctKeyType('EC256'); setAcctEABKid(''); setAcctEABHmac('')
    }
    setAcctOpen(true)
  }

  async function submitAcct() {
    if (!acctEmail.trim() || !acctDir.trim()) {
      pushSnack(t('admin:certs.validation_required'), 'error')
      return
    }
    setAcctBusy(true)
    try {
      const req = {
        name: acctName.trim(), email: acctEmail.trim(), directory: acctDir.trim(),
        eab_key_id: acctEABKid.trim(), eab_hmac: acctEABHmac, key_type: acctKeyType,
      }
      if (acctEditing) await updateACMEAccount(acctEditing.id, req)
      else await createACMEAccount(req)
      pushSnack(t('common:saved', { defaultValue: '已保存' }), 'success')
      setAcctOpen(false)
      reload()
    } catch {
      /* toast */
    } finally {
      setAcctBusy(false)
    }
  }

  async function onDeleteAcct(a: ACMEAccount) {
    const ok = await confirm({
      title: t('admin:certs.acct_delete_title', { defaultValue: '删除 ACME 账号' }),
      message: t('admin:certs.acct_delete_msg', { name: a.name || a.email, defaultValue: '确定删除「{{name}}」？仍在使用它的证书需先改用其他账号。' }),
      destructive: true,
      confirmText: t('common:actions.delete'),
    })
    if (!ok) return
    try {
      await deleteACMEAccount(a.id)
      pushSnack(t('common:actions.deleted', { defaultValue: '已删除' }), 'success')
      reload()
    } catch {
      /* toast */
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
      confirmText: t('common:actions.delete'),
    })
    if (!ok) return
    await deleteCert(c.id)
    pushSnack(t('admin:certs.deleted'), 'success')
    reload()
  }

  // ---- cert detail dialog ----
  const [detailOpen, setDetailOpen] = useState(false)
  const [detailCert, setDetailCert] = useState<Cert | null>(null)
  const [detailTask, setDetailTask] = useState<CertTask | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [downloading, setDownloading] = useState(false)
  // ---- PEM viewer (reveal cert chain + private key, copyable) ----
  const [pemOpen, setPemOpen] = useState(false)
  const [pemData, setPemData] = useState<CertPEM | null>(null)
  const [pemLoading, setPemLoading] = useState(false)

  // Hover detail for the status chip (like the SSO-method chip): the failure
  // reason when failed, otherwise the validity window.
  function certStatusTooltip(c: Cert): string {
    switch (c.status) {
      case 'failed':
        return c.last_error || t('admin:certs.status.failed')
      case 'expired':
        return `${t('admin:certs.status.expired')} · ${formatDualTz(c.not_after, panelTz)}`
      case 'active':
        return c.not_after ? `${t('admin:certs.col_expiry')}: ${formatDualTz(c.not_after, panelTz)}` : t('admin:certs.status.active')
      default:
        return t('admin:certs.status.pending')
    }
  }

  async function openDetail(c: Cert) {
    setDetailCert(c)
    setDetailTask(null)
    setDetailOpen(true)
    setDetailLoading(true)
    try {
      const d = await getCertDetail(c.id)
      setDetailCert(d.cert)
      setDetailTask(d.task ?? null)
    } catch {
      /* error toast via the axios interceptor */
    } finally {
      setDetailLoading(false)
    }
  }

  function triggerDownload(filename: string, content: string) {
    const blob = new Blob([content], { type: 'application/x-pem-file' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  // openPEM reveals the full chain + private key in a copyable popup (the
  // explicit admin "show me the material" action). Distinct from the list/detail
  // DTOs, which never carry PEMs — this hits the dedicated download endpoint.
  async function openPEM(id: number) {
    setPemData(null)
    setPemOpen(true)
    setPemLoading(true)
    try {
      setPemData(await downloadCert(id))
    } catch {
      setPemOpen(false)
    } finally {
      setPemLoading(false)
    }
  }

  async function onDownloadPEM(id: number, which: 'cert' | 'key') {
    setDownloading(true)
    try {
      const pem = await downloadCert(id)
      const base = (pem.name || 'cert').replace(/[^a-zA-Z0-9._-]/g, '_')
      if (which === 'cert') triggerDownload(`${base}.fullchain.pem`, pem.cert_pem)
      else triggerDownload(`${base}.key.pem`, pem.key_pem)
    } catch {
      /* toast */
    } finally {
      setDownloading(false)
    }
  }

  // ---- DNS credential dialog ----
  const [credOpen, setCredOpen] = useState(false)
  const [credEditing, setCredEditing] = useState<DNSCredential | null>(null)
  const [credName, setCredName] = useState('')
  const [credProvider, setCredProvider] = useState('')
  // Named-provider inputs keyed by env var; custom (exec/httpreq/unknown) inputs.
  const [credValues, setCredValues] = useState<Record<string, string>>({})
  const [credPairs, setCredPairs] = useState<{ k: string; v: string }[]>([{ k: '', v: '' }])
  // Named fields whose stored secret the admin chose to replace (clicked "Change").
  const [credChanged, setCredChanged] = useState<Set<string>>(new Set())
  const [credBusy, setCredBusy] = useState(false)

  // The schema for the chosen provider; null/undefined or custom=true → free-form KV.
  const selProvider = useMemo(() => providers.find(p => p.name === credProvider) ?? null, [providers, credProvider])
  const isCustomProvider = !selProvider || selProvider.custom

  function openCred(c?: DNSCredential) {
    setCredEditing(c ?? null)
    setCredName(c?.name ?? '')
    const provName = c?.provider ?? ''
    setCredProvider(provName)
    // Secret VALUES are write-only — on edit the inputs start blank, and a blank
    // value means "keep the stored secret" (the backend merges it).
    setCredValues({})
    setCredChanged(new Set())
    setCredPairs(c && c.keys.length ? c.keys.map(k => ({ k, v: '' })) : [{ k: '', v: '' }])
    setCredOpen(true)
  }

  // Switching provider resets the credential inputs so stale fields don't carry
  // over between two different vendors' schemas.
  function changeProvider(name: string) {
    setCredProvider(name)
    setCredValues({})
    setCredChanged(new Set())
    setCredPairs([{ k: '', v: '' }])
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
    if (isCustomProvider) {
      for (const p of credPairs) {
        if (p.k.trim()) credentials[p.k.trim()] = p.v
      }
      // On create every secret must be filled — a blank value only means
      // "keep the stored secret" when editing (the backend merges it).
      if (!credEditing && Object.values(credentials).some(v => !v.trim())) {
        pushSnack(t('admin:certs.validation_secret_required'), 'error')
        return
      }
    } else {
      for (const f of selProvider!.fields ?? []) {
        const v = credValues[f.key] ?? ''
        if (credEditing) {
          // Send every field; blank = keep the stored value (backend merge).
          credentials[f.key] = v
        } else if (v.trim()) {
          credentials[f.key] = v
        } else if (!f.optional) {
          pushSnack(t('admin:certs.validation_secret_required'), 'error')
          return
        }
      }
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
      confirmText: t('common:actions.delete'),
    })
    if (!ok) return
    await deleteDNSCred(c.id)
    pushSnack(t('admin:certs.deleted'), 'success')
    reload()
  }

  // ---- ACME settings tab ----
  const [settings, setSettings] = useState<UISettings | null>(null)
  const [acmeBusy, setAcmeBusy] = useState(false)

  function patchSettings<K extends keyof UISettings>(key: K, value: UISettings[K]) {
    setSettings(prev => (prev ? { ...prev, [key]: value } : prev))
  }

  async function saveACME() {
    if (!settings) return
    setAcmeBusy(true)
    try {
      const updated = await putUISettings(settings)
      setSettings(updated)
      pushSnack(t('common:saved'), 'success')
    } catch {
      /* toast */
    } finally {
      setAcmeBusy(false)
    }
  }

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:certs.page_title')}
        subtitle={t('admin:certs.page_subtitle')}
        actions={
          tab === 0 ? (
            <Button startIcon={<AddIcon />} variant="contained" disabled={creds.length === 0 || accounts.length === 0} onClick={() => openCert()}>
              {t('admin:certs.new')}
            </Button>
          ) : tab === 1 ? (
            <Button startIcon={<AddIcon />} variant="contained" onClick={() => openCred()}>
              {t('admin:certs.cred_new')}
            </Button>
          ) : undefined
        }
      />
      <Tabs value={tab} onChange={(_, v) => setTab(v)} sx={{ mb: 2, borderBottom: `1px solid ${md.outlineVariant}` }}>
        <Tab label={t('admin:certs.tab_certs')} />
        <Tab label={t('admin:certs.tab_creds')} />
        <Tab label={t('admin:certs.tab_acme')} />
      </Tabs>

      {/* ---- Tab 0: Certificates ---- */}
      {tab === 0 && (
        <>
          {(accounts.length === 0 || creds.length === 0) && (
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 1.5 }}>
              {accounts.length === 0 && creds.length === 0
                ? t('admin:certs.need_acct_and_cred', { defaultValue: '签发证书前，请先到「ACME 账号」标签建一个 CA 账号，并到「DNS 凭据」标签建一个 DNS 凭据。' })
                : accounts.length === 0
                  ? t('admin:certs.need_acct_first', { defaultValue: '请先到「ACME 账号」标签建一个 ACME 账号，才能签发证书。' })
                  : t('admin:certs.need_cred_first')}
            </Typography>
          )}
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <TableContainer>
              <Table>
                <TableHead>
                  <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}` } }}>
                    <TableCell>{t('admin:certs.col_name')}</TableCell>
                    <TableCell>{t('admin:certs.col_domains')}</TableCell>
                    <TableCell>{t('admin:certs.col_status')}</TableCell>
                    <TableCell>{t('admin:certs.col_expiry')}</TableCell>
                    <TableCell align="right">{t('admin:certs.col_actions')}</TableCell>
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
                  {certs.slice((certPage - 1) * certPageSize, certPage * certPageSize).map(c => (
                    <TableRow key={c.id} hover>
                      <TableCell>{c.name}</TableCell>
                      <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{c.domains.join(', ')}</TableCell>
                      <TableCell>
                        <Tooltip title={certStatusTooltip(c)} arrow>
                          <Chip
                            label={t(`admin:certs.status.${c.status}`, { defaultValue: c.status })}
                            size="small"
                            sx={{ bgcolor: statusColor(md, c.status), color: md.surface ?? '#fff', height: 22, cursor: 'default', '&:hover': { bgcolor: statusColor(md, c.status) } }}
                          />
                        </Tooltip>
                      </TableCell>
                      <TableCell sx={{ whiteSpace: 'nowrap', fontSize: 13 }}>{formatDualTz(c.not_after, panelTz)}</TableCell>
                      <TableCell align="right" sx={{ whiteSpace: 'nowrap' }}>
                        <Tooltip title={t('admin:certs.detail_title')}>
                          <IconButton size="small" onClick={() => openDetail(c)}>
                            <InfoOutlinedIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                        <Tooltip title={t('common:actions.edit')}>
                          <IconButton size="small" onClick={() => openCert(c)}>
                            <EditIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                        <Tooltip title={t('admin:certs.renew')}>
                          <IconButton size="small" onClick={() => onRenew(c)}>
                            <AutorenewIcon fontSize="small" />
                          </IconButton>
                        </Tooltip>
                        <Tooltip title={t('common:actions.delete')}>
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
            <PagedTableFooter
              total={certs.length} page={certPage} pageSize={certPageSize}
              onPageChange={setCertPage} onPageSizeChange={s => { setCertPageSize(s); setCertPage(1) }}
            />
          </Card>
        </>
      )}

      {/* ---- Tab 1: DNS credentials ---- */}
      {tab === 1 && (
        <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
          <TableContainer>
            <Table>
              <TableHead>
                <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}` } }}>
                  <TableCell>{t('admin:certs.col_name')}</TableCell>
                  <TableCell>{t('admin:certs.cred_provider')}</TableCell>
                  <TableCell>{t('admin:certs.cred_keys')}</TableCell>
                  <TableCell align="right">{t('admin:certs.col_actions')}</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {creds.slice((credPage - 1) * credPageSize, credPage * credPageSize).map(c => (
                  <TableRow key={c.id} hover>
                    <TableCell>{c.name}</TableCell>
                    <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{c.provider}</TableCell>
                    <TableCell>
                      <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>
                        {c.keys.map(k => (
                          <Chip key={k} label={k} size="small" variant="outlined" sx={{ fontFamily: 'monospace', fontSize: 11, height: 22 }} />
                        ))}
                      </Box>
                    </TableCell>
                    <TableCell align="right">
                      <Tooltip title={t('common:actions.edit')}>
                        <IconButton size="small" onClick={() => openCred(c)}>
                          <EditIcon fontSize="small" />
                        </IconButton>
                      </Tooltip>
                      <Tooltip title={t('common:actions.delete')}>
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
          <PagedTableFooter
            total={creds.length} page={credPage} pageSize={credPageSize}
            onPageChange={setCredPage} onPageSizeChange={s => { setCredPageSize(s); setCredPage(1) }}
          />
        </Card>
      )}

      {/* ---- Tab 2: ACME accounts ---- */}
      {tab === 2 && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          {/* ACME CA accounts — a certificate issues under the account it selects */}
          <Card sx={{ bgcolor: md.surfaceContainerLow, boxShadow: '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)', overflow: 'hidden' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', p: 2 }}>
              <Box>
                <Typography sx={{ fontWeight: 600 }}>{t('admin:certs.acct_title', { defaultValue: 'ACME 账号' })}</Typography>
                <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:certs.acct_subtitle', { defaultValue: '管理多个 CA 账号（不同 CA / 联系邮箱 / EAB 凭据），证书签发时各自选用。' })}</Typography>
              </Box>
              <Button variant="contained" size="small" startIcon={<AddIcon />} onClick={() => openAcct()}>
                {t('admin:certs.acct_new', { defaultValue: '新建账号' })}
              </Button>
            </Box>
            <TableContainer>
              <Table>
                <TableHead>
                  <TableRow sx={{ '& th': { color: md.onSurfaceVariant, fontWeight: 500, fontSize: 12, textTransform: 'uppercase', letterSpacing: '.5px', borderBottom: `1px solid ${md.outlineVariant}` } }}>
                    <TableCell>{t('admin:certs.col_name')}</TableCell>
                    <TableCell>{t('admin:certs.acct_email', { defaultValue: '邮箱' })}</TableCell>
                    <TableCell>{t('admin:certs.acct_ca', { defaultValue: 'CA' })}</TableCell>
                    <TableCell>{t('admin:certs.acct_key_type', { defaultValue: '密钥类型' })}</TableCell>
                    <TableCell align="right">{t('admin:certs.col_actions')}</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {accounts.map(a => (
                    <TableRow key={a.id} hover>
                      <TableCell>{a.name || '—'}</TableCell>
                      <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{a.email}</TableCell>
                      <TableCell>
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexWrap: 'wrap' }}>
                          {caLabel(a.directory)}
                          {a.eab_key_id && <Chip label="EAB" size="small" variant="outlined" sx={{ height: 20, fontSize: 10 }} />}
                          {!a.registered && <Chip label={t('admin:certs.acct_unregistered', { defaultValue: '未注册' })} size="small" variant="outlined" sx={{ height: 20, fontSize: 10, color: md.onSurfaceVariant }} />}
                        </Box>
                      </TableCell>
                      <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{a.key_type || 'EC256'}</TableCell>
                      <TableCell align="right">
                        <Tooltip title={t('common:actions.edit')}>
                          <IconButton size="small" onClick={() => openAcct(a)}><EditIcon fontSize="small" /></IconButton>
                        </Tooltip>
                        <Tooltip title={t('common:actions.delete')}>
                          <IconButton size="small" onClick={() => onDeleteAcct(a)}><DeleteIcon fontSize="small" /></IconButton>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                  {accounts.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} align="center" sx={{ color: md.onSurfaceVariant }}>
                        {t('admin:certs.acct_empty', { defaultValue: '还没有 ACME 账号。新建一个后才能签发证书。' })}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </TableContainer>
          </Card>

          {/* Renewal cadence — panel-wide (not per-account) */}
          <Card sx={{ p: 3, maxWidth: 720, bgcolor: md.surfaceContainerLow, border: `1px solid ${md.outlineVariant}` }}>
            <Typography sx={{ fontSize: 18, fontWeight: 600, mb: 0.5 }}>{t('admin:certs.renew_title', { defaultValue: '续期' })}</Typography>
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant, mb: 2 }}>{t('admin:certs.renew_subtitle', { defaultValue: '自动续期的阈值与检查频率（对所有证书生效）。' })}</Typography>
            {!settings ? (
              <CircularProgress size={22} />
            ) : (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <Box sx={{ display: 'flex', gap: 2 }}>
                  <TextField
                    label={t('admin:certs.acme_renew_before_days')}
                    type="number"
                    value={settings.cert_renew_before_days}
                    onChange={e => patchSettings('cert_renew_before_days', Number(e.target.value))}
                    size="small"
                    sx={{ flex: 1 }}
                  />
                  <TextField
                    label={t('admin:certs.acme_renew_check_hours')}
                    type="number"
                    value={settings.cert_renew_check_interval_hours}
                    onChange={e => patchSettings('cert_renew_check_interval_hours', Number(e.target.value))}
                    size="small"
                    sx={{ flex: 1 }}
                  />
                </Box>
                <Box>
                  <Button variant="contained" onClick={saveACME} disabled={acmeBusy}>
                    {acmeBusy ? <CircularProgress size={20} /> : t('common:actions.save')}
                  </Button>
                </Box>
              </Box>
            )}
          </Card>
        </Box>
      )}

      {/* cert detail dialog */}
      <Dialog open={detailOpen} onClose={() => setDetailOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>{t('admin:certs.detail_title')}{detailCert ? ` — ${detailCert.name}` : ''}</DialogTitle>
        <DialogContent>
          {detailLoading || !detailCert ? (
            <Box sx={{ display: 'grid', placeItems: 'center', py: 3 }}><CircularProgress size={24} /></Box>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, pt: 1 }}>
              <DetailRow md={md} label={t('admin:certs.col_status')}>
                <Chip
                  label={t(`admin:certs.status.${detailCert.status}`, { defaultValue: detailCert.status })}
                  size="small"
                  sx={{ bgcolor: statusColor(md, detailCert.status), color: md.surface ?? '#fff', height: 22, cursor: 'default', '&:hover': { bgcolor: statusColor(md, detailCert.status) } }}
                />
              </DetailRow>
              <DetailRow md={md} label={t('admin:certs.col_domains')}>
                <Box sx={{ display: 'flex', gap: 0.5, flexWrap: 'wrap' }}>
                  {detailCert.domains.map(d => (
                    <Chip key={d} label={d} size="small" variant="outlined" sx={{ fontFamily: 'monospace', fontSize: 11, height: 22 }} />
                  ))}
                </Box>
              </DetailRow>
              <DetailRow md={md} label={t('admin:certs.not_before')}>{formatDualTz(detailCert.not_before, panelTz)}</DetailRow>
              <DetailRow md={md} label={t('admin:certs.col_expiry')}>{formatDualTz(detailCert.not_after, panelTz)}</DetailRow>
              {detailCert.fingerprint && (
                <DetailRow md={md} label={t('admin:certs.fingerprint')}>
                  <Typography sx={{ fontFamily: 'monospace', fontSize: 11, wordBreak: 'break-all', color: md.onSurfaceVariant }}>{detailCert.fingerprint}</Typography>
                </DetailRow>
              )}

              {detailCert.status === 'failed' && detailCert.last_error && (
                <Box sx={{ p: 1.5, borderRadius: 2, border: `1px solid ${md.error}` }}>
                  <Typography sx={{ fontSize: 12, fontWeight: 600, mb: 0.5, color: md.error }}>{t('admin:certs.failure_reason')}</Typography>
                  <Typography sx={{ fontSize: 12, fontFamily: 'monospace', wordBreak: 'break-word', color: md.onSurfaceVariant }}>{detailCert.last_error}</Typography>
                </Box>
              )}

              {detailCert.status === 'pending' && (
                <Box sx={{ p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainerHigh ?? 'rgba(255,255,255,.04)' }}>
                  <Typography sx={{ fontSize: 12, fontWeight: 600, mb: 0.5 }}>{t('admin:certs.progress')}</Typography>
                  {detailTask ? (
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                      {t('admin:certs.progress_task', { status: detailTask.status, attempts: detailTask.attempts })}
                      {detailTask.next_run_at ? ` · ${t('admin:certs.next_retry')}: ${formatDualTz(detailTask.next_run_at, panelTz)}` : ''}
                      {detailTask.last_error ? ` · ${detailTask.last_error}` : ''}
                    </Typography>
                  ) : (
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:certs.progress_queued')}</Typography>
                  )}
                </Box>
              )}

              {detailCert.fingerprint && (
                <>
                  <Divider sx={{ my: 0.5 }} />
                  <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
                    <Button size="small" variant="outlined" startIcon={<VisibilityIcon />} onClick={() => openPEM(detailCert.id)}>
                      {t('admin:certs.view_pem')}
                    </Button>
                    <Button size="small" variant="outlined" startIcon={<DownloadIcon />} disabled={downloading} onClick={() => onDownloadPEM(detailCert.id, 'cert')}>
                      {t('admin:certs.download_cert')}
                    </Button>
                    <Button size="small" variant="outlined" startIcon={<DownloadIcon />} disabled={downloading} onClick={() => onDownloadPEM(detailCert.id, 'key')} sx={{ color: md.error, borderColor: md.error }}>
                      {t('admin:certs.download_key')}
                    </Button>
                  </Box>
                </>
              )}
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setDetailOpen(false)}>{t('common:actions.close')}</Button>
        </DialogActions>
      </Dialog>

      {/* PEM viewer — reveal full chain + private key, copyable (explicit admin action) */}
      <Dialog open={pemOpen} onClose={() => setPemOpen(false)} maxWidth="md" fullWidth>
        <DialogTitle>{t('admin:certs.pem_title')}{pemData ? ` — ${pemData.name}` : ''}</DialogTitle>
        <DialogContent>
          {pemLoading || !pemData ? (
            <Box sx={{ display: 'grid', placeItems: 'center', py: 3 }}><CircularProgress size={24} /></Box>
          ) : (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2, pt: 1 }}>
              <PemBox md={md} label={t('admin:certs.cert_chain_label')} value={pemData.cert_pem} copyTitle={t('admin:certs.copy_cert')} />
              <PemBox md={md} label={t('admin:certs.private_key_label')} value={pemData.key_pem} copyTitle={t('admin:certs.copy_key')} danger />
            </Box>
          )}
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPemOpen(false)}>{t('common:actions.close')}</Button>
        </DialogActions>
      </Dialog>

      {/* create / edit cert dialog */}
      <Dialog open={certOpen} onClose={() => setCertOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>{certEditing ? t('admin:certs.edit', { defaultValue: '编辑证书' }) : t('admin:certs.new')}</DialogTitle>
        <DialogContent>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
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
            <InputLabel>{t('admin:certs.acct_title', { defaultValue: 'ACME 账号' })}</InputLabel>
            <Select label={t('admin:certs.acct_title', { defaultValue: 'ACME 账号' })} value={certAccountId} onChange={e => setCertAccountId(e.target.value as number)}>
              {accounts.map(a => (
                <MenuItem key={a.id} value={a.id}>
                  {a.name || a.email} — {caLabel(a.directory)}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
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
          <FormControlLabel control={<Switch checked={certAutoRenew} onChange={e => setCertAutoRenew(e.target.checked)} />}
            label={t('admin:certs.auto_renew')}
            sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1 } }} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCertOpen(false)}>{t('common:actions.cancel')}</Button>
          <Button variant="contained" onClick={submitCert} disabled={certBusy}>
            {certBusy ? <CircularProgress size={20} /> : (certEditing ? t('common:actions.save') : t('common:actions.create'))}
          </Button>
        </DialogActions>
      </Dialog>

      {/* create/edit ACME account dialog */}
      <Dialog open={acctOpen} onClose={() => setAcctOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>{acctEditing ? t('admin:certs.acct_edit', { defaultValue: '编辑 ACME 账号' }) : t('admin:certs.acct_new', { defaultValue: '新建账号' })}</DialogTitle>
        <DialogContent>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
            <TextField label={t('admin:certs.col_name')} value={acctName} onChange={e => setAcctName(e.target.value)} size="small" autoFocus
              placeholder={t('admin:certs.acct_name_ph', { defaultValue: "例如：Let's Encrypt — ops@team.com" })} />
            <TextField label={t('admin:certs.acct_email', { defaultValue: '邮箱' })} value={acctEmail} onChange={e => setAcctEmail(e.target.value)} size="small"
              placeholder="you@example.com" helperText={t('admin:certs.acme_email_hint')} />
            <TextField label={t('admin:certs.acct_directory', { defaultValue: 'CA Directory URL' })} value={acctDir} onChange={e => setAcctDir(e.target.value)} size="small"
              helperText={t('admin:certs.acme_directory_hint')} />
            <Box sx={{ display: 'flex', gap: 1, mt: -1, flexWrap: 'wrap' }}>
              {CA_PRESETS.map(p => (
                <Button key={p.url} size="small" variant={acctDir === p.url ? 'contained' : 'outlined'} onClick={() => setAcctDir(p.url)}>
                  {p.label}
                </Button>
              ))}
            </Box>
            <FormControl size="small">
              <InputLabel>{t('admin:certs.acct_key_type', { defaultValue: '密钥类型' })}</InputLabel>
              <Select label={t('admin:certs.acct_key_type', { defaultValue: '密钥类型' })} value={acctKeyType} onChange={e => setAcctKeyType(e.target.value)}>
                {(keyTypes.length ? keyTypes : ['EC256', 'EC384', 'RSA2048', 'RSA4096']).map(k => (
                  <MenuItem key={k} value={k}>{k}</MenuItem>
                ))}
              </Select>
            </FormControl>
            <Divider sx={{ borderColor: md.outlineVariant }} />
            <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
              {t('admin:certs.acct_eab_hint', { defaultValue: '外部账号绑定（EAB）—— ZeroSSL、Google Public CA 等需要；Let\'s Encrypt 留空即可。' })}
            </Typography>
            <TextField label={t('admin:certs.acct_eab_kid', { defaultValue: 'EAB Key ID' })} value={acctEABKid} onChange={e => setAcctEABKid(e.target.value)} size="small" />
            <TextField label={t('admin:certs.acct_eab_hmac', { defaultValue: 'EAB HMAC Key' })} value={acctEABHmac} onChange={e => setAcctEABHmac(e.target.value)} size="small"
              type="password" autoComplete="new-password"
              placeholder={acctEditing?.has_eab_hmac ? t('admin:certs.secret_keep', { defaultValue: '已设置 — 留空保持不变' }) : ''} />
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setAcctOpen(false)}>{t('common:actions.cancel')}</Button>
          <Button variant="contained" onClick={submitAcct} disabled={acctBusy}>
            {acctBusy ? <CircularProgress size={20} /> : t('common:actions.save')}
          </Button>
        </DialogActions>
      </Dialog>

      {/* create/edit DNS credential dialog */}
      <Dialog open={credOpen} onClose={() => setCredOpen(false)} maxWidth="sm" fullWidth>
        <DialogTitle>{credEditing ? t('common:actions.edit') : t('admin:certs.cred_new')}</DialogTitle>
        <DialogContent>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5, pt: 1 }}>
          <TextField label={t('admin:certs.col_name')} value={credName} onChange={e => setCredName(e.target.value)} size="small" autoFocus />
          <Autocomplete
            freeSolo
            options={providers.map(p => p.name)}
            value={credProvider}
            getOptionLabel={name => {
              const info = providers.find(p => p.name === name)
              return info ? `${info.label} (${info.name})` : name
            }}
            onChange={(_, v) => changeProvider(v ?? '')}
            onInputChange={(_, v, reason) => {
              if (reason === 'input') changeProvider(v)
            }}
            renderInput={params => <TextField {...params} label={t('admin:certs.cred_provider')} size="small" helperText={t('admin:certs.provider_help')} />}
          />

          {credEditing && isCustomProvider && (
            <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('admin:certs.cred_edit_keep_hint')}</Typography>
          )}

          {/* Named fields for a curated provider; free-form KV for exec/httpreq/unknown. */}
          {!isCustomProvider ? (
            (selProvider!.fields ?? []).map(f => {
              // Stored secret on an existing credential: show the "saved / Change"
              // state (the app's write-only-secret convention, same as the SMTP
              // password) instead of a blank input — until the admin clicks Change
              // to enter a new value.
              const stored = !!credEditing && credEditing.keys.includes(f.key) && !credChanged.has(f.key)
              if (stored) {
                return (
                  <Box key={f.key}>
                    <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant, mb: 0.5 }}>{f.label}</Typography>
                    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1.5, minHeight: 40, px: 1.75, py: 0.5, borderRadius: 1.5, border: `1px solid ${md.outlineVariant}` }}>
                      <Typography variant="body2">{t('admin:certs.field_kept')}</Typography>
                      <Button size="small" variant="text" onClick={() => setCredChanged(prev => new Set(prev).add(f.key))}>
                        {t('admin:certs.field_change')}
                      </Button>
                    </Box>
                  </Box>
                )
              }
              return (
                <TextField
                  key={f.key}
                  label={f.label + (f.optional ? ` (${t('admin:certs.optional')})` : '')}
                  value={credValues[f.key] ?? ''}
                  onChange={e => setCredValues(prev => ({ ...prev, [f.key]: e.target.value }))}
                  size="small"
                  type={f.secret ? 'password' : 'text'}
                  helperText={f.key}
                />
              )
            })
          ) : (
            <>
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
            </>
          )}
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setCredOpen(false)}>{t('common:actions.cancel')}</Button>
          <Button variant="contained" onClick={submitCred} disabled={credBusy}>
            {credBusy ? <CircularProgress size={20} /> : t('common:actions.save')}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  )
}
