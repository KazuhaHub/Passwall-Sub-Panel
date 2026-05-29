# PSP × 3X-UI 兼容性

PSP 通过 `/panel/api/*` 对接 3X-UI 面板。本文档维护两件事：

1. **每个 PSP 版本对应的最低 / 已测试 3X-UI 版本范围**（升级前查这里）
2. **历史踩过的兼容性坑**（升级 3X-UI 之前看这里，避免重复踩）

## 当前兼容矩阵

| PSP 版本 | 最低 3X-UI | 已实测通过 | 备注 |
|---|---|---|---|
| **v3.6.2+** | **3.2.0** | 3.2.0 | client 适配迁到一级 `/clients/*` API,**硬切 ≥ 3.2.0**,见下文 |
| v3.6.0 – v3.6.1 | 3.1.0 | 3.1.0 | 仍走 inbound-scoped 端点;别跑在 3.2.0 上,先升 PSP 到 v3.6.2 |
| v3.5.1 – v3.5.x | 3.1.0 | 3.1.0 | `/inbounds/list` 把 settings 等改成 nested object,见下文 |
| v3.5.0 | 3.0.x | 3.0.x | 跨 3.1.0 升级会破坏 traffic poll |
| v3.4.x | 3.0.x | 3.0.x | 同上 |
| ≤ v3.3.x | 2.x – 3.0.x | 3.0.x | 历史兼容性见 CHANGELOG |

> 这张表是人看的速查；运行时真相源是 `docs/compat/v3.json`。`min_xui` 和 `max_tested_xui` 两个字段**都已接入运行时**(PSP 按需拉取并据此判 too_old / untested)。

**规则**:
- "最低 3X-UI" = 该 PSP 版本能正常工作的最早 3X-UI 版本(低于这个会破)
- "已实测通过" = 在该版本上真实跑过 traffic poll / reconcile / render 全套
- 任何高于"已实测通过"的 3X-UI 版本都属于**未知风险**——升级前先在一台 panel 上小流量验证

## 历史兼容性事件

### 2026-05-23 / 3X-UI 3.1.0 → PSP v3.5.0 破坏

**症状**: 任何升级到 3X-UI 3.1.0 的 panel 一旦被 PSP 接入,traffic poll Phase 1 fetch 全失败,日志报
"cannot unmarshal object into Go struct field of type string"。表现为所有 user 流量数据停止更新。

**根因**: 3X-UI 3.1.0 改了 `/panel/api/inbounds/list` 响应:
- `settings` / `streamSettings` / `sniffing` 从 escaped string(`"settings": "{\"clients\":[]}"`) 改成 nested object(`"settings": {"clients":[]}`)
- `allocate` 从 escaped string 改成 `null`
- 写端仍接受 legacy escaped-string 写法,没破坏

PSP `rawInbound` 这四个字段定义为 Go `string`,`json.Unmarshal` 一个 object 进去直接报错。

**修复**: PSP v3.5.1 新增 `flexJSON` 类型(nested object/array 原样捕获,null → "")。**硬切只支持 3X-UI ≥ 3.1.0**——不再维护 3.0.x 兼容路径,因为自用项目可以控制对接版本。

**附带发现**:
- 3.1.0 `clientStats[*]` 多了 `uuid` / `subId` / `lastOnline` 字段——Go json 默认忽略未知字段,PSP 当前 `rawClientTraffic` 不受影响
- `lastOnline` 是个免费的"用户最近活跃时间"素材,未来可以做"在线徽章"
- 新增端点 `/inbounds/list/slim`、`/inbounds/options`、`/clients/list/paged`、`/clients/{add,update,attach,detach}`——PSP 当前不用,但 slim 是未来 traffic poll 优化候选

## 升级 3X-UI 时的检查清单

1. **查本文的兼容矩阵**——目标版本是否在当前 PSP 版本的"已实测通过"范围内?
2. 不在范围内的话: 先升级 PSP 到支持目标 3X-UI 版本的版本
3. 升级**单台** panel 先,观察 5-10 分钟:
   - PSP traffic poll 日志无错(看 `traffic poll panel` warn 行)
   - PSP reconcile axis A 日志无错
   - 一个 user 用真实客户端拉订阅看是否能连
4. 全部正常后再升级其它 panel
5. **不要批量升级**——3X-UI 任意小版本都可能像 3.1.0 这样改 schema

## 当 3X-UI 升级踩到新破坏怎么办

1. 立即记录到本文的"历史兼容性事件"
2. PSP 这边: 走 patch 版本(v3.5.x) 修复兼容性,**同时更新兼容矩阵的"最低 3X-UI"**
3. 更新 `reference_xui_v3_api_break` memory(项目 memory 系统),把"这次踩坑 + 修复方式"沉淀

