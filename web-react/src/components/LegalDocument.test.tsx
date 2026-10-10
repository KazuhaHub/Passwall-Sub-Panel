// @vitest-environment jsdom
import { ThemeProvider } from '@mui/material/styles'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAppTheme } from '@/theme'
import type { LegalPublicDocument } from '@/api/legal'
import zh from '@/locales/zh-CN/auth.json'
import { flatten, type Nested } from '@/i18n/options'
import LegalDocument, { splitCollectionMarker } from './LegalDocument'
import ReleaseNotes from './ReleaseNotes'
import DataCollectionCard from './DataCollectionCard'

const copy = vi.hoisted(() => ({ dictionary: {} as Record<string, string> }))
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string, values?: Record<string, unknown>) =>
  (copy.dictionary[key] ?? key).replace(/\{\{(\w+)\}\}/g, (match, name: string) => values && name in values ? String(values[name]) : match) }) }))
copy.dictionary = flatten(zh as Nested)
afterEach(cleanup)

const doc: LegalPublicDocument = {
  version: 1, consent_version: 1, locale: 'zh-CN', content: '', published_at: '2026-10-09T00:00:00Z',
  data_collection: { sub_log_retention_days: 0, auth_event_retention_days: 0, connection_retention_days: 7, hwid_captured: false,
    hwid_retention_days: 0, flag_record_retention_days: 90, risk_assessment_refresh_minutes: 30, risk_review_purge_after_deletion_minutes: 60, access: [] },
}

function mount(content: string, mode: 'light' | 'dark' = 'light') {
  return render(<ThemeProvider theme={createAppTheme({ mode, sourceColor: '#6750a4', language: 'zh-CN' })}><LegalDocument document={{ ...doc, content }} /></ThemeProvider>)
}

describe('LegalDocument', () => {
  it.each(['light', 'dark'] as const)('inserts actual collection between text in %s', mode => {
    mount('Before\n\n[[data-collection]]\n\nAfter', mode)
    expect(screen.getByText('Before')).toBeTruthy()
    expect(screen.getByText('After')).toBeTruthy()
    expect(screen.getByRole('heading', { name: '我们收集什么' })).toBeTruthy()
    expect(screen.getAllByText('永久保留')).toHaveLength(2)
    expect(screen.getByText('保留 7 天')).toBeTruthy()
    expect(screen.getByText('保留 90 天')).toBeTruthy()
    expect(screen.getByText('每 30 分钟覆盖')).toBeTruthy()
    expect(screen.queryByText('设备标识（HWID 摘要）')).toBeNull()
    expect(screen.queryByText(/访问规则命中/)).toBeNull()
  })

  it('does not treat inline or code examples as the marker', () => {
    for (const text of ['text [[data-collection]]', '`[[data-collection]]`', '```\n[[data-collection]]\n```', '~~~~\n[[data-collection]]\n~~~~', '    [[data-collection]]']) {
      expect(splitCollectionMarker(text)).toBeNull()
    }
    expect(splitCollectionMarker('```\n[[data-collection]]\n```\n[[data-collection]]\nEnd')).toEqual(['```\n[[data-collection]]\n```', 'End'])
  })

  it('shows enabled HWID capture and only reported nonzero collector counts', () => {
    render(<ThemeProvider theme={createAppTheme({ mode: 'light', sourceColor: '#6750a4', language: 'zh-CN' })}><DataCollectionCard data={{
      ...doc.data_collection, hwid_captured: true, hwid_retention_days: 14,
      access: [{ kind: 'hits', nodes: 2, retention_days: 30 }, { kind: 'trial', nodes: 0, retention_days: 7 }, { kind: 'usage', nodes: 1, retention_days: 7 }],
    }} /></ThemeProvider>)
    expect(screen.getByText('设备标识（HWID 摘要）')).toBeTruthy()
    expect(screen.getByText('保留 14 天')).toBeTruthy()
    expect(screen.getByText('访问规则命中（2 台节点）')).toBeTruthy()
    expect(screen.getByText('按网站用量（1 台节点）')).toBeTruthy()
    expect(screen.queryByText(/白名单试运行/)).toBeNull()
  })

  it('drops unsafe markup and image fetches and keeps only absolute web links', () => {
    const { container } = mount('<script>alert(1)</script>\n\n<img src="https://tracker.test/pixel">\n\n![image description](https://tracker.test/image)\n\n[safe](https://example.test/) [bad](javascript:alert%281%29) [email](mailto:owner@example.test) [relative](/admin)')
    expect(container.querySelector('script, img')).toBeNull()
    expect(screen.getByText('image description')).toBeTruthy()
    const link = screen.getByRole('link', { name: 'safe' })
    expect(link.getAttribute('href')).toBe('https://example.test/')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(container.querySelectorAll('a')).toHaveLength(1)
  })

  it('keeps the same safety rules for existing release notes', () => {
    const { container } = render(<ReleaseNotes>{'<img src="https://tracker.test/">\n\n![alt](https://tracker.test/pixel)\n\n[web](https://example.test/) [bad](javascript:alert%281%29)'}</ReleaseNotes>)
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelectorAll('a')).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'web' }).getAttribute('rel')).toBe('noopener noreferrer')
  })
})
