import { Suspense, useEffect, useMemo, useState } from 'react'
import CssBaseline from '@mui/material/CssBaseline'
import { ThemeProvider } from '@mui/material/styles'
import { RouterProvider } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Box, CircularProgress } from '@mui/material'

import { createAppTheme, type AppLanguage } from '@/theme'
import { SUPPORTED_LANGUAGES, currentLanguage } from '@/i18n'
import { useAppearanceStore, selectEffectiveColor, resolveEffectiveMode } from '@/stores/appearance'
import SnackbarHost from '@/components/SnackbarHost'
import ConfirmHost from '@/components/ConfirmHost'
import { router } from '@/router'

// Snapshot of prefers-color-scheme. SSR-safe defaults to "light" so
// the initial render before the listener fires doesn't flash dark on
// systems that aren't dark.
function systemPrefersDarkNow(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

export default function App() {
  const mode = useAppearanceStore(s => s.mode)
  const sourceColor = useAppearanceStore(selectEffectiveColor)
  const density = useAppearanceStore(s => s.density)
  const { i18n } = useTranslation()
  const [language, setLanguageState] = useState<AppLanguage>(currentLanguage)
  const [systemDark, setSystemDark] = useState(systemPrefersDarkNow)

  useEffect(() => {
    const handler = (lng: string) => {
      const next = (SUPPORTED_LANGUAGES.includes(lng as AppLanguage) ? lng : 'zh-CN') as AppLanguage
      setLanguageState(next)
      document.documentElement.lang = next
    }
    handler(i18n.resolvedLanguage ?? 'zh-CN')
    i18n.on('languageChanged', handler)
    return () => { i18n.off('languageChanged', handler) }
  }, [i18n])

  // Track prefers-color-scheme changes so 'auto' mode flips live when
  // the OS theme changes (e.g. macOS auto-switches at sunset).
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    // addEventListener is the modern API; some older Safari versions
    // only expose addListener — fall through to either.
    if (mql.addEventListener) mql.addEventListener('change', onChange)
    else mql.addListener(onChange)
    return () => {
      if (mql.removeEventListener) mql.removeEventListener('change', onChange)
      else mql.removeListener(onChange)
    }
  }, [])

  const effectiveMode = resolveEffectiveMode(mode, systemDark)

  const theme = useMemo(
    () => createAppTheme({ mode: effectiveMode, sourceColor, language, density }),
    [effectiveMode, sourceColor, language, density],
  )

  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <Suspense fallback={<RouteFallback />}>
        <RouterProvider router={router} />
      </Suspense>
      <SnackbarHost />
      <ConfirmHost />
    </ThemeProvider>
  )
}

function RouteFallback() {
  return (
    <Box sx={{ position: 'fixed', inset: 0, display: 'grid', placeItems: 'center' }}>
      <CircularProgress />
    </Box>
  )
}
