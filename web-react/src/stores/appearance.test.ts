// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/theme', () => ({ DEFAULT_PRESET_HEX: '#6750A4' }))

import {
  resolveEffectiveMode,
  selectEffectiveColor,
  useAppearanceStore,
} from './appearance'

beforeEach(() => {
  localStorage.clear()
  useAppearanceStore.setState({
    systemColor: '#6750A4',
    userColor: null,
    mode: 'auto',
    density: 'comfortable',
  })
})

describe('appearance store', () => {
  it('validates and normalizes system and user colors', () => {
    useAppearanceStore.getState().setSystemColor('invalid')
    expect(useAppearanceStore.getState().systemColor).toBe('#6750A4')
    useAppearanceStore.getState().setSystemColor('#abcdef')
    expect(useAppearanceStore.getState().systemColor).toBe('#ABCDEF')

    useAppearanceStore.getState().setUserColor('#123abc')
    expect(useAppearanceStore.getState().userColor).toBe('#123ABC')
    expect(localStorage.getItem('psp-user-theme-color')).toBe('#123ABC')
    expect(selectEffectiveColor(useAppearanceStore.getState())).toBe('#123ABC')

    useAppearanceStore.getState().setUserColor(null)
    expect(useAppearanceStore.getState().userColor).toBeNull()
    expect(localStorage.getItem('psp-user-theme-color')).toBeNull()
    expect(selectEffectiveColor(useAppearanceStore.getState())).toBe('#ABCDEF')
  })

  it('persists explicit mode and density preferences', () => {
    useAppearanceStore.getState().setMode('dark')
    useAppearanceStore.getState().setDensity('compact')
    expect(useAppearanceStore.getState()).toMatchObject({ mode: 'dark', density: 'compact' })
    expect(localStorage.getItem('psp-user-theme-mode')).toBe('dark')
    expect(localStorage.getItem('psp-user-density')).toBe('compact')
  })

  it('resolves automatic mode from the system preference', () => {
    expect(resolveEffectiveMode('auto', true)).toBe('dark')
    expect(resolveEffectiveMode('auto', false)).toBe('light')
    expect(resolveEffectiveMode('light', true)).toBe('light')
    expect(resolveEffectiveMode('dark', false)).toBe('dark')
  })
})
