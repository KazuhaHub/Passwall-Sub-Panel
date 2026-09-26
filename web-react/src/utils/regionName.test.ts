import { describe, expect, it, vi } from 'vitest'

// The two SHIPPED bundles, imported by name. Never a glob over locales/*/:
// zh-TW is generated from zh-CN at prebuild and gitignored, so a glob would
// read it on a developer's machine and not in CI.
import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import type { Translate } from './geoAnomaly'
import { CN_SUBDIVISION_CODES, cnSubdivisionCode, regionNamer } from './regionName'

// ISO 3166-2:CN's change of 2017-11-23, which replaced the numeric codes with
// the alphabetic ones, pair by pair. Copied here from the ISO change list, not
// from regionName.ts, so a typo in either table shows up as a disagreement.
const NUMERIC_TO_ALPHA: Record<string, string> = {
  '11': 'BJ', '12': 'TJ', '13': 'HE', '14': 'SX', '15': 'NM', '21': 'LN', '22': 'JL', '23': 'HL',
  '31': 'SH', '32': 'JS', '33': 'ZJ', '34': 'AH', '35': 'FJ', '36': 'JX', '37': 'SD', '41': 'HA',
  '42': 'HB', '43': 'HN', '44': 'GD', '45': 'GX', '46': 'HI', '50': 'CQ', '51': 'SC', '52': 'GZ',
  '53': 'YN', '54': 'XZ', '61': 'SN', '62': 'GS', '63': 'QH', '64': 'NX', '65': 'XJ', '71': 'TW',
  '91': 'HK', '92': 'MO',
}

// The geo_regions.cn object of one shipped bundle, as the file nests it.
function cnNames(bundle: unknown): Record<string, unknown> {
  const regions = (bundle as { geo_regions?: { cn?: Record<string, unknown> } }).geo_regions
  return regions?.cn ?? {}
}

// A stand-in for i18next's t over one shipped bundle, as riskSignals.test.ts
// builds it: the admin namespace flattened the way the SPA registers it, and
// the defaultValue only when the key is absent.
function translator(bundle: Nested): Translate {
  const dict = flatten(bundle)
  return (key, opts = {}) => {
    const flat = key.startsWith('admin:') ? key.slice('admin:'.length) : key
    return dict[flat] ?? (typeof opts.defaultValue === 'string' ? opts.defaultValue : key)
  }
}

describe('CN_SUBDIVISION_CODES', () => {
  it('is the 34 alphabetic ISO 3166-2:CN codes, sorted, SARs and TW included', () => {
    expect(CN_SUBDIVISION_CODES.length).toBe(34)
    expect(new Set(CN_SUBDIVISION_CODES).size).toBe(34)
    for (const c of CN_SUBDIVISION_CODES) expect(c).toMatch(/^[A-Z]{2}$/)
    expect([...CN_SUBDIVISION_CODES].sort()).toEqual([...CN_SUBDIVISION_CODES])
    // MaxMind files these three as countries; they are here for a database
    // that files them under CN, which ISO 3166-2:CN also lists.
    for (const c of ['HK', 'MO', 'TW']) expect(CN_SUBDIVISION_CODES).toContain(c)
  })

  it('is exactly the provinces both shipped bundles name', () => {
    // A code without a name would fall back to English in a Chinese UI; a
    // name without a code is a string nothing ever asks for.
    // Held to the ISO list above as well, so an empty table and an empty
    // bundle cannot agree with each other and pass.
    const codes = [...CN_SUBDIVISION_CODES].sort()
    const iso = Object.values(NUMERIC_TO_ALPHA).sort()
    expect(Object.keys(cnNames(zh)).sort()).toEqual(iso)
    expect(Object.keys(cnNames(en)).sort()).toEqual(iso)
    expect(codes).toEqual(iso)
  })

  it('names every province in Chinese characters in zh-CN', () => {
    const names = cnNames(zh)
    for (const c of CN_SUBDIVISION_CODES) {
      expect(names[c], c).toMatch(/^[一-鿿]+$/)
    }
    expect(names).toMatchObject({
      GD: '广东', BJ: '北京', SH: '上海', NM: '内蒙古', HK: '香港', MO: '澳门', TW: '台湾',
    })
  })
})

