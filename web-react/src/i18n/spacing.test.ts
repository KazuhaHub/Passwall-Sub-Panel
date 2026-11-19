import '@/test/initI18n'
import i18n from 'i18next'
import { describe, expect, it } from 'vitest'
import { resolveBoundaries, spacedT } from './spacing'

const zh = spacedT(i18n.getFixedT('zh-CN'))
const en = spacedT(i18n.getFixedT('en-US'))
const O = ''
const C = ''
const v = (s: string) => `${O}${s}${C}`

describe('resolveBoundaries', () => {
  it('adds a space only where Chinese meets a Latin letter or digit', () => {
    expect(resolveBoundaries(`经${v('Taiwan')}端口转发`)).toBe('经 Taiwan 端口转发')
    expect(resolveBoundaries(`已选${v('3')}项`)).toBe('已选 3 项')
  })
  it('leaves Chinese next to Chinese alone', () => {
    expect(resolveBoundaries(`经${v('香港')}端口转发`)).toBe('经香港端口转发')
  })
  it('never adds a space next to full-width punctuation', () => {
    expect(resolveBoundaries(`（${v('Cloudflare')}），再到${v('Irvine')}`)).toBe('（Cloudflare），再到 Irvine')
    expect(resolveBoundaries(`${v('Irvine')}，再`)).toBe('Irvine，再')
  })
  it('never touches the inside of a value, and never doubles an existing space', () => {
    expect(resolveBoundaries(`选择 ${v('香港01')}`)).toBe('选择 香港01')
    expect(resolveBoundaries(`${v('HK03')} 已中断`)).toBe('HK03 已中断')
    expect(resolveBoundaries(`到${v('香港 01')}去`)).toBe('到香港 01 去')
  })
  it('resolves two values meeting directly', () => {
    expect(resolveBoundaries(`${v('HK03')}${v('已中断')}`)).toBe('HK03 已中断')
    expect(resolveBoundaries(`${v('香港')}${v('已中断')}`)).toBe('香港已中断')
  })
  it('ignores empty values', () => {
    expect(resolveBoundaries(`经${v('')}到${v('X')}`)).toBe('经到 X')
  })
  it('does not add spaces between literal template text: those are written in the template', () => {
    expect(resolveBoundaries('按Esc取消')).toBe('按Esc取消')
  })
})

describe('rendered examples (zh-CN templates)', () => {
  // The same template, filled with the kinds of value that really occur.
  const cases: [string, string, Record<string, unknown>, string][] = [
    ['a Chinese name', 'ui.data.select_row_named', { name: '香港节点' }, '选择香港节点'],
    ['a Latin name', 'ui.data.select_row_named', { name: 'Taiwan HiNet' }, '选择 Taiwan HiNet'],
    ['a name ending in a digit', 'ui.data.select_row_named', { name: '香港01' }, '选择香港01'],
    ['a Latin name ending in a digit', 'ui.data.select_row_named', { name: 'HK 03' }, '选择 HK 03'],
    ['a number', 'ui.data.selected_count', { count: 3 }, '已选 3 项'],
    ['two numbers', 'ui.form.number_range', { min: 1, max: 600 }, '取值范围是 1 到 600'],
    ['a value before full-width punctuation', 'ui.data.reorder_picked', { name: 'Irvine Home', pos: 2, total: 9 }, '已拿起 Irvine Home，当前第 2 位，共 9 项'],
    ['a Chinese value before full-width punctuation', 'ui.data.reorder_picked', { name: '香港01', pos: 2, total: 9 }, '已拿起香港01，当前第 2 位，共 9 项'],
    ['a value after full-width punctuation', 'ui.form.inherit_mode', { label: 'Traffic', level: '本人' }, 'Traffic：本人的取值方式'],
    ['a Latin value then Chinese', 'ui.diagram.status_items', { items: 'Irvine Home', status: '已中断' }, 'Irvine Home 已中断'],
    ['a Chinese value then Chinese', 'ui.diagram.status_items', { items: '落地节点', status: '已中断' }, '落地节点已中断'],
  ]
  it.each(cases)('%s', (_name, key, opts, expected) => {
    expect(zh(key, opts)).toBe(expected)
  })

  it('does not touch English', () => {
    expect(en('ui.data.select_row_named', { name: '香港01' })).toBe('Select 香港01')
    expect(en('ui.data.selected_count', { count: 3 })).toBe('3 selected')
  })

  it('keeps plural selection working: the count is wrapped for spacing, not for choosing the form', () => {
    expect(en('ui.diagram.graph_through', { count: 1 })).toBe('1 line passes through')
    expect(en('ui.diagram.graph_through', { count: 2 })).toBe('2 lines pass through')
    expect(zh('ui.diagram.graph_through', { count: 2 })).toBe('2 条线路经过')
  })
})
