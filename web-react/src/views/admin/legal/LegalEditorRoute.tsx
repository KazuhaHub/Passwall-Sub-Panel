import { useParams } from 'react-router'
import { ObjectPage } from '@/components/common/ObjectPage'
import { useBackTarget } from '@/components/common/objectReturn'
import { useAdminT } from '../useAdminT'
import LegalEditorPage from './LegalEditorPage'
import { legalBackTarget, parseLegalKind, SETTINGS_PATH } from './legalRoutes'

/**
 * `/admin/settings/legal/:kind`: the editor of one document. Anything but `terms` or `privacy` is "no such document"
 * in the page's own frame, without a request.
 */
export default function LegalEditorRoute() {
  const t = useAdminT()
  const back = legalBackTarget(useBackTarget(SETTINGS_PATH))
  const kind = parseLegalKind(useParams().kind)
  if (!kind) {
    return <ObjectPage back={{ to: back, label: t('admin:settings.title') }} state={{ kind: 'not_found', title: t('admin:settings.legal.editor.not_found') }} />
  }
  // Keyed by the document, so the other one starts from clean state.
  return <LegalEditorPage key={kind} kind={kind} back={back} />
}
