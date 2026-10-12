import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

const source = fileURLToPath(new URL('third-party-network.sh', import.meta.url)).replaceAll('\\', '/')
function mode(extra = {}) {
  return execFileSync('bash', ['-c', 'source "$1" && third_party_network_mode', '_', source], {
    encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, PSP_BACKEND_NETWORK: '', GITHUB_ACTIONS: '', GITHUB_REPOSITORY: '', RUNNER_ENVIRONMENT: '', ...extra },
  }).trim()
}
const owned = { PSP_BACKEND_NETWORK: 'host', PSP_BACKEND_HOST: '10.254.73.91', GITHUB_ACTIONS: 'true', GITHUB_REPOSITORY: 'KazuhaHub/Passwall-Sub-Panel', RUNNER_ENVIRONMENT: 'github-hosted' }
test('third-party network keeps bridge defaults and admits only the dedicated hosted host profile', () => {
  assert.equal(mode(), 'bridge')
  assert.equal(mode({ PSP_BACKEND_NETWORK: 'bridge' }), 'bridge')
  assert.equal(mode(owned), 'host')
  for (const change of [
    { PSP_BACKEND_NETWORK: 'foreign-network' }, { PSP_BACKEND_NETWORK: 'host; touch private' },
    { PSP_BACKEND_HOST: '127.0.0.1' }, { PSP_BACKEND_HOST: '10.254.256.1' }, { PSP_BACKEND_HOST: '10.254.1.0' },
    { PSP_BACKEND_HOST: '10.254.1.2; private' }, { PSP_BACKEND_HOST: 'panel.example' },
    { GITHUB_ACTIONS: 'false' }, { GITHUB_REPOSITORY: 'foreign/repository' }, { RUNNER_ENVIRONMENT: 'self-hosted' },
  ]) assert.throws(() => mode({ ...owned, ...change }))
})

test('host S-UI subscription moves off 3X-UI port while retaining all other database rows', () => {
  const root = mkdtempSync(join(tmpdir(), 'psp-sui-host-network-'))
  const python = process.platform === 'win32' ? 'python' : 'python3'
  const db = join(root, 's-ui.db').replaceAll('\\', '/')
  const run = code => execFileSync(python, ['-c', code, db], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
  const configure = path => execFileSync('bash', ['-c', 'python3() { "${PSP_TEST_PYTHON}" "$@"; }; source "$1" && third_party_configure_sui_host_db "$2"', '_', source, path], {
    encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env, PSP_TEST_PYTHON: python },
  })
  try {
    run("import sqlite3,sys; d=sqlite3.connect(sys.argv[1]); d.executescript(\"CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT); INSERT INTO settings VALUES ('subPort','2096'),('webPort','2095'),('webPath','/app/'),('secret','private-preserved'); CREATE TABLE tokens (token TEXT); INSERT INTO tokens VALUES ('private-preserved');\"); d.commit()")
    configure(db)
    configure(db) // restart preparation is idempotent
    const rows = JSON.parse(run("import sqlite3,sys,json; d=sqlite3.connect(sys.argv[1]); print(json.dumps([d.execute('SELECT * FROM settings ORDER BY key').fetchall(), d.execute('SELECT * FROM tokens').fetchall()]))"))
    assert.deepEqual(rows, [[['secret', 'private-preserved'], ['subPort', '2097'], ['webPath', '/app/'], ['webPort', '2095']], [['private-preserved']]])
    run("import sqlite3,sys; d=sqlite3.connect(sys.argv[1]); d.execute(\"UPDATE settings SET value='foreign' WHERE key='subPort'\"); d.commit()")
    assert.throws(() => configure(db))
    assert.equal(run("import sqlite3,sys; d=sqlite3.connect(sys.argv[1]); print(d.execute(\"SELECT value FROM settings WHERE key='subPort'\").fetchone()[0])").trim(), 'foreign')
    const missing = join(root, 'absent.db').replaceAll('\\', '/')
    assert.throws(() => configure(missing))
    assert(!existsSync(missing))
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
