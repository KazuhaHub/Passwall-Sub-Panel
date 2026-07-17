export const DEFAULT_ASYNC_CONCURRENCY = 8

/**
 * Promise.allSettled semantics with bounded concurrency and stable result
 * ordering. This prevents large admin selections from opening hundreds of
 * simultaneous HTTP requests while still reporting every individual failure.
 */
export async function allSettledLimited<T, R>(
  items: readonly T[],
  task: (item: T, index: number) => PromiseLike<R> | R,
  concurrency = DEFAULT_ASYNC_CONCURRENCY,
): Promise<PromiseSettledResult<R>[]> {
  if (!Number.isFinite(concurrency) || concurrency < 1) {
    throw new RangeError('concurrency must be a positive finite number')
  }
  if (items.length === 0) return []

  const results = new Array<PromiseSettledResult<R>>(items.length)
  let nextIndex = 0
  const worker = async () => {
    while (nextIndex < items.length) {
      const index = nextIndex++
      try {
        results[index] = { status: 'fulfilled', value: await task(items[index], index) }
      } catch (reason) {
        results[index] = { status: 'rejected', reason }
      }
    }
  }

  const workerCount = Math.min(Math.floor(concurrency), items.length)
  await Promise.all(Array.from({ length: workerCount }, worker))
  return results
}
