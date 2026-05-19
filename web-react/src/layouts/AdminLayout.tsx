import { Suspense, useEffect, useState, type MouseEvent } from 'react'
import {
  AppBar,
  Avatar,
  Box,
  CircularProgress,
  Drawer,
  IconButton,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Toolbar,
  Tooltip,
  Typography,
  alpha,
  useMediaQuery,
  useTheme,
} from '@mui/material'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import MenuIcon from '@mui/icons-material/Menu'
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft'
import ChevronRightIcon from '@mui/icons-material/ChevronRight'
import DashboardIcon from '@mui/icons-material/Dashboard'
import GroupIcon from '@mui/icons-material/Group'
import StorageIcon from '@mui/icons-material/Storage'
import DnsIcon from '@mui/icons-material/Dns'
import WorkspacesIcon from '@mui/icons-material/Workspaces'
import RuleIcon from '@mui/icons-material/Rule'
import LayersIcon from '@mui/icons-material/Layers'
import InsightsIcon from '@mui/icons-material/Insights'
import ReceiptLongIcon from '@mui/icons-material/ReceiptLong'
import SyncIcon from '@mui/icons-material/Sync'
import SettingsIcon from '@mui/icons-material/Settings'
import LogoutIcon from '@mui/icons-material/Logout'

import AppearanceMenu from '@/components/AppearanceMenu'
import DensityToggle from '@/components/DensityToggle'
import LanguageMenu from '@/components/LanguageMenu'
import BrandLogo from '@/components/BrandLogo'
import { useAuthStore, selectLabel } from '@/stores/auth'
import { useSiteStore, selectIcon } from '@/stores/site'
import { useAppearanceStore } from '@/stores/appearance'
import { setLanguage, currentLanguage } from '@/i18n'
import { DEFAULT_PRESET_HEX, type AppLanguage } from '@/theme'
import { getVersion, type VersionInfo } from '@/api/version'

// Sidebar widths follow the global density setting: compact trims the
// expanded rail to give admin pages another ~50px of horizontal room
// (most felt in tables on smaller laptops), while the collapsed
// (icon-only) state stays the same because icons + padding already
// dominate the width.
const DRAWER_WIDTH_EXPANDED_COMFORTABLE = 256
const DRAWER_WIDTH_EXPANDED_COMPACT = 208
const DRAWER_WIDTH_COLLAPSED = 76
const COLLAPSED_STORAGE_KEY = 'psp-sidebar-collapsed'

// Sidebar version badge is on by default; operators who don't want it
// can suppress it at SPA build time with `VITE_SHOW_VERSION=0 npm run
// build`. Intentionally not a runtime/admin-settings toggle — the
// choice belongs to whoever ships the binary, not to admins clicking
// around in the UI.
const SHOW_VERSION = import.meta.env.VITE_SHOW_VERSION !== '0'

interface NavItem {
  to: string
  labelKey: string
  Icon: typeof DashboardIcon
  /** When true, the item only renders for admin role (operators don't
   *  see it in the sidebar). Mirrors the backend's adminGroup vs
   *  staffGroup split — keep these in sync. */
  adminOnly?: boolean
}

const ADMIN_NAV: NavItem[] = [
  { to: '/admin/dashboard', labelKey: 'nav:admin.dashboard', Icon: DashboardIcon },
  { to: '/admin/users', labelKey: 'nav:admin.users', Icon: GroupIcon },
  { to: '/admin/servers', labelKey: 'nav:admin.servers', Icon: StorageIcon, adminOnly: true },
  { to: '/admin/nodes', labelKey: 'nav:admin.nodes', Icon: DnsIcon },
  { to: '/admin/groups', labelKey: 'nav:admin.groups', Icon: WorkspacesIcon },
  { to: '/admin/rules', labelKey: 'nav:admin.rules', Icon: RuleIcon },
  { to: '/admin/templates', labelKey: 'nav:admin.templates', Icon: LayersIcon },
  { to: '/admin/traffic', labelKey: 'nav:admin.traffic', Icon: InsightsIcon },
  { to: '/admin/logs', labelKey: 'nav:admin.logs', Icon: ReceiptLongIcon },
  { to: '/admin/sync-tasks', labelKey: 'nav:admin.sync_tasks', Icon: SyncIcon },
  { to: '/admin/settings', labelKey: 'nav:admin.settings', Icon: SettingsIcon, adminOnly: true },
]

