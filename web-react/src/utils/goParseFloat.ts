// goParseFloat is Go's strconv.ParseFloat(s, 64): the float64 it returns, or
// undefined where it returns an error — ErrSyntax, or ErrRange for a value
// that overflows. It exists for the group-override editor, which has to say
// what a stored float override will do, and the server decides that with
// ParseFloat (floatField): an error means the override is skipped and the
// global value stays, anything else is applied.
//
// Number() is not that parser, in both directions. It takes " 2", "0x10" and
// "0b1", which Go refuses, and refuses "inf", "NaN", "1_5" and "0x1.8p1",
// which Go takes; and where Go reports 1e400 as out of range, Number()
// quietly answers Infinity. So this is a port of Go's own scanner
// (internal/strconv: special, readFloat, underscoreOK, atofHex), and
// goParseFloat.vectors.json — produced by ParseFloat, and checked against
// floatField by the sqlstore tests — is what holds the two together.
//
// The one place it leans on the JavaScript engine is a decimal's value,
// which both languages round to the nearest float64. A decimal with more
// than 20 significant digits is allowed to round differently in JavaScript;
// no ratio an admin types comes near that.
export function goParseFloat(s: string): number | undefined {
  const sp = special(s)
  if (sp !== undefined) return sp
  const f = readFloat(s)
  if (f === undefined) return undefined
  if (f.hex) return hexValue(f)
  // Rebuilt with the exponent Go actually used: it stops accumulating
  // exponent digits once past 10000, which differs from the literal
  // exponent only for absurd inputs — but then it differs the same way here.
  const v = Number(`${f.neg ? '-' : ''}${f.intDigits || '0'}.${f.fracDigits || '0'}e${f.exp}`)
  return Number.isFinite(v) ? v : undefined
}

