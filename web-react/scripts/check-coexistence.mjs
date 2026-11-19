// Coexistence check for the MUI / Tailwind-shadcn boundary (see the contract at
// the top of src/styles/ui.css). NEEDS THE DEV SERVER; not wired into CI.
//
//   cd web-react
//   PSP_DEV_NO_PROXY=1 npm run dev          # terminal 1 (no /api forwarding)
//   node scripts/check-coexistence.mjs      # terminal 2
//
// Env: PSP_DEV_URL (default http://localhost:5174), CHROME_PATH (a Chrome /
// Chromium binary; common macOS and Linux names are tried otherwise).
//
// It loads /__ui-kit/mixed, which renders (src/views/admin/UiMixedTree.tsx):
//   #mix-outside    one MUI sample outside any new wrapper
//   #mix-inside     the same sample inside SidebarProvider > SidebarInset >
//                   LegacyBoundary
//   #mix-new-shell  new Button/Input/Badge directly in the shell
//   #mix-new-reenter the same three inside the legacy content (re-entry)
// and asserts, for light and dark and both densities, that every element (and
// its ::before / ::after) of each pair has identical computed style for every
// property except the geometry ones listed below. Exit code 1 on any mismatch.
import { spawn } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const base = process.env.PSP_DEV_URL || 'http://localhost:5174'

// Used-value properties that legitimately differ because the two copies sit at
// different places and widths. Everything else must match.
const GEOMETRY = new Set([
  'width', 'height', 'inline-size', 'block-size', 'perspective-origin', 'transform-origin',
  'min-width', 'min-height', 'min-inline-size', 'min-block-size',
])

const chromeCandidates = [
  process.env.CHROME_PATH,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  'google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser',
].filter(Boolean)

const sleep = ms => new Promise(r => setTimeout(r, ms))

function connect(endpoint) {
  const ws = new WebSocket(endpoint)
  let id = 0
  const pending = new Map()
  ws.addEventListener('message', ev => {
    const m = JSON.parse(String(ev.data))
    if (m.id !== undefined) {
      const c = pending.get(m.id); pending.delete(m.id)
      if (c) m.error ? c.reject(new Error(m.error.message)) : c.resolve(m.result)
    }
  })
  return {
    opened: new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', () => rej(new Error('devtools connection failed')), { once: true }) }),
    send(method, params = {}, sessionId) {
      const i = ++id
      return new Promise((resolve, reject) => { pending.set(i, { resolve, reject }); ws.send(JSON.stringify({ id: i, method, params, ...(sessionId ? { sessionId } : {}) })) })
    },
    close() { ws.close() },
  }
}

async function launch() {
  const profile = await mkdtemp(join(tmpdir(), 'psp-coexist-'))
  for (const cmd of chromeCandidates) {
    const child = spawn(cmd, ['--headless=new', '--no-sandbox', '--disable-gpu', '--no-first-run',
      `--user-data-dir=${profile}`, '--remote-debugging-port=0', 'about:blank'])
    const endpoint = await new Promise(resolve => {
      let err = ''
      child.stderr.on('data', c => { err += c; const m = err.match(/DevTools listening on (ws:\/\/\S+)/); if (m) resolve(m[1]) })
      child.on('error', () => resolve(null))
      child.on('close', () => resolve(null))
    })
    if (endpoint) return { child, endpoint, profile }
  }
  throw new Error('no usable Chrome found; set CHROME_PATH')
}

// Runs in the page: returns { pair: [{path, props}...] } for the four roots.
const COLLECT = `(() => {
  const roots = ['mix-outside', 'mix-inside', 'mix-new-shell', 'mix-new-reenter']
  const out = {}
  for (const id of roots) {
    const root = document.getElementById(id)
    let els = [root, ...root.querySelectorAll('*')]
    // The new-component roots hold plain wrapper divs; by the contract an
    // unmarked wrapper inside legacy content IS legacy, so only the components
    // (data-slot elements) and their descendants are compared.
    if (id.startsWith('mix-new')) els = els.filter(el => { const c = el.closest('[data-slot]'); return c && root.contains(c) })
    out[id] = els.flatMap(el => {
      const rows = []
      for (const pseudo of [null, '::before', '::after']) {
        const cs = getComputedStyle(el, pseudo)
        if (pseudo && cs.content === 'none') continue
        const props = {}
        for (const p of cs) props[p] = cs.getPropertyValue(p)
        rows.push({ tag: el.tagName.toLowerCase() + (pseudo || ''), props })
      }
      return rows
    })
  }
  return out
})()`

function comparePair(name, a, b) {
  const mismatches = []
  let props = 0
  if (a.length !== b.length) mismatches.push(`${name}: ${a.length} vs ${b.length} nodes`)
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    for (const p of Object.keys(a[i].props)) {
      if (GEOMETRY.has(p)) continue
      props++
      if (a[i].props[p] !== b[i].props[p]) {
        mismatches.push(`${name} #${i} <${a[i].tag}> ${p}: ${a[i].props[p]} | ${b[i].props[p]}`)
      }
    }
  }
  return { elements: Math.min(a.length, b.length), props, mismatches }
}

const { child, endpoint, profile } = await launch()
const browser = connect(endpoint)
await browser.opened
let failed = false
try {
  const { targetId } = await browser.send('Target.createTarget', { url: 'about:blank' })
  const { sessionId } = await browser.send('Target.attachToTarget', { targetId, flatten: true })
  const page = (m, p) => browser.send(m, p, sessionId)
  const evaluate = async expr => {
    const r = await page('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text)
    return r.result.value
  }
  await page('Page.enable'); await page('Runtime.enable')
  await page('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false })

  for (const mode of ['light', 'dark']) {
    for (const density of ['comfortable', 'compact']) {
      await page('Page.navigate', { url: `${base}/__ui-kit/mixed` })
      await sleep(500)
      await evaluate(`localStorage.setItem('psp-user-theme-mode','${mode}');localStorage.setItem('psp-user-density','${density}')`)
      await page('Page.navigate', { url: `${base}/__ui-kit/mixed` })
      const deadline = Date.now() + 60_000
      while (!(await evaluate(`!!document.getElementById('mix-ready') && document.fonts.status === 'loaded'`).catch(() => false))) {
        if (Date.now() > deadline) throw new Error('mixed-tree page never became ready (is the dev server up?)')
        await sleep(200)
      }
      await sleep(500)
      const dump = await evaluate(COLLECT)
      const legacy = comparePair('old content', dump['mix-outside'], dump['mix-inside'])
      const reenter = comparePair('new components', dump['mix-new-shell'], dump['mix-new-reenter'])
      for (const [label, r] of [['old content outside vs inside shell', legacy], ['new components in shell vs re-entered', reenter]]) {
        console.log(`${mode}/${density}  ${label}: ${r.elements} elements, ${r.props} properties compared, ${r.mismatches.length} mismatches`)
        for (const m of r.mismatches.slice(0, 15)) console.log(`    ${m}`)
        if (r.mismatches.length > 15) console.log(`    ... and ${r.mismatches.length - 15} more`)
        if (r.mismatches.length) failed = true
      }
    }
  }
} finally {
  browser.close()
  child.kill()
  await sleep(300)
  await rm(profile, { recursive: true, force: true })
}
console.log(failed ? 'FAIL' : 'OK')
process.exit(failed ? 1 : 0)
