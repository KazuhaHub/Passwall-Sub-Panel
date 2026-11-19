// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'

import { eventOrigin, isCodeEditorTarget, isTypingTarget } from './typing'

const el = (html: string) => { const d = document.createElement('div'); d.innerHTML = html; document.body.appendChild(d); return d.firstElementChild as HTMLElement }

describe('isTypingTarget', () => {
  it.each(['<input>', '<input type="text">', '<input type="search">', '<input type="password">', '<input type="number">', '<textarea></textarea>',
    '<div contenteditable="true"><span id="in">x</span></div>', '<div role="textbox" tabindex="0"></div>', '<div class="cm-editor"><div></div></div>'])('%s is typing', html => {
    const target = el(html)
    expect(isTypingTarget(target.querySelector('span,div') ?? target)).toBe(true)
  })

  it.each(['<input type="checkbox">', '<input type="radio">', '<input type="button">', '<input type="range">', '<input type="color">', '<input type="file">', '<select></select>', '<button>b</button>'])('%s is not typing', html => {
    expect(isTypingTarget(el(html))).toBe(false)
  })

  it('does not count a read-only or disabled field: it takes no keystrokes', () => {
    expect(isTypingTarget(el('<input readonly>'))).toBe(false)
    expect(isTypingTarget(el('<input disabled>'))).toBe(false)
    expect(isTypingTarget(el('<textarea readonly></textarea>'))).toBe(false)
  })

  it('does not count contenteditable="false"', () => {
    expect(isTypingTarget(el('<div contenteditable="false"></div>'))).toBe(false)
  })

  it('knows the code editor from a plain field', () => {
    expect(isCodeEditorTarget(el('<div class="cm-editor"></div>'))).toBe(true)
    expect(isCodeEditorTarget(el('<input>'))).toBe(false)
  })

  it('finds a field inside a shadow root through the composed path', () => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const input = document.createElement('input')
    host.attachShadow({ mode: 'open' }).appendChild(input)
    let origin: EventTarget | null = null
    document.addEventListener('keydown', e => { origin = eventOrigin(e) }, { once: true })
    input.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, composed: true }))
    expect(origin).toBe(input)
    expect(isTypingTarget(origin)).toBe(true)
  })
})
