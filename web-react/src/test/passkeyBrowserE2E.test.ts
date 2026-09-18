// @vitest-environment node
//
// The other half of the passkey path, and the seam #102 and #113 actually form.
//
// The Go end-to-end test (security-test-suite/psp_e2e_test.go) drives a real
// panel with a software authenticator written in Go. That rules out the SERVER
// being broken. It cannot say anything about @simplewebauthn/browser, because
// the bytes it sends are built by Go.
//
// This test runs the REAL @simplewebauthn/browser -- the same package the panel
// ships to browsers, at the version #113 moved it to -- against the REAL panel.
// Only navigator.credentials.{create,get} is replaced, with a software
// authenticator; that is the hardware boundary, and it is the one piece of the
// path that cannot exist in a test process. Everything either side of it runs
// for real:
//
//   panel's options JSON  ->  startRegistration()  ->  navigator.credentials.create()
//                                                              (software authenticator)
//                          <-  attestation JSON    <-  finish endpoint
//
// What it would catch that the Go test cannot: a change in how
// @simplewebauthn/browser 14 marshals the options it consumes or the response it
// produces. That is exactly the risk a major bump of that package carries, and
// it is invisible to a test whose client is not that package.
//
// Skipped unless PSP_BROWSER_E2E_BASE_URL, _UPN and _PASSWORD are set. It
// MUTATES the target's settings and enrolls a credential on the account, so
// point it only at a throwaway panel with a fresh database.
//
//   PSP_BROWSER_E2E_BASE_URL=http://localhost:18788 \
//   PSP_BROWSER_E2E_UPN=admin PSP_BROWSER_E2E_PASSWORD=... \
//   npx vitest run src/test/passkeyBrowserE2E.test.ts

import { beforeAll, describe, expect, it } from 'vitest'
import { startAuthentication, startRegistration } from '@simplewebauthn/browser'

const BASE = (process.env.PSP_BROWSER_E2E_BASE_URL ?? '').replace(/\/+$/, '')
const UPN = process.env.PSP_BROWSER_E2E_UPN ?? ''
const PASSWORD = process.env.PSP_BROWSER_E2E_PASSWORD ?? ''
const enabled = Boolean(BASE && UPN && PASSWORD)

// ---------------------------------------------------------------------------
// A minimal CBOR encoder. Only what an attestationObject and a COSE key need:
// unsigned and negative integers, byte strings, text strings, arrays and maps.
// ---------------------------------------------------------------------------

function cborEncode(value: unknown): Uint8Array {
  const out: number[] = []

  const head = (major: number, n: number) => {
    if (n < 24) out.push((major << 5) | n)
    else if (n < 0x100) out.push((major << 5) | 24, n)
    else if (n < 0x10000) out.push((major << 5) | 25, n >> 8, n & 0xff)
    else out.push((major << 5) | 26, (n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff)
  }

  const encode = (v: unknown): void => {
    if (typeof v === 'number' && Number.isInteger(v)) {
      if (v >= 0) head(0, v)
      else head(1, -1 - v)
      return
    }
    if (typeof v === 'string') {
      const bytes = new TextEncoder().encode(v)
      head(3, bytes.length)
      out.push(...bytes)
      return
    }
    if (v instanceof Uint8Array) {
      head(2, v.length)
      out.push(...v)
      return
    }
    if (Array.isArray(v)) {
      head(4, v.length)
      v.forEach(encode)
      return
    }
    if (v instanceof Map) {
      head(5, v.size)
      for (const [k, val] of v) {
        encode(k)
        encode(val)
      }
      return
    }
    throw new Error(`cborEncode: unsupported value ${String(v)}`)
  }

  encode(value)
  return new Uint8Array(out)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

function bufToB64url(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf)
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function concat(...parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((n, p) => n + p.length, 0)
  const out = new Uint8Array(total)
  let at = 0
  for (const p of parts) {
    out.set(p, at)
    at += p.length
  }
  return out
}

async function sha256(data: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', data as BufferSource))
}

// ---------------------------------------------------------------------------
// The software authenticator behind navigator.credentials
// ---------------------------------------------------------------------------

interface StoredCredential {
  credentialId: Uint8Array
  privateKey: CryptoKey
  userHandle: Uint8Array
  signCount: number
}

const AAGUID = new Uint8Array(16) // all zeroes, as an un-attested authenticator sends

let stored: StoredCredential | null = null

/** COSE_Key for an ES256 (P-256) public key, as a WebAuthn authenticator reports it. */
function coseEs256(jwk: JsonWebKey): Uint8Array {
  const x = new Uint8Array(
    atob((jwk.x ?? '').replace(/-/g, '+').replace(/_/g, '/')).split('').map((c) => c.charCodeAt(0)),
  )
  const y = new Uint8Array(
    atob((jwk.y ?? '').replace(/-/g, '+').replace(/_/g, '/')).split('').map((c) => c.charCodeAt(0)),
  )
  return cborEncode(
    new Map<number, unknown>([
      [1, 2], // kty: EC2
      [3, -7], // alg: ES256
      [-1, 1], // crv: P-256
      [-2, x],
      [-3, y],
    ]),
  )
}

/**
 * WebCrypto's ECDSA signature is raw r‖s. WebAuthn requires ASN.1 DER for ES256,
 * and go-webauthn enforces it: its verifier runs asn1.Unmarshal over the bytes and
 * rejects anything that does not round-trip through a canonical re-encode. A real
 * authenticator emits DER; this converts the raw output so the shim speaks the
 * same wire format one does.
 */
function rawEcdsaToDer(raw: Uint8Array): Uint8Array {
  const half = raw.length / 2
  const int = (b: Uint8Array): Uint8Array => {
    let i = 0
    while (i < b.length - 1 && b[i] === 0) i++
    const trimmed = Uint8Array.from(b.subarray(i))
    // DER INTEGERs are signed, so a value whose top bit is set needs a leading
    // zero byte to stay positive.
    return trimmed[0] & 0x80 ? concat(new Uint8Array([0]), trimmed) : trimmed
  }
  const r = int(raw.slice(0, half))
  const sPart = int(raw.slice(half))
  const body = concat(new Uint8Array([0x02, r.length]), r, new Uint8Array([0x02, sPart.length]), sPart)
  return concat(new Uint8Array([0x30, body.length]), body)
}

function toArrayBuffer(u: Uint8Array): ArrayBuffer {
  // A fresh, exactly-sized buffer: Uint8Array.buffer may be a pooled allocation
  // larger than the view, and the library reads .byteLength off it.
  return u.slice().buffer as ArrayBuffer
}

function newCredentialId(): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(32))
}

