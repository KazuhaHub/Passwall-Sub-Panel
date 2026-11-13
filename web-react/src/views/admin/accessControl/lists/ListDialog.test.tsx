// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess, snack } from '@/test/accessControlHarness'
import ListDialog from './ListDialog'

vi.mock('@/api/accessControl', async importOriginal => {
  const real = await importOriginal<typeof import('@/api/accessControl')>()
  const fns = Object.fromEntries(Object.keys(real).filter(k => typeof (real as Record<string, unknown>)[k] === 'function').map(k => [k, vi.fn()]))
  return { ...real, ...fns, destErrorOf: real.destErrorOf, isDestUnavailable: real.isDestUnavailable }
})

const axiosErr = (status: number, data: unknown) => Object.assign(new Error('x'), { isAxiosError: true, response: { status, data } })
const report = (o: Partial<api.DestParseReport> = {}): api.DestParseReport => ({ accepted: 1, ignored: 1, rewritten: 1, duplicates: 0, samples: [
  { line: 3, text: '*.fake-bank.net', reason: 'rewritten_to_network' }, { line: 5, text: 'regexp:.*', reason: 'broad_regexp' },
], warnings: [{ line: 7, text: 'regexp:foo', code: 'regexp_no_anchor' }], warning_count: 1, ...o })
const budget = { rules: { used: 0, limit: 1 }, domains: { used: 0, limit: 1 }, regexps: { used: 0, limit: 1 }, cidrs: { used: 0, limit: 1 }, source_cidrs: { used: 0, limit: 1 }, subjects: { used: 0, limit: 1 }, bytes: { used: 0, limit: 1 } }
const written = (id = 7): api.DestListWrite => ({ list: { id, name: 'Risky', kind: 'custom', side: 'dest', entry_count: 1, regexp_count: 0, state: 'ready', empty: false, last_fetched_at: 1, last_error: '', owner_group_id: 0, updated_at: 2, used_by: [] }, report: null })

const onClose = vi.fn()
const onSaved = vi.fn()
const dialog = (p: Partial<React.ComponentProps<typeof ListDialog>> = {}) => <ListDialog open editId={null} onClose={onClose} onSaved={onSaved} {...p} />

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
})

// A required field's label carries a star, so match the start of it.
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(new RegExp(`^${label}`)), { target: { value } })