const isDigit = (c: string) => c >= '0' && c <= '9'
// Go's lower() is c | 0x20, which folds only ASCII letters; toLowerCase()
// folds far more than that.
const isHexLetter = (c: string) => (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
const is = (c: string | undefined, lowerASCII: string) =>
  c !== undefined && (c === lowerASCII || c === lowerASCII.toUpperCase())

// special: "inf" and "infinity", optionally signed, and "nan", unsigned —
// ASCII case ignored. ParseFloat then refuses anything after the match, so
// "infin" and "+nan" are errors, not infinity and NaN.
function special(s: string): number | undefined {
  let sign = 1
  let body = s
  if (s[0] === '+' || s[0] === '-') {
    if (s[0] === '-') sign = -1
    body = s.slice(1)
    return /^(inf|infinity)$/i.test(body) ? sign * Infinity : undefined
  }
  if (/^(inf|infinity)$/i.test(body)) return Infinity
  if (/^nan$/i.test(body)) return NaN
  return undefined
}

interface Scanned {
  neg: boolean
  hex: boolean
  intDigits: string // mantissa digits before the point, underscores removed
  fracDigits: string // and after it
  exp: number // the decimal (e) or binary (p) exponent, as Go capped it
}

// readFloat is Go's readFloat for a whole string: ParseFloat refuses a string
// its scan does not consume to the end.
function readFloat(s: string): Scanned | undefined {
  let i = 0
  let neg = false
  if (s[i] === '+') i++
  else if (s[i] === '-') { i++; neg = true }

  let hex = false
  if (i + 2 < s.length && s[i] === '0' && is(s[i + 1], 'x')) { hex = true; i += 2 }

  let underscores = false
  let sawdot = false
  let intDigits = ''
  let fracDigits = ''
  for (; i < s.length; i++) {
    const c = s[i]
    if (c === '_') { underscores = true; continue }
    if (c === '.') {
      if (sawdot) break
      sawdot = true
      continue
    }
    if (isDigit(c) || (hex && isHexLetter(c))) {
      if (sawdot) fracDigits += c
      else intDigits += c
      continue
    }
    break
  }
  if (intDigits === '' && fracDigits === '') return undefined

  let exp = 0
  if (is(s[i], hex ? 'p' : 'e')) {
    i++
    let esign = 1
    if (s[i] === '+') i++
    else if (s[i] === '-') { i++; esign = -1 }
    if (i >= s.length || !isDigit(s[i])) return undefined
    let e = 0
    for (; i < s.length && (isDigit(s[i]) || s[i] === '_'); i++) {
      if (s[i] === '_') { underscores = true; continue }
      if (e < 10000) e = e * 10 + (s.charCodeAt(i) - 48)
    }
    exp = e * esign
  } else if (hex) {
    return undefined // a hex float must have its p exponent
  }

  if (underscores && !underscoreOK(s.slice(0, i))) return undefined
  if (i !== s.length) return undefined
  return { neg, hex, intDigits, fracDigits, exp }
}

// underscoreOK: an underscore only between two digits, or between the base
// prefix and a digit.
function underscoreOK(s: string): boolean {
  let saw = '^'
  let i = 0
  if (s[0] === '-' || s[0] === '+') s = s.slice(1)
  let hex = false
  if (s.length >= 2 && s[0] === '0' && (is(s[1], 'b') || is(s[1], 'o') || is(s[1], 'x'))) {
    i = 2
    saw = '0'
    hex = is(s[1], 'x')
  }
  for (; i < s.length; i++) {
    const c = s[i]
    if (isDigit(c) || (hex && isHexLetter(c))) { saw = '0'; continue }
    if (c === '_') {
      if (saw !== '0') return false
      saw = '_'
      continue
    }
    if (saw === '_') return false
    saw = '!'
  }
  return saw !== '_'
}

// hexValue rounds mantissa × 2^exp to a float64 exactly as atofHex does —
// to nearest, ties to even, with the subnormal range rounding at 2^-1074 —
// and reports an overflow as the ErrRange it is. It works on the exact
// integer mantissa and builds the IEEE bits itself, so no step can round a
// second time.
function hexValue(f: Scanned): number | undefined {
  let m = BigInt(`0x${f.intDigits}${f.fracDigits}`)
  const sign = f.neg ? -1 : 1
  if (m === 0n) return sign * 0
  let e = f.exp - 4 * f.fracDigits.length // value = m · 2^e
  const top = m.toString(2).length - 1 + e // floor(log2(value))
  if (top > 1023) return undefined
  // Below half the smallest subnormal: rounds to zero. Answered here so an
  // exponent like p-99999 never builds a 100000-bit shift.
  if (top < -1075) return sign * 0
  // The lowest bit a float64 keeps: 52 below the top for a normal number,
  // never below 2^-1074. Bring m to that bit, rounding what falls off.
  const lsb = Math.max(top - 52, -1074)
  if (lsb > e) {
    const shift = BigInt(lsb - e)
    const kept = m >> shift
    const rest = m - (kept << shift)
    const half = 1n << (shift - 1n)
    m = rest > half || (rest === half && (kept & 1n) === 1n) ? kept + 1n : kept
  } else {
    m <<= BigInt(e - lsb)
  }
  e = lsb
  // m · 2^e is now the float64: m in [2^52, 2^53] for a normal number (2^53
  // only after a carry), below 2^52 for a subnormal (2^52 if it rounded up
  // into the smallest normal).
  if (m === 1n << 53n) { m >>= 1n; e++ }
  if (m === 0n) return sign * 0
  let bits: bigint
  if (m < 1n << 52n) {
    bits = m // subnormal: e is -1074, the field is the mantissa itself
  } else {
    const biased = e + 52 + 1023
    if (biased >= 2047) return undefined
    bits = (BigInt(biased) << 52n) | (m - (1n << 52n))
  }
  const view = new DataView(new ArrayBuffer(8))
  view.setBigUint64(0, bits)
  return sign * view.getFloat64(0)
}