describe('cnSubdivisionCode', () => {
  it.each([
    ['GD', 'GD'],
    [' gd ', 'GD'],
    // The pre-2017 numeric form: MaxMind's own test database still files its
    // CN record under "22".
    ['44', 'GD'],
    ['22', 'JL'],
    ['91', 'HK'],
    ['71', 'TW'],
    // Plausible codes that are not CN's.
    ['ZZ', ''],
    ['99', ''],
    // The ASCII check comes before upper-casing, as on the server:
    // toUpperCase maps "ſ" to "S", so the other order would accept "GS".
    ['gſ', ''],
    ['CN-GD', ''],
    ['', ''],
  ])('%j → %j', (rc, want) => {
    expect(cnSubdivisionCode(rc)).toBe(want)
  })

  it('reads an absent code as none', () => {
    expect(cnSubdivisionCode(undefined)).toBe('')
  })

  it('maps the 34 numeric codes one-to-one onto the 34 alphabetic ones', () => {
    for (const [numeric, alpha] of Object.entries(NUMERIC_TO_ALPHA)) {
      expect(cnSubdivisionCode(numeric), numeric).toBe(alpha)
    }
    const targets = Object.values(NUMERIC_TO_ALPHA)
    expect(new Set(targets).size).toBe(34)
    expect([...targets].sort()).toEqual([...CN_SUBDIVISION_CODES].sort())
  })
})

describe('regionNamer', () => {
  const zhT = translator(zh as Nested)
  const gd = { cc: 'CN', region: 'Guangdong', rc: 'GD' }

  it('names a CN province in Chinese for a Chinese UI, whichever form its code takes', () => {
    const name = regionNamer(zhT, 'zh-CN')
    expect(name(gd)).toBe('广东')
    expect(name({ ...gd, rc: 'gd' })).toBe('广东')
    expect(name({ ...gd, rc: '44' })).toBe('广东')
    expect(name({ cc: 'CN', region: 'Jilin Sheng', rc: '22' })).toBe('吉林')
    // The country code is compared the way the server writes it, and a
    // stray lower-case one still means China.
    expect(name({ ...gd, cc: ' cn ' })).toBe('广东')
  })

  it('asks the bundle for any Chinese UI, zh-TW included', () => {
    // zh-TW's names are OpenCC's conversion of zh-CN's; here the zh-CN
    // dictionary stands in for it, so the call is what is checked.
    const t = vi.fn(zhT)
    expect(regionNamer(t, 'zh-TW')(gd)).toBe('广东')
    expect(t).toHaveBeenCalledWith('admin:geo_regions.cn.GD', expect.objectContaining({ defaultValue: 'Guangdong' }))
  })

  it('keeps the database\'s own name for any other UI, without asking the bundle', () => {
    // The en-US block exists for parity only: a non-Chinese UI shows the
    // spelling the sub-log region column already uses.
    const t = vi.fn(zhT)
    expect(regionNamer(t, 'en-US')(gd)).toBe('Guangdong')
    expect(regionNamer(t, undefined)(gd)).toBe('Guangdong')
    expect(t).not.toHaveBeenCalled()
  })

  it('keeps the database\'s name when there is no CN code to look up', () => {
    const name = regionNamer(zhT, 'zh-CN')
    expect(name({ cc: 'CN', region: 'Guangdong' })).toBe('Guangdong')
    expect(name({ cc: 'CN', region: 'Guangdong', rc: 'ZZ' })).toBe('Guangdong')
    // JP "13" is Tokyo, and CN's pre-2017 "13" is Hebei: the table is CN's
    // alone.
    expect(name({ cc: 'JP', region: 'Tokyo', rc: '13' })).toBe('Tokyo')
  })

  it('falls back to the database\'s name when the bundle lacks the province', () => {
    // An uploaded zh-* pack without these keys, as i18next would resolve a
    // key it finds nowhere.
    const bare: Translate = (key, opts = {}) => (typeof opts.defaultValue === 'string' ? opts.defaultValue : key)
    expect(regionNamer(bare, 'zh-CN')(gd)).toBe('Guangdong')
  })
})
