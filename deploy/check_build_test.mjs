import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

const root = fileURLToPath(new URL('../', import.meta.url))
const gate = join(root, 'deploy/check-build.sh')
const compiler = /^toolchain (go\d+\.\d+\.\d+)$/m.exec(readFileSync(join(root, 'go.mod'), 'utf8'))?.[1]
assert(compiler, 'require an exact preferred compiler')
const commit = '0123456789abcdef0123456789abcdef01234567'
const clean = `binary: ${compiler}\n\tpath\tgithub.com/KazuhaHub/passwall-sub-panel/cmd/panel\n\tbuild\tGOOS=linux\n\tbuild\tGOARCH=arm64\n\tbuild\tCGO_ENABLED=0\n\tbuild\tvcs.revision=${commit}\n\tbuild\tvcs.modified=false\n`

test('build provenance gate has valid POSIX shell syntax', () => {
  execFileSync('sh', ['-n', gate])
})

for (const [name, info, valid] of [
  ['correct', clean, true],
  ['wrong compiler', clean.replace(compiler, 'go1.0.0'), false],
  ['wrong OS', clean.replace('GOOS=linux', 'GOOS=darwin'), false],
  ['wrong architecture', clean.replace('GOARCH=arm64', 'GOARCH=amd64'), false],
  ['wrong commit', clean.replace(commit, 'ffffffffffffffffffffffffffffffffffffffff'), false],
  ['dirty source', clean.replace('modified=false', 'modified=true'), false],
  ['missing clean proof', clean.replace('\tbuild\tvcs.modified=false\n', ''), false],
  ['missing revision', clean.replace(`\tbuild\tvcs.revision=${commit}\n`, ''), false],
  ['CGO', clean.replace('CGO_ENABLED=0', 'CGO_ENABLED=1'), false],
  ['wrong command', clean.replace('/cmd/panel', '/cmd/dump-user'), false],
  ['missing build info', '', false],
]) {
  test(`build provenance ${name}`, () => {
    const dir = mkdtempSync(join(tmpdir(), 'psp-build-provenance-'))
    try {
      // A fake go tool returns static test-only build metadata. No target
      // binary is executed, and this fixture does not claim a release build.
      writeFileSync(join(dir, 'go'), `#!/bin/sh\nprintf '%s' '${info}'\n`, { mode: 0o700 })
      const result = spawnSync('sh', [gate, 'binary', 'linux', 'arm64', commit], {
        cwd: root, encoding: 'utf8', env: { ...process.env, PATH: dir + delimiter + process.env.PATH }, timeout: 10_000,
      })
      assert.equal(result.status === 0, valid, `status=${result.status} stderr=${result.stderr}`)
    } finally {
      rmSync(dir, { recursive: true, force: true }) // only this test's mkdtemp
    }
  })
}
