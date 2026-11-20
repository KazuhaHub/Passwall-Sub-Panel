import { Field } from '@/components/form/Field'
import { InfoTip } from '@/components/common/HelpTip'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import type { CdnIssue } from '@/api/types'
import { useNodesT } from '../shared/useNodesT'
import { ID, PortInput, type FieldsProps } from './fieldKit'
import { CDN_DEFAULT_PORT, defaultClientTLS } from './lineModel'

/**
 * What the server found likely to stop a CDN line working. A warning is drawn in the danger colour; none of it
 * blocks a save. A landing that cannot go through a CDN is explained from its code, in the locale.
 */
export function CdnIssues({ issues }: { issues: readonly CdnIssue[] }) {
  const t = useNodesT()
  if (issues.length === 0) return null
  return (
    <ul data-testid="cdn-issues" className="list-disc space-y-1 pl-5 text-xs">
      {issues.map(issue => (
        <li key={issue.code} data-code={issue.code} className={issue.warn ? 'text-destructive' : 'text-muted-foreground'}>
          {issue.code === 'landing_not_capable'
            ? t(`nodes.cdn.reason.${issue.params?.reason_code}`, { ...issue.params, defaultValue: issue.params?.reason })
            : t(`nodes.cdn.issue.${issue.code}`, { ...issue.params })}
        </li>
      ))}
    </ul>
  )
}

/** The fields of a CDN line: the domain, the optimised address, the port, and the SNI and Host the CDN routes by. */
export function CdnFields({ draft, patch, error, blur, issues }: FieldsProps & { issues: readonly CdnIssue[] }) {
  const t = useNodesT()
  const placeholder = draft.domain?.trim() || 'cdn.example.com'
  return (
    <div data-testid="cdn-line" className="flex flex-col gap-4">
      <p className="flex items-start gap-1 text-xs text-muted-foreground">
        <span>{t('nodes.cdn.no_entry')}</span>
        <InfoTip text={t('nodes.cdn.tip')} />
      </p>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        <Field label={t('nodes.cdn.domain')} required hint={t('nodes.cdn.domain_hint')} error={error('domain') && t(`nodes.${error('domain')}`)}>
          <Input id={ID.domain} value={draft.domain ?? ''} placeholder="cdn.example.com" onChange={e => patch({ domain: e.target.value })} onBlur={() => blur('domain')} />
        </Field>
        <Field label={t('nodes.cdn.port')} hint={t('nodes.cdn.port_hint')} error={error('port') && t(`nodes.${error('port')}`)}>
          <PortInput id={ID.port} value={draft.port} placeholder={String(CDN_DEFAULT_PORT)} onChange={port => patch({ port })} onBlur={() => blur('port')} />
        </Field>
      </div>
      <Field label={t('nodes.cdn.address')} hint={t('nodes.cdn.address_hint')}>
        <Input id={ID.address} value={draft.address} placeholder={placeholder} onChange={e => patch({ address: e.target.value })} />
      </Field>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Field label={t('nodes.relay.sni')} hint={t('nodes.cdn.sni_hint')}>
          <Input value={draft.sni ?? ''} placeholder={placeholder} onChange={e => patch({ sni: e.target.value })} />
        </Field>
        <Field label={t('nodes.cdn.host')} hint={t('nodes.cdn.host_hint')}>
          <Input value={draft.host ?? ''} placeholder={placeholder} onChange={e => patch({ host: e.target.value })} />
        </Field>
      </div>
      <div className="flex items-center gap-1">
        <Field orientation="horizontal" className="flex-1" label={t('nodes.cdn.client_tls')}>
          <Switch checked={draft.client_tls ?? defaultClientTLS(draft.port || CDN_DEFAULT_PORT)} onCheckedChange={checked => patch({ client_tls: checked })} />
        </Field>
        <InfoTip text={t('nodes.cdn.client_tls_tip')} />
      </div>
      <CdnIssues issues={issues} />
    </div>
  )
}
