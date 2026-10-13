import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

const require = (value, code = 1) => {
  if (!value) throw Object.assign(new Error('Idle state write evidence invalid; private content withheld'), { code })
}
export const idleTraceFailureDiagnostic = error => ({ schema_version: 1, error_code: Number.isInteger(error?.code) && error.code >= 1 && error.code <= 15 ? error.code : 1 })
const count = value => Number.isSafeInteger(value) && value >= 0
const calls = ['write', 'pwrite64', 'writev', 'pwritev', 'pwritev2', 'fsync', 'fdatasync', 'mmap', 'msync', 'restart_syscall']

// Input is private strace -f -ttt -yy -s 0 output from the owned actual Node.
// Successful write return values are logical syscall bytes, not physical disk
// allocation. WAL/journal are separate; unrelated files and SQLite SHM do not
// become state.db bytes. Instrumented CPU must be reported separately.
export function summarizeIdleStateWrites(input, statePath, windows) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 64 * 1024 * 1024)
  require(typeof statePath === 'string' && /^\/(?:[^<>\r\n]+\/)state\.db$/.test(statePath), 2)
  require(Array.isArray(windows) && windows.length > 0 && windows.length <= 20, 3)
  let last = -1
  for (const window of windows) {
    require(window && Object.keys(window).sort().join() === 'from_unix_us,until_unix_us', 3)
    require(count(window.from_unix_us) && count(window.until_unix_us) && window.from_unix_us < window.until_unix_us && window.from_unix_us >= last, 3)
    last = window.until_unix_us
  }
  const result = windows.map(() => ({ db_bytes: 0, wal_bytes: 0, journal_bytes: 0, write_calls: 0, sync_calls: 0 }))
  const pending = new Map()
  const consume = (stamp, call) => {
    const name = call.match(/^([a-z0-9_]+)\(/)?.[1]
    require(calls.includes(name), 13)
    // Attaching a tracer can stop/resume kernel waits. Only closed, known
    // non-writing wait restarts contribute no SQLite bytes. Ambiguous or
    // writing restarts remain failures, including incomplete thread pairs.
    if (name === 'restart_syscall') {
      require(!call.includes(statePath) && /^restart_syscall\(<\.\.\. resuming interrupted (?:futex|poll|nanosleep|clock_nanosleep) \.\.\.>\)\s+=\s+-?\d+(?:\s+[A-Z][A-Z0-9_]*(?: \([^"\r\n]*\))?)?$/.test(call), 13)
      return
    }
    const fd = call.match(/^\w+\(\d+<([^>]+)>/)
    const path = fd?.[1]
    if (path?.startsWith(statePath) && path.includes(' (deleted)')) require(false, 9)
    const field = path === statePath ? 'db_bytes' : path === statePath + '-wal' ? 'wal_bytes' : path === statePath + '-journal' ? 'journal_bytes' : undefined
    // A shared writable mapping could dirty state without a traced write.
    if (name === 'mmap') {
      if (call.includes(`<${statePath}>`) || call.includes(`<${statePath}-wal>`) || call.includes(`<${statePath}-journal>`)) require(!/PROT_WRITE/.test(call) || !/MAP_SHARED/.test(call), 10)
      return
    }
    require(name !== 'msync', 11)
    // Without -yy's descriptor identity a successful write might belong to
    // SQLite; refusing it is safer than reporting a fabricated zero.
    require(fd, 8)
    if (!field) { require(!call.includes(statePath) || path === statePath + '-shm' || path?.startsWith(statePath + '.'), 9); return }
    const returned = call.match(/\)\s+=\s+(-?\d+)(?:\s|$)/)
    require(returned && count(Number(returned[1])), 12)
    const value = Number(returned[1])
    const window = windows.findIndex(w => stamp >= w.from_unix_us && stamp < w.until_unix_us)
    if (window < 0) return
    const output = result[window]
    if (name === 'fsync' || name === 'fdatasync') { require(value === 0, 12); output.sync_calls++ }
    else { output[field] += value; output.write_calls++; require(count(output[field]), 14) }
  }
  for (const line of input.split(/\r?\n/).filter(Boolean)) {
    require(Buffer.byteLength(line) <= 16 * 1024)
    // -s 0 omits buffers; never accept a dump of private written content.
    require(!/"(?:[^"\\]|\\.)+"/.test(line), 5)
    const row = line.match(/^\s*(?:\[pid\s+(\d+)\]|(\d+))\s+(\d+)\.(\d{6})\s+(.+)$/)
    if (!row) { require(!line.includes(statePath) && /^(?:\+\+\+|---)/.test(line), 6); continue }
    const pid = row[1] ?? row[2], stamp = Number(row[3]) * 1000000 + Number(row[4]), call = row[5]
    require(count(stamp), 4)
    const resumed = call.match(/^<\.\.\. (\w+) resumed>(.*)$/)
    if (resumed) {
      const started = pending.get(pid)
      require(started && started.name === resumed[1], 7); pending.delete(pid)
      consume(started.stamp, started.call + resumed[2]); continue
    }
    if (/^(?:\+\+\+|---)/.test(call)) { require(!pending.has(pid), 7); continue }
    const unfinished = call.match(/^(\w+)\((.*) <unfinished \.\.\.>$/)
    if (unfinished) {
      require(!pending.has(pid) && calls.includes(unfinished[1]), 15)
      pending.set(pid, { name: unfinished[1], stamp, call: unfinished[1] + '(' + unfinished[2] }); continue
    }
    require(!pending.has(pid), 15); consume(stamp, call)
  }
  require(pending.size === 0, 7)
  return result
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 5)
    process.stdout.write(JSON.stringify(summarizeIdleStateWrites(readFileSync(process.argv[2], 'utf8'), process.argv[3], JSON.parse(readFileSync(process.argv[4], 'utf8')))) + '\n')
  } catch (error) {
    console.error(JSON.stringify(idleTraceFailureDiagnostic(error)))
    process.exitCode = 1
  }
}
