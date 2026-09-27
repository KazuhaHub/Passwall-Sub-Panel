// Chinese names for the provinces a location database reports, chosen by the
// region's ISO 3166-2 code rather than by its English name. The code rides
// beside the name on the Geo tab's spots and on sub_spread provinces (`rc`);
// it is display only, and nothing is ever grouped or judged by it.
//
// Codes live here and names live in the locale files (admin:geo_regions.cn.*).
// zh-TW is a built-in UI language generated from zh-CN by OpenCC at prebuild,
// so a name in the bundle reaches a Traditional reader as 廣東 / 臺灣 / 內蒙古
// with no work here; a string table in this file would show them simplified.
import type { Translate } from './geoAnomaly'

/**
 * The region part of every ISO 3166-2:CN code (the "GD" of "CN-GD"), as
 * MaxMind's subdivisions[].iso_code carries it. 23 provinces (TW included),
 * 5 autonomous regions, 4 municipalities, 2 SARs. MaxMind files HK and TW
 * (and, by the same ISO 3166-1 rule, MO) as countries, so those three are
 * reached only if a database files them under CN. Names live in
 * admin:geo_regions.cn.* so zh-TW gets them through OpenCC.
 */
export const CN_SUBDIVISION_CODES: readonly string[] = [
  'AH', 'BJ', 'CQ', 'FJ', 'GD', 'GS', 'GX', 'GZ', 'HA', 'HB', 'HE', 'HI', 'HK', 'HL', 'HN', 'JL', 'JS',
  'JX', 'LN', 'MO', 'NM', 'NX', 'QH', 'SC', 'SD', 'SH', 'SN', 'SX', 'TJ', 'TW', 'XJ', 'XZ', 'YN', 'ZJ',
]

/**
 * The pre-2017-11-23 numeric codes (CN-44 → GD). Current GeoLite2 files are
 * alphabetic, but MaxMind's own test database still files its CN record
 * under "22", and older or other databases may too.
 */
const CN_NUMERIC_CODES: Readonly<Record<string, string>> = {
  '11': 'BJ', '12': 'TJ', '13': 'HE', '14': 'SX', '15': 'NM', '21': 'LN', '22': 'JL', '23': 'HL',
  '31': 'SH', '32': 'JS', '33': 'ZJ', '34': 'AH', '35': 'FJ', '36': 'JX', '37': 'SD', '41': 'HA',
  '42': 'HB', '43': 'HN', '44': 'GD', '45': 'GX', '46': 'HI', '50': 'CQ', '51': 'SC', '52': 'GZ',
  '53': 'YN', '54': 'XZ', '61': 'SN', '62': 'GS', '63': 'QH', '64': 'NX', '65': 'XJ', '71': 'TW',
  '91': 'HK', '92': 'MO',
}

/** The alphabetic CN code for rc (either form), '' when it is none. rc is
 *  trimmed, checked against /^[A-Za-z0-9]{1,3}$/ BEFORE upper-casing (as the
 *  server does), then mapped. */
export function cnSubdivisionCode(rc: string | undefined): string {
  const s = (rc ?? '').trim()
  // The server already normalizes, and this repeats its rule rather than
  // trusting it: toUpperCase maps "ſ" to "S" and "ı" to "I", so checking
  // after upper-casing would name "gſ" Gansu.
  if (!/^[A-Za-z0-9]{1,3}$/.test(s)) return ''
  const code = s.toUpperCase()
  const alpha = Object.hasOwn(CN_NUMERIC_CODES, code) ? CN_NUMERIC_CODES[code] : code
  return CN_SUBDIVISION_CODES.includes(alpha) ? alpha : ''
}

export interface RegionRef { cc: string; region: string; rc?: string }
export type RegionNamer = (p: RegionRef) => string

/**
 * Chinese UI (language starting "zh"), cc CN and a known code → the localized
 * name (defaultValue: the database's region). Anything else → p.region
 * unchanged, the same spelling the sub-log region column shows. t is never
 * called for a non-Chinese UI, so the en-US block is parity only.
 */
export function regionNamer(t: Translate, language: string | undefined): RegionNamer {
  // Only a Chinese reader gains from the table: an English UI already reads
  // the database's English, and a translated English name that differed from
  // the sub-log column's spelling would make one province look like two.
  if (!(language ?? '').toLowerCase().startsWith('zh')) return p => p.region
  return p => {
    // The code table is CN's alone: JP "13" is Tokyo, CN's old "13" Hebei.
    if (p.cc.trim().toUpperCase() !== 'CN') return p.region
    const code = cnSubdivisionCode(p.rc)
    if (!code) return p.region
    // A zh-* pack uploaded without these keys falls back to zh-CN through
    // i18next; the database's name is the last resort, never a raw key.
    return t(`admin:geo_regions.cn.${code}`, { defaultValue: p.region })
  }
}
