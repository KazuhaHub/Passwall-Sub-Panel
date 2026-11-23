// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
// The request client is not under test; importing the real one would drag in the i18n bootstrap.
vi.mock('@/api/client', () => ({ client: {} }))
import i18n from '@/test/initI18n'
import { flatten, type Nested } from '@/i18n/options'
import enAdmin from '@/locales/en-US/admin.json'
import ScopeOverridesEditor from './ScopeOverridesEditor'
import type { ScopeState } from './scopeOverrides'

// The REAL English strings, so an assertion on "Region / state" proves the option went through its translation key
// rather than printing the raw stored code, and a missing key shows up as itself.
i18n.addResourceBundle('en-US', 'admin', flatten(enAdmin as Nested), true, true)

afterEach(cleanup)

function state(key: string, global: string, edit: { on: boolean; value: string }): ScopeState {
  return {
    overridable: [key],
    global: { [key]: global },
    orig: edit.on ? { [key]: edit.value } : {},
    edit: { [key]: edit },
  }
}

function mount(scope: ScopeState, props: Partial<Parameters<typeof ScopeOverridesEditor>[0]> = {}) {
  const onChange = vi.fn()
  render(<ScopeOverridesEditor scope={scope} onChange={onChange} categories={['geo', 'geo_ban', 'risk']} {...props} />)
  return onChange
}

/** The one row of an inheritance field, by what its trail shows. */
const trail = () => screen.getByRole('list', { name: 'Where the value comes from' })

describe('ScopeOverridesEditor, inheritance', () => {
  it('shows the global value, that the group follows it, and the switch to set its own', () => {
    mount(state('notify.expire_before_days', '3', { on: false, value: '3' }), { categories: ['notify'] })
    expect(trail().textContent).toContain('Global3')
    expect(trail().textContent).toContain('This groupFollows above')
    expect(screen.getByText('Following the level above, currently 3')).toBeTruthy()
    expect(screen.queryByRole('spinbutton')).toBeNull()
  })

  it('shows an override, the value in effect, and goes back to following global with nothing stored', () => {
    const onChange = mount(state('notify.expire_before_days', '3', { on: true, value: '7' }), { categories: ['notify'] })
    expect(trail().textContent).toContain('Global3')
    expect(trail().textContent).toContain('This group7')
    expect(within(trail()).getByText('In effect').closest('li')!.textContent).toContain('This group')
    fireEvent.click(screen.getByRole('radio', { name: 'Follow the level above' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      edit: { 'notify.expire_before_days': { on: false, value: '3' } },
    }))
  })

  it('starts an override from the value that was in effect, and writes what is typed', () => {
    const onChange = mount(state('notify.expire_before_days', '3', { on: false, value: '3' }), { categories: ['notify'] })
    fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      edit: { 'notify.expire_before_days': { on: true, value: '3' } },
    }))
  })

  it('renders a switch for a bool, as On or Off in the trail', () => {
    const onChange = mount(state('security.totp_enabled', '1', { on: true, value: '0' }), { categories: ['2fa'] })
    expect(trail().textContent).toContain('GlobalOn')
    expect(trail().textContent).toContain('This groupOff')
    fireEvent.click(screen.getByRole('switch'))
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ edit: { 'security.totp_enabled': { on: true, value: '1' } } }))
  })

  it('offers only the keys asked for, and never one the backend does not list', () => {
    const scope: ScopeState = {
      overridable: ['notify.expire_before_days'],
      global: { 'notify.expire_before_days': '3', 'notify.traffic_remain_percent': '10' },
      orig: {},
      edit: { 'notify.expire_before_days': { on: false, value: '3' }, 'notify.traffic_remain_percent': { on: false, value: '10' } },
    }
    mount(scope, { categories: ['notify'] })
    expect(screen.queryByText('Expiry reminder (days)')).not.toBeNull()
    expect(screen.queryByText('Traffic-low reminder (%)')).toBeNull()
    cleanup()
    mount({ ...scope, overridable: [...scope.overridable, 'notify.traffic_remain_percent'] }, { categories: ['notify'], omitKeys: ['notify.expire_before_days'] })
    expect(screen.queryByText('Expiry reminder (days)')).toBeNull()
    expect(screen.queryByText('Traffic-low reminder (%)')).not.toBeNull()
    cleanup()
    mount({ ...scope, overridable: [...scope.overridable, 'notify.traffic_remain_percent'] }, { categories: ['notify'], keys: ['notify.traffic_remain_percent'] })
    expect(screen.queryByText('Expiry reminder (days)')).toBeNull()
  })
})

