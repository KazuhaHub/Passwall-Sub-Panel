import { Suspense, useEffect, useState, type MouseEvent } from 'react'
import {
  AppBar,
  Avatar,
  Box,
  CircularProgress,
  IconButton,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Toolbar,
  Tooltip,
  Typography,
  useTheme,
} from '@mui/material'
import { Outlet } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import LogoutIcon from '@mui/icons-material/Logout'

import AppearanceMenu from '@/components/AppearanceMenu'
import LanguageMenu from '@/components/LanguageMenu'
import BrandLogo from '@/components/BrandLogo'
import { useAuthStore, selectLabel } from '@/stores/auth'
import { useSiteStore } from '@/stores/site'
import { useAppearanceStore } from '@/stores/appearance'
import { setLanguage, currentLanguage } from '@/i18n'
import { DEFAULT_PRESET_HEX, type AppLanguage } from '@/theme'

export default function UserLayout() {
  const theme = useTheme()
  const md = theme.palette.md
  const { t } = useTranslation('common')
  const auth = useAuthStore()
  const label = selectLabel(auth)
  const site = useSiteStore()
  const appearance = useAppearanceStore()

  const [userAnchor, setUserAnchor] = useState<HTMLElement | null>(null)

  useEffect(() => { void site.load() }, [site])
  useEffect(() => {
    // getState() avoids subscribing — putting `appearance` in deps causes
    // an infinite loop (setSystemColor → store updates → effect re-fires).
    if (site.loaded) useAppearanceStore.getState().setSystemColor(site.themeColor || DEFAULT_PRESET_HEX)
  }, [site.loaded, site.themeColor])

  function handleLogout() {
    setUserAnchor(null)
    auth.logout()
  }

  function handleLanguageChange(lng: AppLanguage) { setLanguage(lng) }

  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'flex', flexDirection: 'column', bgcolor: md.surface }}>
      <AppBar position="static">
        <Toolbar sx={{ gap: 0.5, minHeight: 64 }}>
          <BrandLogo height={36} />
          <Typography variant="h6" sx={{ ml: 1.5, fontWeight: 500, fontSize: 17 }}>
            {site.appTitle || site.siteTitle}
          </Typography>
          <Box sx={{ flex: 1 }} />
          <LanguageMenu value={currentLanguage()} onChange={handleLanguageChange} />
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
            slotProps={{ paper: { sx: { mt: 1, minWidth: 180 } } }}
          >
            <MenuItem onClick={handleLogout}>
              <ListItemIcon><LogoutIcon fontSize="small" /></ListItemIcon>
              <ListItemText primary={t('auth.logout')} />
            </MenuItem>
          </Menu>
        </Toolbar>
      </AppBar>
      <Box component="main" sx={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>
        {/* Local Suspense so route-chunk loads don't unmount the topbar.
            See the matching note in AdminLayout. */}
        <Suspense fallback={
          <Box sx={{ height: '100%', display: 'grid', placeItems: 'center', py: 6 }}>
            <CircularProgress size={28} />
          </Box>
        }>
          <Outlet />
        </Suspense>
      </Box>
      {site.footerText && (
        <Box component="footer" sx={{
          py: 1.25, textAlign: 'center',
          fontSize: 11, color: md.onSurfaceVariant,
          borderTop: `1px solid ${md.outlineVariant}`,
          bgcolor: md.surfaceContainerLow,
        }}>
          {site.footerText}
        </Box>
      )}
    </Box>
  )
}
