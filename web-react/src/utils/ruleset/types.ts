/**
 * The rule types the panel knows about. `SIMPLE_TYPES` are the ones whose line is exactly `TYPE,value,target[,options]`
 * and so can be edited with a form; everything else (logical rules, SUB-RULE, MATCH, REMATCH-NAME, a type nobody
 * knows) is shown and moved like any other line but edited as text.
 *
 * `COMMON_TYPES` is the short list the type picker leads with (design 3.5); `SINGBOX_TYPES` is what the sing-box
 * compiler keeps (internal/service/render/singbox.go parseClashRuleLine, singBoxRouteRule). Anything else is
 * silently skipped there, which is worth saying next to the type.
 */
export const COMMON_TYPES = [
  'DOMAIN-SUFFIX', 'DOMAIN', 'DOMAIN-KEYWORD', 'IP-CIDR', 'IP-CIDR6', 'GEOSITE', 'GEOIP', 'PROCESS-NAME', 'DST-PORT', 'SRC-IP-CIDR', 'NETWORK',
] as const

export const MORE_TYPES = [
  'DOMAIN-REGEX', 'DOMAIN-WILDCARD', 'IP-ASN', 'IP-SUFFIX', 'SRC-IP-SUFFIX', 'SRC-IP-ASN', 'SRC-GEOIP', 'SRC-PORT', 'IN-PORT', 'IN-TYPE', 'IN-USER', 'IN-NAME',
  'PROCESS-PATH', 'PROCESS-NAME-REGEX', 'PROCESS-PATH-REGEX', 'UID', 'DSCP',
] as const

export const SIMPLE_TYPES: ReadonlySet<string> = new Set<string>([...COMMON_TYPES, ...MORE_TYPES])

/** Every type the table can name, simple or not. */
export const KNOWN_TYPES: ReadonlySet<string> = new Set<string>([...SIMPLE_TYPES, 'AND', 'OR', 'NOT', 'SUB-RULE', 'MATCH', 'RULE-SET', 'REMATCH-NAME'])

export const SINGBOX_TYPES: ReadonlySet<string> = new Set([
  'DOMAIN', 'DOMAIN-SUFFIX', 'DOMAIN-KEYWORD', 'IP-CIDR', 'IP-CIDR6', 'SRC-IP-CIDR', 'GEOSITE', 'GEOIP', 'PROCESS-NAME', 'DST-PORT', 'NETWORK', 'AND', 'MATCH',
])

/** The two flags a rule can carry after its target. */
export const RULE_OPTIONS: ReadonlySet<string> = new Set(['no-resolve', 'src'])

/** IP-style rules, which take `no-resolve`. */
export const IP_TYPES: ReadonlySet<string> = new Set(['IP-CIDR', 'IP-CIDR6', 'SRC-IP-CIDR', 'IP-SUFFIX', 'SRC-IP-SUFFIX', 'IP-ASN', 'SRC-IP-ASN', 'GEOIP', 'SRC-GEOIP'])

/** Built-in outbounds a rule can name (internal/service/render/proxy_groups.go). */
export const BUILTIN_TARGETS = ['DIRECT', 'REJECT', 'REJECT-DROP', 'PASS'] as const
