import { coerce, gte } from 'semver'

const MLKEM_FIRST_VERSION = '26.9.8'

/** Whether the observed Xray core requires X25519MLKEM768 as the first key share. */
export function xrayRequiresMLKEMFirst(version?: string): boolean {
  if (!version?.trim()) return false
  const parsed = coerce(version)
  return parsed !== null && gte(parsed, MLKEM_FIRST_VERSION)
}