export default function AdminLayout() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation(['nav', 'common'])
  const location = useLocation()
  const navigate = useNavigate()
  const isMobile = useMediaQuery(theme.breakpoints.down('md'))

  const auth = useAuthStore()
  const label = selectLabel(auth)
  const site = useSiteStore()
  const appearance = useAppearanceStore()

  const [mobileOpen, setMobileOpen] = useState(false)
  const [userAnchor, setUserAnchor] = useState<HTMLElement | null>(null)
  const [versionInfo, setVersionInfo] = useState<VersionInfo | null>(null)
  // Collapsed-rail state. Persisted to localStorage so the choice survives
  // refresh. Mobile (temporary drawer) ignores this — it always shows the
  // full sidebar when opened.
  const [collapsed, setCollapsed] = useState<boolean>(() => {
    if (typeof window === 'undefined') return false
    return window.localStorage.getItem(COLLAPSED_STORAGE_KEY) === '1'
  })
  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem(COLLAPSED_STORAGE_KEY, collapsed ? '1' : '0')
  }, [collapsed])

  // Effective collapsed: only on desktop. Mobile drawer always renders the
  // full-width version regardless of the stored preference.
  const railCollapsed = collapsed && !isMobile
  // Density-aware expanded width — see constants above for why only the
  // expanded variant tightens.
  const drawerWidthExpanded = appearance.density === 'compact'
    ? DRAWER_WIDTH_EXPANDED_COMPACT
    : DRAWER_WIDTH_EXPANDED_COMFORTABLE

  // Load site branding once on mount.
  useEffect(() => { void site.load() }, [site])

  // Fetch build identity once on mount. Endpoint is public and the
  // sidebar badge silently no-ops if the call fails (no point toasting
  // for a missing version string). Skipped entirely when the badge is
  // disabled at build time.
  useEffect(() => {
    if (!SHOW_VERSION) return
    let cancelled = false
    getVersion()
      .then(info => { if (!cancelled) setVersionInfo(info) })
      .catch(() => { /* leave badge hidden */ })
    return () => { cancelled = true }
  }, [])

  // Re-sync system theme color from settings once site is loaded. Empty
  // theme_color falls back to the compiled default. We pull the setter via
  // getState() instead of the subscribed `appearance` object — putting the
  // whole store in the dep array creates an infinite loop (set → new store
  // reference → effect re-fires → set → ...).
  useEffect(() => {
    if (site.loaded) {
      useAppearanceStore.getState().setSystemColor(site.themeColor || DEFAULT_PRESET_HEX)
    }
  }, [site.loaded, site.themeColor])

  function handleNav(to: string) {
    navigate(to)
    if (isMobile) setMobileOpen(false)
  }

  function handleLogout() {
    setUserAnchor(null)
    auth.logout()
  }

  function handleLanguageChange(lng: AppLanguage) {
    setLanguage(lng)
  }

  const drawerContent = (
    <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column', bgcolor: md.surfaceContainerLow }}>
      <Box sx={{
        height: 64, display: 'flex', alignItems: 'center', gap: 1.5,
        px: railCollapsed ? 0 : 2.5,
        justifyContent: railCollapsed ? 'center' : 'flex-start',
      }}>
        {railCollapsed ? (
          // Square favicon fits the 76px rail much better than the wide
          // wordmark logo — the brand mark is reserved for the expanded view
          // where there's room for it next to the title text.
          <Box
            component="img"
            src={selectIcon(site)}
            alt=""
            sx={{ width: 36, height: 36, borderRadius: 1, objectFit: 'contain', display: 'block' }}
          />
        ) : (
          <>
            <BrandLogo height={36} />
            <Typography sx={{ fontWeight: 500, fontSize: 16, color: md.onSurface, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {site.appTitle || site.siteTitle}
            </Typography>
          </>
        )}
      </Box>
      <List sx={{ flex: 1, px: railCollapsed ? 0.75 : 1.5, pt: 1 }}>
        {ADMIN_NAV.filter(item => !item.adminOnly || auth.role === 'admin').map(item => {
          const active = location.pathname === item.to || location.pathname.startsWith(item.to + '/')
          const button = (
            <ListItemButton
              key={item.to}
              onClick={() => handleNav(item.to)}
              sx={{
                borderRadius: railCollapsed ? 2 : 9999,
                minHeight: 44,
                mb: 0.5,
                px: railCollapsed ? 1.25 : 2,
                justifyContent: railCollapsed ? 'center' : 'flex-start',
                color: active ? md.onSecondaryContainer : md.onSurfaceVariant,
                bgcolor: active ? md.secondaryContainer : 'transparent',
                '&:hover': { bgcolor: active ? md.secondaryContainer : alpha(md.onSurface, 0.08) },
              }}
            >
              <ListItemIcon sx={{ minWidth: railCollapsed ? 0 : 40, color: 'inherit', justifyContent: 'center' }}>
                <item.Icon sx={{ fontSize: 22 }} />
              </ListItemIcon>
              {!railCollapsed && (
                <ListItemText
                  primary={t(item.labelKey)}
                  primaryTypographyProps={{ fontSize: 14, fontWeight: active ? 600 : 500 }}
                />
              )}
            </ListItemButton>
          )
          return railCollapsed
            ? <Tooltip key={item.to} title={t(item.labelKey)} placement="right">{button}</Tooltip>
            : button
        })}
      </List>
      {/* Build identity — populated by the /api/version call on mount.
          ldflags-stamped Version/Commit/BuildDate are surfaced here so an
          admin can confirm at a glance which release the panel is running.
          Hidden when the fetch hasn't resolved (or failed). Collapsed rail
          (76 px) can only fit ~6 monospace chars at 11 px, so we strip the
          pre-release suffix there ("v3.0.0-rc.6" → "v3.0.0") and rely on
          the tooltip for the full string + commit + build date. */}
      {SHOW_VERSION && versionInfo && (
        <Tooltip
          placement="right"
          title={
            <Box sx={{ fontSize: 11, lineHeight: 1.5 }}>
              <Box>{versionInfo.version || 'dev'}</Box>
              {versionInfo.commit && <Box>commit: {versionInfo.commit}</Box>}
              {versionInfo.build_date && <Box>built: {versionInfo.build_date}</Box>}
            </Box>
          }
        >
          <Box sx={{
            px: railCollapsed ? 0 : 2,
            py: 0.75,
            textAlign: 'center',
            color: md.onSurfaceVariant,
            fontSize: 11,
            fontFamily: 'monospace',
            opacity: 0.7,
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            userSelect: 'none',
            cursor: 'default',
          }}>
            {(() => {
              const full = versionInfo.version || 'dev'
              if (!railCollapsed) return full
              const dash = full.indexOf('-')
              return dash === -1 ? full : full.slice(0, dash)
            })()}
          </Box>
        </Tooltip>
      )}
      {/* Collapse toggle — desktop only; mobile drawer is dismissed by tapping outside */}
      {!isMobile && (
        <Box sx={{
          display: 'flex',
          justifyContent: railCollapsed ? 'center' : 'flex-end',
          px: railCollapsed ? 0 : 1.5, py: 1,
          borderTop: `1px solid ${md.outlineVariant}`,
        }}>
          <Tooltip title={t(railCollapsed ? 'common:nav.expand' : 'common:nav.collapse')} placement="right">
            <IconButton size="small" onClick={() => setCollapsed(c => !c)}
              sx={{ color: md.onSurfaceVariant }}>
              {railCollapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}
            </IconButton>
          </Tooltip>
        </Box>
      )}
    </Box>
  )

  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'flex', bgcolor: md.surface }}>
      {/* Persistent drawer (desktop) */}
      {!isMobile && (
        <Box component="nav" sx={{
          width: railCollapsed ? DRAWER_WIDTH_COLLAPSED : drawerWidthExpanded,
          flexShrink: 0,
          transition: theme.transitions.create('width', { duration: theme.transitions.duration.shorter }),
        }}>
          <Drawer
            variant="permanent"
            open
            sx={{
              '& .MuiDrawer-paper': {
                width: railCollapsed ? DRAWER_WIDTH_COLLAPSED : drawerWidthExpanded,
                overflowX: 'hidden',
                borderRight: `1px solid ${md.outlineVariant}`,
                bgcolor: md.surfaceContainerLow,
                transition: theme.transitions.create('width', { duration: theme.transitions.duration.shorter }),
              },
            }}
          >
            {drawerContent}
          </Drawer>
        </Box>
      )}

      {/* Temporary drawer (mobile) */}
      {isMobile && (
        <Drawer
          variant="temporary"
          open={mobileOpen}
          onClose={() => setMobileOpen(false)}
          sx={{ '& .MuiDrawer-paper': { width: drawerWidthExpanded } }}
        >
          {drawerContent}
        </Drawer>
      )}

      <Box sx={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0 }}>
        <AppBar position="static">
          <Toolbar sx={{ gap: 0.5, minHeight: 64 }}>
            {isMobile && (
              <IconButton edge="start" onClick={() => setMobileOpen(true)} aria-label="menu">
                <MenuIcon />
              </IconButton>
            )}
            <Typography variant="h6" sx={{ ml: isMobile ? 1 : 0, fontWeight: 500, fontSize: 17 }}>
              {site.siteTitle}
            </Typography>
            <Box sx={{ flex: 1 }} />
            <LanguageMenu value={currentLanguage()} onChange={handleLanguageChange} />
            <DensityToggle />
            <AppearanceMenu
              state={{ systemColor: appearance.systemColor, userColor: appearance.userColor, mode: appearance.mode }}
              onChange={(patch) => {
                if ('userColor' in patch) appearance.setUserColor(patch.userColor ?? null)
                if (patch.mode) appearance.setMode(patch.mode)
              }}
            />
            <Tooltip title={label}>
              <IconButton onClick={(e: MouseEvent<HTMLElement>) => setUserAnchor(e.currentTarget)} sx={{ ml: 1, p: 0.5 }}>
                <Avatar sx={{ width: 32, height: 32, bgcolor: md.primary, color: md.onPrimary, fontSize: 14, fontWeight: 500 }}>
                  {label.charAt(0).toUpperCase()}
                </Avatar>
              </IconButton>
            </Tooltip>
            <Menu
              open={!!userAnchor}
              anchorEl={userAnchor}
              onClose={() => setUserAnchor(null)}
              anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
              transformOrigin={{ vertical: 'top', horizontal: 'right' }}
              PaperProps={{ sx: { mt: 1, minWidth: 180 } }}
            >
              <MenuItem disabled sx={{ opacity: '1 !important' }}>
                <Box>
                  <Typography sx={{ fontSize: 14, fontWeight: 500, color: md.onSurface }}>{label}</Typography>
                  <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
                    {auth.role === 'admin' ? 'Administrator' : auth.role === 'operator' ? 'Operator' : 'User'}
                  </Typography>
                </Box>
              </MenuItem>
              <MenuItem onClick={handleLogout}>
                <ListItemIcon><LogoutIcon fontSize="small" /></ListItemIcon>
                <ListItemText primary={t('common:auth.logout')} />
              </MenuItem>
            </Menu>
          </Toolbar>
        </AppBar>

        <Box component="main" sx={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
          {/* Per-route Suspense boundary. Lives INSIDE the layout so that
              switching sidebar items only swaps the main content area — the
              outer App-level Suspense would unmount the whole layout (and
              its mount-time effects) on every cross-section navigation,
              which felt like a noticeable lag/flash. */}
          <Suspense fallback={
            <Box sx={{ height: '100%', display: 'grid', placeItems: 'center', py: 6 }}>
              <CircularProgress size={28} />
            </Box>
          }>
            <Outlet />
          </Suspense>
        </Box>
      </Box>
    </Box>
  )
}
