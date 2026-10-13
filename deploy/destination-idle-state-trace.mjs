import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

const require = value => { if (!value) throw new Error('Idle state write evidence invalid; private content withheld') }
const count = value => Number.isSafeInteger(value) && value >= 0
const calls = ['write', 'pwrite64', 'writev', 'pwritev', 'pwritev2', 'fsync', 'fdatasync', 'mmap', 'msync']

// Input is private strace -f -ttt -yy -s 0 output from the owned actual Node.
// Successful write return values are logical syscall bytes, not physical disk
// allocation. WAL/journal are separate; unrelated files and SQLite SHM do not
// become state.db bytes. Instrumented CPU must be reported separately.
export function summarizeIdleStateWrites(input, statePath, windows) {
  require(typeof input === 'string' && Buffer.byteLength(input) <= 64 * 1024 * 1024)
  require(typeof statePath === 'string' && /^\/(?:[^<>\r\n]+\/)state\.db$/.test(statePath))
  require(Array.isArray(windows) && windows.length > 0 && windows.length <= 20)
  let last = -1
  for (const window of windows) {
    require(window && Object.keys(window).sort().join() === 'from_unix_us,until_unix_us')
    require(count(window.from_unix_us) && count(window.until_unix_us) && window.from_unix_us < window.until_unix_us && window.from_unix_us >= last)
    last = window.until_unix_us
  }
  const result = windows.map(() => ({ db_bytes: 0, wal_bytes: 0, journal_bytes: 0, write_calls: 0, sync_calls: 0 }))
  const pending = new Map()
  const consume = (stamp, call) => {
    const name = call.match(/^([a-z0-9]+)\(/)?.[1]
    require(calls.includes(name))
    const fd = call.match(/^\w+\(\d+<([^>]+)>/)
    const path = fd?.[1]
    if (path?.startsWith(statePath) && path.includes(' (deleted)')) require(false)
    const field = path === statePath ? 'db_bytes' : path === statePath + '-wal' ? 'wal_bytes' : path === statePath + '-journal' ? 'journal_bytes' : undefined
    // A shared writable mapping could dirty state without a traced write.
    if (name === 'mmap') {
      if (call.includes(`<${statePath}>`) || call.includes(`<${statePath}-wal>`) || call.includes(`<${statePath}-journal>`)) require(!/PROT_WRITE/.test(call) || !/MAP_SHARED/.test(call))
      return
    }
    require(name !== 'msync')
    // Without -yy's descriptor identity a successful write might belong to
    // SQLite; refusing it is safer than reporting a fabricated zero.
    require(fd)
    if (!field) { require(!call.includes(statePath) || path === statePath + '-shm' || path?.startsWith(statePath + '.')); return }
    const returned = call.match(/\)\s+=\s+(-?\d+)(?:\s|$)/)
    require(returned && count(Number(returned[1])))
    const value = Number(returned[1])
    const window = windows.findIndex(w => stamp >= w.from_unix_us && stamp < w.until_unix_us)
    if (window < 0) return
    const output = result[window]
    if (name === 'fsync' || name === 'fdatasync') { require(value === 0); output.sync_calls++ }
    else { output[field] += value; output.write_calls++; require(count(output[field])) }
  }
  for (const line of input.split(/\r?\n/).filter(Boolean)) {
    require(Buffer.byteLength(line) <= 16 * 1024)
    // -s 0 omits buffers; never accept a dump of private written content.
    require(!/"(?:[^"\\]|\\.)+"/.test(line))
    const row = line.match(/^\s*(?:\[pid\s+(\d+)\]|(\d+))\s+(\d+)\.(\d{6})\s+(.+)$/)
    if (!row) { require(!line.includes(statePath) && /^(?:\+\+\+|---)/.test(line)); continue }
    const pid = row[1] ?? row[2], stamp = Number(row[3]) * 1000000 + Number(row[4]), call = row[5]
    require(count(stamp))
    const resumed = call.match(/^<\.\.\. (\w+) resumed>(.*)$/)
    if (resumed) {
      const started = pending.get(pid)
      require(started && started.name === resumed[1]); pending.delete(pid)
      consume(started.stamp, started.call + resumed[2]); continue
    }
    if (/^(?:\+\+\+|---)/.test(call)) { require(!pending.has(pid)); continue }
    const unfinished = call.match(/^(\w+)\((.*) <unfinished \.\.\.>$/)
    if (unfinished) {
      require(!pending.has(pid) && calls.includes(unfinished[1]))
      pending.set(pid, { name: unfinished[1], stamp, call: unfinished[1] + '(' + unfinished[2] }); continue
    }
    require(!pending.has(pid)); consume(stamp, call)
  }
  require(pending.size === 0)
  return result
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    require(process.argv.length === 5)
    process.stdout.write(JSON.stringify(summarizeIdleStateWrites(readFileSync(process.argv[2], 'utf8'), process.argv[3], JSON.parse(readFileSync(process.argv[4], 'utf8')))) + '\n')
  } catch {
    console.error('Idle state write evidence invalid; private content withheld')
    process.exitCode = 1
  }
}
