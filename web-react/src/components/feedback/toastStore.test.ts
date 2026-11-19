import { describe, expect, it } from 'vitest'

import { createFakeClock } from '@/test/fakeClock'
import { createToastStore, type ToastStore } from './toastStore'

const DURATION = 4000

function setup(over: Partial<Parameters<typeof createToastStore>[0]> = {}) {
  const clock = createFakeClock()
  const announced: { message: string; severity: string }[] = []
  const store = createToastStore({ ...clock, onAnnounce: m => announced.push(m), ...over })
  store.setActive(true)
  return { clock, store, announced }
}

const messages = (s: ToastStore) => s.getState().visible.map(v => v.message)

// Samples the store every `step` ms and records, per message, when it entered and left.
function watch(clock: ReturnType<typeof createFakeClock>, store: ToastStore) {
  const first = new Map<string, number>()
  const last = new Map<string, number>()
  let maxVisible = 0
  return {
    sample() {
      const v = store.getState().visible
      maxVisible = Math.max(maxVisible, v.length)
      for (const t of v) { if (!first.has(t.message)) first.set(t.message, clock.now()); last.set(t.message, clock.now()) }
    },
    first, last,
    get maxVisible() { return maxVisible },
    run(total: number, step = 50) {
      for (let t = 0; t < total; t += step) { this.sample(); clock.advance(step) }
      this.sample()
    },
  }
}

describe('the visible cap and the queue', () => {
  it('shows three of eight pushed in one tick and queues the rest', () => {
    const { store } = setup()
    for (let i = 1; i <= 8; i++) store.push({ message: `e${i}`, severity: 'error' })
    expect(messages(store)).toEqual(['e1', 'e2', 'e3'])
    expect(store.getState().queued).toBe(5)
  })

  it('gives every one of eight pushed in one tick its full duration, never more than three at once', () => {
    const { clock, store } = setup()
    const w = watch(clock, store)
    for (let i = 1; i <= 8; i++) store.push({ message: `e${i}`, severity: 'error' })
    w.run(DURATION * 4)
    expect([...w.first.keys()]).toHaveLength(8)
    for (const [m, first] of w.first) expect(w.last.get(m)! - first + 50, m).toBeGreaterThanOrEqual(DURATION)
    expect(w.maxVisible).toBe(3)
    expect(store.getState().visible).toHaveLength(0)
    expect(store.getState().queued).toBe(0)
  })

  it('shows eight pushed over six seconds, each for its full duration', () => {
    const { clock, store } = setup()
    const w = watch(clock, store)
    for (let i = 1; i <= 8; i++) { store.push({ message: `m${i}`, severity: 'error' }); w.run(750) }
    w.run(DURATION * 3)
    expect([...w.first.keys()]).toHaveLength(8)
    for (const [m, first] of w.first) expect(w.last.get(m)! - first + 50, m).toBeGreaterThanOrEqual(DURATION)
    expect(w.maxVisible).toBeLessThanOrEqual(3)
  })

  it('starts a queued toast’s countdown when it enters, not when it was pushed', () => {
    const { clock, store } = setup()
    for (const m of ['a', 'b', 'c', 'd']) store.push({ message: m })
    clock.advance(DURATION - 1)
    expect(messages(store)).toEqual(['a', 'b', 'c'])
    clock.advance(1)
    expect(messages(store)).toEqual(['d'])
    clock.advance(DURATION - 1)
    expect(messages(store)).toEqual(['d'])
    clock.advance(1)
    expect(messages(store)).toEqual([])
  })

  it('releases the next toast the moment the user dismisses one', () => {
    const { store } = setup()
    for (const m of ['a', 'b', 'c', 'd']) store.push({ message: m })
    store.dismiss(store.getState().visible[1].id)
    expect(messages(store)).toEqual(['a', 'c', 'd'])
  })

  it('can dismiss a queued toast too', () => {
    const { store } = setup()
    for (const m of ['a', 'b', 'c', 'd']) store.push({ message: m })
    store.dismiss(store.getState().visible[0].id)
    expect(store.getState().queued).toBe(0)
    expect(messages(store)).toEqual(['b', 'c', 'd'])
  })
})

