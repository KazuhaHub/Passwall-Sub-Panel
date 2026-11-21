/**
 * The traffic pages' byte format: two decimals and a binary unit up to TB, and a bare "0" for zero.
 * One copy for the dashboard, the traffic page and the chart, which each carried their own and must
 * not drift apart (an admin compares the same figure on all three).
 */
export function bytesToHuman(n: number): string {
  if (n === 0) return '0'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) { v /= 1024; u++ }
  return `${v.toFixed(2)} ${units[u]}`
}
