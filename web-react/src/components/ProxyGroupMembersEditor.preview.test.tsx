// @vitest-environment jsdom
import { useState } from 'react'
import { fireEvent, screen, waitFor, act } from '@testing-library/react'
import { expect, it } from 'vitest'
import { api, mount, group } from '@/test/adminSaveHarness'
import type { RuleSet, GroupSubscriptionPreview } from '@/api/rules'
import type { Group } from '@/api/types'
import ProxyGroupMembersEditor from './ProxyGroupMembersEditor'

const draft: RuleSet = { slug: 'test', name: 'Test', sort: 0, enabled: true, direct_subscription_domain:false, content: '- MATCH,Pick', proxy_group_order: ['Pick'], proxy_group_members: { Pick: [{kind: 'node_set', value: 'remaining'}] }, proxy_group_options: {} }
const inspection = { groups: [{name: 'Pick', configured: true, options_configured: false, options: {type:'select'}, default_members: [], members: draft.proxy_group_members!.Pick}], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] }
const result: GroupSubscriptionPreview = {group_id: 1,status:'ready', groups:[{name:'Pick',members:['🇯🇵专线']}]}
const previewGroup: Group = {...group,tag_filter:{all:true,tags:[]},layout:{separators:[],sort:[],default_sort_strategy:''}}

function Editor({ value = draft }: { value?: RuleSet }) {
  return <ProxyGroupMembersEditor content={value.content} groupOrder={value.proxy_group_order || []} initialGroupOrder={[]} onGroupOrderChange={() => {}}
    members={value.proxy_group_members || {}} initialMembers={{}} onChange={() => {}} options={value.proxy_group_options || {}} initialOptions={{}} onOptionsChange={() => {}}
    previewGroups={[previewGroup, {...previewGroup,id:2,name:'Second'}]} draft={value} />
}

it('uses final compiled labels, defaults on the server, and sends the entire draft', async () => {
  api.post.mockImplementation(async (url: string) => ({data: url.endsWith('/preview-proxy-groups') ? result : inspection}))
  mount(<Editor />)
  expect(await screen.findByText('1. 🇯🇵专线')).toBeTruthy()
  expect(screen.queryByText(/\[3:/)).toBeNull()
  expect(screen.queryByText('admin:rules.members.preview_all')).toBeNull()
  expect(screen.getByText('group')).toBeTruthy()
  expect(api.post).toHaveBeenCalledWith('/admin/rules/preview-proxy-groups', {draft,preview_group_id:undefined}, expect.objectContaining({signal:expect.any(AbortSignal),_skipErrorToast:true}))
  expect(screen.queryByText('admin:rules.members.preview_scope_hint')).toBeNull()
})

it('clears stale results on scope changes, ignores late responses, and surfaces errors', async () => {
  let resolveLate: (value: unknown) => void = () => {}
  let previewCalls = 0
  api.post.mockImplementation(async (url: string) => {
    if (!url.endsWith('/preview-proxy-groups')) return {data:inspection}
    previewCalls++
    if (previewCalls === 1) return {data:result}
    if (previewCalls === 2) return new Promise(resolve => { resolveLate = resolve })
    throw {response:{data:{error:'ambiguous line'}}}
  })
  mount(<Editor />)
  await screen.findByText('1. 🇯🇵专线')
  const select = screen.getAllByRole('combobox').at(-1)!
  fireEvent.mouseDown(select)
  fireEvent.click(await screen.findByRole('option',{name:'Second'}))
  await waitFor(() => expect(screen.queryByText('1. 🇯🇵专线')).toBeNull())
  await waitFor(() => expect(previewCalls).toBe(2))
  fireEvent.mouseDown(select)
  fireEvent.click(await screen.findByRole('option',{name:'group'}))
  await screen.findByText('ambiguous line')
  await act(async () => {resolveLate({data:{...result,group_id:2,groups:[{name:'Pick',members:['STALE']}]}})})
  expect(screen.queryByText('1. STALE')).toBeNull()
  expect(screen.queryByText('1. 🇯🇵专线')).toBeNull()
})

it('does not disguise a disabled draft as applied', async () => {
  api.post.mockImplementation(async (url: string) => ({data:url.endsWith('/preview-proxy-groups')? {...result,status:'disabled'}:inspection}))
  mount(<Editor />)
  await screen.findByText('admin:rules.members.preview_disabled')
  expect(screen.queryByText('1. 🇯🇵专线')).toBeNull()
})

it('refreshes for ordering and enabled-state changes without saving', async () => {
  api.post.mockImplementation(async (url: string) => ({data:url.endsWith('/preview-proxy-groups')?result:inspection}))
  const changed={...draft,enabled:false,proxy_group_order:['Another','Pick'],direct_subscription_domain:true}
  function Controlled() {
    const [value,setValue]=useState(draft)
    return <><button onClick={() => setValue(changed)}>Change draft</button><Editor value={value} /></>
  }
  mount(<Controlled />)
  await screen.findByText('1. 🇯🇵专线')
  fireEvent.click(screen.getByRole('button',{name:'Change draft'}))
  await waitFor(() => expect(api.post).toHaveBeenCalledWith('/admin/rules/preview-proxy-groups',expect.objectContaining({draft:changed}),expect.anything()))
  expect(api.put).not.toHaveBeenCalled()
})

it('keeps the resolved default group visible when compilation fails', async () => {
  api.post.mockImplementation(async (url: string) => {
    if (url.endsWith('/preview-proxy-groups')) throw {response:{data:{error:'invalid template',group_id:2}}}
    return {data:inspection}
  })
  mount(<Editor />)
  await screen.findByText('invalid template')
  expect(screen.getByText('Second')).toBeTruthy()
  expect(screen.queryByText('1. 🇯🇵专线')).toBeNull()
})

it('uses the rule-set group type for member-count warnings', async () => {
  api.post.mockImplementation(async (url: string) => ({data:url.endsWith('/preview-proxy-groups')?{...result,groups:[{...result.groups[0],type:'url-test'}]}:inspection}))
  mount(<Editor />)
  await screen.findByText('admin:rules.members.issues.insufficient_auto_members')
  expect(screen.queryByText('admin:rules.members.preview_overridden')).toBeNull()
})

it('shows empty member lists without inventing a DIRECT member', async () => {
  api.post.mockImplementation(async (url: string) => ({data:url.endsWith('/preview-proxy-groups')?{...result,groups:[{...result.groups[0],members:[]}]}:inspection}))
  mount(<Editor />)
  await screen.findByText('admin:rules.members.preview_empty')
  expect(screen.queryByText('1. DIRECT')).toBeNull()
})
