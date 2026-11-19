import { describe, expect, it } from 'vitest'
import { autoSpace } from './cjkSpacing'

describe('autoSpace', () => {
  it('puts a space between Chinese and Latin letters or digits', () => {
    expect(autoSpace('用户经Taiwan HiNet Dynamic端口转发到Irvine Home')).toBe('用户经 Taiwan HiNet Dynamic 端口转发到 Irvine Home')
    expect(autoSpace('已选3项')).toBe('已选 3 项')
  })
  it('leaves two Chinese words together and an existing space alone', () => {
    expect(autoSpace('端口转发到落地')).toBe('端口转发到落地')
    expect(autoSpace('选择 Node 02')).toBe('选择 Node 02')
  })
  it('does not touch English or full-width punctuation', () => {
    expect(autoSpace('User through Entry by port forwarding')).toBe('User through Entry by port forwarding')
    expect(autoSpace('Irvine（:443）')).toBe('Irvine（:443）')
  })
})
