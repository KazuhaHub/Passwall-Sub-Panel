import { useState } from 'react'
import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Link, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { acceptLegal, outdatedConsentVersion } from '@/api/legal'
import { panelURL } from '@/panelPath'
import { meKeys } from '@/query/keys'
import { useMyProfile } from '@/query/me'
import { useQueryScope } from '@/query/useQueryScope'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { pushSnack } from '@/components/SnackbarHost'

const LATER_KEY = 'psp_legal_later'

// "Later" is remembered for this browser session only, per account and consent
// version: a new version asks again. Storage can be blocked, and then the dialog
// simply asks again on the next load.
function laterToken(userId: number | null, version: number) { return `${userId ?? 0}:${version}` }
function readLater(): string | null {
  try { return sessionStorage.getItem(LATER_KEY) } catch { return null }
}
function writeLater(token: string) {
  try { sessionStorage.setItem(LATER_KEY, token) } catch { /* the next load asks again */ }
}

/**
 * Asks an account of role user to consent to the current terms and privacy
 * policy while the profile says it is pending. Staff are never asked, and the
 * profile is not read for the question when the feature is off. Nothing is
 * gated on the answer: Later only closes it for this session.
 */
export default function ConsentDialog() {
  const role = useAuthStore(s => s.role)
  const enabled = useSiteStore(s => s.legalEnabled)
  if (role !== 'user' || !enabled) return null
  return <PendingConsent />
}

function PendingConsent() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('user')
  const scope = useQueryScope()
  const qc = useQueryClient()
  const userId = useAuthStore(s => s.userId)
  const siteVersion = useSiteStore(s => s.legalConsentVersion)
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const profile = useMyProfile(scope)

  // A 409 carries the version that is current now; it replaces the one the page
  // was loaded with.
  const [version, setVersion] = useState(siteVersion)
  const [dismissed, setDismissed] = useState(() => readLater() === laterToken(userId, siteVersion))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!profile.data?.legal_pending || dismissed) return null

  function later() {
    writeLater(laterToken(userId, version))
    setDismissed(true)
  }

  async function accept() {
    setBusy(true)
    setError('')
    try {
      await acceptLegal(version)
      pushSnack(t('legal.consent_accepted'), 'success')
      await qc.invalidateQueries({ queryKey: meKeys.profile(scope) })
    } catch (err) {
      const current = outdatedConsentVersion(err)
      if (current !== null) {
        if (current > 0) setVersion(current)
        setError(t('legal.consent_outdated'))
      } else {
        setError(t('legal.consent_failed'))
      }
    } finally {
      setBusy(false)
    }
  }

  const link = (kind: 'terms' | 'privacy', label: string) => (
    <Link href={panelURL(`/legal/${kind}`)} target="_blank" rel="noopener noreferrer"
      sx={{ display: 'inline-flex', alignItems: 'center', minHeight: phone ? 44 : 'auto', mr: 2 }}>{label}</Link>
  )

  return (
    <Dialog open maxWidth="sm" fullWidth fullScreen={phone}
      // Only Esc closes it, and it means Later: a click on the backdrop is easy
      // to make by accident and would record a decision nobody made.
      onClose={(_, reason) => { if (reason === 'escapeKeyDown') later() }}
      aria-labelledby="consent-title">
      <DialogTitle id="consent-title">{t('legal.consent_title')}</DialogTitle>
      <DialogContent>
        <Typography sx={{ color: md.onSurfaceVariant }}>{t('legal.consent_body')}</Typography>
        <Box sx={{ mt: 1.5 }}>
          {link('terms', t('legal.consent_terms'))}
          {link('privacy', t('legal.consent_privacy'))}
        </Box>
        {error && <Alert severity="error" sx={{ mt: 2 }}>{error}</Alert>}
      </DialogContent>
      <DialogActions sx={{ px: 3, pb: 2, ...(phone ? { flexDirection: 'column', alignItems: 'stretch', gap: 1, '& > :not(style) ~ :not(style)': { ml: 0 } } : {}) }}>
        <Button fullWidth={phone} onClick={later} disabled={busy} sx={phone ? { minHeight: 44 } : undefined}>{t('legal.consent_later')}</Button>
        <Button fullWidth={phone} variant="contained" onClick={() => void accept()} disabled={busy} sx={phone ? { minHeight: 44 } : undefined}>
          {t('legal.consent_accept')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
