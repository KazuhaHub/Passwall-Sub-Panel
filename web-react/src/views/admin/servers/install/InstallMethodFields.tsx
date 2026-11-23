import { useTranslation } from 'react-i18next'
import type { NativeInstallationSelection } from '@/api/servers'
import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'

/**
 * How the Passwall Node will be installed: the method, and for an offline install the platform and architecture.
 * Shared by the add-server form (which chooses it before the record exists) and the installation flow.
 */
export function InstallMethodFields({ selection, onChange, disabled = false }: {
  selection: NativeInstallationSelection
  onChange: (selection: NativeInstallationSelection) => void
  disabled?: boolean
}) {
  const { t } = useTranslation('admin')
  return (
    <div className="flex flex-col gap-4">
      <Field label={t('admin:servers.native.method_label')}>
        <NativeSelect value={selection.method} disabled={disabled}
          onChange={event => onChange({ ...selection, method: event.target.value as NativeInstallationSelection['method'] })}>
          {(['linux', 'github', 'docker', 'manual'] as const).map(method => <option key={method} value={method}>{t(`admin:servers.native.method.${method}`)}</option>)}
        </NativeSelect>
      </Field>
      {selection.method === 'manual' && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label={t('admin:servers.native.platform_label')}>
            <NativeSelect value={selection.os} disabled={disabled}
              onChange={event => onChange({ ...selection, os: event.target.value as NativeInstallationSelection['os'] })}>
              {(['linux', 'darwin', 'windows'] as const).map(os => <option key={os} value={os}>{t(`admin:servers.native.platform.${os}`)}</option>)}
            </NativeSelect>
          </Field>
          <Field label={t('admin:servers.native.architecture')}>
            <NativeSelect value={selection.arch} disabled={disabled}
              onChange={event => onChange({ ...selection, arch: event.target.value as NativeInstallationSelection['arch'] })}>
              <option value="amd64">amd64</option>
              <option value="arm64">arm64</option>
            </NativeSelect>
          </Field>
        </div>
      )}
    </div>
  )
}
