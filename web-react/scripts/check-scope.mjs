// Proves the generated "new scope" selector (scripts/psp-scope.mjs) in headless
// Chrome, with no dev server, and runs from `npm run smoke:dist`.
//
//   node scripts/check-scope.mjs            (CHROME_PATH selects the browser)
//
// A. EXHAUSTIVE: every marker chain of length 1..8 over {.psp-ui, [data-slot],
//    .psp-legacy, plain element} with the probe as the innermost node (87,380
//    chains) is built, the real expanded selector is applied with
//    Element.matches, and the result is compared with the nearest-marker rule
//    computed in JS. Required: 0 mismatches within the supported depth, and 0
//    legacy elements ever classified as new. The entry-point selector (new element
//    whose parent is not new) is checked on the same chains. Chains NEEDING more alternations
//    than supported are counted separately, so the limit is documented by
//    measurement (they are all classified "not new").
// B. SHIM: the real shim block of ui.css (expanded) plus `.MuiX{box-sizing:
//    content-box; font-weight:300}`, placed before AND after it, over every chain
//    of length 1..6: an MUI-class element must keep content-box in legacy and
//    plain places and must lose it only in new scope.
import { spawn } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { NEW_SCOPE_LEVELS, isNewChain, levelNeeded } from './psp-scope-chain.mjs'
import { expandScope, newRootSelector, newScopeMembers } from './psp-scope.mjs'

const sleep = ms => new Promise(r => setTimeout(r, ms))
const chromeCandidates = [
  process.env.CHROME_PATH,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  'google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser',
].filter(Boolean)

async function withPage(fn) {
  const profile = await mkdtemp(join(tmpdir(), 'psp-scope-'))
  let child, endpoint
  for (const cmd of chromeCandidates) {
    child = spawn(cmd, ['--headless=new', '--no-sandbox', '--disable-gpu', '--no-first-run',
      `--user-data-dir=${profile}`, '--remote-debugging-port=0', 'about:blank'])
    endpoint = await new Promise(resolve => {
      let err = ''
      child.stderr.on('data', c => { err += c; const m = err.match(/DevTools listening on (ws:\/\/\S+)/); if (m) resolve(m[1]) })
      child.on('error', () => resolve(null))
      child.on('close', () => resolve(null))
    })
    if (endpoint) break
  }
  if (!endpoint) throw new Error('no usable Chrome found; set CHROME_PATH')
  const ws = new WebSocket(endpoint)
  await new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', () => rej(new Error('devtools connection failed')), { once: true }) })
  let id = 0
  const pending = new Map()
  ws.addEventListener('message', ev => {
    const m = JSON.parse(String(ev.data))
    if (m.id !== undefined) { const c = pending.get(m.id); pending.delete(m.id); if (c) m.error ? c.reject(new Error(m.error.message)) : c.resolve(m.result) }
  })
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const i = ++id; pending.set(i, { resolve, reject })
    ws.send(JSON.stringify({ id: i, method, params, ...(sessionId ? { sessionId } : {}) }))
  })
  try {
    const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
    const evaluate = async expr => {
      const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true }, sessionId)
      if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text)
      return r.result.value
    }
    return await fn(evaluate)
  } finally {
    ws.close(); child.kill(); await sleep(300)
    await rm(profile, { recursive: true, force: true })
  }
}

const MAX_MATCH_LEN = 8
const MAX_SHIM_LEN = 6

// Runs in the page. `levels` is the supported level count; helpers are injected.
const PAGE_MATCH = `(async ({ selector, rootSelector, levels, maxLen, isNewChainSrc, levelNeededSrc }) => {
  const isNewChain = eval('(' + isNewChainSrc + ')')
  const levelNeeded = eval('(' + levelNeededSrc + ')')
  const stats = { rootMismatch: 0, chains: 0, supported: 0, beyond: 0, mismatchSupported: 0, beyondMatchedNew: 0, legacyAsNew: 0, newMissedBySupported: 0, firstBad: [] }
  const root = document.createElement('div')
  const kinds = ['c', 'd', 'L', 'p']
  function walk(parent, chain, path, depth) {
    for (const k of kinds) {
      const el = document.createElement('div')
      if (k === 'c') el.className = 'psp-ui'
      else if (k === 'd') el.setAttribute('data-slot', 'x')
      else if (k === 'L') el.className = 'psp-legacy'
      parent.appendChild(el)
      const next = k === 'c' || k === 'd' ? chain + 'U' : k === 'L' ? chain + 'L' : chain
      const expectNew = isNewChain(next)
      const got = el.matches(selector)
      // an entry point is a new element whose parent is not new (parent chain = chain)
      if (!(isNewChain(chain) && levelNeeded(chain) >= levels) && !(expectNew && levelNeeded(next) >= levels)) {
        if (el.matches(rootSelector) !== (expectNew && !isNewChain(chain))) { stats.rootMismatch++; if (stats.firstBad.length < 5) stats.firstBad.push('root ' + path + k) }
      }
      stats.chains++
      if (!expectNew && got) stats.legacyAsNew++
      if (expectNew && levelNeeded(next) >= levels) {
        stats.beyond++
        if (got) stats.beyondMatchedNew++
      } else {
        stats.supported++
        if (got !== expectNew) {
          stats.mismatchSupported++
          if (stats.firstBad.length < 5) stats.firstBad.push(path + k)
        }
      }
      if (depth < maxLen) walk(el, next, path + k, depth + 1)
    }
  }
  walk(root, '', '', 1)
  return stats
})`

