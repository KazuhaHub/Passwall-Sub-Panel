import { readFileSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawnSync } from 'node:child_process'
import { test } from 'node:test'
import assert from 'node:assert/strict'

const workflow = readFileSync(new URL('../.github/workflows/promote.yml', import.meta.url), 'utf8')
function script(name) {
  const step = workflow.split(`      - name: ${name}\n`)[1]
  assert(step)
  const body = step.split('        run: |\n')[1].split(/\n      - /)[0]
  return body.split('\n').map(line => line.replace(/^          /, '')).join('\n')
}
test('promotion refuses beta and rc identities before reading or changing release state', () => {
  const dir = mkdtempSync(join(tmpdir(), 'release-promotion-'))
  try {
    writeFileSync(join(dir, 'go'), '#!/bin/sh\nprintf "%s\\n" "$STUB_VERSION"\n', { mode: 0o755 })
    // A call reaching GitHub or git would prove the refusal ran too late.
    for (const command of ['gh', 'git']) writeFileSync(join(dir, command), '#!/bin/sh\necho unexpected-network-or-mutation >&2\nexit 97\n', { mode: 0o755 })
    for (const version of ['4.1.0-beta.1', '4.1.0-rc.1']) {
      const result = spawnSync('bash', ['-c', script('Verify release identity and completed publication')], {
        env: { ...process.env, PATH: dir + ':' + process.env.PATH, STUB_VERSION: version, TAG: 'v' + version }, encoding: 'utf8',
      })
      assert.notEqual(result.status, 0)
      assert.match(result.stderr, /beta\/rc identities remain testing forever/)
      assert.doesNotMatch(result.stderr, /unexpected-network-or-mutation/)
    }
  } finally { rmSync(dir, { recursive: true, force: true }) }
})
test('only verified publication can move stable aliases; promotion does not rebuild', () => {
  assert.match(workflow, /if: github.ref == 'refs\/heads\/main'/)
  assert.match(workflow, /environment: release-promotion/)
  assert.match(workflow, /sha256sum -c SHA256SUMS.txt/)
  assert.match(workflow, /test "\$checked" = 6/)
  assert.match(workflow, /--source-digest "\$SHA"/)
  assert.match(workflow, /--signer-workflow/)
  assert.match(workflow, /APPROVED=true bash \.github\/scripts\/update-image-channels\.sh/)
  const withoutPolicyBuild = workflow.replace('go build -o "$RUNNER_TEMP/release-stage" ./cmd/release-stage', '')
  assert.doesNotMatch(withoutPolicyBuild, /go build|build-push-action|type=raw,value=latest/)
})
