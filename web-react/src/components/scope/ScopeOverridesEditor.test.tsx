// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ScopeOverridesEditor from './ScopeOverridesEditor'
import type { ScopeState } from './scopeOverrides'

// A real dictionary for the keys under test, so an assertion on "Region /
// state" proves the option went through its translation key rather than
// printing the raw stored code. Everything else falls back to its default.
const dict: Record<string, string> = {
  'admin:groups.scope.geo_scope_city': 'City, tiered',
  'admin:groups.scope.geo_scope_region': 'Region / state',
  'admin:groups.scope.geo_scope_country': 'Country only',
  'admin:groups.scope.geo_scope_off': 'Off',
  'admin:groups.scope.default_suffix': ' (default)',
  'admin:groups.scope.global_prefix': 'Global',
}
// The editor is pure; its module's load/save helpers are not under test, and
// importing the real API client would drag in the i18n bootstrap.
vi.mock('@/api/client', () => ({ client: {} }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string, o?: { defaultValue?: string }) => dict[k] ?? o?.defaultValue ?? k,
    i18n: { language: 'en-US' },
  }),
}))

afterEach(cleanup)

function state(key: string, global: string, edit: { on: boolean; value: string }): ScopeState {
  return {
    overridable: [key],
    global: { [key]: global },
    orig: edit.on ? { [key]: edit.value } : {},
    edit: { [key]: edit },
  }
}

function mount(scope: ScopeState) {
  const onChange = vi.fn()
  render(<ScopeOverridesEditor scope={scope} onChange={onChange} categories={['geo', 'geo_ban', 'risk']} />)
  return onChange
}

describe('ScopeOverridesEditor, enum rows', () => {
  it('renders an overridden enum as a select with translated options', () => {
    const onChange = mount(state('geo_anomaly.scope', '', { on: true, value: 'region' }))

    const select = screen.queryByRole('combobox')
    expect(select?.textContent).toBe('Region / state')

    fireEvent.mouseDown(select!)
    const options = within(screen.getByRole('listbox')).getAllByRole('option')
    expect(options.map(o => o.textContent)).toEqual(['City, tiered', 'Region / state', 'Country only', 'Off'])

    fireEvent.click(options[2])
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      edit: { 'geo_anomaly.scope': { on: true, value: 'country' } },
    }))
  })

  it('shows an inherited empty scope as the default label', () => {
    // '' is what the server stores when nobody chose a scope, and it judges
    // that as city. "Global: " followed by nothing would read as "off".
    mount(state('geo_anomaly.scope', '', { on: false, value: '' }))
    expect(screen.queryByText('Global: City, tiered (default)')).not.toBeNull()
  })

  it('shows an inherited chosen scope by its label, without the default suffix', () => {
    mount(state('geo_anomaly.scope', 'country', { on: false, value: 'country' }))
    expect(screen.queryByText('Global: Country only')).not.toBeNull()
  })

  it('seeds the default when an override is switched on from an unset scope', () => {
    // Switching on copies the inherited value. An inherited '' is not one of
    // the options, so the select would open blank and save '' — the default
    // it stands for is what the admin is looking at, so that is what is seeded.
    const onChange = mount(state('geo_anomaly.scope', '', { on: false, value: '' }))
    fireEvent.click(screen.getAllByRole('switch')[0])
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({
      edit: { 'geo_anomaly.scope': { on: true, value: 'city' } },
    }))
  })
})

describe('ScopeOverridesEditor, unset numeric rows', () => {
  it('shows an inherited 0 int as its unset default', () => {
    // The geo tolerances read 0 as "never configured". "Global: 0" would tell
    // a group admin the fleet tolerates no second city at all.
    mount(state('geo_anomaly.max_cities', '0', { on: false, value: '0' }))
    expect(screen.queryByText('Global: 2 (default)')).not.toBeNull()
  })

  it('shows a configured int as itself', () => {
    mount(state('geo_anomaly.max_cities', '4', { on: false, value: '4' }))
    expect(screen.queryByText('Global: 4')).not.toBeNull()
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
      expect(screen.queryByText(/\(default\)/), `override ${JSON.stringify(v)}`).toBeNull()
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
    render(<ScopeOverridesEditor onChange={vi.fn()} categories={['notify']}
      scope={state('notify.expire_before_days', '3', { on: true, value: '0' })} />)
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
      expect(screen.queryByRole('spinbutton', { description: '= 3 (default)' }), `override ${v}`).not.toBeNull()
      cleanup()
    }
  })

  it('reads ParseFloat-only spellings as the number they are', () => {
    // +Inf parses, and is kept as the largest finite ratio: "never over",
    // which is what an infinite ratio says. Underscores and hex floats are
    // Go float syntax. Each one applies, so no hint.
    for (const v of ['Inf', '+infinity', '1_5', '0x1.8p1']) {
      mount(state('risk.usage_ratio', '4', { on: true, value: v }))
      expect(screen.queryByRole('spinbutton'), `override ${v}`).not.toBeNull()
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
  afterEach(() => vi.unstubAllGlobals())

  function mountWithHints(scope: ScopeState) {
    render(<ScopeOverridesEditor scope={scope} onChange={vi.fn()} categories={['geo', 'geo_ban', 'risk']} showHints />)
  }

  // The risk center's group card explains each row where the global field
  // is explained; the other hosts keep their compact rows.
  it('shows the hint under the label only when asked', () => {
    mountWithHints(state('geo_anomaly.max_cities', '0', { on: false, value: '0' }))
    expect(screen.getByText('admin:settings.geo_anomaly.max_cities_hint')).toBeTruthy()
    cleanup()
    mount(state('geo_anomaly.max_cities', '0', { on: false, value: '0' }))
    expect(screen.queryByText('admin:settings.geo_anomaly.max_cities_hint')).toBeNull()
  })

  // U11: three fixed columns beside a hint leave the label a sliver on a
  // phone. There the label and its hint take a line of their own, and the
  // switch and the value share the line under it.
  it('gives the label and hint their own line on a phone', () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: true, media: query, onchange: null, addListener: vi.fn(), removeListener: vi.fn(),
      addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    }))
    mountWithHints(state('geo_anomaly.max_cities', '4', { on: false, value: '4' }))
    const hint = screen.getByText('admin:settings.geo_anomaly.max_cities_hint')
    const labelLine = hint.parentElement as HTMLElement
    expect(within(labelLine).getByText('城市容错')).toBeTruthy()
    expect(within(labelLine).queryByRole('switch')).toBeNull()
    const row = labelLine.parentElement as HTMLElement
    expect(row.children).toHaveLength(2)
    const controls = row.children[1] as HTMLElement
    expect(within(controls).getByRole('switch')).toBeTruthy()
    expect(within(controls).getByText('Global: 4')).toBeTruthy()
  })

  it('keeps label, switch and value on one line on a wide screen', () => {
    mountWithHints(state('geo_anomaly.max_cities', '4', { on: false, value: '4' }))
    const hint = screen.getByText('admin:settings.geo_anomaly.max_cities_hint')
    const row = (hint.parentElement as HTMLElement).parentElement as HTMLElement
    expect(row.children).toHaveLength(3)
  })
})
