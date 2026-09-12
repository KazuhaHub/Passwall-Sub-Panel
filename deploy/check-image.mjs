#!/usr/bin/env node
// PSP's existing package is public. Anonymous access errors are uncertainty,
// never proof that an immutable exact image tag can safely be published.
import { pathToFileURL } from 'node:url'

const manifestTypes = [
  'application/vnd.oci.image.index.v1+json',
  'application/vnd.docker.distribution.manifest.list.v2+json',
  'application/vnd.oci.image.manifest.v1+json',
  'application/vnd.docker.distribution.manifest.v2+json',
].join(', ')

function validateCoordinates(repository, tag) {
  const match = typeof repository === 'string' && /^([a-z0-9]+(?:-[a-z0-9]+)*)\/passwall-sub-panel$/.exec(repository)
  if (!match || match[1].length > 39) throw new Error('require a canonical GitHub owner/passwall-sub-panel repository')
  // URL/tag I/O safety only. The workflow's pinned published Node Go CLI is
  // the single semver authority; this intentionally does not replace it.
  if (typeof tag !== 'string' || !/^v[0-9][0-9A-Za-z_.-]{0,126}$/.test(tag)) {
    throw new Error('require an explicit version tag without aliases or URL syntax')
  }
}

async function request(fetchImpl, url, headers) {
  try {
    const response = await fetchImpl(url, {
      method: 'GET', redirect: 'error', headers, signal: AbortSignal.timeout(15_000),
    })
    if (response.redirected) throw new Error('redirect rejected')
    return response
  } catch {
    throw new Error('anonymous registry request failed; refusing publication')
  }
}

async function readJSON(response) {
  try {
    if (!/^application\/json(?:\s*;|$)/i.test(response.headers.get('content-type') ?? '')) throw new Error('not JSON')
    return await response.json()
  } catch {
    throw new Error('registry returned invalid JSON evidence; refusing publication')
  }
}

export async function verifyImageTagMissing(repository, tag, fetchImpl = globalThis.fetch) {
  validateCoordinates(repository, tag)
  const tokenURL = new URL('https://ghcr.io/token')
  tokenURL.searchParams.set('service', 'ghcr.io')
  tokenURL.searchParams.set('scope', `repository:${repository}:pull`)
  const tokenResponse = await request(fetchImpl, tokenURL, { Accept: 'application/json' })
  if (tokenResponse.status !== 200) throw new Error('anonymous pull access unavailable; refusing publication')
  const tokenBody = await readJSON(tokenResponse)
  const token = tokenBody?.token
  if (typeof token !== 'string' || token.length > 8192 || !/^[A-Za-z0-9._~+/-]+=*$/.test(token)) {
    throw new Error('anonymous pull token unavailable; refusing publication')
  }
  const manifestURL = new URL(`https://ghcr.io/v2/${repository}/manifests/${tag}`)
  const manifestResponse = await request(fetchImpl, manifestURL, {
    Accept: manifestTypes, Authorization: `Bearer ${token}`,
  })
  if (manifestResponse.status === 200) throw new Error('exact image tag already exists; publish a new version')
  if (manifestResponse.status !== 404) throw new Error('registry did not prove the image tag absent; refusing publication')
  const evidence = await readJSON(manifestResponse)
  if (!Array.isArray(evidence?.errors) || evidence.errors.length === 0 ||
    !evidence.errors.every(error => error && error.code === 'MANIFEST_UNKNOWN')) {
    throw new Error('registry did not prove MANIFEST_UNKNOWN; refusing publication')
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.argv.length !== 4) throw new Error('require repository and exact tag')
    await verifyImageTagMissing(process.argv[2], process.argv[3])
    console.log('Anonymous registry verified the exact image tag is absent.')
  } catch {
    // Never forward fetch/JSON diagnostics, token objects or response bodies.
    console.error('GHCR exact-tag preflight failed; refusing publication.')
    process.exitCode = 1
  }
}
