import { useEffect, useState } from 'react'
import type { GlobalAnnouncement } from '@/api/me'
import { Notice } from '@/components/common/Notice'
import type { Tone } from '@/components/common/tones'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useMeT } from './useMeT'

/** The browser-local "do not remind me" stamp. It holds the announcement's updated_at, so editing the announcement re-shows it for everyone. */
const DISMISS_KEY = 'psp.announce.dismiss'

const TONE_OF: Record<string, Tone> = { danger: 'danger', warning: 'warning' }
const toneOf = (level: string): Tone => TONE_OF[level] ?? 'info'

/**
 * A coloured banner with a bold title and its sentence, at the very top of the page, the way the portal always showed
 * an announcement and a service hold. The tint is the tone's and the icon carries it too (Notice), so it is never colour alone.
 */
export function Banner({ tone, title, children, testId }: { tone: Exclude<Tone, 'neutral'>; title: string; children?: string; testId?: string }) {
  return (
    <div data-testid={testId} data-tone={tone}>
      {/* A polite status for every tone: a banner is already on the page when it loads, so it is read in place and does not interrupt; a heading, so heading navigation finds it. */}
      <Notice tone={tone} role="status">
        <h2 className="font-medium">{title}</h2>
        {children && <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground">{children}</p>}
      </Notice>
    </div>
  )
}

/** The admin's announcement, always shown inline while it is on. */
export function AnnouncementBanner({ announcement }: { announcement: GlobalAnnouncement }) {
  const tone = toneOf(announcement.level)
  return <Banner tone={tone === 'neutral' ? 'info' : tone} title={announcement.title} testId="announcement">{announcement.content}</Banner>
}

/**
 * The same announcement as a popup, when the admin opted in (`popup`). It opens once the profile has loaded unless this
 * browser chose "don't show again" for this exact version. The effect depends on the announcement's IDENTITY, not on
 * the profile object: every refresh replaces the profile with a new object, which would otherwise re-open a popup that
 * was closed for this session only. A real edit changes updated_at or the title and does re-open it.
 */
export function AnnouncementPopup({ announcement }: { announcement: GlobalAnnouncement | null | undefined }) {
  const { t } = useMeT()
  const [open, setOpen] = useState(false)
  const eligible = !!announcement?.enabled && !!announcement.popup && !!announcement.title
  const stamp = announcement?.updated_at || announcement?.title || ''

  useEffect(() => {
    if (!eligible) return
    try {
      if (localStorage.getItem(DISMISS_KEY) === stamp) return
    } catch { /* storage unavailable: show it */ }
    setOpen(true)
  }, [eligible, stamp])

  function close(persist: boolean) {
    if (persist) {
      try { localStorage.setItem(DISMISS_KEY, stamp) } catch { /* ignore */ }
    }
    setOpen(false)
  }

  if (!announcement || !eligible) return null
  return (
    <Dialog open={open} onOpenChange={o => { if (!o) close(false) }}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{announcement.title}</DialogTitle>
          <DialogDescription className="whitespace-pre-wrap">{announcement.content}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => close(true)}>{t('announce.mute')}</Button>
          <Button onClick={() => close(false)}>{t('announce.ack')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
