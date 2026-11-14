import { describe, expect, it } from 'vitest'
import { normalizeTarget, parseResolvedIps, validIp, validPort } from './testInput'

describe('normalizeTarget', () => {
  it.each([
    ['smtp.gmail.com', 'smtp.gmail.com', null, false],
    ['https://www.example.com/a/b?q=1', 'www.example.com', null, true],
    ['https://user:pw@example.com:8443/x', 'example.com', 8443, true],
    ['example.com/path', 'example.com', null, true],
    ['1.2.3.4', '1.2.3.4', null, false],
    ['[2001:db8::1]', '2001:db8::1', null, false],
  ])('%s', (raw, host, port, stripped) => {
    expect(normalizeTarget(raw)).toEqual({ host, port, stripped })
  })
})

describe('validators', () => {
  it('ip', () => {
    expect(validIp('')).toBe(true); expect(validIp('10.0.0.1')).toBe(true); expect(validIp('2001:db8::1')).toBe(true)
    expect(validIp('300.1.1.1')).toBe(false); expect(validIp('abc')).toBe(false)
  })
  it('port', () => {
    expect(validPort('')).toBe(true); expect(validPort('587')).toBe(true)
    expect(validPort('0')).toBe(false); expect(validPort('70000')).toBe(false); expect(validPort('x')).toBe(false)
  })
})

describe('parseResolvedIps', () => {
  it('empty is no list and valid', () => {
    expect(parseResolvedIps('')).toEqual({ ips: [], bad: false })
    expect(parseResolvedIps('  ,  ')).toEqual({ ips: [], bad: false })
  })
  it('splits on commas and whitespace', () => {
    expect(parseResolvedIps('10.0.0.1, 2001:db8::1  192.168.1.1')).toEqual({ ips: ['10.0.0.1', '2001:db8::1', '192.168.1.1'], bad: false })
  })
  it('a bad literal or more than 16 is bad', () => {
    expect(parseResolvedIps('10.0.0.1 nope').bad).toBe(true)
    expect(parseResolvedIps(Array.from({ length: 17 }, (_, i) => `10.0.0.${i}`).join(',')).bad).toBe(true)
    expect(parseResolvedIps(Array.from({ length: 16 }, (_, i) => `10.0.0.${i}`).join(',')).bad).toBe(false)
  })
})