describe('ScopeOverridesEditor, enum rows', () => {
  it('renders an overridden enum as a select with translated options', () => {
    const onChange = mount(state('geo_anomaly.scope', '', { on: true, value: 'region' }))

    const select = screen.getByRole('combobox') as HTMLSelectElement
    expect(select.value).toBe('region')
    expect(Array.from(select.options).map(o => o.textContent)).toEqual(['City, tiered', 'Region / state', 'Country only', 'Off'])

    fireEvent.change(select, { target: { value: 'country' } })
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      edit: { 'geo_anomaly.scope': { on: true, value: 'country' } },
    }))
  })

  it('shows an inherited empty scope as the default label', () => {
    // '' is what the server stores when nobody chose a scope, and it judges
    // that as city. A global shown as nothing would read as "off".
    mount(state('geo_anomaly.scope', '', { on: false, value: '' }))
    expect(trail().textContent).toContain('GlobalCity, tiered (default)')
  })

  it('shows an inherited chosen scope by its label, without the default suffix', () => {
    mount(state('geo_anomaly.scope', 'country', { on: false, value: 'country' }))
    expect(trail().textContent).toContain('GlobalCountry only')
    expect(trail().textContent).not.toContain('(default)')
  })

  it('starts from the default when an override is switched on from an unset scope, and writes nothing until it is edited', () => {
    // An inherited '' is not one of the options, so a select opened on it would
    // show blank and save ''. The select starts on the default it stands for, a
    // note says so, and only an edit stores it.
    const onChange = mount(state('geo_anomaly.scope', '', { on: false, value: '' }))
    fireEvent.click(screen.getByRole('radio', { name: 'Set here' }))
    expect((screen.getByRole('combobox') as HTMLSelectElement).value).toBe('city')
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'off' } })
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      edit: { 'geo_anomaly.scope': { on: true, value: 'off' } },
    }))
  })
})

describe('ScopeOverridesEditor, unset numeric rows', () => {
  it('shows an inherited 0 int as its unset default', () => {
    // The geo tolerances read 0 as "never configured". "Global: 0" would tell
    // a group admin the fleet tolerates no second city at all.
    mount(state('geo_anomaly.max_cities', '0', { on: false, value: '0' }))
    expect(trail().textContent).toContain('Global2 (default)')
  })

  it('shows a configured int as itself', () => {
    mount(state('geo_anomaly.max_cities', '4', { on: false, value: '4' }))
    expect(trail().textContent).toContain('Global4')
  })
})

describe('ScopeOverridesEditor, overridden unset numeric rows', () => {
  it('explains an overridden 0 or negative int as the default it stands for', () => {
    // The server reads a group's value exactly like the global one: a number
    // that is not positive is "never configured" and becomes the shipped
    // default. Against a global 4, a group admin typing 0 (meaning "zero" or
    // "inherit") has made the group STRICTER, and the bare 0 says neither.
    for (const v of ['0', '-1']) {
      mount(state('geo_anomaly.max_cities', '4', { on: true, value: v }))
      // By role and description: the hint belongs to the box it explains.
      expect(screen.queryByRole('spinbutton', { description: '= 2 (default)' }), `override ${v}`).not.toBeNull()
      cleanup()
    }
  })

  it('explains an overridden empty or non-integer int as the global value', () => {
    // Not the default: the settings layer skips an empty or unparsable group
    // value and leaves the inherited global one in place, so the override
    // does nothing. Promising the default here would be the same lie the
    // bare 0 told, the other way round.
    for (const v of ['', '1.5']) {
      mount(state('geo_anomaly.max_cities', '4', { on: true, value: v }))
      expect(screen.queryByText('= Global: 4'), `override ${JSON.stringify(v)}`).not.toBeNull()
      expect(screen.queryByText(/^= .*\(default\)/), `override ${JSON.stringify(v)}`).toBeNull()
      cleanup()
    }
    // ...and the global value is itself read the usual way.
    mount(state('geo_anomaly.max_cities', '0', { on: true, value: '' }))
    expect(screen.queryByText('= Global: 2 (default)')).not.toBeNull()
  })

  it('adds no hint to an overridden positive int', () => {
    mount(state('geo_anomaly.max_cities', '0', { on: true, value: '3' }))
    expect(screen.queryByText(/^=/)).toBeNull()
  })

  it('adds no hint to an int whose 0 is a real value', () => {
    // Only keys with an unsetValue read 0 as unset. Elsewhere 0 means 0, and
    // a hint would invent a default the server never applies.
    mount(state('notify.expire_before_days', '3', { on: true, value: '0' }), { categories: ['notify'] })
    expect(screen.queryByRole('spinbutton')).not.toBeNull()
    expect(screen.queryByText(/^=/)).toBeNull()
  })
})

