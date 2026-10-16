// @vitest-environment jsdom
import { useState } from 'react'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, mount } from '@/test/adminSaveHarness'
import type { ProxyGroupInspection, ProxyGroupMember } from '@/api/rules'
import ProxyGroupMembersEditor from './ProxyGroupMembersEditor'

const initialMembers: Record<string, ProxyGroupMember[]> = { Pick: [{ kind: 'builtin', value: 'DIRECT' }] }
const node = (id: number, name: string) => ({ id, display_name: name, server_address: 'example.test', region: 'HK', tags: [], enabled: true })
const inspection: ProxyGroupInspection = {
  groups: [{ name: 'Pick', configured: true, options_configured: false, options: { type: 'select' }, default_members: [], members: initialMembers.Pick, preview: [] }],
  builtins: ['DIRECT'], nodes: [node(1, 'Alpha'), node(2, 'Beta')], regions: [], tags: [], issues: [],
  lines: [
    { node_id: 1, line_id: 'direct', kind: 'direct', name: 'Alpha', display_name: 'Alpha', enabled: true, available: true },
    { node_id: 1, line_id: 'relay-a', kind: 'relay', name: 'my-relay-line', display_name: 'Alpha my-relay-line', enabled: true, available: true },
    { node_id: 1, line_id: 'route-a', kind: 'vless_route', name: 'my-vlessroute-line', display_name: 'Alpha my-vlessroute-line', enabled: true, available: true },
    { node_id: 2, line_id: 'relay-b', kind: 'relay', name: 'beta-only', display_name: 'Beta beta-only', enabled: true, available: false },
  ],
}

function Controlled({ changed }: { changed: (members: Record<string, ProxyGroupMember[]>) => void }) {
  const [members, setMembers] = useState(initialMembers)
  return <ProxyGroupMembersEditor content="- MATCH,Pick" groupOrder={[]} initialGroupOrder={[]} onGroupOrderChange={() => {}}
    members={members} initialMembers={initialMembers} onChange={next => { changed(next); setMembers(next) }}
    options={{}} initialOptions={{}} onOptionsChange={() => {}} previewGroups={[]} />
}

async function setup() {
  api.post.mockResolvedValue({ data: inspection })
  const changed = vi.fn()
  mount(<Controlled changed={changed} />)
  expect(screen.queryByText('admin:rules.members.groups_hint')).toBeNull()
  const kind = await screen.findByText('admin:rules.members.kind_node')
  fireEvent.mouseDown(kind.closest('[role="combobox"]')!)
  fireEvent.click(await screen.findByRole('option', { name: 'admin:rules.members.kind_line' }))
  return changed
}

async function chooseNode(name: string) {
  const input = screen.getByRole('combobox', { name: 'admin:rules.members.select_line_node' })
  fireEvent.click(within(input.closest('.MuiAutocomplete-root') as HTMLElement).getByRole('button', { name: 'Open' }))
  fireEvent.click(await screen.findByRole('option', { name }))
}

it('first chooses a node, then lists only its own named lines and saves stable references', async () => {
  const changed = await setup()
  expect((screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement).disabled).toBe(true)
  await chooseNode('Alpha')
  const line = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  expect(line.disabled).toBe(false)
  fireEvent.change(line, { target: { value: '[' } })
  expect(await screen.findByRole('option', { name: '[admin:rules.members.line_type_direct]' })).toBeTruthy()
  expect(screen.queryByRole('option', { name: /Alpha \[/ })).toBeNull()
  fireEvent.change(line, { target: { value: 'my-' } })
  expect(await screen.findByRole('option', { name: 'my-relay-line [admin:rules.members.line_type_relay]' })).toBeTruthy()
  const route = screen.getByRole('option', { name: 'my-vlessroute-line [admin:rules.members.line_type_vless_route]' })
  expect(screen.queryByRole('option', { name: /beta-only/ })).toBeNull()
  expect(screen.queryByRole('option', { name: /Alpha my-/ })).toBeNull()
  fireEvent.click(route)
  fireEvent.click(screen.getByRole('button', { name: 'admin:rules.members.add' }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith({ Pick: [...initialMembers.Pick, { kind: 'line', node_id: 1, line_id: 'route-a' }] }))
  expect((screen.getByRole('combobox', { name: 'admin:rules.members.select_line_node' }) as HTMLInputElement).value).toBe('Alpha')
  expect(screen.queryByText('line:1:route-a')).toBeNull()
  const memberName = screen.getByText('Alpha my-vlessroute-line')
  expect(memberName.tagName).toBe('SPAN')
  expect(memberName.classList.contains('MuiTypography-noWrap')).toBe(true)
  expect(memberName.getAttribute('title')).toBe('line:1:route-a')
  fireEvent.mouseOver(memberName)
  expect((await screen.findByRole('tooltip')).textContent).toBe('line:1:route-a')
  fireEvent.mouseLeave(memberName)
})

it('shows a category-specific explanation for every member category', async () => {
  await setup()
  for (const [kind, label] of [
    ['line', 'line'], ['node', 'node'], ['remaining', 'remaining'], ['region', 'region'],
    ['tag', 'tag'], ['builtin', 'builtin'], ['proxy_group', 'group'], ['rematch', 'rematch'],
  ]) {
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    const current = screen.getAllByRole('combobox').find(element => element.textContent?.startsWith('admin:rules.members.kind_'))!
    fireEvent.mouseDown(current)
    fireEvent.click(await screen.findByRole('option', { name: `admin:rules.members.kind_${label}` }))
    expect(screen.getByText(`admin:rules.members.kind_${kind}_hint`)).toBeTruthy()
  }
})

it('clears the selected line when changing or clearing its node and keeps unavailable lines identifiable', async () => {
  const changed = await setup()
  await chooseNode('Alpha')
  let line = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  fireEvent.change(line, { target: { value: 'my-relay' } })
  fireEvent.click(await screen.findByRole('option', { name: 'my-relay-line [admin:rules.members.line_type_relay]' }))
  await chooseNode('Beta')
  line = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  expect(line.value).toBe('')
  expect((screen.getByRole('button', { name: 'admin:rules.members.add' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.change(line, { target: { value: 'beta' } })
  expect(await screen.findByRole('option', { name: /beta-only \[admin:rules.members.line_type_relay\]/ })).toBeTruthy()
  expect(screen.getByText('admin:rules.members.line_unavailable')).toBeTruthy()
  expect(screen.queryByRole('option', { name: /my-relay/ })).toBeNull()
  fireEvent.keyDown(line, { key: 'Escape' })
  const parent = screen.getByRole('combobox', { name: 'admin:rules.members.select_line_node' }) as HTMLInputElement
  fireEvent.click(within(parent.closest('.MuiAutocomplete-root') as HTMLElement).getByRole('button', { name: 'Clear' }))
  expect(parent.value).toBe('')
  const clearedLine = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  expect(clearedLine.value).toBe('')
  expect(clearedLine.disabled).toBe(true)
  expect(changed).not.toHaveBeenCalled()
})
