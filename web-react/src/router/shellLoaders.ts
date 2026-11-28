import { ensureNamespaces } from '@/i18n'

// THE SHELL ROUTES LOAD THEIR WORDING WITH THEIR CODE.
//
// The `admin` and `user` i18n namespaces are no longer part of the boot (the login page
// reads neither). A shell's lazy loader fetches the namespace in parallel with the shell's
// own chunk, and the route's Suspense fallback stays up until both have arrived, so
// nothing inside the shell ever renders a raw key. A failure rejects like a failed chunk
// would, and lands on the router's error element.
export const loadAdminShell = () => Promise.all([ensureNamespaces(['admin']), import('./AdminShellRoute')]).then(([, mod]) => mod)
export const loadUserShell = () => Promise.all([ensureNamespaces(['user']), import('./UserShellRoute')]).then(([, mod]) => mod)
// The mandatory-2FA enrollment page sits outside both shells but reads `user`.
export const loadEnroll2FA = () => Promise.all([ensureNamespaces(['user']), import('@/views/Enroll2FAView')]).then(([, mod]) => mod)
// The dev-only component gallery shows both consoles' components.
export const withAllNamespaces = <T,>(load: () => Promise<T>) => () => Promise.all([ensureNamespaces(['admin', 'user']), load()]).then(([, mod]) => mod)
