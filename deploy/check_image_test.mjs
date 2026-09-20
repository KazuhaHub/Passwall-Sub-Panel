import assert from 'node:assert/strict'
import { test } from 'node:test'
import { verifyImageTagMissing } from './check-image.mjs'

const repository = 'kazuhahub/passwall-sub-panel'
const tag = 'v4.0.0-beta.1'
const token = 'anonymous-test-token-do-not-log'
const privateBody = 'private-response-body-do-not-log'
const json = (body, status = 200) => new Response(JSON.stringify(body), {
  status, headers: { 'Content-Type': 'application/json; charset=utf-8' },
})

function fixture(manifestResponse, tokenResponse = json({ token })) {
  const calls = []
  const fetchImpl = async (url, options) => {
    calls.push({ url: String(url), options })
    return calls.length === 1 ? tokenResponse : manifestResponse
  }
  return { calls, fetchImpl }
}

async function rejectsRedacted(fetchImpl) {
  await assert.rejects(verifyImageTagMissing(repository, tag, fetchImpl), error => {
    assert(!error.message.includes(token))
    assert(!error.message.includes(privateBody))
    return true
  })
}

test('only JSON MANIFEST_UNKNOWN proves absence, with anonymous repository pull scope', async () => {
  const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN', message: privateBody }] }, 404))
  await verifyImageTagMissing(repository, tag, fetchImpl)
  assert.equal(calls.length, 2)
  const tokenURL = new URL(calls[0].url)
  assert.equal(tokenURL.origin, 'https://ghcr.io')
  assert.equal(tokenURL.pathname, '/token')
  assert.equal(tokenURL.searchParams.get('service'), 'ghcr.io')
  assert.equal(tokenURL.searchParams.get('scope'), `repository:${repository}:pull`)
  assert.equal(calls[0].options.headers.Authorization, undefined)
  assert.equal(calls[1].url, `https://ghcr.io/v2/${repository}/manifests/${tag}`)
  assert.equal(calls[1].options.headers.Authorization, `Bearer ${token}`)
  for (const call of calls) {
    assert.equal(call.options.method, 'GET')
    assert.equal(call.options.redirect, 'error')
    assert(call.options.signal instanceof AbortSignal)
    assert(!call.url.includes(token))
  }
})

test('an existing exact image refuses publication without reading its body', async () => {
  const response = json({ private: privateBody })
  await rejectsRedacted(fixture(response).fetchImpl)
  assert.equal(response.bodyUsed, false)
})

for (const status of [401, 403, 429, 500, 503]) {
  test(`manifest ${status} fails closed`, async () => {
    await rejectsRedacted(fixture(json({ errors: [{ code: 'DENIED', message: privateBody }] }, status)).fetchImpl)
  })
}

for (const status of [401, 403, 404, 429, 500]) {
  test(`anonymous token ${status} fails before manifest access`, async () => {
    const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404), json({ error: privateBody }, status))
    await rejectsRedacted(fetchImpl)
    assert.equal(calls.length, 1)
  })
}

for (const [index, body] of [null, {}, { token: '' }, { token: 12 }, { token: `${token}\r\n${privateBody}` }, { token: '=' }, { token: 'x'.repeat(8193) }].entries()) {
  test(`invalid token evidence ${index + 1} fails closed`, async () => {
    const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404), json(body))
    await rejectsRedacted(fetchImpl)
    assert.equal(calls.length, 1)
  })
}

for (const [index, evidence] of [
  null, {}, { errors: [] }, { code: 'MANIFEST_UNKNOWN' },
  { errors: [{ code: 'NAME_UNKNOWN' }] }, { errors: [{ code: 'DENIED', message: privateBody }] },
  { errors: [{ code: 'MANIFEST_UNKNOWN' }, { code: 'DENIED' }] }, { errors: [null] },
].entries()) {
  test(`ambiguous/non-manifest 404 ${index + 1} fails closed`, async () => {
    await rejectsRedacted(fixture(json(evidence, 404)).fetchImpl)
  })
}

