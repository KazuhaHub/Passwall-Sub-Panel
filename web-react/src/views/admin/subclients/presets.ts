// The client-family data of the Sub clients page: the built-in presets, the platform list and the
// normaliser. Pure data and functions, no React, so SettingsView (which still imports
// normalizeRegistry through ./clientRegistry) and the page share one copy.
import type { SubClientFamily, SubPlatform } from '@/api/settings'

// CLIENT_PRESETS mirrors defaultSubClients() on the Go side — keep them in
// sync. Each family carries its UA keywords + render format and the import apps
// nested under it. "Add preset" appends a whole family; "reset" rebuilds the
// registry from this list.
export const CLIENT_PRESETS: SubClientFamily[] = [
  {
    name: 'Clash / mihomo', keywords: ['clash', 'mihomo', 'meta'], render_format: 'mihomo', enabled: true,
    apps: [
      { name: 'Clash Verge Rev', platforms: ['windows', 'macos', 'linux'], import_url_template: 'clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}', install_url: 'https://github.com/clash-verge-rev/clash-verge-rev/releases', enabled: true, sort: 10, recommended_for: ['windows', 'macos', 'linux'] },
      { name: 'Clash Meta for Android', platforms: ['android'], import_url_template: 'clash://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}&update-interval={{ sub_update_interval_minutes }}', install_url: 'https://github.com/MetaCubeX/ClashMetaForAndroid/releases', enabled: true, sort: 20, recommended_for: ['android'] },
      { name: 'Clash Mi', platforms: ['windows', 'macos', 'linux', 'android', 'ios'], import_url_template: 'clashmi://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}', install_url: 'https://github.com/KaringX/clashmi/releases', enabled: true, sort: 25, recommended_for: ['ios'] },
    ],
  },
  {
    name: 'sing-box', keywords: ['sing-box', 'karing'], render_format: 'sing-box', enabled: true,
    apps: [
      { name: 'sing-box', platforms: ['ios', 'macos', 'android'], import_url_template: 'sing-box://import-remote-profile?url={{ sub_url_encoded }}#{{ profile_name_encoded }}', install_url: 'https://sing-box.sagernet.org/clients/', enabled: true, sort: 40, recommended_for: [] },
      { name: 'Karing', platforms: ['windows', 'macos', 'linux', 'android', 'ios'], import_url_template: 'karing://install-config?url={{ sub_url_encoded }}&name={{ profile_name_encoded }}', install_url: 'https://github.com/KaringX/karing/releases', enabled: true, sort: 65, recommended_for: [] },
    ],
  },
  {
    name: 'Shadowrocket', keywords: ['shadowrocket'], render_format: 'uri-list', enabled: true,
    apps: [
      { name: 'Shadowrocket', platforms: ['ios'], import_url_template: 'shadowrocket://add/sub://{{ sub_url_b64_url_safe }}?remark={{ profile_name_encoded }}', install_url: 'https://apps.apple.com/app/shadowrocket/id932747118', enabled: true, sort: 60, recommended_for: [] },
    ],
  },
  { name: 'Quantumult X', keywords: ['quantumult'], render_format: 'mihomo', enabled: true, apps: [] },
  {
    name: 'V2rayNG', keywords: ['v2rayng'], render_format: 'uri-list', enabled: true,
    apps: [
      { name: 'V2rayNG', platforms: ['android'], import_url_template: 'v2rayng://install-sub?url={{ sub_url_encoded }}#{{ profile_name_encoded }}', install_url: 'https://github.com/2dust/v2rayNG/releases', enabled: true, sort: 55, recommended_for: [] },
    ],
  },
  {
    name: 'V2RayN', keywords: ['v2rayn', 'v2ray'], render_format: 'uri-list', enabled: true,
    apps: [
      { name: 'V2rayN', platforms: ['windows'], import_url_template: '{{ sub_url }}?remarks={{ profile_name_encoded }}', install_url: 'https://github.com/2dust/v2rayN/releases', enabled: true, sort: 50, recommended_for: [] },
    ],
  },
  { name: 'Passwall (OpenWrt)', keywords: ['passwall'], render_format: 'uri-list', enabled: true, apps: [] },
  {
    name: 'Stash', keywords: ['stash'], render_format: 'mihomo', enabled: true,
    apps: [
      { name: 'Stash', platforms: ['ios'], import_url_template: 'stash://install-config?url={{ sub_url_encoded }}', install_url: 'https://apps.apple.com/app/stash-rule-based-proxy/id1596063349', enabled: true, sort: 30, recommended_for: [] },
    ],
  },
]

export const PLATFORM_OPTIONS: SubPlatform[] = ['windows', 'macos', 'linux', 'android', 'ios', 'other']

export function clonePreset(p: SubClientFamily): SubClientFamily {
  return JSON.parse(JSON.stringify(p)) as SubClientFamily
}

// normalizeRegistry guards against null array fields from the server: a
// detection-only family serializes apps as JSON null (Go nil slice), and
// keyword / platform / recommended_for arrays can be null too. Coerce them all
// to [] on the way into form state so the editor's .map/.length never see null.
export function normalizeRegistry(fams?: SubClientFamily[] | null): SubClientFamily[] {
  return (fams ?? []).map(f => ({
    ...f,
    keywords: f.keywords ?? [],
    apps: (f.apps ?? []).map(a => ({
      ...a,
      platforms: a.platforms ?? [],
      recommended_for: a.recommended_for ?? [],
    })),
  }))
}
