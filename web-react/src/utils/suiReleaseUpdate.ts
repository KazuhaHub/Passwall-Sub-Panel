import { gt, prerelease, valid } from 'semver'

/** Compare shared upstream metadata with the panel's last observed identity. */
export function newerSUIRelease(current: string | undefined, target: string | null): string | undefined {
  if (!current || !target) return undefined
  const observed = current.trim().replace(/^v/, '')
  const release = target.replace(/^v/, '')
  // Do not coerce dev/unknown/malformed observations into an upgrade hint.
  // Only stable upstream releases are advertised; capabilities remain separate.
  if (valid(observed) !== observed || valid(release) !== release || prerelease(release) !== null) return undefined
  return gt(release, observed) ? target : undefined
}
