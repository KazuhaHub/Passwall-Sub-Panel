import { useEffect, useState } from 'react'
import type { GlobalAnnouncement } from '@/api/me'
import { StatusDot } from '@/components/common/StatusDot'
import type { Tone } from '@/components/common/tones'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { TONE_STYLE } from '@/components/common/tones'
import { useMeT } from './useMeT'

/** The browser-local "do not remind me" stamp. It holds the announcement's updated_at, so editing the announcement re-shows it for everyone. */
const DISMISS_KEY = 'psp.announce.dismiss'

const TONE_OF: Record<string, Tone> = { danger: 'danger', warning: 'warning' }
const toneOf = (level: string): Tone => TONE_OF[level] ?? 'info'

/** The admin's announcement, always shown inline while it is on. */
export function AnnouncementBanner({ announcement }: { announcement: GlobalAnnouncement }) {
  const tone = toneOf(announcement.level)
  return (
    <Card data-testid="announcement" className={cn(tone !== 'info' && cn('ring-2', TONE_STYLE[tone].ring))}>
      <CardContent className="flex flex-col gap-1.5">
        <h2 className="text-base font-semibold"><StatusDot tone={tone} label={announcement.title} className="text-base" /></h2>
        {announcement.content && <p className="text-sm whitespace-pre-wrap text-muted-foreground">{announcement.content}</p>}
      </CardContent>
    </Card>
  )
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
