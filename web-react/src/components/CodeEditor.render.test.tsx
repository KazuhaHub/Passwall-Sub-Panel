// @vitest-environment jsdom
import { cleanup, render } from '@testing-library/react'
import { forceParsing } from '@codemirror/language'
import { EditorState } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import CodeEditor from './CodeEditor'
import { languageExtensions, type EditorLanguage } from './codeEditor/languages'
import { templatePlaceholders } from './codeEditor/placeholders'
import { vscodeTheme } from './codeEditor/theme'
import { DARK_PLUS, LIGHT_PLUS } from './codeEditor/vscodePalette'

// The real CodeMirror, in jsdom. It has no layout, so these only read the DOM it writes: which class each token got.
beforeAll(() => {
  const rect = { x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0, toJSON: () => ({}) }
  Range.prototype.getClientRects = () => ({ length: 0, item: () => null, [Symbol.iterator]: function* () {} }) as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => rect as DOMRect
})
afterEach(() => cleanup())

function mount(doc: string, language: EditorLanguage, dark = false) {
  const parent = document.body.appendChild(document.createElement('div'))
  const view = new EditorView({
    parent,
    state: EditorState.create({ doc, extensions: [...languageExtensions(language), templatePlaceholders, vscodeTheme(dark)] }),
  })
  // The parser works in time slices; on a busy machine the first one may not finish. Finish it, then let the view redraw.
  forceParsing(view, view.state.doc.length, 5000)
  view.dispatch({})
  return { view, dom: view.dom, destroy: () => { view.destroy(); parent.remove() } }
}
/** The text of every element carrying `cls`. */
const texts = (dom: HTMLElement, cls: string) => Array.from(dom.querySelectorAll(`.${cls}`)).map(e => e.textContent)

describe('token classes', () => {
  it('colours a YAML key, a quoted string and a comment', () => {
    const { dom, destroy } = mount('name: "hello"\n# note\nport: 8080\nok: true\n', 'yaml')
    expect(texts(dom, 'psp-tok-yaml-key')).toEqual(expect.arrayContaining(['name', 'port', 'ok']))
    expect(texts(dom, 'psp-tok-string')).toContain('"hello"')
    expect(texts(dom, 'psp-tok-comment')).toContain('# note')
    // The parser calls both plain scalars; the overlay tells a number and a constant apart.
    expect(texts(dom, 'psp-tok-scalar-number')).toContain('8080')
    expect(texts(dom, 'psp-tok-scalar-const')).toContain('true')
    // Any other plain value is a string, as in VS Code (the quoted one above is a string already).
    const plain = mount('k: some text\nblock: |\n  line one\n', 'yaml')
    expect(texts(plain.dom, 'psp-tok-string')).toEqual(expect.arrayContaining(['some text']))
    expect(texts(plain.dom, 'psp-tok-string').join('')).toContain('line one')
    plain.destroy()
    // A key is never drawn as a value.
    expect(texts(dom, 'psp-tok-scalar-number')).not.toContain('port')
    destroy()
  })

  it('draws anchors, aliases and tags in their own roles', () => {
    const { dom, destroy } = mount('a: &x !!str v\nb: *x\n', 'yaml')
    expect(texts(dom, 'psp-tok-anchor').join(' ')).toContain('&x')
    expect(texts(dom, 'psp-tok-anchor').join(' ')).toContain('*x')
    expect(texts(dom, 'psp-tok-tag').join(' ')).toContain('!!str')
    destroy()
  })

  it('tells a JSON key from a JSON value', () => {
    const { dom, destroy } = mount('{"a": "b", "n": 1, "t": true, "z": null}', 'json')
    expect(texts(dom, 'psp-tok-json-key')).toEqual(['"a"', '"n"', '"t"', '"z"'])
    expect(texts(dom, 'psp-tok-string')).toEqual(['"b"'])
    expect(texts(dom, 'psp-tok-number')).toEqual(['1'])
    expect(texts(dom, 'psp-tok-const')).toEqual(['true', 'null'])
    destroy()
  })

  it('colours the markers and the body of a PEM block, and headings of Markdown', () => {
    const pem = mount('-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----', 'pem')
    expect(texts(pem.dom, 'psp-tok-keyword')).toEqual(['-----BEGIN CERTIFICATE-----', '-----END CERTIFICATE-----'])
    expect(texts(pem.dom, 'psp-tok-string')).toEqual(['MIIB'])
    pem.destroy()
    const md = mount('# Terms\n\nSome **bold** and `code`.\n', 'markdown')
    expect(texts(md.dom, 'psp-tok-heading')).toEqual(['# Terms'])
    expect(texts(md.dom, 'psp-tok-strong')).toEqual(['**bold**'])
    expect(texts(md.dom, 'psp-tok-code')).toEqual(['`code`'])
    md.destroy()
  })

  it('leaves a plain list uncoloured, so domain:example.com is not a key', () => {
    const { dom, destroy } = mount('domain:example.com\n10.0.0.0/8\n', 'plain')
    expect(dom.querySelector('.psp-tok-yaml-key')).toBeNull()
    expect(dom.querySelector('[class*="psp-tok-"]')).toBeNull()
    destroy()
  })

  it.each(['yaml', 'json', 'markdown', 'plain'] as const)('marks a {{ placeholder }} in %s', language => {
    const { dom, destroy } = mount('x: {{ proxies }}\n', language)
    expect(texts(dom, 'psp-tok-placeholder')).toEqual(['{{ proxies }}'])
    destroy()
  })
})

