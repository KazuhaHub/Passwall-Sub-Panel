// THE TOAST STATE MACHINE: ONE OWNER OF TIME AND OF THE LIST. No React, no DOM, no
// real clock: the clock and the timers are injected, so every rule below is tested
// deterministically. The host (ToastHost.tsx) only subscribes and draws.
//
// Why it exists in this shape: a queue with its own timers layered over a renderer with
// its own timers and pause rules cannot be made to agree with it. Here a toast has one
// countdown, owned here, and "visible" means exactly "in `visible`": there is no second
// list in a renderer to disagree with.
//
// The rules:
//  - at most `maxVisible` toasts are on screen; the rest wait in a FIFO queue and enter
//    when a slot frees, their countdown starting when they enter;
//  - a toast's countdown runs only while the page is being looked at: it is paused,
//    ALL of them together, while the tab is hidden, the pointer is over the toasts or
//    focus is inside them, and resumes from the time that was left;
//  - a push with the key of a VISIBLE toast restarts its countdown and bumps its ×N; one
//    with the key of a QUEUED toast is ignored; one with the key of a toast that has
//    left is a new toast, the same instant it left (removal is immediate: an exit
//    animation is the renderer's own business and never holds a slot or a key);
//  - the backlog is bounded: past `collapseAfter` waiting, the queue collapses into ONE
//    summary toast ("N more") that points at the log, which keeps every pushed message
//    (the last `logLimit` plus every collapsed one until the log has been looked at), so
//    nothing, an error least of all, is dropped silently and nobody waits minutes;
//  - severity decides who gets a slot: an arriving error or warning pre-empts a visible
//    info or success toast when every slot is full (the pre-empted one goes to the log),
//    and waits ahead of lower severities in the queue; only errors and warnings that find
//    the slots already full of errors and warnings are collapsed;
//  - a summary that holds an error or a warning does not expire: it stays until the log is
//    opened or the user dismisses it, and says how many of each it holds. There is at most
//    one summary;
//  - the log is bounded whether or not anyone looks: the last `logLimit` ordinary messages
//    and at most `collapsedLimit` unseen ones, errors kept in preference, with a count of
//    what was left out;
//  - a toast that keeps being repeated cannot hold its slot for ever: refreshes extend it
//    up to `lifetimeFactor` times the duration of running time, after which it yields.

export type Severity = 'info' | 'success' | 'warning' | 'error'

export interface ToastAction { label: string; onClick: () => void }

export interface ToastInput {
  message: string
  severity?: Severity
  /** De-duplication key. Defaults to severity + message. */
  key?: string
  /** One action link or button on the toast. */
  action?: ToastAction
}

export interface ToastView {
  id: number
  key: string
  kind: 'toast' | 'summary'
  message: string
  severity: Severity
  /** ×N: how many times this toast was pushed while it was on screen. */
  count: number
  action?: ToastAction
  /** For a summary: how many messages it stands for, and how many of them are errors and warnings. */
  pending?: number
  errors?: number
  warnings?: number
}

export interface LogEntry {
  id: number
  message: string
  severity: Severity
  at: number
  count: number
  state: 'queued' | 'shown' | 'collapsed'
  /** The user has opened the log since this was collapsed: it no longer counts as unread. */
  seen?: boolean
}

export interface ToastState {
  visible: readonly ToastView[]
  /** How many are waiting for a slot (a summary counts as one). */
  queued: number
  log: readonly LogEntry[]
  /** Messages the user has not seen as a toast, left out of the log by its bound included. */
  unread: number
  unreadErrors: number
  /** How many older messages the log bound left out since it was last looked at. */
  omitted: number
}

export interface Timers {
  now: () => number
  /** Returns a handle of the caller's own type; `clearTimer` receives it back. */
  setTimer: (fn: () => void, ms: number) => unknown
  clearTimer: (handle: never) => void
}

