// gen-locale-hant.mjs — auto-generate a Traditional-Chinese language pack from
// the shipped zh-CN (Simplified) locale files using OpenCC (opencc-js).
//
// This is a MECHANICAL conversion, not a translation: OpenCC does phrase-level
// Simplified→Traditional conversion (软件→軟體, 视频→影片, 默认→預設, 网络→網路),
// so the output reads idiomatically without any hand-translation. Re-run it
// whenever src/locales/zh-CN/* changes — zero maintenance.
//
// Usage:
//   node scripts/gen-locale-hant.mjs            # default: zh-TW (Taiwan, s2twp)
//   node scripts/gen-locale-hant.mjs zh-TW zh-HK
//
// Output: scripts/generated/<code>.json — upload it on the admin "Language packs"
// page, or drop it into the panel's <ConfigDir>/locales/.
//
// Safety: only string VALUES are converted; object KEYS (i18next dotted
// identifiers) and interpolation placeholders like {{count}} are left untouched
// (OpenCC never rewrites ASCII / braces).

import { readdirSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execSync } from 'node:child_process'
import * as OpenCC from 'opencc-js'

const __dirname = dirname(fileURLToPath(import.meta.url))
const SRC_DIR = join(__dirname, '..', 'src', 'locales', 'zh-CN')
const OUT_DIR = join(__dirname, 'generated')

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
  if (typeof value === 'string') return convert(value)
  if (Array.isArray(value)) return value.map(v => convertDeep(v, convert))
  if (value && typeof value === 'object') {
    const out = {}
    for (const [k, v] of Object.entries(value)) out[k] = convertDeep(v, convert)
    return out
  }
  return value
}

function buildPack(code) {
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

function main() {
  const codes = process.argv.slice(2)
  const targets = codes.length ? codes : ['zh-TW']
  mkdirSync(OUT_DIR, { recursive: true })
  for (const code of targets) {
    const pack = buildPack(code)
    const outPath = join(OUT_DIR, `${code}.json`)
    writeFileSync(outPath, JSON.stringify(pack, null, 2) + '\n', 'utf8')
    // eslint-disable-next-line no-console
    console.log(`wrote ${outPath}  (${Object.keys(pack.namespaces).length} namespaces)`)
  }
}

main()
