// The brand colours offered in the appearance menu. Each is the source colour of a Material 3
// tonal palette; the fill and label tones derived from it (light and dark) are written out in
// styles/ui.css under the same id, which is what the page actually uses.
export interface ColorPreset {
  id: string
  labelKey: string
  hex: string
}

export const COLOR_PRESETS: ColorPreset[] = [
  // labelKey is relative to the `appearance` namespace — AppearanceMenu calls
  // useTranslation('appearance'), so a leading "appearance." would make the
  // lookup miss and surface the raw key as the tooltip text.
  { id: 'blue', labelKey: 'preset.blue', hex: '#0061A4' },
  { id: 'purple', labelKey: 'preset.purple', hex: '#6750A4' },
  { id: 'teal', labelKey: 'preset.teal', hex: '#006A6B' },
  { id: 'green', labelKey: 'preset.green', hex: '#386A20' },
  { id: 'orange', labelKey: 'preset.orange', hex: '#825500' },
  { id: 'pink', labelKey: 'preset.pink', hex: '#9D2C5F' },
]

export const DEFAULT_PRESET_HEX = COLOR_PRESETS[0].hex
