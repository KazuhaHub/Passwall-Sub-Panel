// Node 26 exposes a configurable global localStorage accessor whose value is
// undefined unless --localstorage-file is supplied. That pre-existing name
// shadows jsdom's storage when Vitest installs the DOM environment. Give each
// isolated test worker a real in-memory Storage instead of depending on a
// process-wide experimental file.
if (typeof window !== 'undefined') {
  const values = new Map<string, string>()
  const storage: Storage = {
    get length() { return values.size },
    clear() { values.clear() },
    getItem(key) { return values.get(String(key)) ?? null },
    key(index) { return [...values.keys()][index] ?? null },
    removeItem(key) { values.delete(String(key)) },
    setItem(key, value) { values.set(String(key), String(value)) },
  }
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: storage,
  })
}

// The code editor is CodeMirror, which has no layout in jsdom, and a page test is about the page's values, not the colours.
// Every page that edits source text goes through LazyCodeEditor, so it is replaced here by a text area with the same
// accessibility wiring. The real editor is tested in components/CodeEditor*.test.tsx.
import { vi } from 'vitest'
vi.mock('@/components/LazyCodeEditor', async () => (await import('./codeEditorStub')).lazyCodeEditorMock())
