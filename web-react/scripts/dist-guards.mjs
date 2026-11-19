// Static checks over the built dist, run by smoke-dist.mjs before the browser
// starts. They keep the Tailwind/shadcn vendor chunks out of the pages that do
// not use them yet and cost nothing at runtime.
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

// Chunks that hold the new stack (shadcn / base-ui / lucide ...). index.html may
// modulepreload one only when the entry itself statically imports it; otherwise it
// is a stray preload that makes every page download code the entry never runs.
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

// Static imports of the entry script (the <script src> in index.html), from its
// source when `files` (name -> code) is given, followed transitively: Vite preloads
// the whole static closure of the entry, so a new-stack chunk reached through a shared
// chunk (or through another vendor chunk) is a legitimate preload, not a stray one.
// Only static edges (`from"./x.js"` and the side-effect `import"./x.js"`) count; a
// dynamic `import("./x.js")` loads on demand and puts nothing in the closure.
const STATIC_IMPORT = /\b(?:from|import)\s*["']\.\/([^"']+\.js)["']/g

function entryImports(indexHtml, files) {
  const names = new Set()
  const entry = (indexHtml.match(/<script\b[^>]*>/g) ?? []).map(t => /\bsrc\s*=\s*"\.\/assets\/([^"]+)"/.exec(t)?.[1]).find(Boolean)
  const queue = entry && files?.[entry] !== undefined ? [entry] : []
  const seen = new Set(queue)
  while (queue.length) {
    const code = files[queue.pop()] ?? ''
    for (const m of code.matchAll(STATIC_IMPORT)) {
      names.add(m[1])
      if (!seen.has(m[1])) { seen.add(m[1]); queue.push(m[1]) }
    }
  }
  return names
}

export function checkPreloads(indexHtml, files) {
  const needed = entryImports(indexHtml, files)
  return preloadedAssets(indexHtml)
    .map(f => f.replace(/^assets\//, ''))
    .filter(name => NEW_STACK_CHUNK.test(name) && !needed.has(name))
    .map(name => `index.html preloads ${name}, which the entry does not import`)
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

// The coarse-pointer minimum size of icon buttons (components/common/IconButton.tsx):
// Tailwind finds the utility by scanning for its literal text, so a refactor of that
// string can silently drop the rule from the build. The built CSS must carry it, inside
// a (pointer: coarse) media query.
export function checkCoarseTarget(css) {
  // Both axes, inside one (pointer: coarse) block: a rule for the height alone would leave
  // the button narrower than a finger.
  const media = /@media\s*\(pointer:\s*coarse\)\s*\{([^@]*)\}\s*(?=[.@]|$)/g
  for (const m of css.matchAll(media)) {
    const body = m[1]
    if (/\.pointer-coarse\\:min-h-\\\[2rem\\\]\s*\{[^}]*min-height:\s*2rem/.test(body)
      && /\.pointer-coarse\\:min-w-\\\[2rem\\\]\s*\{[^}]*min-width:\s*2rem/.test(body)) return []
  }
  return ['built CSS has no (pointer: coarse) rule giving icon buttons a 2rem minimum height and width']
}

export function checkDist(dist) {
  const assets = join(dist, 'assets')
  const names = readdirSync(assets)
  const js = Object.fromEntries(names.filter(n => n.endsWith('.js')).map(n => [n, readFileSync(join(assets, n), 'utf8')]))
  const problems = [
    ...checkPreloads(readFileSync(join(dist, 'index.html'), 'utf8'), js),
    ...checkVendorImports(js),
    ...checkCoarseTarget(names.filter(n => n.endsWith('.css')).map(n => readFileSync(join(assets, n), 'utf8')).join('\n')),
  ]
  return problems
}