describe('pausing', () => {
  it('freezes every countdown while the tab is hidden and resumes from what was left', () => {
    const { clock, store } = setup()
    const w = watch(clock, store)
    for (let i = 1; i <= 8; i++) store.push({ message: `e${i}`, severity: 'error' })
    clock.advance(1000)
    store.setPaused('hidden', true)
    clock.advance(60_000)
    expect(messages(store)).toEqual(['e1', 'e2', 'e3'])
    store.setPaused('hidden', false)
    clock.advance(2999)
    expect(messages(store)).toEqual(['e1', 'e2', 'e3'])
    clock.advance(1)
    expect(messages(store)).toEqual(['e4', 'e5', 'e6'])
    // Everything after it still gets the full time.
    w.run(DURATION * 3)
    for (const m of ['e4', 'e5', 'e6', 'e7', 'e8']) expect(w.last.get(m)! - w.first.get(m)! + 50).toBeGreaterThanOrEqual(DURATION)
  })

  it('pauses on hover and on focus together, and runs again only when both have gone', () => {
    const { clock, store } = setup()
    store.push({ message: 'a' })
    clock.advance(1500)
    store.setPaused('hover', true)
    clock.advance(10_000)
    store.setPaused('focus', true)
    clock.advance(10_000)
    store.setPaused('hover', false)
    clock.advance(10_000)
    expect(messages(store)).toEqual(['a'])
    store.setPaused('focus', false)
    clock.advance(2499)
    expect(messages(store)).toEqual(['a'])
    clock.advance(1)
    expect(messages(store)).toEqual([])
  })

  it('does not start a queued toast’s clock while paused, and starts it on resume', () => {
    const { clock, store } = setup()
    for (const m of ['a', 'b', 'c', 'd']) store.push({ message: m })
    clock.advance(1000)
    store.setPaused('hover', true)
    store.dismiss(store.getState().visible[0].id)
    expect(messages(store)).toEqual(['b', 'c', 'd'])
    clock.advance(60_000)
    expect(messages(store)).toEqual(['b', 'c', 'd'])
    store.setPaused('hover', false)
    clock.advance(DURATION)
    expect(messages(store)).toEqual([])
  })

  it('keeps everything frozen while no host is attached', () => {
    const { clock, store } = setup()
    store.push({ message: 'a' })
    store.setActive(false)
    clock.advance(60_000)
    expect(messages(store)).toEqual(['a'])
    store.setActive(true)
    clock.advance(DURATION)
    expect(messages(store)).toEqual([])
  })
})

describe('de-duplication', () => {
  it('restarts a visible toast’s countdown and counts it', () => {
    const { clock, store } = setup()
    store.push({ message: 'a', severity: 'error' })
    clock.advance(3000)
    store.push({ message: 'a', severity: 'error' })
    store.push({ message: 'a', severity: 'error' })
    expect(store.getState().visible).toHaveLength(1)
    expect(store.getState().visible[0].count).toBe(3)
    clock.advance(3999)
    expect(messages(store)).toEqual(['a'])
    clock.advance(1)
    expect(messages(store)).toEqual([])
  })

  it('ignores a duplicate of a queued toast', () => {
    const { store } = setup()
    for (const m of ['a', 'b', 'c', 'd']) store.push({ message: m })
    store.push({ message: 'd' })
    expect(store.getState().queued).toBe(1)
  })

  it('treats the same words at another severity as another toast', () => {
    const { store } = setup()
    store.push({ message: 'x', severity: 'info' })
    store.push({ message: 'x', severity: 'error' })
    expect(store.getState().visible).toHaveLength(2)
  })

  it('uses an explicit key in place of severity + message', () => {
    const { store } = setup()
    store.push({ message: 'queued 1 task', key: 'sync' })
    store.push({ message: 'queued 2 tasks', key: 'sync' })
    expect(store.getState().visible).toHaveLength(1)
    expect(store.getState().visible[0].count).toBe(2)
  })

  it('makes a duplicate that arrives the instant the original left a new toast', () => {
    const { clock, store } = setup()
    store.push({ message: 'a' })
    clock.advance(DURATION)
    expect(messages(store)).toEqual([])
    store.push({ message: 'a' })
    expect(store.getState().visible).toHaveLength(1)
    expect(store.getState().visible[0].count).toBe(1)
    clock.advance(DURATION - 1)
    expect(messages(store)).toEqual(['a'])
  })

  it('treats a duplicate that arrives just before the original expires as a refresh', () => {
    const { clock, store } = setup()
    store.push({ message: 'a' })
    clock.advance(DURATION - 1)
    store.push({ message: 'a' })
    clock.advance(DURATION - 1)
    expect(messages(store)).toEqual(['a'])
  })

  it('never lets a toast that keeps being refreshed bring a fourth onto the screen', () => {
    const { clock, store } = setup()
    const w = watch(clock, store)
    store.push({ message: 'keep' })
    store.push({ message: 'b' })
    store.push({ message: 'c' })
    for (let i = 0; i < 40; i++) {
      clock.advance(1000)
      store.push({ message: 'keep' })
      store.push({ message: `n${i}` })
      w.sample()
    }
    expect(w.maxVisible).toBe(3)
    expect(store.getState().visible.some(v => v.message === 'keep')).toBe(true)
  })
})

