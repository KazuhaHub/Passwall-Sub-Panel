import { useEffect, useState } from 'react'
import { getTransitLink, updateTransitLink, type TransitLink, type TransitLinkProtocol } from '@/api/nodes'
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { IconChevronDown, IconChevronRight } from '@/lib/icons'
import { LinkQuality } from '../LinkQuality'
import { TunnelFields } from '../TunnelFields'
import { useNodesT } from '../../shared/useNodesT'

/**
 * The tunnel between the node's server and one exit server a route uses, with its own Save: the settings are shared
 * by every node of that pair, so they are not part of the routes' save. Loaded only once opened.
 */
export function RouteTunnelSettings({ entry, exit, name, defaultExitNodes = [] }: {
  entry: number
  exit: number
  name: string
  /** Nodes on the entry server whose default exit is this exit server: the tunnel they carry everything through. */
  defaultExitNodes?: string[]
}) {
  const t = useNodesT()
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState<TransitLink | null>(null)
  const [dirty, setDirty] = useState(false)
  const [failed, setFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  // The protocol the tunnel had when it was loaded: changing it re-handshakes the link.
  const [loadedProtocol, setLoadedProtocol] = useState<TransitLinkProtocol | null>(null)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setFailed(false)
    getTransitLink(entry, exit)
      .then(link => { if (!cancelled) { setForm(link); setLoadedProtocol(link.protocol); setDirty(false) } })
      .catch(() => { if (!cancelled) { setForm(null); setFailed(true) } })
    return () => { cancelled = true }
  }, [open, entry, exit])

  const patch = (p: Partial<TransitLink>) => { setForm(f => (f ? { ...f, ...p } : f)); setDirty(true) }

  async function save() {
    if (!form) return
    const protocolChanged = loadedProtocol !== null && form.protocol !== loadedProtocol
    // A pure address edit that no default exit depends on needs no go-ahead.
    if (protocolChanged || defaultExitNodes.length > 0) {
      const ok = await confirm({
        title: t('nodes.routes.tunnel.confirm_title', { name }),
        message: [
          defaultExitNodes.length > 0 ? t('nodes.routes.tunnel.confirm_nodes', { nodes: defaultExitNodes.join(', ') }) : '',
          protocolChanged ? t('nodes.routes.tunnel.confirm_protocol') : '',
        ].filter(Boolean).join('\n\n'),
        destructive: protocolChanged,
        confirmText: t('nodes.routes.tunnel.save'),
      })
      if (!ok) return
    }
    setBusy(true)
    try {
      const reality = form.protocol === 'vless_reality'
      const link = await updateTransitLink(entry, exit, {
        protocol: form.protocol,
        exit_address: form.exit_address.trim(),
        reality_server_name: reality ? form.reality_server_name.trim() : undefined,
        reality_target: reality ? form.reality_target.trim() : undefined,
      })
      setForm(link)
      setLoadedProtocol(link.protocol)
      setDirty(false)
      pushToast({ message: t('nodes.routes.tunnel.saved'), severity: 'success' })
    } catch { /* the shared client already reports the refusal */ } finally { setBusy(false) }
  }

  const Chevron = open ? IconChevronDown : IconChevronRight
  return (
    <div data-testid="route-tunnel" className="rounded-lg border p-2">
      <Button type="button" variant="outline" size="sm" data-testid="route-tunnel-toggle" aria-expanded={open} onClick={() => setOpen(o => !o)}>
        <Chevron />{t('nodes.routes.tunnel.title', { name })}
      </Button>
      {open && (
        <div className="flex flex-col gap-3 px-1 pt-2 pb-1">
          {failed && <Alert variant="destructive"><AlertDescription>{t('nodes.egress.load_failed')}</AlertDescription></Alert>}
          {form && (
            <>
              <p className="text-xs text-muted-foreground">{t('nodes.egress.shared_hint')}</p>
              {defaultExitNodes.length > 0 && (
                <p data-testid="route-tunnel-users" className="text-xs text-warning-ink">{t('nodes.routes.tunnel.default_users', { nodes: defaultExitNodes.join(', ') })}</p>
              )}
              <LinkQuality entry={entry} exit={exit} />
              <TunnelFields link={form} onChange={patch} />
              <div className="flex justify-end">
                <Button type="button" data-testid="route-tunnel-save" disabled={!dirty || busy} onClick={() => void save()}>{t('nodes.routes.tunnel.save')}</Button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}
