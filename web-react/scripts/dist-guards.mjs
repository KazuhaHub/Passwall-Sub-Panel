// Static checks over the built dist, run by smoke-dist.mjs before the browser
// starts. They protect the coexistence of the MUI pages with the Tailwind/shadcn
// stack (see src/styles/ui.css) and cost nothing at runtime.
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

// Chunks that hold the new stack. The login page (and every un-migrated MUI
// page) must not load them, so index.html must not preload them.
// (?![a-z]) rather than \b: a de-duplicated name such as vendor-ui2-<hash> must match too.
const NEW_STACK_CHUNK = /^vendor-(?:base-ui|ui)(?![a-z])/

// Every asset index.html loads eagerly: <script src> and <link rel=modulepreload>,
// whatever the attribute order.
export function preloadedAssets(indexHtml) {
  const out = []
  for (const tag of indexHtml.match(/<(?:link|script)\b[^>]*>/g) ?? []) {
    const attr = name => new RegExp(`\\b${name}\\s*=\\s*"([^"]*)"`).exec(tag)?.[1]
    const url = tag.startsWith('<script') ? attr('src') : /modulepreload/.test(attr('rel') ?? '') ? attr('href') : undefined
    const m = url && /^\.\/(assets\/.+)$/.exec(url)
    if (m) out.push(m[1])
  }
  return out
}

export function checkPreloads(indexHtml) {
  return preloadedAssets(indexHtml)
    .filter(f => NEW_STACK_CHUNK.test(f.replace(/^assets\//, '')))
    .map(f => `index.html preloads ${f}: the new UI stack must not load on un-migrated pages`)
}

// A vendor chunk may import only other vendor chunks and the runtime chunk. One
// that imports an app chunk (or a stray shared one) is how a "vendor-ui" ends up
// dragging page code, or React core, into the wrong place.
export function checkVendorImports(files) {
  const problems = []
  for (const [name, code] of Object.entries(files)) {
    if (!name.startsWith('vendor-')) continue
    // static `from"./x.js"`, side-effect `import"./x.js"` and dynamic `import("./x.js")`
    for (const m of code.matchAll(/\b(?:from|import)\s*\(?\s*["']\.\/([^"']+\.js)["']/g)) {
      const dep = m[1]
      if (!dep.startsWith('vendor-') && !dep.startsWith('rolldown-runtime')) {
        problems.push(`${name} imports ${dep}: vendor chunks may import only vendor chunks`)
      }
    }
  }
  return problems
}

// Parse minified CSS into style rules, ignoring @font-face / @keyframes /
// @property / `@layer properties` (Tailwind's custom-property fallbacks), and
// report any rule on a global selector (`*`, html, body, :host, :root, bare
// ::before/::after/::backdrop) that sets a real property. Custom properties
// (`--x`) on those selectors are fine: nothing reads them but Tailwind.
// Split a selector list on top-level commas only: not inside (), [] or after a
// backslash (Tailwind escapes commas in arbitrary-value class names).
function splitSelectors(prelude) {
  const parts = []
  let depth = 0, start = 0
  for (let i = 0; i < prelude.length; i++) {
    const c = prelude[i]
    if (c === '\\') i++
    else if (c === '(' || c === '[') depth++
    else if (c === ')' || c === ']') depth--
    else if (c === ',' && depth === 0) { parts.push(prelude.slice(start, i)); start = i + 1 }
  }
  parts.push(prelude.slice(start))
  return parts
}

export function checkGlobalRules(css) {
  const problems = []
  // A selector is global when it can match an element without any class,
  // id or attribute narrowing it: a bare type or universal selector (`a`,
  // `svg`, `h1`, `table td`, `*`), or the document roots (`html`, `body`,
  // `:root`, `:host`) with only class / attribute / pseudo qualifiers, since
  // `:root.dark` is still the one <html> element.
  const isGlobalSelector = sel => {
    const s = sel.trim()
    if (/^(?::root|:host|html|body)(?:[.#[:][^\s>+~]*)*$/.test(s)) return true
    return !/[.#[]/.test(s)
  }
  const skipAt = /^@(font-face|keyframes|property|-webkit-keyframes|counter-style|page)\b/

  function walk(text, ctx) {
    let i = 0
    while (i < text.length) {
      const open = text.indexOf('{', i)
      if (open < 0) break
      // Drop earlier statements (`@layer a, b;`) that share the span before `{`.
      const prelude = text.slice(i, open).replace(/^[\s\S]*;/, '').replace(/^[;}\s]+/, '').trim()
      let depth = 1, j = open + 1
      for (; j < text.length && depth > 0; j++) {
        const c = text[j]
        if (c === '"' || c === "'") { const q = c; for (j++; j < text.length && text[j] !== q; j++) if (text[j] === '\\') j++ }
        else if (c === '{') depth++
        else if (c === '}') depth--
      }
      const body = text.slice(open + 1, j - 1)
      if (prelude.startsWith('@')) {
        if (skipAt.test(prelude) || /^@layer\s+properties$/.test(prelude)) { /* skipped */ }
        else walk(body, `${ctx} ${prelude}`)
      } else if (splitSelectors(prelude).some(isGlobalSelector)) {
        const decls = body.split(';').map(d => d.trim()).filter(Boolean)
        const real = decls.filter(d => !d.startsWith('--') && !d.includes('{'))
        if (real.length) problems.push(`global rule "${prelude}"${ctx ? ` in${ctx}` : ''} sets ${real.map(d => d.split(':')[0]).join(', ')}`)
      } else if (body.includes('{')) {
        walk(body, ctx) // CSS nesting
      }
      i = j
    }
  }
  walk(css, '')
  return problems
}

export function checkDist(dist) {
  const assets = join(dist, 'assets')
  const names = readdirSync(assets)
  const js = Object.fromEntries(names.filter(n => n.endsWith('.js')).map(n => [n, readFileSync(join(assets, n), 'utf8')]))
  const problems = [
    ...checkPreloads(readFileSync(join(dist, 'index.html'), 'utf8')),
    ...checkVendorImports(js),
  ]
  for (const n of names.filter(n => n.endsWith('.css'))) {
    problems.push(...checkGlobalRules(readFileSync(join(assets, n), 'utf8')).map(p => `${n}: ${p}`))
  }
  return problems
}
