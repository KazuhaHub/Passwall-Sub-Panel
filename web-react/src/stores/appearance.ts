import { create } from 'zustand'
import { DEFAULT_PRESET_HEX } from '@/theme'

const USER_COLOR_KEY = 'psp-user-theme-color'
const USER_MODE_KEY = 'psp-user-theme-mode'
const USER_DENSITY_KEY = 'psp-user-density'

export type ThemeMode = 'light' | 'dark' | 'auto'
// Density controls how tightly controls and table rows pack. It is mirrored onto
// <html data-density>, where styles/ui.css turns it into --control-h and --row-h.
//   - comfortable: taller controls and rows.
//   - compact: shorter controls and rows.
// Choice persists in localStorage. The default for a visitor with no saved
// choice is "compact" (owner decision); a saved choice is always honoured.
export type Density = 'comfortable' | 'compact'

interface AppearanceState {
  // System default — populated from /auth/methods on app boot.
  // Falls back to the default preset (blue) until the site store loads.
  systemColor: string
  // User-level override stored in localStorage. null = follow system.
  userColor: string | null
  // Stored preference: 'auto' = follow OS prefers-color-scheme; 'light'
  // / 'dark' force the corresponding mode regardless of OS. Default for
  // new visitors is 'auto'.
  mode: ThemeMode
  density: Density

  setSystemColor: (hex: string | undefined) => void
  setUserColor: (hex: string | null) => void
  setMode: (mode: ThemeMode) => void
  setDensity: (density: Density) => void
}

function loadUserColor(): string | null {
  const v = localStorage.getItem(USER_COLOR_KEY)
  return v && /^#[0-9a-fA-F]{6}$/.test(v) ? v.toUpperCase() : null
}

function loadUserMode(): ThemeMode {
  const v = localStorage.getItem(USER_MODE_KEY)
  // Treat unknown values (including the historical absent default) as
  // 'auto' — that's the new default. Previously-stored 'light'/'dark'
  // explicit choices stick around.
  if (v === 'light' || v === 'dark' || v === 'auto') return v
  return 'auto'
}

function loadUserDensity(): Density {
  const v = localStorage.getItem(USER_DENSITY_KEY)
  if (v === 'comfortable' || v === 'compact') return v
  // Compact is the default for a visitor with no saved choice; a saved choice
  // (either value) is honoured.
  return 'compact'
}

export const useAppearanceStore = create<AppearanceState>((set) => ({
  systemColor: DEFAULT_PRESET_HEX,
  userColor: loadUserColor(),
  mode: loadUserMode(),
  density: loadUserDensity(),

  setSystemColor(hex) {
    if (!hex || !/^#[0-9a-fA-F]{6}$/.test(hex)) return
    set({ systemColor: hex.toUpperCase() })
  },

  setUserColor(hex) {
    if (hex === null) {
      localStorage.removeItem(USER_COLOR_KEY)
      set({ userColor: null })
      return
    }
    if (!/^#[0-9a-fA-F]{6}$/.test(hex)) return
    const next = hex.toUpperCase()
    localStorage.setItem(USER_COLOR_KEY, next)
    set({ userColor: next })
  },

  setMode(mode) {
    localStorage.setItem(USER_MODE_KEY, mode)
    set({ mode })
  },

  setDensity(density) {
    localStorage.setItem(USER_DENSITY_KEY, density)
    set({ density })
  },
}))

export const selectEffectiveColor = (s: AppearanceState) => s.userColor ?? s.systemColor

// resolveEffectiveMode collapses 'auto' to the concrete light/dark mode
// driven by the OS's prefers-color-scheme. Pure function so callers can
// re-derive on media-query changes without going through the store.
export function resolveEffectiveMode(mode: ThemeMode, systemPrefersDark: boolean): 'light' | 'dark' {
  if (mode === 'auto') return systemPrefersDark ? 'dark' : 'light'
  return mode
}
