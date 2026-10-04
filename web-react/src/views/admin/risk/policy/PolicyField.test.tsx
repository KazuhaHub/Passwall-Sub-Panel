/** @vitest-environment jsdom */
import { useState } from 'react'
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'

vi.mock('@/api/client', () => ({ client: {} }))
// t over the REAL zh-CN admin bundle, so a key the field asks for but the
// bundle lacks shows up as its raw key.
const dict = vi.hoisted(() => ({ current: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: Record<string, unknown>) => {
      const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
      const raw = dict.current[flat] ?? k
      return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
    },
    i18n: { language: 'zh-CN' },
  }),
}))

import zh from '@/locales/zh-CN/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import { cardFields, POLICY_CARDS, type PolicyFieldSpec } from './policyLayout'
import PolicyField from '@/components/PolicyField'
dict.current = flatten(zh as Nested)

const theme = createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })

const DEFAULTS: Record<string, number> = {
  geo_anomaly_min_placed_ratio: 0.5, geo_anomaly_max_cities: 2, risk_usage_ratio: 3, risk_max_devices: 3,
  geo_anomaly_fresh_window_seconds: 120,
}

function spec(key: string): PolicyFieldSpec {
  const f = POLICY_CARDS.flatMap(cardFields).find(x => x.key === key)
  if (!f) throw new Error(`no field ${key}`)
  return f
}

/** The field over a stored value it owns, the way the page drives it. */
function mount(key: string, initial: number, effective?: number) {
  const stored: number[] = []
  function Harness() {
    const [value, setValue] = useState<number>(initial)
    return (
      <PolicyField spec={spec(key)} value={value} defaults={DEFAULTS} effective={effective}
        onChange={v => { stored.push(v as number); setValue(v as number) }} />
    )
  }
  render(<ThemeProvider theme={theme}><Harness /></ThemeProvider>)
  const input = screen.getByRole('textbox', { name: dict.current[spec(key).label.slice('admin:'.length)] }) as HTMLInputElement
  return { input, stored }
}

function description(input: HTMLInputElement): string {
  const id = input.getAttribute('aria-describedby') ?? ''
  return document.getElementById(id)?.textContent ?? ''
}

afterEach(cleanup)

describe('PolicyField, numbers', () => {
  // U2: a number box that rewrites what is typed on every keystroke cannot
  // take "0.5" — "0." is not a number yet. The text stays as typed while the
  // field has focus; the store gets the number it reads as.
  it('takes 0.5 typed one character at a time', () => {
    const { input, stored } = mount('geo_anomaly_min_placed_ratio', 0)
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '0' } })
    fireEvent.change(input, { target: { value: '0.' } })
    expect(input.value).toBe('0.')
    fireEvent.change(input, { target: { value: '0.5' } })
    expect(stored.at(-1)).toBe(0.5)
    expect(input.value).toBe('0.5')
    fireEvent.blur(input)
    expect(input.value).toBe('0.5')
  })

  // Unset is an empty field whose placeholder is the served default and
  // which says "default" — never a 0 that reads as "zero tolerance".
  it('shows an unset value as empty, with the default as placeholder and a default mark', () => {
    const { input } = mount('geo_anomaly_max_cities', 0)
    expect(input.value).toBe('')
    expect(input.placeholder).toBe('2')
    expect(screen.getByText('默认')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '恢复默认' })).toBeNull()
  })

  it('shows a set value with a reset that stores 0', () => {
    const { input, stored } = mount('geo_anomaly_max_cities', 4)
    expect(input.value).toBe('4')
    expect(screen.queryByText('默认')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '恢复默认' }))
    expect(stored).toEqual([0])
    expect(input.value).toBe('')
    expect(input.placeholder).toBe('2')
  })

  it('stores an emptied field as 0 and a whole number for an integer key', () => {
    const { input, stored } = mount('geo_anomaly_max_cities', 4)
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.change(input, { target: { value: '3.7' } })
    expect(stored).toEqual([0, 3])
  })

  // The server clamps a ratio under 1.5 up to it; the page says so before
  // the admin saves, and the toolbar refuses the save.
  it('marks a value outside the bounds', () => {
    const { input } = mount('risk_usage_ratio', 1.2)
    expect(input.getAttribute('aria-invalid')).toBe('true')
    expect(description(input)).toContain('超出允许范围')
  })

  it('does not mark an unset value, whatever its bounds', () => {
    const { input } = mount('risk_usage_ratio', 0)
    expect(input.getAttribute('aria-invalid')).toBe('false')
  })

  // U18: the hint says what the knob means; the tail says its range and
  // default, taken from the served defaults.
  it('ends the hint with the range and the served default', () => {
    expect(description(mount('geo_anomaly_min_placed_ratio', 0).input)).toContain('范围 0–1，默认 0.5')
    cleanup()
    expect(description(mount('risk_usage_ratio', 0).input)).toContain('不小于 1.5，默认 3')
    cleanup()
    const own = description(mount('risk_max_devices', 0).input)
    expect(own).toContain(dict.current['settings.risk.max_devices_hint'])
    expect(own).toContain('默认 3')
  })

  // A runtime knob's hint states its own default and range; its caption
  // is the number the server runs with.
  it('captions a runtime knob with its value in effect and no tail', () => {
    const { input } = mount('geo_anomaly_fresh_window_seconds', 0, 42)
    const text = description(input)
    expect(text).toContain('当前生效：42')
    expect(text).not.toContain('范围 20–900，默认 120')
    expect(input.placeholder).toBe('120')
  })
})