describe('a bounded backlog', () => {
  it('collapses 200 pushed in one tick into three toasts and one summary, with all 200 in the log', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 200; i++) store.push({ message: `err ${i}`, severity: 'error' })
    const s = store.getState()
    expect(s.visible).toHaveLength(3)
    expect(s.queued).toBe(1)
    expect(s.log).toHaveLength(200)
    expect(s.log.filter(l => l.state === 'collapsed')).toHaveLength(197)
    // The summary arrives as soon as a slot frees, and the queue is drained.
    clock.advance(DURATION)
    const after = store.getState()
    expect(after.queued).toBe(0)
    expect(after.visible).toHaveLength(1)
    expect(after.visible[0]).toMatchObject({ kind: 'summary', pending: 197, severity: 'error' })
  })

  it('never shows more than three at once while collapsing and draining', () => {
    const { clock, store } = setup()
    const w = watch(clock, store)
    for (let i = 1; i <= 200; i++) store.push({ message: `err ${i}`, severity: 'error' })
    w.run(DURATION * 6)
    expect(w.maxVisible).toBeLessThanOrEqual(3)
    expect(store.getState().queued).toBe(0)
  })

  it('lets a late push join a summary that is still waiting', () => {
    const { store } = setup()
    for (let i = 1; i <= 10; i++) store.push({ message: `m${i}` })
    expect(store.getState().queued).toBe(1)
    store.push({ message: 'late' })
    expect(store.getState().queued).toBe(1)
    expect(store.getState().log.find(l => l.message === 'late')?.state).toBe('collapsed')
  })

  it('shows the summary at the worst severity it covers', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 3; i++) store.push({ message: `e${i}`, severity: 'error' })
    for (let i = 1; i <= 6; i++) store.push({ message: `w${i}`, severity: i === 4 ? 'error' : 'warning' })
    clock.advance(DURATION)
    expect(store.getState().visible[0]).toMatchObject({ kind: 'summary', severity: 'error', pending: 6, errors: 1, warnings: 5 })
  })

  it('keeps an info-only summary on screen a little longer than an ordinary toast, then lets it go', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 10; i++) store.push({ message: `m${i}` })
    clock.advance(DURATION)
    clock.advance(DURATION + 1)
    expect(store.getState().visible[0]?.kind).toBe('summary')
    clock.advance(DURATION * 2)
    expect(store.getState().visible).toHaveLength(0)
  })

  it('keeps the last 50 ordinary messages, and every collapsed one until the log is acknowledged', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 80; i++) { store.push({ message: `old ${i}` }); clock.advance(DURATION) }
    expect(store.getState().log).toHaveLength(50)
    expect(store.getState().log[49].message).toBe('old 80')
    for (let i = 1; i <= 100; i++) store.push({ message: `burst ${i}` })
    expect(store.getState().log.filter(l => l.state === 'collapsed').length).toBeGreaterThan(80)
    const keptBefore = store.getState().log.length
    store.acknowledgeLog()
    expect(store.getState().log.length).toBeLessThan(keptBefore)
    expect(store.getState().log.length).toBeLessThanOrEqual(50)
  })
})

describe('announcements', () => {
  it('announces every new message once, queued and collapsed ones included, and no duplicate of a visible one', () => {
    const { store, announced } = setup()
    for (let i = 1; i <= 20; i++) store.push({ message: `e${i}`, severity: 'error' })
    store.push({ message: 'e1', severity: 'error' })
    expect(announced).toHaveLength(20)
    expect(new Set(announced.map(a => a.message)).size).toBe(20)
  })
})

describe('subscription', () => {
  it('notifies on every change and hands out a stable snapshot between changes', () => {
    const { store } = setup()
    let calls = 0
    const off = store.subscribe(() => { calls++ })
    const before = store.getState()
    expect(store.getState()).toBe(before)
    store.push({ message: 'a' })
    expect(calls).toBeGreaterThan(0)
    expect(store.getState()).not.toBe(before)
    off()
  })
})

