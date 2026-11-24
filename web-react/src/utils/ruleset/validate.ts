/** Format checks for the value of a simple rule, for the single-row form (design 3.5). */

export function isIPv4(s: string): boolean {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(?:\/(\d{1,2}))?$/.exec(s)
  if (!m) return false
  return [m[1], m[2], m[3], m[4]].every(o => Number(o) <= 255) && (m[5] === undefined || Number(m[5]) <= 32)
}

export function isIPv6(s: string): boolean {
  const [host, prefix, ...rest] = s.split('/')
  if (rest.length > 0 || !host.includes(':') || !/^[0-9a-fA-F:.]+$/.test(host)) return false
  if (prefix !== undefined && !(/^\d{1,3}$/.test(prefix) && Number(prefix) <= 128)) return false
  try { new URL(`http://[${host}]/`); return true } catch { return false }
}

export type ValueProblem = 'value_empty' | 'value_comma' | 'value_space' | 'value_cidr' | 'value_port' | 'value_network'

/** What is wrong with `value` for `type`, as a locale key suffix, or null. Types it knows nothing about get only the structural checks. */
export function validateRuleValue(type: string, value: string): ValueProblem | null {
  const v = value.trim()
  if (v === '') return 'value_empty'
  if (/[,\r\n]/.test(v)) return 'value_comma'
  switch (type.toUpperCase()) {
    case 'DOMAIN': case 'DOMAIN-SUFFIX': case 'DOMAIN-KEYWORD': case 'DOMAIN-WILDCARD':
      return /\s/.test(v) ? 'value_space' : null
    case 'IP-CIDR': return isIPv4(v) ? null : 'value_cidr'
    case 'IP-CIDR6': return isIPv6(v) ? null : 'value_cidr'
    case 'SRC-IP-CIDR': return isIPv4(v) || isIPv6(v) ? null : 'value_cidr'
    case 'DST-PORT': case 'SRC-PORT': case 'IN-PORT': {
      const m = /^(\d{1,5})(?:-(\d{1,5}))?$/.exec(v)
      return m && Number(m[1]) <= 65535 && (m[2] === undefined || Number(m[2]) <= 65535) ? null : 'value_port'
    }
    case 'NETWORK': return /^(tcp|udp)$/i.test(v) ? null : 'value_network'
    default: return null
  }
}
