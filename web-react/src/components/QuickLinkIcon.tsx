// The ONE implementation of a quick link's icon, shared by the admin picker (settings, portal
// section) and the user portal's quick links. It renders the single string an admin stored,
// detecting its kind so nobody picks a "type":
//   - "http://…" / "https://…"  → external image (<img>)
//   - "mui:Name"                → a built-in icon from QUICK_LINK_ICONS
//   - anything else (non-empty)  → literal text, i.e. an emoji
//   - empty                      → nothing
//
// The stored names ("mui:Description", "mui:MenuBook", …) were the Material icon names and are
// kept verbatim: they live in the settings of existing installs and in the backend contract,
// so renaming one would blank that link's icon. Only the drawing changed, to the app's icon set
// (lib/icons.ts). The prefix is historical too and must stay. A name that is not in the table
// draws nothing, as it always did.
import { useState } from 'react'

import {
  IconAnnouncement, IconBug, IconChat, IconCloud, IconDiagnostics, IconDocs, IconDonate, IconDownload, IconFavorite,
  IconFile, IconForum, IconGlobe, IconHelp, IconHome, IconInfo, IconLink, IconMail, IconPayment, IconSend,
  IconSettings, IconStar, IconTrendingUp, type IconType,
} from '@/lib/icons'
import { cn } from '@/lib/utils'

// Curated built-in icons, in the order the picker shows them. `key` is the value stored after
// "mui:"; `label` is the human name shown in the admin picker.
export const QUICK_LINK_ICONS: { key: string; label: string; Icon: IconType }[] = [
  { key: 'Description', label: '文档', Icon: IconFile },
  { key: 'MenuBook', label: '教程', Icon: IconDocs },
  { key: 'HelpOutline', label: '帮助', Icon: IconHelp },
  { key: 'InfoOutlined', label: '信息', Icon: IconInfo },
  { key: 'Chat', label: '聊天', Icon: IconChat },
  { key: 'Forum', label: '论坛', Icon: IconForum },
  { key: 'Send', label: 'Telegram', Icon: IconSend },
  { key: 'Campaign', label: '公告', Icon: IconAnnouncement },
  { key: 'TrendingUp', label: '趋势', Icon: IconTrendingUp },
  { key: 'MonitorHeart', label: '状态', Icon: IconDiagnostics },
  { key: 'Favorite', label: '收藏', Icon: IconFavorite },
  { key: 'VolunteerActivism', label: '赞助', Icon: IconDonate },
  { key: 'Payment', label: '续费', Icon: IconPayment },
  { key: 'Download', label: '下载', Icon: IconDownload },
  { key: 'Link', label: '链接', Icon: IconLink },
  { key: 'Public', label: '官网', Icon: IconGlobe },
  { key: 'Home', label: '主页', Icon: IconHome },
  { key: 'Settings', label: '设置', Icon: IconSettings },
  { key: 'Email', label: '邮箱', Icon: IconMail },
  { key: 'Star', label: '星标', Icon: IconStar },
  { key: 'Cloud', label: '云', Icon: IconCloud },
  { key: 'BugReport', label: '反馈', Icon: IconBug },
]

const ICON_MAP: Record<string, IconType> = Object.fromEntries(QUICK_LINK_ICONS.map(i => [i.key, i.Icon]))

export function isImageIcon(icon: string): boolean {
  return /^https?:\/\//i.test(icon.trim())
}

/** Size it with a `size-*` class; the default is the portal's 20px. */
export function QuickLinkIcon({ icon, className = 'size-5' }: { icon: string; className?: string }) {
  const [broken, setBroken] = useState(false)
  const v = (icon || '').trim()
  if (!v) return null
  if (isImageIcon(v)) {
    // A broken external image is hidden rather than shown as the browser's placeholder glyph,
    // which looks worse than no icon at all.
    return broken ? null : <img src={v} alt="" className={cn(className, 'rounded-md object-contain')} onError={() => setBroken(true)} />
  }
  if (v.startsWith('mui:')) {
    const Glyph = ICON_MAP[v.slice(4)]
    return Glyph ? <Glyph aria-hidden className={className} /> : null
  }
  // Literal text / emoji.
  return <span aria-hidden className="text-lg leading-none">{v}</span>
}
