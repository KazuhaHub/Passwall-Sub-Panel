import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from './options'

// THE RISK CENTER AND THE DIAGNOSTICS PAGE READ EVERY WORD FROM THE BUNDLES.
// Their views pass no Chinese defaultValue to t() and carry no Chinese text
// of their own: a defaultValue is what an English admin reads the day a key
// goes missing, and a Chinese one there is a silent untranslated string. So
// two rules hold for every non-test source under views/admin/risk and
// views/admin/diagnostics: each literal 'admin:<key>' names a key both
// bundles have, and no CJK character appears outside a comment (comments may
// name a tab by its Chinese label).

const bundles = { zh: flatten(zh as Nested), en: flatten(en as Nested) }

/**
 * The source with its comments blanked out, strings and code kept. A small
 * scanner rather than a regex: a URL in a string holds "//", an apostrophe
 * in a comment would open a "string", and a regex literal may hold a quote.
 * Template literals nest through ${ }, so the scanner descends into them.
 */
function withoutComments(src: string): string {
  let i = 0
  let out = ''
  // The last significant character and word of code, to tell a regex
  // literal's opening slash from a division.
  let last = ''
  let word = ''
  const regexMayStart = () => last === '' || '(,=:[!&|?{};'.includes(last)
    || ['return', 'typeof', 'case', 'in', 'of', 'void', 'throw'].includes(word)

  const copyEscape = () => { out += src.slice(i, i + 2); i += 2 }

  const quoted = (quote: string) => {
    out += src[i++]
    while (i < src.length) {
      const c = src[i]
      if (c === '\\') { copyEscape(); continue }
      out += c
      i++
      if (c === quote || c === '\n') return
    }
  }

  const template = () => {
    out += src[i++]
    while (i < src.length) {
      const c = src[i]
      if (c === '\\') { copyEscape(); continue }
      if (c === '$' && src[i + 1] === '{') {
        out += '${'
        i += 2
        code(true)
        continue
      }
      out += c
      i++
      if (c === '`') return
    }
  }

  const regex = () => {
    let inClass = false
    out += src[i++]
    while (i < src.length) {
      const c = src[i]
      if (c === '\\') { copyEscape(); continue }
      out += c
      i++
      if (c === '[') inClass = true
      else if (c === ']') inClass = false
      else if ((c === '/' && !inClass) || c === '\n') return
    }
  }

  // Code up to the brace closing a template's ${ (inTemplate), or to the end.
  function code(inTemplate: boolean) {
    let depth = 0
    while (i < src.length) {
      const c = src[i]
      const next = src[i + 1]
      if (c === '/' && next === '/') {
        while (i < src.length && src[i] !== '\n') i++
        continue
      }
      if (c === '/' && next === '*') {
        const end = src.indexOf('*/', i + 2)
        i = end < 0 ? src.length : end + 2
        out += ' '
        continue
      }
      if (c === '\'' || c === '"') { quoted(c); last = 'x'; word = ''; continue }
      if (c === '`') { template(); last = 'x'; word = ''; continue }
      if (c === '/' && regexMayStart()) { regex(); last = 'x'; word = ''; continue }
      if (inTemplate && c === '{') depth++
      if (inTemplate && c === '}') {
        if (depth === 0) { out += c; i++; return }
        depth--
      }
      out += c
      i++
      if (/[\w$]/.test(c)) word = /[\w$]/.test(src[i - 2] ?? '') ? word + c : c
      else if (!/\s/.test(c)) word = ''
      if (!/\s/.test(c)) last = c
    }
  }

  code(false)
  return out
}

/** Every literal 'admin:<key>' in the source; a prefix ("admin:x.") and a
 *  key built at run time (`admin:x.${k}`) are not literals. */
