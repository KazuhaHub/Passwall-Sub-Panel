import type { TransitLink, TransitLinkProtocol } from '@/api/nodes'
import { Field } from '@/components/form/Field'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { IconRandom } from '@/lib/icons'
import { randomRealitySite } from '@/utils/xrayReality'
import { useNodesT } from '../shared/useNodesT'

/**
 * The settings of one tunnel between an entry and an exit server: the protocol, the address the entry dials, and
 * (for REALITY) the camouflage site. The parent owns the draft and its Save; every change is a patch of the link.
 */
export function TunnelFields({ link, onChange }: { link: TransitLink; onChange: (patch: Partial<TransitLink>) => void }) {
  const t = useNodesT()
  return (
    <div className="flex flex-col gap-4">
      <Field label={t('nodes.egress.protocol')} hint={link.reality_refused ? t('nodes.egress.reality_refused') : undefined}>
        <NativeSelect value={link.protocol} onChange={e => {
          const protocol = e.target.value as TransitLinkProtocol
          // REALITY needs a camouflage site and target; fill them in so switching to it never leaves them empty.
          const site = link.reality_server_name || randomRealitySite()
          onChange(protocol === 'vless_reality'
            ? { protocol, reality_server_name: site, reality_target: link.reality_target || `${site}:443` }
            : { protocol })
        }}>
          <option value="ss2022">Shadowsocks 2022</option>
          {/* A sing-box entry cannot dial an Xray REALITY exit; an already chosen REALITY stays selectable so it can be seen. */}
          <option value="vless_reality" disabled={link.reality_refused && link.protocol !== 'vless_reality'}>VLESS + REALITY</option>
        </NativeSelect>
      </Field>
      <Field label={t('nodes.egress.exit_address')}
        hint={link.server_address ? t('nodes.egress.exit_address_default', { address: link.server_address }) : t('nodes.egress.exit_address_unknown')}>
        <Input value={link.exit_address} placeholder={link.server_address} onChange={e => onChange({ exit_address: e.target.value })} />
      </Field>
      {link.protocol === 'vless_reality' && (
        <>
          <div className="flex items-start gap-2">
            <Field className="flex-1" label={t('nodes.egress.reality_server_name')}>
              <Input value={link.reality_server_name} onChange={e => onChange({ reality_server_name: e.target.value })} />
            </Field>
            <Button type="button" variant="outline" className="mt-7" onClick={() => {
              const site = randomRealitySite([link.reality_server_name])
              onChange({ reality_server_name: site, reality_target: `${site}:443` })
            }}><IconRandom />{t('nodes.egress.random_site')}</Button>
          </div>
          <Field label={t('nodes.egress.reality_target')}>
            <Input value={link.reality_target} onChange={e => onChange({ reality_target: e.target.value })} />
          </Field>
        </>
      )}
    </div>
  )
}