describe('severity decides who gets a slot', () => {
  it('lets an arriving error pre-empt a visible info toast, which goes to the log as unseen', () => {
    const { store } = setup()
    for (const m of ['i1', 'i2', 'i3']) store.push({ message: m })
    store.push({ message: 'boom', severity: 'error' })
    expect(messages(store)).not.toContain('i1')
    expect(messages(store)).toContain('boom')
    expect(messages(store)).toHaveLength(3)
    expect(store.getState().queued).toBe(0)
    const s = store.getState()
    expect(s.unread).toBe(1)
    expect(s.log.filter(l => l.state === 'collapsed').map(l => l.message)).toEqual(['i1'])
  })

  it('lets a warning pre-empt a success, but never one error or warning another', () => {
    const { store } = setup()
    for (const m of ['s1', 's2']) store.push({ message: m, severity: 'success' })
    store.push({ message: 'e1', severity: 'error' })
    store.push({ message: 'w1', severity: 'warning' })
    expect(messages(store)).toContain('w1')
    expect(messages(store)).toHaveLength(3)
    store.push({ message: 'e2', severity: 'error' })
    store.push({ message: 'e3', severity: 'error' })
    expect(messages(store).filter(m => m.startsWith('s'))).toHaveLength(0)
    store.push({ message: 'e4', severity: 'error' })
    // The slots are full of errors and warnings: e3 and e4 wait, they do not evict one.
    expect(messages(store)).toEqual(['e1', 'w1', 'e2'])
    expect(store.getState().queued).toBe(2)
  })

  it('shows three errors at once when 3 successes were up and 6 errors arrive, and the other three wait their turn', () => {
    const { clock, store } = setup()
    for (const m of ['s1', 's2', 's3']) store.push({ message: m, severity: 'success' })
    for (let i = 1; i <= 6; i++) store.push({ message: `e${i}`, severity: 'error' })
    expect(messages(store)).toEqual(['e1', 'e2', 'e3'])
    expect(store.getState().queued).toBe(3)
    clock.advance(DURATION)
    expect(messages(store)).toEqual(['e4', 'e5', 'e6'])
  })

  it('queues an error ahead of waiting lower severities, and keeps equal ones in order', () => {
    const { clock, store } = setup()
    for (const m of ['e1', 'e2', 'e3']) store.push({ message: m, severity: 'error' })
    store.push({ message: 'i1' })
    store.push({ message: 'i2' })
    store.push({ message: 'e4', severity: 'error' })
    store.push({ message: 'e5', severity: 'error' })
    clock.advance(DURATION)
    expect(messages(store)).toEqual(['e4', 'e5', 'i1'])
  })

  it('collapses errors into a summary only when the slots are already full of errors and warnings', () => {
    const { store } = setup()
    for (const m of ['e1', 'e2', 'e3']) store.push({ message: m, severity: 'error' })
    for (let i = 1; i <= 8; i++) store.push({ message: `x${i}`, severity: 'error' })
    expect(store.getState().queued).toBe(1)
    expect(store.getState().log.filter(l => l.state === 'collapsed')).toHaveLength(8)
  })
})

