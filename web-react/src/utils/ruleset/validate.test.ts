import { describe, expect, it } from 'vitest'
import { validateRuleValue } from './validate'

describe('validateRuleValue', () => {
  it.each([
    ['DOMAIN-SUFFIX', 'example.com'], ['DOMAIN', 'a.b.example'], ['DOMAIN-KEYWORD', 'ads'], ['DOMAIN-REGEX', '^a\\.example$'],
    ['IP-CIDR', '10.0.0.0/8'], ['IP-CIDR', '1.2.3.4'], ['IP-CIDR6', 'fc00::/7'], ['SRC-IP-CIDR', '192.168.1.0/24'], ['SRC-IP-CIDR', 'fe80::/10'],
    ['DST-PORT', '443'], ['DST-PORT', '1000-2000'], ['NETWORK', 'udp'], ['NETWORK', 'TCP'], ['GEOIP', 'CN'], ['GEOSITE', 'geolocation-cn'], ['PROCESS-NAME', 'OneDrive'],
  ])('accepts %s %s', (type, value) => expect(validateRuleValue(type, value)).toBeNull())

  it.each([
    ['DOMAIN-SUFFIX', '', 'value_empty'], ['DOMAIN-SUFFIX', '  ', 'value_empty'],
    ['DOMAIN-SUFFIX', 'a b.example', 'value_space'], ['DOMAIN', 'a,b', 'value_comma'], ['PROCESS-NAME', 'a\nb', 'value_comma'],
    ['IP-CIDR', '10.0.0.0/33', 'value_cidr'], ['IP-CIDR', '300.1.1.1', 'value_cidr'], ['IP-CIDR', 'fc00::/7', 'value_cidr'],
    ['IP-CIDR6', '10.0.0.0/8', 'value_cidr'], ['IP-CIDR6', 'zz::1', 'value_cidr'], ['SRC-IP-CIDR', 'nope', 'value_cidr'],
    ['DST-PORT', '70000', 'value_port'], ['DST-PORT', 'http', 'value_port'], ['NETWORK', 'icmp', 'value_network'],
  ])('rejects %s %j as %s', (type, value, why) => expect(validateRuleValue(type, value)).toBe(why))
})
