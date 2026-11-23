import { describe, expect, it } from 'vitest'
import type { Server } from '@/api/servers'
import { EMPTY_FORM, createRequest, formOf, updateRequest, validateServerForm, withPanelType } from './serverForm'

const base: Server = {
  id: 3, name: 'tokyo', url: 'https://x.example', panel_type: '3xui', auth_method: 'token', has_api_token: true, has_password: false,
  insecure_https: false, capabilities: [], region: 'JP', tags: ['a'],
}

describe('the server form rules', () => {
  it('leaves a stored secret out of an edit unless a new one was typed (empty keeps it)', () => {
    const untouched = formOf(base)
    expect(untouched.api_token).toBe('')
    expect(untouched.password).toBe('')
    const kept = updateRequest(base, untouched)
    expect('api_token' in kept).toBe(false)
    expect('password' in kept).toBe(false)
    expect(updateRequest(base, { ...untouched, api_token: 'new-token' }).api_token).toBe('new-token')
    expect(updateRequest(base, { ...untouched, auth_method: 'password', password: 'pw' }).password).toBe('pw')
  })
  it('requires the credential the auth method reads, unless one is already stored', () => {
    const f = { ...EMPTY_FORM, panel_type: '3xui' as const, name: 'n', url: 'https://u.example', auth_method: 'token' as const }
    expect(validateServerForm(f, null).api_token).toBeTruthy()
    expect(validateServerForm(f, base).api_token).toBeUndefined()
    // Switching to a password with none stored is refused, and a stale password never makes S-UI look valid.
    expect(validateServerForm({ ...f, auth_method: 'password' }, base).password).toBeTruthy()
    const sui = withPanelType({ ...f, password: 'stale' }, 'sui')
    expect(sui.password).toBe('')
    expect(validateServerForm(sui, null).api_token).toBeTruthy()
  })
  it('treats choosing the type already chosen as no change', () => {
    expect(withPanelType(EMPTY_FORM, 'psp')).toBe(EMPTY_FORM)
  })
  it('asks a Passwall Node only for a name', () => {
    expect(validateServerForm({ ...EMPTY_FORM, name: 'n' }, null)).toEqual({})
    expect(validateServerForm({ ...EMPTY_FORM, name: '' }, null).name).toBeTruthy()
  })
  it('sends region and tags only when they changed', () => {
    const f = formOf(base)
    expect(updateRequest(base, f).region).toBeUndefined()
    expect(updateRequest(base, f).tags).toBeUndefined()
    expect(updateRequest(base, { ...f, region: ' US ', tags: ['b', ' c '] })).toMatchObject({ region: 'US', tags: ['b', 'c'] })
  })
  it('builds the create body of each kind', () => {
    expect(createRequest({ ...EMPTY_FORM, name: 'n' })).toEqual({ name: 'n', panel_type: 'psp', remark: undefined, update_channel: 'stable' })
    expect(createRequest({ ...EMPTY_FORM, panel_type: 'sui', name: 'n', url: 'https://u', api_token: 't', auth_method: 'token' })).toMatchObject({
      name: 'n', url: 'https://u', panel_type: 'sui', api_token: 't', auth_method: 'token', password: undefined,
    })
  })
})
