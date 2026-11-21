// @vitest-environment jsdom
import { useState } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { api, mount } from '@/test/adminSaveHarness'
import type { ProxyGroupInspection, ProxyGroupMember, ProxyGroupOptions, ProxyGroupType } from '@/api/rules'
import ProxyGroupMembersEditor from './ProxyGroupMembersEditor'

const initialMembers: Record<string, ProxyGroupMember[]> = { Pick: [{ kind: 'builtin', value: 'DIRECT' }] }
const node = (id: number, name: string) => ({ id, display_name: name, server_address: 'example.test', region: 'HK', tags: [], enabled: true })
const inspection: ProxyGroupInspection = {
  groups: [{ name: 'Pick', configured: true, options_configured: false, options: { type: 'select' }, default_members: [], members: initialMembers.Pick }],
  builtins: ['DIRECT'], nodes: [node(1, 'Alpha'), node(2, 'Beta')], regions: [], tags: [], issues: [],
  lines: [
    { node_id: 1, line_id: 'direct', kind: 'direct', name: 'Alpha', display_name: 'Alpha', available: true },
    { node_id: 1, line_id: 'relay-a', kind: 'relay', name: 'my-relay-line', display_name: 'Alpha my-relay-line', available: true },
    { node_id: 2, line_id: 'relay-b', kind: 'relay', name: 'beta-only', display_name: 'Beta beta-only', available: false },
  ],
}

function Controlled({ changed }: { changed: (members: Record<string, ProxyGroupMember[]>) => void }) {
  const [members, setMembers] = useState(initialMembers)
  return <ProxyGroupMembersEditor content="- MATCH,Pick" groupOrder={[]} initialGroupOrder={[]} onGroupOrderChange={() => {}}
    members={members} initialMembers={initialMembers} onChange={next => { changed(next); setMembers(next) }}
    options={{}} initialOptions={{}} onOptionsChange={() => {}} previewGroups={[]} />
}

function OptionsEditor({ initialOptions, changed }: { initialOptions: Record<string, ProxyGroupOptions>; changed: (options: Record<string, ProxyGroupOptions>) => void }) {
  const [options, setOptions] = useState(initialOptions)
  return <ProxyGroupMembersEditor content="- MATCH,Pick" groupOrder={[]} initialGroupOrder={[]} onGroupOrderChange={() => {}}
    members={initialMembers} initialMembers={initialMembers} onChange={() => {}}
    options={options} initialOptions={initialOptions} onOptionsChange={next => { changed(next); setOptions(next) }} previewGroups={[]} />
}

async function chooseType(type: ProxyGroupType) {
  fireEvent.change(await screen.findByRole('combobox', { name: 'admin:rules.members.group_type' }), { target: { value: type } })
}

/** The member kind of the add row ("select by node", "select by line", ...). */
async function chooseKind(kind: string) {
  fireEvent.change(await screen.findByRole('combobox', { name: 'admin:rules.members.kind_label' }), { target: { value: kind } })
}

/** Types into a combobox and shows its list (the list opens when the person types or presses the down arrow). */
function typeInto(input: HTMLElement, text: string) {
  fireEvent.change(input, { target: { value: text } })
  openList(input)
}

/** Opens a combobox's list the way a keyboard user does. */
function openList(input: HTMLElement) {
  fireEvent.keyDown(input, { key: 'ArrowDown' })
}

async function setup() {
  api.post.mockResolvedValue({ data: inspection })
  const changed = vi.fn()
  mount(<Controlled changed={changed} />)
  expect(screen.queryByText('admin:rules.members.groups_hint')).toBeNull()
  await chooseKind('line')
  return changed
}

async function chooseNode(name: string) {
  openList(screen.getByRole('combobox', { name: 'admin:rules.members.select_line_node' }))
  fireEvent.click(await screen.findByRole('option', { name }))
}

