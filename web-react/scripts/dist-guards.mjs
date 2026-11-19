// Static checks over the built dist, run by smoke-dist.mjs before the browser
// starts. They protect the coexistence of the MUI pages with the Tailwind/shadcn
// stack (see src/styles/ui.css) and cost nothing at runtime.
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

// Chunks that hold the new stack. The login page (and every un-migrated MUI
// page) must not load them, so index.html must not preload them.
const NEW_STACK_CHUNK = /^vendor-(base-ui|ui)\b/

export function preloadedAssets(indexHtml) {
  const out = []
  const re = /<(?:link[^>]*\brel="modulepreload"[^>]*\bhref|script[^>]*\bsrc)="\.\/(assets\/[^"]+)"/g
  for (let m; (m = re.exec(indexHtml)); ) out.push(m[1])
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
    for (const m of code.matchAll(/from\s*"\.\/([^"]+\.js)"/g)) {
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
export function checkGlobalRules(css) {
  const problems = []
  const isGlobalSelector = sel => {
    const s = sel.trim()
    // Anything rooted in a class, id, attribute, :where/:is or :not is scoped.
    if (/[.#[]|:where|:is|:not|:has/.test(s)) return false
    return /^(\*|html|body|:host|:root|:?:?(before|after|backdrop)|\*:?:?(before|after|backdrop))$/.test(s)
  }
  const skipAt = /^@(font-face|keyframes|property|-webkit-keyframes|counter-style|page)\b/

  function walk(text, ctx) {
    let i = 0
    while (i < text.length) {
      const open = text.indexOf('{', i)
      if (open < 0) break
      const prelude = text.slice(i, open).trim().replace(/^[;}\s]+/, '')
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
      } else if (prelude.split(',').some(isGlobalSelector)) {
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