async function buildRegistrationResponse(publicKey: {
  rp: { id: string }
  user: { id: ArrayBuffer }
  challenge: ArrayBuffer
}): Promise<{ id: string; rawId: ArrayBuffer; type: string; response: unknown; getClientExtensionResults: () => object }> {
  const rpIdHash = await sha256(new TextEncoder().encode(publicKey.rp.id))
  const userHandle = new Uint8Array(publicKey.user.id)

  // Extractable, because the public half has to be exported as a COSE key to be
  // attested; a real authenticator generates the pair internally and reports
  // only the public half, which is what this stands in for.
  const pair = (await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, [
    'sign',
    'verify',
  ])) as CryptoKeyPair
  stored = { credentialId: newCredentialId(), privateKey: pair.privateKey, userHandle, signCount: 0 }
  const jwk = await crypto.subtle.exportKey('jwk', pair.publicKey)
  const cose = coseEs256(jwk)

  const flags = 0x01 | 0x04 | 0x40 // UP | UV | AT
  const signCount = new Uint8Array(4)
  const credIdLen = new Uint8Array([(stored.credentialId.length >> 8) & 0xff, stored.credentialId.length & 0xff])
  const attestedCredentialData = concat(AAGUID, credIdLen, stored.credentialId, cose)
  const authenticatorData = concat(rpIdHash, new Uint8Array([flags]), signCount, attestedCredentialData)

  const clientDataJSON = new TextEncoder().encode(
    JSON.stringify({
      type: 'webauthn.create',
      challenge: bufToB64url(publicKey.challenge),
      origin: BASE,
      crossOrigin: false,
    }),
  )

  const attestationObject = cborEncode(
    new Map<string, unknown>([
      ['fmt', 'none'],
      ['attStmt', new Map()],
      ['authData', authenticatorData],
    ]),
  )

  return {
    id: bufToB64url(stored.credentialId),
    rawId: toArrayBuffer(stored.credentialId),
    type: 'public-key',
    response: {
      attestationObject: toArrayBuffer(attestationObject),
      clientDataJSON: toArrayBuffer(clientDataJSON),
      getTransports: () => ['internal'],
      getPublicKeyAlgorithm: () => -7,
      getPublicKey: () => toArrayBuffer(cose),
      getAuthenticatorData: () => toArrayBuffer(authenticatorData),
    },
    getClientExtensionResults: () => ({}),
  }
}

async function buildAssertionResponse(publicKey: { challenge: ArrayBuffer; rpId: string }) {
  if (!stored) throw new Error('no credential registered in this process')
  const rpIdHash = await sha256(new TextEncoder().encode(publicKey.rpId))
  stored.signCount += 1
  const counter = new Uint8Array(4)
  new DataView(counter.buffer).setUint32(0, stored.signCount, false)

  const flags = 0x01 | 0x04 // UP | UV
  const authenticatorData = concat(rpIdHash, new Uint8Array([flags]), counter)

  const clientDataJSON = new TextEncoder().encode(
    JSON.stringify({
      type: 'webauthn.get',
      challenge: bufToB64url(publicKey.challenge),
      origin: BASE,
      crossOrigin: false,
    }),
  )

  const rawSignature = new Uint8Array(
    await crypto.subtle.sign(
      { name: 'ECDSA', hash: 'SHA-256' },
      stored.privateKey,
      concat(authenticatorData, await sha256(clientDataJSON)) as BufferSource,
    ),
  )
  const signature = rawEcdsaToDer(rawSignature)

  return {
    id: bufToB64url(stored.credentialId),
    rawId: toArrayBuffer(stored.credentialId),
    type: 'public-key',
    response: {
      authenticatorData: toArrayBuffer(authenticatorData),
      clientDataJSON: toArrayBuffer(clientDataJSON),
      signature: toArrayBuffer(signature),
      userHandle: toArrayBuffer(stored.userHandle),
    },
    getClientExtensionResults: () => ({}),
  }
}

