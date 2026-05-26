# PSP × 3X-UI 兼容性

PSP 通过 `/panel/api/*` 对接 3X-UI 面板。本文档维护两件事：

1. **每个 PSP 版本对应的最低 / 已测试 3X-UI 版本范围**（升级前查这里）
2. **历史踩过的兼容性坑**（升级 3X-UI 之前看这里，避免重复踩）

## 当前兼容矩阵

| PSP 版本 | 最低 3X-UI | 已实测通过 | 备注 |
|---|---|---|---|
| **v3.5.1+** | **3.1.0** | 3.1.0 | `/inbounds/list` 把 settings 等改成 nested object,见下文 |
| v3.5.0 | 3.0.x | 3.0.x | 跨 3.1.0 升级会破坏 traffic poll |
| v3.4.x | 3.0.x | 3.0.x | 同上 |
| ≤ v3.3.x | 2.x – 3.0.x | 3.0.x | 历史兼容性见 CHANGELOG |

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

## v3.6.0 路线图: PSP 自动感知 3X-UI 版本

分 3 个 beta 渐进交付:

- **v3.6.0-beta.1 (后端基础设施)** ── `xui_panels` 表加 `panel_version` / `xray_version` /
  `version_checked_at` 三列(3X-UI 是 panel 实例,版本是 panel 级属性,**不是** node 级);
  adapter 新增 `GetServerStatus(ctx)` 调 `/panel/api/server/status`(此端点直接返回
  `panelVersion` + `xray.version`,比 `/getPanelUpdateInfo` 更全面,一次调用拿全);PSP
  启动时同步探测所有 panel 一次,写库 + 比对兼容范围,超出范围的写 `log.Warn` 告警。
  零额外后台 loop,零干扰 health/reconcile/traffic poll(继续 v3.5 解耦原则)。
  兼容范围 hardcode 在 `internal/version/compat.go`,声明 `MinXUI` / `MaxTested` 常量。

- **v3.6.0-beta.2 (admin 操作面)** ── Servers 页加版本列展示;手动"刷新版本"按钮;
  admin UI 顶部红条 banner(版本超出兼容范围时);新增"远程升级 3X-UI / Xray" 按钮
  + 二次确认 + 升级后 smoke probe(立即调 `GetServerStatus` + `ListInbounds` 看 panel
  是否回来且 schema 没崩)。

- **v3.6.0-beta.3 (`lastOnline` 集成)** ── 3.1.0 `clientStats[*]` 已经免费带 `lastOnline`,
  PSP 顺手解到 `rawClientTraffic` + 写入 traffic snapshot,admin 用户列表加"最近活跃"列。

这样下次类似 3.1.0 这种破坏可以在 admin UI 提前看到,而不是 traffic poll 静默失败才察觉。