describe('theme', () => {
  it('is dark for Dark+ and light for Light+, with VS Code backgrounds', () => {
    const dark = mount('a: 1', 'yaml', true)
    const light = mount('a: 1', 'yaml', false)
    expect(dark.dom.classList.contains('cm-focused')).toBe(false)
    // CodeMirror writes the dark flag into the editor's scope class pair (ͼo = dark base theme).
    expect(dark.view.state.facet(EditorView.darkTheme)).toBe(true)
    expect(light.view.state.facet(EditorView.darkTheme)).toBe(false)
    dark.destroy(); light.destroy()
    expect(DARK_PLUS.background).toBe('#1e1e1e')
    expect(LIGHT_PLUS.background).toBe('#ffffff')
    expect(DARK_PLUS.string).toBe('#ce9178')
    expect(LIGHT_PLUS.string).toBe('#a31515')
  })

  it('serves the same extension object per mode, so the editor is not reconfigured on every render', () => {
    expect(vscodeTheme(true)).toBe(vscodeTheme(true))
    expect(vscodeTheme(true)).not.toBe(vscodeTheme(false))
  })
})

describe('the component', () => {
  it('wires the label, description and state onto the text area and honours readOnly', () => {
    const { container, rerender } = render(<CodeEditor value="a: 1" onChange={() => {}} id="f" labelledBy="l" aria-describedby="h" aria-invalid language="yaml" />)
    const content = container.querySelector<HTMLElement>('.cm-content')!
    expect(content.id).toBe('f')
    expect(content.getAttribute('aria-labelledby')).toBe('l')
    expect(content.getAttribute('aria-describedby')).toBe('h')
    expect(content.getAttribute('aria-invalid')).toBe('true')
    expect(content.getAttribute('contenteditable')).toBe('true')
    expect(content.getAttribute('aria-readonly')).toBeNull()
    rerender(<CodeEditor value="a: 1" onChange={() => {}} readOnly language="yaml" />)
    const after = container.querySelector<HTMLElement>('.cm-content')!
    expect(after.getAttribute('aria-readonly')).toBe('true')
  })

  it('shows the placeholder, keeping its line breaks, while the text is empty', () => {
    const { container } = render(<CodeEditor value="" onChange={() => {}} language="plain" placeholder={'domain:a\nfull:b'} />)
    expect(container.querySelector('.cm-placeholder')?.textContent).toBe('domain:a\nfull:b')
  })
})
