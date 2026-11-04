import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')

test('V3 publisher cannot share npm or Go caches', () => {
  assert(!/cache:\s*npm/.test(workflow))
  assert(!/cache-dependency-path:/.test(workflow))
  assert.equal((workflow.match(/package-manager-cache: false/g) ?? []).length, 2)
  assert.equal((workflow.match(/cache: false/g) ?? []).length, 3)
})

test('V3 publisher binds each downstream checkout to the exact setup commit', () => {
  assert.equal((workflow.match(/ref: \$\{\{ github.sha \}\}/g) ?? []).length, 4)
  assert(workflow.includes('test "$GITHUB_REF" = "refs/tags/$tag"'))
  assert(workflow.includes('test "$release_sha" = "$GITHUB_SHA"'))
  assert(workflow.includes('COMMIT: ${{ needs.setup.outputs.sha }}'))
  assert(workflow.includes('BUILD_DATE: ${{ needs.setup.outputs.build_date }}'))
  assert(workflow.includes('overwrite_files: false'))
  assert(workflow.includes('generate_release_notes: false'))
  assert(workflow.includes('cp LICENSE "${pkgdir}/"'))
})

test('V3 literal shell blocks parse and do not interpolate dispatch input', () => {
  const lines = workflow.split('\n')
  let count = 0
  for (let i = 0; i < lines.length; i++) {
    const match = /^(\s*)run: \|$/.exec(lines[i])
    if (!match) continue
    const indent = match[1].length + 2
    const body = []
    while (++i < lines.length) {
      if (!lines[i].trim()) { body.push(''); continue }
      if (lines[i].search(/\S/) < indent) { i--; break }
      body.push(lines[i].slice(indent))
    }
    const script = body.join('\n')
    assert(!script.includes('${{ inputs.'), 'dispatch input must be environment data')
    execFileSync('bash', ['-n'], { input: script.replace(/\$\{\{[^}]+\}\}/g, 'fixture') })
    count++
  }
  assert(count >= 6)
})

test('V3 publisher yields :latest and the GitHub Latest marker to V4', () => {
  assert(!/value=latest/.test(workflow), 'V3 must not push the :latest image tag')
  assert(!/passwall-sub-panel:latest/.test(workflow), 'V3 release notes must not offer :latest')
  assert(/type=raw,value=v3,enable=/.test(workflow), 'V3 keeps its rolling :v3 tag')
  const start = workflow.indexOf('- name: Publish GitHub Release')
  const publish = workflow.slice(start, workflow.indexOf('\n  docker:', start))
  assert.match(publish, /^ {10}make_latest: false$/m)
})
