import { describe, expect, it } from 'vitest'
import { legacyTemplateTarget, newTemplatePath, templatePath } from './templateRoutes'

describe('template addresses', () => {
  it('puts the slug in the path and the source of a copy in the query', () => {
    expect(templatePath('default-mihomo')).toBe('/admin/templates/default-mihomo')
    expect(newTemplatePath()).toBe('/admin/templates/new')
    expect(newTemplatePath('a b')).toBe('/admin/templates/new?copy=a%20b')
  })
})

describe('legacyTemplateTarget', () => {
  it.each([
    ['?edit=custom', '/admin/templates/custom'],
    ['?q=x&edit=custom', '/admin/templates/custom'],
    ['?edit=new', '/admin/templates/new'],
    ['?edit=new&copy=custom', '/admin/templates/new?copy=custom'],
  ])('%s -> %s', (search, to) => expect(legacyTemplateTarget(search)).toBe(to))

  it.each(['', '?q=a', '?edit=', '?copy=custom'])('%s is not a link', search => {
    expect(legacyTemplateTarget(search)).toBeNull()
  })
})
