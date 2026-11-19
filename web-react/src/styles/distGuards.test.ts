import { describe, expect, it } from 'vitest'
import { checkGlobalRules, checkPreloads, checkVendorImports } from '../../scripts/dist-guards.mjs'

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

describe('checkVendorImports', () => {
  it('flags a vendor chunk importing an app chunk', () => {
    expect(checkVendorImports({ 'vendor-ui-a.js': 'import{t as s}from"./SyncTasksView-b.js";' })).toHaveLength(1)
  })
  it('allows vendor and runtime imports', () => {
    expect(checkVendorImports({ 'vendor-ui-a.js': 'import{r}from"./rolldown-runtime-x.js";import{a}from"./vendor-react-y.js";' })).toEqual([])
  })
})

describe('checkGlobalRules', () => {
  it('flags a real property on * / html / body / :host, layered or not', () => {
    expect(checkGlobalRules('@layer base{*,:after,:before{margin:0}}')).toHaveLength(1)
    expect(checkGlobalRules('html{line-height:1.5}')).toHaveLength(1)
    expect(checkGlobalRules('@media (min-width:1px){body{color:red}}')).toHaveLength(1)
    expect(checkGlobalRules(':host{tab-size:4}')).toHaveLength(1)
  })
  it('allows custom properties on global selectors', () => {
    expect(checkGlobalRules(':root{--a:1;--b:2}')).toEqual([])
    expect(checkGlobalRules('@layer properties{*,:before{--tw-x:0;margin:0}}')).toEqual([])
  })
  it('allows scoped selectors and at-rules', () => {
    const css = '@layer base{:where(.psp-ui,[data-slot]) *{margin:0}}@keyframes a{from{opacity:0}to{opacity:1}}@font-face{font-family:x;src:url(a)}.psp-ui{margin:0}'
    expect(checkGlobalRules(css)).toEqual([])
  })
})
