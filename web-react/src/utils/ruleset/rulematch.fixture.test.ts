import { describe, expect, it } from 'vitest'
import fixture from './rulematch.fixture.json'
import { evalRule, prepareProbe, type ProbeInput } from './match'
import { parseLine } from './parse'

/**
 * The browser matcher against the file the Go matcher (internal/pkg/rulematch) is tested with. Each case is one rule line
 * and one connection; the expected answer is yes / no / unknown plus, for unknown, the reason.
 */
interface FixtureProbe { host: string; ip?: string; port?: number; network?: string; process?: string; src_ip?: string; src_port?: number }
interface Case { rule: string; probe: string; expect: 'yes' | 'no' | 'unknown'; reason?: string; note?: string; mihomo?: string }

const probes = fixture.probes as Record<string, FixtureProbe>
const cases = fixture.cases as Case[]

function inputOf(p: FixtureProbe): ProbeInput {
  return {
    address: p.host,
    port: p.port ? String(p.port) : '',
    network: p.network === 'udp' ? 'udp' : 'tcp',
    resolvedIp: p.ip ?? '',
    process: p.process ?? '',
    srcPort: p.src_port ? String(p.src_port) : '',
    srcIp: p.src_ip ?? '',
  }
}

describe('browser matcher agrees with the shared fixture', () => {
  it.each(cases.map(c => [`${c.rule}  @${c.probe}`, c] as const))('%s', (_name, c) => {
    const prepared = prepareProbe(inputOf(probes[c.probe]))
    expect(prepared.ok).toBe(true)
    if (!prepared.ok) return
    const line = parseLine(`- ${c.rule}`)
    expect(line.kind).toBe('rule')
    const verdict = evalRule(line.rule!, prepared.probe)
    expect(verdict.verdict).toBe(c.expect)
    if (c.expect === 'unknown' && verdict.verdict === 'unknown') expect(verdict.reason).toBe(c.reason)
  })
})
