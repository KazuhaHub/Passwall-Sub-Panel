// QuickLinkIcon renders a quick-link's icon from a single string, auto-detecting
// the source so admins don't pick a "type":
//   - "http://…" / "https://…"  → external image (<img>)
//   - "mui:Name"                → a built-in icon from QUICK_LINK_ICONS
//   - anything else (non-empty)  → literal text, i.e. an emoji
//   - empty                      → nothing
//
// The built-in set is a curated allowlist (not the whole MUI icon library) so
// the bundle only carries the handful of icons that make sense for shortcuts.
import type { SvgIconComponent } from '@mui/icons-material'
import DescriptionIcon from '@mui/icons-material/Description'
import MenuBookIcon from '@mui/icons-material/MenuBook'
import HelpOutlineIcon from '@mui/icons-material/HelpOutlined'
import ChatIcon from '@mui/icons-material/Chat'
import ForumIcon from '@mui/icons-material/Forum'
import CampaignIcon from '@mui/icons-material/Campaign'
import SendIcon from '@mui/icons-material/Send'
import TrendingUpIcon from '@mui/icons-material/TrendingUp'
import MonitorHeartIcon from '@mui/icons-material/MonitorHeart'
import FavoriteIcon from '@mui/icons-material/Favorite'
import VolunteerActivismIcon from '@mui/icons-material/VolunteerActivism'
import PaymentIcon from '@mui/icons-material/Payment'
import DownloadIcon from '@mui/icons-material/Download'
import LinkIcon from '@mui/icons-material/Link'
import PublicIcon from '@mui/icons-material/Public'
import HomeIcon from '@mui/icons-material/Home'
import SettingsIcon from '@mui/icons-material/Settings'
import EmailIcon from '@mui/icons-material/Email'
import StarIcon from '@mui/icons-material/Star'
import CloudIcon from '@mui/icons-material/Cloud'
import BugReportIcon from '@mui/icons-material/BugReport'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'

// Curated built-in icons. Keyed by the value stored in QuickLink.icon
// ("mui:<key>"); label is the human name shown in the admin picker.
export const QUICK_LINK_ICONS: { key: string; label: string; Icon: SvgIconComponent }[] = [
  { key: 'Description', label: '文档', Icon: DescriptionIcon },
  { key: 'MenuBook', label: '教程', Icon: MenuBookIcon },
  { key: 'HelpOutline', label: '帮助', Icon: HelpOutlineIcon },
  { key: 'InfoOutlined', label: '信息', Icon: InfoOutlinedIcon },
  { key: 'Chat', label: '聊天', Icon: ChatIcon },
  { key: 'Forum', label: '论坛', Icon: ForumIcon },
  { key: 'Send', label: 'Telegram', Icon: SendIcon },
  { key: 'Campaign', label: '公告', Icon: CampaignIcon },
  { key: 'TrendingUp', label: '趋势', Icon: TrendingUpIcon },
  { key: 'MonitorHeart', label: '状态', Icon: MonitorHeartIcon },
  { key: 'Favorite', label: '收藏', Icon: FavoriteIcon },
  { key: 'VolunteerActivism', label: '赞助', Icon: VolunteerActivismIcon },
  { key: 'Payment', label: '续费', Icon: PaymentIcon },
  { key: 'Download', label: '下载', Icon: DownloadIcon },
  { key: 'Link', label: '链接', Icon: LinkIcon },
  { key: 'Public', label: '官网', Icon: PublicIcon },
  { key: 'Home', label: '主页', Icon: HomeIcon },
  { key: 'Settings', label: '设置', Icon: SettingsIcon },
  { key: 'Email', label: '邮箱', Icon: EmailIcon },
  { key: 'Star', label: '星标', Icon: StarIcon },
  { key: 'Cloud', label: '云', Icon: CloudIcon },
  { key: 'BugReport', label: '反馈', Icon: BugReportIcon },
]

const ICON_MAP: Record<string, SvgIconComponent> = Object.fromEntries(
  QUICK_LINK_ICONS.map(i => [i.key, i.Icon]),
)

export function isImageIcon(icon: string): boolean {
  return /^https?:\/\//i.test(icon.trim());
}

export function QuickLinkIcon({ icon, size = 22, color }: { icon: string; size?: number; color?: string }) {
  const v = (icon || '').trim()
  if (!v) return null
  if (isImageIcon(v)) {
    return (
      <img
        src={v}
        alt=""
        width={size}
        height={size}
        style={{ objectFit: 'contain', borderRadius: 6, display: 'block' }}
        // Hide a broken external image rather than show the browser's
        // placeholder glyph, which looks worse than no icon at all.
        onError={e => { (e.currentTarget as HTMLImageElement).style.display = 'none' }}
      />
    )
  }
  if (v.startsWith('mui:')) {
    const Cmp = ICON_MAP[v.slice(4)]
    return Cmp ? <Cmp sx={{ fontSize: size, color }} /> : null
  }
  // Literal text / emoji.
  return <span style={{ fontSize: size * 0.9, lineHeight: 1, color }}>{v}</span>
}
