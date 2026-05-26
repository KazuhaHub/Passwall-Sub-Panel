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

## 远期: PSP 自动版本探测(规划中,v3.6.0)

计划在 v3.6.0 引入:
- `nodes` 表加 `xui_version` 列,health 时记录每台 panel 的 3X-UI 版本(来自 `/panel/api/server/getPanelUpdateInfo`)
- PSP 启动 / reconcile 时比对版本范围,超出本 PSP 版本的"已实测通过"范围 → admin UI 红条告警
- Servers 页加"远程升级 3X-UI / Xray"按钮,集中触发 + 二次确认 + 升级后 smoke probe(立即调 ListInbounds 看 schema 没崩)

这样下次类似 3.1.0 这种破坏可以在 admin UI 提前看到,而不是 traffic poll 静默失败才察觉。
