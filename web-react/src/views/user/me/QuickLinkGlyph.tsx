import { useState } from 'react'
import {
  IconAnnouncement, IconBug, IconChat, IconCloud, IconDiagnostics, IconDocs, IconDonate, IconDownload, IconFavorite,
  IconFile, IconForum, IconGlobe, IconHelp, IconHome, IconInfo, IconLink, IconMail, IconPayment, IconSend,
  IconSettings, IconStar, IconTrendingUp, type IconType,
} from '@/lib/icons'

/**
 * The built-in glyphs an admin can pick for a quick link, keyed by the name stored after "mui:" in QuickLink.icon. The
 * stored names are the Material ones, so existing links keep their meaning: only the drawing is the new set's. An
 * unknown name draws nothing, as before.
 */
const GLYPHS: Record<string, IconType> = {
  Description: IconFile, MenuBook: IconDocs, HelpOutline: IconHelp, InfoOutlined: IconInfo, Chat: IconChat,
  Forum: IconForum, Send: IconSend, Campaign: IconAnnouncement, TrendingUp: IconTrendingUp, MonitorHeart: IconDiagnostics,
  Favorite: IconFavorite, VolunteerActivism: IconDonate, Payment: IconPayment, Download: IconDownload, Link: IconLink,
  Public: IconGlobe, Home: IconHome, Settings: IconSettings, Email: IconMail, Star: IconStar, Cloud: IconCloud, BugReport: IconBug,
}

/**
 * A quick link's icon from the one string the admin stored: an http(s) address is an image, "mui:Name" a built-in glyph,
 * anything else is literal text (an emoji), empty is nothing.
 */
export function QuickLinkGlyph({ icon, className = 'size-5' }: { icon: string; className?: string }) {
  const [broken, setBroken] = useState(false)
  const v = (icon || '').trim()
  if (!v) return null
  if (/^https?:\/\//i.test(v)) {
    // A broken external image is hidden rather than shown as the browser's placeholder, which looks worse than no icon.
    return broken ? null : <img src={v} alt="" className={`${className} rounded-md object-contain`} onError={() => setBroken(true)} />
  }
  if (v.startsWith('mui:')) {
    const Glyph = GLYPHS[v.slice(4)]
    return Glyph ? <Glyph aria-hidden className={className} /> : null
  }
  return <span aria-hidden className="text-lg leading-none">{v}</span>
}