describe('ScopeOverridesEditor, overridden unset float rows', () => {
  it('reads a fractional override as the number it is', () => {
    // The server parses a float row with strconv.ParseFloat, so 2.5 is a
    // real ratio. Read with the integer rule it would be "unparsable" and
    // the hint would claim the override does nothing.
    mount(state('risk.usage_ratio', '0', { on: true, value: '2.5' }))
    expect(screen.queryByRole('spinbutton')).not.toBeNull()
    expect(screen.queryByText(/^=/)).toBeNull()
  })

  it('still explains a 0 or an empty float override', () => {
    mount(state('risk.usage_ratio', '4', { on: true, value: '0' }))
    expect(screen.queryByRole('spinbutton', { description: '= 3 (default)' })).not.toBeNull()
    cleanup()

    mount(state('risk.usage_ratio', '4', { on: true, value: '' }))
    expect(screen.queryByText('= Global: 4')).not.toBeNull()
  })

  // A group override is stored as typed or as PUT through the API, without
  // validation, and decoded by strconv.ParseFloat. These are the values a
  // decimal regex read wrongly, in both directions.

  it('explains NaN and a negative infinity as the default', () => {
    // Both parse. RiskPolicyFromSettings keeps a ratio only when it is > 0,
    // which neither is, so the group judges with the shipped 3 — not with
    // the global 4 that "= Global" promised.
    for (const v of ['NaN', 'nan', '-Inf', '-infinity']) {
      mount(state('risk.usage_ratio', '4', { on: true, value: v }))
      expect(screen.queryByText('= 3 (default)'), `override ${v}`).not.toBeNull()
      cleanup()
    }
  })

  it('reads ParseFloat-only spellings as the number they are', () => {
    // +Inf parses, and is kept as the largest finite ratio: "never over",
    // which is what an infinite ratio says. Underscores and hex floats are
    // Go float syntax. Each one applies, so no hint.
    for (const v of ['Inf', '+infinity', '1_5', '0x1.8p1']) {
      mount(state('risk.usage_ratio', '4', { on: true, value: v }))
      expect(screen.queryByText(/^=/), `override ${v}`).toBeNull()
      cleanup()
    }
  })

  it('explains an overflowing or ill-formed float override as the global value', () => {
    // 1e400 is well formed but out of range: ParseFloat returns ErrRange,
    // floatField returns the error, and the settings layer skips the
    // override. The others are syntax ParseFloat refuses though Number()
    // or a looser pattern would take them.
    for (const v of ['1e400', '-1e400', '+nan', 'infin', '1__5', ' 2', '0x1']) {
      mount(state('risk.usage_ratio', '4', { on: true, value: v }))
      expect(screen.queryByText('= Global: 4'), `override ${JSON.stringify(v)}`).not.toBeNull()
      cleanup()
    }
  })
})

describe('ScopeOverridesEditor, multi-line strings', () => {
  it('renders a multi-line string override as a textarea', () => {
    // Co-travel is one country set per LINE; a single-line input would
    // silently flatten the sets into one.
    mount(state('geo_anomaly.co_travel', '', { on: true, value: 'JP,TW\nDE,AT' }))
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(box.tagName).toBe('TEXTAREA')
    expect(box.value).toBe('JP,TW\nDE,AT')
  })
})

describe('ScopeOverridesEditor, hints', () => {
  // The risk center's group card explains each row where the global field
  // is explained; the other hosts keep their compact rows.
  it('shows the hint under the row only when asked', () => {
    const scope = state('geo_anomaly.max_cities', '0', { on: false, value: '0' })
    const hint = i18n.t('admin:settings.geo_anomaly.max_cities_hint')
    mount(scope, { showHints: true })
    expect(screen.getByText(hint)).toBeTruthy()
    cleanup()
    mount(scope)
    expect(screen.queryByText(hint)).toBeNull()
  })
})
