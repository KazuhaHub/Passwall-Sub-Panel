// @vitest-environment jsdom
import { configure, fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

// Counts the full parses of the rules text: the cost of a keystroke in the Text view is dominated by them (a rule set of
// 9,500 lines takes about 30 ms to read, and the page used to read it several times per keystroke).
vi.mock('@/utils/ruleset/parse', async importOriginal => {
  const actual = await importOriginal<typeof import('@/utils/ruleset/parse')>()
  return { ...actual, parseDoc: vi.fn(actual.parseDoc) }
})

import { parseDoc } from '@/utils/ruleset/parse'
import { api, installReads, list, mountRuleSets } from './testHarness'

configure({ asyncUtilTimeout: 4000 })

const content = '- DOMAIN,a.example,Proxy\n- DOMAIN-SUFFIX,b.example,Proxy\n# ads\n- DOMAIN-KEYWORD,adx,Block\n'
const base = { slug: 'custom', name: 'Custom rules', sort: 5, enabled: true, direct_subscription_domain: false, proxy_group_order: [] as string[], content, revision: 'r1' }
const inspection = { groups: [], builtins: [], nodes: [], lines: [], regions: [], tags: [], issues: [] }

describe('typing in the Text view', () => {
  it('does not read the whole document on every keystroke, and saves exactly what was typed', async () => {
    installReads({ '/admin/rules': list([base]), '/admin/templates': list([]), '/admin/groups': list([]) })
    api.post.mockResolvedValue({ data: inspection })
    mountRuleSets('/admin/rules/custom')
    fireEvent.click(await screen.findByRole('radio', { name: 'Text' }))
    const box = await screen.findByRole('textbox', { name: 'Main rules' })
    const spy = vi.mocked(parseDoc)
    // Let anything the switch itself schedules finish before counting.
    await new Promise(r => setTimeout(r, 700))
    spy.mockClear()

    let text = content
    for (let i = 0; i < 12; i++) {
      text += `# note ${i}\n`
      fireEvent.change(box, { target: { value: text } })
    }
    // Twelve keystrokes later nothing has read the text yet: the document is read once typing pauses.
    expect(spy).not.toHaveBeenCalled()
    // ... and then it is read once for the page, not once per consumer (the editor, the page's own checks).
    await waitFor(() => expect(spy).toHaveBeenCalled())
    await new Promise(r => setTimeout(r, 700))
    expect(spy.mock.calls.length).toBeLessThanOrEqual(2)

    fireEvent.click(await screen.findByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
    expect((api.put.mock.calls[0][1] as { content: string }).content).toBe(text)
  })
})
