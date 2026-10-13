import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/test.yml', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
const planner = readFileSync(new URL('../.github/scripts/plan-race-shards.sh', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
const app = 'github.com/KazuhaHub/passwall-sub-panel/internal/app'
const store = 'github.com/KazuhaHub/passwall-sub-panel/internal/adapters/sqlstore'
const others = Array.from({ length: 35 }, (_, i) => `fixture/pkg${i}`)
const runPlanner = args => spawnSync('bash', ['-c', 'go() { printf "%s\\n" "$PSP_RACE_FIXTURE_PACKAGES"; }\n' + planner, 'planner-fixture', ...args], { encoding: 'utf8', env: { ...process.env, PSP_RACE_FIXTURE_PACKAGES: [app, store, ...others].join('\n') } })
const job = name => {
  const start = workflow.indexOf(`  ${name}:\n`)
  assert(start >= 0, `missing ${name}`)
  return workflow.slice(start).split(/\n(?=  [a-z][a-z0-9_-]*:\n)/)[0]
}

test('race planner excludes both sliced packages and covers each remaining package exactly once', () => {
  const assigned = []
  for (let shard = 1; shard <= 3; shard++) {
    const result = runPlanner(['--shards', '3', '--shard', String(shard), '--exclude', store, '--exclude', app])
    assert.equal(result.status, 0, result.stderr)
    const packages = result.stdout.trim().split(/\s+/)
    assert(packages.length > 0 && !packages.includes(store) && !packages.includes(app))
    assigned.push(...packages)
  }
  assert.deepEqual(assigned.sort(), [...others].sort())
  for (const missing of ['missing/first', 'missing/second']) {
    const names = missing.endsWith('first') ? [missing, app] : [store, missing]
    const result = runPlanner(['--shards', '3', '--exclude', names[0], '--exclude', names[1]])
    assert.notEqual(result.status, 0)
    assert.equal(result.stdout, '')
    assert(result.stderr.includes(missing))
  }
})

test('separate App race slices preserve every test and the required full-suite gate', () => {
  const normal = job('race-shard'), sliced = job('app-race-shard'), gate = job('sqlite')
  assert.match(normal, /app=github\.com\/KazuhaHub\/passwall-sub-panel\/internal\/app/)
  assert(normal.includes('--exclude "$heavy" --exclude "$app"'))
  assert.match(sliced, /part: \[1, 2, 3\]/)
  assert.match(sliced, /fail-fast: false/)
  assert.match(sliced, /heavy=github\.com\/KazuhaHub\/passwall-sub-panel\/internal\/app/)
  assert.match(sliced, /go test -race -list '\^\(Test\|Fuzz\|Example\)' "\$heavy"/)
  assert(sliced.includes('test -n "$tests"') && sliced.includes('test -n "$regex"'))
  assert(sliced.includes('go test -race -v -count=1 -timeout=4m -run "^(${regex})$" "$heavy"'))
  assert(sliced.includes('> app-roots.txt') && sliced.includes('> app-selected.txt') && sliced.includes("paste -sd'|' app-selected.txt"))
  assert.match(sliced, /if: always\(\)/)
  assert.match(sliced, /name: app-race-shard-\$\{\{ matrix\.part \}\}-test-output/)
  assert.match(gate, /needs: \[race-shard, app-race-shard(?:, [a-z-]+)*\]/)
  assert(gate.includes('if: always()') && gate.includes('needs.race-shard.result') && gate.includes('needs.app-race-shard.result'))
  const minutes = Number(/timeout-minutes: (\d+)/.exec(sliced)?.[1])
  assert(minutes >= 6)
  const roundRobin = /awk -v s="\$\{\{ matrix\.part \}\}" -v m=(\d+) '([^']+)'/.exec(sliced)
  assert(roundRobin && Number(roundRobin[1]) === 3)
  const roots = Array.from({ length: 375 }, (_, i) => `${i % 3 === 0 ? 'Fuzz' : i % 3 === 1 ? 'Example' : 'Test'}Fixture${i}`)
  const assigned = []
  for (let shard = 1; shard <= 3; shard++) {
    const result = spawnSync('awk', ['-v', `s=${shard}`, '-v', 'm=3', roundRobin[2]], { input: roots.join('\n') + '\n', encoding: 'utf8' })
    assert.equal(result.status, 0)
    const selected = result.stdout.trim().split('\n')
    assert(selected.length > 0)
    assigned.push(...selected)
  }
  assert.deepEqual(assigned.sort(), [...roots].sort())
})
