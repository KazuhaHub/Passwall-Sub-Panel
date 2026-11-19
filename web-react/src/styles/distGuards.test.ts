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

describe('checkGlobalRules bare selectors and class-qualified roots', () => {
  it('flags bare element selectors', () => {
    expect(checkGlobalRules('a{color:red}')).toHaveLength(1)
    expect(checkGlobalRules('svg{display:block}')).toHaveLength(1)
    expect(checkGlobalRules('h1,h2{font-size:1em}')).toHaveLength(1)
    expect(checkGlobalRules('table td{padding:0}')).toHaveLength(1)
  })
  it('flags real properties on a class-qualified root', () => {
    expect(checkGlobalRules(':root.dark{color-scheme:dark}')).toHaveLength(1)
    expect(checkGlobalRules('html[data-x]{margin:0}')).toHaveLength(1)
  })
  it('still allows custom properties on a class-qualified root', () => {
    expect(checkGlobalRules(':root.dark[data-brand="blue"]{--brand:#fff}')).toEqual([])
  })
  it('does not split a selector on commas inside :where() / :is() or escaped commas', () => {
    expect(checkGlobalRules(':where(h1,h2):where(.psp-ui,[data-slot]){font-size:inherit}')).toEqual([])
    expect(checkGlobalRules('.max-h-\\[min\\(18rem\\,4rem\\)\\]{max-height:1px}')).toEqual([])
  })
  it('is not confused by a preceding @layer statement', () => {
    expect(checkGlobalRules('@layer a,b;@layer b{html{margin:0}}')[0]).toContain('in @layer b')
  })
  it('allows class-scoped element selectors', () => {
    expect(checkGlobalRules('.psp-ui a{color:red}.x svg{display:block}')).toEqual([])
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
