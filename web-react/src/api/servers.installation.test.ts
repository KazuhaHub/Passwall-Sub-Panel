import { beforeEach, describe, expect, it, vi } from 'vitest'

const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('./client', () => ({ client: http }))

import { createNativeInstallScript, getNativeAgentStatus, getNativeInstallation, importNativeCredential } from './servers'

beforeEach(() => {
  vi.clearAllMocks()
  http.get.mockResolvedValue({ data: { state: 'waiting', configured_nodes: 0 } })
  http.post.mockResolvedValue({ data: '#!/bin/sh\n# private script\n' })
})

describe('administrator Node installation API', () => {
  it('uses abortable administrator GETs and returns their DTOs without issuing credentials', async () => {
    const signal = new AbortController().signal
    const installation = { server: { id: 7 }, agent_id: 'agt_7', credential: 'existing-secret', endpoint: 'https://panel.test/v1/node/sync' }
    http.get.mockResolvedValueOnce({ data: installation })
    await expect(getNativeInstallation(7, signal)).resolves.toEqual(installation)
    await expect(getNativeAgentStatus(7, signal)).resolves.toEqual({ state: 'waiting', configured_nodes: 0 })
    expect(http.get).toHaveBeenCalledWith('/admin/servers/7/node-installation', { signal, _skipErrorToast: true })
    expect(http.get).toHaveBeenCalledWith('/admin/servers/7/node-agent-status', { signal, _skipErrorToast: true })
    expect(http.post).not.toHaveBeenCalled()
  })

  it('imports an existing value in a POST body and requests private scripts as plain text', async () => {
    const signal = new AbortController().signal
    http.post.mockResolvedValueOnce({ data: { ok: true } })
    await expect(importNativeCredential(7, 'original-secret', signal)).resolves.toEqual({ ok: true })
    await expect(createNativeInstallScript(7, 'v1.2.3-beta.1', signal)).resolves.toBe('#!/bin/sh\n# private script\n')
    expect(http.post).toHaveBeenCalledWith('/admin/servers/7/node-credential', { credential: 'original-secret' }, { signal, _skipErrorToast: true })
    expect(http.post).toHaveBeenCalledWith('/admin/servers/7/node-install-script', { version: 'v1.2.3-beta.1' }, { responseType: 'text', signal, _skipErrorToast: true })
    expect(http.post.mock.calls.every(([url]) => !String(url).includes('secret'))).toBe(true)
  })
})
