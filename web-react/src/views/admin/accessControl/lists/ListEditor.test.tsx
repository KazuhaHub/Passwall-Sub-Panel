// @vitest-environment jsdom
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import * as api from '@/api/accessControl'
import { confirmMock, renderAccess, snack, whereIs } from '@/test/accessControlHarness'
import ListEditor from './ListEditor'
import { ListCreateRoute, ListDetailRoute } from './ListRoutes'

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

const onCreated = vi.fn()
const dialog = (p: Partial<React.ComponentProps<typeof ListEditor>> = {}) => (
  <ListEditor editId={null} back={{ to: '/admin/access-control', label: '访问控制' }} onCreated={onCreated} {...p} />
)
/** The Save button is aria-disabled (not disabled) while the bar names what to fix. */
const saveOff = () => screen.getByRole('button', { name: '保存' }).getAttribute('aria-disabled') === 'true'

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getDestPolicies).mockResolvedValue({ allow: [], block: [], observe: [], exemptions: { count: 0 }, allowlist_groups: [], hit_window_days: 7, budget })
  vi.mocked(api.getDestLists).mockResolvedValue({ items: [], refresh_hours: 24, budget })
  vi.mocked(api.getDestStatus).mockResolvedValue({ nodes: [], policy_notes: {}, totals: {} } as unknown as api.DestStatus)
})

// A required field's label carries a star, so match the start of it.
const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(new RegExp(`^${label}`)), { target: { value } })

describe('the list editor page', () => {
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
    expect(onCreated).toHaveBeenCalledWith(7)
  })

  it('lists the lines a 400 refuses, by number, and a name clash lands on the name field', async () => {
    vi.mocked(api.previewDestList).mockRejectedValue(axiosErr(400, { code: 'dest_list_parse_failed', lines: [{ line: 2, text: 'regexp:(', code: 'regexp_invalid', detail: 'missing )' }] }))
    renderAccess(dialog())
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'x\nregexp:(' } })
    expect(await screen.findByText('有无法保存的行')).toBeTruthy()
    expect(screen.getByText('第 2 行')).toBeTruthy()
    expect(saveOff()).toBe(true)
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

  it('saves a remote list with exactly the fields the old editor sent', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0, http_status: 200, bytes: 10 })
    vi.mocked(api.createDestList).mockResolvedValue(written(8))
    renderAccess(dialog({ initialKind: 'remote' }))
    type('名称', ' Feed ')
    type('地址', ' https://example.org/list.txt ')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.createDestList).toHaveBeenCalledWith({ kind: 'remote', side: 'dest', name: 'Feed', source_url: 'https://example.org/list.txt' }))
  })

  it('saves a community category with its attributes and no name', async () => {
    vi.mocked(api.getGeositeCategories).mockResolvedValue({ categories: [{ name: 'cn', entries: 5, attrs: [{ name: '!cn', entries: 1 }] }] } as never)
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 5, regexp_count: 0 })
    vi.mocked(api.createDestList).mockResolvedValue(written(9))
    renderAccess(dialog({ initialKind: 'geosite' }))
    const input = await screen.findByLabelText('分类') as HTMLInputElement
    await waitFor(() => expect(input.disabled).toBe(false))
    fireEvent.mouseDown(input)
    fireEvent.click(await screen.findByRole('option', { name: 'cn · 5 条' }))
    await waitFor(() => expect(saveOff()).toBe(false))
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.createDestList).toHaveBeenCalledWith({ kind: 'geosite', geosite_category: 'cn', geosite_attrs: [] }))
  })

  it('an edit seeds from the pasted source, locks the type and sends updated_at; a 409 offers the latest', async () => {
    const list = written(4).list
    vi.mocked(api.getDestList).mockResolvedValue({ list, samples: [], source_text: '# note\na.com' })
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    vi.mocked(api.updateDestList).mockRejectedValue(axiosErr(409, { code: 'dest_list_stale' }))
    renderAccess(dialog({ editId: 4 }))
    await waitFor(() => expect((screen.getByLabelText('code') as HTMLTextAreaElement).value).toBe('# note\na.com'))
    expect(screen.getByRole('radio', { name: '自定义' }).getAttribute('aria-disabled') === 'true').toBe(true)
    expect(api.getDestList).toHaveBeenCalledWith(4, true, expect.anything())
    fireEvent.change(screen.getByLabelText('code'), { target: { value: 'b.com' } })
    await screen.findByTestId('parse-report')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(vi.mocked(api.updateDestList).mock.calls[0][1]).toMatchObject({ updated_at: 2, text: 'b.com' }))
    expect((await screen.findAllByText('这个列表刚被其他人修改过')).length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: '载入最新' })).toBeTruthy()
  })

  it('switching the type with input asks first and then clears it', async () => {
    renderAccess(dialog())
    fireEvent.change(await screen.findByLabelText('code'), { target: { value: 'a.com' } })
    fireEvent.click(screen.getByRole('radio', { name: '远程地址' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '切换类型会清空已填内容？' })))
    expect(await screen.findByLabelText(/^地址/)).toBeTruthy()
  })

  it('leaving with unsaved input asks first, with Cancel focused; a clean page leaves without asking', async () => {
    const view = renderAccess(dialog())
    await screen.findByLabelText('code')
    await view.router.navigate('/admin/dashboard')
    expect(confirmMock).not.toHaveBeenCalled()
    expect(view.router.state.location.pathname).toBe('/admin/dashboard')
  })

  it('leaving a page with typed input is held until the prompt is answered', async () => {
    const view = renderAccess(dialog())
    type('名称', 'x')
    confirmMock.mockResolvedValueOnce(false)
    await view.router.navigate('/admin/dashboard')
    await waitFor(() => expect(confirmMock).toHaveBeenCalledWith(expect.objectContaining({ title: '放弃未保存的修改？', initialFocus: 'cancel' })))
    expect(view.router.state.location.pathname).not.toBe('/admin/dashboard')
  })

  it('Enter in the name (or a form submit) writes nothing', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0, http_status: 200, bytes: 10 })
    renderAccess(dialog({ initialKind: 'remote' }))
    type('名称', 'Feed')
    type('地址', 'https://example.org/list.txt')
    const name = screen.getByLabelText(/^名称/)
    fireEvent.keyDown(name, { key: 'Enter' })
    fireEvent.submit(name.closest('form')!)
    await new Promise(r => setTimeout(r, 50))
    expect(api.createDestList).not.toHaveBeenCalled()
  })

  it('after an edit is saved the page is clean even when the typed name had spaces around it', async () => {
    const list = { ...written(4).list, name: 'Risky' }
    vi.mocked(api.getDestList).mockResolvedValue({ list, samples: [], source_text: 'a.com' })
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0 })
    vi.mocked(api.updateDestList).mockResolvedValue({ list: { ...list, updated_at: 3 }, report: null })
    renderAccess(dialog({ editId: 4 }))
    await waitFor(() => expect((screen.getByLabelText('code') as HTMLTextAreaElement).value).toBe('a.com'))
    await screen.findByTestId('parse-report')
    type('名称', ' Risky ')
    await screen.findByRole('button', { name: '保存' })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(api.updateDestList).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByRole('button', { name: '保存' })).toBeNull())
    expect((screen.getByLabelText(/^名称/) as HTMLInputElement).value).toBe('Risky')
  })

  it('a second Save while one is running does not write twice', async () => {
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0, http_status: 200, bytes: 10 })
    let release: (w: api.DestListWrite) => void = () => undefined
    vi.mocked(api.createDestList).mockReturnValue(new Promise(r => { release = r }))
    renderAccess(dialog({ initialKind: 'remote' }))
    type('名称', 'Feed')
    type('地址', 'https://example.org/list.txt')
    const save = screen.getByRole('button', { name: '保存' })
    fireEvent.click(save)
    fireEvent.click(save)
    release(written(8))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(8))
    expect(api.createDestList).toHaveBeenCalledTimes(1)
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
    expect(await screen.findByText('被 1 条启用的策略使用；内容变化会让最多 2 台节点各重启一次。')).toBeTruthy()
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

