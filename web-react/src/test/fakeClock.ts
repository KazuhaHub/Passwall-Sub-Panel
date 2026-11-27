// A deterministic clock with timers, for the toast store: nothing runs until advance().
export function createFakeClock() {
  let t = 0
  let nextId = 1
  const timers = new Map<number, { at: number; fn: () => void }>()
  return {
    now: () => t,
    setTimer: (fn: () => void, ms: number) => { const id = nextId++; timers.set(id, { at: t + ms, fn }); return id },
    clearTimer: (id: number) => { timers.delete(id) },
    pending: () => timers.size,
    /** Moves time forward, firing every timer that falls due, in order, at its own instant. */
    advance(ms: number) {
      const end = t + ms
      for (;;) {
        let due: [number, { at: number; fn: () => void }] | undefined
        for (const e of timers) if (e[1].at <= end && (!due || e[1].at < due[1].at)) due = e
        if (!due) break
        timers.delete(due[0])
        t = due[1].at
        due[1].fn()
      }
      t = end
    },
  }
}