describe('the summary', () => {
  it('stays until it is dealt with when it holds an error, however long that takes', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 3; i++) store.push({ message: `e${i}`, severity: 'error' })
    for (let i = 1; i <= 8; i++) store.push({ message: `x${i}`, severity: i === 2 ? 'warning' : 'error' })
    clock.advance(DURATION)
    expect(store.getState().visible[0]).toMatchObject({ kind: 'summary', pending: 8, errors: 7, warnings: 1 })
    clock.advance(60_000)
    expect(store.getState().visible[0]?.kind).toBe('summary')
    expect(clock.pending()).toBe(0)
  })

  it('becomes permanent when an error joins a summary that was about to expire', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 10; i++) store.push({ message: `m${i}` })
    clock.advance(DURATION)
    clock.advance(DURATION)
    store.push({ message: 'm11' })
    clock.advance(DURATION)
    // m11 and friends: the summary is info-only and expires on its own.
    clock.advance(DURATION * 4)
    expect(store.getState().visible).toHaveLength(0)
  })

  it('is removed when the log is opened, and the user can dismiss it', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 3; i++) store.push({ message: `e${i}`, severity: 'error' })
    for (let i = 1; i <= 8; i++) store.push({ message: `x${i}`, severity: 'error' })
    clock.advance(DURATION)
    store.openLog()
    expect(store.getState().visible).toHaveLength(0)
    for (let i = 1; i <= 3; i++) store.push({ message: `f${i}`, severity: 'error' })
    for (let i = 1; i <= 8; i++) store.push({ message: `y${i}`, severity: 'error' })
    clock.advance(DURATION)
    store.dismiss(store.getState().visible[0].id)
    expect(store.getState().visible).toHaveLength(0)
  })

  it('is one summary however often the backlog overflows, merging into the one on screen', () => {
    const { clock, store } = setup()
    for (let i = 1; i <= 3; i++) store.push({ message: `e${i}`, severity: 'error' })
    for (let i = 1; i <= 20; i++) store.push({ message: `a${i}`, severity: 'error' })
    clock.advance(DURATION)
    expect(store.getState().visible.filter(v => v.kind === 'summary')).toHaveLength(1)
    for (let i = 1; i <= 3; i++) store.push({ message: `b${i}`, severity: 'error' })
    for (let i = 1; i <= 20; i++) store.push({ message: `c${i}`, severity: 'error' })
    const s = store.getState()
    expect([...s.visible].filter(v => v.kind === 'summary')).toHaveLength(1)
    expect(s.queued).toBeLessThanOrEqual(5)
    clock.advance(DURATION * 5)
    const after = store.getState()
    expect(after.visible.filter(v => v.kind === 'summary').length + 0).toBeLessThanOrEqual(1)
    expect(after.visible.find(v => v.kind === 'summary')?.pending).toBeGreaterThan(20)
  })
})

describe('the log is bounded whether or not anyone looks', () => {
  it('holds at most about 250 rows after 10,000 pushes, errors kept in preference, with a count of what was left out', () => {
    const { store } = setup()
    for (let i = 1; i <= 10_000; i++) store.push({ message: `n${i}`, severity: i % 100 === 0 ? 'error' : 'info' })
    const s = store.getState()
    expect(s.log.length).toBeLessThanOrEqual(250)
    expect(s.omitted).toBeGreaterThan(9000)
    expect(s.log.filter(l => l.severity === 'error').length).toBeGreaterThanOrEqual(100)
    expect(s.visible.filter(v => v.kind === 'summary').length).toBeLessThanOrEqual(1)
    expect(s.queued).toBeLessThanOrEqual(5)
    expect(s.unread).toBeGreaterThan(9000)
    expect(s.unreadErrors).toBeGreaterThanOrEqual(100 - 3)
  })

  it('forgets the omitted count and the unread marks once the log is acknowledged', () => {
    const { store } = setup()
    for (let i = 1; i <= 600; i++) store.push({ message: `n${i}` })
    expect(store.getState().omitted).toBeGreaterThan(0)
    store.acknowledgeLog()
    const s = store.getState()
    expect(s.omitted).toBe(0)
    expect(s.unread).toBe(0)
    expect(s.log.length).toBeLessThanOrEqual(50)
  })
})

describe('a toast that keeps being refreshed', () => {
  it('cannot hold its slot for ever: refreshes stop extending it after three durations of running time', () => {
    const { clock, store } = setup()
    store.push({ message: 'keep', severity: 'info' })
    for (let i = 0; i < 30; i++) { clock.advance(1000); store.push({ message: 'keep', severity: 'info' }) }
    // 30 s later it would still be there if refreshes extended it without bound.
    clock.advance(DURATION)
    expect(messages(store)).not.toContain('keep')
  })

  it('yields to waiting toasts so a summary and its errors finally get a slot', () => {
    const { clock, store } = setup()
    for (const m of ['k1', 'k2', 'k3']) store.push({ message: m, severity: 'error' })
    store.push({ message: 'waiting', severity: 'error' })
    let sawWaiting = false
    for (let i = 0; i < 20; i++) {
      clock.advance(1000)
      for (const m of ['k1', 'k2', 'k3']) store.push({ message: m, severity: 'error' })
      if (messages(store).includes('waiting')) sawWaiting = true
    }
    expect(sawWaiting).toBe(true)
  })

  it('counts only running time against the cap, so a hidden tab does not use it up', () => {
    const { clock, store } = setup()
    store.push({ message: 'keep' })
    clock.advance(1000)
    store.setPaused('hidden', true)
    clock.advance(10 * 60_000)
    store.setPaused('hidden', false)
    store.push({ message: 'keep' })
    clock.advance(DURATION - 1)
    expect(messages(store)).toEqual(['keep'])
  })
})

