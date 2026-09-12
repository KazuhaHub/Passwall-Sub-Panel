import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const workflow = readFileSync(new URL('../.github/workflows/release.yml', import.meta.url), 'utf8')

// This intentionally guards a bounded, canonical workflow layout; the Go
// version package additionally parses the full YAML and validates SDK gates.
function job(name) {
  const marker = `  ${name}:\n`
  const start = workflow.indexOf(marker)
  assert(start >= 0, `missing ${name} job`)
  const tail = workflow.slice(start + marker.length)
  const next = /^  [a-z][a-z-]*:\n/m.exec(tail)
  return tail.slice(0, next?.index ?? tail.length)
}

function literalScripts() {
  const lines = workflow.split('\n')
  const scripts = []
  for (let i = 0; i < lines.length; i++) {
    const match = /^(\s*)run: \|$/.exec(lines[i])
    if (!match) continue
    const indent = match[1].length + 2
    const body = []
    while (++i < lines.length) {
      if (lines[i].trim() === '') { body.push(''); continue }
      if (lines[i].search(/\S/) < indent) { i--; break }
      body.push(lines[i].slice(indent))
    }
    // Matrix extension is trusted workflow metadata; replace it only for
    // syntax checking, never evaluate GitHub expressions as executable code.
    scripts.push(body.join('\n').replace(/\$\{\{[^}]+\}\}/g, 'fixture'))
  }
  return scripts
}

function assertPublisherCachesDisabled(raw) {
  assert(!raw.includes('actions/cache@'), 'publisher must not use a shared cache action')
  assert(!/^\s*cache-(?:from|to):/m.test(raw), 'publisher must not import/export a shared container build cache')
  const setups = [...raw.matchAll(/^        uses: actions\/setup-(go|node)@v6$/gm)]
  assert(setups.length > 0, 'require publisher setup actions')
  for (const setup of setups) {
    // Inspect only this action's input block, never a following setup's flags.
    const block = raw.slice(setup.index + setup[0].length).split(/\n      - |\n  [a-z][a-z-]*:\n/, 1)[0]
    if (setup[1] === 'go') {
      assert(/^          cache: false$/m.test(block), 'every publisher Go setup must explicitly disable its default shared cache')
    } else {
      assert(/^          package-manager-cache: false$/m.test(block), 'every publisher Node setup must disable automatic package-manager caching')
      assert(!/^\s*(?:cache|cache-dependency-path):/m.test(block), 'publisher Node setup must not explicitly restore/save npm caches')
    }
  }
}

test('all literal release shell scripts parse without executing or publishing', () => {
  const scripts = literalScripts()
  assert(scripts.length >= 7)
  for (const script of scripts) execFileSync('bash', ['-n'], { input: script })
})

test('release tag input uses env plus the pinned published canonical validator', () => {
  const setup = job('setup')
  assert(setup.includes('REQUESTED_TAG: ${{ inputs.tag }}'))
  assert(setup.includes('go run github.com/KazuhaHub/passwall-node/deployment/cmd/release-tag "$tag"'))
  assert(setup.includes('test "$GITHUB_REF" = "refs/tags/${tag}"'))
  assert(setup.includes('git rev-parse --verify "refs/tags/${tag}^{commit}"'))
  assert(setup.includes('test "$release_sha" = "$GITHUB_SHA"'))
  assert(setup.includes('gh api --paginate "repos/${GITHUB_REPOSITORY}/releases"'))
  assert(setup.includes('test -z "$existing"'))
  for (const script of literalScripts()) {
    assert(!script.includes('${{ inputs.'), 'untrusted input must never be interpolated into shell source')
  }
})

test('every downstream job checks out immutable setup SHA and binaries share one stamp', () => {
  for (const name of ['web', 'build', 'release', 'docker']) {
    assert(job(name).includes('ref: ${{ needs.setup.outputs.sha }}'), `${name} checkout is not pinned`)
  }
  const build = job('build')
  assert(build.includes('COMMIT: ${{ needs.setup.outputs.sha }}'))
  assert(build.includes('BUILD_DATE: ${{ needs.setup.outputs.build_date }}'))
  assert(!build.includes('date -u'))
  assert(build.includes('sh deploy/check-build.sh'))
  assert(job('release').includes('target_commitish: ${{ needs.setup.outputs.sha }}'))
  assert(job('docker').includes('org.opencontainers.image.revision=${{ needs.setup.outputs.sha }}'))
})

test('published releases and exact images cannot silently overwrite while rolling channels remain intentional', () => {
  const release = job('release')
  const docker = job('docker')
  assert(release.includes('overwrite_files: false'))
  assert(release.includes('fail_on_unmatched_files: true'))
  assert(release.includes('release tag changed during the build'))
  assert(docker.includes('release tag changed during the build'))
  assert(job('setup').includes('node deploy/check-image.mjs'))
  assert(docker.includes('node deploy/check-image.mjs'))
  assert(docker.includes("value=latest,enable=${{ startsWith(needs.setup.outputs.tag, 'v') && !contains(needs.setup.outputs.tag, '-') }}"))
  assert(docker.includes("value=beta,enable=${{ startsWith(needs.setup.outputs.tag, 'v') }}"))
  assert(workflow.includes('group: release-passwall-sub-panel\n  cancel-in-progress: false'))
})

test('archives and OCI metadata carry PSP AGPL license and reviewed version notes', () => {
  assert(job('release').includes('cp LICENSE README.md "${pkgdir}/"'))
  assert(job('release').includes('note="docs/releases/${VERSION}.md"'))
  assert(job('release').includes('cat "$note" >> release_notes.md'))
  assert(job('docker').includes('org.opencontainers.image.licenses=AGPL-3.0'))
  assert(!job('docker').includes('Apache-2.0'))
  assert(readFileSync(new URL('../LICENSE', import.meta.url), 'utf8').includes('GNU AFFERO GENERAL PUBLIC LICENSE'))
})

test('write tokens are scoped to publisher jobs only', () => {
  const global = workflow.slice(0, workflow.indexOf('\njobs:'))
  assert(global.includes('permissions:\n  contents: read'))
  assert(!global.includes('packages: write'))
  for (const name of ['setup', 'web', 'build']) assert(!job(name).includes('contents: write'))
  assert(job('release').includes('permissions:\n      contents: write'))
  assert(job('docker').includes('permissions:\n      contents: read\n      packages: write'))
})

test('publisher explicitly disables every Go and Node shared dependency cache', () => {
  assertPublisherCachesDisabled(workflow)
})

test('publisher cache guard rejects implicit defaults and explicit cache restoration', () => {
  for (const [label, mutated] of [
    ['implicit Go cache', workflow.replace('          cache: false\n', '')],
    ['enabled Go cache', workflow.replace('          cache: false', '          cache: true')],
    ['implicit Node cache', workflow.replace('          package-manager-cache: false\n', '')],
    ['enabled Node cache', workflow.replace('          package-manager-cache: false', '          package-manager-cache: true')],
    ['explicit npm cache', workflow.replace('          package-manager-cache: false', '          package-manager-cache: false\n          cache: npm')],
    ['npm cache path', workflow.replace('          package-manager-cache: false', '          package-manager-cache: false\n          cache-dependency-path: web-react/package-lock.json')],
    ['shared cache action', workflow + '\n      - uses: actions/cache@v4\n'],
    ['shared image cache', workflow + '\n          cache-from: type=gha\n'],
  ]) {
    assert.throws(() => assertPublisherCachesDisabled(mutated), { name: 'AssertionError' }, label)
  }
})
