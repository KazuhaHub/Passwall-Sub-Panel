import { spawn } from 'node:child_process'
import { createReadStream, existsSync } from 'node:fs'
import { mkdtemp, rm, stat } from 'node:fs/promises'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { extname, join, normalize, resolve, sep } from 'node:path'

const dist = resolve(import.meta.dirname, '../../internal/web/dist')
const profile = await mkdtemp(join(tmpdir(), 'psp-web-smoke-'))

const contentTypes = {
  '.css': 'text/css; charset=utf-8',
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
}

function sendFile(pathname, response) {
  response.statusCode = 200
  response.setHeader('Content-Type', contentTypes[extname(pathname)] || 'application/octet-stream')
  createReadStream(pathname).pipe(response)
}

const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url || '/', 'http://127.0.0.1')
    if (url.pathname.startsWith('/api/')) {
      response.statusCode = 404
      response.setHeader('Content-Type', 'application/json')
      response.end('{"error":"not available during frontend smoke test"}')
      return
    }

    const relative = normalize(decodeURIComponent(url.pathname)).replace(/^([/\\])+/, '')
    const candidate = resolve(dist, relative || 'index.html')
    if (candidate !== dist && !candidate.startsWith(`${dist}${sep}`)) {
      response.statusCode = 400
      response.end('invalid path')
      return
    }

    if (existsSync(candidate) && (await stat(candidate)).isFile()) {
      sendFile(candidate, response)
      return
    }
    sendFile(join(dist, 'index.html'), response)
  } catch (error) {
    response.statusCode = 500
    response.end(String(error))
  }
})

// CI sets CHROME_PATH to the Playwright-pinned chrome-headless-shell (test.yml's
// web job), so the browser there moves with the pin and not with the runner
// image. The rest of the list is for a local run.
const chromeCandidates = [
  process.env.CHROME_PATH,
  process.platform === 'win32' && 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  process.platform === 'win32' && 'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
  'google-chrome',
  'google-chrome-stable',
  'chromium',
  'chromium-browser',
].filter(Boolean)

// Chrome handles SIGTERM gracefully and exits 0, so a browser we had to kill
// is INDISTINGUISHABLE from a clean run by exit code alone. That mattered: a
// hung browser dumped nothing, exited 0, and the content checks below then
// reported three APPLICATION render failures for what was a runner problem.
// These kinds keep the two apart.
const NOT_FOUND = 'NOT_FOUND'   // this binary isn't here — try the next candidate
const TIMEOUT = 'TIMEOUT'      // it ran and never finished
const EXITED = 'EXITED'        // it ran and failed

class BrowserError extends Error {
  constructor(kind, message) {
    super(message)
    this.kind = kind
  }
}

// Overridable so the failure paths can be exercised without waiting 30s.
const browserTimeoutMs = Number(process.env.PSP_SMOKE_TIMEOUT_MS) || 30_000
// How long the page may take to show the login form once it is loaded. Only a
// ceiling for a page that really never gets there: the wait ends the moment the
// form is in the DOM.
const renderWaitMs = Number(process.env.PSP_SMOKE_RENDER_WAIT_MS) || 15_000

// THE PAGE IS READ WHEN IT IS READY, NOT AFTER A FIXED SPAN OF BROWSER TIME.
// The login form appears only after the page's own /api/auth/methods probe has
// answered (the spinner is shown until then), and that is a real round trip.
// `--dump-dom --virtual-time-budget=5000` dumped when the budget ran out in
// virtual time, which does not wait for a probe that is still in flight on a busy
// runner: the dump caught the spinner (a root, an SVG, no form) and the release
// failed with "login form rendered" — 8 of 10 runs reproduce it with the CPU busy.
// So the browser is driven over the DevTools protocol and the DOM is polled for
// the thing being asserted.
function connect(endpoint) {
  const ws = new WebSocket(endpoint)
  let nextId = 0
  const pending = new Map()
  const listeners = new Set()
  ws.addEventListener('message', event => {
    const message = JSON.parse(String(event.data))
    if (message.id !== undefined) {
      const call = pending.get(message.id)
      pending.delete(message.id)
      if (call) message.error ? call.reject(new Error(`${call.method}: ${message.error.message}`)) : call.resolve(message.result)
    } else {
      for (const listener of listeners) listener(message)
    }
  })
  const closed = () => { for (const call of pending.values()) call.reject(new Error(`${call.method}: browser connection closed`)); pending.clear() }
  ws.addEventListener('close', closed)
  ws.addEventListener('error', closed)
  return {
    opened: new Promise((resolveOpen, rejectOpen) => {
      ws.addEventListener('open', resolveOpen, { once: true })
      ws.addEventListener('error', () => rejectOpen(new Error('could not open the DevTools connection')), { once: true })
    }),
    send(method, params = {}, sessionId) {
      const id = ++nextId
      return new Promise((resolve, reject) => {
        pending.set(id, { method, resolve, reject })
        ws.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }))
      })
    },
    on(listener) { listeners.add(listener) },
    close() { ws.close() },
  }
}

const sleep = ms => new Promise(resolveSleep => setTimeout(resolveSleep, ms))

