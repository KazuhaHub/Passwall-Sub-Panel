// gen-locale-hant.mjs — auto-generate a Traditional-Chinese language pack from
// the shipped zh-CN (Simplified) locale files using OpenCC (opencc-js).
//
// This is a MECHANICAL conversion, not a translation: OpenCC does phrase-level
// Simplified→Traditional conversion (软件→軟體, 视频→影片, 默认→預設, 网络→網路),
// so the output reads idiomatically without any hand-translation. Re-run it
// whenever src/locales/zh-CN/* changes — zero maintenance.
//
// Usage:
//   node scripts/gen-locale-hant.mjs                  # pack for admin upload
//   node scripts/gen-locale-hant.mjs zh-TW zh-HK      # upload packs
//   node scripts/gen-locale-hant.mjs --builtin        # built-in zh-TW locale files
//   node scripts/gen-locale-hant.mjs --builtin zh-TW # same, explicit code
//
// Normal output: scripts/generated/<code>.json — upload it on the admin
// "Language packs" page, or drop it into the panel's <ConfigDir>/locales/.
// Built-in output: src/locales/<code>/*.json — generated before the Vite build
// so the compiled SPA always ships the current Traditional-Chinese resources.
//
// Safety: only string VALUES are converted; object KEYS (i18next dotted
// identifiers) and every {{...}} placeholder are left untouched, including
// placeholders whose Go-template field name contains Chinese characters.

import { readdirSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execSync } from 'node:child_process'
import * as OpenCC from 'opencc-js'

const __dirname = dirname(fileURLToPath(import.meta.url))
const SRC_DIR = join(__dirname, '..', 'src', 'locales', 'zh-CN')
const OUT_DIR = join(__dirname, 'generated')
const BUILTIN_DIR = join(__dirname, '..', 'src', 'locales')

// Pack format version — keep in sync with service/locale.Format (backend) and
// LANGUAGE_PACK_FORMAT (web-react/src/i18n/index.ts).
const PACK_FORMAT = 1

// Variant registry. `to` is an opencc-js target locale preset:
//   twp = Taiwan + idiomatic phrase conversion (軟體/影片/預設)  ← default
//   hk  = Hong Kong usage
//   t   = generic Traditional (character-only, no phrase swap)
const VARIANTS = {
  'zh-TW': { to: 'twp', name: '繁體中文' },
  'zh-HK': { to: 'hk', name: '繁體中文（香港）' },
  'zh-Hant': { to: 't', name: '繁體中文' },
}

function panelVersion() {
  try {
    return execSync('git describe --tags --abbrev=0', { cwd: __dirname, stdio: ['ignore', 'pipe', 'ignore'] })
      .toString().trim()
  } catch {
    return ''
  }
}

// convertDeep walks a translation tree, converting only string leaves. Keys and
// placeholders are preserved verbatim.
function convertDeep(value, convert) {
  if (typeof value === 'string') return convertText(value, convert)
  if (Array.isArray(value)) return value.map(v => convertDeep(v, convert))
  if (value && typeof value === 'object') {
    const out = {}
    for (const [k, v] of Object.entries(value)) out[k] = convertDeep(v, convert)
    return out
  }
  return value
}

// OpenCC must not translate identifiers inside interpolation or Go-template
// expressions. Protect the complete {{...}} spans with ASCII sentinels while
// converting the surrounding prose, then restore the exact original bytes.
function convertText(value, convert) {
  const placeholders = []
  const protectedValue = value.replace(/{{[\s\S]*?}}/g, match => {
    const token = `__PSP_OPENCC_PLACEHOLDER_${placeholders.length}__`
    placeholders.push([token, match])
    return token
  })
  const converted = convert(protectedValue)
  return placeholders.reduce((text, [token, original]) => text.replace(token, original), converted)
}

function convertedNamespaces(code) {
  const variant = VARIANTS[code]
  if (!variant) {
    throw new Error(`unknown variant ${code}; known: ${Object.keys(VARIANTS).join(', ')}`)
  }
  const convert = OpenCC.Converter({ from: 'cn', to: variant.to })
  const namespaces = {}
  for (const file of readdirSync(SRC_DIR)) {
    if (!file.endsWith('.json')) continue
    const ns = file.slice(0, -'.json'.length)
    const tree = JSON.parse(readFileSync(join(SRC_DIR, file), 'utf8'))
    namespaces[ns] = convertDeep(tree, convert)
  }
  return namespaces
}

function buildPack(code) {
  const variant = VARIANTS[code]
  if (!variant) {
    throw new Error(`unknown variant ${code}; known: ${Object.keys(VARIANTS).join(', ')}`)
  }
  const namespaces = convertedNamespaces(code)

  return {
    psp_language_pack: PACK_FORMAT,
    code,
    name: variant.name,
    author: `auto-generated (OpenCC cn→${variant.to})`,
    base_language: 'zh-CN',
    base_version: panelVersion(),
    namespaces,
  }
}

function writeBuiltin(code) {
  const namespaces = convertedNamespaces(code)
  const outDir = join(BUILTIN_DIR, code)
  mkdirSync(outDir, { recursive: true })
  for (const [ns, tree] of Object.entries(namespaces)) {
    writeFileSync(join(outDir, `${ns}.json`), JSON.stringify(tree, null, 2) + '\n', 'utf8')
  }
  // eslint-disable-next-line no-console
  console.log(`wrote ${outDir}  (${Object.keys(namespaces).length} namespaces)`)
}

function main() {
  const args = process.argv.slice(2)
  const builtin = args.includes('--builtin')
  const codes = args.filter(arg => arg !== '--builtin')
  if (builtin && codes.length > 1) {
    throw new Error('--builtin accepts at most one language code')
  }
  const targets = codes.length ? codes : ['zh-TW']
  if (builtin) {
    for (const code of targets) writeBuiltin(code)
  } else {
    mkdirSync(OUT_DIR, { recursive: true })
    for (const code of targets) {
      const pack = buildPack(code)
      const outPath = join(OUT_DIR, `${code}.json`)
      writeFileSync(outPath, JSON.stringify(pack, null, 2) + '\n', 'utf8')
      // eslint-disable-next-line no-console
      console.log(`wrote ${outPath}  (${Object.keys(pack.namespaces).length} namespaces)`)
    }
  }
}

main()
