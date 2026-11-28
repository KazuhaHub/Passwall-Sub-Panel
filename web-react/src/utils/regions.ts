import { countryFlag } from './geo'

/** ISO 3166-1 alpha-2, plus XK (Kosovo, user-assigned but in wide use). A node's region is stored as one of these. */
const CODES = ('AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ '
  + 'CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR '
  + 'GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP '
  + 'KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT '
  + 'MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW '
  + 'SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG '
  + 'UM US UY UZ VA VC VE VG VI VN VU WF WS XK YE YT ZA ZM ZW').split(' ')

export interface RegionOption {
  value: string
  /** "🇹🇼 Taiwan · TW"; in another language the English name follows the local one, so either can be typed. */
  label: string
}

const nameCache = new Map<string, (code: string) => string | undefined>()
function names(lang: string): (code: string) => string | undefined {
  let fn = nameCache.get(lang)
  if (fn) return fn
  try {
    const dn = new Intl.DisplayNames([lang], { type: 'region', fallback: 'none' })
    fn = code => dn.of(code) ?? undefined
  } catch {
    fn = () => undefined
  }
  nameCache.set(lang, fn)
  return fn
}

/** One region as the picker and the lists show it. A value that is not a known code (an older free-text region) is shown as is. */
export function regionLabel(code: string, lang = 'en'): string {
  const c = code.trim()
  if (!/^[A-Za-z]{2}$/.test(c)) return c
  const up = c.toUpperCase()
  const local = names(lang)(up)
  const en = lang.startsWith('en') ? local : names('en')(up)
  if (!local && !en) return up
  const name = local && en && local !== en ? `${local} ${en}` : (local ?? en)
  return `${countryFlag(up)} ${name} · ${up}`
}

/**
 * Every country, the ones already in use first (most used first, as `used` gives them), then the rest by name. Values
 * in `used` that are not codes stay pickable, so an older free-text region is not lost by opening the form.
 */
export function regionOptions(used: readonly string[], lang = 'en'): RegionOption[] {
  const usedSet = new Set(used.map(u => (/^[A-Za-z]{2}$/.test(u) ? u.toUpperCase() : u)))
  const head = [...usedSet].map(value => ({ value, label: regionLabel(value, lang) }))
  const tail = CODES.filter(c => !usedSet.has(c))
    .map(value => ({ value, label: regionLabel(value, lang) }))
    .sort((a, b) => a.label.replace(/^\S+\s/, '').localeCompare(b.label.replace(/^\S+\s/, ''), lang))
  return [...head, ...tail]
}