const PAGE_SHIM = `(async ({ css, maxLen, order, levels, isNewChainSrc, levelNeededSrc }) => {
  const isNewChain = eval('(' + isNewChainSrc + ')')
  const levelNeeded = eval('(' + levelNeededSrc + ')')
  const mui = '.MuiX{box-sizing:content-box;font-weight:300}'
  const base = '@layer base{*{box-sizing:border-box}}' + css
  const style = (t) => { const s = document.createElement('style'); s.textContent = t; document.head.appendChild(s); return s }
  const sheets = order === 'before' ? [style(mui), style(base)] : [style(base), style(mui)]
  const root = document.createElement('div')
  document.body.appendChild(root)
  const stats = { chains: 0, legacyKeptMui: 0, legacyWrong: 0, newReverted: 0, newWrong: 0, beyond: 0, beyondKeptMui: 0, firstBad: [] }
  const kinds = ['c', 'd', 'L', 'p']
  function walk(parent, chain, path, depth) {
    for (const k of kinds) {
      const el = document.createElement('div')
      el.className = 'MuiX' + (k === 'c' ? ' psp-ui' : k === 'L' ? ' psp-legacy' : '')
      if (k === 'd') el.setAttribute('data-slot', 'x')
      parent.appendChild(el)
      const next = k === 'c' || k === 'd' ? chain + 'U' : k === 'L' ? chain + 'L' : chain
      const bs = getComputedStyle(el).boxSizing
      stats.chains++
      if (!isNewChain(next)) { if (bs === 'content-box') stats.legacyKeptMui++; else { stats.legacyWrong++; if (stats.firstBad.length < 5) stats.firstBad.push('legacy ' + path + k + ' ' + bs) } }
      else if (levelNeeded(next) >= levels) { stats.beyond++; if (bs === 'content-box') stats.beyondKeptMui++ }
      else if (bs === 'border-box') stats.newReverted++
      else { stats.newWrong++; if (stats.firstBad.length < 5) stats.firstBad.push('new ' + path + k + ' ' + bs) }
      if (depth < maxLen) walk(el, next, path + k, depth + 1)
    }
  }
  walk(root, '', '', 1)
  root.remove(); sheets.forEach(s => s.remove())
  return stats
})`

function shimCss() {
  const css = readFileSync(fileURLToPath(new URL('../src/styles/ui.css', import.meta.url)), 'utf8')
  const start = css.indexOf('===== MUI coexistence shim')
  const end = css.indexOf('/* ===== end MUI coexistence shim')
  if (start < 0 || end < 0) throw new Error('could not find the shim block in ui.css')
  // from the first rule after the block comment to the end marker
  const body = css.slice(css.indexOf('*/', start) + 2, end)
  return expandScope(body)
}

export async function runScopeCheck() {
  const selector = `:is(${newScopeMembers().join(', ')})`
  const args = { levels: NEW_SCOPE_LEVELS, isNewChainSrc: isNewChain.toString(), levelNeededSrc: levelNeeded.toString() }
  return withPage(async evaluate => {
    const a = await evaluate(`${PAGE_MATCH}(${JSON.stringify({ ...args, selector, rootSelector: newRootSelector(), maxLen: MAX_MATCH_LEN })})`)
    const css = shimCss()
    const shim = {}
    for (const order of ['before', 'after']) {
      shim[order] = await evaluate(`${PAGE_SHIM}(${JSON.stringify({ ...args, css, order, maxLen: MAX_SHIM_LEN })})`)
    }
    return { match: a, shim }
  })
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const { match: a, shim } = await runScopeCheck()
  console.log(`supported depth: ${NEW_SCOPE_LEVELS} levels (chains with up to ${NEW_SCOPE_LEVELS - 1} LU pairs)`)
  console.log(`A. selector vs nearest-marker rule, every chain up to length ${MAX_MATCH_LEN}: ${a.chains} chains`)
  console.log(`   within supported depth: ${a.supported} chains, ${a.mismatchSupported} mismatches${a.firstBad.length ? ' e.g. ' + a.firstBad.join(',') : ''}`)
  console.log(`   entry-point selector (new element whose parent is not new) mismatches: ${a.rootMismatch}`)
  console.log(`   legacy/plain elements classified as new (any depth): ${a.legacyAsNew}`)
  console.log(`   beyond supported depth: ${a.beyond} new chains, ${a.beyondMatchedNew} classified new, ${a.beyond - a.beyondMatchedNew} classified NOT new (the documented limit)`)
  let bad = a.mismatchSupported + a.legacyAsNew + a.beyondMatchedNew + a.rootMismatch
  for (const order of ['before', 'after']) {
    const s = shim[order]
    console.log(`B. shim, .MuiX rule ${order} the stylesheet, every chain up to length ${MAX_SHIM_LEN}: ${s.chains} chains; legacy kept content-box ${s.legacyKeptMui}, wrong ${s.legacyWrong}; new reverted ${s.newReverted}, wrong ${s.newWrong}; beyond-depth new kept MUI ${s.beyondKeptMui}/${s.beyond}`)
    bad += s.legacyWrong + s.newWrong
    if (s.firstBad.length) console.log('   ' + s.firstBad.join('; '))
  }
  if (a.chains !== 87380) { console.log(`expected 87380 chains, got ${a.chains}`); bad++ }
  console.log(bad ? 'FAIL' : 'OK')
  process.exit(bad ? 1 : 0)
}