function literalAdminKeys(src: string): string[] {
  const keys = new Set<string>()
  for (const m of withoutComments(src).matchAll(/(['"`])admin:([A-Za-z0-9_.-]+)\1/g)) {
    if (!m[2].endsWith('.') && !m[2].endsWith('_')) keys.add(m[2])
  }
  return [...keys]
}

/** The literal keys either bundle lacks, each as "<lang>: <key>". */
function missingKeys(src: string): string[] {
  const out: string[] = []
  for (const key of literalAdminKeys(src)) {
    for (const [lang, dict] of Object.entries(bundles)) {
      if (typeof dict[key] !== 'string' || dict[key] === '') out.push(`${lang}: ${key}`)
    }
  }
  return out
}

/** Each run of CJK characters outside the comments. */
function cjkOutsideComments(src: string): string[] {
  return withoutComments(src).match(/[　-〿一-鿿＀-￯]+/g) ?? []
}

const viewDir = (rel: string) => fileURLToPath(new URL(rel, import.meta.url))

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap(name => {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) return sources(path)
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [path] : []
  })
}

describe('the key checkers', () => {
  // The checkers themselves, on planted input: a guard that finds nothing
  // because it looks at nothing must fail here first.
  it('finds a planted missing key and ignores prefixes and built keys', () => {
    const fixture = [
      "t('admin:risk_center.title')",
      't("admin:risk_center.no_such_key")',
      "const A = 'admin:risk_center.actions.'",
      't(`admin:risk_center.flags.source.${src}`)',
      '<HelpTip textKey="admin:risk_center.also_missing" />',
      "// t('admin:risk_center.only_in_a_comment')",
    ].join('\n')
    expect(missingKeys(fixture)).toEqual([
      'zh: risk_center.no_such_key', 'en: risk_center.no_such_key',
      'zh: risk_center.also_missing', 'en: risk_center.also_missing',
    ])
  })

  it('finds a planted Chinese literal and skips Chinese comments', () => {
    const fixture = [
      "// 概览: a comment's label, with an apostrophe",
      '/* 连接 tab */ const url = \'https://example.com/a\'',
      "t('admin:x', { defaultValue: '选择用户' })",
      'const re = /["\']/',
      'const s = `a ${b ? `c` : \'d\'} 设备`',
    ].join('\n')
    expect(cjkOutsideComments(fixture)).toEqual(['选择用户', '设备'])
  })
})

describe.each([
  // The floor on files keeps a moved or renamed directory from passing by
  // scanning nothing.
  { dir: '../views/admin/risk', name: 'views/admin/risk', atLeast: 11 },
  { dir: '../views/admin/diagnostics', name: 'views/admin/diagnostics', atLeast: 10 },
  { dir: '../views/admin/accessControl', name: 'views/admin/accessControl', atLeast: 20 },
])('$name reads every word from the bundles', ({ dir, name, atLeast }) => {
  const files = sources(viewDir(dir))

  it(`scans the views under ${name}`, () => {
    expect(files.length).toBeGreaterThanOrEqual(atLeast)
  })

  it(`every literal admin key under ${name} exists in both bundles`, () => {
    const found = files.flatMap(f => missingKeys(readFileSync(f, 'utf8')).map(k => `${f}: ${k}`))
    expect(found).toEqual([])
  })

  it(`no Chinese text outside comments under ${name}`, () => {
    const found = files.flatMap(f => cjkOutsideComments(readFileSync(f, 'utf8')).map(s => `${f}: ${s}`))
    expect(found).toEqual([])
  })
})

// ONE ACTION, ONE NAME (spec §3.7). The risk center's service actions are the
// Users page's: an admin who suspends a user from the row menu and then opens
// the risk drawer from the same row must meet the same words, and the toast
// and the resulting state must not call it something else.
describe('the service actions read as the Users page names them', () => {
  it.each(Object.entries(bundles))('%s: pause and resume are the row menu\'s words', (_lang, dict) => {
    expect(dict['risk_center.actions.pause']).toBe(dict['users.more_menu.suspend_service'])
    expect(dict['risk_center.actions.resume']).toBe(dict['users.more_menu.resume_service'])
  })

  it('never calls suspending proxy service "pausing" it in English', () => {
    const paused = Object.entries(bundles.en)
      .filter(([, v]) => /paus\w*\s+proxy service|proxy service\s+(is\s+)?paused/i.test(v))
      .map(([k]) => k)
    expect(paused).toEqual([])
  })
})