describe('the list routes', () => {
  it('a malformed id is "not found" in the page frame and asks for nothing', async () => {
    renderAccess(<ListDetailRoute />, { route: '/admin/access-control/lists/0', path: '/admin/access-control/lists/:id' })
    expect(await screen.findByText('没有找到这个列表，可能已被删除')).toBeTruthy()
    expect(api.getDestList).not.toHaveBeenCalled()
  })

  it('a 404 read is "not found" too', async () => {
    vi.mocked(api.getDestList).mockRejectedValue(axiosErr(404, { code: 'dest_bad_id' }))
    renderAccess(<ListDetailRoute />, { route: '/admin/access-control/lists/9', path: '/admin/access-control/lists/:id' })
    expect(await screen.findByText('没有找到这个列表，可能已被删除')).toBeTruthy()
  })

  it('a create lands on the new list\'s page, replacing the form, and a clean page then leaves without asking', async () => {
    vi.mocked(api.createDestList).mockResolvedValue(written(8))
    vi.mocked(api.previewDestList).mockResolvedValue({ report: report({ samples: [], warnings: [] }), entries: [], entry_count: 1, regexp_count: 0, http_status: 200, bytes: 10 })
    const view = renderAccess(<ListCreateRoute />, { route: '/admin/access-control/lists/new?kind=remote', path: '/admin/access-control/lists/new' })
    type('名称', 'Feed')
    type('地址', 'https://example.org/list.txt')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(whereIs(view.router)).toBe('/admin/access-control/lists/8'))
    expect(confirmMock).not.toHaveBeenCalled()
  })
})