export interface ToastStoreOptions extends Timers {
  maxVisible?: number
  duration?: number
  /** The summary lingers longer: it is the way to the messages that were not shown. */
  summaryDuration?: number
  collapseAfter?: number
  logLimit?: number
  /** Unseen messages kept in the log. */
  collapsedLimit?: number
  /** Refreshes may extend a toast to at most this many durations of running time. */
  lifetimeFactor?: number
  /** Called once for every NEW message pushed (shown, queued or collapsed), synchronously. */
  onAnnounce?: (m: { message: string; severity: Severity }) => void
}

export type PauseReason = 'hidden' | 'hover' | 'focus'

export interface ToastStore {
  push(input: ToastInput): void
  dismiss(id: number): void
  setPaused(reason: PauseReason, on: boolean): void
  /** A detached store (no host mounted) keeps every countdown frozen. */
  setActive(on: boolean): void
  /** The log is being opened: the summary toast, which points at it, has done its job. */
  openLog(): void
  /** Forgets everything: the toasts, the queue, the log. For a change of signed-in identity. */
  reset(): void
  /** Everything collapsed so far has now been put in front of the user: the unread count clears
   *  (the rows stay, and are trimmed only when the log is acknowledged on close). */
  markSeen(): void
  /** The log has been looked at: collapsed messages become ordinary history. */
  acknowledgeLog(): void
  subscribe(fn: () => void): () => void
  getState(): ToastState
}

interface Entry {
  view: ToastView
  duration: number
  remaining: number
  /** Running time this toast may still be given by refreshes. */
  lifeLeft: number
  startedAt: number | null
  timer: unknown
  logIds: number[]
  /** When this toast's message was last announced (the first push, or a repeat). */
  announcedAt: number
  /** For a summary only. */
  counts?: { total: number; errors: number; warnings: number }
}

const RANK: Record<Severity, number> = { error: 3, warning: 2, info: 1, success: 0 }
const urgent = (s: Severity) => RANK[s] >= 2

