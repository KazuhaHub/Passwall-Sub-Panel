import { describe, expect, it } from 'vitest'
import { checkPreloads, checkVendorImports } from '../../scripts/dist-guards.mjs'

describe('checkPreloads', () => {
  it('flags a preloaded new-stack chunk', () => {
    const html = '<script type="module" src="./assets/index-a.js"></script><link rel="modulepreload" href="./assets/vendor-base-ui-x.js"><link rel="modulepreload" href="./assets/vendor-ui-y.js">'
    expect(checkPreloads(html)).toHaveLength(2)
  })
  it('accepts the existing vendor chunks', () => {
    const html = '<link rel="modulepreload" href="./assets/vendor-react-a.js"><link rel="modulepreload" href="./assets/vendor-mui-b.js">'
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

