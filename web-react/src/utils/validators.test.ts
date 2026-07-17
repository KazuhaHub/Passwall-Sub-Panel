import { describe, expect, it } from 'vitest'

import {
  firstError,
  hasErrors,
  isEmail,
  isHostOrIP,
  isHttpUrl,
  isIsoDate,
  isNonEmpty,
  isPort,
  isStrongPassword,
  validateEmail,
  validateName,
  validateNonNegativeInt,
  validateNonNegativeNumber,
  validatePassword,
  validatePort,
  validateRange,
} from './validators'

describe('primitive validators', () => {
  it.each([
    [null, false],
    [undefined, false],
    ['', false],
    ['  ', false],
    [' value ', true],
    [0, true],
    [Number.NaN, false],
    [false, true],
  ])('isNonEmpty(%o) is %s', (value, expected) => {
    expect(isNonEmpty(value)).toBe(expected)
  })

  it('validates common email shapes without accepting whitespace or missing domains', () => {
    expect(isEmail(' user@example.com ')).toBe(true)
    expect(isEmail('user@localhost')).toBe(false)
    expect(isEmail('user @example.com')).toBe(false)
  })

  it.each([
    ['https://example.com/path?q=1', true],
    ['http://localhost:8080', true],
    ['https://127.0.0.1', true],
    ['https://[2001:db8::1]/', true],
    ['ftp://example.com', false],
    ['http://127.0.0', false],
    ['http://256.0.0.1', false],
    ['not a url', false],
  ])('isHttpUrl(%s) is %s', (value, expected) => {
    expect(isHttpUrl(value)).toBe(expected)
  })

  it('validates host, port and password boundaries', () => {
    expect(isHostOrIP('node.example.com')).toBe(true)
    expect(isHostOrIP('192.168.1.10')).toBe(true)
    expect(isHostOrIP('-bad.example')).toBe(false)
    expect(isPort(1)).toBe(true)
    expect(isPort(65535)).toBe(true)
    expect(isPort(0)).toBe(false)
    expect(isPort(65536)).toBe(false)
    expect(isStrongPassword('abc12345')).toBe(true)
    expect(isStrongPassword('abcdefgh')).toBe(false)
  })

  it.each([
    ['', true],
    ['2024-02-29', true],
    ['2023-02-29', false],
    ['2024-02-30', false],
    ['2024-13-01', false],
    ['2024-1-01', false],
  ])('isIsoDate(%s) is %s', (value, expected) => {
    expect(isIsoDate(value)).toBe(expected)
  })
})

describe('field validators', () => {
  it('distinguishes optional, required, format and range errors', () => {
    expect(validateEmail('')).toBe('')
    expect(validateEmail('', { required: true })).toBe('validation.required')
    expect(validateEmail('bad')).toBe('validation.email')
    expect(validatePort(0)).toBe('')
    expect(validatePort(0, { required: true })).toBe('validation.required')
    expect(validateRange(11, 1, 10)).toBe('validation.range')
    expect(validateNonNegativeInt(1.5)).toBe('validation.non_negative_int')
    expect(validateNonNegativeNumber(1.5)).toBe('')
    expect(validatePassword('weak', { strong: true })).toBe('validation.password_strength')
  })

  it('trims names before enforcing length limits', () => {
    expect(validateName('  ab  ', { min: 3 })).toBe('validation.too_short')
    expect(validateName('  abc  ', { min: 3, max: 3 })).toBe('')
    expect(validateName('abcd', { max: 3 })).toBe('validation.too_long')
  })

  it('returns the first field error in insertion order', () => {
    const errors = { email: '', port: 'validation.port', name: 'validation.required' }
    expect(firstError(errors)).toBe('validation.port')
    expect(hasErrors(errors)).toBe(true)
    expect(hasErrors({ email: '' })).toBe(false)
  })
})
