import { useEffect, useState } from 'react'
import { QRCodeSVG } from 'qrcode.react'
import { IconHide, IconKey, IconRules, IconShow } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { CopyButton } from '@/components/common/CopyButton'
import { IconButton } from '@/components/common/IconButton'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { maskUrl } from './format'
import { useMeT } from './useMeT'

/**
 * THE SUBSCRIPTION LINK. It is a credential: anyone who has it can use the account's traffic, so it is hidden by
 * default (a screenshot or a screen share must not leak it) and the QR code, which encodes the same link, stays blurred
 * until the person reveals it. Copying works without revealing: the clipboard is private. The link is never put in the
 * page address, a toast or a log; it only lives in this card.
 *
 * `maskSignal` changes whenever the profile is reloaded, which re-hides the link: after a credential reset the link is a
 * different one, and leaving the "shown" flag on would display the new secret the moment it arrives.
 */
export function SubscriptionCard({ subUrl, maskSignal, onReset, onOpenRules }: {
  subUrl: string
  maskSignal: number
  /** Confirms, resets and reloads; the returned promise keeps the button busy until it is all done. */
  onReset: () => Promise<void>
  onOpenRules: () => void
}) {
  const { t } = useMeT()
  const [revealed, setRevealed] = useState(false)
  useEffect(() => { setRevealed(false) }, [maskSignal, subUrl])

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('sub.title')}</CardTitle>
        <CardDescription>{t('sub.intro')}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center gap-1 rounded-lg bg-muted py-1 pr-1 pl-3 text-sm">
          {/* The whole link is one unbroken string: break-all so a long token wraps instead of widening the page. */}
          <span data-testid="sub-url" className="min-w-0 flex-1 font-mono text-[0.8125rem] break-all">{revealed ? subUrl : maskUrl(subUrl)}</span>
          <IconButton label={revealed ? t('sub.hide') : t('sub.reveal')} onClick={() => setRevealed(v => !v)}>
            {revealed ? <IconHide /> : <IconShow />}
          </IconButton>
          <CopyButton value={subUrl} name={t('sub.title')} size="icon" />
        </div>
        <div className="flex flex-col items-center gap-2">
          {/* A QR code needs a white ground and dark modules in both themes, so the frame is white on purpose. */}
          <div className="relative rounded-lg border bg-white p-3 leading-none">
            <div className={revealed ? undefined : 'blur-[8px]'} aria-hidden={!revealed}>
              <QRCodeSVG value={subUrl} size={160} level="M" />
            </div>
            {!revealed && (
              <button
                type="button" aria-label={t('sub.reveal')} onClick={() => setRevealed(true)}
                className="absolute inset-0 grid place-items-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <span className="grid size-12 place-items-center rounded-full bg-black/55 text-white shadow-md"><IconShow className="size-6" /></span>
              </button>
            )}
          </div>
          <p className="text-center text-xs text-muted-foreground">{revealed ? t('sub.qr_hint') : t('sub.masked_hint')}</p>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onOpenRules}><IconRules />{t('actions.personal_rules')}</Button>
          {/* The reset awaits the confirm, the request and the reload; AsyncButton stays busy through all of it, so a slow
              link cannot invite a second, overlapping reset. It needs the handler to RETURN that promise. */}
          <AsyncButton variant="destructive" size="sm" icon={<IconKey />} onClick={onReset}>{t('sub.reset')}</AsyncButton>
        </div>
      </CardContent>
    </Card>
  )
}
