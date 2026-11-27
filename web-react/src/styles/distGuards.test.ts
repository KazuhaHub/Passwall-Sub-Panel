import { describe, expect, it } from 'vitest'
import { checkCoarseTarget, checkPreloads, checkVendorImports } from '../../scripts/dist-guards.mjs'

describe('checkPreloads when the entry itself needs the new stack', () => {
  const entry = 'import{a}from"./vendor-ui-y.js";import"./vendor-react-r.js";'
  it('flags a new-stack preload nothing in the entry imports', () => {
    const html = '<script type="module" src="./assets/index-a.js"></script><link rel="modulepreload" href="./assets/vendor-ui-y.js">'
    expect(checkPreloads(html, { 'index-a.js': 'import"./vendor-react-r.js";' })).toHaveLength(1)
  })
  it('accepts it once the entry statically imports it', () => {
    const html = '<script type="module" src="./assets/index-a.js"></script><link rel="modulepreload" href="./assets/vendor-ui-y.js">'
    expect(checkPreloads(html, { 'index-a.js': entry })).toEqual([])
  })
})

describe('checkPreloads follows the entry\'s static imports transitively', () => {
  const html = '<script type="module" src="./assets/index-a.js"></script>'
    + '<link rel="modulepreload" href="./assets/vendor-ui-y.js"><link rel="modulepreload" href="./assets/vendor-base-ui-z.js">'
  const files = {
    // The entry imports a shared (non-vendor) chunk, and that chunk is what imports the
    // new stack: Vite preloads the whole closure, so this is a legitimate preload.
    'index-a.js': 'import{a}from"./shell-b.js";import"./vendor-react-r.js";',
    'shell-b.js': 'import{c}from"./vendor-ui-y.js";',
    'vendor-ui-y.js': 'import{d}from"./vendor-base-ui-z.js";import{r}from"./rolldown-runtime-x.js";',
    'vendor-base-ui-z.js': '',
  }
  it('accepts a new-stack chunk reached through a shared chunk, and through another vendor chunk', () => {
    expect(checkPreloads(html, files)).toEqual([])
  })
  it('still flags one that nothing reachable from the entry imports', () => {
    expect(checkPreloads(html, { ...files, 'shell-b.js': 'export const c=1;', 'vendor-ui-y.js': '' })).toEqual([
      'index.html preloads vendor-ui-y.js, which the entry does not import',
      'index.html preloads vendor-base-ui-z.js, which the entry does not import',
    ])
  })
  it('does not follow a dynamic import: a lazily loaded chunk is not eager', () => {
    const lazy = { ...files, 'index-a.js': 'const m=()=>import("./shell-b.js");import"./vendor-react-r.js";' }
    expect(checkPreloads(html, lazy)).toHaveLength(2)
  })
  it('terminates on an import cycle', () => {
    expect(checkPreloads(html, { ...files, 'vendor-base-ui-z.js': 'import"./shell-b.js";' })).toEqual([])
  })
})

describe('checkPreloads', () => {
  it('flags a preloaded new-stack chunk', () => {
    const html = '<script type="module" src="./assets/index-a.js"></script><link rel="modulepreload" href="./assets/vendor-base-ui-x.js"><link rel="modulepreload" href="./assets/vendor-ui-y.js">'
    expect(checkPreloads(html)).toHaveLength(2)
  })
  it('accepts the existing vendor chunks', () => {
    const html = '<link rel="modulepreload" href="./assets/vendor-react-a.js"><link rel="modulepreload" href="./assets/vendor-echarts-b.js">'
    expect(checkPreloads(html)).toEqual([])
  })
})

describe('checkPreloads attribute order and renamed chunks', () => {
  it('matches href before rel', () => {
    expect(checkPreloads('<link href="./assets/vendor-ui-y.js" crossorigin rel="modulepreload">')).toHaveLength(1)
  })
  it('matches a de-duplicated name such as vendor-ui2', () => {
    expect(checkPreloads('<link rel="modulepreload" href="./assets/vendor-ui2-y.js">')).toHaveLength(1)
    expect(checkPreloads('<link rel="modulepreload" href="./assets/vendor-base-ui3-y.js">')).toHaveLength(1)
  })
  it('does not match unrelated chunks that merely start with ui', () => {
    expect(checkPreloads('<link rel="modulepreload" href="./assets/vendor-uikit-y.js">')).toEqual([])
  })
})

describe('checkVendorImports side-effect and dynamic imports', () => {
  it('flags a side-effect import of an app chunk', () => {
    expect(checkVendorImports({ 'vendor-ui-a.js': 'import"./index-x.js";' })).toHaveLength(1)
  })
  it('flags a dynamic import of an app chunk', () => {
    expect(checkVendorImports({ 'vendor-ui-a.js': 'const m=()=>import("./Page-x.js");' })).toHaveLength(1)
  })
})

describe('checkVendorImports', () => {
  it('flags a vendor chunk importing an app chunk', () => {
    expect(checkVendorImports({ 'vendor-ui-a.js': 'import{t as s}from"./SyncTasksView-b.js";' })).toHaveLength(1)
  })
  it('allows vendor and runtime imports', () => {
    expect(checkVendorImports({ 'vendor-ui-a.js': 'import{r}from"./rolldown-runtime-x.js";import{a}from"./vendor-react-y.js";' })).toEqual([])
  })
})


describe('checkCoarseTarget', () => {
  const built = '@media (pointer:coarse){.pointer-coarse\\:min-h-\\[2rem\\]{min-height:2rem}.pointer-coarse\\:min-w-\\[2rem\\]{min-width:2rem}}'
  it('accepts CSS that carries the coarse-pointer minimum size', () => {
    expect(checkCoarseTarget(`a{b:c}${built}d{e:f}`)).toEqual([])
  })
  it('flags a rule for the height alone, or for the width alone', () => {
    expect(checkCoarseTarget('@media (pointer:coarse){.pointer-coarse\\:min-h-\\[2rem\\]{min-height:2rem}}')).toHaveLength(1)
    expect(checkCoarseTarget('@media (pointer:coarse){.pointer-coarse\\:min-w-\\[2rem\\]{min-width:2rem}}')).toHaveLength(1)
  })
  it('flags CSS without it, or with it outside a coarse-pointer query', () => {
    expect(checkCoarseTarget('a{b:c}')).toHaveLength(1)
    expect(checkCoarseTarget('.pointer-coarse\\:min-h-\\[2rem\\]{min-height:2rem}')).toHaveLength(1)
  })
})
