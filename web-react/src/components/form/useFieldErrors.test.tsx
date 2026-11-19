// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useFieldErrors } from './useFieldErrors'

afterEach(cleanup)

interface Draft { name: string; port: string }
const rules = {
  name: (d: Draft) => (d.name.trim() ? null : 'Enter a name'),
  port: (d: Draft) => (/^\d+$/.test(d.port) && +d.port >= 1 && +d.port <= 65535 ? null : 'Enter a port between 1 and 65535'),
}

describe('useFieldErrors', () => {
  it('shows nothing until a field is left', () => {
    const { result } = renderHook(() => useFieldErrors<Draft>(rules))
    expect(result.current.errors).toEqual({})
    act(() => { result.current.blur('name', { name: '', port: '' }) })
    expect(result.current.errors).toEqual({ name: 'Enter a name' })
  })

  it('clears an error as soon as the edit fixes it, without waiting for another blur', () => {
    const { result } = renderHook(() => useFieldErrors<Draft>(rules))
    act(() => { result.current.blur('port', { name: 'a', port: '0' }) })
    expect(result.current.errors.port).toBeDefined()
    act(() => { result.current.change('port', { name: 'a', port: '443' }) })
    expect(result.current.errors.port).toBeUndefined()
  })

  it('does not nag about a field that has not been blurred while it is typed into', () => {
    const { result } = renderHook(() => useFieldErrors<Draft>(rules))
    act(() => { result.current.change('port', { name: 'a', port: '4' }) })
    expect(result.current.errors).toEqual({})
  })

  it('validates everything on submit and says whether the form may be saved', () => {
    const { result } = renderHook(() => useFieldErrors<Draft>(rules))
    let ok = true
    act(() => { ok = result.current.validateAll({ name: '', port: 'x' }) })
    expect(ok).toBe(false)
    expect(Object.keys(result.current.errors).sort()).toEqual(['name', 'port'])
    expect(result.current.count).toBe(2)
    act(() => { ok = result.current.validateAll({ name: 'a', port: '443' }) })
    expect(ok).toBe(true)
    expect(result.current.count).toBe(0)
  })

  it('lets the server put an error on a field', () => {
    const { result } = renderHook(() => useFieldErrors<Draft>(rules))
    act(() => { result.current.setError('name', 'That name is taken. Choose another.') })
    expect(result.current.errors.name).toBe('That name is taken. Choose another.')
    act(() => { result.current.reset() })
    expect(result.current.errors).toEqual({})
  })
})