it('loads and edits the optional test URL for manual select groups without automatic test parameters', async () => {
  api.post.mockResolvedValue({ data: inspection })
  const initialOptions: Record<string, ProxyGroupOptions> = { Pick: { type: 'select', url: 'https://example.com/saved' } }
  const changed = vi.fn()
  mount(<OptionsEditor initialOptions={initialOptions} changed={changed} />)
  const input = await screen.findByRole('textbox', { name: 'admin:rules.members.test_url' }) as HTMLInputElement
  expect(input.value).toBe('https://example.com/saved')
  for (const field of ['interval', 'timeout', 'lazy', 'tolerance', 'strategy']) {
    expect(screen.queryByLabelText(`admin:rules.members.${field}`)).toBeNull()
  }
  fireEvent.change(input, { target: { value: '' } })
  expect(changed).toHaveBeenLastCalledWith({ Pick: { type: 'select', url: '' } })
  fireEvent.change(input, { target: { value: 'https://example.com/check' } })
  expect(changed).toHaveBeenLastCalledWith({ Pick: { type: 'select', url: 'https://example.com/check' } })
  expect(input.value).toBe('https://example.com/check')
  await waitFor(() => expect(api.post).toHaveBeenLastCalledWith('/admin/rules/inspect-proxy-groups', expect.objectContaining({
    proxy_group_options: { Pick: { type: 'select', url: 'https://example.com/check' } },
  }), expect.anything()))
})

it.each(['https://example.com/saved', ''])('preserves the test URL %j when switching between manual and automatic types', async url => {
  api.post.mockResolvedValue({ data: inspection })
  const initialOptions: Record<string, ProxyGroupOptions> = { Pick: { type: 'select', url } }
  const changed = vi.fn()
  mount(<OptionsEditor initialOptions={initialOptions} changed={changed} />)
  const input = await screen.findByRole('textbox', { name: 'admin:rules.members.test_url' }) as HTMLInputElement
  for (const type of ['url-test', 'fallback', 'load-balance'] as const) {
    await chooseType(type)
    expect(input.value).toBe(url)
    expect(changed).toHaveBeenLastCalledWith({ Pick: expect.objectContaining({ type, url, interval: 300, timeout: 5000, lazy: true }) })
    await chooseType('select')
    expect(input.value).toBe(url)
    expect(changed).toHaveBeenLastCalledWith({ Pick: expect.objectContaining({ type: 'select', url }) })
    expect(screen.queryByLabelText('admin:rules.members.interval')).toBeNull()
  }
  fireEvent.click(screen.getByRole('button', { name: 'admin:rules.members.restore_default' }))
  expect(changed).toHaveBeenLastCalledWith({})
  expect(input.value).toBe('')
})

it('preserves all edited parameters across types and inspection refreshes, including zero and false', async () => {
  api.post.mockResolvedValue({ data: inspection })
  const saved: ProxyGroupOptions = { type: 'load-balance', url: 'https://example.com/check', interval: 0, timeout: 1234, lazy: false, strategy: 'round-robin' }
  const changed = vi.fn()
  mount(<OptionsEditor initialOptions={{ Pick: saved }} changed={changed} />)
  await screen.findByRole('combobox', { name: 'admin:rules.members.strategy' })
  await chooseType('url-test')
  fireEvent.change(screen.getByLabelText('admin:rules.members.tolerance'), { target: { value: '73' } })
  const expected = { ...saved, tolerance: 73 }
  for (const type of ['select', 'fallback', 'load-balance', 'url-test'] as const) {
    await chooseType(type)
    expect(changed).toHaveBeenLastCalledWith({ Pick: { ...expected, type } })
    await waitFor(() => expect(api.post).toHaveBeenLastCalledWith('/admin/rules/inspect-proxy-groups', expect.objectContaining({
      proxy_group_options: { Pick: { ...expected, type } },
    }), expect.anything()))
    if (type === 'select') {
      expect(screen.queryByLabelText('admin:rules.members.interval')).toBeNull()
    } else {
      expect((screen.getByLabelText('admin:rules.members.interval') as HTMLInputElement).value).toBe('0')
      expect((screen.getByLabelText('admin:rules.members.timeout') as HTMLInputElement).value).toBe('1234')
      expect(screen.getByRole('switch', { name: 'admin:rules.members.lazy' }).getAttribute('aria-checked')).toBe('false')
    }
    if (type === 'load-balance') expect((screen.getByRole('combobox', { name: 'admin:rules.members.strategy' }) as HTMLSelectElement).value).toBe('round-robin')
    if (type === 'url-test') expect((screen.getByLabelText('admin:rules.members.tolerance') as HTMLInputElement).value).toBe('73')
  }
  expect(saved).not.toHaveProperty('tolerance')
})

