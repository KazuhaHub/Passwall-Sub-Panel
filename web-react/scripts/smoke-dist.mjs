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
      '--virtual-time-budget=5000',
      '--dump-dom',
      url,
    ], { windowsHide: true })

    let stdout = ''
    let stderr = ''
    let timedOut = false
    const timeout = setTimeout(() => {
      timedOut = true
      child.kill()
    }, browserTimeoutMs)
    child.stdout.on('data', chunk => { stdout += chunk })
    child.stderr.on('data', chunk => { stderr += chunk })
    child.once('error', error => {
      clearTimeout(timeout)
      reject(error.code === 'ENOENT'
        ? new BrowserError(NOT_FOUND, `${command}: not installed`)
        : error)
    })
    child.once('close', code => {
      clearTimeout(timeout)
      if (timedOut) {
        reject(new BrowserError(TIMEOUT,
          `${command}: still running after ${browserTimeoutMs / 1000}s, killed. It dumped `
          + `${stdout.length} bytes. The page never reached its --virtual-time-budget of 5s, `
          + `so this is a browser or runner problem, NOT a rendering regression.`
          + (stderr ? `\n${stderr}` : '')))
        return
      }
      if (code === 0) resolveRun({ stdout, stderr })
      else reject(new BrowserError(EXITED, `${command}: exited with ${code}: ${stderr}`))
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
    'React error #130 absent': !/Minified React error #130/.test(result.stdout + result.stderr),
  }
  const failed = Object.entries(checks).filter(([, passed]) => !passed).map(([name]) => name)
  if (failed.length) throw new Error(`production browser smoke failed: ${failed.join(', ')}`)

  console.log(`production browser smoke passed (${Object.keys(checks).length} checks)`)
} finally {
  await new Promise(resolveClose => server.close(resolveClose))
  await rm(profile, { recursive: true, force: true })
}
