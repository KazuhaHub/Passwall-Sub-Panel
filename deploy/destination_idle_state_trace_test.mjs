import assert from 'node:assert/strict'
import { test } from 'node:test'
import { summarizeIdleStateWrites } from './destination-idle-state-trace.mjs'

// Synthetic parser fixtures only; not measurements of a running Node.
const state = '/owned/private-node/state.db'
const windows = [{ from_unix_us: 1791851000000000, until_unix_us: 1791851001000000 }, { from_unix_us: 1791851001000000, until_unix_us: 1791851002000000 }]
const line = (stamp, call) => `123 179185100${stamp} ${call}\n`

test('idle trace counts actual successful bytes only for the exact state and durable journal files', () => {
  const input = line('0.100000', `pwrite64(8<${state}>, ""..., 4096, 0) = 4096`)
    + line('0.200000', `write(9<${state}-wal>, ""..., 32) = 32`)
    + line('0.300000', `write(9<${state}-wal>, ""..., 4096) = 2048`)
    + line('0.400000', `fdatasync(9<${state}-wal>) = 0`)
    + line('0.500000', `write(10<${state}-journal>, ""..., 12) = 12`)
    + line('0.600000', 'write(1<pipe:[123]>, ""..., 6000) = 6000')
    + line('0.700000', `write(11<${state}.backup>, ""..., 500) = 500`)
    + line('0.800000', `write(12<${state}-shm>, ""..., 32) = 32`)
    + line('1.000000', `pwritev(8<${state}>, [{iov_base=""..., iov_len=16}], 1, 0) = 16`)
    + line('1.100000', `fsync(8<${state}>) = 0`)
  const result = summarizeIdleStateWrites(input, state, windows)
  assert.deepEqual(result, [
    { db_bytes: 4096, wal_bytes: 2080, journal_bytes: 12, write_calls: 4, sync_calls: 1 },
    { db_bytes: 16, wal_bytes: 0, journal_bytes: 0, write_calls: 1, sync_calls: 1 },
  ])
  assert(!JSON.stringify(result).includes('private-node'))
})

test('idle trace retains entry timestamps and thread identity for unfinished writes', () => {
  const input = line('0.990000', `pwrite64(8<${state}-wal>, ""..., 4096, 0 <unfinished ...>`)
    + '124 1791851001.000000 write(1<pipe:[1]>, ""..., 1) = 1\n'
    + '123 1791851001.010000 <... pwrite64 resumed>) = 4096\n'
  assert.equal(summarizeIdleStateWrites(input, state, windows)[0].wal_bytes, 4096)
  assert.equal(summarizeIdleStateWrites(input, state, windows)[1].wal_bytes, 0)
})

test('idle trace refuses ambiguous state writes, lost calls, private payload and invalid windows', () => {
  for (const input of [
    line('0.100000', `write(8<${state}>, "private", 7) = 7`),
    line('0.100000', 'pwrite64(8, ""..., 4096, 0) = 4096'),
    line('0.100000', `write(8<${state}>, ""..., 7) = -1 EIO (Input/output error)`),
    line('0.100000', `pwrite64(8<${state}>, ""..., 4096, 0 <unfinished ...>`),
    line('0.100000', '<... pwrite64 resumed>) = 4096'),
    line('0.100000', `mmap(NULL, 4096, PROT_READ|PROT_WRITE, MAP_SHARED, 8<${state}>, 0) = 0x7000`),
    line('0.100000', `write(8<${state} (deleted)>, ""..., 7) = 7`),
    line('0.100000', `pwrite64(8<${state}>, ""..., 4096, 0) = 9007199254740992`),
    `private unparsed state write ${state}\n`,
  ]) assert.throws(() => summarizeIdleStateWrites(input, state, windows), /private content withheld/)
  for (const bad of [[], [windows[1], windows[0]], [{ ...windows[0], until_unix_us: windows[0].from_unix_us }], [{ ...windows[0], private_path: state }]]) assert.throws(() => summarizeIdleStateWrites('', state, bad))
})
