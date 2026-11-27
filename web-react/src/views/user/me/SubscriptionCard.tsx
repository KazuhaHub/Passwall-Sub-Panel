import { useState } from 'react'
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
 * What the blurred QR frame draws while the link is hidden. It looks like a code (which is the point: the person sees a
 * code to uncover) but encodes nothing of theirs, so the real link is never in the page until it is revealed: a blurred
 * SVG of the real code would still carry the whole link in the markup. Fixed text, so its density does not hint at how
 * long the link is either.
 */
const DECOY = 'https://subscription.example.invalid/0123456789abcdef0123456789abcdef'

/**
 * THE SUBSCRIPTION LINK. It is a credential: anyone who has it can use the account's traffic, so it is hidden by
 * default (a screenshot or a screen share must not leak it) and the QR code, which encodes the same link, stays blurred
 * until the person reveals it, from either eye. Copying works without revealing: the clipboard is private. The link is
 * never put in the page address, a toast or a log; it only lives in this card.
 *
 * Layout is the long-standing one: the QR frame on the left, the link row with its eye and copy buttons on the right,
 * the hint under it and the two actions that change what the link serves (personal rules, reset) at its foot. On a phone
 * the frame is centred above the link.
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
  // "Shown" is remembered FOR the link and reload it was shown for, not as a bare flag reset by an effect: an effect runs
  // after the render, so a changed link would be drawn in clear for one frame first.
  const key = `${maskSignal}|${subUrl}`
  const [shownFor, setShownFor] = useState<string | null>(null)
  const revealed = shownFor === key
  const setRevealed = (v: boolean) => setShownFor(v ? key : null)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('sub.title')}</CardTitle>
        <CardDescription>{t('sub.intro')}</CardDescription>
      </CardHeader>
      <CardContent className="grid grid-cols-1 gap-5 sm:grid-cols-[auto_1fr]">
        {/* A QR code needs a white ground and dark modules in both themes, so the frame is white on purpose. */}
        <div className="relative justify-self-center rounded-lg border bg-white p-3 leading-none sm:justify-self-auto">
          {revealed ? (
            <QRCodeSVG value={subUrl} size={160} level="M" className="size-40 sm:size-[140px]" />
          ) : (
            <div data-testid="qr-placeholder" aria-hidden className="blur-[8px]">
              <QRCodeSVG value={DECOY} size={160} level="M" className="size-40 sm:size-[140px]" />
            </div>
          )}
          {!revealed && (
            <button
              type="button" aria-label={t('sub.reveal')} onClick={() => setRevealed(true)}
              className="absolute inset-0 grid cursor-pointer place-items-center rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              {/* On a fixed dark chip so the eye reads in both themes: the frame is white either way. */}
              <span className="grid size-12 place-items-center rounded-full bg-black/55 text-white shadow-md"><IconShow className="size-6" /></span>
            </button>
          )}
        </div>
        <div className="flex min-w-0 flex-col">
          <div className="flex items-center gap-1 rounded-lg bg-muted py-1 pr-1 pl-3 text-sm">
            {/* The whole link is one unbroken string: break-all so a long token wraps instead of widening the page. */}
            <span data-testid="sub-url" className="min-w-0 flex-1 font-mono text-[0.8125rem] break-all">{revealed ? subUrl : maskUrl(subUrl)}</span>
            <IconButton label={revealed ? t('sub.hide') : t('sub.reveal')} onClick={() => setRevealed(!revealed)}>
              {revealed ? <IconHide /> : <IconShow />}
            </IconButton>
            <CopyButton value={subUrl} name={t('sub.title')} size="icon" />
          </div>
          <p className="mt-3 text-xs text-muted-foreground">{revealed ? t('sub.qr_hint') : t('sub.masked_hint')}</p>
          {/* The actions sit under the hint, in the column the taller QR frame leaves empty anyway, rather than in a footer band. */}
          <div className="mt-auto flex flex-wrap justify-end gap-1 pt-4">
            <Button variant="ghost" size="sm" onClick={onOpenRules}><IconRules />{t('actions.personal_rules')}</Button>
            {/* The reset awaits the confirm, the request and the reload; AsyncButton stays busy through all of it, so a slow
                link cannot invite a second, overlapping reset. It needs the handler to RETURN that promise. */}
            <AsyncButton variant="ghost" size="sm" className="text-destructive hover:text-destructive" icon={<IconKey />} onClick={onReset}>{t('sub.reset')}</AsyncButton>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
