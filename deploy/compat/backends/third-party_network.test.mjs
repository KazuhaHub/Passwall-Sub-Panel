import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
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
