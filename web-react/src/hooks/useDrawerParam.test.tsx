/** @vitest-environment jsdom */
import { MemoryRouter, useLocation, useNavigate } from 'react-router'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { useDrawerParam } from './useDrawerParam'

afterEach(cleanup)
function Harness() {
  const sheet = useDrawerParam<string>('sheet', { parse: raw => raw || null, exclusive: ['user'] })
  const user = useDrawerParam('user', { exclusive: ['sheet', 'list', 'node_state'] })
  const location = useLocation()
  const navigate = useNavigate()
  return <>
    <div data-testid="url">{location.pathname + location.search}</div>
    <div data-testid="state">{JSON.stringify(location.state)}</div>
    <div data-testid="sheet">{sheet.id ?? 'closed'}</div>
    <button onClick={() => sheet.open('test', { state: { prefill: { target: 'private.example', port: 443 } } })}>sheet</button>
    <button onClick={() => user.open(7, { replace: true })}>user</button>
    <button onClick={sheet.close}>close sheet</button>
    <button onClick={() => navigate(-1)}>back</button>
  </>
}
const url = () => screen.getByTestId('url').textContent
const state = () => JSON.parse(screen.getByTestId('state').textContent || 'null')

it('replaces sheet with user so Back closes both and prefill never revives', () => {
  render(<MemoryRouter initialEntries={['/before', { pathname: '/access', search: '?tab=policies', state: { retained: 'yes' } }]} initialIndex={1}><Harness /></MemoryRouter>)
  fireEvent.click(screen.getByText('sheet'))
  expect(screen.getByTestId('sheet').textContent).toBe('test')
  expect(state()).toEqual({ retained: 'yes', drawer: 'sheet', prefill: { target: 'private.example', port: 443 } })
  expect(url()).not.toContain('private.example')
  fireEvent.click(screen.getByText('user'))
  expect(url()).toBe('/access?tab=policies&user=7')
  expect(state().prefill.target).toBe('private.example')
  fireEvent.click(screen.getByText('back'))
  expect(url()).toBe('/access?tab=policies')
  expect(state()).toEqual({ retained: 'yes' })
  fireEvent.click(screen.getByText('back'))
  expect(url()).toBe('/before')
})

it('closes a cold linked sheet in place and preserves unrelated parameters', () => {
  render(<MemoryRouter initialEntries={['/before', '/access?tab=lists&sheet=list&list=4&node_state=problem']} initialIndex={1}><Harness /></MemoryRouter>)
  fireEvent.click(screen.getByText('close sheet'))
  expect(url()).toBe('/access?tab=lists&list=4&node_state=problem')
})