async function render(endpoint, url) {
  const browser = connect(endpoint)
  await browser.opened
  try {
    const { targetId } = await browser.send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await browser.send('Target.attachToTarget', { targetId, flatten: true })
    const page = (method, params) => browser.send(method, params, sessionId)
    const logs = []
    browser.on(message => {
      if (message.sessionId !== sessionId) return
      if (message.method === 'Runtime.consoleAPICalled') {
        logs.push(message.params.args.map(arg => arg.value ?? arg.description ?? '').join(' '))
      } else if (message.method === 'Runtime.exceptionThrown') {
        logs.push(message.params.exceptionDetails.exception?.description ?? message.params.exceptionDetails.text)
      }
    })
    await page('Runtime.enable')
    await page('Page.enable')
    await page('Page.navigate', { url })

    const snapshot = async () => {
      const { result } = await page('Runtime.evaluate', {
        expression: 'document.documentElement.outerHTML', returnByValue: true,
      })
      return String(result.value ?? '')
    }
    const deadline = Date.now() + renderWaitMs
    let dom = ''
    for (;;) {
      dom = await snapshot()
      const root = dom.match(/<div id="root">([\s\S]*?)<\/body>/)
      if (root && /<form\b/.test(root[1])) break
      if (Date.now() >= deadline) break
      await sleep(50)
    }
    return { stdout: dom, logs }
  } finally {
    browser.close()
  }
}

function runChrome(command, url) {
  return new Promise((resolveRun, reject) => {
    const child = spawn(command, [
      '--headless=new',
      '--no-sandbox',
      '--disable-dev-shm-usage',
      '--disable-gpu',
      '--disable-extensions',
      '--no-first-run',
      `--user-data-dir=${profile}`,
      '--remote-debugging-port=0',
      'about:blank',
    ], { windowsHide: true })

    let stderr = ''
    let timedOut = false
    let settled = false
    let started = false
    let exited = false
    const finish = (fn, value) => {
      if (settled) return
      settled = true
      clearTimeout(timeout)
      // Report only once the browser has gone: it is still writing into the
      // profile directory the caller is about to delete.
      if (exited) { fn(value); return }
      child.once('close', () => fn(value))
      child.kill()
    }
    const timeout = setTimeout(() => {
      timedOut = true
      finish(reject, new BrowserError(TIMEOUT,
        `${command}: still working after ${browserTimeoutMs / 1000}s, killed. It had printed `
        + `${stderr.length} bytes of stderr. It never finished loading the page, `
        + `so this is a browser or runner problem, NOT a rendering regression.`
        + (stderr ? `\n${stderr}` : '')))
    }, browserTimeoutMs)
    child.stdout.resume()
    child.stderr.on('data', chunk => {
      stderr += chunk
      const match = stderr.match(/DevTools listening on (ws:\/\/\S+)/)
      if (match && !started) {
        started = true
        render(match[1], url).then(
          result => finish(resolveRun, { ...result, stderr }),
          error => { if (!timedOut) finish(reject, new BrowserError(EXITED, `${command}: ${error.message}\n${stderr}`)) })
      }
    })
    child.once('error', error => {
      finish(reject, error.code === 'ENOENT'
        ? new BrowserError(NOT_FOUND, `${command}: not installed`)
        : error)
    })
    child.once('close', code => {
      exited = true
      finish(reject, new BrowserError(EXITED, `${command}: exited with ${code} before the page was read: ${stderr}`))
    })
  })
}

try {
  await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen))
  const address = server.address()
  const url = `http://127.0.0.1:${address.port}/`

  let result
  const attempts = []
  for (const candidate of chromeCandidates) {
    try {
      result = await runChrome(candidate, url)
      break
    } catch (error) {
      // Only "this binary isn't here" justifies trying another one. A browser
      // that RAN and misbehaved is the answer; swallowing it would let the next
      // candidate's ENOENT overwrite it and report a missing browser instead of
      // the hang we actually hit.
      if (error.kind !== NOT_FOUND) throw error
      attempts.push(error.message)
    }
  }
  if (!result) throw new Error(`no usable Chrome/Chromium found:\n  ${attempts.join('\n  ')}`)

  // Separate "the browser gave us nothing to check" from "the app rendered
  // nothing". Falling back to '' here is what turned an empty dump into three
  // confident, wrong claims about React.
  const rootMatch = result.stdout.match(/<div id="root">([\s\S]*?)<\/body>/)
  if (!rootMatch) {
    throw new Error(result.stdout.length < 200
      ? `the browser produced no DOM dump (${result.stdout.length} bytes of stdout). `
        + 'That is a browser or runner failure — the render checks never ran, so this '
        + 'says nothing about the bundle.'
      : `dumped ${result.stdout.length} bytes, but no <div id="root"> … </body> region `
        + 'matched. The page shell changed shape, so the render checks could not be '
        + 'evaluated against it.')
  }
  const root = rootMatch[1]
  const checks = {
    'React root rendered': /<\w+/.test(root),
    'login form rendered': /<form\b/.test(root),
    'MUI SVG icon rendered': /<svg\b/.test(root),
    'React error #130 absent': !/Minified React error #130/.test(`${result.stdout}\n${result.logs.join('\n')}\n${result.stderr}`),
  }
  const failed = Object.entries(checks).filter(([, passed]) => !passed).map(([name]) => name)
  if (failed.length) {
    throw new Error(`production browser smoke failed: ${failed.join(', ')}`
      + (checks['login form rendered'] ? '' : `\nThe page was read for up to ${renderWaitMs / 1000}s and never showed the form; `
        + `the root held: ${root.replace(/\s+/g, ' ').slice(0, 300)}`))
  }

  console.log(`production browser smoke passed (${Object.keys(checks).length} checks)`)
} finally {
  await new Promise(resolveClose => server.close(resolveClose))
  await rm(profile, { recursive: true, force: true })
}