describe('pre-emption takes the slot it frees', () => {
  it('shows the error itself when 9 infos are up or waiting and an info summary is queued', () => {
    const { store } = setup()
    for (let i = 1; i <= 9; i++) store.push({ message: `i${i}` })
    expect(store.getState().queued).toBe(1)
    store.push({ message: 'boom', severity: 'error' })
    expect(messages(store)).toContain('boom')
    const logEntry = store.getState().log.find(l => l.message === 'boom')
    expect(logEntry?.state).toBe('shown')
  })

  it('does so ahead of a waiting summary that holds errors too', () => {
    const { store } = setup()
    for (const m of ['i1', 'i2', 'i3']) store.push({ message: m })
    for (let i = 1; i <= 8; i++) store.push({ message: `w${i}`, severity: 'warning' })
    // the warnings pre-empted the infos, then the rest collapsed into a summary
    store.push({ message: 'i-late' })
    store.push({ message: 'boom', severity: 'error' })
    const visible = messages(store)
    expect(visible.length).toBe(3)
    expect(visible.filter(m => m.startsWith('w') || m === 'boom').length).toBe(3)
  })

  it('shows the first error of a 10,000-message stream, not a summary line that swallowed it', () => {
    const { store } = setup()
    for (let i = 1; i <= 10_000; i++) store.push({ message: `n${i}`, severity: i === 50 ? 'error' : 'info' })
    expect(store.getState().log.find(l => l.message === 'n50')?.state).toBe('shown')
    expect(messages(store)).toContain('n50')
  })
})

describe('repeats are announced, throttled', () => {
  it('announces a repeat of a visible toast again after two seconds, not before', () => {
    const { clock, store, announced } = setup()
    store.push({ message: 'fail', severity: 'error' })
    clock.advance(500)
    store.push({ message: 'fail', severity: 'error' })
    expect(announced).toHaveLength(1)
    clock.advance(1600)
    store.push({ message: 'fail', severity: 'error' })
    expect(announced).toHaveLength(2)
    clock.advance(100)
    store.push({ message: 'fail', severity: 'error' })
    expect(announced).toHaveLength(2)
    clock.advance(2000)
    store.push({ message: 'fail', severity: 'error' })
    expect(announced).toHaveLength(3)
  })
})

describe('unread', () => {
  it('counts the errors the log bound left out, not just the ones still in it', () => {
    const { store } = setup({ collapsedLimit: 5 })
    for (const m of ['a', 'b', 'c']) store.push({ message: m, severity: 'error' })
    for (let i = 1; i <= 20; i++) store.push({ message: `x${i}`, severity: 'error' })
    const s = store.getState()
    expect(s.omitted).toBe(15)
    expect(s.log.filter(l => l.state === 'collapsed')).toHaveLength(5)
    expect(s.unreadErrors).toBe(20)
    expect(s.unread).toBe(20)
  })

  it('clears when the log is marked seen, while the rows stay until it is acknowledged', () => {
    const { store } = setup()
    for (let i = 1; i <= 12; i++) store.push({ message: `m${i}`, severity: 'error' })
    expect(store.getState().unreadErrors).toBeGreaterThan(0)
    store.markSeen()
    const s = store.getState()
    expect(s.unread).toBe(0)
    expect(s.unreadErrors).toBe(0)
    expect(s.log.filter(l => l.state === 'collapsed').length).toBeGreaterThan(0)
  })
})

describe('reset', () => {
  it('clears the omitted counts too, so the next user inherits no unread numbers', () => {
    const { store } = setup({ collapsedLimit: 5 })
    for (const m of ['a', 'b', 'c']) store.push({ message: m, severity: 'error' })
    for (let i = 1; i <= 20; i++) store.push({ message: `x${i}`, severity: 'error' })
    expect(store.getState().omitted).toBeGreaterThan(0)
    expect(store.getState().unreadErrors).toBeGreaterThan(0)
    store.reset()
    const s = store.getState()
    expect(s.omitted).toBe(0)
    expect(s.unread).toBe(0)
    expect(s.unreadErrors).toBe(0)
    expect(s.log).toHaveLength(0)
    expect(s.visible).toHaveLength(0)
    expect(s.queued).toBe(0)
  })
})
