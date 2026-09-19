import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

const escapeRe = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// ---------------------------------------------------------------------------
// `third-party.sh env` IS EVALUATED, NOT PARSED.
//
// Its whole contract is "prints the environment those tests read", and the
// documented way to consume it is `eval "$(deploy/compat/backends/third-party.sh
// env)"` (see the README). The tests it feeds — `go test` — are CHILD processes,
// so they inherit only the EXPORTED environment.
//
// That distinction is silent and it is not hypothetical: `eval "NAME=value"`
// sets a shell variable and no more, so every line printed without `export`
// passed any check the shell could make about itself while reaching `go test`
// as nothing at all. The suite then skipped every live test, and a skip is
// indistinguishable from a pass to anything reading an exit code — which is
// exactly the failure `check-go-results.mjs` exists to refuse. It reported
// `third-party-3xui-live is not a pass (skip)`.
//
// So the assertion is made from a CHILD PROCESS. Checking `$NAME` in the shell
// that did the evaluating is the check that already passed while the bug was
// live, and repeating it here would have proved nothing.
// ---------------------------------------------------------------------------

const SCRIPT = fileURLToPath(new URL('third-party.sh', import.meta.url))

// The names `env` promises. Listed rather than inferred so that dropping one is
// a deliberate edit here too: the workflow's own emptiness loop can only check
// the names it was told about, and a variable that stops being printed — or is
// printed under a new name — would otherwise be caught by nothing until the
// suite skipped again.
const EXPECTED = [
  'PSP_LIVE_XUI_URL',
  'PSP_LIVE_XUI_TOKEN',
  'PSP_LIVE_XUI_DB',
  'PSP_LIVE_SUI_URL',
  'PSP_LIVE_SUI_TOKEN',
]

// `env` reads what `up` wrote, so the fake workdir stands in for a running
// panel. PSP_CONTAINER_RUNTIME=echo satisfies the runtime probe without a
// runtime: the script only asks whether the command can run `info`.
function printedEnv() {
  const workdir = mkdtempSync(join(tmpdir(), 'psp-backends-env-'))
  try {
    mkdirSync(join(workdir, '3xui'), { recursive: true })
    writeFileSync(join(workdir, '3xui.token'), 'token-three-x-ui\n')
    writeFileSync(join(workdir, 'sui.token'), 'token-s-ui\n')
    writeFileSync(join(workdir, 'sui.base'), 'http://127.0.0.1:2095/app\n')
    return execFileSync('bash', [SCRIPT, 'env'], {
      encoding: 'utf8',
      env: { ...process.env, PSP_CONTAINER_RUNTIME: 'echo', PSP_BACKEND_WORKDIR: workdir },
    })
  } finally {
    rmSync(workdir, { recursive: true, force: true })
  }
}

// Evaluate the printed environment the documented way, then read it back from a
// child process — `env` is a separate binary, so it sees the exported
// environment and nothing else.
function childEnvironment(output, cwd) {
  return execFileSync('bash', ['-c', 'eval "$1" || exit 1; env', '_', output], { encoding: 'utf8', cwd })
}

test('the printed environment survives into a child process', () => {
  const output = printedEnv()
  const seen = childEnvironment(output)
  for (const name of EXPECTED) {
    assert.match(
      seen,
      new RegExp(`^${name}=.+$`, 'm'),
      `${name} did not reach a child process; a bare assignment is a shell variable, and go test inherits only the exported environment`,
    )
  }
})

test('the printed environment declares exactly the names the tests read', () => {
  const names = printedEnv()
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => line.replace(/^export /, '').split('=')[0])
  assert.deepEqual(names.sort(), [...EXPECTED].sort())
})

// The values are DATA: a panel mints the tokens, and the workdir comes from the
// host. Evaluated unquoted, a value carrying a space would be word-split and a
// `$`, a backtick or a `;` would be RUN by the very eval the README tells you to
// use. So the assertion is byte-identity through the child, not a quoting shape:
// any escaping that survives with the value intact is acceptable, and the only
// one that fails is the one that lets the value be interpreted.
test('a value carrying shell metacharacters reaches the child unchanged', () => {
  const nasty = `tok with spaces 'quoted' $(touch pwned) \`backtick\` ;semi ${'$'}HOME`
  const workdir = mkdtempSync(join(tmpdir(), 'psp-backends-nasty '))
  try {
    mkdirSync(join(workdir, '3xui'), { recursive: true })
    writeFileSync(join(workdir, '3xui.token'), `${nasty}\n`)
    writeFileSync(join(workdir, 'sui.token'), 'plain\n')
    writeFileSync(join(workdir, 'sui.base'), `http://127.0.0.1:2095/app?x=${nasty}\n`)
    const output = execFileSync('bash', [SCRIPT, 'env'], {
      encoding: 'utf8',
      env: { ...process.env, PSP_CONTAINER_RUNTIME: 'echo', PSP_BACKEND_WORKDIR: workdir },
      cwd: workdir,
    })
    const seen = childEnvironment(output, workdir)
    assert.match(seen, new RegExp(`^PSP_LIVE_XUI_TOKEN=${escapeRe(nasty)}$`, 'm'))
    assert.match(seen, new RegExp(`^PSP_LIVE_SUI_URL=http://127\\.0\\.0\\.1:2095/app\\?x=${escapeRe(nasty)}$`, 'm'))
    // The workdir has a space in it; the DB path is derived from it.
    assert.match(seen, new RegExp(`^PSP_LIVE_XUI_DB=${escapeRe(join(workdir, '3xui/x-ui.db'))}$`, 'm'))
    assert(!existsSync(join(workdir, 'pwned')), 'a value was executed rather than passed through')
  } finally {
    rmSync(workdir, { recursive: true, force: true })
  }
})
