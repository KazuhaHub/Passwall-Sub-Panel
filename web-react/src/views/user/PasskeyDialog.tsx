import { useEffect, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  List,
  ListItem,
  ListItemText,
  TextField,
  Tooltip,
  Typography,
} from '@mui/material'
import AddIcon from '@mui/icons-material/Add'
import DeleteIcon from '@mui/icons-material/DeleteOutlined'
import EditIcon from '@mui/icons-material/EditOutlined'
import CheckIcon from '@mui/icons-material/Check'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import { startRegistration } from '@simplewebauthn/browser'
import { useTranslation } from 'react-i18next'
import type { AxiosError } from 'axios'

import { beginPasskeyEnroll, deletePasskey, finishPasskeyEnroll, renamePasskey } from '@/api/me'
import type { PasskeyCredential } from '@/api/types'
import type { M3Tokens } from '@/theme'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import { copyToClipboard } from '@/utils/clipboard'

interface Props {
  open: boolean
  available: boolean
  credentials: PasskeyCredential[]
  md: M3Tokens
  onClose: () => void
  onChanged: () => void
}

// PasskeyDialog manages a user's registered passkeys: list, rename, delete, and
// register a new one (which runs the browser WebAuthn create() ceremony).
export default function PasskeyDialog({ open, available, credentials, md, onClose, onChanged }: Props) {
  const { t } = useTranslation('user')
  const [busy, setBusy] = useState(false)
  const [adding, setAdding] = useState(false)
  const [newName, setNewName] = useState('')
  const [editId, setEditId] = useState<number | null>(null)
  const [editName, setEditName] = useState('')
  // One-time recovery codes returned when this is the account's FIRST passkey —
  // a passkey is a second factor, so the user needs a printable fallback. Shown
  // until acknowledged; never re-fetchable (stored hashed server-side).
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)

  useEffect(() => {
    if (open) {
      setAdding(false)
      setNewName('')
      setEditId(null)
      setRecoveryCodes(null)
    }
  }, [open])

  function inlineErr(err: unknown, fallbackKey: string) {
    const e = err as AxiosError<{ error?: string }>
    pushSnack(e.response?.data?.error || t(fallbackKey), 'error')
  }

  async function addPasskey() {
    const name = newName.trim() || t('passkey.default_name')
    setBusy(true)
    try {
      const { session_id, publicKey } = await beginPasskeyEnroll()
      const attResp = await startRegistration({ optionsJSON: publicKey })
      const { recovery_codes } = await finishPasskeyEnroll(session_id, name, attResp)
      pushSnack(t('passkey.added'), 'success')
      setAdding(false)
      setNewName('')
      onChanged()
      // First passkey → show the one-time recovery codes before anything else.
      if (recovery_codes && recovery_codes.length > 0) {
        setRecoveryCodes(recovery_codes)
      }
    } catch (err) {
      const errName = (err as { name?: string })?.name
      // Quietly ignore a user-cancelled browser prompt.
      if (errName !== 'NotAllowedError' && errName !== 'AbortError') {
        inlineErr(err, 'passkey.add_failed')
      }
    } finally {
      setBusy(false)
    }
  }

  async function saveRename(id: number) {
    const name = editName.trim()
    if (!name) {
      setEditId(null)
      return
    }
    setBusy(true)
    try {
      await renamePasskey(id, name)
      setEditId(null)
      onChanged()
    } catch (err) {
      inlineErr(err, 'passkey.rename_failed')
    } finally {
      setBusy(false)
    }
  }

  async function removePasskey(c: PasskeyCredential) {
    const ok = await confirm({
      title: t('passkey.confirm_delete_title'),
      message: t('passkey.confirm_delete_message', { name: c.name }),
      destructive: true,
      confirmText: t('passkey.delete'),
    })
    if (!ok) return
    setBusy(true)
    try {
      await deletePasskey(c.id)
      pushSnack(t('passkey.deleted'), 'success')
      onChanged()
    } catch (err) {
      inlineErr(err, 'passkey.delete_failed')
    } finally {
      setBusy(false)
    }
  }

  // First-passkey recovery codes take over the dialog until acknowledged so they
  // can't be lost to a stray backdrop click (they're shown only once).
  if (recoveryCodes) {
    return (
      <Dialog open={open} onClose={() => { /* force the explicit button */ }} fullWidth maxWidth="xs"
        slotProps={{
          paper: { sx: { bgcolor: md.surfaceContainerHigh } }
        }}>
        <DialogTitle>{t('passkey.recovery_title', { defaultValue: '保存你的备用码' })}</DialogTitle>
        <DialogContent>
          <Alert severity="warning" sx={{ mb: 2 }}>
            {t('passkey.recovery_warning', { defaultValue: '这是你的一次性备用码——当你无法使用通行密钥时，可用它登录。请妥善保存，它只显示这一次。' })}
          </Alert>
          <Box sx={{
            p: 2, bgcolor: md.surfaceContainerHighest, borderRadius: 1,
            fontFamily: 'monospace', fontSize: 14, lineHeight: 1.9,
            display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 0.5,
          }}>
            {recoveryCodes.map(c => <Box key={c}>{c}</Box>)}
          </Box>
          <Button size="small" startIcon={<ContentCopyIcon fontSize="small" />} sx={{ mt: 1.5 }}
            onClick={() => { void copyToClipboard(recoveryCodes.join('\n')) }}>
            {t('passkey.copy_all', { defaultValue: '全部复制' })}
          </Button>
        </DialogContent>
        <DialogActions>
          <Button variant="contained" onClick={() => setRecoveryCodes(null)}>
            {t('passkey.recovery_done', { defaultValue: '我已保存' })}
          </Button>
        </DialogActions>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onClose={() => !busy && onClose()} fullWidth maxWidth="xs"
      slotProps={{
        paper: { sx: { bgcolor: md.surfaceContainerHigh } }
      }}>
      <DialogTitle>{t('passkey.title')}</DialogTitle>
      <DialogContent>
        <Typography variant="body2" sx={{ color: md.onSurfaceVariant, mb: 1.5 }}>
          {t('passkey.intro')}
        </Typography>

        {credentials.length === 0 && !adding && (
          <Typography variant="body2" sx={{ color: md.onSurfaceVariant, py: 2, textAlign: 'center' }}>
            {t('passkey.list_empty')}
          </Typography>
        )}

        <List dense disablePadding>
          {credentials.map(c => (
            <ListItem key={c.id} disableGutters
              secondaryAction={editId === c.id ? (
                <IconButton edge="end" onClick={() => saveRename(c.id)} disabled={busy}>
                  <CheckIcon fontSize="small" />
                </IconButton>
              ) : (
                <Box>
                  <Tooltip title={t('passkey.rename')}>
                    <IconButton edge="end" onClick={() => { setEditId(c.id); setEditName(c.name) }} disabled={busy}>
                      <EditIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                  <Tooltip title={t('passkey.delete')}>
                    <IconButton edge="end" onClick={() => removePasskey(c)} disabled={busy}>
                      <DeleteIcon fontSize="small" />
                    </IconButton>
                  </Tooltip>
                </Box>
              )}>
              {editId === c.id ? (
                <TextField size="small" fullWidth autoFocus value={editName}
                  onChange={e => setEditName(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter') void saveRename(c.id) }}
                  sx={{ mr: 6 }} />
              ) : (
                <ListItemText
                  primary={c.name}
                  secondary={t('passkey.added_at', { date: new Date(c.created_at).toLocaleDateString() })}
                  slotProps={{
                    primary: { sx: { fontSize: 14 } },
                    secondary: { sx: { fontSize: 12 } }
                  }} />
              )}
            </ListItem>
          ))}
        </List>

        {adding && (
          <Box sx={{ mt: 2, display: 'flex', flexDirection: 'column', gap: 1.5 }}>
            <TextField size="small" fullWidth autoFocus
              label={t('passkey.name_label')}
              placeholder={t('passkey.default_name')}
              value={newName}
              onChange={e => setNewName(e.target.value)} />
            <Box sx={{ display: 'flex', gap: 1, justifyContent: 'flex-end' }}>
              <Button size="small" onClick={() => { setAdding(false); setNewName('') }} disabled={busy}>
                {t('passkey.cancel')}
              </Button>
              <Button size="small" variant="contained" onClick={addPasskey} disabled={busy}
                startIcon={busy ? <CircularProgress size={14} color="inherit" /> : undefined}>
                {t('passkey.continue')}
              </Button>
            </Box>
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        {available && !adding && (
          <Button startIcon={<AddIcon />} onClick={() => setAdding(true)} disabled={busy}>
            {t('passkey.add')}
          </Button>
        )}
        <Box sx={{ flex: 1 }} />
        <Button onClick={onClose} disabled={busy}>{t('passkey.close')}</Button>
      </DialogActions>
    </Dialog>
  );
}