test('HTML/malformed/non-JSON 404 responses cannot prove absence', async () => {
  for (const response of [
    new Response(privateBody, { status: 404, headers: { 'Content-Type': 'application/json' } }),
    new Response('<html>not found</html>', { status: 404, headers: { 'Content-Type': 'text/html' } }),
    new Response('{"errors":[{"code":"MANIFEST_UNKNOWN"}]}', { status: 404, headers: { 'Content-Type': 'text/plain' } }),
  ]) await rejectsRedacted(fixture(response).fetchImpl)
})

test('malformed token JSON is redacted and prevents manifest access', async () => {
  const { calls, fetchImpl } = fixture(json({}, 404), new Response(privateBody, {
    headers: { 'Content-Type': 'application/json' },
  }))
  await rejectsRedacted(fetchImpl)
  assert.equal(calls.length, 1)
})

test('token and manifest redirects are never followed or interpreted as absence', async () => {
  for (const redirectStage of [1, 2]) {
    let calls = 0
    const fetchImpl = async (_url, options) => {
      calls++
      assert.equal(options.redirect, 'error')
      if (calls === redirectStage) throw new Error(`${token}: ${privateBody}`)
      return json({ token })
    }
    await rejectsRedacted(fetchImpl)
    assert.equal(calls, redirectStage)
  }
  await rejectsRedacted(fixture(new Response(null, { status: 302 })).fetchImpl)
  const redirected = json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404)
  Object.defineProperty(redirected, 'redirected', { value: true })
  await rejectsRedacted(fixture(redirected).fetchImpl)
})

// A PRODUCT VERSION IS NOT `v`-PREFIXED, AND THIS FILE REQUIRED THAT IT WAS. The
// shape it enforced was the LEGACY one, so the first product release was refused
// before the registry was consulted at all: `GHCR exact-tag preflight failed`,
// which reads as a registry problem rather than as a shape this file got wrong.
//
// The shape authority is the pinned published Node CLI the workflow has already
// run by this point; re-deciding what a version is here would be a second opinion,
// and this file cannot see `releaseid` to have a good one. What remains is I/O
// safety, and both shapes have to reach the registry.
test('both version shapes reach the registry', async () => {
  for (const version of ['4.0.0', 'v4.0.0-beta.1', '102.1.0', 'v0.0.1-beta12']) {
    const { calls, fetchImpl } = fixture(json({ errors: [{ code: 'MANIFEST_UNKNOWN' }] }, 404))
    await verifyImageTagMissing(repository, version, fetchImpl)
    assert.equal(calls.length, 2, `${version} did not reach the registry`)
    assert.equal(calls[1].url, `https://ghcr.io/v2/${repository}/manifests/${version}`)
  }
})

test('invalid coordinates are rejected before any network request', async () => {
  for (const [candidateRepository, candidateTag] of [
    ['KazuhaHub/passwall-sub-panel', tag], ['kazuhahub/passwall-node', tag],
    ['kazuhahub/passwall-sub-panel/extra', tag], ['kazuhahub@evil.test/passwall-sub-panel', tag],
    ['../passwall-sub-panel', tag], ['owner_name/passwall-sub-panel', tag], ['a'.repeat(40) + '/passwall-sub-panel', tag],
    [repository, 'latest'], [repository, 'beta'], [repository, ''], [repository, 'v1?token=secret'],
    [repository, 'v1/another'], [repository, 'v1%2Fanother'], [repository, 'v1\nsecret'], [repository, 'v' + '1'.repeat(128)],
    // THE PRODUCT SHAPE IS NOT A LICENCE TO PASS ANYTHING. These are the same
    // refusals in the shape that used to be turned away by the missing `v`, and
    // they are the reason the leading character and the reserved names are
    // checked rather than left to fall out of a prefix.
    [repository, '4.0.0/../../evil'], [repository, '4.0.0?x=1'], [repository, '4.0.0%2Fevil'],
    [repository, '-4.0.0'], [repository, '.4.0.0'], [repository, '4.0.0 '], [repository, '4.0.0\nsecret'],
    [repository, '4' + '0'.repeat(128)],
  ]) {
    let calls = 0
    await assert.rejects(verifyImageTagMissing(candidateRepository, candidateTag, async () => { calls++; return json({}) }))
    assert.equal(calls, 0)
  }
})