it('shows dangling-reference warnings without blocking save or rewriting members', async () => {
  const members: Record<string, ProxyGroupMember[]> = { Pick: [
    { kind: 'proxy_group', value: 'Deleted group' },
    { kind: 'rematch', value: 'Deleted Rematch' },
    { kind: 'builtin', value: 'Unknown' },
  ] }
  const issues: ProxyGroupInspection['issues'] = ['missing_group', 'missing_rematch', 'unknown_builtin'].map(code => ({
    level: 'warning', group: 'Pick', code, message: code,
  }))
  api.post.mockResolvedValue({ data: { ...inspection, issues } })
  const validation = vi.fn()
  const changed = vi.fn()
  mount(<ProxyGroupMembersEditor content="- MATCH,Pick" groupOrder={[]} initialGroupOrder={[]} onGroupOrderChange={() => {}}
    members={members} initialMembers={members} onChange={changed} options={{}} initialOptions={{}} onOptionsChange={() => {}}
    previewGroups={[]} onValidationChange={validation} />)
  await waitFor(() => expect(validation).toHaveBeenLastCalledWith(false))
  for (const issue of issues) expect(await screen.findByText(`admin:rules.members.issues.${issue.code}`)).toBeTruthy()
  expect(changed).not.toHaveBeenCalled()
})

it('first chooses a node, then lists only its own named lines and saves stable references', async () => {
  const changed = await setup()
  expect((screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement).disabled).toBe(true)
  await chooseNode('Alpha')
  const line = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  expect(line.disabled).toBe(false)
  typeInto(line, '[')
  expect(await screen.findByRole('option', { name: '[admin:rules.members.line_type_direct]' })).toBeTruthy()
  expect(screen.queryByRole('option', { name: /Alpha \[/ })).toBeNull()
  typeInto(line, 'my-')
  const relay = await screen.findByRole('option', { name: 'my-relay-line [admin:rules.members.line_type_relay]' })
  expect(screen.queryByRole('option', { name: /beta-only/ })).toBeNull()
  expect(screen.queryByRole('option', { name: /Alpha my-/ })).toBeNull()
  fireEvent.click(relay)
  fireEvent.click(screen.getByRole('button', { name: 'admin:rules.members.add' }))
  await waitFor(() => expect(changed).toHaveBeenCalledWith({ Pick: [...initialMembers.Pick, { kind: 'line', node_id: 1, line_id: 'relay-a' }] }))
  expect((screen.getByRole('combobox', { name: 'admin:rules.members.select_line_node' }) as HTMLInputElement).value).toBe('Alpha')
  expect(screen.queryByText('line:1:relay-a')).toBeNull()
  // The full identity is the name's title (and the name takes focus), not a tooltip.
  const memberName = screen.getByText('admin:rules.members.member_label_relay')
  expect(memberName.tagName).toBe('SPAN')
  expect(memberName.getAttribute('title')).toBe('line:1:relay-a')
  expect(memberName.getAttribute('tabindex')).toBe('0')
})

it('prefixes sortable cards by member type without changing identities or inspection payloads', async () => {
  const members: Record<string, ProxyGroupMember[]> = { Pick: [
    { kind: 'node', node_id: 1 },
    { kind: 'line', node_id: 1, line_id: 'direct' },
    { kind: 'line', node_id: 1, line_id: 'relay-a' },
    { kind: 'node_set', value: 'remaining' },
    { kind: 'node_set', value: 'region:HK' },
    { kind: 'node_set', value: 'tag:fast' },
    { kind: 'builtin', value: 'DIRECT' },
    { kind: 'proxy_group', value: 'Other' },
    { kind: 'rematch', value: 'Mark' },
    { kind: 'line', node_id: 1, line_id: 'deleted' },
  ] }
  api.post.mockResolvedValue({ data: inspection })
  const changed = vi.fn()
  mount(<ProxyGroupMembersEditor content="- MATCH,Pick" groupOrder={[]} initialGroupOrder={[]} onGroupOrderChange={() => {}}
    members={members} initialMembers={members} onChange={changed} options={{}} initialOptions={{}} onOptionsChange={() => {}} previewGroups={[]} />)
  await screen.findByText('admin:rules.members.member_label_relay')
  for (const key of ['node', 'direct', 'relay', 'builtin', 'proxy_group', 'rematch', 'line']) {
    const name = screen.getByText(`admin:rules.members.member_label_${key}`)
    expect(name.tagName).toBe('SPAN')
    expect(name.getAttribute('title')).toBeTruthy()
  }
  for (const key of ['remaining', 'region_set', 'tag_set']) expect(screen.getByText(`admin:rules.members.${key}`)).toBeTruthy()
  expect(screen.getByText('admin:rules.members.member_label_direct').getAttribute('title')).toBe('line:1:direct')
  expect(screen.getByText('admin:rules.members.member_label_rematch').getAttribute('title')).toBe('rematch:Mark:0')
  expect(api.post).toHaveBeenCalledWith('/admin/rules/inspect-proxy-groups', expect.objectContaining({ proxy_group_members: members }), expect.anything())
  expect(changed).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'common:actions.delete admin:rules.members.member_label_relay' }))
  expect(changed).toHaveBeenCalledWith({ Pick: members.Pick.filter(member => member.line_id !== 'relay-a') })
})

