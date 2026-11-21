import { IconCopy, IconDownload, IconWarning } from '@/lib/icons'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { copyToClipboard } from '@/utils/clipboard'
import { useMeT } from './me/useMeT'

/** Saves text as a file through a temporary link; used for the one-time codes so they can be kept off the screen. */
function downloadText(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/**
 * The one-time recovery codes, with their warning, a copy-all and a download. The server returns them exactly once and
 * stores only hashes, so every dialog that shows them keeps itself open until the person says they are saved; this is
 * the one place they are drawn, so the three flows that hand them out (turning on the authenticator, the first passkey,
 * regenerating) cannot differ in how they are shown or saved.
 */
export function RecoveryCodeList({ codes, warning }: { codes: string[]; warning: string }) {
  const { t } = useMeT()
  const text = codes.join('\n')
  return (
    <div className="flex flex-col gap-3">
      <Alert><IconWarning /><AlertDescription>{warning}</AlertDescription></Alert>
      <ul data-testid="recovery-codes" className="grid grid-cols-2 gap-x-3 gap-y-1 rounded-lg bg-muted p-3 font-mono text-sm leading-relaxed">
        {codes.map(c => <li key={c}>{c}</li>)}
      </ul>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => { void copyToClipboard(text) }}><IconCopy />{t('twofa.copy_all')}</Button>
        <Button type="button" variant="outline" size="sm" onClick={() => downloadText('recovery-codes.txt', text + '\n')}><IconDownload />{t('twofa.download')}</Button>
      </div>
    </div>
  )
}
