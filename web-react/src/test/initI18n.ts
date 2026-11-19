import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'

import enCommon from '@/locales/en-US/common.json'
import zhCommon from '@/locales/zh-CN/common.json'
import { I18N_STATIC_OPTIONS, flatten, type Nested } from '@/i18n/options'

// Real `common` strings behind the global i18next instance (en-US active,
// zh-CN loaded for tests that ask for it with i18n.getFixedT('zh-CN')), so a
// DOM test of a shared building block reads the words a person would read and a
// missing key shows up as a raw key in the assertion rather than passing on an
// echo mock. Import it for its side effect, before rendering.
if (!i18n.isInitialized) {
  void i18n.use(initReactI18next).init({
    ...I18N_STATIC_OPTIONS,
    lng: 'en-US',
    resources: {
      'en-US': { common: flatten(enCommon as Nested) },
      'zh-CN': { common: flatten(zhCommon as Nested) },
    },
  })
}
export default i18n
