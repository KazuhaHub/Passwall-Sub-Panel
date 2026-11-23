/** @vitest-environment jsdom */
import { useState } from 'react'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

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
import zhCommon from '@/locales/zh-CN/common.json'
import { flatten, type Nested } from '@/i18n/options'
import { cardFields, POLICY_CARDS, type PolicyFieldSpec } from '@/views/admin/risk/policy/policyLayout'
import PolicyField from './PolicyField'
dict.current = { ...flatten(zhCommon as Nested), ...flatten(zh as Nested) }

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
  render(<Harness />)
  const input = screen.getByRole('textbox', { name: dict.current[spec(key).label.slice('admin:'.length)] }) as HTMLInputElement
  return { input, stored }
}

function description(input: HTMLInputElement): string {
  const id = input.getAttribute('aria-describedby') ?? ''
  return document.getElementById(id)?.textContent ?? ''
}


afterEach(cleanup)

describe('PolicyField, numbers', () => {
  // A number box that rewrites what is typed on every keystroke cannot take "0.5": "0." is not a
  // number yet. The text stays as typed; the store gets the number it reads as.
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

  // Unset is an empty field showing the default, never a 0 that reads as "zero tolerance"; the served
  // default is in the hint under the box.
  it('shows an unset value as empty, with the served default as placeholder and in the hint', () => {
    const { input } = mount('geo_anomaly_max_cities', 0)
    expect(input.value).toBe('')
    expect(input.placeholder).toBe('2')
    expect(description(input)).toContain('默认 2')
    expect(screen.queryByRole('button', { name: '恢复跟随' })).toBeNull()
  })

  it('shows a set value with a reset that stores 0', () => {
    const { input, stored } = mount('geo_anomaly_max_cities', 4)
    expect(input.value).toBe('4')
    fireEvent.click(screen.getByRole('button', { name: '恢复跟随' }))
    expect(stored).toEqual([0])
    expect(input.value).toBe('')
  })

  it('stores an emptied field as 0 once it is left', () => {
    const { input, stored } = mount('geo_anomaly_max_cities', 4)
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.blur(input)
    expect(stored).toEqual([0])
  })

  // A whole-number key refuses a fraction with a message instead of silently cutting it.
  it('does not take a fraction for an integer key', () => {
    const { input, stored } = mount('geo_anomaly_max_cities', 4)
    fireEvent.focus(input)
    fireEvent.change(input, { target: { value: '3.7' } })
    expect(stored).toEqual([])
    expect(input.getAttribute('aria-invalid')).toBe('true')
  })

  // The server clamps a ratio under 1.5 up to it; the field says so before the admin saves.
  it('marks a value outside the bounds', () => {
    const { input } = mount('risk_usage_ratio', 1.2)
    expect(input.getAttribute('aria-invalid')).toBe('true')
  })

  it('does not mark an unset value, whatever its bounds', () => {
    const { input } = mount('risk_usage_ratio', 0)
    expect(input.getAttribute('aria-invalid')).toBeNull()
  })

  // The hint says what the knob means; the tail says its range and default, taken from the served defaults.
  it('ends the hint with the range and the served default', () => {
    expect(description(mount('geo_anomaly_min_placed_ratio', 0).input)).toContain('范围 0–1，默认 0.5')
    cleanup()
    expect(description(mount('risk_usage_ratio', 0).input)).toContain('不小于 1.5，默认 3')
    cleanup()
    const own = description(mount('risk_max_devices', 0).input)
    expect(own).toContain(dict.current['settings.risk.max_devices_hint'])
    expect(own).toContain('默认 3')
  })

  // A runtime knob's hint states its own default and range; its caption is the number the server runs with.
  it('captions a runtime knob with its value in effect and no tail', () => {
    const { input } = mount('geo_anomaly_fresh_window_seconds', 0, 42)
    const text = description(input)
    expect(text).toContain('当前生效：42')
    expect(text).not.toContain('范围 20–900，默认 120')
  })
})

describe('PolicyField, the other kinds', () => {
  const spec = { key: 'k', kind: 'inverted', label: 'admin:risk_center.policy.default_adornment' } as const

  // An *_off key reads as its opposite: the switch says what is ON, and storing it writes the inverse.
  it('an inverted switch shows the opposite of the stored flag and stores the inverse', () => {
    const stored: boolean[] = []
    render(<PolicyField spec={spec} value={true} defaults={{}} onChange={v => stored.push(v as boolean)} />)
    const sw = screen.getByRole('switch')
    expect(sw.getAttribute('aria-checked')).toBe('false')
    fireEvent.click(sw)
    expect(stored).toEqual([false])
  })

  it('a select with no stored value shows its first option', () => {
    const select = { key: 's', kind: 'select', label: 'admin:risk_center.policy.default_adornment', options: [{ value: 'a', label: 'admin:risk_center.policy.default_adornment' }, { value: 'b', label: 'admin:risk_center.policy.out_of_range' }] } as const
    render(<PolicyField spec={select as never} value={''} defaults={{}} onChange={() => {}} />)
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe('a')
  })
})
