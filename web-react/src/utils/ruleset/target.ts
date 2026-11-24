/**
 * What the backend reads a part of a rule line as: `normalizeRulePart` (internal/service/render/proxy_groups.go), kept in
 * step on purpose. The part is trimmed; if it starts and ends with the same quote it is unwrapped: a `"` part as one CSV
 * field (a stray quote inside, or more than one field, is an error there and leaves the part as it was), a `'` part with
 * `''` read as `'`. The backend finds groups, counts them, matches targets and builds the subscription from this value, so
 * everything here that compares or groups targets compares THIS, never the text as written: `Proxy`, `'Proxy'` and
 * `"Proxy"` are one group.
 */
export function normalizeTarget(raw: string): string {
  const s = raw.trim()
  if (s.length >= 2 && s[0] === s[s.length - 1]) {
    const inner = s.slice(1, -1)
    if (s[0] === '"') {
      // A CSV field: every quote inside is doubled. Anything else is an error, and the part stays as written.
      if (/(^|[^"])("")*"([^"]|$)/.test(inner)) return s
      return inner.replace(/""/g, '"')
    }
    if (s[0] === "'") return inner.replace(/''/g, "'")
  }
  return s
}

/**
 * `name` written the way `field` is written: inside the same quotes if it had them (a rename keeps each line's style),
 * bare otherwise. Whitespace around the field is not part of it and is dropped; callers replace only the trimmed span.
 */
export function quoteLike(field: string, name: string): string {
  const s = field.trim()
  if (s.length >= 2 && s[0] === s[s.length - 1]) {
    if (s[0] === '"') return `"${name.replace(/"/g, '""')}"`
    if (s[0] === "'") return `'${name.replace(/'/g, "''")}'`
  }
  return name
}