it('shows a category-specific explanation for every member category', async () => {
  await setup()
  // The select's values are the kinds themselves ('group' is the proxy_group kind's value in the list).
  for (const kind of ['line', 'node', 'remaining', 'region', 'tag', 'builtin', 'proxy_group', 'rematch']) {
    await chooseKind(kind)
    expect(screen.getByText(`admin:rules.members.kind_${kind}_hint`)).toBeTruthy()
  }
})

it('clears the selected line when changing or clearing its node and keeps unavailable lines identifiable', async () => {
  const changed = await setup()
  await chooseNode('Alpha')
  let line = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  typeInto(line, 'my-relay')
  fireEvent.click(await screen.findByRole('option', { name: 'my-relay-line [admin:rules.members.line_type_relay]' }))
  await chooseNode('Beta')
  line = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  expect(line.value).toBe('')
  expect((screen.getByRole('button', { name: 'admin:rules.members.add' }) as HTMLButtonElement).disabled).toBe(true)
  typeInto(line, 'beta')
  expect(await screen.findByRole('option', { name: /beta-only \[admin:rules.members.line_type_relay\]/ })).toBeTruthy()
  expect(screen.getByText('admin:rules.members.line_unavailable')).toBeTruthy()
  expect(screen.queryByRole('option', { name: /my-relay/ })).toBeNull()
  fireEvent.keyDown(line, { key: 'Escape' })
  const parent = screen.getByRole('combobox', { name: 'admin:rules.members.select_line_node' }) as HTMLInputElement
  fireEvent.click(parent.closest('[data-slot="input-group"]')!.querySelector('[data-slot="combobox-clear"]')!)
  expect(parent.value).toBe('')
  const clearedLine = screen.getByRole('combobox', { name: 'admin:rules.members.select_line' }) as HTMLInputElement
  expect(clearedLine.value).toBe('')
  expect(clearedLine.disabled).toBe(true)
  expect(changed).not.toHaveBeenCalled()
})