export function createToastStore(opts: ToastStoreOptions): ToastStore {
  const maxVisible = opts.maxVisible ?? 3
  const duration = opts.duration ?? 4000
  const summaryDuration = opts.summaryDuration ?? duration * 3
  const collapseAfter = opts.collapseAfter ?? 5
  const logLimit = opts.logLimit ?? 50
  const collapsedLimit = opts.collapsedLimit ?? 200
  const lifetimeFactor = opts.lifetimeFactor ?? 3
  // A repeat of a visible toast is announced again, but at most this often per toast: a
  // screen-reader user whose retry fails the same way hears each failure, not a stutter.
  const reannounceAfter = 2000

  let nextId = 1
  let visible: Entry[] = []
  let queue: Entry[] = []
  let log: LogEntry[] = []
  let omitted = 0
  let omittedErrors = 0
  const paused = new Set<PauseReason>()
  let active = false
  const listeners = new Set<() => void>()
  let snapshot: ToastState = { visible: [], queued: 0, log: [], unread: 0, unreadErrors: 0, omitted: 0 }

  const running = () => active && paused.size === 0

  function emit() {
    const collapsed = log.filter(l => l.state === 'collapsed')
    snapshot = {
      visible: visible.map(e => e.view),
      queued: queue.length,
      log: log.slice(),
      unread: collapsed.filter(l => !l.seen).length + omitted,
      unreadErrors: collapsed.filter(l => !l.seen && l.severity === 'error').length + omittedErrors,
      omitted,
    }
    listeners.forEach(fn => fn())
  }

  function startTimer(e: Entry) {
    // A toast with no end (a summary that holds an error) has no timer to run.
    if (!Number.isFinite(e.remaining)) return
    e.startedAt = opts.now()
    e.timer = opts.setTimer(() => expire(e), Math.max(0, e.remaining))
  }

  function freeze(e: Entry) {
    if (e.startedAt !== null) {
      const elapsed = opts.now() - e.startedAt
      e.remaining = Math.max(0, e.remaining - elapsed)
      e.lifeLeft = Math.max(0, e.lifeLeft - elapsed)
    }
    if (e.timer !== null) opts.clearTimer(e.timer as never)
    e.timer = null
    e.startedAt = null
  }

  function expire(e: Entry) {
    e.timer = null
    remove(e)
  }

  function remove(e: Entry) {
    if (e.timer !== null) opts.clearTimer(e.timer as never)
    e.timer = null
    visible = visible.filter(v => v !== e)
    queue = queue.filter(v => v !== e)
    promote()
    emit()
  }

  function promote() {
    while (visible.length < maxVisible && queue.length > 0) {
      const e = queue.shift()!
      visible.push(e)
      e.remaining = e.duration
      e.lifeLeft = Number.isFinite(e.duration) ? e.duration * lifetimeFactor : Infinity
      for (const id of e.logIds) {
        const l = log.find(x => x.id === id)
        if (l && l.state === 'queued') l.state = 'shown'
      }
      if (running()) startTimer(e)
    }
  }

  function trimLog() {
    // Ordinary history is capped at logLimit, oldest first.
    let ordinary = log.filter(l => l.state !== 'collapsed').length
    if (ordinary > logLimit) log = log.filter(l => l.state === 'collapsed' || ordinary-- <= logLimit)
    // Unseen messages are capped too, whether or not anyone has looked: the oldest
    // non-errors go first, then the oldest errors, and what was left out is counted.
    let unseen = log.filter(l => l.state === 'collapsed').length
    if (unseen > collapsedLimit) {
      for (const pass of ['others', 'errors'] as const) {
        for (const l of log) {
          if (unseen <= collapsedLimit) break
          if (l.state !== 'collapsed') continue
          if ((pass === 'others') === (l.severity === 'error')) continue
          l.id = -l.id // marks it for removal below
          unseen--
          omitted++
          if (l.severity === 'error') omittedErrors++
        }
      }
      log = log.filter(l => l.id > 0)
    }
  }

  function newSummary(): Entry {
    return {
      view: { id: nextId++, key: '\u0000summary', kind: 'summary', message: '', severity: 'info', count: 1, pending: 0, errors: 0, warnings: 0 },
      duration: summaryDuration, remaining: summaryDuration, lifeLeft: Infinity, startedAt: null, timer: null, logIds: [], announcedAt: 0,
      counts: { total: 0, errors: 0, warnings: 0 },
    }
  }

  function addToSummary(s: Entry, severity: Severity) {
    const c = s.counts!
    c.total++
    if (severity === 'error') c.errors++
    else if (severity === 'warning') c.warnings++
    const worst = c.errors > 0 ? 'error' : c.warnings > 0 ? 'warning' : RANK[s.view.severity] > RANK[severity] ? s.view.severity : severity
    s.view = { ...s.view, pending: c.total, errors: c.errors, warnings: c.warnings, severity: worst }
    if (c.errors + c.warnings > 0 && Number.isFinite(s.duration)) {
      // It holds something that must not be missed: it stays until it is dealt with.
      s.duration = Infinity
      s.remaining = Infinity
      if (s.timer !== null) { opts.clearTimer(s.timer as never); s.timer = null; s.startedAt = null }
    }
  }

  const summaryEntry = () => visible.find(e => e.view.kind === 'summary') ?? queue.find(e => e.view.kind === 'summary')

  function collapseQueue() {
    const entries = queue.filter(e => e.view.kind === 'toast')
    if (entries.length === 0) return
    let target = summaryEntry()
    if (!target) target = newSummary()
    for (const e of entries) {
      for (const id of e.logIds) {
        const l = log.find(x => x.id === id)
        if (l) l.state = 'collapsed'
      }
      addToSummary(target, e.view.severity)
    }
    // At most one summary: it sits in the queue if it is not yet on screen, and the
    // queue is otherwise empty.
    queue = visible.includes(target) ? [] : [target]
  }

  const store: ToastStore = {
    push(input) {
      const severity = input.severity ?? 'info'
      const key = input.key ?? `${severity}:${input.message}`
      const hit = visible.find(e => e.view.key === key)
      if (hit) {
        // A repeat of what is on screen: the same toast, held longer (up to its lifetime
        // cap), with a counter.
        hit.view = { ...hit.view, count: hit.view.count + 1, action: input.action ?? hit.view.action }
        if (running()) freeze(hit)
        hit.remaining = Math.min(hit.duration, hit.lifeLeft)
        if (running()) startTimer(hit)
        const l = log.find(x => x.id === hit.logIds[0])
        if (l) { l.count += 1; l.at = opts.now() }
        if (opts.now() - hit.announcedAt >= reannounceAfter) {
          hit.announcedAt = opts.now()
          opts.onAnnounce?.({ message: input.message, severity })
        }
        emit()
        return
      }
      if (queue.some(e => e.view.key === key)) return

      opts.onAnnounce?.({ message: input.message, severity })
      const logEntry: LogEntry = { id: nextId++, message: input.message, severity, at: opts.now(), count: 1, state: 'queued' }
      log.push(logEntry)

      // An error or a warning that finds every slot taken pre-empts an info or success
      // toast: the user has had that one's chance, and the log has it. The slot it frees is
      // ITS slot: it goes to the front of the queue, ahead of a waiting summary too.
      let preempted = false
      if (visible.length >= maxVisible && urgent(severity)) {
        let victim: Entry | undefined
        for (const v of visible) {
          if (v.view.kind !== 'toast' || urgent(v.view.severity)) continue
          if (!victim || RANK[v.view.severity] < RANK[victim.view.severity]) victim = v
        }
        if (victim) {
          for (const id of victim.logIds) {
            const l = log.find(x => x.id === id)
            if (l) l.state = 'collapsed'
          }
          if (victim.timer !== null) opts.clearTimer(victim.timer as never)
          visible = visible.filter(v => v !== victim)
          preempted = true
        }
      }

      const summary = queue.find(e => e.view.kind === 'summary')
      if (summary && !preempted) {
        // Already collapsed: this one joins the summary instead of the queue.
        logEntry.state = 'collapsed'
        addToSummary(summary, severity)
      } else {
        const entry: Entry = {
          view: { id: nextId++, key, kind: 'toast', message: input.message, severity, count: 1, action: input.action },
          duration, remaining: duration, lifeLeft: duration * lifetimeFactor, startedAt: null, timer: null, logIds: [logEntry.id], announcedAt: opts.now(),
        }
        // Higher severities wait ahead of lower ones; equal ones keep their order. One that
        // pre-empted a visible toast goes first of all, so the freed slot is its own.
        let at = preempted ? 0 : queue.length
        while (!preempted && at > 0 && RANK[queue[at - 1].view.severity] < RANK[severity]) at--
        queue.splice(at, 0, entry)
        if (visible.length >= maxVisible && queue.length > collapseAfter) collapseQueue()
      }
      promote()
      trimLog()
      emit()
    },

    dismiss(id) {
      const e = visible.find(v => v.view.id === id) ?? queue.find(v => v.view.id === id)
      if (e) remove(e)
    },

    setPaused(reason, on) {
      const was = running()
      if (on) paused.add(reason); else paused.delete(reason)
      syncRunning(was)
    },

    setActive(on) {
      const was = running()
      active = on
      syncRunning(was)
    },

    reset() {
      for (const e of [...visible, ...queue]) if (e.timer !== null) opts.clearTimer(e.timer as never)
      visible = []
      queue = []
      log = []
      omitted = 0
      omittedErrors = 0
      emit()
    },

    markSeen() {
      let changed = omitted > 0
      omitted = 0
      omittedErrors = 0
      for (const l of log) if (l.state === 'collapsed' && !l.seen) { l.seen = true; changed = true }
      if (changed) emit()
    },

    openLog() {
      for (const e of [...visible, ...queue]) if (e.view.kind === 'summary') remove(e)
    },

    acknowledgeLog() {
      let changed = omitted > 0
      omitted = 0
      omittedErrors = 0
      for (const l of log) if (l.state === 'collapsed') { l.state = 'shown'; changed = true }
      if (changed) { trimLog(); emit() }
    },

    subscribe(fn) {
      listeners.add(fn)
      return () => { listeners.delete(fn) }
    },

    getState: () => snapshot,
  }

  function syncRunning(was: boolean) {
    const now = running()
    if (was && !now) visible.forEach(freeze)
    else if (!was && now) visible.forEach(e => { if (e.timer === null) startTimer(e) })
    if (was !== now) promote()
  }

  return store
}
