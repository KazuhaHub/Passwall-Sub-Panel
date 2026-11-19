import { useCallback, useMemo, useRef } from 'react'
import { useLocation } from 'react-router'
import { useUrlWriter, type UrlHistory } from './urlBatch'

/**
 * How one value is spelled in the query string. `parse` must accept anything a
 * person could type into the address bar and fall back to the default instead
 * of throwing; `serialize` returns null for the default so a clean URL stays
 * clean (`/admin/risk`, not `/admin/risk?tab=queue&page=1`).
 */
export interface UrlCodec<T> {
  parse: (raw: string | null) => T
  serialize: (value: T) => string | null
}

export const urlCodecs = {
  string(def = ''): UrlCodec<string> {
    return { parse: raw => raw ?? def, serialize: v => (v === def ? null : v) }
  },
  int(def: number, { min = Number.MIN_SAFE_INTEGER, max = Number.MAX_SAFE_INTEGER }: { min?: number; max?: number } = {}): UrlCodec<number> {
    return {
      // A strict digits-only match: Number('7.5'), Number('07x') and Number('') all
      // produce something, and none of them is a page number somebody meant.
      parse: raw => {
        if (raw === null || !/^-?\d+$/.test(raw)) return def
        const n = Number(raw)
        return Number.isSafeInteger(n) && n >= min && n <= max ? n : def
      },
      serialize: v => (v === def ? null : String(v)),
    }
  },
  bool(def = false): UrlCodec<boolean> {
    return {
      parse: raw => (raw === null ? def : raw === '1' || raw === 'true'),
      serialize: v => (v === def ? null : v ? '1' : '0'),
    }
  },
  oneOf<V extends string>(values: readonly V[], def: V): UrlCodec<V> {
    return {
      parse: raw => (raw !== null && (values as readonly string[]).includes(raw) ? (raw as V) : def),
      serialize: v => (v === def ? null : v),
    }
  },
  /** A comma-joined list, for multi-select facets. Items may not contain commas. */
  list(): UrlCodec<string[]> {
    return {
      parse: raw => (raw ? raw.split(',').filter(Boolean) : []),
      serialize: v => (v.length ? v.join(',') : null),
    }
  },
}

export type { UrlHistory }
export interface UrlWriteOptions { history?: UrlHistory }

// `any`, not `unknown`: codecs are contravariant in the value they serialize, so a
// UrlCodec<'a' | 'b'> is not assignable to UrlCodec<unknown>.
type Schema = Record<string, UrlCodec<any>>
export type UrlValues<S extends Schema> = { [K in keyof S]: S[K] extends UrlCodec<infer T> ? T : never }

/**
 * Several typed query-string values behind one setter.
 *
 * Replace vs push is the whole point of the option: a tab or a filter is a
 * view of the page, so it REPLACES (Back leaves the page, not the last filter
 * keystroke); an opened record is a place, so it PUSHES (Back closes it). The
 * default is replace; pass `{ history: 'push' }` per hook or per call.
 *
 * A patch writes only the keys it names and leaves every other param alone
 * (another hook, a utm tag). Writes in the same tick, from this hook or any
 * other, compose into one navigation (see useUrlWriter).
 */
export function useUrlStates<S extends Schema>(
  schema: S,
  opts: UrlWriteOptions = {},
): [UrlValues<S>, (patch: Partial<UrlValues<S>>, o?: UrlWriteOptions) => void] {
  const { search } = useLocation()
  const write = useUrlWriter()

  // The schema is a literal at the call site, new on every render; read it
  // through a ref so the setter keeps its identity.
  const cfg = useRef({ schema, history: opts.history ?? 'replace' })
  cfg.current = { schema, history: opts.history ?? 'replace' }

  const values = useMemo(() => {
    const params = new URLSearchParams(search)
    const out: Record<string, unknown> = {}
    for (const [key, codec] of Object.entries(cfg.current.schema)) out[key] = codec.parse(params.get(key))
    return out as UrlValues<S>
    // `search` is the only input that can change what the schema parses.
  }, [search])

  const set = useCallback((patch: Partial<UrlValues<S>>, o: UrlWriteOptions = {}) => {
    const entries = Object.entries(patch)
    const schemaNow = cfg.current.schema
    write(params => {
      for (const [key, value] of entries) {
        const codec = schemaNow[key]
        if (!codec) continue
        const s = codec.serialize(value)
        if (s === null) params.delete(key)
        else params.set(key, s)
      }
    }, o.history ?? cfg.current.history)
  }, [write])

  return [values, set]
}

/** One typed query-string value; see useUrlStates for the replace-vs-push rule. */
export function useUrlState<T>(
  key: string,
  codec: UrlCodec<T>,
  opts: UrlWriteOptions = {},
): [T, (next: T | ((prev: T) => T), o?: UrlWriteOptions) => void] {
  const schema = useMemo(() => ({ [key]: codec }), [key, codec])
  const [values, patch] = useUrlStates(schema, opts)
  const current = values[key] as T
  const currentRef = useRef(current)
  currentRef.current = current
  const set = useCallback((next: T | ((prev: T) => T), o?: UrlWriteOptions) => {
    const value = typeof next === 'function' ? (next as (p: T) => T)(currentRef.current) : next
    currentRef.current = value
    patch({ [key]: value } as Partial<UrlValues<typeof schema>>, o)
  }, [key, patch])
  return [current, set]
}
