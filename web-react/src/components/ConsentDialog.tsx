import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { acceptLegal, outdatedConsentVersion } from '@/api/legal'
import { panelURL } from '@/panelPath'
import { meKeys } from '@/query/keys'
import { useMyProfile } from '@/query/me'
import { useQueryScope } from '@/query/useQueryScope'
import { useAuthStore } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { pushSnack } from '@/components/feedback/toasts'
import { AsyncButton } from '@/components/common/AsyncButton'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

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
  const { t } = useTranslation('user')
  const scope = useQueryScope()
  const qc = useQueryClient()
  const userId = useAuthStore(s => s.userId)
  const siteVersion = useSiteStore(s => s.legalConsentVersion)
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
    <a href={panelURL(`/legal/${kind}`)} target="_blank" rel="noopener noreferrer"
      className="inline-flex min-h-11 items-center text-primary underline-offset-4 hover:underline sm:min-h-0">{label}</a>
  )

  return (
    <Dialog open
      // Only Esc closes it, and it means Later: a click outside is easy to make by
      // accident and would record a decision nobody made.
      disablePointerDismissal
      onOpenChange={(open, details) => { if (!open && details.reason === 'escape-key') later() }}>
      {/* Full screen on a phone, a dialog from sm up. No close button: Later is the way out. */}
      <DialogContent showCloseButton={false}
        className="max-sm:inset-0 max-sm:top-0 max-sm:left-0 max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-none max-sm:content-start sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('legal.consent_title')}</DialogTitle>
          <DialogDescription>{t('legal.consent_body')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap gap-x-4">
          {link('terms', t('legal.consent_terms'))}
          {link('privacy', t('legal.consent_privacy'))}
        </div>
        {error && <Alert variant="destructive">{error}</Alert>}
        {/* Later above Agree when stacked, Later left of Agree from sm up. */}
        <DialogFooter className="flex-col sm:flex-row max-sm:mt-auto">
          <Button variant="outline" onClick={later} disabled={busy} className="max-sm:h-11">{t('legal.consent_later')}</Button>
          <AsyncButton onClick={() => accept()} pending={busy} className="max-sm:h-11">{t('legal.consent_accept')}</AsyncButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
