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
    const timeout = setTimeout(() => child.kill(), 30_000)
    child.stdout.on('data', chunk => { stdout += chunk })
    child.stderr.on('data', chunk => { stderr += chunk })
    child.once('error', error => {
      clearTimeout(timeout)
      reject(error)
    })
    child.once('close', code => {
      clearTimeout(timeout)
      if (code === 0) resolveRun({ stdout, stderr })
      else reject(new Error(`${command} exited with ${code}: ${stderr}`))
    })
  })
}

try {
  await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen))
  const address = server.address()
  const url = `http://127.0.0.1:${address.port}/`

  let result
  let lastError
  for (const candidate of chromeCandidates) {
    try {
      result = await runChrome(candidate, url)
      break
    } catch (error) {
      lastError = error
    }
  }
  if (!result) throw lastError || new Error('Chrome/Chromium executable not found')

  const root = result.stdout.match(/<div id="root">([\s\S]*?)<\/body>/)?.[1] || ''
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
