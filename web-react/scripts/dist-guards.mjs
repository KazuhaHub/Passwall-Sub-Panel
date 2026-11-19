// Static checks over the built dist, run by smoke-dist.mjs before the browser
// starts. They keep the Tailwind/shadcn vendor chunks out of the pages that do
// not use them yet and cost nothing at runtime.
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
export function checkDist(dist) {
  const assets = join(dist, 'assets')
  const names = readdirSync(assets)
  const js = Object.fromEntries(names.filter(n => n.endsWith('.js')).map(n => [n, readFileSync(join(assets, n), 'utf8')]))
  const problems = [
    ...checkPreloads(readFileSync(join(dist, 'index.html'), 'utf8')),
    ...checkVendorImports(js),
  ]
  return problems
}