/** Install the shim in place of the browser's navigator.credentials. */
function installAuthenticatorShim() {
  // browserSupportsWebAuthn() requires this global to exist and be a function.
  ;(globalThis as Record<string, unknown>).PublicKeyCredential = function PublicKeyCredential() {}
  Object.defineProperty(globalThis.navigator, 'credentials', {
    configurable: true,
    value: {
      create: async (options: { publicKey: never }) => buildRegistrationResponse(options.publicKey),
      get: async (options: { publicKey: never }) => buildAssertionResponse(options.publicKey),
    },
  })
}

// ---------------------------------------------------------------------------
// The test
// ---------------------------------------------------------------------------

async function api(path: string, init: RequestInit = {}, token?: string) {
  const headers: Record<string, string> = { ...((init.headers as Record<string, string>) ?? {}) }
  if (token) headers.Authorization = `Bearer ${token}`
  if (init.body) headers['Content-Type'] = 'application/json'
  const res = await fetch(BASE + path, { ...init, headers })
  const text = await res.text()
  let body: unknown
  try {
    body = JSON.parse(text)
  } catch {
    body = text
  }
  return { status: res.status, body }
}

describe.skipIf(!enabled)('passkey ceremony through @simplewebauthn/browser against a real panel', () => {
  let token = ''

  beforeAll(() => {
    installAuthenticatorShim()
  })

  it('registers, then logs in passwordlessly, using the browser library for real', async () => {
    // ---- log in and turn the passkey paths on ------------------------------
    const login = await api('/api/auth/local/login', {
      method: 'POST',
      body: JSON.stringify({ upn: UPN, password: PASSWORD }),
    })
    expect(login.status, JSON.stringify(login.body)).toBe(200)
    token = (login.body as { access_token?: string }).access_token ?? ''
    expect(token).not.toBe('')

    const settings = await api('/api/admin/settings/ui', {}, token)
    expect(settings.status).toBe(200)
    const next = {
      ...(settings.body as Record<string, unknown>),
      sub_base_url: BASE,
      passkey_enabled: true,
      passkey_passwordless: true,
    }
    const saved = await api('/api/admin/settings/ui', { method: 'PUT', body: JSON.stringify(next) }, token)
    expect(saved.status, JSON.stringify(saved.body)).toBe(200)

    // ---- register, driven by the real startRegistration() ------------------
    const begin = await api('/api/user/me/passkeys/begin', { method: 'POST' }, token)
    expect(begin.status, JSON.stringify(begin.body)).toBe(200)
    const beginBody = begin.body as { session_id: string; publicKey: unknown }

    // This is the seam. If @simplewebauthn/browser 14 could not consume the
    // options go-webauthn emits, or emitted an attestation it will not accept,
    // this call is where it shows.
    const attestation = await startRegistration({ optionsJSON: beginBody.publicKey as never })

    const finish = await api(
      `/api/user/me/passkeys/finish?session=${beginBody.session_id}&name=browser-e2e`,
      { method: 'POST', body: JSON.stringify(attestation) },
      token,
    )
    expect(finish.status, `finish rejected the browser library's attestation: ${JSON.stringify(finish.body)}`).toBe(200)

    // ---- passwordless login, driven by the real startAuthentication() ------
    const loginBegin = await api('/api/auth/passkey/begin', { method: 'POST' })
    expect(loginBegin.status, JSON.stringify(loginBegin.body)).toBe(200)
    const loginBeginBody = loginBegin.body as { session_id: string; publicKey: unknown }

    const assertion = await startAuthentication({ optionsJSON: loginBeginBody.publicKey as never })

    const loginFinish = await api(
      `/api/auth/passkey/finish?session=${loginBeginBody.session_id}`,
      { method: 'POST', body: JSON.stringify(assertion) },
    )
    expect(
      loginFinish.status,
      `passwordless login rejected the browser library's assertion: ${JSON.stringify(loginFinish.body)}`,
    ).toBe(200)

    // Prove the minted session works rather than comparing token strings: a JWT
    // signed for the same account in the same second is byte-identical.
    const minted = (loginFinish.body as { access_token?: string }).access_token ?? ''
    expect(minted).not.toBe('')
    const me = await api('/api/user/me/passkeys', {}, minted)
    expect(me.status, JSON.stringify(me.body)).toBe(200)
  })
})