## 维护 `docs/compat/v<MAJOR>.json` 的 SOP

每个 PSP major 一个 JSON 文件(v3.x 都拉 `docs/compat/v3.json`,v4.x 都拉 `v4.json`)。
这是 v3.6.0-beta.7 引入的 per-major 分文件设计,理由见 ARCHITECTURE.md。

### 何时改 / 改什么

- **新 3X-UI 出 patch 版本(无 API 改动)** ── 在当前 active major 的 JSON 里把
  覆盖你 PSP 版本那条 entry 的 `max_tested_xui` 改成新版本号,顺手更新 `updated_at`
  和 `notes`。commit + push 到 main → 所有该 major 的 PSP 部署 60 秒内自动感知。
- **PSP 发新 minor (比如 v3.6 → v3.7)** ── 在当前 major 的 JSON 加新 entry,
  `psp_min: "v3.7.0"`, `psp_max: "v3.7.99"`,把新 entry 放在 `entries` 数组**最前**
  (first-match-wins 让新版优先匹配)。
- **PSP 发新 major (比如 v3.x → v4.0)** ── 新建 `docs/compat/v4.json`,内部 `major: 4`,
  entries 从只覆盖 v4.0 的 baseline 开始。`v3.json` 保持不动 — 仍跑 v3.x 的部署继续从
  那个文件拿数据。
- **patch 级精度区间(罕见)** ── 比如 v3.6.5-v3.6.8 单独验过 3.2.0,其它 v3.6.x
  还是 3.1.0:在 entries 数组前面插入一条更窄的 entry(narrower 在前 = first-match
  生效),broader 的 baseline 在后面兜底。
- **抬高最低版本(硬切)** ── entry 的 `min_xui` **已接入运行时**(PSP 拉到后按它判
  too_old),但它和代码里的 `version.MinXUI` const **是同一个下限的两处表述,必须相等**:
  `TestMinXUIConstMatchesCompatJSON` 会读这个 JSON 断言"覆盖最新版本那条 entry 的
  `min_xui` == const",**drift 直接让 `go test` 红**——所以改下限时**两处一起改**,忘了
  哪处提交前测试就拦住你(这正是 v3.6.2 漏掉 const、3.1.0 面板没警告那个坑的防忘闸)。
  运行时 `ActiveMinXUI() = max(MinXUI, JSON min_xui)` 只是安全网:正常发版两者相等、max
  是空操作;万一某次 drift 漏过测试发了版,取较高值兜底,下限永远不会被错误降低。

### entries 数组语义

- 每个 entry 是 PSP 版本的**闭区间** `[psp_min, psp_max]`(含两端)
- `psp_min` / `psp_max` 端点**只写 stable semver** `vX.Y.Z`,**不带** pre-release suffix
  (PSP 比对时会丢自己 version 的 `-beta.x` 后缀,把 `v3.6.0-beta.7` 当成 `v3.6.0` 匹配)
- **first-match-wins** ── 数组顺序就是优先级,narrower / 更新的 entry 放前
- 重叠 OK,顺序决定胜出

### PSP 拉不到时的行为(故障容错)

PSP 启动时先读本地 cache(`<DataDir>/compat-cache.json`)装入上次成功 fetch 的范围;
然后 admin 打开 Servers 页(或手动点 Test)触发 RefreshRemoteCompat。任何一步失败
(网络挂 / JSON 解析错 / major 不匹配 / 没匹配 entry)只是让 CheckXUI 返回 Unknown
状态,admin 仍可通过 upgrade-panel 的 `force` 按钮强制升级 — 不是 hard wall。

## v3.6.0 路线图: PSP 自动感知 3X-UI 版本 ── 已完成

| beta | 内容 |
|---|---|
| beta.1 | xui_panels 加 panel_version / xray_version / version_checked_at 三列;adapter GetServerStatus;app.go boot probe + traffic-loop piggyback |
| beta.2 | Servers 页 Version 列 + compat banner + Test 按钮顺手刷版本 |
| beta.3 | 远程升级 3X-UI / Xray 按钮 + smoke probe + 跨大版本 migrate 政策修订 |
| beta.4 | lastOnline 集成到 admin 用户列表"最近活跃"列 |
| beta.5 | dynamic compat (GitHub raw 单文件) + admin force override |
| beta.6 | 5 个 audit 发现 bug 修复 + local compat cache 兜底 |
| beta.7 | dynamic compat schema v2: per-major 分文件 + entries 数组 + psp_min/psp_max 范围 |

这样下次类似 3.1.0 这种破坏可以在 admin UI 提前看到,而不是 traffic poll 静默失败才察觉。
