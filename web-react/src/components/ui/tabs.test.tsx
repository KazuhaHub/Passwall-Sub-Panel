// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { Tabs, TabsContent, TabsList, TabsTrigger } from './tabs'
import * as tabsModule from './tabs'

afterEach(cleanup)

function Demo() {
  return (
    <Tabs defaultValue="rules">
      <TabsList>
        <TabsTrigger value="rules">Rules</TabsTrigger>
        <TabsTrigger value="groups">Proxy groups</TabsTrigger>
        <TabsTrigger value="general" disabled>General</TabsTrigger>
      </TabsList>
      <TabsContent value="rules">r</TabsContent>
      <TabsContent value="groups">g</TabsContent>
    </Tabs>
  )
}

describe('tabs: the selected tab can be told apart', () => {
  it('marks only the selected trigger as active, and follows a change', () => {
    render(<Demo />)
    const rules = screen.getByRole('tab', { name: 'Rules' })
    const groups = screen.getByRole('tab', { name: 'Proxy groups' })
    expect(rules.hasAttribute('data-active')).toBe(true)
    expect(rules.getAttribute('aria-selected')).toBe('true')
    expect(groups.hasAttribute('data-active')).toBe(false)
    fireEvent.click(groups)
    expect(groups.hasAttribute('data-active')).toBe(true)
    expect(rules.hasAttribute('data-active')).toBe(false)
  })

  it('gives the active trigger the segmented control look: own surface, border ring, shadow, weight', () => {
    render(<Demo />)
    const cls = screen.getByRole('tab', { name: 'Rules' }).className
    for (const c of ['data-active:bg-segment-active', 'data-active:ring-1', 'data-active:ring-segment-border', 'data-active:shadow-sm', 'data-active:font-semibold', 'data-active:text-foreground']) {
      expect(cls).toContain(c)
    }
  })

  it('does not let hover paint an inactive tab with the active surface', () => {
    render(<Demo />)
    const cls = screen.getByRole('tab', { name: 'Proxy groups' }).className
    expect(cls).not.toMatch(/(^|\s)hover:bg-segment-active/)
    expect(cls).not.toMatch(/(^|\s)hover:ring/)
    expect(cls).toContain('not-data-active:hover:bg-')
  })

  it('draws the track with the same tint as the segmented control and needs no measured indicator', () => {
    render(<Demo />)
    expect(screen.getByRole('tablist').className).toContain('bg-segment-track')
    expect((tabsModule as Record<string, unknown>).TabsIndicator).toBeUndefined()
  })
})
