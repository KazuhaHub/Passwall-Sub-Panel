// The toast layer's long-standing entry point. `pushSnack(message, severity)` is what 86
// files call and `default` is what App mounts; both stay, and what stands behind them is
// the toast store and host under components/feedback (see toastStore.ts for the rules).
export { pushSnack } from './feedback/toasts'
export { default } from './feedback/ToastHost'
