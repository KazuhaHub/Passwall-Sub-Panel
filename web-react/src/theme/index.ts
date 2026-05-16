import { createTheme, type Theme, alpha } from '@mui/material/styles'
import * as muiLocales from '@mui/material/locale'
import { tokensFromSource, type M3Tokens } from './tokensFromSource'

declare module '@mui/material/styles' {
  interface Palette {
    md: M3Tokens
  }
  interface PaletteOptions {
    md?: M3Tokens
  }
}

export type AppLanguage = 'zh-CN' | 'en-US'

// Map our language codes to MUI's bundled locale objects.
const MUI_LOCALE_MAP: Record<AppLanguage, object> = {
  'zh-CN': muiLocales.zhCN,
  'en-US': muiLocales.enUS,
}

export interface CreateAppThemeArgs {
  mode: 'light' | 'dark'
  sourceColor: string
  language: AppLanguage
}

export function createAppTheme({ mode, sourceColor, language }: CreateAppThemeArgs): Theme {
  const t = tokensFromSource(sourceColor, mode)
  const muiLocale = MUI_LOCALE_MAP[language] ?? muiLocales.enUS

  return createTheme({
    palette: {
      mode,
      primary: { main: t.primary, contrastText: t.onPrimary },
      secondary: { main: t.secondary },
      error: { main: t.error },
      background: { default: t.surface, paper: t.surfaceContainerLow },
      text: { primary: t.onSurface, secondary: t.onSurfaceVariant },
      divider: t.outlineVariant,
      md: t,
    },
    shape: { borderRadius: 12 },
    typography: {
      // Roboto first (covers Latin), then Noto Sans SC (covers CJK), then
      // system fallbacks. The two Google fonts are designed together so
      // CJK / Latin mixes don't look mismatched.
      fontFamily: '"Roboto","Noto Sans SC","PingFang SC","Microsoft YaHei",-apple-system,"Segoe UI",sans-serif',
      h4: { fontWeight: 400, fontSize: 28, lineHeight: '36px' },
      h6: { fontWeight: 500, fontSize: 20 },
      button: { textTransform: 'none', fontWeight: 500, letterSpacing: 0.1 },
      body2: { fontSize: 13, color: t.onSurfaceVariant },
    },
    components: {
      MuiCssBaseline: {
        styleOverrides: {
          body: { backgroundColor: t.surface },
        },
      },
      MuiAppBar: {
        defaultProps: { elevation: 0, color: 'transparent' },
        styleOverrides: {
          root: {
            backgroundColor: t.surfaceContainer,
            color: t.onSurface,
          },
        },
      },
      MuiButton: {
        defaultProps: { disableElevation: true },
        styleOverrides: {
          root: {
            borderRadius: 9999,
            minHeight: 40,
            paddingLeft: 24,
            paddingRight: 24,
          },
          sizeSmall: { minHeight: 32, paddingLeft: 14, paddingRight: 14 },
          containedPrimary: {
            backgroundColor: t.primary,
            color: t.onPrimary,
            '&:hover': {
              backgroundColor: t.primary,
              boxShadow:
                '0 1px 2px rgba(0,0,0,.3),0 1px 3px 1px rgba(0,0,0,.15)',
            },
          },
          outlined: {
            borderColor: t.outline,
            color: t.primary,
            '&:hover': {
              borderColor: t.outline,
              backgroundColor: alpha(t.primary, 0.08),
            },
          },
          text: {
            color: t.primary,
            '&:hover': { backgroundColor: alpha(t.primary, 0.08) },
          },
        },
      },
      MuiIconButton: {
        styleOverrides: {
          root: {
            color: t.onSurfaceVariant,
            '&:hover': { backgroundColor: alpha(t.onSurface, 0.08) },
          },
        },
      },
      // Global dialog padding. Content gets a 32dp horizontal inset so it
      // sits comfortably away from the dialog's rounded edge. Actions match
      // the same 32dp gutter on both sides so the right-most button's edge
      // mirrors the content's left edge — without this, the right cluster
      // looked glued to the wall while the title/body still had breathing
      // room on the left, breaking the visual centering.
      MuiDialogTitle: {
        styleOverrides: {
          root: { padding: '24px 32px 8px' },
        },
      },
      MuiDialogContent: {
        styleOverrides: {
          root: { padding: '12px 32px 20px' },
        },
      },
      MuiDialogActions: {
        styleOverrides: {
          root: { padding: '8px 32px 20px' },
        },
      },
      MuiCard: {
        defaultProps: { elevation: 0 },
        styleOverrides: {
          root: { borderRadius: 16, backgroundImage: 'none' },
        },
      },
      MuiChip: {
        styleOverrides: {
          root: {
            borderRadius: 8,
            height: 32,
            fontWeight: 500,
            fontSize: 14,
            backgroundColor: 'transparent',
            border: `1px solid ${t.outline}`,
            color: t.onSurfaceVariant,
            '&:hover': { backgroundColor: alpha(t.onSurface, 0.08) },
          },
          filled: {
            backgroundColor: t.secondaryContainer,
            color: t.onSecondaryContainer,
            border: 'none',
            '&:hover': { backgroundColor: t.secondaryContainer },
          },
        },
      },
      MuiSwitch: {
        styleOverrides: {
          root: {
            width: 52,
            height: 32,
            padding: 0,
            '& .MuiSwitch-switchBase': {
              padding: 0,
              margin: 8,
              transitionDuration: '200ms',
              '&.Mui-checked': {
                transform: 'translateX(20px)',
                margin: 4,
                color: t.onPrimary,
                '& + .MuiSwitch-track': {
                  backgroundColor: t.primary,
                  borderColor: t.primary,
                  opacity: 1,
                },
                '& .MuiSwitch-thumb': {
                  width: 24,
                  height: 24,
                  // M3 spec: thumb on a checked switch uses the onPrimary
                  // role (high-contrast against the primary track).
                  backgroundColor: t.onPrimary,
                },
              },
            },
            '& .MuiSwitch-thumb': {
              width: 16,
              height: 16,
              backgroundColor: t.outline,
              boxShadow: 'none',
              transition:
                'width 0.2s, height 0.2s, background-color 0.2s',
            },
            '& .MuiSwitch-track': {
              borderRadius: 9999,
              backgroundColor: t.surfaceContainerHighest,
              border: `2px solid ${t.outline}`,
              opacity: 1,
              boxSizing: 'border-box',
            },
          },
        },
      },
      MuiOutlinedInput: {
        styleOverrides: {
          root: { borderRadius: 12 },
        },
      },
      MuiPaper: {
        styleOverrides: {
          root: { backgroundImage: 'none' },
        },
      },
      MuiSnackbarContent: {
        styleOverrides: {
          root: {
            backgroundColor: t.surfaceContainerHighest,
            color: t.onSurface,
            borderRadius: 4,
          },
        },
      },
    },
  }, muiLocale)
}

export { COLOR_PRESETS, DEFAULT_PRESET_HEX } from './presets'
export type { ColorPreset } from './presets'
export { isValidHex, type M3Tokens } from './tokensFromSource'