describe('the list dialog', () => {
  it('previews a custom list as typing pauses (not on every key) and shows what was understood', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report(), entries: [], entry_count: 1, regexp_count: 0 })
    renderAccess(dialog())
    const editor = await screen.findByLabelText('code')
    fireEvent.change(editor, { target: { value: 'a' } })
    fireEvent.change(editor, { target: { value: 'a.com\n*.fake-bank.net' } })
    expect(api.previewDestList).not.toHaveBeenCalled()
    await waitFor(() => expect(api.previewDestList).toHaveBeenCalledTimes(1), { timeout: 2000 })
    expect(vi.mocked(api.previewDestList).mock.calls[0][0]).toMatchObject({ kind: 'custom', side: 'dest', text: 'a.com\n*.fake-bank.net' })
    expect((await screen.findByTestId('parse-report')).textContent).toContain('解析结果  接受 1 条 · 改写 1 条 · 忽略 1 条')
    expect(screen.getByText('已改写：IP 段已对齐到网络号')).toBeTruthy()
    expect(screen.getByText('过宽：几乎匹配所有域名')).toBeTruthy()
    expect(screen.getByText('正则没有字面量锚点，匹配会很慢')).toBeTruthy()
  })

  it('saves a custom list, says so and hands the id over to open the entries', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    vi.mocked(api.createDestList).mockResolvedValue(written())
    renderAccess(dialog())
    type('名称', 'Risky')
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'a.com' } })
    await screen.findByTestId('parse-report')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.createDestList).toHaveBeenCalledWith({ kind: 'custom', side: 'dest', name: 'Risky', text: 'a.com' }))
    expect(snack).toHaveBeenCalledWith('已保存「Risky」', 'success')
    expect(onSaved).toHaveBeenCalledWith(7)
    expect(onClose).toHaveBeenCalled()
  })

  it('lists the lines a 400 refuses, by number, and a name clash lands on the name field', async () => {
    vi.mocked(api.previewDestList).mockRejectedValue(axiosErr(400, { code: 'dest_list_parse_failed', lines: [{ line: 2, text: 'regexp:(', code: 'regexp_invalid', detail: 'missing )' }] }))
    renderAccess(dialog())
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'x\nregexp:(' } })
    expect(await screen.findByText('有无法保存的行')).toBeTruthy()
    expect(screen.getByText('第 2 行')).toBeTruthy()
    expect((screen.getByRole('button', { name: '保存' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('maps a name clash to the name field', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    vi.mocked(api.createDestList).mockRejectedValue(axiosErr(409, { code: 'dest_name_taken' }))
    renderAccess(dialog())
    type('名称', 'Risky')
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'a.com' } })
    await screen.findByTestId('parse-report')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('这个名称已被使用')).toBeTruthy()
  })

  it('a remote list is https only, and is fetched only on [test fetch]', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ accepted: 8012, ignored: 0, samples: [], warnings: [] }), entries: [], entry_count: 8012, regexp_count: 0, http_status: 200, bytes: 319488 })
    renderAccess(dialog({ initialKind: 'remote' }))
    type('地址', 'http://example.org/list.txt')
    expect(await screen.findByText('只支持 https 地址')).toBeTruthy()
    expect((screen.getByRole('button', { name: '测试拉取' }) as HTMLButtonElement).disabled).toBe(true)
    type('地址', 'https://example.org/list.txt')
    expect(api.previewDestList).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '测试拉取' }))
    expect(await screen.findByText(/拉取结果\s+HTTP 200/)).toBeTruthy()
    expect(vi.mocked(api.previewDestList).mock.calls[0][0]).toMatchObject({ kind: 'remote', source_url: 'https://example.org/list.txt' })
  })

  it('an edit seeds from the pasted source, locks the type and sends updated_at; a 409 offers the latest', async () => {
    const list = written(4).list
    vi.mocked(api.getDestList).mockResolvedValue({ list, samples: [], source_text: '# note\na.com' })
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    vi.mocked(api.updateDestList).mockRejectedValue(axiosErr(409, { code: 'dest_list_stale' }))
    renderAccess(dialog({ editId: 4 }))
    await waitFor(() => expect((screen.getByLabelText('code') as HTMLTextAreaElement).value).toBe('# note\na.com'))
    expect(screen.getByRole('button', { name: '自定义' }).hasAttribute('disabled')).toBe(true)
    expect(api.getDestList).toHaveBeenCalledWith(4, true, expect.anything())
    fireEvent.change(screen.getByLabelText('code'), { target: { value: 'b.com' } })
    await screen.findByTestId('parse-report')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(vi.mocked(api.updateDestList).mock.calls[0][1]).toMatchObject({ updated_at: 2, text: 'b.com' }))
    expect(await screen.findByText('这个列表刚被其他人修改过')).toBeTruthy()
    expect(screen.getByRole('button', { name: '载入最新' })).toBeTruthy()
  })

  it('switching the type with input asks first and then clears it', async () => {
    renderAccess(dialog())
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'a.com' } })
    fireEvent.click(screen.getByRole('button', { name: '远程地址' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '切换类型会清空已填内容？' })))
    expect(await screen.findByLabelText(/^地址/)).toBeTruthy()
  })

  it('closing with unsaved input asks first; closing a clean dialog does not', async () => {
    renderAccess(dialog())
    fireEvent.click(await screen.findByRole('button', { name: '关闭' }))
    expect(confirmMock).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
    type('名称', 'x')
    confirmMock.mockResolvedValueOnce(false)
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的修改？' })))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('a community category with no data downloaded says so and offers the download', async () => {
    vi.mocked(api.getGeositeCategories).mockRejectedValue(axiosErr(503, { code: 'dest_geosite_unavailable' }))
    renderAccess(dialog({ initialKind: 'geosite' }))
    expect(await screen.findByText('社区分类数据尚未下载')).toBeTruthy()
    expect(screen.getByRole('button', { name: '立即下载' })).toBeTruthy()
    expect(api.updateGeosite).not.toHaveBeenCalled()
  })

  it('footer: not used, used by an enabled policy (with the node count) and unchanged', async () => {
    const used = { ...written(4).list, used_by: [{ kind: 'policy' as const, id: 9, name: 'P' }] }
    vi.mocked(api.getDestList).mockResolvedValue({ list: used, samples: [], source_text: 'a.com' })
    vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [{ id: 9, state: 'on' } as api.DestPolicy], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
    vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [{ kind: 'psp', supports: { policy: true } }, { kind: 'psp', supports: { policy: true } }], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    renderAccess(dialog({ editId: 4 }))
    expect(await screen.findByText('内容未变化，不会触发重启')).toBeTruthy()
    fireEvent.change(screen.getByLabelText('code'), { target: { value: 'b.com' } })
    expect(await screen.findByText('被 1 条启用的策略使用；内容变化会让 2 台节点各重启一次。')).toBeTruthy()
  })

  it('does not call the empty name an error until it is blurred; typing elsewhere does not mark it', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    renderAccess(dialog())
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'a.com' } })
    await screen.findByTestId('parse-report')
    expect(screen.queryByText('名称必填')).toBeNull()
    fireEvent.blur(screen.getByLabelText(/^名称/))
    expect(await screen.findByText('名称必填')).toBeTruthy()
  })
})
